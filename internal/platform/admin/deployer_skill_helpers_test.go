// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestBuildPerSkillToolConfig_AgentOverridesSkillDefaults(t *testing.T) {
	t.Parallel()
	schemas := map[string][]configSchemaField{
		"render":  {{Key: "api_key", Type: "string", Secret: true}, {Key: "region", Type: "string"}},
		"cleanup": {{Key: "ttl_seconds", Type: "integer"}},
	}
	skillVals := map[string]map[string]any{
		"render":  {"api_key": "sk-shared", "region": "us-east-1"},
		"cleanup": {"ttl_seconds": 3600},
	}
	agentVals := map[string]map[string]any{
		"render": {"api_key": "sk-agent-override", "region": nil},
	}
	got := buildPerSkillToolConfig(schemas, skillVals, agentVals)

	if got["render"]["api_key"] != "sk-agent-override" {
		t.Errorf("render.api_key = %v, want sk-agent-override (agent should win)", got["render"]["api_key"])
	}
	if _, has := got["render"]["region"]; has {
		t.Errorf("render.region should be deleted by nil agent override, got %v", got["render"])
	}
	if got["cleanup"]["ttl_seconds"] != 3600 {
		t.Errorf("cleanup.ttl_seconds = %v, want 3600 (skill default preserved)", got["cleanup"]["ttl_seconds"])
	}
}

func TestBuildPerSkillToolConfig_NoAgentOverlay(t *testing.T) {
	t.Parallel()
	schemas := map[string][]configSchemaField{
		"tool_a": {{Key: "k"}},
		"tool_b": {{Key: "k"}},
	}
	skillVals := map[string]map[string]any{
		"tool_a": {"k": "from_skill"},
		"tool_b": {"k": "from_skill"},
	}
	got := buildPerSkillToolConfig(schemas, skillVals, nil)
	if got["tool_a"]["k"] != "from_skill" || got["tool_b"]["k"] != "from_skill" {
		t.Errorf("expected skill defaults on both tools, got %v", got)
	}
}

func TestBuildPerSkillToolConfig_EmptyMergedNotEmitted(t *testing.T) {
	t.Parallel()
	schemas := map[string][]configSchemaField{
		"render": {{Key: "api_key"}},
	}
	got := buildPerSkillToolConfig(schemas, nil, nil)
	if _, has := got["render"]; has {
		t.Errorf("expected no entry for tool with no config, got %v", got["render"])
	}
}

func TestBuildPerSkillToolConfig_PerToolIsolation(t *testing.T) {
	t.Parallel()
	schemas := map[string][]configSchemaField{
		"tool_a": {{Key: "api_key"}},
		"tool_b": {{Key: "api_key"}},
	}
	skillVals := map[string]map[string]any{
		"tool_a": {"api_key": "shared"},
		"tool_b": {"api_key": "shared"},
	}
	got := buildPerSkillToolConfig(schemas, skillVals, nil)
	got["tool_a"]["api_key"] = "mutated"
	if got["tool_b"]["api_key"] != "shared" {
		t.Errorf("tool_b leaked from tool_a mutation: %v", got["tool_b"])
	}
}

func TestBuildPerSkillToolConfigWithSchemaDefaults_FillsBlanks(t *testing.T) {
	t.Parallel()
	info := SkillConfigInfo{
		SkillName: "manim",
		ConfigValues: map[string]map[string]any{
			"render": {"api_key": "sk-set"},
		},
		SchemasByTool: map[string][]configSchemaField{
			"render": {
				{Key: "api_key", Secret: true},
				{Key: "endpoint", Default: "https://default-api"},
				{Key: "region", Default: "us-east-1"},
			},
		},
	}
	got := buildPerSkillToolConfigWithSchemaDefaults(info)

	if got["render"]["api_key"] != "sk-set" {
		t.Errorf("api_key should keep operator-set value, got %v", got["render"]["api_key"])
	}
	if got["render"]["endpoint"] != "https://default-api" {
		t.Errorf("endpoint should fall back to schema default, got %v", got["render"]["endpoint"])
	}
	if got["render"]["region"] != "us-east-1" {
		t.Errorf("region should fall back to schema default, got %v", got["render"]["region"])
	}
}

func TestBuildPerSkillToolConfigWithSchemaDefaults_PreservesAgentMerge(t *testing.T) {
	t.Parallel()
	info := SkillConfigInfo{
		ConfigValues: map[string]map[string]any{
			"render": {"api_key": "sk-set"},
		},
		SchemasByTool: map[string][]configSchemaField{
			"render": {
				{Key: "api_key"},
				{Key: "endpoint", Default: "https://default"},
			},
		},
	}
	_ = buildPerSkillToolConfigWithSchemaDefaults(info)

	if _, has := info.ConfigValues["render"]["endpoint"]; has {
		t.Errorf("source ConfigValues should not be mutated with schema defaults, got %v",
			info.ConfigValues["render"])
	}
}

func TestValidateSkillSecretFields_ReportsMissingSecrets(t *testing.T) {
	t.Parallel()
	configs := map[string]SkillConfigInfo{
		"manim": {
			SkillName: "manim",
			ConfigValues: map[string]map[string]any{
				"render": {"region": "us-east-1"},
			},
			SchemasByTool: map[string][]configSchemaField{
				"render": {
					{Key: "api_key", Secret: true},
					{Key: "region", Secret: false},
				},
			},
		},
	}
	err := validateSkillRequiredFields(configs)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(verr.Message, "manim/render/api_key") {
		t.Errorf("expected skill/tool/key triple in error message, got %q", verr.Message)
	}
}

func TestValidateSkillSecretFields_PassesWhenCoveredOrDefaulted(t *testing.T) {
	t.Parallel()
	configs := map[string]SkillConfigInfo{
		"manim": {
			SkillName: "manim",
			ConfigValues: map[string]map[string]any{
				"render": {"api_key": "sk-set"},
			},
			SchemasByTool: map[string][]configSchemaField{
				"render": {
					{Key: "api_key", Secret: true},
				},
				"cleanup": {
					{Key: "endpoint", Secret: true, Default: "https://default"},
				},
			},
		},
	}
	if err := validateSkillRequiredFields(configs); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestValidateSkillSecretFields_EmptyInput(t *testing.T) {
	t.Parallel()
	if err := validateSkillRequiredFields(nil); err != nil {
		t.Errorf("expected nil for empty input, got %v", err)
	}
	if err := validateSkillRequiredFields(map[string]SkillConfigInfo{}); err != nil {
		t.Errorf("expected nil for zero-len map, got %v", err)
	}
}

func TestValidateSkillSecretFields_DeterministicOrder(t *testing.T) {
	t.Parallel()
	configs := map[string]SkillConfigInfo{
		"alpha": {
			SkillName:    "alpha",
			ConfigValues: map[string]map[string]any{},
			SchemasByTool: map[string][]configSchemaField{
				"tool_z": {{Key: "k", Secret: true}},
			},
		},
		"beta": {
			SkillName:    "beta",
			ConfigValues: map[string]map[string]any{},
			SchemasByTool: map[string][]configSchemaField{
				"tool_a": {{Key: "k", Secret: true}},
			},
		},
	}
	err := validateSkillRequiredFields(configs)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if got := verr.Message; !strings.Contains(got, "alpha/tool_z/k, beta/tool_a/k") {
		t.Errorf("expected sorted ordering in message, got %q", got)
	}
}

func TestValidateSkillRequiredFields_NonSecretRequired(t *testing.T) {
	t.Parallel()
	configs := map[string]SkillConfigInfo{
		"plotter": {
			SkillName:    "plotter",
			ConfigValues: map[string]map[string]any{},
			SchemasByTool: map[string][]configSchemaField{
				"plot": {
					{Key: "region", Required: true, Secret: false},
				},
			},
		},
	}
	err := validateSkillRequiredFields(configs)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError for required non-secret field, got %T: %v", err, err)
	}
	if got := verr.Details["plotter/plot/region"]; len(got) == 0 {
		t.Errorf("expected validationDetails to include plotter/plot/region, got %v", verr.Details)
	}
}

func TestResolveAttachedSkillConfigs_AgentOverridesWin(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	ctx := t.Context()

	bt := skillSchemaMultiTool
	sk := &store.Skill{
		Name: "manim", Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	skillVals := `{"render":{"api_key":"sk-shared"}}`
	if err := repo.SetSkillConfig(ctx, sk.ID, &skillVals, "operator"); err != nil {
		t.Fatalf("set skill config: %v", err)
	}

	agentID := "a0000000-0000-0000-0000-000000000010"
	if err := store.NewAgentRepo(db).Create(ctx, &store.Agent{
		ID: agentID, Name: "agent-x", Description: strPtr("test agent fixture x"), CreatedBy: "u", UpdatedBy: "u",
	}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agentJSON := `{"render":{"api_key":"sk-agent-override"},"cleanup":{"ttl_seconds":7200}}`
	if err := repo.SetAgentSkillsWithConfigs(ctx, agentID, []store.AgentSkillRow{
		{AgentID: agentID, SkillID: sk.ID, ConfigValues: &agentJSON},
	}); err != nil {
		t.Fatalf("set agent skills: %v", err)
	}

	resolved, err := resolveAttachedSkillConfigs(ctx, agentID, []string{sk.ID}, repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveAttachedSkillConfigs: %v", err)
	}
	info, ok := resolved["manim"]
	if !ok {
		t.Fatalf("expected manim entry, got keys %v", keysOf(resolved))
	}
	if info.ConfigValues["render"]["api_key"] != "sk-agent-override" {
		t.Errorf("render.api_key = %v, want sk-agent-override", info.ConfigValues["render"]["api_key"])
	}
	if v, ok := info.ConfigValues["cleanup"]["ttl_seconds"].(float64); !ok || v != 7200 {
		t.Errorf("cleanup.ttl_seconds = %v (%T), want 7200", info.ConfigValues["cleanup"]["ttl_seconds"], info.ConfigValues["cleanup"]["ttl_seconds"])
	}
}

func TestResolveAttachedSkillConfigs_SkipsNonReadySkills(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	ctx := t.Context()

	bt := skillSchemaSecretRequired
	pending := &store.Skill{
		Name: "pending_skill", Origin: "platform", DiscoveryStatus: "pending",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := repo.Create(ctx, pending); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := resolveAttachedSkillConfigs(ctx, "agent-x", []string{pending.ID}, repo, slog.Default())
	if err != nil {
		t.Fatalf("resolveAttachedSkillConfigs: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil, got %v", out)
	}
}

func TestResolveAttachedSkillConfigs_NilRepo(t *testing.T) {
	t.Parallel()
	out, err := resolveAttachedSkillConfigs(t.Context(), "a", []string{"s"}, nil, slog.Default())
	if err != nil || out != nil {
		t.Errorf("expected nil/nil, got %v/%v", out, err)
	}
}

func TestResolveAttachedSkillConfigs_EmptySkillIDs(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	out, err := resolveAttachedSkillConfigs(t.Context(), "a", nil, repo, slog.Default())
	if err != nil || out != nil {
		t.Errorf("expected nil/nil, got %v/%v", out, err)
	}
}

func TestResolveAttachedSkillConfigs_MalformedAgentBindingJSONSkipped(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	ctx := t.Context()

	bt := skillSchemaSecretRequired
	sk := &store.Skill{
		Name: "corrupt_binding_skill", Origin: "platform", DiscoveryStatus: "ready",
		BundledTools: &bt, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed: %v", err)
	}

	skillVals := `{"render":{"api_key":"sk-shared"}}`
	if err := repo.SetSkillConfig(ctx, sk.ID, &skillVals, "operator"); err != nil {
		t.Fatalf("set skill config: %v", err)
	}

	agentID := "a0000000-0000-0000-0000-000000000020"
	if err := store.NewAgentRepo(db).Create(ctx, &store.Agent{
		ID: agentID, Name: "agent-y", Description: strPtr("test agent fixture y"), CreatedBy: "u", UpdatedBy: "u",
	}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	garbage := `{not valid json`
	if err := repo.SetAgentSkillsWithConfigs(ctx, agentID, []store.AgentSkillRow{
		{AgentID: agentID, SkillID: sk.ID, ConfigValues: &garbage},
	}); err != nil {
		t.Fatalf("set agent skills: %v", err)
	}

	logger, buf := captureLogger()
	resolved, err := resolveAttachedSkillConfigs(ctx, agentID, []string{sk.ID}, repo, logger)
	if err != nil {
		t.Fatalf("resolveAttachedSkillConfigs: %v", err)
	}
	if !strings.Contains(buf.String(), "malformed agent_skills.config_values") {
		t.Errorf("expected warn log, got %q", buf.String())
	}
	if resolved["corrupt_binding_skill"].ConfigValues["render"]["api_key"] != "sk-shared" {
		t.Errorf("expected skill-level fallback, got %v", resolved)
	}
}

func keysOf(m map[string]SkillConfigInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestResolveSkillImpliedToolsets_UnionsAndDedups(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	ctx := t.Context()

	ts1 := `["bamboohr_profile_provider","atlassian_rest_request"]`
	ts2 := `["atlassian_rest_request","datadog_api_request"]`

	skA := &store.Skill{Name: "user", Origin: "platform", DiscoveryStatus: "ready", Toolsets: &ts1, CreatedBy: "u", UpdatedBy: "u"}
	skB := &store.Skill{Name: "ops", Origin: "platform", DiscoveryStatus: "ready", Toolsets: &ts2, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, skA); err != nil {
		t.Fatalf("seed skA: %v", err)
	}
	if err := repo.Create(ctx, skB); err != nil {
		t.Fatalf("seed skB: %v", err)
	}

	got, err := resolveSkillImpliedToolsets(ctx, repo, []string{skA.ID, skB.ID}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveSkillImpliedToolsets: %v", err)
	}
	want := []string{"atlassian_rest_request", "bamboohr_profile_provider", "datadog_api_request"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("got[%d] = %q, want %q", i, got[i], n)
		}
	}
}

func TestResolveSkillImpliedToolsets_SkipsNonReady(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	ctx := t.Context()

	ts := `["bamboohr_profile_provider"]`
	pending := &store.Skill{Name: "pending", Origin: "platform", DiscoveryStatus: "pending", Toolsets: &ts, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, pending); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := resolveSkillImpliedToolsets(ctx, repo, []string{pending.ID}, slog.New(slog.DiscardHandler))
	if err != nil || got != nil {
		t.Errorf("expected nil/nil for non-ready skill, got %v/%v", got, err)
	}
}

func TestResolveSkillImpliedToolsets_MalformedJSONSkipped(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	ctx := t.Context()

	bad := `{not valid json`
	good := `["valid_toolset"]`
	skBad := &store.Skill{Name: "bad", Origin: "platform", DiscoveryStatus: "ready", Toolsets: &bad, CreatedBy: "u", UpdatedBy: "u"}
	skGood := &store.Skill{Name: "good", Origin: "platform", DiscoveryStatus: "ready", Toolsets: &good, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, skBad); err != nil {
		t.Fatalf("seed bad: %v", err)
	}
	if err := repo.Create(ctx, skGood); err != nil {
		t.Fatalf("seed good: %v", err)
	}

	logger, buf := captureLogger()
	got, err := resolveSkillImpliedToolsets(ctx, repo, []string{skBad.ID, skGood.ID}, logger)
	if err != nil {
		t.Fatalf("resolveSkillImpliedToolsets: %v", err)
	}
	if !strings.Contains(buf.String(), "malformed skills.toolsets") {
		t.Errorf("expected warn log, got %q", buf.String())
	}
	if len(got) != 1 || got[0] != "valid_toolset" {
		t.Errorf("got %v, want [valid_toolset]", got)
	}
}

func TestResolveSkillImpliedToolsets_NilRepoOrEmpty(t *testing.T) {
	t.Parallel()
	out, err := resolveSkillImpliedToolsets(t.Context(), nil, []string{"s"}, slog.Default())
	if err != nil || out != nil {
		t.Errorf("nil repo: expected nil/nil, got %v/%v", out, err)
	}

	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	out, err = resolveSkillImpliedToolsets(t.Context(), repo, nil, slog.Default())
	if err != nil || out != nil {
		t.Errorf("empty ids: expected nil/nil, got %v/%v", out, err)
	}
}

func TestUnionToolsets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		direct []string
		add    []string
		want   []string
	}{
		{"empty add returns direct unchanged", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"empty direct returns add appended", nil, []string{"a", "b"}, []string{"a", "b"}},
		{"dedups across both lists", []string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"preserves direct order, appends new from add", []string{"z", "a"}, []string{"a", "m"}, []string{"z", "a", "m"}},
		{"both empty returns nil-ish", nil, nil, nil},
		{
			"collapses a skill's alias onto the agent's platform id",
			[]string{"builtin_web_request_tools"},
			[]string{"web_tools"},
			[]string{"builtin_web_request_tools"},
		},
		{
			"canonicalizes an alias arriving only from the skill",
			[]string{"builtin_artifact_tools"},
			[]string{"research"},
			[]string{"builtin_artifact_tools", "builtin_research_tools"},
		},
		{
			"canonicalizes the agent's own alias even with nothing to add",
			[]string{"web_tools"},
			nil,
			[]string{"builtin_web_request_tools"},
		},
		{
			"leaves names with no catalog equivalent alone",
			[]string{"general_agent_tools"},
			[]string{"my_custom_package"},
			[]string{"general_agent_tools", "my_custom_package"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unionToolsets(tt.direct, tt.add)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
