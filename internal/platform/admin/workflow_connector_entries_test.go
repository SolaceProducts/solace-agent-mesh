// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func sqlHydrationConnector(t *testing.T, repo *store.ConnectorRepo, name string) *store.Connector {
	t.Helper()
	return createHydrationConnector(t, repo, name, "sql", "postgres",
		`{"hostname":"localhost","port":"5432","database":"demo","username":"u","password":"p"}`)
}

func builderShapedBody(connectorName string) map[string]any {
	return map[string]any{
		"connectors": []any{
			map[string]any{"name": connectorName, "type": "sql", "subtype": "mysql"},
		},
		"nodes": []any{
			map[string]any{
				"id": "query_customer_data", "type": "tool", "connector": connectorName,
				"input": map[string]any{"query": "SELECT 1"},
			},
		},
	}
}

func TestResolveWorkflowConnectorBindings_RejectsWorkflowLevelEntryForBoundConnector(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	sqlHydrationConnector(t, repo, "Mysql-demo-db")

	withConfig := builderShapedBody("Mysql-demo-db")
	withConfig["connectors"].([]any)[0].(map[string]any)["config"] = map[string]any{"hostname": "db", "database": "demo"}
	for name, appConfig := range map[string]map[string]any{
		"flat":            builderShapedBody("Mysql-demo-db"),
		"wrapped":         wrappedAppConfig(builderShapedBody("Mysql-demo-db")),
		"carrying config": withConfig,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
			if err == nil {
				t.Fatal("save accepted a workflow-level entry the engine cannot build")
			}
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Errorf("err is %T, want *ValidationError so the caller answers 422 rather than 500", err)
			}
			for _, want := range []string{`"Mysql-demo-db"`, `tool node "query_customer_data"`, "workflow-level `connectors:`"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to mention %q", err, want)
				}
			}
		})
	}

	fixed := builderShapedBody("Mysql-demo-db")
	delete(fixed, "connectors")
	bindings, err := resolveWorkflowConnectorBindings(t.Context(), fixed, nil, repo)
	if err != nil {
		t.Fatalf("the node binding alone must save: %v", err)
	}
	if len(bindings) != 1 || bindings[0].RefName != "Mysql-demo-db" {
		t.Errorf("bindings = %+v, want one binding for Mysql-demo-db", bindings)
	}
}

func TestHydrateWorkflowConnectors_DropsWorkflowLevelEntryForBoundConnector(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := sqlHydrationConnector(t, repo, "Mysql-demo-db")
	bindings := []store.WorkflowConnectorBinding{{ConnectorID: conn.ID, RefName: "Mysql-demo-db"}}

	t.Run("entry carrying config", func(t *testing.T) {
		t.Parallel()
		body := builderShapedBody("Mysql-demo-db")
		body["connectors"].([]any)[0].(map[string]any)["config"] = map[string]any{"hostname": "db"}
		appConfig := wrappedAppConfig(body)
		if err := hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil); err != nil {
			t.Fatalf("hydrate: %v", err)
		}
		if got, present := body["connectors"]; present {
			t.Errorf("workflow-level connectors = %v, want the restated sql entry dropped despite its config", got)
		}
	})

	t.Run("only entry", func(t *testing.T) {
		t.Parallel()
		appConfig := wrappedAppConfig(builderShapedBody("Mysql-demo-db"))
		if err := hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil); err != nil {
			t.Fatalf("hydrate: %v", err)
		}
		body := appConfig["workflow"].(map[string]any)
		if got, present := body["connectors"]; present {
			t.Errorf("workflow-level connectors = %v, want the key removed so the engine builds none", got)
		}
		node := toolNodeList(appConfig, t)[0]
		wantTool := "demo_sql_query_" + connectorSuffix(conn.ID)
		if node["tool_name"] != wantTool {
			t.Errorf("tool_name = %v, want %q", node["tool_name"], wantTool)
		}
		tools, _ := appConfig["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("emitted tools = %v, want the connector's one SQL tool", tools)
		}
	})

	t.Run("inline engine connector kept", func(t *testing.T) {
		t.Parallel()
		body := builderShapedBody("Mysql-demo-db")
		inline := map[string]any{"name": "mailer", "type": "email", "config": map[string]any{"smtp_host": "smtp.example.com"}}
		body["connectors"] = append(body["connectors"].([]any), inline)
		appConfig := wrappedAppConfig(body)
		appConfig["connectors"] = []any{map[string]any{"name": "Mysql-demo-db", "type": "sql"}}

		if err := hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil); err != nil {
			t.Fatalf("hydrate: %v", err)
		}
		got, _ := body["connectors"].([]any)
		if len(got) != 1 || got[0].(map[string]any)["name"] != "mailer" {
			t.Errorf("body connectors = %v, want only the inline mailer entry", got)
		}
		if got, present := appConfig["connectors"]; present {
			t.Errorf("app-level connectors = %v, want the restated entry dropped there too", got)
		}
	})
}

func TestE2E_LegacyRestatedConnectorEntry(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	sqlHydrationConnector(t, h.connectorRepo, "e2e-warehouse")

	withOutput := func(body map[string]any) map[string]any {
		body["output_mapping"] = map[string]any{"rows": "{{query_customer_data.output}}"}
		return body
	}
	clean := withOutput(builderShapedBody("e2e-warehouse"))
	delete(clean, "connectors")
	wfID := h.createWorkflow("e2e_restated_connector", clean)

	restated := withOutput(builderShapedBody("e2e-warehouse"))
	_, err := h.workflowSvc.Update(h.ctx, wfID, UpdateWorkflowRequest{
		Name: "e2e_restated_connector", Description: "E2E test workflow for testing", AppConfig: restated,
	}, "e2e-user")
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("Update err = %v (%T), want *ValidationError for the restated entry", err, err)
	}

	wf, err := h.workflowRepo.GetByID(h.ctx, wfID)
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	raw, err := json.Marshal(restated)
	if err != nil {
		t.Fatalf("marshal appConfig: %v", err)
	}
	wf.AppConfig = string(raw)
	connBindings, err := h.workflowRepo.GetConnectorBindings(h.ctx, wfID)
	if err != nil {
		t.Fatalf("get bindings: %v", err)
	}
	if err := h.workflowRepo.Update(h.ctx, wf, store.WorkflowBindings{Connectors: connBindings}); err != nil {
		t.Fatalf("write legacy row: %v", err)
	}

	h.deployWorkflow(wfID)
	h.waitForInstance(workflowInstanceName(wfID), 5*time.Second)
}
