// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestToolRepo_CreateAndGetByID(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	tool := &Tool{
		ID:             "e0000000-0000-0000-0000-000000000001",
		Name:           "search_web",
		Description:    strPtr("Search the web"),
		Parameters:     strPtr(`{"type":"object"}`),
		Instructions:   strPtr("Use this tool to search"),
		ArtifactParams: strPtr(`{"max_size":1024}`),
		Origin:         "filesystem",
		Status:         "available",
		CreatedBy:      "admin",
		UpdatedBy:      "admin",
		CreatedTime:    1000,
		UpdatedTime:    2000,
	}

	if err := repo.Create(ctx, tool); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, tool.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetByID returned nil")
	}

	if got.ID != tool.ID {
		t.Errorf("ID = %q, want %q", got.ID, tool.ID)
	}
	if got.Name != tool.Name {
		t.Errorf("Name = %q, want %q", got.Name, tool.Name)
	}
	if got.Description == nil || *got.Description != *tool.Description {
		t.Errorf("Description = %v, want %v", got.Description, tool.Description)
	}
	if got.Parameters == nil || *got.Parameters != *tool.Parameters {
		t.Errorf("Parameters = %v, want %v", got.Parameters, tool.Parameters)
	}
	if got.Instructions == nil || *got.Instructions != *tool.Instructions {
		t.Errorf("Instructions = %v, want %v", got.Instructions, tool.Instructions)
	}
	if got.ArtifactParams == nil || *got.ArtifactParams != *tool.ArtifactParams {
		t.Errorf("ArtifactParams = %v, want %v", got.ArtifactParams, tool.ArtifactParams)
	}
	if got.Origin != tool.Origin {
		t.Errorf("Origin = %q, want %q", got.Origin, tool.Origin)
	}
	if got.Status != tool.Status {
		t.Errorf("Status = %q, want %q", got.Status, tool.Status)
	}
	if got.CreatedBy != tool.CreatedBy {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, tool.CreatedBy)
	}
	if got.UpdatedBy != tool.UpdatedBy {
		t.Errorf("UpdatedBy = %q, want %q", got.UpdatedBy, tool.UpdatedBy)
	}
	if got.CreatedTime != tool.CreatedTime {
		t.Errorf("CreatedTime = %d, want %d", got.CreatedTime, tool.CreatedTime)
	}
	if got.UpdatedTime != tool.UpdatedTime {
		t.Errorf("UpdatedTime = %d, want %d", got.UpdatedTime, tool.UpdatedTime)
	}
}

func TestToolRepo_GetByID_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	got, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestToolRepo_GetByName(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	tool := &Tool{
		ID:        "e0000000-0000-0000-0000-000000000002",
		Name:      "read_file",
		Origin:    "filesystem",
		Status:    "available",
		CreatedBy: "admin",
		UpdatedBy: "admin",
	}
	if err := repo.Create(ctx, tool); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByName(ctx, "read_file")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName returned nil")
	}
	if got.ID != tool.ID {
		t.Errorf("ID = %q, want %q", got.ID, tool.ID)
	}
	if got.Name != "read_file" {
		t.Errorf("Name = %q, want %q", got.Name, "read_file")
	}
}

func TestToolRepo_GetByName_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	got, err := repo.GetByName(ctx, "nonexistent_tool")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestToolRepo_UpsertByName_Insert(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	tool := &Tool{
		ID:          "e0000000-0000-0000-0000-000000000003",
		Name:        "upsert_new_tool",
		Description: strPtr("A brand new tool"),
		Parameters:  strPtr(`{"type":"object","properties":{}}`),
		Origin:      "filesystem",
		Status:      "available",
		CreatedBy:   "system",
		UpdatedBy:   "system",
	}

	if err := repo.UpsertByName(ctx, tool); err != nil {
		t.Fatalf("UpsertByName (insert): %v", err)
	}

	got, err := repo.GetByName(ctx, "upsert_new_tool")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("expected tool to exist after upsert insert")
	}
	if got.Name != "upsert_new_tool" {
		t.Errorf("Name = %q, want %q", got.Name, "upsert_new_tool")
	}
	if got.Description == nil || *got.Description != "A brand new tool" {
		t.Errorf("Description = %v, want %q", got.Description, "A brand new tool")
	}
	if got.Origin != "filesystem" {
		t.Errorf("Origin = %q, want %q", got.Origin, "filesystem")
	}
	if got.Status != "available" {
		t.Errorf("Status = %q, want %q", got.Status, "available")
	}
}

func TestToolRepo_UpsertByName_Update(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	original := &Tool{
		ID:          "e0000000-0000-0000-0000-000000000004",
		Name:        "upsert_existing",
		Description: strPtr("Original description"),
		Origin:      "filesystem",
		Status:      "available",
		CreatedBy:   "admin",
		UpdatedBy:   "admin",
	}
	if err := repo.Create(ctx, original); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := &Tool{
		ID:          "e0000000-0000-0000-0000-999999999999",
		Name:        "upsert_existing",
		Description: strPtr("Updated description"),
		Origin:      "platform",
		Status:      "unavailable",
		CreatedBy:   "other",
		UpdatedBy:   "reconciler",
	}
	if err := repo.UpsertByName(ctx, updated); err != nil {
		t.Fatalf("UpsertByName (update): %v", err)
	}

	got, err := repo.GetByName(ctx, "upsert_existing")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("expected tool to exist after upsert update")
	}

	if got.Description == nil || *got.Description != "Updated description" {
		t.Errorf("Description should be updated: got %v, want %q", got.Description, "Updated description")
	}
	if got.Status != "unavailable" {
		t.Errorf("Status should be updated: got %q, want %q", got.Status, "unavailable")
	}

	if got.Origin != "filesystem" {
		t.Errorf("Origin should be PRESERVED: got %q, want %q", got.Origin, "filesystem")
	}

	if got.ID != original.ID {
		t.Errorf("ID should be preserved from original insert: got %q, want %q", got.ID, original.ID)
	}
}

func TestToolRepo_Update(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	tool := &Tool{
		ID:          "e0000000-0000-0000-0000-000000000005",
		Name:        "updatable_tool",
		Description: strPtr("Before update"),
		Origin:      "filesystem",
		Status:      "available",
		CreatedBy:   "admin",
		UpdatedBy:   "admin",
	}
	if err := repo.Create(ctx, tool); err != nil {
		t.Fatalf("Create: %v", err)
	}

	tool.Description = strPtr("After update")
	tool.UpdatedBy = "editor"
	if err := repo.Update(ctx, tool); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByID(ctx, tool.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Description == nil || *got.Description != "After update" {
		t.Errorf("Description = %v, want %q", got.Description, "After update")
	}
	if got.UpdatedBy != "editor" {
		t.Errorf("UpdatedBy = %q, want %q", got.UpdatedBy, "editor")
	}
}

func TestToolRepo_Update_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	tool := &Tool{
		ID:        "fffffffe-ffff-ffff-ffff-ffffffffffff",
		Name:      "ghost_tool",
		Origin:    "filesystem",
		Status:    "available",
		CreatedBy: "admin",
		UpdatedBy: "admin",
	}
	err := repo.Update(ctx, tool)
	if err == nil {
		t.Fatal("expected error updating nonexistent tool")
	}
}

func TestToolRepo_Delete(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	tool := &Tool{
		ID:        "e0000000-0000-0000-0000-000000000006",
		Name:      "deletable_tool",
		Origin:    "filesystem",
		Status:    "available",
		CreatedBy: "admin",
		UpdatedBy: "admin",
	}
	if err := repo.Create(ctx, tool); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, tool.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.GetByID(ctx, tool.ID)
	if err != nil {
		t.Fatalf("GetByID after delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestToolRepo_Delete_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	err := repo.Delete(ctx, "fffffffd-ffff-ffff-ffff-ffffffffffff")
	if err == nil {
		t.Fatal("expected error deleting nonexistent tool")
	}
}

func TestToolRepo_ListPaginated(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	for i, name := range []string{"tool_a", "tool_b", "tool_c"} {
		tool := &Tool{
			ID:          "e0000000-0000-0000-0000-00000000010" + string(rune('0'+i)),
			Name:        name,
			Origin:      "filesystem",
			Status:      "available",
			CreatedBy:   "admin",
			UpdatedBy:   "admin",
			CreatedTime: int64(1000 + i),
		}
		if err := repo.Create(ctx, tool); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	tools, total, err := repo.ListPaginated(ctx, 1, 2)
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(tools) != 2 {
		t.Errorf("len(tools) = %d, want 2", len(tools))
	}
}

func TestToolRepo_SetStatus(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := context.Background()

	tool := &Tool{
		ID:        "e0000000-0000-0000-0000-000000000007",
		Name:      "status_tool",
		Origin:    "filesystem",
		Status:    "available",
		CreatedBy: "admin",
		UpdatedBy: "admin",
	}
	if err := repo.Create(ctx, tool); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.SetStatus(ctx, "status_tool", "unavailable"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	got, err := repo.GetByName(ctx, "status_tool")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName returned nil")
	}
	if got.Status != "unavailable" {
		t.Errorf("Status = %q, want %q", got.Status, "unavailable")
	}
}

func TestToolRepo_TouchMovesOnlyTheUpdatedTime(t *testing.T) {
	db := TestDB(t)
	repo := NewToolRepo(db)
	ctx := t.Context()
	tool := &Tool{ID: "e0000000-0000-0000-0000-000000000099", Name: "baked_tool", Description: strPtr("baked"),
		Origin: "filesystem", Status: "available", CreatedBy: "system", UpdatedBy: "system"}
	if err := repo.Create(ctx, tool); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Touch(ctx, "baked_tool", 42); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, err := repo.GetByName(ctx, "baked_tool")
	if err != nil || got == nil {
		t.Fatalf("GetByName: %v, %v", got, err)
	}
	if got.UpdatedTime != 42 || got.Status != "available" || *got.Description != "baked" || got.UpdatedBy != "system" {
		t.Errorf("after Touch = %+v, want only updated_time moved to 42", got)
	}
}
