// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestStringifyHeaders_DropsCRLFEntries(t *testing.T) {
	out := stringifyHeaders(map[string]any{
		"X-OK":                "fine",
		"X-Bad":               "v\r\nX-Injected: pwn",
		"X-Bad\r\nX-Injected": "v",
	})
	if out["X-OK"] != "fine" {
		t.Errorf("clean header dropped: %v", out)
	}
	if len(out) != 1 {
		t.Errorf("CRLF entries not dropped, got %d entries: %v", len(out), out)
	}
}

func TestExtraHeadersCRLFKey(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantBad bool
	}{
		{"nil params", nil, false},
		{"no headers", map[string]any{"temperature": 0.7}, false},
		{"non-map extra_headers ignored", map[string]any{"extra_headers": "not-a-map"}, false},
		{"clean", map[string]any{"extra_headers": map[string]any{"X-Correlation-ID": "abc-123"}}, false},
		{"crlf in key", map[string]any{"extra_headers": map[string]any{"X-Bad\r\nX-Injected": "v"}}, true},
		{"crlf in value", map[string]any{"extra_headers": map[string]any{"X-Bad": "v\r\nX-Injected: pwn"}}, true},
		{"crlf in non-string value", map[string]any{"extra_headers": map[string]any{"X-Bad": []any{"v\r\nX-Injected"}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, bad := ExtraHeadersCRLFKey(tt.params); bad != tt.wantBad {
				t.Errorf("bad = %v, want %v", bad, tt.wantBad)
			}
		})
	}
}

func TestParseModelProvider(t *testing.T) {
	tests := []struct {
		input        string
		wantProvider string
		wantModel    string
	}{
		{"openai/gpt-4", "openai", "gpt-4"},
		{"anthropic/claude-sonnet-4-20250514", "anthropic", "claude-sonnet-4-20250514"},
		{"google/gemini-1.5-pro", "gemini", "gemini-1.5-pro"},
		{"vertex/gemini-2.0-flash", "vertex", "gemini-2.0-flash"},
		{"vertex_ai/gemini-2.0-flash", "vertex", "gemini-2.0-flash"},
		{"gemini/gemini-2.0-flash", "gemini", "gemini-2.0-flash"},
		{"bedrock/anthropic.claude-v2", "bedrock", "anthropic.claude-v2"},
		{"azure/gpt-4o", "azure", "gpt-4o"},
		{"cohere/command-r-plus", "cohere", "command-r-plus"},
		{"mistral/mistral-large", "mistral", "mistral-large"},
		{"groq/llama-3.1-70b", "groq", "llama-3.1-70b"},
		{"ollama/llama3", "ollama", "llama3"},
		{"xai/grok-1", "xai", "grok-1"},

		{"OpenAI/gpt-4", "openai", "gpt-4"},
		{"ANTHROPIC/claude-3", "anthropic", "claude-3"},

		{"claude-sonnet-4-20250514", "anthropic", "claude-sonnet-4-20250514"},
		{"claude-3-haiku", "anthropic", "claude-3-haiku"},
		{"gemini-1.5-pro", "gemini", "gemini-1.5-pro"},
		{"gpt-4", "openai", "gpt-4"},
		{"gpt-4o-mini", "openai", "gpt-4o-mini"},
		{"llama-3.1-70b", "openai", "llama-3.1-70b"},

		{"unknown-provider/some-model", "openai", "unknown-provider/some-model"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			provider, model := ParseModelProvider(tt.input)
			if provider != tt.wantProvider {
				t.Errorf("provider = %q, want %q", provider, tt.wantProvider)
			}
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
		})
	}
}

func TestParseModelConfig_StringInput(t *testing.T) {
	cfg, err := ParseModelConfig("openai/gpt-4", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" {
		t.Errorf("provider = %q, want openai", cfg.Provider)
	}
	if cfg.Model != "gpt-4" {
		t.Errorf("model = %q, want gpt-4", cfg.Model)
	}
}

func TestParseModelConfig_MapInput(t *testing.T) {
	raw := map[string]any{
		"model":    "anthropic/claude-sonnet-4-20250514",
		"api_base": "https://custom.endpoint.com",
		"api_key":  "sk-test-key-12345",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", cfg.Provider)
	}
	if cfg.Model != "claude-sonnet-4-20250514" {
		t.Errorf("model = %q, want claude-sonnet-4-20250514", cfg.Model)
	}
	if cfg.BaseURL != "https://custom.endpoint.com" {
		t.Errorf("base_url = %q, want https://custom.endpoint.com", cfg.BaseURL)
	}
	if cfg.APIKeyEnvVar == "" {
		t.Error("expected APIKeyEnvVar to be set")
	}
}

func TestParseModelConfig_EmptyAPIKeyTreatedAsAbsent(t *testing.T) {
	raw := map[string]any{
		"model":    "my-model",
		"api_base": "http://vllm.internal:8000/v1",
		"api_key":  "",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" {
		t.Errorf("provider = %q, want openai", cfg.Provider)
	}
	if cfg.APIKeyEnvVar != "" {
		t.Errorf("APIKeyEnvVar = %q, want empty for an empty api_key", cfg.APIKeyEnvVar)
	}
}

func TestParseModelConfig_MapMinimal(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-4",
	}
	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" {
		t.Errorf("provider = %q, want openai", cfg.Provider)
	}
	if cfg.Model != "gpt-4" {
		t.Errorf("model = %q, want gpt-4", cfg.Model)
	}
	if cfg.BaseURL != "" {
		t.Errorf("base_url = %q, want empty", cfg.BaseURL)
	}
	if cfg.APIKeyEnvVar != "" {
		t.Errorf("api_key_env = %q, want empty", cfg.APIKeyEnvVar)
	}
}

func TestParseModelConfig_TheModelConfigIDNeverReachesTheProvider(t *testing.T) {
	raw := map[string]any{"model": "gpt-4", ModelConfigIDKey: "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"}
	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProviderConfig != nil {
		t.Errorf("ProviderConfig = %v, want none: the id is SAM's, not a provider setting", cfg.ProviderConfig)
	}
	if !IsClientConfigKey(ModelConfigIDKey) {
		t.Error("model_config_id is not reserved, so a request could forward it as a body parameter")
	}
}

func TestSentModelName_NamesAModelByTheBifrostProviderItIsSentTo(t *testing.T) {
	for raw, want := range map[string]string{
		"vertex_ai/gemini-2.5-pro": "vertex/gemini-2.5-pro",
		"google/gemini-2.5-pro":    "gemini/gemini-2.5-pro",
		"gpt-4o":                   "openai/gpt-4o",
		"claude-sonnet-4-5":        "anthropic/claude-sonnet-4-5",
		"openai/gpt-4o":            "openai/gpt-4o",
		"":                         "",
	} {
		if got := SentModelName(raw); got != want {
			t.Errorf("SentModelName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestForPythonAgent_DropsTheIDWithoutChangingTheBinding(t *testing.T) {
	raw := map[string]any{"model": "gpt-4", ModelConfigIDKey: "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"}
	sent := ForPythonAgent(raw)
	if _, present := sent[ModelConfigIDKey]; present {
		t.Errorf("config for a Python agent = %v, want no %s", sent, ModelConfigIDKey)
	}
	if sent["model"] != "gpt-4" {
		t.Errorf("config for a Python agent = %v, want the model kept", sent)
	}
	if ModelConfigID(raw) == "" {
		t.Error("ForPythonAgent removed the id from the caller's map, which Go agents still read")
	}
	if ForPythonAgent(nil) != nil {
		t.Error("ForPythonAgent(nil) is not nil, so a missing model would not be sent as null")
	}
}

func TestForPythonAgent_KeepsExistingAPIBases(t *testing.T) {
	for _, tc := range []struct{ model, base string }{
		{"openai/m", "https://h"},
		{"openai/m", "https://h/"},
		{"openai/m", "https://h/v1"},
		{"openai/m", "https://h/api/v3"},
		{"openai/m", "https://h/api/v3?k=v"},
		{"openai/m", "https://h/chat/completions/proxy"},
		{"gpt-4o", "https://h/v1"},
		{"gpt-4o", "https://h/api/v3"},
		{"azure/m", "https://h/openai/deployments/d/chat/completions"},
		{"groq/m", "https://h/api/v3/chat/completions"},
		{"anthropic/claude-sonnet-4-6", "https://h/api/v3/chat/completions"},
		{"claude-sonnet-4-6", "https://h/api/v3/chat/completions"},
		{"datarobot/azure/gpt-4o", "https://app.datarobot.com/api/v2/genai/llmgw/chat/completions"},
		{"hosted_vllm/m", "https://h/api/v3/chat/completions"},
		{"openai/m", "https://h/api/v3/chat/completions?k=v"},
		{"gpt-4o", "https://h/api/v3/chat/completions?k=v"},
	} {
		sent := ForPythonAgent(map[string]any{"model": tc.model, "api_base": tc.base})
		if sent["api_base"] != tc.base {
			t.Errorf("%s with api_base %q sent %v, want it unchanged", tc.model, tc.base, sent["api_base"])
		}
	}
}

func TestForPythonAgent_SendsLiteLLMTheEndpointsRoot(t *testing.T) {
	for _, model := range []string{"openai/deepseek-v3", "gpt-4o"} {
		for base, want := range map[string]string{
			"https://h/api/v3/chat/completions":  "https://h/api/v3",
			"https://h/api/v3/chat/completions/": "https://h/api/v3",
		} {
			raw := map[string]any{"model": model, "api_base": base}
			if sent := ForPythonAgent(raw); sent["api_base"] != want {
				t.Errorf("%s with api_base %q sent %v, want %q", model, base, sent["api_base"], want)
			}
			if raw["api_base"] != base {
				t.Errorf("ForPythonAgent changed the caller's api_base to %v, which Go agents still read", raw["api_base"])
			}
		}
	}
}

func TestParseModelConfig_ProviderPassthrough(t *testing.T) {
	raw := map[string]any{
		"model":       "azure/gpt-5",
		"api_key":     "sk-test",
		"endpoint":    "https://my-azure.openai.azure.com",
		"api_version": "2024-10-21",
		"deployments": map[string]any{"gpt-5": "gpt-5"},
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "azure" {
		t.Errorf("provider = %q, want azure", cfg.Provider)
	}
	if cfg.Model != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", cfg.Model)
	}
	if cfg.ProviderConfig == nil {
		t.Fatal("expected ProviderConfig to be set")
	}
	if cfg.ProviderConfig["endpoint"] != "https://my-azure.openai.azure.com" {
		t.Errorf("endpoint = %v, want https://my-azure.openai.azure.com", cfg.ProviderConfig["endpoint"])
	}
	if cfg.ProviderConfig["api_version"] != "2024-10-21" {
		t.Errorf("api_version = %v, want 2024-10-21", cfg.ProviderConfig["api_version"])
	}
	deployments, ok := cfg.ProviderConfig["deployments"].(map[string]any)
	if !ok {
		t.Fatalf("deployments type = %T, want map[string]any", cfg.ProviderConfig["deployments"])
	}
	if deployments["gpt-5"] != "gpt-5" {
		t.Errorf("deployments[gpt-5] = %v, want gpt-5", deployments["gpt-5"])
	}
}

func TestParseModelConfig_NumRetries(t *testing.T) {
	t.Run("present is honored verbatim", func(t *testing.T) {
		cfg, err := ParseModelConfig(map[string]any{
			"model":       "openai/gpt-4o",
			"num_retries": 5,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxRetries == nil || *cfg.MaxRetries != 5 {
			t.Errorf("MaxRetries = %v, want 5", cfg.MaxRetries)
		}
		if cfg.ProviderConfig != nil {
			t.Errorf("expected nil ProviderConfig, got %v", cfg.ProviderConfig)
		}
	})
	t.Run("explicit zero is preserved (not treated as unset)", func(t *testing.T) {
		cfg, err := ParseModelConfig(map[string]any{
			"model":       "openai/gpt-4o",
			"num_retries": 0,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxRetries == nil || *cfg.MaxRetries != 0 {
			t.Errorf("MaxRetries = %v, want pointer to 0", cfg.MaxRetries)
		}
	})
	t.Run("absent leaves MaxRetries nil (package default applies)", func(t *testing.T) {
		cfg, err := ParseModelConfig(map[string]any{
			"model": "openai/gpt-4o",
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxRetries != nil {
			t.Errorf("MaxRetries = %v, want nil", cfg.MaxRetries)
		}
	})
}

func TestParseModelConfig_RetryBudgetSeconds(t *testing.T) {
	t.Run("present is honored verbatim and not leaked", func(t *testing.T) {
		cfg, err := ParseModelConfig(map[string]any{
			"model":                    "openai/gpt-4o",
			"llm_retry_budget_seconds": 45,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RetryBudgetSeconds == nil || *cfg.RetryBudgetSeconds != 45 {
			t.Errorf("RetryBudgetSeconds = %v, want 45", cfg.RetryBudgetSeconds)
		}
		if cfg.ProviderConfig != nil {
			t.Errorf("expected nil ProviderConfig, got %v", cfg.ProviderConfig)
		}
	})
	t.Run("explicit zero is preserved (not treated as unset)", func(t *testing.T) {
		cfg, err := ParseModelConfig(map[string]any{
			"model":                    "openai/gpt-4o",
			"llm_retry_budget_seconds": 0,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RetryBudgetSeconds == nil || *cfg.RetryBudgetSeconds != 0 {
			t.Errorf("RetryBudgetSeconds = %v, want pointer to 0", cfg.RetryBudgetSeconds)
		}
	})
	t.Run("absent leaves it nil (package default applies)", func(t *testing.T) {
		cfg, err := ParseModelConfig(map[string]any{
			"model": "openai/gpt-4o",
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RetryBudgetSeconds != nil {
			t.Errorf("RetryBudgetSeconds = %v, want nil", cfg.RetryBudgetSeconds)
		}
	})
}

func TestParseModelConfig_LLMConcurrency(t *testing.T) {
	t.Run("llm_concurrency and llm_buffer_size are parsed and not leaked", func(t *testing.T) {
		cfg, err := ParseModelConfig(map[string]any{
			"model":           "openai/gpt-4o",
			"llm_concurrency": 50,
			"llm_buffer_size": 200,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Concurrency != 50 {
			t.Errorf("Concurrency = %d, want 50", cfg.Concurrency)
		}
		if cfg.BufferSize != 200 {
			t.Errorf("BufferSize = %d, want 200", cfg.BufferSize)
		}
		if cfg.ProviderConfig != nil {
			t.Errorf("expected nil ProviderConfig, got %v", cfg.ProviderConfig)
		}
	})
	t.Run("absent leaves zero values (package defaults apply downstream)", func(t *testing.T) {
		cfg, err := ParseModelConfig(map[string]any{"model": "openai/gpt-4o"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Concurrency != 0 || cfg.BufferSize != 0 {
			t.Errorf("Concurrency/BufferSize = %d/%d, want 0/0", cfg.Concurrency, cfg.BufferSize)
		}
	})
}

func TestParseModelConfig_NoExtraKeys(t *testing.T) {
	raw := map[string]any{
		"model":   "openai/gpt-4o",
		"api_key": "sk-test",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProviderConfig != nil {
		t.Errorf("expected nil ProviderConfig, got %v", cfg.ProviderConfig)
	}
}

func TestParseModelConfig_FileKeyResolution(t *testing.T) {
	tmpFile := t.TempDir() + "/creds.json"
	if err := os.WriteFile(tmpFile, []byte(`{"type":"service_account","project_id":"test"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	raw := map[string]any{
		"model":                 "vertex/gemini-2.5-flash",
		"project_id":            "test-project",
		"region":                "us-central1",
		"auth_credentials_file": tmpFile,
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProviderConfig["auth_credentials_file"] != nil {
		t.Error("auth_credentials_file should have been removed")
	}
	creds, ok := cfg.ProviderConfig["auth_credentials"].(string)
	if !ok {
		t.Fatalf("auth_credentials type = %T, want string", cfg.ProviderConfig["auth_credentials"])
	}
	if creds != `{"type":"service_account","project_id":"test"}` {
		t.Errorf("auth_credentials = %q, want JSON content", creds)
	}
}

func TestParseModelConfig_FileKeyResolution_Missing(t *testing.T) {
	raw := map[string]any{
		"model":                 "vertex/gemini-2.5-flash",
		"auth_credentials_file": "/nonexistent/path/creds.json",
	}

	_, err := ParseModelConfig(raw, nil)
	if err == nil {
		t.Fatal("expected error for missing credentials file")
	}
}

func TestParseModelConfig_KnownFileKeyResolution_Missing(t *testing.T) {
	cases := []string{
		"api_key_file",
		"oauth_token_url_file",
		"oauth_client_id_file",
		"oauth_client_secret_file",
	}
	for _, fileKey := range cases {
		t.Run(fileKey, func(t *testing.T) {
			raw := map[string]any{
				"model": "openai/gpt-4o",
				fileKey: "/nonexistent/path/secret.txt",
			}
			_, err := ParseModelConfig(raw, nil)
			if err == nil {
				t.Fatalf("expected error for missing %s", fileKey)
			}
			if !strings.Contains(err.Error(), fileKey) {
				t.Errorf("error should mention %s; got: %v", fileKey, err)
			}
		})
	}
}

func TestParseModelConfig_FileKeyResolution_EmptyPath(t *testing.T) {
	raw := map[string]any{
		"model":                 "vertex/gemini-2.5-flash",
		"project_id":            "test-project",
		"region":                "us-central1",
		"auth_credentials_file": "",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ProviderConfig == nil {
		t.Fatal("expected ProviderConfig to be set (project_id, region, auth_credentials_file)")
	}
	if _, ok := cfg.ProviderConfig["auth_credentials"]; ok {
		t.Error("auth_credentials should not be set when file path is empty")
	}
}

func TestParseModelConfig_ExtraHeaders(t *testing.T) {
	raw := map[string]any{
		"model":   "anthropic/claude-sonnet-4-20250514",
		"api_key": "sk-test",
		"extra_headers": map[string]any{
			"anthropic-beta": "prompt-caching-2024-07-31",
			"x-custom":       "value",
		},
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExtraHeaders == nil {
		t.Fatal("expected ExtraHeaders to be set")
	}
	if cfg.ExtraHeaders["anthropic-beta"] != "prompt-caching-2024-07-31" {
		t.Errorf("anthropic-beta = %q, want prompt-caching-2024-07-31", cfg.ExtraHeaders["anthropic-beta"])
	}
	if cfg.ExtraHeaders["x-custom"] != "value" {
		t.Errorf("x-custom = %q, want value", cfg.ExtraHeaders["x-custom"])
	}
	if cfg.ProviderConfig != nil {
		t.Errorf("expected nil ProviderConfig, got %v", cfg.ProviderConfig)
	}
}

func TestParseModelConfig_InvalidType(t *testing.T) {
	_, err := ParseModelConfig(42, nil)
	if err == nil {
		t.Error("expected error for int input")
	}
}

func TestParseModelConfig_EmptyMap(t *testing.T) {
	cfg, err := ParseModelConfig(map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestParseModelConfig_OAuth2Full(t *testing.T) {
	pemContent := "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIUEi2KAqX8R2+xXQF+\n-----END CERTIFICATE-----\n"
	caFile := t.TempDir() + "/corp-ca.pem"
	if err := os.WriteFile(caFile, []byte(pemContent), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{
		"model":                              "openai/gpt-4o",
		"api_key":                            "sk-test",
		"oauth_token_url":                    "https://login.example.com/oauth2/token",
		"oauth_client_id":                    "my-client-id",
		"oauth_client_secret":                "my-client-secret",
		"oauth_scope":                        "api://gateway/.default",
		"oauth_ca_cert":                      caFile,
		"oauth_token_refresh_buffer_seconds": 600,
		"oauth_max_retries":                  5,
		"oauth_allow_insecure_token_url":     true,
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth2 == nil {
		t.Fatal("expected OAuth2 to be set")
	}
	if cfg.OAuth2.TokenURL != "https://login.example.com/oauth2/token" {
		t.Errorf("TokenURL = %q", cfg.OAuth2.TokenURL)
	}
	if cfg.OAuth2.ClientID != "my-client-id" {
		t.Errorf("ClientID = %q", cfg.OAuth2.ClientID)
	}
	if cfg.OAuth2.ClientSecret != "my-client-secret" {
		t.Errorf("ClientSecret = %q", cfg.OAuth2.ClientSecret)
	}
	if cfg.OAuth2.Scope != "api://gateway/.default" {
		t.Errorf("Scope = %q", cfg.OAuth2.Scope)
	}
	if cfg.OAuth2.CACertPath != caFile {
		t.Errorf("CACertPath = %q, want %q", cfg.OAuth2.CACertPath, caFile)
	}
	if cfg.OAuth2.TokenRefreshBufferSeconds != 600 {
		t.Errorf("TokenRefreshBufferSeconds = %d", cfg.OAuth2.TokenRefreshBufferSeconds)
	}
	if cfg.OAuth2.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d", cfg.OAuth2.MaxRetries)
	}
	if !cfg.OAuth2.AllowInsecureTokenURL {
		t.Error("AllowInsecureTokenURL should be true")
	}
}

func TestParseModelConfig_OAuth2Minimal(t *testing.T) {
	raw := map[string]any{
		"model":               "openai/gpt-4o",
		"oauth_token_url":     "https://auth.example.com/token",
		"oauth_client_id":     "cid",
		"oauth_client_secret": "csecret",
		"oauth_scope":         "read",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth2 == nil {
		t.Fatal("expected OAuth2 to be set")
	}
	if cfg.OAuth2.TokenURL != "https://auth.example.com/token" {
		t.Errorf("TokenURL = %q", cfg.OAuth2.TokenURL)
	}
	if cfg.OAuth2.Scope != "read" {
		t.Errorf("Scope = %q", cfg.OAuth2.Scope)
	}
	if cfg.OAuth2.TokenRefreshBufferSeconds != 0 {
		t.Errorf("TokenRefreshBufferSeconds = %d, want 0 (default applied by TokenManager)", cfg.OAuth2.TokenRefreshBufferSeconds)
	}
}

func TestParseModelConfig_OAuth2NotConfigured(t *testing.T) {
	raw := map[string]any{
		"model":   "openai/gpt-4o",
		"api_key": "sk-test",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth2 != nil {
		t.Errorf("expected nil OAuth2, got %+v", cfg.OAuth2)
	}
}

func TestParseModelConfig_OAuth2DoesNotLeakToProviderConfig(t *testing.T) {
	raw := map[string]any{
		"model":               "openai/gpt-4o",
		"oauth_token_url":     "https://auth.example.com/token",
		"oauth_client_id":     "cid",
		"oauth_client_secret": "csecret",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProviderConfig != nil {
		t.Errorf("oauth_ keys leaked to ProviderConfig: %v", cfg.ProviderConfig)
	}
}

func TestParseModelConfig_OAuth2IncompleteConfig(t *testing.T) {
	raw := map[string]any{
		"model":           "openai/gpt-4o",
		"oauth_token_url": "https://auth.example.com/token",
		"oauth_client_id": "cid",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth2 != nil {
		t.Errorf("expected nil OAuth2 for incomplete config, got %+v", cfg.OAuth2)
	}
}

func TestParseModelConfig_Azure_OAuth2RoutesToServicePrincipal(t *testing.T) {
	raw := map[string]any{
		"model":               "azure/gpt-4o",
		"api_base":            "https://my-resource.openai.azure.com/",
		"oauth_token_url":     "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token",
		"oauth_client_id":     "00000000-0000-0000-0000-000000000002",
		"oauth_client_secret": "the-secret",
		"oauth_scope":         "https://cognitiveservices.azure.com/.default",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth2 != nil {
		t.Fatal("OAuth2 should be nil for azure — creds must route to Bifrost's native Service Principal path so the Bearer token lands in Authorization, not api-key")
	}
	pc := cfg.ProviderConfig
	if pc == nil {
		t.Fatal("expected ProviderConfig populated with Azure Service Principal fields")
	}
	if pc["client_id"] != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("client_id = %v", pc["client_id"])
	}
	if pc["client_secret"] != "the-secret" {
		t.Errorf("client_secret = %v", pc["client_secret"])
	}
	if pc["tenant_id"] != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("tenant_id = %v", pc["tenant_id"])
	}
	scopes, ok := pc["scopes"].([]string)
	if !ok || len(scopes) != 1 || scopes[0] != "https://cognitiveservices.azure.com/.default" {
		t.Errorf("scopes = %v", pc["scopes"])
	}
}

func TestParseModelConfig_TrimsTheEndpoint(t *testing.T) {
	cfg, err := ParseModelConfig(map[string]any{
		"model":    "azure/gpt-4.1-mini",
		"api_base": " https://sam.services.ai.azure.com \n",
		"endpoint": " https://other.services.ai.azure.com \n",
		"api_key":  "k",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://sam.services.ai.azure.com" {
		t.Errorf("BaseURL = %q, want it trimmed", cfg.BaseURL)
	}
	if got := cfg.ProviderConfig["endpoint"]; got != "https://other.services.ai.azure.com" {
		t.Errorf("endpoint = %q, want it trimmed", got)
	}
}

func TestParseModelConfig_Azure_OAuth2MultiScopeSplit(t *testing.T) {
	raw := map[string]any{
		"model":               "azure/gpt-4o",
		"oauth_token_url":     "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token",
		"oauth_client_id":     "cid",
		"oauth_client_secret": "csecret",
		"oauth_scope":         "https://cognitiveservices.azure.com/.default offline_access",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	scopes, ok := cfg.ProviderConfig["scopes"].([]string)
	if !ok {
		t.Fatalf("scopes = %v (%T), want []string", cfg.ProviderConfig["scopes"], cfg.ProviderConfig["scopes"])
	}
	want := []string{"https://cognitiveservices.azure.com/.default", "offline_access"}
	if len(scopes) != len(want) {
		t.Fatalf("scopes = %v, want %v", scopes, want)
	}
	for i, s := range want {
		if scopes[i] != s {
			t.Errorf("scopes[%d] = %q, want %q", i, scopes[i], s)
		}
	}
}

func TestParseModelConfig_Azure_OAuth2WhitespaceScopeOmitsKey(t *testing.T) {
	raw := map[string]any{
		"model":               "azure/gpt-4o",
		"oauth_token_url":     "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token",
		"oauth_client_id":     "cid",
		"oauth_client_secret": "csecret",
		"oauth_scope":         "   ",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.ProviderConfig["scopes"]; ok {
		t.Errorf("scopes key must be omitted for whitespace-only oauth_scope, got %v", cfg.ProviderConfig["scopes"])
	}
}

func TestParseModelConfig_Azure_OAuth2NonEntraLogsHostOnly(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	raw := map[string]any{
		"model":               "azure/gpt-4o",
		"oauth_token_url":     "https://custom-idp.example.com/oauth2/token?realm=leaky",
		"oauth_client_id":     "cid",
		"oauth_client_secret": "csecret",
	}

	if _, err := ParseModelConfig(raw, logger); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "level=INFO") {
		t.Errorf("expected INFO-level log for supported non-Entra fallback, got: %q", out)
	}
	if strings.Contains(out, "level=WARN") {
		t.Errorf("non-Entra fallback must not log at WARN, got: %q", out)
	}
	if !strings.Contains(out, "tokenURLHost=custom-idp.example.com") {
		t.Errorf("expected tokenURLHost=custom-idp.example.com, got: %q", out)
	}
	if strings.Contains(out, "realm=leaky") {
		t.Errorf("token URL query string leaked into log, got: %q", out)
	}
}

func TestParseModelConfig_Azure_OAuth2WarnsOnKeyOverride(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	raw := map[string]any{
		"model":               "azure/gpt-4o",
		"oauth_token_url":     "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token",
		"oauth_client_id":     "oauth-derived-cid",
		"oauth_client_secret": "oauth-derived-secret",
		"client_id":           "operator-supplied-cid",
		"tenant_id":           "operator-supplied-tenant",
	}

	cfg, err := ParseModelConfig(raw, logger)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ProviderConfig["client_id"] != "oauth-derived-cid" {
		t.Errorf("client_id = %v, want oauth-derived value (overwrite is intentional)", cfg.ProviderConfig["client_id"])
	}
	if cfg.ProviderConfig["tenant_id"] != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("tenant_id = %v, want extracted-from-token-URL value", cfg.ProviderConfig["tenant_id"])
	}

	out := buf.String()
	if !strings.Contains(out, "overridden by oauth2-derived values") {
		t.Errorf("expected WARN about overridden provider config keys, got: %q", out)
	}
	if !strings.Contains(out, `overriddenKeys="[client_id tenant_id]"`) {
		t.Errorf("expected overriddenKeys=[client_id tenant_id] (sorted), got: %q", out)
	}
}

func TestParseModelConfig_Azure_OAuth2WarnsOnDroppedOAuthFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	raw := map[string]any{
		"model":                              "azure/gpt-4o",
		"oauth_token_url":                    "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token",
		"oauth_client_id":                    "cid",
		"oauth_client_secret":                "csecret",
		"oauth_token_refresh_buffer_seconds": 120,
		"oauth_max_retries":                  5,
	}

	if _, err := ParseModelConfig(raw, logger); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "oauth_* fields dropped, not supported for Azure") {
		t.Errorf("expected WARN about dropped OAuth2 fields, got: %q", out)
	}
	if !strings.Contains(out, `droppedKeys="[oauth_token_refresh_buffer_seconds oauth_max_retries]"`) {
		t.Errorf("expected droppedKeys=[oauth_token_refresh_buffer_seconds oauth_max_retries], got: %q", out)
	}
}

func TestParseModelConfig_Azure_OAuth2NonEntraURLKeepsGenericPath(t *testing.T) {
	raw := map[string]any{
		"model":               "azure/gpt-4o",
		"oauth_token_url":     "https://custom-idp.example.com/oauth2/token",
		"oauth_client_id":     "cid",
		"oauth_client_secret": "csecret",
		"oauth_scope":         "https://cognitiveservices.azure.com/.default",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth2 == nil {
		t.Fatal("non-Entra token URL should keep the generic OAuth2 path")
	}
	for _, k := range []string{"client_id", "client_secret", "tenant_id", "scopes"} {
		if _, ok := cfg.ProviderConfig[k]; ok {
			t.Errorf("azure SP field %q must not be populated for non-Entra URLs, got %v", k, cfg.ProviderConfig[k])
		}
	}
}

func TestParseModelConfig_NonAzure_OAuth2Unchanged(t *testing.T) {
	raw := map[string]any{
		"model":               "openai/gpt-4o",
		"oauth_token_url":     "https://login.microsoftonline.com/tenant-guid/oauth2/v2.0/token",
		"oauth_client_id":     "cid",
		"oauth_client_secret": "csecret",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth2 == nil {
		t.Fatal("non-azure provider must keep SAM's OAuth2 token manager")
	}
	for _, k := range []string{"client_id", "client_secret", "tenant_id", "scopes"} {
		if _, ok := cfg.ProviderConfig[k]; ok {
			t.Errorf("azure SP field %q must not be populated for non-azure provider, got %v", k, cfg.ProviderConfig[k])
		}
	}
}

func TestExtractEntraTenantID(t *testing.T) {
	tests := []struct {
		url        string
		wantTenant string
		wantOK     bool
	}{
		{"https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token", "00000000-0000-0000-0000-000000000001", true},
		{"https://login.microsoftonline.com/00000000-0000-0000-0000-000000000001/oauth2/token", "00000000-0000-0000-0000-000000000001", true},
		{"https://login.windows.net/00000000-0000-0000-0000-000000000001/oauth2/v2.0/token", "00000000-0000-0000-0000-000000000001", true},
		{"https://sts.windows.net/00000000-0000-0000-0000-000000000001/oauth2/token", "00000000-0000-0000-0000-000000000001", true},
		{"https://login.microsoftonline.com/common/oauth2/v2.0/token", "common", true},
		{"https://LOGIN.MICROSOFTONLINE.COM/tenant/oauth2/v2.0/token", "tenant", true},
		{"https://login.microsoftonline.com:443/tenant/oauth2/v2.0/token", "tenant", true},
		{"https://custom-idp.example.com/oauth2/token", "", false},
		{"https://login.microsoftonline.com/", "", false},
		{"https://login.microsoftonline.com/tenant/discovery/keys", "", false},
		{"not a url at all", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, ok := extractEntraTenantID(tt.url)
			if ok != tt.wantOK || got != tt.wantTenant {
				t.Errorf("extractEntraTenantID(%q) = (%q, %v), want (%q, %v)", tt.url, got, ok, tt.wantTenant, tt.wantOK)
			}
		})
	}
}

func TestParseModelConfig_APICACert_Valid(t *testing.T) {
	pemContent := "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIUEi2KAqX8R2+xXQF+\n-----END CERTIFICATE-----\n"
	tmpFile := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(tmpFile, []byte(pemContent), 0o600); err != nil {
		t.Fatal(err)
	}

	raw := map[string]any{
		"model":       "openai/gpt-4o",
		"api_ca_cert": tmpFile,
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CACertPEM != pemContent {
		t.Errorf("CACertPEM = %q, want PEM content", cfg.CACertPEM)
	}
}

func TestParseModelConfig_APICACert_MissingFile(t *testing.T) {
	raw := map[string]any{
		"model":       "openai/gpt-4o",
		"api_ca_cert": "/nonexistent/ca.pem",
	}

	_, err := ParseModelConfig(raw, nil)
	if err == nil {
		t.Fatal("expected error for missing CA cert file")
	}
}

func TestParseModelConfig_FailureAfterAPIKey_DoesNotLeakEnv(t *testing.T) {
	secret := "sk-leak-regression-test-" + t.Name()

	raw := map[string]any{
		"model":       "openai/gpt-4o",
		"api_key":     secret,
		"api_ca_cert": "/nonexistent/ca.pem",
	}

	if _, err := ParseModelConfig(raw, nil); err == nil {
		t.Fatal("expected error for missing CA cert file")
	}

	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SAM_AWE_LLM_API_KEY_") {
			continue
		}
		name, value, _ := strings.Cut(kv, "=")
		if value == secret {
			_ = os.Unsetenv(name)
			t.Fatalf("api key leaked into env var %s after parse failure", name)
		}
	}
}

func TestParseModelConfig_APICACert_InvalidPEM(t *testing.T) {
	tmpFile := t.TempDir() + "/bad.pem"
	if err := os.WriteFile(tmpFile, []byte("not valid PEM content"), 0o600); err != nil {
		t.Fatal(err)
	}

	raw := map[string]any{
		"model":       "openai/gpt-4o",
		"api_ca_cert": tmpFile,
	}

	_, err := ParseModelConfig(raw, nil)
	if err == nil {
		t.Fatal("expected error for invalid PEM content")
	}
}

func TestParseModelConfig_APICACert_EmptyString(t *testing.T) {
	raw := map[string]any{
		"model":       "openai/gpt-4o",
		"api_ca_cert": "",
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CACertPEM != "" {
		t.Errorf("CACertPEM = %q, want empty for empty path", cfg.CACertPEM)
	}
}

func TestParseModelConfig_APICACert_WithSkipTLSVerify(t *testing.T) {
	tmpFile := t.TempDir() + "/ca.pem"
	pemContent := "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIUEi2KAqX8R2+xXQF+\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(tmpFile, []byte(pemContent), 0o600); err != nil {
		t.Fatal(err)
	}

	raw := map[string]any{
		"model":               "openai/gpt-4o",
		"api_ca_cert":         tmpFile,
		"api_skip_tls_verify": true,
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify to be true")
	}
	if cfg.CACertPEM != "" {
		t.Errorf("CACertPEM = %q, want empty when api_skip_tls_verify is true", cfg.CACertPEM)
	}
}

func TestParseModelConfig_APICACert_DoesNotLeakToProviderConfig(t *testing.T) {
	pemContent := "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIUEi2KAqX8R2+xXQF+\n-----END CERTIFICATE-----\n"
	tmpFile := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(tmpFile, []byte(pemContent), 0o600); err != nil {
		t.Fatal(err)
	}

	raw := map[string]any{
		"model":       "openai/gpt-4o",
		"api_ca_cert": tmpFile,
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProviderConfig != nil {
		t.Errorf("api_ca_cert leaked to ProviderConfig: %v", cfg.ProviderConfig)
	}
}

func TestParseModelConfig_OAuth_SkipTLSVerify_IgnoresCACert(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	raw := map[string]any{
		"model":                 "openai/gpt-4o",
		"oauth_token_url":       "https://auth.example.com/oauth2/token",
		"oauth_client_id":       "my-id",
		"oauth_client_secret":   "my-secret",
		"oauth_ca_cert":         "/etc/ssl/custom-ca.pem",
		"oauth_skip_tls_verify": true,
	}

	cfg, err := ParseModelConfig(raw, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OAuth2 == nil {
		t.Fatal("expected OAuth2 config to be populated")
	}
	if !cfg.OAuth2.InsecureSkipVerify {
		t.Error("expected OAuth2.InsecureSkipVerify to be true")
	}
	if cfg.OAuth2.CACertPath != "/etc/ssl/custom-ca.pem" {
		t.Errorf("CACertPath = %q, want the raw configured path", cfg.OAuth2.CACertPath)
	}

	out := buf.String()
	if !strings.Contains(out, "TLS certificate verification disabled") {
		t.Errorf("expected TLS-disabled warning; got: %q", out)
	}
	if !strings.Contains(out, "oauth_ca_cert is ignored") {
		t.Errorf("expected oauth_ca_cert-ignored warning; got: %q", out)
	}
}

func TestParseModelConfig_OAuthCACert_Valid(t *testing.T) {
	pemContent := "-----BEGIN CERTIFICATE-----\nMIIBkTCB+wIUEi2KAqX8R2+xXQF+\n-----END CERTIFICATE-----\n"
	tmpFile := t.TempDir() + "/oauth-ca.pem"
	if err := os.WriteFile(tmpFile, []byte(pemContent), 0o600); err != nil {
		t.Fatal(err)
	}

	raw := map[string]any{
		"model":               "openai/gpt-4o",
		"oauth_token_url":     "https://auth.example.com/oauth2/token",
		"oauth_client_id":     "my-id",
		"oauth_client_secret": "my-secret",
		"oauth_ca_cert":       tmpFile,
	}

	cfg, err := ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuth2 == nil {
		t.Fatal("expected OAuth2 config to be populated")
	}
	if cfg.OAuth2.CACertPath != tmpFile {
		t.Errorf("CACertPath = %q, want %q", cfg.OAuth2.CACertPath, tmpFile)
	}
}

func TestParseModelConfig_OAuthCACert_MissingFile(t *testing.T) {
	raw := map[string]any{
		"model":               "openai/gpt-4o",
		"oauth_token_url":     "https://auth.example.com/oauth2/token",
		"oauth_client_id":     "my-id",
		"oauth_client_secret": "my-secret",
		"oauth_ca_cert":       "/nonexistent/oauth-ca.pem",
	}

	_, err := ParseModelConfig(raw, nil)
	if err == nil {
		t.Fatal("expected error for missing oauth_ca_cert file")
	}
	if !strings.Contains(err.Error(), "oauth_ca_cert") {
		t.Errorf("error should mention oauth_ca_cert; got: %v", err)
	}
}

func TestParseModelConfig_OAuthCACert_InvalidPEM(t *testing.T) {
	tmpFile := t.TempDir() + "/bad-oauth-ca.pem"
	if err := os.WriteFile(tmpFile, []byte("not valid PEM content"), 0o600); err != nil {
		t.Fatal(err)
	}

	raw := map[string]any{
		"model":               "openai/gpt-4o",
		"oauth_token_url":     "https://auth.example.com/oauth2/token",
		"oauth_client_id":     "my-id",
		"oauth_client_secret": "my-secret",
		"oauth_ca_cert":       tmpFile,
	}

	_, err := ParseModelConfig(raw, nil)
	if err == nil {
		t.Fatal("expected error for invalid oauth_ca_cert PEM content")
	}
	if !strings.Contains(err.Error(), "oauth_ca_cert") {
		t.Errorf("error should mention oauth_ca_cert; got: %v", err)
	}
}

func TestParseModelConfig_OAuthCACert_SkipVerifyBypassesFileRead(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	raw := map[string]any{
		"model":                 "openai/gpt-4o",
		"oauth_token_url":       "https://auth.example.com/oauth2/token",
		"oauth_client_id":       "my-id",
		"oauth_client_secret":   "my-secret",
		"oauth_ca_cert":         "/nonexistent/skipped-oauth-ca.pem",
		"oauth_skip_tls_verify": true,
	}

	cfg, err := ParseModelConfig(raw, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OAuth2 == nil {
		t.Fatal("expected OAuth2 config to be populated")
	}
	if !cfg.OAuth2.InsecureSkipVerify {
		t.Error("expected OAuth2.InsecureSkipVerify to be true")
	}
	if !strings.Contains(buf.String(), "oauth_ca_cert is ignored") {
		t.Errorf("expected oauth_ca_cert-ignored warning; got: %q", buf.String())
	}
}

func TestParseModelConfig_StreamIdleTimeoutFromJSON(t *testing.T) {
	cfg, err := ParseModelConfig(map[string]any{
		"model":                       "anthropic/claude-opus-5-5",
		"stream_idle_timeout_seconds": float64(450),
	}, nil)
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}
	if cfg.StreamIdleTimeoutSeconds != 450 {
		t.Errorf("StreamIdleTimeoutSeconds = %d, want 450", cfg.StreamIdleTimeoutSeconds)
	}
}

func TestParseModelConfig_SanitizesNonCompletionKeys(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	raw := map[string]any{
		"model":                       "openai/gpt-4o",
		"api_key":                     "sk-test",
		"api_base":                    "https://api.example.com",
		"request_timeout_seconds":     30,
		"stream_idle_timeout_seconds": 450,
		"cache_strategy":              "1h",
		"thinking":                    map[string]any{"budget_tokens": 10000},
		"type":                        "custom",
		"oauth_token_url":             "https://auth.example.com/token",
		"oauth_client_id":             "id",
		"oauth_client_secret":         "secret",
		"oauth_scope":                 "read",
		"oauth_custom_foo":            "value",
		"project_id":                  "my-project",
	}

	cfg, err := ParseModelConfig(raw, logger)
	if err != nil {
		t.Fatalf("ParseModelConfig returned error: %v", err)
	}

	if cfg.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", cfg.Model)
	}
	if cfg.BaseURL != "https://api.example.com" {
		t.Errorf("BaseURL = %q, want https://api.example.com", cfg.BaseURL)
	}
	if cfg.RequestTimeoutSeconds != 30 {
		t.Errorf("RequestTimeoutSeconds = %d, want 30", cfg.RequestTimeoutSeconds)
	}
	if cfg.StreamIdleTimeoutSeconds != 450 {
		t.Errorf("StreamIdleTimeoutSeconds = %d, want 450", cfg.StreamIdleTimeoutSeconds)
	}
	if cfg.OAuth2 == nil || cfg.OAuth2.ClientID != "id" {
		t.Errorf("OAuth2 config missing/wrong: %+v", cfg.OAuth2)
	}

	for _, k := range []string{
		"cache_strategy", "thinking", "type",
		"request_timeout_seconds", "stream_idle_timeout_seconds",
		"oauth_token_url", "oauth_client_id", "oauth_client_secret",
		"oauth_scope", "oauth_custom_foo",
	} {
		if _, ok := cfg.ProviderConfig[k]; ok {
			t.Errorf("ProviderConfig contains sanitized key %q: %v", k, cfg.ProviderConfig[k])
		}
	}

	if got, ok := cfg.ProviderConfig["project_id"]; !ok || got != "my-project" {
		t.Errorf("ProviderConfig.project_id = %v (ok=%v), want my-project", got, ok)
	}
}

func TestParseModelConfig_OnlyNonCompletionKeys_EmptyProviderConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	raw := map[string]any{
		"model":          "openai/gpt-4o",
		"api_key":        "sk-test",
		"cache_strategy": "5m",
		"thinking":       map[string]any{"budget_tokens": 5000},
		"type":           "custom",
	}

	cfg, err := ParseModelConfig(raw, logger)
	if err != nil {
		t.Fatalf("ParseModelConfig returned error: %v", err)
	}
	if len(cfg.ProviderConfig) != 0 {
		t.Errorf("ProviderConfig should be empty, got: %v", cfg.ProviderConfig)
	}
}

func TestExtractCacheStrategy(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want string
	}{
		{"valid_5m", map[string]any{"cache_strategy": "5m"}, "5m"},
		{"valid_1h", map[string]any{"cache_strategy": "1h"}, "1h"},
		{"valid_none", map[string]any{"cache_strategy": "none"}, "none"},
		{"invalid_value", map[string]any{"cache_strategy": "bogus"}, ""},
		{"missing", map[string]any{}, ""},
		{"non_map", "openai/gpt-4o", ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractCacheStrategy(tc.raw); got != tc.want {
				t.Errorf("ExtractCacheStrategy() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractReasoningBudgetTokens(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want int
	}{
		{"int_value", map[string]any{"thinking": map[string]any{"budget_tokens": 5000}}, 5000},
		{"float_value", map[string]any{"thinking": map[string]any{"budget_tokens": float64(7500)}}, 7500},
		{"missing_thinking", map[string]any{}, 0},
		{"missing_budget", map[string]any{"thinking": map[string]any{}}, 0},
		{"thinking_not_map", map[string]any{"thinking": "yes"}, 0},
		{"non_map", "raw-string", 0},
		{"nil", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractReasoningBudgetTokens(tc.raw); got != tc.want {
				t.Errorf("ExtractReasoningBudgetTokens() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestExtractReasoningEffort(t *testing.T) {
	effortRaw := func(v any) map[string]any {
		return map[string]any{"thinking": map[string]any{"effort": v}}
	}
	cases := []struct {
		name          string
		raw           any
		want          string
		wantOffending string
		wantRejected  bool
	}{
		{"minimal", effortRaw("minimal"), "minimal", "", false},
		{"low", effortRaw("low"), "low", "", false},
		{"medium", effortRaw("medium"), "medium", "", false},
		{"high", effortRaw("high"), "high", "", false},
		{"none_is_a_level", effortRaw("none"), "none", "", false},
		{"case_and_space_tolerated", effortRaw(" High "), "high", "", false},
		{"unknown_level_rejected", effortRaw("extreme"), "", "extreme", true},
		{"typo_reported_verbatim", effortRaw(" hgih "), "", " hgih ", true},
		{"empty_string_is_unset", effortRaw(""), "", "", false},
		{"explicit_null_is_unset", effortRaw(nil), "", "", false},
		{"non_string_rejected", effortRaw(3), "", "3", true},
		{"bool_rendered", effortRaw(true), "", "true", true},
		{"nested_map_rendered", effortRaw(map[string]any{"level": "high"}), "", "map[level:high]", true},
		{"missing_effort", map[string]any{"thinking": map[string]any{"budget_tokens": 2048}}, "", "", false},
		{"missing_thinking", map[string]any{"model": "claude"}, "", "", false},
		{"thinking_not_map", map[string]any{"thinking": "yes"}, "", "", false},
		{"non_map", "raw-string", "", "", false},
		{"nil", nil, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, offending, rejected := ExtractReasoningEffort(tc.raw)
			if got != tc.want {
				t.Errorf("ExtractReasoningEffort() = %q, want %q", got, tc.want)
			}
			if offending != tc.wantOffending {
				t.Errorf("ExtractReasoningEffort() offending = %q, want %q", offending, tc.wantOffending)
			}
			if rejected != tc.wantRejected {
				t.Errorf("ExtractReasoningEffort() rejected = %v, want %v", rejected, tc.wantRejected)
			}
		})
	}
}

func TestParseModelConfig_AzureWorkloadIdentity(t *testing.T) {
	cfg, err := ParseModelConfig(map[string]any{
		"model":    "azure/gpt-4.1-mini",
		"api_base": "https://sam.openai.azure.com/",
		"azure_workload_identity": map[string]any{
			"client_id": " 11111111-2222-3333-4444-555555555555 ",
			"tenant_id": "66666666-7777-8888-9999-000000000000",
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := llm.AzureWorkloadIdentity{ClientID: "11111111-2222-3333-4444-555555555555", TenantID: "66666666-7777-8888-9999-000000000000"}
	if cfg.AzureWorkloadIdentity == nil || *cfg.AzureWorkloadIdentity != want {
		t.Errorf("AzureWorkloadIdentity = %+v, want %+v", cfg.AzureWorkloadIdentity, want)
	}
	if _, leaked := cfg.ProviderConfig["azure_workload_identity"]; leaked {
		t.Error("the identity block reached Bifrost's provider config")
	}
}

func TestParseModelConfig_AzureWorkloadIdentityRejectsIncompleteOrNonAzure(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(secretFile, []byte("old-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]any{
		"missing tenant": {
			"model":                   "azure/gpt-4.1-mini",
			"azure_workload_identity": map[string]any{"client_id": "11111111-2222-3333-4444-555555555555"},
		},
		"not a map": {
			"model":                   "azure/gpt-4.1-mini",
			"azure_workload_identity": true,
		},
		"other provider": {
			"model": "openai/gpt-4o",
			"azure_workload_identity": map[string]any{
				"client_id": "11111111-2222-3333-4444-555555555555",
				"tenant_id": "66666666-7777-8888-9999-000000000000",
			},
		},
		"leftover client secret": {
			"model":         "azure/gpt-4.1-mini",
			"api_base":      "https://sam.openai.azure.com/",
			"client_secret": "old-secret",
			"azure_workload_identity": map[string]any{
				"client_id": "11111111-2222-3333-4444-555555555555",
				"tenant_id": "66666666-7777-8888-9999-000000000000",
			},
		},
		"client secret read from a file": {
			"model":              "azure/gpt-4.1-mini",
			"api_base":           "https://sam.openai.azure.com/",
			"client_secret_file": secretFile,
			"azure_workload_identity": map[string]any{
				"client_id": "11111111-2222-3333-4444-555555555555",
				"tenant_id": "66666666-7777-8888-9999-000000000000",
			},
		},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseModelConfig(raw, nil); err == nil || !strings.Contains(err.Error(), "azure_workload_identity") {
				t.Errorf("error = %v, want one naming azure_workload_identity", err)
			}
		})
	}
}
