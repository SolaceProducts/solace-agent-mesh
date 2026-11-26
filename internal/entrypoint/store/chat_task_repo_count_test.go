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

func TestCountBySessionForUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewChatTaskRepo(db)
	ctx := context.Background()

	sid := idgen.New()
	other := idgen.New()
	user := "user-a"
	otherUser := "user-b"

	sessRepo := NewSessionRepo(db)
	if err := sessRepo.Create(ctx, &Session{ID: sid, UserID: user}); err != nil {
		t.Fatal(err)
	}
	if err := sessRepo.Create(ctx, &Session{ID: other, UserID: user}); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if err := repo.Create(ctx, &ChatTask{
			ID: idgen.New(), SessionID: sid, UserID: user, MessageBubbles: "[]",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Create(ctx, &ChatTask{
		ID: idgen.New(), SessionID: sid, UserID: otherUser, MessageBubbles: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &ChatTask{
		ID: idgen.New(), SessionID: other, UserID: user, MessageBubbles: "[]",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.CountBySessionForUser(ctx, sid, user)
	if err != nil {
		t.Fatalf("CountBySessionForUser: %v", err)
	}
	if got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}
