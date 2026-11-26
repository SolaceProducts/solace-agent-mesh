// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func TestPromptRepo_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	groupRepo := NewPromptGroupRepo(db)
	repo := NewPromptRepo(db)
	ctx := context.Background()

	_ = groupRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"})

	p := &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "Hello world", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "60000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected prompt")
	}
	if got.PromptText != "Hello world" {
		t.Errorf("prompt_text = %s, want Hello world", got.PromptText)
	}
	if got.GroupID != "50000000-0000-0000-0000-000000000001" {
		t.Errorf("group_id = %s, want 50000000-0000-0000-0000-000000000001", got.GroupID)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
	if got.CreatedAt == 0 {
		t.Error("expected created_at to be set")
	}
}

func TestPromptRepo_ListByGroup(t *testing.T) {
	db := newTestDB(t)
	groupRepo := NewPromptGroupRepo(db)
	repo := NewPromptRepo(db)
	ctx := context.Background()

	_ = groupRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"})
	_ = repo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "v1", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1})
	_ = repo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000002", PromptText: "v2", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 2})

	prompts, err := repo.ListByGroup(ctx, "50000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Errorf("len = %d, want 2", len(prompts))
	}
}

func TestPromptRepo_MaxVersion(t *testing.T) {
	db := newTestDB(t)
	groupRepo := NewPromptGroupRepo(db)
	repo := NewPromptRepo(db)
	ctx := context.Background()

	_ = groupRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"})

	maxVer, err := repo.MaxVersion(ctx, "50000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if maxVer != 0 {
		t.Errorf("max_version = %d, want 0", maxVer)
	}

	_ = repo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "v1", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1})
	_ = repo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000002", PromptText: "v2", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 3})

	maxVer, err = repo.MaxVersion(ctx, "50000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if maxVer != 3 {
		t.Errorf("max_version = %d, want 3", maxVer)
	}
}

func TestPromptRepo_Update(t *testing.T) {
	db := newTestDB(t)
	groupRepo := NewPromptGroupRepo(db)
	repo := NewPromptRepo(db)
	ctx := context.Background()

	_ = groupRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"})
	_ = repo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "original", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1})

	got, _ := repo.GetByID(ctx, "60000000-0000-0000-0000-000000000001")
	got.PromptText = "updated"
	name := "new name"
	got.Name = &name

	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}

	got, _ = repo.GetByID(ctx, "60000000-0000-0000-0000-000000000001")
	if got.PromptText != "updated" {
		t.Errorf("prompt_text = %s, want updated", got.PromptText)
	}
	if got.Name == nil || *got.Name != "new name" {
		t.Errorf("name = %v, want 'new name'", got.Name)
	}
}

func TestPromptRepo_Delete(t *testing.T) {
	db := newTestDB(t)
	groupRepo := NewPromptGroupRepo(db)
	repo := NewPromptRepo(db)
	ctx := context.Background()

	_ = groupRepo.Create(ctx, &PromptGroup{ID: "50000000-0000-0000-0000-000000000001", Name: "Group", UserID: "u1"})
	_ = repo.Create(ctx, &Prompt{ID: "60000000-0000-0000-0000-000000000001", PromptText: "to delete", GroupID: "50000000-0000-0000-0000-000000000001", UserID: "u1", Version: 1})

	if err := repo.Delete(ctx, "60000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "60000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}
