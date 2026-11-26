// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolbind"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const parityAgentYAML = `apps:
  - name: Sales
    app_config:
      agent_name: Sales
      description: Analyses sales data for the retail business.
      instruction: You analyse sales data and report on it clearly.
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

func parityNames() []string {
	names := map[string]struct{}{"execute_sql_query": {}, "xlsx_read": {}, "xlsx_write": {}, "docx_create_document": {}, "sam-excel__xlsx_read": {}, "no_such_tool": {}, "notify_user": {}, "RedirectUser": {}}
	paddedName := " web_request"
	names[paddedName] = struct{}{}
	for binary := range toolbind.ConnectorRuntimes {
		names[binary] = struct{}{}
	}
	for name := range schema.ValidBuiltinToolNames {
		names[name] = struct{}{}
	}
	for group := range schema.ValidBuiltinGroups {
		for _, member := range toolinit.LookupBuiltinGroup(group, toolinit.Options{}) {
			names[member] = struct{}{}
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func TestValidatorAndSaveAgreeOnEveryToolName(t *testing.T) {
	t.Parallel()
	validator := toolinit.LookupBuiltinTool("validate_component_config", nil, toolinit.Options{})
	if validator == nil {
		t.Fatal("validate_component_config is not registered")
	}
	for _, name := range parityNames() {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%v", name, existing), func(t *testing.T) {
				t.Parallel()
				config := fmt.Sprintf(parityAgentYAML, name)
				res, err := validator.Execute(t.Context(), map[string]any{"config_yaml": config}, &tool.Context{})
				if err != nil {
					t.Fatalf("validator: %v", err)
				}
				valid, _ := res.Data["valid"].(bool)

				svc := newTestService(t)
				if existing {
					seed, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("Sales")}}, testActor)
					if err != nil {
						t.Fatalf("seed: %v", err)
					}
					requireOneAgentOperation(t, seed, actionCreate)
				}
				request := ApplyBuildRequest{Components: []BuildComponentInput{{Kind: kindAgent, Name: "Sales", Content: config}}}
				_, _, planErr := svc.Plan(t.Context(), request, testActor)
				if planErr == nil {
					applied, applyErr := svc.Apply(t.Context(), request, testActor)
					if applyErr != nil {
						t.Fatalf("Apply: %v", applyErr)
					}
					wantAction := actionCreate
					if existing {
						wantAction = actionUpdate
					}
					requireOneAgentOperation(t, applied, wantAction)
				}

				if valid != (planErr == nil) {
					t.Errorf("validator says valid=%v (%s), the save plans with error %v", valid, res.Message, planErr)
				}
				if planErr != nil && !strings.Contains(planErr.Error(), `"`+name+`"`) {
					t.Errorf("the save's refusal does not name %q: %v", name, planErr)
				}
			})
		}
	}
}

func requireOneAgentOperation(t *testing.T, resp *ApplyBuildResponse, wantAction string) {
	t.Helper()
	if len(resp.Operations) != 1 {
		t.Fatalf("want one operation, got %d: %+v", len(resp.Operations), resp.Operations)
	}
	op := resp.Operations[0]
	if op.Kind != kindAgent || op.Action != wantAction || op.Status != statusSucceeded {
		t.Fatalf("operation = %s %s %s (error %+v), want %s %s %s", op.Kind, op.Action, op.Status, op.Error, kindAgent, wantAction, statusSucceeded)
	}
}
