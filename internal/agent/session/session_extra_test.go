// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"testing"
)

func TestMemoryStore_MetadataDeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	nested := map[string]any{"inner": "original"}
	err := store.Create(ctx, &Session{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
		Metadata:  map[string]any{"nested": nested},
	})
	if err != nil {
		t.Fatal(err)
	}

	nested["inner"] = "mutated"

	got, err := store.Get(ctx, "app", "u", "s")
	if err != nil {
		t.Fatal(err)
	}
	gotNested, ok := got.Metadata["nested"].(map[string]any)
	if !ok {
		t.Fatal("expected nested map in metadata")
	}
	if gotNested["inner"] != "original" {
		t.Errorf("metadata leaked mutation: inner = %q, want %q", gotNested["inner"], "original")
	}

	gotNested["inner"] = "from-get"
	got2, _ := store.Get(ctx, "app", "u", "s")
	gotNested2 := got2.Metadata["nested"].(map[string]any)
	if gotNested2["inner"] != "original" {
		t.Errorf("Get returned shared reference: inner = %q, want %q", gotNested2["inner"], "original")
	}
}

func TestMemoryStore_ListSessions_Empty(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sessions, err := store.ListSessions(ctx, "app", "user1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected empty slice, got %d sessions", len(sessions))
	}
	if sessions == nil {
		t.Error("expected non-nil empty slice")
	}
}

func TestMemoryStore_ListSessions_FiltersCorrectly(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Create(ctx, &Session{AppName: "app", UserID: "user1", SessionID: "s1"})
	_ = store.Create(ctx, &Session{AppName: "app", UserID: "user1", SessionID: "s2"})
	_ = store.Create(ctx, &Session{AppName: "app", UserID: "user2", SessionID: "s3"})

	sessions, err := store.ListSessions(ctx, "app", "user1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions for user1, got %d", len(sessions))
	}

	sessions2, err := store.ListSessions(ctx, "app", "user2")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions2) != 1 {
		t.Errorf("expected 1 session for user2, got %d", len(sessions2))
	}
}
