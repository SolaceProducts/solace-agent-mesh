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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedPlatformSkill(t *testing.T, repo *store.SkillRepo, name string) {
	t.Helper()
	desc := ""
	if err := repo.UpsertByName(context.Background(), &store.Skill{
		Name:            name,
		Description:     &desc,
		Origin:          "platform",
		DiscoveryStatus: "pending",
		CreatedBy:       "u",
		UpdatedBy:       "u",
	}); err != nil {
		t.Fatalf("seed %q: %v", name, err)
	}
}

func TestSkillService_UpsertFromInit_IgnoresUnknownSkill(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:   "filesystem-skill",
		Description: "lives on disk in STR",
	})

	got, err := repo.GetByName(ctx, "filesystem-skill")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got != nil {
		t.Fatalf("filesystem skill should not be persisted, got %+v", got)
	}
}

func TestSkillService_UpsertFromInit_SkipsUnchanged(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	seedPlatformSkill(t, repo, "stable-skill")

	params := remotetool.SkillInitParams{
		SkillName:     "stable-skill",
		Description:   "unchanged",
		Tags:          []string{"a", "b"},
		HasReferences: true,
		ResourceCount: 3,
	}

	svc.UpsertFromInit(ctx, params)
	first, err := repo.GetByName(ctx, params.SkillName)
	if err != nil || first == nil {
		t.Fatalf("first GetByName: skill=%v err=%v", first, err)
	}

	time.Sleep(5 * time.Millisecond)

	svc.UpsertFromInit(ctx, params)
	second, err := repo.GetByName(ctx, params.SkillName)
	if err != nil || second == nil {
		t.Fatalf("second GetByName: skill=%v err=%v", second, err)
	}
	if second.UpdatedTime != first.UpdatedTime {
		t.Fatalf("UpdatedTime changed on identical republish: first=%d second=%d",
			first.UpdatedTime, second.UpdatedTime)
	}
}

func TestSkillService_UpsertFromInit_AppliesChange(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.Default())
	ctx := context.Background()

	seedPlatformSkill(t, repo, "evolving-skill")

	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:     "evolving-skill",
		Description:   "v1",
		Tags:          []string{"old"},
		ResourceCount: 1,
	})

	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:     "evolving-skill",
		Description:   "v2",
		ResourceCount: 5,
	})
	got, err := repo.GetByName(ctx, "evolving-skill")
	if err != nil || got == nil {
		t.Fatalf("GetByName: skill=%v err=%v", got, err)
	}
	if got.Description == nil || *got.Description != "v2" {
		t.Errorf("Description not updated: %v", got.Description)
	}
	if got.ResourceCount != 5 {
		t.Errorf("ResourceCount = %d, want 5", got.ResourceCount)
	}
	if got.Tags != nil {
		t.Errorf("Tags should be nil after drop, got %v", *got.Tags)
	}
}
