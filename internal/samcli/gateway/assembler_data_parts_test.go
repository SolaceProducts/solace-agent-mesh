// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import (
	"reflect"
	"testing"
)

func completedTask(parts ...any) map[string]any {
	return map[string]any{"result": map[string]any{
		"kind": "task",
		"id":   "task-1",
		"status": map[string]any{
			"state":   "completed",
			"message": map[string]any{"parts": parts},
		},
	}}
}

func artifactNotice(filename string) map[string]any {
	return map[string]any{
		"kind":     "data",
		"data":     map[string]any{"filename": filename, "version": 0, "status": "success"},
		"metadata": map[string]any{"tool_name": "_notify_artifact_save"},
	}
}

func TestAssembler_FinalTaskKeepsDataParts(t *testing.T) {
	result := map[string]any{"greeting": "Hello, Alpha"}
	tests := []struct {
		name string
		task map[string]any
		want []any
	}{
		{
			name: "data parts are kept in order and text parts are not",
			task: completedTask(
				map[string]any{"kind": "data", "data": result},
				map[string]any{"kind": "text", "text": "summary"},
				map[string]any{"kind": "data", "data": map[string]any{"second": true}},
			),
			want: []any{result, map[string]any{"second": true}},
		},
		{
			name: "an artifact notice with a text part is not a result",
			task: completedTask(
				artifactNotice("report.md"),
				map[string]any{"kind": "text", "text": "Done."},
			),
			want: nil,
		},
		{
			name: "an artifact notice before a result leaves only the result",
			task: completedTask(
				artifactNotice("report.md"),
				map[string]any{"kind": "data", "data": result},
				artifactNotice("other.md"),
				map[string]any{"kind": "text", "text": "summary"},
			),
			want: []any{result},
		},
		{
			name: "a data part with other metadata is kept",
			task: completedTask(map[string]any{
				"kind": "data", "data": result,
				"metadata": map[string]any{"tool_name": "something_else"},
			}),
			want: []any{result},
		},
		{
			name: "a data part with no data is skipped",
			task: completedTask(map[string]any{"kind": "data"}),
			want: nil,
		},
		{
			name: "a text-only final message has none",
			task: completedTask(map[string]any{"kind": "text", "text": "plain"}),
			want: nil,
		},
		{
			name: "a failed task keeps none",
			task: failedTask(nil, structuredResult("boom")),
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, _ := NewMessageAssembler().ProcessEvent("final_response", tc.task)
			if !reflect.DeepEqual(msg.DataParts, tc.want) {
				t.Fatalf("DataParts = %#v; want %#v", msg.DataParts, tc.want)
			}
		})
	}
}
