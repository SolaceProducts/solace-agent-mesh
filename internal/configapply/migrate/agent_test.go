// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
)

const legacyAgentYAML = `
apps:
  - name: employee-app
    app_module: solace_agent_mesh.agent.sac.app
    broker:
      broker_url: ws://localhost:8080
    app_config:
      namespace: dev
      supports_streaming: true
      agent_name: Employee
      display_name: Employee Agent
      model_provider: [general]
      inject_user_profile: true
      instruction: "You are the EmployeeAgent."
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
        - tool_type: builtin-group
          group_name: employee_tools
          tool_config: { type: bamboohr }
        - tool_type: custom-inline
          # No group_name — should bucket into misc with a warning.
      session_service:
        type: memory
      artifact_service:
        type: s3
      agent_card_publishing: { interval_seconds: 10 }
      agent_card:
        description: "Employee data agent."
        defaultInputModes: [text]
        defaultOutputModes: [text]
        skills:
          - id: get_employee_data
            name: Get Employee Information
            description: Retrieves employee profiles.
          - id: get_org_chart
            name: Get Organizational Chart
            description: Looks up org charts.
`

func parseDoc(t *testing.T, src string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	return doc
}

func TestMigrateAgent_Employee(t *testing.T) {
	doc := parseDoc(t, legacyAgentYAML)
	clean, warnings, err := MigrateAgent(doc)
	if err != nil {
		t.Fatalf("MigrateAgent: %v", err)
	}
	if clean.Kind != "agent" || clean.Name != "Employee Agent" {
		t.Errorf("identity = %s/%s; want agent/'Employee Agent'", clean.Kind, clean.Name)
	}
	if clean.Description != "Employee data agent." {
		t.Errorf("description = %q", clean.Description)
	}

	if got, _ := clean.Spec["systemPrompt"].(string); got != "You are the EmployeeAgent." {
		t.Errorf("systemPrompt = %q", got)
	}

	provider, _ := clean.Spec["modelProvider"].([]string)
	if !slices.Equal(provider, []string{"general"}) {
		t.Errorf("modelProvider = %v", clean.Spec["modelProvider"])
	}

	toolsets, _ := clean.Spec["toolsets"].([]string)
	wantToolsets := []string{"artifact_management", "employee_tools", "misc"}
	if !slices.Equal(toolsets, wantToolsets) {
		t.Errorf("toolsets = %v; want %v", toolsets, wantToolsets)
	}

	skills, _ := clean.Spec["skills"].([]any)
	if len(skills) != 2 {
		t.Fatalf("skills count = %d; want 2", len(skills))
	}
	first, _ := skills[0].(map[string]any)
	if first["name"] != "Get Employee Information" || first["description"] != "Retrieves employee profiles." {
		t.Errorf("skills[0] = %v", first)
	}

	hasMisc := false
	for _, w := range warnings {
		if strings.Contains(w, "misc") {
			hasMisc = true
		}
	}
	if !hasMisc {
		t.Errorf("expected a misc-bucket warning; got: %v", warnings)
	}

	in, _ := clean.Spec["inputModes"].([]string)
	if !slices.Equal(in, []string{"text"}) {
		t.Errorf("inputModes = %v", in)
	}
	out, _ := clean.Spec["outputModes"].([]string)
	if !slices.Equal(out, []string{"text"}) {
		t.Errorf("outputModes = %v", out)
	}

	if _, err := translate.ForwardAgent(clean.Spec, translate.AgentHooks{}); err != nil {
		t.Fatalf("ForwardAgent on migrated spec: %v", err)
	}
}

func TestMigrateAgent_NameWhenDisplayNameMatchesAgentName(t *testing.T) {
	src := `
apps:
  - app_module: solace_agent_mesh.agent.sac.app
    app_config:
      agent_name: orchestrator
      display_name: orchestrator
      instruction: "x"
      agent_card:
        description: "d"
`
	clean, _, err := MigrateAgent(parseDoc(t, src))
	if err != nil {
		t.Fatalf("MigrateAgent: %v", err)
	}
	if clean.Name != "orchestrator" {
		t.Errorf("name = %q; want orchestrator", clean.Name)
	}
}

func TestMigrateAgent_FallsBackToDisplayNameWhenAgentNameMissing(t *testing.T) {
	src := `
apps:
  - app_module: solace_agent_mesh.agent.sac.app
    app_config:
      display_name: NoNameAgent
      instruction: "x"
      agent_card:
        description: "d"
`
	clean, _, err := MigrateAgent(parseDoc(t, src))
	if err != nil {
		t.Fatalf("MigrateAgent: %v", err)
	}
	if clean.Name != "NoNameAgent" {
		t.Errorf("name = %q; want NoNameAgent", clean.Name)
	}
}

func TestMigrateAgent_NoNameAtAllErrors(t *testing.T) {
	src := `
apps:
  - app_module: solace_agent_mesh.agent.sac.app
    app_config:
      instruction: "x"
`
	if _, _, err := MigrateAgent(parseDoc(t, src)); err == nil {
		t.Fatalf("expected error for missing name")
	}
}

func TestMigrateAgent_LeftoverFieldsBucketedAndWarned(t *testing.T) {
	src := `
apps:
  - app_module: solace_agent_mesh.agent.sac.app
    app_config:
      agent_name: A
      instruction: "x"
      mystery_field: 42
      another_unknown: foo
      agent_card:
        description: "d"
`
	clean, warnings, err := MigrateAgent(parseDoc(t, src))
	if err != nil {
		t.Fatalf("MigrateAgent: %v", err)
	}
	leg, _ := clean.Spec["legacy"].(map[string]any)
	if leg["mystery_field"] != 42 || leg["another_unknown"] != "foo" {
		t.Errorf("legacy bucket = %v", leg)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "spec.legacy") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected spec.legacy warning; got %v", warnings)
	}
}

func TestMigrateAgent_DroppedKeysSilentlyStripped(t *testing.T) {
	for _, key := range []string{"data_tools_config", "extract_content_from_artifact_config"} {
		t.Run(key, func(t *testing.T) {
			src := `
apps:
  - app_module: solace_agent_mesh.agent.sac.app
    app_config:
      agent_name: A
      instruction: "x"
      ` + key + `:
        timeout_seconds: 5
`
			clean, warnings, err := MigrateAgent(parseDoc(t, src))
			if err != nil {
				t.Fatalf("MigrateAgent: %v", err)
			}
			for _, w := range warnings {
				if strings.Contains(w, key) {
					t.Errorf("dropped key %q surfaced in warnings: %q", key, w)
				}
			}
			if legacy, _ := clean.Spec["legacy"].(map[string]any); legacy != nil {
				if _, present := legacy[key]; present {
					t.Errorf("dropped key %q leaked into spec.legacy", key)
				}
			}
		})
	}
}
