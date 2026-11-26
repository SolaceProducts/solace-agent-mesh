// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func newHILExecutorFor(t *testing.T, cfg *WorkflowConfig, dag *DependencyGraph, registered tool.Tool) (*DAGExecutor, *capturingBroker) {
	t.Helper()
	state := NewExecutionState(cfg.Name, "exec-hil")
	e, brk := newCapturingExecutor(t, cfg, dag, state)
	t.Cleanup(e.execCtx.Cancel)
	db, err := wfstore.Open("sqlite:///"+filepath.Join(t.TempDir(), "wf.db"), slog.Default())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	e.instance.ctx = t.Context()
	e.instance.pausedDB = db
	e.instance.pausedRepo = wfstore.NewPausedRepo(db)
	e.instance.tools = toolSetWith(t, registered)
	e.execCtx.UserProps.UserID = "approver@test"
	exec := &WorkflowExecution{State: state, Context: e.execCtx, Executor: e}
	e.instance.executions[e.execCtx.TaskID] = exec
	e.instance.executionsByID[state.ExecutionID] = exec
	return e, brk
}

func waitForToolSignals(t *testing.T, brk *capturingBroker, want int) []toolSignal {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		signals := toolSignals(t, brk)
		if len(signals) >= want {
			return signals
		}
		if time.Now().After(deadline) {
			t.Fatalf("saw %d tool signals, want %d: %+v", len(signals), want, signals)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertEveryStartClosedOnce(t *testing.T, signals []toolSignal) map[string]toolSignal {
	t.Helper()
	starts, results := map[string]int{}, map[string]toolSignal{}
	resultCount := map[string]int{}
	for _, s := range signals {
		switch s.Type {
		case string(a2a.SignalToolInvocationStart):
			starts[s.CallID]++
		case string(a2a.SignalToolResult):
			resultCount[s.CallID]++
			results[s.CallID] = s
		}
	}
	for call, n := range starts {
		if n != 1 || resultCount[call] != 1 {
			t.Errorf("call %s: %d starts and %d results, want exactly one of each", call, n, resultCount[call])
		}
	}
	for call := range resultCount {
		if starts[call] == 0 {
			t.Errorf("call %s has a result but no start", call)
		}
	}
	return results
}

func resultCode(s toolSignal) any {
	payload, _ := s.Data["result_data"].(map[string]any)
	return payload["error_code"]
}

type perItemTool struct {
	*schemaTool
	fail string
}

func (p perItemTool) Execute(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
	q, _ := args["q"].(string)
	if q == p.fail {
		return tool.Error("item failed"), nil
	}
	return &tool.Result{Status: tool.StatusPending, Data: map[string]any{
		"hil_request_id": "req-" + q, "surface": map[string]any{"surfaceId": "s"},
	}}, nil
}

func deferringMapWorkflow(items []any) (*WorkflowConfig, *DependencyGraph) {
	nodes := []*NodeConfig{
		{ID: "each", Type: "map", TemplateNode: "ask_each", ItemsExpression: items},
		{ID: "ask_each", Type: "tool", ToolName: "ask", Input: map[string]any{"q": "{{_map_item}}"}, DependsOn: []string{"each"}},
	}
	dag, _ := BuildDependencyGraph(nodes)
	return &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, FailFast: true, OutputMapping: map[string]any{"done": "yes"}}, dag
}

func TestFailFastMapClosesEveryPausedSiblingCall(t *testing.T) {
	cfg, dag := deferringMapWorkflow([]any{"a", "b", "c"})
	e, brk := newHILExecutorFor(t, cfg, dag, perItemTool{schemaTool: &schemaTool{name: "ask"}, fail: "b"})

	runToCompletion(t, e)
	waitForToolSignals(t, brk, 6)
	e.instance.drainRetirements()
	results := assertEveryStartClosedOnce(t, toolSignals(t, brk))
	if len(results) != 3 {
		t.Fatalf("results for %d calls, want 3", len(results))
	}
	for call, r := range results {
		code := resultCode(r)
		if code != nil && code != "HIL_RUN_ENDED" && code != "HIL_RUN_ENDED_UNANSWERED" {
			t.Errorf("call %s closed with %v, want the failure or a run-ended refusal", call, code)
		}
		assertToolNodeMetadata(t, r, "ask_each")
	}
}

func TestWorkflowTimeoutClosesAPausedCall(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))
	pausedRequestID(t, e)

	deliverDeadline(t, e.instance, "exec-hil")
	e.instance.drainRetirements()
	start, result := onePair(t, toolSignals(t, brk))
	assertToolNodeMetadata(t, start, "ask_node")
	assertHILRefusal(t, result, "HIL_RUN_ENDED_UNANSWERED")
}

func TestApprovedCallPublishesUnderTheParkedIdentity(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, gatedTelemetryNode(), scriptedTool{schemaTool: &schemaTool{name: "gated"}, result: tool.OK("ran")})
	gateTool(e, "gated")
	requestID := pausedRequestID(t, e)
	rewriteParkedState(t, e.instance, "exec-hil", func(s *WorkflowExecutionState) { s.RetryCounts["gated_node"] = 1 })

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	e.instance.wg.Wait()

	start, result := onePair(t, toolSignals(t, brk))
	assertToolNodeMetadata(t, start, "gated_node")
	assertToolNodeMetadata(t, result, "gated_node")
}

func TestRowWithoutAPersistedIdentityIsSettledWithTheWholePair(t *testing.T) {
	legacy := persistedRouting{NodeID: "ask_node", SubTaskID: "sub-1", ToolNodeID: "ask_node", ToolName: "ask"}
	if legacy.toolCall().announced {
		t.Error("a row a pre-telemetry binary wrote never had its start published, so it must not be read as announced")
	}
	current := legacy
	current.RetryGroupID = "g-1"
	if !current.toolCall().announced {
		t.Error("a deferred row with a persisted identity had its start published")
	}
}

func TestClosingAbandonedCallsStopsWhenTheBudgetIsSpent(t *testing.T) {
	cfg, dag := singleToolNodeWorkflow(askTelemetryNode())
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	var logged bytes.Buffer
	e.instance.logger = slog.New(slog.NewTextHandler(&logged, nil))
	row := pausedRow{
		row:              wfstore.PausedWorkflow{RequestID: "req-1", ExecutionID: "exec-1"},
		routing:          persistedRouting{NodeID: "ask_node", SubTaskID: "sub-1", ToolNodeID: "ask_node", ToolName: "ask", RetryGroupID: "g-1"},
		execContext:      persistedExecContext{UserProps: a2a.UserProperties{A2AStatusTopic: telemetryStatusTopic}, TaskID: "test-task"},
		execContextKnown: true,
	}
	spent, cancel := context.WithCancel(t.Context())
	cancel()

	e.instance.closeAbandonedToolCalls(spent, []pausedRow{row})

	if signals := toolSignals(t, brk); len(signals) != 0 {
		t.Errorf("published %d signals after the budget was spent", len(signals))
	}
	if !strings.Contains(logged.String(), "could not close some tool calls") {
		t.Errorf("the unclosed calls were not reported:\n%s", logged.String())
	}
}

func TestAbandonedRowWithAnUnreadableContextIsReported(t *testing.T) {
	cfg, dag := singleToolNodeWorkflow(askTelemetryNode())
	e, _ := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	var logged bytes.Buffer
	e.instance.logger = slog.New(slog.NewTextHandler(&logged, nil))
	row := pausedRow{
		row:     wfstore.PausedWorkflow{RequestID: "req-1", ExecutionID: "exec-1"},
		routing: persistedRouting{NodeID: "ask_node", SubTaskID: "sub-1", ToolNodeID: "ask_node", ToolName: "ask", RetryGroupID: "g-1"},
	}

	e.instance.closeAbandonedToolCalls(t.Context(), []pausedRow{row})

	if !strings.Contains(logged.String(), "execution context will not decode") {
		t.Errorf("a call left open by an unreadable row was not reported:\n%s", logged.String())
	}
}

type blockingDeferral struct {
	*schemaTool
	release chan struct{}
}

func (b blockingDeferral) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	<-b.release
	return &tool.Result{Status: tool.StatusPending, Data: map[string]any{
		"hil_request_id": "req-late", "surface": map[string]any{"surfaceId": "s"},
	}}, nil
}

func TestPendingResultAfterTheRunFinalizedClosesItsCall(t *testing.T) {
	release := make(chan struct{})
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), blockingDeferral{&schemaTool{name: "ask"}, release})
	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatal(err)
	}
	waitForToolSignals(t, brk, 1)
	if err := e.Advance(t.Context(), &EventCancel{Reason: "user"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	e.instance.wg.Wait()

	_, result := onePair(t, toolSignals(t, brk))
	assertHILRefusal(t, result, "HIL_RUN_ENDED")
}

func TestPauseForANodeNoLongerPendingNamesWhatResolvedIt(t *testing.T) {
	for _, tc := range []struct {
		name           string
		pendingOutcome string
		want           tool.ApprovalRefusal
	}{
		{name: "a node its deadline resolved is refused timed_out", pendingOutcome: "", want: "timed_out"},
		{name: "a node of a run that is ending is refused run_ended", pendingOutcome: "failure", want: "run_ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &DAGExecutor{state: &WorkflowExecutionState{PendingOutcome: tc.pendingOutcome}, logger: slog.New(slog.DiscardHandler)}
			pause := &EventNodePaused{NodeID: "resolved_node", SubTaskID: "sub-1", RequestID: "req-1"}
			e.handlePause(t.Context(), pause)
			if pause.parked || pause.unparked != tc.want {
				t.Errorf("handlePause(%s) parked=%v unparked=%q, want unparked %q", pause.NodeID, pause.parked, pause.unparked, tc.want)
			}
		})
	}
}

func TestSettledCallKeepsTheIdentityItsStartWasPublishedUnder(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))
	requestID := pausedRequestID(t, e)
	rewriteParkedState(t, e.instance, "exec-hil", func(s *WorkflowExecutionState) { s.RetryCounts["ask_node"] = 1 })

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"choice": "yes"})
	e.instance.wg.Wait()

	start, result := onePair(t, toolSignals(t, brk))
	if start.MessageMeta["retry_group_id"] != result.MessageMeta["retry_group_id"] ||
		start.MessageMeta["retry_attempt"] != result.MessageMeta["retry_attempt"] {
		t.Errorf("start identity %v/%v, result identity %v/%v; a settle must reuse the start's",
			start.MessageMeta["retry_group_id"], start.MessageMeta["retry_attempt"],
			result.MessageMeta["retry_group_id"], result.MessageMeta["retry_attempt"])
	}
}

func TestResumeOnAnotherPodPublishesUnderThePausedIdentity(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))
	requestID := pausedRequestID(t, e)
	rewriteParkedState(t, e.instance, "exec-hil", func(s *WorkflowExecutionState) { s.RetryCounts["ask_node"] = 1 })

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"choice": "yes"})
	e.instance.wg.Wait()

	start, result := onePair(t, waitForToolSignals(t, brk, 2))
	assertToolNodeMetadata(t, result, "ask_node")
	if start.MessageMeta["retry_attempt"] != result.MessageMeta["retry_attempt"] {
		t.Errorf("a reconstructed execution closed the call as attempt %v, its start was attempt %v",
			result.MessageMeta["retry_attempt"], start.MessageMeta["retry_attempt"])
	}
	if start.MessageMeta["retry_group_id"] != result.MessageMeta["retry_group_id"] {
		t.Errorf("a reconstructed execution closed the call under group %v, its start used %v",
			result.MessageMeta["retry_group_id"], start.MessageMeta["retry_group_id"])
	}
}

func TestInputResolutionFailurePublishesARefusedPair(t *testing.T) {
	node := &NodeConfig{ID: "bad_input", Type: "tool", ToolName: "lookup",
		Input: map[string]any{"q": map[string]any{"coalesce": "not-a-list"}}}
	cfg, dag := singleToolNodeWorkflow(node)
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})

	runToCompletion(t, e)

	start, result := onePair(t, toolSignals(t, brk))
	if args, _ := start.Data["tool_args"].(map[string]any); args == nil {
		t.Errorf("tool_args = %#v, want an empty object rather than null", start.Data["tool_args"])
	}
	assertDispatchFailurePayload(t, result, tool.ErrorCodeDispatchError)
}

func TestGateRefusalsPublishARefusedPair(t *testing.T) {
	t.Run("no approval route", func(t *testing.T) {
		cfg, dag := singleToolNodeWorkflow(gatedTelemetryNode())
		e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
		e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "gated"}, result: tool.OK("ran")})
		gateTool(e, "gated")

		runToCompletion(t, e)

		_, result := onePair(t, toolSignals(t, brk))
		assertHILRefusal(t, result, "HIL_NO_APPROVAL_ROUTE")
	})
	t.Run("approval pause that cannot be parked", func(t *testing.T) {
		e, brk := newHILTelemetryExecutor(t, gatedTelemetryNode(), scriptedTool{schemaTool: &schemaTool{name: "gated"}, result: tool.OK("ran")})
		gateTool(e, "gated")
		e.execCtx.UserProps.UserID = ""

		runToCompletion(t, e)

		_, result := onePair(t, toolSignals(t, brk))
		assertHILRefusal(t, result, "HIL_PROMPT_FAILED")
	})
	t.Run("approval answered after the run ended", func(t *testing.T) {
		e, brk := newHILTelemetryExecutor(t, gatedTelemetryNode(), scriptedTool{schemaTool: &schemaTool{name: "gated"}, result: tool.OK("ran")})
		gateTool(e, "gated")
		requestID := pausedRequestID(t, e)
		rewriteParkedState(t, e.instance, "exec-hil", func(s *WorkflowExecutionState) { s.Finalized = true })

		e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
		e.instance.wg.Wait()

		_, result := onePair(t, toolSignals(t, brk))
		assertHILRefusal(t, result, "HIL_RUN_ENDED")
	})
	t.Run("approval expired", func(t *testing.T) {
		e, brk := newHILTelemetryExecutor(t, gatedTelemetryNode(), scriptedTool{schemaTool: &schemaTool{name: "gated"}, result: tool.OK("ran")})
		e.instance.hilToolConfigs = map[string]tool.HILToolConfig{"gated": {RequireApproval: true, Timeout: -time.Minute}}
		e.instance.tools.SetHILConfigs(e.instance.hilToolConfigs)
		requestID := pausedRequestID(t, e)

		e.instance.answer(t.Context(), requestID, "timed_out", "timed_out", nil)
		e.instance.wg.Wait()

		_, result := onePair(t, waitForToolSignals(t, brk, 2))
		assertHILRefusal(t, result, "HIL_TIMED_OUT")
	})
}

func TestIterationDeferralsCloseUnderTheTemplateNode(t *testing.T) {
	t.Run("map template", func(t *testing.T) {
		cfg, dag := deferringMapWorkflow([]any{"a"})
		e, brk := newHILExecutorFor(t, cfg, dag, perItemTool{schemaTool: &schemaTool{name: "ask"}})
		requestID := pausedRequestID(t, e)
		started := toolSignals(t, brk)
		branches := e.state.ActiveBranches["each"]
		if len(started) != 1 || len(branches) != 1 || started[0].CallID != branches[0].SubTaskID {
			t.Fatalf("paused map item published %+v for branches %+v, want its start under the iteration's sub-task ID", started, branches)
		}

		e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"choice": "yes"})
		e.instance.wg.Wait()

		_, result := onePair(t, toolSignals(t, brk))
		assertToolNodeMetadata(t, result, "ask_each")
	})
	t.Run("loop body", func(t *testing.T) {
		nodes := []*NodeConfig{
			{ID: "again", Type: "loop", BodyNode: "ask_body", Condition: "true", MaxIterations: 1},
			{ID: "ask_body", Type: "tool", ToolName: "ask", Input: map[string]any{"q": "x"}, DependsOn: []string{"again"}},
		}
		dag, _ := BuildDependencyGraph(nodes)
		cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, OutputMapping: map[string]any{"done": "yes"}}
		e, brk := newHILExecutorFor(t, cfg, dag, perItemTool{schemaTool: &schemaTool{name: "ask"}})
		requestID := pausedRequestID(t, e)

		e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"choice": "yes"})
		e.instance.wg.Wait()

		_, result := onePair(t, toolSignals(t, brk))
		assertToolNodeMetadata(t, result, "ask_body")
	})
}

func TestLoopIterationsAreDistinctRetryGroups(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "again", Type: "loop", BodyNode: "step", Condition: "true", MaxIterations: 2},
		{ID: "step", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "x"}, DependsOn: []string{"again"}},
	}
	dag, _ := BuildDependencyGraph(nodes)
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, OutputMapping: map[string]any{"done": "yes"}}
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})

	runToCompletion(t, e)

	signals := waitForToolSignals(t, brk, 4)
	groups := map[any]bool{}
	for _, s := range signals {
		if s.Type == string(a2a.SignalToolResult) {
			groups[s.MessageMeta["retry_group_id"]] = true
		}
	}
	if len(groups) != 2 {
		t.Errorf("two loop iterations used %d retry groups %v, want 2", len(groups), groups)
	}
}

type failsOnceTool struct {
	*schemaTool
	calls *atomic.Int32
}

func (f failsOnceTool) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	if f.calls.Add(1) == 1 {
		return tool.Error("first attempt fails"), nil
	}
	return tool.OK("ok"), nil
}

func TestRetriedMapKeepsItemGroupsAndCountsTheAttempt(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "each", Type: "map", TemplateNode: "step", ItemsExpression: []any{"x"},
			Retry: &RetryStrategy{Limit: 1, RetryPolicy: "Always", Backoff: &BackoffStrategy{Duration: time.Millisecond, Factor: 1}}},
		{ID: "step", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "{{_map_item}}"}, DependsOn: []string{"each"}},
	}
	dag, _ := BuildDependencyGraph(nodes)
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, FailFast: true, OutputMapping: map[string]any{"done": "yes"}}
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, failsOnceTool{&schemaTool{name: "lookup"}, &atomic.Int32{}})

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatal(err)
	}
	signals := waitForToolSignals(t, brk, 4)
	var results []toolSignal
	for _, s := range signals {
		if s.Type == string(a2a.SignalToolResult) {
			results = append(results, s)
		}
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want one per attempt", len(results))
	}
	if results[0].MessageMeta["retry_group_id"] != results[1].MessageMeta["retry_group_id"] {
		t.Errorf("the retried item changed group: %v then %v", results[0].MessageMeta["retry_group_id"], results[1].MessageMeta["retry_group_id"])
	}
	if results[0].MessageMeta["retry_attempt"] != float64(0) || results[1].MessageMeta["retry_attempt"] != float64(1) {
		t.Errorf("retry_attempt = %v then %v, want 0 then 1", results[0].MessageMeta["retry_attempt"], results[1].MessageMeta["retry_attempt"])
	}
}

func TestProgressForwardingKeepsItsMetadataOffTheMessage(t *testing.T) {
	cfg, dag := singleToolNodeWorkflow(askTelemetryNode())
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	if err := e.toolStatusForwarder("ask_node", "sub-1")("working on it"); err != nil {
		t.Fatal(err)
	}
	for _, m := range brk.snapshot() {
		var envelope struct {
			Result struct {
				Metadata map[string]any `json:"metadata"`
				Status   struct {
					Message struct {
						Metadata map[string]any `json:"metadata"`
					} `json:"message"`
				} `json:"status"`
			} `json:"result"`
		}
		if err := json.Unmarshal(m.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Result.Metadata["type"] != "agent_progress_update" {
			t.Errorf("event metadata = %#v, want the progress update type", envelope.Result.Metadata)
		}
		if envelope.Result.Status.Message.Metadata != nil {
			t.Errorf("status.message.metadata = %#v; only tool-call telemetry sets it", envelope.Result.Status.Message.Metadata)
		}
	}
}

func TestOutcomeResultNeverContradictsTheNode(t *testing.T) {
	ok := tool.OK("fine")
	failed := tool.Error("tool said no", tool.WithErrorCode("RATE_LIMITED"))
	pending := &tool.Result{Status: tool.StatusPending}
	nodeErr := errors.New("node failed")
	for _, tc := range []struct {
		name        string
		result      *tool.Result
		dispatchErr error
		saveErr     error
		nodeErr     error
		wantStatus  string
		wantCode    string
	}{
		{"success", ok, nil, nil, nil, tool.StatusSuccess, ""},
		{"tool failure", failed, nil, nil, nodeErr, tool.StatusError, "RATE_LIMITED"},
		{"dispatch deadline", nil, context.DeadlineExceeded, nil, nodeErr, tool.StatusError, "DISPATCH_TIMEOUT"},
		{"artifact not saved", ok, nil, errors.New("disk full at /var/secret"), nil, tool.StatusError, ""},
		{"no result", nil, nil, nil, nodeErr, tool.StatusError, ""},
		{"pending the node rejects", pending, nil, nil, nodeErr, tool.StatusError, taxonomy.ErrorCodePendingNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := outcomeResult(tc.result, tc.dispatchErr, tc.saveErr, tc.nodeErr)
			if got.Status != tc.wantStatus || got.ErrorCode != tc.wantCode {
				t.Errorf("outcome = %s/%q, want %s/%q", got.Status, got.ErrorCode, tc.wantStatus, tc.wantCode)
			}
			if tc.saveErr != nil && got.Message == tc.saveErr.Error() {
				t.Error("the persistence error, which names the store, reached telemetry")
			}
		})
	}
}

func TestPendingResultWithoutHILPublishesAFailure(t *testing.T) {
	cfg, dag := singleToolNodeWorkflow(askTelemetryNode())
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, deferringTool(""))

	runToCompletion(t, e)

	_, result := onePair(t, toolSignals(t, brk))
	payload, _ := result.Data["result_data"].(map[string]any)
	if payload["status"] != tool.StatusError || payload["error_code"] != taxonomy.ErrorCodePendingNotSupported {
		t.Errorf("result_data = %#v, want a failure the node agrees with", payload)
	}
	if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "ask_node" {
		t.Errorf("the node did not fail: %+v", e.state.ErrorState)
	}
}

func TestCallAnsweredOnAnotherPodIsNotClosedAgainWhenTheResidentCopyEnds(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))
	requestID := pausedRequestID(t, e)
	delete(e.instance.executions, e.execCtx.TaskID)
	delete(e.instance.executionsByID, e.state.ExecutionID)

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"choice": "yes"})
	e.instance.wg.Wait()
	if err := e.Advance(t.Context(), &EventWorkflowTimeout{}); err != nil {
		t.Fatal(err)
	}
	e.instance.drainRetirements()

	_, result := onePair(t, toolSignals(t, brk))
	if payload, _ := result.Data["result_data"].(map[string]any); payload["choice"] != "yes" {
		t.Errorf("result_data = %#v, want the answer the other pod published", payload)
	}
}

func TestPauseWhoseRowStaysAnswerableLeavesTheCloseToTheRow(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))
	claimed := false
	brk.publishHook = func(topic string) error {
		if !strings.Contains(topic, "/notify/") {
			return nil
		}
		_, won, err := e.instance.pausedRepo.Claim(t.Context(), "req-deferred", "resumed")
		claimed = err == nil && won
		return errors.New("notification service unavailable")
	}

	runToCompletion(t, e)

	if !claimed {
		t.Fatal("the stand-in answer did not claim the row; the test would not reach the still-open branch")
	}
	signals := toolSignals(t, brk)
	if len(signals) != 1 || signals[0].Type != string(a2a.SignalToolInvocationStart) {
		t.Errorf("published %+v; a row another claimant holds is closed by that claimant, not by the failed pause", signals)
	}
}

func TestToolIterationInputFailurePublishesARefusedPair(t *testing.T) {
	t.Run("map template", func(t *testing.T) {
		nodes := []*NodeConfig{
			{ID: "each", Type: "map", TemplateNode: "step", ItemsExpression: []any{"x"}},
			{ID: "step", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": map[string]any{"coalesce": "not-a-list"}}, DependsOn: []string{"each"}},
		}
		dag, _ := BuildDependencyGraph(nodes)
		cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, FailFast: true, OutputMapping: map[string]any{"done": "yes"}}
		e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
		e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})

		runToCompletion(t, e)

		_, result := onePair(t, toolSignals(t, brk))
		assertToolNodeMetadata(t, result, "step")
		assertDispatchFailurePayload(t, result, tool.ErrorCodeDispatchError)
	})
	t.Run("loop body", func(t *testing.T) {
		nodes := []*NodeConfig{
			{ID: "again", Type: "loop", BodyNode: "step", Condition: "true", MaxIterations: 1},
			{ID: "step", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": map[string]any{"coalesce": "not-a-list"}}, DependsOn: []string{"again"}},
		}
		dag, _ := BuildDependencyGraph(nodes)
		cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, OutputMapping: map[string]any{"done": "yes"}}
		e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
		e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})

		runToCompletion(t, e)

		_, result := onePair(t, toolSignals(t, brk))
		assertToolNodeMetadata(t, result, "step")
		assertDispatchFailurePayload(t, result, tool.ErrorCodeDispatchError)
	})
}

func TestApprovalForANodeAlreadyResolvedPublishesARefusedPair(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, gatedTelemetryNode(), scriptedTool{schemaTool: &schemaTool{name: "gated"}, result: tool.OK("ran")})
	gateTool(e, "gated")
	requestID := pausedRequestID(t, e)
	rewriteParkedState(t, e.instance, "exec-hil", func(s *WorkflowExecutionState) { delete(s.PendingNodes, "gated_node") })

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	e.instance.wg.Wait()

	start, result := onePair(t, toolSignals(t, brk))
	assertToolNodeMetadata(t, start, "gated_node")
	assertHILRefusal(t, result, "HIL_RUN_ENDED")
}

func TestMapItemsWhoseInputWillNotResolveAreEachLaunchedOnce(t *testing.T) {
	for _, concurrency := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("concurrency %d", concurrency), func(t *testing.T) {
			items := []any{"a", "b", "c", "d", "e"}
			nodes := []*NodeConfig{
				{ID: "each", Type: "map", TemplateNode: "step", ItemsExpression: items, ConcurrencyLimit: concurrency},
				{ID: "step", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": map[string]any{"coalesce": "not-a-list"}}, DependsOn: []string{"each"}},
			}
			dag, _ := BuildDependencyGraph(nodes)
			cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, FailFast: false, OutputMapping: map[string]any{"done": "yes"}}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
			e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})

			done := make(chan struct{})
			go func() {
				defer close(done)
				runToCompletion(t, e)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("the run never returned; %d tool signals were published", len(toolSignals(t, brk)))
			}

			results := assertEveryStartClosedOnce(t, toolSignals(t, brk))
			if len(results) != len(items) {
				t.Errorf("refused %d calls, want one per item", len(results))
			}
			launched := map[int]int{}
			for _, branch := range e.state.ActiveBranches["each"] {
				launched[branch.Index]++
			}
			for index := range items {
				if launched[index] != 1 {
					t.Errorf("item %d launched %d times, want once", index, launched[index])
				}
			}
		})
	}
}

func TestMapInitialBatchDoesNotRelaunchAnItemARefillTook(t *testing.T) {
	for _, concurrency := range []int{2, 3} {
		t.Run(fmt.Sprintf("concurrency %d", concurrency), func(t *testing.T) {
			items := []any{"a", "b", "c", "d", "e", "f"}
			nodes := []*NodeConfig{
				{ID: "each", Type: "map", TemplateNode: "ask", ItemsExpression: items, ConcurrencyLimit: concurrency},
				{ID: "ask", Type: "agent", AgentName: "agentA", DependsOn: []string{"each"}},
			}
			dag, _ := BuildDependencyGraph(nodes)
			cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, FailFast: false}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
			requests := 0
			brk.publishHook = func(topic string) error {
				if topic != a2a.AgentRequestTopic("ns", "agentA") {
					return nil
				}
				requests++
				if requests%2 == 0 {
					return errors.New("broker unavailable")
				}
				return nil
			}

			if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
				t.Fatal(err)
			}

			e.mu.Lock()
			defer e.mu.Unlock()
			launched := map[int]int{}
			for _, branch := range e.state.ActiveBranches["each"] {
				launched[branch.Index]++
			}
			for index, n := range launched {
				if n != 1 {
					t.Errorf("item %d launched %d times, want once", index, n)
				}
			}
		})
	}
}

func nodeTimerFor(state *WorkflowExecutionState, subTaskID string) bool {
	for _, timer := range state.Timers {
		if timer.Kind == TimerKindNodeTimeout && timer.SubTaskID == subTaskID {
			return true
		}
	}
	return false
}

func TestMapDropsASupersededCompletionAndItsTimer(t *testing.T) {
	cfg, dag := singleToolNodeWorkflow(askTelemetryNode())
	state := NewExecutionState("wf", "e1")
	state.Metadata["m_results"] = []any{nil, nil}
	state.Metadata["m_done"] = make([]bool, 2)
	state.Metadata["m_completed"] = 0
	state.Metadata["m_total"] = 2
	state.ActiveBranches["m"] = []BranchInfo{{Index: 0, SubTaskID: "current-0", Type: "map"}, {Index: 1, SubTaskID: "current-1", Type: "map"}}
	state.AddTimer(Timer{Kind: TimerKindNodeTimeout, SubTaskID: "stale-0", FiresAt: time.Now().Add(-time.Second), MapNodeID: "m"})
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()

	e.handleMapChildCompletion(NodeCompletion{MapNodeID: "m", ItemIndex: 0, SubTaskID: "stale-0", Output: map[string]any{"v": "stale"}})

	if c := state.Metadata["m_completed"].(int); c != 0 {
		t.Errorf("a superseded completion was counted: _completed = %d", c)
	}
	if results := state.Metadata["m_results"].([]any); results[0] != nil {
		t.Errorf("a superseded completion took the current attempt's slot: %v", results[0])
	}
	if nodeTimerFor(state, "stale-0") {
		t.Error("the superseded completion's timer was left armed")
	}

	e.handleMapChildCompletion(NodeCompletion{MapNodeID: "m", ItemIndex: 0, SubTaskID: "current-0", Output: map[string]any{"v": "fresh"}})
	if c := state.Metadata["m_completed"].(int); c != 1 {
		t.Errorf("the current attempt's completion was not counted: _completed = %d", c)
	}
}

type blocksFirstAttemptTool struct {
	*schemaTool
	calls   *atomic.Int32
	release chan struct{}
}

func (b blocksFirstAttemptTool) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	switch n := b.calls.Add(1); {
	case n == 1:
		return tool.Error("first call fails"), nil
	case n <= 3:
		<-b.release
	}
	return tool.OK("ok"), nil
}

func TestRetriedMapIsNotStalledByTheFailedAttemptsTimers(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "each", Type: "map", TemplateNode: "step", ItemsExpression: []any{"a", "b", "c"}, ConcurrencyLimit: 3,
			Retry: &RetryStrategy{Limit: 1, RetryPolicy: "Always", Backoff: &BackoffStrategy{Duration: 300 * time.Millisecond, Factor: 1}}},
		{ID: "step", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "{{_map_item}}"}, DependsOn: []string{"each"}, Timeout: 100 * time.Millisecond},
	}
	dag, _ := BuildDependencyGraph(nodes)
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, FailFast: true, OutputMapping: map[string]any{"done": "yes"}}
	e, _ := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	release := make(chan struct{})
	defer close(release)
	e.instance.tools = toolSetWith(t, blocksFirstAttemptTool{&schemaTool{name: "lookup"}, &atomic.Int32{}, release})

	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.mu.Lock()
		finalized, outcome := e.state.Finalized, e.state.PendingOutcome
		e.mu.Unlock()
		if finalized {
			if outcome != "success" {
				t.Errorf("the retried map's run ended %q, want success", outcome)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never finalized; the failed attempt's timers kept the retry from running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type failsFirstCallTool struct {
	*schemaTool
	calls *atomic.Int32
}

func (f failsFirstCallTool) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	if f.calls.Add(1) == 1 {
		return tool.Error("first call fails"), nil
	}
	return tool.OK("ok"), nil
}

func TestRetriedMapRelaunchesEveryItem(t *testing.T) {
	for _, concurrency := range []int{0, 1} {
		t.Run(fmt.Sprintf("concurrency limit %d", concurrency), func(t *testing.T) {
			items := []any{"a", "b", "c"}
			nodes := []*NodeConfig{
				{ID: "each", Type: "map", TemplateNode: "step", ItemsExpression: items, ConcurrencyLimit: concurrency,
					Retry: &RetryStrategy{Limit: 1, RetryPolicy: "Always", Backoff: &BackoffStrategy{Duration: time.Millisecond, Factor: 1}}},
				{ID: "step", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "{{_map_item}}"}, DependsOn: []string{"each"}},
			}
			dag, _ := BuildDependencyGraph(nodes)
			cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, FailFast: true, OutputMapping: map[string]any{"done": "yes"}}
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
			e.instance.tools = toolSetWith(t, failsFirstCallTool{&schemaTool{name: "lookup"}, &atomic.Int32{}})

			if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				e.mu.Lock()
				completed := e.state.IsNodeCompleted("each")
				e.mu.Unlock()
				if completed {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the retried map never completed; tool signals: %d", len(toolSignals(t, brk)))
				}
				time.Sleep(5 * time.Millisecond)
			}
			e.instance.wg.Wait()

			retried := 0
			for _, s := range toolSignals(t, brk) {
				if s.Type == string(a2a.SignalToolResult) && s.MessageMeta["retry_attempt"] == float64(1) {
					retried++
				}
			}
			if retried != len(items) {
				t.Errorf("the retry closed %d calls, want one per item", retried)
			}
		})
	}
}

func TestRefusalsShareOnePublishBudgetPerAdvance(t *testing.T) {
	cfg, dag := singleToolNodeWorkflow(askTelemetryNode())
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	var logged bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&logged, nil))
	refused := func(callID string) *toolNodeTelemetry {
		return e.telemetryFor(toolCallIdentity{toolNodeID: "ask_node", toolName: "ask", callID: callID})
	}

	t.Run("a pair whose start went out still gets its result", func(t *testing.T) {
		budget, cancel := context.WithCancel(t.Context())
		published := 0
		brk.guaranteedHook = func(topic string) error {
			if topic == telemetryStatusTopic {
				published++
				if published == 2 {
					cancel()
				}
			}
			return nil
		}
		e.mu.Lock()
		e.refusalBudget, e.cancelRefusalBudget = budget, cancel
		e.refuseInOrder(refused("sub-1"), nil, tool.PreDispatchFailure(errors.New("refused")))
		e.mu.Unlock()
		onePair(t, toolSignals(t, brk))
	})

	t.Run("refusals past a spent budget are dropped and reported once", func(t *testing.T) {
		before := len(toolSignals(t, brk))
		e.mu.Lock()
		e.refuseInOrder(refused("sub-2"), nil, tool.PreDispatchFailure(errors.New("refused")))
		e.refuseInOrder(refused("sub-3"), nil, tool.PreDispatchFailure(errors.New("refused")))
		e.endRefusalBudget()
		e.mu.Unlock()
		if got := len(toolSignals(t, brk)); got != before {
			t.Errorf("published %d signals past the spent budget", got-before)
		}
		if strings.Count(logged.String(), "budget spent") != 1 || !strings.Contains(logged.String(), "dropped=2") {
			t.Errorf("want one summary warning for both dropped refusals:\n%s", logged.String())
		}
	})
}

func TestAdvanceEndsItsRefusalBudget(t *testing.T) {
	node := &NodeConfig{ID: "broken_node", Type: "tool", ToolName: "never_registered", Input: map[string]any{"q": "1"}}
	cfg, dag := singleToolNodeWorkflow(node)
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "other"}})

	runToCompletion(t, e)

	onePair(t, toolSignals(t, brk))
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.refusalBudget != nil || e.refusalsDropped != 0 {
		t.Error("the Advance that refused the call left its budget behind")
	}
}

func failFirstTelemetryPublish(brk *capturingBroker) {
	failed := false
	brk.guaranteedHook = func(topic string) error {
		if topic == telemetryStatusTopic && !failed {
			failed = true
			return errors.New("broker unavailable")
		}
		return nil
	}
}

func TestResultIsNotPublishedForAStartThatDidNotGoOut(t *testing.T) {
	cfg, dag := singleToolNodeWorkflow(&NodeConfig{ID: "lookup_node", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "1"}})
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})
	failFirstTelemetryPublish(brk)

	runToCompletion(t, e)

	if signals := toolSignals(t, brk); len(signals) != 0 {
		t.Errorf("published %+v; a result without its start is half a call", signals)
	}
	if !e.state.IsNodeCompleted("lookup_node") {
		t.Errorf("a failed telemetry start failed the node: %+v", e.state.ErrorState)
	}
}

func TestPausedCallWhoseStartFailedIsSettledWithTheWholePair(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))
	failFirstTelemetryPublish(brk)
	requestID := pausedRequestID(t, e)
	if signals := toolSignals(t, brk); len(signals) != 0 {
		t.Fatalf("precondition: the start should have failed, got %+v", signals)
	}

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"choice": "yes"})
	e.instance.wg.Wait()

	_, result := onePair(t, toolSignals(t, brk))
	assertToolNodeMetadata(t, result, "ask_node")
}

func TestApprovalForAToolNodeThatLeftTheWorkflowPublishesARefusedPair(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, gatedTelemetryNode(), scriptedTool{schemaTool: &schemaTool{name: "gated"}, result: tool.OK("ran")})
	gateTool(e, "gated")
	requestID := pausedRequestID(t, e)
	e.mu.Lock()
	delete(e.dag.Nodes, "gated_node")
	e.mu.Unlock()

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	e.instance.wg.Wait()

	start, result := onePair(t, toolSignals(t, brk))
	assertToolNodeMetadata(t, start, "gated_node")
	assertDispatchFailurePayload(t, result, tool.ErrorCodeDispatchError)
}

func TestRetirementReportsTheCallsItCouldNotClose(t *testing.T) {
	cfg, dag := singleToolNodeWorkflow(askTelemetryNode())
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	var logged bytes.Buffer
	e.instance.logger = slog.New(slog.NewTextHandler(&logged, nil))
	brk.publishHook = func(string) error { return errors.New("broker unavailable") }
	row := pausedRow{
		row:              wfstore.PausedWorkflow{RequestID: "req-1", ExecutionID: "exec-1"},
		routing:          persistedRouting{NodeID: "ask_node", SubTaskID: "sub-1", ToolNodeID: "ask_node", ToolName: "ask", RetryGroupID: "g-1"},
		execContext:      persistedExecContext{UserProps: a2a.UserProperties{A2AStatusTopic: telemetryStatusTopic}, TaskID: "test-task"},
		execContextKnown: true,
	}

	e.instance.closeAbandonedToolCalls(t.Context(), []pausedRow{row})

	if !strings.Contains(logged.String(), "could not close some tool calls") || !strings.Contains(logged.String(), "unclosed=1") {
		t.Errorf("a close that failed was not reported:\n%s", logged.String())
	}
}
