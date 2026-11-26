// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"
)

func TestProjectUserRepo_AddUserAndHasAccess(t *testing.T) {
	db := newTestDB(t)
	projectRepo := NewProjectRepo(db)
	repo := NewProjectUserRepo(db)
	ctx := context.Background()

	_ = projectRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Test", UserID: "owner"})

	pu := &ProjectUser{
		ProjectID:     "30000000-0000-0000-0000-000000000001",
		UserID:        "u1",
		Role:          "editor",
		AddedByUserID: "owner",
	}
	if err := repo.AddUser(ctx, pu); err != nil {
		t.Fatal(err)
	}

	if pu.ID == "" {
		t.Error("expected ID to be set")
	}
	if pu.AddedAt == 0 {
		t.Error("expected AddedAt to be set")
	}

	has, err := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected user u1 to have access")
	}
}

func TestProjectUserRepo_HasAccess_NoAccess(t *testing.T) {
	db := newTestDB(t)
	projectRepo := NewProjectRepo(db)
	repo := NewProjectUserRepo(db)
	ctx := context.Background()

	_ = projectRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Test", UserID: "owner"})

	has, err := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("expected no access for stranger")
	}
}

func TestProjectUserRepo_AddUser_ExplicitIDAndTimestamp(t *testing.T) {
	db := newTestDB(t)
	projectRepo := NewProjectRepo(db)
	repo := NewProjectUserRepo(db)
	ctx := context.Background()

	_ = projectRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Test", UserID: "owner"})

	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	pu := &ProjectUser{
		ID:            "80000000-0000-0000-0000-000000000001",
		ProjectID:     "30000000-0000-0000-0000-000000000001",
		UserID:        "u2",
		Role:          "viewer",
		AddedAt:       ts,
		AddedByUserID: "owner",
	}
	if err := repo.AddUser(ctx, pu); err != nil {
		t.Fatal(err)
	}
	if pu.ID != "80000000-0000-0000-0000-000000000001" {
		t.Errorf("ID = %s, want 80000000-0000-0000-0000-000000000001", pu.ID)
	}
	if pu.AddedAt != ts {
		t.Errorf("AddedAt = %d, want %d", pu.AddedAt, ts)
	}
}

func TestProjectUserRepo_RemoveByProject(t *testing.T) {
	db := newTestDB(t)
	projectRepo := NewProjectRepo(db)
	repo := NewProjectUserRepo(db)
	ctx := context.Background()

	_ = projectRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Test", UserID: "owner"})
	_ = repo.AddUser(ctx, &ProjectUser{ProjectID: "30000000-0000-0000-0000-000000000001", UserID: "u1", Role: "editor", AddedByUserID: "owner"})
	_ = repo.AddUser(ctx, &ProjectUser{ProjectID: "30000000-0000-0000-0000-000000000001", UserID: "u2", Role: "viewer", AddedByUserID: "owner"})

	has1, _ := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "u1")
	has2, _ := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "u2")
	if !has1 || !has2 {
		t.Fatal("expected both users to have access before removal")
	}

	if err := repo.RemoveByProject(ctx, "30000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	has1, _ = repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "u1")
	has2, _ = repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "u2")
	if has1 || has2 {
		t.Error("expected no access after RemoveByProject")
	}
}

func TestProjectUserRepo_RemoveByProject_NoUsers(t *testing.T) {
	db := newTestDB(t)
	projectRepo := NewProjectRepo(db)
	repo := NewProjectUserRepo(db)
	ctx := context.Background()

	_ = projectRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Test", UserID: "owner"})

	if err := repo.RemoveByProject(ctx, "30000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}
}

func TestProjectUserRepo_MultipleProjects(t *testing.T) {
	db := newTestDB(t)
	projectRepo := NewProjectRepo(db)
	repo := NewProjectUserRepo(db)
	ctx := context.Background()

	_ = projectRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Project 1", UserID: "owner"})
	_ = projectRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000002", Name: "Project 2", UserID: "owner"})

	_ = repo.AddUser(ctx, &ProjectUser{ProjectID: "30000000-0000-0000-0000-000000000001", UserID: "u1", Role: "editor", AddedByUserID: "owner"})
	_ = repo.AddUser(ctx, &ProjectUser{ProjectID: "30000000-0000-0000-0000-000000000002", UserID: "u1", Role: "viewer", AddedByUserID: "owner"})

	_ = repo.RemoveByProject(ctx, "30000000-0000-0000-0000-000000000001")

	has1, _ := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000001", "u1")
	has2, _ := repo.HasAccess(ctx, "30000000-0000-0000-0000-000000000002", "u1")
	if has1 {
		t.Error("expected no access to p1 after removal")
	}
	if !has2 {
		t.Error("expected u1 to still have access to p2")
	}
}
