// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func newTestModelConfigService(t *testing.T) *ModelConfigService {
	t.Helper()
	db := store.TestDB(t)
	repo := store.NewModelConfigRepo(db)
	svc := NewModelConfigService(repo, slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	return svc
}

func TestModelConfigService_CreateAndGet(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:     "general",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-secret-123",
		},
	}, "testuser")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.Alias != "general" {
		t.Errorf("alias = %q, want general", resp.Alias)
	}
	if resp.AuthType != "apikey" {
		t.Errorf("authType = %q, want apikey", resp.AuthType)
	}
	if _, ok := resp.AuthConfig["api_key"]; ok {
		t.Error("api_key should be redacted from response")
	}
	if resp.APIBase == nil || *resp.APIBase != "https://llm.example.com/v1" {
		t.Errorf("api_base = %v", resp.APIBase)
	}

	got, err := svc.GetByAlias(ctx, "general")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != resp.ID {
		t.Errorf("id mismatch: %q vs %q", got.ID, resp.ID)
	}
}

func TestModelConfigService_Create_DuplicateAlias(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	req := CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}
	if _, err := svc.Create(ctx, req, "user1"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.Create(ctx, req, "user1")
	if err == nil {
		t.Fatal("expected conflict error for duplicate alias")
	}
	if _, ok := errors.AsType[*ConflictError](err); !ok {
		t.Errorf("expected ConflictError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Create_Validation(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	tests := []struct {
		name string
		req  CreateModelConfigRequest
	}{
		{"empty alias", CreateModelConfigRequest{Provider: "openai", ModelName: "gpt-4"}},
		{"empty provider", CreateModelConfigRequest{Alias: "test", ModelName: "gpt-4"}},
		{"empty model name", CreateModelConfigRequest{Alias: "test", Provider: "openai"}},
		{"whitespace-only model name", CreateModelConfigRequest{Alias: "test", Provider: "openai", ModelName: "   "}},
		{"tab and non-breaking-space model name", CreateModelConfigRequest{Alias: "test", Provider: "openai", ModelName: "\t\u00a0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Create(ctx, tt.req, "user1")
			if err == nil {
				t.Fatal("expected validation error")
			}
			if _, ok := errors.AsType[*ValidationError](err); !ok {
				t.Errorf("expected ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestModelConfigService_Create_RejectsCRLFHeader(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:     "crlf-create",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		ModelParams: map[string]any{
			"extra_headers": map[string]any{"X-Bad": "v\r\nX-Injected: pwn"},
		},
	}, "user1")
	if err == nil {
		t.Fatal("expected validation error for CRLF header on create, got nil")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Update_RejectsCRLFHeader(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:     "crlf-update",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, UpdateModelConfigRequest{
		ModelParams: map[string]any{
			"extra_headers": map[string]any{"X-Bad": "v\r\nX-Injected: pwn"},
		},
	}, "user2")
	if err == nil {
		t.Fatal("expected validation error for CRLF header on update, got nil")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Update(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:     "general",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-original",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newModel := "gpt-4-turbo"
	resp, err := svc.Update(ctx, created.ID, UpdateModelConfigRequest{
		ModelName: &newModel,
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if resp.ModelName != "gpt-4-turbo" {
		t.Errorf("model = %q, want gpt-4-turbo", resp.ModelName)
	}
	if resp.UpdatedBy != "user2" {
		t.Errorf("updatedBy = %q, want user2", resp.UpdatedBy)
	}

	raw, err := svc.GetRawByAlias(ctx, "general")
	if err != nil {
		t.Fatalf("get raw: %v", err)
	}
	if raw.ModelAuthConfig == "{}" {
		t.Error("auth config should be preserved from create, not empty")
	}
}

func TestModelConfigService_Update_MergeAuthConfig(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:     "test",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":          "oauth2",
			"client_id":     "id123",
			"client_secret": "secret123",
			"token_url":     "https://auth.example.com/token",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"client_id": "new-id",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	raw, err := svc.GetRawByAlias(ctx, "test")
	if err != nil {
		t.Fatalf("get raw: %v", err)
	}
	config := ToRawLiteLLMConfig(raw)
	if config["oauth_client_id"] != "new-id" {
		t.Errorf("oauth_client_id should be updated to new-id, got %v", config["oauth_client_id"])
	}
	if config["oauth_client_secret"] != "secret123" {
		t.Errorf("oauth_client_secret should be preserved, got %v", config["oauth_client_secret"])
	}
}

func TestModelConfigService_Delete(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias: "todelete", Provider: "openai", ModelName: "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deleted, err := svc.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted.Alias != "todelete" {
		t.Errorf("deleted alias = %q, want todelete", deleted.Alias)
	}

	_, err = svc.GetByAlias(ctx, "todelete")
	if err == nil {
		t.Fatal("expected not found after delete")
	}
}

func TestModelConfigService_Delete_NotFound(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	_, err := svc.Delete(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if _, ok := errors.AsType[*NotFoundError](err); !ok {
		t.Errorf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestModelConfigService_CheckStatus(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	status, err := svc.CheckStatus(ctx)
	if err != nil {
		t.Fatalf("check status: %v", err)
	}
	if status.Configured {
		t.Error("should not be configured with no models")
	}

	_, err = svc.Create(ctx, CreateModelConfigRequest{
		Alias: "general", Provider: "openai", ModelName: "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create general: %v", err)
	}

	status, err = svc.CheckStatus(ctx)
	if err != nil {
		t.Fatalf("check status: %v", err)
	}
	if status.Configured {
		t.Error("should not be configured with only general")
	}

	_, err = svc.Create(ctx, CreateModelConfigRequest{
		Alias: "planning", Provider: "openai", ModelName: "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create planning: %v", err)
	}

	status, err = svc.CheckStatus(ctx)
	if err != nil {
		t.Fatalf("check status: %v", err)
	}
	if !status.Configured {
		t.Error("should be configured with both general and planning")
	}
}

func TestModelConfigService_Create_TrimAlias(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:      "  general  ",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.Alias != "general" {
		t.Errorf("alias = %q, want %q", resp.Alias, "general")
	}

	got, err := svc.GetByAlias(ctx, "general")
	if err != nil {
		t.Fatalf("get by alias: %v", err)
	}
	if got.ID != resp.ID {
		t.Errorf("id mismatch: %q vs %q", got.ID, resp.ID)
	}
}

func TestModelConfigService_Create_WhitespaceOnlyAlias(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:      "   ",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err == nil {
		t.Fatal("expected validation error for whitespace-only alias")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Create_DuplicateAfterTrim(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = svc.Create(ctx, CreateModelConfigRequest{
		Alias:      " general ",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err == nil {
		t.Fatal("expected conflict error for trimmed duplicate alias")
	}
	if _, ok := errors.AsType[*ConflictError](err); !ok {
		t.Errorf("expected ConflictError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Update_TrimAlias(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newAlias := "  planning  "
	resp, err := svc.Update(ctx, created.ID, UpdateModelConfigRequest{
		Alias: &newAlias,
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if resp.Alias != "planning" {
		t.Errorf("alias = %q, want %q", resp.Alias, "planning")
	}
}

func TestModelConfigService_Update_WhitespaceOnlyAlias(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	wsAlias := "   "
	_, err = svc.Update(ctx, created.ID, UpdateModelConfigRequest{
		Alias: &wsAlias,
	}, "user2")
	if err == nil {
		t.Fatal("expected validation error for whitespace-only alias")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_RejectsEmptyAPIBase(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	for _, provider := range []string{"ollama", "custom", "azure_openai"} {
		t.Run(provider, func(t *testing.T) {
			_, err := svc.Create(ctx, CreateModelConfigRequest{
				Alias: provider + "-test", Provider: provider, ModelName: "model",
				AuthConfig: map[string]any{"type": "none"},
			}, "user1")
			if err == nil {
				t.Fatal("expected ValidationError for missing api_base")
			}
			if _, ok := errors.AsType[*ValidationError](err); !ok {
				t.Errorf("expected *ValidationError, got %T: %v", err, err)
			}
		})
	}
}

func TestModelConfigService_FillsDefaultAPIBase(t *testing.T) {
	cases := []struct {
		provider   string
		authConfig map[string]any
		expected   string
	}{
		{"openai", map[string]any{"type": "none"}, "https://api.openai.com/v1"},
		{"anthropic", map[string]any{"type": "none"}, "https://api.anthropic.com"},
		{"google_ai_studio", map[string]any{"type": "none"}, "https://generativelanguage.googleapis.com/v1beta"},
		{"vertex_ai", map[string]any{"type": "none", "vertex_location": "europe-west1"}, "https://europe-west1-aiplatform.googleapis.com/v1"},
		{"bedrock", map[string]any{"type": "none", "aws_region_name": "us-west-2"}, "https://bedrock-runtime.us-west-2.amazonaws.com"},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			svc := newTestModelConfigService(t)
			resp, err := svc.Create(context.Background(), CreateModelConfigRequest{
				Alias: tc.provider + "-fill", Provider: tc.provider, ModelName: "m",
				AuthConfig: tc.authConfig,
			}, "user1")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if resp.APIBase == nil || *resp.APIBase != tc.expected {
				t.Errorf("api_base = %v, want %q", resp.APIBase, tc.expected)
			}
		})
	}
}

func TestModelConfigService_Create_TrimsModelName(t *testing.T) {
	svc := newTestModelConfigService(t)

	created, err := svc.Create(context.Background(), CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  "  gpt-4  ",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ModelName != "gpt-4" {
		t.Errorf("ModelName = %q, want %q", created.ModelName, "gpt-4")
	}
}

func TestModelConfigService_Update_ModelNameWhitespace(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, blank := range []string{"", "   "} {
		_, err = svc.Update(ctx, created.ID, UpdateModelConfigRequest{ModelName: &blank}, "user2")
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Errorf("Update(ModelName=%q): expected ValidationError, got %T: %v", blank, err, err)
		}
	}

	padded := " gpt-4o "
	updated, err := svc.Update(ctx, created.ID, UpdateModelConfigRequest{ModelName: &padded}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.ModelName != "gpt-4o" {
		t.Errorf("ModelName = %q, want %q", updated.ModelName, "gpt-4o")
	}
}

func TestModelConfigService_Update_RejectsEmptyAPIBase(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias: "upd", Provider: "custom", ModelName: "my-model",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	empty := ""
	_, err = svc.Update(ctx, created.ID, UpdateModelConfigRequest{APIBase: &empty}, "user1")
	if err == nil {
		t.Fatal("expected ValidationError when Update sets api_base to empty")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Update_RefillsDefaultAPIBase(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias: "refill", Provider: "custom", ModelName: "my-model",
		APIBase:    "https://my-proxy.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	anthropic := "anthropic"
	empty := ""
	resp, err := svc.Update(ctx, created.ID, UpdateModelConfigRequest{
		Provider: &anthropic,
		APIBase:  &empty,
	}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if resp.APIBase == nil || *resp.APIBase != "https://api.anthropic.com" {
		t.Errorf("api_base after switch to anthropic = %v, want anthropic default", resp.APIBase)
	}
}

func TestToRawLiteLLMConfig(t *testing.T) {
	apiBase := "https://api.openai.com/v1"
	mc := &store.ModelConfiguration{
		ID:              "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42",
		ModelName:       "gpt-4",
		APIBase:         &apiBase,
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`,
		ModelParams:     `{"temperature":0.7}`,
	}
	raw := ToRawLiteLLMConfig(mc)
	if raw[samapp.ModelConfigIDKey] != mc.ID {
		t.Errorf("model_config_id = %v, want the configuration's id %s", raw[samapp.ModelConfigIDKey], mc.ID)
	}
	if raw["model"] != "gpt-4" {
		t.Errorf("model = %v", raw["model"])
	}
	if raw["api_base"] != "https://api.openai.com/v1" {
		t.Errorf("api_base = %v", raw["api_base"])
	}
	if raw["api_key"] != "sk-test" {
		t.Errorf("api_key = %v", raw["api_key"])
	}
	if raw["temperature"] != 0.7 {
		t.Errorf("temperature = %v", raw["temperature"])
	}
	if _, ok := raw["type"]; ok {
		t.Error("type should be excluded from raw config")
	}
}

func TestToRawLiteLLMConfig_PrefixesModelForProvider(t *testing.T) {
	apiBase := "https://us-central1-aiplatform.googleapis.com/v1"
	mc := &store.ModelConfiguration{
		Provider:        "vertex_ai",
		ModelName:       "claude-3-haiku@20240307",
		APIBase:         &apiBase,
		ModelAuthConfig: `{"type":"gcp_service_account"}`,
		ModelParams:     `{}`,
	}
	raw := ToRawLiteLLMConfig(mc)
	if raw["model"] != "vertex_ai/claude-3-haiku@20240307" {
		t.Errorf("model = %v, want %q", raw["model"], "vertex_ai/claude-3-haiku@20240307")
	}
}

func TestToRawLiteLLMConfig_OAuthRemapping(t *testing.T) {
	mc := &store.ModelConfiguration{
		ModelName:       "gpt-4",
		ModelAuthConfig: `{"type":"oauth2","client_id":"id123","client_secret":"secret456","token_url":"https://auth.example.com/token","scope":"openai"}`,
		ModelParams:     `{}`,
	}
	raw := ToRawLiteLLMConfig(mc)

	if raw["oauth_client_id"] != "id123" {
		t.Errorf("oauth_client_id = %v, want id123", raw["oauth_client_id"])
	}
	if raw["oauth_client_secret"] != "secret456" {
		t.Errorf("oauth_client_secret = %v, want secret456", raw["oauth_client_secret"])
	}
	if raw["oauth_token_url"] != "https://auth.example.com/token" {
		t.Errorf("oauth_token_url = %v", raw["oauth_token_url"])
	}
	if raw["oauth_scope"] != "openai" {
		t.Errorf("oauth_scope = %v, want openai", raw["oauth_scope"])
	}

	if _, ok := raw["client_id"]; ok {
		t.Error("client_id should not be in raw config (should be oauth_client_id)")
	}
	if _, ok := raw["client_secret"]; ok {
		t.Error("client_secret should not be in raw config (should be oauth_client_secret)")
	}
	if _, ok := raw["type"]; ok {
		t.Error("type should be excluded")
	}
}

func TestModelConfigService_Update_PreservesSecret_OnRedactedSentinel(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:     "redacted-roundtrip",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-original",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": RedactedSentinel,
		},
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	mc, err := svc.GetRawByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get raw: %v", err)
	}
	var stored map[string]any
	if jerr := json.Unmarshal([]byte(mc.ModelAuthConfig), &stored); jerr != nil {
		t.Fatalf("parse stored auth: %v", jerr)
	}
	if stored["api_key"] != "sk-original" {
		t.Errorf("api_key = %v, want sk-original (sentinel must not be persisted)", stored["api_key"])
	}
}

func TestModelConfigService_Update_PreservesSecret_OnEmptyString(t *testing.T) {
	tests := []struct {
		name       string
		authType   string
		storedAuth map[string]any
		emptyPatch map[string]any
		secretKey  string
		wantStored string
	}{
		{
			name:       "apikey",
			authType:   "apikey",
			storedAuth: map[string]any{"type": "apikey", "api_key": "sk-original"},
			emptyPatch: map[string]any{"type": "apikey", "api_key": ""},
			secretKey:  "api_key",
			wantStored: "sk-original",
		},
		{
			name:       "gcp_service_account_vertex_credentials",
			authType:   "gcp_service_account",
			storedAuth: map[string]any{"type": "gcp_service_account", "vertex_credentials": `{"type":"service_account","private_key":"KEY"}`, "vertex_project": "p"},
			emptyPatch: map[string]any{"type": "gcp_service_account", "vertex_credentials": "", "vertex_project": "p"},
			secretKey:  "vertex_credentials",
			wantStored: `{"type":"service_account","private_key":"KEY"}`,
		},
		{
			name:       "oauth2_client_secret",
			authType:   "oauth2",
			storedAuth: map[string]any{"type": "oauth2", "client_id": "cid", "client_secret": "shhh", "token_url": "https://idp/token"},
			emptyPatch: map[string]any{"type": "oauth2", "client_id": "cid", "client_secret": "", "token_url": "https://idp/token"},
			secretKey:  "client_secret",
			wantStored: "shhh",
		},
		{
			name:       "aws_iam_secret_access_key",
			authType:   "aws_iam",
			storedAuth: map[string]any{"type": "aws_iam", "aws_access_key_id": "AKIA", "aws_secret_access_key": "supersecret", "aws_region_name": "us-east-1"},
			emptyPatch: map[string]any{"type": "aws_iam", "aws_access_key_id": "AKIA", "aws_secret_access_key": "", "aws_region_name": "us-east-1"},
			secretKey:  "aws_secret_access_key",
			wantStored: "supersecret",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestModelConfigService(t)
			ctx := t.Context()

			created, err := svc.Create(ctx, CreateModelConfigRequest{
				Alias:      "empty-secret-" + tc.name,
				Provider:   "openai",
				ModelName:  "gpt-4",
				APIBase:    "https://llm.example.com/v1",
				AuthConfig: tc.storedAuth,
			}, "user1")
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			_, err = svc.Update(ctx, created.ID, UpdateModelConfigRequest{
				AuthConfig: tc.emptyPatch,
			}, "user2")
			if err != nil {
				t.Fatalf("update: %v", err)
			}

			mc, err := svc.GetRawByID(ctx, created.ID)
			if err != nil {
				t.Fatalf("get raw: %v", err)
			}
			var stored map[string]any
			if jerr := json.Unmarshal([]byte(mc.ModelAuthConfig), &stored); jerr != nil {
				t.Fatalf("parse stored auth: %v", jerr)
			}
			if stored[tc.secretKey] != tc.wantStored {
				t.Errorf("%s = %q, want %q (empty-string patch must not wipe stored credential)", tc.secretKey, stored[tc.secretKey], tc.wantStored)
			}
		})
	}
}

func TestModelConfigService_Update_NoPanicOnNullStoredAuthConfig(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewModelConfigRepo(db)
	svc := NewModelConfigService(repo, slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	ctx := t.Context()

	apiBase := "https://llm.example.com/v1"
	row := &store.ModelConfiguration{
		ID:              "00000000-0000-0000-0000-000000000099",
		Alias:           "null-direct",
		Provider:        "openai",
		ModelName:       "gpt-4",
		APIBase:         &apiBase,
		ModelAuthType:   "apikey",
		ModelAuthConfig: "null",
		ModelParams:     "{}",
		CreatedBy:       "test",
		UpdatedBy:       "test",
	}
	if err := repo.Create(ctx, row); err != nil {
		t.Fatalf("seed null row: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Update panicked on null stored auth_config: %v", r)
		}
	}()
	_, err := svc.Update(ctx, row.ID, UpdateModelConfigRequest{
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-new"},
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestModelConfigService_CheckStatus_PlaceholderNotConfigured(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewModelConfigRepo(db)
	svc := NewModelConfigService(repo, slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	ctx := context.Background()

	apiBase := "https://llm.example.com/v1"
	for _, alias := range []string{"general", "planning"} {
		if err := repo.Create(ctx, &store.ModelConfiguration{
			Alias:           alias,
			Provider:        modelConfigPlaceholderValue,
			ModelName:       modelConfigPlaceholderValue,
			APIBase:         &apiBase,
			ModelAuthType:   "none",
			ModelAuthConfig: `{"type":"none"}`,
			ModelParams:     "{}",
			CreatedBy:       "seed",
			UpdatedBy:       "seed",
		}); err != nil {
			t.Fatalf("seed placeholder %s: %v", alias, err)
		}
	}

	status, err := svc.CheckStatus(ctx)
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if status.Configured {
		t.Error("configured: true; placeholder rows must not count as configured")
	}
}

func TestModelConfigService_Create_AliasLowercased(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:      "General",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.Alias != "general" {
		t.Errorf("alias = %q, want %q (Create must lowercase)", resp.Alias, "general")
	}

	got, err := svc.GetByAlias(ctx, "general")
	if err != nil {
		t.Fatalf("GetByAlias(general): %v", err)
	}
	if got.ID != resp.ID {
		t.Errorf("id mismatch: %q vs %q", got.ID, resp.ID)
	}
}

func TestModelConfigService_Update_AliasLowercased(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateModelConfigRequest{
		Alias:      "mymodel",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newAlias := "MyModel"
	updated, err := svc.Update(ctx, created.ID, UpdateModelConfigRequest{Alias: &newAlias}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Alias != "mymodel" {
		t.Errorf("updated alias = %q, want %q (Update must lowercase)", updated.Alias, "mymodel")
	}
}

func TestModelConfigService_CheckStatus_CapitalisedAliasCountsAsConfigured(t *testing.T) {
	svc := newTestModelConfigService(t)
	ctx := context.Background()

	for _, alias := range []string{"General", "Planning"} {
		if _, err := svc.Create(ctx, CreateModelConfigRequest{
			Alias:      alias,
			Provider:   "openai",
			ModelName:  "gpt-4",
			APIBase:    "https://llm.example.com/v1",
			AuthConfig: map[string]any{"type": "none"},
		}, "user1"); err != nil {
			t.Fatalf("create %s: %v", alias, err)
		}
	}

	status, err := svc.CheckStatus(ctx)
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if !status.Configured {
		t.Error("configured: false; rows with capitalised aliases must be found by CheckStatus after lowercasing on Create")
	}
}

func TestIsModelConfigured_TreatsBlankAsUnconfigured(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		row  *store.ModelConfiguration
		want bool
	}{
		{"nil row", nil, false},
		{"empty model name", &store.ModelConfiguration{ModelName: ""}, false},
		{"placeholder model name", &store.ModelConfiguration{ModelName: modelConfigPlaceholderValue}, false},
		{"blank model name", &store.ModelConfiguration{ModelName: " \t"}, false},
		{"real model name", &store.ModelConfiguration{ModelName: "gpt-4"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsModelConfigured(tc.row); got != tc.want {
				t.Errorf("IsModelConfigured(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func decisionNodesOn() *features.Registry {
	registry := features.NewRegistry()
	registry.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: true})
	return registry
}

func TestModelConfigService_Create_RejectsKeyWithAzureWorkloadIdentity(t *testing.T) {
	svc := newTestModelConfigService(t)

	_, err := svc.Create(t.Context(), CreateModelConfigRequest{
		Alias:      "azure-wi",
		Provider:   "azure_openai",
		ModelName:  "gpt-4.1-mini",
		APIBase:    "https://sam.openai.azure.com/",
		AuthConfig: map[string]any{"type": AuthTypeAzureWorkloadIdentity, "api_key": "k"},
	}, "user1")

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Details["api_key"] == nil {
		t.Fatalf("Create error = %v, want a ValidationError on api_key", err)
	}
}

func TestModelConfigService_Update_AzureAuthTypeSwitchReplacesConfig(t *testing.T) {
	enableAzureWorkloadIdentity(t)
	cases := []struct {
		name    string
		initial map[string]any
		patch   map[string]any
		want    map[string]any
	}{
		{
			name:    "apikey to azure_workload_identity",
			initial: map[string]any{"type": "apikey", "api_key": "stored-key", "api_version": "2024-10-21"},
			patch:   map[string]any{"type": AuthTypeAzureWorkloadIdentity, "client_id": testAzureClientID, "tenant_id": testAzureTenantID, "api_version": "2024-10-21"},
			want:    map[string]any{"type": AuthTypeAzureWorkloadIdentity, "client_id": testAzureClientID, "tenant_id": testAzureTenantID, "api_version": "2024-10-21"},
		},
		{
			name:    "azure_workload_identity to apikey",
			initial: map[string]any{"type": AuthTypeAzureWorkloadIdentity, "client_id": testAzureClientID, "tenant_id": testAzureTenantID, "api_version": "2024-10-21"},
			patch:   map[string]any{"type": "apikey", "api_key": "new-key"},
			want:    map[string]any{"type": "apikey", "api_key": "new-key"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestModelConfigService(t)
			ctx := t.Context()

			created, err := svc.Create(ctx, CreateModelConfigRequest{
				Alias:      "azure-auth-switch",
				Provider:   "azure_openai",
				ModelName:  "gpt-4.1-mini",
				APIBase:    "https://sam.openai.azure.com/",
				AuthConfig: tc.initial,
			}, "user1")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if _, err = svc.Update(ctx, created.ID, UpdateModelConfigRequest{AuthConfig: tc.patch}, "user2"); err != nil {
				t.Fatalf("update: %v", err)
			}

			mc, err := svc.GetRawByID(ctx, created.ID)
			if err != nil {
				t.Fatalf("get raw: %v", err)
			}
			var stored map[string]any
			if jerr := json.Unmarshal([]byte(mc.ModelAuthConfig), &stored); jerr != nil {
				t.Fatalf("parse stored auth: %v", jerr)
			}
			if !reflect.DeepEqual(stored, tc.want) {
				t.Errorf("stored authConfig = %v, want %v", stored, tc.want)
			}
		})
	}
}

func TestToRawLiteLLMConfig_CarriesTheStoredParamsAsTheirOwnEntry(t *testing.T) {
	mc := &store.ModelConfiguration{
		ID:              "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42",
		ModelName:       "gpt-4",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`,
		ModelParams:     `{"temperature":0.7,"top_p":0.9}`,
	}

	raw := ToRawLiteLLMConfig(mc)

	want := map[string]any{"temperature": 0.7, "top_p": 0.9}
	if got, _ := raw[samapp.ModelParamsKey].(map[string]any); !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want the stored params %v", samapp.ModelParamsKey, raw[samapp.ModelParamsKey], want)
	}
	if raw["temperature"] != 0.7 {
		t.Errorf("flattened temperature = %v, want 0.7: a Python agent reads the params from the top level", raw["temperature"])
	}

	mc.ModelParams = `{}`
	if _, present := ToRawLiteLLMConfig(mc)[samapp.ModelParamsKey]; present {
		t.Error("a model with no stored params carries an empty stored-params entry")
	}
}
