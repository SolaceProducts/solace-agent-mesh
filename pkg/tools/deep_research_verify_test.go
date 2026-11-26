// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestGenerateResearchPlan_ReturnsStepsFromLLM(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) {
		return `{"steps": ["Gather data", "Analyze results", "Compile report"]}`, nil
	}
	steps := generateResearchPlan(t.Context(), llm, "What is quantum computing?",
		[]string{"quantum computing basics", "quantum computing applications"})
	want := []string{"Gather data", "Analyze results", "Compile report"}
	if !equalSlices(steps, want) {
		t.Errorf("steps = %v, want %v", steps, want)
	}
}

func TestGenerateResearchPlan_TruncatesToSixSteps(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) {
		return `{"steps": ["s0","s1","s2","s3","s4","s5","s6","s7","s8","s9"]}`, nil
	}
	steps := generateResearchPlan(t.Context(), llm, "q", []string{"q1"})
	if len(steps) != 6 {
		t.Errorf("expected 6 steps, got %d: %v", len(steps), steps)
	}
}

func TestGenerateResearchPlan_FallsBackToQueriesOnEmpty(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) { return "", nil }
	queries := []string{"query A", "query B"}
	got := generateResearchPlan(t.Context(), llm, "q", queries)
	if !equalSlices(got, queries) {
		t.Errorf("got = %v, want fallback %v", got, queries)
	}
}

func TestGenerateResearchPlan_FallsBackOnInvalidJSON(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) { return "this is not json", nil }
	queries := []string{"q1", "q2"}
	got := generateResearchPlan(t.Context(), llm, "q", queries)
	if !equalSlices(got, queries) {
		t.Errorf("got = %v, want fallback %v", got, queries)
	}
}

func TestGenerateResearchPlan_FallsBackOnLLMError(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) {
		return "", errors.New("boom")
	}
	queries := []string{"fallback1"}
	got := generateResearchPlan(t.Context(), llm, "q", queries)
	if !equalSlices(got, queries) {
		t.Errorf("got = %v, want fallback %v", got, queries)
	}
}

func TestRegenerateQueriesFromSteps_ReturnsQueriesFromLLM(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) {
		return `{"queries": ["search query 1", "search query 2"]}`, nil
	}
	got := regenerateQueriesFromSteps(t.Context(), llm,
		[]string{"Investigate X", "Analyze Y"}, "What is X?")
	want := []string{"search query 1", "search query 2"}
	if !equalSlices(got, want) {
		t.Errorf("got = %v, want %v", got, want)
	}
}

func TestRegenerateQueriesFromSteps_TruncatesToStepCount(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) {
		return `{"queries": ["q1","q2","q3","q4","q5"]}`, nil
	}
	got := regenerateQueriesFromSteps(t.Context(), llm,
		[]string{"step1", "step2", "step3"}, "q")
	if len(got) != 3 {
		t.Errorf("expected 3 queries (capped to step count), got %d: %v", len(got), got)
	}
}

func TestRegenerateQueriesFromSteps_FallsBackToStepsOnError(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) {
		return "", errors.New("fail")
	}
	steps := []string{"s1", "s2"}
	got := regenerateQueriesFromSteps(t.Context(), llm, steps, "q")
	if !equalSlices(got, steps) {
		t.Errorf("got = %v, want fallback %v", got, steps)
	}
}

func TestRegenerateQueriesFromSteps_FallsBackToStepsOnEmpty(t *testing.T) {
	llm := func(_ context.Context, _, _ string, _ float64) (string, error) { return "", nil }
	steps := []string{"step A", "step B"}
	got := regenerateQueriesFromSteps(t.Context(), llm, steps, "q")
	if !equalSlices(got, steps) {
		t.Errorf("got = %v, want fallback %v", got, steps)
	}
}

func TestIsInteractivePlanClient_CapabilityFlagAccepts(t *testing.T) {
	ctx := &tool.Context{
		ClientID:            "any-gateway-id",
		GatewayCapabilities: map[string]bool{"interactive_plan_verification": true},
	}
	if !isInteractivePlanClient(ctx, nil) {
		t.Error("expected true when capability flag is set")
	}
}

func TestIsInteractivePlanClient_CapabilityFlagFalseRejects(t *testing.T) {
	ctx := &tool.Context{
		ClientID:            "any-gateway-id",
		GatewayCapabilities: map[string]bool{"interactive_plan_verification": false},
	}
	if isInteractivePlanClient(ctx, nil) {
		t.Error("expected false when capability flag explicitly false")
	}
}

func TestIsInteractivePlanClient_NoSignalsRejects(t *testing.T) {
	ctx := &tool.Context{ClientID: "http_sse_gateway"}
	if isInteractivePlanClient(ctx, nil) {
		t.Error("expected false when neither capability nor allowlist set")
	}
}

func TestIsInteractivePlanClient_LegacyAllowlistAccepts(t *testing.T) {
	ctx := &tool.Context{ClientID: "my_custom_webui"}
	cfg := map[string]any{
		"interactive_plan_verification_clients": []any{"my_custom_webui"},
	}
	if !isInteractivePlanClient(ctx, cfg) {
		t.Error("expected true when client is on legacy allowlist")
	}
}

func TestIsInteractivePlanClient_LegacyWildcardAccepts(t *testing.T) {
	ctx := &tool.Context{ClientID: "anything"}
	cfg := map[string]any{
		"interactive_plan_verification_clients": []any{"*"},
	}
	if !isInteractivePlanClient(ctx, cfg) {
		t.Error("expected true with wildcard allowlist")
	}
}

func TestIsInteractivePlanClient_LegacyAllowlistRejectsUnlisted(t *testing.T) {
	ctx := &tool.Context{ClientID: "http_sse_gateway"}
	cfg := map[string]any{
		"interactive_plan_verification_clients": []any{"my_custom_webui"},
	}
	if isInteractivePlanClient(ctx, cfg) {
		t.Error("expected false when client not on allowlist")
	}
}

func TestIsInteractivePlanClient_WorkflowDispatchedTaskMatchesTheAllowlist(t *testing.T) {
	cfg := map[string]any{
		"interactive_plan_verification_clients": []any{"my_custom_webui"},
	}
	dispatched := &tool.Context{ClientID: "my_custom_webui", Origin: a2a.Origin{EntrypointName: "my_custom_webui"}}
	if !isInteractivePlanClient(dispatched, cfg) {
		t.Error("a workflow-dispatched task from an allowlisted client must match")
	}
}

func TestIsInteractivePlanClient_CapabilityWinsOverLegacy(t *testing.T) {
	ctx := &tool.Context{
		ClientID:            "not-on-list",
		GatewayCapabilities: map[string]bool{"interactive_plan_verification": true},
	}
	cfg := map[string]any{
		"interactive_plan_verification_clients": []any{"only-this-one"},
	}
	if !isInteractivePlanClient(ctx, cfg) {
		t.Error("capability flag should win over legacy allowlist")
	}
}

type recordingSignal struct {
	mu      sync.Mutex
	signals []any
}

func (r *recordingSignal) send(s any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signals = append(r.signals, s)
	return nil
}

func TestRunPlanVerification_PublishesPlanThenAwaitsResponse(t *testing.T) {
	rs := &recordingSignal{}
	ctx := &tool.Context{
		AgentName:           "ResearchAgent",
		UserID:              "u-1",
		GatewayCapabilities: map[string]bool{"interactive_plan_verification": true},
		SendSignal:          rs.send,
		AwaitDeepResearchPlanResponse: func(_ context.Context, _, _ string) (*tool.PlanResponse, error) {
			return &tool.PlanResponse{Action: "start", Steps: []string{"edited step"}}, nil
		},
	}
	out := runPlanVerification(t.Context(), ctx,
		"plan-1", "Title", "Question?", "quick",
		[]string{"original step"}, []string{"web"}, 3, 300)
	if out.Cancelled {
		t.Errorf("expected accept, got cancelled outcome: %+v", out)
	}
	if len(out.Steps) != 1 || out.Steps[0] != "edited step" {
		t.Errorf("expected user-edited steps, got %v", out.Steps)
	}
	if len(rs.signals) != 1 {
		t.Fatalf("expected exactly 1 signal, got %d", len(rs.signals))
	}
	plan, ok := rs.signals[0].(a2a.DeepResearchPlanData)
	if !ok {
		t.Fatalf("signal is not DeepResearchPlanData: %T", rs.signals[0])
	}
	if plan.PlanID != "plan-1" || plan.AgentName != "ResearchAgent" || plan.Title != "Title" {
		t.Errorf("plan signal fields wrong: %+v", plan)
	}
}

func TestRunPlanVerification_TimeoutPublishesStaleSignal(t *testing.T) {
	rs := &recordingSignal{}
	ctx := &tool.Context{
		AgentName:           "ResearchAgent",
		UserID:              "u-1",
		GatewayCapabilities: map[string]bool{"interactive_plan_verification": true},
		SendSignal:          rs.send,
		AwaitDeepResearchPlanResponse: func(innerCtx context.Context, _, _ string) (*tool.PlanResponse, error) {
			<-innerCtx.Done()
			return nil, innerCtx.Err()
		},
	}
	timeoutCtx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	out := runPlanVerification(timeoutCtx, ctx,
		"plan-1", "T", "Q?", "quick",
		[]string{"step"}, []string{"web"}, 3, 300)
	if !out.Cancelled || !out.CancelledTimeout {
		t.Errorf("expected timeout cancel, got %+v", out)
	}
	if len(rs.signals) != 2 {
		t.Fatalf("expected 2 signals (plan+stale), got %d", len(rs.signals))
	}
	stale, ok := rs.signals[1].(a2a.DeepResearchPlanStaleData)
	if !ok {
		t.Fatalf("second signal is not DeepResearchPlanStaleData: %T", rs.signals[1])
	}
	if stale.PlanID != "plan-1" || stale.Reason != "timed_out" {
		t.Errorf("stale signal wrong: %+v", stale)
	}
}

func TestRunPlanVerification_AutoApprovesWhenAwaiterMissing(t *testing.T) {
	rs := &recordingSignal{}
	ctx := &tool.Context{
		AgentName:  "ResearchAgent",
		SendSignal: rs.send,
	}
	out := runPlanVerification(t.Context(), ctx,
		"plan-1", "T", "Q?", "quick",
		[]string{"s1"}, nil, 3, 300)
	if out.Cancelled {
		t.Errorf("expected auto-approve, got cancelled")
	}
	if len(out.Steps) != 1 || out.Steps[0] != "s1" {
		t.Errorf("auto-approve should pass through original steps, got %v", out.Steps)
	}
}

func TestRunPlanVerification_AutoApprovesWhenUserIDMissing(t *testing.T) {
	rs := &recordingSignal{}
	called := false
	ctx := &tool.Context{
		AgentName:  "ResearchAgent",
		SendSignal: rs.send,
		AwaitDeepResearchPlanResponse: func(_ context.Context, _, _ string) (*tool.PlanResponse, error) {
			called = true
			return nil, nil
		},
	}
	out := runPlanVerification(t.Context(), ctx,
		"plan-1", "T", "Q?", "quick",
		[]string{"s1"}, nil, 3, 300)
	if out.Cancelled || called {
		t.Errorf("expected auto-approve without awaiter call (called=%v outcome=%+v)", called, out)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
