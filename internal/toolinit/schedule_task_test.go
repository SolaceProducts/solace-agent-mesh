// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"testing"
)

func TestScheduleTaskToolResolves(t *testing.T) {
	opts := Options{}

	want := []string{
		"schedule_task",
		"list_scheduled_tasks",
		"update_scheduled_task",
		"delete_scheduled_task",
		"set_scheduled_task_enabled",
	}

	for _, name := range want {
		tl := LookupBuiltinTool(name, map[string]any{}, opts)
		if tl == nil {
			t.Fatalf("LookupBuiltinTool(%q) = nil, want a tool", name)
		}
		if tl.Name() != name {
			t.Errorf("Name() = %q, want %q", tl.Name(), name)
		}
	}

	if got := LookupBuiltinGroup("scheduling_tools", opts); !slices.Equal(got, want) {
		t.Errorf("scheduling_tools group = %v, want %v", got, want)
	}
}
