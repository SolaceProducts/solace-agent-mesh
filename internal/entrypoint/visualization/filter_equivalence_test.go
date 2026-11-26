// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package visualization

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestShouldFilterWorkingState_TypedMatchesMap(t *testing.T) {
	dataPart := func(status string) a2a.Part {
		return a2a.NewDataPart(map[string]any{"status": status})
	}
	statusEvent := func(state a2a.TaskState, parts ...a2a.Part) *a2a.TaskStatusUpdateEvent {
		e := &a2a.TaskStatusUpdateEvent{
			TaskID: "t1", ContextID: "c1", Kind: "status-update",
			Status: a2a.TaskStatus{State: state},
		}
		if parts != nil {
			e.Status.Message = &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: parts}
		}
		return e
	}

	cases := []struct {
		name  string
		event *a2a.TaskStatusUpdateEvent
	}{
		{"working_single_text", statusEvent(a2a.TaskStateWorking, a2a.NewTextPart("hi"))},
		{"working_multi_text", statusEvent(a2a.TaskStateWorking, a2a.NewTextPart("a"), a2a.NewTextPart("b"))},
		{"working_data_in_progress", statusEvent(a2a.TaskStateWorking, dataPart("in-progress"))},
		{"working_data_done", statusEvent(a2a.TaskStateWorking, dataPart("done"))},
		{"working_data_no_status", statusEvent(a2a.TaskStateWorking, a2a.NewDataPart(map[string]any{"type": "llm_invocation"}))},
		{"working_mixed_text_data", statusEvent(a2a.TaskStateWorking, a2a.NewTextPart("a"), dataPart("in-progress"))},
		{"working_file_part", statusEvent(a2a.TaskStateWorking, a2a.NewFilePart("artifact://x", "x", "text/plain"))},
		{"working_mixed_text_file", statusEvent(a2a.TaskStateWorking, a2a.NewTextPart("a"), a2a.NewFilePart("artifact://x", "x", "text/plain"))},
		{"working_data_array", statusEvent(a2a.TaskStateWorking, a2a.NewDataPart([]any{1, 2}))},
		{"working_data_string", statusEvent(a2a.TaskStateWorking, a2a.NewDataPart("hi"))},
		{"working_data_number", statusEvent(a2a.TaskStateWorking, a2a.NewDataPart(42))},
		{"working_data_null", statusEvent(a2a.TaskStateWorking, a2a.NewDataPart(nil))},
		{"working_no_message", statusEvent(a2a.TaskStateWorking)},
		{"working_empty_parts", statusEvent(a2a.TaskStateWorking, []a2a.Part{}...)},
		{"completed_text", statusEvent(a2a.TaskStateCompleted, a2a.NewTextPart("done"))},
		{"input_required_text", statusEvent(a2a.TaskStateInputNeeded, a2a.NewTextPart("?"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(a2a.NewResponse("t1", tc.event))
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			mapResult := ShouldFilterWorkingState(payload)
			typedResult := ShouldFilterWorkingStateEvent(tc.event)
			if mapResult != typedResult {
				t.Fatalf("mismatch: map=%v typed=%v", mapResult, typedResult)
			}
		})
	}
}
