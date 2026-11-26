// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"gopkg.in/yaml.v3"
)

func timeWorkflowAppConfig(declareTool bool) map[string]any {
	appConfig := map[string]any{
		"output_mapping": map[string]any{"time": "{{stamp.output}}"},
		"nodes": []any{
			map[string]any{"id": "stamp", "type": "tool", "tool_name": "get_current_time"},
		},
	}
	if declareTool {
		appConfig["tools"] = []any{map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"}}
	}
	return appConfig
}

func (h *e2eHarness) attemptWorkflowDeploy(workflowID string) *WorkflowDeploymentResponse {
	h.t.Helper()
	resp, err := h.wfDeploySvc.Create(h.ctx, CreateWorkflowDeploymentRequest{
		WorkflowID: workflowID,
		Action:     DeployActionDeploy,
	}, "e2e-user")
	if err != nil {
		h.t.Fatalf("deploy workflow %s: %v", workflowID, err)
	}
	return resp
}

func requireToolSourceRefusal(t *testing.T, resp *WorkflowDeploymentResponse) {
	t.Helper()
	if resp.Status != DeployStatusFailed {
		t.Fatalf("deploy status = %q, want %q: the tool node would fail on every run", resp.Status, DeployStatusFailed)
	}
	if !strings.Contains(resp.ErrorMessage, `tool node "stamp"`) {
		t.Fatalf("deploy error should name the tool node; got %q", resp.ErrorMessage)
	}
}

func TestE2E_WorkflowDeployRefusesToolNodeWithoutToolSource(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	wfID := h.createWorkflow("e2e_undeclared_tool_node", timeWorkflowAppConfig(false))
	requireToolSourceRefusal(t, h.attemptWorkflowDeploy(wfID))
	if _, err := h.aweExe.GetInstance(workflowInstanceName(wfID)); err == nil {
		t.Fatal("a refused deploy must not start an instance")
	}
}

func TestE2E_WorkflowRedeployRefusalKeepsThePreviousInstanceServing(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	const name = "e2e_tool_node_regression"
	wfID := h.createWorkflow(name, timeWorkflowAppConfig(true))
	h.deployWorkflow(wfID)
	instance := workflowInstanceName(wfID)
	h.waitForInstance(instance, 10*time.Second)

	if _, err := h.workflowSvc.Update(h.ctx, wfID, UpdateWorkflowRequest{
		Name:        name,
		Description: "E2E test workflow for testing",
		AppConfig:   timeWorkflowAppConfig(false),
	}, "e2e-user"); err != nil {
		t.Fatalf("update workflow: %v", err)
	}
	requireToolSourceRefusal(t, h.attemptWorkflowDeploy(wfID))

	info, err := h.aweExe.GetInstance(instance)
	if err != nil || info == nil || info.Status != awe.StatusRunning {
		t.Fatalf("the previously deployed instance must keep serving after a refused redeploy; got %+v, %v", info, err)
	}
	latest := h.latestWorkflowDeployment(wfID)
	if latest == nil || latest.Status != DeployStatusFailed || latest.CreatedBy == rollbackCreatedBy {
		t.Fatalf("the refusal must land before the instance is replaced, leaving the failed row latest rather than a rollback; got %+v", latest)
	}
}

func TestE2E_WorkflowDeployAcceptsToolNodeServedByASiblingConnector(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`openapi: 3.0.0
info:
  title: Petstore
  version: 1.0.0
servers:
  - url: https://pets.example.com
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        "200":
          description: ok
  /pets/{id}:
    get:
      operationId: getPet
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok
`))
	}))
	t.Cleanup(specSrv.Close)
	createHydrationConnector(t, h.connectorRepo, "sibling-api", "api", "openapi",
		fmt.Sprintf(`{"base_url":"https://pets.example.com","auth_type":"none","allow_list":"listPets,getPet","specification_url":%q}`,
			specSrv.URL+"/petstore.yaml"))

	appConfig := map[string]any{
		"output_mapping": map[string]any{"pet": "{{one.output}}"},
		"nodes": []any{
			map[string]any{"id": "all", "type": "tool", "connector": "sibling-api", "tool": "list_pets", "input": map[string]any{}},
			map[string]any{"id": "one", "type": "tool", "tool_name": "get_pet", "input": map[string]any{"id": "1"}},
		},
	}
	wfID := h.createWorkflow("e2e_connector_sibling_tool", appConfig)
	resp := h.attemptWorkflowDeploy(wfID)
	if resp.Status != DeployStatusSuccess {
		t.Fatalf("a tool node calling another tool of a bound connector runs today and must deploy; got %q (%s)", resp.Status, resp.ErrorMessage)
	}
	appCfg := deployedWorkflowAppConfig(t, h.latestWorkflowDeployment(wfID))
	openapiTool := findToolEntry(appCfg, func(tool map[string]any) bool { return tool["tool_type"] == "openapi" })
	if openapiTool == nil {
		t.Fatalf("hydration must append the bound connector's openapi tool to tools:; got %+v", appCfg["tools"])
	}
	if allowList, _ := openapiTool["allow_list"].([]any); !slices.Contains(allowList, any("getPet")) {
		t.Fatalf("the hydrated openapi tool must expose getPet, which serves the sibling node; got allow_list %+v", openapiTool["allow_list"])
	}
	if got := workflowNodeToolName(appCfg, "all"); got != "list_pets" {
		t.Fatalf("hydration must rewrite the connector node to tool_name list_pets; got %q", got)
	}
}

func TestE2E_ConnectorRotationRedeploysWorkflowWithASiblingToolNode(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)

	const connectorName = "rotating-mailer"
	createHydrationConnector(t, h.connectorRepo, connectorName, "email", "smtp",
		`{"smtp_host":"old.example.com","smtp_port":"2525","smtp_tls":"none","smtp_auth_type":"none","envelope_from_none":"a@example.com","recipient_allowlist":"example.com"}`)
	connRows, err := h.connectorRepo.GetAllByName(h.ctx, connectorName)
	if err != nil || len(connRows) != 1 {
		t.Fatalf("get connector row: %v (%d rows)", err, len(connRows))
	}
	emailTool := "send_email_" + connectorSuffix(connRows[0].ID)
	emailInput := map[string]any{"to": []any{"x@example.com"}, "subject": "s", "body": "b"}
	appConfig := map[string]any{
		"output_mapping": map[string]any{"receipt": "{{notify.output}}"},
		"nodes": []any{
			map[string]any{"id": "send", "type": "tool", "connector": connectorName, "input": emailInput},
			map[string]any{"id": "notify", "type": "tool", "tool_name": emailTool, "input": emailInput},
		},
	}
	wfID := h.createWorkflow("e2e_rotation_sibling_tool", appConfig)
	h.deployWorkflow(wfID)
	if findToolEntry(deployedWorkflowAppConfig(t, h.latestWorkflowDeployment(wfID)), func(tool map[string]any) bool {
		return tool["tool_name"] == emailTool
	}) == nil {
		t.Fatalf("hydration must emit %s, the tool the sibling node calls, as a tools: entry", emailTool)
	}

	rotated := `{"smtp_host":"new.example.com","smtp_port":"2525","smtp_tls":"none","smtp_auth_type":"none","envelope_from_none":"a@example.com","recipient_allowlist":"example.com"}`
	if updateErr := h.connectorRepo.UpdateValues(h.ctx, connRows[0].ID, rotated); updateErr != nil {
		t.Fatalf("update connector values: %v", updateErr)
	}
	pub := NewRedeployPublisher(h.deploySvc, h.agentRepo, testLogger()).
		WithWorkflowRedeploy(h.wfDeploySvc, h.workflowRepo)
	if pubErr := pub.PublishOutboxEvent(h.ctx, store.OutboxEvent{
		EventType:   "connector.updated",
		AggregateID: connRows[0].ID,
		Payload:     fmt.Sprintf(`{"agentIds":[],"workflowIds":[%q],"userId":"editor"}`, wfID),
	}); pubErr != nil {
		t.Fatalf("publish outbox event: %v", pubErr)
	}

	latest := h.latestWorkflowDeployment(wfID)
	if latest == nil || latest.Status != DeployStatusSuccess || latest.CreatedBy != "editor" {
		t.Fatalf("a connector rotation must redeploy the workflow rather than freeze it on the old credentials; got %+v", latest)
	}
	if latest.WorkflowConfigSnapshot == nil || !strings.Contains(*latest.WorkflowConfigSnapshot, "new.example.com") {
		t.Fatal("the redeployed snapshot must carry the rotated connector values")
	}
}

func deployedWorkflowAppConfig(t *testing.T, deployment *store.WorkflowDeployment) map[string]any {
	t.Helper()
	if deployment == nil || deployment.WorkflowConfigSnapshot == nil {
		t.Fatalf("no deployment snapshot; got %+v", deployment)
	}
	var snapshot struct {
		Apps []struct {
			AppConfig map[string]any `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err := yaml.Unmarshal([]byte(*deployment.WorkflowConfigSnapshot), &snapshot); err != nil || len(snapshot.Apps) != 1 {
		t.Fatalf("parse deployment snapshot: %v (%d apps)", err, len(snapshot.Apps))
	}
	return snapshot.Apps[0].AppConfig
}

func findToolEntry(appCfg map[string]any, match func(map[string]any) bool) map[string]any {
	tools, _ := appCfg["tools"].([]any)
	for _, entry := range tools {
		if tool, ok := entry.(map[string]any); ok && match(tool) {
			return tool
		}
	}
	return nil
}

func workflowNodeToolName(appCfg map[string]any, nodeID string) string {
	workflow, _ := appCfg["workflow"].(map[string]any)
	nodes, _ := workflow["nodes"].([]any)
	for _, entry := range nodes {
		if node, ok := entry.(map[string]any); ok && node["id"] == nodeID {
			toolName, _ := node["tool_name"].(string)
			return toolName
		}
	}
	return ""
}
