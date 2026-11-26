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

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func decisionWorkflowBody(alias string) map[string]any {
	return map[string]any{
		"decision": map[string]any{"model_alias": alias},
		"nodes":    []any{map[string]any{"id": "dec", "type": "decision"}},
	}
}

func seedAPIKeyModel(t *testing.T, repo *store.ModelConfigRepo, alias, modelType string) {
	t.Helper()
	apiBase := "https://llm.example.com"
	mc := &store.ModelConfiguration{
		Alias: alias, Provider: "openai", ModelName: "m-1", APIBase: &apiBase,
		ModelAuthType: authTypeAPIKey, ModelAuthConfig: `{"type":"apikey","api_key":"k"}`, ModelParams: "{}",
		ModelType: modelType, CreatedBy: "tester", UpdatedBy: "tester",
	}
	if err := repo.Create(t.Context(), mc); err != nil {
		t.Fatalf("seed model %q: %v", alias, err)
	}
}

func TestWorkflowServiceValidateDecisionModel(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	modelRepo := store.NewModelConfigRepo(db)
	svc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), nil, nil, nil, "test-ns", slog.Default())
	svc.SetModelConfigs(modelRepo)
	seedAPIKeyModel(t, modelRepo, "judge", store.ModelTypeDecision)
	seedAPIKeyModel(t, modelRepo, "chatty", store.ModelTypeChat)

	tests := []struct {
		name      string
		appConfig map[string]any
		wantErr   string
	}{
		{"configured decision model", decisionWorkflowBody("judge"), ""},
		{"chat model", decisionWorkflowBody("chatty"), "needs a model with modelType decision"},
		{"missing alias", decisionWorkflowBody("ghost"), `decision.model_alias "ghost" is not a configured model`},
		{"no decision node", map[string]any{"nodes": []any{map[string]any{"id": "a", "type": "agent"}}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.ValidateDecisionModel(t.Context(), tt.appConfig)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateDecisionModel = %v, want nil", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("ValidateDecisionModel = %v, want a *ValidationError", err)
			}
			if !strings.Contains(ve.Message, tt.wantErr) {
				t.Errorf("message = %q, want it to contain %q", ve.Message, tt.wantErr)
			}
			if ve.Field != fieldAppConfig {
				t.Errorf("field = %q, want %q", ve.Field, fieldAppConfig)
			}
		})
	}
}

func TestWorkflowServiceCreateReportsTheValidateDecisionModelError(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	modelRepo := store.NewModelConfigRepo(db)
	svc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), nil, nil, nil, "test-ns", slog.Default())
	svc.SetModelConfigs(modelRepo)
	seedAPIKeyModel(t, modelRepo, "chatty", store.ModelTypeChat)

	body := decisionWorkflowBody("chatty")
	want := svc.ValidateDecisionModel(t.Context(), body)
	_, got := svc.Create(t.Context(), CreateWorkflowRequest{Name: "triage", Description: "routes tickets", AppConfig: body}, "tester")
	if want == nil || got == nil || got.Error() != want.Error() {
		t.Fatalf("Create err = %v, ValidateDecisionModel err = %v, want the same error", got, want)
	}
}

func TestWorkflowUsesDecisionNodes(t *testing.T) {
	t.Parallel()
	if !WorkflowUsesDecisionNodes(decisionWorkflowBody("judge")) {
		t.Error("a body with a decision node reported false")
	}
	if WorkflowUsesDecisionNodes(map[string]any{"nodes": []any{map[string]any{"id": "a", "type": "agent"}}}) {
		t.Error("a plain agent-node body reported true")
	}
}
