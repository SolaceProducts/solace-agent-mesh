// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"
)

func TestNotificationRepo_AddDedupedWritesOncePerRecipient(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))

	id, inserted, err := repo.AddDeduped(ctx, NewNotification{UserID: "u-1", Title: "T", Body: "B", Source: "budget"}, "budget:mesh:2026-10:warn")
	if err != nil || !inserted || id == "" {
		t.Fatalf("first AddDeduped = (%q, %v, %v), want a new row", id, inserted, err)
	}
	if _, inserted, err := repo.AddDeduped(ctx, NewNotification{UserID: "u-1", Title: "T", Body: "B", Source: "budget"}, "budget:mesh:2026-10:warn"); err != nil || inserted {
		t.Fatalf("repeat AddDeduped = (%v, %v), want no row", inserted, err)
	}
	if _, inserted, err := repo.AddDeduped(ctx, NewNotification{UserID: "u-2", Title: "T", Body: "B", Source: "budget"}, "budget:mesh:2026-10:warn"); err != nil || !inserted {
		t.Fatalf("same key for another recipient = (%v, %v), want a new row", inserted, err)
	}
	if n, _ := repo.CountByUser(ctx, "u-1", InboxFilter{}); n != 1 {
		t.Fatalf("u-1 inbox has %d rows, want 1", n)
	}

	if err := repo.Delete(ctx, "u-1", id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, inserted, err := repo.AddDeduped(ctx, NewNotification{UserID: "u-1", Title: "T", Body: "B", Source: "budget"}, "budget:mesh:2026-10:warn"); err != nil || inserted {
		t.Fatalf("AddDeduped after the user deleted the row = (%v, %v), want the key to stay claimed", inserted, err)
	}
}

func TestNotificationRepo_ClaimDedupSharesKeysWithAddDeduped(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))

	if claimed, err := repo.ClaimDedup(ctx, "u-1", "k"); err != nil || !claimed {
		t.Fatalf("first ClaimDedup = (%v, %v), want claimed", claimed, err)
	}
	if claimed, err := repo.ClaimDedup(ctx, "u-1", "k"); err != nil || claimed {
		t.Fatalf("repeat ClaimDedup = (%v, %v), want not claimed", claimed, err)
	}
	if _, inserted, err := repo.AddDeduped(ctx, NewNotification{UserID: "u-1", Title: "T", Body: "B", Source: "s"}, "k"); err != nil || inserted {
		t.Fatalf("AddDeduped on a claimed key = (%v, %v), want no row", inserted, err)
	}
	if n, _ := repo.CountByUser(ctx, "u-1", InboxFilter{}); n != 0 {
		t.Fatalf("inbox has %d rows, want 0", n)
	}
}

func TestNotificationRepo_DeleteDedupKeysOlderThanRearmsOldKeys(t *testing.T) {
	ctx := t.Context()
	repo := NewNotificationRepo(openTestDB(t))

	if _, err := repo.ClaimDedup(ctx, "u-1", "k"); err != nil {
		t.Fatalf("ClaimDedup: %v", err)
	}
	if n, err := repo.DeleteDedupKeysOlderThan(ctx, time.Now().Add(-time.Hour).UnixMilli()); err != nil || n != 0 {
		t.Fatalf("pruning before the key's age = (%d, %v), want nothing deleted", n, err)
	}
	if n, err := repo.DeleteDedupKeysOlderThan(ctx, time.Now().Add(time.Hour).UnixMilli()); err != nil || n != 1 {
		t.Fatalf("pruning past the key's age = (%d, %v), want 1 deleted", n, err)
	}
	if claimed, err := repo.ClaimDedup(ctx, "u-1", "k"); err != nil || !claimed {
		t.Fatalf("ClaimDedup after pruning = (%v, %v), want claimed", claimed, err)
	}
}
