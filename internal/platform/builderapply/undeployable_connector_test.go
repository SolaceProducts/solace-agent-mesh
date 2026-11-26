// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const unreachableSpecURL = "http://127.0.0.1:1/openapi.json"

const shipmentSpec = `openapi: 3.0.0
info:
  title: Shipping
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /shipments/{tracking}:
    get:
      operationId: getShipment
      parameters:
        - name: tracking
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok
`

func apiConnectorValues(extra map[string]any) map[string]any {
	values := map[string]any{"base_url": "https://api.example.com", "auth_type": "none"}
	for k, v := range extra {
		values[k] = v
	}
	return values
}

func apiConnectorComponent(name string, values map[string]any) BuildComponentInput {
	return connectorWith(name, func(b map[string]any) {
		b["type"], b["subtype"] = "api", "openapi"
		b["values"] = values
	})
}

func workflowCallingConnector(name, connector, tool, input string) BuildComponentInput {
	return BuildComponentInput{Kind: kindWorkflow, Name: name, Content: `
apps:
  - name: ` + name + `
    app_config:
      name: ` + name + `
      agent_type: workflow
      workflow:
        output_mapping:
          result: "{{ship.output}}"
        agent_card:
          description: "A workflow that calls a connector."
        nodes:
          - id: ship
            type: tool
            connector: ` + connector + `
            tool: ` + tool + `
` + input}
}

func shipInput(key string) string {
	return "            input:\n              " + key + ": \"{{workflow.input.tracking}}\"\n"
}

func seedStoredShipmentConnector(t *testing.T, svc *Service, name string) {
	t.Helper()
	seedStoredSpecConnector(t, svc, name, shipmentSpec, nil)
}

func seedStoredSpecConnector(t *testing.T, svc *Service, name, spec string, extraValues map[string]any) string {
	t.Helper()
	created, err := svc.deps.Connectors.Create(t.Context(), entplatform.CreateConnectorRequest{
		Name: name, Description: "A stored shipping connector.", Type: "api", Subtype: "openapi",
		Values: apiConnectorValues(extraValues),
	}, "tester")
	if err != nil {
		t.Fatalf("seed connector %q: %v", name, err)
	}
	if _, err := svc.deps.Connectors.UploadResource(t.Context(), created.ID, "spec.yaml", strings.NewReader(spec)); err != nil {
		t.Fatalf("upload spec for %q: %v", name, err)
	}
	return created.ID
}

func seedStoredURLOnlyConnector(t *testing.T, svc *Service, name string) {
	t.Helper()
	if _, err := svc.deps.Connectors.Create(t.Context(), entplatform.CreateConnectorRequest{
		Name: name, Description: "A stored spec-URL connector.", Type: "api", Subtype: "openapi",
		Values: apiConnectorValues(map[string]any{"specification_url": unreachableSpecURL}),
	}, "tester"); err != nil {
		t.Fatalf("seed connector %q: %v", name, err)
	}
}

func requireRefusedBeforeWriting(t *testing.T, svc *Service, want string, components ...BuildComponentInput) (dry, applied *ApplyBuildResponse) {
	t.Helper()
	before := platformSnapshot(t, svc)
	dry, applied = applyBoth(t, svc, components...)
	requireSameFailure(t, opFor(t, dry, kindWorkflow, "ship_flow"), opFor(t, applied, kindWorkflow, "ship_flow"), want)
	if after := platformSnapshot(t, svc); after != before {
		t.Fatal("a build refused for its connector tool node wrote to the platform")
	}
	if message := opFor(t, applied, kindWorkflow, "ship_flow").Error.Message; strings.Contains(message, "specification_file") {
		t.Errorf("the refusal advises specification_file, which a Builder save cannot carry: %q", message)
	}
	return dry, applied
}

func TestApply_RefusesAConnectorNodeThatCannotDeploy(t *testing.T) {
	t.Parallel()

	t.Run("a stored spec-URL connector with no allow_list", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		seedStoredURLOnlyConnector(t, svc, "petstore")
		requireRefusedBeforeWriting(t, svc, "set allow_list", workflowCallingConnector("ship_flow", "petstore", "listPets", ""))
	})

	t.Run("a connector created in the same build with no allow_list", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		_, applied := requireRefusedBeforeWriting(t, svc, "set allow_list",
			apiConnectorComponent("petstore", apiConnectorValues(map[string]any{"specification_url": unreachableSpecURL})),
			workflowCallingConnector("ship_flow", "petstore", "listPets", ""))
		if got := opFor(t, applied, kindConnector, "petstore").Status; got != statusSkipped {
			t.Errorf("connector = %q, want skipped: the build was refused before its first write", got)
		}
	})

	t.Run("a selector outside the allow_list", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		requireRefusedBeforeWriting(t, svc, `emits no tool named "getPet"`,
			apiConnectorComponent("petstore", apiConnectorValues(map[string]any{"specification_url": unreachableSpecURL, "allow_list": "listPets"})),
			workflowCallingConnector("ship_flow", "petstore", "getPet", ""))
	})

	t.Run("an input that misses a required parameter of the stored spec", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		seedStoredShipmentConnector(t, svc, "carrier-api")
		requireRefusedBeforeWriting(t, svc, "tracking",
			agentComponent("good_agent"),
			workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", shipInput("tracking_number")))
	})

	t.Run("a connector neither stored nor in the build", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		_, applied := requireRefusedBeforeWriting(t, svc, `connector "ghost" not found`,
			agentComponent("good_agent"),
			workflowCallingConnector("ship_flow", "ghost", "anything", ""))
		if got := opFor(t, applied, kindAgent, "good_agent").Status; got != statusSkipped {
			t.Errorf("agent = %q, want skipped: the workflow's write would have failed after it", got)
		}
	})
}

func TestApply_AcceptsAConnectorNodeThatDeploys(t *testing.T) {
	t.Parallel()

	t.Run("a connector created with an allow_list warns that its inputs are unchecked", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		dry, applied := applyBoth(t, svc,
			apiConnectorComponent("petstore", apiConnectorValues(map[string]any{"specification_url": unreachableSpecURL, "allow_list": "listPets"})),
			workflowCallingConnector("ship_flow", "petstore", "listPets", ""))
		for _, resp := range []*ApplyBuildResponse{dry, applied} {
			op := opFor(t, resp, kindWorkflow, "ship_flow")
			if op.Status == statusFailed || !mentions(op.Warnings, `connector "petstore" could not be checked: it has no stored specification`) {
				t.Errorf("dryRun=%v: workflow = %+v, want accepted with the unchecked-inputs warning", resp.DryRun, op)
			}
		}
		if applied.Summary.Failed != 0 {
			t.Fatalf("apply failed: %+v", applied.Operations)
		}
	})

	t.Run("a stored connector whose spec the input satisfies", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		seedStoredShipmentConnector(t, svc, "carrier-api")
		dry, applied := applyBoth(t, svc, workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", shipInput("tracking")))
		for _, resp := range []*ApplyBuildResponse{dry, applied} {
			if op := opFor(t, resp, kindWorkflow, "ship_flow"); op.Status == statusFailed || len(op.Warnings) != 0 {
				t.Errorf("dryRun=%v: workflow = %+v, want accepted with no warning", resp.DryRun, op)
			}
		}
	})

	t.Run("a bound connector renamed since the workflow was saved", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		seedStoredShipmentConnector(t, svc, "carrier-api")
		first, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
			workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", shipInput("tracking")),
		}}, testActor)
		if err != nil || first.Summary.Failed != 0 {
			t.Fatalf("save the workflow: %v %+v", err, first)
		}
		renameStoredConnector(t, svc, "carrier-api", "carrier-api-renamed")

		edited := workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", "            input:\n              tracking: \"literal\"\n")
		dry, applied := applyBoth(t, svc, edited)
		for _, resp := range []*ApplyBuildResponse{dry, applied} {
			op := opFor(t, resp, kindWorkflow, "ship_flow")
			if op.Action != actionUpdate || op.Status == statusFailed {
				t.Errorf("dryRun=%v: workflow = %+v, want an accepted update: its binding survives the rename", resp.DryRun, op)
			}
		}
	})
}

func renameStoredConnector(t *testing.T, svc *Service, from, to string) {
	t.Helper()
	repo := store.NewConnectorRepo(svc.deps.DB)
	rows, err := repo.GetAllByName(t.Context(), from)
	if err != nil || len(rows) != 1 {
		t.Fatalf("find connector %q: %v %v", from, rows, err)
	}
	rows[0].Name = to
	if err := repo.Update(t.Context(), rows[0]); err != nil {
		t.Fatalf("rename connector: %v", err)
	}
}

func TestPlannedConnectorsOf_KeysByPlatformNameAndSkipsUnchanged(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	connector := apiConnectorComponent("petstore", apiConnectorValues(map[string]any{"allow_list": "listPets"}))
	plans, _, err := svc.Plan(ctx, ApplyBuildRequest{Components: []BuildComponentInput{connector}}, testActor)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	planned := plannedConnectorsOf(plans)
	got, ok := planned["petstore"]
	if !ok || got.ID != "" || got.Type != "api" || got.Subtype != "openapi" || got.Values["allow_list"] != "listPets" {
		t.Fatalf("planned = %+v, want one new api/openapi connector petstore", planned)
	}
	if keys := mapKeys(planned); !slices.Equal(keys, []string{"petstore"}) {
		t.Errorf("keys = %v, want only the platform name", keys)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{connector}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("apply: %v %+v", err, resp)
	}
	plans, _, err = svc.Plan(ctx, ApplyBuildRequest{Components: []BuildComponentInput{connector}}, testActor)
	if err != nil {
		t.Fatalf("replan: %v", err)
	}
	if planned := plannedConnectorsOf(plans); len(planned) != 0 {
		t.Errorf("planned = %+v, want none: the connector is unchanged", planned)
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func TestApply_ReportsARequestShapeErrorAndAnUndeployableNodeInOneRound(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedStoredURLOnlyConnector(t, svc, "petstore")
	workflow := workflowCallingConnector("ship_flow", "petstore", "listPets", "")
	workflow.Content = strings.Replace(workflow.Content, "        nodes:\n", "        connector_refs: []\n        nodes:\n", 1)

	dry, applied := applyBoth(t, svc, workflow)
	for _, resp := range []*ApplyBuildResponse{dry, applied} {
		op := opFor(t, resp, kindWorkflow, "ship_flow")
		if op.Status != statusFailed || op.Error == nil ||
			!strings.Contains(op.Error.Message, "connector_refs is no longer supported") || !strings.Contains(op.Error.Message, "set allow_list") {
			t.Errorf("dryRun=%v: workflow = %+v, want one refusal naming both reasons", resp.DryRun, op)
		}
	}
}
