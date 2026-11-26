// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestUpsertSkillFromInit_PersistsToolsets(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	skillRepo := store.NewSkillRepo(db)
	ctx := t.Context()

	desc := "test skill"
	if err := skillRepo.UpsertByName(ctx, &store.Skill{
		Name:            "fixture-skill",
		Description:     &desc,
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	svc := NewSkillService(db, skillRepo, slog.New(slog.DiscardHandler))

	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:   "fixture-skill",
		Description: "test skill",
		Toolsets:    []string{"toolset-a", "toolset-b"},
	})

	got, err := skillRepo.GetByName(ctx, "fixture-skill")
	if err != nil {
		t.Fatalf("re-read skill: %v", err)
	}
	if got == nil {
		t.Fatal("skill missing after upsert")
	}
	if got.Toolsets == nil {
		t.Fatal("Toolsets is nil after UpsertFromInit — wire field not persisted")
	}
	want := `["toolset-a","toolset-b"]`
	if *got.Toolsets != want {
		t.Errorf("Toolsets = %q, want %q", *got.Toolsets, want)
	}
}

func TestUpsertSkillFromInit_DoesNotClearToolsetsWhenEmpty(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	skillRepo := store.NewSkillRepo(db)
	ctx := t.Context()

	desc := "test skill"
	existing := `["toolset-a"]`
	if err := skillRepo.UpsertByName(ctx, &store.Skill{
		Name:            "fixture-skill",
		Description:     &desc,
		Origin:          "platform",
		DiscoveryStatus: "ready",
		Toolsets:        &existing,
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := NewSkillService(db, skillRepo, slog.New(slog.DiscardHandler))

	svc.UpsertFromInit(ctx, remotetool.SkillInitParams{
		SkillName:   "fixture-skill",
		Description: "changed description",
	})

	got, err := skillRepo.GetByName(ctx, "fixture-skill")
	if err != nil {
		t.Fatalf("re-read skill: %v", err)
	}
	if got.Toolsets != nil {
		t.Errorf("Toolsets = %v, want nil (current behaviour: clears on empty Toolsets)", *got.Toolsets)
	}
}

func TestE2E_SkillToolsets_FlowsToToolConfig(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "skill_implied", "do",
		`[{"key":"api_key","type":"string","secret":true},{"key":"model_id","type":"string","default":"gpt-4"}]`,
	)
	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-from-toolset"}},
	}, "u"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	db := f.repo.DB()
	skillRepo := store.NewSkillRepo(db)
	desc := "skill-with-toolset"
	toolsetsJSON := `["skill_implied"]`
	skill := &store.Skill{
		Name:            "skill-with-toolset",
		Description:     &desc,
		Origin:          "platform",
		DiscoveryStatus: "ready",
		Toolsets:        &toolsetsJSON,
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if err := skillRepo.Create(ctx, skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{Name: "skill-only-agent", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}
	if err := skillRepo.SetAgentSkills(ctx, agent.ID, []string{skill.ID}); err != nil {
		t.Fatalf("set agent skills: %v", err)
	}

	implied, err := resolveSkillImpliedToolsets(ctx, skillRepo, []string{skill.ID}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveSkillImpliedToolsets: %v", err)
	}
	if len(implied) != 1 || implied[0] != "skill_implied" {
		t.Fatalf("implied = %v, want [skill_implied]", implied)
	}

	effective := unionToolsets(nil, implied)
	if len(effective) != 1 || effective[0] != "skill_implied" {
		t.Fatalf("effective = %v, want [skill_implied]", effective)
	}

	infos, err := resolveToolPackageInfos(ctx, agent.ID, effective, nil, f.repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveToolPackageInfos: %v", err)
	}
	info, ok := infos["skill_implied"]
	if !ok {
		t.Fatalf("missing skill_implied in resolved package infos; got keys %v", keysOfInfoMap(infos))
	}
	if info.ConfigValues == nil {
		t.Fatal("ConfigValues is nil")
	}
	doCfg := info.ConfigValues["do"]
	if doCfg["api_key"] != "sk-from-toolset" {
		t.Errorf("api_key = %v, want sk-from-toolset (toolset-layer)", doCfg["api_key"])
	}

	rendered := buildToolConfigForFile(info.ToolFiles[0], doCfg)
	if rendered["api_key"] != "sk-from-toolset" {
		t.Errorf("rendered tool_config api_key = %v, want sk-from-toolset", rendered["api_key"])
	}
	if rendered["model_id"] != "gpt-4" {
		t.Errorf("rendered tool_config model_id = %v, want gpt-4 (schema default fills the gap)", rendered["model_id"])
	}
}

func keysOfInfoMap(m map[string]ToolPackageInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestE2E_SkillToolsets_PendingSkillSkipped(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	skillRepo := store.NewSkillRepo(db)
	ctx := t.Context()

	desc := "pending"
	toolsetsJSON := `["my_toolset"]`
	pending := &store.Skill{
		Name:            "pending-skill",
		Description:     &desc,
		Origin:          "platform",
		DiscoveryStatus: "pending",
		Toolsets:        &toolsetsJSON,
		CreatedBy:       "u",
		UpdatedBy:       "u",
	}
	if err := skillRepo.Create(ctx, pending); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	implied, err := resolveSkillImpliedToolsets(ctx, skillRepo, []string{pending.ID}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveSkillImpliedToolsets: %v", err)
	}
	if implied != nil {
		t.Errorf("implied = %v, want nil for pending skill", implied)
	}
}
