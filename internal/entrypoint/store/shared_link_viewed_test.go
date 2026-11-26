// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"testing"
)

func unreadOf(t *testing.T, r *SharedLinkUserRepo, shareID, email string) bool {
	t.Helper()
	u, err := r.GetByShareAndEmail(context.Background(), shareID, email)
	if err != nil {
		t.Fatalf("GetByShareAndEmail(%s): %v", email, err)
	}
	if u == nil {
		t.Fatalf("grant %s/%s not found", shareID, email)
	}
	return u.ViewedAt == nil || *u.ViewedAt < u.AddedAt
}

func viewedAtOf(t *testing.T, r *SharedLinkUserRepo, shareID, email string) string {
	t.Helper()
	u, err := r.GetByShareAndEmail(context.Background(), shareID, email)
	if err != nil || u == nil {
		t.Fatalf("GetByShareAndEmail(%s): %v", email, err)
	}
	if u.ViewedAt == nil {
		return "nil"
	}
	return fmt.Sprintf("%d", *u.ViewedAt)
}

func TestSharedLinkUserRepo_AddStartsUnread(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedByUserID: "owner"},
	})

	u, err := userRepo.GetByShareAndEmail(context.Background(), "share-users", "recipient@example.com")
	if err != nil {
		t.Fatalf("GetByShareAndEmail: %v", err)
	}
	if u.ViewedAt != nil {
		t.Fatalf("a new grant must start unopened, got viewedAt=%d", *u.ViewedAt)
	}
	if !unreadOf(t, userRepo, "share-users", "recipient@example.com") {
		t.Fatal("a new grant must read as unread")
	}
}

func TestSharedLinkUserRepo_MarkViewed(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})

	ok, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 2000)
	if err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	if !ok {
		t.Fatal("MarkViewed on an existing grant must report the grant exists")
	}
	if unreadOf(t, userRepo, "share-users", "recipient@example.com") {
		t.Fatal("share must read as read after being marked viewed")
	}
}

func TestSharedLinkUserRepo_MarkViewedIsMonotone(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})
	if _, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 5000); err != nil {
		t.Fatalf("MarkViewed(5000): %v", err)
	}

	ok, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 2000)
	if err != nil {
		t.Fatalf("MarkViewed(2000): %v", err)
	}
	if !ok {
		t.Fatal("an out-of-order mark must still report the grant exists")
	}

	if got := viewedAtOf(t, userRepo, "share-users", "recipient@example.com"); got != "5000" {
		t.Fatalf("viewedAt must not move backwards, got %s want 5000", got)
	}
}

func TestSharedLinkUserRepo_MarkViewedIdempotent(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})
	if _, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 2000); err != nil {
		t.Fatalf("first MarkViewed: %v", err)
	}

	ok, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 2000)
	if err != nil {
		t.Fatalf("second MarkViewed: %v", err)
	}
	if !ok {
		t.Fatal("re-marking an already-read share must report the grant exists")
	}
}

func TestSharedLinkUserRepo_MarkViewedUnknownGrant(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedByUserID: "owner"},
	})

	cases := []struct{ name, shareID, email string }{
		{"unknown share", "no-such-share", "recipient@example.com"},
		{"email without a grant on this share", "share-users", "stranger@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := userRepo.MarkViewed(ctx, tc.shareID, tc.email, 2000)
			if err != nil {
				t.Fatalf("MarkViewed: %v", err)
			}
			if ok {
				t.Fatal("MarkViewed must report no grant so the handler can 404")
			}
		})
	}
}

func TestSharedLinkUserRepo_MarkViewedLowercasesEmail(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})

	ok, err := userRepo.MarkViewed(ctx, "share-users", "  ReCiPiEnT@Example.COM ", 2000)
	if err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	if !ok {
		t.Fatal("a mixed-case email must resolve to the lowercased stored grant")
	}
}

func TestSharedLinkUserRepo_MarkViewedIsPerRecipient(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "a@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
		{UserEmail: "b@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})
	if _, err := userRepo.MarkViewed(ctx, "share-users", "a@example.com", 2000); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}

	if unreadOf(t, userRepo, "share-users", "a@example.com") {
		t.Fatal("the recipient who opened the share must read as read")
	}
	if !unreadOf(t, userRepo, "share-users", "b@example.com") {
		t.Fatal("the other recipient must still read as unread")
	}
}

func TestSharedLinkUserRepo_UpdateSnapshotTimeRemarksUnread(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})
	if _, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 2000); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}

	if _, err := userRepo.UpdateSnapshotTime(ctx, "share-users", "recipient@example.com", 3000); err != nil {
		t.Fatalf("UpdateSnapshotTime: %v", err)
	}

	if !unreadOf(t, userRepo, "share-users", "recipient@example.com") {
		t.Fatal("advancing the snapshot must return the share to unread")
	}
}

func TestSharedLinkUserRepo_UpdateAccessLevelRemarksUnread(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})
	if _, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 2000); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}

	if _, err := userRepo.UpdateAccessLevel(ctx, "share-users", "recipient@example.com", "resource_editor"); err != nil {
		t.Fatalf("UpdateAccessLevel: %v", err)
	}

	if !unreadOf(t, userRepo, "share-users", "recipient@example.com") {
		t.Fatal("a role change must return the share to unread")
	}
}

func TestSharedLinkUserRepo_OwnerWriteRemarksUnreadDespiteEarlierTimestamp(t *testing.T) {
	ctx := context.Background()

	t.Run("snapshot advance", func(t *testing.T) {
		_, _, userRepo := setupShareWithUsers(t)
		addUsers(t, userRepo, "share-users", []SharedLinkUser{
			{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 500, AddedByUserID: "owner"},
		})
		if _, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 5000); err != nil {
			t.Fatalf("MarkViewed: %v", err)
		}
		if _, err := userRepo.UpdateSnapshotTime(ctx, "share-users", "recipient@example.com", 1000); err != nil {
			t.Fatalf("UpdateSnapshotTime: %v", err)
		}
		if !unreadOf(t, userRepo, "share-users", "recipient@example.com") {
			t.Fatalf("share must be unread after the owner advanced the snapshot, got viewedAt=%s",
				viewedAtOf(t, userRepo, "share-users", "recipient@example.com"))
		}
	})

	t.Run("role change", func(t *testing.T) {
		_, _, userRepo := setupShareWithUsers(t)
		addUsers(t, userRepo, "share-users", []SharedLinkUser{
			{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 500, AddedByUserID: "owner"},
		})
		if _, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 5000); err != nil {
			t.Fatalf("MarkViewed: %v", err)
		}
		if _, err := userRepo.UpdateAccessLevel(ctx, "share-users", "recipient@example.com", "resource_editor"); err != nil {
			t.Fatalf("UpdateAccessLevel: %v", err)
		}
		if !unreadOf(t, userRepo, "share-users", "recipient@example.com") {
			t.Fatalf("share must be unread after a role change, got viewedAt=%s",
				viewedAtOf(t, userRepo, "share-users", "recipient@example.com"))
		}
	})
}

func TestSharedLinkUserRepo_RefreshOwnSnapshotStaysRead(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})

	ok, err := userRepo.RefreshOwnSnapshot(ctx, "share-users", "recipient@example.com", 3000)
	if err != nil {
		t.Fatalf("RefreshOwnSnapshot: %v", err)
	}
	if !ok {
		t.Fatal("RefreshOwnSnapshot must report the grant exists")
	}
	if unreadOf(t, userRepo, "share-users", "recipient@example.com") {
		t.Fatalf("catching up must leave the share read, got viewedAt=%s",
			viewedAtOf(t, userRepo, "share-users", "recipient@example.com"))
	}
}

func TestSharedLinkUserRepo_MarkViewedIgnoresSoftDeletedShare(t *testing.T) {
	_, linkRepo, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "recipient@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
	})
	if err := linkRepo.SoftDelete(ctx, "share-users"); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	ok, err := userRepo.MarkViewed(ctx, "share-users", "recipient@example.com", 2000)
	if err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}
	if ok {
		t.Fatal("a soft-deleted share must report no live grant")
	}
}
