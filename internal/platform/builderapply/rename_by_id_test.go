// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func seedAgent(t *testing.T, svc *Service, name string) entplatform.AgentResponse {
	t.Helper()
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent(name)}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("seed %q: %v %+v", name, err, resp)
	}
	return agentNamed(t.Context(), t, svc, name)
}

func withResourceID(c BuildComponentInput, id string) BuildComponentInput {
	c.ResourceID = id
	return c
}

func storedAgentNames(t *testing.T, svc *Service) []string {
	t.Helper()
	list, err := svc.deps.listAgents(t.Context())
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.Name)
	}
	slices.Sort(names)
	return names
}

func TestApply_RenameByResourceIDUpdatesTheStoredAgentInPlace(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	old := seedAgent(t, svc, "refund_orchestrator")

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		withResourceID(agentComponent("refund_handler"), old.ID),
	}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("rename: %v %+v", err, resp)
	}

	op := opFor(t, resp, kindAgent, "refund_handler")
	if op.Action != actionUpdate || op.Status != statusSucceeded || op.ResourceID != old.ID {
		t.Fatalf("op = %+v, want a succeeded update of the stored agent %s", op, old.ID)
	}
	if !slices.Contains(op.ChangedFields, "name") {
		t.Errorf("changedFields = %v, want name among them", op.ChangedFields)
	}
	if !mentions(op.Warnings, "renames \"refund_orchestrator\" to \"refund_handler\" in place") {
		t.Errorf("warnings = %v, want the in-place rename stated", op.Warnings)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"refund_handler"}) {
		t.Fatalf("agents = %v, want the one renamed agent and no second agent", got)
	}
	if renamed := agentNamed(t.Context(), t, svc, "refund_handler"); renamed.ID != old.ID {
		t.Errorf("renamed agent id = %s, want the original %s", renamed.ID, old.ID)
	}
}

func TestApply_RenameWithoutResourceIDStillCreatesASecondAgent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedAgent(t, svc, "refund_orchestrator")

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("refund_handler")}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("apply: %v %+v", err, resp)
	}
	if op := opFor(t, resp, kindAgent, "refund_handler"); op.Action != actionCreate {
		t.Errorf("action = %q, want create: without the id the endpoint cannot tell a rename from a new agent", op.Action)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"refund_handler", "refund_orchestrator"}) {
		t.Errorf("agents = %v, want both", got)
	}
}

func TestDryRun_ShowsTheRenameBeforeItIsApplied(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	old := seedAgent(t, svc, "refund_orchestrator")

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
		withResourceID(agentComponent("refund_handler"), old.ID),
	}}, testActor)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	op := opFor(t, resp, kindAgent, "refund_handler")
	if op.Action != actionUpdate || op.Status != statusPlanned || op.ResourceID != old.ID {
		t.Errorf("op = %+v, want a planned update of %s", op, old.ID)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"refund_orchestrator"}) {
		t.Errorf("agents = %v, a dry run must write nothing", got)
	}
}

func TestApply_AStoredResourceCarryingTheNameWinsOverTheID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	stale := seedAgent(t, svc, "refund_orchestrator")
	holder := seedAgent(t, svc, "refund_handler")

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		withResourceID(agentComponent("refund_handler"), stale.ID),
	}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("apply: %v %+v", err, resp)
	}
	if op := opFor(t, resp, kindAgent, "refund_handler"); op.ResourceID != holder.ID {
		t.Errorf("updated %s, want the agent that carries the name (%s)", op.ResourceID, holder.ID)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"refund_handler", "refund_orchestrator"}) {
		t.Errorf("agents = %v, want the stale one left alone", got)
	}
}

func TestApply_AnIDThatNamesNothingOfTheKindIsIgnored(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedAgent(t, svc, "refund_orchestrator")
	if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "refund_orchestrator")}}, testActor); err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("seed workflow: %v %+v", err, resp)
	}
	workflow := workflowNamed(t, svc, "OrderFlow")

	for name, id := range map[string]string{
		"an id nothing holds":                "01a15453-e34d-7330-a292-50320e36ef02",
		"the id of a workflow":               workflow.ID,
		"the id of a workflow in upper case": strings.ToUpper(workflow.ID),
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
				withResourceID(agentComponent("refund_handler"), id),
			}}, testActor)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if op := opFor(t, resp, kindAgent, "refund_handler"); op.Action != actionCreate {
				t.Errorf("action = %q, want create", op.Action)
			}
		})
	}
}

func TestApply_TheIDMatchesWhateverCaseTheManifestSpellsIt(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	old := seedAgent(t, svc, "refund_orchestrator")

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
		withResourceID(agentComponent("refund_handler"), strings.ToUpper(old.ID)),
	}}, testActor)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "refund_handler"); op.Action != actionUpdate || op.ResourceID != old.ID {
		t.Errorf("op = %+v, want an update of %s", op, old.ID)
	}
}

func TestPlan_TwoComponentsCarryingOneIDAreRefused(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	old := seedAgent(t, svc, "refund_orchestrator")

	_, _, err := svc.Plan(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		withResourceID(agentComponent("refund_handler"), old.ID),
		withResourceID(agentComponent("shipping_handler"), old.ID),
	}}, testActor)

	ve, ok := errors.AsType[*entplatform.ValidationError](err)
	if !ok || !strings.Contains(ve.Message, "carry the same resourceId") {
		t.Fatalf("err = %v, want a validation error naming the shared resourceId", err)
	}
}

func TestApply_RenameByResourceIDWorksForAWorkflow(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedAgent(t, svc, "refund_orchestrator")
	if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "refund_orchestrator")}}, testActor); err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("seed workflow: %v %+v", err, resp)
	}
	old := workflowNamed(t, svc, "OrderFlow")

	renamed := withResourceID(workflowComponent("order_pipeline", "OrderPipeline", "refund_orchestrator"), old.ID)
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{renamed}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("rename: %v %+v", err, resp)
	}
	if op := opFor(t, resp, kindWorkflow, "order_pipeline"); op.Action != actionUpdate || op.ResourceID != old.ID {
		t.Fatalf("op = %+v, want an update of workflow %s", op, old.ID)
	}
	list, err := svc.deps.listWorkflows(t.Context())
	if err != nil || len(list) != 1 || list[0].Name != "OrderPipeline" || list[0].ID != old.ID {
		t.Fatalf("workflows = %+v, %v; want the one renamed workflow with its id", list, err)
	}
}

func TestPlan_OnlyAgentsAndWorkflowsAreRenamedByID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{connectorComponent("orders_db"), a2aComponent("edited_remote", nil), gatewayComponent("order_events", "Fulfilment")}}, testActor); err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("seed: %v %+v", err, resp)
	}
	connectors, err := svc.deps.listConnectors(t.Context())
	if err != nil || len(connectors) != 1 {
		t.Fatalf("connectors = %+v, %v", connectors, err)
	}
	remotes, err := svc.deps.listA2AAgents(t.Context())
	if err != nil || len(remotes) != 1 {
		t.Fatalf("remote agents = %+v, %v", remotes, err)
	}
	gateways, err := svc.deps.listGateways(t.Context())
	if err != nil || len(gateways) != 1 {
		t.Fatalf("entrypoints = %+v, %v", gateways, err)
	}

	plans, _, err := svc.Plan(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		withResourceID(connectorComponent("orders_store"), connectors[0].ID),
		withResourceID(a2aComponent("renamed_remote", nil), remotes[0].ID),
		withResourceID(gatewayComponent("order_stream", "Fulfilment"), gateways[0].ID),
	}}, testActor)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, c := range []struct{ kind, name string }{{kindConnector, "orders_store"}, {kindA2AAgent, "renamed_remote"}, {kindGateway, "order_stream"}} {
		if op := findOp(t, plans, c.kind, c.name); op.Action != actionCreate {
			t.Errorf("%s %q: action = %q, want create: the id decides only for an agent or a workflow", c.kind, c.name, op.Action)
		}
	}
}

func TestApply_ARenamedAgentAndANewOneTakingItsOldNameEachGetTheirOwn(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	first := seedAgent(t, svc, "refund_orchestrator")

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		withResourceID(agentComponent("refund_handler"), first.ID),
		agentComponent("refund_orchestrator"),
	}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("apply: %v %+v", err, resp)
	}
	if op := opFor(t, resp, kindAgent, "refund_orchestrator"); op.ResourceID != first.ID {
		t.Errorf("the name's holder was %s, want it kept by the component that still carries the name (%s)", op.ResourceID, first.ID)
	}
	if op := opFor(t, resp, kindAgent, "refund_handler"); op.Action != actionCreate {
		t.Errorf("action = %q, want create: its id names the resource the other component holds by name", op.Action)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"refund_handler", "refund_orchestrator"}) {
		t.Errorf("agents = %v, want both", got)
	}
}

func TestPostBuild_RenamesTheStoredAgentFromTheWireShape(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	old := seedAgent(t, svc, "refund_orchestrator")
	body, err := json.Marshal(map[string]any{
		"buildName": "refunds",
		"components": []map[string]any{{
			"kind":           "agent",
			"name":           "refund_handler",
			"content":        agentComponent("refund_handler").Content,
			"sourceFilename": "refund_handler.yaml",
			"resourceId":     old.ID,
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := postBuild(t, svc, string(body), allBuildScopes)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var resp ApplyBuildResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if op := opFor(t, &resp, kindAgent, "refund_handler"); op.Action != actionUpdate || op.ResourceID != old.ID || op.Status != statusSucceeded {
		t.Errorf("op = %+v, want a succeeded update of %s", op, old.ID)
	}
	if got := storedAgentNames(t, svc); !slices.Equal(got, []string{"refund_handler"}) {
		t.Errorf("agents = %v, want the one renamed agent", got)
	}
}

func TestPostBuild_RefusesAResourceIDThatIsNotAUUID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	body, err := json.Marshal(map[string]any{
		"components": []map[string]any{{"kind": "agent", "name": "refund_handler", "content": agentComponent("refund_handler").Content, "resourceId": "not-an-id"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := postBuild(t, svc, string(body), allBuildScopes)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "resourceid") {
		t.Errorf("body = %s, want the refusal to name resourceId", rec.Body)
	}
	if got := storedAgentNames(t, svc); len(got) != 0 {
		t.Errorf("agents = %v, want nothing written", got)
	}
}

func TestPostBuild_PreviewNamesTheStoredResourceItRenames(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	old := seedAgent(t, svc, "refund_orchestrator")
	body, err := json.Marshal(map[string]any{
		"dryRun": true,
		"components": []map[string]any{{
			"kind": "agent", "name": "refund_handler", "content": agentComponent("refund_handler").Content, "resourceId": old.ID,
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := postBuild(t, svc, string(body), allBuildScopes)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var wire struct {
		Operations []map[string]any `json:"operations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil || len(wire.Operations) != 1 {
		t.Fatalf("decode: %v, %s", err, rec.Body)
	}
	row := wire.Operations[0]
	if row["renamedFrom"] != "refund_orchestrator" || row["resolvedName"] != "refund_handler" {
		t.Errorf("row = %v, want renamedFrom refund_orchestrator and resolvedName refund_handler on the wire", row)
	}
}
