// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestTaskRows_ResolvesDisplayName(t *testing.T) {
	slug := "agent_019ef609_21a8_754f_9b9e_814806827854"
	svc := (&Service{}).WithAgentNameResolver(func(s string) string {
		if s == slug {
			return "Activity Monitor"
		}
		return ""
	})

	tasks := []*store.Task{{
		ID:          "task-1",
		HandlerName: strptr(slug),
		HandlerType: strptr(store.TaskHandlerTypeAgent),
		Status:      strptr("failed"),
		StartTime:   1_700_000_000_000,
	}}

	row := svc.taskRows(tasks, true, nil)[0]
	if row.Name != "Activity Monitor" {
		t.Errorf("Name = %q, want %q (raw slug must not leak into failure listings)", row.Name, "Activity Monitor")
	}
}

func TestTaskRows_FallsBackToSlugWhenUnresolved(t *testing.T) {
	svc := (&Service{}).WithAgentNameResolver(func(string) string { return "" })
	tasks := []*store.Task{{
		ID:          "task-2",
		HandlerName: strptr("unknown_slug"),
		Status:      strptr("failed"),
		StartTime:   1_700_000_000_000,
	}}
	row := svc.taskRows(tasks, true, nil)[0]
	if row.Name != "unknown_slug" {
		t.Errorf("Name = %q, want %q (fallback to slug)", row.Name, "unknown_slug")
	}
}

func TestTaskRows_StatusEmojiIsServerComputedNeverGuessed(t *testing.T) {
	svc := (&Service{}).WithAgentNameResolver(func(string) string { return "" })
	cases := []struct {
		status string
		want   string
	}{
		{"completed", "✅"},
		{"timeout", "⚠️"},
		{"stale", "⚠️"},
		{"failed", "❌"},
		{"error", "❌"},
		{"cancelled", "❌"},
		{"canceled", "❌"},
		{"in_progress", ""},
	}
	for _, c := range cases {
		tasks := []*store.Task{{ID: "task-" + c.status, Status: strptr(c.status), StartTime: 1_700_000_000_000}}
		row := svc.taskRows(tasks, true, nil)[0]
		if row.StatusEmoji != c.want {
			t.Errorf("status=%q: StatusEmoji = %q, want %q", c.status, row.StatusEmoji, c.want)
		}
	}
}
