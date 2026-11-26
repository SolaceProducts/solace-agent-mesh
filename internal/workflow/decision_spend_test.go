// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func TestADecisionRowBooksTheVerifiedCallerOverTheUnsignedUserID(t *testing.T) {
	const stampedRow, signedRow = "0190c0de-0000-7000-8000-000000000001", "0190c0de-0000-7000-8000-000000000002"
	cases := []struct {
		name     string
		stamped  string
		identity *auth.Identity
		wantID   string
		wantSub  string
	}{
		{"the signed row outranks the stamp", stampedRow, &auth.Identity{UserID: "verified-user", PlatformUserID: signedRow}, signedRow, ""},
		{"the verified subject outranks a stamp it did not sign", stampedRow, &auth.Identity{UserID: "verified-user"}, "", "verified-user"},
		{"without auth the stamped row is booked", stampedRow, nil, stampedRow, ""},
		{"without auth or a stamp the claimed userId is booked", "", nil, "", "claimed-user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &DAGExecutor{
				cfg:      &WorkflowConfig{Name: "nightly"},
				instance: &WorkflowInstance{cfg: &WorkflowConfig{}},
				execCtx: &WorkflowExecutionContext{
					UserProps: a2a.UserProperties{UserID: "claimed-user", UserUUID: tc.stamped},
					Identity:  tc.identity,
				},
			}

			got := e.decisionSpendAttribution(decisionCall{node: &NodeConfig{ID: "pick"}, topLevelNodeID: "pick"})

			if got.UserID != tc.wantID || got.UserSub != tc.wantSub {
				t.Errorf("user = %q/%q, want %q/%q", got.UserID, got.UserSub, tc.wantID, tc.wantSub)
			}
		})
	}
}

func TestADecisionRowBooksTheEntrypointTheChainStartedAt(t *testing.T) {
	cases := []struct {
		name  string
		props a2a.UserProperties
	}{
		{"a workflow the gateway submitted books the gateway", a2a.UserProperties{ClientID: "webui"}},
		{"a workflow an agent delegated books the entrypoint the agent forwarded",
			a2a.UserProperties{DelegatingAgentName: "OrchestratorAgent", EntrypointName: "webui"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &DAGExecutor{
				cfg:      &WorkflowConfig{Name: "nightly"},
				instance: &WorkflowInstance{cfg: &WorkflowConfig{}},
				execCtx:  &WorkflowExecutionContext{UserProps: tc.props},
			}

			got := e.decisionSpendAttribution(decisionCall{node: &NodeConfig{ID: "pick"}, topLevelNodeID: "pick"})

			if got.EntrypointName != "webui" || got.EntrypointID != "" {
				t.Errorf("entrypoint = %q/%q, want the webui entrypoint by name", got.EntrypointID, got.EntrypointName)
			}
		})
	}
}

func TestADecisionNodeRunAsAMapTemplateBooksEachCallToItselfUnderTheMapNode(t *testing.T) {
	jev := &fakeJev{respond: func(map[string]any) string {
		return `{"model":"jev-1.13.0","answers":{"privileged":{"type":"noul","noul":0.1}},"usage":{"input_tokens":5,"output_tokens":1}}`
	}}
	run := newDecisionRun(t, decisionMapWorkflow, jev, true)
	withPages(run, 2)
	spent := captureSpend(t, run)
	run.e.execCtx.UserProps = a2a.UserProperties{UserID: "user-1"}

	run.start(t)
	run.wait(t)

	for i, e := range spent(t, 2) {
		if e.Surface != spend.SurfaceDecision || e.WorkflowNodeID != "classify" || e.RootNodeID != "each" {
			t.Errorf("event %d surface=%s node=%s root node=%s, want a decision call by classify under the map node each",
				i, e.Surface, e.WorkflowNodeID, e.RootNodeID)
		}
	}
}

func TestADecisionTheProviderBilledButFailedStillBooksItsTokens(t *testing.T) {
	jev := &fakeJev{respond: func(map[string]any) string {
		return `{"model":"jev-1.13.0","answers":{"department":{"type":"unheard-of"}},"usage":{"input_tokens":9,"output_tokens":2}}`
	}}
	run := newDecisionRun(t, routedDecisionWorkflow, jev, true)
	spent := captureSpend(t, run)
	run.e.execCtx.UserProps = a2a.UserProperties{UserID: "user-1"}

	run.execute(t)

	if msg := run.errorMessage(); !strings.Contains(msg, string(decision.FailureInvalidResponse)) {
		t.Fatalf("workflow error = %q, want the undecodable answer to fail the node", msg)
	}
	e := spent(t, 1)[0]
	if want := (spend.Tokens{UncachedInput: 9, Output: 2}); e.Tokens != want || e.WorkflowNodeID != "triage" || e.ModelName != "typesafe/jev-1.13.0" {
		t.Errorf("event tokens=%+v node=%s model=%s, want %+v billed by triage on typesafe/jev-1.13.0", e.Tokens, e.WorkflowNodeID, e.ModelName, want)
	}
}

func captureSpend(t *testing.T, run *decisionRun) func(t *testing.T, n int) []spend.Event {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	queue, err := brk.CreateQueue("spend-test", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(queue, a2a.SpendEventTopic("ns")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publisher := spend.NewPublisher(brk, "ns", slog.New(slog.DiscardHandler))
	run.e.instance.SetSpendPublisher(publisher)
	return func(t *testing.T, n int) []spend.Event {
		t.Helper()
		if err := publisher.Close(t.Context()); err != nil {
			t.Fatalf("close publisher: %v", err)
		}
		events := make([]spend.Event, n)
		for i := range events {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			msg, err := brk.Receive(ctx, queue)
			cancel()
			if err != nil {
				t.Fatalf("receive event %d: %v", i, err)
			}
			if err = json.Unmarshal(msg.Payload, &events[i]); err != nil {
				t.Fatalf("decode event %d: %v", i, err)
			}
		}
		return events
	}
}

func TestADecisionOnARegisteredModelIsBookedToTheConfigurationItRanOn(t *testing.T) {
	const configID = "0190c0de-0000-7000-8000-0000000000c1"
	jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, aliasedDecisionWorkflow, jev, true,
		withRegistry(t, nil, "jev", func(url string) map[string]any {
			raw := registeredDecisionModel(url, "registry-key")
			raw[samapp.ModelConfigIDKey] = configID
			return raw
		}))
	spent := captureSpend(t, run)
	run.e.execCtx.UserProps = a2a.UserProperties{UserID: "user-1"}

	run.execute(t)

	e := spent(t, 1)[0]
	if e.ModelKey != "jev" || e.ModelConfigID != configID {
		t.Errorf("event model key %q, configuration %q, want jev on %s", e.ModelKey, e.ModelConfigID, configID)
	}
}

func TestADecisionIsBookedToTheModelTheProviderReportsElseTheOneItAskedFor(t *testing.T) {
	answer := func(model string) func(map[string]any) string {
		return func(map[string]any) string {
			return `{` + model + `"answers":{
				"department":{"type":"choice","choice":"billing","probabilities":{"billing":0.5,"tech":0.5},"confidence":0.9},
				"urgent":{"type":"noul","noul":0.9}},"usage":{"input_tokens":40,"output_tokens":1}}`
		}
	}
	for name, tc := range map[string]struct{ reported, want string }{
		"the provider names the model": {`"model":"jev-1.14.0",`, "typesafe/jev-1.14.0"},
		"the provider names no model":  {"", "typesafe/jev-1.13.0"},
	} {
		t.Run(name, func(t *testing.T) {
			run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{respond: answer(tc.reported)}, true)
			spent := captureSpend(t, run)
			run.e.execCtx.UserProps = a2a.UserProperties{UserID: "user-1"}

			run.execute(t)

			if got := spent(t, 1)[0].ModelName; got != tc.want {
				t.Errorf("model_name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestADecisionIsBookedToTheModelNamedByItsDecisionProvider(t *testing.T) {
	typesafeAlias := withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredDecisionModel(url, "k") })
	openAIAlias := withRegistry(t, nil, "jev", func(url string) map[string]any { return registeredOpenAIDecisionModel(url, "k") })
	for name, tc := range map[string]struct {
		doc    string
		answer func(map[string]any) string
		wire   []func(string, *stubServices)
		want   string
	}{
		"inline typesafe": {routedDecisionWorkflow, departmentAnswer("billing", 0.9), nil, "typesafe/jev-1.13.0"},
		"inline openai":   {openAIDecisionWorkflow, openAIDepartmentAnswer("billing", 0.9), nil, "openai/gpt-6-luna"},
		"an alias to a typesafe model": {aliasedDecisionWorkflow, departmentAnswer("billing", 0.9),
			[]func(string, *stubServices){typesafeAlias}, "typesafe/jev-1.13.0"},
		"an alias to an openai model": {aliasedDecisionWorkflow, openAIDepartmentAnswer("billing", 0.9),
			[]func(string, *stubServices){openAIAlias}, "openai/gpt-6-luna"},
	} {
		t.Run(name, func(t *testing.T) {
			run := newDecisionRun(t, tc.doc, &fakeJev{respond: tc.answer}, true, tc.wire...)
			spent := captureSpend(t, run)
			run.e.execCtx.UserProps = a2a.UserProperties{UserID: "user-1"}

			run.execute(t)

			if got := spent(t, 1)[0].ModelName; got != tc.want {
				t.Errorf("model_name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestADecisionNodeRunAsALoopBodyBooksEachIterationToItselfUnderTheLoopNode(t *testing.T) {
	doc := `
decision: {api_key: test-key, model: jev-1.13.0}
output_mapping: {r: "{{poll.output}}"}
nodes:
  - {id: poll, type: loop, node: check, condition: "{{check.output.answers.done.value}} == false", max_iterations: 5}
  - id: check
    type: decision
    input: {attempt: "{{_loop_iteration}}"}
    questions:
      done: {type: boolean, instructions: "The job has finished"}
`
	jev := &fakeJev{}
	jev.respond = func(map[string]any) string {
		p := "0.1"
		if jev.calls.Load() >= 2 {
			p = "0.9"
		}
		return `{"model":"jev-1.13.0","answers":{"done":{"type":"noul","noul":` + p + `}},"usage":{"input_tokens":40,"output_tokens":1}}`
	}
	run := newDecisionRun(t, doc, jev, true)
	spent := captureSpend(t, run)
	run.e.execCtx.UserProps = a2a.UserProperties{UserID: "user-1"}

	run.execute(t)

	for i, e := range spent(t, 2) {
		if e.WorkflowNodeID != "check" || e.RootKind != spend.RootKind(a2a.RootKindWorkflow) ||
			e.RootName != run.e.cfg.Name || e.RootNodeID != "poll" {
			t.Errorf("event %d node=%s root=%s/%s/%s, want check booked under the loop node poll of the root workflow %s",
				i, e.WorkflowNodeID, e.RootKind, e.RootName, e.RootNodeID, run.e.cfg.Name)
		}
	}
}

func TestADecisionAfterAnApprovalPauseIsBookedToTheOriginTheRunStartedWith(t *testing.T) {
	doc := `
decision: {api_key: test-key, model: jev-1.13.0}
output_mapping: {r: "{{check.output}}"}
nodes:
  - {id: call_gated, type: tool, tool_name: gated_call}
  - id: check
    type: decision
    depends_on: [call_gated]
    questions:
      done: {type: boolean, instructions: "The job has finished"}
`
	jev := &fakeJev{respond: func(map[string]any) string {
		return `{"model":"jev-1.13.0","answers":{"done":{"type":"noul","noul":0.9}},"usage":{"input_tokens":40,"output_tokens":1}}`
	}}
	run := newDecisionRun(t, doc, jev, true)
	paused, _ := newHILInstance(t)
	w := run.e.instance
	w.ctx, w.pausedDB, w.pausedRepo = t.Context(), paused.pausedDB, paused.pausedRepo
	t.Setenv(features.EnvVarName(features.KeyWorkflowToolApprovalEnforcement), "")
	w.svc.FeatureRegistry().Register(features.Definition{Key: features.KeyWorkflowToolApprovalEnforcement, Default: true})
	w.hilToolConfigs = gatedCallConfig()
	w.tools.SetHILConfigs(w.hilToolConfigs)
	spent := captureSpend(t, run)
	run.e.execCtx.UserProps = a2a.UserProperties{
		UserID: "user-1", Trigger: a2a.TriggerInteractive, Channel: "webui", EntrypointName: "webui",
		RootKind: a2a.RootKindAgent, RootID: "OrchestratorAgent", DelegatingAgentName: "parent-wf",
	}
	run.e.execCtx.SpendOrigin = agent.SpendOrigin{WorkflowName: "parent-wf", WorkflowNodeID: "child", ParentTaskID: "parent-task"}

	run.start(t)
	run.wait(t)
	rows, err := w.pausedRepo.Expired(t.Context(), w.cfg.Name, time.Now().Add(24*time.Hour).UnixMilli(), 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("paused rows = %d (err %v), want the gated call parked", len(rows), err)
	}
	if jev.calls.Load() != 0 {
		t.Fatal("the decision ran before the gated call was approved")
	}
	w.answer(t.Context(), rows[0].RequestID, "resumed", "answered", map[string]any{"approved": true})
	run.wait(t)

	e := spent(t, 1)[0]
	want := spend.Event{
		UserSub: "user-1", WorkflowNodeID: "check",
		RootKind: spend.RootKind(a2a.RootKindAgent), RootName: "OrchestratorAgent",
		EntrypointName: "webui", Channel: "webui",
		TriggerKind: spend.TriggerWorkflow, TriggerID: "parent-wf",
		TaskID: run.e.execCtx.TaskID, ParentTaskID: "parent-task",
	}
	got := spend.Event{
		UserID: e.UserID, UserSub: e.UserSub, WorkflowNodeID: e.WorkflowNodeID,
		RootKind: e.RootKind, RootID: e.RootID, RootName: e.RootName, RootNodeID: e.RootNodeID,
		EntrypointID: e.EntrypointID, EntrypointName: e.EntrypointName, Channel: e.Channel,
		TriggerKind: e.TriggerKind, TriggerID: e.TriggerID, TaskID: e.TaskID, ParentTaskID: e.ParentTaskID,
	}
	if got != want {
		t.Errorf("resumed decision booked as\n  %+v\nwant\n  %+v", got, want)
	}
}
