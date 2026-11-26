// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeepMergeYAML_RootMerge(t *testing.T) {
	base := map[string]any{
		"name": "agent1",
		"model": map[string]any{
			"model":    "gpt-4o",
			"api_base": "https://api.openai.com",
		},
	}
	overlay := map[string]any{
		"description": "A new agent",
		"model": map[string]any{
			"model": "gpt-4o-mini",
		},
	}

	deepMergeYAML(base, overlay)

	if base["name"] != "agent1" {
		t.Errorf("expected name=agent1, got %v", base["name"])
	}
	if base["description"] != "A new agent" {
		t.Errorf("expected description to be added, got %v", base["description"])
	}
	model := base["model"].(map[string]any)
	if model["model"] != "gpt-4o-mini" {
		t.Errorf("expected model to be overwritten, got %v", model["model"])
	}
	if model["api_base"] != "https://api.openai.com" {
		t.Errorf("expected api_base to be preserved, got %v", model["api_base"])
	}
}

func TestDeepMergeYAML_ArrayReplacement(t *testing.T) {
	base := map[string]any{
		"tools": []any{"tool1", "tool2"},
	}
	overlay := map[string]any{
		"tools": []any{"tool3"},
	}

	deepMergeYAML(base, overlay)

	tools := base["tools"].([]any)
	if len(tools) != 1 || tools[0] != "tool3" {
		t.Errorf("expected tools to be replaced with [tool3], got %v", tools)
	}
}

func TestDeepMergeYAML_ScalarOverwrite(t *testing.T) {
	base := map[string]any{
		"enabled": true,
		"count":   42,
	}
	overlay := map[string]any{
		"enabled": false,
		"count":   99,
	}

	deepMergeYAML(base, overlay)

	if base["enabled"] != false {
		t.Errorf("expected enabled=false, got %v", base["enabled"])
	}
	if base["count"] != 99 {
		t.Errorf("expected count=99, got %v", base["count"])
	}
}

func TestDeepMergeYAML_NestedMaps(t *testing.T) {
	base := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"a": 1,
				"b": 2,
			},
		},
	}
	overlay := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"b": 3,
				"c": 4,
			},
		},
	}

	deepMergeYAML(base, overlay)

	l2 := base["level1"].(map[string]any)["level2"].(map[string]any)
	if l2["a"] != 1 {
		t.Errorf("expected a=1, got %v", l2["a"])
	}
	if l2["b"] != 3 {
		t.Errorf("expected b=3, got %v", l2["b"])
	}
	if l2["c"] != 4 {
		t.Errorf("expected c=4, got %v", l2["c"])
	}
}

func TestMergeAtPath_MapKey(t *testing.T) {
	base := map[string]any{
		"app_config": map[string]any{
			"name":        "agent1",
			"instruction": "old instruction",
		},
	}

	err := mergeAtPath(base, "app_config.instruction", "new instruction")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := base["app_config"].(map[string]any)
	if ac["instruction"] != "new instruction" {
		t.Errorf("expected instruction to be updated, got %v", ac["instruction"])
	}
	if ac["name"] != "agent1" {
		t.Errorf("expected name to be preserved, got %v", ac["name"])
	}
}

func TestMergeAtPath_ArrayIndex(t *testing.T) {
	base := map[string]any{
		"apps": []any{
			map[string]any{
				"name": "app0",
				"app_config": map[string]any{
					"agent_name": "agent0",
				},
			},
		},
	}

	err := mergeAtPath(base, "apps.0.app_config", map[string]any{
		"description": "added description",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := base["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
	if ac["agent_name"] != "agent0" {
		t.Errorf("expected agent_name preserved, got %v", ac["agent_name"])
	}
	if ac["description"] != "added description" {
		t.Errorf("expected description added, got %v", ac["description"])
	}
}

func TestMergeAtPath_InvalidPath(t *testing.T) {
	base := map[string]any{
		"name": "agent1",
	}

	err := mergeAtPath(base, "nonexistent.child", "value")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestMergeAtPath_ArrayIndexOutOfBounds(t *testing.T) {
	base := map[string]any{
		"apps": []any{
			map[string]any{"name": "app0"},
		},
	}

	err := mergeAtPath(base, "apps.5", map[string]any{"name": "app5"})
	if err == nil {
		t.Fatal("expected error for out of bounds index")
	}
}

func TestMergeAtPath_ReplaceArrayAtPath(t *testing.T) {
	base := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"tools": []any{
						map[string]any{"tool_type": "builtin-group", "group_name": "old_tools"},
					},
				},
			},
		},
	}

	newTools := []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management"},
		map[string]any{"tool_type": "builtin-group", "group_name": "web_tools"},
	}

	err := mergeAtPath(base, "apps.0.app_config.tools", newTools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tools := base["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
}

func TestDeepMergeYAML_TypeMismatchOverwrites(t *testing.T) {
	base := map[string]any{
		"config": map[string]any{"a": 1},
	}
	overlay := map[string]any{
		"config": "simple_string",
	}

	deepMergeYAML(base, overlay)

	if base["config"] != "simple_string" {
		t.Errorf("expected type mismatch to replace, got %v", base["config"])
	}
}

func TestMergeAtPath_DeepMergeAtTarget(t *testing.T) {
	base := map[string]any{
		"app_config": map[string]any{
			"model": map[string]any{
				"model":    "gpt-4o",
				"api_base": "https://api.openai.com",
				"api_key":  "sk-xxx",
			},
		},
	}

	err := mergeAtPath(base, "app_config.model", map[string]any{
		"model": "gpt-4o-mini",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	model := base["app_config"].(map[string]any)["model"].(map[string]any)
	if model["model"] != "gpt-4o-mini" {
		t.Errorf("expected model updated, got %v", model["model"])
	}
	if model["api_base"] != "https://api.openai.com" {
		t.Errorf("expected api_base preserved, got %v", model["api_base"])
	}
	if model["api_key"] != "sk-xxx" {
		t.Errorf("expected api_key preserved, got %v", model["api_key"])
	}
}

func TestRoundTripYAML(t *testing.T) {
	baseYAML := `apps:
  - name: agent1
    app_config:
      agent_name: agent1
      instruction: |
        You are a helpful agent.
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
`
	fragmentYAML := `apps:
  - name: agent1
    app_config:
      description: Added via merge
`

	var base, fragment map[string]any
	if err := yaml.Unmarshal([]byte(baseYAML), &base); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(fragmentYAML), &fragment); err != nil {
		t.Fatal(err)
	}

	deepMergeYAML(base, fragment)

	result, err := yaml.Marshal(base)
	if err != nil {
		t.Fatalf("failed to marshal merged result: %v", err)
	}

	var roundTrip map[string]any
	if err := yaml.Unmarshal(result, &roundTrip); err != nil {
		t.Fatalf("merged YAML failed to parse: %v", err)
	}

	apps := roundTrip["apps"].([]any)
	if len(apps) != 1 {
		t.Errorf("expected 1 app, got %d", len(apps))
	}
}

func parseYAMLNode(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("failed to parse YAML: %v", err)
	}
	return doc.Content[0]
}

func marshalNode(t *testing.T, node *yaml.Node) string {
	t.Helper()
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{node}}
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return string(out)
}

func TestNodeMergeMapping_PreservesKeyOrder(t *testing.T) {
	base := parseYAMLNode(t, `
name: agent1
instruction: old
model:
  model: gpt-4o
  api_base: https://api.openai.com
tools:
  - tool1
`)
	overlay := parseYAMLNode(t, `
description: added
model:
  model: gpt-4o-mini
`)

	nodeMergeMapping(base, overlay)

	result := marshalNode(t, base)

	nameIdx := strings.Index(result, "name:")
	instrIdx := strings.Index(result, "instruction:")
	modelIdx := strings.Index(result, "model:")
	toolsIdx := strings.Index(result, "tools:")
	descIdx := strings.Index(result, "description:")

	if nameIdx > instrIdx || instrIdx > modelIdx || modelIdx > toolsIdx || toolsIdx > descIdx {
		t.Errorf("key order not preserved. Output:\n%s", result)
	}

	if !strings.Contains(result, "api_base:") {
		t.Errorf("expected api_base preserved in merged model, got:\n%s", result)
	}
	if !strings.Contains(result, "gpt-4o-mini") {
		t.Errorf("expected model updated to gpt-4o-mini, got:\n%s", result)
	}
}

func TestNodeMergeAtPath_ArrayConcatenationAppendsInsteadOfReplacing(t *testing.T) {
	base := parseYAMLNode(t, `
apps:
  - name: test-workflow
    app_config:
      agent_name: TestWorkflow
      nodes:
        - id: start
          type: agent
          agent_name: StartAgent
        - id: process
          type: agent
          agent_name: ProcessAgent
          depends_on: [start]
`)
	fragment := parseYAMLNode(t, `
- id: new_node
  type: agent
  agent_name: NewAgent
  depends_on: [start]
`)

	err := nodeMergeAtPath(base, "apps.0.app_config.nodes", fragment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := marshalNode(t, base)

	if !strings.Contains(result, "id: start") {
		t.Errorf("expected 'start' node preserved, got:\n%s", result)
	}
	if !strings.Contains(result, "id: process") {
		t.Errorf("expected 'process' node preserved, got:\n%s", result)
	}
	if !strings.Contains(result, "id: new_node") {
		t.Errorf("expected 'new_node' appended, got:\n%s", result)
	}

	startIdx := strings.Index(result, "id: start")
	processIdx := strings.Index(result, "id: process")
	newIdx := strings.Index(result, "id: new_node")
	if startIdx > processIdx || processIdx > newIdx {
		t.Errorf("expected nodes in order start, process, new_node. Got:\n%s", result)
	}
}

func TestNodeMergeAtPath_MapDeepMerge(t *testing.T) {
	base := parseYAMLNode(t, `
app_config:
  model:
    model: gpt-4o
    api_base: https://api.openai.com
    api_key: sk-xxx
`)
	fragment := parseYAMLNode(t, `
model: gpt-4o-mini
`)

	err := nodeMergeAtPath(base, "app_config.model", fragment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := marshalNode(t, base)

	if !strings.Contains(result, "gpt-4o-mini") {
		t.Errorf("expected model updated, got:\n%s", result)
	}
	if !strings.Contains(result, "api_base:") {
		t.Errorf("expected api_base preserved, got:\n%s", result)
	}
	if !strings.Contains(result, "api_key:") {
		t.Errorf("expected api_key preserved, got:\n%s", result)
	}
}

func TestNodeMergeAtPath_ScalarReplace(t *testing.T) {
	base := parseYAMLNode(t, `
app_config:
  name: old_name
  instruction: old instruction
`)
	fragment := parseYAMLNode(t, `new instruction`)

	err := nodeMergeAtPath(base, "app_config.instruction", fragment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := marshalNode(t, base)
	if !strings.Contains(result, "new instruction") {
		t.Errorf("expected instruction replaced, got:\n%s", result)
	}
	if !strings.Contains(result, "name: old_name") {
		t.Errorf("expected name preserved, got:\n%s", result)
	}
}

func TestNodeMergeAtPath_InvalidPath(t *testing.T) {
	base := parseYAMLNode(t, `name: agent1`)

	fragment := parseYAMLNode(t, `value`)
	err := nodeMergeAtPath(base, "nonexistent.child", fragment)
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestNodeMergeAtPath_ArrayIndexOutOfBounds(t *testing.T) {
	base := parseYAMLNode(t, `
apps:
  - name: app0
`)
	fragment := parseYAMLNode(t, `name: app5`)

	err := nodeMergeAtPath(base, "apps.5", fragment)
	if err == nil {
		t.Fatal("expected error for out of bounds index")
	}
}

func TestNodeMergeMapping_RootLevelArrayReplacement(t *testing.T) {
	base := parseYAMLNode(t, `
tools:
  - tool1
  - tool2
`)
	overlay := parseYAMLNode(t, `
tools:
  - tool3
`)

	nodeMergeMapping(base, overlay)

	result := marshalNode(t, base)
	if strings.Contains(result, "tool1") || strings.Contains(result, "tool2") {
		t.Errorf("expected root-level array replaced, but old items survived:\n%s", result)
	}
	if !strings.Contains(result, "tool3") {
		t.Errorf("expected tool3 in result, got:\n%s", result)
	}
}

func TestNodeMergeAtPath_AddNewKey(t *testing.T) {
	base := parseYAMLNode(t, `
app_config:
  name: agent1
`)
	fragment := parseYAMLNode(t, `new description`)

	err := nodeMergeAtPath(base, "app_config.description", fragment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := marshalNode(t, base)
	if !strings.Contains(result, "description: new description") {
		t.Errorf("expected new key added, got:\n%s", result)
	}
	if !strings.Contains(result, "name: agent1") {
		t.Errorf("expected existing key preserved, got:\n%s", result)
	}
}

func TestNodeMergePreservesFullDocumentStructure(t *testing.T) {
	baseYAML := `apps:
  - name: test-workflow
    app_config:
      agent_name: TestWorkflow
      agent_type: workflow
      namespace: solace-agent-mesh
      description: Comprehensive test workflow
      nodes:
        - id: start
          type: agent
          agent_name: Agent1
          input:
            data: "{{trigger.output}}"
        - id: mode_switch
          type: switch
          depends_on: [start]
          condition: "{{start.output.mode}}"
          cases:
            - value: fast
              next: fast_analysis
            - value: thorough
              next: thorough_analysis
        - id: fast_analysis
          type: agent
          agent_name: Agent2
          depends_on: [mode_switch]
        - id: thorough_analysis
          type: agent
          agent_name: Agent3
          depends_on: [mode_switch]
        - id: aggregate
          type: map
          depends_on: [fast_analysis, thorough_analysis]
          items: "{{start.output.items}}"
      agent_card:
        description: A test workflow
`

	fragmentYAML := `- id: prepare_items
  type: agent
  agent_name: Agent4
  depends_on: [mode_switch]
  input:
    data: "{{start.output.items}}"
`

	var baseDoc yaml.Node
	if err := yaml.Unmarshal([]byte(baseYAML), &baseDoc); err != nil {
		t.Fatal(err)
	}
	baseRoot := baseDoc.Content[0]

	var fragDoc yaml.Node
	if err := yaml.Unmarshal([]byte(fragmentYAML), &fragDoc); err != nil {
		t.Fatal(err)
	}
	fragRoot := fragDoc.Content[0]

	if err := nodeMergeAtPath(baseRoot, "apps.0.app_config.nodes", fragRoot); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, err := yaml.Marshal(&baseDoc)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	result := string(out)

	for _, id := range []string{"start", "mode_switch", "fast_analysis", "thorough_analysis", "aggregate"} {
		if !strings.Contains(result, "id: "+id) {
			t.Errorf("original node '%s' missing from result:\n%s", id, result)
		}
	}

	if !strings.Contains(result, "id: prepare_items") {
		t.Errorf("new node 'prepare_items' missing from result:\n%s", result)
	}

	nameIdx := strings.Index(result, "name: test-workflow")
	appConfigIdx := strings.Index(result, "app_config:")
	if nameIdx < 0 || appConfigIdx < 0 || nameIdx > appConfigIdx {
		t.Errorf("expected 'name' before 'app_config' (key order). Got:\n%s", result)
	}

	agentNameIdx := strings.Index(result, "agent_name: TestWorkflow")
	agentTypeIdx := strings.Index(result, "agent_type:")
	if agentNameIdx < 0 || agentTypeIdx < 0 || agentNameIdx > agentTypeIdx {
		t.Errorf("expected 'agent_name' before 'agent_type' (key order). Got:\n%s", result)
	}

	if !strings.Contains(result, "agent_card:") {
		t.Errorf("agent_card section missing from result:\n%s", result)
	}

	nodeCount := strings.Count(result, "- id: ")
	if nodeCount != 6 {
		t.Errorf("expected 6 nodes, got %d. Result:\n%s", nodeCount, result)
	}
}
