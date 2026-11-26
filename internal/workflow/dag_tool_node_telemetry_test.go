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
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const telemetryStatusTopic = "test/status"

type scriptedTool struct {
	*schemaTool
	result *tool.Result
	err    error
}

func (s scriptedTool) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	return s.result, s.err
}

type toolSignal struct {
	Type        string
	CallID      string
	Data        map[string]any
	MessageMeta map[string]any
	EventMeta   map[string]any
}

func toolSignals(t *testing.T, brk *capturingBroker) []toolSignal {
	t.Helper()
	var out []toolSignal
	for _, m := range brk.snapshot() {
		if m.Topic != telemetryStatusTopic {
			continue
		}
		var envelope struct {
			Result struct {
				Metadata map[string]any `json:"metadata"`
				Status   struct {
					Message struct {
						Metadata map[string]any `json:"metadata"`
						Parts    []struct {
							Data map[string]any `json:"data"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"status"`
			} `json:"result"`
		}
		if err := json.Unmarshal(m.Payload, &envelope); err != nil {
			t.Fatalf("unmarshal status payload: %v\n%s", err, m.Payload)
		}
		for _, part := range envelope.Result.Status.Message.Parts {
			kind, _ := part.Data["type"].(string)
			if kind != string(a2a.SignalToolInvocationStart) && kind != string(a2a.SignalToolResult) {
				continue
			}
			callID, _ := part.Data["function_call_id"].(string)
			out = append(out, toolSignal{
				Type:        kind,
				CallID:      callID,
				Data:        part.Data,
				MessageMeta: envelope.Result.Status.Message.Metadata,
				EventMeta:   envelope.Result.Metadata,
			})
		}
	}
	return out
}

func onePair(t *testing.T, signals []toolSignal) (start, result toolSignal) {
	t.Helper()
	if len(signals) != 2 {
		t.Fatalf("got %d tool signals, want exactly one start and one result: %+v", len(signals), signals)
	}
	start, result = signals[0], signals[1]
	if start.Type != string(a2a.SignalToolInvocationStart) || result.Type != string(a2a.SignalToolResult) {
		t.Fatalf("signal types = %q, %q; want start then result", start.Type, result.Type)
	}
	if start.CallID == "" || start.CallID != result.CallID {
		t.Fatalf("call IDs %q / %q must be equal and non-empty", start.CallID, result.CallID)
	}
	return start, result
}

func assertToolNodeMetadata(t *testing.T, sig toolSignal, nodeID string) {
	t.Helper()
	for name, meta := range map[string]map[string]any{"status.message.metadata": sig.MessageMeta, "result.metadata": sig.EventMeta} {
		if meta["workflow_name"] != "wf" || meta["node_id"] != nodeID || meta["node_type"] != "tool" {
			t.Errorf("%s on %s = %#v, want workflow_name=wf node_id=%s node_type=tool", name, sig.Type, meta, nodeID)
		}
		if group, _ := meta["retry_group_id"].(string); group == "" {
			t.Errorf("%s on %s has no retry_group_id: %#v", name, sig.Type, meta)
		}
		if meta["retry_attempt"] != float64(0) {
			t.Errorf("%s on %s retry_attempt = %#v, want 0", name, sig.Type, meta["retry_attempt"])
		}
	}
}

func singleToolNodeWorkflow(node *NodeConfig) (*WorkflowConfig, *DependencyGraph) {
	nodes := []*NodeConfig{node}
	dag, _ := BuildDependencyGraph(nodes)
	return &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, OutputMapping: map[string]any{"done": "yes"}}, dag
}

func runToCompletion(t *testing.T, e *DAGExecutor) {
	t.Helper()
	if err := e.Advance(e.execCtx.Ctx, &EventStart{TaskID: "test-task", ContextID: "test-ctx"}); err != nil {
		t.Fatalf("Advance(EventStart): %v", err)
	}
	e.instance.wg.Wait()
}

func TestToolNodeSuccessPublishesOnePairedStartAndResult(t *testing.T) {
	node := &NodeConfig{ID: "lookup_node", Type: "tool", ToolName: "lookup",
		Input: map[string]any{"query": "acme", "api_key": "sk-live-should-not-publish"}}
	cfg, dag := singleToolNodeWorkflow(node)
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, scriptedTool{
		schemaTool: &schemaTool{name: "lookup"},
		result:     tool.OK("found", tool.WithData(map[string]any{"hits": 3})),
	})

	runToCompletion(t, e)

	start, result := onePair(t, toolSignals(t, brk))
	assertToolNodeMetadata(t, start, "lookup_node")
	assertToolNodeMetadata(t, result, "lookup_node")
	if start.Data["tool_name"] != "lookup" || result.Data["tool_name"] != "lookup" {
		t.Errorf("tool_name = %v / %v, want lookup", start.Data["tool_name"], result.Data["tool_name"])
	}
	if sub, ok := e.state.CompletedNodes["lookup_node"]; !ok || sub != start.CallID {
		t.Errorf("call ID %q is not the node's sub-task ID %q", start.CallID, sub)
	}
	args, _ := start.Data["tool_args"].(map[string]any)
	if args["query"] != "acme" {
		t.Errorf("tool_args = %#v, want the resolved arguments", args)
	}
	if args["api_key"] == "sk-live-should-not-publish" {
		t.Error("a credential argument reached the status topic unredacted")
	}
	if got, _ := result.Data["result_data"].(map[string]any); got["hits"] != float64(3) {
		t.Errorf("result_data = %#v, want the tool's data", result.Data["result_data"])
	}
}

func TestToolReportedFailurePublishesOnePairedFailure(t *testing.T) {
	node := &NodeConfig{ID: "limited_node", Type: "tool", ToolName: "limited", Input: map[string]any{"q": "1"}}
	cfg, dag := singleToolNodeWorkflow(node)
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, scriptedTool{
		schemaTool: &schemaTool{name: "limited"},
		result:     tool.Error("slow down", tool.WithErrorCode("RATE_LIMITED")),
	})

	runToCompletion(t, e)

	start, result := onePair(t, toolSignals(t, brk))
	assertToolNodeMetadata(t, start, "limited_node")
	payload, _ := result.Data["result_data"].(map[string]any)
	if payload["status"] != tool.StatusError || payload["message"] != "slow down" || payload["error_code"] != "RATE_LIMITED" {
		t.Errorf("result_data = %#v, want status error, the message, and error_code RATE_LIMITED", payload)
	}
	if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "limited_node" {
		t.Errorf("the node did not fail: %+v", e.state.ErrorState)
	}
}

func TestDispatchFailurePublishesOnePairWithTheClosedCode(t *testing.T) {
	registered := toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "other"}})
	erroring := toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "broken"}, err: errors.New("connection reset")})
	deadline := toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "broken"}, err: fmt.Errorf("call: %w", context.DeadlineExceeded)})
	for _, tc := range []struct {
		name     string
		toolName string
		tools    *tool.Set
		code     string
	}{
		{"no tool set", "broken", nil, tool.ErrorCodeDispatchError},
		{"unknown tool", "broken", registered, tool.ErrorCodeDispatchError},
		{"dispatch error", "broken", erroring, "DISPATCH_INTERNAL"},
		{"dispatch deadline reads as the timeout the audit records", "broken", deadline, "DISPATCH_TIMEOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &NodeConfig{ID: "broken_node", Type: "tool", ToolName: tc.toolName, Input: map[string]any{"q": "1"}}
			cfg, dag := singleToolNodeWorkflow(node)
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
			e.instance.tools = tc.tools

			runToCompletion(t, e)

			start, result := onePair(t, toolSignals(t, brk))
			assertToolNodeMetadata(t, start, "broken_node")
			assertToolNodeMetadata(t, result, "broken_node")
			assertDispatchFailurePayload(t, result, tc.code)
			if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "broken_node" {
				t.Errorf("the node did not fail: %+v", e.state.ErrorState)
			}
		})
	}
}

func TestIterationDispatchFailurePublishesOnePair(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "each", Type: "map", TemplateNode: "body", ItemsExpression: []any{"x"}},
		{ID: "body", Type: "tool", ToolName: "never_registered", Input: map[string]any{"q": "1"}, DependsOn: []string{"each"}},
	}
	dag, _ := BuildDependencyGraph(nodes)
	e, brk := newCapturingExecutor(t, &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}, dag, NewExecutionState("wf", "exec-1"))

	runToCompletion(t, e)

	start, result := onePair(t, toolSignals(t, brk))
	if branches := e.state.ActiveBranches["each"]; len(branches) != 1 || start.CallID != branches[0].SubTaskID {
		t.Errorf("call ID = %q, want the iteration's sub-task ID from %+v", start.CallID, branches)
	}
	assertToolNodeMetadata(t, result, "body")
	assertDispatchFailurePayload(t, result, tool.ErrorCodeDispatchError)
}

func TestPanickingToolStillClosesItsPair(t *testing.T) {
	node := &NodeConfig{ID: "boom_node", Type: "tool", ToolName: "boom"}
	cfg, dag := singleToolNodeWorkflow(node)
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, panickingTool{&schemaTool{name: "boom", authored: true}})

	runToCompletion(t, e)

	_, result := onePair(t, toolSignals(t, brk))
	assertDispatchFailurePayload(t, result, "DISPATCH_INTERNAL")
}

func assertDispatchFailurePayload(t *testing.T, result toolSignal, code string) {
	t.Helper()
	payload, _ := result.Data["result_data"].(map[string]any)
	if payload["status"] != tool.StatusError || payload["error_code"] != code {
		t.Errorf("result_data = %#v, want status error and error_code %s", payload, code)
	}
	if msg, _ := payload["message"].(string); msg == "" {
		t.Errorf("result_data has no message: %#v", payload)
	}
}

func TestNoStatusTopicPublishesNoToolSignals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools func(*testing.T) *tool.Set
	}{
		{"success", func(t *testing.T) *tool.Set {
			return toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})
		}},
		{"dispatch failure", func(*testing.T) *tool.Set { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &NodeConfig{ID: "lookup_node", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "1"}}
			cfg, dag := singleToolNodeWorkflow(node)
			e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
			e.execCtx.UserProps.A2AStatusTopic = ""
			e.instance.tools = tc.tools(t)

			runToCompletion(t, e)

			for _, m := range brk.snapshot() {
				if strings.Contains(string(m.Payload), `"function_call_id"`) {
					t.Errorf("a tool signal was published with no status topic, on %q: %s", m.Topic, m.Payload)
				}
			}
		})
	}
}

func TestTwoToolNodesKeepDistinctNodeAndCallIDs(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "first", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "1"}},
		{ID: "second", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "2"}},
	}
	dag, _ := BuildDependencyGraph(nodes)
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes, OutputMapping: map[string]any{"done": "yes"}}
	e, brk := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
	e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})

	runToCompletion(t, e)

	byNode := map[string][]toolSignal{}
	for _, sig := range toolSignals(t, brk) {
		nodeID, _ := sig.MessageMeta["node_id"].(string)
		byNode[nodeID] = append(byNode[nodeID], sig)
	}
	if len(byNode) != 2 {
		t.Fatalf("signals grouped by node_id = %v, want exactly first and second", byNode)
	}
	firstStart, _ := onePair(t, byNode["first"])
	secondStart, _ := onePair(t, byNode["second"])
	if firstStart.CallID == secondStart.CallID {
		t.Errorf("both tool nodes used call ID %q", firstStart.CallID)
	}
	if firstStart.MessageMeta["retry_group_id"] == secondStart.MessageMeta["retry_group_id"] {
		t.Errorf("two logical node executions share retry group %v", firstStart.MessageMeta["retry_group_id"])
	}
}

type statusFailingBroker struct{ stubBroker }

func (statusFailingBroker) Publish(_ context.Context, topic string, _ *broker.Message) error {
	if topic == telemetryStatusTopic {
		return errors.New("broker unavailable")
	}
	return nil
}

func (b statusFailingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	return b.Publish(ctx, topic, msg)
}

func TestTelemetryPublishFailureDoesNotFailTheNode(t *testing.T) {
	node := &NodeConfig{ID: "lookup_node", Type: "tool", ToolName: "lookup", Input: map[string]any{"q": "1"}}
	cfg, dag := singleToolNodeWorkflow(node)
	state := NewExecutionState("wf", "exec-1")
	e := newTestDAGExecutor(cfg, dag, state)
	defer e.execCtx.Cancel()
	var logged bytes.Buffer
	e.logger = slog.New(slog.NewTextHandler(&logged, nil))
	e.instance.svc = &stubServices{brk: statusFailingBroker{}}
	e.execCtx.UserProps.A2AStatusTopic = telemetryStatusTopic
	e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "lookup"}, result: tool.OK("ok")})

	runToCompletion(t, e)

	if !state.IsNodeCompleted("lookup_node") {
		t.Errorf("a telemetry publish failure failed the node: %+v", state.ErrorState)
	}
	if !strings.Contains(logged.String(), "tool node telemetry publish failed") {
		t.Errorf("the dropped telemetry was not logged:\n%s", logged.String())
	}
}

func newHILTelemetryExecutor(t *testing.T, node *NodeConfig, registered tool.Tool) (*DAGExecutor, *capturingBroker) {
	t.Helper()
	cfg, dag := singleToolNodeWorkflow(node)
	state := NewExecutionState("wf", "exec-hil")
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

func gateTool(e *DAGExecutor, toolName string) {
	e.instance.hilToolConfigs = map[string]tool.HILToolConfig{toolName: {RequireApproval: true}}
	e.instance.tools.SetHILConfigs(e.instance.hilToolConfigs)
}

func gatedTelemetryNode() *NodeConfig {
	return &NodeConfig{ID: "gated_node", Type: "tool", ToolName: "gated", Input: map[string]any{"q": "1"}}
}

func askTelemetryNode() *NodeConfig {
	return &NodeConfig{ID: "ask_node", Type: "tool", ToolName: "ask", Input: map[string]any{"q": "1"}}
}

func deferringTool(expiresAt string) scriptedTool {
	return scriptedTool{
		schemaTool: &schemaTool{name: "ask"},
		result: &tool.Result{Status: tool.StatusPending, Data: map[string]any{
			"hil_request_id": "req-deferred",
			"surface":        map[string]any{"surfaceId": "s"},
			"expires_at":     expiresAt,
		}},
	}
}

func pausedRequestID(t *testing.T, e *DAGExecutor) string {
	t.Helper()
	runToCompletion(t, e)
	rows, err := e.instance.pausedRepo.Expired(t.Context(), e.instance.cfg.Name, time.Now().Add(24*time.Hour).UnixMilli(), 10)
	if err != nil {
		t.Fatalf("list paused: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 paused row, got %d: the call did not take the pause branch", len(rows))
	}
	return rows[0].RequestID
}

func assertHILRefusal(t *testing.T, result toolSignal, code string) {
	t.Helper()
	payload, _ := result.Data["result_data"].(map[string]any)
	if payload["status"] != tool.StatusError || payload["error_code"] != code {
		t.Errorf("result_data = %#v, want status error and error_code %s", payload, code)
	}
}

func TestHILApprovalThenApprovePublishesOnePair(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, gatedTelemetryNode(), scriptedTool{
		schemaTool: &schemaTool{name: "gated"},
		result:     tool.OK("done", tool.WithData(map[string]any{"rows": 2})),
	})
	gateTool(e, "gated")

	requestID := pausedRequestID(t, e)
	if signals := toolSignals(t, brk); len(signals) != 0 {
		t.Fatalf("a call awaiting approval published tool signals: %+v", signals)
	}

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	e.instance.wg.Wait()

	start, result := onePair(t, toolSignals(t, brk))
	assertToolNodeMetadata(t, start, "gated_node")
	assertToolNodeMetadata(t, result, "gated_node")
	if got, _ := result.Data["result_data"].(map[string]any); got["rows"] != float64(2) {
		t.Errorf("result_data = %#v, want the approved call's data", result.Data["result_data"])
	}
	var final WorkflowExecutionState
	if err := json.Unmarshal([]byte(runRowOf(t, e.instance, "exec-hil").StateSnapshot), &final); err == nil && final.ErrorState != nil {
		t.Errorf("the approved node failed: %+v", final.ErrorState)
	}
	if run := runRowOf(t, e.instance, "exec-hil"); run.Status != "ended" {
		t.Errorf("run status = %q, want ended once the approved node completed", run.Status)
	}
}

func TestHILDeferredThenAnsweredPublishesOnePair(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))

	requestID := pausedRequestID(t, e)
	paused := toolSignals(t, brk)
	if len(paused) != 1 || paused[0].Type != string(a2a.SignalToolInvocationStart) {
		t.Fatalf("paused call published %+v, want its start alone", paused)
	}

	e.instance.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"choice": "yes"})
	e.instance.wg.Wait()

	start, result := onePair(t, toolSignals(t, brk))
	if start.CallID != paused[0].CallID {
		t.Errorf("result closed call %q, want the paused call %q", result.CallID, paused[0].CallID)
	}
	assertToolNodeMetadata(t, result, "ask_node")
	payload, _ := result.Data["result_data"].(map[string]any)
	if payload["choice"] != "yes" || payload["status"] != "answered" {
		t.Errorf("result_data = %#v, want the answer", payload)
	}
	if _, pending := payload["hil_request_id"]; pending {
		t.Errorf("the pending result was published as the outcome: %#v", payload)
	}
}

func TestHILDeniedOrExpiredPublishesOneFailedPair(t *testing.T) {
	gated := scriptedTool{schemaTool: &schemaTool{name: "gated"}, result: tool.OK("ran")}
	answer := func(nodeStatus string, data map[string]any) func(*testing.T, *DAGExecutor, string) {
		return func(t *testing.T, e *DAGExecutor, requestID string) {
			e.instance.answer(t.Context(), requestID, "resumed", nodeStatus, data)
		}
	}
	timeOut := func(t *testing.T, e *DAGExecutor, requestID string) {
		e.instance.answer(t.Context(), requestID, "timed_out", "timed_out", nil)
	}
	expired := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)

	for _, tc := range []struct {
		name   string
		node   *NodeConfig
		tool   tool.Tool
		gated  bool
		settle func(*testing.T, *DAGExecutor, string)
		code   string
	}{
		{"approval denied", gatedTelemetryNode(), gated, true, answer("answered", map[string]any{"approved": false}), "HIL_DENIED"},
		{"approval timed out", gatedTelemetryNode(), gated, true, timeOut, "HIL_TIMED_OUT"},
		{"deferred denied", askTelemetryNode(), deferringTool(""), false, answer("answered", map[string]any{"approved": false}), "HIL_DENIED"},
		{"deferred expired", askTelemetryNode(), deferringTool(expired), false, timeOut, "HIL_TIMED_OUT"},
		{"deferred cancelled", askTelemetryNode(), deferringTool(""), false, answer("cancelled", nil), "HIL_CANCELLED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, brk := newHILTelemetryExecutor(t, tc.node, tc.tool)
			if tc.gated {
				gateTool(e, tc.node.ToolName)
			}

			requestID := pausedRequestID(t, e)
			tc.settle(t, e, requestID)
			e.instance.wg.Wait()

			start, result := onePair(t, toolSignals(t, brk))
			assertToolNodeMetadata(t, start, tc.node.ID)
			assertToolNodeMetadata(t, result, tc.node.ID)
			assertHILRefusal(t, result, tc.code)
		})
	}
}

func TestHILDeferredPauseFailureClosesItsStart(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))
	e.execCtx.UserProps.UserID = ""

	runToCompletion(t, e)

	_, result := onePair(t, toolSignals(t, brk))
	assertHILRefusal(t, result, "HIL_PROMPT_FAILED")
	if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "ask_node" {
		t.Errorf("the node did not fail: %+v", e.state.ErrorState)
	}
}
