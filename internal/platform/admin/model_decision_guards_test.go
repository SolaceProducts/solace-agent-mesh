// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func requireAuthConfigValidationError(t *testing.T, err error) {
	t.Helper()
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *ValidationError", err, err)
	}
	if len(verr.Details["authConfig"]) == 0 || !strings.Contains(verr.Message, "api_key") {
		t.Errorf("validation error = %+v, want it on authConfig naming api_key", verr)
	}
}

func TestDecisionModelCreateRequiresAnAPIKey(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)

	cases := map[string]map[string]any{
		"auth type none":   {"type": "none"},
		"oauth2":           {"type": "oauth2", "client_id": "cid", "client_secret": "cs", "token_url": "https://idp/token"},
		"apikey no key":    {"type": "apikey"},
		"apikey blank":     {"type": "apikey", "api_key": "  "},
		"apikey sentinel":  {"type": "apikey", "api_key": redactedSentinel},
		"key without type": {"api_key": "ts-secret"},
	}
	for name, authConfig := range cases {
		t.Run(name, func(t *testing.T) {
			req := decisionModelRequest("jev-" + strings.ReplaceAll(name, " ", "-"))
			req.AuthConfig = authConfig
			_, err := svc.Create(t.Context(), req, "tester")
			requireAuthConfigValidationError(t, err)
		})
	}

	t.Run("top-level authType none with no authConfig", func(t *testing.T) {
		req := decisionModelRequest("jev-bare")
		req.AuthConfig = nil
		req.AuthType = "none"
		_, err := svc.Create(t.Context(), req, "tester")
		requireAuthConfigValidationError(t, err)
	})

	t.Run("a chat model may still use none", func(t *testing.T) {
		if _, err := svc.Create(t.Context(), &CreateModelConfigRequest{
			Alias: "local-chat", Provider: "openai", ModelName: "gpt-4o", APIBase: "http://localhost:4000",
			AuthConfig: map[string]any{"type": "none"},
		}, "tester"); err != nil {
			t.Fatalf("chat model with no auth: %v", err)
		}
	})
}

func TestDecisionModelUpdateCannotDropTheAPIKey(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	created, err := svc.Create(t.Context(), decisionModelRequest("jev"), "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	none := "none"
	for name, req := range map[string]*UpdateModelConfigRequest{
		"switch to oauth2": {AuthConfig: map[string]any{
			"type": "oauth2", "client_id": "cid", "client_secret": "cs", "token_url": "https://idp/token",
		}},
		"top-level authType none": {AuthType: &none},
	} {
		t.Run(name, func(t *testing.T) {
			_, updateErr := svc.Update(t.Context(), created.ID, req, "tester")
			requireAuthConfigValidationError(t, updateErr)
		})
	}

	stored, err := repo.GetByID(t.Context(), created.ID)
	if err != nil || stored == nil {
		t.Fatalf("reload: %v", err)
	}
	if requireDecisionAPIKey(stored) != nil {
		t.Fatalf("a refused PATCH changed the stored auth: %s", stored.ModelAuthConfig)
	}

	for name, req := range map[string]*UpdateModelConfigRequest{
		"echoed redacted key": {AuthConfig: map[string]any{"type": "apikey", "api_key": redactedSentinel}},
		"rotated key":         {AuthConfig: map[string]any{"type": "apikey", "api_key": "ts-rotated"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Update(t.Context(), created.ID, req, "tester"); err != nil {
				t.Fatalf("PATCH keeping a key: %v", err)
			}
			stored, err := repo.GetByID(t.Context(), created.ID)
			if err != nil || stored == nil {
				t.Fatalf("reload: %v", err)
			}
			if strings.Contains(stored.ModelAuthConfig, redactedSentinel) {
				t.Errorf("the redacted sentinel was stored as the key: %s", stored.ModelAuthConfig)
			}
		})
	}
}

func saveKeylessDecisionModel(t *testing.T, repo *store.ModelConfigRepo, alias string) {
	t.Helper()
	apiBase := "https://api.typesafe.ai"
	if err := repo.Create(t.Context(), &store.ModelConfiguration{
		Alias: alias, Provider: platpkg.ProviderTypeSafe, ModelName: "jev-1.13.0", APIBase: &apiBase,
		ModelAuthType: "none", ModelAuthConfig: `{"type":"none"}`, ModelParams: "{}",
		ModelType: store.ModelTypeDecision, CreatedBy: "tester", UpdatedBy: "tester",
	}); err != nil {
		t.Fatalf("seed keyless decision model: %v", err)
	}
}

func TestWorkflowGuardRequiresAModelAliasForDecisionNodes(t *testing.T) {
	t.Parallel()
	decisionNode := map[string]any{"id": "triage", "type": "decision"}
	agentNode := map[string]any{"id": "desk", "type": "agent", "agent_name": "Desk"}
	svc := &WorkflowService{}
	for name, body := range map[string]map[string]any{
		"flat body":   {"nodes": []any{decisionNode, agentNode}},
		"nested body": {"workflow": map[string]any{"nodes": []any{agentNode, decisionNode}}},
	} {
		err := svc.validateDecisionModel(t.Context(), body)
		if _, ok := errors.AsType[*ValidationError](err); !ok || !strings.Contains(err.Error(), "decision.model_alias") {
			t.Errorf("%s: err = %v, want the missing model_alias reported", name, err)
		}
	}
	if err := svc.validateDecisionModel(t.Context(), map[string]any{"nodes": []any{agentNode}}); err != nil {
		t.Errorf("a workflow with no decision node: err = %v, want it accepted", err)
	}
}

func TestWorkflowGuardRejectsADecisionModelWithoutAKey(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	saveKeylessDecisionModel(t, repo, "keyless")

	err := (&WorkflowService{modelConfigs: repo}).validateDecisionModel(t.Context(), flatDecisionBody("keyless"))
	if _, ok := errors.AsType[*ValidationError](err); !ok || !strings.Contains(err.Error(), "is not a configured model") {
		t.Fatalf("err = %v, want the not-configured refusal", err)
	}
	if raw, err := svc.GetByAliasRaw(t.Context(), "keyless"); err != nil || raw != nil {
		t.Errorf("raw binding = %v, %v; a keyless decision model must not be pushed as configured", raw, err)
	}
}

func TestAgentServiceUpdateRefusesADecisionModel(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	modelRepo := store.NewModelConfigRepo(db)
	modelSvc := NewModelConfigService(modelRepo, slog.Default())
	modelSvc.SetFeatureRegistry(decisionNodesOn())
	if _, err := modelSvc.Create(t.Context(), decisionModelRequest("jev"), "tester"); err != nil {
		t.Fatal(err)
	}
	agentRepo := store.NewAgentRepo(db)
	agentID := createTestAgent(t, agentRepo, "Router", "standard", nil)
	agents := NewAgentService(agentRepo, store.NewAgentDeploymentRepo(db), nil, nil, nil, modelSvc, slog.Default())

	_, err := agents.Update(t.Context(), agentID, UpdateAgentRequest{
		Name: "Router", Description: "routes incoming requests", SystemPrompt: "route things",
		ModelProvider: []string{"jev"},
	}, "tester")
	if err == nil || !strings.Contains(err.Error(), "is a decision model") {
		t.Fatalf("Update err = %v, want the decision-model refusal", err)
	}
}

func TestWorkflowServiceSaveRefusesAChatModelAlias(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	modelRepo := store.NewModelConfigRepo(db)
	decisionSvc := NewModelConfigService(modelRepo, slog.Default())
	decisionSvc.SetFeatureRegistry(decisionNodesOn())
	if _, err := decisionSvc.Create(t.Context(), &CreateModelConfigRequest{
		Alias: "chatty", Provider: "openai", ModelName: "gpt-4o",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk"},
	}, "tester"); err != nil {
		t.Fatal(err)
	}
	workflowRepo := store.NewWorkflowRepo(db)
	workflows := NewWorkflowService(workflowRepo, store.NewWorkflowDeploymentRepo(db), nil, nil, nil, "test-ns", slog.Default())
	workflows.SetModelConfigs(modelRepo)

	body := flatDecisionBody("chatty")
	if _, err := workflows.Create(t.Context(), CreateWorkflowRequest{
		Name: "triage", Description: "routes incoming tickets", AppConfig: body,
	}, "tester"); err == nil || !strings.Contains(err.Error(), "is a chat model") {
		t.Fatalf("Create err = %v, want the chat-model refusal", err)
	}

	existing := &store.Workflow{Name: "triage", AppConfig: `{"nodes":[]}`, CreatedBy: "tester", UpdatedBy: "tester"}
	if err := workflowRepo.Create(t.Context(), existing, store.WorkflowBindings{}); err != nil {
		t.Fatal(err)
	}
	if _, err := workflows.Update(t.Context(), existing.ID, UpdateWorkflowRequest{
		Name: "triage", Description: "routes incoming tickets", AppConfig: body,
	}, "tester"); err == nil || !strings.Contains(err.Error(), "is a chat model") {
		t.Fatalf("Update err = %v, want the chat-model refusal", err)
	}
}
