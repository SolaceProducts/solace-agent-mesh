// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	skillSchemaSecretRequired = `[{"tool_name":"render","config_schema":[{"key":"api_key","type":"string","description":"the key","secret":true},{"key":"model_id","type":"string","description":"model id","default":"gpt-4"}]}]`
	skillSchemaWithBoolInt    = `[{"tool_name":"render","config_schema":[{"key":"verbose","type":"boolean","default":false},{"key":"timeout","type":"integer","default":30}]}]`
	skillSchemaMultiTool      = `[
		{"tool_name":"render","config_schema":[{"key":"api_key","type":"string","secret":true,"required":true}]},
		{"tool_name":"cleanup","config_schema":[{"key":"ttl_seconds","type":"integer","default":3600}]}
	]`
)

type skillFixture struct {
	svc  *SkillService
	repo *store.SkillRepo
	db   *store.DB
}

func newSkillFixture(t *testing.T) *skillFixture {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))
	return &skillFixture{svc: svc, repo: repo, db: db}
}

func (f *skillFixture) seedReadySkill(ctx context.Context, t *testing.T, name, bundledToolsJSON string) string {
	t.Helper()
	bt := bundledToolsJSON
	sk := &store.Skill{
		Name: name, Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "test", UpdatedBy: "test",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed skill %q: %v", name, err)
	}
	return sk.ID
}

func TestSkillService_GetConfig_UnknownSkill(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)

	_, err := f.svc.GetConfig(t.Context(), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	var nfErr *NotFoundError
	if !errors.As(err, &nfErr) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestSkillService_GetConfig_NotReady(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()

	bt := skillSchemaSecretRequired
	sk := &store.Skill{
		Name: "notready", Origin: "platform", DiscoveryStatus: "pending",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := f.svc.GetConfig(ctx, sk.ID)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestSkillService_GetConfig_EmptyWhenUnset(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "empty_skill", skillSchemaSecretRequired)

	resp, err := f.svc.GetConfig(ctx, id)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if len(resp.Values) != 0 {
		t.Errorf("expected empty Values, got %v", resp.Values)
	}
	if resp.UpdatedBy != "" {
		t.Errorf("expected empty UpdatedBy, got %q", resp.UpdatedBy)
	}
}

func TestSkillService_GetSecretsDiff(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "diff_skill", skillSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-stored", "model_id": "gpt-5"},
		},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	cases := []struct {
		name      string
		candidate map[string]any
		want      bool
	}{
		{"same value matches", map[string]any{"render": map[string]any{"api_key": "sk-stored"}}, true},
		{"rotated value mismatches", map[string]any{"render": map[string]any{"api_key": "sk-rotated"}}, false},
		{"redacted sentinel preserves", map[string]any{"render": map[string]any{"api_key": redactedSentinel}}, true},
		{"omitted preserves", map[string]any{"render": map[string]any{"model_id": "gpt-5"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := f.svc.GetSecretsDiff(ctx, id, tc.candidate)
			if err != nil {
				t.Fatalf("GetSecretsDiff: %v", err)
			}
			if _, ok := res["render.model_id"]; ok {
				t.Errorf("non-secret model_id leaked into diff result: %+v", res)
			}
			got, ok := res["render.api_key"]
			if !ok {
				t.Fatalf("render.api_key missing from diff result: %+v", res)
			}
			if got.Matches != tc.want {
				t.Errorf("render.api_key matches = %v; want %v", got.Matches, tc.want)
			}
		})
	}
}

func TestSkillService_GetSecretsDiff_NoStoredConfig(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "diff_fresh", skillSchemaSecretRequired)

	res, err := f.svc.GetSecretsDiff(ctx, id, map[string]any{
		"render": map[string]any{"api_key": "sk-new"},
	})
	if err != nil {
		t.Fatalf("GetSecretsDiff: %v", err)
	}
	if got := res["render.api_key"]; got.Matches {
		t.Errorf("render.api_key matches = true for unset stored value; want false")
	}
}

func TestSkillService_GetSecretsDiff_NotReady(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()

	bt := skillSchemaSecretRequired
	sk := &store.Skill{
		Name: "diff_notready", Origin: "platform", DiscoveryStatus: "pending",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := f.svc.GetSecretsDiff(ctx, sk.ID, map[string]any{"render": map[string]any{"api_key": "x"}})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError for not-ready skill, got %T: %v", err, err)
	}
}

func TestSkillService_GetSecretsDiff_UnknownSkill(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)

	_, err := f.svc.GetSecretsDiff(t.Context(), "ffffffff-ffff-ffff-ffff-ffffffffffff", map[string]any{"render": map[string]any{"api_key": "x"}})
	var nfErr *NotFoundError
	if !errors.As(err, &nfErr) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestSkillService_PatchConfig_RedactsSecretsOnRead(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "redact_skill", skillSchemaSecretRequired)

	_, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-secret", "model_id": "gpt-5"},
		},
	}, "alice")
	if err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, id)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	render := resp.Values["render"]
	if got := render["api_key"]; got != redactedSentinel {
		t.Errorf("render.api_key = %v, want %q", got, redactedSentinel)
	}
	if got := render["model_id"]; got != "gpt-5" {
		t.Errorf("render.model_id = %v, want gpt-5", got)
	}
	if resp.UpdatedBy != "alice" {
		t.Errorf("UpdatedBy = %q, want alice", resp.UpdatedBy)
	}
}

func TestSkillService_PatchConfig_RedactedRoundTrip(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "rtrip_skill", skillSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-real", "model_id": "gpt-4"},
		},
	}, "alice"); err != nil {
		t.Fatalf("first PatchConfig: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": redactedSentinel, "model_id": "gpt-5"},
		},
	}, "bob"); err != nil {
		t.Fatalf("second PatchConfig: %v", err)
	}

	row, err := f.repo.GetSkillConfig(ctx, id)
	if err != nil || row == nil || row.ConfigValues == nil {
		t.Fatalf("read raw row: %v", err)
	}
	if !strings.Contains(*row.ConfigValues, `"api_key":"sk-real"`) {
		t.Errorf("stored secret = %s, want preserved original (round-trip guard failed)", *row.ConfigValues)
	}
	if !strings.Contains(*row.ConfigValues, `"model_id":"gpt-5"`) {
		t.Errorf("stored model_id = %s, want gpt-5", *row.ConfigValues)
	}
}

func TestSkillService_PatchConfig_RejectsUnknownToolAndField(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "unknown_skill", skillSchemaSecretRequired)

	_, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"unknown_field": "v"},
			"ghost":  {"x": "y"},
		},
	}, "alice")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if _, has := verr.Details["render.unknown_field"]; !has {
		t.Errorf("expected detail for render.unknown_field, got %+v", verr.Details)
	}
	if _, has := verr.Details["ghost"]; !has {
		t.Errorf("expected detail for ghost (unknown tool), got %+v", verr.Details)
	}
}

func TestSkillService_PatchConfig_RejectsWrongTypes(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "types_skill", skillSchemaWithBoolInt)

	_, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"verbose": "not_a_bool", "timeout": 3.5},
		},
	}, "alice")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if len(verr.Details) != 2 {
		t.Errorf("expected 2 details, got %d: %v", len(verr.Details), verr.Details)
	}
	if _, has := verr.Details["render.verbose"]; !has {
		t.Errorf("expected detail for render.verbose, got %v", verr.Details)
	}
	if _, has := verr.Details["render.timeout"]; !has {
		t.Errorf("expected detail for render.timeout, got %v", verr.Details)
	}
}

func TestSkillService_PatchConfig_NotReady(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()

	bt := skillSchemaSecretRequired
	sk := &store.Skill{
		Name: "patch_notready", Origin: "platform", DiscoveryStatus: "pending",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := f.repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := f.svc.PatchConfig(ctx, sk.ID, SkillConfigPatchRequest{
		Values: map[string]map[string]any{"render": {"api_key": "x"}},
	}, "u")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestSkillService_PatchConfig_OmittedKeysPreserved(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "omit_skill", skillSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-1", "model_id": "gpt-4"},
		},
	}, "alice"); err != nil {
		t.Fatalf("first PatchConfig: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"model_id": "gpt-5"},
		},
	}, "bob"); err != nil {
		t.Fatalf("partial PatchConfig: %v", err)
	}

	resp, _ := f.svc.GetConfig(ctx, id)
	render := resp.Values["render"]
	if got := render["api_key"]; got != redactedSentinel {
		t.Errorf("api_key gone or wrong = %v, want preserved+redacted", got)
	}
	if got := render["model_id"]; got != "gpt-5" {
		t.Errorf("model_id = %v, want gpt-5", got)
	}
}

func TestSkillService_PatchConfig_NullClearsKey(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "null_skill", skillSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"model_id": "gpt-4"},
		},
	}, "alice"); err != nil {
		t.Fatalf("first PatchConfig: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"model_id": nil},
		},
	}, "bob"); err != nil {
		t.Fatalf("clear PatchConfig: %v", err)
	}

	resp, _ := f.svc.GetConfig(ctx, id)
	render := resp.Values["render"]
	if _, has := render["model_id"]; has {
		t.Errorf("model_id should be absent after null clear, got %v", render)
	}
}

func TestSkillService_PatchConfig_EmptyValuesNoOp(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "empty_patch_skill", skillSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-original"},
		},
	}, "alice"); err != nil {
		t.Fatalf("seed PatchConfig: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{},
	}, "bob"); err != nil {
		t.Fatalf("empty PatchConfig: %v", err)
	}

	resp, _ := f.svc.GetConfig(ctx, id)
	if v := resp.Values["render"]["api_key"]; v != redactedSentinel {
		t.Errorf("api_key gone after empty patch — should be preserved, got %v", v)
	}
}

func TestSkillService_PatchConfig_DropsOrphanedKeys(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "orphan_skill", skillSchemaSecretRequired)

	seeded := `{"render":{"api_key":"sk-from-old","legacy_field":"orphan-value"}}`
	if err := f.repo.SetSkillConfig(ctx, id, &seeded, "system"); err != nil {
		t.Fatalf("seed pre-drift row: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"model_id": "gpt-5"},
		},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	resp, _ := f.svc.GetConfig(ctx, id)
	render := resp.Values["render"]
	if _, has := render["legacy_field"]; has {
		t.Errorf("legacy_field should be dropped as orphan, got %v", render)
	}
	if render["model_id"] != "gpt-5" {
		t.Errorf("model_id = %v, want gpt-5", render["model_id"])
	}
	if _, has := render["api_key"]; !has {
		t.Errorf("api_key should survive orphan-drop, got %v", render)
	}
}

func TestSkillService_PatchConfig_DropsOrphanedTool(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "orphan_tool_skill", skillSchemaSecretRequired)

	seeded := `{"render":{"api_key":"sk-1"},"removed_tool":{"foo":"bar"}}`
	if err := f.repo.SetSkillConfig(ctx, id, &seeded, "system"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"model_id": "gpt-5"},
		},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	resp, _ := f.svc.GetConfig(ctx, id)
	if _, has := resp.Values["removed_tool"]; has {
		t.Errorf("removed_tool entry should be dropped, got %v", resp.Values)
	}
}

func TestSkillService_PatchConfig_RecoversFromCorruptStoredJSON(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "corrupt_skill", skillSchemaSecretRequired)

	corrupt := `{not valid json`
	if err := f.repo.SetSkillConfig(ctx, id, &corrupt, "system"); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render": {"api_key": "sk-recovered"},
		},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig after corrupt: %v", err)
	}

	resp, _ := f.svc.GetConfig(ctx, id)
	if v := resp.Values["render"]["api_key"]; v != redactedSentinel {
		t.Errorf("api_key should be redacted on read, got %v", v)
	}
}

func TestSkillService_PatchConfig_MultipleBundledTools(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "multi_skill", skillSchemaMultiTool)

	_, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
		Values: map[string]map[string]any{
			"render":  {"api_key": "sk-render"},
			"cleanup": {"ttl_seconds": 7200},
		},
	}, "alice")
	if err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	resp, _ := f.svc.GetConfig(ctx, id)
	if resp.Values["render"]["api_key"] != redactedSentinel {
		t.Errorf("render.api_key = %v", resp.Values["render"]["api_key"])
	}
	got := resp.Values["cleanup"]["ttl_seconds"]
	if v, ok := got.(float64); !ok || v != 7200 {
		t.Errorf("cleanup.ttl_seconds = %v (%T), want 7200", got, got)
	}
}

func TestSkillService_BuildSkillConfigSummary(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "summary_skill", skillSchemaSecretRequired)

	t.Run("uncovered_when_no_defaults_set", func(t *testing.T) {
		sum, err := f.svc.BuildSkillConfigSummary(ctx, id, "ready")
		if err != nil {
			t.Fatalf("BuildSkillConfigSummary: %v", err)
		}
		if sum == nil {
			t.Fatal("expected non-nil summary for ready skill")
		}
		if len(sum.RequiredFields) != 1 || sum.RequiredFields[0] != "render.api_key" {
			t.Errorf("RequiredFields = %v, want [render.api_key]", sum.RequiredFields)
		}
		if len(sum.RequiredCoveredByDefaults) != 0 {
			t.Errorf("expected 0 covered, got %v", sum.RequiredCoveredByDefaults)
		}
		if !sum.RequiresAgentConfig {
			t.Error("RequiresAgentConfig should be true when api_key is uncovered")
		}
	})

	t.Run("covered_after_patch", func(t *testing.T) {
		if _, err := f.svc.PatchConfig(ctx, id, SkillConfigPatchRequest{
			Values: map[string]map[string]any{
				"render": {"api_key": "sk-set"},
			},
		}, "alice"); err != nil {
			t.Fatalf("PatchConfig: %v", err)
		}
		sum, err := f.svc.BuildSkillConfigSummary(ctx, id, "ready")
		if err != nil {
			t.Fatalf("BuildSkillConfigSummary: %v", err)
		}
		if len(sum.RequiredCoveredByDefaults) != 1 || sum.RequiredCoveredByDefaults[0] != "render.api_key" {
			t.Errorf("RequiredCoveredByDefaults = %v, want [render.api_key]", sum.RequiredCoveredByDefaults)
		}
		if sum.RequiresAgentConfig {
			t.Error("RequiresAgentConfig should be false when api_key is covered")
		}
	})

	t.Run("nil_when_not_ready", func(t *testing.T) {
		sum, err := f.svc.BuildSkillConfigSummary(ctx, id, "pending")
		if err != nil {
			t.Fatalf("BuildSkillConfigSummary: %v", err)
		}
		if sum != nil {
			t.Errorf("expected nil summary for non-ready, got %+v", sum)
		}
	})
}

func TestSkillService_GetByID_IncludesConfigSummary(t *testing.T) {
	t.Parallel()
	f := newSkillFixture(t)
	ctx := t.Context()
	id := f.seedReadySkill(ctx, t, "detail_skill", skillSchemaSecretRequired)

	resp, err := f.svc.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if resp.ConfigSummary == nil {
		t.Fatal("expected ConfigSummary on detail response, got nil")
	}
	if !resp.ConfigSummary.RequiresAgentConfig {
		t.Error("RequiresAgentConfig should be true for an unconfigured skill")
	}
}

func TestParseSkillBundledToolSchemas_StripsSkillPrefix(t *testing.T) {
	t.Parallel()
	bundled := `[
		{"tool_name":"manim__render","config_schema":[{"key":"api_key","type":"string","secret":true}]},
		{"tool_name":"manim__cleanup","config_schema":[{"key":"ttl_seconds","type":"integer","default":30}]}
	]`
	got := parseSkillBundledToolSchemas("manim", &bundled)
	if _, ok := got["render"]; !ok {
		t.Errorf("expected bareName key 'render' after strip, got keys %v", schemaKeys(got))
	}
	if _, ok := got["cleanup"]; !ok {
		t.Errorf("expected bareName key 'cleanup' after strip, got keys %v", schemaKeys(got))
	}
	if _, ok := got["manim__render"]; ok {
		t.Errorf("prefixed key 'manim__render' still present — strip failed")
	}
}

func TestParseSkillBundledToolSchemas_TolerantOfBareEntries(t *testing.T) {
	t.Parallel()
	bundled := `[{"tool_name":"render","config_schema":[{"key":"api_key","type":"string"}]}]`
	got := parseSkillBundledToolSchemas("manim", &bundled)
	if _, ok := got["render"]; !ok {
		t.Errorf("expected key 'render' to survive when no prefix present, got keys %v", schemaKeys(got))
	}
}

func schemaKeys(m map[string][]configSchemaField) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
