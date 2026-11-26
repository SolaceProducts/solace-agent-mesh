// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func dataStatusEvent(d map[string]any) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(d)}}},
	}
}

func textStatusEvent(s string) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: s}}}},
	}
}

func countCalls(calls []slackapi.RecordedCall, method string) int {
	n := 0
	for _, c := range calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func appendedChunks(calls []slackapi.RecordedCall) string {
	var b strings.Builder
	for _, c := range calls {
		if c.Method == "AppendStream" {
			b.WriteString(c.Params["chunks"])
		}
	}
	return b.String()
}

func TestAdapter_StreamingTimeline_EndToEnd(t *testing.T) {
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{ShowActivityTimeline: true, CorrectMarkdown: true})
	mem := slackapi.NewInMemorySlackAPI()
	a.api = mem
	a.queues = make(map[string]*MessageQueue)

	taskID := "task-stream"
	_ = a.core.Tasks().Register(taskID, "agent", "u1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &SlackTaskMeta{ChannelID: "C1", ThreadTS: "1700000000.1", UserID: "U1", TeamID: "T1"})

	a.OnStatusUpdate(t.Context(), taskID, dataStatusEvent(map[string]any{"type": "agent_progress_update", "status_text": "Working"}), nil)
	a.OnStatusUpdate(t.Context(), taskID, dataStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "search", "function_call_id": "fc1", "tool_args": map[string]any{"q": "x"}}), nil)
	a.OnStatusUpdate(t.Context(), taskID, dataStatusEvent(map[string]any{"type": "tool_result", "function_call_id": "fc1", "result_data": "ok"}), nil)
	a.OnStatusUpdate(t.Context(), taskID, dataStatusEvent(map[string]any{"type": "artifact_creation_progress", "filename": "r.pdf", "status": "in-progress"}), nil)
	a.OnStatusUpdate(t.Context(), taskID, textStatusEvent("Here is the answer."), nil)
	a.OnFinalResponse(t.Context(), taskID, failedTask(taskID, a2a.TaskStateCompleted), nil)

	calls := mem.Calls()
	if got := countCalls(calls, "StartStream"); got != 1 {
		t.Errorf("StartStream calls = %d, want 1", got)
	}
	if got := countCalls(calls, "StopStream"); got != 1 {
		t.Errorf("StopStream calls = %d, want 1", got)
	}
	if got := countCalls(calls, "PostMessage"); got != 0 {
		t.Errorf("PostMessage calls = %d, want 0 (no status placeholder in streaming mode)", got)
	}
	chunks := appendedChunks(calls)
	for _, want := range []string{`"type":"task_update"`, `"type":"plan_update"`, `"type":"markdown_text"`} {
		if !strings.Contains(chunks, want) {
			t.Errorf("missing chunk %s in: %s", want, chunks)
		}
	}
	if !strings.Contains(chunks, "Here is the answer.") {
		t.Errorf("answer not streamed: %s", chunks)
	}
}

func TestAdapter_StreamingOnError(t *testing.T) {
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{ShowActivityTimeline: true})
	mem := slackapi.NewInMemorySlackAPI()
	a.api = mem
	a.queues = make(map[string]*MessageQueue)

	taskID := "task-err"
	_ = a.core.Tasks().Register(taskID, "agent", "u1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &SlackTaskMeta{ChannelID: "C1", ThreadTS: "1700000000.2"})

	a.OnStatusUpdate(t.Context(), taskID, dataStatusEvent(map[string]any{"type": "agent_progress_update", "status_text": "Working"}), nil)
	a.OnError(t.Context(), taskID, &a2a.Error{Message: "boom"}, nil)

	calls := mem.Calls()
	if got := countCalls(calls, "StopStream"); got != 1 {
		t.Errorf("StopStream calls = %d, want 1", got)
	}
	if !strings.Contains(appendedChunks(calls), "boom") {
		t.Errorf("error text not streamed: %s", appendedChunks(calls))
	}
}

func TestAdapter_StreamingSkipsWorkflowToolNodeTelemetry(t *testing.T) {
	a, _ := newAdapterForBehaviorTest(t, &SlackConfig{ShowActivityTimeline: true})
	mem := slackapi.NewInMemorySlackAPI()
	a.api = mem
	a.queues = make(map[string]*MessageQueue)

	taskID := "task-wf"
	_ = a.core.Tasks().Register(taskID, "agent", "u1", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &SlackTaskMeta{ChannelID: "C1", ThreadTS: "1700000000.3"})

	withNodeMeta := func(d map[string]any) *a2a.TaskStatusUpdateEvent {
		event := dataStatusEvent(d)
		meta := map[string]any{"workflow_name": "wf", "node_id": "lookup_node", "node_type": "tool"}
		event.Metadata = meta
		event.Status.Message.Metadata = meta
		return event
	}
	a.OnStatusUpdate(t.Context(), taskID, dataStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "lookup_node", "node_type": "tool"}), nil)
	a.OnStatusUpdate(t.Context(), taskID, withNodeMeta(map[string]any{"type": "tool_invocation_start", "tool_name": "lookup", "function_call_id": "sub-tool-call"}), nil)
	a.OnStatusUpdate(t.Context(), taskID, withNodeMeta(map[string]any{"type": "tool_result", "tool_name": "lookup", "function_call_id": "sub-tool-call", "result_data": "ok"}), nil)
	forwarded := dataStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "lookup", "function_call_id": "forwarded-tool-call"})
	forwarded.Status.Message.Metadata = map[string]any{
		"agent_name": "Orchestrator", "function_call_id": "call-7", "forwarded_from_peer": "wf",
		"workflow_name": "wf", "node_id": "lookup_node", "node_type": "tool",
	}
	a.OnStatusUpdate(t.Context(), taskID, forwarded, nil)
	a.OnStatusUpdate(t.Context(), taskID, dataStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": "search", "function_call_id": "agent-call"}), nil)
	a.OnFinalResponse(t.Context(), taskID, failedTask(taskID, a2a.TaskStateCompleted), nil)

	chunks := appendedChunks(mem.Calls())
	if strings.Contains(chunks, "forwarded-tool-call") {
		t.Errorf("a workflow tool node's telemetry forwarded through an orchestrator opened a second card: %s", chunks)
	}
	if !strings.Contains(chunks, "lookup_node") {
		t.Errorf("the workflow node's own card is missing: %s", chunks)
	}
	if strings.Contains(chunks, "sub-tool-call") {
		t.Errorf("the workflow tool node's telemetry opened a second card: %s", chunks)
	}
	if !strings.Contains(chunks, "agent-call") {
		t.Errorf("an agent's own tool call no longer gets a card: %s", chunks)
	}
}
