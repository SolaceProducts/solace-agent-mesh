// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestManagementEndpoints(t *testing.T) {
	exe := NewExecutor(newTestServices())
	endpoints := exe.ManagementEndpoints()

	if len(endpoints) != 12 {
		t.Fatalf("expected 12 endpoints, got %d", len(endpoints))
	}

	expected := map[string]string{
		"get:instances":                             "List all instances",
		"get:instances/{name}":                      "Get instance details",
		"post:instances":                            "Add and start a new instance",
		"delete:instances/{name}":                   "Stop and remove an instance",
		"patch:instances/{name}":                    "Enable/disable (start/stop) an instance",
		"get:instances/{name}/config":               "Inspect an instance's current config",
		"patch:instances/{name}/config":             "Hot-update an instance's config (sparse)",
		"get:bindings":                              "Inspect the AWE's current pod-level bindings",
		"patch:bindings":                            "Upsert pod-level bindings (sparse)",
		"post:instances/{name}/toolAuth/query":      "List tool auth requirements and stored-credential status for a user",
		"post:instances/{name}/toolAuth/begin":      "Begin a delegated OAuth flow for one credential on a user's behalf",
		"post:instances/{name}/toolAuth/disconnect": "Remove a user's stored credential so the next run must authorize again",
	}
	for _, ep := range endpoints {
		key := ep.Method + ":" + ep.Path
		if _, ok := expected[key]; !ok {
			t.Errorf("unexpected endpoint: %s %s", ep.Method, ep.Path)
		}
	}
}

func TestHandleManagementEmptyPath(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "get", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestHandleManagementUnknownResource(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "get", []string{"unknown"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown resource")
	}
}

func TestHandleManagementUnsupportedMethodPath(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "put", []string{"instances"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for unsupported method")
	}
}

func TestHandleManagementGetInstances(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("agent-a", "agent"))
	exe.AddInstance(newMockInstance("agent-b", "agent"))

	result, err := exe.HandleManagement(context.Background(), "get", []string{"instances"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	list, ok := result.([]InstanceInfo)
	if !ok {
		t.Fatalf("expected []InstanceInfo, got %T", result)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(list))
	}
}

func TestHandleManagementGetSingleInstance(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("my-agent", "agent"))

	result, err := exe.HandleManagement(context.Background(), "get", []string{"instances", "my-agent"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	info, ok := result.(*InstanceInfo)
	if !ok {
		t.Fatalf("expected *InstanceInfo, got %T", result)
	}
	if info.Name != "my-agent" {
		t.Errorf("name = %q, want %q", info.Name, "my-agent")
	}
}

func TestHandleManagementGetSingleInstanceNotFound(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "get", []string{"instances", "nonexistent"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent instance")
	}
}

func TestHandleManagementPostInstance(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})

	body, _ := json.Marshal(map[string]any{
		"kind": "agent",
		"name": "new-agent",
	})

	result, err := exe.HandleManagement(context.Background(), "post", []string{"instances"}, body, nil)
	if err != nil {
		t.Fatal(err)
	}

	info, ok := result.(*InstanceInfo)
	if !ok {
		t.Fatalf("expected *InstanceInfo, got %T", result)
	}
	if info.Name != "new-agent" {
		t.Errorf("name = %q, want %q", info.Name, "new-agent")
	}
	if info.Status != StatusRunning {
		t.Errorf("status = %q, want %q", info.Status, StatusRunning)
	}
}

func TestHandleManagementPostInstanceEmptyBody(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "post", []string{"instances"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestHandleManagementPostInstanceInvalidJSON(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "post", []string{"instances"}, json.RawMessage(`not json`), nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleManagementDeleteInstance(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("doomed", "agent")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	_, err := exe.HandleManagement(context.Background(), "delete", []string{"instances", "doomed"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(exe.Instances()) != 0 {
		t.Errorf("expected 0 instances after delete, got %d", len(exe.Instances()))
	}
	if m.stopCalled.Load() != 1 {
		t.Error("expected stop to be called on removed instance")
	}
}

func TestHandleManagementDeleteInstance_ColdCacheSucceeds(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "delete", []string{"instances", "nonexistent"}, nil, nil)
	if err != nil {
		t.Fatalf("DELETE on cold cache should succeed (idempotent deprovision), got: %v", err)
	}
}

func TestHandleManagementPatchInstanceEnable(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("stopped-agent", "agent")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusStopped
	exe.instances[0].enabled = false
	exe.mu.Unlock()

	enabled := true
	body, _ := json.Marshal(patchRequest{Enabled: &enabled})

	result, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "stopped-agent"}, body, nil)
	if err != nil {
		t.Fatal(err)
	}

	info, ok := result.(*InstanceInfo)
	if !ok {
		t.Fatalf("expected *InstanceInfo, got %T", result)
	}
	if !info.Enabled {
		t.Error("expected instance to be enabled")
	}
	if info.Status != StatusRunning {
		t.Errorf("status = %q, want %q", info.Status, StatusRunning)
	}
	if m.startCalled.Load() != 1 {
		t.Error("expected start to be called")
	}
}

func TestHandleManagementPatchInstanceDisable(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("running-agent", "agent")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.instances[0].enabled = true
	exe.mu.Unlock()

	enabled := false
	body, _ := json.Marshal(patchRequest{Enabled: &enabled})

	result, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "running-agent"}, body, nil)
	if err != nil {
		t.Fatal(err)
	}

	info, ok := result.(*InstanceInfo)
	if !ok {
		t.Fatalf("expected *InstanceInfo, got %T", result)
	}
	if info.Enabled {
		t.Error("expected instance to be disabled")
	}
	if info.Status != StatusStopped {
		t.Errorf("status = %q, want %q", info.Status, StatusStopped)
	}
	if m.stopCalled.Load() != 1 {
		t.Error("expected stop to be called")
	}
}

func TestHandleManagementPatchEmptyBody(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("a", "agent"))

	_, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "a"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestHandleManagementPatchInvalidJSON(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("a", "agent"))

	_, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "a"}, json.RawMessage(`bad`), nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleManagementPatchMissingEnabledField(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("a", "agent"))

	body, _ := json.Marshal(map[string]any{"other": "field"})
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "a"}, body, nil)
	if err == nil {
		t.Fatal("expected error when 'enabled' field missing")
	}
}

func TestParseRemoveInstanceBody(t *testing.T) {
	cases := []struct {
		name     string
		body     json.RawMessage
		wantDepr bool
		wantErr  bool
	}{
		{"empty body", nil, true, false},
		{"empty object", json.RawMessage(`{}`), true, false},
		{"deprovision null", json.RawMessage(`{"deprovision":null}`), true, false},
		{"deprovision false", json.RawMessage(`{"deprovision":false}`), false, false},
		{"deprovision true", json.RawMessage(`{"deprovision":true}`), true, false},
		{"malformed JSON", json.RawMessage(`not-json`), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRemoveInstanceBody(tc.body)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantDepr {
				t.Errorf("deprovision = %v, want %v", got, tc.wantDepr)
			}
		})
	}
}

func TestHandleManagement_DeleteMalformedBody_ReturnsError(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "delete",
		[]string{"instances", "x"}, json.RawMessage(`{"deprovision":`), nil)
	if err == nil {
		t.Fatal("expected error for malformed delete body")
	}
}

func TestHandleManagementPostInstanceNoEnvExpansion(t *testing.T) {
	t.Setenv("SECRET_TOKEN", "super-secret-value")

	exe := NewExecutor(newTestServices())
	var capturedCfg config.Config
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		capturedCfg = cfg
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})

	body := json.RawMessage(`{"kind":"agent","name":"test","token":"${SECRET_TOKEN}"}`)
	_, err := exe.HandleManagement(context.Background(), "post", []string{"instances"}, body, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := capturedCfg.GetString("token")
	if got != "${SECRET_TOKEN}" {
		t.Errorf("token = %q, want literal ${SECRET_TOKEN} (env var should NOT be expanded)", got)
	}
	if got == "super-secret-value" {
		t.Error("env var was expanded in HTTP body — security vulnerability")
	}
}
