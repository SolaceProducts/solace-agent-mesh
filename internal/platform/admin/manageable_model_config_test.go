// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

func TestHandleManagement_ModelConfigs_Existing(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "planning",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-secret",
		},
	}, "tester"); err != nil {
		t.Fatalf("create: %v", err)
	}

	mp := &ManageablePlatform{
		deps:   &RouteDeps{ModelConfigSvc: svc},
		logger: slog.Default(),
	}

	result, err := mp.HandleManagement(ctx, "get", []string{"model-configs", "planning"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	raw, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if raw["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", raw["model"])
	}
	if raw["api_key"] != "sk-secret" {
		t.Errorf("api_key = %v, want sk-secret (raw form is unredacted)", raw["api_key"])
	}
}

func TestHandleManagement_ModelConfigs_ByID(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "general",
		Provider:  "openai",
		ModelName: "gpt-4o",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-secret",
		},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mp := &ManageablePlatform{
		deps:   &RouteDeps{ModelConfigSvc: svc},
		logger: slog.Default(),
	}

	result, err := mp.HandleManagement(ctx, "get", []string{"model-configs", created.ID}, nil, nil)
	if err != nil {
		t.Fatalf("lookup by ID: %v", err)
	}
	raw, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if raw["model"] != "gpt-4o" {
		t.Errorf("model = %v, want gpt-4o", raw["model"])
	}
}

func TestHandleManagement_ModelConfigs_MissingAlias(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	mp := &ManageablePlatform{
		deps:   &RouteDeps{ModelConfigSvc: svc},
		logger: slog.Default(),
	}

	_, err := mp.HandleManagement(context.Background(), "get", []string{"model-configs", "nope"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing alias")
	}
	if !errors.Is(err, control.ErrResourceNotFound) {
		t.Errorf("want ErrResourceNotFound, got %v", err)
	}
}

func TestHandleManagement_ModelConfigs_NoAliasPath(t *testing.T) {
	t.Parallel()
	mp := &ManageablePlatform{
		deps:   &RouteDeps{},
		logger: slog.Default(),
	}
	_, err := mp.HandleManagement(context.Background(), "get", []string{"model-configs"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing alias path segment")
	}
}

func TestHandleManagement_ModelConfigs_EmptyAlias(t *testing.T) {
	t.Parallel()
	mp := &ManageablePlatform{
		deps:   &RouteDeps{},
		logger: slog.Default(),
	}
	_, err := mp.HandleManagement(context.Background(), "get", []string{"model-configs", ""}, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty alias")
	}
}

func TestHandleManagement_ModelConfigs_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	mp := &ManageablePlatform{
		deps:   &RouteDeps{ModelConfigSvc: svc},
		logger: slog.Default(),
	}
	_, err := mp.HandleManagement(context.Background(), "post", []string{"model-configs", "planning"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for non-GET method on model-configs")
	}
}

func TestHandleManagement_ModelConfigs_RequiresScope(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "planning",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-secret",
		},
	}, "tester"); err != nil {
		t.Fatalf("create: %v", err)
	}
	mp := &ManageablePlatform{
		deps:   &RouteDeps{ModelConfigSvc: svc},
		logger: slog.Default(),
	}

	identity := &auth.Identity{Scopes: []string{"connector:*:read"}}
	_, err := mp.HandleManagement(ctx, "get", []string{"model-configs", "planning"}, nil, identity)
	if err == nil {
		t.Fatal("expected insufficient-scope error")
	}

	identity = &auth.Identity{Scopes: []string{"model_config:*:read"}}
	result, err := mp.HandleManagement(ctx, "get", []string{"model-configs", "planning"}, nil, identity)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["model"] != "gpt-4" {
		t.Errorf("unexpected result: %v", result)
	}
}

func TestHandleManagement_ModelConfigs_ServiceUnavailable(t *testing.T) {
	t.Parallel()
	mp := &ManageablePlatform{
		deps:   &RouteDeps{},
		logger: slog.Default(),
	}
	_, err := mp.HandleManagement(context.Background(), "get", []string{"model-configs", "planning"}, nil, nil)
	if err == nil {
		t.Fatal("expected error when ModelConfigSvc is not wired")
	}
}

func TestHandleManagement_ModelConfigs_AliasWithSlashes(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:      "openai/gpt-4o",
		Provider:   "openai",
		ModelName:  "gpt-4o",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-secret"},
	}, "tester"); err != nil {
		t.Fatalf("create: %v", err)
	}
	mp := &ManageablePlatform{deps: &RouteDeps{ModelConfigSvc: svc}, logger: slog.Default()}

	result, err := mp.HandleManagement(ctx, "get", []string{"model-configs", "openai", "gpt-4o"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if raw, _ := result.(map[string]any); raw["model"] != "gpt-4o" {
		t.Fatalf("result = %v, want the openai/gpt-4o row", result)
	}
}
