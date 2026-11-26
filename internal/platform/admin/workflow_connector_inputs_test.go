// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

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
        - name: carrierCode
          in: query
          schema:
            type: string
      responses:
        "200":
          description: ok
  /shipments:
    post:
      operationId: createShipment
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [destination]
              properties:
                destination:
                  type: string
                note:
                  type: string
      responses:
        "201":
          description: created
  /ping:
    get:
      operationId: ping
      responses:
        "200":
          description: ok
`

func storeStoredSpec(t *testing.T, repo *store.ConnectorRepo, specs ResourceStore, conn *store.Connector, spec string) {
	t.Helper()
	key := "connectors/" + conn.ID + "/spec.yaml"
	if err := specs.Put(t.Context(), key, strings.NewReader(spec)); err != nil {
		t.Fatalf("store spec: %v", err)
	}
	if err := repo.UpdateResourceKey(t.Context(), conn.ID, key, "hash"); err != nil {
		t.Fatalf("set resource key: %v", err)
	}
}

type inputCheckFixture struct {
	svc     *WorkflowService
	repo    *store.ConnectorRepo
	specs   ResourceStore
	logs    *bytes.Buffer
	counter int
}

func newInputCheckFixture(t *testing.T) *inputCheckFixture {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	specs := newMemResourceStore()
	logs := &bytes.Buffer{}
	svc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), repo, nil, nil, "ns",
		slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	svc.SetResourceStore(specs)
	return &inputCheckFixture{svc: svc, repo: repo, specs: specs, logs: logs}
}

func (f *inputCheckFixture) shipmentConnector(t *testing.T, name string) {
	t.Helper()
	conn := createHydrationConnector(t, f.repo, name, "api", "openapi", apiConnectorValues(""))
	storeStoredSpec(t, f.repo, f.specs, conn, shipmentSpec)
}

func (f *inputCheckFixture) create(t *testing.T, node map[string]any) error {
	t.Helper()
	f.counter++
	_, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "shipment-flow-" + string(rune('a'+f.counter)),
		Description: "binds a shipping connector",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"nodes":          []any{node},
		},
	}, "u")
	return err
}

func shipNode(tool string, input map[string]any) map[string]any {
	node := map[string]any{"id": "ship", "type": "tool", "connector": "carrier-api", "tool": tool}
	if input != nil {
		node["input"] = input
	}
	return node
}

func TestWorkflowSave_RefusesAConnectorNodeThatCannotSatisfyItsOperation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		node  map[string]any
		wants []string
	}{
		{
			name: "an invented key for a path parameter",
			node: shipNode("get_shipment", map[string]any{"tracking_number": "{{workflow.input.tracking}}"}),
			wants: []string{`node "ship"`, `operation "get_shipment" of connector "carrier-api"`, "tracking_number",
				"tracking (required)", "carrier_code (optional)", "mandatory input parameters are not present"},
		},
		{
			name:  "the selector spelled as the spec's operationId",
			node:  shipNode("getShipment", map[string]any{"tracking_number": "x"}),
			wants: []string{"tracking_number", "tracking (required)"},
		},
		{
			name:  "a request body's required property left out",
			node:  shipNode("create_shipment", map[string]any{"note": "fragile"}),
			wants: []string{"destination", "destination (required)", "note (optional)"},
		},
		{
			name:  "an empty input names nothing",
			node:  shipNode("get_shipment", map[string]any{}),
			wants: []string{"required input parameter(s) tracking"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			f.shipmentConnector(t, "carrier-api")

			err := f.create(t, tc.node)
			if err == nil {
				t.Fatal("save accepted a node that fails on every run")
			}
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("err is %T, want *ValidationError so the caller answers 422 rather than 500", err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q is missing %q", err, want)
				}
			}
		})
	}
}

func TestWorkflowSave_AcceptsEveryConnectorNodeThatRuns(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]any{
		"the declared key with a template":                 shipNode("get_shipment", map[string]any{"tracking": "{{workflow.input.tracking}}"}),
		"the declared key with a literal":                  shipNode("get_shipment", map[string]any{"tracking": "1Z999"}),
		"an optional parameter left out":                   shipNode("get_shipment", map[string]any{"tracking": "{{workflow.input.t}}"}),
		"an optional parameter named":                      shipNode("get_shipment", map[string]any{"tracking": "{{workflow.input.t}}", "carrier_code": "ups"}),
		"no input at all, fed by the workflow input":       shipNode("get_shipment", nil),
		"coalesce as the whole input":                      shipNode("get_shipment", map[string]any{"coalesce": []any{"{{workflow.input}}", "{{workflow.input.fallback}}"}}),
		"concat as the whole input":                        shipNode("get_shipment", map[string]any{"concat": []any{"{{workflow.input.a}}", "{{workflow.input.b}}"}}),
		"an operation with a request body, required named": shipNode("create_shipment", map[string]any{"destination": "{{workflow.input.to}}"}),
		"an operation with a body, every property named":   shipNode("create_shipment", map[string]any{"destination": "x", "note": "y"}),
		"an operation with no parameters and no keys":      shipNode("ping", map[string]any{}),
		"a tool selector the platform cannot match":        shipNode("no_such_op", map[string]any{"anything": 1}),
		"an undeclared key beside every required one":      shipNode("get_shipment", map[string]any{"tracking": "{{workflow.input.t}}", "tracking_number": "x"}),
	}
	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			f.shipmentConnector(t, "carrier-api")

			if err := f.create(t, node); err != nil {
				t.Fatalf("save refused a node that runs: %v", err)
			}
		})
	}
}

func TestWorkflowSave_LeavesAConnectorItCannotReadUnchecked(t *testing.T) {
	t.Parallel()
	wrongKey := map[string]any{"tracking_number": "x"}
	cases := []struct {
		name  string
		build func(t *testing.T, f *inputCheckFixture)
	}{
		{"an allow_list and no uploaded spec: names without parameters", func(t *testing.T, f *inputCheckFixture) {
			createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi",
				apiConnectorValues(`,"specification_url":"https://specs.example.com/s.yaml","allow_list":"getShipment"`))
		}},
		{"a stored spec that does not parse", func(t *testing.T, f *inputCheckFixture) {
			conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(`,"allow_list":"getShipment"`))
			storeStoredSpec(t, f.repo, f.specs, conn, "this is: [not an openapi document")
		}},
		{"a stored spec the resource store has lost", func(t *testing.T, f *inputCheckFixture) {
			conn := createHydrationConnector(t, f.repo, "carrier-api", "api", "openapi", apiConnectorValues(`,"allow_list":"getShipment"`))
			if err := f.repo.UpdateResourceKey(t.Context(), conn.ID, "connectors/missing.yaml", "hash"); err != nil {
				t.Fatalf("set resource key: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newInputCheckFixture(t)
			tc.build(t, f)
			if err := f.create(t, shipNode("get_shipment", wrongKey)); err != nil {
				t.Fatalf("save refused a workflow whose connector it could not read: %v", err)
			}
		})
	}
}

func TestWorkflowSave_WithoutAResourceStoreTheCheckStaysQuiet(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")
	f.svc.SetResourceStore(nil)

	if err := f.create(t, shipNode("get_shipment", map[string]any{"tracking_number": "x"})); err != nil {
		t.Fatalf("save refused a workflow although no spec could be read: %v", err)
	}
}

func TestWorkflowUpdate_RefusesAConnectorNodeThatCannotSatisfyItsOperation(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")

	created, err := f.svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "shipment-flow-update",
		Description: "starts valid",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"nodes":          []any{shipNode("get_shipment", map[string]any{"tracking": "{{workflow.input.t}}"})},
		},
	}, "u")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = f.svc.Update(t.Context(), created.ID, UpdateWorkflowRequest{
		Name:        "shipment-flow-update",
		Description: "edited to the Builder's invented key",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"nodes":          []any{shipNode("get_shipment", map[string]any{"tracking_number": "{{workflow.input.t}}"})},
		},
	}, "u")
	if err == nil || !strings.Contains(err.Error(), "tracking_number") {
		t.Fatalf("update err = %v, want the invented key named", err)
	}
}

func TestControlAction_ReportsWhatTheBuilderValidates(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")
	createHydrationConnector(t, f.repo, "carrier-url-only", "api", "openapi",
		apiConnectorValues(`,"specification_url":"https://specs.example.com/s.yaml","allow_list":"getShipment"`))
	m := &ManageablePlatform{deps: &RouteDeps{WorkflowSvc: f.svc}, logger: slog.Default()}

	body, err := json.Marshal(map[string]any{"appConfig": map[string]any{
		"nodes": []any{
			shipNode("get_shipment", map[string]any{"tracking_number": "x"}),
			map[string]any{"id": "other", "type": "tool", "connector": "carrier-url-only", "tool": "get_shipment", "input": map[string]any{"x": 1}},
			map[string]any{"id": "ghost", "type": "tool", "connector": "not-created-yet", "input": map[string]any{"x": 1}},
		},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := m.HandleManagement(context.Background(), "post", []string{"workflows", "validate", "connectorToolInputs"}, body, nil)
	if err != nil {
		t.Fatalf("HandleManagement: %v", err)
	}
	report, ok := got.(*ConnectorToolInputReport)
	if !ok {
		t.Fatalf("response is %T, want *ConnectorToolInputReport", got)
	}
	if len(report.Problems) != 1 || report.Problems[0].NodeID != "ship" ||
		!strings.Contains(report.Problems[0].Message, "tracking_number") {
		t.Fatalf("problems = %+v, want one problem for node ship", report.Problems)
	}
	unchecked := map[string]string{}
	for _, u := range report.Unchecked {
		unchecked[u.Connector] = u.Reason
	}
	if !strings.Contains(unchecked["carrier-url-only"], "no uploaded spec") {
		t.Errorf("unchecked = %v, want the URL-only connector explained", unchecked)
	}
	if _, listed := unchecked["not-created-yet"]; !listed {
		t.Errorf("unchecked = %v, want the connector that does not exist yet", unchecked)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(encoded), `"nodeId":"ship"`) {
		t.Errorf("wire form %s must use camelCase keys", encoded)
	}
}

func TestControlAction_EmptyReportMarshalsListsNotNull(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	report := f.svc.CheckConnectorToolInputs(t.Context(), map[string]any{"nodes": []any{}})
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"problems":[],"unchecked":[]}` {
		t.Fatalf("report = %s, want empty lists, not null", encoded)
	}
}

func TestControlAction_RejectsABodyWithoutAnAppConfig(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	m := &ManageablePlatform{deps: &RouteDeps{WorkflowSvc: f.svc}, logger: slog.Default()}
	_, err := m.HandleManagement(context.Background(), "post", []string{"workflows", "validate", "connectorToolInputs"}, []byte(`{}`), nil)
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
}

func TestControlProtocol_WorkflowConnectorToolInputs(t *testing.T) {
	t.Parallel()
	if WorkflowConnectorToolInputsPath != tools.WorkflowConnectorToolInputsPath {
		t.Fatalf("path %q differs from the Builder's %q", WorkflowConnectorToolInputsPath, tools.WorkflowConnectorToolInputsPath)
	}
	f := newInputCheckFixture(t)
	f.shipmentConnector(t, "carrier-api")
	createHydrationConnector(t, f.repo, "listed-only", "api", "openapi",
		apiConnectorValues(`,"specification_url":"https://specs.example.com/s.json","allow_list":"getShipment"`))
	env := setupControlTestWithDeps(t, func(deps *RouteDeps) { deps.WorkflowSvc = f.svc })

	body, err := json.Marshal(map[string]any{"appConfig": map[string]any{
		"nodes": []any{
			connectorNode("missing", "carrier-api", "get_shipment", map[string]any{"tracking_number": "x"}),
			connectorNode("extra", "carrier-api", "get_shipment", map[string]any{"tracking": "t", "tracking_number": "x"}),
			connectorNode("ungraded", "listed-only", "get_shipment", map[string]any{"anything": "x"}),
		},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw, err := env.Client.Custom(t.Context(), "platform", "post", tools.WorkflowConnectorToolInputsPath, body)
	if err != nil {
		t.Fatalf("Custom: %v", err)
	}

	var report tools.ConnectorToolInputReport
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("the Builder's decoder rejects %s: %v", raw, err)
	}
	fatal := map[string]bool{}
	for _, p := range report.Problems {
		fatal[p.NodeID] = p.Fatal
		if p.Connector != "carrier-api" || p.Message == "" {
			t.Errorf("problem = %+v, want the connector and a message", p)
		}
	}
	if len(report.Problems) != 2 || !fatal["missing"] || fatal["extra"] {
		t.Fatalf("problems = %+v, want the missing required parameter fatal and the undeclared key not", report.Problems)
	}
	if len(report.Unchecked) != 1 || report.Unchecked[0].Connector != "listed-only" || !strings.Contains(report.Unchecked[0].Reason, "no uploaded spec") {
		t.Fatalf("unchecked = %+v, want the allow_list-only connector with its reason", report.Unchecked)
	}

	var generic struct {
		Problems  []map[string]any `json:"problems"`
		Unchecked []map[string]any `json:"unchecked"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	for _, key := range []string{"nodeId", "connector", "message", "fatal"} {
		if _, present := generic.Problems[0][key]; !present {
			t.Errorf("problem %v lacks the wire key %q", generic.Problems[0], key)
		}
	}
	for _, key := range []string{"connector", "reason"} {
		if _, present := generic.Unchecked[0][key]; !present {
			t.Errorf("unchecked entry %v lacks the wire key %q", generic.Unchecked[0], key)
		}
	}
	if len(generic.Problems[0]) != 4 || len(generic.Unchecked[0]) != 2 {
		t.Errorf("wire entries %v and %v carry keys the Builder does not know", generic.Problems[0], generic.Unchecked[0])
	}
}

func TestWorkflowConnectorToolInputsAction_RequiresWorkflowAndConnectorRead(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	m := &ManageablePlatform{deps: &RouteDeps{WorkflowSvc: f.svc}, logger: slog.Default()}
	path := strings.Split(WorkflowConnectorToolInputsPath, "/")
	body := json.RawMessage(`{"appConfig":{"nodes":[]}}`)

	allowed := &auth.Identity{Scopes: []string{rbac.ScopeWorkflowBuilderRead, rbac.ScopeConnectorRead}}
	if _, err := m.HandleManagement(t.Context(), "post", path, body, allowed); err != nil {
		t.Errorf("workflow and connector read: %v", err)
	}
	for name, scopes := range map[string][]string{
		"workflow read only":  {rbac.ScopeWorkflowBuilderRead},
		"connector read only": {rbac.ScopeConnectorRead},
	} {
		_, err := m.HandleManagement(t.Context(), "post", path, body, &auth.Identity{Scopes: scopes})
		if err == nil || !strings.Contains(err.Error(), "insufficient permissions") {
			t.Errorf("%s: err = %v, want insufficient permissions", name, err)
		}
	}
}
