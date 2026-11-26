// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func TestPromptGroupRepo_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	repo := NewPromptGroupRepo(db)
	ctx := context.Background()

	g := &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "My Group", UserID: "u1"}
	if err := repo.Create(ctx, g); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "50000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected prompt group")
	}
	if got.Name != "My Group" {
		t.Errorf("name = %s, want My Group", got.Name)
	}
	if got.UserID != "u1" {
		t.Errorf("user_id = %s, want u1", got.UserID)
	}
	if got.CreatedAt == 0 {
		t.Error("expected created_at to be set")
	}
}

func TestPromptGroupRepo_ListByUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewPromptGroupRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group 1", UserID: "u1"})
	_ = repo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000002", Name: "Group 2", UserID: "u1"})

	groups, total, err := repo.ListByUser(ctx, "u1", nil, "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(groups) != 2 {
		t.Errorf("len = %d, want 2", len(groups))
	}
}

func TestPromptGroupRepo_ListByUser_SearchIsCaseInsensitiveAndEscaped(t *testing.T) {
	db := newTestDB(t)
	repo := NewPromptGroupRepo(db)
	ctx := context.Background()

	desc := "Handles 50% discounts"
	_ = repo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Weather Prompts", UserID: "u1"})
	_ = repo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000002", Name: "Billing", UserID: "u1", Description: &desc})

	for _, query := range []string{"weather", "WEATHER", "Weather"} {
		groups, total, err := repo.ListByUser(ctx, "u1", nil, "", query, 10, 0)
		if err != nil {
			t.Errorf("ListByUser(search=%q): %v", query, err)
			continue
		}
		if total != 1 || len(groups) != 1 {
			t.Errorf("ListByUser(search=%q): total = %d len = %d, want 1 and 1", query, total, len(groups))
		}
	}

	groups, total, err := repo.ListByUser(ctx, "u1", nil, "", "%", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(groups) != 1 {
		t.Fatalf("ListByUser(search=%%): total = %d len = %d, want 1 and 1", total, len(groups))
	}
	if groups[0].Name != "Billing" {
		t.Errorf("ListByUser(search=%%): matched %q, want Billing", groups[0].Name)
	}
}

func TestPromptGroupRepo_Update(t *testing.T) {
	db := newTestDB(t)
	repo := NewPromptGroupRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Original", UserID: "u1"})

	got, _ := repo.GetByID(ctx, "50000000-0000-0000-0000-000000000001")
	got.Name = "Updated"
	desc := "new description"
	got.Description = &desc

	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}

	got, _ = repo.GetByID(ctx, "50000000-0000-0000-0000-000000000001")
	if got.Name != "Updated" {
		t.Errorf("name = %s, want Updated", got.Name)
	}
	if got.Description == nil || *got.Description != "new description" {
		t.Errorf("description = %v, want 'new description'", got.Description)
	}
}

func TestPromptGroupRepo_Delete(t *testing.T) {
	db := newTestDB(t)
	repo := NewPromptGroupRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "To Delete", UserID: "u1"})

	if err := repo.Delete(ctx, "50000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "50000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestPromptGroupRepo_CommandExistsForUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewPromptGroupRepo(db)
	ctx := context.Background()

	cmd := "/test"
	_ = repo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group 1", UserID: "u1", Command: &cmd})

	exists, err := repo.CommandExistsForUser(ctx, "/test", "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("expected command to exist")
	}

	exists, err = repo.CommandExistsForUser(ctx, "/test", "u1", "50000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected command not to exist when excluding the owning group")
	}

	exists, err = repo.CommandExistsForUser(ctx, "/test", "u2", "")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected command not to exist for different user")
	}
}
