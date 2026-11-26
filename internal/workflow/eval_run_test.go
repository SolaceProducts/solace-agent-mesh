// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func requestMetadata(t *testing.T, brk *capturingBroker, topic string) map[string]any {
	t.Helper()
	for _, m := range brk.snapshot() {
		if m.Topic != topic {
			continue
		}
		var req struct {
			Params struct {
				Message struct {
					Metadata map[string]any `json:"metadata"`
				} `json:"message"`
			} `json:"params"`
		}
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			t.Fatalf("decode request on %s: %v", topic, err)
		}
		return req.Params.Message.Metadata
	}
	t.Fatalf("no request published to %s; saw %v", topic, capturedTopics(brk))
	return nil
}

func TestNodeRequestsCarryTheCallersModelOverride(t *testing.T) {
	cases := []struct {
		name   string
		node   *NodeConfig
		target string
	}{
		{"agent node", &NodeConfig{ID: "step", Type: "agent", AgentName: "Researcher"}, "Researcher"},
		{"sub-workflow node", &NodeConfig{ID: "step", Type: "workflow", WorkflowName: "nested_wf"}, "nested_wf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []*NodeConfig{tc.node}
			cfg := &WorkflowConfig{Name: "outer", Namespace: "test-ns", Nodes: nodes}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, brk := newCapturingExecutorWithAuthz(t, cfg, dag, NewExecutionState("outer", "exec-1"), &recordingAuthorizer{allow: true})
			seedCard(e.instance, a2a.AgentCard{Name: tc.target})
			e.execCtx.ModelOverrideAlias = "fast-model"

			e.dispatchAgentNode(context.Background(), tc.node, "subtask-1")

			metadata := requestMetadata(t, brk, a2a.AgentRequestTopic(cfg.Namespace, tc.target))
			override, _ := metadata[a2a.MetadataModelOverride].(map[string]any)
			if override["model_id"] != "fast-model" {
				t.Fatalf("model_override = %v, want the caller's alias forwarded unresolved", metadata[a2a.MetadataModelOverride])
			}
		})
	}
}

func TestNodeRequestsCarryNoModelOverrideWhenTheCallerSentNone(t *testing.T) {
	nodes := []*NodeConfig{{ID: "step", Type: "agent", AgentName: "Researcher"}}
	cfg := &WorkflowConfig{Name: "outer", Namespace: "test-ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	e, brk := newCapturingExecutorWithAuthz(t, cfg, dag, NewExecutionState("outer", "exec-1"), &recordingAuthorizer{allow: true})
	seedCard(e.instance, a2a.AgentCard{Name: "Researcher"})

	e.dispatchAgentNode(context.Background(), nodes[0], "subtask-1")

	metadata := requestMetadata(t, brk, a2a.AgentRequestTopic(cfg.Namespace, "Researcher"))
	if _, present := metadata[a2a.MetadataModelOverride]; present {
		t.Fatalf("model_override = %v, want none when the caller asked for none", metadata[a2a.MetadataModelOverride])
	}
}

func TestModelOverrideAliasReadsTheRequestMetadata(t *testing.T) {
	cases := []struct {
		name        string
		metadata    map[string]any
		want        string
		wantPresent bool
		wantUsable  bool
	}{
		{"alias", map[string]any{"model_override": map[string]any{"model_id": "fast"}}, "fast", true, true},
		{"absent", map[string]any{}, "", false, false},
		{"nil metadata", nil, "", false, false},
		{"null value asks for nothing", map[string]any{"model_override": nil}, "", false, false},
		{"not a map", map[string]any{"model_override": "fast"}, "", true, false},
		{"no model_id", map[string]any{"model_override": map[string]any{}}, "", true, false},
		{"model_id is not a string", map[string]any{"model_override": map[string]any{"model_id": 7}}, "", true, false},
		{"empty model_id", map[string]any{"model_override": map[string]any{"model_id": ""}}, "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, present, usable := modelOverrideAlias(tc.metadata)
			if got != tc.want || present != tc.wantPresent || usable != tc.wantUsable {
				t.Errorf("modelOverrideAlias = (%q, present %v, usable %v), want (%q, %v, %v)",
					got, present, usable, tc.want, tc.wantPresent, tc.wantUsable)
			}
		})
	}
}

func TestPausedExecutionResumesWithItsModelOverride(t *testing.T) {
	w, _ := newHILInstance(t)
	w.dag = &DependencyGraph{}
	state := NewExecutionState("wf", "exec-1")
	state.AddPending("approve")
	execCtx := NewExecutionContext(w.ctx, "task-1", "ctx-1", nil, a2a.UserProperties{UserID: "alice@example.com"}, false)
	execCtx.ModelOverrideAlias = "fast-model"
	e := &DAGExecutor{cfg: w.cfg, instance: w, state: state, execCtx: execCtx, logger: discardLog()}
	if err := w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-1", RequestID: "req-1",
		Surface: json.RawMessage(`{"surfaceId":"s"}`), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("pauseWorkflowNode: %v", err)
	}
	exec := parkAndRebuild(t, w, e, nil)

	if exec.Context.ModelOverrideAlias != "fast-model" {
		t.Errorf("resumed model override = %q, want fast-model", exec.Context.ModelOverrideAlias)
	}
}

func finalResponseMetadata(t *testing.T, brk *capturingBroker, replyTo string) map[string]any {
	t.Helper()
	for _, msg := range brk.snapshot() {
		if msg.Topic != replyTo {
			continue
		}
		var envelope struct {
			Result struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"result"`
		}
		if err := json.Unmarshal(msg.Payload, &envelope); err != nil {
			t.Fatalf("decode final response: %v", err)
		}
		return envelope.Result.Metadata
	}
	t.Fatalf("no final response on %s; saw %v", replyTo, capturedTopics(brk))
	return nil
}

func TestCancelledRunNamesWhatEndedIt(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
		want   string
	}{
		{"workflow deadline", []Event{&EventWorkflowTimeout{}}, a2a.TerminationWorkflowTimeout},
		{"external cancel", []Event{&EventCancel{}}, a2a.TerminationCancelled},
		{"cancel after the deadline keeps the deadline", []Event{&EventWorkflowTimeout{}, &EventCancel{}}, a2a.TerminationWorkflowTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, brk := newPublishFinalTestExecutor(t)
			e.instance.executions = map[string]*WorkflowExecution{}
			e.instance.subTaskIndex = map[string]string{}
			for _, evt := range tc.events {
				e.applyEvent(t.Context(), evt)
			}

			e.publishFinalCancelled()

			metadata := finalResponseMetadata(t, brk, e.execCtx.UserProps.ReplyTo)
			if got := metadata[a2a.MetadataTerminationReason]; got != tc.want {
				t.Errorf("termination_reason = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestFailedRunNamesTheNodeAndWhy(t *testing.T) {
	cases := []struct {
		name              string
		nodeErr           error
		wantReason        string
		wantFailureReason string
	}{
		{"node error", errors.New("unsupported currency"), a2a.TerminationNodeFailed, "error"},
		{"node asked for a human on an eval run", errNoHumanInEvalRun, a2a.TerminationPausedForInput, a2a.FailureReasonPausedForInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []*NodeConfig{{ID: "convert", Type: "agent", AgentName: "fx"}}
			cfg := &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: nodes, FailFast: true}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatal(err)
			}
			e, brk := newCapturingExecutorWithAuthz(t, cfg, dag, NewExecutionState("wf", "exec-1"), &recordingAuthorizer{allow: true})
			e.execCtx.UserProps.ReplyTo = "test/reply"
			e.state.AddPending("convert")

			e.handleNodeErrorKind("convert", tc.nodeErr, ErrorKindFailure)

			if failure := e.state.ErrorState; failure == nil || failure.FailureReason != tc.wantFailureReason {
				t.Errorf("ErrorState = %+v, want failure reason %q", failure, tc.wantFailureReason)
			}
			metadata := finalResponseMetadata(t, brk, "test/reply")
			if got := metadata[a2a.MetadataTerminationReason]; got != tc.wantReason {
				t.Errorf("termination_reason = %v, want %q", got, tc.wantReason)
			}
			if got := metadata[a2a.MetadataFailedNodeID]; got != "convert" {
				t.Errorf("failed_node_id = %v, want convert", got)
			}
		})
	}
}

func TestRejectedInputIsNamedOnTheFailure(t *testing.T) {
	e, brk := newPublishFinalTestExecutor(t)

	e.rejectInvalidInput([]string{"order_id is required"})

	metadata := finalResponseMetadata(t, brk, e.execCtx.UserProps.ReplyTo)
	if got := metadata[a2a.MetadataTerminationReason]; got != a2a.TerminationInvalidInput {
		t.Errorf("termination_reason = %v, want %q", got, a2a.TerminationInvalidInput)
	}
	if _, present := metadata[a2a.MetadataFailedNodeID]; present {
		t.Errorf("failed_node_id = %v, want none: no node ran", metadata[a2a.MetadataFailedNodeID])
	}
}

func TestEvalRunRefusesAGatedToolInsteadOfWaitingForApproval(t *testing.T) {
	e, rec := newApprovalExecutor(t, requireApproval())
	e.execCtx.UserProps.Trigger = a2a.TriggerEval

	e.invokeToolNode(gatedToolNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	if rec.executed.Load() {
		t.Fatal("a tool requiring approval ran on an eval run with nobody to approve it")
	}
	failure := e.state.ErrorState
	if failure == nil {
		t.Fatal("the gated node neither ran nor failed — the trial would hang")
	}
	if failure.FailureReason != a2a.FailureReasonPausedForInput {
		t.Errorf("FailureReason = %q, want %q", failure.FailureReason, a2a.FailureReasonPausedForInput)
	}
}

func TestEvalRunPublishesAndAuditsTheRefusalOfAGatedTool(t *testing.T) {
	e, brk := newHILTelemetryExecutor(t, gatedTelemetryNode(), scriptedTool{
		schemaTool: &schemaTool{name: "gated"},
		result:     tool.OK("done"),
	})
	gateTool(e, "gated")
	audit := &approvalAudit{}
	installAudit(t, e, audit, true)
	e.execCtx.UserProps.Trigger = a2a.TriggerEval
	e.execCtx.UserProps.ReplyTo = "test/reply"
	e.cfg.FailFast = true

	e.invokeToolNode(gatedTelemetryNode(), map[string]any{"q": "1"},
		NodeCompletion{NodeID: "gated_node", SubTaskID: "sub-1"})
	e.instance.wg.Wait()

	_, result := onePair(t, toolSignals(t, brk))
	assertHILRefusal(t, result, tool.HILRefusalErrorCode(tool.RefusalNoHumanInEvalRun))
	if got := audit.only(t).ApprovalRefusal; got != string(tool.RefusalNoHumanInEvalRun) {
		t.Errorf("audited refusal = %q, want %q", got, tool.RefusalNoHumanInEvalRun)
	}

	lastSignal, reply := -1, -1
	for i, m := range brk.snapshot() {
		switch m.Topic {
		case telemetryStatusTopic:
			lastSignal = i
		case "test/reply":
			if reply < 0 {
				reply = i
			}
		}
	}
	if reply < 0 {
		t.Fatal("the run published no final reply")
	}
	if lastSignal > reply {
		t.Error("the refused call's result was published after the run's final reply")
	}
}

func TestEvalPauseRefusalIsNotRetried(t *testing.T) {
	for _, policy := range []string{"Always", "OnError"} {
		t.Run(policy, func(t *testing.T) {
			nodes := []*NodeConfig{{ID: "each", Type: "agent", AgentName: "agentA"}}
			cfg := &WorkflowConfig{
				Nodes: nodes,
				RetryStrategy: &RetryStrategy{
					Limit:       3,
					RetryPolicy: policy,
					Backoff:     &BackoffStrategy{Duration: time.Millisecond, Factor: 1},
				},
			}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatal(err)
			}
			e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "e1"))
			t.Cleanup(e.execCtx.Cancel)

			e.state.AddPending("each")
			if e.handleNodeErrorKind("each", fmt.Errorf("item 0: %w", errNoHumanInEvalRun), ErrorKindError) {
				t.Fatal("an eval pause refusal was scheduled for retry")
			}
			if e.state.ErrorState == nil || e.state.ErrorState.FailureReason != a2a.FailureReasonPausedForInput {
				t.Errorf("ErrorState = %+v, want the node failed as paused for input", e.state.ErrorState)
			}

			e.state.ErrorState = nil
			e.state.AddPending("each")
			if !e.handleNodeErrorKind("each", errors.New("transient"), ErrorKindError) {
				t.Error("precondition: an ordinary error under this policy should be retried")
			}
		})
	}
}

func TestEvalRunFailsANodeThatPausesForInput(t *testing.T) {
	w, _ := newHILInstance(t)
	node := &NodeConfig{ID: "ask", Type: "tool", ToolName: "ask"}
	dag, err := BuildDependencyGraph([]*NodeConfig{node})
	if err != nil {
		t.Fatal(err)
	}
	w.cfg = &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: []*NodeConfig{node}, FailFast: true}
	w.dag = dag
	state := NewExecutionState("wf", "exec-eval")
	state.AddPending("ask")
	state.TrackSubTask("st-1", "ask")
	execCtx := NewExecutionContext(w.ctx, "task-1", "ctx-1", nil, a2a.UserProperties{UserID: "system:eval", Trigger: a2a.TriggerEval}, false)
	e := &DAGExecutor{cfg: w.cfg, dag: w.dag, instance: w, state: state, execCtx: execCtx, logger: discardLog()}
	pause := &EventNodePaused{
		NodeID: "ask", SubTaskID: "st-1", RequestID: "req-eval",
		Surface: json.RawMessage(`{}`), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}

	if err := e.Advance(t.Context(), pause); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if row, _ := w.pausedRepo.Get(t.Context(), "req-eval"); row != nil {
		t.Fatal("an eval run parked a pause nobody will answer")
	}
	if pause.parked {
		t.Error("pause reported parked on an eval run")
	}
	if state.ErrorState == nil || state.ErrorState.FailureReason != a2a.FailureReasonPausedForInput {
		t.Fatalf("ErrorState = %+v, want the node failed as paused_for_input", state.ErrorState)
	}
	if !state.Finalized {
		t.Error("a fail-fast run whose only node failed was not finalized")
	}
}

func TestNodeResultsCarryPreviewsOnlyOnEvalRuns(t *testing.T) {
	for _, trigger := range []string{a2a.TriggerEval, a2a.TriggerInteractive} {
		t.Run(trigger, func(t *testing.T) {
			e, brk := newPublishFinalTestExecutor(t)
			e.execCtx.Ctx = t.Context()
			e.execCtx.UserProps.A2AStatusTopic = "test/status"
			e.execCtx.UserProps.Trigger = trigger
			e.recordInputPreview("lookup", map[string]any{"order_id": "O-5003"})
			e.state.NodeOutputs["lookup"] = map[string]any{"output": map[string]any{"status": "delayed"}}

			e.publishNodeResultMeta("lookup", "success", nil, map[string]any{"skip_reason": "kept"})

			metadata := nodeResultMetadata(t, brk, "test/status", "lookup")
			if metadata["skip_reason"] != "kept" {
				t.Errorf("caller metadata dropped: %v", metadata)
			}
			input, hasInput := metadata[a2a.MetadataInputPreview].(string)
			output, hasOutput := metadata[a2a.MetadataOutputPreview].(string)
			if trigger != a2a.TriggerEval {
				if hasInput || hasOutput {
					t.Fatalf("a %s run published previews: %v", trigger, metadata)
				}
				return
			}
			if !strings.Contains(input, "O-5003") || !strings.Contains(output, "delayed") {
				t.Errorf("previews = input %q output %q, want the node's input and output", input, output)
			}
		})
	}
}

func TestPreviewIsCapped(t *testing.T) {
	preview := previewOf(map[string]any{"blob": strings.Repeat("x", nodePreviewMaxRunes*2)})

	if len([]rune(preview)) > nodePreviewMaxRunes+len("... (truncated)") {
		t.Fatalf("preview is %d runes, want at most %d plus the marker", len([]rune(preview)), nodePreviewMaxRunes)
	}
	if !strings.HasSuffix(preview, "(truncated)") {
		t.Errorf("a cut preview does not say so: ...%s", preview[len(preview)-20:])
	}
}

func TestPreviewsRedactWhatToolTelemetryRedacts(t *testing.T) {
	e, brk := newPublishFinalTestExecutor(t)
	e.execCtx.Ctx = t.Context()
	e.execCtx.UserProps.A2AStatusTopic = "test/status"
	e.execCtx.UserProps.Trigger = a2a.TriggerEval
	const argSecret, resultSecret = "sk-live-0123456789abcdef", "tok-0123456789abcdef"
	e.recordInputPreview("call", map[string]any{"order_id": "O-5003", "api_key": argSecret})
	e.state.NodeOutputs["call"] = map[string]any{"output": map[string]any{"status": "delayed", "access_token": resultSecret}}

	e.publishNodeResultMeta("call", "success", nil, nil)

	metadata := nodeResultMetadata(t, brk, "test/status", "call")
	input, output := metadata[a2a.MetadataInputPreview].(string), metadata[a2a.MetadataOutputPreview].(string)
	if strings.Contains(input, argSecret) || strings.Contains(output, resultSecret) {
		t.Fatalf("a credential reached a preview: input %q output %q", input, output)
	}
	if !strings.Contains(input, "O-5003") || !strings.Contains(output, "delayed") {
		t.Errorf("redaction removed more than the credentials: input %q output %q", input, output)
	}
	if !strings.Contains(input, "<REDACTED>") || !strings.Contains(output, "<REDACTED>") {
		t.Errorf("the redaction marker is missing or escaped: input %q output %q", input, output)
	}
}

func TestPreviewIsTheCompactJSONOfItsValue(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"html characters are left as written", map[string]any{"a": "<&>"}, `{"a":"<&>"}`},
		{"no trailing newline", map[string]any{"a": 1}, `{"a":1}`},
		{"nil", nil, `null`},
		{"string", "plain", `"plain"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := previewOf(tc.value); got != tc.want {
				t.Errorf("previewOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPreviewOfAValueThatCannotBeEncodedSaysSo(t *testing.T) {
	if got := previewOf(make(chan int)); got != "<unserializable: chan int>" {
		t.Errorf("previewOf(chan) = %q, want the fixed marker", got)
	}
}

func TestRunPastItsDeadlineIsNamedByWhicheverEventNoticesFirst(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(e *DAGExecutor)
		want    string
	}{
		{"deadline noticed before the timeout event", func(*DAGExecutor) {}, a2a.TerminationWorkflowTimeout},
		{"the caller's context ended too", func(e *DAGExecutor) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			e.execCtx.Ctx = ctx
		}, a2a.TerminationCancelled},
		{"a cancel arrived first", func(e *DAGExecutor) { e.applyEvent(t.Context(), &EventCancel{}) }, a2a.TerminationCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, brk := newPublishFinalTestExecutor(t)
			e.instance.executions = map[string]*WorkflowExecution{}
			e.instance.subTaskIndex = map[string]string{}
			e.state.WorkflowDeadline = time.Now().Add(-time.Second)
			tc.arrange(e)

			e.progress(t.Context())

			metadata := finalResponseMetadata(t, brk, e.execCtx.UserProps.ReplyTo)
			if got := metadata[a2a.MetadataTerminationReason]; got != tc.want {
				t.Errorf("termination_reason = %v, want %q", got, tc.want)
			}
		})
	}
}

const evalRunStatusTopic = "test/eval-status"

func TestEvalRunClosesARefusedPausesToolCallBeforeTheRunEnds(t *testing.T) {
	cases := []struct {
		name             string
		kind             string
		startUnpublished bool
		wantStarts       int
		wantResults      int
		wantClosed       bool
	}{
		{"start already published", "", false, 0, 1, true},
		{"start never published leaves the call to its goroutine", "", true, 0, 0, false},
		{"approval pause, start never published", PauseKindApproval, false, 1, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &NodeConfig{ID: "ask", Type: "tool", ToolName: "ask"}
			e, brk := newUsageFactsExecutorFailingFast(t, []*NodeConfig{node}, true)
			e.execCtx.UserProps.Trigger = a2a.TriggerEval
			e.execCtx.UserProps.A2AStatusTopic = evalRunStatusTopic
			e.state.AddPending("ask")
			e.state.TrackSubTask("st-1", "ask")
			pause := &EventNodePaused{
				NodeID: "ask", SubTaskID: "st-1", RequestID: "req-1", ToolNodeID: "ask", ToolName: "ask",
				Kind: tc.kind, startUnpublished: tc.startUnpublished,
				PendingArgs: map[string]any{"url": "https://example.test"},
			}

			if err := e.Advance(t.Context(), pause); err != nil {
				t.Fatalf("Advance: %v", err)
			}

			if pause.closed != tc.wantClosed {
				t.Errorf("closed = %v, want %v", pause.closed, tc.wantClosed)
			}
			starts := toolEvents(t, brk, a2a.SignalToolInvocationStart, "st-1")
			results := toolEvents(t, brk, a2a.SignalToolResult, "st-1")
			if len(starts) != tc.wantStarts || len(results) != tc.wantResults {
				t.Fatalf("call published %d starts and %d results, want %d and %d", len(starts), len(results), tc.wantStarts, tc.wantResults)
			}
			if len(results) == 0 {
				return
			}
			if terminal := firstPublishedTo(brk, usageFactsReplyTopic); terminal < 0 || results[0].index > terminal {
				t.Errorf("tool_result at %d, final response at %d: the refusal must come before the run ends", results[0].index, terminal)
			}
			if encoded, _ := json.Marshal(results[0].data); !strings.Contains(string(encoded), string(tool.RefusalNoHumanInEvalRun)) {
				t.Errorf("the closing result does not name its refusal: %s", encoded)
			}
			if len(starts) == 1 {
				if starts[0].index > results[0].index {
					t.Errorf("start at %d after its result at %d", starts[0].index, results[0].index)
				}
				if args, _ := starts[0].data["tool_args"].(map[string]any); args["url"] != "https://example.test" {
					t.Errorf("start carries tool_args %v, want the call's own arguments", starts[0].data["tool_args"])
				}
			}
		})
	}
}

func TestARefusalDroppedForItsBudgetLeavesTheCallToItsDispatcher(t *testing.T) {
	node := &NodeConfig{ID: "ask", Type: "tool", ToolName: "ask"}
	e, brk := newUsageFactsExecutorFailingFast(t, []*NodeConfig{node}, true)
	e.execCtx.UserProps.Trigger = a2a.TriggerEval
	e.execCtx.UserProps.A2AStatusTopic = evalRunStatusTopic
	e.state.AddPending("ask")
	e.state.TrackSubTask("st-1", "ask")
	spent, cancel := context.WithCancel(t.Context())
	cancel()
	e.refusalBudget, e.cancelRefusalBudget = spent, cancel
	pause := &EventNodePaused{NodeID: "ask", SubTaskID: "st-1", RequestID: "req-1", ToolNodeID: "ask", ToolName: "ask"}

	if err := e.Advance(t.Context(), pause); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	if pause.closed {
		t.Error("the pause is marked closed although its result was never published, so no one would close the call")
	}
	if results := toolEvents(t, brk, a2a.SignalToolResult, "st-1"); len(results) != 0 {
		t.Errorf("the executor published %d results with its budget spent, want none", len(results))
	}
}

func firstPublishedTo(brk *capturingBroker, topic string) int {
	for i, msg := range brk.snapshot() {
		if msg.Topic == topic {
			return i
		}
	}
	return -1
}

func toolEventIndexes(t *testing.T, brk *capturingBroker, signal a2a.SignalType, callID string) []int {
	t.Helper()
	var indexes []int
	for _, event := range toolEvents(t, brk, signal, callID) {
		indexes = append(indexes, event.index)
	}
	return indexes
}

type publishedToolEvent struct {
	index int
	data  map[string]any
}

func toolEvents(t *testing.T, brk *capturingBroker, signal a2a.SignalType, callID string) []publishedToolEvent {
	t.Helper()
	var events []publishedToolEvent
	for i, msg := range brk.snapshot() {
		if msg.Topic != evalRunStatusTopic {
			continue
		}
		var envelope struct {
			Result struct {
				Status struct {
					Message struct {
						Parts []struct {
							Data map[string]any `json:"data"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"status"`
			} `json:"result"`
		}
		if err := json.Unmarshal(msg.Payload, &envelope); err != nil {
			t.Fatalf("decode status event: %v", err)
		}
		for _, part := range envelope.Result.Status.Message.Parts {
			if part.Data["type"] == string(signal) && part.Data["function_call_id"] == callID {
				events = append(events, publishedToolEvent{index: i, data: part.Data})
			}
		}
	}
	return events
}

func nodeResultMetadata(t *testing.T, brk *capturingBroker, statusTopic, nodeID string) map[string]any {
	t.Helper()
	for _, msg := range brk.snapshot() {
		if msg.Topic != statusTopic {
			continue
		}
		var envelope struct {
			Result struct {
				Status struct {
					Message struct {
						Parts []struct {
							Data WorkflowNodeExecutionResultData `json:"data"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"status"`
			} `json:"result"`
		}
		if err := json.Unmarshal(msg.Payload, &envelope); err != nil {
			t.Fatalf("decode status event: %v", err)
		}
		for _, part := range envelope.Result.Status.Message.Parts {
			if part.Data.Type == string(a2a.SignalWorkflowNodeExecResult) && part.Data.NodeID == nodeID {
				return part.Data.Metadata
			}
		}
	}
	t.Fatalf("no node result for %s on %s; saw %v", nodeID, statusTopic, capturedTopics(brk))
	return nil
}
