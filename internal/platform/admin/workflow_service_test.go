// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

func TestWorkflowService_BuildResponse_RuntimeStatus(t *testing.T) {
	t.Parallel()

	wf := &store.Workflow{
		ID:        "11111111-2222-3333-4444-555555555555",
		Name:      "demo-workflow",
		AppConfig: "{}",
		CreatedBy: "u",
		UpdatedBy: "u",
	}
	registry := platpkg.NewAgentRegistry()
	svc := &WorkflowService{agentRegistry: registry}

	internalName := "workflow_" + strings.ReplaceAll(wf.ID, "-", "_")

	t.Run("running_when_registry_has_card", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{Name: internalName})
		resp := svc.buildResponse(wf, DisplayStatusDeployed)
		if resp.RuntimeStatus.Status() != RuntimeStatusRunning {
			t.Errorf("RuntimeStatus = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusRunning)
		}
	})

	t.Run("degraded_when_registry_card_reports_active_problems", func(t *testing.T) {
		registry.Clear()
		registry.AddOrUpdate(a2a.AgentCard{
			Name: internalName,
			Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
				a2a.HealthProblemsExtension([]health.Problem{{ID: "queue:x", Severity: health.Degraded, Detail: "boom"}}),
			}},
		})
		resp := svc.buildResponse(wf, DisplayStatusDeployed)
		if resp.RuntimeStatus.Status() != RuntimeStatusDegraded {
			t.Errorf("RuntimeStatus.Status() = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusDegraded)
		}
		if len(resp.RuntimeStatus.Problems()) != 1 || resp.RuntimeStatus.Problems()[0].ID != "queue:x" {
			t.Errorf("RuntimeStatus.Problems() = %v, want one problem with id queue:x", resp.RuntimeStatus.Problems())
		}
	})

	t.Run("disconnected_when_deployed_but_no_card", func(t *testing.T) {
		registry.Clear()
		resp := svc.buildResponse(wf, DisplayStatusDeployed)
		if resp.RuntimeStatus.Status() != RuntimeStatusDisconnected {
			t.Errorf("RuntimeStatus = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusDisconnected)
		}
	})

	t.Run("stopped_when_not_deployed", func(t *testing.T) {
		registry.Clear()
		resp := svc.buildResponse(wf, DisplayStatusNotDeployed)
		if resp.RuntimeStatus.Status() != RuntimeStatusStopped {
			t.Errorf("RuntimeStatus = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusStopped)
		}
	})
}

func TestWorkflowService_CreateRejectsUnknownConnector(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), nil, nil, "ns", nil)

	_, err := svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "bad-refs-workflow",
		Description: "workflow whose node references a nonexistent connector",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "x"},
			"nodes": []any{
				map[string]any{"id": "send", "type": "tool", "connector": "ghost"},
			},
		},
	}, "u")
	if err == nil {
		t.Fatal("expected save-time validation error for an unknown connector reference")
	}
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("error = %T (%v), want *ValidationError", err, err)
	}
	if !strings.Contains(vErr.Message, `connector "ghost" not found`) {
		t.Errorf("message %q should name the unknown connector", vErr.Message)
	}
}

func TestWorkflowService_SaveBindsConnectorsToUUIDs(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	connRepo := store.NewConnectorRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	conn := createHydrationConnector(t, connRepo, "mailer", "email", "smtp", emailConnectorValues())

	svc := NewWorkflowService(workflowRepo, store.NewWorkflowDeploymentRepo(db), connRepo, nil, nil, "ns", nil)
	created, err := svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "bind-workflow",
		Description: "workflow bound to the mailer connector",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "x"},
			"nodes": []any{
				map[string]any{"id": "send", "type": "tool", "connector": "mailer"},
			},
		},
	}, "u")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	bindings, err := workflowRepo.GetConnectorBindings(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get bindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].ConnectorID != conn.ID || bindings[0].RefName != "mailer" {
		t.Fatalf("bindings = %+v, want one UUID binding for mailer", bindings)
	}

	if _, updateErr := svc.Update(t.Context(), created.ID, UpdateWorkflowRequest{
		Name:        "bind-workflow",
		Description: "workflow no longer bound to the mailer connector",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "x"},
			"nodes": []any{
				map[string]any{"id": "send", "type": "tool", "tool_name": "explicit_tool"},
			},
		},
	}, "u"); updateErr != nil {
		t.Fatalf("update workflow: %v", updateErr)
	}
	bindings, err = workflowRepo.GetConnectorBindings(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get bindings after update: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("bindings after removing the ref = %+v, want none", bindings)
	}
}

func TestWorkflowService_GetConfigurationKeepsSymbolicRefs(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	connRepo := store.NewConnectorRepo(db)
	createHydrationConnector(t, connRepo, "mailer", "email", "smtp", emailConnectorValues())

	svc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), nil, nil, "ns", nil)
	created, err := svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "mail-workflow",
		Description: "workflow that references the mailer connector",
		AppConfig: map[string]any{
			"output_mapping": map[string]any{"result": "{{nodes.send.output}}"},
			"nodes": []any{
				map[string]any{"id": "send", "type": "tool", "connector": "mailer"},
			},
		},
	}, "u")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	cfg, err := svc.GetConfiguration(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get configuration: %v", err)
	}
	yamlStr := cfg.ConfigurationFile
	if !strings.Contains(yamlStr, "connector: mailer") {
		t.Error("configuration YAML lost the node's symbolic connector field")
	}
	for _, leaked := range []string{"smtp.example.com", "smtp_host", "send_email_"} {
		if strings.Contains(yamlStr, leaked) {
			t.Errorf("configuration YAML leaked hydrated connector data %q:\n%s", leaked, yamlStr)
		}
	}
}

func TestWorkflowService_SaveBindsToolsets(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	seedToolPackage(t, db, "bind-tools", "ready", "{}", "run")

	svc := NewWorkflowService(workflowRepo, store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), toolPackageRepo, nil, "ns", nil)
	created, err := svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "toolset-workflow",
		Description: "workflow bound to the bind-tools toolset",
		AppConfig: map[string]any{
			"toolsets":       []any{"bind-tools", "builtin_artifact_tools"},
			"output_mapping": map[string]any{"result": "x"},
			"nodes": []any{
				map[string]any{"id": "run", "type": "tool", "tool_name": "bind-tools__run"},
			},
		},
	}, "u")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	toolsets, err := workflowRepo.GetToolsets(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get toolsets: %v", err)
	}
	if len(toolsets) != 2 || toolsets[0] != "bind-tools" || toolsets[1] != "builtin_artifact_tools" {
		t.Errorf("toolsets = %v, want [bind-tools builtin_artifact_tools]", toolsets)
	}

	ids, err := toolPackageRepo.GetWorkflowIDsUsingPackage(t.Context(), "bind-tools")
	if err != nil {
		t.Fatalf("get workflows using package: %v", err)
	}
	if len(ids) != 1 || ids[0] != created.ID {
		t.Errorf("GetWorkflowIDsUsingPackage = %v, want [%s]", ids, created.ID)
	}

	if _, err := svc.Update(t.Context(), created.ID, UpdateWorkflowRequest{
		Name:        "toolset-workflow",
		Description: "workflow bound to the bind-tools toolset",
		AppConfig: map[string]any{
			"toolsets":       []any{"builtin_artifact_tools"},
			"output_mapping": map[string]any{"result": "x"},
			"nodes":          []any{},
		},
	}, "u"); err != nil {
		t.Fatalf("update workflow: %v", err)
	}
	if ids, _ := toolPackageRepo.GetWorkflowIDsUsingPackage(t.Context(), "bind-tools"); len(ids) != 0 {
		t.Errorf("bind-tools still lists workflows after the ref was removed: %v", ids)
	}
}

func TestWorkflowService_CreateRejectsUnknownToolset(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), store.NewToolPackageRepo(db), nil, "ns", nil)

	_, err := svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "bad-toolset-workflow",
		Description: "workflow referencing a nonexistent toolset",
		AppConfig: map[string]any{
			"toolsets":       []any{"ghost-tools"},
			"output_mapping": map[string]any{"result": "x"},
			"nodes":          []any{},
		},
	}, "u")
	if err == nil {
		t.Fatal("expected save-time validation error for an unknown toolset reference")
	}
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("error = %T (%v), want *ValidationError", err, err)
	}
	if !strings.Contains(vErr.Message, `unknown toolset "ghost-tools"`) {
		t.Errorf("message %q should name the unknown toolset", vErr.Message)
	}
}

func TestWorkflowService_GetConfigurationKeepsSymbolicToolsets(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)
	seedToolPackage(t, db, "cfg-tools", "ready", "{}", "run")

	svc := NewWorkflowService(workflowRepo, store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), store.NewToolPackageRepo(db), nil, "ns", nil)
	created, err := svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "symbolic-toolset-workflow",
		Description: "workflow whose configuration stays symbolic",
		AppConfig: map[string]any{
			"toolsets":       []any{"cfg-tools"},
			"output_mapping": map[string]any{"result": "x"},
			"nodes": []any{
				map[string]any{"id": "run", "type": "tool", "tool_name": "cfg-tools__run"},
			},
		},
	}, "u")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	cfg, err := svc.GetConfiguration(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get configuration: %v", err)
	}
	if !strings.Contains(cfg.ConfigurationFile, "cfg-tools") || !strings.Contains(cfg.ConfigurationFile, "toolsets:") {
		t.Error("configuration YAML lost the symbolic toolsets list")
	}
	if strings.Contains(cfg.ConfigurationFile, "tool_type: builtin") {
		t.Errorf("configuration YAML contains hydrated toolset tool entries:\n%s", cfg.ConfigurationFile)
	}
}

func TestWorkflowService_UpdateRejectsUnknownToolset(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)
	svc := NewWorkflowService(workflowRepo, store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), store.NewToolPackageRepo(db), nil, "ns", nil)

	created, err := svc.Create(t.Context(), CreateWorkflowRequest{
		Name:        "update-toolset-workflow",
		Description: "workflow later updated with a bad toolset ref",
		AppConfig:   map[string]any{"output_mapping": map[string]any{"result": "x"}, "nodes": []any{}},
	}, "u")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	_, err = svc.Update(t.Context(), created.ID, UpdateWorkflowRequest{
		Name:        "update-toolset-workflow",
		Description: "workflow later updated with a bad toolset ref",
		AppConfig: map[string]any{
			"toolsets":       []any{"ghost-tools"},
			"output_mapping": map[string]any{"result": "x"},
			"nodes":          []any{},
		},
	}, "u")
	if err == nil || !strings.Contains(err.Error(), `unknown toolset "ghost-tools"`) {
		t.Fatalf("err = %v, want unknown-toolset validation error on update", err)
	}
	if toolsets, _ := workflowRepo.GetToolsets(t.Context(), created.ID); len(toolsets) != 0 {
		t.Errorf("rejected update must not persist toolset bindings, got %v", toolsets)
	}
}

func TestWrapWorkflowAppConfig_CardDisplayNameIsStoredName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		userConfig map[string]any
	}{
		{
			name:       "flat shape without a label",
			userConfig: minimalWorkflowBody(nil),
		},
		{
			name:       "flat shape with an authored label",
			userConfig: minimalWorkflowBody(map[string]any{"display_name": "Mule Collection Workflow"}),
		},
		{
			name:       "flat shape with an authored camelCase label",
			userConfig: minimalWorkflowBody(map[string]any{"displayName": "Mule Collection Workflow"}),
		},
		{
			name: "wrapped shape with authored labels at both levels",
			userConfig: map[string]any{
				"display_name": "Top Level Label",
				"workflow":     minimalWorkflowBody(map[string]any{"display_name": "Nested Label"}),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wf := &store.Workflow{ID: "11111111-2222-3333-4444-555555555555", Name: "MuleCollectionWorkflow"}
			wrapped := wrapWorkflowAppConfig(wf, "ns", tt.userConfig)
			if got := wrapped["display_name"]; got != wf.Name {
				t.Errorf("top-level display_name = %v, want %q", got, wf.Name)
			}
			wfCfg, ok := wrapped["workflow"].(map[string]any)
			if !ok {
				t.Fatalf("workflow section missing: %#v", wrapped)
			}
			cfg, err := workflow.ParseWorkflowConfig(wf.Name, "ns", wfCfg)
			if err != nil {
				t.Fatalf("parse wrapped workflow: %v", err)
			}
			if cfg.DisplayName != wf.Name {
				t.Errorf("card display name = %q, want %q", cfg.DisplayName, wf.Name)
			}
		})
	}
}

func TestWrapWorkflowAppConfig_CardDescriptionFromRecord(t *testing.T) {
	t.Parallel()
	stored := "Triages one supplier invoice."
	tests := []struct {
		name       string
		userConfig map[string]any
		want       string
	}{
		{name: "body without a description", userConfig: minimalWorkflowBody(nil), want: stored},
		{name: "authored body description kept", userConfig: minimalWorkflowBody(map[string]any{"description": "Authored."}), want: "Authored."},
		{name: "wrapped shape", userConfig: map[string]any{"workflow": minimalWorkflowBody(nil)}, want: stored},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wf := &store.Workflow{ID: "11111111-2222-3333-4444-555555555555", Name: "InvoiceTriage", Description: &stored}
			wfCfg, ok := wrapWorkflowAppConfig(wf, "ns", tt.userConfig)["workflow"].(map[string]any)
			if !ok {
				t.Fatal("workflow section missing")
			}
			cfg, err := workflow.ParseWorkflowConfig(wf.Name, "ns", wfCfg)
			if err != nil {
				t.Fatalf("parse wrapped workflow: %v", err)
			}
			if cfg.Description != tt.want {
				t.Errorf("card description = %q, want %q", cfg.Description, tt.want)
			}
		})
	}
}

func minimalWorkflowBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"nodes":          []any{map[string]any{"id": "decide", "type": "agent", "agent_name": "MuleCollectionDecider"}},
		"output_mapping": map[string]any{"decision": "{{decide.output}}"},
	}
	maps.Copy(body, extra)
	return body
}
