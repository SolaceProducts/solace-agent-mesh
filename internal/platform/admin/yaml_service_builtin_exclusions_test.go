// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestBuildAgentConfigFull_BuiltinExclusions_ExpandsAndOmits(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	emission := ToolEmission{
		BuiltinExcludedTools: map[string][]string{
			"builtin_web_request_tools": {"web_search_google"},
		},
	}

	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_web_request_tools"}, nil, nil, nil, emission)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)

	if len(tools) != 1 {
		t.Fatalf("want 1 tool entry, got %d: %+v", len(tools), tools)
	}
	if tools[0]["tool_type"] != "builtin" {
		t.Errorf("tool_type = %v, want builtin", tools[0]["tool_type"])
	}
	if tools[0]["tool_name"] != "web_request" {
		t.Errorf("tool_name = %v, want web_request", tools[0]["tool_name"])
	}
}

func TestBuildAgentConfigFull_BuiltinExclusions_AllExcluded_EmitsNothing(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	emission := ToolEmission{
		BuiltinExcludedTools: map[string][]string{
			"builtin_web_request_tools": builtinToolsetTools["builtin_web_request_tools"],
		},
	}

	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_web_request_tools"}, nil, nil, nil, emission)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	if tools := parseToolsFromYAML(t, yamlStr); len(tools) != 0 {
		t.Errorf("want no tool entries, got %+v", tools)
	}
}

func TestBuildAgentConfigFull_BuiltinExclusions_OtherToolsetsUnaffected(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	emission := ToolEmission{
		BuiltinExcludedTools: map[string][]string{
			"builtin_web_request_tools": {"web_search_google"},
		},
	}

	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t),
		[]string{"builtin_web_request_tools", "builtin_time_tools"}, nil, nil, nil, emission)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)

	var sawGroup bool
	for _, e := range tools {
		if e["tool_type"] == "builtin-group" && e["group_name"] == "builtin_time_tools" {
			sawGroup = true
		}
		if e["tool_name"] == "web_search_google" {
			t.Error("excluded web_search_google was emitted")
		}
	}
	if !sawGroup {
		t.Errorf("builtin_time_tools should keep its builtin-group entry, got %+v", tools)
	}
}

func TestBuildAgentConfigFull_BuiltinExclusions_CoexistWithToolConfig(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	emission := ToolEmission{
		BuiltinToolConfigs: map[string]map[string]map[string]any{
			"builtin_web_request_tools": {"web_request": {"timeout": 30}},
		},
		BuiltinExcludedTools: map[string][]string{
			"builtin_web_request_tools": {"web_search_google"},
		},
	}

	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_web_request_tools"}, nil, nil, nil, emission)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)

	if len(tools) != 1 {
		t.Fatalf("want 1 tool entry, got %d: %+v", len(tools), tools)
	}
	cfg, ok := tools[0]["tool_config"].(map[string]any)
	if !ok {
		t.Fatalf("web_request lost its tool_config: %+v", tools[0])
	}
	if cfg["timeout"] != 30 {
		t.Errorf("tool_config = %v, want timeout 30", cfg)
	}
}

func TestDecodeExclusions(t *testing.T) {
	t.Parallel()
	got, err := decodeExclusions(map[string]string{
		"builtin_web_request_tools": `["web_search_google"]`,
		"empty":                     `[]`,
	})
	if err != nil {
		t.Fatalf("decodeExclusions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 decoded entry, got %+v", got)
	}
	if len(got["builtin_web_request_tools"]) != 1 || got["builtin_web_request_tools"][0] != "web_search_google" {
		t.Errorf("decoded = %+v", got)
	}
	if nilOut, nilErr := decodeExclusions(nil); nilOut != nil || nilErr != nil {
		t.Errorf("nil input should decode to (nil, nil), got (%+v, %v)", nilOut, nilErr)
	}
}

func TestDecodeExclusions_MalformedIsAnError(t *testing.T) {
	t.Parallel()
	if _, err := decodeExclusions(map[string]string{"builtin_web_request_tools": `{not json`}); err == nil {
		t.Error("expected an error for a malformed exclusion row")
	}
}

func TestBuildAgentConfigFull_MalformedExclusions_FailsDeploy(t *testing.T) {
	t.Parallel()
	if _, err := decodeExclusions(map[string]string{"data_analysis": `["query_data_with_sql"`}); err == nil {
		t.Fatal("expected a decode error the deployer can surface")
	}
}

func TestEncodeExclusions_ExpectsCanonicalKeys(t *testing.T) {
	t.Parallel()
	got, err := encodeExclusions(map[string][]string{"builtin_web_request_tools": {"web_search_google"}})
	if err != nil {
		t.Fatalf("encodeExclusions: %v", err)
	}
	if _, ok := got["builtin_web_request_tools"]; !ok {
		t.Errorf("want key builtin_web_request_tools, got %+v", got)
	}
}

func TestBuildAgentConfigFull_AllExcludedAutoInjectedGroup_OptsOut(t *testing.T) {
	t.Parallel()
	for _, toolset := range []string{"builtin_artifact_tools", "data_analysis"} {
		t.Run(toolset, func(t *testing.T) {
			t.Parallel()
			svc := NewYAMLService("test-ns", slog.Default())
			emission := ToolEmission{
				BuiltinExcludedTools: map[string][]string{toolset: builtinToolsetTools[toolset]},
			}

			yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{toolset}, nil, nil, nil, emission)
			if err != nil {
				t.Fatalf("GenerateAgentYAMLFull: %v", err)
			}
			if tools := parseToolsFromYAML(t, yamlStr); len(tools) != 0 {
				t.Errorf("want no tool entries, got %+v", tools)
			}

			var parsed map[string]any
			if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
				t.Fatalf("invalid YAML: %v", err)
			}
			cfg := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
			key := autoInjectGroupConfigKeys[schema.CanonicalBuiltinGroup(toolset)]
			if got, ok := cfg[key]; !ok || got != false {
				t.Errorf("%s = %v (present=%v), want false so AWE does not re-add the emptied group", key, got, ok)
			}
		})
	}
}

func TestBuildAgentConfigFull_PartialExclusion_KeepsAutoInject(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	members := builtinToolsetTools["builtin_artifact_tools"]
	emission := ToolEmission{
		BuiltinExcludedTools: map[string][]string{"builtin_artifact_tools": members[1:]},
	}

	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_artifact_tools"}, nil, nil, nil, emission)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	cfg := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
	if _, ok := cfg["auto_inject_artifact_tools"]; ok {
		t.Error("a partial exclusion needs no opt-out; the kept tools are visible to AWE")
	}
}

func TestBuildAgentConfigFull_NoExclusions_WritesNoOptOut(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_artifact_tools", "data_analysis"}, nil, nil, nil, ToolEmission{})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	for _, key := range []string{"auto_inject_artifact_tools", "auto_inject_data_analysis_tools"} {
		if strings.Contains(yamlStr, key) {
			t.Errorf("%s must not be emitted for an agent with no exclusions", key)
		}
	}
}

func TestBuildAgentConfigFull_AuthoredAutoInjectTrue_LosesToExclusions(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	agent := builtinAgent(t)
	agent.AdditionalConfigurations = []byte(`{"autoInjectArtifactTools": true}`)

	emission := ToolEmission{
		BuiltinExcludedTools: map[string][]string{"builtin_artifact_tools": builtinToolsetTools["builtin_artifact_tools"]},
	}

	yamlStr, err := svc.GenerateAgentYAMLFull(agent, []string{"builtin_artifact_tools"}, nil, nil, nil, emission)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	cfg := appConfigFromYAML(t, yamlStr)
	if got := cfg["auto_inject_artifact_tools"]; got != false {
		t.Errorf("auto_inject_artifact_tools = %v, want false — per-tool exclusions must beat the coarse authored flag", got)
	}
	if tools := parseToolsFromYAML(t, yamlStr); len(tools) != 0 {
		t.Errorf("want no tool entries, got %+v", tools)
	}
}

func TestBuildAgentConfigFull_AuthoredAutoInjectSurvivesWithoutExclusions(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"true", "false"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := NewYAMLService("test-ns", slog.Default())
			agent := builtinAgent(t)
			agent.AdditionalConfigurations = []byte(`{"autoInjectArtifactTools": ` + name + `}`)

			yamlStr, err := svc.GenerateAgentYAMLFull(agent, []string{"builtin_artifact_tools"}, nil, nil, nil, ToolEmission{})
			if err != nil {
				t.Fatalf("GenerateAgentYAMLFull: %v", err)
			}
			cfg := appConfigFromYAML(t, yamlStr)
			if got := cfg["auto_inject_artifact_tools"]; got != (name == "true") {
				t.Errorf("auto_inject_artifact_tools = %v, want the authored %s preserved when nothing is excluded", got, name)
			}
		})
	}
}
