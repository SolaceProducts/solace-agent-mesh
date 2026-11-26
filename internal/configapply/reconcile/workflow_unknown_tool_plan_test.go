// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func pinnedMCPEntry() map[string]any {
	return map[string]any{
		"tool_type":         "mcp",
		"tool_name":         "get_forecast",
		"connection_params": map[string]any{"type": "streamable-http", "url": "http://localhost:9999/mcp"},
	}
}

func weatherNode(tool string) map[string]any {
	return map[string]any{"id": "weather", "type": "tool", "tool_name": tool, "input": map[string]any{"location": "Boston"}}
}

func unknownToolSpec(tool string) map[string]any {
	return toolNodeWorkflow([]any{pinnedMCPEntry()}, weatherNode(tool))
}

func TestPlanWorkflows_RefusesAToolNodeNoPinnedSourceRegisters(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedWorkflow("weather", unknownToolSpec("get_weather"))}

	t.Run("create is fatal and lists the registered names", func(t *testing.T) {
		_, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
		if err == nil {
			t.Fatal("a workflow whose tool node can never run must fail the plan")
		}
		for _, want := range []string{`workflow "weather"`, `"get_weather"`, "references unknown tool", "the tools it registers are: get_forecast"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q is missing %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "older than the platform") {
			t.Errorf("error %q offers the CLI-version advice, which does not apply to a name the workflow never declares", err)
		}
	})

	t.Run("an unchanged workflow only warns", func(t *testing.T) {
		plan, err := planWorkflows(desired, []wire.WorkflowResponse{
			actualWorkflow("id-1", "weather", unknownToolSpec("get_weather")),
		}, translate.WorkflowHooks{})
		if err != nil {
			t.Fatalf("an unchanged workflow must not fail the plan: %v", err)
		}
		if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "get_weather") || !strings.Contains(plan.Warnings[0], "not changing this workflow") {
			t.Fatalf("warnings = %v, want one advisory naming the tool", plan.Warnings)
		}
	})

	t.Run("the permissive flag does not downgrade it, because the platform refuses regardless", func(t *testing.T) {
		t.Setenv("SAM_FEATURE_WORKFLOW_TOOL_VALIDATION_PERMISSIVE", "true")
		if _, err := planWorkflows(desired, nil, translate.WorkflowHooks{}); err == nil {
			t.Fatal("a name no source registers must still fail the plan under the permissive flag: apply would fail 422")
		}
	})
}

func TestPlanWorkflows_AnUnpinnedMCPEntryIsAdvisedNotRefused(t *testing.T) {
	unpinned := map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "streamable-http", "url": "http://localhost:9999/mcp"},
	}
	spec := toolNodeWorkflow([]any{unpinned}, weatherNode("get_weather"))
	for name, permissive := range map[string]string{"strict": "", "permissive": "true"} {
		t.Run(name, func(t *testing.T) {
			if permissive != "" {
				t.Setenv("SAM_FEATURE_WORKFLOW_TOOL_VALIDATION_PERMISSIVE", permissive)
			}
			plan, err := planOne(t, "weather", spec)
			if err != nil {
				t.Fatalf("a name the plan cannot judge must not fail it: %v", err)
			}
			if len(plan.Warnings) != 1 {
				t.Fatalf("warnings = %v, want exactly the one note", plan.Warnings)
			}
			for _, want := range []string{`workflow "weather"`, `"get_weather"`, "not checked", "allow_list or an inline manifest"} {
				if !strings.Contains(plan.Warnings[0], want) {
					t.Errorf("warning %q is missing %q", plan.Warnings[0], want)
				}
			}
		})
	}
}

func TestPlanWorkflows_AcceptsAToolNodeAPinnedSourceRegisters(t *testing.T) {
	plan, err := planOne(t, "weather", unknownToolSpec("get_forecast"))
	if err != nil {
		t.Fatalf("a registered tool must plan: %v", err)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", plan.Warnings)
	}
}
