// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestSkillService_HandleToolFailed_MarksSkillFailed(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	sk := &store.Skill{
		ID:              "a1000000-0000-0000-0000-000000000001",
		Name:            "blitzskill8b",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("Create: %v", err)
	}

	svc.HandleToolFailed(ctx, "blitzskill8b__g_blitz_greet", "add `runtime: go` to manifest")

	got, err := repo.GetByID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("skill row not found after HandleToolFailed")
	}
	if got.DiscoveryStatus != "failed" {
		t.Errorf("DiscoveryStatus = %q, want \"failed\"", got.DiscoveryStatus)
	}
}

func TestSkillService_HandleToolFailed_UnknownSkillIsNoOp(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	svc.HandleToolFailed(ctx, "nosuchskill__sometool", "some error")

	got, err := repo.GetByName(ctx, "nosuchskill")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got != nil {
		t.Errorf("expected no skill row, got %+v", got)
	}
}

func TestSkillService_HandleToolFailed_NoPrefixIsNoOp(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	svc.HandleToolFailed(ctx, "notprefixed", "some error")
}

func TestDBResourceEventHandler_OnToolFailed_RoutesSkillToFailed(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	sk := &store.Skill{
		ID:              "a1000000-0000-0000-0000-000000000002",
		Name:            "routedskill",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("Create: %v", err)
	}

	h := NewDBResourceEventHandler(svc, nil, nil, slog.Default())
	h.OnToolFailed(ctx, remotetool.FailedDiscoveryParams{
		ToolName:     "routedskill__g_blitz_greet",
		ErrorMessage: "add `runtime: go` to manifest",
	})

	got, err := repo.GetByID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.DiscoveryStatus != "failed" {
		t.Errorf("DiscoveryStatus = %v, want \"failed\"", got)
	}
}

func TestSkillService_HandleToolFailed_LookupErrorDoesNotFlip(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())

	sk := &store.Skill{
		ID:              "a1000000-0000-0000-0000-000000000003",
		Name:            "lookuperrskill",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := repo.Create(context.Background(), sk); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	svc.HandleToolFailed(cancelled, "lookuperrskill__sometool", "some error")

	got, err := repo.GetByID(context.Background(), sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.DiscoveryStatus != "ready" {
		t.Errorf("DiscoveryStatus = %v, want \"ready\" (a repo error must not flip the skill)", got)
	}
}
