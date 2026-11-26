// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const (
	testAzureClientID = "11111111-2222-3333-4444-555555555555"
	testAzureTenantID = "66666666-7777-8888-9999-000000000000"
)

func enableAzureWorkloadIdentity(t *testing.T) {
	t.Helper()
	t.Setenv(llm.AzureWorkloadIdentityEnabledEnv, "true")
	t.Setenv(llm.AzureFederatedTokenFileEnv, "/var/run/secrets/sam/azure/token")
}

func azureWorkloadIdentityRaw(extra map[string]any) map[string]any {
	raw := map[string]any{
		"model":       "azure/gpt-4.1-mini",
		"api_base":    "https://sam.openai.azure.com/",
		"api_version": "2024-10-21",
		llm.AzureWorkloadIdentityKey: map[string]any{
			"client_id": testAzureClientID,
			"tenant_id": testAzureTenantID,
		},
	}
	for k, v := range extra {
		raw[k] = v
	}
	return raw
}

func TestValidateRuntimeModelConfig_AzureWorkloadIdentity(t *testing.T) {
	enableAzureWorkloadIdentity(t)
	cases := []struct {
		name       string
		raw        map[string]any
		wantFields []string
	}{
		{name: "valid", raw: azureWorkloadIdentityRaw(nil)},
		{name: "cognitiveservices host", raw: azureWorkloadIdentityRaw(map[string]any{"api_base": "https://sam.cognitiveservices.azure.com/"})},
		{name: "foundry host", raw: azureWorkloadIdentityRaw(map[string]any{"api_base": "https://sam.services.ai.azure.com/"})},
		{name: "empty credential values are absent", raw: azureWorkloadIdentityRaw(map[string]any{"api_key": "", "client_id": nil})},
		{name: "model params pass through", raw: azureWorkloadIdentityRaw(map[string]any{"temperature": 0.2, "extra_headers": map[string]any{"x": "y"}})},
		{name: "gateway host", raw: azureWorkloadIdentityRaw(map[string]any{"api_base": "https://apim.example.com/openai"})},
		{name: "endpoint overriding api base", raw: azureWorkloadIdentityRaw(map[string]any{"endpoint": "https://gateway.example.com"})},
		{name: "other provider", raw: azureWorkloadIdentityRaw(map[string]any{"model": "openai/gpt-4o"}), wantFields: []string{"provider"}},
		{
			name:       "missing ids",
			raw:        azureWorkloadIdentityRaw(map[string]any{llm.AzureWorkloadIdentityKey: map[string]any{}}),
			wantFields: []string{"authConfig.client_id", "authConfig.tenant_id"},
		},
		{
			name: "ids that are not GUIDs",
			raw: azureWorkloadIdentityRaw(map[string]any{llm.AzureWorkloadIdentityKey: map[string]any{
				"client_id": "{" + testAzureClientID + "}",
				"tenant_id": "contoso.onmicrosoft.com",
			}}),
			wantFields: []string{"authConfig.client_id", "authConfig.tenant_id"},
		},
		{name: "api key", raw: azureWorkloadIdentityRaw(map[string]any{"api_key": "k"}), wantFields: []string{"api_key"}},
		{name: "top-level client secret sign-in", raw: azureWorkloadIdentityRaw(map[string]any{"client_id": "c", "client_secret": "s", "tenant_id": "t"}), wantFields: []string{"client_id", "client_secret", "tenant_id"}},
		{name: "client secret read from a file", raw: azureWorkloadIdentityRaw(map[string]any{"client_secret_file": "/etc/secret"}), wantFields: []string{"client_secret_file"}},
		{name: "oauth fields", raw: azureWorkloadIdentityRaw(map[string]any{"oauth_token_url": "https://idp/token"}), wantFields: []string{"oauth_token_url"}},
		{name: "deployment chat route", raw: azureWorkloadIdentityRaw(map[string]any{"azure_chat_route": " Deployment "}), wantFields: []string{"azure_chat_route"}},
		{name: "v1 chat route", raw: azureWorkloadIdentityRaw(map[string]any{"azure_chat_route": "v1"})},
		{name: "no block leaves other auth types alone", raw: map[string]any{"model": "azure/gpt-4.1-mini", "api_base": "https://apim.example.com", "api_key": "k"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRuntimeModelConfig(tc.raw)
			if len(tc.wantFields) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %v, want a ValidationError", err)
			}
			var got []string
			for field := range validationErr.Details {
				got = append(got, field)
			}
			slices.Sort(got)
			if !reflect.DeepEqual(got, tc.wantFields) {
				t.Errorf("detail fields = %v, want %v (message %q)", got, tc.wantFields, validationErr.Message)
			}
		})
	}
}

func TestWriteAuthConfigToRaw_AzureWorkloadIdentityNestsIDs(t *testing.T) {
	raw := map[string]any{}
	WriteAuthConfigToRaw(raw, map[string]any{
		"type":        AuthTypeAzureWorkloadIdentity,
		"client_id":   testAzureClientID,
		"tenant_id":   testAzureTenantID,
		"api_version": "2024-10-21",
	})

	want := map[string]any{
		llm.AzureWorkloadIdentityKey: map[string]any{"client_id": testAzureClientID, "tenant_id": testAzureTenantID},
		"api_version":                "2024-10-21",
	}
	if !reflect.DeepEqual(raw, want) {
		t.Errorf("raw = %v, want %v", raw, want)
	}
}

func TestAuthTypeSwitchReplacesConfig(t *testing.T) {
	cases := []struct {
		oldType, newType string
		want             bool
	}{
		{"apikey", AuthTypeAzureWorkloadIdentity, true},
		{AuthTypeAzureWorkloadIdentity, "apikey", true},
		{"oauth2", AuthTypeAzureWorkloadIdentity, true},
		{AuthTypeAzureWorkloadIdentity, "oauth2", true},
		{AuthTypeAzureWorkloadIdentity, AuthTypeAzureWorkloadIdentity, false},
		{AuthTypeAzureWorkloadIdentity, "", false},
		{"apikey", "oauth2", false},
	}
	for _, tc := range cases {
		if got := AuthTypeSwitchReplacesConfig(tc.oldType, tc.newType); got != tc.want {
			t.Errorf("AuthTypeSwitchReplacesConfig(%q, %q) = %v, want %v", tc.oldType, tc.newType, got, tc.want)
		}
	}
}

func TestValidateRuntimeModelConfig_AzureWorkloadIdentityRefusedWhereNotEnabled(t *testing.T) {
	t.Setenv(llm.AzureWorkloadIdentityEnabledEnv, "")
	t.Setenv(llm.AzureFederatedTokenFileEnv, "/var/run/secrets/azure/tokens/azure-identity-token")

	var validationErr *ValidationError
	if err := ValidateRuntimeModelConfig(azureWorkloadIdentityRaw(nil)); !errors.As(err, &validationErr) || validationErr.Details["authConfig.type"] == nil {
		t.Fatalf("error = %v, want a ValidationError on authConfig.type", err)
	}
}
