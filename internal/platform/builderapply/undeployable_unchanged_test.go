// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const shipmentAndPingSpec = shipmentSpec + `  /ping:
    get:
      operationId: ping
      responses:
        "200":
          description: ok
`

func petstoreWithAllowList(allowList string) BuildComponentInput {
	return apiConnectorComponent("petstore", apiConnectorValues(map[string]any{"specification_url": unreachableSpecURL, "allow_list": allowList}))
}

func seedPetstoreAndWorkflow(t *testing.T, svc *Service) BuildComponentInput {
	t.Helper()
	workflow := workflowCallingConnector("ship_flow", "petstore", "listPets", "")
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{petstoreWithAllowList("listPets"), workflow}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("seed the connector and its workflow: %v %+v", err, resp)
	}
	return workflow
}

func TestApply_WarnsAboutAnUnchangedWorkflowTheBuildsConnectorChangeWouldBreak(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		connector BuildComponentInput
		want      string
	}{
		{"the allow_list is narrowed away from the node's tool", petstoreWithAllowList("getPet"), `emits no tool named "listPets"`},
		{"the connector loses its allow_list", petstoreWithAllowList(""), "set allow_list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			workflow := seedPetstoreAndWorkflow(t, svc)
			dry, applied := applyBoth(t, svc, tc.connector, workflow)
			for _, resp := range []*ApplyBuildResponse{dry, applied} {
				op := opFor(t, resp, kindWorkflow, "ship_flow")
				if op.Action != actionUnchanged || op.Status == statusFailed {
					t.Fatalf("dryRun=%v: workflow = %+v, want it unchanged and accepted: this build did not break it", resp.DryRun, op)
				}
				if !mentions(op.Warnings, tc.want) || !mentions(op.Warnings, "was not changed by this build") || !mentions(op.Warnings, "when it is next deployed") {
					t.Errorf("dryRun=%v: warnings = %v, want one saying the workflow was not changed and what fails at its next deploy (%q)", resp.DryRun, op.Warnings, tc.want)
				}
			}
			if applied.Summary.Failed != 0 {
				t.Fatalf("apply failed: %+v", applied.Operations)
			}
			if got := opFor(t, applied, kindConnector, "petstore").Status; got != statusSucceeded {
				t.Errorf("connector = %q, want succeeded: a problem on an unchanged workflow must not refuse the connector's change", got)
			}
		})
	}
}

func TestApply_LeavesAnUnchangedWorkflowAloneWhenNoConnectorItCallsChanges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	workflow := seedPetstoreAndWorkflow(t, svc)

	repo := store.NewConnectorRepo(svc.deps.DB)
	rows, err := repo.GetAllByName(t.Context(), "petstore")
	if err != nil || len(rows) != 1 {
		t.Fatalf("find the stored connector: %v %v", rows, err)
	}
	narrowed := `{"base_url":"https://api.example.com","auth_type":"none","specification_url":"` + unreachableSpecURL + `","allow_list":"getPet"}`
	rows[0].Values = &narrowed
	if err := repo.Update(t.Context(), rows[0]); err != nil {
		t.Fatalf("narrow the stored connector behind the workflow's back: %v", err)
	}

	dry, applied := applyBoth(t, svc, connectorComponent("other-db"), workflow)
	for _, resp := range []*ApplyBuildResponse{dry, applied} {
		if op := opFor(t, resp, kindWorkflow, "ship_flow"); op.Action != actionUnchanged || op.Status == statusFailed {
			t.Errorf("dryRun=%v: workflow = %+v, want it left as it is", resp.DryRun, op)
		}
	}
	if applied.Summary.Failed != 0 {
		t.Fatalf("apply failed: %+v", applied.Operations)
	}
}

func TestApply_GradesTheValuesTheBuildWritesForAStoredConnector(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedStoredSpecConnector(t, svc, "carrier-api", shipmentAndPingSpec, map[string]any{"allow_list": "ping"})

	_, applied := requireRefusedBeforeWriting(t, svc, "tracking",
		apiConnectorComponent("carrier-api", apiConnectorValues(map[string]any{"allow_list": "getShipment,ping"})),
		workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", shipInput("tracking_number")))
	if message := opFor(t, applied, kindWorkflow, "ship_flow").Error.Message; strings.Contains(message, "emits no tool named") {
		t.Errorf("the node was graded against the stored allow_list, not the build's: %q", message)
	}
}

func TestApply_AcceptsAStoredWorkflowWhoseBoundConnectorWasRenamedAndIsUpdatedWithIt(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedStoredSpecConnector(t, svc, "carrier-api", shipmentSpec, nil)
	saved, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", shipInput("tracking")),
	}}, testActor)
	if err != nil || saved.Summary.Failed != 0 {
		t.Fatalf("save the workflow: %v %+v", err, saved)
	}
	renameStoredConnector(t, svc, "carrier-api", "carrier-api-renamed")

	connector := apiConnectorComponent("carrier-api-renamed", apiConnectorValues(map[string]any{"allow_list": "getShipment"}))
	edited := workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", "            input:\n              tracking: \"literal\"\n")
	dry, applied := applyBoth(t, svc, connector, edited)
	for _, resp := range []*ApplyBuildResponse{dry, applied} {
		for _, op := range []BuildOperationResult{opFor(t, resp, kindConnector, "carrier-api-renamed"), opFor(t, resp, kindWorkflow, "ship_flow")} {
			if op.Action != actionUpdate || op.Status == statusFailed {
				t.Errorf("dryRun=%v: %s %q = %+v, want an accepted update: the workflow's binding survives the rename", resp.DryRun, op.Kind, op.Name, op)
			}
		}
	}
	if applied.Summary.Failed != 0 {
		t.Fatalf("apply failed: %+v", applied.Operations)
	}
}

func TestApply_AnUnchangedWorkflowCarriesTheUncheckedInputsWarningOfAConnectorTheBuildChanges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	workflow := seedPetstoreAndWorkflow(t, svc)

	dry, applied := applyBoth(t, svc, petstoreWithAllowList("listPets,getPet"), workflow)

	for _, resp := range []*ApplyBuildResponse{dry, applied} {
		op := opFor(t, resp, kindWorkflow, "ship_flow")
		if op.Action != actionUnchanged || op.Status == statusFailed || !mentions(op.Warnings, `connector "petstore" could not be checked: it has no stored specification`) {
			t.Errorf("dryRun=%v: workflow = %+v, want it unchanged and accepted with the unchecked-inputs warning", resp.DryRun, op)
		}
	}
	if applied.Summary.Failed != 0 {
		t.Fatalf("apply failed: %+v", applied.Operations)
	}
}
