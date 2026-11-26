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

func TestSkillService_InitWithDiscoveryFailed_DoesNotReviveFailedSkill(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	sk := &store.Skill{
		ID:              "b1000000-0000-0000-0000-000000000001",
		Name:            "raceskill",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("Create: %v", err)
	}

	svc.HandleToolFailed(ctx, "raceskill__g_blitz_greet", "binary cannot run on this host")
	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:       "raceskill",
		Description:     "a skill whose only bundled tool failed to register",
		DiscoveryFailed: true,
	})

	got, err := repo.GetByID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.DiscoveryStatus != "failed" {
		t.Errorf("DiscoveryStatus = %v, want \"failed\" (init must not revive a failed skill)", got)
	}
}

func TestSkillService_InitWithDiscoveryFailed_FailsEvenWithoutPriorEvent(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	sk := &store.Skill{
		ID:              "b1000000-0000-0000-0000-000000000002",
		Name:            "initonlyfail",
		Origin:          "platform",
		DiscoveryStatus: "pending",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("Create: %v", err)
	}

	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:       "initonlyfail",
		Description:     "bundled tool unavailable, reported only on the init",
		DiscoveryFailed: true,
	})

	got, err := repo.GetByID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.DiscoveryStatus != "failed" {
		t.Errorf("DiscoveryStatus = %v, want \"failed\"", got)
	}
}

func TestSkillService_FailedDiscoveryAfterFailedInit_StaysFailed(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	sk := &store.Skill{
		ID:              "b1000000-0000-0000-0000-000000000004",
		Name:            "reverseorder",
		Origin:          "platform",
		DiscoveryStatus: "pending",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("Create: %v", err)
	}

	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:       "reverseorder",
		Description:     "init reports the bundled tool could not register",
		DiscoveryFailed: true,
	})
	svc.HandleToolFailed(ctx, "reverseorder__g_blitz_greet", "binary cannot run on this host")

	got, err := repo.GetByID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.DiscoveryStatus != "failed" {
		t.Errorf("DiscoveryStatus = %v, want \"failed\"", got)
	}
}

func TestSkillService_InitWithoutDiscoveryFailed_MarksReady(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	sk := &store.Skill{
		ID:              "b1000000-0000-0000-0000-000000000003",
		Name:            "healthyskill",
		Origin:          "platform",
		DiscoveryStatus: "pending",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("Create: %v", err)
	}

	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:   "healthyskill",
		Description: "all bundled tools registered",
		BundledTools: []remotetool.SkillBundledTool{
			{ToolName: "healthyskill__g_blitz_greet", Description: "greets"},
		},
	})

	got, err := repo.GetByID(ctx, sk.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.DiscoveryStatus != "ready" {
		t.Errorf("DiscoveryStatus = %v, want \"ready\"", got)
	}
}
