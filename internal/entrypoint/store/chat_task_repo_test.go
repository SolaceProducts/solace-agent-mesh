// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestChatTaskRepo_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	sessionRepo := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	_ = sessionRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})

	ct := &ChatTask{
		ID:             "20000000-0000-0000-0000-000000000001",
		SessionID:      "00000000-0000-0000-0000-000000000001",
		UserID:         "u1",
		MessageBubbles: `[{"text":"hello"}]`,
	}
	if err := repo.Create(ctx, ct); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "20000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected chat task")
	}
	if got.SessionID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("session_id = %s, want 00000000-0000-0000-0000-000000000001", got.SessionID)
	}
	if got.CreatedTime == 0 {
		t.Error("expected created_time to be set")
	}
}

func TestChatTaskRepo_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewChatTaskRepo(db)

	got, err := repo.GetByID(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil")
	}
}

func TestChatTaskRepo_ListBySession(t *testing.T) {
	db := newTestDB(t)
	sessionRepo := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	_ = sessionRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})

	for i := range 3 {
		_ = repo.Create(ctx, &ChatTask{
			ID:             fmt.Sprintf("20000000-0000-0000-0000-00000000000%d", i+1),
			SessionID:      "00000000-0000-0000-0000-000000000001",
			UserID:         "u1",
			MessageBubbles: "[]",
		})
	}

	tasks, err := repo.ListBySession(ctx, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Errorf("expected 3 chat tasks, got %d", len(tasks))
	}
}

func TestChatTaskRepo_UpdateBubbles(t *testing.T) {
	db := newTestDB(t)
	sessionRepo := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	_ = sessionRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})
	_ = repo.Create(ctx, &ChatTask{
		ID: "20000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", MessageBubbles: "[]",
	})

	newBubbles := `[{"text":"updated"}]`
	if err := repo.UpdateBubbles(ctx, "20000000-0000-0000-0000-000000000001", newBubbles); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "20000000-0000-0000-0000-000000000001")
	if got.MessageBubbles != newBubbles {
		t.Errorf("message_bubbles = %s, want %s", got.MessageBubbles, newBubbles)
	}
	if got.UpdatedTime == nil {
		t.Error("expected updated_time to be set")
	}
}

func TestChatTaskRepo_UpdateBubbles_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewChatTaskRepo(db)

	err := repo.UpdateBubbles(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff", "[]")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestChatTaskRepo_Upsert_Create(t *testing.T) {
	db := newTestDB(t)
	sessionRepo := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	_ = sessionRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})

	ct := &ChatTask{
		ID: "20000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", MessageBubbles: "[]",
	}
	created, err := repo.Upsert(ctx, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected created=true for new task")
	}

	got, _ := repo.GetByID(ctx, "20000000-0000-0000-0000-000000000001")
	if got == nil {
		t.Fatal("expected task to exist")
	}
}

func TestChatTaskRepo_Upsert_Update(t *testing.T) {
	db := newTestDB(t)
	sessionRepo := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	_ = sessionRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})
	_ = repo.Create(ctx, &ChatTask{
		ID: "20000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", MessageBubbles: "[]",
	})

	updated := &ChatTask{
		ID: "20000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1", MessageBubbles: `[{"new":"data"}]`,
	}
	_, err := repo.Upsert(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "20000000-0000-0000-0000-000000000001")
	if got.MessageBubbles != `[{"new":"data"}]` {
		t.Errorf("expected updated bubbles, got %s", got.MessageBubbles)
	}
	if got.UpdatedTime == nil {
		t.Error("expected updated_time to be set")
	}
}
