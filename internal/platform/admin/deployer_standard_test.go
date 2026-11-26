// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedReadyPkgForDeploy(ctx context.Context, t *testing.T, f *toolsetFixture, pkgName, toolName, schemaJSON string) *ToolsetResponse {
	t.Helper()
	return seedReadyToolWithSchema(ctx, t, f, pkgName, toolName, schemaJSON)
}

func TestResolveToolPackageInfos_AgentOverridesToolset(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "deploy_override", "do",
		`[{"key":"api_key","type":"string","secret":true},{"key":"model_id","type":"string","default":"gpt-4"}]`,
	)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-toolset", "model_id": "from-toolset"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	db := f.repo.DB()
	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{Name: "deploy-override", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}
	agentCfg := `{"do":{"model_id":"from-agent"}}`
	if err := f.repo.SetToolsetConfigsForAgent(ctx, agent.ID, []store.AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: p.ID, ConfigValues: &agentCfg},
	}); err != nil {
		t.Fatalf("SetToolsetConfigsForAgent: %v", err)
	}

	got, err := resolveToolPackageInfos(ctx, agent.ID, []string{p.Name}, nil, f.repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveToolPackageInfos: %v", err)
	}
	info, ok := got[p.Name]
	if !ok {
		t.Fatalf("missing %q in resolved map", p.Name)
	}

	if v := info.ConfigValues["do"]["model_id"]; v != "from-agent" {
		t.Errorf("model_id = %v, want from-agent (agent overrides toolset)", v)
	}
	if v := info.ConfigValues["do"]["api_key"]; v != "sk-toolset" {
		t.Errorf("api_key = %v, want sk-toolset (toolset fills gap)", v)
	}
}

func TestResolveToolPackageInfos_ToolsetCoversUnboundAgent(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "deploy_inherit", "do",
		`[{"key":"api_key","type":"string","secret":true}]`,
	)
	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-toolset"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	db := f.repo.DB()
	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{Name: "deploy-inherit", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	got, err := resolveToolPackageInfos(ctx, agent.ID, []string{p.Name}, nil, f.repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveToolPackageInfos: %v", err)
	}
	info := got[p.Name]
	if info.ConfigValues["do"]["api_key"] != "sk-toolset" {
		t.Errorf("api_key = %v, want sk-toolset (inherited from toolset)", info.ConfigValues["do"]["api_key"])
	}
}

func TestResolveToolPackageInfos_NoToolsetConfig_AgentOnly(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "deploy_agentonly", "do",
		`[{"key":"api_key","type":"string","secret":true}]`,
	)

	db := f.repo.DB()
	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{Name: "deploy-agentonly", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}
	agentCfg := `{"do":{"api_key":"sk-agent"}}`
	if err := f.repo.SetToolsetConfigsForAgent(ctx, agent.ID, []store.AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: p.ID, ConfigValues: &agentCfg},
	}); err != nil {
		t.Fatalf("SetToolsetConfigsForAgent: %v", err)
	}

	got, err := resolveToolPackageInfos(ctx, agent.ID, []string{p.Name}, nil, f.repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveToolPackageInfos: %v", err)
	}
	info := got[p.Name]
	if info.ConfigValues["do"]["api_key"] != "sk-agent" {
		t.Errorf("api_key = %v, want sk-agent (agent-only)", info.ConfigValues["do"]["api_key"])
	}
}

func TestResolveToolPackageInfos_NestedAgentOverlayCoversAllTools(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "multi_tool_pkg", "tool_a",
		`[{"key":"api_key","type":"string","secret":true}]`,
	)
	params := `{"type":"object"}`
	schemaB := `[{"key":"api_key","type":"string","secret":true}]`
	f.svc.HandleToolInit(ctx, "multi_tool_pkg__tool_b", "second tool", ToolInitMeta{Params: &params, ConfigSchema: &schemaB})

	db := f.repo.DB()
	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{Name: "multi-tool-agent", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	agentCfg := `{"tool_a":{"api_key":"sk-shared"},"tool_b":{"api_key":"sk-shared"}}`
	if err := f.repo.SetToolsetConfigsForAgent(ctx, agent.ID, []store.AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: p.ID, ConfigValues: &agentCfg},
	}); err != nil {
		t.Fatalf("SetToolsetConfigsForAgent: %v", err)
	}

	got, err := resolveToolPackageInfos(ctx, agent.ID, []string{p.Name}, nil, f.repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveToolPackageInfos: %v", err)
	}
	info, ok := got[p.Name]
	if !ok {
		t.Fatalf("missing %q in resolved map", p.Name)
	}
	for _, toolName := range []string{"tool_a", "tool_b"} {
		toolCfg, has := info.ConfigValues[toolName]
		if !has {
			t.Errorf("tool %q: no merged config (nested overlay was not applied)", toolName)
			continue
		}
		if toolCfg["api_key"] != "sk-shared" {
			t.Errorf("tool %q: api_key = %v, want sk-shared", toolName, toolCfg["api_key"])
		}
	}

	storeAgent, _, _, _, _, err := agentRepo.GetByID(ctx, agent.ID)
	if err != nil || storeAgent == nil {
		t.Fatalf("re-read agent: %v", err)
	}
	systemPrompt := "act"
	storeAgent.SystemPrompt = &systemPrompt
	deployer := &StandardAgentDeployer{
		toolPackageRepo: f.repo,
		logger:          slog.New(slog.DiscardHandler),
	}
	if err := deployer.Validate(ctx, DeployInput{
		Agent:    storeAgent,
		Action:   DeployActionDeploy,
		Toolsets: []string{p.Name},
	}); err != nil {
		t.Errorf("Validate should pass when nested agent overlay covers every tool's secret, got: %v", err)
	}
}

func TestValidateSecretFields_PassesWhenToolsetCovers(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "deploy_validate", "do",
		`[{"key":"api_key","type":"string","secret":true}]`,
	)
	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-set"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	db := f.repo.DB()
	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{Name: "deploy-validate", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	deployer := &StandardAgentDeployer{
		toolPackageRepo: f.repo,
		logger:          slog.New(slog.DiscardHandler),
	}
	systemPrompt := "act"
	storeAgent, _, _, _, _, err := agentRepo.GetByID(ctx, agent.ID)
	if err != nil || storeAgent == nil {
		t.Fatalf("re-read agent: %v", err)
	}
	storeAgent.SystemPrompt = &systemPrompt

	err = deployer.Validate(ctx, DeployInput{
		Agent:    storeAgent,
		Action:   DeployActionDeploy,
		Toolsets: []string{p.Name},
	})
	if err != nil {
		t.Errorf("Validate should pass when toolset covers secret, got: %v", err)
	}
}

func TestToolsetService_GetByID_IncludesConfigSummary(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyPkgForDeploy(ctx, t, f, "summary_get", "do",
		`[{"key":"api_key","type":"string","secret":true},{"key":"model_id","type":"string","default":"gpt-4"}]`,
	)

	resp, err := f.svc.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.ConfigSummary == nil {
		t.Fatal("expected ConfigSummary to be populated")
	}
	if len(resp.ConfigSummary.RequiredFields) != 1 || resp.ConfigSummary.RequiredFields[0] != "do/api_key" {
		t.Errorf("RequiredFields = %v, want [do/api_key]", resp.ConfigSummary.RequiredFields)
	}
	if !resp.ConfigSummary.RequiresAgentConfig {
		t.Error("RequiresAgentConfig should be true before any defaults set")
	}

	if _, patchErr := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-set"}},
	}, "u"); patchErr != nil {
		t.Fatalf("PatchConfig: %v", patchErr)
	}
	resp2, err := f.svc.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID 2: %v", err)
	}
	if resp2.ConfigSummary.RequiresAgentConfig {
		t.Error("RequiresAgentConfig should be false after toolset default set")
	}
}

func TestE2E_ToolsetConfigFlowsIntoToolConfigYAML(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "e2e_flow", "do",
		`[{"key":"api_key","type":"string","secret":true},{"key":"model_id","type":"string","default":"gpt-4"}]`,
	)
	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-toolset", "model_id": "v-toolset"}},
	}, "u"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	agentRepo := store.NewAgentRepo(f.repo.DB())
	agent := &store.Agent{Name: "e2e-agent", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}
	agentCfg := `{"do":{"model_id":"v-agent"}}`
	if err := f.repo.SetToolsetConfigsForAgent(ctx, agent.ID, []store.AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: p.ID, ConfigValues: &agentCfg},
	}); err != nil {
		t.Fatalf("SetToolsetConfigsForAgent: %v", err)
	}

	pkgInfos, err := resolveToolPackageInfos(ctx, agent.ID, []string{p.Name}, nil, f.repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveToolPackageInfos: %v", err)
	}
	pkgInfo := pkgInfos[p.Name]
	if pkgInfo.ConfigValues == nil {
		t.Fatal("expected merged ConfigValues, got nil")
	}

	toolCfg := buildToolConfigForFile(pkgInfo.ToolFiles[0], pkgInfo.ConfigValues[pkgInfo.ToolFiles[0].ToolName])
	if toolCfg["api_key"] != "sk-toolset" {
		t.Errorf("tool_config api_key = %v, want sk-toolset (toolset-layer)", toolCfg["api_key"])
	}
	if toolCfg["model_id"] != "v-agent" {
		t.Errorf("tool_config model_id = %v, want v-agent (agent-layer overrides)", toolCfg["model_id"])
	}
}

func TestResolveToolPackageInfos_LegacyFlatToolsetConfigMigratesAtRead(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "deploy_legacy_flat", "do",
		`[{"key":"api_key","type":"string","secret":true}]`,
	)
	flatJSON := `{"api_key":"sk-from-legacy-flat"}`
	if err := f.repo.SetToolsetConfig(ctx, p.ID, &flatJSON, "legacy-cli"); err != nil {
		t.Fatalf("SetToolsetConfig: %v", err)
	}

	db := f.repo.DB()
	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{Name: "deploy-legacy-flat", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	got, err := resolveToolPackageInfos(ctx, agent.ID, []string{p.Name}, nil, f.repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveToolPackageInfos: %v", err)
	}
	info, ok := got[p.Name]
	if !ok {
		t.Fatalf("missing %q in resolved map", p.Name)
	}
	if v := info.ConfigValues["do"]["api_key"]; v != "sk-from-legacy-flat" {
		t.Errorf("api_key = %v, want sk-from-legacy-flat (FLAT row should auto-migrate to nested at read)", v)
	}

	deployer := &StandardAgentDeployer{
		toolPackageRepo: f.repo,
		logger:          slog.New(slog.DiscardHandler),
	}
	storeAgent, _, _, _, _, _ := agentRepo.GetByID(ctx, agent.ID)
	systemPrompt := "act"
	storeAgent.SystemPrompt = &systemPrompt
	if err := deployer.Validate(ctx, DeployInput{
		Agent:    storeAgent,
		Action:   DeployActionDeploy,
		Toolsets: []string{p.Name},
	}); err != nil {
		t.Fatalf("Validate should pass after FLAT→NESTED migration, got: %v", err)
	}
}

func TestNormalizeToolsetConfigShape_PreservesNestedAndAuthBlock(t *testing.T) {
	t.Parallel()
	schema := `[{"key":"api_key","type":"string","secret":true}]`
	files := []store.ToolPackageFile{{ToolName: "do", ConfigSchema: &schema}}

	t.Run("already nested is no-op", func(t *testing.T) {
		t.Parallel()
		in := map[string]any{"do": map[string]any{"api_key": "x"}}
		out := normalizeToolsetConfigShape(files, in, slog.New(slog.DiscardHandler), "agent-x", "pkg", "test")
		if v, ok := out["do"].(map[string]any); !ok || v["api_key"] != "x" {
			t.Errorf("nested input was modified: %v", out)
		}
	})

	t.Run("flat row with reserved auth survives", func(t *testing.T) {
		t.Parallel()
		in := map[string]any{
			"api_key": "x",
			"auth":    map[string]any{"type": "oauth"},
		}
		out := normalizeToolsetConfigShape(files, in, slog.New(slog.DiscardHandler), "agent-x", "pkg", "test")
		if v, ok := out["do"].(map[string]any); !ok || v["api_key"] != "x" {
			t.Errorf("FLAT row not fanned out: %v", out)
		}
		if _, ok := out["auth"].(map[string]any); !ok {
			t.Errorf("reserved auth block lost during FLAT→NESTED migration: %v", out)
		}
	})

	t.Run("empty input is no-op", func(t *testing.T) {
		t.Parallel()
		if got := normalizeToolsetConfigShape(files, nil, slog.New(slog.DiscardHandler), "agent-x", "pkg", "test"); got != nil {
			t.Errorf("nil input should pass through, got %v", got)
		}
	})

	t.Run("tool named auth does not trick the FLAT detector", func(t *testing.T) {
		t.Parallel()
		authSchema := `[{"key":"realm","type":"string"}]`
		doSchema := `[{"key":"api_key","type":"string","secret":true}]`
		filesWithAuthTool := []store.ToolPackageFile{
			{ToolName: "auth", ConfigSchema: &authSchema},
			{ToolName: "do", ConfigSchema: &doSchema},
		}
		in := map[string]any{
			"api_key": "sk-x",
			"auth":    map[string]any{"type": "oauth"},
		}
		out := normalizeToolsetConfigShape(filesWithAuthTool, in, slog.New(slog.DiscardHandler), "agent-x", "pkg", "test")
		if v, ok := out["do"].(map[string]any); !ok || v["api_key"] != "sk-x" {
			t.Errorf("api_key not fanned out to do tool — detector wrongly assumed NESTED: %v", out)
		}
		if a, ok := out["auth"].(map[string]any); !ok || a["type"] != "oauth" {
			t.Errorf("reserved auth block was lost: %v", out)
		}
	})
}

func TestBuildPayloads_DedupsMigratedSkillRefs(t *testing.T) {
	t.Parallel()

	const builtinRef = "sam-docs"
	if !builtinskills.HasName(builtinRef) {
		t.Fatalf("precondition: %q must be a bundled built-in skill", builtinRef)
	}

	newAgent := func() *store.Agent {
		return &store.Agent{
			Name:         "snapshot-dedup",
			Description:  strPtr("test"),
			SystemPrompt: strPtr("act"),
			SkillRefs:    strPtr(`["migrated-skill","` + builtinRef + `"]`),
		}
	}

	buildSnapshot := func(t *testing.T, skillIDs []string) DeployedAgentInfo {
		t.Helper()
		deployer := &StandardAgentDeployer{
			yamlSvc: NewYAMLService("test-ns", slog.New(slog.DiscardHandler)),
			logger:  slog.New(slog.DiscardHandler),
		}
		res, err := deployer.BuildPayloads(t.Context(), DeployInput{
			Agent:    newAgent(),
			Action:   DeployActionDeploy,
			SkillIDs: skillIDs,
		})
		if err != nil {
			t.Fatalf("BuildPayloads: %v", err)
		}
		snap, ok := res.DataSnapshot.(DeployedAgentInfo)
		if !ok {
			t.Fatalf("DataSnapshot type = %T, want DeployedAgentInfo", res.DataSnapshot)
		}
		return snap
	}

	t.Run("M2M present drops non-builtin ref", func(t *testing.T) {
		t.Parallel()
		snap := buildSnapshot(t, []string{"00000000-0000-0000-0000-000000000001"})
		if want := []string{builtinRef}; !slices.Equal(snap.SkillRefs, want) {
			t.Errorf("SkillRefs = %v, want %v (migrated non-builtin ref must be dropped)", snap.SkillRefs, want)
		}
	})

	t.Run("no M2M keeps both refs", func(t *testing.T) {
		t.Parallel()
		snap := buildSnapshot(t, nil)
		if want := []string{"migrated-skill", builtinRef}; !slices.Equal(snap.SkillRefs, want) {
			t.Errorf("SkillRefs = %v, want %v (no M2M means no dedup)", snap.SkillRefs, want)
		}
	})
}

func TestValidateSecretFields_FailsWhenNeitherLayerCovers(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	p := seedReadyPkgForDeploy(ctx, t, f, "deploy_validate_fail", "do",
		`[{"key":"api_key","type":"string","secret":true}]`,
	)

	db := f.repo.DB()
	agentRepo := store.NewAgentRepo(db)
	agent := &store.Agent{Name: "deploy-validate-fail", Description: strPtr("test"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	deployer := &StandardAgentDeployer{
		toolPackageRepo: f.repo,
		logger:          slog.New(slog.DiscardHandler),
	}
	systemPrompt := "act"
	storeAgent, _, _, _, _, _ := agentRepo.GetByID(ctx, agent.ID)
	storeAgent.SystemPrompt = &systemPrompt

	err := deployer.Validate(ctx, DeployInput{
		Agent:    storeAgent,
		Action:   DeployActionDeploy,
		Toolsets: []string{p.Name},
	})
	if err == nil {
		t.Fatal("Validate should fail when neither layer covers the required secret")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error should mention api_key, got: %v", err)
	}
}

func TestE2E_ToolsetLayerRequiredScopes_PatchPreserveResolve(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyPkgForDeploy(ctx, t, f, "compliance_scope_flow", "run_migrations",
		`[{"key":"api_key","type":"string"}]`)
	wantScope := "tool:" + p.Name + "__run_migrations:invoke"

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			reservedRequiredScopesConfigKey: map[string]any{
				"run_migrations": []any{wantScope},
			},
		},
	}, "u"); err != nil {
		t.Fatalf("PatchConfig(required_scopes) at toolset layer: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"run_migrations": map[string]any{"api_key": "x"}},
	}, "u"); err != nil {
		t.Fatalf("PatchConfig(unrelated field): %v", err)
	}

	agentRepo := store.NewAgentRepo(f.repo.DB())
	agent := &store.Agent{Name: "scope-toolset-agent", Description: strPtr("t"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, []string{p.Name}, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}
	infos, err := resolveToolPackageInfos(ctx, agent.ID, []string{p.Name}, nil, f.repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveToolPackageInfos: %v", err)
	}
	got := requiredScopesFromConfig(infos[p.Name].ConfigValues["run_migrations"])
	if len(got) != 1 || got[0] != wantScope {
		t.Fatalf("toolset-default required_scopes lost across PATCH/resolve: got %v, want [%s]", got, wantScope)
	}
}

func TestNormalizeToolsetConfigShape_ReservedOnly_NoWarn(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{{ToolName: "run_migrations"}}
	vals := map[string]any{
		reservedRequiredScopesConfigKey: map[string]any{
			"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"},
		},
	}
	logger, buf := captureLogger()
	got := normalizeToolsetConfigShape(files, vals, logger, "a", "p", "toolset_configs")
	if _, ok := got[reservedRequiredScopesConfigKey]; !ok {
		t.Errorf("reserved-only config should be returned as-is; got %v", got)
	}
	if strings.Contains(buf.String(), "legacy flat-shaped") {
		t.Errorf("reserved-only config must not warn 'legacy flat-shaped'; log = %s", buf.String())
	}
}

func TestOrphanedRequiredScopes_BlocksDiscoveryReady(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyPkgForDeploy(ctx, t, f, "compliance_tools", "run_migrations", `[]`)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			reservedRequiredScopesConfigKey: map[string]any{
				"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"},
			},
		},
	}, "u"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	if err := f.repo.UpdateZipInfo(ctx, p.ID, "python", "s3://fake", "pkg.zip", 2, "tester"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}
	params := `{"type":"object"}`
	f.svc.HandleToolInit(ctx, "compliance_tools__migrate", "renamed tool", ToolInitMeta{
		Params:           &params,
		PackageToolNames: []string{"migrate"},
	})

	pkg, err := f.repo.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if pkg.DiscoveryStatus == "ready" {
		t.Errorf("package must NOT flip to ready with an orphaned required_scopes gate")
	}
	if pkg.DiscoveryErrors == nil || !strings.Contains(*pkg.DiscoveryErrors, "run_migrations") {
		t.Errorf("expected a discovery error naming the orphaned gate; got %v", pkg.DiscoveryErrors)
	}
}

func TestOrphanedRequiredScopes_ConfigRecoverable(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyPkgForDeploy(ctx, t, f, "compliance_tools", "run_migrations", `[]`)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			reservedRequiredScopesConfigKey: map[string]any{
				"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"},
			},
		},
	}, "u"); err != nil {
		t.Fatalf("author gate: %v", err)
	}

	if err := f.repo.UpdateZipInfo(ctx, p.ID, "python", "s3://fake", "pkg.zip", 2, "tester"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}
	params := `{"type":"object"}`
	f.svc.HandleToolInit(ctx, "compliance_tools__migrate", "renamed tool", ToolInitMeta{
		Params:           &params,
		PackageToolNames: []string{"migrate"},
	})

	pkg, err := f.repo.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if pkg.DiscoveryStatus == "ready" {
		t.Fatalf("precondition: orphaned gate must have blocked ready; status=%s", pkg.DiscoveryStatus)
	}

	if _, err = f.svc.GetConfig(ctx, p.ID); err != nil {
		t.Fatalf("GetConfig must succeed for orphan recovery, got: %v", err)
	}

	if _, err = f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{reservedRequiredScopesConfigKey: map[string]any{}},
	}, "u"); err != nil {
		t.Fatalf("PatchConfig clear must succeed for orphan recovery, got: %v", err)
	}

	recovered, err := f.repo.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID after recovery: %v", err)
	}
	if recovered.DiscoveryStatus != "ready" {
		t.Errorf("package must return to ready after clearing the orphaned gate; status=%s", recovered.DiscoveryStatus)
	}
}

func TestOrphanedRequiredScopes_PartialDiscoveryNotForcedReady(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyPkgForDeploy(ctx, t, f, "compliance_tools", "run_migrations", `[]`)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			reservedRequiredScopesConfigKey: map[string]any{
				"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"},
			},
		},
	}, "u"); err != nil {
		t.Fatalf("author gate: %v", err)
	}

	if err := f.repo.UpdateZipInfo(ctx, p.ID, "python", "s3://fake", "pkg.zip", 2, "tester"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}
	params := `{"type":"object"}`
	f.svc.HandleToolInit(ctx, "compliance_tools__migrate", "renamed tool", ToolInitMeta{
		Params:           &params,
		PackageToolNames: []string{"migrate", "helper"},
	})
	if err := f.repo.AppendDiscoveryError(ctx, p.ID, `tool "helper" rejected: empty description`); err != nil {
		t.Fatalf("AppendDiscoveryError: %v", err)
	}

	pre, err := f.repo.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if pre.DiscoveryStatus != "pending" {
		t.Fatalf("precondition: partial discovery must leave the package pending; status=%s", pre.DiscoveryStatus)
	}

	if _, err = f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{reservedRequiredScopesConfigKey: map[string]any{}},
	}, "u"); err != nil {
		t.Fatalf("PatchConfig clear: %v", err)
	}

	got, err := f.repo.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetByID after clear: %v", err)
	}
	if got.DiscoveryStatus == "ready" {
		t.Errorf("partial-discovery package must NOT be forced ready by clearing an orphaned gate; status=%s", got.DiscoveryStatus)
	}
	if got.DiscoveryErrors == nil || !strings.Contains(*got.DiscoveryErrors, "helper") {
		t.Errorf("unrelated discovery error must survive the gate clear; got %v", got.DiscoveryErrors)
	}
}
