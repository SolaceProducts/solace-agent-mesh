// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const toolCrashMessage = "tool execution failed (TOOL_EXIT_NONZERO): process exited with code 1"

func deliverNodeTaskParts(t *testing.T, e *DAGExecutor, subTaskID string, state a2a.TaskState, parts ...a2a.Part) {
	t.Helper()
	task := a2a.Task{
		ID:   subTaskID,
		Kind: "task",
		Status: a2a.TaskStatus{
			State:   state,
			Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: parts},
		},
	}
	payload, err := json.Marshal(a2a.NewResponse(subTaskID, task))
	if err != nil {
		t.Fatalf("marshal node response: %v", err)
	}
	e.instance.handleAgentResponse(t.Context(),
		a2a.Request{ID: subTaskID}, &broker.Message{Payload: payload})
}

func structuredErrorPart(msg string) a2a.Part {
	return a2a.NewDataPart(map[string]any{
		"type":          "structured_invocation_result",
		"status":        "error",
		"error_message": msg,
	})
}

func terminalTask(t *testing.T, brk *capturingBroker) a2a.Task {
	t.Helper()
	for _, m := range brk.snapshot() {
		if m.Topic != usageFactsReplyTopic {
			continue
		}
		var envelope struct {
			Result a2a.Task `json:"result"`
		}
		if err := json.Unmarshal(m.Payload, &envelope); err != nil {
			t.Fatalf("unmarshal terminal task: %v", err)
		}
		return envelope.Result
	}
	t.Fatalf("the workflow published no terminal task to %s", usageFactsReplyTopic)
	return a2a.Task{}
}

func TestFailedStructuredNodeKeepsItsReason(t *testing.T) {
	e, brk := newUsageFactsExecutorFailingFast(t, agentNodes(1), true)
	subTasks := dispatchedSubTasks(t, e)

	deliverNodeTaskParts(t, e, subTasks[0], a2a.TaskStateFailed, structuredErrorPart(toolCrashMessage))

	reason, _ := terminalTaskMetadata(t, brk)["error"].(string)
	if !strings.Contains(reason, toolCrashMessage) {
		t.Errorf("workflow failure = %q, want it to carry the node's reason %q", reason, toolCrashMessage)
	}
}

func TestPartialFailureReachesTheTerminalTask(t *testing.T) {
	e, brk := newUsageFactsExecutor(t, agentNodes(2))
	subTasks := dispatchedSubTasks(t, e)

	deliverNodeTaskParts(t, e, subTasks[0], a2a.TaskStateFailed, structuredErrorPart(toolCrashMessage))
	deliverNodeTaskParts(t, e, subTasks[1], a2a.TaskStateCompleted, a2a.NewTextPart("done"))

	task := terminalTask(t, brk)
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("state = %q, want completed: callers opted into a best-effort run", task.Status.State)
	}
	report, _ := task.Metadata["partial_failure"].(map[string]any)
	if report["failed_node_id"] != "node_0" {
		t.Errorf("partial_failure = %#v, want failed_node_id node_0", report)
	}
	var text string
	for _, p := range task.Status.Message.Parts {
		text += p.Text
	}
	for _, want := range []string{"node_0", toolCrashMessage} {
		if !strings.Contains(text, want) {
			t.Errorf("reply text %q must mention %q", text, want)
		}
	}
}
