// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const rejectedInputMessage = `Workflow "TripPlanner" failed: Input validation failed: missing properties 'destination', 'needsHotel'. Send the input as a JSON object. Required: destination (string), needsHotel (boolean).`

func finalTask(state a2a.TaskState, text string) *a2a.Task {
	task := &a2a.Task{Status: a2a.TaskStatus{State: state}}
	if text != "" {
		task.Status.Message = &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{{Kind: "text", Text: text}}}
	}
	return task
}

func TestUnsuccessfulEnd(t *testing.T) {
	structured := &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed, Message: &a2a.Message{Parts: []a2a.Part{
		a2a.NewDataPart(map[string]any{"type": "structured_invocation_result", "error_message": "  node blew up\n"}),
	}}}}
	metadataOnly := &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}, Metadata: map[string]any{"error": "workflow timed out"}}
	bare := &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}

	tests := []struct {
		name        string
		task        *a2a.Task
		wantOK      bool
		wantMessage string
	}{
		{"nil task", nil, false, ""},
		{"completed", finalTask(a2a.TaskStateCompleted, "done"), false, ""},
		{"working is not terminal", finalTask(a2a.TaskStateWorking, ""), false, ""},
		{"failed with text", finalTask(a2a.TaskStateFailed, rejectedInputMessage), true, rejectedInputMessage},
		{"rejected counts as failed", finalTask(a2a.TaskStateRejected, "refused"), true, "refused"},
		{"canceled, as a workflow timeout ends, counts as failed", finalTask(a2a.TaskStateCancelled, `Workflow "TripPlanner" was cancelled.`), true, `Workflow "TripPlanner" was cancelled.`},
		{"structured error when no text, trimmed", structured, true, "node blew up"},
		{"metadata error when no message", metadataOnly, true, "workflow timed out"},
		{"names the state when nothing else is said", bare, true, `task ended in state "failed"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			message, ok := unsuccessfulEnd(tc.task)
			if ok != tc.wantOK {
				t.Fatalf("unsuccessfulEnd ok = %v, want %v", ok, tc.wantOK)
			}
			if message != tc.wantMessage {
				t.Errorf("unsuccessfulEnd message = %q, want %q", message, tc.wantMessage)
			}
		})
	}
}
