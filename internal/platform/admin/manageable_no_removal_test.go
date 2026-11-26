// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var controlPostEndpoints = []string{
	WorkflowConnectorToolInputsPath,
	"connectors/lookup/byName",
	"connectors/mcp/discover",
	"connectors/openapi/detectAuth",
	"connectors/openapi/parse",
	"model-grants/{id}/token",
	"tools/describe",
}

var controlGrantWriteEndpoints = []string{
	"put model-grants/{id}",
	"delete model-grants/{id}",
}

func TestManageablePlatform_ServesNoEndpointThatChangesOrRemovesAnything(t *testing.T) {
	t.Parallel()
	mp := testManageablePlatform()
	for _, endpoint := range mp.ManagementEndpoints() {
		if slices.Contains(controlGrantWriteEndpoints, endpoint.Method+" "+endpoint.Path) {
			continue
		}
		switch endpoint.Method {
		case "get":
		case "post":
			if !slices.Contains(controlPostEndpoints, endpoint.Path) {
				t.Errorf("POST %s is not a known read-only or discovery endpoint: confirm it changes and removes nothing, then list it in controlPostEndpoints", endpoint.Path)
			}
		default:
			t.Errorf("%s %s: the control protocol serves only get and the listed post endpoints", strings.ToUpper(endpoint.Method), endpoint.Path)
		}
		if strings.Contains(strings.ToLower(endpoint.Path), "remov") || strings.Contains(strings.ToLower(endpoint.Path), "delete") || strings.Contains(strings.ToLower(endpoint.Description), "delete") {
			t.Errorf("%s %s reads as a removal", endpoint.Method, endpoint.Path)
		}
	}
}

func TestManageablePlatform_NoMethodOrPathRemovesAResource(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := testOpenDB(t)
	logger := slog.Default()
	agentRepo := store.NewAgentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	agentSvc := NewAgentService(agentRepo, store.NewAgentDeploymentRepo(db), connectorRepo, nil, nil, nil, logger)
	created, err := agentSvc.Create(ctx, CreateAgentRequest{
		Name:         "control-protocol-victim",
		Description:  "an agent the control protocol must not be able to remove",
		SystemPrompt: "You are a test agent. This system prompt is long enough to satisfy the validator's minimum length requirement.",
	}, "test-user")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	workflowSvc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), connectorRepo, nil, nil, "test-ns", logger)
	mp := &ManageablePlatform{deps: &RouteDeps{AgentSvc: agentSvc, WorkflowSvc: workflowSvc}, logger: logger}

	body := []byte(`{"builderSessionId":"0197b2c4-0000-7000-8000-00000000000a","resources":[{"kind":"agent","id":"` + created.ID + `"}]}`)
	paths := [][]string{
		{"agents", created.ID},
		{"agents"},
		{"workflows", created.ID},
		{"builder", "removals"},
		{"builder", "builds"},
		{"agents", created.ID, "remove"},
		{"removals"},
	}
	for _, method := range []string{"delete", "put", "patch", "post", "remove", "DELETE"} {
		for _, path := range paths {
			if _, callErr := mp.HandleManagement(ctx, method, path, body, nil); callErr == nil {
				t.Errorf("%s %s succeeded; the control protocol must refuse it", method, strings.Join(path, "/"))
			}
		}
	}

	if _, getErr := agentSvc.GetByID(ctx, created.ID); getErr != nil {
		t.Fatalf("agent after every attempt: %v; want it still stored", getErr)
	}
}
