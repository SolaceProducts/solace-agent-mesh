// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newSeedAgentService(t *testing.T, db *store.DB) (*AgentService, *store.AgentRepo) {
	t.Helper()
	agentRepo := store.NewAgentRepo(db)
	svc := NewAgentService(agentRepo, store.NewAgentDeploymentRepo(db), store.NewConnectorRepo(db), store.NewToolPackageRepo(db), platpkg.NewAgentRegistry(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.WithSkillRepo(store.NewSkillRepo(db))
	return svc, agentRepo
}

func TestSeedBuiltinAgents_EmptyDBSeedsOrchestrator(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("SeedBuiltinAgents: %v", err)
	}

	all, err := agentRepo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected exactly 2 seeded agents (Orchestrator + Sentinel), got %d", len(all))
	}

	var orchID string
	for _, a := range all {
		if a.Name == builtinOrchestratorName {
			orchID = a.ID
		}
	}
	if orchID == "" {
		t.Fatalf("Orchestrator not found among seeded agents: %+v", all)
	}

	resp, err := svc.GetByID(ctx, orchID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if resp.Name != builtinOrchestratorName {
		t.Errorf("name = %q, want %q", resp.Name, builtinOrchestratorName)
	}
	if resp.Type != AgentTypeStandard {
		t.Errorf("type = %q, want %q", resp.Type, AgentTypeStandard)
	}
	if got := resp.ModelProvider; len(got) != 1 || got[0] != "general" {
		t.Errorf("modelProvider = %v, want [general]", got)
	}
	for _, unwanted := range []string{"builtin_artifact_tools", "data_analysis"} {
		if slices.Contains(resp.Toolsets, unwanted) {
			t.Errorf("toolsets %v include %q; the Orchestrator delegates file work", resp.Toolsets, unwanted)
		}
	}
	for _, want := range builtinOrchestratorSkillRefs {
		if !slices.Contains(resp.SkillRefs, want) {
			t.Errorf("skillRefs %v missing %q", resp.SkillRefs, want)
		}
	}
	if resp.SystemPrompt == "" {
		t.Error("systemPrompt is empty")
	}

	var extra struct {
		InterAgentCommunication struct {
			AllowList []string `json:"allowList"`
		} `json:"interAgentCommunication"`
		AgentCard struct {
			Welcome struct {
				Message     string `json:"message"`
				Suggestions []any  `json:"suggestions"`
			} `json:"welcome"`
		} `json:"agentCard"`
	}
	if err := json.Unmarshal(resp.AdditionalConfigurations, &extra); err != nil {
		t.Fatalf("unmarshal additionalConfigurations: %v", err)
	}
	if got := extra.InterAgentCommunication.AllowList; len(got) != 1 || got[0] != "*" {
		t.Errorf("allowList = %v, want [*]", got)
	}
	if extra.AgentCard.Welcome.Message == "" {
		t.Error("welcome message is empty")
	}
	if len(extra.AgentCard.Welcome.Suggestions) == 0 {
		t.Error("welcome suggestions are empty")
	}
}

func TestSeedBuiltinAgents_SeedsSentinel(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("SeedBuiltinAgents: %v", err)
	}

	all, err := agentRepo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	var sentinelID string
	for _, a := range all {
		if a.Name == builtinSentinelName {
			sentinelID = a.ID
		}
	}
	if sentinelID == "" {
		t.Fatalf("Sentinel not found among seeded agents: %+v", all)
	}

	resp, err := svc.GetByID(ctx, sentinelID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	for _, want := range []string{
		"builtin_artifact_tools", "data_analysis", "observability_analytics",
		"builtin_document_tools", "builtin_diagram_tools",
		"observability_platform_read",
	} {
		if !slices.Contains(resp.Toolsets, want) {
			t.Errorf("Sentinel toolsets %v missing %q", resp.Toolsets, want)
		}
	}
	if slices.Contains(resp.Toolsets, "observability_datadog") {
		t.Errorf("Sentinel should not seed observability_datadog (log analysis is opt-in): %v", resp.Toolsets)
	}
	for _, want := range []string{"sam-observability", "sam-excel", "sam-docs"} {
		if !slices.Contains(resp.SkillRefs, want) {
			t.Errorf("Sentinel skillRefs %v missing %q", resp.SkillRefs, want)
		}
	}

	var extra struct {
		RequiredScopes []string `json:"requiredScopes"`
		AgentCard      struct {
			Welcome struct {
				Message string `json:"message"`
			} `json:"welcome"`
		} `json:"agentCard"`
	}
	if err := json.Unmarshal(resp.AdditionalConfigurations, &extra); err != nil {
		t.Fatalf("unmarshal additionalConfigurations: %v", err)
	}
	if !slices.Contains(extra.RequiredScopes, "analytics:_:read") {
		t.Errorf("Sentinel requiredScopes = %v, want to contain analytics:_:read", extra.RequiredScopes)
	}
	if extra.AgentCard.Welcome.Message == "" {
		t.Error("Sentinel welcome message is empty")
	}
}

func TestSeedBuiltinAgents_SeedsActivity(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, true, logger); err != nil {
		t.Fatalf("SeedBuiltinAgents: %v", err)
	}

	all, err := agentRepo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	var activityID string
	for _, a := range all {
		if a.Name == BuiltinActivityMonitorName {
			activityID = a.ID
		}
	}
	if activityID == "" {
		t.Fatalf("Activity Monitor not found among seeded agents: %+v", all)
	}

	resp, err := svc.GetByID(ctx, activityID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	for _, want := range []string{"activity_reporting", "data_analysis"} {
		if !slices.Contains(resp.Toolsets, want) {
			t.Errorf("Activity Monitor toolsets %v missing %q", resp.Toolsets, want)
		}
	}

	var extra struct {
		RequiredScopes []string `json:"requiredScopes"`
	}
	if err := json.Unmarshal(resp.AdditionalConfigurations, &extra); err != nil {
		t.Fatalf("unmarshal additionalConfigurations: %v", err)
	}
	if len(extra.RequiredScopes) != 0 {
		t.Errorf("Activity Monitor requiredScopes = %v, want empty (invoke gated by standard agent-invoke machinery)",
			extra.RequiredScopes)
	}
}

func TestSeedBuiltinAgents_ActivityDisabled(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("SeedBuiltinAgents: %v", err)
	}
	all, err := agentRepo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	for _, a := range all {
		if a.Name == BuiltinActivityMonitorName {
			t.Errorf("Activity Monitor seeded despite flag off: %+v", a)
		}
	}
}

func TestSeedBuiltinAgents_SentinelDisabled(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("SeedBuiltinAgents: %v", err)
	}
	all, err := agentRepo.GetAll(ctx)
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("sentinel disabled: expected only the Orchestrator, got %d agents: %+v", len(all), all)
	}
	if all[0].Name != builtinOrchestratorName {
		t.Errorf("seeded agent = %q, want %q", all[0].Name, builtinOrchestratorName)
	}
}

func TestSeedBuiltinAgents_Idempotent(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	first, _ := agentRepo.GetAll(ctx)
	if len(first) != 2 {
		t.Fatalf("after first seed: got %d agents, want 2", len(first))
	}

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	second, _ := agentRepo.GetAll(ctx)
	if len(second) != 2 {
		t.Fatalf("after second seed: got %d agents, want 2", len(second))
	}
	firstIDs := map[string]bool{}
	for _, a := range first {
		firstIDs[a.ID] = true
	}
	for _, a := range second {
		if !firstIDs[a.ID] {
			t.Errorf("seeded agent row changed across seeds: new ID %q", a.ID)
		}
	}
}

func TestSeedBuiltinAgents_SeedsSentinelOnExistingDeployment(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("seed (flag off): %v", err)
	}
	before, _ := agentRepo.GetAll(ctx)
	if len(before) == 0 {
		t.Fatal("expected the Orchestrator to be seeded on the empty table")
	}
	for _, a := range before {
		if a.Name == builtinSentinelName {
			t.Fatal("Sentinel must not be seeded while the flag is off")
		}
	}

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("seed (flag on): %v", err)
	}
	after, _ := agentRepo.GetAll(ctx)
	sentinel, orch := 0, 0
	for _, a := range after {
		switch a.Name {
		case builtinSentinelName:
			sentinel++
		case builtinOrchestratorName:
			orch++
		}
	}
	if sentinel != 1 {
		t.Errorf("expected exactly 1 Sentinel seeded on the existing deployment, got %d", sentinel)
	}
	if orch != 1 {
		t.Errorf("expected the Orchestrator not to be duplicated, got %d", orch)
	}
}

func TestSeedBuiltinAgents_SentinelCreateErrorPropagates(t *testing.T) {
	_, agentRepo := newSeedAgentService(t, testOpenDB(t))

	brokenDB := testOpenDB(t)
	brokenSvc, _ := newSeedAgentService(t, brokenDB)
	if err := brokenDB.Close(); err != nil {
		t.Fatalf("close broken db: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := SeedBuiltinAgents(t.Context(), brokenSvc, agentRepo, true, false, logger); err == nil {
		t.Fatal("expected the Sentinel createBuiltinAgent error to propagate")
	}
}

func TestSeedBuiltinAgents_SentinelBuildErrorPropagates(t *testing.T) {
	orig := buildSentinelRequest
	t.Cleanup(func() { buildSentinelRequest = orig })
	buildSentinelRequest = func() (CreateAgentRequest, error) {
		return CreateAgentRequest{}, errors.New("synthetic build failure")
	}

	svc, agentRepo := newSeedAgentService(t, testOpenDB(t))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := SeedBuiltinAgents(t.Context(), svc, agentRepo, true, false, logger)
	if err == nil || !strings.Contains(err.Error(), "build sentinel") {
		t.Fatalf("expected a wrapped build-sentinel error, got %v", err)
	}
}

func TestMaybeSeedBuiltinAgents(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mk := func(db *store.DB) (*store.AgentRepo, *store.AgentDeploymentRepo, *store.ConnectorRepo, *store.ToolPackageRepo, *store.SkillRepo) {
		return store.NewAgentRepo(db), store.NewAgentDeploymentRepo(db), store.NewConnectorRepo(db), store.NewToolPackageRepo(db), store.NewSkillRepo(db)
	}

	t.Run("disabled is a no-op", func(t *testing.T) {
		db := testOpenDB(t)
		ar, dr, cr, tr, sr := mk(db)
		maybeSeedBuiltinAgents(t.Context(), false, true, false, ar, dr, cr, tr, platpkg.NewAgentRegistry(), sr, logger)
		all, err := ar.GetAll(t.Context())
		if err != nil {
			t.Fatalf("GetAll: %v", err)
		}
		if len(all) != 0 {
			t.Errorf("disabled seed created %d agents, want 0", len(all))
		}
	})

	t.Run("enabled seeds the built-in agents", func(t *testing.T) {
		db := testOpenDB(t)
		ar, dr, cr, tr, sr := mk(db)
		maybeSeedBuiltinAgents(t.Context(), true, true, false, ar, dr, cr, tr, platpkg.NewAgentRegistry(), sr, logger)
		all, err := ar.GetAll(t.Context())
		if err != nil {
			t.Fatalf("GetAll: %v", err)
		}
		names := make(map[string]bool, len(all))
		for _, a := range all {
			names[a.Name] = true
		}
		if !names[builtinOrchestratorName] || !names[builtinSentinelName] {
			t.Errorf("enabled seed did not create both built-ins: %+v", all)
		}
	})

	t.Run("enabled logs but does not panic on error", func(t *testing.T) {
		db := testOpenDB(t)
		ar, dr, cr, tr, sr := mk(db)
		_ = db.Close()
		maybeSeedBuiltinAgents(t.Context(), true, true, false, ar, dr, cr, tr, platpkg.NewAgentRegistry(), sr, logger)
	})
}

func TestSeedBuiltinAgents_SurfacesRepoError(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	_ = db.Close()

	err := SeedBuiltinAgents(t.Context(), svc, agentRepo, true, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected an error when the agents table cannot be read")
	}
}

func TestSeedBuiltinAgents_NonEmptyDBSeedsAbsentOrchestrator(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if _, err := svc.Create(ctx, CreateAgentRequest{
		Name:         "MyAgent",
		Description:  "A pre-existing user agent carried across an upgrade.",
		SystemPrompt: "You are a helpful assistant used to verify seed-if-absent on a non-empty database.",
		Type:         AgentTypeStandard,
	}, "tester"); err != nil {
		t.Fatalf("create pre-existing agent: %v", err)
	}

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("SeedBuiltinAgents: %v", err)
	}

	all, _ := agentRepo.GetAll(ctx)
	names := make([]string, len(all))
	for i, a := range all {
		names[i] = a.Name
	}
	if len(all) != 2 {
		t.Fatalf("expected MyAgent + seeded Orchestrator, got %d agents: %v", len(all), names)
	}
	if !slices.Contains(names, builtinOrchestratorName) {
		t.Errorf("Orchestrator was not seeded into the non-empty table; agents: %v", names)
	}
	if !slices.Contains(names, "MyAgent") {
		t.Errorf("pre-existing agent was lost; agents: %v", names)
	}
}

func TestSeedBuiltinAgents_DeletedOrchestratorStaysDeleted(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	orch, _, _, _, _, err := agentRepo.GetByName(ctx, builtinOrchestratorName)
	if err != nil || orch == nil {
		t.Fatalf("Orchestrator not seeded: %v", err)
	}

	if err = agentRepo.Delete(ctx, orch.ID); err != nil {
		t.Fatalf("delete Orchestrator: %v", err)
	}

	if err = SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	after, _, _, _, _, err := agentRepo.GetByName(ctx, builtinOrchestratorName)
	if err != nil {
		t.Fatalf("lookup after reseed: %v", err)
	}
	if after != nil {
		t.Fatalf("deleted Orchestrator was re-seeded: %+v", after)
	}
}

func TestSeedBuiltinAgents_BackfillsMarkerForPreMarkerRow(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	orchReq, err := buildBuiltinOrchestratorRequest()
	if err != nil {
		t.Fatalf("build orchestrator: %v", err)
	}
	if _, err := svc.Create(ctx, orchReq, builtinAgentActor); err != nil {
		t.Fatalf("pre-seed Orchestrator: %v", err)
	}
	if applied, err := store.IsSeedApplied(ctx, db, orchestratorSeedMarkerKey); err != nil || applied {
		t.Fatalf("precondition: marker must be absent (applied=%v err=%v)", applied, err)
	}

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if applied, err := store.IsSeedApplied(ctx, db, orchestratorSeedMarkerKey); err != nil || !applied {
		t.Fatalf("marker not backfilled for pre-marker row (applied=%v err=%v)", applied, err)
	}
	all, _ := agentRepo.GetAll(ctx)
	orchCount := 0
	for _, a := range all {
		if a.Name == builtinOrchestratorName {
			orchCount++
		}
	}
	if orchCount != 1 {
		t.Fatalf("backfill must not duplicate the Orchestrator: got %d", orchCount)
	}

	orch, _, _, _, _, _ := agentRepo.GetByName(ctx, builtinOrchestratorName)
	if err := agentRepo.Delete(ctx, orch.ID); err != nil {
		t.Fatalf("delete Orchestrator: %v", err)
	}
	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	after, _, _, _, _, _ := agentRepo.GetByName(ctx, builtinOrchestratorName)
	if after != nil {
		t.Fatalf("pre-marker Orchestrator re-seeded after backfill+delete: %+v", after)
	}
}

func TestSeedBuiltinAgents_OrchestratorHasPriority(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, false, false, logger); err != nil {
		t.Fatalf("seed: %v", err)
	}
	orch, _, _, _, _, err := agentRepo.GetByName(ctx, builtinOrchestratorName)
	if err != nil || orch == nil {
		t.Fatalf("Orchestrator not seeded: %v", err)
	}
	resp, err := svc.GetByID(ctx, orch.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	var extra struct {
		AgentCard struct {
			Priority int `json:"priority"`
		} `json:"agentCard"`
	}
	if err := json.Unmarshal(resp.AdditionalConfigurations, &extra); err != nil {
		t.Fatalf("unmarshal additionalConfigurations: %v", err)
	}
	if extra.AgentCard.Priority != builtinOrchestratorPriority {
		t.Errorf("priority = %d, want %d", extra.AgentCard.Priority, builtinOrchestratorPriority)
	}
}

func TestSeedBuiltinAgents_DeployWithGatewayContextInjection(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, true, logger); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, name := range []string{builtinOrchestratorName, builtinSentinelName, BuiltinActivityMonitorName} {
		t.Run(name, func(t *testing.T) {
			agent, _, _, _, _, err := agentRepo.GetByName(ctx, name)
			if err != nil || agent == nil {
				t.Fatalf("%s not seeded: %v", name, err)
			}
			cfg, err := NewYAMLService("test-ns", logger).BuildAgentConfigFull(agent, nil, nil, nil, nil, ToolEmission{})
			if err != nil {
				t.Fatalf("BuildAgentConfigFull: %v", err)
			}
			appCfg := cfg["apps"].([]map[string]any)[0]["app_config"].(map[string]any)
			for _, key := range []string{"inject_system_purpose", "inject_response_format", "inject_user_profile"} {
				if got := appCfg[key]; got != true {
					t.Errorf("app_config %s = %v, want true", key, got)
				}
			}
		})
	}
}

func TestSeedBuiltinAgents_OrchestratorLookupErrorPropagates(t *testing.T) {
	db := testOpenDB(t)
	svc, agentRepo := newSeedAgentService(t, db)
	_ = db.Close()

	err := SeedBuiltinAgents(t.Context(), svc, agentRepo, false, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected an error when the agents table cannot be read")
	}
}

func TestReconcileBuiltinAgent_UpdatesDriftedSystemOwned(t *testing.T) {
	svc, agentRepo := newSeedAgentService(t, testOpenDB(t))
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	orig := buildSentinelRequest
	t.Cleanup(func() { buildSentinelRequest = orig })
	canonical, buildErr := orig()
	if buildErr != nil {
		t.Fatalf("build canonical sentinel: %v", buildErr)
	}

	stripped := canonical
	stripped.Toolsets = []string{"builtin_artifact_tools", "data_analysis"}
	stripped.SkillRefs = []string{"sam-observability"}
	buildSentinelRequest = func() (CreateAgentRequest, error) { return stripped, nil }
	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("initial seed: %v", err)
	}

	buildSentinelRequest = func() (CreateAgentRequest, error) { return canonical, nil }
	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("reseed: %v", err)
	}

	existing, _, _, _, _, err := agentRepo.GetByName(ctx, builtinSentinelName)
	if err != nil || existing == nil {
		t.Fatalf("lookup sentinel: %v", err)
	}
	if existing.UpdatedBy != builtinAgentActor {
		t.Errorf("reconcile must keep system ownership, updatedBy=%q", existing.UpdatedBy)
	}
	live, err := svc.GetByID(ctx, existing.ID)
	if err != nil {
		t.Fatalf("get sentinel: %v", err)
	}
	for _, want := range canonical.Toolsets {
		if !slices.Contains(live.Toolsets, want) {
			t.Errorf("after reconcile toolsets %v missing %q", live.Toolsets, want)
		}
	}
	for _, want := range canonical.SkillRefs {
		if !slices.Contains(live.SkillRefs, want) {
			t.Errorf("after reconcile skillRefs %v missing %q", live.SkillRefs, want)
		}
	}
	n := 0
	all, _ := agentRepo.GetAll(ctx)
	for _, a := range all {
		if a.Name == builtinSentinelName {
			n++
		}
	}
	if n != 1 {
		t.Errorf("reconcile must update in place, not duplicate: got %d Sentinels", n)
	}
}

func TestReconcileBuiltinAgent_PreservesOperatorEdits(t *testing.T) {
	svc, agentRepo := newSeedAgentService(t, testOpenDB(t))
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	orig := buildSentinelRequest
	t.Cleanup(func() { buildSentinelRequest = orig })
	canonical, buildErr := orig()
	if buildErr != nil {
		t.Fatalf("build canonical sentinel: %v", buildErr)
	}
	buildSentinelRequest = func() (CreateAgentRequest, error) { return canonical, nil }

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("seed: %v", err)
	}
	existing, _, _, _, _, err := agentRepo.GetByName(ctx, builtinSentinelName)
	if err != nil || existing == nil {
		t.Fatalf("lookup: %v", err)
	}

	op := builtinUpdateRequest(canonical)
	op.Toolsets = []string{"builtin_artifact_tools"}
	if _, updErr := svc.Update(ctx, existing.ID, op, "operator@example.com"); updErr != nil {
		t.Fatalf("operator update: %v", updErr)
	}

	if err = SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	after, _, _, _, _, err := agentRepo.GetByName(ctx, builtinSentinelName)
	if err != nil || after == nil {
		t.Fatalf("lookup after: %v", err)
	}
	if after.UpdatedBy != "operator@example.com" {
		t.Errorf("operator ownership lost: updatedBy=%q", after.UpdatedBy)
	}
	live, err := svc.GetByID(ctx, after.ID)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if !slices.Equal(live.Toolsets, []string{"builtin_artifact_tools"}) {
		t.Errorf("operator toolset edit reverted by reconcile: %v", live.Toolsets)
	}
}

func TestReconcileBuiltinAgent_NoDriftIsNoOp(t *testing.T) {
	svc, agentRepo := newSeedAgentService(t, testOpenDB(t))
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _, _, _, _, err := agentRepo.GetByName(ctx, builtinSentinelName)
	if err != nil || before == nil {
		t.Fatalf("lookup before: %v", err)
	}

	if err = SeedBuiltinAgents(ctx, svc, agentRepo, true, false, logger); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	after, _, _, _, _, err := agentRepo.GetByName(ctx, builtinSentinelName)
	if err != nil || after == nil {
		t.Fatalf("lookup after: %v", err)
	}
	if before.UpdatedTime != after.UpdatedTime {
		t.Errorf("drift-free reseed updated the row: updatedTime %d -> %d", before.UpdatedTime, after.UpdatedTime)
	}
	if after.UpdatedBy != builtinAgentActor {
		t.Errorf("drift-free reseed changed ownership: updatedBy=%q", after.UpdatedBy)
	}
}

func TestSameStringSet(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"a", "b"}, []string{"b", "a"}, true},
		{[]string{"a", "a", "b"}, []string{"b", "a"}, true},
		{[]string{"a"}, []string{"a", "b"}, false},
		{nil, []string{}, true},
		{[]string{"a"}, nil, false},
	}
	for _, c := range cases {
		if got := sameStringSet(c.a, c.b); got != c.want {
			t.Errorf("sameStringSet(%v,%v)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSameJSONObject(t *testing.T) {
	if !sameJSONObject(nil, nil) {
		t.Error("nil/nil should be equal")
	}
	if !sameJSONObject([]byte(`{"a":1,"b":2}`), []byte(`{"b":2,"a":1}`)) {
		t.Error("key order must not matter")
	}
	if sameJSONObject([]byte(`{"a":1}`), []byte(`{"a":2}`)) {
		t.Error("different values must differ")
	}
	if sameJSONObject([]byte(`{bad`), []byte(`{}`)) {
		t.Error("invalid json must not equal")
	}
}

func TestSameToolsetConfigs(t *testing.T) {
	a := []ToolsetConfigBinding{{ToolsetName: "sentinel_code", VolumeBindings: map[string]string{"workspace": "ws"}}}
	b := []ToolsetConfigBinding{{ToolsetName: "sentinel_code", VolumeBindings: map[string]string{"workspace": "ws"}}}
	if !sameToolsetConfigs(a, b) {
		t.Error("identical bindings must be equal")
	}
	if !sameToolsetConfigs(nil, []ToolsetConfigBinding{}) {
		t.Error("nil and empty must be equal")
	}
	c := []ToolsetConfigBinding{{ToolsetName: "sentinel_code", VolumeBindings: map[string]string{"workspace": "other"}}}
	if sameToolsetConfigs(a, c) {
		t.Error("different binding target must differ")
	}

	n1 := []ToolsetConfigBinding{{ToolsetName: "sentinel_code", NetworkBindings: map[string]string{"anthropic_api": "egress"}}}
	n2 := []ToolsetConfigBinding{{ToolsetName: "sentinel_code", NetworkBindings: map[string]string{"anthropic_api": "egress"}}}
	if !sameToolsetConfigs(n1, n2) {
		t.Error("identical network bindings must be equal")
	}
	n3 := []ToolsetConfigBinding{{ToolsetName: "sentinel_code", NetworkBindings: map[string]string{"anthropic_api": "other"}}}
	if sameToolsetConfigs(n1, n3) {
		t.Error("different network policy must differ")
	}
	if sameToolsetConfigs(a, []ToolsetConfigBinding{{ToolsetName: "sentinel_code", VolumeBindings: map[string]string{"workspace": "ws"}, NetworkBindings: map[string]string{"anthropic_api": "egress"}}}) {
		t.Error("adding a network binding must register as drift")
	}
}

func TestBuiltinConfigDrifted(t *testing.T) {
	base := CreateAgentRequest{
		Description:              "a built-in agent description",
		SystemPrompt:             "a system prompt that is long enough to be valid for the agent dto",
		Toolsets:                 []string{"a", "b"},
		SkillRefs:                []string{"s1"},
		AdditionalConfigurations: json.RawMessage(`{"x":1}`),
	}
	mk := func() *AgentResponse {
		return &AgentResponse{
			Description:              base.Description,
			SystemPrompt:             base.SystemPrompt,
			Toolsets:                 []string{"b", "a"},
			SkillRefs:                []string{"s1"},
			AdditionalConfigurations: json.RawMessage(`{"x":1}`),
		}
	}
	if builtinConfigDrifted(base, mk()) {
		t.Error("identical config (toolsets reordered) must not drift")
	}
	if l := mk(); func() bool { l.Toolsets = []string{"a"}; return !builtinConfigDrifted(base, l) }() {
		t.Error("toolset difference must drift")
	}
	if l := mk(); func() bool { l.SkillRefs = []string{"s2"}; return !builtinConfigDrifted(base, l) }() {
		t.Error("skillRef difference must drift")
	}
	if l := mk(); func() bool { l.SystemPrompt = "different"; return !builtinConfigDrifted(base, l) }() {
		t.Error("system prompt difference must drift")
	}
	if l := mk(); func() bool {
		l.AdditionalConfigurations = json.RawMessage(`{"x":2}`)
		return !builtinConfigDrifted(base, l)
	}() {
		t.Error("additionalConfigurations difference must drift")
	}
}

func TestActivityMonitorPrompt_RoutesFeedbackQuestions(t *testing.T) {
	req, err := buildBuiltinActivityMonitorRequest()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	prompt := strings.Join(strings.Fields(req.SystemPrompt), " ")
	for _, name := range []string{"summarize_feedback", "list_feedback", "explain_feedback"} {
		if !strings.Contains(prompt, name) {
			t.Errorf("prompt does not name %s", name)
		}
	}
	for _, rule := range []string{"read_trace", "trace access", "ratingEmoji", "updatedAt"} {
		if strings.Contains(prompt, rule) {
			t.Errorf("prompt restates %q, which belongs to the tool that returns it", rule)
		}
	}
}
