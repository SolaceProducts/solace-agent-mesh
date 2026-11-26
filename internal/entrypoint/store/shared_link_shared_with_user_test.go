// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func seedShareGrantedTo(t *testing.T, linkRepo *SharedLinkRepo, userRepo *SharedLinkUserRepo, shareID, owner, email string, addedAt int64) {
	t.Helper()
	ctx := context.Background()
	if err := linkRepo.Create(ctx, &SharedLink{
		ShareID:   shareID,
		SessionID: "00000000-0000-0000-0000-00000000000a",
		UserID:    owner,
	}); err != nil {
		t.Fatalf("create shared link %s: %v", shareID, err)
	}
	if _, err := userRepo.Add(ctx, &SharedLinkUser{
		ShareID:       shareID,
		UserEmail:     email,
		AccessLevel:   "resource_viewer",
		AddedAt:       addedAt,
		AddedByUserID: owner,
	}); err != nil {
		t.Fatalf("grant %s to %s: %v", shareID, email, err)
	}
}

func TestSharedLinkRepo_ListSharedWithUserOrdersByShareTime(t *testing.T) {
	db := newTestDB(t)
	linkRepo := NewSharedLinkRepo(db)
	userRepo := NewSharedLinkUserRepo(db)
	const email = "recipient@example.com"

	seedShareGrantedTo(t, linkRepo, userRepo, "first", "owner", email, 1000)
	seedShareGrantedTo(t, linkRepo, userRepo, "second", "owner", email, 3000)
	seedShareGrantedTo(t, linkRepo, userRepo, "third", "owner", email, 2000)

	rows, err := linkRepo.ListSharedWithUser(context.Background(), email, 10, 0)
	if err != nil {
		t.Fatalf("ListSharedWithUser: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.ShareID)
	}

	want := []string{"second", "third", "first"}
	if len(got) != len(want) {
		t.Fatalf("got %d rows (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("share order = %v, want %v (most recently shared first)", got, want)
		}
	}
}

func TestSharedLinkRepo_ListSharedWithUserExcludesSoftDeleted(t *testing.T) {
	db := newTestDB(t)
	linkRepo := NewSharedLinkRepo(db)
	userRepo := NewSharedLinkUserRepo(db)
	ctx := context.Background()
	const email = "recipient@example.com"

	seedShareGrantedTo(t, linkRepo, userRepo, "kept", "owner", email, 1000)
	seedShareGrantedTo(t, linkRepo, userRepo, "removed", "owner", email, 2000)
	if err := linkRepo.SoftDelete(ctx, "removed"); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	rows, err := linkRepo.ListSharedWithUser(ctx, email, 10, 0)
	if err != nil {
		t.Fatalf("ListSharedWithUser: %v", err)
	}
	if len(rows) != 1 || rows[0].ShareID != "kept" {
		t.Fatalf("a soft-deleted share must not be listed, got %+v", rows)
	}
}

func TestSharedLinkRepo_ListSharedWithUserProjectsOwnGrant(t *testing.T) {
	db := newTestDB(t)
	linkRepo := NewSharedLinkRepo(db)
	userRepo := NewSharedLinkUserRepo(db)
	ctx := context.Background()

	if err := linkRepo.Create(ctx, &SharedLink{
		ShareID: "shared", SessionID: "00000000-0000-0000-0000-00000000000a", UserID: "owner",
	}); err != nil {
		t.Fatalf("create shared link: %v", err)
	}
	addUsers(t, userRepo, "shared", []SharedLinkUser{
		{UserEmail: "viewer@example.com", AccessLevel: "resource_viewer", AddedAt: 1000, AddedByUserID: "owner"},
		{UserEmail: "editor@example.com", AccessLevel: "resource_editor", AddedAt: 2000, AddedByUserID: "owner"},
	})
	if _, err := userRepo.MarkViewed(ctx, "shared", "editor@example.com", 2500); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}

	viewer, err := linkRepo.ListSharedWithUser(ctx, "viewer@example.com", 10, 0)
	if err != nil {
		t.Fatalf("ListSharedWithUser(viewer): %v", err)
	}
	if len(viewer) != 1 {
		t.Fatalf("viewer should see exactly one share, got %d", len(viewer))
	}
	if viewer[0].AccessLevel != "resource_viewer" || viewer[0].AddedAt != 1000 {
		t.Fatalf("viewer got the wrong grant columns: %+v", viewer[0])
	}
	if viewer[0].ViewedAt != nil {
		t.Fatalf("viewer never opened the share, got viewedAt=%d", *viewer[0].ViewedAt)
	}
	if viewer[0].OwnerUserID != "owner" {
		t.Fatalf("ownerUserID = %q, want %q", viewer[0].OwnerUserID, "owner")
	}

	editor, err := linkRepo.ListSharedWithUser(ctx, "editor@example.com", 10, 0)
	if err != nil {
		t.Fatalf("ListSharedWithUser(editor): %v", err)
	}
	if len(editor) != 1 {
		t.Fatalf("editor should see exactly one share, got %d", len(editor))
	}
	if editor[0].AccessLevel != "resource_editor" || editor[0].AddedAt != 2000 {
		t.Fatalf("editor got the wrong grant columns: %+v", editor[0])
	}
	if editor[0].ViewedAt == nil {
		t.Fatal("editor viewedAt must be set, got nil")
	} else if *editor[0].ViewedAt != 2500 {
		t.Fatalf("editor viewedAt = %d, want 2500", *editor[0].ViewedAt)
	}
}

func TestSharedLinkRepo_CountSharedWithUser(t *testing.T) {
	db := newTestDB(t)
	linkRepo := NewSharedLinkRepo(db)
	userRepo := NewSharedLinkUserRepo(db)
	ctx := context.Background()
	const email = "recipient@example.com"

	seedShareGrantedTo(t, linkRepo, userRepo, "one", "owner", email, 1000)
	seedShareGrantedTo(t, linkRepo, userRepo, "two", "owner", email, 2000)
	seedShareGrantedTo(t, linkRepo, userRepo, "other", "owner", "someone-else@example.com", 3000)

	total, err := linkRepo.CountSharedWithUser(ctx, email)
	if err != nil {
		t.Fatalf("CountSharedWithUser: %v", err)
	}
	if total != 2 {
		t.Fatalf("count = %d, want 2 (another recipient's grant must not count)", total)
	}

	rows, err := linkRepo.ListSharedWithUser(ctx, email, 100, 0)
	if err != nil {
		t.Fatalf("ListSharedWithUser: %v", err)
	}
	if total != len(rows) {
		t.Fatalf("count %d disagrees with listed rows %d", total, len(rows))
	}
}

func TestSharedLinkRepo_ListSharedWithUserPaginates(t *testing.T) {
	db := newTestDB(t)
	linkRepo := NewSharedLinkRepo(db)
	userRepo := NewSharedLinkUserRepo(db)
	const email = "recipient@example.com"

	seedShareGrantedTo(t, linkRepo, userRepo, "newest", "owner", email, 3000)
	seedShareGrantedTo(t, linkRepo, userRepo, "middle", "owner", email, 2000)
	seedShareGrantedTo(t, linkRepo, userRepo, "oldest", "owner", email, 1000)

	page, err := linkRepo.ListSharedWithUser(context.Background(), email, 2, 1)
	if err != nil {
		t.Fatalf("ListSharedWithUser: %v", err)
	}
	if len(page) != 2 || page[0].ShareID != "middle" || page[1].ShareID != "oldest" {
		got := make([]string, 0, len(page))
		for _, r := range page {
			got = append(got, r.ShareID)
		}
		t.Fatalf("limit/offset window wrong, got %v want [middle oldest]", got)
	}
}
