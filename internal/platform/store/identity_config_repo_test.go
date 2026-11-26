// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func TestPlatformIdentityConfigRepo_GetEmptyReturnsNil(t *testing.T) {
	db := TestDB(t)
	repo := NewPlatformIdentityConfigRepo(db)

	got, err := repo.Get(t.Context())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for unwritten singleton, got %+v", got)
	}
}

func TestPlatformIdentityConfigRepo_SetUpsertsAndUpdates(t *testing.T) {
	db := TestDB(t)
	repo := NewPlatformIdentityConfigRepo(db)
	ctx := t.Context()

	pkgRepo := NewToolPackageRepo(db)
	pkg1 := newTestPackage("identity-toolset-1")
	if err := pkgRepo.Create(ctx, pkg1); err != nil {
		t.Fatalf("create package 1: %v", err)
	}
	toolsetID := pkg1.ID
	if err := repo.Set(ctx, &toolsetID, "email", "alice@example.com"); err != nil {
		t.Fatalf("set initial: %v", err)
	}

	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if got == nil {
		t.Fatal("expected row after Set")
	}
	if got.ID != PlatformIdentityConfigSingletonID {
		t.Errorf("id = %q, want %q", got.ID, PlatformIdentityConfigSingletonID)
	}
	if got.ToolsetID == nil || *got.ToolsetID != toolsetID {
		t.Errorf("toolsetID = %v, want %q", got.ToolsetID, toolsetID)
	}
	if got.LookupKey != "email" {
		t.Errorf("lookupKey = %q, want email", got.LookupKey)
	}
	if got.CreatedBy != "alice@example.com" || got.UpdatedBy != "alice@example.com" {
		t.Errorf("audit fields = (%q,%q), want (alice@example.com,alice@example.com)", got.CreatedBy, got.UpdatedBy)
	}
	if got.CreatedTime == 0 || got.UpdatedTime == 0 {
		t.Errorf("expected non-zero timestamps, got created=%d updated=%d", got.CreatedTime, got.UpdatedTime)
	}

	pkg2 := newTestPackage("identity-toolset-2")
	if createErr := pkgRepo.Create(ctx, pkg2); createErr != nil {
		t.Fatalf("create package 2: %v", createErr)
	}
	newToolset := pkg2.ID
	if upErr := repo.Set(ctx, &newToolset, "preferred_username", "bob@example.com"); upErr != nil {
		t.Fatalf("set upsert: %v", upErr)
	}
	got2, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if got2.ToolsetID == nil || *got2.ToolsetID != newToolset {
		t.Errorf("upsert toolsetID = %v, want %q", got2.ToolsetID, newToolset)
	}
	if got2.LookupKey != "preferred_username" {
		t.Errorf("upsert lookupKey = %q, want preferred_username", got2.LookupKey)
	}
	if got2.UpdatedBy != "bob@example.com" {
		t.Errorf("upsert updatedBy = %q, want bob@example.com", got2.UpdatedBy)
	}
	if got2.CreatedBy != "alice@example.com" {
		t.Errorf("createdBy = %q, want alice@example.com (preserved across upsert)", got2.CreatedBy)
	}
}

func TestPlatformIdentityConfigRepo_ClearNullsToolset(t *testing.T) {
	db := TestDB(t)
	repo := NewPlatformIdentityConfigRepo(db)
	ctx := t.Context()

	pkgRepo := NewToolPackageRepo(db)
	pkg := newTestPackage("identity-toolset-clear")
	if err := pkgRepo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	toolsetID := pkg.ID
	if err := repo.Set(ctx, &toolsetID, "email", "alice@example.com"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := repo.Clear(ctx, "bob@example.com"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got == nil {
		t.Fatal("expected row to persist after clear (only toolset_id nullified)")
	}
	if got.ToolsetID != nil {
		t.Errorf("toolsetID after clear = %v, want nil", got.ToolsetID)
	}
	if got.LookupKey != "email" {
		t.Errorf("lookupKey changed across clear = %q, want email", got.LookupKey)
	}
	if got.UpdatedBy != "bob@example.com" {
		t.Errorf("updatedBy after clear = %q, want bob@example.com", got.UpdatedBy)
	}
}

func TestPlatformIdentityConfigRepo_ClearWhenUnsetIsNoop(t *testing.T) {
	db := TestDB(t)
	repo := NewPlatformIdentityConfigRepo(db)
	ctx := t.Context()

	if err := repo.Clear(ctx, "alice@example.com"); err != nil {
		t.Fatalf("clear on empty: %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil row after Clear on empty, got %+v", got)
	}
}

func TestPlatformIdentityConfigRepo_DeletePackageNullsToolset(t *testing.T) {
	db := TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("toolset_id FK ON DELETE SET NULL is Postgres-only; the SQLite migration is a no-op with no FK")
	}
	ctx := t.Context()
	repo := NewPlatformIdentityConfigRepo(db)

	pkgRepo := NewToolPackageRepo(db)
	pkg := newTestPackage("identity-toolset-fk")
	if err := pkgRepo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	toolsetID := pkg.ID
	if err := repo.Set(ctx, &toolsetID, "email", "alice@example.com"); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := pkgRepo.Delete(ctx, pkg.ID); err != nil {
		t.Fatalf("delete package: %v", err)
	}

	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got == nil {
		t.Fatal("identity config row must survive deletion of its wired tool package")
	}
	if got.ToolsetID != nil {
		t.Errorf("toolsetID after package delete = %v, want nil (ON DELETE SET NULL)", got.ToolsetID)
	}
	if got.LookupKey != "email" {
		t.Errorf("lookupKey after package delete = %q, want email (row otherwise intact)", got.LookupKey)
	}
}

func TestPlatformIdentityConfigRepo_SetWithNilToolsetID(t *testing.T) {
	db := TestDB(t)
	repo := NewPlatformIdentityConfigRepo(db)
	ctx := t.Context()

	if err := repo.Set(ctx, nil, "email", "alice@example.com"); err != nil {
		t.Fatalf("set nil toolset: %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected row")
	}
	if got.ToolsetID != nil {
		t.Errorf("toolsetID = %v, want nil", got.ToolsetID)
	}
}
