// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"gopkg.in/yaml.v3"
)

func TestBuildAgentConfigWithPackages_HILEmission(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	hilBlock := map[string]any{
		"require_approval_when": []any{
			map[string]any{"arg": "method", "not_in": []any{"GET", "get", "HEAD", "head"}},
		},
		"approval_message": "{{.method}} {{.path}} — approve this write?",
		"show_args":        true,
	}

	packages := map[string]ToolPackageInfo{
		"atlassian_rest_request": {
			ToolFiles: []store.ToolPackageFile{
				{ToolName: "atlassian_rest_request"},
				{ToolName: "other_tool"},
			},
			ConfigValues: map[string]map[string]any{
				"atlassian_rest_request": {reservedHILConfigKey: hilBlock},
			},
		},
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "deadbeef-1234-1234-1234-123456789012",
		Name:        "Atlassian Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, []string{"atlassian_rest_request"}, nil, nil, nil, packages)
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
		case "atlassian_rest_request__atlassian_rest_request":
			gated = entry
		case "atlassian_rest_request__other_tool":
			ungated = entry
		}
	}
	if gated == nil || ungated == nil {
		t.Fatalf("missing tool entries; got tools = %+v", tools)
	}

	hil, ok := gated["hil"].(map[string]any)
	if !ok {
		t.Fatalf("expected hil: block on gated tool, got %T: %v", gated["hil"], gated["hil"])
	}
	if hil["approval_message"] != "{{.method}} {{.path}} — approve this write?" {
		t.Errorf("approval_message = %v", hil["approval_message"])
	}
	rules, ok := hil["require_approval_when"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("require_approval_when = %v", hil["require_approval_when"])
	}

	if _, present := ungated["hil"]; present {
		t.Errorf("ungated tool should have no hil block; got %+v", ungated["hil"])
	}
}

func TestBuildPerToolConfig_HILAgentOverlayWins(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{{ToolName: "rest"}, {ToolName: "search"}}

	tsHIL := map[string]any{
		"rest":   map[string]any{"require_approval": true, "approval_message": "toolset default"},
		"search": map[string]any{"require_approval": true},
	}
	agentHIL := map[string]any{
		"rest": map[string]any{"require_approval": true, "approval_message": "agent override"},
	}
	tsVals := map[string]any{reservedHILConfigKey: tsHIL}
	agentVals := map[string]any{reservedHILConfigKey: agentHIL}

	perTool := buildPerToolConfig(files, tsVals, agentVals)

	restHIL, _ := perTool["rest"][reservedHILConfigKey].(map[string]any)
	if restHIL["approval_message"] != "agent override" {
		t.Errorf("rest hil should be the agent override, got %v", restHIL["approval_message"])
	}
	searchHIL, _ := perTool["search"][reservedHILConfigKey].(map[string]any)
	if searchHIL == nil || searchHIL["require_approval"] != true {
		t.Errorf("search hil should fall through to the toolset default, got %v", searchHIL)
	}
}

func TestValidateToolsetHIL(t *testing.T) {
	t.Parallel()
	names := map[string]struct{}{"rest": {}}

	tests := []struct {
		name      string
		val       any
		wantPaths []string
	}{
		{
			name: "valid require_approval_when",
			val: map[string]any{
				"rest": map[string]any{
					"require_approval_when": []any{
						map[string]any{"arg": "method", "not_in": []any{"GET", "HEAD"}},
					},
					"approval_message": "{{.method}}",
				},
			},
		},
		{
			name: "valid require_approval",
			val:  map[string]any{"rest": map[string]any{"require_approval": true}},
		},
		{name: "nil is valid", val: nil},
		{
			name:      "not an object",
			val:       []any{"rest"},
			wantPaths: []string{reservedHILConfigKey},
		},
		{
			name:      "unknown tool name",
			val:       map[string]any{"nope": map[string]any{"require_approval": true}},
			wantPaths: []string{"hil.nope"},
		},
		{
			name:      "entry not an object",
			val:       map[string]any{"rest": "true"},
			wantPaths: []string{"hil.rest"},
		},
		{
			name:      "nested tools sub-map rejected",
			val:       map[string]any{"rest": map[string]any{"tools": map[string]any{}}},
			wantPaths: []string{"hil.rest.tools"},
		},
		{
			name:      "unknown field",
			val:       map[string]any{"rest": map[string]any{"require_approve": true}},
			wantPaths: []string{"hil.rest"},
		},
		{
			name: "bad rule — missing operator",
			val: map[string]any{
				"rest": map[string]any{"require_approval_when": []any{map[string]any{"arg": "method"}}},
			},
			wantPaths: []string{"hil.rest"},
		},
		{
			name: "bad rule — missing arg",
			val: map[string]any{
				"rest": map[string]any{"require_approval_when": []any{map[string]any{"in": []any{"x"}}}},
			},
			wantPaths: []string{"hil.rest"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			details := validateToolsetHIL(tc.val, names)
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

func TestValidateApprovalWhenRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		v       any
		wantErr bool
	}{
		{name: "nil is refused", v: nil, wantErr: true},
		{name: "valid in", v: []any{map[string]any{"arg": "method", "in": []any{"POST"}}}},
		{name: "valid not_in", v: []any{map[string]any{"arg": "method", "not_in": []any{"GET"}}}},
		{name: "valid exists", v: []any{map[string]any{"arg": "token", "exists": true}}},
		{name: "valid gt numeric", v: []any{map[string]any{"arg": "amount", "gt": 1000}}},
		{name: "valid eq scalar", v: []any{map[string]any{"arg": "priority", "eq": "critical"}}},
		{name: "not a list", v: map[string]any{"arg": "method"}, wantErr: true},
		{name: "rule not a map", v: []any{"method"}, wantErr: true},
		{name: "missing arg", v: []any{map[string]any{"in": []any{"POST"}}}, wantErr: true},
		{name: "missing operator", v: []any{map[string]any{"arg": "method"}}, wantErr: true},
		{name: "unknown operator", v: []any{map[string]any{"arg": "method", "not_int": []any{"x"}}}, wantErr: true},
		{name: "multiple operators", v: []any{map[string]any{"arg": "method", "in": []any{"x"}, "eq": "y"}}, wantErr: true},
		{name: "in operand not a list", v: []any{map[string]any{"arg": "method", "in": "POST"}}, wantErr: true},
		{name: "in operand empty list", v: []any{map[string]any{"arg": "method", "in": []any{}}}, wantErr: true},
		{name: "in operand non-scalar element", v: []any{map[string]any{"arg": "method", "in": []any{map[string]any{}}}}, wantErr: true},
		{name: "gt operand not a number", v: []any{map[string]any{"arg": "amount", "gt": "high"}}, wantErr: true},
		{name: "eq operand non-scalar", v: []any{map[string]any{"arg": "x", "eq": []any{"a"}}}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateApprovalWhenRules(tc.v, "hil.rest")
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRejectReservedConfigKey_HIL(t *testing.T) {
	t.Parallel()
	schema := `[{"key":"hil","type":"string"}]`
	if msg := rejectReservedConfigKey(&schema); !strings.Contains(msg, "human-in-the-loop") {
		t.Errorf("expected hil reservation rejection, got %q", msg)
	}
}
