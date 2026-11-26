// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type agentSkillBindingsFixture struct {
	skillRepo *store.SkillRepo
	agentRepo *store.AgentRepo
	agentID   string
	skill     *store.Skill
}

func newAgentSkillBindingsFixture(t *testing.T) *agentSkillBindingsFixture {
	t.Helper()
	db := testOpenDB(t)
	skillRepo := store.NewSkillRepo(db)
	agentRepo := store.NewAgentRepo(db)
	ctx := t.Context()

	bt := skillSchemaSecretRequired
	sk := &store.Skill{
		Name: "bindings_skill", Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "test", UpdatedBy: "test",
	}
	if err := skillRepo.Create(ctx, sk); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	agentID := "f1000000-0000-0000-0000-000000000aaa"
	agent := &store.Agent{ID: agentID, Name: "binding-agent", Description: strPtr("a"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return &agentSkillBindingsFixture{
		skillRepo: skillRepo,
		agentRepo: agentRepo,
		agentID:   agentID,
		skill:     sk,
	}
}

func TestBuildAgentSkillRows_HappyPath(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	rows, err := buildAgentSkillRows(t.Context(), f.agentID, []string{f.skill.ID},
		[]SkillConfigBinding{{
			SkillID: f.skill.ID,
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": "sk-override", "model_id": "gpt-4"},
			},
		}}, f.skillRepo)
	if err != nil {
		t.Fatalf("buildAgentSkillRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.SkillID != f.skill.ID || row.AgentID != f.agentID {
		t.Errorf("row IDs wrong: %+v", row)
	}
	if row.ConfigValues == nil {
		t.Fatal("ConfigValues nil — expected serialized JSON for a non-empty binding")
	}
	var got map[string]map[string]any
	if err := json.Unmarshal([]byte(*row.ConfigValues), &got); err != nil {
		t.Fatalf("unmarshal stored row: %v", err)
	}
	if got["render"]["api_key"] != "sk-override" {
		t.Errorf("api_key = %v, want sk-override", got["render"]["api_key"])
	}
	if got["render"]["model_id"] != "gpt-4" {
		t.Errorf("model_id = %v, want gpt-4", got["render"]["model_id"])
	}
}

func TestBuildAgentSkillRows_NoBindingProducesNullColumn(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	rows, err := buildAgentSkillRows(t.Context(), f.agentID, []string{f.skill.ID}, nil, f.skillRepo)
	if err != nil {
		t.Fatalf("buildAgentSkillRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].ConfigValues != nil {
		t.Errorf("expected nil ConfigValues for no-binding case, got %v", *rows[0].ConfigValues)
	}
}

func TestBuildAgentSkillRows_RejectsStaleSkillID(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	staleID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	_, err := buildAgentSkillRows(t.Context(), f.agentID, []string{f.skill.ID},
		[]SkillConfigBinding{{
			SkillID: staleID,
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": "sk-stale"},
			},
		}}, f.skillRepo)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if len(verr.Details[staleID]) == 0 {
		t.Errorf("expected Details entry for stale skill ID, got %+v", verr.Details)
	}
	if !strings.Contains(verr.Details[staleID][0], "not bound to this agent") {
		t.Errorf("expected 'not bound to this agent' diagnostic, got %q", verr.Details[staleID][0])
	}
}

func TestBuildAgentSkillRows_REDACTEDRoundTripPreservesSecret(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	ctx := t.Context()

	prior := `{"render":{"api_key":"sk-real","model_id":"gpt-4"}}`
	if err := f.skillRepo.SetAgentSkillsWithConfigs(ctx, f.agentID, []store.AgentSkillRow{{
		AgentID: f.agentID, SkillID: f.skill.ID, ConfigValues: &prior,
	}}); err != nil {
		t.Fatalf("seed prior binding: %v", err)
	}

	rows, err := buildAgentSkillRows(ctx, f.agentID, []string{f.skill.ID},
		[]SkillConfigBinding{{
			SkillID: f.skill.ID,
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": redactedSentinel, "model_id": "gpt-5"},
			},
		}}, f.skillRepo)
	if err != nil {
		t.Fatalf("buildAgentSkillRows: %v", err)
	}
	if len(rows) != 1 || rows[0].ConfigValues == nil {
		t.Fatalf("expected non-nil ConfigValues, got %+v", rows)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal([]byte(*rows[0].ConfigValues), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["render"]["api_key"] != "sk-real" {
		t.Errorf("api_key = %v, want sk-real preserved (REDACTED round-trip failed)", got["render"]["api_key"])
	}
	if got["render"]["model_id"] != "gpt-5" {
		t.Errorf("model_id = %v, want gpt-5", got["render"]["model_id"])
	}
}

func TestBuildAgentSkillRows_RejectsUnknownToolAndField(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	_, err := buildAgentSkillRows(t.Context(), f.agentID, []string{f.skill.ID},
		[]SkillConfigBinding{{
			SkillID: f.skill.ID,
			ConfigValues: map[string]map[string]any{
				"ghost":  {"x": "y"},
				"render": {"unknown_field": "ignore"},
			},
		}}, f.skillRepo)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if _, ok := verr.Details["bindings_skill/ghost"]; !ok {
		t.Errorf("expected 'bindings_skill/ghost' detail, got %+v", verr.Details)
	}
	if _, ok := verr.Details["bindings_skill/render.unknown_field"]; !ok {
		t.Errorf("expected 'bindings_skill/render.unknown_field' detail, got %+v", verr.Details)
	}
}

func TestBuildAgentSkillRows_ResolvesByName(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	rows, err := buildAgentSkillRows(t.Context(), f.agentID, []string{f.skill.ID},
		[]SkillConfigBinding{{
			SkillName: f.skill.Name,
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": "sk-byname"},
			},
		}}, f.skillRepo)
	if err != nil {
		t.Fatalf("buildAgentSkillRows: %v", err)
	}
	if len(rows) != 1 || rows[0].ConfigValues == nil {
		t.Fatalf("expected row with values, got %+v", rows)
	}
}

func TestLoadAgentSkillConfigBindings_RedactsSecrets(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	ctx := t.Context()

	stored := `{"render":{"api_key":"sk-leakable","model_id":"gpt-4"}}`
	if err := f.skillRepo.SetAgentSkillsWithConfigs(ctx, f.agentID, []store.AgentSkillRow{{
		AgentID: f.agentID, SkillID: f.skill.ID, ConfigValues: &stored,
	}}); err != nil {
		t.Fatalf("seed agent_skills row: %v", err)
	}

	bindings, bindingsErr := loadAgentSkillConfigBindings(ctx, f.agentID, f.skillRepo)
	if bindingsErr != nil {
		t.Fatalf("loadAgentSkillConfigBindings: %v", bindingsErr)
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	render := bindings[0].ConfigValues["render"]
	if render["api_key"] != redactedSentinel {
		t.Errorf("api_key = %v, want %q (secret leak)", render["api_key"], redactedSentinel)
	}
	if render["model_id"] != "gpt-4" {
		t.Errorf("model_id = %v, want gpt-4 (non-secret should pass through)", render["model_id"])
	}
}

func TestLoadAgentSkillConfigBindings_NilRepoReturnsNil(t *testing.T) {
	t.Parallel()
	if got, _ := loadAgentSkillConfigBindings(t.Context(), "any", nil); got != nil {
		t.Errorf("expected nil for nil repo, got %v", got)
	}
}

func TestLoadAgentSkillConfigBindings_EmptyBindingOmitted(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	ctx := t.Context()
	if err := f.skillRepo.SetAgentSkillsWithConfigs(ctx, f.agentID, []store.AgentSkillRow{{
		AgentID: f.agentID, SkillID: f.skill.ID,
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got, _ := loadAgentSkillConfigBindings(ctx, f.agentID, f.skillRepo); got != nil {
		t.Errorf("expected nil bindings when no overrides, got %v", got)
	}
}

func TestLoadSkillConfigFields_CoverageMatrix(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	ctx := t.Context()

	skillVals := `{"render":{"model_id":"gpt-4"}}`
	if err := f.skillRepo.SetSkillConfig(ctx, f.skill.ID, &skillVals, "ops"); err != nil {
		t.Fatalf("seed skill_configs: %v", err)
	}
	agentVals := `{"render":{"api_key":"sk-agent"}}`
	if err := f.skillRepo.SetAgentSkillsWithConfigs(ctx, f.agentID, []store.AgentSkillRow{{
		AgentID: f.agentID, SkillID: f.skill.ID, ConfigValues: &agentVals,
	}}); err != nil {
		t.Fatalf("seed agent_skills: %v", err)
	}

	out := loadSkillConfigFields(ctx, f.agentID, f.skillRepo, slog.New(slog.DiscardHandler))
	if out == nil {
		t.Fatal("loadSkillConfigFields returned nil")
	}
	fields, ok := out[f.skill.ID]
	if !ok {
		t.Fatalf("no entry for skill ID %s", f.skill.ID)
	}
	got := map[string]SkillConfigField{}
	for _, f := range fields {
		got[f.Key] = f
	}
	if !got["api_key"].CoveredByAgent {
		t.Errorf("api_key.CoveredByAgent = false, want true (agent set it)")
	}
	if got["api_key"].CoveredBySkill {
		t.Errorf("api_key.CoveredBySkill = true, want false (skill row didn't set it)")
	}
	if !got["model_id"].CoveredBySkill {
		t.Errorf("model_id.CoveredBySkill = false, want true (skill row set it)")
	}
	if got["model_id"].CoveredByAgent {
		t.Errorf("model_id.CoveredByAgent = true, want false (agent didn't override)")
	}
}

func TestLoadSkillConfigFields_MalformedJSONLogged(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	ctx := t.Context()

	bad := `{"render":{"api_key":`
	if err := f.skillRepo.SetAgentSkillsWithConfigs(ctx, f.agentID, []store.AgentSkillRow{{
		AgentID: f.agentID, SkillID: f.skill.ID, ConfigValues: &bad,
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := loadSkillConfigFields(ctx, f.agentID, f.skillRepo, slog.New(slog.DiscardHandler))
	if out == nil {
		t.Fatal("expected non-nil map even with malformed JSON")
	}
	fields := out[f.skill.ID]
	for _, f := range fields {
		if f.CoveredByAgent {
			t.Errorf("field %q reported CoveredByAgent despite malformed JSON", f.Key)
		}
	}
}

func TestLoadSkillConfigFields_NilRepo(t *testing.T) {
	t.Parallel()
	if got := loadSkillConfigFields(t.Context(), "any", nil, nil); got != nil {
		t.Errorf("expected nil for nil repo, got %v", got)
	}
}

func TestResolveAgentSkillBindings_UnknownNameReportedAsDetail(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	skillIDSet := map[string]struct{}{f.skill.ID: {}}

	configs, details, err := resolveAgentSkillBindings(t.Context(),
		[]SkillConfigBinding{{
			SkillName: "this-skill-doesnt-exist",
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": "ignored"},
			},
		}}, skillIDSet, f.skillRepo)
	if err != nil {
		t.Fatalf("unexpected internal error: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("expected zero configs for unknown name, got %v", configs)
	}
	if _, ok := details["this-skill-doesnt-exist"]; !ok {
		t.Errorf("expected detail for the unknown name, got %v", details)
	}
}

func TestMergeAgentSkillBindingSecrets_NoPriorReturnsIncoming(t *testing.T) {
	t.Parallel()
	schemas := map[string][]configSchemaField{
		"render": {{Key: "api_key", Secret: true}, {Key: "model_id"}},
	}
	got, err := mergeAgentSkillBindingSecrets(map[string]map[string]any{
		"render": {"api_key": "sk-new", "model_id": "gpt-5"},
	}, nil, schemas)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got["render"]["api_key"] != "sk-new" {
		t.Errorf("api_key = %v, want sk-new (no prior, sentinel logic should not fire)", got["render"]["api_key"])
	}
}

func TestMergeAgentSkillBindingSecrets_MalformedPriorTolerated(t *testing.T) {
	t.Parallel()
	bad := `{"render":`
	got, err := mergeAgentSkillBindingSecrets(map[string]map[string]any{
		"render": {"api_key": "sk-new"},
	}, &bad, map[string][]configSchemaField{
		"render": {{Key: "api_key", Secret: true}},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got["render"]["api_key"] != "sk-new" {
		t.Errorf("api_key = %v, want sk-new (malformed prior should not corrupt new value)", got["render"]["api_key"])
	}
}

func TestMergeAgentSkillBindingSecrets_MalformedPriorRejectsSentinel(t *testing.T) {
	t.Parallel()
	bad := `{"render":`
	_, err := mergeAgentSkillBindingSecrets(map[string]map[string]any{
		"render": {"api_key": redactedSentinel, "model_id": "gpt-5"},
	}, &bad, map[string][]configSchemaField{
		"render": {{Key: "api_key", Secret: true}, {Key: "model_id"}},
	})
	if err == nil {
		t.Fatal("merge with an undecodable prior accepted a <REDACTED> it could not resolve")
	}
}

func TestBuildAgentSkillRows_SentinelForToolWithNoPriorIsDropped(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	ctx := t.Context()

	bt := `[
		{"tool_name":"render","config_schema":[{"key":"api_key","type":"string","secret":true},{"key":"model_id","type":"string"}]},
		{"tool_name":"export","config_schema":[{"key":"token","type":"string","secret":true},{"key":"format","type":"string"}]}
	]`
	sk := &store.Skill{
		Name: "two_tool_skill", Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "test", UpdatedBy: "test",
	}
	if err := f.skillRepo.Create(ctx, sk); err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	prior := `{"render":{"api_key":"sk-real","model_id":"gpt-4"}}`
	if err := f.skillRepo.SetAgentSkillsWithConfigs(ctx, f.agentID, []store.AgentSkillRow{{
		AgentID: f.agentID, SkillID: sk.ID, ConfigValues: &prior,
	}}); err != nil {
		t.Fatalf("seed prior binding: %v", err)
	}

	rows, err := buildAgentSkillRows(ctx, f.agentID, []string{sk.ID},
		[]SkillConfigBinding{{
			SkillID: sk.ID,
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": redactedSentinel, "model_id": "gpt-4"},
				"export": {"token": redactedSentinel, "format": "csv"},
			},
		}}, f.skillRepo)
	if err != nil {
		t.Fatalf("buildAgentSkillRows: %v", err)
	}
	if len(rows) != 1 || rows[0].ConfigValues == nil {
		t.Fatalf("expected one row with config values, got %+v", rows)
	}
	want := `{"export":{"format":"csv"},"render":{"api_key":"sk-real","model_id":"gpt-4"}}`
	if got := *rows[0].ConfigValues; got != want {
		t.Errorf("config values = %s, want %s", got, want)
	}
}

func TestValidateAgentSkillBindingValues_TypeMismatch(t *testing.T) {
	t.Parallel()
	schemas := map[string][]configSchemaField{
		"render": {{Key: "timeout", Type: "integer"}},
	}
	_, details := validateAgentSkillBindingValues("plotter",
		map[string]map[string]any{
			"render": {"timeout": "not-an-int"},
		}, schemas)
	if _, ok := details["plotter/render.timeout"]; !ok {
		t.Errorf("expected validation detail for type mismatch, got %v", details)
	}
}

func TestBuildAgentSkillRows_SkillNotInSet_NameOnly(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	ctx := t.Context()

	_, err := buildAgentSkillRows(ctx, f.agentID, []string{},
		[]SkillConfigBinding{{
			SkillName: f.skill.Name,
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": "sk-attempt"},
			},
		}}, f.skillRepo)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestBuildAgentSkillRows_EmptyBindingsListNoError(t *testing.T) {
	t.Parallel()
	f := newAgentSkillBindingsFixture(t)
	rows, err := buildAgentSkillRows(t.Context(), f.agentID, []string{f.skill.ID}, []SkillConfigBinding{}, f.skillRepo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].ConfigValues != nil {
		t.Errorf("expected nil ConfigValues, got %v", *rows[0].ConfigValues)
	}
}
