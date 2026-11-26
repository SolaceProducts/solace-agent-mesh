// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestBuildAgentCard_DeclaredOutputSurvivesTheWire(t *testing.T) {
	t.Parallel()
	cfg := &WorkflowConfig{
		Name:         "doc-pipeline",
		Namespace:    "ns1",
		Description:  "Summarizes then translates.",
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"spanish_summary": map[string]any{"type": "string"}}},
		OutputMapping: map[string]any{
			"english_summary": "{{summarize.output.summary}}",
			"spanish_summary": "{{translate.output.translation}}",
		},
		Nodes: []*NodeConfig{{ID: "summarize", Type: "agent", AgentName: "SummarizerAgent"}},
	}
	raw, err := json.Marshal(NewWorkflowInstance(cfg).BuildAgentCard())
	if err != nil {
		t.Fatal(err)
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(raw, &card); err != nil {
		t.Fatal(err)
	}

	params := map[string]map[string]any{}
	for _, ext := range card.Capabilities.Extensions {
		params[ext.URI] = ext.Params
	}

	schema, _ := params[a2a.ExtensionURISchemas]["output_schema"].(map[string]any)
	if props, _ := schema["properties"].(map[string]any); props["spanish_summary"] == nil {
		t.Errorf("output_schema.properties not on the schemas extension: %v", params[a2a.ExtensionURISchemas])
	}
	viz, _ := params[a2a.ExtensionURIWorkflowVisualization]["workflow_config"].(map[string]any)
	mapping, _ := viz["output_mapping"].(map[string]any)
	if mapping["english_summary"] == nil || mapping["spanish_summary"] == nil {
		t.Errorf("output_mapping keys not on workflow_config of the visualization extension: %v", viz)
	}
}
