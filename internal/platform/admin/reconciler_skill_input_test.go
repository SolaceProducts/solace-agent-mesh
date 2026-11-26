// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestReconciler_BuildDeployInput_PopulatesSkillIDs(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	skillRepo := store.NewSkillRepo(db)
	agentRepo := store.NewAgentRepo(db)

	desc := "test-skill"
	skill := &store.Skill{
		Name:            "test-skill",
		Description:     &desc,
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := skillRepo.Create(ctx, skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	agent := &store.Agent{
		Name:        "skill-bound-agent",
		Type:        AgentTypeStandard,
		Description: strPtr("test"),
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := skillRepo.SetAgentSkills(ctx, agent.ID, []string{skill.ID}); err != nil {
		t.Fatalf("set agent skills: %v", err)
	}

	rec := &Reconciler{
		agentRepo: agentRepo,
		skillRepo: skillRepo,
		logger:    slog.New(slog.DiscardHandler),
	}

	input, err := rec.buildDeployInput(ctx, agent)
	if err != nil {
		t.Fatalf("buildDeployInput: %v", err)
	}
	if !slices.Contains(input.SkillIDs, skill.ID) {
		t.Errorf("SkillIDs = %v, want it to contain %q", input.SkillIDs, skill.ID)
	}
}

func TestReconciler_BuildDeployInput_SkillRepoNil(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{
		Name:        "no-skill-repo-agent",
		Type:        AgentTypeStandard,
		Description: strPtr("test"),
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	rec := &Reconciler{
		agentRepo: agentRepo,
		logger:    slog.New(slog.DiscardHandler),
	}

	input, err := rec.buildDeployInput(ctx, agent)
	if err != nil {
		t.Fatalf("buildDeployInput: %v", err)
	}
	if len(input.SkillIDs) != 0 {
		t.Errorf("SkillIDs = %v, want empty when SkillRepo is nil", input.SkillIDs)
	}
}
