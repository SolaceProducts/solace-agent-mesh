// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"hash/crc32"
	"testing"
)

func seedSessions(t *testing.T, repo *SessionRepo, userID string, n int) []string {
	t.Helper()
	sched := sessionSourceScheduler
	ids := make([]string, 0, n)
	for i := range n {
		id := fmt.Sprintf("10000000-0000-0000-%04x-%012d", crc32.ChecksumIEEE([]byte(userID))&0xffff, i)
		if err := repo.Create(context.Background(), &Session{ID: id, UserID: userID, Source: &sched}); err != nil {
			t.Fatalf("seed session %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func TestSessionRepo_ListByUserPage_HasMoreBoundary(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	seedSessions(t, repo, "u1", 5)

	rows, hasMore, err := repo.ListByUserPage(ctx, "u1", SessionListFilter{NonEmptyOnly: true, ExcludeExternalGateways: true}, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hasMore {
		t.Errorf("limit=5 over 5 rows: hasMore=true, want false")
	}
	if len(rows) != 5 {
		t.Errorf("limit=5: len=%d, want 5", len(rows))
	}

	rows, hasMore, err = repo.ListByUserPage(ctx, "u1", SessionListFilter{NonEmptyOnly: true, ExcludeExternalGateways: true}, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMore {
		t.Errorf("limit=4 over 5 rows: hasMore=false, want true")
	}
	if len(rows) != 4 {
		t.Errorf("limit=4: len=%d, want 4 (over-fetch row must be trimmed)", len(rows))
	}

	rows, hasMore, err = repo.ListByUserPage(ctx, "u1", SessionListFilter{NonEmptyOnly: true, ExcludeExternalGateways: true}, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if hasMore {
		t.Errorf("tail page: hasMore=true, want false")
	}
	if len(rows) != 1 {
		t.Errorf("tail page: len=%d, want 1", len(rows))
	}
}

func TestSessionRepo_CountByUser_Isolation(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	seedSessions(t, repo, "u1", 3)
	seedSessions(t, repo, "u2", 7)

	n, err := repo.CountByUser(ctx, "u1", SessionListFilter{NonEmptyOnly: true, ExcludeExternalGateways: true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("CountByUser(u1) = %d, want 3", n)
	}

	n, err = repo.CountByUser(ctx, "u2", SessionListFilter{NonEmptyOnly: true, ExcludeExternalGateways: true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Errorf("CountByUser(u2) = %d, want 7", n)
	}
}
