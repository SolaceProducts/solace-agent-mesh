// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type fakeJev struct {
	mu               sync.Mutex
	status           int
	errorBody        string
	statusFor        func(state map[string]any) int
	delay            time.Duration
	respond          func(state map[string]any) string
	arrived          chan struct{}
	abandoned        chan struct{}
	calls            atomic.Int32
	inFlight         atomic.Int32
	peak             atomic.Int32
	lastBody         map[string]any
	lastAuth         string
	lastImagesHeader string
}

func (f *fakeJev) handler(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	f.enter()
	defer f.inFlight.Add(-1)
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.lastBody = body
	f.lastAuth = r.Header.Get("Authorization")
	f.lastImagesHeader = r.Header.Get("TypeSafe-Danger-Zone-Images")
	f.mu.Unlock()
	if f.arrived != nil {
		select {
		case f.arrived <- struct{}{}:
		default:
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-r.Context().Done():
			if f.abandoned != nil {
				select {
				case f.abandoned <- struct{}{}:
				default:
				}
			}
			return
		}
	}
	state, _ := body["state"].(map[string]any)
	status := f.status
	if f.statusFor != nil {
		status = f.statusFor(state)
	}
	if status != 0 {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, f.errorBody)
		return
	}
	_, _ = io.WriteString(w, f.respond(state))
}

func (f *fakeJev) enter() {
	n := f.inFlight.Add(1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			return
		}
	}
}

func (f *fakeJev) sentState() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, _ := f.lastBody["state"].(map[string]any)
	return state
}

func departmentAnswer(choice string, confidence float64) func(map[string]any) string {
	return func(map[string]any) string {
		return `{"model":"jev-1.13.0","answers":{
			"department":{"type":"choice","choice":"` + choice + `","probabilities":{"billing":0.5,"tech":0.5},"confidence":` + jsonNumber(confidence) + `},
			"urgent":{"type":"noul","noul":0.9}},
			"usage":{"input_tokens":40,"output_tokens":0}}`
	}
}

func departmentAnswerWithoutConfidence(choice string) func(map[string]any) string {
	return func(map[string]any) string {
		return `{"model":"jev-1.13.0","answers":{
			"department":{"type":"choice","choice":"` + choice + `","probabilities":{"billing":0.5,"tech":0.5}},
			"urgent":{"type":"noul","noul":0.9}}}`
	}
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

const routedDecisionWorkflow = `
decision:
  api_key: test-key
  model: jev-1.13.0
output_mapping:
  result: {coalesce: ["{{billing.output}}", "{{tech.output}}", "{{fallback.output}}"]}
nodes:
  - id: triage
    type: decision
    questions:
      department:
        type: choice
        instructions: "Which team owns ` + "`ticket`" + `?"
        criteria: {billing: Payments, tech: Bugs, sales: Pricing}
      urgent: {type: boolean, instructions: "The ticket is urgent"}
    route_on: department
    routes: {billing: billing, tech: tech}
    min_confidence: 0.8
    escalate: fallback
    escalation_mode: handoff
    default: fallback
  - {id: billing, type: tool, tool_name: billing_tool, depends_on: [triage]}
  - {id: tech, type: tool, tool_name: tech_tool, depends_on: [triage]}
  - {id: fallback, type: tool, tool_name: fallback_tool, depends_on: [triage]}
`

type decisionRun struct {
	e     *DAGExecutor
	jev   *fakeJev
	tools map[string]*fakeTool
}

func newDecisionRun(t *testing.T, doc string, jev *fakeJev, flagOn bool, wire ...func(jevURL string, svc *stubServices)) *decisionRun {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(jev.handler))
	t.Cleanup(srv.Close)

	cfg, err := parseYAMLWorkflow(t, doc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Decision != nil && cfg.Decision.ModelAlias == "" {
		cfg.Decision.APIBase = srv.URL
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	state := NewExecutionState(cfg.Name, "exec-1")
	state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{"ticket": "charged twice"}}
	e := newTestDAGExecutor(cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)

	svc := &stubServices{brk: stubBroker{}, features: decisionFeatureRegistry(t, flagOn)}
	for _, w := range wire {
		w(srv.URL, svc)
	}
	e.instance.svc = svc

	run := &decisionRun{e: e, jev: jev, tools: map[string]*fakeTool{}}
	ts := tool.NewSet()
	for _, n := range cfg.Nodes {
		if n.Type == "tool" {
			ft := &fakeTool{name: n.ToolName}
			run.tools[n.ID] = ft
			if err := ts.Register(ft); err != nil {
				t.Fatalf("register %s: %v", n.ToolName, err)
			}
		}
	}
	e.instance.tools = ts
	if err := e.instance.initDecisions(svc); err != nil {
		t.Fatalf("initDecisions: %v", err)
	}
	return run
}

func decisionFeatureRegistry(t *testing.T, on bool) *features.Registry {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyWorkflowDecisionNode), "")
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: on})
	return reg
}

func (r *decisionRun) execute(t *testing.T) {
	t.Helper()
	if err := r.e.Advance(t.Context(), &EventStart{
		TaskID: "task-1", ContextID: "ctx-1", Input: map[string]any{"ticket": "charged twice"},
		UserProps: a2a.UserProperties{},
	}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	r.e.instance.wg.Wait()
}

func (r *decisionRun) output(t *testing.T, nodeID string) map[string]any {
	t.Helper()
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	out, _ := r.e.state.NodeOutputs[nodeID]["output"].(map[string]any)
	if out == nil {
		t.Fatalf("node %q has no output; completed=%v skipped=%v error=%+v",
			nodeID, r.e.state.CompletedNodes, r.e.state.SkippedNodes, r.e.state.ErrorState)
	}
	return out
}

func (r *decisionRun) assertBranches(t *testing.T, ran string, skipped ...string) {
	t.Helper()
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	if !r.e.state.IsNodeCompleted(ran) {
		t.Errorf("%q should have run; completed=%v error=%+v", ran, r.e.state.CompletedNodes, r.e.state.ErrorState)
	}
	for _, id := range skipped {
		if !r.e.state.IsNodeSkipped(id) {
			t.Errorf("%q should have been skipped", id)
		}
		if r.tools[id].lastToolCtx != nil {
			t.Errorf("%q was skipped but its tool still ran", id)
		}
		if reason := r.e.state.SkippedNodes[id]; !strings.Contains(reason, `not selected by decision "triage"`) {
			t.Errorf("%q skip reason = %q", id, reason)
		}
	}
}

func (r *decisionRun) start(t *testing.T) {
	t.Helper()
	if err := r.e.Advance(t.Context(), &EventStart{
		TaskID: "task-1", ContextID: "ctx-1", Input: map[string]any{"ticket": "charged twice"},
		UserProps: a2a.UserProperties{},
	}); err != nil {
		t.Fatalf("advance: %v", err)
	}
}

func (r *decisionRun) wait(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		r.e.instance.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the workflow did not settle within 15s")
	}
}

func (r *decisionRun) escalationNeverRan(t *testing.T) {
	t.Helper()
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	if _, dispatched := r.e.state.SubTaskForNode("fallback"); dispatched || r.e.state.IsNodeCompleted("fallback") {
		t.Error("the escalate target was dispatched")
	}
	if r.tools["fallback"].lastToolCtx != nil {
		t.Error("the escalate target's tool ran")
	}
}

func (r *decisionRun) errorMessage() string {
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	if r.e.state.ErrorState == nil {
		return ""
	}
	return r.e.state.ErrorState.ErrorMessage
}

func TestDecisionNodeRoutesOnTheChosenOption(t *testing.T) {
	jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, routedDecisionWorkflow, jev, true)
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	out := run.output(t, "triage")
	if out["selected_branch"] != "billing" || out["escalated"] != false || out["model"] != "jev-1.13.0" {
		t.Errorf("triage output = %v", out)
	}
	answers := out["answers"].(map[string]any)
	if dept := answers["department"].(map[string]any); dept["choice"] != "billing" || dept["confidence"] != 0.9 {
		t.Errorf("department answer = %v", dept)
	}
	if urgent := answers["urgent"].(map[string]any); urgent["value"] != true {
		t.Errorf("urgent answer = %v", urgent)
	}
	if got := jev.sentState()["ticket"]; got != "charged twice" {
		t.Errorf("provider received state %v, want the workflow input", jev.sentState())
	}
}

func TestDecisionNodeEscalatesBelowMinConfidence(t *testing.T) {
	run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.55)}, true)
	run.execute(t)

	run.assertBranches(t, "fallback", "billing", "tech")
	out := run.output(t, "triage")
	if out["escalated"] != true || out["escalation_reason"] != escalationLowConfidence {
		t.Errorf("triage output = %v", out)
	}
}

func TestDecisionNodeFallsBackToDefaultForAnUnroutedOption(t *testing.T) {
	run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{respond: departmentAnswer("sales", 0.95)}, true)
	run.execute(t)

	run.assertBranches(t, "fallback", "billing", "tech")
	if out := run.output(t, "triage"); out["escalated"] != false {
		t.Errorf("a default route is not an escalation: %v", out)
	}
}

func TestSelectDecisionBranchWithNoRouteAndNoDefaultFails(t *testing.T) {
	node := &NodeConfig{ID: "triage", RouteOn: "department", Routes: map[string]string{"billing": "billing"}}
	_, _, err := selectDecisionBranch(node, decision.Answer{Type: decision.TypeChoice, Choice: "sales"})
	if err == nil || !strings.Contains(err.Error(), `chose "sales"`) || !strings.Contains(err.Error(), "no default") {
		t.Fatalf("err = %v, want the unrouted-choice failure", err)
	}
}

func TestDecisionNodeConfidenceBoundary(t *testing.T) {
	cases := map[string]struct {
		respond       func(map[string]any) string
		wantBranch    string
		wantEscalated bool
	}{
		"confidence equal to min_confidence routes": {
			respond: departmentAnswer("billing", 0.8), wantBranch: "billing",
		},
		"missing confidence escalates": {
			respond: departmentAnswerWithoutConfidence("billing"), wantBranch: "fallback", wantEscalated: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{respond: tc.respond}, true)
			run.execute(t)

			out := run.output(t, "triage")
			if out["selected_branch"] != tc.wantBranch || out["escalated"] != tc.wantEscalated {
				t.Errorf("triage output = %v, want branch %q escalated=%v", out, tc.wantBranch, tc.wantEscalated)
			}
		})
	}
}

var escalatingDecisionWorkflow = strings.Replace(routedDecisionWorkflow,
	"    default: fallback\n", "    default: fallback\n    on_provider_error: escalate\n", 1)

func TestDecisionNodeProviderFailure(t *testing.T) {
	t.Run("a transient failure fails the node by default", func(t *testing.T) {
		run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{status: http.StatusServiceUnavailable}, true)
		run.execute(t)
		if msg := run.errorMessage(); !strings.Contains(msg, "503") {
			t.Fatalf("workflow error = %q, want the provider failure", msg)
		}
		run.escalationNeverRan(t)
	})
	t.Run("a transient failure escalates when configured to, exposing only its category", func(t *testing.T) {
		run := newDecisionRun(t, escalatingDecisionWorkflow, &fakeJev{status: http.StatusServiceUnavailable}, true)
		run.execute(t)

		run.assertBranches(t, "fallback", "billing", "tech")
		out := run.output(t, "triage")
		if out["escalation_reason"] != escalationProviderError || out["provider_error"] != string(decision.FailureUnreachable) {
			t.Errorf("triage output = %v, want a provider_error escalation of kind %q", out, decision.FailureUnreachable)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), "127.0.0.1") || strings.Contains(string(raw), "503") {
			t.Errorf("the output carries raw provider error text: %s", raw)
		}
		if meta := decisionResultMetadata(out); meta["provider_error"] != string(decision.FailureUnreachable) {
			t.Errorf("result metadata = %v, want the failure kind", meta)
		}
	})
	t.Run("an input over the model's context limit escalates to the larger model", func(t *testing.T) {
		jev := &fakeJev{status: http.StatusBadRequest, errorBody: `{"detail":{"error_type":"max_tokens_exceeded"}}`}
		run := newDecisionRun(t, escalatingDecisionWorkflow, jev, true)
		run.execute(t)

		run.assertBranches(t, "fallback", "billing", "tech")
		out := run.output(t, "triage")
		if out["escalation_reason"] != escalationProviderError || out["provider_error"] != string(decision.FailureInputTooLarge) {
			t.Errorf("triage output = %v, want a provider_error escalation of kind %q", out, decision.FailureInputTooLarge)
		}
	})
	t.Run("a spent budget fails the node before its provider, even when it escalates", func(t *testing.T) {
		jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
		run := newDecisionRun(t, escalatingDecisionWorkflow, jev, true)
		useBudgetGate(run.e.instance, budgettest.BlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)))
		run.execute(t)

		if msg := run.errorMessage(); !strings.Contains(msg, "the mesh budget is spent until 2099-11-01T00:00:00Z") {
			t.Fatalf("workflow error = %q, want the budget refusal", msg)
		}
		run.escalationNeverRan(t)
		if n := jev.calls.Load(); n != 0 {
			t.Errorf("the provider was called %d times while the budget was spent, want 0", n)
		}
	})
	t.Run("a spent budget ends the workflow as a budget stop, not a fault", func(t *testing.T) {
		brk := &capturingBroker{}
		run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.9)}, true,
			func(_ string, svc *stubServices) { svc.brk = brk })
		useBudgetGate(run.e.instance, budgettest.BlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)))
		run.e.execCtx.UserProps.ReplyTo = usageFactsReplyTopic
		run.execute(t)

		task := terminalTask(t, brk)
		if task.Status.State != a2a.TaskStateFailed {
			t.Fatalf("terminal state = %q, want failed", task.Status.State)
		}
		if got := task.Metadata[a2a.ErrorDataLLMErrorCategory]; got != taxonomy.ErrorCategoryBudgetExhausted {
			t.Errorf("failure category = %v, want %q so the scheduler does not count it toward auto-pause",
				got, taxonomy.ErrorCategoryBudgetExhausted)
		}
		var text string
		for _, part := range task.Status.Message.Parts {
			text += part.Text
		}
		if !strings.Contains(text, "usage budget is used up until 1 November 2099") {
			t.Errorf("failure text = %q, want the budget stop the web UI classifies", text)
		}
		if reason, _ := task.Metadata["error"].(string); !strings.Contains(reason, "the mesh budget is spent until 2099-11-01T00:00:00Z") {
			t.Errorf("metadata error = %q, want the node's refusal that evidence recognizes", reason)
		}
	})
	for name, tc := range map[string]struct{ user, want string }{
		"a stop on the invoking user's own budget says it is theirs": {
			"alice@example.com", "your usage budget is used up until 1 November 2099"},
		"a stop on a service account's own budget names the service account": {
			"system:nightly", `usage budget is used up until 1 November 2099 00:00 UTC for the service account "nightly" this runs as`},
	} {
		t.Run(name, func(t *testing.T) {
			brk := &capturingBroker{}
			run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{respond: departmentAnswer("billing", 0.9)}, true,
				func(_ string, svc *stubServices) { svc.brk = brk })
			useBudgetGate(run.e.instance, budgettest.UserBlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC),
				budget.UserKey("", tc.user)))
			run.e.execCtx.UserProps.ReplyTo = usageFactsReplyTopic
			run.e.execCtx.UserProps.UserID = tc.user
			run.execute(t)

			var text string
			for _, part := range terminalTask(t, brk).Status.Message.Parts {
				text += part.Text
			}
			if !strings.Contains(text, tc.want) {
				t.Errorf("failure text = %q, want %q", text, tc.want)
			}
		})
	}
	for name, tc := range map[string]struct {
		status   int
		wantKind decision.FailureKind
	}{
		"rejected credentials": {status: http.StatusUnauthorized, wantKind: decision.FailureUnauthorized},
		"refused request":      {status: http.StatusUnprocessableEntity, wantKind: decision.FailureConfig},
	} {
		t.Run(name+" fails the node even when it escalates", func(t *testing.T) {
			run := newDecisionRun(t, escalatingDecisionWorkflow, &fakeJev{status: tc.status}, true)
			logs := &lockedWriter{w: &strings.Builder{}, mu: &sync.Mutex{}}
			run.e.logger = slog.New(slog.NewTextHandler(logs, nil))
			run.execute(t)

			msg := run.errorMessage()
			if !strings.Contains(msg, strconv.Itoa(tc.status)) || !strings.Contains(msg, string(tc.wantKind)+" failure") {
				t.Fatalf("workflow error = %q, want the provider failure tagged %s", msg, tc.wantKind)
			}
			run.escalationNeverRan(t)
			logs.mu.Lock()
			got := logs.w.String()
			logs.mu.Unlock()
			if n := strings.Count(got, "node=triage"); n != 1 || !strings.Contains(got, `msg="node failed" node=triage`) {
				t.Errorf("want the failure logged once, by the executor; got %d node lines:\n%s", n, got)
			}
		})
	}
}

func TestDecisionNodeTransportFailureKeepsTheEndpointOutOfTheError(t *testing.T) {
	run := newDecisionRun(t, routedDecisionWorkflow, &fakeJev{}, true)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	run.e.instance.cfg.Decision.APIBase = closedURL
	if err := run.e.instance.initDecisions(run.e.instance.svc); err != nil {
		t.Fatalf("initDecisions: %v", err)
	}
	logs := &lockedWriter{w: &strings.Builder{}, mu: &sync.Mutex{}}
	run.e.logger = slog.New(slog.NewTextHandler(logs, nil))
	run.execute(t)

	endpoint := strings.TrimPrefix(closedURL, "http://")
	msg := run.errorMessage()
	if !strings.Contains(msg, "unreachable failure") || strings.Contains(msg, endpoint) || strings.Contains(msg, "127.0.0.1") {
		t.Errorf("workflow error = %q, want the unreachable category without the endpoint %s", msg, endpoint)
	}
	logs.mu.Lock()
	got := logs.w.String()
	logs.mu.Unlock()
	if !strings.Contains(got, endpoint) {
		t.Errorf("the node-failure log lost the cause naming %s:\n%s", endpoint, got)
	}
}

func TestDecisionNodeEscalatesWhenTheProviderOutlivesTheNodeTimeout(t *testing.T) {
	doc := strings.Replace(routedDecisionWorkflow, "    default: fallback\n",
		"    default: fallback\n    on_provider_error: escalate\n    timeout: 300ms\n", 1)
	run := newDecisionRun(t, doc, &fakeJev{delay: 3 * time.Second, respond: departmentAnswer("billing", 0.9)}, true)
	run.execute(t)

	run.assertBranches(t, "fallback", "billing", "tech")
	if out := run.output(t, "triage"); out["escalation_reason"] != escalationProviderError ||
		out["provider_error"] != string(decision.FailureTimeout) {
		t.Errorf("triage output = %v, want a provider_error escalation of kind %q", out, decision.FailureTimeout)
	}
}

func TestDecisionNodeCancelledMidCallNeverEscalates(t *testing.T) {
	const cancelPropagation = 2 * time.Second
	for name, stop := range map[string]Event{
		"cancel":           &EventCancel{Reason: "client-requested"},
		"workflow timeout": &EventWorkflowTimeout{},
	} {
		t.Run(name, func(t *testing.T) {
			jev := &fakeJev{delay: time.Minute, arrived: make(chan struct{}, 1), abandoned: make(chan struct{}, 1)}
			run := newDecisionRun(t, escalatingDecisionWorkflow, jev, true)
			logs := &lockedWriter{w: &strings.Builder{}, mu: &sync.Mutex{}}
			run.e.logger = slog.New(slog.NewTextHandler(logs, nil))
			run.start(t)
			select {
			case <-jev.arrived:
			case <-time.After(5 * time.Second):
				t.Fatal("the provider call never started")
			}

			if err := run.e.Advance(t.Context(), stop); err != nil {
				t.Fatalf("advance %s: %v", name, err)
			}
			select {
			case <-jev.abandoned:
			case <-time.After(cancelPropagation):
				t.Fatalf("the in-flight provider call was not cancelled within %v of %s", cancelPropagation, name)
			}
			run.wait(t)

			logs.mu.Lock()
			got := logs.w.String()
			logs.mu.Unlock()
			if strings.Contains(got, "escalating") {
				t.Errorf("a cancelled call was treated as a provider failure to escalate:\n%s", got)
			}

			run.e.mu.Lock()
			cancelled, finalized := run.e.state.Cancelled, run.e.state.Finalized
			run.e.mu.Unlock()
			if !cancelled || !finalized {
				t.Errorf("state cancelled=%v finalized=%v, want a cancelled, finalized run", cancelled, finalized)
			}
			run.escalationNeverRan(t)
		})
	}
}

func TestDecisionNodeAnswersDriveDownstreamWhenClauses(t *testing.T) {
	doc := `
decision: {api_key: test-key, model: jev-1.13.0}
output_mapping: {r: {coalesce: ["{{page.output}}", "{{noop.output}}"]}}
nodes:
  - id: triage
    type: decision
    questions:
      urgent: {type: boolean, instructions: "The ticket is urgent"}
      department:
        type: choice
        instructions: route it
        criteria: {billing: b, tech: t}
  - {id: page, type: tool, tool_name: page_tool, depends_on: [triage], when: "{{triage.output.answers.urgent.value}} == true"}
  - {id: noop, type: tool, tool_name: noop_tool, depends_on: [triage], when: "'{{triage.output.answers.department.choice}}' == 'billing'"}
`
	jev := &fakeJev{respond: departmentAnswer("tech", 0.9)}
	run := newDecisionRun(t, doc, jev, true)
	run.execute(t)

	run.e.mu.Lock()
	defer run.e.mu.Unlock()
	if !run.e.state.IsNodeCompleted("page") {
		t.Errorf("urgent=true should run page; skipped=%v error=%+v", run.e.state.SkippedNodes, run.e.state.ErrorState)
	}
	if !run.e.state.IsNodeSkipped("noop") {
		t.Errorf("department=tech should skip noop")
	}
	if jev.calls.Load() != 1 {
		t.Errorf("both questions must share one provider call, got %d calls", jev.calls.Load())
	}
}

const decisionMapWorkflow = `
decision: {api_key: test-key, model: jev-1.13.0}
output_mapping: {pages: "{{each.output}}"}
nodes:
  - id: each
    type: map
    items: "{{workflow_input.output.pages}}"
    node: classify
  - id: classify
    type: decision
    input: {page: "{{_map_item}}"}
    questions:
      privileged: {type: boolean, instructions: "` + "`page`" + ` may be privileged"}
`

func privilegedAnswer(map[string]any) string {
	return `{"model":"jev-1.13.0","answers":{"privileged":{"type":"noul","noul":0.1}}}`
}

func withPages(run *decisionRun, n int) {
	pages := make([]any, n)
	for i := range pages {
		pages[i] = "page-" + strconv.Itoa(i)
	}
	run.e.state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{"pages": pages}}
}

func TestDecisionNodeAsMapTemplateClassifiesEachItem(t *testing.T) {
	doc := decisionMapWorkflow
	jev := &fakeJev{respond: func(state map[string]any) string {
		p := "0.1"
		if strings.Contains(state["page"].(string), "attorney") {
			p = "0.95"
		}
		return `{"model":"jev-1.13.0","answers":{"privileged":{"type":"noul","noul":` + p + `}}}`
	}}
	run := newDecisionRun(t, doc, jev, true)
	run.e.state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{
		"pages": []any{"quarterly numbers", "note from our attorney", "lunch menu"},
	}}
	run.execute(t)

	out := run.output(t, "each")
	results, _ := out["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("map output = %v, want three results", out)
	}
	var privileged []bool
	for _, r := range results {
		answers := r.(map[string]any)["answers"].(map[string]any)
		privileged = append(privileged, answers["privileged"].(map[string]any)["value"].(bool))
	}
	if privileged[0] || !privileged[1] || privileged[2] {
		t.Errorf("privileged per page = %v, want [false true false]", privileged)
	}
	if jev.calls.Load() != 3 {
		t.Errorf("calls = %d, want one per item", jev.calls.Load())
	}
}

func TestDecisionMapCapsConcurrentProviderCalls(t *testing.T) {
	const items = 50
	jev := &fakeJev{delay: 20 * time.Millisecond, respond: privilegedAnswer}
	run := newDecisionRun(t, decisionMapWorkflow, jev, true)
	withPages(run, items)
	run.start(t)
	run.wait(t)

	if results, _ := run.output(t, "each")["results"].([]any); len(results) != items {
		t.Fatalf("map output has %d results, want %d", len(results), items)
	}
	if calls := jev.calls.Load(); calls != items {
		t.Errorf("calls = %d, want one per item", calls)
	}
	if peak := jev.peak.Load(); peak > defaultToolMapConcurrency {
		t.Errorf("peak concurrent provider calls = %d, want at most %d", peak, defaultToolMapConcurrency)
	}
}

func TestDecisionMapFailsOnAPermanentItemFailure(t *testing.T) {
	jev := &fakeJev{respond: privilegedAnswer, statusFor: func(state map[string]any) int {
		if state["page"] == "page-3" {
			return http.StatusUnprocessableEntity
		}
		return 0
	}}
	run := newDecisionRun(t, decisionMapWorkflow, jev, true)
	withPages(run, 20)
	run.start(t)
	run.wait(t)

	if msg := run.errorMessage(); !strings.Contains(msg, "422") {
		t.Fatalf("workflow error = %q, want the refused item to fail the map", msg)
	}
}

func TestDecisionNodeAsLoopBodyStopsTheLoopOnItsAnswer(t *testing.T) {
	doc := `
decision: {api_key: test-key, model: jev-1.13.0}
output_mapping: {r: "{{poll.output}}"}
nodes:
  - id: poll
    type: loop
    node: check
    condition: "{{check.output.answers.done.value}} == false"
    max_iterations: 5
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
		return `{"model":"jev-1.13.0","answers":{"done":{"type":"noul","noul":` + p + `}}}`
	}
	run := newDecisionRun(t, doc, jev, true)
	run.execute(t)

	out := run.output(t, "poll")
	if out["iterations_completed"] != 2 {
		t.Errorf("loop output = %v, want it to stop after the second answer said done", out)
	}
	if jev.calls.Load() != 2 {
		t.Errorf("calls = %d, want one per iteration", jev.calls.Load())
	}
}

func TestDecisionNodeWithFeatureOffSendsNothing(t *testing.T) {
	jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, routedDecisionWorkflow, jev, false)
	run.execute(t)

	if msg := run.errorMessage(); !strings.Contains(msg, features.KeyWorkflowDecisionNode) {
		t.Fatalf("workflow error = %q, want it to name the disabled flag", msg)
	}
	if jev.calls.Load() != 0 {
		t.Errorf("a disabled decision node reached the provider %d times", jev.calls.Load())
	}
}

func TestInitDecisionsRequiresAProviderWhenEnabled(t *testing.T) {
	svc := &stubServices{brk: stubBroker{}, features: decisionFeatureRegistry(t, true)}

	cases := map[string]struct {
		provider *DecisionProviderConfig
		wantErr  string
	}{
		"no provider block": {provider: nil, wantErr: "require a workflow `decision:` provider"},
		"no api key":        {provider: &DecisionProviderConfig{Provider: DecisionProviderTypeSafe}, wantErr: "api_key is required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := &WorkflowConfig{
				Name:     "wf",
				Decision: tc.provider,
				Nodes:    []*NodeConfig{{ID: "d", Type: "decision"}},
			}
			w := newTestWorkflowInstance(cfg, svc)
			w.logger = slog.New(slog.DiscardHandler)
			err := w.initDecisions(svc)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestBuildVizNodeDescribesADecisionNode(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, routedDecisionWorkflow)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	viz := w.buildVizNode(cfg.Nodes[0])

	if viz["type"] != "decision" || viz["route_on"] != "department" || viz["escalate"] != "fallback" || viz["escalation_mode"] != EscalationModeHandoff ||
		viz["default"] != "fallback" || viz["min_confidence"] != 0.8 {
		t.Errorf("viz node = %v", viz)
	}
	if routes := viz["routes"].(map[string]string); routes["billing"] != "billing" || routes["tech"] != "tech" {
		t.Errorf("routes = %v", routes)
	}
	questions := viz["questions"].([]map[string]any)
	if len(questions) != 2 || questions[0]["key"] != "department" || questions[1]["type"] != "boolean" {
		t.Fatalf("questions = %v", questions)
	}
	if criteria := questions[0]["criteria"].([]string); strings.Join(criteria, ",") != "billing,sales,tech" {
		t.Errorf("department criteria = %v", criteria)
	}
	if _, ok := questions[1]["criteria"]; ok {
		t.Error("a question with no criteria should not advertise any")
	}
	if _, ok := viz["on_provider_error"]; ok {
		t.Errorf("on_provider_error = %v on a node that leaves it at the default", viz["on_provider_error"])
	}
}

func TestBuildVizNodeEmitsTheDefaultEscalationModeAsReroute(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, escalationRoutedWorkflow)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Nodes[0].EscalationMode != "" {
		t.Fatalf("fixture sets escalation_mode %q; this test needs it unset", cfg.Nodes[0].EscalationMode)
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if got := w.buildVizNode(cfg.Nodes[0])["escalation_mode"]; got != EscalationModeReroute {
		t.Errorf("escalation_mode = %v, want %q for a node that leaves it unset", got, EscalationModeReroute)
	}
}

func TestBuildVizNodeKeepsARoutesAuthoredDependencies(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, escalationRoutedWorkflow)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	tech := dag.Nodes["tech"]
	if !slices.Contains(tech.waitsFor(), "reclassify") {
		t.Fatalf("tech waits for %v, want the reroute wait on reclassify", tech.waitsFor())
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if got := w.buildVizNode(tech)["depends_on"]; !slices.Equal(got.([]string), []string{"triage"}) {
		t.Errorf("viz depends_on = %v, want the authored [triage]", got)
	}
}

func TestBuildVizNodeCarriesOnProviderError(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, escalatingDecisionWorkflow)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if got := w.buildVizNode(cfg.Nodes[0])["on_provider_error"]; got != "escalate" {
		t.Errorf("on_provider_error = %v, want escalate", got)
	}
}

func TestInitDecisionsSkipsWorkflowsWithoutDecisionNodes(t *testing.T) {
	cfg := &WorkflowConfig{Name: "wf", Nodes: []*NodeConfig{{ID: "a", Type: "agent", AgentName: "A"}}}
	w := newTestWorkflowInstance(cfg, &stubServices{brk: stubBroker{}})
	if err := w.initDecisions(w.svc); err != nil {
		t.Fatalf("a workflow with no decision nodes needs no provider: %v", err)
	}
	if w.decisions != nil {
		t.Error("no provider should be built for a workflow without decision nodes")
	}
}

func useBudgetGate(w *WorkflowInstance, gate *budget.Gate) {
	w.svc = budgettest.WithGate(w.svc, gate)
}
