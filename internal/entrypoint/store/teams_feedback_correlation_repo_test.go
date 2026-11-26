// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"
)

func TestTeamsFeedbackCorrelationRepo_RoundTrip(t *testing.T) {
	repo := NewTeamsFeedbackCorrelationRepo(newTestDB(t))
	ctx := t.Context()

	if err := repo.Put(ctx, "conv-1", "msg-1", toSeedUUID("task-1"), toSeedUUID("sess-1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := repo.Get(ctx, "conv-1", "msg-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Get = (%v, %v), want a row", ok, err)
	}
	if got.TaskID != toSeedUUID("task-1") || got.SessionID != toSeedUUID("sess-1") {
		t.Errorf("Get = %+v, want the task-1/sess-1 pair", got)
	}

	if _, ok, _ := repo.Get(ctx, "conv-1", "never-written", time.Hour); ok {
		t.Error("an unknown message must not resolve")
	}
}

func TestTeamsFeedbackCorrelationRepo_SameMessageIDAcrossConversations(t *testing.T) {
	repo := NewTeamsFeedbackCorrelationRepo(newTestDB(t))
	ctx := t.Context()

	if err := repo.Put(ctx, "conv-a", "shared", toSeedUUID("task-a"), toSeedUUID("sess-a")); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := repo.Put(ctx, "conv-b", "shared", toSeedUUID("task-b"), toSeedUUID("sess-b")); err != nil {
		t.Fatalf("Put b: %v", err)
	}

	for _, tc := range []struct{ conv, wantTask string }{{"conv-a", toSeedUUID("task-a")}, {"conv-b", toSeedUUID("task-b")}} {
		got, ok, err := repo.Get(ctx, tc.conv, "shared", time.Hour)
		if err != nil || !ok {
			t.Fatalf("%s: Get = (%v, %v)", tc.conv, ok, err)
		}
		if got.TaskID != tc.wantTask {
			t.Errorf("%s: taskID = %q, want %q", tc.conv, got.TaskID, tc.wantTask)
		}
	}
}

func TestTeamsFeedbackCorrelationRepo_GetRefusesPastMaxAge(t *testing.T) {
	repo := NewTeamsFeedbackCorrelationRepo(newTestDB(t))
	ctx := t.Context()

	if err := repo.Put(ctx, "conv-1", "msg-1", toSeedUUID("task-1"), toSeedUUID("sess-1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok, _ := repo.Get(ctx, "conv-1", "msg-1", time.Nanosecond); ok {
		t.Error("a row past maxAge must not resolve, even though the sweep has not run")
	}
	if _, ok, _ := repo.Get(ctx, "conv-1", "msg-1", time.Hour); !ok {
		t.Error("the row itself must still be there; maxAge is a read filter, not a delete")
	}
}

func TestTeamsFeedbackCorrelationRepo_DeleteOlderThan(t *testing.T) {
	repo := NewTeamsFeedbackCorrelationRepo(newTestDB(t))
	ctx := t.Context()

	if err := repo.Put(ctx, "conv-1", "msg-1", toSeedUUID("task-1"), toSeedUUID("sess-1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	deleted, err := repo.DeleteOlderThan(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted %d rows inside the horizon, want 0", deleted)
	}

	deleted, err = repo.DeleteOlderThan(ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted %d rows past the horizon, want 1", deleted)
	}
	if _, ok, _ := repo.Get(ctx, "conv-1", "msg-1", time.Hour); ok {
		t.Error("the swept row still resolves")
	}
}

func TestTeamsFeedbackCorrelationRepo_PutReplaces(t *testing.T) {
	repo := NewTeamsFeedbackCorrelationRepo(newTestDB(t))
	ctx := t.Context()

	if err := repo.Put(ctx, "conv-1", "msg-1", toSeedUUID("task-1"), toSeedUUID("sess-1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := repo.Put(ctx, "conv-1", "msg-1", toSeedUUID("task-2"), toSeedUUID("sess-2")); err != nil {
		t.Fatalf("Put again: %v", err)
	}
	got, ok, err := repo.Get(ctx, "conv-1", "msg-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("Get = (%v, %v)", ok, err)
	}
	if got.TaskID != toSeedUUID("task-2") {
		t.Errorf("taskID = %q, want the rewritten task-2", got.TaskID)
	}
}
