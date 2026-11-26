// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const builderWorkflowYAML = `
apps:
  - name: mood_to_playlist_workflow
    app_config:
      name: MoodToPlaylistWorkflow
      agent_type: workflow
      workflow:
        output_mapping:
          result: "{{summarize.output}}"
        agent_card:
          description: "Turns a described mood into a playlist."
        nodes:
          - id: summarize
            type: agent
            agent_name: Summarizer
            input:
              text: "{{workflow.input.mood}}"
`

func TestDesiredWorkflow_PrefersAppConfigName(t *testing.T) {
	t.Parallel()
	got, err := desiredWorkflow("MoodToPlaylistWorkflow", builderWorkflowYAML)
	if err != nil {
		t.Fatalf("desiredWorkflow: %v", err)
	}
	create, ok := got.create.(entplatform.CreateWorkflowRequest)
	if !ok {
		t.Fatalf("create is %T, want CreateWorkflowRequest", got.create)
	}
	if create.Name != "MoodToPlaylistWorkflow" {
		t.Errorf("Name = %q, want the app_config name", create.Name)
	}
	if create.Description != "Turns a described mood into a playlist." {
		t.Errorf("Description = %q", create.Description)
	}
}

func TestDesiredWorkflow_NamesFlatBuilderWorkflowByAgentName(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: mule_detection_workflow
    app_config:
      agent_name: MuleDetectionWorkflow
      agent_type: workflow
      description: "Scores a bank transaction for money-mule risk."
      nodes:
        - id: score_transaction
          type: agent
          agent_name: MuleRiskScorer
      output_mapping:
        risk: "{{score_transaction.output}}"
`
	got, err := desiredWorkflow("MuleDetectionWorkflow", y)
	if err != nil {
		t.Fatalf("desiredWorkflow: %v", err)
	}
	create, ok := got.create.(entplatform.CreateWorkflowRequest)
	if !ok {
		t.Fatalf("create is %T, want CreateWorkflowRequest", got.create)
	}
	if create.Name != "MuleDetectionWorkflow" {
		t.Errorf("Name = %q, want the app_config agent_name", create.Name)
	}
	if got.legacyName != "mule_detection_workflow" {
		t.Errorf("legacyName = %q, want the app name an earlier save stored it under", got.legacyName)
	}
}

func TestDesiredWorkflow_StatedNameOutranksAgentName(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: mule_detection_workflow
    app_config:
      name: StatedWorkflow
      agent_name: MuleDetectionWorkflow
      agent_type: workflow
      description: "Scores a bank transaction for money-mule risk."
      nodes:
        - id: score_transaction
          type: agent
          agent_name: MuleRiskScorer
      output_mapping:
        risk: "{{score_transaction.output}}"
`
	got, err := desiredWorkflow("MuleDetectionWorkflow", y)
	if err != nil {
		t.Fatalf("desiredWorkflow: %v", err)
	}
	create, ok := got.create.(entplatform.CreateWorkflowRequest)
	if !ok {
		t.Fatalf("create is %T, want CreateWorkflowRequest", got.create)
	}
	if create.Name != "StatedWorkflow" {
		t.Errorf("Name = %q, want the stated app_config name", create.Name)
	}
	if got.legacyName != "" {
		t.Errorf("legacyName = %q, want empty: the old rule chose the stated name too", got.legacyName)
	}
}

func TestDesiredWorkflow_ForwardsAppConfigVerbatim(t *testing.T) {
	t.Parallel()
	got, err := desiredWorkflow("MoodToPlaylistWorkflow", builderWorkflowYAML)
	if err != nil {
		t.Fatalf("desiredWorkflow: %v", err)
	}
	appConfig := got.create.(entplatform.CreateWorkflowRequest).AppConfig
	if _, nested := appConfig["workflow"]; !nested {
		t.Error("the authored nested shape was flattened; it must be forwarded as written")
	}
	if appConfig["name"] != "MoodToPlaylistWorkflow" {
		t.Error("the outer app_config name was lost")
	}
}

func TestWorkflowBody_HandlesBothShapes(t *testing.T) {
	t.Parallel()
	nested := map[string]any{"workflow": map[string]any{"nodes": []any{"a"}}}
	if body := workflowBody(nested); body["nodes"] == nil {
		t.Error("nested shape: nodes not reachable")
	}
	flat := map[string]any{"nodes": []any{"a"}}
	if body := workflowBody(flat); body["nodes"] == nil {
		t.Error("flat shape: nodes not reachable")
	}
}

func TestDesiredWorkflow_ReadsFlatBodyDescription(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: flat_wf
    app_config:
      name: FlatWorkflow
      description: "A flat-shaped workflow used by older configs."
      output_mapping:
        result: "{{n.output}}"
      nodes:
        - id: n
          type: agent
          agent_name: A
`
	got, err := desiredWorkflow("FlatWorkflow", y)
	if err != nil {
		t.Fatalf("desiredWorkflow: %v", err)
	}
	create := got.create.(entplatform.CreateWorkflowRequest)
	if create.Description != "A flat-shaped workflow used by older configs." {
		t.Errorf("Description = %q, want it read from the flat body", create.Description)
	}
}

func TestDesiredWorkflow_PlansOverHashView(t *testing.T) {
	t.Parallel()
	got, err := desiredWorkflow("MoodToPlaylistWorkflow", builderWorkflowYAML)
	if err != nil {
		t.Fatalf("desiredWorkflow: %v", err)
	}
	if _, ok := got.planProto.(workflowHashView); !ok {
		t.Fatalf("planProto is %T, want workflowHashView", got.planProto)
	}
}

func TestDesiredWorkflow_RejectsWorkflowLevelEntryForBoundConnector(t *testing.T) {
	t.Parallel()
	const content = `
apps:
  - name: fast-recommendation
    app_config:
      agent_name: fast-recommendation
      agent_type: workflow
      connectors:
        - name: Mysql-demo-db
          subtype: mysql
          type: sql
      nodes:
        - id: query_customer_data
          type: tool
          connector: Mysql-demo-db
          input:
            query: "SELECT 1"
      output_mapping:
        rows: "{{query_customer_data.output}}"
`
	_, err := desiredWorkflow("fast-recommendation", content)
	if err == nil {
		t.Fatal("Build & Activate accepted a workflow-level entry the engine cannot build")
	}
	if !strings.Contains(err.Error(), `tool node "query_customer_data"`) {
		t.Errorf("err = %v, want the binding node named", err)
	}
}
