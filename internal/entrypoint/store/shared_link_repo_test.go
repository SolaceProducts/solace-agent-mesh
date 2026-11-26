// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"strings"
	"testing"
)

func TestSharedLinkRepo_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	title := "Test Chat"
	link := &SharedLink{
		ShareID:               "share-abc-123",
		SessionID:             "00000000-0000-0000-0000-000000000001",
		UserID:                "user-1",
		Title:                 &title,
		IsPublic:              true,
		RequireAuthentication: true,
	}
	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if link.CreatedTime == 0 {
		t.Fatal("expected auto-generated CreatedTime")
	}

	got, err := repo.GetByID(ctx, "share-abc-123")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil shared link")
	}
	if got.SessionID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("SessionID = %q, want 00000000-0000-0000-0000-000000000001", got.SessionID)
	}
	if !got.IsPublic {
		t.Error("expected IsPublic = true")
	}
	if !got.RequireAuthentication {
		t.Error("expected RequireAuthentication = true")
	}
	if got.Title == nil || *got.Title != "Test Chat" {
		t.Errorf("Title = %v, want 'Test Chat'", got.Title)
	}
}

func TestSharedLinkRepo_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)

	got, err := repo.GetByID(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent share")
	}
}

func TestSharedLinkRepo_GetBySessionAndUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	link := &SharedLink{
		ShareID:   "share-1",
		SessionID: "00000000-0000-0000-0000-000000000001",
		UserID:    "user-A",
		IsPublic:  true,
	}
	_ = repo.Create(ctx, link)

	got, err := repo.GetBySessionAndUser(ctx, "00000000-0000-0000-0000-000000000001", "user-A")
	if err != nil {
		t.Fatalf("GetBySessionAndUser: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.ShareID != "share-1" {
		t.Errorf("ShareID = %q, want share-1", got.ShareID)
	}

	got, err = repo.GetBySessionAndUser(ctx, "00000000-0000-0000-0000-000000000001", "user-B")
	if err != nil {
		t.Fatalf("GetBySessionAndUser: %v", err)
	}
	if got != nil {
		t.Error("expected nil for different user")
	}
}

func TestSharedLinkRepo_ListByUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &SharedLink{ShareID: "s1", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "user-1"})
	_ = repo.Create(ctx, &SharedLink{ShareID: "s2", SessionID: "00000000-0000-0000-0000-000000000002", UserID: "user-1"})
	_ = repo.Create(ctx, &SharedLink{ShareID: "s3", SessionID: "00000000-0000-0000-0000-000000000003", UserID: "user-2"})

	links, err := repo.ListByUser(ctx, "user-1", 10, 0)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("expected 2 links for user-1, got %d", len(links))
	}
}

func TestSharedLinkRepo_Update(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	link := &SharedLink{ShareID: "share-upd", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "user"}
	_ = repo.Create(ctx, link)

	title := "Updated Title"
	domains := "example.com,test.com"
	link.Title = &title
	link.AllowedDomains = &domains
	link.RequireAuthentication = true

	if err := repo.Update(ctx, link); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := repo.GetByID(ctx, "share-upd")
	if got.Title == nil || *got.Title != "Updated Title" {
		t.Errorf("Title = %v, want 'Updated Title'", got.Title)
	}
	if got.AllowedDomains == nil || *got.AllowedDomains != "example.com,test.com" {
		t.Errorf("AllowedDomains = %v, want 'example.com,test.com'", got.AllowedDomains)
	}
	if !got.RequireAuthentication {
		t.Error("expected RequireAuthentication = true")
	}
}

func TestSharedLinkRepo_SoftDelete(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &SharedLink{ShareID: "share-del", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "user"})

	if err := repo.SoftDelete(ctx, "share-del"); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	got, _ := repo.GetByID(ctx, "share-del")
	if got != nil {
		t.Error("expected nil after soft delete")
	}

	got, _ = repo.GetBySessionAndUser(ctx, "00000000-0000-0000-0000-000000000001", "user")
	if got != nil {
		t.Error("expected nil after soft delete via GetBySessionAndUser")
	}
}

func TestSharedLinkRepo_SoftDelete_AllowsRecreate(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &SharedLink{ShareID: "share-v1", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "user"})
	_ = repo.SoftDelete(ctx, "share-v1")

	err := repo.Create(ctx, &SharedLink{ShareID: "share-v2", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "user"})
	if err != nil {
		t.Fatalf("Create after soft delete: %v", err)
	}

	got, _ := repo.GetBySessionAndUser(ctx, "00000000-0000-0000-0000-000000000001", "user")
	if got == nil || got.ShareID != "share-v2" {
		t.Errorf("expected share-v2, got %v", got)
	}
}

func TestSharedLinkRepo_CreateSetsSnapshotTime(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	link := &SharedLink{ShareID: "share-snap", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "user"}
	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if link.SnapshotTime != link.CreatedTime {
		t.Errorf("Create should freeze SnapshotTime at CreatedTime; snapshot=%d created=%d", link.SnapshotTime, link.CreatedTime)
	}

	got, _ := repo.GetByID(ctx, "share-snap")
	if got.SnapshotTime != link.CreatedTime {
		t.Errorf("persisted SnapshotTime = %d, want %d", got.SnapshotTime, link.CreatedTime)
	}
}

func TestSharedLinkRepo_UpdateLinkSnapshotTime(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	link := &SharedLink{ShareID: "share-up", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "user"}
	_ = repo.Create(ctx, link)

	ok, err := repo.UpdateLinkSnapshotTime(ctx, "share-up", link.CreatedTime+5000)
	if err != nil {
		t.Fatalf("UpdateLinkSnapshotTime: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for existing share")
	}
	got, _ := repo.GetByID(ctx, "share-up")
	if got.SnapshotTime != link.CreatedTime+5000 {
		t.Errorf("SnapshotTime = %d, want %d", got.SnapshotTime, link.CreatedTime+5000)
	}

	ok, err = repo.UpdateLinkSnapshotTime(ctx, "nonexistent", 123)
	if err != nil {
		t.Fatalf("UpdateLinkSnapshotTime(unknown): %v", err)
	}
	if ok {
		t.Error("expected ok=false for unknown share")
	}

	_ = repo.SoftDelete(ctx, "share-up")
	ok, err = repo.UpdateLinkSnapshotTime(ctx, "share-up", 999)
	if err != nil {
		t.Fatalf("UpdateLinkSnapshotTime(soft-deleted): %v", err)
	}
	if ok {
		t.Error("expected ok=false for soft-deleted share")
	}
}

func TestSharedLinkRepo_UpdateLinkSnapshotTime_DBError(t *testing.T) {
	db := newTestDB(t)
	repo := NewSharedLinkRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &SharedLink{ShareID: "share-err", SessionID: "00000000-0000-0000-0000-000000000001", UserID: "user"})
	if err := db.RawDB().Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	ok, err := repo.UpdateLinkSnapshotTime(ctx, "share-err", 123)
	if err == nil {
		t.Fatal("expected error after DB closed")
	}
	if ok {
		t.Error("expected ok=false on error")
	}
	if !strings.Contains(err.Error(), "UpdateLinkSnapshotTime") {
		t.Errorf("error should be wrapped with method context, got: %v", err)
	}
}
