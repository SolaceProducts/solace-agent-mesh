// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func storeWithoutSaveGate(t *testing.T, h *e2eHarness, name string, appConfig map[string]any, bindings []store.WorkflowConnectorBinding) string {
	t.Helper()
	body, err := json.Marshal(appConfig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wf := &store.Workflow{Name: name, AppConfig: string(body), CreatedBy: "u", UpdatedBy: "u"}
	if err := h.workflowRepo.Create(h.ctx, wf, store.WorkflowBindings{Connectors: bindings}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	return wf.ID
}

func deployThrough(h *e2eHarness, specs ResourceStore, workflowID string) *WorkflowDeploymentResponse {
	h.t.Helper()
	return deployThroughLogging(h, specs, workflowID, slog.Default())
}

func deployThroughLogging(h *e2eHarness, specs ResourceStore, workflowID string, logger *slog.Logger) *WorkflowDeploymentResponse {
	h.t.Helper()
	svc := NewWorkflowDeploymentService(h.wfDeployRepo, h.workflowRepo, h.connectorRepo, nil, h.agentRepo, specs, h.controlClient, h.namespace, logger)
	h.useHarnessAWEs(svc)
	resp, err := svc.Create(h.ctx, CreateWorkflowDeploymentRequest{WorkflowID: workflowID, Action: DeployActionDeploy}, "e2e-user")
	if err != nil {
		h.t.Fatalf("deploy call errored: %v", err)
	}
	return resp
}

func TestWorkflowDeploy_RefusesAConnectorNodeThatCannotSatisfyItsOperation(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	specs := newMemResourceStore()
	conn := createHydrationConnector(t, h.connectorRepo, "carrier-api", "api", "openapi", apiConnectorValues(""))
	storeStoredSpec(t, h.connectorRepo, specs, conn, shipmentSpec)
	binding := []store.WorkflowConnectorBinding{{ConnectorID: conn.ID, RefName: "carrier-api"}}

	workflowFor := func(input map[string]any) map[string]any {
		return map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"nodes":          []any{shipNode("get_shipment", input)},
		}
	}

	badID := storeWithoutSaveGate(t, h, "bad-keys", workflowFor(map[string]any{"tracking_number": "{{workflow.input.t}}"}), binding)
	resp := deployThrough(h, specs, badID)
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q for a node that fails on every run", resp.Status, DeployStatusFailed)
	}
	for _, want := range []string{"tracking_number", "tracking (required)"} {
		if !strings.Contains(resp.ErrorMessage, want) {
			t.Errorf("deploy error %q is missing %q", resp.ErrorMessage, want)
		}
	}

	goodID := storeWithoutSaveGate(t, h, "good-keys", workflowFor(map[string]any{"tracking": "{{workflow.input.t}}"}), binding)
	if resp = deployThrough(h, specs, goodID); resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy status = %q (%s), want the corrected workflow to deploy", resp.Status, resp.ErrorMessage)
	}
}

func TestWorkflowDeploy_LogsAnUndeclaredKeyUnderTheWorkflowID(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	specs := newMemResourceStore()
	conn := createHydrationConnector(t, h.connectorRepo, "carrier-api", "api", "openapi", apiConnectorValues(""))
	storeStoredSpec(t, h.connectorRepo, specs, conn, shipmentSpec)
	binding := []store.WorkflowConnectorBinding{{ConnectorID: conn.ID, RefName: "carrier-api"}}
	id := storeWithoutSaveGate(t, h, "extra-key", map[string]any{
		"output_mapping": map[string]any{"result": "{{ship.output}}"},
		"nodes":          []any{shipNode("get_shipment", map[string]any{"tracking": "{{workflow.input.t}}", "tracking_number": "x"})},
	}, binding)

	var logs bytes.Buffer
	resp := deployThroughLogging(h, specs, id, slog.New(slog.NewJSONHandler(&logs, nil)))
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy status = %q (%s), want an undeclared key beside every required one to deploy", resp.Status, resp.ErrorMessage)
	}
	line := findLog(t, &logs, "passes input keys its OpenAPI operation does not declare")
	assertLogFields(t, line, map[string]any{"workflowID": id, "nodeID": "ship", "connector": "carrier-api"})
}

func TestWorkflowDeploy_RefusesAToolNodeNoPinnedSourceRegisters(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	pinned := func(nodeTool string) map[string]any {
		return map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"tools": []any{map[string]any{
				"tool_type": "mcp", "tool_name": "get_shipment",
				"connection_params": map[string]any{"type": "streamable-http", "url": "http://127.0.0.1:1/mcp"},
			}},
			"nodes": []any{map[string]any{"id": "ship", "type": "tool", "tool_name": nodeTool}},
		}
	}

	resp := deployThrough(h, nil, storeWithoutSaveGate(t, h, "unknown-tool", pinned("track_package"), nil))
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q", resp.Status, DeployStatusFailed)
	}
	for _, want := range []string{`"track_package"`, "references unknown tool", "get_shipment"} {
		if !strings.Contains(resp.ErrorMessage, want) {
			t.Errorf("deploy error %q is missing %q", resp.ErrorMessage, want)
		}
	}

	builtin := map[string]any{
		"output_mapping": map[string]any{"result": "{{ship.output}}"},
		"tools":          []any{map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"}},
		"nodes":          []any{map[string]any{"id": "ship", "type": "tool", "tool_name": "get_current_time"}},
	}
	if resp = deployThrough(h, nil, storeWithoutSaveGate(t, h, "known-tool", builtin, nil)); resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy status = %q (%s), want the corrected workflow to deploy", resp.Status, resp.ErrorMessage)
	}
}

func TestWorkflowSave_RefusesAToolNodeNoPinnedSourceRegisters(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)

	appConfig := func(toolEntry map[string]any, nodeTool string) map[string]any {
		return map[string]any{
			"output_mapping": map[string]any{"result": "{{ship.output}}"},
			"tools":          []any{toolEntry},
			"nodes":          []any{map[string]any{"id": "ship", "type": "tool", "tool_name": nodeTool}},
		}
	}
	mcpEntry := func(extra map[string]any) map[string]any {
		entry := map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"type": "streamable-http", "url": "http://127.0.0.1:1/mcp"},
		}
		for k, v := range extra {
			entry[k] = v
		}
		return entry
	}
	save := func(name string, cfg map[string]any) error {
		_, err := f.svc.Create(t.Context(), CreateWorkflowRequest{Name: name, Description: "d", AppConfig: cfg}, "u")
		return err
	}

	err := save("mcp-pinned-wrong", appConfig(mcpEntry(map[string]any{"tool_name": "get_shipment"}), "track_package"))
	if err == nil {
		t.Fatal("save accepted a tool node no source registers")
	}
	for _, want := range []string{`"track_package"`, "references unknown tool", "get_shipment"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}

	for name, cfg := range map[string]map[string]any{
		"mcp-pinned-right":  appConfig(mcpEntry(map[string]any{"tool_name": "get_shipment"}), "get_shipment"),
		"mcp-unpinned":      appConfig(mcpEntry(nil), "anything_the_server_lists"),
		"mcp-allow-listed":  appConfig(mcpEntry(map[string]any{"allow_list": []any{"get_shipment", "ping"}}), "ping"),
		"mcp-manifest-pins": appConfig(mcpEntry(map[string]any{"manifest": []any{map[string]any{"name": "get_shipment"}}}), "get_shipment"),
	} {
		if err := save(name, cfg); err != nil {
			t.Errorf("save refused %s, which runs: %v", name, err)
		}
	}
}
