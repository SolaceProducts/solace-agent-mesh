// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func testConfigAccount(cfg BifrostConfig) *configAccount {
	return newConfigAccount(cfg, "")
}

func TestGetKeys_OpenAI_WithAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-123")
	acc := testConfigAccount(BifrostConfig{Provider: "openai"})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	key := keys[0]
	if key.ID != "openai-key" {
		t.Errorf("key ID = %q, want %q", key.ID, "openai-key")
	}
	if !key.Value.IsFromEnv() {
		t.Error("expected key value to be from env")
	}
	if key.Value.GetValue() != "sk-test-123" {
		t.Errorf("key value = %q, want %q", key.Value.GetValue(), "sk-test-123")
	}
	if key.AzureKeyConfig != nil || key.VertexKeyConfig != nil || key.BedrockKeyConfig != nil {
		t.Error("expected no provider-specific KeyConfig")
	}
}

func TestGetKeys_OpenAI_CustomEnvVar(t *testing.T) {
	t.Setenv("MY_CUSTOM_KEY", "custom-key-456")
	acc := testConfigAccount(BifrostConfig{
		Provider:     "openai",
		APIKeyEnvVar: "MY_CUSTOM_KEY",
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	if key := keys[0]; key.Value.GetValue() != "custom-key-456" {
		t.Errorf("key value = %q, want %q", key.Value.GetValue(), "custom-key-456")
	}
}

func TestGetKeys_OpenAI_DefaultEnvVar(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "default-env-key")
	acc := testConfigAccount(BifrostConfig{
		Provider:     "openai",
		APIKeyEnvVar: "",
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	if key := keys[0]; key.Value.GetValue() != "default-env-key" {
		t.Errorf("key value = %q, want %q", key.Value.GetValue(), "default-env-key")
	}
}

func TestGetKeys_Anthropic_WithAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	acc := testConfigAccount(BifrostConfig{Provider: "anthropic"})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Anthropic)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	key := keys[0]
	if key.ID != "anthropic-key" {
		t.Errorf("key ID = %q, want %q", key.ID, "anthropic-key")
	}
	if key.Value.GetValue() != "sk-ant-test" {
		t.Errorf("key value = %q, want %q", key.Value.GetValue(), "sk-ant-test")
	}
}

func TestGetKeys_Vertex_NoAPIKey(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "vertex",
		ProviderConfig: map[string]any{
			"project_id": "my-project",
			"region":     "us-central1",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Vertex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	key := keys[0]
	if key.VertexKeyConfig == nil {
		t.Fatal("expected VertexKeyConfig to be set")
	}
	if key.Value.GetValue() != "" {
		t.Errorf("key value = %q, want empty (no API key)", key.Value.GetValue())
	}
}

func TestGetKeys_Bedrock_NoAPIKey(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "bedrock",
		ProviderConfig: map[string]any{
			"access_key": "AKIA...",
			"secret_key": "secret...",
			"region":     "us-east-1",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Bedrock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	key := keys[0]
	if key.BedrockKeyConfig == nil {
		t.Fatal("expected BedrockKeyConfig to be set")
	}
}

func TestGetKeys_MissingAPIKey_NoProviderConfig(t *testing.T) {
	t.Setenv("NONEXISTENT_TEST_KEY_12345", "")
	t.Setenv("OPENAI_API_KEY", "")
	acc := testConfigAccount(BifrostConfig{
		Provider:     "openai",
		APIKeyEnvVar: "NONEXISTENT_TEST_KEY_12345",
	})

	_, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI)
	if err == nil {
		t.Fatal("expected error for missing API key with no ProviderConfig")
	}
}

func TestGetKeys_OpenAI_CustomBaseURL_NoKey_InjectsPlaceholder(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "http://vllm.internal:8000/v1",
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error for keyless custom endpoint: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	if got := keys[0].Value.GetValue(); got != keylessAPIKeyPlaceholder {
		t.Errorf("key value = %q, want placeholder %q", got, keylessAPIKeyPlaceholder)
	}
}

func TestGetKeys_OpenAI_PublicBaseURL_NoKey_Errors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "https://api.openai.com/v1",
	})

	if _, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI); err == nil {
		t.Fatal("expected error for OpenAI public endpoint with no API key")
	}
}

func TestGetKeys_OpenAI_CustomBaseURL_PrefersEnvKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-real-key")
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "http://vllm.internal:8000/v1",
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := keys[0].Value.GetValue(); got != "sk-real-key" {
		t.Errorf("key value = %q, want %q", got, "sk-real-key")
	}
}

func TestGetKeys_OpenAI_SubdomainBaseURL_NoKey_Errors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "https://my.openai.com/v1",
	})

	if _, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI); err == nil {
		t.Fatal("expected error for *.openai.com endpoint with no API key")
	}
}

func TestGetKeys_OpenAI_SchemelessBaseURL_NoKey_Errors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "api.openai.com/v1",
	})

	if _, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI); err == nil {
		t.Fatal("expected error for scheme-less base URL with no API key")
	}
}

func TestGetKeys_NonOpenAI_CustomBaseURL_NoKey_Errors(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	acc := testConfigAccount(BifrostConfig{
		Provider: "anthropic",
		BaseURL:  "http://proxy.internal:8080",
	})

	if _, err := acc.GetKeysForProvider(t.Context(), schemas.Anthropic); err == nil {
		t.Fatal("expected error: non-openai provider must not get the keyless placeholder")
	}
}

func TestGetKeys_OpenAI_ApexBaseURL_NoKey_Errors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "https://openai.com/v1",
	})

	if _, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI); err == nil {
		t.Fatal("expected error for openai.com apex endpoint with no API key")
	}
}

func TestGetKeys_OpenAI_TrailingDotBaseURL_NoKey_Errors(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "https://api.openai.com./v1",
	})

	if _, err := acc.GetKeysForProvider(t.Context(), schemas.OpenAI); err == nil {
		t.Fatal("expected error for trailing-dot OpenAI FQDN with no API key")
	}
}

func TestGetKeys_MissingAPIKey_WithProviderConfig(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "vertex",
		ProviderConfig: map[string]any{
			"project_id": "test-project",
			"region":     "us-east1",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Vertex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
}

func TestGetKeys_Azure_WithBothKeyAndConfig(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key-123")
	acc := testConfigAccount(BifrostConfig{
		Provider: "azure",
		ProviderConfig: map[string]any{
			"endpoint": "https://my-azure.openai.azure.com",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Azure)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	key := keys[0]
	if key.Value.GetValue() != "az-key-123" {
		t.Errorf("key value = %q, want %q", key.Value.GetValue(), "az-key-123")
	}
	if key.AzureKeyConfig == nil {
		t.Fatal("expected AzureKeyConfig to be set")
	}
	if key.AzureKeyConfig.Endpoint.GetValue() != "https://my-azure.openai.azure.com" {
		t.Errorf("endpoint = %q, want %q",
			key.AzureKeyConfig.Endpoint.GetValue(), "https://my-azure.openai.azure.com")
	}
}

func TestApplyConfig_Azure_Full(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")
	acc := testConfigAccount(BifrostConfig{
		Provider: "azure",
		ProviderConfig: map[string]any{
			"endpoint":    "https://my-azure.openai.azure.com",
			"api_version": "2024-10-21",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Azure)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	key := keys[0]
	if key.AzureKeyConfig == nil {
		t.Fatal("expected AzureKeyConfig to be set")
	}
	cfg := key.AzureKeyConfig
	if cfg.Endpoint.GetValue() != "https://my-azure.openai.azure.com" {
		t.Errorf("endpoint = %q, want %q", cfg.Endpoint.GetValue(), "https://my-azure.openai.azure.com")
	}
}

func TestAzureAPIVersionWarnsOnceAtConstruction(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")
	var buf bytes.Buffer
	client, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider: "azure",
		Model:    "gpt-4o",
		BaseURL:  "https://my-azure.openai.azure.com",
		Logger:   slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})),
		ProviderConfig: map[string]any{
			"endpoint":    "https://my-azure.openai.azure.com",
			"api_version": "2024-10-21",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(client.Shutdown)

	logged := buf.String()
	if !strings.Contains(logged, "azure api_version is no longer configurable") {
		t.Errorf("expected a warning that api_version was ignored, got: %s", logged)
	}
	if !strings.Contains(logged, "2024-10-21") {
		t.Errorf("expected the warning to name the configured value, got: %s", logged)
	}
	if strings.Contains(logged, "effective") {
		t.Errorf("warning still claims an effective api-version; none is sent on chat: %s", logged)
	}
	if n := strings.Count(logged, "azure api_version is no longer configurable"); n != 1 {
		t.Errorf("warning appeared %d times at construction, want 1", n)
	}
}

func TestApplyConfig_Azure_Minimal(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")
	acc := testConfigAccount(BifrostConfig{
		Provider: "azure",
		ProviderConfig: map[string]any{
			"endpoint": "https://minimal-azure.openai.azure.com",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Azure)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := keys[0].AzureKeyConfig
	if cfg == nil {
		t.Fatal("expected AzureKeyConfig to be set")
	}
	if cfg.Endpoint.GetValue() != "https://minimal-azure.openai.azure.com" {
		t.Errorf("endpoint = %q, want %q", cfg.Endpoint.GetValue(), "https://minimal-azure.openai.azure.com")
	}
}

func TestApplyConfig_Azure_UnknownFieldsIgnored(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")
	acc := testConfigAccount(BifrostConfig{
		Provider: "azure",
		ProviderConfig: map[string]any{
			"endpoint":           "https://azure.openai.azure.com",
			"some_unknown_field": "some_value",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Azure)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := keys[0].AzureKeyConfig
	if cfg == nil {
		t.Fatal("expected AzureKeyConfig to be set")
	}
	if cfg.Endpoint.GetValue() != "https://azure.openai.azure.com" {
		t.Errorf("endpoint = %q, want %q", cfg.Endpoint.GetValue(), "https://azure.openai.azure.com")
	}
}

func TestApplyConfig_Vertex_Full(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "vertex",
		ProviderConfig: map[string]any{
			"project_id":       "my-gcp-project",
			"region":           "us-central1",
			"auth_credentials": `{"type":"service_account","project_id":"my-gcp-project"}`,
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Vertex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := keys[0].VertexKeyConfig
	if cfg == nil {
		t.Fatal("expected VertexKeyConfig to be set")
	}
	if cfg.ProjectID.GetValue() != "my-gcp-project" {
		t.Errorf("project_id = %q, want %q", cfg.ProjectID.GetValue(), "my-gcp-project")
	}
	if cfg.Region.GetValue() != "us-central1" {
		t.Errorf("region = %q, want %q", cfg.Region.GetValue(), "us-central1")
	}
	if cfg.AuthCredentials.GetValue() != `{"type":"service_account","project_id":"my-gcp-project"}` {
		t.Errorf("auth_credentials = %q, want JSON content", cfg.AuthCredentials.GetValue())
	}
}

func TestApplyConfig_Vertex_ADC(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "vertex",
		ProviderConfig: map[string]any{
			"project_id": "adc-project",
			"region":     "europe-west1",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Vertex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := keys[0].VertexKeyConfig
	if cfg == nil {
		t.Fatal("expected VertexKeyConfig to be set")
	}
	if cfg.ProjectID.GetValue() != "adc-project" {
		t.Errorf("project_id = %q, want %q", cfg.ProjectID.GetValue(), "adc-project")
	}
	if cfg.Region.GetValue() != "europe-west1" {
		t.Errorf("region = %q, want %q", cfg.Region.GetValue(), "europe-west1")
	}
	if cfg.AuthCredentials.GetValue() != "" {
		t.Errorf("auth_credentials = %q, want empty", cfg.AuthCredentials.GetValue())
	}
}

func TestApplyConfig_Vertex_FileResolvedCreds(t *testing.T) {
	credsJSON := `{"type":"service_account","private_key":"-----BEGIN RSA PRIVATE KEY-----..."}`
	acc := testConfigAccount(BifrostConfig{
		Provider: "vertex",
		ProviderConfig: map[string]any{
			"project_id":       "file-project",
			"region":           "us-east1",
			"auth_credentials": credsJSON,
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Vertex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := keys[0].VertexKeyConfig
	if cfg == nil {
		t.Fatal("expected VertexKeyConfig to be set")
	}
	if cfg.AuthCredentials.GetValue() != credsJSON {
		t.Errorf("auth_credentials = %q, want %q", cfg.AuthCredentials.GetValue(), credsJSON)
	}
}

func TestApplyConfig_Bedrock_Full(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "bedrock",
		ProviderConfig: map[string]any{
			"access_key": "AKIAIOSFODNN7EXAMPLE",
			"secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"region":     "us-east-1",
			"arn":        "arn:aws:iam::123456789012:role/MyRole",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Bedrock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := keys[0].BedrockKeyConfig
	if cfg == nil {
		t.Fatal("expected BedrockKeyConfig to be set")
	}
	if cfg.AccessKey.GetValue() != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("access_key = %q, want %q", cfg.AccessKey.GetValue(), "AKIAIOSFODNN7EXAMPLE")
	}
	if cfg.SecretKey.GetValue() != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
		t.Errorf("secret_key = %q, want %q", cfg.SecretKey.GetValue(), "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	}
	if cfg.Region == nil || cfg.Region.GetValue() != "us-east-1" {
		t.Errorf("region = %v, want %q", cfg.Region, "us-east-1")
	}
	if cfg.ARN == nil || cfg.ARN.GetValue() != "arn:aws:iam::123456789012:role/MyRole" {
		t.Errorf("arn = %v, want %q", cfg.ARN, "arn:aws:iam::123456789012:role/MyRole")
	}
}

func TestApplyConfig_Bedrock_NoARN(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "bedrock",
		ProviderConfig: map[string]any{
			"access_key": "AKIAIOSFODNN7EXAMPLE",
			"secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"region":     "eu-west-1",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Bedrock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := keys[0].BedrockKeyConfig
	if cfg == nil {
		t.Fatal("expected BedrockKeyConfig to be set")
	}
	if cfg.Region == nil || cfg.Region.GetValue() != "eu-west-1" {
		t.Errorf("region = %v, want %q", cfg.Region, "eu-west-1")
	}
	if cfg.ARN != nil && cfg.ARN.GetValue() != "" {
		t.Errorf("arn = %q, want nil/empty", cfg.ARN.GetValue())
	}
}

func TestApplyConfig_Bedrock_WithSessionToken(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "bedrock",
		ProviderConfig: map[string]any{
			"access_key":    "ASIAIOSFODNN7EXAMPLE",
			"secret_key":    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"session_token": "FwoGZXIvYXdzEBYaDN...",
			"region":        "us-west-2",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Bedrock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := keys[0].BedrockKeyConfig
	if cfg == nil {
		t.Fatal("expected BedrockKeyConfig to be set")
	}
	if cfg.SessionToken == nil || cfg.SessionToken.GetValue() != "FwoGZXIvYXdzEBYaDN..." {
		t.Errorf("session_token = %v, want %q", cfg.SessionToken, "FwoGZXIvYXdzEBYaDN...")
	}
}

func TestApplyConfig_UnknownProvider(t *testing.T) {
	t.Setenv("COHERE_API_KEY", "cohere-key")
	acc := testConfigAccount(BifrostConfig{
		Provider: "cohere",
		ProviderConfig: map[string]any{
			"some_field": "some_value",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Cohere)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	key := keys[0]
	if key.AzureKeyConfig != nil {
		t.Error("expected nil AzureKeyConfig")
	}
	if key.VertexKeyConfig != nil {
		t.Error("expected nil VertexKeyConfig")
	}
	if key.BedrockKeyConfig != nil {
		t.Error("expected nil BedrockKeyConfig")
	}
}

func TestApplyConfig_EmptyProviderConfig(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "az-key")
	acc := testConfigAccount(BifrostConfig{
		Provider:       "azure",
		BaseURL:        "https://example.openai.azure.com/",
		ProviderConfig: map[string]any{},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Azure)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	key := keys[0]
	if key.AzureKeyConfig == nil {
		t.Fatal("expected AzureKeyConfig populated from BaseURL, got nil")
	}
	if key.AzureKeyConfig.Endpoint.GetValue() != "https://example.openai.azure.com/" {
		t.Errorf("Endpoint = %q, want the BaseURL", key.AzureKeyConfig.Endpoint.GetValue())
	}
	if key.Value.GetValue() != "az-key" {
		t.Errorf("key value = %q, want %q", key.Value.GetValue(), "az-key")
	}
}

func TestApplyConfig_MalformedConfig(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "azure",
		ProviderConfig: map[string]any{
			"endpoint": make(chan int),
		},
	})

	_, err := acc.GetKeysForProvider(t.Context(), schemas.Azure)
	if err == nil {
		t.Fatal("expected error for malformed provider config")
	}
}

func TestGetConfig_AllowsPrivateNetwork(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "http://mock-llm-server:8088",
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pcfg.NetworkConfig.AllowPrivateNetwork {
		t.Error("AllowPrivateNetwork = false, want true")
	}
}

func TestGetConfig_CustomBaseURL(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "https://custom.endpoint.com",
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.BaseURL != "https://custom.endpoint.com" {
		t.Errorf("BaseURL = %q, want %q", pcfg.NetworkConfig.BaseURL, "https://custom.endpoint.com")
	}
}

func TestGetConfig_NoBaseURL(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "openai",
		BaseURL:  "",
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.BaseURL != schemas.DefaultNetworkConfig.BaseURL {
		t.Errorf("BaseURL = %q, want default %q",
			pcfg.NetworkConfig.BaseURL, schemas.DefaultNetworkConfig.BaseURL)
	}
}

func TestGetConfig_ExtraHeaders(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "anthropic",
		ExtraHeaders: map[string]string{
			"x-custom":       "value",
			"anthropic-beta": "prompt-caching-2024-07-31",
		},
	})

	pcfg, err := acc.GetConfigForProvider(schemas.Anthropic)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.ExtraHeaders == nil {
		t.Fatal("expected ExtraHeaders to be set")
	}
	if pcfg.NetworkConfig.ExtraHeaders["x-custom"].Val != "value" {
		t.Errorf("ExtraHeaders[x-custom] = %q, want %q",
			pcfg.NetworkConfig.ExtraHeaders["x-custom"].Val, "value")
	}
	if pcfg.NetworkConfig.ExtraHeaders["anthropic-beta"].Val != "prompt-caching-2024-07-31" {
		t.Errorf("ExtraHeaders[anthropic-beta] = %q, want %q",
			pcfg.NetworkConfig.ExtraHeaders["anthropic-beta"].Val, "prompt-caching-2024-07-31")
	}
}

func TestGetConfig_NoExtraHeaders(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider:     "openai",
		ExtraHeaders: nil,
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.ExtraHeaders != nil {
		t.Errorf("ExtraHeaders = %v, want nil", pcfg.NetworkConfig.ExtraHeaders)
	}
}

func TestGetConfig_CustomTimeout(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider:              "openai",
		RequestTimeoutSeconds: 30,
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.DefaultRequestTimeoutInSeconds != 30 {
		t.Errorf("timeout = %d, want 30",
			pcfg.NetworkConfig.DefaultRequestTimeoutInSeconds)
	}
}

func TestGetConfig_DefaultTimeout(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider:              "openai",
		RequestTimeoutSeconds: 0,
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.DefaultRequestTimeoutInSeconds != 600 {
		t.Errorf("timeout = %d, want 600 (default)",
			pcfg.NetworkConfig.DefaultRequestTimeoutInSeconds)
	}
}

func TestGetConfig_StreamIdleTimeout(t *testing.T) {
	tests := []struct {
		name string
		cfg  int
		want int
	}{
		{name: "unset uses default", cfg: 0, want: 300},
		{name: "negative uses default", cfg: -1, want: 300},
		{name: "explicit value", cfg: 900, want: 900},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := testConfigAccount(BifrostConfig{
				Provider:                 "anthropic",
				StreamIdleTimeoutSeconds: tt.cfg,
			})
			pcfg, err := acc.GetConfigForProvider(schemas.Anthropic)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := pcfg.NetworkConfig.StreamIdleTimeoutInSeconds; got != tt.want {
				t.Errorf("StreamIdleTimeoutInSeconds = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetConfig_AllFields(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider:              "openai",
		BaseURL:               "https://all-fields.example.com",
		RequestTimeoutSeconds: 120,
		ExtraHeaders: map[string]string{
			"x-request-id": "test-123",
		},
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.BaseURL != "https://all-fields.example.com" {
		t.Errorf("BaseURL = %q, want %q",
			pcfg.NetworkConfig.BaseURL, "https://all-fields.example.com")
	}
	if pcfg.NetworkConfig.DefaultRequestTimeoutInSeconds != 120 {
		t.Errorf("timeout = %d, want 120",
			pcfg.NetworkConfig.DefaultRequestTimeoutInSeconds)
	}
	if pcfg.NetworkConfig.ExtraHeaders["x-request-id"].Val != "test-123" {
		t.Errorf("ExtraHeaders[x-request-id] = %q, want %q",
			pcfg.NetworkConfig.ExtraHeaders["x-request-id"].Val, "test-123")
	}
}

func TestGetConfig_CACertPEM(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider:  "openai",
		CACertPEM: "-----BEGIN CERTIFICATE-----\nMIIBtest\n-----END CERTIFICATE-----",
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.CACertPEM == nil {
		t.Fatal("CACertPEM = nil, want the PEM content")
	}
	if got := pcfg.NetworkConfig.CACertPEM.GetValue(); got != "-----BEGIN CERTIFICATE-----\nMIIBtest\n-----END CERTIFICATE-----" {
		t.Errorf("CACertPEM = %q, want PEM content", got)
	}
}

func TestGetConfig_NoCACertPEM(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider:  "openai",
		CACertPEM: "",
	})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.NetworkConfig.CACertPEM != nil {
		t.Errorf("CACertPEM = %q, want unset", pcfg.NetworkConfig.CACertPEM.GetValue())
	}
}

func TestGetConfig_ProxyConfigUsesEnvProxy(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{Provider: "openai"})

	pcfg, err := acc.GetConfigForProvider(schemas.OpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pcfg.ProxyConfig == nil {
		t.Fatal("ProxyConfig is nil; LLM egress would ignore HTTP(S)_PROXY/NO_PROXY")
	}
	if pcfg.ProxyConfig.Type != schemas.EnvProxy {
		t.Errorf("ProxyConfig.Type = %q, want %q", pcfg.ProxyConfig.Type, schemas.EnvProxy)
	}
}

func TestGetKeys_Azure_InjectsEndpointFromBaseURL(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "test-azure-key")
	acc := testConfigAccount(BifrostConfig{
		Provider: "azure",
		BaseURL:  "https://example-resource.openai.azure.com/",
		ProviderConfig: map[string]any{
			"api_version": "2024-12-01-preview",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Azure)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0].AzureKeyConfig == nil {
		t.Fatalf("expected one key with AzureKeyConfig, got %+v", keys)
	}
	cfg := keys[0].AzureKeyConfig
	if cfg.Endpoint.GetValue() != "https://example-resource.openai.azure.com/" {
		t.Errorf("Endpoint = %q, want the BaseURL", cfg.Endpoint.GetValue())
	}
}

func TestGetKeys_Azure_InjectsEndpointWhenProviderConfigEmpty(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "test-azure-key")
	acc := testConfigAccount(BifrostConfig{
		Provider: "azure",
		BaseURL:  "https://example-resource.openai.azure.com/",
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Azure)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0].AzureKeyConfig == nil {
		t.Fatalf("expected one key with AzureKeyConfig, got %+v", keys)
	}
	if keys[0].AzureKeyConfig.Endpoint.GetValue() != "https://example-resource.openai.azure.com/" {
		t.Errorf("Endpoint = %q, want the BaseURL", keys[0].AzureKeyConfig.Endpoint.GetValue())
	}
}

func TestGetKeys_Ollama_InjectsURLFromBaseURL(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Ollama)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0].OllamaKeyConfig == nil {
		t.Fatalf("expected one key with OllamaKeyConfig, got %+v", keys)
	}
	if keys[0].OllamaKeyConfig.URL.GetValue() != "http://localhost:11434" {
		t.Errorf("URL = %q, want the BaseURL", keys[0].OllamaKeyConfig.URL.GetValue())
	}
}

func TestGetKeys_Ollama_TrimsTrailingV1FromBaseURL(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434/v1",
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Ollama)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0].OllamaKeyConfig == nil {
		t.Fatalf("expected one key with OllamaKeyConfig, got %+v", keys)
	}
	if got := keys[0].OllamaKeyConfig.URL.GetValue(); got != "http://localhost:11434" {
		t.Errorf("URL = %q, want %q (trailing /v1 trimmed)", got, "http://localhost:11434")
	}
}

func TestGetKeys_Vertex_TranslatesLiteLLMKeyNames(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "vertex",
		ProviderConfig: map[string]any{
			"vertex_project":     "my-gcp-project",
			"vertex_location":    "europe-west1",
			"vertex_credentials": `{"type":"service_account"}`,
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Vertex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0].VertexKeyConfig == nil {
		t.Fatalf("expected one key with VertexKeyConfig, got %+v", keys)
	}
	cfg := keys[0].VertexKeyConfig
	if cfg.ProjectID.GetValue() != "my-gcp-project" {
		t.Errorf("ProjectID = %q, want %q", cfg.ProjectID.GetValue(), "my-gcp-project")
	}
	if cfg.Region.GetValue() != "europe-west1" {
		t.Errorf("Region = %q, want %q", cfg.Region.GetValue(), "europe-west1")
	}
	if cfg.AuthCredentials.GetValue() != `{"type":"service_account"}` {
		t.Errorf("AuthCredentials = %q, want service account JSON", cfg.AuthCredentials.GetValue())
	}
}

func TestGetKeys_Bedrock_TranslatesLiteLLMKeyNames(t *testing.T) {
	acc := testConfigAccount(BifrostConfig{
		Provider: "bedrock",
		ProviderConfig: map[string]any{
			"aws_access_key_id":     "AKIA-example",
			"aws_secret_access_key": "secret-example",
			"aws_session_token":     "session-example",
			"aws_region_name":       "us-west-2",
		},
	})

	keys, err := acc.GetKeysForProvider(t.Context(), schemas.Bedrock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 || keys[0].BedrockKeyConfig == nil {
		t.Fatalf("expected one key with BedrockKeyConfig, got %+v", keys)
	}
	cfg := keys[0].BedrockKeyConfig
	if cfg.AccessKey.GetValue() != "AKIA-example" {
		t.Errorf("AccessKey = %q, want %q", cfg.AccessKey.GetValue(), "AKIA-example")
	}
	if cfg.SecretKey.GetValue() != "secret-example" {
		t.Errorf("SecretKey = %q, want %q", cfg.SecretKey.GetValue(), "secret-example")
	}
	if cfg.SessionToken == nil || cfg.SessionToken.GetValue() != "session-example" {
		t.Errorf("SessionToken = %+v, want session-example", cfg.SessionToken)
	}
	if cfg.Region == nil || cfg.Region.GetValue() != "us-west-2" {
		t.Errorf("Region = %+v, want us-west-2", cfg.Region)
	}
}
