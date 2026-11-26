// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import "testing"

func failedTask(metadata map[string]any, parts ...any) map[string]any {
	result := map[string]any{
		"kind": "task",
		"id":   "task-1",
		"status": map[string]any{
			"state":   "failed",
			"message": map[string]any{"parts": parts},
		},
	}
	if metadata != nil {
		result["metadata"] = metadata
	}
	return map[string]any{"result": result}
}

func structuredResult(reason string) map[string]any {
	return map[string]any{
		"kind": "data",
		"data": map[string]any{
			"type":          "structured_invocation_result",
			"status":        "error",
			"error_message": reason,
		},
	}
}

func TestAssembler_FinalTaskFailedReason(t *testing.T) {
	tests := []struct {
		name string
		task map[string]any
		want string
	}{
		{
			name: "structured result reason when there is no text part",
			task: failedTask(nil, structuredResult("Input validation failed: missing property 'destination'")),
			want: "Input validation failed: missing property 'destination'",
		},
		{
			name: "a text part wins over a structured result",
			task: failedTask(nil, structuredResult("from data"), map[string]any{"kind": "text", "text": "from text"}),
			want: "from text",
		},
		{
			name: "an empty text part does not hide a later structured result",
			task: failedTask(nil, map[string]any{"kind": "text", "text": ""}, structuredResult("from data")),
			want: "from data",
		},
		{
			name: "the first structured result wins",
			task: failedTask(nil, structuredResult("first"), structuredResult("second")),
			want: "first",
		},
		{
			name: "a data part that is not a structured result is not a reason",
			task: failedTask(nil, map[string]any{"kind": "data", "data": map[string]any{"error_message": "unrelated"}}),
			want: "Unknown error",
		},
		{
			name: "the task's own error metadata is the last resort",
			task: failedTask(map[string]any{"error": "from metadata"}, map[string]any{"kind": "data", "data": map[string]any{"type": "structured_invocation_result"}}),
			want: "from metadata",
		},
		{
			name: "no reason anywhere is unknown",
			task: failedTask(nil, map[string]any{"kind": "data", "data": map[string]any{"type": "structured_invocation_result"}}),
			want: "Unknown error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, newText := NewMessageAssembler().ProcessEvent("final_response", tc.task)
			if msg.ErrorMessage != tc.want || newText != "\nTask failed: "+tc.want {
				t.Fatalf("ErrorMessage = %q, new text = %q; want %q", msg.ErrorMessage, newText, tc.want)
			}
		})
	}
}
