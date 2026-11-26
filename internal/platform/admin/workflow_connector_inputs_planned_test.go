// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func plannedAPI(name string, values map[string]any) map[string]PlannedConnector {
	return map[string]PlannedConnector{name: {Name: name, Type: "api", Subtype: "openapi", Values: values}}
}

func plannedConfig(nodes ...map[string]any) map[string]any {
	list := make([]any, len(nodes))
	for i, n := range nodes {
		list[i] = n
	}
	return map[string]any{"nodes": list}
}

func petNode(tool string) map[string]any {
	return map[string]any{"id": "pets", "type": "tool", "connector": "petstore", "tool": tool}
}

func requireRefusalNaming(t *testing.T, err error, wants ...string) {
	t.Helper()
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	for _, want := range wants {
		if !strings.Contains(invalid.Message, want) {
			t.Errorf("refusal %q is missing %q", invalid.Message, want)
		}
	}
}

func TestRejectUndeployableConnectorNodes(t *testing.T) {
	t.Parallel()
	urlOnly := map[string]any{"specification_url": "http://127.0.0.1:1/openapi.json"}
	withAllowList := map[string]any{"specification_url": "http://127.0.0.1:1/openapi.json", "allow_list": "listPets"}

	t.Run("refuses a planned api connector whose tool names cannot be listed", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(petNode("listPets")), plannedAPI("petstore", urlOnly))
		requireRefusalNaming(t, err, `node "pets"`, "cannot be enumerated at deploy time", "set allow_list")
	})

	t.Run("reports an unlistable connector once however many nodes call it", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		second := petNode("listPets")
		second["id"] = "pets2"
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(petNode("listPets"), second), plannedAPI("petstore", urlOnly))
		requireRefusalNaming(t, err)
		if n := strings.Count(err.Error(), "cannot be enumerated"); n != 1 {
			t.Errorf("listing refusal reported %d times, want once: %v", n, err)
		}
	})

	t.Run("accepts a planned connector that lists its tools by allow_list", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		if err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(petNode("listPets")), plannedAPI("petstore", withAllowList)); err != nil {
			t.Fatalf("refused a node that deploys: %v", err)
		}
	})

	t.Run("refuses a tool selector the allow_list does not settle", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(petNode("getPet")), plannedAPI("petstore", withAllowList))
		requireRefusalNaming(t, err, `node "pets"`, `emits no tool named "getPet"`)
	})

	t.Run("refuses a connector that is neither stored nor planned", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		node := petNode("listPets")
		node["connector"] = "ghost"
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(node), plannedAPI("petstore", withAllowList))
		requireRefusalNaming(t, err, `"ghost"`, "not found")
	})

	t.Run("ignores a body whose shape the request check owns", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		node := petNode("listPets")
		node["type"] = "agent"
		if err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(node), nil); err != nil {
			t.Fatalf("refused a shape error: %v", err)
		}
	})
}

func nestedConfig(nodes ...map[string]any) map[string]any {
	return map[string]any{"workflow": plannedConfig(nodes...)}
}

func TestRejectUndeployableConnectorNodes_StoredConnectors(t *testing.T) {
	t.Parallel()

	t.Run("refuses a stored spec that does not parse", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(""))
		storeStoredSpec(t, f.repo, f.specs, conn, "this is: [not an openapi document")
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(shipNode("get_shipment", nil)), nil)
		requireRefusalNaming(t, err, `node "ship"`, "not valid OpenAPI or references external documents")
		logged := f.logs.String()
		if !strings.Contains(logged, `"failureClass":"unparseable"`) || strings.Contains(logged, "not an openapi document") {
			t.Errorf("want the failure class logged and no spec content: %s", logged)
		}
	})

	t.Run("refuses a stored spec over the size the platform reads, naming the limit", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(""))
		oversized := shipmentSpec + "# " + strings.Repeat("x", connectorSpecMaxBytes) + "\n"
		storeStoredSpec(t, f.repo, f.specs, conn, oversized)
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(shipNode("get_shipment", nil)), nil)
		requireRefusalNaming(t, err, `node "ship"`, "larger than the 10 MB", "set allow_list")
		if strings.Contains(err.Error(), "not valid OpenAPI") {
			t.Errorf("a truncated read is reported as a parse failure: %v", err)
		}
	})

	t.Run("refuses a stored spec that declares no operations", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(""))
		storeStoredSpec(t, f.repo, f.specs, conn, "openapi: 3.0.0\ninfo:\n  title: Empty\n  version: 1.0.0\npaths: {}\n")
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(shipNode("get_shipment", nil)), nil)
		requireRefusalNaming(t, err, "declares no operations")
	})

	t.Run("leaves a stored spec the resource store has lost to the write", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(""))
		if err := f.repo.UpdateResourceKey(t.Context(), conn.ID, "connectors/missing.yaml", "hash"); err != nil {
			t.Fatalf("set resource key: %v", err)
		}
		if err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(shipNode("get_shipment", nil)), nil); err != nil {
			t.Fatalf("refused a spec it could not read: %v", err)
		}
	})

	t.Run("refuses a connector the save renames away from the node's name", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		stored := onlyConnectorNamed(t, f, "carrier-api")
		planned := map[string]PlannedConnector{"carrier-api-b": {ID: stored.ID, Name: "carrier-api-b", Values: map[string]any{"base_url": "https://api.example.com"}}}
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(shipNode("get_shipment", nil)), planned)
		requireRefusalNaming(t, err, `"carrier-api"`, "not found")
	})

	t.Run("a planned update keeps the stored type when the plan names none", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		stored := onlyConnectorNamed(t, f, "carrier-api")
		planned := map[string]PlannedConnector{"carrier-api": {ID: stored.ID, Values: map[string]any{"base_url": "https://api.example.com", "allow_list": "getShipment"}}}
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(shipNode("ping", nil)), planned)
		requireRefusalNaming(t, err, `emits no tool named "ping"`)
	})

	t.Run("grades the planned values of a connector the save updates when reached by name", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		stored := onlyConnectorNamed(t, f, "carrier-api")
		planned := map[string]PlannedConnector{"keyed-by-another-name": {ID: stored.ID, Values: map[string]any{"base_url": "https://api.example.com", "allow_list": "getShipment"}}}
		err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(shipNode("ping", nil)), planned)
		requireRefusalNaming(t, err, `emits no tool named "ping"`)
	})

	t.Run("reads the nested workflow form", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		err := rejectUndeployable(t.Context(), f.svc, "", nestedConfig(petNode("getPet")),
			plannedAPI("petstore", map[string]any{"allow_list": "listPets"}))
		requireRefusalNaming(t, err, `emits no tool named "getPet"`)
	})
}

func onlyConnectorNamed(t *testing.T, f *inputCheckFixture, name string) *store.Connector {
	t.Helper()
	found, err := f.repo.GetAllByName(t.Context(), name)
	if err != nil || len(found) != 1 {
		t.Fatalf("connector %q: %v %v", name, found, err)
	}
	return found[0]
}

func TestCheckPlannedConnectorToolInputs(t *testing.T) {
	t.Parallel()

	t.Run("a planned connector with no spec cannot be graded", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		planned := plannedAPI("petstore", map[string]any{"specification_url": "http://127.0.0.1:1/openapi.json", "allow_list": "listPets"})
		report := f.svc.NewPlannedChecks().CheckToolInputs(t.Context(), "", plannedConfig(petNode("listPets")), planned)
		if len(report.Problems) != 0 {
			t.Errorf("problems = %+v, want none", report.Problems)
		}
		if len(report.Unchecked) != 1 || report.Unchecked[0].Connector != "petstore" || !InputsNotCheckable(report.Unchecked[0]) {
			t.Errorf("unchecked = %+v, want petstore as not checkable", report.Unchecked)
		}
	})

	t.Run("planned values overlay the stored row and keep its uploaded spec", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		stored, err := f.repo.GetAllByName(t.Context(), "carrier-api")
		if err != nil || len(stored) != 1 {
			t.Fatalf("stored connector: %v %v", stored, err)
		}
		planned := plannedAPI("carrier-api", map[string]any{"base_url": "https://api.example.com", "allow_list": "getShipment,vanished"})
		planned["carrier-api"] = PlannedConnector{ID: stored[0].ID, Name: "carrier-api", Type: "api", Subtype: "openapi", Values: planned["carrier-api"].Values}

		report := f.svc.NewPlannedChecks().CheckToolInputs(t.Context(), "", plannedConfig(shipNode("vanished", map[string]any{"tracking": "x"})), planned)
		if len(report.Problems) != 1 || !report.Problems[0].Fatal || !strings.Contains(report.Problems[0].Message, `"vanished"`) {
			t.Fatalf("problems = %+v, want one fatal problem for the allow_list entry the spec lacks", report.Problems)
		}
	})

	t.Run("a planned update with no type is still graded as the stored api connector", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		stored := onlyConnectorNamed(t, f, "carrier-api")
		planned := map[string]PlannedConnector{"carrier-api": {ID: stored.ID, Values: map[string]any{"base_url": "https://api.example.com", "allow_list": "getShipment,vanished"}}}
		report := f.svc.NewPlannedChecks().CheckToolInputs(t.Context(), "", plannedConfig(shipNode("vanished", map[string]any{"tracking": "x"})), planned)
		if len(report.Problems) != 1 || !report.Problems[0].Fatal {
			t.Fatalf("problems = %+v, want the api check to run on the stored type", report.Problems)
		}
	})

	t.Run("reads the nested workflow form", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		report := f.svc.NewPlannedChecks().CheckToolInputs(t.Context(), "", nestedConfig(shipNode("get_shipment", map[string]any{"carrier": "x"})), nil)
		if len(report.Problems) != 1 || !strings.Contains(report.Problems[0].Message, "tracking") {
			t.Fatalf("problems = %+v, want the missing parameter named", report.Problems)
		}
	})

	t.Run("a stored connector is graded like CheckConnectorToolInputs", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		config := plannedConfig(shipNode("get_shipment", map[string]any{"carrier": "x"}))
		report := f.svc.NewPlannedChecks().CheckToolInputs(t.Context(), "", config, nil)
		if len(report.Problems) != 1 || !report.Problems[0].Fatal || !strings.Contains(report.Problems[0].Message, "tracking") {
			t.Fatalf("problems = %+v, want one fatal problem naming tracking", report.Problems)
		}
	})
}

func TestPlannedConnectorChecks_ResolveThroughTheWorkflowsBinding(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")
	created, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name: "renamed-connector-flow", Description: "binds a connector that is renamed later",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"nodes":          []any{shipNode("get_shipment", map[string]any{"tracking": "x"})},
		},
	}, "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bound, err := f.repo.GetAllByName(t.Context(), "carrier-api")
	if err != nil || len(bound) != 1 {
		t.Fatalf("bound connector: %v %v", bound, err)
	}
	renamed := bound[0]
	renamed.Name = "carrier-api-renamed"
	if updateErr := f.repo.Update(t.Context(), renamed); updateErr != nil {
		t.Fatalf("rename: %v", updateErr)
	}
	createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(""))

	config := plannedConfig(shipNode("get_shipment", map[string]any{"carrier": "x"}))
	if rejectErr := rejectUndeployable(t.Context(), f.svc, created.ID, config, nil); rejectErr != nil {
		t.Fatalf("refused a node whose connector was only renamed: %v", rejectErr)
	}
	report := f.svc.NewPlannedChecks().CheckToolInputs(t.Context(), created.ID, config, nil)
	if len(report.Problems) != 1 || !strings.Contains(report.Problems[0].Message, "tracking") {
		t.Fatalf("problems = %+v, want the bound (renamed) connector graded against its spec", report.Problems)
	}
	if len(report.Unchecked) != 0 {
		t.Errorf("unchecked = %+v, want none: the namesake with no spec was graded instead of the bound row", report.Unchecked)
	}

	updates := map[string]PlannedConnector{"carrier-api-renamed": {ID: renamed.ID, Values: map[string]any{"base_url": "https://api.example.com", "allow_list": "getShipment,vanished"}}}
	updatedReport := f.svc.NewPlannedChecks().CheckToolInputs(t.Context(), created.ID, plannedConfig(shipNode("vanished", map[string]any{"tracking": "x"})), updates)
	if len(updatedReport.Problems) != 1 || !strings.Contains(updatedReport.Problems[0].Message, `"vanished"`) {
		t.Fatalf("problems = %+v, want the bound connector graded with the values the save writes to it", updatedReport.Problems)
	}
	narrowed := map[string]PlannedConnector{"carrier-api-renamed": {ID: renamed.ID, Values: map[string]any{"base_url": "https://api.example.com", "allow_list": "getShipment"}}}
	rejectErr := rejectUndeployable(t.Context(), f.svc, created.ID, plannedConfig(shipNode("create_shipment", nil)), narrowed)
	requireRefusalNaming(t, rejectErr, `emits no tool named "create_shipment"`)
}

func rejectUndeployable(ctx context.Context, svc *WorkflowService, workflowID string, appConfig map[string]any, planned map[string]PlannedConnector) error {
	writeRefusal, deployRefusal := svc.NewPlannedChecks().RejectUndeployableConnectorNodes(ctx, workflowID, appConfig, planned)
	if writeRefusal != nil {
		return writeRefusal
	}
	return deployRefusal
}

type countingResourceStore struct {
	ResourceStore
	gets int
}

func (c *countingResourceStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	c.gets++
	return c.ResourceStore.Get(ctx, key)
}

func TestPlannedChecks_ReadAStoredSpecOncePerRequest(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")
	counting := &countingResourceStore{ResourceStore: f.specs}
	f.svc.SetResourceStore(counting)
	stored := onlyConnectorNamed(t, f, "carrier-api")
	planned := map[string]PlannedConnector{"carrier-api": {ID: stored.ID, Values: map[string]any{"base_url": "https://api.example.com"}}}
	workflows := []map[string]any{
		plannedConfig(shipNode("get_shipment", map[string]any{"tracking": "a"})),
		plannedConfig(shipNode("get_shipment", map[string]any{"tracking": "b"})),
		plannedConfig(shipNode("get_shipment", map[string]any{"tracking": "c"})),
	}
	runAll := func(checksFor func() *PlannedConnectorChecks) {
		for _, config := range workflows {
			checks := checksFor()
			checks.UsesPlannedConnector(t.Context(), "", config, planned)
			if _, deploy := checks.RejectUndeployableConnectorNodes(t.Context(), "", config, planned); deploy != nil {
				t.Fatalf("refused a connector that deploys: %v", deploy)
			}
			if report := checks.CheckToolInputs(t.Context(), "", config, planned); len(report.Problems) != 0 {
				t.Fatalf("graded a satisfied input as a problem: %+v", report.Problems)
			}
		}
	}

	request := f.svc.NewPlannedChecks()
	runAll(func() *PlannedConnectorChecks { return request })
	if counting.gets != 1 {
		t.Errorf("the spec was read %d times for one request over %d workflows, want 1", counting.gets, len(workflows))
	}

	counting.gets = 0
	for _, config := range workflows {
		_, _ = f.svc.NewPlannedChecks().RejectUndeployableConnectorNodes(t.Context(), "", config, planned)
		f.svc.NewPlannedChecks().CheckToolInputs(t.Context(), "", config, planned)
	}
	if counting.gets <= len(workflows) {
		t.Errorf("a control that shares nothing read the spec %d times, want more than one per workflow: the count proves nothing", counting.gets)
	}
}
