// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestSkillDiscoveryChecker_MarksStuckPendingAsFailed(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	stuckSkill := &store.Skill{
		ID:              "f0000000-0000-0000-0000-000000000001",
		Name:            "stuck-pending-skill",
		Origin:          "platform",
		DiscoveryStatus: "pending",
		CreatedBy:       "system",
		UpdatedBy:       "system",
		CreatedTime:     time.Now().Add(-5 * time.Minute).UnixMilli(),
		UpdatedTime:     time.Now().Add(-5 * time.Minute).UnixMilli(),
	}
	if err := repo.Create(ctx, stuckSkill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cutoff := time.Now().Add(-defaultDiscoveryPendingTimeout).UnixMilli()
	if err := svc.FailStalePending(ctx, cutoff); err != nil {
		t.Fatalf("FailStalePending: %v", err)
	}

	got, err := repo.GetByID(ctx, stuckSkill.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("skill row not found after FailStalePending")
	}
	if got.DiscoveryStatus != "failed" {
		t.Errorf("DiscoveryStatus = %q, want \"failed\"", got.DiscoveryStatus)
	}
}

func TestSkillDiscoveryChecker_SkipsRecentPending(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	freshSkill := &store.Skill{
		ID:              "f0000000-0000-0000-0000-000000000002",
		Name:            "fresh-pending-skill",
		Origin:          "platform",
		DiscoveryStatus: "pending",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := repo.Create(ctx, freshSkill); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cutoff := time.Now().Add(-defaultDiscoveryPendingTimeout).UnixMilli()
	if err := svc.FailStalePending(ctx, cutoff); err != nil {
		t.Fatalf("FailStalePending: %v", err)
	}

	got, err := repo.GetByID(ctx, freshSkill.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("skill row not found")
	}
	if got.DiscoveryStatus != "pending" {
		t.Errorf("DiscoveryStatus = %q, want \"pending\"", got.DiscoveryStatus)
	}
}
