// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const wantGoldenAgentCard = `{"name":"agent_0199aaaa_bbbb_7ccc_8ddd_eeeeffff0000","description":"A fully populated agent card pinned for Python SAM wire parity.","url":"solace:test-ns/a2a/v1/agent/request/agent_0199aaaa_bbbb_7ccc_8ddd_eeeeffff0000","version":"3.1.4","protocolVersion":"0.3.0","capabilities":{"streaming":true,"pushNotifications":false,"stateTransitionHistory":false,"extensions":[{"uri":"https://solace.com/a2a/extensions/sam/tools","params":{"tools":[{"description":"Echoes its input back.","effect":"side_effecting","effect_source":"default","id":"echo_tool","name":"echo_tool","requiredScopes":[],"required_scopes":[]},{"description":"Delegate a task to the \"data_agent\" agent.","effect":"side_effecting","effect_source":"declared","id":"peer_data_agent","name":"peer_data_agent","requiredScopes":[],"required_scopes":[]},{"description":"Delegate a task to the \"math_agent\" agent.","effect":"side_effecting","effect_source":"declared","id":"peer_math_agent","name":"peer_math_agent","requiredScopes":[],"required_scopes":[]}]}},{"uri":"https://solace.com/a2a/extensions/sam/tool-registrations","description":"Startup registration status of connector-sourced tool entries.","params":{"entries":[{"connector_id":"conn-1","name":"weather","status":"ready","tool_count":3,"tool_type":"mcp"}]}},{"uri":"https://solace.com/a2a/extensions/agent-type","params":{"type":"standard"}},{"uri":"https://solace.com/a2a/extensions/sam/schemas","params":{"input_schema":{"properties":{"query":{"type":"string"}},"required":["query"],"type":"object"},"output_schema":{"properties":{"answer":{"type":"string"}},"type":"object"}}},{"uri":"https://solace.com/a2a/extensions/sam/required-scopes","params":{"scopes":["agent:golden:invoke"]}},{"uri":"https://solace.com/a2a/extensions/display-name","params":{"display_name":"Golden Wire Agent"}},{"uri":"https://solace.com/a2a/extensions/sam/internal","params":{"internal":true,"show_sessions":true}},{"uri":"https://solace.com/a2a/extensions/sam/priority","params":{"priority":42}},{"uri":"https://solace.com/a2a/extensions/sam/ephemeral","params":{"ephemeral":true}},{"uri":"https://solace.com/a2a/extensions/sam/deployment","description":"SAM deployment tracking for rolling updates","params":{"deployment_id":"deploy_0199aaaa_bbbb_7ccc"}},{"uri":"https://solace.com/a2a/extensions/peer-agent-topology","description":"A list of peer agents this agent is configured to communicate with.","params":{"peer_agent_names":["data_agent","math_agent"]}},{"uri":"https://solace.com/a2a/extensions/sam/welcome","params":{"suggestions":[{"auto_send":true,"label":"Get started","prompt":"How do I start?"}],"welcome_message":"Welcome to the golden agent."}},{"uri":"https://solace.com/a2a/extensions/sam/skills","params":{"skills":[{"description":"PowerPoint authoring","name":"pptx"}]}}]},"defaultInputModes":["text","file"],"defaultOutputModes":["text"],"skills":[{"id":"summarize","name":"Summarize","description":"Summarize a body of text.","tags":["text","nlp"],"examples":["Summarize this article"]}],"provider":{"organization":"Solace","url":"https://solace.com"},"documentationUrl":"https://docs.example.com/golden-agent"}`

func buildGoldenAgentCard(t *testing.T) a2a.AgentCard {
	t.Helper()

	agent := New(AgentConfig{
		Name:               "agent_0199aaaa_bbbb_7ccc_8ddd_eeeeffff0000",
		DisplayName:        "Golden Wire Agent",
		Description:        "A fully populated agent card pinned for Python SAM wire parity.",
		Version:            "3.1.4",
		Namespace:          "test-ns",
		AgentType:          "standard",
		SupportsStreaming:  true,
		Priority:           42,
		DeploymentID:       "deploy_0199aaaa_bbbb_7ccc",
		DocumentationURL:   "https://docs.example.com/golden-agent",
		DefaultInputModes:  []string{"text", "file"},
		DefaultOutputModes: []string{"text"},
		RequiredScopes:     []string{"agent:golden:invoke"},
		Internal:           true,
		ShowSessions:       true,
		Ephemeral:          true,
		Provider: &a2a.AgentProvider{
			Organization: "Solace",
			URL:          "https://solace.com",
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []any{"query"},
		},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"answer": map[string]any{"type": "string"},
			},
		},
		Skills: []a2a.AgentSkill{
			{
				ID:          "summarize",
				Name:        "Summarize",
				Description: "Summarize a body of text.",
				Tags:        []string{"text", "nlp"},
				Examples:    []string{"Summarize this article"},
			},
		},
		Welcome: &WelcomeConfig{
			Message: "Welcome to the golden agent.",
			Suggestions: []WelcomeSuggestion{
				{Label: "Get started", Prompt: "How do I start?", AutoSend: true},
			},
		},
	})

	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "echo_tool",
		description: "Echoes its input back.",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
	}); err != nil {
		t.Fatal(err)
	}

	agent.SetToolRegistrations([]ToolEntryRegistration{
		{ToolType: "mcp", ConnectorID: "conn-1", Name: "weather", Status: "ready", ToolCount: 3},
	})

	agent.SetSkillManager(&fakeSkillManager{available: []AvailableSkill{
		{Name: "pptx", Description: "PowerPoint authoring"},
	}})

	agent.InjectPeerCard(a2a.AgentCard{Name: "math_agent"})
	agent.InjectPeerCard(a2a.AgentCard{Name: "data_agent"})

	return agent.BuildAgentCard()
}

func TestAgentCardWireGolden(t *testing.T) {
	card := buildGoldenAgentCard(t)

	got, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal agent card: %v", err)
	}

	if string(got) != wantGoldenAgentCard {
		t.Fatalf("agent-card wire bytes changed — this is a FROZEN A2A surface.\n"+
			"Review every byte against the frozen agent-identity wire surfaces,\n"+
			"then paste this into wantGoldenAgentCard:\n\n%s\n", got)
	}
}

func TestAgentCardWireIdentityBytes(t *testing.T) {
	card := buildGoldenAgentCard(t)
	got, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal agent card: %v", err)
	}
	payload := string(got)

	frozen := []string{
		`"name":"agent_0199aaaa_bbbb_7ccc_8ddd_eeeeffff0000"`,
		`"uri":"https://solace.com/a2a/extensions/display-name"`,
		`"display_name":"Golden Wire Agent"`,
		`"uri":"https://solace.com/a2a/extensions/sam/deployment"`,
		`"deployment_id":"deploy_0199aaaa_bbbb_7ccc"`,
		`"uri":"https://solace.com/a2a/extensions/sam/tools"`,
	}
	for _, want := range frozen {
		if !strings.Contains(payload, want) {
			t.Errorf("agent card missing frozen identity bytes %q\npayload: %s", want, payload)
		}
	}
}
