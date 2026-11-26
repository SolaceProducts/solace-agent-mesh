// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const workflowsToolChecksSpec = `openapi: 3.0.0
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

const workflowsToolChecksConnector = "carrier-api"

func newWorkflowsToolChecksAPI(t *testing.T) (*entrypointAPI, string) {
	t.Helper()
	api, _ := newAWEAPI(t)
	connector := createNamedConnector(t, api.apiClient, workflowsToolChecksConnector)
	expectStatus(t, uploadSpec(t, api.apiClient, connector.ID, "spec.yaml", workflowsToolChecksSpec), http.StatusOK)
	return api, connector.ID
}

func workflowsToolChecksShipNode(input map[string]any) map[string]any {
	return map[string]any{"id": "ship", "type": "tool", "connector": workflowsToolChecksConnector, "tool": "get_shipment", "input": input}
}

func workflowsToolChecksMCPEntry(url string, extra map[string]any) map[string]any {
	entry := map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "streamable-http", "url": url},
	}
	for k, v := range extra {
		entry[k] = v
	}
	return entry
}

func workflowsToolChecksShipConfig(input map[string]any) map[string]any {
	return map[string]any{
		"output_mapping": map[string]any{"result": "{{ship.output}}"},
		"nodes":          []any{workflowsToolChecksShipNode(input)},
	}
}

func workflowsToolChecksWeatherConfig(tool map[string]any) map[string]any {
	return map[string]any{
		"output_mapping": map[string]any{"result": "{{weather.output}}"},
		"tools":          []any{tool},
		"nodes":          []any{map[string]any{"id": "weather", "type": "tool", "tool_name": "get_weather"}},
	}
}

func workflowsToolChecksSave(api *entrypointAPI, name string, appConfig map[string]any) *httptest.ResponseRecorder {
	api.t.Helper()
	return api.call(http.MethodPost, "/workflows", map[string]any{
		"name": name, "description": "Workflow that binds a shipping connector.", "appConfig": appConfig,
	})
}

func workflowsToolChecksStore(api *entrypointAPI, name string, appConfig map[string]any, bindings []store.WorkflowConnectorBinding) string {
	api.t.Helper()
	body, err := json.Marshal(appConfig)
	if err != nil {
		api.t.Fatal(err)
	}
	wf := &store.Workflow{Name: name, AppConfig: string(body), CreatedBy: "test", UpdatedBy: "test"}
	if err := store.NewWorkflowRepo(api.core.DB()).Create(api.t.Context(), wf, store.WorkflowBindings{Connectors: bindings}); err != nil {
		api.t.Fatalf("store workflow: %v", err)
	}
	return wf.ID
}

func workflowsToolChecksWhy(t *testing.T, rec *httptest.ResponseRecorder, field string) string {
	t.Helper()
	return strings.Join(decodeError(t, rec).ValidationDetails[field], " ")
}

func TestWorkflows_SaveChecksAConnectorNodeAgainstTheUploadedSpec(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input map[string]any
		want  int
	}{
		{"a required parameter left out", map[string]any{"tracking_number": "{{workflow.input.t}}"}, http.StatusUnprocessableEntity},
		{"the declared key", map[string]any{"tracking": "{{workflow.input.t}}"}, http.StatusCreated},
		{"an undeclared key beside every required one, as the runtime takes it", map[string]any{"tracking": "{{workflow.input.t}}", "tracking_number": "x"}, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newWorkflowsToolChecksAPI(t)

			rec := workflowsToolChecksSave(api, "shipment-flow", workflowsToolChecksShipConfig(tc.input))

			expectStatus(t, rec, tc.want)
			if tc.want != http.StatusUnprocessableEntity {
				return
			}
			why := workflowsToolChecksWhy(t, rec, "appConfig")
			for _, want := range []string{"tracking_number", "tracking (required)"} {
				if !strings.Contains(why, want) {
					t.Errorf("appConfig details %q are missing %q", why, want)
				}
			}
		})
	}
}

func TestWorkflows_SaveRefusesAToolNameNoPinnedSourceRegistersAndListsTheRegisteredNames(t *testing.T) {
	api, _ := newWorkflowsToolChecksAPI(t)

	rec := workflowsToolChecksSave(api, "unknown-tool-flow",
		workflowsToolChecksWeatherConfig(workflowsToolChecksMCPEntry("http://127.0.0.1:1/mcp", map[string]any{"tool_name": "get_forecast"})))

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if why := workflowsToolChecksWhy(t, rec, "appConfig.nodes"); !strings.Contains(why, "get_forecast") {
		t.Errorf("appConfig.nodes details %q do not list the registered names", why)
	}
}

func TestWorkflowDeployments_RefuseAConnectorNodeTheUploadedSpecCannotSatisfy(t *testing.T) {
	api, connectorID := newWorkflowsToolChecksAPI(t)
	id := workflowsToolChecksStore(api, "deploy-bad-key", workflowsToolChecksShipConfig(map[string]any{"tracking_number": "x"}),
		[]store.WorkflowConnectorBinding{{ConnectorID: connectorID, RefName: workflowsToolChecksConnector}})

	rec := deployWorkflow(api, id)

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if why := workflowsToolChecksWhy(t, rec, "workflowId"); !strings.Contains(why, "tracking (required)") {
		t.Errorf("workflowId details %q do not name the operation's parameters", why)
	}
}

func TestWorkflowDeployments_RefuseAToolNameAPinnedEntryDoesNotRegister(t *testing.T) {
	api, _ := newWorkflowsToolChecksAPI(t)
	id := workflowsToolChecksStore(api, "deploy-unknown-pinned-tool",
		workflowsToolChecksWeatherConfig(workflowsToolChecksMCPEntry("http://127.0.0.1:1/mcp", map[string]any{"tool_name": "get_forecast"})), nil)

	rec := deployWorkflow(api, id)

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if why := workflowsToolChecksWhy(t, rec, "workflowId"); !strings.Contains(why, "references unknown tool") || !strings.Contains(why, "get_forecast") {
		t.Errorf("workflowId details %q, want the unknown tool refused with the registered names", why)
	}
}

func TestWorkflowDeployments_NeverCallAnMCPServerToLearnItsTools(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	api, _ := newWorkflowsToolChecksAPI(t)
	id := workflowsToolChecksStore(api, "deploy-unpinned-mcp", workflowsToolChecksWeatherConfig(workflowsToolChecksMCPEntry(server.URL+"/mcp", nil)), nil)

	expectCreated[WorkflowDeployment](t, deployWorkflow(api, id), "workflowDeployments")

	if got := requests.Load(); got != 0 {
		t.Errorf("deploy sent %d request(s) to the MCP server, want none", got)
	}
}
