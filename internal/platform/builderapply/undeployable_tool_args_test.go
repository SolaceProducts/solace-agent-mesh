// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	badFetchInput      = "              url: \"https://example.com\"\n              output_filename: x.jpg\n"
	goodFetchInput     = "              url: \"https://example.com\"\n"
	producedFetchField = "{{fetch.output}}"
	missingFetchField  = "{{fetch.output.no_such_field}}"
)

func workflowFetching(name, input, output string) BuildComponentInput {
	c := workflowComponent(name, name, "test_agent")
	c.Content = strings.Replace(c.Content, "          result: \"{{step.output}}\"\n", "          result: \""+output+"\"\n", 1)
	c.Content = strings.Replace(c.Content, "        nodes:\n", "        tools:\n          - tool_type: builtin\n            tool_name: web_request\n        nodes:\n", 1)
	c.Content = strings.Replace(c.Content,
		"          - id: step\n            type: agent\n            agent_name: test_agent\n            input:\n              text: \"{{workflow.input.text}}\"\n",
		"          - id: fetch\n            type: tool\n            tool_name: web_request\n            input:\n"+input, 1)
	return c
}

func requireWarningNaming(t *testing.T, resp *ApplyBuildResponse, workflowName, want string) {
	t.Helper()
	op := opFor(t, resp, kindWorkflow, workflowName)
	if op.Status == statusFailed || !slices.ContainsFunc(op.Warnings, func(w string) bool { return strings.Contains(w, want) }) {
		t.Fatalf("dryRun=%v: workflow = %+v, want accepted with a warning naming %q", resp.DryRun, op, want)
	}
}

func TestApply_RefusesABuiltinToolNodeArgumentTheToolRejects(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	before := platformSnapshot(t, svc)
	dry, applied := applyBoth(t, svc, agentComponent("test_agent"), workflowFetching("fetch_flow", badFetchInput, producedFetchField))
	requireSameFailure(t, opFor(t, dry, kindWorkflow, "fetch_flow"), opFor(t, applied, kindWorkflow, "fetch_flow"), "output_filename")
	if after := platformSnapshot(t, svc); after != before {
		t.Fatal("a build refused for a tool node argument wrote to the platform")
	}
}

func TestApply_AnUnproducibleOutputReferenceIsAWarning(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	dry, applied := applyBoth(t, svc, agentComponent("test_agent"), workflowFetching("fetch_flow", goodFetchInput, missingFetchField))
	requireWarningNaming(t, dry, "fetch_flow", "no_such_field")
	requireWarningNaming(t, applied, "fetch_flow", "no_such_field")
	stored := workflowNamed(t, svc, "fetch_flow")
	if stored.Name == "" {
		t.Fatal("the workflow was not saved")
	}
}

func TestApply_PermissiveToolValidationMakesABadArgumentAWarning(t *testing.T) {
	t.Setenv("SAM_FEATURE_WORKFLOW_TOOL_VALIDATION_PERMISSIVE", "true")
	svc := newTestService(t)
	svc.features = features.NewRegistry()
	if err := features.LoadFlags(svc.features); err != nil {
		t.Fatalf("load feature catalog: %v", err)
	}
	dry, applied := applyBoth(t, svc, agentComponent("test_agent"), workflowFetching("fetch_flow", badFetchInput, producedFetchField))
	requireWarningNaming(t, dry, "fetch_flow", "output_filename")
	requireWarningNaming(t, applied, "fetch_flow", "output_filename")
}

const removedEscalationChoiceProperties = `            depends_on: []
            questions:
              department:
                type: choice
                instructions: Which team owns it?
                criteria:
                  billing: Payments
                  tech: Bugs
            route_on: department
            routes:
              billing: step
              tech: step
            escalate: step
            escalation_choice: "{{step.output.department}}"
`

func workflowWithRemovedEscalationChoice(name, targetAgent string) BuildComponentInput {
	c := workflowWithDecisionAndAlias(name, targetAgent)
	c.Content = strings.Replace(c.Content, "            depends_on: []\n", removedEscalationChoiceProperties, 1)
	return c
}

func workflowWithUnparseableNodes(name string) BuildComponentInput {
	c := workflowComponent(name, name, "test_agent")
	body, _, _ := strings.Cut(c.Content, "        nodes:\n")
	c.Content = body + "        nodes: not-a-list\n"
	return c
}

func TestApply_RefusesADecisionNodeThatSetsTheRemovedEscalationChoice(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedModel(t, svc, "judge", store.ModelTypeDecision)
	before := platformSnapshot(t, svc)
	dry, applied := applyBoth(t, svc, agentComponent("test_agent"), workflowWithRemovedEscalationChoice("triage_flow", "test_agent"))
	requireSameFailure(t, opFor(t, dry, kindWorkflow, "triage_flow"), opFor(t, applied, kindWorkflow, "triage_flow"), "escalation_choice was replaced by escalation_mode")
	if after := platformSnapshot(t, svc); after != before {
		t.Fatal("a build refused for the removed escalation_choice key wrote to the platform")
	}
}

func TestApply_AWorkflowBodyThatDoesNotParseIsAcceptedWithASkippedChecksWarning(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	dry, applied := applyBoth(t, svc, agentComponent("test_agent"), workflowWithUnparseableNodes("broken_flow"))
	requireWarningNaming(t, dry, "broken_flow", "tool-node checks were skipped")
	requireWarningNaming(t, applied, "broken_flow", "tool-node checks were skipped")
}
