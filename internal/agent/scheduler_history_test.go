// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"io"
	"log/slog"
	"testing"
)

func TestSchedulerHistoryMessages(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name string
		meta map[string]any
		want []struct{ role, content string }
	}{
		{
			name: "absent metadata",
			meta: map[string]any{},
			want: nil,
		},
		{
			name: "wrong type",
			meta: map[string]any{"schedulerConversationHistory": "not-a-list"},
			want: nil,
		},
		{
			name: "valid round-trip shape",
			meta: map[string]any{"schedulerConversationHistory": []any{
				map[string]any{"role": "user", "content": "run report"},
				map[string]any{"role": "assistant", "content": "done"},
			}},
			want: []struct{ role, content string }{
				{"user", "run report"},
				{"assistant", "done"},
			},
		},
		{
			name: "skips malformed and empty entries",
			meta: map[string]any{"schedulerConversationHistory": []any{
				"junk",
				map[string]any{"role": "system", "content": "x"},
				map[string]any{"role": "user", "content": ""},
				map[string]any{"role": "user", "content": "keep"},
			}},
			want: []struct{ role, content string }{{"user", "keep"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := schedulerHistoryMessages(tc.meta, logger)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d messages, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i].Role != tc.want[i].role || got[i].Content != tc.want[i].content {
					t.Errorf("msg %d = {%q,%q}, want {%q,%q}",
						i, got[i].Role, got[i].Content, tc.want[i].role, tc.want[i].content)
				}
			}
		})
	}
}
