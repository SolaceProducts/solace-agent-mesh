// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func appConfigFromYAML(t *testing.T, src string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return m
}

func TestWorkflowToolSource_FlagsUndeclaredToolNodes(t *testing.T) {
	cases := []struct {
		name      string
		appConfig string
		wantPaths []string
	}{
		{
			name: "flat body, as the Builder writes it",
			appConfig: `
agent_type: workflow
nodes:
  - id: fetch_weather
    type: tool
    tool_name: WeatherToolset__get_current_weather
`,
			wantPaths: []string{"nodes[0]/tool_name"},
		},
		{
			name: "nested body, as the platform stores it",
			appConfig: `
agent_type: workflow
workflow:
  nodes:
    - id: step
      type: agent
      agent_name: Worker
    - id: stamp
      type: tool
      tool_name: get_current_time
`,
			wantPaths: []string{"workflow/nodes[1]/tool_name"},
		},
		{
			name: "every undeclared tool node is reported",
			appConfig: `
nodes:
  - id: a
    type: tool
    tool_name: get_current_time
  - id: b
    type: tool
    toolName: web_request
`,
			wantPaths: []string{"nodes[0]/tool_name", "nodes[1]/toolName"},
		},
		{
			name: "a non-string tool_name is still a tool the engine will try to dispatch",
			appConfig: `
nodes:
  - id: a
    type: tool
    tool_name: 123
`,
			wantPaths: []string{"nodes[0]/tool_name"},
		},
		{
			name: "empty declarations are no declaration",
			appConfig: `
tools: []
toolsets: []
connectors: []
nodes:
  - id: stamp
    type: tool
    tool_name: get_current_time
`,
			wantPaths: []string{"nodes[0]/tool_name"},
		},
		{
			name: "a connector: on a non-tool node binds nothing",
			appConfig: `
nodes:
  - id: step
    type: agent
    agent_name: Worker
    connector: petstore
  - id: one
    type: tool
    tool_name: get_pet
`,
			wantPaths: []string{"nodes[1]/tool_name"},
		},
		{
			name: "an empty connector: binds nothing",
			appConfig: `
nodes:
  - id: all
    type: tool
    connector: ""
    tool: list_pets
  - id: one
    type: tool
    tool_name: get_pet
`,
			wantPaths: []string{"nodes[1]/tool_name"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := workflowToolSourceValidationErrors(appConfigFromYAML(t, tc.appConfig))
			var got []string
			for _, e := range errs {
				got = append(got, e.Path)
			}
			if strings.Join(got, ",") != strings.Join(tc.wantPaths, ",") {
				t.Fatalf("paths = %v, want %v (errors: %v)", got, tc.wantPaths, errs)
			}
		})
	}
}

func TestWorkflowToolSource_AcceptsDeclaredSources(t *testing.T) {
	toolNode := `
  - id: stamp
    type: tool
    tool_name: get_current_time
`
	cases := map[string]string{
		"tools: in the body":      "tools:\n  - tool_type: builtin\n    tool_name: get_current_time\nnodes:" + toolNode,
		"toolsets: in the body":   "toolsets: [TimeToolset]\nnodes:" + toolNode,
		"connectors: in the body": "connectors:\n  - name: ops\n    type: email\nnodes:" + toolNode,
		"toolsets: beside a nested body": "toolsets: [TimeToolset]\nworkflow:\n  nodes:\n" +
			indent(toolNode),
		"tools: inside a nested body": "workflow:\n  tools:\n    - tool_type: builtin\n      tool_name: get_current_time\n  nodes:\n" +
			indent(toolNode),
		"a node-level connector": `
nodes:
  - id: place
    type: tool
    connector: orders_db
    tool_name: place_order
`,
		"a plain tool node beside a node-level connector": `
nodes:
  - id: all
    type: tool
    connector: petstore
    tool: list_pets
  - id: one
    type: tool
    tool_name: get_pet
`,
		"a node-level connector inside a nested body": `
workflow:
  nodes:
    - id: all
      type: tool
      connector: petstore
    - id: one
      type: tool
      tool_name: get_pet
`,
		"no tool nodes at all": `
nodes:
  - id: step
    type: agent
    agent_name: Worker
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if errs := workflowToolSourceValidationErrors(appConfigFromYAML(t, src)); len(errs) != 0 {
				t.Fatalf("want no errors, got %v", errs)
			}
		})
	}
}

func TestWorkflowToolSource_SuggestsTheMatchingDeclaration(t *testing.T) {
	cases := map[string][]string{
		"WeatherToolset__get_current_weather": {
			"`toolsets: [WeatherToolset]`",
			"{tool_type: builtin, tool_name: WeatherToolset__get_current_weather}",
			"skill",
			"do not reach workflow tool nodes",
		},
		"get_current_time": {
			"`{tool_type: builtin, tool_name: get_current_time}`",
			"`connector: <name>` on the node",
		},
		"__weird":  {"`{tool_type: builtin, tool_name: __weird}`"},
		"prefix__": {"`{tool_type: builtin, tool_name: prefix__}`"},
	}
	for toolName, wants := range cases {
		t.Run(toolName, func(t *testing.T) {
			errs := workflowToolSourceValidationErrors(map[string]any{
				"nodes": []any{map[string]any{"id": "n", "type": "tool", "tool_name": toolName}},
			})
			if len(errs) != 1 {
				t.Fatalf("want one error, got %v", errs)
			}
			for _, part := range append([]string{`"n"`, toolName, "fails whenever it runs"}, wants...) {
				if !strings.Contains(errs[0].Message, part) {
					t.Errorf("message %q is missing %q", errs[0].Message, part)
				}
			}
		})
	}
}

func TestValidateConfig_RejectsWorkflowToolNodeWithoutToolSource(t *testing.T) {
	src := `apps:
  - name: weather_flow
    app_config:
      agent_name: WeatherFlow
      agent_type: workflow
      description: "Workflow whose tool node names a toolset tool it never declares"
      nodes:
        - id: fetch_weather
          type: tool
          tool_name: WeatherToolset__get_current_weather
      output_mapping:
        weather: "{{fetch_weather.output}}"
`
	r, err := ValidateConfig([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("want invalid: the tool node has no declared source and fails whenever it runs")
	}
	want := "apps[0].app_config/nodes[0]/tool_name"
	for _, e := range r.Errors {
		if e.Path == want {
			return
		}
	}
	t.Fatalf("no error at %s; errors: %v", want, r.Errors)
}

func TestValidateConfig_AcceptsWorkflowDeclaringToolsets(t *testing.T) {
	src := `apps:
  - name: weather_flow
    app_config:
      agent_name: WeatherFlow
      agent_type: workflow
      description: "Workflow whose tool node calls a tool of a declared toolset"
      toolsets: [WeatherToolset]
      nodes:
        - id: fetch_weather
          type: tool
          tool_name: WeatherToolset__get_current_weather
      output_mapping:
        weather: "{{fetch_weather.output}}"
`
	r, err := ValidateConfig([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid || len(r.Errors) != 0 {
		t.Fatalf("want valid with no errors, got valid=%v errors=%v", r.Valid, r.Errors)
	}
}

func TestValidateConfig_AcceptsWorkflowToolNodeBindingConnector(t *testing.T) {
	src := `apps:
  - name: pets_flow
    app_config:
      agent_name: PetsFlow
      agent_type: workflow
      description: "Workflow whose tool nodes bind a platform connector on the node"
      nodes:
        - id: all
          type: tool
          connector: petstore
          tool: list_pets
        - id: one
          type: tool
          tool_name: get_pet
          depends_on: [all]
      output_mapping:
        pets: "{{one.output}}"
`
	r, err := ValidateConfig([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid || len(r.Errors) != 0 {
		t.Fatalf("want valid with no errors, got valid=%v errors=%v", r.Valid, r.Errors)
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimPrefix(s, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

func TestWorkflowToolSourceError_JoinsEveryNode(t *testing.T) {
	err := WorkflowToolSourceError(appConfigFromYAML(t, `
nodes:
  - id: a
    type: tool
    tool_name: get_current_time
  - id: b
    type: tool
    tool_name: web_request
`))
	if err == nil || !strings.Contains(err.Error(), `tool node "a"`) || !strings.Contains(err.Error(), `tool node "b"`) {
		t.Fatalf("want one error naming both nodes, got %v", err)
	}
	if n := strings.Count(err.Error(), "declares no tools:, toolsets: or connectors:"); n != 1 {
		t.Fatalf("want the header once, got %d times in %q", n, err)
	}
	if err := WorkflowToolSourceError(appConfigFromYAML(t, "toolsets: [TimeToolset]\nnodes:\n  - id: a\n    type: tool\n    tool_name: x\n")); err != nil {
		t.Fatalf("a declared source must pass, got %v", err)
	}
}
