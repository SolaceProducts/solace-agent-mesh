// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestProjectRepo_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	p := &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Test Project", UserID: "u1"}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "30000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected project")
	}
	if got.Name != "Test Project" {
		t.Errorf("name = %s, want Test Project", got.Name)
	}
	if got.UserID != "u1" {
		t.Errorf("user_id = %s, want u1", got.UserID)
	}
	if got.CreatedAt == 0 {
		t.Error("expected created_at to be set")
	}
}

func TestProjectRepo_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)

	got, err := repo.GetByID(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil")
	}
}

func TestProjectRepo_ListByUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Project 1", UserID: "u1"})
	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000002", Name: "Project 2", UserID: "u1"})
	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000003", Name: "Project 3", UserID: "u1"})

	projects, err := repo.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 {
		t.Errorf("len = %d, want 3", len(projects))
	}
}

func TestProjectRepo_Update(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Original", UserID: "u1"})

	got, _ := repo.GetByID(ctx, "30000000-0000-0000-0000-000000000001")
	got.Name = "Updated"
	desc := "new description"
	got.Description = &desc

	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}

	got, _ = repo.GetByID(ctx, "30000000-0000-0000-0000-000000000001")
	if got.Name != "Updated" {
		t.Errorf("name = %s, want Updated", got.Name)
	}
	if got.Description == nil || *got.Description != "new description" {
		t.Errorf("description = %v, want 'new description'", got.Description)
	}
	if got.UpdatedAt == nil {
		t.Error("expected updated_at to be set")
	}
}

func TestProjectRepo_SoftDelete(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Project 1", UserID: "u1"})
	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000002", Name: "Project 2", UserID: "u1"})

	if err := repo.SoftDelete(ctx, "30000000-0000-0000-0000-000000000001", "admin"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "30000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for soft-deleted project")
	}

	projects, err := repo.ListByUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Errorf("len = %d, want 1", len(projects))
	}
	if projects[0].ID != "30000000-0000-0000-0000-000000000002" {
		t.Errorf("expected 30000000-0000-0000-0000-000000000002, got %s", projects[0].ID)
	}
}

func TestProjectRepo_NameExistsForUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "My Project", UserID: "u1"})

	cases := []struct {
		name      string
		queryName string
		userID    string
		excludeID string
		want      bool
	}{
		{"exact match", "My Project", "u1", "", true},
		{"case-insensitive match", "my project", "u1", "", true},
		{"different name", "Other Project", "u1", "", false},
		{"different user", "My Project", "u2", "", false},
		{"excludes self", "My Project", "u1", "30000000-0000-0000-0000-000000000001", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.NameExistsForUser(ctx, tc.queryName, tc.userID, tc.excludeID)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("NameExistsForUser(%q, %q, %q) = %v, want %v", tc.queryName, tc.userID, tc.excludeID, got, tc.want)
			}
		})
	}

	if err := repo.SoftDelete(ctx, "30000000-0000-0000-0000-000000000001", "admin"); err != nil {
		t.Fatal(err)
	}
	exists, err := repo.NameExistsForUser(ctx, "My Project", "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("expected soft-deleted project name to be reusable")
	}
}

func TestProjectRepo_ListAccessible(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Project 1", UserID: "u1"})
	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000002", Name: "Project 2", UserID: "u1"})
	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000003", Name: "Project 3", UserID: "u2"})

	projects, err := repo.ListAccessible(ctx, "u1", []string{"30000000-0000-0000-0000-000000000003"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 {
		t.Errorf("expected 3 accessible projects, got %d", len(projects))
	}
}

func TestProjectRepo_ListAccessible_EmptySharedIDs(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Project 1", UserID: "u1"})

	projects, err := repo.ListAccessible(ctx, "u1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(projects))
	}
}

func TestProjectRepo_ListAccessible_ExcludesSoftDeleted(t *testing.T) {
	db := newTestDB(t)
	repo := NewProjectRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Project 1", UserID: "u1"})
	_ = repo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000002", Name: "Project 2", UserID: "u2"})
	_ = repo.SoftDelete(ctx, "30000000-0000-0000-0000-000000000002", "admin")

	projects, err := repo.ListAccessible(ctx, "u1", []string{"30000000-0000-0000-0000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Errorf("expected 1 project (soft-deleted excluded), got %d", len(projects))
	}
}

func TestProjectRepo_SoftDeleteSessionsByProjectForUsers(t *testing.T) {
	db := newTestDB(t)
	projRepo := NewProjectRepo(db)
	sessRepo := NewSessionRepo(db)
	ctx := context.Background()

	_ = projRepo.Create(ctx, &Project{ID: "30000000-0000-0000-0000-000000000001", Name: "Project 1", UserID: "owner"})
	pid := "30000000-0000-0000-0000-000000000001"
	_ = sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "alice", ProjectID: &pid})
	_ = sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "bob", ProjectID: &pid})
	_ = sessRepo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000003", UserID: "alice", ProjectID: &pid})

	err := projRepo.SoftDeleteSessionsByProjectForUsers(ctx, "30000000-0000-0000-0000-000000000001", []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}

	deletedAt := func(id string) sql.NullInt64 {
		var v sql.NullInt64
		row := db.QueryRowContext(ctx, `SELECT deleted_at FROM sessions WHERE id = ?`, id)
		if err := row.Scan(&v); err != nil {
			t.Fatalf("scan deleted_at(%s): %v", id, err)
		}
		return v
	}
	if !deletedAt("00000000-0000-0000-0000-000000000001").Valid {
		t.Error("expected alice's session s1 to have deleted_at set")
	}
	if !deletedAt("00000000-0000-0000-0000-000000000003").Valid {
		t.Error("expected alice's session s3 to have deleted_at set")
	}
	if deletedAt("00000000-0000-0000-0000-000000000002").Valid {
		t.Error("expected bob's session s2 to NOT have deleted_at set")
	}
}
