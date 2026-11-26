// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func setupShareForArtifacts(t *testing.T) (*DB, *SharedArtifactRepo) {
	t.Helper()
	db := newTestDB(t)
	linkRepo := NewSharedLinkRepo(db)

	if err := linkRepo.Create(context.Background(), &SharedLink{
		ShareID:   "share-art",
		SessionID: "00000000-0000-0000-0000-00000000aa01",
		UserID:    "owner",
	}); err != nil {
		t.Fatalf("create shared link: %v", err)
	}
	return db, NewSharedArtifactRepo(db)
}

func TestSharedArtifactRepo_CreateAndList(t *testing.T) {
	_, repo := setupShareForArtifacts(t)
	ctx := context.Background()

	ver := int64(1)
	a1 := &SharedArtifact{ShareID: "share-art", ArtifactURI: "file1.txt", ArtifactVersion: &ver, IsPublic: true}
	a2 := &SharedArtifact{ShareID: "share-art", ArtifactURI: "file2.png", IsPublic: true}

	if err := repo.Create(ctx, a1); err != nil {
		t.Fatalf("Create a1: %v", err)
	}
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("Create a2: %v", err)
	}

	if a1.CreatedTime == 0 {
		t.Error("expected auto-generated CreatedTime for a1")
	}

	list, err := repo.ListByShareID(ctx, "share-art")
	if err != nil {
		t.Fatalf("ListByShareID: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(list))
	}
}

func TestSharedArtifactRepo_DeleteByShareID(t *testing.T) {
	_, repo := setupShareForArtifacts(t)
	ctx := context.Background()

	_ = repo.Create(ctx, &SharedArtifact{ShareID: "share-art", ArtifactURI: "file.txt", IsPublic: true})
	_ = repo.Create(ctx, &SharedArtifact{ShareID: "share-art", ArtifactURI: "file2.txt", IsPublic: true})

	if err := repo.DeleteByShareID(ctx, "share-art"); err != nil {
		t.Fatalf("DeleteByShareID: %v", err)
	}

	list, _ := repo.ListByShareID(ctx, "share-art")
	if len(list) != 0 {
		t.Errorf("expected 0 artifacts after delete, got %d", len(list))
	}
}

func TestSharedArtifactRepo_CascadeDelete(t *testing.T) {
	db, repo := setupShareForArtifacts(t)
	ctx := context.Background()

	_ = repo.Create(ctx, &SharedArtifact{ShareID: "share-art", ArtifactURI: "file.txt", IsPublic: true})

	_, _ = db.ExecContext(ctx, `DELETE FROM shared_links WHERE share_id = ?`, "share-art")

	list, _ := repo.ListByShareID(ctx, "share-art")
	if len(list) != 0 {
		t.Errorf("expected 0 artifacts after cascade delete, got %d", len(list))
	}
}

func TestSharedArtifactRepo_ListByShareID_Empty(t *testing.T) {
	_, repo := setupShareForArtifacts(t)

	list, err := repo.ListByShareID(context.Background(), "share-art")
	if err != nil {
		t.Fatalf("ListByShareID: %v", err)
	}
	if list != nil {
		t.Errorf("expected nil for empty list, got %v", list)
	}
}
