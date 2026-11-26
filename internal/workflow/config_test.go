// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseWorkflowConfig_Minimal(t *testing.T) {
	raw := map[string]any{
		"description":    "Test workflow",
		"output_mapping": map[string]any{"result": "{{node1.output}}"},
		"nodes": []any{
			map[string]any{
				"id":         "node1",
				"type":       "agent",
				"agent_name": "test-agent",
			},
		},
	}

	cfg, err := ParseWorkflowConfig("test-wf", "default", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Name != "test-wf" {
		t.Errorf("Name = %q, want %q", cfg.Name, "test-wf")
	}
	if cfg.Namespace != "default" {
		t.Errorf("Namespace = %q, want %q", cfg.Namespace, "default")
	}
	if cfg.Description != "Test workflow" {
		t.Errorf("Description = %q, want %q", cfg.Description, "Test workflow")
	}
	if cfg.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1.0.0")
	}
	if !cfg.FailFast {
		t.Error("FailFast should default to true")
	}
	if cfg.MaxCallDepth != 3 {
		t.Errorf("MaxCallDepth = %d, want 3", cfg.MaxCallDepth)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("Nodes count = %d, want 1", len(cfg.Nodes))
	}
	if cfg.Nodes[0].AgentName != "test-agent" {
		t.Errorf("AgentName = %q, want %q", cfg.Nodes[0].AgentName, "test-agent")
	}
	if cfg.CardPublishIntervalSeconds != 60 {
		t.Errorf("CardPublishIntervalSeconds = %d, want 60 (default)", cfg.CardPublishIntervalSeconds)
	}
}

func TestParseWorkflowConfig_CardPublishInterval(t *testing.T) {
	raw := map[string]any{
		"description":                   "Test",
		"output_mapping":                map[string]any{"r": "v"},
		"card_publish_interval_seconds": 45,
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "a1"},
		},
	}
	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CardPublishIntervalSeconds != 45 {
		t.Errorf("CardPublishIntervalSeconds = %d, want 45", cfg.CardPublishIntervalSeconds)
	}
}

func TestParseWorkflowConfig_CamelCaseAliases(t *testing.T) {
	raw := map[string]any{
		"description":   "Test",
		"outputMapping": map[string]any{"r": "v"},
		"inputSchema":   map[string]any{"type": "object"},
		"outputSchema":  map[string]any{"type": "object"},
		"failFast":      false,
		"maxCallDepth":  5,
		"nodes": []any{
			map[string]any{
				"id":           "n1",
				"type":         "agent",
				"agent_name":   "a1",
				"dependencies": []any{"nonexistent"},
			},
		},
	}

	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.InputSchema == nil {
		t.Error("InputSchema should not be nil")
	}
	if cfg.OutputSchema == nil {
		t.Error("OutputSchema should not be nil")
	}
	if cfg.FailFast {
		t.Error("FailFast should be false")
	}
	if cfg.MaxCallDepth != 5 {
		t.Errorf("MaxCallDepth = %d, want 5", cfg.MaxCallDepth)
	}
	if len(cfg.Nodes[0].DependsOn) != 1 || cfg.Nodes[0].DependsOn[0] != "nonexistent" {
		t.Errorf("DependsOn = %v, want [nonexistent]", cfg.Nodes[0].DependsOn)
	}
}

func TestParseWorkflowConfig_ToolsAndConnectorsFromBody(t *testing.T) {
	raw := map[string]any{
		"output_mapping": map[string]any{"result": "{{fetch.output}}"},
		"tools": []any{
			map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
		},
		"connectors": []any{
			map[string]any{"name": "traffic-db", "type": "sql"},
		},
		"nodes": []any{
			map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request"},
		},
	}

	cfg, err := ParseWorkflowConfig("test-wf", "default", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Tools) != 1 {
		t.Fatalf("Tools = %v, want the body's single entry", cfg.Tools)
	}
	entry, ok := cfg.Tools[0].(map[string]any)
	if !ok || entry["tool_name"] != "web_request" {
		t.Errorf("Tools[0] = %v, want the web_request entry", cfg.Tools[0])
	}
	if len(cfg.Connectors) != 1 {
		t.Fatalf("Connectors = %v, want the body's single entry", cfg.Connectors)
	}
}

func TestParseWorkflowConfig_NoToolsLeavesNil(t *testing.T) {
	raw := map[string]any{
		"output_mapping": map[string]any{"result": "{{n1.output}}"},
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "a1"},
		},
	}

	cfg, err := ParseWorkflowConfig("test-wf", "default", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tools != nil || cfg.Connectors != nil {
		t.Errorf("Tools = %v, Connectors = %v, want both nil", cfg.Tools, cfg.Connectors)
	}
}

func parseWorkflowWithBodyTools(t *testing.T) *WorkflowConfig {
	t.Helper()
	raw := map[string]any{
		"output_mapping": map[string]any{"result": "{{fetch.output}}"},
		"tools": []any{
			map[string]any{"tool_type": "builtin", "tool_name": "web_request", "tool_config": map[string]any{"source": "body"}},
		},
		"connectors": []any{
			map[string]any{"name": "traffic-db", "type": "sql"},
		},
		"nodes": []any{
			map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request"},
		},
	}

	cfg, err := ParseWorkflowConfig("test-wf", "default", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return cfg
}

func TestWorkflowConfig_MergeAppConfigToolsAndConnectors_KeepsBothSources(t *testing.T) {
	cfg := parseWorkflowWithBodyTools(t)

	appTools := []any{map[string]any{"tool_type": "builtin", "tool_name": "time"}}
	appConnectors := []any{map[string]any{"name": "billing-db", "type": "sql"}}
	cfg.MergeAppConfigToolsAndConnectors(appTools, appConnectors)

	if got := toolNames(t, cfg.Tools); !slices.Equal(got, []string{"time", "web_request"}) {
		t.Errorf("tool names = %v, want [time web_request] (app_config first, body preserved)", got)
	}
	if got := connectorNames(t, cfg.Connectors); !slices.Equal(got, []string{"billing-db", "traffic-db"}) {
		t.Errorf("connector names = %v, want [billing-db traffic-db] (app_config first, body preserved)", got)
	}
}

func TestWorkflowConfig_MergeAppConfigToolsAndConnectors_AppConfigWinsCollision(t *testing.T) {
	cfg := parseWorkflowWithBodyTools(t)

	appTools := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "web_request", "tool_config": map[string]any{"source": "app_config"}},
	}
	cfg.MergeAppConfigToolsAndConnectors(appTools, nil)

	if got := toolNames(t, cfg.Tools); !slices.Equal(got, []string{"web_request"}) {
		t.Fatalf("tool names = %v, want just [web_request] — the body duplicate must be dropped", got)
	}
	entry, ok := cfg.Tools[0].(map[string]any)
	if !ok {
		t.Fatalf("Tools[0] = %v, want a map entry", cfg.Tools[0])
	}
	toolCfg, _ := entry["tool_config"].(map[string]any)
	if toolCfg["source"] != "app_config" {
		t.Errorf("surviving web_request entry came from %v, want app_config", toolCfg["source"])
	}
	if !slices.Equal(cfg.supersededBodyTools, []string{"web_request"}) {
		t.Errorf("supersededBodyTools = %v, want [web_request] so Init can report the drop", cfg.supersededBodyTools)
	}
}

func TestWorkflowConfig_MergeAppConfigToolsAndConnectors_KeepsNonBuiltinDuplicates(t *testing.T) {
	cfg := &WorkflowConfig{Tools: []any{
		map[string]any{"tool_type": "mcp", "tool_name": "search", "tool_name_prefix": "body_"},
	}}
	appTools := []any{map[string]any{"tool_type": "mcp", "tool_name": "search"}}

	cfg.MergeAppConfigToolsAndConnectors(appTools, nil)

	if len(cfg.Tools) != 2 {
		t.Errorf("Tools = %v, want both mcp entries retained", cfg.Tools)
	}
	if cfg.supersededBodyTools != nil {
		t.Errorf("supersededBodyTools = %v, want nil for non-builtin entries", cfg.supersededBodyTools)
	}
}

func TestWorkflowConfig_MergeAppConfigToolsAndConnectors_NoAppConfigKeepsNil(t *testing.T) {
	cfg := &WorkflowConfig{}
	cfg.MergeAppConfigToolsAndConnectors(nil, nil)

	if cfg.Tools != nil || cfg.Connectors != nil {
		t.Errorf("Tools = %v, Connectors = %v, want both nil", cfg.Tools, cfg.Connectors)
	}
}

func TestWorkflowConfig_MergeAppConfigToolsAndConnectors_DoesNotAliasCaller(t *testing.T) {
	appTools := make([]any, 1, 4)
	appTools[0] = map[string]any{"tool_type": "builtin", "tool_name": "time"}

	cfg := &WorkflowConfig{Tools: []any{map[string]any{"tool_type": "builtin", "tool_name": "web_request"}}}
	cfg.MergeAppConfigToolsAndConnectors(appTools, nil)

	if spare := appTools[:cap(appTools)][1]; spare != nil {
		t.Errorf("caller's spare capacity holds %v; merge wrote through to its backing array", spare)
	}
}

func toolNames(t *testing.T, entries []any) []string {
	t.Helper()
	return entryNames(t, entries, "tool_name")
}

func connectorNames(t *testing.T, entries []any) []string {
	t.Helper()
	return entryNames(t, entries, "name")
}

func entryNames(t *testing.T, entries []any, key string) []string {
	t.Helper()
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			names = append(names, "")
			continue
		}
		name, _ := m[key].(string)
		names = append(names, name)
	}
	return names
}

func TestParseWorkflowConfig_MissingOutputMapping(t *testing.T) {
	raw := map[string]any{
		"description": "Test",
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "a1"},
		},
	}
	_, err := ParseWorkflowConfig("wf", "ns", raw)
	if err == nil {
		t.Fatal("expected error for missing output_mapping")
	}
}

func TestParseWorkflowConfig_NoNodes(t *testing.T) {
	raw := map[string]any{
		"description":    "Test",
		"output_mapping": map[string]any{"r": "v"},
		"nodes":          []any{},
	}
	_, err := ParseWorkflowConfig("wf", "ns", raw)
	if err == nil {
		t.Fatal("expected error for empty nodes")
	}
}

func TestParseWorkflowConfig_Timeouts(t *testing.T) {
	raw := map[string]any{
		"description":          "Test",
		"output_mapping":       map[string]any{"r": "v"},
		"workflow_timeout":     "5m",
		"default_node_timeout": "30s",
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "a1"},
		},
	}
	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkflowTimeout != 5*time.Minute {
		t.Errorf("WorkflowTimeout = %v, want 5m", cfg.WorkflowTimeout)
	}
	if cfg.DefaultNodeTimeout != 30*time.Second {
		t.Errorf("DefaultNodeTimeout = %v, want 30s", cfg.DefaultNodeTimeout)
	}
	if cfg.Nodes[0].Timeout != 30*time.Second {
		t.Errorf("Node Timeout = %v, want 30s", cfg.Nodes[0].Timeout)
	}
}

func TestParseWorkflowConfig_RetryStrategy(t *testing.T) {
	raw := map[string]any{
		"description":    "Test",
		"output_mapping": map[string]any{"r": "v"},
		"retryStrategy": map[string]any{
			"limit":       5,
			"retryPolicy": "Always",
			"backoff": map[string]any{
				"duration":    "2s",
				"factor":      3.0,
				"maxDuration": "1m",
			},
		},
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "a1"},
		},
	}
	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RetryStrategy == nil {
		t.Fatal("RetryStrategy should not be nil")
	}
	if cfg.RetryStrategy.Limit != 5 {
		t.Errorf("Limit = %d, want 5", cfg.RetryStrategy.Limit)
	}
	if cfg.RetryStrategy.RetryPolicy != "Always" {
		t.Errorf("RetryPolicy = %q, want Always", cfg.RetryStrategy.RetryPolicy)
	}
	if cfg.RetryStrategy.Backoff == nil {
		t.Fatal("Backoff should not be nil")
	}
	if cfg.RetryStrategy.Backoff.Duration != 2*time.Second {
		t.Errorf("Backoff.Duration = %v, want 2s", cfg.RetryStrategy.Backoff.Duration)
	}
	if cfg.RetryStrategy.Backoff.Factor != 3.0 {
		t.Errorf("Backoff.Factor = %f, want 3.0", cfg.RetryStrategy.Backoff.Factor)
	}
	if cfg.RetryStrategy.Backoff.MaxDuration != time.Minute {
		t.Errorf("Backoff.MaxDuration = %v, want 1m", cfg.RetryStrategy.Backoff.MaxDuration)
	}
}

func TestParseWorkflowConfig_ExitHandler_String(t *testing.T) {
	raw := map[string]any{
		"description":    "Test",
		"output_mapping": map[string]any{"r": "v"},
		"on_exit":        "cleanup",
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "a1"},
		},
	}
	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OnExit == nil || cfg.OnExit.Always != "cleanup" {
		t.Errorf("OnExit.Always = %v, want cleanup", cfg.OnExit)
	}
}

func TestParseWorkflowConfig_ExitHandler_Map(t *testing.T) {
	raw := map[string]any{
		"description":    "Test",
		"output_mapping": map[string]any{"r": "v"},
		"onExit": map[string]any{
			"onSuccess": "notify",
			"onFailure": "alert",
		},
		"nodes": []any{
			map[string]any{"id": "n1", "type": "agent", "agent_name": "a1"},
		},
	}
	cfg, err := ParseWorkflowConfig("wf", "ns", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OnExit == nil {
		t.Fatal("OnExit should not be nil")
	}
	if cfg.OnExit.OnSuccess != "notify" {
		t.Errorf("OnExit.OnSuccess = %q, want notify", cfg.OnExit.OnSuccess)
	}
	if cfg.OnExit.OnFailure != "alert" {
		t.Errorf("OnExit.OnFailure = %q, want alert", cfg.OnExit.OnFailure)
	}
}

func TestParseNodeConfig_Agent(t *testing.T) {
	raw := map[string]any{
		"id":          "agent1",
		"type":        "agent",
		"agent_name":  "my-agent",
		"when":        "{{node1.output.success}} == true",
		"instruction": "Process this data",
		"input":       map[string]any{"data": "{{workflow.input.data}}"},
		"timeout":     "1m",
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.AgentName != "my-agent" {
		t.Errorf("AgentName = %q, want my-agent", node.AgentName)
	}
	if node.When != "{{node1.output.success}} == true" {
		t.Errorf("When = %q", node.When)
	}
	if node.Instruction != "Process this data" {
		t.Errorf("Instruction = %q", node.Instruction)
	}
	if node.Input == nil {
		t.Error("Input should not be nil")
	}
	if node.Timeout != time.Minute {
		t.Errorf("Timeout = %v, want 1m", node.Timeout)
	}
}

func TestParseNodeConfig_Agent_MissingName(t *testing.T) {
	raw := map[string]any{
		"id":   "agent1",
		"type": "agent",
	}
	_, err := parseNodeConfig(raw, 0)
	if err == nil {
		t.Fatal("expected error for missing agent_name")
	}
}

func TestParseNodeConfig_Switch(t *testing.T) {
	raw := map[string]any{
		"id":   "sw1",
		"type": "switch",
		"cases": []any{
			map[string]any{"condition": "x > 0", "node": "positive"},
			map[string]any{"when": "x < 0", "then": "negative"},
		},
		"default": "zero",
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(node.Cases) != 2 {
		t.Fatalf("Cases count = %d, want 2", len(node.Cases))
	}
	if node.Cases[0].Condition != "x > 0" || node.Cases[0].Node != "positive" {
		t.Errorf("Case[0] = %+v", node.Cases[0])
	}
	if node.Cases[1].Condition != "x < 0" || node.Cases[1].Node != "negative" {
		t.Errorf("Case[1] = %+v (expected when/then aliases)", node.Cases[1])
	}
	if node.DefaultCase != "zero" {
		t.Errorf("DefaultCase = %q, want zero", node.DefaultCase)
	}
}

func TestParseNodeConfig_Switch_NoCases(t *testing.T) {
	raw := map[string]any{
		"id":    "sw1",
		"type":  "switch",
		"cases": []any{},
	}
	_, err := parseNodeConfig(raw, 0)
	if err == nil {
		t.Fatal("expected error for empty cases")
	}
}

func TestParseNodeConfig_Loop(t *testing.T) {
	raw := map[string]any{
		"id":            "loop1",
		"type":          "loop",
		"node":          "body1",
		"condition":     "{{body1.output.done}} != true",
		"maxIterations": 50,
		"delay":         "5s",
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.BodyNode != "body1" {
		t.Errorf("BodyNode = %q, want body1", node.BodyNode)
	}
	if node.Condition != "{{body1.output.done}} != true" {
		t.Errorf("Condition = %q", node.Condition)
	}
	if node.MaxIterations != 50 {
		t.Errorf("MaxIterations = %d, want 50", node.MaxIterations)
	}
	if node.Delay != 5*time.Second {
		t.Errorf("Delay = %v, want 5s", node.Delay)
	}
}

func TestParseNodeConfig_Loop_Defaults(t *testing.T) {
	raw := map[string]any{
		"id":        "loop1",
		"type":      "loop",
		"node":      "body1",
		"condition": "true",
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.MaxIterations != 100 {
		t.Errorf("MaxIterations = %d, want 100 (default)", node.MaxIterations)
	}
}

func TestParseNodeConfig_Map_Items(t *testing.T) {
	raw := map[string]any{
		"id":    "map1",
		"type":  "map",
		"items": "{{workflow.input.items}}",
		"node":  "process",
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.TemplateNode != "process" {
		t.Errorf("TemplateNode = %q, want process", node.TemplateNode)
	}
	if node.ItemsExpression != "{{workflow.input.items}}" {
		t.Errorf("ItemsExpression = %v", node.ItemsExpression)
	}
	if node.MaxItems != 100 {
		t.Errorf("MaxItems = %d, want 100 (default)", node.MaxItems)
	}
}

func TestParseNodeConfig_Map_WithParam(t *testing.T) {
	raw := map[string]any{
		"id":               "map1",
		"type":             "map",
		"withParam":        "{{prev.output.list}}",
		"node":             "process",
		"maxItems":         50,
		"concurrencyLimit": 5,
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.ItemsExpression != "{{prev.output.list}}" {
		t.Errorf("ItemsExpression = %v", node.ItemsExpression)
	}
	if node.MaxItems != 50 {
		t.Errorf("MaxItems = %d, want 50", node.MaxItems)
	}
	if node.ConcurrencyLimit != 5 {
		t.Errorf("ConcurrencyLimit = %d, want 5", node.ConcurrencyLimit)
	}
}

func TestParseNodeConfig_Map_WithItems(t *testing.T) {
	raw := map[string]any{
		"id":        "map1",
		"type":      "map",
		"withItems": []any{"a", "b", "c"},
		"node":      "process",
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, ok := node.ItemsExpression.([]any)
	if !ok {
		t.Fatalf("ItemsExpression should be []any, got %T", node.ItemsExpression)
	}
	if len(items) != 3 {
		t.Errorf("ItemsExpression length = %d, want 3", len(items))
	}
}

func TestParseNodeConfig_Map_MultipleItemsSources(t *testing.T) {
	raw := map[string]any{
		"id":        "map1",
		"type":      "map",
		"items":     "{{x}}",
		"withParam": "{{y}}",
		"node":      "process",
	}
	_, err := parseNodeConfig(raw, 0)
	if err == nil {
		t.Fatal("expected error for multiple items sources")
	}
}

func TestParseNodeConfig_Map_NoItemsSource(t *testing.T) {
	raw := map[string]any{
		"id":   "map1",
		"type": "map",
		"node": "process",
	}
	_, err := parseNodeConfig(raw, 0)
	if err == nil {
		t.Fatal("expected error for no items source")
	}
}

func TestParseNodeConfig_Workflow(t *testing.T) {
	raw := map[string]any{
		"id":            "wf1",
		"type":          "workflow",
		"workflow_name": "sub-workflow",
		"input":         map[string]any{"data": "{{workflow.input.data}}"},
		"instruction":   "Run analysis",
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.WorkflowName != "sub-workflow" {
		t.Errorf("WorkflowName = %q", node.WorkflowName)
	}
	if node.Instruction != "Run analysis" {
		t.Errorf("Instruction = %q", node.Instruction)
	}
}

func TestParseNodeConfig_UnknownType(t *testing.T) {
	raw := map[string]any{
		"id":   "n1",
		"type": "custom",
	}
	_, err := parseNodeConfig(raw, 0)
	if err == nil {
		t.Fatal("expected error for unknown node type")
	}
}

func TestParseNodeConfig_MissingID(t *testing.T) {
	raw := map[string]any{
		"type":       "agent",
		"agent_name": "a1",
	}
	_, err := parseNodeConfig(raw, 0)
	if err == nil {
		t.Fatal("expected error for missing ID")
	}
}

func TestBuildDependencyGraph_Linear(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent"},
		{ID: "b", Type: "agent", DependsOn: []string{"a"}},
		{ID: "c", Type: "agent", DependsOn: []string{"b"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Order) != 3 {
		t.Fatalf("Order length = %d, want 3", len(g.Order))
	}
	indexOf := make(map[string]int)
	for i, id := range g.Order {
		indexOf[id] = i
	}
	if indexOf["a"] >= indexOf["b"] || indexOf["b"] >= indexOf["c"] {
		t.Errorf("Order = %v, expected a < b < c", g.Order)
	}
}

func TestBuildDependencyGraph_Diamond(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "start", Type: "agent"},
		{ID: "left", Type: "agent", DependsOn: []string{"start"}},
		{ID: "right", Type: "agent", DependsOn: []string{"start"}},
		{ID: "end", Type: "agent", DependsOn: []string{"left", "right"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Order) != 4 {
		t.Fatalf("Order length = %d, want 4", len(g.Order))
	}
	indexOf := make(map[string]int)
	for i, id := range g.Order {
		indexOf[id] = i
	}
	if indexOf["start"] >= indexOf["left"] || indexOf["start"] >= indexOf["right"] {
		t.Error("start should come before left and right")
	}
	if indexOf["left"] >= indexOf["end"] || indexOf["right"] >= indexOf["end"] {
		t.Error("left and right should come before end")
	}
}

func TestBuildDependencyGraph_CycleDetection(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", DependsOn: []string{"c"}},
		{ID: "b", Type: "agent", DependsOn: []string{"a"}},
		{ID: "c", Type: "agent", DependsOn: []string{"b"}},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for cycle")
	}
}

func TestBuildDependencyGraph_DuplicateID(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent"},
		{ID: "a", Type: "agent"},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

func TestBuildDependencyGraph_MissingDependency(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", DependsOn: []string{"missing"}},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for missing dependency")
	}
}

func TestBuildDependencyGraph_SwitchValidation(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "input", Type: "agent"},
		{ID: "router", Type: "switch", DependsOn: []string{"input"},
			Cases: []SwitchCase{{Condition: "x > 0", Node: "positive"}}},
		{ID: "positive", Type: "agent", DependsOn: []string{"router"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Order) != 3 {
		t.Fatalf("Order length = %d, want 3", len(g.Order))
	}
}

func TestBuildDependencyGraph_SwitchMissingDependency(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "router", Type: "switch",
			Cases: []SwitchCase{{Condition: "true", Node: "target"}}},
		{ID: "target", Type: "agent"},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error: switch target must depend on switch node")
	}
}

func TestBuildDependencyGraph_SwitchNonexistentTarget(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "router", Type: "switch",
			Cases: []SwitchCase{{Condition: "true", Node: "ghost"}}},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for non-existent switch target")
	}
}

func TestBuildDependencyGraph_LoopValidation(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "loop1", Type: "loop", BodyNode: "body1", Condition: "true"},
		{ID: "body1", Type: "agent", AgentName: "worker"},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !g.InnerNodes["body1"] {
		t.Error("body1 should be an inner node")
	}
}

func TestBuildDependencyGraph_LoopMissingBody(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "loop1", Type: "loop", BodyNode: "ghost", Condition: "true"},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for non-existent loop body")
	}
}

func TestBuildDependencyGraph_MapValidation(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "tmpl1", ItemsExpression: "{{x}}"},
		{ID: "tmpl1", Type: "agent", AgentName: "worker"},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !g.InnerNodes["tmpl1"] {
		t.Error("tmpl1 should be an inner node")
	}
}

func TestBuildDependencyGraph_MapMissingTemplate(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "ghost", ItemsExpression: "{{x}}"},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for non-existent map template")
	}
}

func TestBuildDependencyGraph_LoopBodyToolNodeAllowed(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "loop1", Type: "loop", BodyNode: "body1", Condition: "true"},
		{ID: "body1", Type: "tool", ToolName: "do_thing", DependsOn: []string{"loop1"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error for tool node as loop body: %v", err)
	}
	if !g.InnerNodes["body1"] {
		t.Error("body1 should be an inner node")
	}
}

func TestBuildDependencyGraph_LoopBodyAgentMissingName(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "loop1", Type: "loop", BodyNode: "body1", Condition: "true"},
		{ID: "body1", Type: "agent", DependsOn: []string{"loop1"}},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for agent loop body with no agent name")
	}
	if !strings.Contains(err.Error(), "body1") || !strings.Contains(err.Error(), "agent") {
		t.Errorf("error should mention node id and agent: %v", err)
	}
}

func TestBuildDependencyGraph_LoopBodyToolMissingName(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "loop1", Type: "loop", BodyNode: "body1", Condition: "true"},
		{ID: "body1", Type: "tool", DependsOn: []string{"loop1"}},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for tool loop body with no tool name")
	}
	if !strings.Contains(err.Error(), "body1") || !strings.Contains(err.Error(), "tool") {
		t.Errorf("error should mention node id and tool: %v", err)
	}
}

func TestBuildDependencyGraph_LoopBodyWorkflowNodeAllowed(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "loop1", Type: "loop", BodyNode: "body1", Condition: "true"},
		{ID: "body1", Type: "workflow", WorkflowName: "child", DependsOn: []string{"loop1"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !g.InnerNodes["body1"] {
		t.Error("body1 should be an inner node")
	}
}

func TestBuildDependencyGraph_MapTemplateToolNodeAllowed(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "tmpl1", ItemsExpression: "{{x}}"},
		{ID: "tmpl1", Type: "tool", ToolName: "do_thing", DependsOn: []string{"map1"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error for tool node as map template: %v", err)
	}
	if !g.InnerNodes["tmpl1"] {
		t.Error("tmpl1 should be an inner node")
	}
}

func TestBuildDependencyGraph_MapTemplateToolMissingName(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "tmpl1", ItemsExpression: "{{x}}"},
		{ID: "tmpl1", Type: "tool", DependsOn: []string{"map1"}},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for tool map template with no tool name")
	}
	if !strings.Contains(err.Error(), "tmpl1") || !strings.Contains(err.Error(), "tool") {
		t.Errorf("error should mention node id and tool: %v", err)
	}
}

func TestBuildDependencyGraph_MapTemplateWorkflowMissingName(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "tmpl1", ItemsExpression: "{{x}}"},
		{ID: "tmpl1", Type: "workflow", DependsOn: []string{"map1"}},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error for workflow map template with no workflow name")
	}
	if !strings.Contains(err.Error(), "tmpl1") || !strings.Contains(err.Error(), "workflow") {
		t.Errorf("error should mention node id and workflow: %v", err)
	}
}

func TestBuildDependencyGraph_MapTemplateWorkflowNodeAllowed(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "tmpl1", ItemsExpression: "{{x}}"},
		{ID: "tmpl1", Type: "workflow", WorkflowName: "child", DependsOn: []string{"map1"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !g.InnerNodes["tmpl1"] {
		t.Error("tmpl1 should be an inner node")
	}
}

func TestBuildDependencyGraph_SwitchDefaultValidation(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "sw", Type: "switch",
			Cases:       []SwitchCase{{Condition: "true", Node: "a"}},
			DefaultCase: "b"},
		{ID: "a", Type: "agent", DependsOn: []string{"sw"}},
		{ID: "b", Type: "agent"},
	}
	_, err := BuildDependencyGraph(nodes)
	if err == nil {
		t.Fatal("expected error: switch default target must depend on switch node")
	}
}

func TestValidateExitHandler_Valid(t *testing.T) {
	g := &DependencyGraph{
		Nodes: map[string]*NodeConfig{
			"cleanup": {ID: "cleanup", Type: "agent"},
			"notify":  {ID: "notify", Type: "agent"},
		},
	}
	eh := &ExitHandler{Always: "cleanup", OnSuccess: "notify"}
	if err := ValidateExitHandler(eh, g); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateExitHandler_MissingNode(t *testing.T) {
	g := &DependencyGraph{
		Nodes: map[string]*NodeConfig{},
	}
	eh := &ExitHandler{Always: "ghost"}
	if err := ValidateExitHandler(eh, g); err == nil {
		t.Fatal("expected error for non-existent exit handler node")
	}
}

func TestValidateExitHandler_Nil(t *testing.T) {
	g := &DependencyGraph{Nodes: map[string]*NodeConfig{}}
	if err := ValidateExitHandler(nil, g); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseNodeConfig_WithRetryStrategy(t *testing.T) {
	raw := map[string]any{
		"id":         "n1",
		"type":       "agent",
		"agent_name": "a1",
		"retryStrategy": map[string]any{
			"limit":       2,
			"retryPolicy": "OnError",
		},
	}
	node, err := parseNodeConfig(raw, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.Retry == nil {
		t.Fatal("Retry should not be nil")
	}
	if node.Retry.Limit != 2 {
		t.Errorf("Retry.Limit = %d, want 2", node.Retry.Limit)
	}
	if node.Retry.RetryPolicy != "OnError" {
		t.Errorf("Retry.RetryPolicy = %q, want OnError", node.Retry.RetryPolicy)
	}
}

func TestParseNodeConfig_InheritsDefaultTimeout(t *testing.T) {
	raw := map[string]any{
		"id":         "n1",
		"type":       "agent",
		"agent_name": "a1",
	}
	node, err := parseNodeConfig(raw, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s (inherited default)", node.Timeout)
	}
}

func TestParseNodeConfig_OverridesDefaultTimeout(t *testing.T) {
	raw := map[string]any{
		"id":         "n1",
		"type":       "agent",
		"agent_name": "a1",
		"timeout":    "2m",
	}
	node, err := parseNodeConfig(raw, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.Timeout != 2*time.Minute {
		t.Errorf("Timeout = %v, want 2m (overridden)", node.Timeout)
	}
}

func TestCheckUnreachableNodes_AllReachable(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "a1"},
		{ID: "b", Type: "agent", AgentName: "a2", DependsOn: []string{"a"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckUnreachableNodes(g, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckUnreachableNodes_DetectsUnreachable(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "a1"},
		{ID: "b", Type: "agent", AgentName: "a2", DependsOn: []string{"a"}},
		{ID: "orphan", Type: "agent", AgentName: "a3", DependsOn: []string{"nonexistent_but_lets_fake_it"}},
	}
	g := &DependencyGraph{
		Nodes:      map[string]*NodeConfig{"a": nodes[0], "b": nodes[1], "orphan": nodes[2]},
		Forward:    map[string][]string{"a": {"b"}},
		Reverse:    map[string][]string{"b": {"a"}, "orphan": {"nonexistent_but_lets_fake_it"}},
		InnerNodes: map[string]bool{},
	}
	err := CheckUnreachableNodes(g, nil)
	if err == nil {
		t.Fatal("expected error for unreachable node")
	}
	if !strings.Contains(err.Error(), "orphan") {
		t.Errorf("error should mention orphan node, got: %v", err)
	}
}

func TestCheckUnreachableNodes_ExitHandlerExempt(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "step1", Type: "agent", AgentName: "a1"},
		{ID: "step2", Type: "agent", AgentName: "a2", DependsOn: []string{"step1"}},
		{ID: "cleanup", Type: "agent", AgentName: "cleaner", DependsOn: []string{"isolated_dep"}},
	}
	g := &DependencyGraph{
		Nodes:      map[string]*NodeConfig{"step1": nodes[0], "step2": nodes[1], "cleanup": nodes[2]},
		Forward:    map[string][]string{"step1": {"step2"}},
		Reverse:    map[string][]string{"step2": {"step1"}, "cleanup": {"isolated_dep"}},
		InnerNodes: map[string]bool{},
	}

	eh := &ExitHandler{Always: "cleanup"}
	if err := CheckUnreachableNodes(g, eh); err != nil {
		t.Fatalf("exit handler node should be exempt, got: %v", err)
	}

	if err := CheckUnreachableNodes(g, nil); err == nil {
		t.Fatal("expected error for unreachable cleanup node without exit handler")
	}
}

func TestCheckUnreachableNodes_InnerNodesExempt(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "map1", Type: "map", TemplateNode: "body", DependsOn: []string{}},
		{ID: "body", Type: "agent", AgentName: "a1", DependsOn: []string{"map1"}},
	}
	g, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckUnreachableNodes(g, nil); err != nil {
		t.Fatalf("inner node should be exempt, got: %v", err)
	}
}
