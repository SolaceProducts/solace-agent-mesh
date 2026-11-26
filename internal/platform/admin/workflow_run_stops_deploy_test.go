// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"maps"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestWorkflowDeploy_RefusesAFailingNodeOnlyWhereItCanStopARun(t *testing.T) {
	t.Parallel()
	families := []struct {
		name    string
		ship    map[string]any
		main    map[string]any
		tools   []any
		wants   string
		connect bool
	}{
		{
			name: "a connector node missing a required parameter", ship: shipNode("get_shipment", map[string]any{"carrier_code": "ups"}),
			main: connectorMainNode(), wants: "required input parameter(s) tracking", connect: true,
		},
		{
			name: "a tool name no source registers", ship: map[string]any{"id": "ship", "type": "tool", "tool_name": "track_package"},
			main:  map[string]any{"id": "main", "type": "tool", "tool_name": "get_current_time"},
			tools: []any{map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"}}, wants: `"track_package"`,
		},
	}
	for _, family := range families {
		for _, shape := range failingNodeShapes(family.ship, family.main) {
			t.Run(family.name+", "+shape.name, func(t *testing.T) {
				t.Parallel()
				h := setupE2E(t, nil)
				specs := newMemResourceStore()
				var bindings []store.WorkflowConnectorBinding
				if family.connect {
					conn := createHydrationConnector(t, h.connectorRepo, "carrier-api", "api", "openapi", apiConnectorValues(""))
					storeStoredSpec(t, h.connectorRepo, specs, conn, shipmentSpec)
					bindings = []store.WorkflowConnectorBinding{{ConnectorID: conn.ID, RefName: "carrier-api"}}
				}
				shape.body["output_mapping"] = map[string]any{"result": "done"}
				if family.tools != nil {
					shape.body["tools"] = family.tools
				}
				id := storeWithoutSaveGate(t, h, "run-stops", shape.body, bindings)

				var logs bytes.Buffer
				resp := deployThroughLogging(h, specs, id, slog.New(slog.NewJSONHandler(&logs, nil)))
				if shape.refused {
					if resp.Status != DeployStatusFailed || !strings.Contains(resp.ErrorMessage, family.wants) {
						t.Fatalf("deploy = %q (%s), want it refused naming %q", resp.Status, resp.ErrorMessage, family.wants)
					}
					svc := NewWorkflowDeploymentService(h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, h.agentRepo, specs, h.controlClient, h.namespace, slog.Default())
					h.useHarnessAWEs(svc)
					redeploy, err := svc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: id, Action: DeployActionUpdate}, "e2e-user")
					if err != nil || redeploy.Status != DeployStatusFailed {
						t.Fatalf("redeploy = %+v, %v; want the same refusal", redeploy, err)
					}
					return
				}
				if resp.Status != DeployStatusSuccess {
					t.Fatalf("deploy = %q (%s), want a node that cannot stop a run to deploy", resp.Status, resp.ErrorMessage)
				}
				line := findLog(t, &logs, "cannot stop a run")
				assertLogFields(t, line, map[string]any{"level": "WARN", "workflowID": id, "nodeID": "ship"})

				svc := NewWorkflowDeploymentService(h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, h.agentRepo, specs, h.controlClient, h.namespace, slog.Default())
				h.useHarnessAWEs(svc)
				redeploy, err := svc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: id, Action: DeployActionUpdate}, "e2e-user")
				if err != nil || redeploy.Status != DeployStatusSuccess {
					t.Fatalf("redeploy = %+v, %v; want a node that cannot stop a run to redeploy", redeploy, err)
				}
			})
		}
	}
}

func TestWorkflowSave_AnUnknownToolNodeThatCannotStopARunIsLogged(t *testing.T) {
	t.Parallel()
	ship := map[string]any{"id": "ship", "type": "tool", "tool_name": "track_package"}
	main := map[string]any{"id": "main", "type": "tool", "tool_name": "get_current_time"}
	for _, shape := range failingNodeShapes(ship, main) {
		if shape.refused {
			continue
		}
		shape.body["tools"] = []any{map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"}}
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			if err := f.createBody(t, maps.Clone(shape.body)); err != nil {
				t.Fatalf("save refused a node that cannot stop a run: %v", err)
			}
			line := findLog(t, f.logs, "cannot stop a run")
			assertLogFields(t, line, map[string]any{"level": "WARN", "nodeID": "ship", "toolName": "track_package"})
		})
		t.Run(shape.name+", on update", func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			if err := f.createBody(t, map[string]any{"nodes": []any{main}, "tools": shape.body["tools"]}); err != nil {
				t.Fatalf("create: %v", err)
			}
			list, err := f.svc.ListPaginated(t.Context(), 1, 10)
			if err != nil || len(list.Data) != 1 {
				t.Fatalf("list = %+v, %v", list, err)
			}
			body := maps.Clone(shape.body)
			body["output_mapping"] = map[string]any{"result": "done"}
			_, err = f.svc.Update(t.Context(), list.Data[0].ID, UpdateWorkflowRequest{Name: "run-stops-updated", Description: "an updated workflow", AppConfig: body}, "u")
			if err != nil {
				t.Fatalf("update refused a node that cannot stop a run: %v", err)
			}
			line := findLog(t, f.logs, "cannot stop a run")
			assertLogFields(t, line, map[string]any{"level": "WARN", "nodeID": "ship", "toolName": "track_package"})
		})
	}
}
