// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"gopkg.in/yaml.v3"
)

func builtinAgent(t *testing.T) *store.Agent {
	t.Helper()
	return &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "abc12345-1234-1234-1234-123456789012",
		Name:        "Builtin Agent",
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}
}

func parseToolsFromYAML(t *testing.T, yamlStr string) []map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	apps := parsed["apps"].([]any)
	cfg := apps[0].(map[string]any)["app_config"].(map[string]any)
	rawTools, _ := cfg["tools"].([]any)
	out := make([]map[string]any, len(rawTools))
	for i, t := range rawTools {
		out[i] = t.(map[string]any)
	}
	return out
}

func TestBuildAgentConfigFull_BuiltinNoPerToolConfig_EmitsLegacyGroup(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_artifact_tools"}, nil, nil, nil, ToolEmission{})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool entry, got %d: %+v", len(tools), tools)
	}
	if tools[0]["tool_type"] != "builtin-group" || tools[0]["group_name"] != "builtin_artifact_tools" {
		t.Errorf("entry = %v, want builtin-group/builtin_artifact_tools", tools[0])
	}
}

func TestBuildAgentConfigFull_BuiltinPerToolConfig_ExpandsToBuiltinEntries(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	builtinToolConfigs := map[string]map[string]map[string]any{
		"builtin_web_request_tools": {
			"web_search_google": {
				"max_iterations": 2,
				"sources":        []any{"web"},
			},
		},
	}

	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_web_request_tools"}, nil, nil, nil, ToolEmission{BuiltinToolConfigs: builtinToolConfigs})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)

	expectedTools := builtinToolsetTools["builtin_web_request_tools"]
	if len(tools) != len(expectedTools) {
		t.Fatalf("want %d tool entries, got %d: %+v", len(expectedTools), len(tools), tools)
	}

	byName := make(map[string]map[string]any, len(tools))
	for _, e := range tools {
		if e["tool_type"] != "builtin" {
			t.Errorf("tool_type = %v, want builtin (entry %v)", e["tool_type"], e)
			continue
		}
		name, _ := e["tool_name"].(string)
		byName[name] = e
	}
	for _, name := range expectedTools {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing entry for %q", name)
		}
	}

	authored := byName["web_search_google"]
	cfg, ok := authored["tool_config"].(map[string]any)
	if !ok {
		t.Fatalf("expected tool_config map on web_search_google, got %T: %v", authored["tool_config"], authored["tool_config"])
	}
	if cfg["max_iterations"] != 2 {
		t.Errorf("max_iterations = %v, want 2", cfg["max_iterations"])
	}
	if !reflect.DeepEqual(cfg["sources"], []any{"web"}) {
		t.Errorf("sources = %v, want [web]", cfg["sources"])
	}

	other := byName["web_request"]
	if _, present := other["tool_config"]; present {
		t.Errorf("non-authored tool web_request must not carry tool_config: %v", other)
	}
}

func TestBuildAgentConfigFull_BuiltinPerToolConfig_EmptyMap_KeepsLegacyGroup(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	builtinToolConfigs := map[string]map[string]map[string]any{
		"builtin_artifact_tools": {},
	}
	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_artifact_tools"}, nil, nil, nil, ToolEmission{BuiltinToolConfigs: builtinToolConfigs})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)
	if len(tools) != 1 || tools[0]["tool_type"] != "builtin-group" {
		t.Errorf("want a single builtin-group entry for empty per-tool map, got %+v", tools)
	}
}

func TestBuildAgentConfigFull_BuiltinPerToolConfig_UnknownToolset_FallsBack(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	builtinToolConfigs := map[string]map[string]map[string]any{
		"not_a_real_builtin": {
			"some_tool": {"k": "v"},
		},
	}
	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"not_a_real_builtin"}, nil, nil, nil, ToolEmission{BuiltinToolConfigs: builtinToolConfigs})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)
	if len(tools) != 1 || tools[0]["tool_type"] != "builtin-group" {
		t.Errorf("want fallback builtin-group entry for unknown builtin, got %+v", tools)
	}
}

func TestValidateBuiltinToolsetConfigKeys_Accepts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		toolset      string
		configValues map[string]any
	}{
		{"empty configValues", "builtin_artifact_tools", nil},
		{"unknown toolset is ignored at this layer", "not_a_builtin", map[string]any{"foo": "bar"}},
		{"valid tool key", "builtin_artifact_tools", map[string]any{"list_artifacts": map[string]any{"limit": 10}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBuiltinToolsetConfigKeys(tc.toolset, tc.configValues); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateBuiltinToolsetConfigKeys_Rejects(t *testing.T) {
	t.Parallel()
	err := validateBuiltinToolsetConfigKeys("builtin_artifact_tools", map[string]any{
		"foobar": map[string]any{"k": "v"},
	})
	if err == nil {
		t.Fatal("expected ValidationError for unknown tool name")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(valErr.Message, "foobar") || !strings.Contains(valErr.Message, "builtin_artifact_tools") {
		t.Errorf("error message should name the bad key and the toolset: %q", valErr.Message)
	}
	for _, tn := range builtinToolsetTools["builtin_artifact_tools"] {
		if !strings.Contains(valErr.Message, tn) {
			t.Errorf("error should list available tool %q: %q", tn, valErr.Message)
		}
	}
}
