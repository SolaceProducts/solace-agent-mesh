// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func decisionModelRequest(alias string) *CreateModelConfigRequest {
	return &CreateModelConfigRequest{
		Alias:      alias,
		Provider:   platpkg.ProviderTypeSafe,
		ModelName:  "jev-1.13.0",
		ModelType:  store.ModelTypeDecision,
		AuthConfig: map[string]any{"type": "apikey", "api_key": "ts-secret"},
	}
}

func TestModelConfigService_ModelTypeDefaultsToChat(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)

	resp, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias: "plain-chat", Provider: "openai", ModelName: "gpt-4o",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-test"},
	}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.ModelType != store.ModelTypeChat {
		t.Errorf("ModelType = %q, want chat", resp.ModelType)
	}
	raw, err := svc.GetByAliasOrIDRaw(t.Context(), "plain-chat")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if _, ok := raw[platpkg.RawModelTypeKey]; ok {
		t.Errorf("a chat binding must be unchanged on the wire, got model_type in %v", raw)
	}
}

func TestModelConfigService_DecisionModel(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)

	resp, err := svc.Create(t.Context(), decisionModelRequest("jev"), "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.ModelType != store.ModelTypeDecision {
		t.Errorf("ModelType = %q, want decision", resp.ModelType)
	}
	if resp.APIBase == nil || *resp.APIBase != "https://api.typesafe.ai" {
		t.Errorf("APIBase = %v, want the TypeSafe default", resp.APIBase)
	}
	if resp.AuthConfig["api_key"] == "ts-secret" {
		t.Error("the decision model's key must be redacted in API responses")
	}

	raw, err := svc.GetByAliasOrIDRaw(t.Context(), "jev")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if raw[platpkg.RawModelTypeKey] != store.ModelTypeDecision || raw[platpkg.RawDecisionProviderKey] != platpkg.ProviderTypeSafe ||
		raw["api_key"] != "ts-secret" || raw["api_base"] != "https://api.typesafe.ai" || raw["model"] != "jev-1.13.0" {
		t.Errorf("raw binding = %v", raw)
	}

	_, snapshot, err := svc.GetForSnapshot(t.Context(), "jev")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot["modelType"] != store.ModelTypeDecision {
		t.Errorf("snapshot modelType = %v", snapshot["modelType"])
	}
}

func TestModelConfigService_ModelTypeProviderMismatchRejected(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)

	cases := map[string]struct {
		req     *CreateModelConfigRequest
		wantErr string
	}{
		"decision on a chat-only provider": {
			req: &CreateModelConfigRequest{
				Alias: "bad-1", Provider: "anthropic", ModelName: "claude-sonnet", ModelType: store.ModelTypeDecision,
				AuthConfig: map[string]any{"type": "apikey", "api_key": "k"},
			},
			wantErr: `must use provider "typesafe" or "google_ai_studio" or "openai"`,
		},
		"typesafe as a chat model": {
			req: &CreateModelConfigRequest{
				Alias: "bad-2", Provider: platpkg.ProviderTypeSafe, ModelName: "jev-1.13.0",
				AuthConfig: map[string]any{"type": "apikey", "api_key": "k"},
			},
			wantErr: "serves decision models only",
		},
		"unknown model type": {
			req: &CreateModelConfigRequest{
				Alias: "bad-3", Provider: "openai", ModelName: "gpt-4o", ModelType: "embedding",
				AuthConfig: map[string]any{"type": "apikey", "api_key": "k"},
			},
			wantErr: "modelType",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.Create(t.Context(), tc.req, "tester")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestModelConfigService_ModelTypeIsFixedAtCreation(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	created, err := svc.Create(t.Context(), decisionModelRequest("jev-fixed"), "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	anthropic := "anthropic"
	if _, err = svc.Update(t.Context(), created.ID, &UpdateModelConfigRequest{Provider: &anthropic}, "tester"); err == nil ||
		!strings.Contains(err.Error(), `must use provider "typesafe" or "google_ai_studio" or "openai"`) {
		t.Fatalf("moving a decision model to a chat provider: err = %v, want a refusal", err)
	}
	openai := platpkg.ProviderOpenAI
	if _, err = svc.Update(t.Context(), created.ID, &UpdateModelConfigRequest{Provider: &openai}, "tester"); err == nil ||
		!strings.Contains(err.Error(), "provider cannot change") {
		t.Fatalf("moving a decision model to another decision provider: err = %v, want a refusal", err)
	}

	pinned := "jev-1.13.1"
	updated, err := svc.Update(t.Context(), created.ID, &UpdateModelConfigRequest{ModelName: &pinned}, "tester")
	if err != nil {
		t.Fatalf("pinning a decision model's version: %v", err)
	}
	if updated.ModelType != store.ModelTypeDecision || *updated.ModelName != pinned {
		t.Errorf("updated = %+v", updated)
	}
}

func openAIDecisionModelRequest(alias string) *CreateModelConfigRequest {
	return &CreateModelConfigRequest{
		Alias:      alias,
		Provider:   platpkg.ProviderOpenAI,
		ModelName:  "gpt-6-luna",
		ModelType:  store.ModelTypeDecision,
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-decisions"},
	}
}

func TestModelConfigService_GeminiDecisionModel(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)

	resp, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias: "gemini-judge", Provider: platpkg.ProviderGoogleAIStudio, ModelName: "gemini-decision-1-preview",
		ModelType: store.ModelTypeDecision, AuthConfig: map[string]any{"type": "apikey", "api_key": "gk-decisions"},
	}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.APIBase == nil || *resp.APIBase != "https://generativelanguage.googleapis.com/v1beta" {
		t.Errorf("apiBase = %v, want the Gemini API default", resp.APIBase)
	}
	byAlias, err := svc.GetByAliasRaw(t.Context(), "gemini-judge")
	if err != nil {
		t.Fatalf("GetByAliasRaw: %v", err)
	}
	all, err := svc.ListAllRaw(t.Context())
	if err != nil {
		t.Fatalf("ListAllRaw: %v", err)
	}
	for name, raw := range map[string]map[string]any{"GetByAliasRaw": byAlias, "ListAllRaw": all["gemini-judge"]} {
		if raw["model"] != "gemini-decision-1-preview" || raw[platpkg.RawModelTypeKey] != store.ModelTypeDecision ||
			raw[platpkg.RawDecisionProviderKey] != platpkg.ProviderGoogleAIStudio {
			t.Errorf("%s binding = %v, want the registered name on a google_ai_studio decision binding", name, raw)
		}
	}
}

func TestModelConfigService_OpenAIDecisionModel(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)

	resp, err := svc.Create(t.Context(), openAIDecisionModelRequest("luna"), "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.ModelType != store.ModelTypeDecision || resp.APIBase == nil || *resp.APIBase != "https://api.openai.com/v1" {
		t.Errorf("created = type %q, apiBase %v; want a decision model on the OpenAI default base", resp.ModelType, resp.APIBase)
	}
	raw, err := svc.GetByAliasOrIDRaw(t.Context(), "luna")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	if raw[platpkg.RawModelTypeKey] != store.ModelTypeDecision || raw[platpkg.RawDecisionProviderKey] != platpkg.ProviderOpenAI ||
		raw["model"] != "gpt-6-luna" || raw["api_key"] != "sk-decisions" {
		t.Errorf("raw binding = %v", raw)
	}
}
