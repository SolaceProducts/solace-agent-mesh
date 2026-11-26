// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"testing"
	"time"
)

func TestTaskStore_AddGetRemove(t *testing.T) {
	store := NewTaskStore()

	tc := &TaskContext{
		TaskID:    "task-1",
		SessionID: "session-1",
		CreatedAt: time.Now(),
	}
	store.Add(tc)

	if store.Len() != 1 {
		t.Errorf("Len() = %d, want 1", store.Len())
	}

	got := store.Get("task-1")
	if got == nil {
		t.Fatal("expected task")
	}
	if got.SessionID != "session-1" {
		t.Errorf("SessionID = %q", got.SessionID)
	}

	store.Remove("task-1")
	if store.Len() != 0 {
		t.Errorf("Len() = %d after remove, want 0", store.Len())
	}

	if store.Get("task-1") != nil {
		t.Error("expected nil after remove")
	}
}

func TestTaskStore_GetByDownstreamID(t *testing.T) {
	store := NewTaskStore()

	tc1 := &TaskContext{TaskID: "sam-1", DownstreamTaskID: "ds-aaa", CreatedAt: time.Now()}
	tc2 := &TaskContext{TaskID: "sam-2", DownstreamTaskID: "ds-bbb", CreatedAt: time.Now()}
	store.Add(tc1)
	store.Add(tc2)

	got := store.GetByDownstreamID("ds-bbb")
	if got == nil {
		t.Fatal("expected task")
	}
	if got.TaskID != "sam-2" {
		t.Errorf("TaskID = %q, want sam-2", got.TaskID)
	}

	if store.GetByDownstreamID("ds-zzz") != nil {
		t.Error("expected nil for unknown downstream ID")
	}
}

func TestTaskStore_CleanupStale(t *testing.T) {
	store := NewTaskStore()

	old := &TaskContext{TaskID: "old", CreatedAt: time.Now().Add(-2 * time.Hour)}
	recent := &TaskContext{TaskID: "recent", CreatedAt: time.Now()}
	store.Add(old)
	store.Add(recent)

	removed := store.CleanupStale(1 * time.Hour)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if store.Len() != 1 {
		t.Errorf("Len() = %d, want 1", store.Len())
	}
	if store.Get("old") != nil {
		t.Error("old task should have been cleaned up")
	}
	if store.Get("recent") == nil {
		t.Error("recent task should still exist")
	}
}

func TestTaskStore_CleanupStale_AllStale(t *testing.T) {
	store := NewTaskStore()

	for i := range 5 {
		store.Add(&TaskContext{
			TaskID:    "task-" + string(rune('a'+i)),
			CreatedAt: time.Now().Add(-10 * time.Hour),
		})
	}

	removed := store.CleanupStale(1 * time.Hour)
	if removed != 5 {
		t.Errorf("removed = %d, want 5", removed)
	}
	if store.Len() != 0 {
		t.Errorf("Len() = %d, want 0", store.Len())
	}
}

func TestTaskStore_IndexDownstreamID(t *testing.T) {
	store := NewTaskStore()

	tc := &TaskContext{TaskID: "sam-1", CreatedAt: time.Now()}
	store.Add(tc)

	if store.GetByDownstreamID("ds-123") != nil {
		t.Error("expected nil before indexing")
	}

	tc.DownstreamTaskID = "ds-123"
	store.IndexDownstreamID("sam-1", "ds-123")

	got := store.GetByDownstreamID("ds-123")
	if got == nil {
		t.Fatal("expected task after IndexDownstreamID")
	}
	if got.TaskID != "sam-1" {
		t.Errorf("TaskID = %q, want sam-1", got.TaskID)
	}

	store.Remove("sam-1")
	if store.GetByDownstreamID("ds-123") != nil {
		t.Error("expected nil after Remove")
	}
}

func TestTaskStore_IndexDownstreamID_UnknownTask(t *testing.T) {
	store := NewTaskStore()

	store.IndexDownstreamID("no-such-task", "ds-999")
	if store.GetByDownstreamID("ds-999") != nil {
		t.Error("expected nil for unknown SAM task")
	}
}

func TestTaskStore_RemoveNonexistent(t *testing.T) {
	store := NewTaskStore()
	store.Remove("does-not-exist")
}
