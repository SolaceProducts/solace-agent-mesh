// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func TestA2AAgentRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := context.Background()

	desc := "A test remote agent"
	authType := "bearer"
	authConfig := `{"token":"secret123"}`
	a := &A2AAgent{
		AgentURL:              "https://example.com/agent",
		Name:                  "test-remote-agent",
		Description:           &desc,
		AgentCardLocation:     "well_known",
		AgentAuthType:         &authType,
		AgentAuthConfig:       &authConfig,
		RequestTimeoutSeconds: 120,
		CreatedBy:             "user1",
		UpdatedBy:             "user1",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if a.CreatedTime == 0 {
		t.Fatal("expected CreatedTime to be set")
	}

	got, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.Name != "test-remote-agent" {
		t.Errorf("name = %q, want %q", got.Name, "test-remote-agent")
	}
	if got.AgentURL != "https://example.com/agent" {
		t.Errorf("agent_url = %q, want %q", got.AgentURL, "https://example.com/agent")
	}
}

func TestA2AAgentRepo_GetByID_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := context.Background()

	got, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for not found")
	}
}

func TestA2AAgentRepo_GetByName(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := context.Background()

	a := &A2AAgent{
		AgentURL:              "https://example.com/byname",
		Name:                  "find-by-name-agent",
		AgentCardLocation:     "well_known",
		AgentAuthType:         new("none"),
		AgentAuthConfig:       new("{}"),
		RequestTimeoutSeconds: 60,
		CreatedBy:             "user1",
		UpdatedBy:             "user1",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByName(ctx, "find-by-name-agent")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.ID != a.ID {
		t.Errorf("id = %q, want %q", got.ID, a.ID)
	}
	if got.AgentURL != "https://example.com/byname" {
		t.Errorf("agent_url = %q, want %q", got.AgentURL, "https://example.com/byname")
	}
}

func TestA2AAgentRepo_GetByName_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := context.Background()

	got, err := repo.GetByName(ctx, "nonexistent-agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for not found")
	}
}

func TestA2AAgentRepo_ListPaginated(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := context.Background()

	for i := range 3 {
		a := &A2AAgent{
			AgentURL:              "https://example.com/agent",
			Name:                  "paginated-agent-" + string(rune('a'+i)),
			AgentCardLocation:     "well_known",
			AgentAuthType:         new("none"),
			AgentAuthConfig:       new("{}"),
			RequestTimeoutSeconds: 60,
			CreatedBy:             "user1",
			UpdatedBy:             "user1",
		}
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("create agent %d: %v", i, err)
		}
	}

	list, total, err := repo.ListPaginated(ctx, 1, 2)
	if err != nil {
		t.Fatalf("list paginated: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}

	list2, total2, err := repo.ListPaginated(ctx, 2, 2)
	if err != nil {
		t.Fatalf("list paginated page 2: %v", err)
	}
	if total2 != 3 {
		t.Errorf("total page 2 = %d, want 3", total2)
	}
	if len(list2) != 1 {
		t.Errorf("len page 2 = %d, want 1", len(list2))
	}
}

func TestA2AAgentRepo_Update(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := context.Background()

	desc := "original description"
	a := &A2AAgent{
		AgentURL:              "https://example.com/update",
		Name:                  "update-agent",
		Description:           &desc,
		AgentCardLocation:     "well_known",
		AgentAuthType:         new("none"),
		AgentAuthConfig:       new("{}"),
		RequestTimeoutSeconds: 60,
		CreatedBy:             "user1",
		UpdatedBy:             "user1",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}

	newDesc := "updated description"
	a.Description = &newDesc
	a.UpdatedBy = "user2"
	if err := repo.Update(ctx, a); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Description == nil || *got.Description != "updated description" {
		t.Errorf("description = %v, want %q", got.Description, "updated description")
	}
	if got.UpdatedBy != "user2" {
		t.Errorf("updated_by = %q, want %q", got.UpdatedBy, "user2")
	}
}

func TestA2AAgentRepo_NormalizedNameIndex(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := t.Context()

	a := &A2AAgent{
		AgentURL:              "https://example.com/ci",
		Name:                  "My Remote Agent",
		AgentCardLocation:     "well_known",
		AgentAuthType:         new("none"),
		AgentAuthConfig:       new("{}"),
		RequestTimeoutSeconds: 60,
		CreatedBy:             "user1",
		UpdatedBy:             "user1",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("case_insensitive_lookup", func(t *testing.T) {
		for _, lookup := range []string{"my remote agent", "MYREMOTEAGENT", "MyRemoteAgent", "myremoteagent"} {
			got, err := repo.GetByName(ctx, lookup)
			if err != nil {
				t.Fatalf("GetByName(%q): %v", lookup, err)
			}
			if got == nil || got.ID != a.ID {
				t.Errorf("GetByName(%q) = %v, want ID %q", lookup, got, a.ID)
			}
		}
	})

	t.Run("rejects_normalized_duplicate", func(t *testing.T) {
		dup := &A2AAgent{
			AgentURL:              "https://example.com/second",
			Name:                  "myremoteagent",
			AgentCardLocation:     "well_known",
			AgentAuthType:         new("none"),
			AgentAuthConfig:       new("{}"),
			RequestTimeoutSeconds: 60,
			CreatedBy:             "user1",
			UpdatedBy:             "user1",
		}
		if err := repo.Create(ctx, dup); err == nil {
			t.Fatal("expected unique constraint violation for normalized duplicate name")
		}
	})
}

func TestA2AAgentRepo_Delete(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentRepo(db)
	ctx := context.Background()

	a := &A2AAgent{
		AgentURL:              "https://example.com/delete",
		Name:                  "delete-agent",
		AgentCardLocation:     "well_known",
		AgentAuthType:         new("none"),
		AgentAuthConfig:       new("{}"),
		RequestTimeoutSeconds: 60,
		CreatedBy:             "user1",
		UpdatedBy:             "user1",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.Delete(ctx, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}
