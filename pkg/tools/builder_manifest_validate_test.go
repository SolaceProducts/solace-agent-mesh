// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestValidateBuildManifest(t *testing.T) {
	tool := &ValidateBuildManifestTool{}

	tests := []struct {
		name           string
		yaml           string
		fullValidation bool
		wantValid      bool
		wantErrors     []string
	}{
		{
			name: "valid minimal manifest",
			yaml: `
name: my-project
components:
  - filename: my_agent.yaml
    type: agent
    name: MyAgent
    status: validated
    origin: session
`,
			wantValid: true,
		},
		{
			name: "valid manifest with event_mesh gateway",
			yaml: `
name: my-system
components:
  - filename: my_agent.yaml
    type: agent
    name: MyAgent
    status: validated
    origin: session
  - filename: my_gateway.yaml
    type: gateway
    gateway_type: event_mesh
    name: MyGateway
    status: validated
    origin: session
    depends_on: [MyAgent]
`,
			wantValid: true,
		},
		{
			name: "valid manifest with platform component",
			yaml: `
name: my-project
components:
  - filename: ""
    type: agent
    name: ExistingAgent
    status: deployed
    origin: platform
`,
			wantValid: true,
		},
		{
			name: "valid manifest with connector",
			yaml: `
name: my-project
components:
  - filename: my_db.json
    type: connector
    name: my_database
    status: validated
    origin: session
    connector_type: sql
    connector_subtype: postgres
`,
			wantValid: true,
		},
		{
			name: "valid manifest with a2a_agent",
			yaml: `
name: my-project
components:
  - filename: weather_proxy.json
    type: a2a_agent
    name: WeatherProxy
    status: validated
    origin: session
`,
			wantValid: true,
		},
		{
			name: "valid manifest with workflow tool node and platform toolset",
			yaml: `
name: my-project
components:
  - filename: weather_workflow.yaml
    type: workflow
    name: WeatherWorkflow
    status: validated
    origin: session
    nodes:
      - id: fetch_weather
        type: tool
        tool_name: WeatherToolset__get_current_weather
  - filename: ""
    type: toolset
    name: WeatherToolset
    status: deployed
    origin: platform
`,
			wantValid: true,
		},
		{
			name:       "empty input",
			yaml:       "",
			wantValid:  false,
			wantErrors: []string{"manifest_yaml is required"},
		},
		{
			name:       "invalid YAML",
			yaml:       "{{invalid",
			wantValid:  false,
			wantErrors: []string{"invalid YAML"},
		},
		{
			name: "missing name",
			yaml: `
components:
  - filename: a.yaml
    type: agent
    name: A
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"'name' is missing or empty"},
		},
		{
			name: "missing components",
			yaml: `
name: my-project
`,
			wantValid:  false,
			wantErrors: []string{"'components' is missing"},
		},
		{
			name: "empty components array",
			yaml: `
name: my-project
components: []
`,
			wantValid:  false,
			wantErrors: []string{"must not be empty"},
		},
		{
			name: "invalid component type",
			yaml: `
name: my-project
components:
  - filename: x.yaml
    type: service
    name: X
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"invalid component type 'service'"},
		},
		{
			name: "gateway without gateway_type",
			yaml: `
name: my-project
components:
  - filename: gw.yaml
    type: gateway
    name: MyGW
    status: draft
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"must specify 'gateway_type: event_mesh'"},
		},
		{
			name: "gateway with http_sse type",
			yaml: `
name: my-project
components:
  - filename: gw.yaml
    type: gateway
    gateway_type: http_sse
    name: MyGW
    status: draft
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"only 'event_mesh' is allowed"},
		},
		{
			name: "invalid status",
			yaml: `
name: my-project
components:
  - filename: a.yaml
    type: agent
    name: A
    status: ready
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"invalid status 'ready'"},
		},
		{
			name: "invalid origin",
			yaml: `
name: my-project
components:
  - filename: a.yaml
    type: agent
    name: A
    origin: external
`,
			wantValid:  false,
			wantErrors: []string{"invalid origin 'external'"},
		},
		{
			name: "duplicate component names",
			yaml: `
name: my-project
components:
  - filename: a.yaml
    type: agent
    name: MyAgent
    origin: session
  - filename: b.yaml
    type: agent
    name: MyAgent
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"duplicate component name 'MyAgent'"},
		},
		{
			name: "broken dependency reference",
			yaml: `
name: my-project
components:
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
    depends_on: [NonExistent]
`,
			wantValid:  false,
			wantErrors: []string{"dependency 'NonExistent' does not match"},
		},
		{
			name: "session origin with empty filename",
			yaml: `
name: my-project
components:
  - filename: ""
    type: agent
    name: A
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"origin 'session' must have a non-empty filename"},
		},
		{
			name: "platform origin with non-empty filename",
			yaml: `
name: my-project
components:
  - filename: leftover.yaml
    type: agent
    name: A
    origin: platform
`,
			wantValid:  false,
			wantErrors: []string{"origin 'platform' should have an empty filename"},
		},
		{
			name: "missing component name",
			yaml: `
name: my-project
components:
  - filename: a.yaml
    type: agent
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"'name' is missing or empty"},
		},
		{
			name: "multiple errors at once",
			yaml: `
name: ""
components:
  - filename: gw.yaml
    type: gateway
    name: GW
    origin: session
  - filename: a.yaml
    type: invalid
    name: GW
    origin: session
`,
			wantValid:  false,
			wantErrors: []string{"'name' is missing or empty", "must specify 'gateway_type: event_mesh'", "invalid component type", "duplicate component name"},
		},
		{
			name:           "cross: agent depends_on connector but missing connectors field",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: db.json
    type: connector
    name: my_db
    origin: session
    connector_type: sql
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [my_db]
`,
			wantValid:  false,
			wantErrors: []string{"does not list it in its 'connectors' field"},
		},
		{
			name:           "cross: agent connectors references non-existent component",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors: [ghost_connector]
`,
			wantValid:  false,
			wantErrors: []string{"not a connector component in the manifest"},
		},
		{
			name:           "cross: agent connectors references wrong type (agent instead of connector)",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: a.yaml
    type: agent
    name: OtherAgent
    origin: session
  - filename: b.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors: [OtherAgent]
`,
			wantValid:  false,
			wantErrors: []string{"not a connector component in the manifest"},
		},
		{
			name:           "cross: workflow node references non-existent agent",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
    nodes:
      - id: step1
        type: agent
        agent_name: MissingAgent
`,
			wantValid:  false,
			wantErrors: []string{"not an agent component in the manifest"},
		},
		{
			name:           "cross: valid agent with connector properly wired",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: db.json
    type: connector
    name: my_db
    origin: session
    connector_type: sql
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [my_db]
    connectors: [my_db]
`,
			wantValid: true,
		},
		{
			name:           "cross: valid workflow with existing agent nodes",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: a.yaml
    type: agent
    name: AgentA
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
    depends_on: [AgentA]
    nodes:
      - id: step1
        type: agent
        agent_name: AgentA
`,
			wantValid: true,
		},
		{
			name:           "cross: agent with multiple connectors, one missing from connectors field",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: db1.json
    type: connector
    name: db1
    origin: session
  - filename: db2.json
    type: connector
    name: db2
    origin: session
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [db1, db2]
    connectors: [db1]
`,
			wantValid:  false,
			wantErrors: []string{"depends on connector 'db2'"},
		},
		{
			name:           "cross: agent depends_on non-connector — no connectors field needed",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: a.yaml
    type: agent
    name: OtherAgent
    origin: session
  - filename: b.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [OtherAgent]
`,
			wantValid: true,
		},
		{
			name:           "cross: skipped without full_validation flag",
			fullValidation: false,
			yaml: `
name: my-project
components:
  - filename: db.json
    type: connector
    name: my_db
    origin: session
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [my_db]
`,
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"manifest_yaml": tt.yaml, "full_validation": tt.fullValidation}

			if tt.yaml == "" {
				result, err := tool.Execute(context.Background(), args, nil)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result == nil {
					t.Fatal("expected non-nil result")
				}
				return
			}

			result, err := tool.Execute(context.Background(), args, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}

			data := result.Data
			if data == nil {
				if tt.wantValid {
					t.Errorf("expected valid result but got nil data")
				}
				return
			}

			valid, _ := data["valid"].(bool)
			if valid != tt.wantValid {
				errList := extractValidationErrors(data)
				t.Errorf("valid = %v, want %v; errors: %v", valid, tt.wantValid, errList)
			}

			if !tt.wantValid && len(tt.wantErrors) > 0 {
				errList := extractValidationErrors(data)
				allErrText := ""
				for _, msg := range errList {
					allErrText += msg + " | "
				}
				for _, want := range tt.wantErrors {
					found := false
					for _, msg := range errList {
						if strings.Contains(msg, want) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got errors: %s", want, allErrText)
					}
				}
			}
		})
	}
}

func TestValidateBuildManifestWorkflowSchema(t *testing.T) {
	bmTool := &ValidateBuildManifestTool{}

	agentWithSchemas := `
apps:
  - name: Analyzer
    app_config:
      agent_name: Analyzer
      output_schema:
        type: object
        properties:
          score:
            type: number
          summary:
            type: string
        required:
          - score
          - summary
      input_schema:
        type: object
        properties:
          query:
            type: string
          data:
            type: object
            properties:
              score:
                type: number
              summary:
                type: string
            required:
              - score
              - summary
        required:
          - query
`

	agentNoSchema := `
apps:
  - name: Simple
    app_config:
      agent_name: Simple
`

	upstreamAgent := `
apps:
  - name: Upstream
    app_config:
      agent_name: Upstream
      output_schema:
        type: object
        properties:
          result:
            type: string
          count:
            type: number
        required:
          - result
`

	tests := []struct {
		name         string
		manifest     string
		artifacts    map[string]string
		wantValid    bool
		wantErrors   []string
		wantWarnings []string
	}{
		{
			name: "schema: valid linear workflow",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
    depends_on: [Analyzer]
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{step1.output}}"}
      input_schema:
        type: object
        properties:
          query:
            type: string
        required:
          - query
      nodes:
        - id: step1
          type: agent
          agent_name: Analyzer
          input:
            query: "{{workflow.input.query}}"
          depends_on: []
`,
			},
			wantValid: true,
		},
		{
			name: "schema: workflow tool node with platform toolset passes cross-validation",
			manifest: `
name: test-project
components:
  - filename: wf.yaml
    type: workflow
    name: WeatherWorkflow
    origin: session
  - filename: ""
    type: toolset
    name: WeatherToolset
    origin: platform
    status: deployed
`,
			artifacts: map[string]string{
				"wf.yaml": `
apps:
  - name: WeatherWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{fetch_weather.output}}"}
      tools:
        - tool_type: builtin
          tool_name: WeatherToolset__get_current_weather
      nodes:
        - id: fetch_weather
          type: tool
          tool_name: WeatherToolset__get_current_weather
`,
			},
			wantValid: true,
		},
		{
			name: "schema: no workflows in manifest",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
			},
			wantValid: true,
		},
		{
			name: "schema: agent without schemas skips gracefully",
			manifest: `
name: test-project
components:
  - filename: simple.yaml
    type: agent
    name: Simple
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"simple.yaml": agentNoSchema,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{step1.output}}"}
      nodes:
        - id: step1
          type: agent
          agent_name: Simple
          input:
            data: "{{workflow.input.data}}"
`,
			},
			wantValid: true,
		},
		{
			name: "schema: output_schema_override used",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{step2.output}}"}
      nodes:
        - id: step1
          type: agent
          agent_name: Analyzer
          input:
            query: "hello"
          output_schema_override:
            type: object
            properties:
              custom_field:
                type: string
        - id: step2
          type: agent
          agent_name: Analyzer
          depends_on: [step1]
          input:
            query: "{{step1.output.custom_field}}"
`,
			},
			wantValid: true,
		},
		{
			name: "schema: workflow.input refs valid",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{step1.output}}"}
      input_schema:
        type: object
        properties:
          search_term:
            type: string
      nodes:
        - id: step1
          type: agent
          agent_name: Analyzer
          input:
            query: "{{workflow.input.search_term}}"
`,
			},
			wantValid: true,
		},
		{
			name: "schema: coalesce operator refs valid",
			manifest: `
name: test-project
components:
  - filename: upstream.yaml
    type: agent
    name: Upstream
    origin: session
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"upstream.yaml": upstreamAgent,
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{analyze.output}}"}
      nodes:
        - id: fetch
          type: agent
          agent_name: Upstream
          input:
            query: "test"
        - id: analyze
          type: agent
          agent_name: Analyzer
          depends_on: [fetch]
          input:
            query:
              coalesce:
                - "{{fetch.output.result}}"
                - "fallback"
`,
			},
			wantValid: true,
		},
		{
			name: "schema: switch node with valid condition refs",
			manifest: `
name: test-project
components:
  - filename: upstream.yaml
    type: agent
    name: Upstream
    origin: session
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"upstream.yaml": upstreamAgent,
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{analyze.output}}"}
      nodes:
        - id: fetch
          type: agent
          agent_name: Upstream
          input:
            query: "test"
        - id: router
          type: switch
          depends_on: [fetch]
          cases:
            - condition: "{{fetch.output.result}} == 'high'"
              node: analyze
          default: analyze
        - id: analyze
          type: agent
          agent_name: Analyzer
          depends_on: [router]
          input:
            query: "{{fetch.output.result}}"
`,
			},
			wantValid: true,
		},
		{
			name: "schema: nil toolCtx still works",
			manifest: `
name: test-project
components:
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
    depends_on: [SomeAgent]
  - filename: a.yaml
    type: agent
    name: SomeAgent
    origin: session
`,
			artifacts: nil,
			wantValid: true,
		},
		{
			name: "schema: depends_on nonexistent node",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      nodes:
        - id: step1
          type: agent
          agent_name: Analyzer
          depends_on: [nonexistent_node]
          input:
            query: "hello"
`,
			},
			wantValid:  false,
			wantErrors: []string{"depends_on 'nonexistent_node' which does not exist"},
		},
		{
			name: "schema: template ref to nonexistent node",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      nodes:
        - id: step1
          type: agent
          agent_name: Analyzer
          input:
            query: "{{ghost_node.output.field}}"
`,
			},
			wantValid:  false,
			wantErrors: []string{"references non-existent node 'ghost_node'"},
		},
		{
			name: "schema: template ref to nonexistent field",
			manifest: `
name: test-project
components:
  - filename: upstream.yaml
    type: agent
    name: Upstream
    origin: session
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"upstream.yaml": upstreamAgent,
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      nodes:
        - id: fetch
          type: agent
          agent_name: Upstream
          input:
            query: "test"
        - id: analyze
          type: agent
          agent_name: Analyzer
          depends_on: [fetch]
          input:
            query: "{{fetch.output.bogus_field}}"
`,
			},
			wantValid:  false,
			wantErrors: []string{"'bogus_field' not in fetch's output schema"},
		},
		{
			name: "schema: missing required input field",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      nodes:
        - id: step1
          type: agent
          agent_name: Analyzer
          input:
            not_query: "hello"
`,
			},
			wantValid:  false,
			wantErrors: []string{"missing required input 'query'"},
		},
		{
			name: "schema: whole-output missing required fields",
			manifest: `
name: test-project
components:
  - filename: upstream.yaml
    type: agent
    name: Upstream
    origin: session
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"upstream.yaml": upstreamAgent,
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      nodes:
        - id: fetch
          type: agent
          agent_name: Upstream
          input:
            query: "test"
        - id: analyze
          type: agent
          agent_name: Analyzer
          depends_on: [fetch]
          input:
            query: "analyze this"
            data: "{{fetch.output}}"
`,
			},
			wantValid:  false,
			wantErrors: []string{"output missing required field 'score'", "output missing required field 'summary'"},
		},
		{
			name: "schema: whole-output of a tool node is not judged by its override",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      nodes:
        - id: fetch
          type: tool
          tool_name: web_request
          input:
            url: "https://example.com"
          output_schema_override:
            type: object
            properties:
              result: {type: string}
        - id: analyze
          type: agent
          agent_name: Analyzer
          depends_on: [fetch]
          input:
            query: "analyze this"
            data: "{{fetch.output}}"
      output_mapping:
        result: "{{analyze.output}}"
`,
			},
			wantValid: true,
		},
		{
			name: "schema: workflow.input ref to a field the input_schema lacks warns",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {result: "{{step1.output}}"}
      input_schema:
        type: object
        properties:
          query:
            type: string
      nodes:
        - id: step1
          type: agent
          agent_name: Analyzer
          input:
            query: "{{workflow.input.nonexistent_field}}"
`,
			},
			wantValid:    true,
			wantWarnings: []string{"'nonexistent_field' is not in workflow input_schema"},
		},
		{
			name: "schema: multiple errors in one workflow",
			manifest: `
name: test-project
components:
  - filename: upstream.yaml
    type: agent
    name: Upstream
    origin: session
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"upstream.yaml": upstreamAgent,
				"analyzer.yaml": agentWithSchemas,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      nodes:
        - id: fetch
          type: agent
          agent_name: Upstream
          depends_on: [phantom]
          input:
            query: "test"
        - id: analyze
          type: agent
          agent_name: Analyzer
          depends_on: [fetch]
          input:
            not_the_right_field: "{{fetch.output.bogus}}"
`,
			},
			wantValid: false,
			wantErrors: []string{
				"depends_on 'phantom' which does not exist",
				"'bogus' not in fetch's output schema",
				"missing required input 'query'",
			},
		},
		{
			name: "schema: output_mapping field of a schemaless session agent node",
			manifest: `
name: test-project
components:
  - filename: simple.yaml
    type: agent
    name: Simple
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"simple.yaml": agentNoSchema,
				"wf.yaml": `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      nodes:
        - id: decide
          type: agent
          agent_name: Simple
          input:
            transaction: "{{workflow.input}}"
      output_mapping:
        action: "{{decide.output.action}}"
`,
			},
			wantValid: false,
			wantErrors: []string{
				"output_mapping 'action' references '{{decide.output.action}}', but node 'decide' declares no output schema",
				"declare 'action' in the node's output_schema_override (or the output_schema of agent 'Simple')",
				"or map the whole '{{decide.output}}'",
			},
		},
		{
			name: "schema: artifact load fails gracefully",
			manifest: `
name: test-project
components:
  - filename: analyzer.yaml
    type: agent
    name: Analyzer
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
			artifacts: map[string]string{
				"analyzer.yaml": agentWithSchemas,
			},
			wantValid:  false,
			wantErrors: []string{"could not load config 'wf.yaml'"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"manifest_yaml":   tt.manifest,
				"full_validation": true,
			}

			var toolCtx *tool.Context
			if tt.artifacts != nil {
				svc := newMockArtifactService()
				toolCtx = newTestToolCtx(svc)
				bgCtx := context.Background()
				for filename, content := range tt.artifacts {
					saveTestArtifact(bgCtx, svc, toolCtx, filename, content, "application/x-yaml")
				}
			}

			result, err := bmTool.Execute(context.Background(), args, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}

			data := result.Data
			if data == nil {
				if tt.wantValid {
					t.Error("expected valid result but got nil data")
				}
				return
			}

			valid, _ := data["valid"].(bool)
			if valid != tt.wantValid {
				errList := extractValidationErrors(data)
				t.Errorf("valid = %v, want %v; errors: %v", valid, tt.wantValid, errList)
			}

			if !tt.wantValid && len(tt.wantErrors) > 0 {
				errList := extractValidationErrors(data)
				allErrText := strings.Join(errList, " | ")
				for _, want := range tt.wantErrors {
					found := false
					for _, msg := range errList {
						if strings.Contains(msg, want) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got errors: %s", want, allErrText)
					}
				}
			}

			assertWarningsContain(t, result, tt.wantWarnings)
		})
	}
}

func assertWarningsContain(t *testing.T, result *tool.Result, want []string) {
	t.Helper()
	if len(want) == 0 {
		return
	}
	warnings, _ := result.Data["warnings"].([]schema.ValidationError)
	for _, w := range want {
		if !slices.ContainsFunc(warnings, func(v schema.ValidationError) bool { return strings.Contains(v.Message, w) }) {
			t.Errorf("expected warning containing %q, got warnings: %v", w, warnings)
		}
	}
	if valid, _ := result.Data["valid"].(bool); valid && result.Message != "Build manifest is valid with warnings" {
		t.Errorf("message = %q, want %q", result.Message, "Build manifest is valid with warnings")
	}
}

func TestValidateBuildManifestStamp(t *testing.T) {
	bmTool := &ValidateBuildManifestTool{}

	validManifest := `
name: my-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    status: validated
`

	invalidManifest := `
name: my-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [missing_dep]
`

	t.Run("stamp set when full_validation passes", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		ctx := context.Background()

		saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", validManifest, "application/vnd.sam-build-manifest+yaml")
		saveTestArtifact(ctx, svc, toolCtx, "agent.yaml", "apps:\n  - name: MyAgent\n    app_config:\n      agent_name: MyAgent\n      instruction: hi\n", "application/x-yaml")

		args := map[string]any{
			"manifest_yaml":   validManifest,
			"full_validation": true,
		}
		result, err := bmTool.Execute(ctx, args, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		valid, _ := result.Data["valid"].(bool)
		if !valid {
			t.Fatal("expected valid=true")
		}

		loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "build_manifest.yaml")
		content, _, _ := svc.Load(ctx, loc, nil)
		if !strings.Contains(string(content), "full_validation_passed: true") {
			t.Errorf("expected manifest to contain 'full_validation_passed: true', got:\n%s", string(content))
		}
	})

	t.Run("re-stamping an already-stamped manifest writes no new version, or the frontend re-validates in a loop", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		ctx := context.Background()

		saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", validManifest, "application/vnd.sam-build-manifest+yaml")
		saveTestArtifact(ctx, svc, toolCtx, "agent.yaml", "apps:\n  - name: MyAgent\n    app_config:\n      agent_name: MyAgent\n      instruction: hi\n", "application/x-yaml")

		args := map[string]any{"manifest_yaml": validManifest, "full_validation": true}
		loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "build_manifest.yaml")

		if _, err := bmTool.Execute(ctx, args, toolCtx); err != nil {
			t.Fatalf("first execute: %v", err)
		}
		afterFirst, listErr := svc.ListVersions(ctx, loc)
		if listErr != nil {
			t.Fatalf("list versions after the first stamp: %v", listErr)
		}
		if len(afterFirst) != 2 {
			t.Fatalf("expected the seed plus one stamp, got %d version(s)", len(afterFirst))
		}
		content, _, _ := svc.Load(ctx, loc, nil)
		if !strings.Contains(string(content), "full_validation_passed: true") {
			t.Fatalf("first pass did not stamp:\n%s", content)
		}

		for i := range 2 {
			if _, err := bmTool.Execute(ctx, args, toolCtx); err != nil {
				t.Fatalf("re-execute %d: %v", i, err)
			}
		}
		afterRepeat, _ := svc.ListVersions(ctx, loc)

		if len(afterRepeat) != len(afterFirst) {
			t.Errorf("re-stamping added %d version(s) (%d → %d); an unchanged stamp must not write, or each one re-triggers a hidden validate turn",
				len(afterRepeat)-len(afterFirst), len(afterFirst), len(afterRepeat))
		}
		content, _, _ = svc.Load(ctx, loc, nil)
		if !strings.Contains(string(content), "full_validation_passed: true") {
			t.Errorf("manifest lost its stamp after a skipped re-write:\n%s", content)
		}
	})

	t.Run("stamps anyway when the stored manifest cannot be read, because only a positive match may skip the write", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		ctx := context.Background()

		saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", validManifest, "application/vnd.sam-build-manifest+yaml")
		saveTestArtifact(ctx, svc, toolCtx, "agent.yaml", "apps:\n  - name: MyAgent\n    app_config:\n      agent_name: MyAgent\n      instruction: hi\n", "application/x-yaml")
		args := map[string]any{"manifest_yaml": validManifest, "full_validation": true}
		loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "build_manifest.yaml")

		if _, err := bmTool.Execute(ctx, args, toolCtx); err != nil {
			t.Fatalf("seed stamp: %v", err)
		}
		before, _ := svc.ListVersions(ctx, loc)

		svc.failLoads = map[string]bool{"build_manifest.yaml": true}
		if _, err := bmTool.Execute(ctx, args, toolCtx); err != nil {
			t.Fatalf("execute: %v", err)
		}
		svc.failLoads = nil

		after, _ := svc.ListVersions(ctx, loc)
		if len(after) == len(before) {
			t.Errorf("a failed read was treated as a byte match and the stamp was skipped; a store degraded on reads would leave builds unstamped and undeployable")
		}
		content, _, _ := svc.Load(ctx, loc, nil)
		if !strings.Contains(string(content), "full_validation_passed: true") {
			t.Errorf("manifest is not stamped:\n%s", content)
		}
	})

	t.Run("stamp save fails on all attempts: error result, manifest unstamped", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		ctx := context.Background()

		saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", validManifest, "application/vnd.sam-build-manifest+yaml")
		saveTestArtifact(ctx, svc, toolCtx, "agent.yaml", "apps:\n  - name: MyAgent\n    app_config:\n      agent_name: MyAgent\n      instruction: hi\n", "application/x-yaml")

		svc.failSaves = map[string]int{"build_manifest.yaml": stampSaveMaxAttempts}

		args := map[string]any{
			"manifest_yaml":   validManifest,
			"full_validation": true,
		}
		result, err := bmTool.Execute(ctx, args, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != tool.StatusError {
			t.Errorf("expected status %q, got %q", tool.StatusError, result.Status)
		}
		if valid, _ := result.Data["valid"].(bool); !valid {
			t.Error("expected valid=true (validation passed, only stamp failed)")
		}
		if failed, _ := result.Data["stamp_failed"].(bool); !failed {
			t.Error("expected stamp_failed=true in result data")
		}

		loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "build_manifest.yaml")
		content, _, _ := svc.Load(ctx, loc, nil)
		if strings.Contains(string(content), "full_validation_passed") {
			t.Errorf("expected manifest to remain unstamped after save failure, got:\n%s", string(content))
		}
	})

	t.Run("stamp save succeeds on retry", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		ctx := context.Background()

		saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", validManifest, "application/vnd.sam-build-manifest+yaml")
		saveTestArtifact(ctx, svc, toolCtx, "agent.yaml", "apps:\n  - name: MyAgent\n    app_config:\n      agent_name: MyAgent\n      instruction: hi\n", "application/x-yaml")

		svc.failSaves = map[string]int{"build_manifest.yaml": 1}

		args := map[string]any{
			"manifest_yaml":   validManifest,
			"full_validation": true,
		}
		result, err := bmTool.Execute(ctx, args, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != tool.StatusSuccess {
			t.Errorf("expected status %q, got %q", tool.StatusSuccess, result.Status)
		}
		if _, ok := result.Data["stamp_failed"]; ok {
			t.Error("did not expect stamp_failed when the retry succeeds")
		}

		loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "build_manifest.yaml")
		content, _, _ := svc.Load(ctx, loc, nil)
		if !strings.Contains(string(content), "full_validation_passed: true") {
			t.Errorf("expected manifest stamped after retry, got:\n%s", string(content))
		}
	})

	t.Run("stamp NOT set when full_validation fails", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		ctx := context.Background()

		saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", invalidManifest, "application/vnd.sam-build-manifest+yaml")

		args := map[string]any{
			"manifest_yaml":   invalidManifest,
			"full_validation": true,
		}
		result, err := bmTool.Execute(ctx, args, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		valid, _ := result.Data["valid"].(bool)
		if valid {
			t.Fatal("expected valid=false for manifest with broken dependency")
		}

		loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "build_manifest.yaml")
		content, _, _ := svc.Load(ctx, loc, nil)
		if strings.Contains(string(content), "full_validation_passed") {
			t.Errorf("expected NO stamp on failed validation, got:\n%s", string(content))
		}
	})

	t.Run("stamp NOT set when full_validation is false", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		ctx := context.Background()

		saveTestArtifact(ctx, svc, toolCtx, "build_manifest.yaml", validManifest, "application/vnd.sam-build-manifest+yaml")

		args := map[string]any{
			"manifest_yaml":   validManifest,
			"full_validation": false,
		}
		result, err := bmTool.Execute(ctx, args, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		valid, _ := result.Data["valid"].(bool)
		if !valid {
			t.Fatal("expected valid=true")
		}

		loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "build_manifest.yaml")
		content, _, _ := svc.Load(ctx, loc, nil)
		if strings.Contains(string(content), "full_validation_passed") {
			t.Errorf("expected NO stamp when full_validation=false, got:\n%s", string(content))
		}
	})

	t.Run("stamp NOT set when toolCtx is nil", func(t *testing.T) {
		args := map[string]any{
			"manifest_yaml":   validManifest,
			"full_validation": true,
		}
		result, err := bmTool.Execute(context.Background(), args, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		valid, _ := result.Data["valid"].(bool)
		if !valid {
			t.Fatal("expected valid=true")
		}
	})
}

func TestValidateBuildManifestAgentConnectorConfig(t *testing.T) {
	bmTool := &ValidateBuildManifestTool{}

	agentWithConnector := `
apps:
  - name: my_agent
    connectors:
      - my_db
    app_config:
      agent_name: MyAgent
      instruction: "You are an agent that queries the database."
`

	agentMissingConnector := `
apps:
  - name: my_agent
    app_config:
      agent_name: MyAgent
      instruction: "You are an agent that queries the database."
`

	agentPartialConnector := `
apps:
  - name: my_agent
    connectors:
      - db1
    app_config:
      agent_name: MyAgent
      instruction: "You are an agent."
`

	tests := []struct {
		name       string
		manifest   string
		artifacts  map[string]string
		wantValid  bool
		wantErrors []string
	}{
		{
			name: "agent config has matching connectors field — valid",
			manifest: `
name: test-project
components:
  - filename: db.json
    type: connector
    name: my_db
    origin: session
    connector_type: sql
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [my_db]
    connectors: [my_db]
`,
			artifacts: map[string]string{
				"agent.yaml": agentWithConnector,
				"db.json":    `{"name": "my_db", "type": "sql", "subtype": "postgres", "values": {}}`,
			},
			wantValid: true,
		},
		{
			name: "agent config missing connectors field entirely — valid, the manifest binds them",
			manifest: `
name: test-project
components:
  - filename: db.json
    type: connector
    name: my_db
    origin: session
    connector_type: sql
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [my_db]
    connectors: [my_db]
`,
			artifacts: map[string]string{
				"agent.yaml": agentMissingConnector,
				"db.json":    `{"name": "my_db", "type": "sql", "subtype": "postgres", "values": {}}`,
			},
			wantValid: true,
		},
		{
			name: "agent config lists only some of the manifest's connectors — valid, the manifest binds them",
			manifest: `
name: test-project
components:
  - filename: db1.json
    type: connector
    name: db1
    origin: session
    connector_type: sql
  - filename: db2.json
    type: connector
    name: db2
    origin: session
    connector_type: sql
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [db1, db2]
    connectors: [db1, db2]
`,
			artifacts: map[string]string{
				"agent.yaml": agentPartialConnector,
				"db1.json":   `{"name": "db1", "type": "sql", "subtype": "postgres", "values": {}}`,
				"db2.json":   `{"name": "db2", "type": "sql", "subtype": "postgres", "values": {}}`,
			},
			wantValid: true,
		},
		{
			name: "agent with no connectors in manifest — no config check needed",
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
`,
			artifacts: map[string]string{
				"agent.yaml": agentMissingConnector,
			},
			wantValid: true,
		},
		{
			name: "session component config artifact missing fails full validation, not later at Build & Activate",
			manifest: `
name: test-project
components:
  - filename: db.json
    type: connector
    name: my_db
    origin: session
    connector_type: sql
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [my_db]
    connectors: [my_db]
`,
			artifacts:  map[string]string{},
			wantValid:  false,
			wantErrors: []string{"was not found"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"manifest_yaml":   tt.manifest,
				"full_validation": true,
			}

			var toolCtx *tool.Context
			if tt.artifacts != nil {
				svc := newMockArtifactService()
				toolCtx = newTestToolCtx(svc)
				bgCtx := context.Background()
				for filename, content := range tt.artifacts {
					saveTestArtifact(bgCtx, svc, toolCtx, filename, content, "application/x-yaml")
				}
			}

			result, err := bmTool.Execute(context.Background(), args, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}

			data := result.Data
			if data == nil {
				if tt.wantValid {
					t.Error("expected valid result but got nil data")
				}
				return
			}

			valid, _ := data["valid"].(bool)
			if valid != tt.wantValid {
				errList := extractValidationErrors(data)
				t.Errorf("valid = %v, want %v; errors: %v", valid, tt.wantValid, errList)
			}

			if !tt.wantValid && len(tt.wantErrors) > 0 {
				errList := extractValidationErrors(data)
				allErrText := strings.Join(errList, " | ")
				for _, want := range tt.wantErrors {
					found := false
					for _, msg := range errList {
						if strings.Contains(msg, want) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got errors: %s", want, allErrText)
					}
				}
			}
		})
	}
}

func TestValidateBuildManifestSkillComponents(t *testing.T) {
	bmTool := &ValidateBuildManifestTool{}

	tests := []struct {
		name           string
		yaml           string
		fullValidation bool
		wantValid      bool
		wantErrors     []string
	}{
		{
			name: "valid manifest with skill component",
			yaml: `
name: my-project
components:
  - filename: api_ref.yaml
    type: skill
    name: api-reference
    status: validated
    origin: session
`,
			wantValid: true,
		},
		{
			name:           "cross: agent references existing skill — valid",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: api_ref.yaml
    type: skill
    name: api-reference
    origin: session
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    skills: [api-reference]
`,
			wantValid: true,
		},
		{
			name:           "cross: agent references non-existent skill",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    skills: [missing-skill]
`,
			wantValid:  false,
			wantErrors: []string{"not a skill component in the manifest"},
		},
		{
			name:           "cross: agent references wrong type as skill",
			fullValidation: true,
			yaml: `
name: my-project
components:
  - filename: a.yaml
    type: agent
    name: OtherAgent
    origin: session
  - filename: b.yaml
    type: agent
    name: MyAgent
    origin: session
    skills: [OtherAgent]
`,
			wantValid:  false,
			wantErrors: []string{"not a skill component in the manifest"},
		},
		{
			name:           "cross: skill ref check skipped without full_validation",
			fullValidation: false,
			yaml: `
name: my-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    skills: [missing-skill]
`,
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"manifest_yaml": tt.yaml, "full_validation": tt.fullValidation}
			result, err := bmTool.Execute(context.Background(), args, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}

			data := result.Data
			if data == nil {
				if tt.wantValid {
					t.Error("expected valid result but got nil data")
				}
				return
			}

			valid, _ := data["valid"].(bool)
			if valid != tt.wantValid {
				errList := extractValidationErrors(data)
				t.Errorf("valid = %v, want %v; errors: %v", valid, tt.wantValid, errList)
			}

			if !tt.wantValid && len(tt.wantErrors) > 0 {
				errList := extractValidationErrors(data)
				allErrText := strings.Join(errList, " | ")
				for _, want := range tt.wantErrors {
					found := false
					for _, msg := range errList {
						if strings.Contains(msg, want) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error containing %q, got errors: %s", want, allErrText)
					}
				}
			}
		})
	}
}

func extractValidationErrors(data map[string]any) []string {
	rawErrs, ok := data["errors"]
	if !ok {
		return nil
	}
	switch errs := rawErrs.(type) {
	case []schema.ValidationError:
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message
		}
		return msgs
	case []any:
		var msgs []string
		for _, e := range errs {
			if ve, ok := e.(schema.ValidationError); ok {
				msgs = append(msgs, ve.Message)
			}
		}
		return msgs
	}
	return nil
}

func TestValidateBuildManifestUnstatedConnectors(t *testing.T) {
	bmTool := &ValidateBuildManifestTool{}

	agentConfig := `
apps:
  - name: my_agent
    app_config:
      agent_name: MyAgent
      instruction: "You are an agent."
`
	workflowConfig := `
apps:
  - name: MyWorkflow
    app_config:
      agent_type: workflow
      output_mapping: {answer: "{{ask.output}}"}
      nodes:
        - {id: ask, type: agent, agent_name: MyAgent}
`

	tests := []struct {
		name            string
		manifest        string
		wantWarning     bool
		wantWarningPath string
		wantValid       bool
		wantError       string
	}{
		{
			name:            "an agent that never mentions connectors is warned about",
			wantWarning:     true,
			wantWarningPath: "components[0].connectors",
			wantValid:       true,
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
`,
		},
		{
			name:        "an agent stating an empty list is not",
			wantWarning: false,
			wantValid:   true,
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors: []
`,
		},
		{
			name:        "a platform-origin agent is left alone, because the author cannot edit it",
			wantWarning: false,
			wantValid:   true,
			manifest: `
name: test-project
components:
  - filename: ""
    type: agent
    name: MyAgent
    origin: platform
`,
		},
		{
			name:        "an agent whose depends_on names a connector draws the wiring error, not the silence warning",
			wantWarning: false,
			wantValid:   false,
			wantError:   "does not list it in its 'connectors' field",
			manifest: `
name: test-project
components:
  - filename: db.json
    type: connector
    name: my_db
    origin: session
    connector_type: sql
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    depends_on: [my_db]
`,
		},
		{
			name:            "only the agent is asked, not the workflow beside it",
			wantWarning:     true,
			wantWarningPath: "components[0].connectors",
			wantValid:       true,
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
`,
		},
		{
			name:            "a bare connectors: with no value reads as unstated, which warns",
			wantWarning:     true,
			wantWarningPath: "components[0].connectors",
			wantValid:       true,
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors:
`,
		},
		{
			name:      "a scalar connectors is an error",
			wantValid: false,
			wantError: "not a list",
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors: my_db
`,
		},
		{
			name:      "a mapping connectors is an error",
			wantValid: false,
			wantError: "not a list",
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors: {a: b}
`,
		},
		{
			name:      "a list of maps is an error",
			wantValid: false,
			wantError: "entry 0 is map",
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors:
      - name: my_db
`,
		},
		{
			name:      "a list of non-strings is an error",
			wantValid: false,
			wantError: "entries must be plain names",
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors: [1, 2]
`,
		},
		{
			name:      "a malformed depends_on on a workflow is an error too",
			wantValid: false,
			wantError: "not a list",
			manifest: `
name: test-project
components:
  - filename: wf.yaml
    type: workflow
    name: MyWorkflow
    origin: session
    depends_on: MyAgent
`,
		},
		{
			name:      "a malformed tools list is an error",
			wantValid: false,
			wantError: "entries must be plain names",
			manifest: `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors: []
    tools:
      - name: web_request
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			saveTestArtifact(context.Background(), svc, toolCtx, "agent.yaml", agentConfig, "application/x-yaml")
			saveTestArtifact(context.Background(), svc, toolCtx, "wf.yaml", workflowConfig, "application/x-yaml")
			saveTestArtifact(context.Background(), svc, toolCtx, "db.json",
				`{"name": "my_db", "type": "sql", "subtype": "postgres", "values": {}}`, "application/json")

			result, err := bmTool.Execute(context.Background(), map[string]any{
				"manifest_yaml":   tt.manifest,
				"full_validation": true,
			}, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			warnings, _ := result.Data["warnings"].([]schema.ValidationError)
			var connectorWarnings []schema.ValidationError
			for _, w := range warnings {
				if strings.Contains(w.Message, "connectors") {
					connectorWarnings = append(connectorWarnings, w)
				}
			}
			wantCount := 0
			if tt.wantWarning {
				wantCount = 1
			}
			if len(connectorWarnings) != wantCount {
				t.Errorf("connectors warnings = %v, want %d", connectorWarnings, wantCount)
			}
			if tt.wantWarningPath != "" && len(connectorWarnings) > 0 && connectorWarnings[0].Path != tt.wantWarningPath {
				t.Errorf("warning path = %q, want %q", connectorWarnings[0].Path, tt.wantWarningPath)
			}
			if tt.wantValid {
				wantMsg := "Build manifest is valid"
				if len(warnings) > 0 {
					wantMsg += " with warnings"
				}
				if result.Message != wantMsg {
					t.Errorf("message = %q, want %q", result.Message, wantMsg)
				}
			}

			valid, _ := result.Data["valid"].(bool)
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v; errors: %v", valid, tt.wantValid, extractValidationErrors(result.Data))
			}

			if tt.wantError != "" {
				found := false
				for _, msg := range extractValidationErrors(result.Data) {
					if strings.Contains(msg, tt.wantError) {
						found = true
					}
				}
				if !found {
					t.Errorf("errors = %v, want one containing %q", extractValidationErrors(result.Data), tt.wantError)
				}
			}

			if tt.wantWarning && !valid && tt.wantError == "" {
				t.Error("a warning must not fail the build")
			}
		})
	}
}

func TestValidateBuildManifestListShapeCheckedWithoutFullValidation(t *testing.T) {
	bmTool := &ValidateBuildManifestTool{}

	manifest := `
name: test-project
components:
  - filename: agent.yaml
    type: agent
    name: MyAgent
    origin: session
    connectors:
      - name: my_db
`

	for _, fullValidation := range []bool{false, true} {
		name := "quick path"
		if fullValidation {
			name = "full validation"
		}
		t.Run(name, func(t *testing.T) {
			result, err := bmTool.Execute(context.Background(), map[string]any{
				"manifest_yaml":   manifest,
				"full_validation": fullValidation,
			}, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if valid, _ := result.Data["valid"].(bool); valid {
				t.Error("valid = true, want false — a list of maps is unreadable to the canvas whether or not the full pass ran")
			}
			found := false
			for _, msg := range extractValidationErrors(result.Data) {
				if strings.Contains(msg, "entry 0 is map") {
					found = true
				}
			}
			if !found {
				t.Errorf("errors = %v, want the malformed entry reported", extractValidationErrors(result.Data))
			}
		})
	}
}

func TestValidateBuildManifestNullOptionalListsReadAsAbsent(t *testing.T) {
	bmTool := &ValidateBuildManifestTool{}
	const agent = "{filename: a.yaml, type: agent, name: A, origin: session"

	manifests := map[string]string{
		"root risks":           "name: p\nrisks: null\ncomponents:\n  - " + agent + "}\n",
		"root env_vars":        "name: p\nenv_vars: ~\ncomponents:\n  - " + agent + "}\n",
		"component tools":      "name: p\ncomponents:\n  - " + agent + ", tools: null}\n",
		"component connectors": "name: p\ncomponents:\n  - " + agent + ", connectors: }\n",
		"component depends_on": "name: p\ncomponents:\n  - " + agent + ", depends_on: null}\n",
		"workflow nodes":       "name: p\ncomponents:\n  - {filename: w.yaml, type: workflow, name: W, origin: session, nodes: null}\n",
		"node depends_on":      "name: p\ncomponents:\n  - {filename: w.yaml, type: workflow, name: W, origin: session, nodes: [{id: n1, type: agent, agent_name: A, depends_on: null}]}\n",
	}
	for name, manifest := range manifests {
		t.Run(name, func(t *testing.T) {
			result, err := bmTool.Execute(context.Background(), map[string]any{"manifest_yaml": manifest}, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if valid, _ := result.Data["valid"].(bool); !valid {
				t.Errorf("valid = false, want true for a null optional list; errors: %v", extractValidationErrors(result.Data))
			}
		})
	}

	t.Run("a null required field is still refused", func(t *testing.T) {
		result, err := bmTool.Execute(context.Background(), map[string]any{
			"manifest_yaml": "name: p\ncomponents:\n  - {filename: w.yaml, type: workflow, name: W, origin: session, nodes: [{id: null, type: agent, agent_name: A}]}\n",
		}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valid, _ := result.Data["valid"].(bool); valid {
			t.Error("valid = true, want false for a null node id")
		}
	})

	t.Run("a null list item is still refused", func(t *testing.T) {
		result, err := bmTool.Execute(context.Background(), map[string]any{
			"manifest_yaml": "name: p\ncomponents:\n  - " + agent + ", tools: [null]}\n",
		}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valid, _ := result.Data["valid"].(bool); valid {
			t.Error("valid = true, want false for a null list item")
		}
	})
}
