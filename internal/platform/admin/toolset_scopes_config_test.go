// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"gopkg.in/yaml.v3"
)

func TestBuildAgentConfigWithPackages_RequiredScopesEmission(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	packages := map[string]ToolPackageInfo{
		"compliance_tools": {
			ToolFiles: []store.ToolPackageFile{
				{ToolName: "run_migrations"},
				{ToolName: "list_tables"},
			},
			ConfigValues: map[string]map[string]any{
				"run_migrations": {reservedRequiredScopesConfigKey: []string{"tool:compliance_tools__run_migrations:invoke"}},
			},
		},
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "deadbeef-1234-1234-1234-123456789012",
		Name:        "Compliance Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, []string{"compliance_tools"}, nil, nil, nil, packages)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	cfg := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
	tools := cfg["tools"].([]any)

	var gated, ungated map[string]any
	for _, ti := range tools {
		entry := ti.(map[string]any)
		switch entry["tool_name"] {
		case "compliance_tools__run_migrations":
			gated = entry
		case "compliance_tools__list_tables":
			ungated = entry
		}
	}
	if gated == nil || ungated == nil {
		t.Fatalf("missing tool entries; got tools = %+v", tools)
	}

	scopes, ok := gated["required_scopes"].([]any)
	if !ok || len(scopes) != 1 || scopes[0] != "tool:compliance_tools__run_migrations:invoke" {
		t.Fatalf("expected required_scopes on gated tool, got %T: %v", gated["required_scopes"], gated["required_scopes"])
	}
	if _, present := ungated["required_scopes"]; present {
		t.Errorf("ungated tool should have no required_scopes block; got %+v", ungated["required_scopes"])
	}
}

func TestBuildPerToolConfig_RequiredScopesUnionMerge(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{{ToolName: "run_migrations"}, {ToolName: "list_tables"}}

	tsScopes := map[string]any{
		"run_migrations": []any{"tool:compliance_tools__run_migrations:read"},
		"list_tables":    []any{"tool:compliance_tools__list_tables:invoke"},
	}
	agentScopes := map[string]any{
		"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"},
	}
	tsVals := map[string]any{reservedRequiredScopesConfigKey: tsScopes}
	agentVals := map[string]any{reservedRequiredScopesConfigKey: agentScopes}

	perTool := buildPerToolConfig(files, tsVals, agentVals)

	rm := requiredScopesFromConfig(perTool["run_migrations"])
	if len(rm) != 2 || rm[0] != "tool:compliance_tools__run_migrations:read" || rm[1] != "tool:compliance_tools__run_migrations:invoke" {
		t.Errorf("run_migrations scopes should be the union [toolset :read, agent :invoke], got %v", rm)
	}
	lt := requiredScopesFromConfig(perTool["list_tables"])
	if len(lt) != 1 || lt[0] != "tool:compliance_tools__list_tables:invoke" {
		t.Errorf("list_tables scopes should be just the toolset default, got %v", lt)
	}
}

func TestValidateToolsetRequiredScopes(t *testing.T) {
	t.Parallel()
	names := map[string]struct{}{"run_migrations": {}}

	tests := []struct {
		name      string
		val       any
		wantPaths []string
	}{
		{
			name: "valid",
			val:  map[string]any{"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"}},
		},
		{name: "nil is valid", val: nil},
		{
			name:      "not an object",
			val:       []any{"run_migrations"},
			wantPaths: []string{reservedRequiredScopesConfigKey},
		},
		{
			name:      "unknown tool name",
			val:       map[string]any{"nope": []any{"tool:x:invoke"}},
			wantPaths: []string{"required_scopes.nope"},
		},
		{
			name:      "empty scope list",
			val:       map[string]any{"run_migrations": []any{}},
			wantPaths: []string{"required_scopes.run_migrations"},
		},
		{
			name:      "empty-string scope",
			val:       map[string]any{"run_migrations": []any{""}},
			wantPaths: []string{"required_scopes.run_migrations"},
		},
		{
			name:      "malformed shape (space)",
			val:       map[string]any{"run_migrations": []any{"tool:comp tools:invoke"}},
			wantPaths: []string{"required_scopes.run_migrations"},
		},
		{
			name:      "wrong segment count",
			val:       map[string]any{"run_migrations": []any{"tool:onlytwo"}},
			wantPaths: []string{"required_scopes.run_migrations"},
		},
		{
			name:      "value not a list",
			val:       map[string]any{"run_migrations": "tool:x:invoke"},
			wantPaths: []string{"required_scopes.run_migrations"},
		},
		{
			name:      "non-string element",
			val:       map[string]any{"run_migrations": []any{42}},
			wantPaths: []string{"required_scopes.run_migrations"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			details := validateToolsetRequiredScopes(tc.val, names)
			if len(tc.wantPaths) == 0 {
				if len(details) != 0 {
					t.Fatalf("expected no errors, got %v", details)
				}
				return
			}
			for _, p := range tc.wantPaths {
				if _, ok := details[p]; !ok {
					t.Errorf("expected an error at path %q; got details = %v", p, details)
				}
			}
		})
	}
}

func TestEndToEnd_ReservedKeyRequiredScopes_Enforced(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{{ToolName: "run_migrations"}, {ToolName: "list_tables"}}

	agentVals := map[string]any{
		reservedRequiredScopesConfigKey: map[string]any{
			"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"},
		},
	}
	pkgInfo := ToolPackageInfo{ToolFiles: files, ConfigValues: buildPerToolConfig(files, nil, agentVals)}

	buildTool := func(f store.ToolPackageFile) *remote.Tool {
		t.Helper()
		cfg, err := remote.ParseToolConfig(buildPackageToolEntry("compliance_tools", f, pkgInfo))
		if err != nil {
			t.Fatalf("ParseToolConfig(%s): %v", f.ToolName, err)
		}
		return remote.NewTool(cfg, nil)
	}
	gated := buildTool(files[0])
	ungated := buildTool(files[1])

	if got := gated.Name(); got != "compliance_tools__run_migrations" {
		t.Fatalf("gated tool name = %q", got)
	}

	ta := rbac.NewToolAuth(nil, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	authorize := func(scopes []string, tl *remote.Tool) error {
		return ta.AuthorizeTool(ctx, &auth.Identity{UserID: "u", Scopes: scopes}, tl.Name(), tl.RequiredScopes())
	}

	if err := authorize(nil, gated); err == nil {
		t.Error("caller without the scope must be DENIED the gated tool")
	}
	if err := authorize([]string{"tool:compliance_tools__run_migrations:invoke"}, gated); err != nil {
		t.Errorf("caller with the exact scope must be ALLOWED: %v", err)
	}
	if err := authorize([]string{"tool:*"}, gated); err != nil {
		t.Errorf("caller with a wildcard grant must be ALLOWED: %v", err)
	}
	if err := authorize(nil, ungated); err != nil {
		t.Errorf("unscoped sibling tool must NOT be gated per-tool: %v", err)
	}
}

func TestValidateToolsetRequiredScopes_FailClosed(t *testing.T) {
	t.Parallel()

	d := validateToolsetRequiredScopes(
		map[string]any{"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"}},
		map[string]struct{}{},
	)
	if _, ok := d["required_scopes.run_migrations"]; !ok {
		t.Errorf("empty toolNames must fail closed (reject the entry); got %v", d)
	}

	d = validateToolsetRequiredScopes(map[string]any{}, map[string]struct{}{"run_migrations": {}})
	if len(d) != 0 {
		t.Errorf("empty required_scopes block is the removal spelling and must be valid; got %v", d)
	}

	for _, bad := range []string{"agent:*:invoke", "tool:compliance_tools__*:invoke", "tool:_:invoke"} {
		d = validateToolsetRequiredScopes(
			map[string]any{"run_migrations": []any{bad}},
			map[string]struct{}{"run_migrations": {}},
		)
		if _, ok := d["required_scopes.run_migrations"]; !ok {
			t.Errorf("required scope %q must be rejected; got %v", bad, d)
		}
	}
}

func TestRejectReservedConfigKey_Scopes(t *testing.T) {
	t.Parallel()
	schema := `[{"key":"required_scopes","type":"string"}]`
	if msg := rejectReservedConfigKey(&schema); !strings.Contains(msg, "RBAC required scopes") {
		t.Errorf("expected required_scopes reservation rejection, got %q", msg)
	}
}

func TestScopesForTool_OverlayCannotRemoveToolsetScope(t *testing.T) {
	t.Parallel()
	ts := map[string]any{"run_migrations": []any{"tool:compliance_tools__run_migrations:invoke"}}
	cases := map[string]map[string]any{
		"overlay omits the tool":     {},
		"overlay empty list":         {"run_migrations": []any{}},
		"overlay ill-typed (scalar)": {"run_migrations": "not-a-list"},
		"overlay nil entry":          {"run_migrations": nil},
	}
	for name, agent := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := scopesForTool(agent, ts, "run_migrations")
			if len(got) != 1 || got[0] != "tool:compliance_tools__run_migrations:invoke" {
				t.Errorf("overlay must not remove the toolset scope; got %v", got)
			}
		})
	}
}
