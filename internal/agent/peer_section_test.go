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
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestPeerToolsCarryTheCardAndThePromptDoesNotRepeatIt(t *testing.T) {
	agent := newEnterpriseAgentHarness(t)
	skills := make([]a2a.AgentSkill, 10)
	for i := range skills {
		skills[i] = a2a.AgentSkill{ID: fmt.Sprintf("s%d", i), Name: fmt.Sprintf("Skill %d", i)}
	}
	cards := []a2a.AgentCard{
		{
			Name:        "sales-agent",
			Description: "Answers sales questions.",
			URL:         "solace:ns/a2a/v1/agent/request/sales-agent",
			Skills:      skills,
		},
		{
			Name:        "data-pipeline",
			Description: "Runs the nightly pipeline.",
			URL:         "solace:ns/a2a/v1/agent/request/data-pipeline",
			Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
				URI:    ExtensionURIAgentType,
				Params: map[string]any{"type": "workflow"},
			}}},
		},
	}
	for _, card := range cards {
		data, _ := json.Marshal(card)
		agent.handleDiscoveryMessage(&broker.Message{Payload: data})
	}
	identity := &auth.Identity{
		UserID: "sam_dev_user",
		Scopes: []string{rbac.AgentInvokeScope("sales-agent"), rbac.WorkflowInvokeScope("data-pipeline")},
	}

	prompt := agent.buildSystemPrompt(t.Context(), identity, gatewayContext{}, promptSkillState{}, false)
	descriptions := map[string]string{}
	for _, d := range agent.buildLLMTools(t.Context(), identity, nil, "") {
		descriptions[d.Name] = d.Description
	}

	for _, card := range cards {
		if strings.Contains(prompt, card.Description) {
			t.Errorf("system prompt repeats %q, which the tool's description carries", card.Description)
		}
	}
	if d := descriptions["peer_sales-agent"]; !strings.Contains(d, "Answers sales questions.") {
		t.Errorf("peer tool description lost the card description: %q", d)
	}
	for _, s := range skills {
		if !strings.Contains(descriptions["peer_sales-agent"], s.Name) {
			t.Errorf("peer tool description lost skill %q", s.Name)
		}
	}
	if d := descriptions[a2a.WorkflowToolName("data-pipeline")]; !strings.Contains(d, "Runs the nightly pipeline.") {
		t.Errorf("workflow tool description lost the card description: %q", d)
	}
}
