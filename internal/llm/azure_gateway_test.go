// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"strings"
	"testing"
)

func TestNewAzureGatewayRoute(t *testing.T) {
	tests := []struct {
		name        string
		provider    string
		model       string
		baseURL     string
		providerCfg map[string]any
		wantActive  bool
		wantVersion string
	}{
		{
			name:        "gateway in front of azure takes the deployment route",
			provider:    "azure",
			baseURL:     "https://lite-llm.example.net",
			wantActive:  true,
			wantVersion: classicAzureAPIVersion,
		},
		{
			name:        "a configured api_version is honoured again",
			provider:    "azure",
			baseURL:     "https://lite-llm.example.net",
			providerCfg: map[string]any{"api_version": "2025-01-01-preview"},
			wantActive:  true,
			wantVersion: "2025-01-01-preview",
		},
		{
			name:       "first-party azure keeps the current route",
			provider:   "azure",
			baseURL:    "https://contoso.openai.azure.com",
			wantActive: false,
		},
		{
			name:        "foundry keeps the classic route",
			provider:    "azure",
			model:       "gpt-4.1-mini",
			baseURL:     "https://contoso.services.ai.azure.com",
			wantActive:  true,
			wantVersion: classicAzureAPIVersion,
		},
		{
			name:        "v1 opts a gateway back out",
			provider:    "azure",
			baseURL:     "https://lite-llm.example.net",
			providerCfg: map[string]any{azureChatRouteKey: "v1"},
			wantActive:  false,
		},
		{
			name:        "deployment opts first-party azure in",
			provider:    "azure",
			baseURL:     "https://contoso.openai.azure.com",
			providerCfg: map[string]any{azureChatRouteKey: "deployment"},
			wantActive:  true,
			wantVersion: classicAzureAPIVersion,
		},
		{
			name:       "another provider is untouched",
			provider:   "openai",
			baseURL:    "https://lite-llm.example.net",
			wantActive: false,
		},
		{
			name:       "an anthropic model on a gateway keeps the azure provider",
			provider:   "azure",
			model:      "claude-sonnet-4-6",
			baseURL:    "https://lite-llm.example.net",
			wantActive: false,
		},
		{
			name:       "a bedrock-style anthropic id is recognised too",
			provider:   "azure",
			model:      "anthropic.claude-3-5-sonnet",
			baseURL:    "https://lite-llm.example.net",
			wantActive: false,
		},
		{
			name:        "deployment opts an anthropic model back in",
			provider:    "azure",
			model:       "claude-sonnet-4-6",
			baseURL:     "https://lite-llm.example.net",
			providerCfg: map[string]any{azureChatRouteKey: "deployment"},
			wantActive:  true,
			wantVersion: classicAzureAPIVersion,
		},
		{
			name:        "an openai-family model on a gateway is unaffected",
			provider:    "azure",
			model:       "azure-gpt-5-mini",
			baseURL:     "https://lite-llm.example.net",
			wantActive:  true,
			wantVersion: classicAzureAPIVersion,
		},
		{
			name:       "no base url leaves the default alone",
			provider:   "azure",
			wantActive: false,
		},
		{
			name:        "gateway endpoint supplied via provider config",
			provider:    "azure",
			providerCfg: map[string]any{"endpoint": "https://gateway.example.net"},
			wantActive:  true,
			wantVersion: classicAzureAPIVersion,
		},
		{
			name:        "first-party endpoint via provider config keeps the current route",
			provider:    "azure",
			providerCfg: map[string]any{"endpoint": "https://contoso.openai.azure.com"},
			wantActive:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := newAzureGatewayRoute(BifrostConfig{
				Provider:       tc.provider,
				Model:          tc.model,
				BaseURL:        tc.baseURL,
				ProviderConfig: tc.providerCfg,
			})
			if got.active != tc.wantActive {
				t.Fatalf("active = %v, want %v", got.active, tc.wantActive)
			}
			if tc.wantActive && got.apiVersion != tc.wantVersion {
				t.Errorf("apiVersion = %q, want %q", got.apiVersion, tc.wantVersion)
			}
		})
	}
}

func TestAzureGatewayPathFor(t *testing.T) {
	route := azureGatewayRoute{active: true, apiVersion: "2024-10-21"}

	got := route.pathFor("azure-gpt-5-mini")
	want := "/openai/deployments/azure-gpt-5-mini/chat/completions?api-version=2024-10-21"
	if got != want {
		t.Errorf("pathFor() = %q, want %q", got, want)
	}

	if got := route.pathFor("weird name/v2"); strings.Contains(got, " ") ||
		strings.Count(got, "/chat/completions") != 1 {
		t.Errorf("pathFor() did not escape the deployment: %q", got)
	}

	if got := route.pathFor(""); got != "" {
		t.Errorf("pathFor(\"\") = %q, want empty", got)
	}
	if got := (azureGatewayRoute{}).pathFor("m"); got != "" {
		t.Errorf("inactive route returned %q, want empty", got)
	}
}

func TestAzureGatewayApplyPinsKeyEnvBeforeProviderRewrite(t *testing.T) {
	route := azureGatewayRoute{active: true, apiVersion: "2024-10-21"}
	env := func(k string) string {
		if k == "AZURE_API_KEY" {
			return "az-key"
		}
		return ""
	}

	got := route.apply(BifrostConfig{
		Provider: "azure",
		BaseURL:  "https://lite-llm.example.net",
	}, env, nil)

	if got.Provider != "openai" {
		t.Errorf("Provider = %q, want openai so the path override is honoured", got.Provider)
	}
	if got.APIKeyEnvVar != "AZURE_API_KEY" {
		t.Errorf("APIKeyEnvVar = %q, want AZURE_API_KEY", got.APIKeyEnvVar)
	}
}

func TestAzureGatewayApplyKeepsExplicitKeyEnv(t *testing.T) {
	route := azureGatewayRoute{active: true, apiVersion: "2024-10-21"}
	got := route.apply(BifrostConfig{
		Provider:     "azure",
		BaseURL:      "https://lite-llm.example.net",
		APIKeyEnvVar: "MY_GATEWAY_KEY",
	}, func(string) string { return "x" }, nil)

	if got.APIKeyEnvVar != "MY_GATEWAY_KEY" {
		t.Errorf("APIKeyEnvVar = %q, want the operator's own var", got.APIKeyEnvVar)
	}
}

func TestAzureGatewayApplyInactiveIsIdentity(t *testing.T) {
	cfg := BifrostConfig{Provider: "azure", BaseURL: "https://contoso.openai.azure.com"}
	got := (azureGatewayRoute{}).apply(cfg, func(string) string { return "x" }, nil)
	if got.Provider != "azure" {
		t.Errorf("Provider = %q, want azure untouched", got.Provider)
	}
}

func TestAzureGatewayCheckModel(t *testing.T) {
	active := azureGatewayRoute{active: true, apiVersion: classicAzureAPIVersion}

	if err := active.checkModel("azure-gpt-5-mini"); err != nil {
		t.Errorf("an OpenAI-family model is what the route is for: %v", err)
	}

	err := active.checkModel("claude-sonnet-4-6")
	if err == nil {
		t.Fatal("an anthropic model on the deployment route must be refused, not sent")
	}
	for _, want := range []string{"claude-sonnet-4-6", azureChatRouteKey} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	if err := (azureGatewayRoute{}).checkModel("claude-sonnet-4-6"); err != nil {
		t.Errorf("inactive route must not refuse anything: %v", err)
	}
}

func TestAzureGatewayApplyCarriesTheEndpoint(t *testing.T) {
	cfg := BifrostConfig{
		Provider:       "azure",
		Model:          "azure-gpt-5-mini",
		ProviderConfig: map[string]any{"endpoint": "https://gateway.example.net"},
	}
	route := newAzureGatewayRoute(cfg)
	if !route.active {
		t.Fatal("an endpoint-only gateway config is exactly what the route is for")
	}
	got := route.apply(cfg, func(string) string { return "k" }, nil)
	if got.BaseURL != "https://gateway.example.net" {
		t.Errorf("BaseURL = %q, want the gateway endpoint: an empty one reaches "+
			"api.openai.com carrying the Azure key", got.BaseURL)
	}
	if got.ExtraHeaders["api-key"] != "k" {
		t.Errorf("api-key header = %q, want the key: Azure authenticates chat with "+
			"api-key, the OpenAI provider sends Bearer", got.ExtraHeaders["api-key"])
	}
}

func TestAzureGatewayLeavesEntraConfigAlone(t *testing.T) {
	for _, key := range []string{"client_id", "tenant_id", "scopes", "client_secret"} {
		t.Run(key, func(t *testing.T) {
			route := newAzureGatewayRoute(BifrostConfig{
				Provider: "azure",
				Model:    "azure-gpt-5-mini",
				BaseURL:  "https://gateway.example.net",
				ProviderConfig: map[string]any{
					"endpoint": "https://gateway.example.net",
					key:        "value",
				},
			})
			if route.active {
				t.Errorf("%s present: the route must decline rather than drop the credential", key)
			}
		})
	}
}

func TestAzureGatewayKeepsATrailingV1(t *testing.T) {
	route := newAzureGatewayRoute(BifrostConfig{
		Provider: "azure",
		Model:    "azure-gpt-5-mini",
		BaseURL:  "https://gateway.example.net/v1",
	})
	if !route.active {
		t.Fatal("route should be active for a gateway host")
	}
	got := route.pathFor("dep")
	if !strings.HasPrefix(got, "/v1/openai/deployments/") {
		t.Errorf("pathFor() = %q, want the /v1 the trimmed base URL no longer carries", got)
	}

	plain := newAzureGatewayRoute(BifrostConfig{
		Provider: "azure",
		Model:    "azure-gpt-5-mini",
		BaseURL:  "https://gateway.example.net",
	})
	if got := plain.pathFor("dep"); !strings.HasPrefix(got, "/openai/deployments/") {
		t.Errorf("pathFor() = %q, want no invented prefix", got)
	}
}
