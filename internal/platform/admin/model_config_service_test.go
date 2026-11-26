// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestValidateModelParamHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		params    map[string]any
		wantError bool
	}{
		{"nil params", nil, false},
		{"no headers", map[string]any{"temperature": 0.7}, false},
		{"clean headers", map[string]any{"extra_headers": map[string]any{"X-Correlation-ID": "abc-123"}}, false},
		{"crlf in value", map[string]any{"extra_headers": map[string]any{"X-Bad": "v\r\nX-Injected: pwn"}}, true},
		{"crlf in key", map[string]any{"extra_headers": map[string]any{"X-Bad\r\nX-Injected": "v"}}, true},
		{"crlf in non-string leaf is rejected because it is stringified downstream", map[string]any{"extra_headers": map[string]any{"X-Bad": []any{"v\r\nX-Injected: pwn"}}}, true},
		{"grant id", map[string]any{llm.GrantIDKey: testGrantID}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateModelParamHeaders(tt.params)
			if tt.wantError && err == nil {
				t.Errorf("expected error for %q, got nil", tt.name)
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error for %q: %v", tt.name, err)
			}
		})
	}
}

const testGrantID = "0b6f8c1e-2d4a-4f5b-9c3e-7a1d2e3f4a5b"

func TestModelConfigService_SwitchingToDelegatedDropsTheAPIKey(t *testing.T) {
	t.Parallel()
	svc, repo, grantID := setupModelConfigSvcWithGrant(t)
	ctx := t.Context()
	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias: "signed-in", Provider: "openai", ModelName: "gpt-4o", APIBase: grantAPIBase + "/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-before-sign-in"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	delegated := platpkg.AuthTypeOAuth2Delegated
	if _, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthType:   &delegated,
		AuthConfig: map[string]any{llm.GrantIDKey: grantID},
	}, "user1"); err != nil {
		t.Fatalf("update: %v", err)
	}

	stored, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var authConfig map[string]any
	if err := json.Unmarshal([]byte(stored.ModelAuthConfig), &authConfig); err != nil {
		t.Fatalf("decode stored auth config: %v", err)
	}
	if _, kept := authConfig["api_key"]; kept {
		t.Errorf("stored auth config = %v; the API key survived the switch to %s and would be sent beside the grant's token", authConfig, delegated)
	}
	if stored.ModelAuthType != delegated || authConfig[llm.GrantIDKey] != grantID {
		t.Errorf("stored auth = %q %v, want %s naming the grant", stored.ModelAuthType, authConfig, delegated)
	}
}

func TestModelConfigService_CreateRejectsCRLFHeader(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, &CreateModelConfigRequest{
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
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_UpdateRejectsCRLFHeader(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "crlf-update",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		ModelParams: map[string]any{
			"extra_headers": map[string]any{"X-Bad": "v\r\nX-Injected: pwn"},
		},
	}, "user1")
	if err == nil {
		t.Fatal("expected validation error for CRLF header on update, got nil")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_ModelNameWhitespace(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "blank-name",
		Provider:  "openai",
		ModelName: " \t\u00a0",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("create with whitespace-only modelName: expected *ValidationError, got %T: %v", err, err)
	}

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "padded-name",
		Provider:  "openai",
		ModelName: "  gpt-4  ",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := derefOrEmpty(created.ModelName); got != "gpt-4" {
		t.Errorf("created ModelName = %q, want %q", got, "gpt-4")
	}
	stored, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("read back created model: %v", err)
	}
	if stored.ModelName != "gpt-4" || !isConfigured(stored) {
		t.Errorf("stored ModelName = %q, isConfigured = %v; want %q and configured", stored.ModelName, isConfigured(stored), "gpt-4")
	}

	blank := "   "
	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{ModelName: &blank}, "user1")
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("update with whitespace-only modelName: expected *ValidationError, got %T: %v", err, err)
	}

	padded := " gpt-4o "
	updated, err := svc.Update(ctx, created.ID, &UpdateModelConfigRequest{ModelName: &padded}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := derefOrEmpty(updated.ModelName); got != "gpt-4o" {
		t.Errorf("updated ModelName = %q, want %q", got, "gpt-4o")
	}
}

func setupModelConfigSvc(t *testing.T) (*ModelConfigService, *store.ModelConfigRepo) {
	t.Helper()
	db := testOpenDB(t)

	repo := store.NewModelConfigRepo(db)
	svc := NewModelConfigService(repo, slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	return svc, repo
}

func TestModelConfigService_CreateAndList(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	req := &CreateModelConfigRequest{
		Alias:     "test-model",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-secret",
		},
	}
	resp, err := svc.Create(ctx, req, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("expected ID")
	}
	if resp.Alias != "test-model" {
		t.Errorf("alias = %q, want test-model", resp.Alias)
	}
	if v, _ := resp.AuthConfig["api_key"].(string); v != redactedSentinel {
		t.Errorf("api_key = %q, want %q", v, redactedSentinel)
	}
	if resp.AuthType != "apikey" {
		t.Errorf("authType = %q, want apikey", resp.AuthType)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if v, _ := list[0].AuthConfig["api_key"].(string); v != redactedSentinel {
		t.Errorf("api_key in list = %q, want %q", v, redactedSentinel)
	}
}

func TestModelConfigService_CreateDuplicate(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	req := &CreateModelConfigRequest{
		Alias:     "dup",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}
	if _, err := svc.Create(ctx, req, "user1"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := svc.Create(ctx, req, "user1")
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if _, ok := err.(*ConflictError); !ok {
		t.Errorf("expected ConflictError, got %T: %v", err, err)
	}
}

func TestModelConfigService_GetByAlias(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	req := &CreateModelConfigRequest{
		Alias:     "my-model",
		Provider:  "anthropic",
		ModelName: "claude-sonnet",
		APIBase:   "https://llm.example.com/v1",
	}
	if _, err := svc.Create(ctx, req, "user1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.GetByAlias(ctx, "my-model")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Provider == nil || *got.Provider != "anthropic" {
		t.Errorf("provider = %v, want anthropic", got.Provider)
	}

	_, err = svc.GetByAlias(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("expected NotFoundError, got %T", err)
	}
}

func TestModelConfigService_UpdatePartial(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	req := &CreateModelConfigRequest{
		Alias:     "update-test",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-original",
		},
		ModelParams: map[string]any{
			"temperature": 0.7,
		},
	}
	created, err := svc.Create(ctx, req, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newProvider := "anthropic"
	updated, err := svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		Provider: &newProvider,
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Provider == nil || *updated.Provider != "anthropic" {
		t.Errorf("provider = %v, want anthropic", updated.Provider)
	}
	if updated.ModelName == nil || *updated.ModelName != "gpt-4" {
		t.Errorf("modelName = %v, want gpt-4 (should be preserved)", updated.ModelName)
	}
	if updated.UpdatedBy != "user2" {
		t.Errorf("updatedBy = %q, want user2", updated.UpdatedBy)
	}
}

func TestModelConfigService_UpdateAuthConfigMerge(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	req := &CreateModelConfigRequest{
		Alias:     "merge-test",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-original",
			"extra":   "preserved",
		},
	}
	created, err := svc.Create(ctx, req, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-updated",
		},
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	raw, err := svc.GetByAliasRaw(ctx, "merge-test")
	if err != nil {
		t.Fatalf("get raw: %v", err)
	}
	if raw["api_key"] != "sk-updated" {
		t.Errorf("api_key = %v, want sk-updated", raw["api_key"])
	}
	if raw["extra"] != "preserved" {
		t.Errorf("extra = %v, want preserved (should be merged)", raw["extra"])
	}
}

func TestModelConfigService_Delete(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	req := &CreateModelConfigRequest{
		Alias:     "delete-me",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}
	created, err := svc.Create(ctx, req, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if delErr := svc.Delete(ctx, created.ID); delErr != nil {
		t.Fatalf("delete: %v", delErr)
	}

	err = svc.Delete(ctx, created.ID)
	if err == nil {
		t.Fatal("expected not found error")
	}
	if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("expected NotFoundError, got %T", err)
	}
}

func TestModelConfigService_GetStatus(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	status, err := svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status.Configured {
		t.Error("expected not configured initially")
	}

	if _, createErr := svc.Create(ctx, &CreateModelConfigRequest{
		Alias: "general", Provider: "openai", ModelName: "gpt-4",
		APIBase: "https://llm.example.com/v1",
	}, "user1"); createErr != nil {
		t.Fatalf("create general: %v", err)
	}
	status, err = svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status.Configured {
		t.Error("expected not configured with only general")
	}

	if _, createErr := svc.Create(ctx, &CreateModelConfigRequest{
		Alias: "planning", Provider: "openai", ModelName: "gpt-4-turbo",
		APIBase: "https://llm.example.com/v1",
	}, "user1"); createErr != nil {
		t.Fatalf("create planning: %v", err)
	}
	status, err = svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if !status.Configured {
		t.Error("expected configured with both general and planning")
	}
}

func TestModelConfigService_GetStatus_PlaceholderRowsNotConfigured(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	status, err := svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status.Configured {
		t.Error("placeholder rows must not be reported as configured")
	}
}

func TestModelConfigService_PlaceholderRowsExposedAsNullViaAPI(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, alias := range defaultModelAliases {
		resp, err := svc.GetByAlias(ctx, alias)
		if err != nil {
			t.Fatalf("get %q: %v", alias, err)
		}
		if resp.Provider != nil {
			t.Errorf("%s.Provider = %v, want nil (placeholder must serialize as JSON null)", alias, *resp.Provider)
		}
		if resp.ModelName != nil {
			t.Errorf("%s.ModelName = %v, want nil (placeholder must serialize as JSON null)", alias, *resp.ModelName)
		}
		if resp.Alias != alias {
			t.Errorf("Alias = %q, want %q (alias is not stripped)", resp.Alias, alias)
		}
	}
}

func TestToModelConfigResponse_RealValuesPassThrough(t *testing.T) {
	t.Parallel()
	mc := &store.ModelConfiguration{
		Alias:           "general",
		Provider:        "openai",
		ModelName:       "gpt-4",
		ModelAuthType:   "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`,
		ModelParams:     "{}",
	}
	resp := toModelConfigResponse(mc, true)
	if resp.Provider == nil || *resp.Provider != "openai" {
		t.Errorf("Provider = %v, want pointer to %q", resp.Provider, "openai")
	}
	if resp.ModelName == nil || *resp.ModelName != "gpt-4" {
		t.Errorf("ModelName = %v, want pointer to %q", resp.ModelName, "gpt-4")
	}
}

func TestModelConfigService_GetByAliasRaw_PlaceholderReturnsNil(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, alias := range defaultModelAliases {
		raw, err := svc.GetByAliasRaw(ctx, alias)
		if err != nil {
			t.Fatalf("GetByAliasRaw(%q): %v", alias, err)
		}
		if raw != nil {
			t.Errorf("GetByAliasRaw(%q) = %v, want nil (placeholder rows must look like missing to broker consumers)", alias, raw)
		}
	}
}

func TestModelConfigService_GetByIDRaw_PlaceholderReturnsNil(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	general, err := repo.GetByAlias(ctx, "general")
	if err != nil || general == nil {
		t.Fatalf("get general row: %v", err)
	}

	raw, err := svc.GetByIDRaw(ctx, general.ID)
	if err != nil {
		t.Fatalf("GetByIDRaw: %v", err)
	}
	if raw != nil {
		t.Errorf("GetByIDRaw(placeholder) = %v, want nil", raw)
	}
}

func TestModelConfigService_GetByAliasOrIDRaw_PlaceholderReturnsNil(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	raw, err := svc.GetByAliasOrIDRaw(ctx, "general")
	if err != nil {
		t.Fatalf("GetByAliasOrIDRaw: %v", err)
	}
	if raw != nil {
		t.Errorf("GetByAliasOrIDRaw(placeholder) = %v, want nil", raw)
	}
}

func TestModelConfigService_ListAllRaw_PlaceholderRowsExcluded(t *testing.T) {
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	raw, err := svc.ListAllRaw(ctx)
	if err != nil {
		t.Fatalf("ListAllRaw: %v", err)
	}
	if _, ok := raw["general"]; !ok {
		t.Errorf("ListAllRaw missing general (real config should be included)")
	}
	if _, ok := raw["planning"]; ok {
		t.Errorf("ListAllRaw included planning (placeholder rows must be excluded so reconciler doesn't push model=undefined to AWE)")
	}
}

func TestModelConfigService_RawLiteLLMConfig(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	apiBase := "https://api.openai.com/v1"
	req := &CreateModelConfigRequest{
		Alias:     "raw-test",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   apiBase,
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-secret",
		},
		ModelParams: map[string]any{
			"temperature": 0.7,
		},
	}
	created, err := svc.Create(ctx, req, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	raw, err := svc.GetByAliasRaw(ctx, "raw-test")
	if err != nil {
		t.Fatalf("get raw: %v", err)
	}
	if raw[samapp.ModelConfigIDKey] != created.ID {
		t.Errorf("model_config_id = %v, want the configuration's id %s", raw[samapp.ModelConfigIDKey], created.ID)
	}

	if raw["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", raw["model"])
	}
	if raw["api_base"] != apiBase {
		t.Errorf("api_base = %v, want %s", raw["api_base"], apiBase)
	}
	if raw["api_key"] != "sk-secret" {
		t.Errorf("api_key = %v, want sk-secret", raw["api_key"])
	}
	if raw["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", raw["temperature"])
	}
	if _, ok := raw["type"]; ok {
		t.Error("type field should be stripped from raw config")
	}
}

func TestModelConfigService_RawLiteLLMConfig_PrefixesModelForProvider(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	req := &CreateModelConfigRequest{
		Alias:     "vertex-raw-test",
		Provider:  "vertex_ai",
		ModelName: "claude-3-haiku@20240307",
		AuthConfig: map[string]any{
			"type":            "gcp_service_account",
			"vertex_location": "us-central1",
		},
	}
	if _, err := svc.Create(ctx, req, "user1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	raw, err := svc.GetByAliasRaw(ctx, "vertex-raw-test")
	if err != nil {
		t.Fatalf("get raw: %v", err)
	}
	if raw["model"] != "vertex_ai/claude-3-haiku@20240307" {
		t.Errorf("model = %v, want %q", raw["model"], "vertex_ai/claude-3-haiku@20240307")
	}
}

func TestModelConfigService_Create_TrimAlias(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "  general  ",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
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
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "   ",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err == nil {
		t.Fatal("expected validation error for whitespace-only alias")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Create_DuplicateAfterTrim(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "general",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     " general ",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err == nil {
		t.Fatal("expected conflict error for trimmed duplicate alias")
	}
	if _, ok := err.(*ConflictError); !ok {
		t.Errorf("expected ConflictError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Update_TrimAlias(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "general",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newAlias := "  planning  "
	resp, err := svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
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
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "general",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	wsAlias := "   "
	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
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
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "no-apibase",
		Provider:  "custom",
		ModelName: "my-model",
	}, "user1")
	if err == nil {
		t.Fatal("expected ValidationError for missing apiBase")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_Update_RejectsEmptyAPIBase(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "to-update",
		Provider:  "custom",
		ModelName: "my-model",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	empty := ""
	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{APIBase: &empty}, "user1")
	if err == nil {
		t.Fatal("expected ValidationError when Update sets apiBase to empty")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestModelConfigService_CreateRejectsMalformedAPIBase(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "typo",
		Provider:  "custom",
		ModelName: "my-model",
		APIBase:   "https:lite-llm.mymass.net",
	}, "user1")
	if err == nil {
		t.Fatal("expected ValidationError when Create persists a scheme-no-slashes apiBase")
	}
	validErr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if validErr.Field != "apiBase" {
		t.Errorf("field = %q, want %q", validErr.Field, "apiBase")
	}
}

func TestModelConfigService_UpdateRejectsMalformedAPIBase(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "typo-update",
		Provider:  "custom",
		ModelName: "my-model",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	malformed := "https:lite-llm.mymass.net"
	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{APIBase: &malformed}, "user1")
	if err == nil {
		t.Fatal("expected ValidationError when Update sets apiBase to the scheme-no-slashes typo")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestIsConfigured_RequiresAPIBase(t *testing.T) {
	t.Parallel()
	apiBase := "https://llm.example.com/v1"
	whitespace := "   "

	cases := []struct {
		name string
		row  *store.ModelConfiguration
		want bool
	}{
		{"nil apiBase", &store.ModelConfiguration{Provider: "openai", ModelName: "gpt-4", APIBase: nil}, false},
		{"empty apiBase", &store.ModelConfiguration{Provider: "openai", ModelName: "gpt-4", APIBase: strPtr("")}, false},
		{"whitespace apiBase", &store.ModelConfiguration{Provider: "openai", ModelName: "gpt-4", APIBase: &whitespace}, false},
		{"unresolved env ref model name", &store.ModelConfiguration{Provider: "custom", ModelName: "${LLM_SERVICE_GENERAL_MODEL_NAME}", APIBase: &apiBase}, false},
		{"blank model name", &store.ModelConfiguration{Provider: "openai", ModelName: " \t", APIBase: &apiBase}, false},
		{"blank provider", &store.ModelConfiguration{Provider: " ", ModelName: "gpt-4", APIBase: &apiBase}, false},
		{"real apiBase", &store.ModelConfiguration{Provider: "openai", ModelName: "gpt-4", APIBase: &apiBase}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConfigured(tc.row); got != tc.want {
				t.Errorf("isConfigured(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestModelConfigService_GetByAliasOrIDRaw(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "lookup-test",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	raw, err := svc.GetByAliasOrIDRaw(ctx, "lookup-test")
	if err != nil {
		t.Fatalf("get by alias: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil")
	}

	raw, err = svc.GetByAliasOrIDRaw(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil by ID")
	}

	raw, err = svc.GetByAliasOrIDRaw(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if raw != nil {
		t.Fatal("expected nil for missing")
	}
}

func TestModelConfigService_GetForSnapshot(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "judge",
		Provider:  "anthropic",
		ModelName: "claude-opus-4-7",
		APIBase:   "https://api.example.com",
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-secret",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	raw, snap, err := svc.GetForSnapshot(ctx, "judge")
	if err != nil {
		t.Fatalf("GetForSnapshot: %v", err)
	}
	if raw == nil || snap == nil {
		t.Fatalf("expected non-nil pair, got raw=%v snap=%v", raw, snap)
	}

	if raw["model"] != "claude-opus-4-7" {
		t.Errorf("raw[model] = %v, want claude-opus-4-7", raw["model"])
	}

	for _, secret := range []string{"api_key", "authConfig", "authType"} {
		if _, present := snap[secret]; present {
			t.Errorf("snapshot leaked %q: %v", secret, snap[secret])
		}
	}

	if snap["id"] != resp.ID {
		t.Errorf("snap[id] = %v", snap["id"])
	}
	if snap["alias"] != "judge" {
		t.Errorf("snap[alias] = %v", snap["alias"])
	}
	if snap["modelName"] != "claude-opus-4-7" {
		t.Errorf("snap[modelName] = %v", snap["modelName"])
	}
	if snap["provider"] != "anthropic" {
		t.Errorf("snap[provider] = %v", snap["provider"])
	}
	if got, ok := snap["apiBase"].(*string); !ok || got == nil || *got != "https://api.example.com" {
		t.Errorf("snap[apiBase] = %v", snap["apiBase"])
	}

	mp, ok := snap["modelParams"].(map[string]any)
	if !ok {
		t.Fatalf("snap[modelParams] type = %T, want map[string]any", snap["modelParams"])
	}
	if len(mp) != 0 {
		t.Errorf("snap[modelParams] = %v, want empty map", mp)
	}

	raw, snap, err = svc.GetForSnapshot(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	if raw != nil || snap != nil {
		t.Errorf("expected (nil, nil) for missing, got (%v, %v)", raw, snap)
	}
}

func TestModelConfigService_GetForSnapshot_PlaceholderReturnsNil(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, alias := range defaultModelAliases {
		raw, snap, err := svc.GetForSnapshot(ctx, alias)
		if err != nil {
			t.Fatalf("GetForSnapshot(%q): %v", alias, err)
		}
		if raw != nil || snap != nil {
			t.Errorf("GetForSnapshot(%q) = (%v, %v), want (nil, nil) — placeholder rows must look like missing to eval snapshot consumers", alias, raw, snap)
		}
	}

	general, err := repo.GetByAlias(ctx, "general")
	if err != nil || general == nil {
		t.Fatalf("get general row: %v", err)
	}
	raw, snap, err := svc.GetForSnapshot(ctx, general.ID)
	if err != nil {
		t.Fatalf("GetForSnapshot by ID: %v", err)
	}
	if raw != nil || snap != nil {
		t.Errorf("GetForSnapshot(placeholder ID) = (%v, %v), want (nil, nil)", raw, snap)
	}
}

func TestModelConfigService_ListAllRawDualKey(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	apiBase := "https://api.openai.com/v1"
	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "dual-key-test",
		Provider:  "openai",
		ModelName: "openai/gpt-4",
		APIBase:   apiBase,
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-secret",
		},
	}, "tester")
	if err != nil {
		t.Fatalf("create configured row: %v", err)
	}
	configuredID := resp.ID
	configuredAlias := resp.Alias

	placeholder := &store.ModelConfiguration{
		Alias:           "placeholder-alias",
		Provider:        "undefined",
		ModelName:       "undefined",
		ModelAuthType:   "none",
		ModelAuthConfig: `{"type":"none"}`,
		ModelParams:     "{}",
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}
	if createErr := repo.Create(ctx, placeholder); createErr != nil {
		t.Fatalf("create placeholder row: %v", createErr)
	}

	out, err := svc.ListAllRawDualKey(ctx)
	if err != nil {
		t.Fatalf("ListAllRawDualKey: %v", err)
	}

	aliasCfg, hasAlias := out[configuredAlias]
	idCfg, hasID := out[configuredID]
	if !hasAlias {
		t.Errorf("missing alias key %q in dual-key output", configuredAlias)
	}
	if !hasID {
		t.Errorf("missing id key %q in dual-key output", configuredID)
	}
	if hasAlias && hasID {
		if aliasCfg["model"] != idCfg["model"] {
			t.Errorf("alias-keyed model %q != id-keyed model %q", aliasCfg["model"], idCfg["model"])
		}
		if aliasCfg["model"] != "openai/gpt-4" {
			t.Errorf("model = %q, want openai/gpt-4", aliasCfg["model"])
		}
	}

	if _, has := out["placeholder-alias"]; has {
		t.Errorf("placeholder alias key must not appear in dual-key output")
	}
	if _, has := out[placeholder.ID]; has {
		t.Errorf("placeholder id key must not appear in dual-key output")
	}
}

func storedAuthConfig(t *testing.T, repo *store.ModelConfigRepo, id string) map[string]any {
	t.Helper()
	mc, err := repo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("get model config %s: %v", id, err)
	}
	if mc == nil {
		t.Fatalf("model config %s not found", id)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(mc.ModelAuthConfig), &m); err != nil {
		t.Fatalf("unmarshal auth config: %v", err)
	}
	return m
}

func TestModelConfigService_Update_PreservesSecret_OnRedactedSentinel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		authType     string
		initialAuth  map[string]any
		secretFields []string
	}{
		{
			name:     "apikey",
			authType: "apikey",
			initialAuth: map[string]any{
				"type":    "apikey",
				"api_key": "sk-original",
			},
			secretFields: []string{"api_key"},
		},
		{
			name:     "oauth2",
			authType: "oauth2",
			initialAuth: map[string]any{
				"type":          "oauth2",
				"client_id":     "client-abc",
				"client_secret": "oauth-secret-original",
				"token_url":     "https://auth.example.com/token",
			},
			secretFields: []string{"client_secret"},
		},
		{
			name:     "bearer",
			authType: "bearer",
			initialAuth: map[string]any{
				"type":  "bearer",
				"token": "bearer-token-original",
			},
			secretFields: []string{"token"},
		},
		{
			name:     "basic",
			authType: "basic",
			initialAuth: map[string]any{
				"type":     "basic",
				"username": "alice",
				"password": "basic-password-original",
			},
			secretFields: []string{"password"},
		},
		{
			name:     "aws_iam",
			authType: "aws_iam",
			initialAuth: map[string]any{
				"type":                  "aws_iam",
				"aws_region_name":       "us-east-1",
				"aws_access_key_id":     "AKIAEXAMPLE",
				"aws_secret_access_key": "aws-secret-original",
				"aws_session_token":     "aws-session-original",
			},
			secretFields: []string{"aws_secret_access_key", "aws_session_token"},
		},
		{
			name:     "gcp_service_account",
			authType: "gcp_service_account",
			initialAuth: map[string]any{
				"type":                 "gcp_service_account",
				"vertex_project":       "my-project",
				"vertex_location":      "us-central1",
				"service_account_json": `{"client_email":"sa@example.com","private_key":"original"}`,
			},
			secretFields: []string{"service_account_json"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, repo := setupModelConfigSvc(t)
			ctx := t.Context()

			created, err := svc.Create(ctx, &CreateModelConfigRequest{
				Alias:      "redacted-roundtrip-" + tc.name,
				Provider:   "openai",
				ModelName:  "gpt-4",
				APIBase:    "https://llm.example.com/v1",
				AuthConfig: tc.initialAuth,
			}, "user1")
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			originals := make(map[string]any, len(tc.secretFields))
			pre := storedAuthConfig(t, repo, created.ID)
			for _, f := range tc.secretFields {
				originals[f] = pre[f]
			}

			patchAuth := make(map[string]any, len(tc.initialAuth))
			for k, v := range tc.initialAuth {
				patchAuth[k] = v
			}
			for _, f := range tc.secretFields {
				patchAuth[f] = redactedSentinel
			}
			_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
				AuthConfig: patchAuth,
			}, "user2")
			if err != nil {
				t.Fatalf("update: %v", err)
			}

			got := storedAuthConfig(t, repo, created.ID)
			for _, f := range tc.secretFields {
				if got[f] != originals[f] {
					t.Errorf("%s: stored %q = %v, want %v (the sentinel must NOT be persisted)",
						tc.name, f, got[f], originals[f])
				}
				if got[f] == redactedSentinel {
					t.Errorf("%s: stored %q = %q — the literal sentinel was persisted, corrupting the secret",
						tc.name, f, redactedSentinel)
				}
			}
		})
	}
}

func TestModelConfigService_Update_PreservesSecret_OnEmptyString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		authType     string
		initialAuth  map[string]any
		secretFields []string
	}{
		{
			name:         "apikey",
			authType:     "apikey",
			initialAuth:  map[string]any{"type": "apikey", "api_key": "sk-original"},
			secretFields: []string{"api_key"},
		},
		{
			name:     "oauth2",
			authType: "oauth2",
			initialAuth: map[string]any{
				"type":          "oauth2",
				"client_id":     "client-abc",
				"client_secret": "oauth-secret-original",
				"token_url":     "https://auth.example.com/token",
			},
			secretFields: []string{"client_secret"},
		},
		{
			name:     "aws_iam",
			authType: "aws_iam",
			initialAuth: map[string]any{
				"type":                  "aws_iam",
				"aws_region_name":       "us-east-1",
				"aws_access_key_id":     "AKIAEXAMPLE",
				"aws_secret_access_key": "aws-secret-original",
				"aws_session_token":     "aws-session-original",
			},
			secretFields: []string{"aws_secret_access_key", "aws_session_token"},
		},
		{
			name:     "gcp_service_account",
			authType: "gcp_service_account",
			initialAuth: map[string]any{
				"type":                 "gcp_service_account",
				"vertex_project":       "my-project",
				"vertex_location":      "us-central1",
				"service_account_json": `{"client_email":"sa@example.com","private_key":"original"}`,
			},
			secretFields: []string{"service_account_json"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, repo := setupModelConfigSvc(t)
			ctx := t.Context()

			created, err := svc.Create(ctx, &CreateModelConfigRequest{
				Alias:      "empty-roundtrip-" + tc.name,
				Provider:   "openai",
				ModelName:  "gpt-4",
				APIBase:    "https://llm.example.com/v1",
				AuthConfig: tc.initialAuth,
			}, "user1")
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			originals := make(map[string]any, len(tc.secretFields))
			pre := storedAuthConfig(t, repo, created.ID)
			for _, f := range tc.secretFields {
				originals[f] = pre[f]
			}

			patchAuth := make(map[string]any, len(tc.initialAuth))
			for k, v := range tc.initialAuth {
				patchAuth[k] = v
			}
			for _, f := range tc.secretFields {
				patchAuth[f] = ""
			}
			_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
				AuthConfig: patchAuth,
			}, "user2")
			if err != nil {
				t.Fatalf("update: %v", err)
			}

			got := storedAuthConfig(t, repo, created.ID)
			for _, f := range tc.secretFields {
				if got[f] != originals[f] {
					t.Errorf("%s: stored %q = %v, want %v (empty-string must NOT wipe the stored secret)",
						tc.name, f, got[f], originals[f])
				}
				if got[f] == "" {
					t.Errorf("%s: stored %q was wiped by an empty-string PATCH", tc.name, f)
				}
			}
		})
	}
}

func TestModelConfigService_Update_AppliesSecret_OnNewValue(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "rotate-key",
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

	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-rotated",
		},
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got := storedAuthConfig(t, repo, created.ID)
	if got["api_key"] != "sk-rotated" {
		t.Errorf("api_key = %v, want sk-rotated (a real new value must replace the stored one)", got["api_key"])
	}
}

func TestModelConfigService_Update_PreservesSecret_WhenFieldOmitted(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "omit-key",
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

	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"type": "apikey",
		},
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got := storedAuthConfig(t, repo, created.ID)
	if got["api_key"] != "sk-original" {
		t.Errorf("api_key = %v, want sk-original (omitted secret must not be cleared)", got["api_key"])
	}
}

func TestModelConfigService_Update_PreservesNonSecretFields(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "structural",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthConfig: map[string]any{
			"type":          "oauth2",
			"client_id":     "client-abc",
			"client_secret": "oauth-secret",
			"token_url":     "https://auth.example.com/token",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"type":          "oauth2",
			"client_secret": redactedSentinel,
			"token_url":     "https://auth.example.com/v2/token",
		},
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	got := storedAuthConfig(t, repo, created.ID)
	if got["client_secret"] != "oauth-secret" {
		t.Errorf("client_secret = %v, want oauth-secret (sentinel must preserve)", got["client_secret"])
	}
	if got["token_url"] != "https://auth.example.com/v2/token" {
		t.Errorf("token_url = %v, want updated url (non-secret must apply)", got["token_url"])
	}
	if got["client_id"] != "client-abc" {
		t.Errorf("client_id = %v, want client-abc (untouched fields must persist)", got["client_id"])
	}
}

func TestModelConfigService_GetModelsFromProviderByID_UsesStoredSecret_OnRedactedSentinel(t *testing.T) {
	t.Parallel()

	var capturedAuth string
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"}]}`))
	}))
	defer providerServer.Close()

	svc, _ := setupModelConfigSvc(t)
	ctx := t.Context()
	listSvc := NewModelListService(nil, slog.Default())

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "stored-key-edit",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   providerServer.URL,
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-stored",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.GetModelsFromProviderByID(ctx, created.ID, listSvc, "openai",
		map[string]any{
			"type":    "apikey",
			"api_key": redactedSentinel,
		}, "")
	if err != nil {
		t.Fatalf("GetModelsFromProviderByID: %v", err)
	}

	if capturedAuth != "Bearer sk-stored" {
		t.Errorf("provider Authorization = %q, want %q (sentinel must not leak upstream)",
			capturedAuth, "Bearer sk-stored")
	}
}

func TestModelConfigService_GetModelsFromProviderByID_FallsBackToDefaultWhenStoredAPIBaseMalformed(t *testing.T) {
	t.Parallel()

	svc, repo := setupModelConfigSvc(t)
	ctx := t.Context()

	malformed := "https:lite-llm.mymass.net"
	if err := repo.Create(ctx, &store.ModelConfiguration{
		Alias:           "stale-base",
		Provider:        "openai",
		ModelName:       "gpt-4",
		ModelAuthType:   "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-stored"}`,
		ModelParams:     "{}",
		APIBase:         &malformed,
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}); err != nil {
		t.Fatalf("seed malformed-apiBase row: %v", err)
	}
	stored, err := repo.GetByAlias(ctx, "stale-base")
	if err != nil || stored == nil {
		t.Fatalf("read back malformed row: err=%v nil=%v", err, stored == nil)
	}

	var capturedURL string
	listSvc := NewModelListService(nil, slog.Default())
	listSvc.httpClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedURL = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-4"}]}`)),
			Header:     http.Header{"Content-Type": {"application/json"}},
		}, nil
	})

	if _, err := svc.GetModelsFromProviderByID(ctx, stored.ID, listSvc, "openai", nil, ""); err != nil {
		t.Fatalf("GetModelsFromProviderByID: %v (stored apiBase was malformed; the service must fall back to the provider default)", err)
	}
	if !strings.Contains(capturedURL, "api.openai.com") {
		t.Errorf("transport saw URL %q, want it rooted at api.openai.com (the openai default)", capturedURL)
	}
	if strings.Contains(capturedURL, "lite-llm.mymass.net") {
		t.Errorf("transport saw URL %q, must not include the malformed stored base", capturedURL)
	}
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestModelConfigService_GetModelsFromProviderByID_AppliesOverride_OnNewValue(t *testing.T) {
	t.Parallel()

	var capturedAuth string
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"}]}`))
	}))
	defer providerServer.Close()

	svc, _ := setupModelConfigSvc(t)
	ctx := t.Context()
	listSvc := NewModelListService(nil, slog.Default())

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "rotated-key-edit",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   providerServer.URL,
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": "sk-stored",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.GetModelsFromProviderByID(ctx, created.ID, listSvc, "openai",
		map[string]any{
			"type":    "apikey",
			"api_key": "sk-typed-by-user",
		}, "")
	if err != nil {
		t.Fatalf("GetModelsFromProviderByID: %v", err)
	}

	if capturedAuth != "Bearer sk-typed-by-user" {
		t.Errorf("provider Authorization = %q, want %q (real override must replace stored)",
			capturedAuth, "Bearer sk-typed-by-user")
	}
}

func TestModelConfigService_Update_FlipsAuthType_DropsOldSecret(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "flip-auth-type",
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

	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"type":          "oauth2",
			"client_id":     "client-abc",
			"client_secret": "oauth-secret",
			"token_url":     "https://auth.example.com/token",
		},
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	stored := storedAuthConfig(t, repo, created.ID)
	if _, present := stored["api_key"]; present {
		t.Errorf("stored auth_config still carries api_key after auth-type flip: %v", stored)
	}
	if stored["client_secret"] != "oauth-secret" {
		t.Errorf("client_secret = %v, want oauth-secret", stored["client_secret"])
	}

	resp, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, present := resp.AuthConfig["api_key"]; present {
		t.Errorf("GET response still returns api_key after auth-type flip: %v", resp.AuthConfig)
	}
}

func TestModelConfigService_Update_DropsNonStringSecret(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "non-string-secret",
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

	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"type":    "apikey",
			"api_key": []any{"sk-array"},
		},
	}, "user2")
	if err != nil {
		t.Fatalf("update with array secret: %v", err)
	}

	stored := storedAuthConfig(t, repo, created.ID)
	if stored["api_key"] != "sk-original" {
		t.Errorf("api_key = %v (type %T), want sk-original (non-string overlay must be dropped)",
			stored["api_key"], stored["api_key"])
	}
}

func TestRedactModelAuthConfig_GCPVertexCredentials(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"type":               "gcp_service_account",
		"vertex_credentials": `{"client_email":"sa@example.com","private_key":"-----BEGIN PRIVATE KEY-----\n..."}`,
		"vertex_project":     "my-project",
		"vertex_location":    "us-central1",
	}
	got := redactModelAuthConfig(input)
	if got["vertex_credentials"] != redactedSentinel {
		t.Errorf("vertex_credentials = %v, want %q (GCP service-account JSON must be redacted)",
			got["vertex_credentials"], redactedSentinel)
	}
	if got["vertex_project"] != "my-project" {
		t.Errorf("vertex_project = %v, want my-project (non-secret must pass through)", got["vertex_project"])
	}
	if got["vertex_location"] != "us-central1" {
		t.Errorf("vertex_location = %v, want us-central1", got["vertex_location"])
	}

	svc, repo := setupModelConfigSvc(t)
	ctx := t.Context()
	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:      "gcp-vertex-roundtrip",
		Provider:   "vertex_ai",
		ModelName:  "gemini-1.5-pro",
		APIBase:    "https://vertex.googleapis.com",
		AuthConfig: input,
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthConfig: map[string]any{
			"type":               "gcp_service_account",
			"vertex_credentials": redactedSentinel,
			"vertex_project":     "my-project-renamed",
		},
	}, "user2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	stored := storedAuthConfig(t, repo, created.ID)
	if stored["vertex_credentials"] != input["vertex_credentials"] {
		t.Errorf("vertex_credentials after sentinel PATCH = %v, want preserved original", stored["vertex_credentials"])
	}
	if stored["vertex_project"] != "my-project-renamed" {
		t.Errorf("vertex_project = %v, want my-project-renamed (non-secret should still apply)", stored["vertex_project"])
	}
}

func TestModelConfigService_Create_AliasLowercased(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
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

	mc, err := repo.GetByAlias(ctx, "general")
	if err != nil || mc == nil {
		t.Fatalf("GetByAlias(general): %v (nil=%v)", err, mc == nil)
	}
	if mc.Alias != "general" {
		t.Errorf("stored alias = %q, want %q", mc.Alias, "general")
	}
}

func TestModelConfigService_Update_AliasLowercased(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
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
	updated, err := svc.Update(ctx, resp.ID, &UpdateModelConfigRequest{Alias: &newAlias}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Alias != "mymodel" {
		t.Errorf("updated alias = %q, want %q (Update must lowercase)", updated.Alias, "mymodel")
	}
}

func TestModelConfigService_Create_DuplicateAliasCaseInsensitive(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	base := &CreateModelConfigRequest{
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "none"},
	}
	base.Alias = "general"
	if _, err := svc.Create(ctx, base, "user1"); err != nil {
		t.Fatalf("first create: %v", err)
	}

	base.Alias = "GENERAL"
	_, err := svc.Create(ctx, base, "user1")
	if err == nil {
		t.Fatal("expected ConflictError for GENERAL after general; got nil")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Errorf("expected ConflictError, got %T: %v", err, err)
	}
}

func TestBuildSupportedModelsRequest_ExtractsAuthConfig(t *testing.T) {
	t.Parallel()

	req := BuildSupportedModelsRequest("bedrock", "https://api.example", map[string]any{
		"api_key":               "sk-live",
		"client_id":             "cid",
		"client_secret":         "csecret",
		"token_url":             "https://token.example/oauth/token",
		"aws_access_key_id":     "AKIA",
		"aws_secret_access_key": "wj",
		"aws_session_token":     "sess",
		"aws_region_name":       "eu-west-1",
		"vertex_credentials":    `{"type":"service_account"}`,
		"vertex_location":       "europe-west4",
	})

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"Provider", req.Provider, "bedrock"},
		{"APIBase", req.APIBase, "https://api.example"},
		{"APIKey", req.APIKey, "sk-live"},
		{"ClientID", req.ClientID, "cid"},
		{"ClientSecret", req.ClientSecret, "csecret"},
		{"TokenURL", req.TokenURL, "https://token.example/oauth/token"},
		{"AWSAccessKeyID", req.AWSAccessKeyID, "AKIA"},
		{"AWSSecretAccessKey", req.AWSSecretAccessKey, "wj"},
		{"AWSSessionToken", req.AWSSessionToken, "sess"},
		{"AWSRegionName", req.AWSRegionName, "eu-west-1"},
		{"VertexCredentials", req.VertexCredentials, `{"type":"service_account"}`},
		{"VertexLocation", req.VertexLocation, "europe-west4"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestModelConfigService_GetStatus_CapitalisedAliasCountsAsConfigured(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	for _, alias := range []string{"General", "Planning"} {
		if _, err := svc.Create(ctx, &CreateModelConfigRequest{
			Alias:      alias,
			Provider:   "openai",
			ModelName:  "gpt-4",
			APIBase:    "https://llm.example.com/v1",
			AuthConfig: map[string]any{"type": "none"},
		}, "user1"); err != nil {
			t.Fatalf("create %s: %v", alias, err)
		}
	}

	status, err := svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Configured {
		t.Error("configured: false; rows with capitalised aliases must be found by GetStatus after lowercasing on Create")
	}
}

func decisionNodesOn() *features.Registry {
	registry := features.NewRegistry()
	registry.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: true})
	return registry
}

func TestModelConfigService_ListPageReturnsOnePageAndTheTotal(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := t.Context()
	for _, alias := range []string{"a", "b", "c"} {
		_, err := svc.Create(ctx, &CreateModelConfigRequest{
			Alias:      alias,
			Provider:   "openai",
			ModelName:  "gpt-4",
			APIBase:    "https://llm.example.com/v1",
			AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-secret"},
		}, "user1")
		if err != nil {
			t.Fatalf("create %s: %v", alias, err)
		}
	}

	items, total, err := svc.ListPage(ctx, 1, 1, "alias", false)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(items) != 1 || items[0].Alias != "b" {
		t.Fatalf("items = %+v, want the single model b", items)
	}
	if got := items[0].AuthConfig["api_key"]; got == "sk-secret" {
		t.Errorf("api_key was not redacted: %v", got)
	}
}

func TestModelConfigService_GetModelsFromProviderByID_TypelessOverrideKeepsStoredFields(t *testing.T) {
	t.Parallel()

	var capturedAuth string
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"}]}`))
	}))
	defer providerServer.Close()

	svc, _ := setupModelConfigSvc(t)
	ctx := t.Context()
	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias: "typeless-override", Provider: "openai", ModelName: "gpt-4", APIBase: providerServer.URL,
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-stored"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = svc.GetModelsFromProviderByID(ctx, created.ID, NewModelListService(nil, slog.Default()), "openai",
		map[string]any{"api_key": redactedSentinel}, "")
	if err != nil {
		t.Fatalf("GetModelsFromProviderByID: %v", err)
	}
	if capturedAuth != "Bearer sk-stored" {
		t.Errorf("provider Authorization = %q, want %q", capturedAuth, "Bearer sk-stored")
	}
}
