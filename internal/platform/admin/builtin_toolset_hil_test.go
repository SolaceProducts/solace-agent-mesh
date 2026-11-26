// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"gopkg.in/yaml.v3"
)

func TestValidateBuiltinToolsetConfigKeys_HIL(t *testing.T) {
	t.Parallel()
	gate := map[string]any{"require_approval": true, "approval_message": "Delete {{.filename}}?"}
	if err := validateBuiltinToolsetConfigKeys("builtin_artifact_tools", map[string]any{
		"list_artifacts": map[string]any{"limit": 10},
		"hil":            map[string]any{"delete_artifact": gate},
	}); err != nil {
		t.Fatalf("a hil block naming a member tool must be accepted: %v", err)
	}
	rejects := []struct {
		name     string
		hil      any
		wantPath string
	}{
		{"tool outside the toolset", map[string]any{"web_request": gate}, "builtin_artifact_tools.hil.web_request"},
		{"mistyped field", map[string]any{"delete_artifact": map[string]any{"require_approval": "yes"}}, "builtin_artifact_tools.hil.delete_artifact"},
		{"MCP tools: form", map[string]any{"delete_artifact": map[string]any{"tools": map[string]any{}}}, "builtin_artifact_tools.hil.delete_artifact.tools"},
		{"not an object", "require_approval", "builtin_artifact_tools.hil"},
	}
	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateBuiltinToolsetConfigKeys("builtin_artifact_tools", map[string]any{"hil": tc.hil})
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			if _, ok := ve.Details[tc.wantPath]; !ok {
				t.Errorf("details = %v, want an entry at %q", ve.Details, tc.wantPath)
			}
		})
	}
}

func TestValidateBuiltinToolsetConfigBindings_RefusesNullApprovalFields(t *testing.T) {
	t.Parallel()
	const wantPath = "builtin_web_request_tools.hil.web_request"
	tests := []struct {
		field   string
		wantMsg string
	}{
		{field: "require_approval_when", wantMsg: "require_approval_when must be a list"},
		{field: "require_approval", wantMsg: "require_approval must be a bool"},
		{field: "show_args", wantMsg: "show_args must be a bool"},
		{field: "approval_message", wantMsg: "approval_message must be a string"},
	}
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			hil := map[string]any{"web_request": map[string]any{tc.field: nil}}
			err := ValidateBuiltinToolsetConfigBindings([]ToolsetConfigBinding{
				{ToolsetName: "builtin_web_request_tools", ConfigValues: map[string]any{"hil": hil}},
			}, nil)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			if msgs := ve.Details[wantPath]; len(msgs) == 0 || !strings.Contains(msgs[0], tc.wantMsg) {
				t.Errorf("details = %v, want %q at %q", ve.Details, tc.wantMsg, wantPath)
			}
		})
	}
}

func TestAgentService_Create_StoresBuiltinToolsetHIL(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceWithToolPackagesForTest(t)
	hil := map[string]any{"web_request": map[string]any{"require_approval": true}}
	created, err := svc.Create(t.Context(), CreateAgentRequest{
		Name: "gated-builtin", Description: "A valid description that is long enough",
		SystemPrompt: "You are helpful.", Toolsets: []string{"builtin_web_request_tools"},
		ToolsetConfigs: []ToolsetConfigBinding{{ToolsetName: "builtin_web_request_tools", ConfigValues: map[string]any{"hil": hil}}},
	}, "u")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.GetByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.ToolsetConfigs) != 1 || !reflect.DeepEqual(got.ToolsetConfigs[0].ConfigValues["hil"], hil) {
		t.Errorf("toolsetConfigs = %+v, want hil returned verbatim", got.ToolsetConfigs)
	}
}

func seedBuiltinAgent(ctx context.Context, t *testing.T, db *store.DB, toolsets ...string) *store.Agent {
	t.Helper()
	agent := &store.Agent{Description: strPtr("test agent for fixture purposes"), Name: "Gated Builtin Agent",
		SystemPrompt: strPtr("You are a builtin-tools agent."), CreatedBy: "u", UpdatedBy: "u"}
	if err := store.NewAgentRepo(db).Create(ctx, agent, toolsets, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}

func TestResolveBuiltinToolConfigs_SplitsHILFromToolConfig(t *testing.T) {
	t.Parallel()
	db, ctx := testOpenDB(t), t.Context()
	repo := store.NewToolPackageRepo(db)
	agent := seedBuiltinAgent(ctx, t, db, "builtin_web_request_tools")
	vals := `{"web_request":{"timeout":30},"hil":{"web_request":{"require_approval":true}}}`
	if err := repo.SetBuiltinToolsetConfigsForAgent(ctx, agent.ID, []store.AgentBuiltinToolsetConfig{
		{AgentID: agent.ID, ToolsetName: "builtin_web_request_tools", ConfigValues: &vals}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	em, err := resolveBuiltinToolConfigs(ctx, agent.ID, repo, slog.Default())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	configs := em.BuiltinToolConfigs["builtin_web_request_tools"]
	if _, fake := configs["hil"]; fake {
		t.Error("hil surfaced as a tool named hil")
	}
	if !reflect.DeepEqual(configs["web_request"], map[string]any{"timeout": float64(30)}) {
		t.Errorf("tool_config = %v", configs["web_request"])
	}
	if got := em.BuiltinToolHIL["builtin_web_request_tools"]["web_request"]; !reflect.DeepEqual(got, map[string]any{"require_approval": true}) {
		t.Errorf("hil = %v, want the web_request gate", got)
	}
}

func TestResolveBuiltinToolConfigs_HILOnlyToolsetLeavesNoToolConfig(t *testing.T) {
	t.Parallel()
	db, ctx := testOpenDB(t), t.Context()
	repo := store.NewToolPackageRepo(db)
	agent := seedBuiltinAgent(ctx, t, db, "builtin_web_request_tools")
	vals := `{"hil":{"web_request":{"require_approval":true}}}`
	if err := repo.SetBuiltinToolsetConfigsForAgent(ctx, agent.ID, []store.AgentBuiltinToolsetConfig{
		{AgentID: agent.ID, ToolsetName: "builtin_web_request_tools", ConfigValues: &vals}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	em, err := resolveBuiltinToolConfigs(ctx, agent.ID, repo, slog.Default())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if em.BuiltinToolConfigs != nil {
		t.Errorf("BuiltinToolConfigs = %v, want nil when hil was the only key", em.BuiltinToolConfigs)
	}
	if len(em.BuiltinToolHIL["builtin_web_request_tools"]) != 1 {
		t.Errorf("hil = %v, want the web_request gate", em.BuiltinToolHIL)
	}
}

func snapshotToolEntries(t *testing.T, snapshot string) []map[string]any {
	t.Helper()
	var parsed struct {
		Apps []struct {
			AppConfig map[string]any `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err := yaml.Unmarshal([]byte(snapshot), &parsed); err != nil {
		t.Fatalf("snapshot YAML: %v", err)
	}
	for _, app := range parsed.Apps {
		if raw, ok := app.AppConfig["tools"].([]any); ok {
			out := make([]map[string]any, 0, len(raw))
			for _, e := range raw {
				if m, ok := e.(map[string]any); ok {
					out = append(out, m)
				}
			}
			return out
		}
	}
	t.Fatalf("no app carries a tools list:\n%s", snapshot)
	return nil
}

func TestStandardDeployer_BuiltinToolHIL_GatesThatToolOnly(t *testing.T) {
	t.Parallel()
	db, ctx := testOpenDB(t), t.Context()
	repo := store.NewToolPackageRepo(db)
	agent := seedBuiltinAgent(ctx, t, db, "builtin_web_request_tools")
	vals := `{"hil":{"web_request":{"require_approval":true,"approval_message":"Fetch {{.url}}?"}}}`
	if err := repo.SetBuiltinToolsetConfigsForAgent(ctx, agent.ID, []store.AgentBuiltinToolsetConfig{
		{AgentID: agent.ID, ToolsetName: "builtin_web_request_tools", ConfigValues: &vals}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := NewStandardAgentDeployer(NewYAMLService("test-ns", slog.Default()), nil, repo, nil, slog.Default()).
		BuildPayloads(ctx, DeployInput{Agent: agent, Action: DeployActionDeploy, Toolsets: []string{"builtin_web_request_tools"}})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}
	var entries []tool.HILEntry
	for _, e := range snapshotToolEntries(t, res.SnapshotYAML) {
		if e["tool_type"] == "builtin" {
			name, _ := e["tool_name"].(string)
			entries = append(entries, tool.HILEntry{ToolMap: e, ToolNames: []string{name}})
		}
	}
	gates, err := tool.ParseHILConfigs(slog.Default(), entries)
	if err != nil {
		t.Fatalf("the runtime rejects the emitted hil: %v", err)
	}
	if g := gates["web_request"]; !g.RequireApproval || g.ApprovalMessage != "Fetch {{.url}}?" {
		t.Errorf("web_request gate = %+v, want require_approval with the message", g)
	}
	if _, gated := gates["web_search_google"]; gated {
		t.Error("an ungated sibling must not be gated")
	}
}

func TestValidateBuiltinToolsetConfigKeys_RefusesANonObjectToolValueAndReportsEveryBadKey(t *testing.T) {
	t.Parallel()
	err := validateBuiltinToolsetConfigKeys("builtin_web_request_tools", map[string]any{
		"web_search_google": "x",
		"web_request":       []any{1},
		"not_a_tool":        map[string]any{},
		"hil":               map[string]any{"web_request": map[string]any{"require_approval": "yes"}},
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	for _, path := range []string{
		"builtin_web_request_tools.web_search_google",
		"builtin_web_request_tools.web_request",
		"builtin_web_request_tools.not_a_tool",
		"builtin_web_request_tools.hil.web_request",
	} {
		if len(ve.Details[path]) == 0 {
			t.Errorf("details = %v, want an entry at %q", ve.Details, path)
		}
	}
	if err := validateBuiltinToolsetConfigKeys("builtin_web_request_tools", map[string]any{"web_request": nil}); err != nil {
		t.Errorf("a null tool value must stay accepted: %v", err)
	}
}

func TestResolveBuiltinToolConfigs_ABadToolValueDoesNotDropTheGate(t *testing.T) {
	t.Parallel()
	db, ctx := testOpenDB(t), t.Context()
	repo := store.NewToolPackageRepo(db)
	agent := seedBuiltinAgent(ctx, t, db, "builtin_web_request_tools")
	vals := `{"hil":{"web_request":{"require_approval":true}},"web_search_google":"x","web_request":{"timeout":30}}`
	if err := repo.SetBuiltinToolsetConfigsForAgent(ctx, agent.ID, []store.AgentBuiltinToolsetConfig{
		{AgentID: agent.ID, ToolsetName: "builtin_web_request_tools", ConfigValues: &vals}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	em, err := resolveBuiltinToolConfigs(ctx, agent.ID, repo, slog.Default())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := em.BuiltinToolHIL["builtin_web_request_tools"]["web_request"]; !reflect.DeepEqual(got, map[string]any{"require_approval": true}) {
		t.Errorf("hil = %v, want the web_request gate emitted despite the bad sibling", em.BuiltinToolHIL)
	}
	configs := em.BuiltinToolConfigs["builtin_web_request_tools"]
	if _, bad := configs["web_search_google"]; bad || !reflect.DeepEqual(configs["web_request"], map[string]any{"timeout": float64(30)}) {
		t.Errorf("tool configs = %v, want only the readable tool kept", configs)
	}
}

func TestAgentService_GetByID_FailsWhenToolsetConfigsCannotBeRead(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewAgentService(store.NewAgentRepo(db), nil, nil, store.NewToolPackageRepo(db), nil, nil, slog.Default())
	created, err := svc.Create(t.Context(), CreateAgentRequest{
		Name: "unreadable-configs", Description: "A valid description that is long enough",
		SystemPrompt: "You are helpful.", Toolsets: []string{"builtin_web_request_tools"},
	}, "u")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TABLE agent_toolset_configs`); err != nil {
		t.Fatalf("inject read error: %v", err)
	}
	if got, err := svc.GetByID(t.Context(), created.ID); err == nil {
		t.Errorf("GetByID = %+v, want the read error rather than a partial response", got)
	}
}

func TestValidateBuiltinToolsetConfigBindings_ChecksBindingsAgainstTheDeclaredSlotsAndPolicies(t *testing.T) {
	t.Parallel()
	binding := []ToolsetConfigBinding{{ToolsetName: "builtin_volume_tools", VolumeBindings: map[string]string{"workspace": "data"}}}
	if err := ValidateBuiltinToolsetConfigBindings(binding, json.RawMessage(`{"volumes":[{"name":"data"}]}`)); err != nil {
		t.Errorf("a binding to a declared slot was refused: %v", err)
	}
	err := ValidateBuiltinToolsetConfigBindings(binding, json.RawMessage(`{"volumes":[{"name":"scratch"}]}`))
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(strings.Join(ve.Details["volumeBindings.workspace"], " "), `unknown volume slot "data"`) {
		t.Errorf("error = %v, want the unknown slot named", err)
	}
	if err := ValidateBuiltinToolsetConfigBindings(binding, nil); err == nil {
		t.Error("a binding with no declared slots was accepted")
	}
}
