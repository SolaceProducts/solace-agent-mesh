// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const connectorNodeChecksPermissiveEnv = "SAM_FEATURE_BUILDER_CONNECTOR_NODE_CHECKS_PERMISSIVE"

func withCatalogFlags(t *testing.T, svc *Service) {
	t.Helper()
	svc.features = features.NewRegistry()
	if err := features.LoadFlags(svc.features); err != nil {
		t.Fatalf("load feature catalog: %v", err)
	}
}

func TestApply_TheRefusalNamesTheFlagThatDowngradesIt(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	withCatalogFlags(t, svc)
	seedStoredURLOnlyConnector(t, svc, "petstore")
	requireRefusedBeforeWriting(t, svc, connectorNodeChecksPermissiveEnv+"=true",
		workflowCallingConnector("ship_flow", "petstore", "listPets", ""))
}

func TestApply_PermissiveConnectorNodeChecksMakeTheDeployRefusalsWarnings(t *testing.T) {
	t.Setenv(connectorNodeChecksPermissiveEnv, "true")

	cases := []struct {
		name string
		want string
		tool string
		set  map[string]any
	}{
		{"a spec-URL-only connector", "set allow_list", "listPets", map[string]any{"specification_url": unreachableSpecURL}},
		{"a selector outside the allow_list", `emits no tool named "getPet"`, "getPet", map[string]any{"specification_url": unreachableSpecURL, "allow_list": "listPets"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			withCatalogFlags(t, svc)
			dry, applied := applyBoth(t, svc,
				apiConnectorComponent("petstore", apiConnectorValues(tc.set)),
				workflowCallingConnector("ship_flow", "petstore", tc.tool, ""))
			for _, resp := range []*ApplyBuildResponse{dry, applied} {
				op := opFor(t, resp, kindWorkflow, "ship_flow")
				if op.Status == statusFailed || !mentions(op.Warnings, tc.want) {
					t.Errorf("dryRun=%v: workflow = %+v, want accepted with a warning naming %q", resp.DryRun, op, tc.want)
				}
				if mentions(op.Warnings, "SAM_FEATURE_") {
					t.Errorf("dryRun=%v: the warning advertises the flag that is already on: %v", resp.DryRun, op.Warnings)
				}
			}
			if applied.Summary.Failed != 0 {
				t.Fatalf("apply failed: %+v", applied.Operations)
			}
		})
	}
}

func TestApply_PermissiveConnectorNodeChecksStillRefuseAConnectorThatDoesNotExist(t *testing.T) {
	t.Setenv(connectorNodeChecksPermissiveEnv, "true")
	svc := newTestService(t)
	withCatalogFlags(t, svc)
	_, applied := requireRefusedBeforeWriting(t, svc, `connector "ghost" not found`,
		agentComponent("good_agent"), workflowCallingConnector("ship_flow", "ghost", "anything", ""))
	if message := opFor(t, applied, kindWorkflow, "ship_flow").Error.Message; strings.Contains(message, "SAM_FEATURE_") {
		t.Errorf("the refusal offers a flag that would not clear it: %q", message)
	}
}

func TestApply_PermissiveConnectorNodeChecksKeepTheInputChecksFatal(t *testing.T) {
	t.Setenv(connectorNodeChecksPermissiveEnv, "true")
	svc := newTestService(t)
	withCatalogFlags(t, svc)
	seedStoredShipmentConnector(t, svc, "carrier-api")
	_, applied := applyBoth(t, svc, agentComponent("good_agent"),
		workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", shipInput("tracking_number")))
	op := opFor(t, applied, kindWorkflow, "ship_flow")
	if op.Status != statusFailed || op.Error == nil || !strings.Contains(op.Error.Message, "tracking") {
		t.Fatalf("workflow = %+v, want the input check still refusing: the flag covers only the deploy-time refusals", op)
	}
}

func requireUnchangedWorkflowFramedAsAWarning(t *testing.T, svc *Service) {
	t.Helper()
	workflow := seedPetstoreAndWorkflow(t, svc)
	dry, applied := applyBoth(t, svc, petstoreWithAllowList(""), workflow)
	for _, resp := range []*ApplyBuildResponse{dry, applied} {
		op := opFor(t, resp, kindWorkflow, "ship_flow")
		if op.Status == statusFailed || !mentions(op.Warnings, "set allow_list") || !mentions(op.Warnings, "was not changed by this build") || !mentions(op.Warnings, "when it is next deployed") {
			t.Fatalf("dryRun=%v: workflow = %+v, want a warning saying it was not changed and what fails at its next deploy", resp.DryRun, op)
		}
		if mentions(op.Warnings, "SAM_FEATURE_") {
			t.Errorf("dryRun=%v: a warning offers a flag to downgrade itself: %v", resp.DryRun, op.Warnings)
		}
	}
}

func TestApply_AnUnchangedWorkflowsWarningIsFramedWithTheFlagOff(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	withCatalogFlags(t, svc)
	requireUnchangedWorkflowFramedAsAWarning(t, svc)
}

func TestApply_AnUnchangedWorkflowsWarningIsFramedWithTheFlagOn(t *testing.T) {
	t.Setenv(connectorNodeChecksPermissiveEnv, "true")
	svc := newTestService(t)
	withCatalogFlags(t, svc)
	requireUnchangedWorkflowFramedAsAWarning(t, svc)
}

func workflowAlsoCallingAMissingConnector() BuildComponentInput {
	workflow := workflowCallingConnector("ship_flow", "petstore", "listPets", "")
	workflow.Content += "          - id: ghost_step\n            type: tool\n            connector: ghost\n            tool: anything\n"
	return workflow
}

func TestApply_AMissingConnectorDoesNotHideAnotherNodesDeployProblemWithTheFlagOff(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	withCatalogFlags(t, svc)
	seedStoredURLOnlyConnector(t, svc, "petstore")
	requireRefusedBeforeWriting(t, svc, `connector "ghost" not found`, workflowAlsoCallingAMissingConnector())
	_, applied := applyBoth(t, svc, workflowAlsoCallingAMissingConnector())
	if message := opFor(t, applied, kindWorkflow, "ship_flow").Error.Message; !strings.Contains(message, "set allow_list") || !strings.Contains(message, connectorNodeChecksPermissiveEnv) {
		t.Errorf("refusal = %q, want it to report the spec-URL problem too, with the flag", message)
	}
}

func TestApply_AMissingConnectorStillRefusesAndTheOtherDeployProblemIsAWarningWithTheFlagOn(t *testing.T) {
	t.Setenv(connectorNodeChecksPermissiveEnv, "true")
	svc := newTestService(t)
	withCatalogFlags(t, svc)
	seedStoredURLOnlyConnector(t, svc, "petstore")
	_, applied := requireRefusedBeforeWriting(t, svc, `connector "ghost" not found`, workflowAlsoCallingAMissingConnector())
	op := opFor(t, applied, kindWorkflow, "ship_flow")
	if strings.Contains(op.Error.Message, "set allow_list") || !mentions(op.Warnings, "set allow_list") {
		t.Errorf("workflow = %+v, want the spec-URL problem as a warning beside the refusal", op)
	}
}
