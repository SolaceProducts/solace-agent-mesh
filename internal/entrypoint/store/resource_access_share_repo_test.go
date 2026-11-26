// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestResourceAccessShareRepo_GetCollaborators_Empty(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)

	shares, err := repo.GetCollaborators(context.Background(), "30000000-0000-0000-0000-000000000001", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("expected 0 collaborators, got %d", len(shares))
	}
}

func TestResourceAccessShareRepo_BatchShare_CreateNew(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	inputs := []ShareInput{
		{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
		{UserEmail: "bob@example.com", AccessLevel: "resource_viewer"},
	}

	created, updated, err := repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project", inputs, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 created, got %d", len(created))
	}
	if len(updated) != 0 {
		t.Errorf("expected 0 updated, got %d", len(updated))
	}

	collaborators, err := repo.GetCollaborators(ctx, "30000000-0000-0000-0000-000000000001", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(collaborators) != 2 {
		t.Errorf("expected 2 collaborators, got %d", len(collaborators))
	}
}

func TestResourceAccessShareRepo_BatchShare_CreateReturnsCorrectFields(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	created, _, err := repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project",
		[]ShareInput{{UserEmail: "Alice@Example.COM", AccessLevel: "resource_viewer"}},
		"owner@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(created))
	}

	c := created[0]
	if c.ID == "" {
		t.Error("created share ID should not be empty")
	}
	if c.ResourceID != "30000000-0000-0000-0000-000000000001" {
		t.Errorf("ResourceID = %q", c.ResourceID)
	}
	if c.SharedWithUserEmail != "alice@example.com" {
		t.Errorf("email should be lowercased, got %q", c.SharedWithUserEmail)
	}
	if c.AccessLevel != "resource_viewer" {
		t.Errorf("AccessLevel = %q", c.AccessLevel)
	}
	if c.SharedByUserEmail != "owner@example.com" {
		t.Errorf("SharedByUserEmail = %q", c.SharedByUserEmail)
	}
	if c.SharedAt == 0 {
		t.Error("SharedAt should be set")
	}

	collaborators, err := repo.GetCollaborators(ctx, "30000000-0000-0000-0000-000000000001", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(collaborators) != 1 {
		t.Fatalf("expected 1 collaborator, got %d", len(collaborators))
	}
	if collaborators[0].ID != c.ID {
		t.Errorf("returned ID %q does not match DB row ID %q", c.ID, collaborators[0].ID)
	}
}

func TestResourceAccessShareRepo_BatchShare_UpdateReturnsCorrectFields(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	created, _, err := repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"}},
		"owner@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("expected 1 created, got %d", len(created))
	}
	originalID := created[0].ID
	originalSharedAt := created[0].SharedAt

	created2, updated, err := repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_editor"}},
		"owner@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(created2) != 0 {
		t.Errorf("expected 0 created, got %d", len(created2))
	}
	if len(updated) != 1 {
		t.Fatalf("expected 1 updated, got %d", len(updated))
	}

	u := updated[0]

	if u.ID != originalID {
		t.Errorf("updated ID = %q, want original %q (must not generate a new ID on conflict)", u.ID, originalID)
	}

	if u.SharedAt != originalSharedAt {
		t.Errorf("updated SharedAt = %d, want original %d", u.SharedAt, originalSharedAt)
	}

	if u.AccessLevel != "resource_editor" {
		t.Errorf("updated AccessLevel = %q, want resource_editor", u.AccessLevel)
	}

	if u.UpdatedAt == nil {
		t.Error("updated share should have UpdatedAt set")
	}

	collaborators, err := repo.GetCollaborators(ctx, "30000000-0000-0000-0000-000000000001", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(collaborators) != 1 {
		t.Fatalf("expected 1 collaborator, got %d", len(collaborators))
	}
	dbRow := collaborators[0]
	if dbRow.ID != u.ID {
		t.Errorf("returned ID %q does not match DB row ID %q", u.ID, dbRow.ID)
	}
	if dbRow.AccessLevel != "resource_editor" {
		t.Errorf("DB access level = %q, want resource_editor", dbRow.AccessLevel)
	}
	if dbRow.SharedAt != originalSharedAt {
		t.Errorf("DB SharedAt = %d, want original %d (upsert should not overwrite shared_at)", dbRow.SharedAt, originalSharedAt)
	}
}

func TestResourceAccessShareRepo_BatchUnshare(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	_, _, _ = repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project",
		[]ShareInput{
			{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
			{UserEmail: "bob@example.com", AccessLevel: "resource_viewer"},
		},
		"owner@example.com",
	)

	count, emails, err := repo.BatchUnshare(ctx, "30000000-0000-0000-0000-000000000001", "project", []string{"alice@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 deleted, got %d", count)
	}
	if len(emails) != 1 || emails[0] != "alice@example.com" {
		t.Errorf("expected [alice@example.com], got %v", emails)
	}

	has, _ := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "project", "bob@example.com")
	if !has {
		t.Error("expected bob to still have access")
	}
	has, _ = repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "project", "alice@example.com")
	if has {
		t.Error("expected alice to no longer have access")
	}
}

func TestResourceAccessShareRepo_BatchUnshare_NonExistent(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)

	count, emails, err := repo.BatchUnshare(context.Background(), "30000000-0000-0000-0000-000000000001", "project", []string{"nobody@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 deleted, got %d", count)
	}
	if len(emails) != 0 {
		t.Errorf("expected 0 emails, got %v", emails)
	}
}

func TestResourceAccessShareRepo_GetSharedResourceIDs(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	_, _, _ = repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"}}, "owner@example.com")
	_, _, _ = repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000002", "project",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"}}, "owner@example.com")
	_, _, _ = repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000003", "project",
		[]ShareInput{{UserEmail: "bob@example.com", AccessLevel: "resource_viewer"}}, "owner@example.com")

	ids, err := repo.GetSharedResourceIDs(ctx, "alice@example.com", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 shared IDs for alice, got %d", len(ids))
	}

	ids, err = repo.GetSharedResourceIDs(ctx, "bob@example.com", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 shared ID for bob, got %d", len(ids))
	}
}

func TestResourceAccessShareRepo_HasAccess(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	_, _, _ = repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"}}, "owner@example.com")

	has, err := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "project", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected alice to have access")
	}

	has, err = repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "project", "nobody@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("expected nobody to not have access")
	}
}

func TestResourceAccessShareRepo_HasAccess_CaseInsensitive(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	_, _, _ = repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project",
		[]ShareInput{{UserEmail: "Alice@Example.COM", AccessLevel: "resource_viewer"}}, "owner@example.com")

	has, err := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "project", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected case-insensitive access check to work")
	}
}

func TestResourceAccessShareRepo_DeleteAllForResource(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	_, _, _ = repo.BatchShare(ctx, "30000000-0000-0000-0000-000000000001", "project",
		[]ShareInput{
			{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"},
			{UserEmail: "bob@example.com", AccessLevel: "resource_viewer"},
		}, "owner@example.com")

	err := repo.DeleteAllForResource(ctx, "30000000-0000-0000-0000-000000000001", "project")
	if err != nil {
		t.Fatal(err)
	}

	collaborators, _ := repo.GetCollaborators(ctx, "30000000-0000-0000-0000-000000000001", "project")
	if len(collaborators) != 0 {
		t.Errorf("expected 0 collaborators after delete all, got %d", len(collaborators))
	}
}

func TestResourceAccessShareRepo_GetUserAccessLevels(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	const (
		projectID1 = "30000000-0000-0000-0000-000000000001"
		projectID2 = "30000000-0000-0000-0000-000000000002"
		promptID   = "40000000-0000-0000-0000-000000000001"
	)

	if _, _, err := repo.BatchShare(ctx, projectID1, "project",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"}}, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BatchShare(ctx, projectID2, "project",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_editor"}}, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BatchShare(ctx, promptID, "prompt_group",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"}}, "owner@example.com"); err != nil {
		t.Fatal(err)
	}

	t.Run("no shares returns empty map", func(t *testing.T) {
		got, err := repo.GetUserAccessLevels(ctx, "stranger@example.com", "project")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty map", got)
		}
	})

	t.Run("returns id->level map for matching resource_type", func(t *testing.T) {
		got, err := repo.GetUserAccessLevels(ctx, "alice@example.com", "project")
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			projectID1: "resource_viewer",
			projectID2: "resource_editor",
		}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for id, level := range want {
			if got[id] != level {
				t.Errorf("got[%q] = %q, want %q", id, got[id], level)
			}
		}
	})

	t.Run("filters by resource_type (no leakage)", func(t *testing.T) {
		got, err := repo.GetUserAccessLevels(ctx, "alice@example.com", "prompt_group")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[promptID] != "resource_viewer" {
			t.Errorf("got %v, want {%q: resource_viewer} (project shares must not leak)", got, promptID)
		}
	})

	t.Run("email lookup is case-insensitive", func(t *testing.T) {
		got, err := repo.GetUserAccessLevels(ctx, "ALICE@EXAMPLE.COM", "project")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("got %d entries, want 2 (case-insensitive lookup failed)", len(got))
		}
	})
}

func TestResourceAccessShareRepo_BatchShareTx_RollbackPersistsNothing(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	const resourceID = "30000000-0000-0000-0000-0000000000aa"
	rollbackSentinel := errors.New("simulated downstream failure")

	err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
		_, _, batchErr := repo.BatchShareTx(ctx, tx, resourceID, "prompt_group",
			[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"}},
			"owner@example.com")
		if batchErr != nil {
			return batchErr
		}
		return rollbackSentinel
	})
	if !errors.Is(err, rollbackSentinel) {
		t.Fatalf("WithTx err = %v, want sentinel", err)
	}

	shares, err := repo.GetCollaborators(ctx, resourceID, "prompt_group")
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Errorf("len(shares) after rollback = %d, want 0 (upsert must not commit when tx rolls back)", len(shares))
	}
}

func TestResourceAccessShareRepo_BatchUnshareTx_RollbackKeepsRows(t *testing.T) {
	db := newTestDB(t)
	repo := NewResourceAccessShareRepo(db)
	ctx := context.Background()

	const resourceID = "30000000-0000-0000-0000-0000000000bb"
	if _, _, err := repo.BatchShare(ctx, resourceID, "prompt_group",
		[]ShareInput{{UserEmail: "alice@example.com", AccessLevel: "resource_viewer"}},
		"owner@example.com"); err != nil {
		t.Fatal(err)
	}

	rollbackSentinel := errors.New("simulated downstream failure")
	err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, _, delErr := repo.BatchUnshareTx(ctx, tx, resourceID, "prompt_group",
			[]string{"alice@example.com"}); delErr != nil {
			return delErr
		}
		return rollbackSentinel
	})
	if !errors.Is(err, rollbackSentinel) {
		t.Fatalf("WithTx err = %v, want sentinel", err)
	}

	shares, err := repo.GetCollaborators(ctx, resourceID, "prompt_group")
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 {
		t.Errorf("len(shares) after rollback = %d, want 1 (delete must not commit when tx rolls back)", len(shares))
	}
}
