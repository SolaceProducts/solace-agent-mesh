// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestResolveWorkflowConnectorBindings_MCPWithoutStoredToolListRejectedAtSave(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "mcp-server", "mcp", "streamable-http",
		`{"server_url":"http://localhost:9999/mcp"}`)

	appConfig := wrappedAppConfig(map[string]any{
		"nodes": []any{
			map[string]any{"id": "call", "type": "tool", "connector": "mcp-server"},
		},
	})
	_, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err == nil {
		t.Fatal("save accepted an mcp-backed tool node that can never deploy")
	}
	if !strings.Contains(err.Error(), "re-discover") {
		t.Errorf("err = %v, want the same reason deploy gives", err)
	}
	if !strings.Contains(err.Error(), `node "call"`) {
		t.Errorf("err = %v, want the offending node named", err)
	}
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Errorf("err is %T, want *ValidationError so the caller answers 422 rather than 500", err)
	}
}

func TestResolveWorkflowConnectorBindings_MultiToolSelectionRejectedAtSave(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	values := `{"broker_url":"tcp://localhost:55554","broker_vpn":"default","broker_username":"u","broker_password":"p","operations":[{"tool_name":"place_order","topic":"orders/place"},{"tool_name":"cancel_order","topic":"orders/cancel"}]}`
	createHydrationConnector(t, repo, "orders", "event_mesh", "solace", values)

	ambiguous := wrappedAppConfig(map[string]any{
		"nodes": []any{map[string]any{"id": "call", "type": "tool", "connector": "orders"}},
	})
	_, err := resolveWorkflowConnectorBindings(t.Context(), ambiguous, nil, repo)
	if err == nil {
		t.Fatal("save accepted an ambiguous multi-tool binding")
	}
	for _, want := range []string{"place_order", "cancel_order", "set 'tool'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}

	selected := wrappedAppConfig(map[string]any{
		"nodes": []any{map[string]any{
			"id": "call", "type": "tool", "connector": "orders", "tool": "place_order",
		}},
	})
	if _, err := resolveWorkflowConnectorBindings(t.Context(), selected, nil, repo); err != nil {
		t.Fatalf("a correctly selected tool must still save: %v", err)
	}
}

func TestResolveWorkflowConnectorBindings_APIStillSavesWithoutSpecStore(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "pets-api", "api", "openapi",
		apiConnectorValues(`,"specification_url":"https://specs.example.com/petstore.yaml","allow_list":"listPets"`))

	appConfig := apiWorkflowAppConfig(map[string]any{
		"id": "fetch", "type": "tool", "connector": "pets-api", "tool": "list_pets",
	})
	if _, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo); err != nil {
		t.Fatalf("api connectors are judged at deploy, not here: %v", err)
	}
}

func TestResolveWorkflowConnectorBindings_ValidBindingUnaffected(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	createHydrationConnector(t, repo, "mailer", "email", "smtp", emailConnectorValues())

	appConfig := wrappedAppConfig(map[string]any{
		"nodes": []any{map[string]any{"id": "send", "type": "tool", "connector": "mailer"}},
	})
	bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
	if err != nil {
		t.Fatalf("a single-tool connector must still save: %v", err)
	}
	if len(bindings) != 1 || bindings[0].RefName != "mailer" {
		t.Errorf("bindings = %+v, want one binding for mailer", bindings)
	}
}
