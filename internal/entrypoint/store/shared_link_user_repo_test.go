// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func setupShareWithUsers(t *testing.T) (*DB, *SharedLinkRepo, *SharedLinkUserRepo) {
	t.Helper()
	db := newTestDB(t)
	linkRepo := NewSharedLinkRepo(db)
	userRepo := NewSharedLinkUserRepo(db)

	if err := linkRepo.Create(context.Background(), &SharedLink{
		ShareID:   "share-users",
		SessionID: "00000000-0000-0000-0000-000000000001",
		UserID:    "owner",
	}); err != nil {
		t.Fatalf("create shared link: %v", err)
	}
	return db, linkRepo, userRepo
}

func addUsers(t *testing.T, r *SharedLinkUserRepo, shareID string, users []SharedLinkUser) {
	t.Helper()
	ctx := context.Background()
	for i := range users {
		users[i].ShareID = shareID
		if _, err := r.Add(ctx, &users[i]); err != nil {
			t.Fatalf("Add(%s): %v", users[i].UserEmail, err)
		}
	}
}

func TestSharedLinkUserRepo_AddAndList(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "alice@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
		{UserEmail: "bob@example.com", AccessLevel: "RESOURCE_EDITOR", AddedByUserID: "owner"},
	})

	list, err := userRepo.ListByShareID(ctx, "share-users")
	if err != nil {
		t.Fatalf("ListByShareID: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 users, got %d", len(list))
	}

	for _, u := range list {
		if u.ID == "" {
			t.Error("expected auto-generated ID")
		}
	}
}

func TestSharedLinkUserRepo_UpdateAccessLevel_PreservesOriginals(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "alice@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
	})

	updated, err := userRepo.UpdateAccessLevel(ctx, "share-users", "alice@example.com", "RESOURCE_EDITOR")
	if err != nil {
		t.Fatalf("UpdateAccessLevel: %v", err)
	}
	if updated == nil {
		t.Fatal("UpdateAccessLevel: expected non-nil row")
	}
	if updated.AccessLevel != "RESOURCE_EDITOR" {
		t.Errorf("AccessLevel = %q, want RESOURCE_EDITOR", updated.AccessLevel)
	}
	if updated.OriginalAccessLevel == nil || *updated.OriginalAccessLevel != "RESOURCE_VIEWER" {
		t.Errorf("OriginalAccessLevel = %v, want RESOURCE_VIEWER", updated.OriginalAccessLevel)
	}
	if updated.OriginalAddedAt == nil {
		t.Error("expected OriginalAddedAt to be captured")
	}

	secondOriginalLevel := *updated.OriginalAccessLevel
	secondOriginalTime := *updated.OriginalAddedAt
	updated2, err := userRepo.UpdateAccessLevel(ctx, "share-users", "alice@example.com", "RESOURCE_VIEWER")
	if err != nil {
		t.Fatalf("UpdateAccessLevel 2: %v", err)
	}
	if updated2.OriginalAccessLevel == nil || *updated2.OriginalAccessLevel != secondOriginalLevel {
		t.Errorf("OriginalAccessLevel changed on 2nd update: got %v, want %q", updated2.OriginalAccessLevel, secondOriginalLevel)
	}
	if updated2.OriginalAddedAt == nil || *updated2.OriginalAddedAt != secondOriginalTime {
		t.Errorf("OriginalAddedAt changed on 2nd update: got %v, want %d", updated2.OriginalAddedAt, secondOriginalTime)
	}
}

func TestSharedLinkUserRepo_UpdateAccessLevel_NotFound(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	updated, err := userRepo.UpdateAccessLevel(ctx, "share-users", "ghost@example.com", "RESOURCE_EDITOR")
	if err != nil {
		t.Fatalf("UpdateAccessLevel returned error for missing row: %v", err)
	}
	if updated != nil {
		t.Fatalf("expected nil for missing row, got %+v", updated)
	}
}

func TestSharedLinkUserRepo_UpdateSnapshotTime(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "alice@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
	})

	ok, err := userRepo.UpdateSnapshotTime(ctx, "share-users", "alice@example.com", 999999)
	if err != nil {
		t.Fatalf("UpdateSnapshotTime: %v", err)
	}
	if !ok {
		t.Fatal("UpdateSnapshotTime returned false for existing row")
	}
	got, _ := userRepo.GetByShareAndEmail(ctx, "share-users", "alice@example.com")
	if got == nil || got.AddedAt != 999999 {
		t.Fatalf("AddedAt = %v, want 999999", got)
	}
	if got.OriginalAccessLevel != nil || got.OriginalAddedAt != nil {
		t.Errorf("snapshot update must not touch original_* columns; got %+v", got)
	}

	ok, _ = userRepo.UpdateSnapshotTime(ctx, "share-users", "ghost@example.com", 111)
	if ok {
		t.Error("expected ok=false for missing user")
	}
}

func TestSharedLinkUserRepo_BatchRemove(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "alice@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
		{UserEmail: "bob@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
		{UserEmail: "carol@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
	})

	n, err := userRepo.BatchRemove(ctx, "share-users", []string{"alice@example.com", "carol@example.com"})
	if err != nil {
		t.Fatalf("BatchRemove: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted count = %d, want 2", n)
	}

	list, _ := userRepo.ListByShareID(ctx, "share-users")
	if len(list) != 1 {
		t.Fatalf("expected 1 remaining user, got %d", len(list))
	}
	if list[0].UserEmail != "bob@example.com" {
		t.Errorf("remaining email = %q, want bob@example.com", list[0].UserEmail)
	}
}

func TestSharedLinkUserRepo_HasAccess(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "alice@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
	})

	has, err := userRepo.HasAccess(ctx, "share-users", "alice@example.com")
	if err != nil {
		t.Fatalf("HasAccess: %v", err)
	}
	if !has {
		t.Error("expected alice to have access")
	}

	has, _ = userRepo.HasAccess(ctx, "share-users", "nobody@example.com")
	if has {
		t.Error("expected nobody to not have access")
	}
}

func TestSharedLinkUserRepo_HasAny(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	has, err := userRepo.HasAny(ctx, "share-users")
	if err != nil {
		t.Fatalf("HasAny: %v", err)
	}
	if has {
		t.Error("expected no users initially")
	}

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "alice@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
	})

	has, _ = userRepo.HasAny(ctx, "share-users")
	if !has {
		t.Error("expected HasAny=true after Add")
	}
}

func TestSharedLinkUserRepo_GetByShareAndEmail(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "alice@example.com", AccessLevel: "RESOURCE_EDITOR", AddedByUserID: "owner"},
	})

	got, err := userRepo.GetByShareAndEmail(ctx, "share-users", "alice@example.com")
	if err != nil {
		t.Fatalf("GetByShareAndEmail: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.AccessLevel != "RESOURCE_EDITOR" {
		t.Errorf("AccessLevel = %q, want RESOURCE_EDITOR", got.AccessLevel)
	}

	got, _ = userRepo.GetByShareAndEmail(ctx, "share-users", "nonexistent@example.com")
	if got != nil {
		t.Error("expected nil for nonexistent email")
	}
}

func TestSharedLinkUserRepo_EmailCaseInsensitiveLookup(t *testing.T) {
	_, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "Alice@Example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
	})

	got, _ := userRepo.GetByShareAndEmail(ctx, "share-users", "ALICE@EXAMPLE.COM")
	if got == nil || got.UserEmail != "alice@example.com" {
		t.Fatalf("expected lowercased match, got %+v", got)
	}
}

func TestSharedLinkUserRepo_CascadeDelete(t *testing.T) {
	db, _, userRepo := setupShareWithUsers(t)
	ctx := context.Background()

	addUsers(t, userRepo, "share-users", []SharedLinkUser{
		{UserEmail: "alice@example.com", AccessLevel: "RESOURCE_VIEWER", AddedByUserID: "owner"},
	})

	_, _ = db.ExecContext(ctx, `DELETE FROM shared_links WHERE share_id = ?`, "share-users")

	list, _ := userRepo.ListByShareID(ctx, "share-users")
	if len(list) != 0 {
		t.Errorf("expected 0 users after cascade delete, got %d", len(list))
	}
}
