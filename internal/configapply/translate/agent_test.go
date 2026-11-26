// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const agentYAMLFixture = `
name: orchestrator
description: "Manages tasks and coordinates multi-agent workflows."
type: standard
systemPrompt: |
  You are the orchestrator agent.
modelProvider: [planning]
toolsets: [common, builder]
toolsetExcludedTools:
  common: [unsafe_eval]
toolsetConfigs:
  - toolsetName: common
    configValues:
      region: us-east-1
skills:
  - name: orchestrate_task
    description: Plans and dispatches multi-step tasks.
    required: []
    optional: []
skillRefs: [planner, data_analyst]
connectors: [postgres_employees]
inputModes: [text]
outputModes: [text, file]
deploy: true
`

func TestForwardAgent_FullSpec(t *testing.T) {
	in := decodeYAMLMap(t, agentYAMLFixture)

	got, err := ForwardAgent(in, AgentHooks{})
	if err != nil {
		t.Fatalf("ForwardAgent: %v", err)
	}

	want := entplatform.CreateAgentRequest{
		Name:         "orchestrator",
		Description:  "Manages tasks and coordinates multi-agent workflows.",
		Type:         "standard",
		SystemPrompt: "You are the orchestrator agent.\n",
		Skills: []entplatform.SkillRequest{
			{
				Name:        "orchestrate_task",
				Description: "Plans and dispatches multi-step tasks.",
				Required:    []string{},
				Optional:    []string{},
			},
		},
		SkillRefs: []string{"planner", "data_analyst"},
		Toolsets:  []string{"common", "builder"},
		ToolsetExcludedTools: map[string][]string{
			"common": {"unsafe_eval"},
		},
		ToolsetConfigs: []entplatform.ToolsetConfigBinding{
			{
				ToolsetName:  "common",
				ConfigValues: map[string]any{"region": "us-east-1"},
			},
		},
		Connectors:    []string{"postgres_employees"},
		InputModes:    []string{"text"},
		OutputModes:   []string{"text", "file"},
		ModelProvider: []string{"planning"},
		Deploy:        true,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("CreateAgentRequest mismatch (-want +got):\n%s", diff)
	}
}

func TestForwardAgent_A2ASkillFields(t *testing.T) {
	const yamlFixture = `
name: a2a-skills-agent
description: "Agent with the full A2A skill shape."
type: standard
systemPrompt: |
  System prompt.
skills:
  - id: research_topics
    name: Research
    description: Research topics in depth.
    tags: [research, knowledge]
    examples: ["Tell me about quantum computing", "Find recent ML papers"]
    inputModes: [text]
    outputModes: [text, application/json]
inputModes: [text]
outputModes: [text]
deploy: false
`
	in := decodeYAMLMap(t, yamlFixture)

	got, err := ForwardAgent(in, AgentHooks{})
	if err != nil {
		t.Fatalf("ForwardAgent: %v", err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(got.Skills))
	}
	want := entplatform.SkillRequest{
		ID:          "research_topics",
		Name:        "Research",
		Description: "Research topics in depth.",
		Tags:        []string{"research", "knowledge"},
		Examples:    []string{"Tell me about quantum computing", "Find recent ML papers"},
		InputModes:  []string{"text"},
		OutputModes: []string{"text", "application/json"},
	}
	if diff := cmp.Diff(want, got.Skills[0]); diff != "" {
		t.Errorf("Skills[0] mismatch (-want +got):\n%s", diff)
	}
}

func TestReverseAgent_A2ASkillFields(t *testing.T) {
	in := entplatform.AgentResponse{
		Name:        "a2a-skills-agent",
		Description: "desc",
		Skills: []entplatform.SkillResponse{
			{
				ID:          "research_topics",
				Name:        "Research",
				Description: "Research topics in depth.",
				Tags:        []string{"research", "knowledge"},
				Examples:    []string{"ex1", "ex2"},
				InputModes:  []string{"text"},
				OutputModes: []string{"text", "application/json"},
			},
		},
	}
	out, err := ReverseAgent(in, AgentHooks{})
	if err != nil {
		t.Fatalf("ReverseAgent: %v", err)
	}
	skillsAny, ok := out["skills"].([]any)
	if !ok || len(skillsAny) != 1 {
		t.Fatalf("expected skills array of length 1, got %T %v", out["skills"], out["skills"])
	}
	skill, ok := skillsAny[0].(map[string]any)
	if !ok {
		t.Fatalf("skill[0] not map[string]any, got %T", skillsAny[0])
	}
	for _, key := range []string{"id", "name", "description", "tags", "examples", "inputModes", "outputModes"} {
		if _, present := skill[key]; !present {
			t.Errorf("skill[0] missing key %q in reverse output: %v", key, skill)
		}
	}
}

func TestForwardAgent_AdditionalConfigurations_RoundTrip(t *testing.T) {
	const yamlFixture = `
name: orchestrator
description: "Manages tasks."
type: standard
systemPrompt: |
  System prompt.
inputModes: [text]
outputModes: [text]
deploy: true
additionalConfigurations:
  agentCardPublishing:
    intervalSeconds: 5
  supportsStreaming: true
`
	in := decodeYAMLMap(t, yamlFixture)
	create, err := ForwardAgent(in, AgentHooks{})
	if err != nil {
		t.Fatalf("ForwardAgent: %v", err)
	}
	if len(create.AdditionalConfigurations) == 0 {
		t.Fatalf("CreateAgentRequest.AdditionalConfigurations was not populated")
	}

	resp := entplatform.AgentResponse{
		Name:                     create.Name,
		Description:              create.Description,
		AdditionalConfigurations: create.AdditionalConfigurations,
	}
	out, err := ReverseAgent(resp, AgentHooks{})
	if err != nil {
		t.Fatalf("ReverseAgent: %v", err)
	}
	addCfg, ok := out["additionalConfigurations"].(map[string]any)
	if !ok {
		t.Fatalf("reverse output missing additionalConfigurations object: %T %v",
			out["additionalConfigurations"], out["additionalConfigurations"])
	}
	pub, ok := addCfg["agentCardPublishing"].(map[string]any)
	if !ok {
		t.Fatalf("missing agentCardPublishing object: %T %v",
			addCfg["agentCardPublishing"], addCfg["agentCardPublishing"])
	}
	if got, want := pub["intervalSeconds"], float64(5); got != want {
		t.Errorf("agentCardPublishing.intervalSeconds = %v, want %v", got, want)
	}
	if got, want := addCfg["supportsStreaming"], true; got != want {
		t.Errorf("supportsStreaming = %v, want %v", got, want)
	}
}

func TestReverseAgent_AdditionalConfigurations_EmptyOmitted(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("{}"), []byte(""), []byte("null")} {
		resp := entplatform.AgentResponse{
			Name:                     "agent",
			AdditionalConfigurations: raw,
		}
		out, err := ReverseAgent(resp, AgentHooks{})
		if err != nil {
			t.Fatalf("ReverseAgent(%q): %v", string(raw), err)
		}
		if _, present := out["additionalConfigurations"]; present {
			t.Errorf("ReverseAgent(%q) emitted additionalConfigurations; should be omitted",
				string(raw))
		}
	}
}

func TestForwardAgent_AdditionalConfigurations_NullAuthored(t *testing.T) {
	in := map[string]any{
		"name":                     "agent",
		"description":              "desc",
		"systemPrompt":             "p",
		"additionalConfigurations": nil,
	}
	out, err := ForwardAgent(in, AgentHooks{})
	if err != nil {
		t.Fatalf("ForwardAgent: %v", err)
	}
	if out.AdditionalConfigurations != nil {
		t.Errorf("expected nil AdditionalConfigurations on null-authored input, got %q",
			string(out.AdditionalConfigurations))
	}
}

func TestForwardAgentAcceptsSkillIdsField(t *testing.T) {
	in := map[string]any{
		"name":         "orchestrator",
		"description":  "An agent that orchestrates work.",
		"systemPrompt": "system",
		"skillIds":     []string{"support"},
	}
	out, err := ForwardAgent(in, AgentHooks{})
	if err != nil {
		t.Fatalf("ForwardAgent: %v", err)
	}
	if len(out.SkillIDs) != 1 || out.SkillIDs[0] != "support" {
		t.Errorf("SkillIDs = %v, want [support]", out.SkillIDs)
	}
}
