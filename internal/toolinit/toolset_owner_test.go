// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestBuiltinToolHasToolset(t *testing.T) {
	tests := []struct {
		tool string
		want bool
	}{
		{"web_request", true},
		{"load_artifact", true},
		{"create_chart_from_plotly_config", true},
		{"mermaid_diagram_generator", true},
		{"ffmpeg", true},
		{"execute_sql_query", false},
		{"xlsx_read", false},
		{"docx_create_document", false},
		{"sub_task", false},
		{"no_such_tool", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			if got := BuiltinToolHasToolset(tt.tool); got != tt.want {
				t.Errorf("BuiltinToolHasToolset(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}
}

func TestBuiltinToolHasToolsetCoversEveryGroupMember(t *testing.T) {
	for group := range schema.ValidBuiltinGroups {
		for _, member := range LookupBuiltinGroup(group, Options{}) {
			if !BuiltinToolHasToolset(member) {
				t.Errorf("%q is a member of group %q but reports no toolset", member, group)
			}
		}
	}
}

const unbindableToolAgentYAML = `apps:
  - name: Sales
    app_config:
      agent_name: Sales
      description: Analyses sales data for the retail business.
      instruction: do it
      agent_card:
        description: Analyses sales data for the retail business.
        skills:
          - id: sales
            name: Sales
            description: Analyses sales data.
      tools:
        - tool_type: builtin
          tool_name: %s
`

func TestValidateComponentConfigFactoryRejectsWhatTheSaveRejects(t *testing.T) {
	validator := LookupBuiltinTool("validate_component_config", nil, Options{})
	if validator == nil {
		t.Fatal("validate_component_config is not registered")
	}
	tests := []struct {
		tool      string
		wantValid bool
	}{
		{"create_chart_from_plotly_config", true},
		{"web_request", true},
		{"execute_sql_query", false},
		{"xlsx_read", false},
		{"no_such_tool", false},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			config := strings.Replace(unbindableToolAgentYAML, "%s", tt.tool, 1)

			res, err := validator.Execute(t.Context(), map[string]any{"config_yaml": config}, &tool.Context{})
			if err != nil {
				t.Fatalf("execute: %v", err)
			}

			if got := res.Data["valid"]; got != tt.wantValid {
				t.Errorf("valid = %v, want %v (message: %s)", got, tt.wantValid, res.Message)
			}
			if tt.wantValid && res.Data["warnings"] != nil {
				t.Errorf("a tool a toolset registers still draws warnings: %v", res.Data["warnings"])
			}
		})
	}
}

func TestEveryValidToolNameIsGroupedOrDeclaredUngrouped(t *testing.T) {
	for name := range schema.ValidBuiltinToolNames {
		grouped := BuiltinToolHasToolset(name)
		ungrouped := schema.UngroupedBuiltinToolNames[name]
		if grouped == ungrouped {
			t.Errorf("%q: registered by a group = %v, declared ungrouped = %v; exactly one must hold", name, grouped, ungrouped)
		}
	}
	for name := range schema.UngroupedBuiltinToolNames {
		if !schema.ValidBuiltinToolNames[name] {
			t.Errorf("%q is declared ungrouped but is not a valid builtin tool name", name)
		}
	}
	for name := range schema.AutoRegisteredBuiltinToolNames {
		if !schema.UngroupedBuiltinToolNames[name] {
			t.Errorf("%q is declared auto-registered but is not in the ungrouped set", name)
		}
	}
}
