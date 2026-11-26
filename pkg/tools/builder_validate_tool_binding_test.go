// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func bindableOnly(names ...string) func(string) bool {
	return func(tool string) bool {
		for _, n := range names {
			if n == tool {
				return true
			}
		}
		return false
	}
}

func configWithTools(kind string, toolEntries string) string {
	header := "agent_name: Sales\n      description: Analyses sales data for the retail business.\n      instruction: do it\n      agent_card:\n        description: Analyses sales data for the retail business.\n        skills:\n          - id: sales\n            name: Sales\n            description: Analyses sales data.\n"
	if kind == "workflow" {
		header = "agent_type: workflow\n      agent_name: Flow\n"
	}
	return fmt.Sprintf("apps:\n  - name: Sales\n    app_config:\n      %s      tools:\n%s", header, toolEntries)
}

func builtinEntry(name string) string {
	return fmt.Sprintf("        - tool_type: builtin\n          tool_name: %s\n", name)
}

func resultFor(config string, hasToolset func(string) bool) *schema.ValidationResult {
	result := &schema.ValidationResult{Valid: true}
	result.Warnings = []schema.ValidationError{
		{Path: "apps[0].app_config/tools[0]/tool_name", Message: "schema layer warning"},
		{Path: "apps[0].app_config/description", Message: "unrelated warning"},
	}
	reconcileToolBinding(result, []byte(config), hasToolset)
	return result
}

func TestReconcileToolBinding(t *testing.T) {
	bindable := bindableOnly("web_request", "create_chart_from_plotly_config")
	tests := []struct {
		name         string
		kind         string
		entries      string
		hasToolset   func(string) bool
		wantValid    bool
		wantErrFor   string
		wantErrWords []string
		wantWarnings int
	}{
		{
			name:         "a tool a toolset registers binds and loses the schema warning",
			kind:         "agent",
			entries:      builtinEntry("create_chart_from_plotly_config"),
			hasToolset:   bindable,
			wantValid:    true,
			wantWarnings: 1,
		},
		{
			name:         "a connector runtime is an error naming the connector route",
			kind:         "agent",
			entries:      builtinEntry("execute_sql_query"),
			hasToolset:   bindable,
			wantErrFor:   "execute_sql_query",
			wantErrWords: []string{"SQL connectors", "connectors"},
			wantWarnings: 1,
		},
		{
			name:         "a skill's tool is an error naming the skill",
			kind:         "agent",
			entries:      builtinEntry("xlsx_read"),
			hasToolset:   bindable,
			wantErrFor:   "xlsx_read",
			wantErrWords: []string{"sam-excel", "skills:"},
			wantWarnings: 1,
		},
		{
			name:         "another skill's tool names its own skill",
			kind:         "agent",
			entries:      builtinEntry("docx_create_document"),
			hasToolset:   bindable,
			wantErrFor:   "docx_create_document",
			wantErrWords: []string{"sam-docx"},
			wantWarnings: 1,
		},
		{
			name:         "an unknown name falls back to the group advice",
			kind:         "agent",
			entries:      builtinEntry("no_such_tool"),
			hasToolset:   bindable,
			wantErrFor:   "no_such_tool",
			wantErrWords: []string{"builtin-group"},
			wantWarnings: 1,
		},
		{
			name:         "a bound tool beside an unbound one only rejects the unbound",
			kind:         "agent",
			entries:      builtinEntry("web_request") + builtinEntry("xlsx_read"),
			hasToolset:   bindable,
			wantErrFor:   "xlsx_read",
			wantWarnings: 1,
		},
		{
			name:         "a group entry is not judged by tool name",
			kind:         "agent",
			entries:      "        - tool_type: builtin-group\n          group_name: data_analysis\n",
			hasToolset:   bindable,
			wantValid:    true,
			wantWarnings: 2,
		},
		{
			name:         "an mcp entry is not judged",
			kind:         "agent",
			entries:      "        - tool_type: mcp\n          connection_params:\n            type: stdio\n            command: x\n",
			hasToolset:   bindable,
			wantValid:    true,
			wantWarnings: 2,
		},
		{
			name:         "a workflow keeps its skill tool declarations",
			kind:         "workflow",
			entries:      builtinEntry("sam-excel__xlsx_read"),
			hasToolset:   bindable,
			wantValid:    true,
			wantWarnings: 2,
		},
		{
			name:         "without an oracle nothing is judged",
			kind:         "agent",
			entries:      builtinEntry("execute_sql_query"),
			hasToolset:   nil,
			wantValid:    true,
			wantWarnings: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resultFor(configWithTools(tt.kind, tt.entries), tt.hasToolset)

			if result.Valid != tt.wantValid {
				t.Errorf("Valid = %v, want %v", result.Valid, tt.wantValid)
			}
			if tt.wantErrFor == "" {
				if len(result.Errors) != 0 {
					t.Fatalf("unexpected errors: %+v", result.Errors)
				}
			} else {
				if len(result.Errors) != 1 {
					t.Fatalf("errors = %+v, want exactly one", result.Errors)
				}
				msg := result.Errors[0].Message
				if !strings.Contains(msg, `"`+tt.wantErrFor+`"`) {
					t.Errorf("error %q should name %s", msg, tt.wantErrFor)
				}
				for _, word := range tt.wantErrWords {
					if !strings.Contains(msg, word) {
						t.Errorf("error %q should mention %q", msg, word)
					}
				}
			}
			if len(result.Warnings) != tt.wantWarnings {
				t.Errorf("warnings = %+v, want %d left", result.Warnings, tt.wantWarnings)
			}
		})
	}
}

func TestValidateComponentConfig_RejectsATool_NoToolsetProvides(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, minimalManifestYAML, SAMBuildManifestMIMEType)
	validator := &ValidateComponentConfigTool{ToolHasToolset: bindableOnly("web_request")}
	config := configWithTools("agent", builtinEntry("web_request")+builtinEntry("execute_sql_query")+builtinEntry("xlsx_read"))

	res, err := validator.Execute(t.Context(), map[string]any{"config_yaml": config, "mime_type": SAMAgentConfigMIMEType}, toolCtx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %q: %s", res.Status, res.Message)
	}
	if res.Data["valid"] != false {
		t.Fatalf("valid = %v, want false so the sub-task fixes it before the save does", res.Data["valid"])
	}
	errs, _ := res.Data["errors"].([]schema.ValidationError)
	if len(errs) != 2 {
		t.Fatalf("errors = %+v, want one per unbindable tool", errs)
	}
	if !strings.Contains(errs[0].Message, "execute_sql_query") || !strings.Contains(errs[1].Message, "xlsx_read") {
		t.Errorf("errors do not name the tools in order: %+v", errs)
	}
}

func TestReconcileToolBinding_JudgesEachAppByItsOwnKind(t *testing.T) {
	agent := "  - name: Sales\n    app_config:\n      agent_name: Sales\n      description: Analyses sales data for the retail business.\n      instruction: do it\n      tools:\n        - tool_type: builtin\n          tool_name: %s\n"
	workflow := "  - name: Flow\n    app_config:\n      agent_type: workflow\n      agent_name: Flow\n      tools:\n        - tool_type: builtin\n          tool_name: sam-excel__xlsx_read\n"
	tests := []struct {
		name       string
		config     string
		wantErrors int
	}{
		{"agent first, workflow second: the workflow's skill tool stays", "apps:\n" + fmt.Sprintf(agent, "web_request") + workflow, 0},
		{"workflow first, agent second: the agent is still judged", "apps:\n" + workflow + fmt.Sprintf(agent, "execute_sql_query"), 1},
		{"agent first with an unbindable tool, workflow second", "apps:\n" + fmt.Sprintf(agent, "xlsx_read") + workflow, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &schema.ValidationResult{Valid: true, ComponentType: "workflow"}

			reconcileToolBinding(result, []byte(tt.config), bindableOnly("web_request"))

			if len(result.Errors) != tt.wantErrors {
				t.Errorf("errors = %+v, want %d", result.Errors, tt.wantErrors)
			}
			if result.Valid != (tt.wantErrors == 0) {
				t.Errorf("Valid = %v with %d errors", result.Valid, len(result.Errors))
			}
		})
	}
}
