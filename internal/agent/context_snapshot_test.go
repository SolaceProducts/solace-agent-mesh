// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type contextShape struct {
	name       string
	tools      []string
	peers      []a2a.AgentCard
	structured bool
	golden     bool
	budget     int
}

const (
	snapshotSystemPrompt = "Answer questions about Northwind orders."
	snapshotToolInstr    = "Pass the order id exactly as the user wrote it."
)

func snapshotPeer(name, display, description string, workflow bool) a2a.AgentCard {
	exts := []a2a.AgentExtension{{URI: ExtensionURIDisplayName, Params: map[string]any{"display_name": display}}}
	if workflow {
		exts = append(exts, a2a.AgentExtension{URI: ExtensionURIAgentType, Params: map[string]any{"type": "workflow"}})
	}
	return a2a.AgentCard{
		Name:         name,
		Description:  description,
		URL:          "solace:ns/a2a/v1/agent/request/" + name,
		Capabilities: a2a.AgentCapabilities{Extensions: exts},
	}
}

var contextShapes = []contextShape{
	{
		name:  "orchestrator",
		tools: []string{"load_skill"},
		peers: []a2a.AgentCard{
			snapshotPeer("agent_fin", "NW Finance", "Invoices, payments and budgets.", false),
			snapshotPeer("agent_ops", "NW Operations", "Stock levels and shipments.", false),
			snapshotPeer("wf_stockout", "Stockout Response", "Finds substitutes for a SKU that ran out.", true),
		},
		golden: true,
		budget: 6000,
	},
	{name: "specialist", tools: []string{"sql_query_erp"}, budget: 3500},
	{name: "analyst", tools: []string{"sql_query_erp", loadArtifactToolName}, golden: true, budget: 7500},
	{name: "workflow-node", tools: []string{"sql_query_erp"}, structured: true, budget: 4400},
}

func deployedTestConfig(cfg AgentConfig) AgentConfig {
	ApplyDefaultPromptFlags(&cfg)
	return cfg
}

func renderContextSnapshot(t *testing.T, shape contextShape) string {
	t.Helper()
	cfg := AgentConfig{Name: "nw-agent", SystemPrompt: snapshotSystemPrompt, Model: "test-model", Namespace: "ns", MaxLLMCalls: 5}
	ag := New(deployedTestConfig(cfg))
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	ag.svc = svc
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, name := range shape.tools {
		if err := ag.tools.Register(&testBuiltinTool{
			name:         name,
			description:  "The " + name + " tool.",
			instructions: snapshotToolInstr,
			params:       &tool.JSONSchema{Type: "object", Properties: map[string]any{"q": map[string]any{"type": "string"}}},
		}); err != nil {
			t.Fatalf("Register(%s): %v", name, err)
		}
	}
	for _, card := range shape.peers {
		data, err := json.Marshal(card)
		if err != nil {
			t.Fatal(err)
		}
		ag.handleDiscoveryMessage(&broker.Message{Payload: data})
	}

	system := ag.buildSystemPrompt(t.Context(), nil, gatewayContext{}, promptSkillState{}, false)
	var b strings.Builder
	fmt.Fprintf(&b, "=== system prompt\n%s\n\n=== tools\n", system)
	for _, def := range ag.buildLLMTools(t.Context(), nil, nil, "") {
		params, err := json.Marshal(def.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "--- %s\n%s\n%s\n", def.Name, def.Description, params)
	}
	if shape.structured {
		fmt.Fprintf(&b, "\n=== workflow node turn\n%s\n",
			workflowNodeReminder("check_stock", "Stockout Response", "wf_check_stock_output.json", map[string]any{"type": "object"}))
	}
	return b.String()
}

func TestContextSnapshot(t *testing.T) {
	for _, shape := range contextShapes {
		t.Run(shape.name, func(t *testing.T) {
			got := renderContextSnapshot(t, shape)
			if shape.golden {
				checkGolden(t, "context/"+shape.name+".txt", got)
			}
			if len(got) > shape.budget {
				t.Errorf("%s context is %d bytes, over its %d byte budget", shape.name, len(got), shape.budget)
			}
		})
	}
}
