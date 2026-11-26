// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

func TestBuildAPIConnectorTool_WireCompatibleWithAWEParser(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		values        map[string]any
		wantAuthType  openapi.AuthType
		assertAuthCfg func(t *testing.T, auth *openapi.AuthConfig)
	}{
		{
			name: "no_auth",
			values: map[string]any{
				"specification_url": "https://api.example.com/spec.yaml",
				"base_url":          "https://api.example.com",
			},
			wantAuthType: openapi.AuthTypeUnset,
			assertAuthCfg: func(t *testing.T, auth *openapi.AuthConfig) {
				if auth != nil {
					t.Errorf("auth should be nil when auth_type is absent; got %+v", auth)
				}
			},
		},
		{
			name: "apikey_header",
			values: map[string]any{
				"specification_url":    "https://api.example.com/spec.yaml",
				"base_url":             "https://api.example.com",
				"auth_type":            "apikey",
				"auth_apikey_location": "header",
				"auth_apikey_name":     "X-Api-Key",
				"auth_apikey_value":    "key-abc",
			},
			wantAuthType: openapi.AuthTypeAPIKey,
			assertAuthCfg: func(t *testing.T, auth *openapi.AuthConfig) {
				if auth.In != "header" {
					t.Errorf("auth.In = %q, want header", auth.In)
				}
				if auth.HeaderName != "X-Api-Key" {
					t.Errorf("auth.HeaderName = %q, want X-Api-Key", auth.HeaderName)
				}
				if auth.APIKey != "key-abc" {
					t.Errorf("auth.APIKey = %q, want key-abc", auth.APIKey)
				}
			},
		},
		{
			name: "http_bearer",
			values: map[string]any{
				"specification_url":      "https://api.example.com/spec.yaml",
				"base_url":               "https://api.example.com",
				"auth_type":              "http",
				"auth_http_scheme":       "bearer",
				"auth_http_bearer_token": "token-xyz",
			},
			wantAuthType: openapi.AuthTypeBearer,
			assertAuthCfg: func(t *testing.T, auth *openapi.AuthConfig) {
				if auth.Token != "token-xyz" {
					t.Errorf("auth.Token = %q, want token-xyz", auth.Token)
				}
			},
		},
		{
			name: "http_basic",
			values: map[string]any{
				"specification_url":        "https://api.example.com/spec.yaml",
				"base_url":                 "https://api.example.com",
				"auth_type":                "http",
				"auth_http_scheme":         "basic",
				"auth_http_basic_username": "alice",
				"auth_http_basic_password": "s3cret",
			},
			wantAuthType: openapi.AuthTypeBasic,
			assertAuthCfg: func(t *testing.T, auth *openapi.AuthConfig) {
				if auth.Username != "alice" {
					t.Errorf("auth.Username = %q", auth.Username)
				}
				if auth.Password != "s3cret" {
					t.Errorf("auth.Password = %q", auth.Password)
				}
			},
		},
		{
			name: "oauth2",
			values: map[string]any{
				"specification_url":                     "https://api.example.com/spec.yaml",
				"base_url":                              "https://api.example.com",
				"auth_type":                             "oauth",
				"auth_oauth_authorization_url":          "https://auth.example.com/authorize",
				"auth_oauth_token_url":                  "https://auth.example.com/token",
				"auth_oauth_refresh_url":                "https://auth.example.com/refresh",
				"auth_oauth_client_id":                  "client-id",
				"auth_oauth_client_secret":              "client-secret",
				"auth_oauth_scopes":                     "read write",
				"auth_oauth_token_endpoint_auth_method": "client_secret_basic",
			},
			wantAuthType: openapi.AuthTypeOAuth2AuthorizationCode,
			assertAuthCfg: func(t *testing.T, auth *openapi.AuthConfig) {
				if auth.AuthorizationURL != "https://auth.example.com/authorize" {
					t.Errorf("AuthorizationURL = %q", auth.AuthorizationURL)
				}
				if auth.TokenURL != "https://auth.example.com/token" {
					t.Errorf("TokenURL = %q", auth.TokenURL)
				}
				if auth.RefreshURL != "https://auth.example.com/refresh" {
					t.Errorf("RefreshURL = %q", auth.RefreshURL)
				}
				if auth.ClientID != "client-id" {
					t.Errorf("ClientID = %q", auth.ClientID)
				}
				if auth.ClientSecret != "client-secret" {
					t.Errorf("ClientSecret = %q", auth.ClientSecret)
				}
				if len(auth.Scopes) != 2 || auth.Scopes[0] != "read" || auth.Scopes[1] != "write" {
					t.Errorf("Scopes = %v, want [read write]", auth.Scopes)
				}
				if auth.TokenEndpointAuthMethod != "client_secret_basic" {
					t.Errorf("TokenEndpointAuthMethod = %q", auth.TokenEndpointAuthMethod)
				}
				if want := "openapi_connector_770e8400-e29b-41d4-a716-446655440000"; auth.CredentialKey != want {
					t.Errorf("CredentialKey = %q, want %q", auth.CredentialKey, want)
				}
				if auth.DisplayName != testAPIConnectorName {
					t.Errorf("DisplayName = %q, want the connector name %q", auth.DisplayName, testAPIConnectorName)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			emitted := buildConnectorTool(apiConnector(tc.values))
			if emitted["tool_type"] != "openapi" {
				t.Fatalf("tool_type = %v, want openapi", emitted["tool_type"])
			}
			if _, wrapped := emitted["tool_config"]; wrapped {
				t.Fatalf("emitted tool wraps keys under tool_config — the AWE dispatcher would miss them; python emits flat keys")
			}

			rawYAML, err := yaml.Marshal(emitted)
			if err != nil {
				t.Fatalf("marshal tool entry: %v", err)
			}
			var roundTripped map[string]any
			if unmarshalErr := yaml.Unmarshal(rawYAML, &roundTripped); unmarshalErr != nil {
				t.Fatalf("unmarshal: %v\n--- yaml ---\n%s", unmarshalErr, rawYAML)
			}

			cfg, err := openapi.ParseToolsetConfig(roundTripped)
			if err != nil {
				t.Fatalf("ParseToolsetConfig rejected the emitted YAML: %v\n--- yaml ---\n%s", err, rawYAML)
			}
			if cfg.SpecURL != "objstore://"+testAPIConnectorResourceKey {
				t.Errorf("SpecURL = %q", cfg.SpecURL)
			}
			if cfg.BaseURL != "https://api.example.com" {
				t.Errorf("BaseURL = %q", cfg.BaseURL)
			}
			if tc.wantAuthType == openapi.AuthTypeUnset {
				tc.assertAuthCfg(t, cfg.Auth)
				return
			}
			if cfg.Auth == nil {
				t.Fatalf("cfg.Auth is nil, expected type %q\n--- yaml ---\n%s", tc.wantAuthType, rawYAML)
			}
			if cfg.Auth.Type != tc.wantAuthType {
				t.Errorf("cfg.Auth.Type = %q, want %q", cfg.Auth.Type, tc.wantAuthType)
			}
			tc.assertAuthCfg(t, cfg.Auth)
		})
	}
}

func TestBuildAPIConnectorTool_PythonParityWireShape(t *testing.T) {
	t.Parallel()

	emitted := buildConnectorTool(apiConnector(map[string]any{
		"specification_url":                     "https://api.example.com/spec.yaml",
		"base_url":                              "https://api.example.com",
		"auth_type":                             "oauth",
		"auth_oauth_authorization_url":          "https://auth.example.com/authorize",
		"auth_oauth_token_url":                  "https://auth.example.com/token",
		"auth_oauth_client_id":                  "client-id",
		"auth_oauth_client_secret":              "client-secret",
		"auth_oauth_scopes":                     "read write",
		"auth_oauth_token_endpoint_auth_method": "client_secret_basic",
		"allow_list":                            "getUser",
		"custom_headers":                        map[string]any{"X-Tenant-Id": "t1"},
	}))

	keys := []string{"tool_type", "specification_url", "base_url", "auth", "headers", "allow_list"}
	for _, k := range keys {
		if _, has := emitted[k]; !has {
			t.Errorf("missing key %q at tool entry — python emits this flat", k)
		}
	}
	forbidden := []string{"tool_config", "tool_name", "tool_name_prefix", "timeout_seconds", "spec_url", "authentication"}
	for _, k := range forbidden {
		if _, has := emitted[k]; has {
			t.Errorf("forbidden key %q in emitted tool entry (Python has no such key)", k)
		}
	}

	auth, ok := emitted["auth"].(map[string]any)
	if !ok {
		t.Fatal("auth must be a map")
	}
	if auth["type"] != "oauth2" {
		t.Errorf("auth.type = %v, want oauth2 (Python never emits oauth2_authorization_code)", auth["type"])
	}
	for _, k := range []string{"authorization_url", "token_url", "client_id", "client_secret", "scopes", "token_endpoint_auth_method"} {
		if _, has := auth[k]; !has {
			t.Errorf("missing flat oauth field auth.%s", k)
		}
	}
	for _, k := range []string{"scheme", "credential"} {
		if _, has := auth[k]; has {
			t.Errorf("forbidden nested block auth.%s (that's the MCP shape, not OpenAPI)", k)
		}
	}
}
