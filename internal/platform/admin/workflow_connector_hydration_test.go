// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func createHydrationConnector(t *testing.T, repo *store.ConnectorRepo, name, connType, subtype, values string) *store.Connector {
	t.Helper()
	desc := "hydration test connector"
	conn := &store.Connector{Name: name, Description: &desc, Type: connType, Subtype: &subtype, Values: &values, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), conn); err != nil {
		t.Fatalf("create connector %q: %v", name, err)
	}
	return conn
}

func emailConnectorValues() string {
	return `{"smtp_host":"smtp.example.com","smtp_port":"2525","smtp_tls":"none","smtp_auth_type":"none","envelope_from_none":"noreply@example.com","recipient_allowlist":"example.com"}`
}

func wrappedAppConfig(body map[string]any) map[string]any {
	return map[string]any{
		"agent_type": "workflow",
		"workflow":   body,
	}
}

func toolNodeList(appConfig map[string]any, t *testing.T) []map[string]any {
	t.Helper()
	body := appConfig["workflow"].(map[string]any)
	rawNodes := body["nodes"].([]any)
	nodes := make([]map[string]any, len(rawNodes))
	for i, raw := range rawNodes {
		nodes[i] = raw.(map[string]any)
	}
	return nodes
}

func resolveAndHydrate(t *testing.T, appConfig map[string]any, repo *store.ConnectorRepo) error {
	t.Helper()
	return resolveAndHydrateWithSpecs(t, appConfig, repo, nil)
}

func resolveAndHydrateWithSpecs(t *testing.T, appConfig map[string]any, repo *store.ConnectorRepo, specs ResourceStore) error {
	t.Helper()
	bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err != nil {
		return err
	}
	return hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, specs, nil)
}

func TestWorkflowConnectors_SingleToolEmail(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := createHydrationConnector(t, repo, "mailer", "email", "smtp", emailConnectorValues())

	appConfig := wrappedAppConfig(map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.send.output}}"},
		"nodes": []any{
			map[string]any{"id": "send", "type": "tool", "connector": "mailer"},
		},
	})

	if err := resolveAndHydrate(t, appConfig, repo); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}

	wantTool := "send_email_" + connectorSuffix(conn.ID)
	node := toolNodeList(appConfig, t)[0]
	if node["tool_name"] != wantTool {
		t.Errorf("node tool_name = %v, want %q", node["tool_name"], wantTool)
	}
	if _, present := node["connector"]; present {
		t.Error("node still carries symbolic 'connector' after hydration")
	}
	if _, present := node["tool"]; present {
		t.Error("node still carries symbolic 'tool' after hydration")
	}

	tools, _ := appConfig["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	entry := tools[0].(map[string]any)
	if entry["tool_name"] != wantTool {
		t.Errorf("emitted tool_name = %v, want %q", entry["tool_name"], wantTool)
	}
	toolConfig := entry["tool_config"].(map[string]any)
	if toolConfig["smtp_host"] != "smtp.example.com" {
		t.Errorf("tool_config smtp_host = %v, want connector value", toolConfig["smtp_host"])
	}
}

func TestWorkflowConnectors_MultiToolRequiresSelection(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	values := `{"broker_url":"tcp://localhost:55554","broker_vpn":"default","broker_username":"u","broker_password":"p","operations":[{"tool_name":"place_order","topic":"orders/place"},{"tool_name":"cancel_order","topic":"orders/cancel"}]}`
	conn := createHydrationConnector(t, repo, "orders", "event_mesh", "solace", values)

	build := func(node map[string]any) map[string]any {
		return wrappedAppConfig(map[string]any{
			"output_mapping": map[string]any{"result": "{{nodes.call.output}}"},
			"nodes":          []any{node},
		})
	}

	appConfig := build(map[string]any{"id": "call", "type": "tool", "connector": "orders"})
	err := resolveAndHydrate(t, appConfig, repo)
	if err == nil {
		t.Fatal("expected error when 'tool' is omitted for a multi-tool connector")
	}
	for _, want := range []string{"place_order", "cancel_order", "set 'tool'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	appConfig = build(map[string]any{"id": "call", "type": "tool", "connector": "orders", "tool": "cancel_order"})
	if hydrateErr := resolveAndHydrate(t, appConfig, repo); hydrateErr != nil {
		t.Fatalf("resolve+hydrate with tool selection: %v", hydrateErr)
	}
	wantTool := "cancel_order_" + connectorSuffix(conn.ID)
	if got := toolNodeList(appConfig, t)[0]["tool_name"]; got != wantTool {
		t.Errorf("node tool_name = %v, want %q", got, wantTool)
	}
	if tools, _ := appConfig["tools"].([]any); len(tools) != 2 {
		t.Errorf("tools len = %d, want 2 (both operations emitted)", len(tools))
	}

	appConfig = build(map[string]any{"id": "call", "type": "tool", "connector": "orders", "tool": "no_such_op"})
	err = resolveAndHydrate(t, appConfig, repo)
	if err == nil || !strings.Contains(err.Error(), "no_such_op") {
		t.Fatalf("expected error naming the unknown tool, got %v", err)
	}
}

const petstoreSpecTwoOps = `openapi: 3.0.0
info:
  title: Petstore
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        "200":
          description: ok
  /pets/{petId}:
    get:
      operationId: getPetById
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok
`

func apiConnectorValues(extra string) string {
	return `{"base_url":"https://api.example.com","auth_type":"none"` + extra + `}`
}

func apiWorkflowAppConfig(node map[string]any) map[string]any {
	return wrappedAppConfig(map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.fetch.output}}"},
		"nodes":          []any{node},
	})
}

func TestWorkflowConnectors_APIBindsViaAllowList(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := createHydrationConnector(t, repo, "pets-api", "api", "openapi",
		apiConnectorValues(`,"specification_url":"https://specs.example.com/petstore.yaml","allow_list":"listPets"`))

	appConfig := apiWorkflowAppConfig(map[string]any{
		"id": "fetch", "type": "tool", "connector": "pets-api", "tool": "list_pets",
	})
	if err := resolveAndHydrate(t, appConfig, repo); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}
	node := toolNodeList(appConfig, t)[0]
	if node["tool_name"] != "list_pets" {
		t.Errorf("node tool_name = %v, want list_pets (base name, no suffix)", node["tool_name"])
	}
	if _, present := node["connector"]; present {
		t.Error("node still carries symbolic 'connector' after hydration")
	}
	if _, present := node["tool"]; present {
		t.Error("node still carries symbolic 'tool' after hydration")
	}

	tools, _ := appConfig["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want the connector's openapi entry", len(tools))
	}
	entry := tools[0].(map[string]any)
	if entry["tool_type"] != "openapi" {
		t.Errorf("emitted tool_type = %v, want openapi", entry["tool_type"])
	}
	if entry["source_connector_id"] != conn.ID {
		t.Errorf("emitted source_connector_id = %v, want %s", entry["source_connector_id"], conn.ID)
	}
	if entry["specification_url"] != "https://specs.example.com/petstore.yaml" {
		t.Errorf("emitted specification_url = %v, want the connector's URL", entry["specification_url"])
	}

	for _, sel := range []any{"listPets", nil} {
		appConfig = apiWorkflowAppConfig(map[string]any{
			"id": "fetch", "type": "tool", "connector": "pets-api",
		})
		if sel != nil {
			toolNodeList(appConfig, t)[0]["tool"] = sel
		}
		if err := resolveAndHydrate(t, appConfig, repo); err != nil {
			t.Fatalf("resolve+hydrate (selector %v): %v", sel, err)
		}
		if got := toolNodeList(appConfig, t)[0]["tool_name"]; got != "list_pets" {
			t.Errorf("selector %v bound tool_name = %v, want list_pets", sel, got)
		}
	}
}

func TestWorkflowConnectors_APIBindsViaStoredSpec(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := createHydrationConnector(t, repo, "pets-api", "api", "openapi", apiConnectorValues(""))

	specs := newMemResourceStore()
	key := "connectors/" + conn.ID + "/petstore.yaml"
	if err := specs.Put(t.Context(), key, strings.NewReader(petstoreSpecTwoOps)); err != nil {
		t.Fatalf("store spec: %v", err)
	}
	if err := repo.UpdateResourceKey(t.Context(), conn.ID, key, "hash"); err != nil {
		t.Fatalf("set resource key: %v", err)
	}

	appConfig := apiWorkflowAppConfig(map[string]any{
		"id": "fetch", "type": "tool", "connector": "pets-api", "tool": "getPetById",
	})
	if err := resolveAndHydrateWithSpecs(t, appConfig, repo, specs); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}
	if got := toolNodeList(appConfig, t)[0]["tool_name"]; got != "get_pet_by_id" {
		t.Errorf("node tool_name = %v, want get_pet_by_id (runtime snake_case name)", got)
	}

	appConfig = apiWorkflowAppConfig(map[string]any{
		"id": "fetch", "type": "tool", "connector": "pets-api",
	})
	err := resolveAndHydrateWithSpecs(t, appConfig, repo, specs)
	if err == nil {
		t.Fatal("expected error when 'tool' is omitted for a multi-tool api connector")
	}
	for _, want := range []string{"list_pets", "get_pet_by_id", "set 'tool'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	appConfig = apiWorkflowAppConfig(map[string]any{
		"id": "fetch", "type": "tool", "connector": "pets-api", "tool": "no_such_op",
	})
	err = resolveAndHydrateWithSpecs(t, appConfig, repo, specs)
	if err == nil || !strings.Contains(err.Error(), "no_such_op") {
		t.Fatalf("expected error naming the unknown tool, got %v", err)
	}
}

func TestWorkflowConnectors_APIURLOnlyRejected(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "pets-api", "api", "openapi",
		apiConnectorValues(`,"specification_url":"https://specs.example.com/petstore.yaml"`))

	appConfig := apiWorkflowAppConfig(map[string]any{
		"id": "fetch", "type": "tool", "connector": "pets-api", "tool": "list_pets",
	})
	err := resolveAndHydrate(t, appConfig, repo)
	if err == nil {
		t.Fatal("expected URL-only api connector binding to be rejected")
	}
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("error = %T (%v), want *ValidationError", err, err)
	}
	for _, want := range []string{"allow_list", "specification_file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not point the author at %q", err, want)
		}
	}
}

func TestResolveWorkflowConnectorBindings_UnknownConnector(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))

	appConfig := wrappedAppConfig(map[string]any{
		"nodes": []any{
			map[string]any{"id": "send", "type": "tool", "connector": "ghost"},
		},
	})
	_, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err == nil || !strings.Contains(err.Error(), `connector "ghost" not found`) {
		t.Fatalf("expected save-time unknown-connector error, got %v", err)
	}
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("error = %T, want *ValidationError (save-time 422)", err)
	}
}

func TestResolveWorkflowConnectorBindings_RenameStableAcrossEdits(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)
	conn := createHydrationConnector(t, repo, "mailer", "email", "smtp", emailConnectorValues())

	appConfig := wrappedAppConfig(map[string]any{
		"nodes": []any{
			map[string]any{"id": "send", "type": "tool", "connector": "mailer"},
		},
	})
	bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err != nil {
		t.Fatalf("initial resolve: %v", err)
	}

	conn.Name = "corporate-mailer"
	if updateErr := repo.Update(t.Context(), conn); updateErr != nil {
		t.Fatalf("rename connector: %v", updateErr)
	}
	rebound, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, bindings, repo)
	if err != nil {
		t.Fatalf("re-resolve after rename: %v", err)
	}
	if len(rebound) != 1 || rebound[0].ConnectorID != conn.ID || rebound[0].RefName != "mailer" {
		t.Fatalf("rebound = %+v, want the original UUID binding under ref_name mailer", rebound)
	}

	if _, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo); err == nil {
		t.Fatal("fresh resolve of the stale name should fail after rename")
	}
}

func TestResolveWorkflowConnectorBindings_ShapeErrors(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "mailer", "email", "smtp", emailConnectorValues())

	cases := []struct {
		name    string
		body    map[string]any
		wantErr string
	}{
		{
			name:    "connector_refs rejected",
			body:    map[string]any{"connector_refs": []any{"mailer"}},
			wantErr: "no longer supported",
		},
		{
			name: "connector on non-tool node",
			body: map[string]any{
				"nodes": []any{map[string]any{"id": "n", "type": "agent", "connector": "mailer"}},
			},
			wantErr: "only valid on tool nodes",
		},
		{
			name: "empty connector name",
			body: map[string]any{
				"nodes": []any{map[string]any{"id": "n", "type": "tool", "connector": ""}},
			},
			wantErr: "non-empty connector name",
		},
		{
			name: "tool without connector",
			body: map[string]any{
				"nodes": []any{map[string]any{"id": "n", "type": "tool", "tool": "send_email", "tool_name": "x"}},
			},
			wantErr: "requires 'connector'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveWorkflowConnectorBindings(context.Background(), tc.body, nil, repo)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want contains %q", err, tc.wantErr)
			}
			wrapped := map[string]any{"workflow": tc.body}
			if _, err := resolveWorkflowConnectorBindings(context.Background(), wrapped, nil, repo); err == nil {
				t.Fatal("wrapped body must fail the same shape check")
			}
		})
	}
}

func TestHydrateWorkflowConnectors_UnboundNodeRef(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))

	appConfig := wrappedAppConfig(map[string]any{
		"nodes": []any{
			map[string]any{"id": "send", "type": "tool", "connector": "mailer"},
		},
	})
	err := hydrateWorkflowConnectors(t.Context(), appConfig, nil, repo, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatalf("expected unbound-ref error (e.g. connector deleted after save), got %v", err)
	}
}

func TestHydrateWorkflowConnectors_CollisionWithExistingTool(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := createHydrationConnector(t, repo, "mailer", "email", "smtp", emailConnectorValues())

	appConfig := wrappedAppConfig(map[string]any{
		"nodes": []any{
			map[string]any{"id": "send", "type": "tool", "connector": "mailer"},
		},
	})
	appConfig["tools"] = []any{
		map[string]any{"tool_type": "builtin", "tool_name": "send_email_" + connectorSuffix(conn.ID)},
	}
	err := resolveAndHydrate(t, appConfig, repo)
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected collision error, got %v", err)
	}
}

func TestHydrateWorkflowConnectors_MCPWithoutStoredToolListRejected(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "mcp-server", "mcp", "streamable-http", `{"server_url":"http://localhost:9999/mcp"}`)

	appConfig := wrappedAppConfig(map[string]any{
		"nodes": []any{
			map[string]any{"id": "call", "type": "tool", "connector": "mcp-server"},
		},
	})
	err := resolveAndHydrate(t, appConfig, repo)
	if err == nil || !strings.Contains(err.Error(), "re-discover") {
		t.Fatalf("expected an MCP connector with no stored tool list to be rejected with a re-discover hint, got %v", err)
	}
}

func TestHydrateWorkflowConnectors_NoRefsIsNoOp(t *testing.T) {
	t.Parallel()
	appConfig := wrappedAppConfig(map[string]any{
		"nodes": []any{
			map[string]any{"id": "send", "type": "tool", "tool_name": "explicit_tool"},
		},
	})
	if err := hydrateWorkflowConnectors(t.Context(), appConfig, nil, nil, nil, nil); err != nil {
		t.Fatalf("hydrate without refs should be a no-op, got %v", err)
	}
	if _, present := appConfig["tools"]; present {
		t.Error("no-op hydration must not create a tools list")
	}
}

func TestHydrateWorkflowConnectors_StoredConfigUntouched(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "mailer", "email", "smtp", emailConnectorValues())

	stored := `{"output_mapping":{"result":"x"},"nodes":[{"id":"send","type":"tool","connector":"mailer"}]}`
	wf := &store.Workflow{ID: "11111111-2222-3333-4444-555555555555", Name: "wf", AppConfig: stored}

	var userConfig map[string]any
	if err := json.Unmarshal([]byte(wf.AppConfig), &userConfig); err != nil {
		t.Fatalf("parse: %v", err)
	}
	bindings, err := resolveWorkflowConnectorBindings(context.Background(), userConfig, nil, repo)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wrapped := wrapWorkflowAppConfig(wf, "ns", userConfig)
	if err := hydrateWorkflowConnectors(context.Background(), wrapped, bindings, repo, nil, nil); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if wf.AppConfig != stored {
		t.Error("stored appConfig mutated by hydration")
	}
}

func TestReconcilerGenerateDeployableWorkflowYAML(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	connRepo := store.NewConnectorRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	conn := createHydrationConnector(t, connRepo, "mailer", "email", "smtp", emailConnectorValues())

	stored, _ := json.Marshal(map[string]any{
		"output_mapping": map[string]any{"result": "x"},
		"nodes": []any{
			map[string]any{"id": "send", "type": "tool", "connector": "mailer"},
		},
	})
	wf := &store.Workflow{Name: "wf", AppConfig: string(stored), CreatedBy: "u", UpdatedBy: "u"}
	bindings := []store.WorkflowConnectorBinding{{ConnectorID: conn.ID, RefName: "mailer"}}
	if err := workflowRepo.Create(context.Background(), wf, store.WorkflowBindings{Connectors: bindings}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	r := &Reconciler{connectorRepo: connRepo, workflowRepo: workflowRepo, namespace: "ns"}
	yamlStr, err := r.generateDeployableWorkflowYAML(context.Background(), wf)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	wantTool := "send_email_" + connectorSuffix(conn.ID)
	if !strings.Contains(yamlStr, wantTool) {
		t.Errorf("reconciler YAML missing hydrated tool %q:\n%s", wantTool, yamlStr)
	}
	if strings.Contains(yamlStr, "connector: mailer") {
		t.Error("reconciler YAML still carries the symbolic node connector field")
	}
}

func TestReconcilerGenerateDeployableWorkflowYAML_APIStoredSpec(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	connRepo := store.NewConnectorRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	conn := createHydrationConnector(t, connRepo, "pets-api", "api", "openapi", apiConnectorValues(""))

	specs := newMemResourceStore()
	key := "connectors/" + conn.ID + "/petstore.yaml"
	if err := specs.Put(t.Context(), key, strings.NewReader(petstoreSpecTwoOps)); err != nil {
		t.Fatalf("store spec: %v", err)
	}
	if err := connRepo.UpdateResourceKey(t.Context(), conn.ID, key, "hash"); err != nil {
		t.Fatalf("set resource key: %v", err)
	}

	stored, _ := json.Marshal(map[string]any{
		"output_mapping": map[string]any{"result": "x"},
		"nodes": []any{
			map[string]any{"id": "fetch", "type": "tool", "connector": "pets-api", "tool": "getPetById"},
		},
	})
	wf := &store.Workflow{Name: "wf", AppConfig: string(stored), CreatedBy: "u", UpdatedBy: "u"}
	bindings := []store.WorkflowConnectorBinding{{ConnectorID: conn.ID, RefName: "pets-api"}}
	if err := workflowRepo.Create(t.Context(), wf, store.WorkflowBindings{Connectors: bindings}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	r := &Reconciler{connectorRepo: connRepo, workflowRepo: workflowRepo, resourceStore: specs, namespace: "ns"}
	yamlStr, err := r.generateDeployableWorkflowYAML(t.Context(), wf)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(yamlStr, "tool_name: get_pet_by_id") {
		t.Errorf("reconciler YAML missing the spec-enumerated tool name:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, "connector: pets-api") {
		t.Error("reconciler YAML still carries the symbolic node connector field")
	}
}

func TestReconcilerDesiredWorkflows(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)
	deploymentRepo := store.NewWorkflowDeploymentRepo(db)
	ctx := context.Background()

	wf := &store.Workflow{Name: "wf", AppConfig: "{}", CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(ctx, wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	r := &Reconciler{workflowRepo: workflowRepo, workflowDeploymentRepo: deploymentRepo, namespace: "ns", logger: slog.New(slog.DiscardHandler)}
	record := func(action, status, reason string, snapshot *string) {
		t.Helper()
		d := &store.WorkflowDeployment{WorkflowID: wf.ID, Action: action, Status: status, WorkflowConfigSnapshot: snapshot, CreatedBy: "u"}
		if reason != "" {
			d.FailureReason = &reason
		}
		if err := deploymentRepo.Create(ctx, d); err != nil {
			t.Fatalf("record deployment: %v", err)
		}
	}
	want := func() *control.AgentApply {
		t.Helper()
		intents, err := r.newestIntents(ctx, store.IntentKindWorkflow)
		if err != nil {
			t.Fatalf("read the workflow deployments in flight: %v", err)
		}
		rows, err := deploymentRepo.Desired(ctx)
		if err != nil {
			t.Fatalf("read the desired workflow deployments: %v", err)
		}
		return r.desiredWorkflowApply(ctx, wf, intents[wf.ID], rows[wf.ID])
	}
	runs := func(a *control.AgentApply) string {
		if a == nil || len(a.Payloads) == 0 {
			return ""
		}
		name, _ := a.Payloads[0]["name"].(string)
		return name
	}

	if got := want(); got != nil {
		t.Fatalf("a workflow never deployed must not be listed: %+v", got)
	}
	first, second := "apps:\n  - name: first\n    app_config:\n      agent_name: first\n", "apps:\n  - name: second\n    app_config:\n      agent_name: second\n"
	record(DeployActionDeploy, DeployStatusSuccess, "", &first)
	if got := want(); runs(got) != "first" {
		t.Fatalf("want the stored snapshot replayed verbatim, got %+v", got)
	}
	record(DeployActionUpdate, DeployStatusFailed, DeployFailureInvalidConfig, nil)
	if got := want(); runs(got) != "first" {
		t.Fatalf("a redeploy refused before sending must change nothing, got %+v", got)
	}
	record(DeployActionUpdate, DeployStatusFailed, DeployFailureRuntimeError, &second)
	if got := want(); runs(got) != "second" {
		t.Fatalf("a redeploy AWE failed is what should run, got %+v", got)
	}
	record(DeployActionUndeploy, DeployStatusSuccess, "", nil)
	if got := want(); got == nil || !got.Undeploy {
		t.Fatalf("an undeployed workflow must be listed as undeployed, got %+v", got)
	}

	intents := store.NewDeploymentIntentRepo(db)
	pending := store.WorkflowDeployment{ID: idgen.New(), WorkflowID: wf.ID, Action: DeployActionDeploy}
	body, _ := json.Marshal(workflowIntent{Deployment: pending, YAML: second})
	if err := intents.Put(ctx, &store.DeploymentIntent{ID: pending.ID, Kind: store.IntentKindWorkflow, ObjectID: wf.ID, Payload: string(body)}); err != nil {
		t.Fatal(err)
	}
	r.deploys = newDeployLeader(nil, intents, slog.New(slog.DiscardHandler))
	if got := want(); got == nil || got.DeploymentID != pending.ID || runs(got) != "second" {
		t.Fatalf("a deploy in flight newer than every row is what should run, got %+v", got)
	}
}

func TestAPIAllowListToolNames(t *testing.T) {
	t.Parallel()
	if names, listed := apiAllowListToolNames(map[string]any{"allow_list": "getShipment, createShipment,getShipment"}); !listed || !slices.Equal(names, []string{"create_shipment", "get_shipment"}) {
		t.Errorf("got %v, %v, want the sorted tool names without repeats", names, listed)
	}
	for _, values := range []map[string]any{nil, {}, {"allow_list": ""}, {"allow_list": 3}} {
		if names, listed := apiAllowListToolNames(values); listed || names != nil {
			t.Errorf("%v: got %v, %v, want no allow_list", values, names, listed)
		}
	}
}
