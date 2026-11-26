// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func seedChatTask(t *testing.T, repo *ChatTaskRepo, id, sessionID, userID string, createdTime int64) {
	t.Helper()
	if _, err := repo.Upsert(context.Background(), &ChatTask{
		ID:             id,
		SessionID:      sessionID,
		UserID:         userID,
		MessageBubbles: "[]",
		CreatedTime:    createdTime,
	}); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}
}

func TestChatTaskRepo_GetBySessionAndID(t *testing.T) {
	db := newTestDB(t)
	sessionRepo := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	sid := idgen.New()
	other := idgen.New()
	_ = sessionRepo.Create(ctx, &Session{ID: sid, UserID: "u1"})
	_ = sessionRepo.Create(ctx, &Session{ID: other, UserID: "u1"})

	ctID := idgen.New()
	seedChatTask(t, repo, ctID, sid, "u1", 100)

	got, err := repo.GetBySessionAndID(ctx, sid, ctID)
	if err != nil {
		t.Fatalf("GetBySessionAndID: %v", err)
	}
	if got == nil || got.ID != ctID {
		t.Fatalf("expected chat task %s, got %+v", ctID, got)
	}

	if got, _ := repo.GetBySessionAndID(ctx, other, ctID); got != nil {
		t.Errorf("expected nil for wrong session, got %+v", got)
	}
	if got, _ := repo.GetBySessionAndID(ctx, sid, idgen.New()); got != nil {
		t.Errorf("expected nil for unknown id, got %+v", got)
	}
}

func TestChatTaskRepo_ListBySessionPage(t *testing.T) {
	db := newTestDB(t)
	sessionRepo := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	sid := idgen.New()
	other := idgen.New()
	_ = sessionRepo.Create(ctx, &Session{ID: sid, UserID: "u1"})
	_ = sessionRepo.Create(ctx, &Session{ID: other, UserID: "u1"})

	ids := make([]string, 5)
	for i := range ids {
		ids[i] = idgen.New()
		seedChatTask(t, repo, ids[i], sid, "u1", int64(i+1))
	}
	seedChatTask(t, repo, idgen.New(), other, "u1", 99)

	page, total, err := repo.ListBySessionPage(ctx, sid, 2, 0)
	if err != nil {
		t.Fatalf("ListBySessionPage: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(page) != 2 || page[0].ID != ids[0] || page[1].ID != ids[1] {
		t.Errorf("page 1 = %v, want [%s %s]", idsOf(page), ids[0], ids[1])
	}

	page, total, err = repo.ListBySessionPage(ctx, sid, 2, 4)
	if err != nil {
		t.Fatalf("ListBySessionPage last: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(page) != 1 || page[0].ID != ids[4] {
		t.Errorf("last page = %v, want [%s]", idsOf(page), ids[4])
	}
}

func TestChatTaskRepo_ListRecentBySessionForUser(t *testing.T) {
	db := newTestDB(t)
	sessionRepo := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	sid := idgen.New()
	_ = sessionRepo.Create(ctx, &Session{ID: sid, UserID: "u1"})

	ids := make([]string, 5)
	for i := range ids {
		ids[i] = idgen.New()
		seedChatTask(t, repo, ids[i], sid, "u1", int64(i+1))
	}
	seedChatTask(t, repo, idgen.New(), sid, "u2", 6)

	got, err := repo.ListRecentBySessionForUser(ctx, sid, "u1", 3)
	if err != nil {
		t.Fatalf("ListRecentBySessionForUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0].ID != ids[4] || got[1].ID != ids[3] || got[2].ID != ids[2] {
		t.Errorf("got %v, want newest-first [%s %s %s]", idsOf(got), ids[4], ids[3], ids[2])
	}

	all, err := repo.ListRecentBySessionForUser(ctx, sid, "u1", 100)
	if err != nil {
		t.Fatalf("ListRecentBySessionForUser all: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("got %d rows, want 5 (u2 excluded)", len(all))
	}
}

func idsOf(tasks []*ChatTask) []string {
	out := make([]string, len(tasks))
	for i, ct := range tasks {
		out[i] = ct.ID
	}
	return out
}
