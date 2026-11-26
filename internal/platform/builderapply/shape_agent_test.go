// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const builderAgentYAML = `
apps:
  - name: chat_assistant
    app_config:
      agent_name: chat_assistant
      instruction: |
        You are a helpful assistant. Be conversational and clear.
        When the user's request is ambiguous, ask for clarification.
      tools:
        - tool_type: builtin-group
          group_name: general_agent_tools
        - tool_type: builtin-group
          group_name: artifact_management
      artifact_handling_mode: reference
      agent_card:
        description: "General-purpose assistant for questions and tasks."
        skills:
          - id: general-help
            name: General Help
            description: "Answer questions and assist with everyday tasks."
`

func TestDesiredAgent_ConvertsBuilderArtifact(t *testing.T) {
	t.Parallel()
	got, err := desiredAgent("chat_assistant", builderAgentYAML, newToolsetCatalog())
	if err != nil {
		t.Fatalf("desiredAgent: %v", err)
	}
	create, ok := got.create.(entplatform.CreateAgentRequest)
	if !ok {
		t.Fatalf("create is %T, want CreateAgentRequest", got.create)
	}
	if create.Name != "chat_assistant" {
		t.Errorf("Name = %q", create.Name)
	}
	if create.Description != "General-purpose assistant for questions and tasks." {
		t.Errorf("Description = %q, want the agent_card description", create.Description)
	}
	if create.SystemPrompt == "" {
		t.Error("SystemPrompt is empty; instruction should have become it")
	}
	if create.Deploy {
		t.Error("Deploy must be false — saving is not deploying")
	}
	if len(create.Skills) != 1 || create.Skills[0].Name != "General Help" {
		t.Errorf("Skills = %+v, want the single advertised card skill", create.Skills)
	}
}

func TestDesiredAgent_SavesRegistryKeysAndSaysWhatHappensToTheModel(t *testing.T) {
	t.Parallel()
	yaml := strings.Replace(builderAgentYAML, "      agent_name: chat_assistant\n",
		"      agent_name: chat_assistant\n      supports_streaming: true\n      model: ${LLM_SERVICE_GENERAL_MODEL_NAME}\n", 1)
	got, err := desiredAgent("chat_assistant", yaml, newToolsetCatalog())
	if err != nil {
		t.Fatalf("desiredAgent: %v", err)
	}
	want := []string{"agent config key model was not saved: the agent's model comes from its model_provider binding"}
	if !slices.Equal(got.warnings, want) {
		t.Errorf("warnings =\n%q\nwant\n%q", got.warnings, want)
	}
	create := got.create.(entplatform.CreateAgentRequest)
	var stored map[string]any
	if err := json.Unmarshal(create.AdditionalConfigurations, &stored); err != nil {
		t.Fatalf("additionalConfigurations %s: %v", create.AdditionalConfigurations, err)
	}
	wantStored := map[string]any{"supportsStreaming": true, "artifactHandlingMode": "reference"}
	if !maps.Equal(stored, wantStored) {
		t.Errorf("additionalConfigurations = %v, want %v", stored, wantStored)
	}
	if !maps.Equal(got.extraConfig, wantStored) {
		t.Errorf("extraConfig = %v, want %v handed on for the carry", got.extraConfig, wantStored)
	}
}

func TestDesiredAgent_BindsToolsets(t *testing.T) {
	t.Parallel()
	got, err := desiredAgent("chat_assistant", builderAgentYAML, newToolsetCatalog())
	if err != nil {
		t.Fatalf("desiredAgent: %v", err)
	}
	create := got.create.(entplatform.CreateAgentRequest)
	if len(create.Toolsets) == 0 {
		t.Fatal("Toolsets is empty; the tools: block did not translate")
	}
	if !slices.Contains(create.Toolsets, "builtin_artifact_tools") {
		t.Errorf("Toolsets = %v, want the canonical builtin_artifact_tools", create.Toolsets)
	}
	if !slices.Contains(create.Toolsets, "general_agent_tools") {
		t.Errorf("Toolsets = %v, want general_agent_tools (it has no alias)", create.Toolsets)
	}
}

func TestDesiredAgent_BindsIndividualBuiltinTool(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: fetcher
    app_config:
      agent_name: fetcher
      instruction: Fetch things from the web.
      tools:
        - tool_type: builtin
          tool_name: web_request
      agent_card:
        description: "Fetches documents over HTTP."
`
	got, err := desiredAgent("fetcher", y, newToolsetCatalog())
	if err != nil {
		t.Fatalf("desiredAgent: %v", err)
	}
	create := got.create.(entplatform.CreateAgentRequest)
	if !slices.Contains(create.Toolsets, "builtin_web_request_tools") {
		t.Fatalf("Toolsets = %v, want the owning toolset of web_request", create.Toolsets)
	}
	if slices.Contains(create.Toolsets, "misc") {
		t.Error(`bound a "misc" toolset, which does not exist on the platform`)
	}
	if drop := create.ToolsetExcludedTools["builtin_web_request_tools"]; !slices.Contains(drop, "web_search_google") {
		t.Errorf("excluded = %v, want the unrequested sibling narrowed out", drop)
	}
}

func TestDesiredAgent_LoadableSkillsBecomeRefs(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: helper
    app_config:
      agent_name: helper
      instruction: Help.
      skills:
        - name: my-custom-skill
        - another-skill
      agent_card:
        description: "A helper agent for tests."
        skills:
          - id: card-skill
            name: Card Skill
            description: "Advertised capability."
`
	got, err := desiredAgent("helper", y, newToolsetCatalog())
	if err != nil {
		t.Fatalf("desiredAgent: %v", err)
	}
	create := got.create.(entplatform.CreateAgentRequest)
	for _, want := range []string{"my-custom-skill", "another-skill"} {
		if !slices.Contains(create.SkillIDs, want) {
			t.Errorf("SkillIDs = %v, want it to carry %q for cross-reference resolution", create.SkillIDs, want)
		}
	}
	if len(create.Skills) != 1 || create.Skills[0].Name != "Card Skill" {
		t.Errorf("Skills = %+v, want only the advertised card skill", create.Skills)
	}
}

func TestDesiredAgent_RejectsUnknownTool(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: broken
    app_config:
      agent_name: broken
      instruction: Do things.
      tools:
        - tool_type: builtin
          tool_name: no_such_tool
`
	if _, err := desiredAgent("broken", y, newToolsetCatalog()); err == nil {
		t.Fatal("expected an error: shipping an agent missing its capability is worse than refusing the save")
	}
}

func TestDesiredAgent_RejectsMalformedEnvelope(t *testing.T) {
	t.Parallel()
	for name, y := range map[string]string{
		"no apps":       "foo: bar\n",
		"no app_config": "apps:\n  - name: x\n",
		"not yaml":      "\t\tnope: [",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := desiredAgent("x", y, newToolsetCatalog()); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestDesiredAgent_DeckToolBringsDeckSkill(t *testing.T) {
	t.Parallel()
	agent := func(tools, skills string) string {
		return `
apps:
  - name: reporter
    app_config:
      agent_name: reporter
      instruction: Report on sales.
      tools:
` + tools + skills + `
      agent_card:
        description: "Reports on sales data."
`
	}
	const (
		group = `        - tool_type: builtin-group
          group_name: builtin_document_tools
`
		pptxOnly = `        - tool_type: builtin
          tool_name: pptx_create_presentation
`
		pdfOnly = `        - tool_type: builtin
          tool_name: pdf_ops
`
		listsDeckSkill = `      skills:
        - name: sam-pptx
        - sam-docx
`
		otherGroup = `        - tool_type: builtin-group
          group_name: artifact_management
`
		noTools = `
apps:
  - name: reporter
    app_config:
      agent_name: reporter
      instruction: Report on sales.
      agent_card:
        description: "Reports on sales data."
`
	)
	for _, tc := range []struct {
		name, yaml  string
		listsSkills bool
		wantCount   int
	}{
		{"document tools group", agent(group, ""), false, 1},
		{"the pptx tool alone", agent(pptxOnly, ""), false, 1},
		{"skill already listed", agent(group, listsDeckSkill), true, 1},
		{"document tools without the pptx tool", agent(pdfOnly, ""), false, 0},
		{"a different tool group", agent(otherGroup, ""), false, 0},
		{"no tools block", noTools, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := desiredAgent("reporter", tc.yaml, newToolsetCatalog())
			if err != nil {
				t.Fatalf("desiredAgent: %v", err)
			}
			create := got.create.(entplatform.CreateAgentRequest)
			n := 0
			for _, r := range create.SkillRefs {
				if r == deckSkill {
					n++
				}
			}
			if n != tc.wantCount {
				t.Errorf("SkillRefs = %v, want %s %d time(s)", create.SkillRefs, deckSkill, tc.wantCount)
			}
			if tc.listsSkills && !slices.Contains(create.SkillRefs, "sam-docx") {
				t.Errorf("SkillRefs = %v, lost the authored sam-docx", create.SkillRefs)
			}
		})
	}
}
