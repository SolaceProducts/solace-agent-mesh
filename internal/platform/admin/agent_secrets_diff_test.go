// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type agentOverlayFixture struct {
	svc       *AgentService
	agentID   string
	pkgName   string
	skillName string
}

func newAgentOverlayFixture(t *testing.T) *agentOverlayFixture {
	t.Helper()
	ctx := t.Context()
	db := testOpenDB(t)
	logger := slog.New(slog.DiscardHandler)

	toolRepo := store.NewToolPackageRepo(db)
	storage := newToolsetStorage(objectstore.NewStore(objectstore.NewMemoryClient()), logger)
	toolsetSvc := NewToolsetService(db, toolRepo, storage, nil, "testns", logger)
	tf := &toolsetFixture{svc: toolsetSvc, repo: toolRepo, ns: "testns"}
	pkg := seedReadyToolWithSchema(ctx, t, tf, "pkg_overlay", "do", cfgSchemaSecretRequired)

	agentRepo := store.NewAgentRepo(db)
	desc := "agent used for overlay secrets-diff testing"
	prompt := "You are a helpful assistant that always answers politely and accurately to the best of your ability."
	agent := &store.Agent{
		Name: "Overlay Agent", Type: "standard",
		Description: &desc, SystemPrompt: &prompt,
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	toolsetOverlay := `{"do":{"api_key":"sk-toolset-stored","model_id":"gpt-5"}}`
	if err := toolRepo.SetToolsetConfigsForAgent(ctx, agent.ID, []store.AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: pkg.ID, ConfigValues: &toolsetOverlay},
	}); err != nil {
		t.Fatalf("seed toolset overlay: %v", err)
	}

	skillRepo := store.NewSkillRepo(db)
	bt := skillSchemaSecretRequired
	sk := &store.Skill{
		Name: "render_skill", Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := skillRepo.Create(ctx, sk); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	skillOverlay := `{"render":{"api_key":"sk-skill-stored"}}`
	if err := skillRepo.SetAgentSkillsWithConfigs(ctx, agent.ID, []store.AgentSkillRow{
		{AgentID: agent.ID, SkillID: sk.ID, ConfigValues: &skillOverlay},
	}); err != nil {
		t.Fatalf("seed skill overlay: %v", err)
	}

	svc := NewAgentService(agentRepo, nil, nil, toolRepo, nil, nil, logger)
	svc.WithSkillRepo(skillRepo)
	return &agentOverlayFixture{
		svc:       svc,
		agentID:   agent.ID,
		pkgName:   "pkg_overlay",
		skillName: sk.Name,
	}
}

func TestAgentService_GetOverlaySecretsDiff_ToolsetOverlay(t *testing.T) {
	t.Parallel()
	f := newAgentOverlayFixture(t)
	ctx := t.Context()

	cases := []struct {
		name      string
		candidate map[string]any
		want      bool
	}{
		{"same value matches", map[string]any{"do": map[string]any{"api_key": "sk-toolset-stored"}}, true},
		{"rotated value mismatches", map[string]any{"do": map[string]any{"api_key": "sk-rotated"}}, false},
		{"redacted sentinel preserves", map[string]any{"do": map[string]any{"api_key": redactedSentinel}}, true},
		{"omitted preserves", map[string]any{"do": map[string]any{"model_id": "gpt-5"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := f.svc.GetOverlaySecretsDiff(ctx, f.agentID, AgentOverlayCandidate{
				ToolsetConfigs: []ToolsetConfigBinding{{ToolsetName: f.pkgName, ConfigValues: tc.candidate}},
			})
			if err != nil {
				t.Fatalf("GetOverlaySecretsDiff: %v", err)
			}
			if _, ok := res["pkg_overlay/do.model_id"]; ok {
				t.Errorf("non-secret model_id leaked into diff result: %+v", res)
			}
			got, ok := res["pkg_overlay/do.api_key"]
			if !ok {
				t.Fatalf("pkg_overlay/do.api_key missing from diff result: %+v", res)
			}
			if got.Matches != tc.want {
				t.Errorf("pkg_overlay/do.api_key matches = %v; want %v", got.Matches, tc.want)
			}
		})
	}
}

func TestAgentService_GetOverlaySecretsDiff_SkillOverlay(t *testing.T) {
	t.Parallel()
	f := newAgentOverlayFixture(t)
	ctx := t.Context()

	cases := []struct {
		name      string
		candidate map[string]map[string]any
		want      bool
	}{
		{"same value matches", map[string]map[string]any{"render": {"api_key": "sk-skill-stored"}}, true},
		{"rotated value mismatches", map[string]map[string]any{"render": {"api_key": "sk-rotated"}}, false},
		{"redacted sentinel preserves", map[string]map[string]any{"render": {"api_key": redactedSentinel}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := f.svc.GetOverlaySecretsDiff(ctx, f.agentID, AgentOverlayCandidate{
				SkillConfigs: []SkillConfigBinding{{SkillName: f.skillName, ConfigValues: tc.candidate}},
			})
			if err != nil {
				t.Fatalf("GetOverlaySecretsDiff: %v", err)
			}
			got, ok := res["render_skill/render.api_key"]
			if !ok {
				t.Fatalf("render_skill/render.api_key missing from diff result: %+v", res)
			}
			if got.Matches != tc.want {
				t.Errorf("render_skill/render.api_key matches = %v; want %v", got.Matches, tc.want)
			}
		})
	}
}

func TestAgentService_GetOverlaySecretsDiff_SkipsUnknownBindings(t *testing.T) {
	t.Parallel()
	f := newAgentOverlayFixture(t)

	res, err := f.svc.GetOverlaySecretsDiff(t.Context(), f.agentID, AgentOverlayCandidate{
		ToolsetConfigs: []ToolsetConfigBinding{{ToolsetName: "no_such_toolset", ConfigValues: map[string]any{"do": map[string]any{"api_key": "x"}}}},
		SkillConfigs:   []SkillConfigBinding{{SkillName: "no_such_skill", ConfigValues: map[string]map[string]any{"render": {"api_key": "x"}}}},
	})
	if err != nil {
		t.Fatalf("GetOverlaySecretsDiff: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected empty diff result for unknown bindings, got %+v", res)
	}
}

func TestAgentService_GetOverlaySecretsDiff_UnknownAgent(t *testing.T) {
	t.Parallel()
	f := newAgentOverlayFixture(t)

	_, err := f.svc.GetOverlaySecretsDiff(t.Context(), "ffffffff-ffff-ffff-ffff-ffffffffffff", AgentOverlayCandidate{})
	var nfErr *NotFoundError
	if !errors.As(err, &nfErr) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}
