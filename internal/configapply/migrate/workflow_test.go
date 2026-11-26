// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"maps"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
)

const legacyWorkflowYAML = `
apps:
  - name: sfdc-case-advisor-workflow-app
    app_module: solace_agent_mesh.workflow.app
    app_config:
      namespace: prod
      name: SupportCaseAdvisorWorkflow
      display_name: "SFDC Case Advisor"
      session_service:
        type: memory
      artifact_service:
        type: s3
      max_workflow_execution_time_seconds: 900
      default_node_timeout_seconds: 300
      workflow:
        description: "Processes a new SFDC case creation event."
        version: "1.0.0"
        nodes:
          - id: get_case_context
            type: agent
            agent_name: SupportAgent
        input_schema:
          type: object
        output_mapping:
          CaseId__c: "{{get_case_context.output.case_id}}"
`

func TestMigrateWorkflow_SFDC(t *testing.T) {
	clean, warnings, err := MigrateWorkflow(parseDoc(t, legacyWorkflowYAML))
	if err != nil {
		t.Fatalf("MigrateWorkflow: %v", err)
	}
	if clean.Kind != "workflow" || clean.Name != "SFDC Case Advisor" {
		t.Errorf("identity = %s/%s", clean.Kind, clean.Name)
	}
	if !strings.Contains(clean.Description, "SFDC case creation") {
		t.Errorf("description = %q", clean.Description)
	}
	if _, ok := clean.Spec["max_workflow_execution_time_seconds"]; !ok {
		t.Errorf("max_workflow_execution_time_seconds missing; got keys %v", keys(clean.Spec))
	}
	if _, ok := clean.Spec["default_node_timeout_seconds"]; !ok {
		t.Errorf("default_node_timeout_seconds missing")
	}
	wf, ok := clean.Spec["workflow"].(map[string]any)
	if !ok {
		t.Fatalf("workflow block missing or wrong type")
	}
	if wf["description"] == nil {
		t.Errorf("workflow.description should be preserved")
	}
	for _, dropped := range []string{"namespace", "session_service", "artifact_service", "name", "display_name"} {
		if _, ok := clean.Spec[dropped]; ok {
			t.Errorf("expected %q to be dropped from spec", dropped)
		}
	}

	if len(warnings) > 0 {
		t.Logf("warnings: %v", warnings)
	}

	flat := map[string]any{"name": clean.Name}
	maps.Copy(flat, clean.Spec)
	if _, err := translate.ForwardWorkflowV2(flat, translate.WorkflowV2Hooks{}); err != nil {
		t.Fatalf("ForwardWorkflowV2 on migrated spec: %v", err)
	}
}

func TestMigrateWorkflow_SynthesisesDescriptionWhenMissing(t *testing.T) {
	src := `
apps:
  - app_module: solace_agent_mesh.workflow.app
    app_config:
      name: my_wf
      workflow: {}
`
	clean, warnings, err := MigrateWorkflow(parseDoc(t, src))
	if err != nil {
		t.Fatalf("MigrateWorkflow: %v", err)
	}
	if clean.Description == "" {
		t.Errorf("description should be synthesised")
	}
	hasSynth := false
	for _, w := range warnings {
		if strings.Contains(w, "synthesised") {
			hasSynth = true
		}
	}
	if !hasSynth {
		t.Errorf("expected synthesis warning; got %v", warnings)
	}
}

func TestMigrateWorkflow_NoNameErrors(t *testing.T) {
	src := `
apps:
  - app_module: solace_agent_mesh.workflow.app
    app_config: {}
`
	if _, _, err := MigrateWorkflow(parseDoc(t, src)); err == nil {
		t.Fatalf("expected error for missing name")
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
