// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"reflect"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

var unparsedWorkflowKeys = map[string]string{
	"toolsets": "expanded into tools: by the platform at deploy time; the engine never reads it",
	"nodes":    "covered node by node in TestWorkflowSchemaNodeKeysAreParsed",
}

var nodeBaselines = map[string]map[string]any{
	"agent":    {"agent_name": "baseline"},
	"workflow": {"workflow_name": "baseline"},
	"tool":     {"tool_name": "baseline"},
	"switch":   {"cases": []any{map[string]any{"condition": "baseline", "node": "baseline"}}},
	"loop":     {"node": "baseline", "condition": "baseline"},
	"map":      {"node": "baseline", "items": "baseline"},
	"decision": {"questions": map[string]any{"q": map[string]any{"type": "boolean", "instructions": "baseline"}}},
	"timer":    {"duration": "1s"},
	"wait":     {"signal": "s"},
}

var nodeKeysProbedElsewhere = map[string][]string{
	"decision": {"questions", "route_on", "routes", "min_confidence", "escalate", "escalation_mode", "default", "on_provider_error", "image_resolution", "video_resolution"},
}

func TestWorkflowSchemaTopLevelKeysAreParsed(t *testing.T) {
	wf, err := ExtractWorkflowSchema("../../workflow/config.go")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	baseline := minimalWorkflow()
	want := parseWorkflow(t, baseline)
	for _, f := range wf.Fields {
		if _, skip := unparsedWorkflowKeys[f.JSONName]; skip {
			continue
		}
		raw := minimalWorkflow()
		raw[f.JSONName] = probeValue(t, f)
		if reflect.DeepEqual(parseWorkflow(t, raw), want) {
			t.Errorf("documented workflow key %q is not read by the parser", f.JSONName)
		}
	}
}

func TestWorkflowSchemaNodeKeysAreParsed(t *testing.T) {
	wf, err := ExtractWorkflowSchema("../../workflow/config.go")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for nodeType, nt := range wf.NodeTypes {
		if _, ok := nodeBaselines[nodeType]; !ok {
			t.Errorf("node type %q has no baseline; add one to nodeBaselines", nodeType)
			continue
		}
		want := parseNode(t, baselineNode(nodeType))
		for _, f := range nt.Fields {
			if f.JSONName == "type" || slices.Contains(nodeKeysProbedElsewhere[nodeType], f.JSONName) {
				continue
			}
			node := baselineNode(nodeType)
			node[f.JSONName] = probeValue(t, f)
			if reflect.DeepEqual(parseNode(t, node), want) {
				t.Errorf("documented %s node key %q is not read by the parser", nodeType, f.JSONName)
			}
		}
	}
}

var commonNodeKeys = []string{"when", "timeout", "retry_strategy", "input"}

var refusedCommonKeys = map[string][]string{
	"decision": {"retry_strategy"},
	"timer":    {"timeout", "retry_strategy"},
	"wait":     {"retry_strategy"},
}

func TestWorkflowSchemaDocumentsCommonNodeKeysForEveryType(t *testing.T) {
	wf, err := ExtractWorkflowSchema("../../workflow/config.go")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for nodeType, nt := range wf.NodeTypes {
		documented := make(map[string]bool, len(nt.Fields))
		for _, f := range nt.Fields {
			documented[f.JSONName] = true
		}
		refused := refusedCommonKeys[nodeType]
		for _, key := range commonNodeKeys {
			switch {
			case slices.Contains(refused, key) && documented[key]:
				t.Errorf("%s node table documents %q, which its parser rejects", nodeType, key)
			case !slices.Contains(refused, key) && !documented[key]:
				t.Errorf("%s node table omits %q, which the parser reads for every node type", nodeType, key)
			}
		}
	}
}

func minimalWorkflow() map[string]any {
	return map[string]any{
		"output_mapping": map[string]any{"result": "baseline"},
		"nodes":          []any{map[string]any{"id": "baseline", "type": "agent", "agent_name": "baseline"}},
	}
}

func baselineNode(nodeType string) map[string]any {
	node := map[string]any{"id": "baseline", "type": nodeType}
	for k, v := range nodeBaselines[nodeType] {
		node[k] = v
	}
	return node
}

func parseWorkflow(t *testing.T, raw map[string]any) *workflow.WorkflowConfig {
	t.Helper()
	cfg, err := workflow.ParseWorkflowConfig("probe", "probe", raw)
	if err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	return cfg
}

func parseNode(t *testing.T, node map[string]any) *workflow.NodeConfig {
	t.Helper()
	raw := minimalWorkflow()
	raw["nodes"] = []any{node}
	return parseWorkflow(t, raw).Nodes[0]
}

func probeValue(t *testing.T, f FieldSchema) any {
	t.Helper()
	switch f.GoType {
	case "string", "any", "*ExitHandler":
		return "probe"
	case "time.Duration":
		return "7s"
	case "int":
		return 7
	case "bool":
		return false
	case "[]string":
		return []any{"probe"}
	case "map[string]any":
		return map[string]any{"probe": "probe"}
	case "[]any", "[]map[string]any":
		return []any{map[string]any{"probe": "probe"}}
	case "*RetryStrategy":
		return map[string]any{"limit": 7}
	case "*DecisionProviderConfig":
		return map[string]any{"model_alias": "probe"}
	case "[]SwitchCase":
		return []any{map[string]any{"condition": "probe", "node": "probe"}}
	default:
		t.Fatalf("no probe value for %q (Go type %q); add one to probeValue", f.JSONName, f.GoType)
		return nil
	}
}
