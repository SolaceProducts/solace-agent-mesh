// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestHandleManagement_ModelCatalog(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := t.Context()
	for _, req := range []*CreateModelConfigRequest{
		{Alias: "general-chat", Provider: "openai", ModelName: "gpt-4o", APIBase: "https://llm.example.com/v1", AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-chat-secret"}},
		{Alias: "jev", Provider: "typesafe", ModelType: "decision", ModelName: "jev-1.13.0", Description: "Routes tickets", AuthConfig: map[string]any{"type": "apikey", "api_key": "ts-decision-secret"}},
	} {
		if _, err := svc.Create(ctx, req, "tester"); err != nil {
			t.Fatalf("create %s: %v", req.Alias, err)
		}
	}
	enabled := features.NewRegistry()
	enabled.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: true})

	t.Run("lists each model's type without its credentials", func(t *testing.T) {
		mp := &ManageablePlatform{deps: &RouteDeps{ModelConfigSvc: svc, FeatureRegistry: enabled}, logger: slog.Default()}
		result, err := mp.HandleManagement(ctx, "get", []string{"models"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		catalog, ok := result.(ModelCatalog)
		if !ok {
			t.Fatalf("result type = %T, want ModelCatalog", result)
		}
		if !catalog.DecisionNodesEnabled {
			t.Error("decisionNodesEnabled = false, want the registry's true")
		}
		types := map[string]string{}
		for _, m := range catalog.Data {
			types[m.Name] = m.ModelType
		}
		if types["jev"] != "decision" || types["general-chat"] != "chat" {
			t.Errorf("model types = %v", types)
		}
		raw, err := json.Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"sk-chat-secret", "ts-decision-secret", "llm.example.com"} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("catalog %s leaks %q", raw, secret)
			}
		}
	})
	t.Run("reports decision nodes off without a registry", func(t *testing.T) {
		mp := &ManageablePlatform{deps: &RouteDeps{ModelConfigSvc: svc}, logger: slog.Default()}
		result, err := mp.HandleManagement(ctx, "get", []string{"models"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.(ModelCatalog).DecisionNodesEnabled {
			t.Error("decisionNodesEnabled = true with no feature registry")
		}
	})
	t.Run("refuses a single-model path, which would invite a credentialed lookup", func(t *testing.T) {
		mp := &ManageablePlatform{deps: &RouteDeps{ModelConfigSvc: svc}, logger: slog.Default()}
		if _, err := mp.HandleManagement(ctx, "get", []string{"models", "jev"}, nil, nil); err == nil {
			t.Fatal("models/jev succeeded, want the catalog-only error")
		}
	})
	t.Run("serves either Builder and refuses other scopes", func(t *testing.T) {
		mp := &ManageablePlatform{deps: &RouteDeps{ModelConfigSvc: svc}, logger: slog.Default()}
		for _, scope := range []string{rbac.ScopeAgentBuilderRead, rbac.ScopeWorkflowBuilderRead} {
			if _, err := mp.HandleManagement(ctx, "get", []string{"models"}, nil, &auth.Identity{Scopes: []string{scope}}); err != nil {
				t.Errorf("scope %s: %v", scope, err)
			}
		}
		_, err := mp.HandleManagement(ctx, "get", []string{"models"}, nil, &auth.Identity{Scopes: []string{rbac.ScopeSkillRead}})
		if err == nil || !strings.Contains(err.Error(), "insufficient permissions") {
			t.Errorf("skill-only identity: err = %v, want insufficient permissions", err)
		}
	})
}
