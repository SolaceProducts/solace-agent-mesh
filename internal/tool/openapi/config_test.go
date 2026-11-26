// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseToolsetConfig_AuthTypeValidation(t *testing.T) {
	tests := []struct {
		name      string
		authType  string
		wantType  AuthType
		wantErr   bool
		errSubstr string
	}{
		{name: "bearer is valid", authType: "bearer", wantType: AuthTypeBearer},
		{name: "apikey is valid (Python spelling)", authType: "apikey", wantType: AuthTypeAPIKey},
		{name: "basic is valid", authType: "basic", wantType: AuthTypeBasic},
		{name: "serviceaccount is valid", authType: "serviceaccount", wantType: AuthTypeServiceAccount},
		{name: "none is valid", authType: "none", wantType: AuthTypeNone},
		{name: "empty is valid", authType: "", wantType: AuthTypeUnset},
		{name: "unknown type rejected", authType: "oauth2_custom", wantErr: true, errSubstr: "unknown auth.type"},
		{name: "typo rejected", authType: "barer", wantErr: true, errSubstr: "unknown auth.type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := map[string]any{
				"specification_url": "http://example.com/spec.json",
				"auth": map[string]any{
					"type": tt.authType,
				},
			}
			cfg, err := ParseToolsetConfig(raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("expected error containing %q, got: %v", tt.errSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Auth != nil && cfg.Auth.Type != tt.wantType {
				t.Errorf("internal Type = %q, want %q", cfg.Auth.Type, tt.wantType)
			}
		})
	}
}

func TestParseToolsetConfig_OAuth2TLSKnobs(t *testing.T) {
	raw := map[string]any{
		"specification_url": "http://example.com/spec.json",
		"auth": map[string]any{
			"type":                 "oauth2",
			"authorization_url":    "https://idp.example.com/authorize",
			"token_url":            "https://idp.example.com/token",
			"client_id":            "c1",
			"ca_cert_path":         "/etc/pki/idp-ca.pem",
			"insecure_skip_verify": true,
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Auth.CACertPath != "/etc/pki/idp-ca.pem" {
		t.Errorf("CACertPath = %q, want /etc/pki/idp-ca.pem", cfg.Auth.CACertPath)
	}
	if !cfg.Auth.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}
}

func TestParseToolsetConfig_RequiresSpecSource(t *testing.T) {
	_, err := ParseToolsetConfig(map[string]any{})
	if err == nil {
		t.Fatal("expected error when no spec source provided")
	}
}

func TestParseToolsetConfig_AllowDenyLists(t *testing.T) {
	raw := map[string]any{
		"specification_url": "http://example.com/spec.json",
		"allow_list":        []any{"op1", "op2"},
		"deny_list":         []any{"op3"},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowList) != 2 {
		t.Errorf("expected 2 allow list entries, got %d", len(cfg.AllowList))
	}
	if len(cfg.DenyList) != 1 {
		t.Errorf("expected 1 deny list entry, got %d", len(cfg.DenyList))
	}
}

func TestParseToolsetConfig_Headers(t *testing.T) {
	raw := map[string]any{
		"specification_url": "http://example.com/spec.json",
		"headers": map[string]any{
			"X-Custom": "value",
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Headers["X-Custom"] != "value" {
		t.Errorf("expected header X-Custom=value, got %v", cfg.Headers)
	}
}

func TestParseToolsetConfig_InlineSpec(t *testing.T) {
	inline := `{"openapi":"3.0.0"}`

	cfg, err := ParseToolsetConfig(map[string]any{
		"specification":        inline,
		"specification_format": "json",
	})
	if err != nil {
		t.Fatalf("spec_inline: %v", err)
	}
	if cfg.SpecInline != inline || cfg.SpecFormat != "json" {
		t.Errorf("got %+v", cfg)
	}

	cfg, err = ParseToolsetConfig(map[string]any{
		"specification":        inline,
		"specification_format": "yaml",
	})
	if err != nil {
		t.Fatalf("specification: %v", err)
	}
	if cfg.SpecInline != inline || cfg.SpecFormat != "yaml" {
		t.Errorf("python-key: got %+v", cfg)
	}
}

func TestParseToolsetConfig_MutuallyExclusiveSpecSources(t *testing.T) {
	_, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com",
		"specification":     "x",
	})
	if err == nil {
		t.Fatal("expected error for multiple sources")
	}
}

func TestParseToolsetConfig_MaxResponseSize(t *testing.T) {
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com",
		"max_response_size": 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxResponseSize != 2048 {
		t.Errorf("got %d", cfg.MaxResponseSize)
	}
	if clampResponseSize(cfg.MaxResponseSize) != 2048 {
		t.Errorf("effective = %d", clampResponseSize(cfg.MaxResponseSize))
	}

	cfg2, _ := ParseToolsetConfig(map[string]any{"specification_url": "x"})
	if clampResponseSize(cfg2.MaxResponseSize) != DefaultMaxResponseSize {
		t.Errorf("default = %d", clampResponseSize(cfg2.MaxResponseSize))
	}
}

func TestParseToolsetConfig_InvalidSpecFormat(t *testing.T) {
	_, err := ParseToolsetConfig(map[string]any{
		"specification_url":    "http://example.com",
		"specification_format": "xml",
	})
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestParseToolsetConfig_OAuth2RuntimeFields(t *testing.T) {
	raw := map[string]any{
		"specification_url": "http://example.com/spec.json",
		"auth": map[string]any{
			"type":                       "oauth2",
			"authorization_url":          "https://idp.example.com/authorize",
			"token_url":                  "https://idp.example.com/token",
			"refresh_url":                "https://idp.example.com/refresh",
			"client_id":                  "cid",
			"client_secret":              "csecret",
			"scopes":                     []any{"read", "write"},
			"redirect_uri":               "http://localhost:9999/cb",
			"token_endpoint_auth_method": "client_secret_basic",
			"use_pkce":                   true,
			"disable_consent_prompt":     true,
			"audience":                   "https://api.example.com",
			"resource":                   "https://api.example.com/mcp",
			"credential_key":             "custom-key",
			"display_name":               " Alpha CRM ",
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ac := cfg.Auth
	if ac == nil {
		t.Fatal("auth is nil")
	}
	if ac.Type != "oauth2_authorization_code" {
		t.Errorf("Type = %q", ac.Type)
	}
	if ac.AuthorizationURL != "https://idp.example.com/authorize" {
		t.Errorf("AuthorizationURL = %q", ac.AuthorizationURL)
	}
	if ac.TokenURL != "https://idp.example.com/token" {
		t.Errorf("TokenURL = %q", ac.TokenURL)
	}
	if ac.RefreshURL != "https://idp.example.com/refresh" {
		t.Errorf("RefreshURL = %q", ac.RefreshURL)
	}
	if ac.ClientID != "cid" || ac.ClientSecret != "csecret" {
		t.Errorf("client_id/secret = %q / %q", ac.ClientID, ac.ClientSecret)
	}
	if len(ac.Scopes) != 2 || ac.Scopes[0] != "read" || ac.Scopes[1] != "write" {
		t.Errorf("Scopes = %v", ac.Scopes)
	}
	if ac.RedirectURI != "http://localhost:9999/cb" {
		t.Errorf("RedirectURI = %q", ac.RedirectURI)
	}
	if ac.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Errorf("TokenEndpointAuthMethod = %q", ac.TokenEndpointAuthMethod)
	}
	if !ac.UsePKCE {
		t.Error("UsePKCE should be true")
	}
	if !ac.DisableConsentPrompt {
		t.Error("DisableConsentPrompt should be true")
	}
	if ac.Audience != "https://api.example.com" {
		t.Errorf("Audience = %q", ac.Audience)
	}
	if ac.Resource != "https://api.example.com/mcp" {
		t.Errorf("Resource = %q", ac.Resource)
	}
	if ac.CredentialKey != "custom-key" {
		t.Errorf("CredentialKey = %q", ac.CredentialKey)
	}
	if ac.DisplayName != "Alpha CRM" {
		t.Errorf("DisplayName = %q, want the trimmed display_name", ac.DisplayName)
	}
}

func TestNewAuthHandlerForOpenAPI_HandWrittenOAuthKeepsItsDefaults(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com/spec.json",
		"auth": map[string]any{
			"type":              "oauth2",
			"authorization_url": "https://idp.example.com/authorize",
			"token_url":         "https://idp.example.com/token",
			"client_id":         "cid",
			"client_secret":     "csecret",
		},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	h, err := NewAuthHandlerForOpenAPI(cfg.Auth, nil)
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}
	if got := h.CredentialKey(); got != "openapi_oauth2_authorization_code" {
		t.Errorf("CredentialKey = %q, want the unchanged default openapi_oauth2_authorization_code", got)
	}
	if got := h.AuthDisplayName(); got != "" {
		t.Errorf("AuthDisplayName = %q, want none", got)
	}
	signal, err := h.BuildAuthSignal(context.Background(), makeToolCtx(newMemCredSvc()))
	if err != nil {
		t.Fatalf("BuildAuthSignal: %v", err)
	}
	authURI, err := url.Parse(signal.AuthURI)
	if err != nil {
		t.Fatalf("parse auth URI: %v", err)
	}
	if challenge := authURI.Query().Get("code_challenge"); challenge != "" {
		t.Errorf("code_challenge = %q, want none: use_pkce defaults to false in agent YAML", challenge)
	}
}

func TestParseToolsetConfig_ScopesAsSpaceSeparatedString(t *testing.T) {
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com/spec.json",
		"auth": map[string]any{
			"type":              "oauth2",
			"authorization_url": "https://idp.example.com/authorize",
			"token_url":         "https://idp.example.com/token",
			"client_id":         "cid",
			"client_secret":     "csecret",
			"scopes":            "read write admin",
		},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.Auth.Scopes; len(got) != 3 || got[0] != "read" || got[1] != "write" || got[2] != "admin" {
		t.Errorf("Scopes = %v, want [read write admin]", got)
	}
}

func TestParseToolsetConfig_APIKey_PythonKeys(t *testing.T) {
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com/spec.json",
		"auth": map[string]any{
			"type":  "apikey",
			"in":    "header",
			"name":  "X-My-Key",
			"value": "secret-value",
		},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Auth.Type != "api_key" {
		t.Errorf("Type = %q, want api_key (internally normalised from apikey)", cfg.Auth.Type)
	}
	if cfg.Auth.HeaderName != "X-My-Key" {
		t.Errorf("HeaderName = %q", cfg.Auth.HeaderName)
	}
	if cfg.Auth.APIKey != "secret-value" {
		t.Errorf("APIKey = %q", cfg.Auth.APIKey)
	}
}

func TestParseToolsetConfig_RejectsLegacyKeys(t *testing.T) {
	legacySpecURL := "spec" + "_url"
	legacySpecInline := "spec" + "_inline"
	legacyAuthKey := "authen" + "tication"
	legacyOAuthType := "oauth2_authorization" + "_code"
	legacyAPIKeyType := "api" + "_key"

	t.Run("spec_url legacy ignored", func(t *testing.T) {
		_, err := ParseToolsetConfig(map[string]any{legacySpecURL: "http://example.com/spec.json"})
		if err == nil || !strings.Contains(err.Error(), "specification_url") {
			t.Errorf("want missing-spec error, got: %v", err)
		}
	})
	t.Run("spec_inline legacy ignored", func(t *testing.T) {
		_, err := ParseToolsetConfig(map[string]any{legacySpecInline: "openapi: 3.0.0"})
		if err == nil || !strings.Contains(err.Error(), "specification_url") {
			t.Errorf("want missing-spec error, got: %v", err)
		}
	})

	t.Run("authentication key legacy ignored", func(t *testing.T) {
		cfg, err := ParseToolsetConfig(map[string]any{
			"specification_url": "http://example.com/spec.json",
			legacyAuthKey:       map[string]any{"type": "bearer", "token": "t"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Auth != nil {
			t.Errorf("legacy `authentication:` must be ignored, but Auth was populated: %+v", cfg.Auth)
		}
	})

	for _, bad := range []string{legacyOAuthType, legacyAPIKeyType} {
		t.Run("user-facing type "+bad+" rejected", func(t *testing.T) {
			_, err := ParseToolsetConfig(map[string]any{
				"specification_url": "http://example.com/spec.json",
				"auth":              map[string]any{"type": bad},
			})
			if err == nil || !strings.Contains(err.Error(), "unknown auth.type") {
				t.Errorf("want unknown-auth-type error for %q, got: %v", bad, err)
			}
		})
	}
}

func TestParseToolsetConfig_OAuth2GrantInference(t *testing.T) {
	cases := []struct {
		name     string
		authMap  map[string]any
		wantType AuthType
	}{
		{
			name: "authorization_code when authorization_url present",
			authMap: map[string]any{
				"type":              "oauth2",
				"authorization_url": "https://idp.example.com/authorize",
				"token_url":         "https://idp.example.com/token",
				"client_id":         "client-id",
			},
			wantType: AuthTypeOAuth2AuthorizationCode,
		},
		{
			name: "client_credentials when authorization_url absent",
			authMap: map[string]any{
				"type":      "oauth2",
				"token_url": "https://idp.example.com/token",
				"client_id": "client-id",
			},
			wantType: AuthTypeOAuth2ClientCredentials,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseToolsetConfig(map[string]any{
				"specification_url": "http://example.com/spec.json",
				"auth":              tc.authMap,
			})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if cfg.Auth == nil || cfg.Auth.Type != tc.wantType {
				t.Errorf("Auth.Type = %q, want %q", cfg.Auth.Type, tc.wantType)
			}
		})
	}
}

func TestParseToolsetConfig_ServiceAccount(t *testing.T) {
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com/spec.json",
		"auth": map[string]any{
			"type":                 "serviceaccount",
			"service_account_json": `{"type":"service_account","client_email":"x@y.iam.gserviceaccount.com"}`,
			"scopes":               []any{"https://www.googleapis.com/auth/cloud-platform"},
		},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Auth.Type != "serviceaccount" {
		t.Errorf("Type = %q", cfg.Auth.Type)
	}
	if cfg.Auth.ServiceAccountJSON == "" {
		t.Error("ServiceAccountJSON should be set")
	}
	if len(cfg.Auth.Scopes) != 1 {
		t.Errorf("Scopes = %v", cfg.Auth.Scopes)
	}
}

func writeTempYAMLSpec(t *testing.T) string {
	t.Helper()
	const specYAML = `openapi: 3.0.0
info:
  title: YAML Spec
  version: "1.0"
servers:
  - url: http://example.com
paths:
  /ping:
    get:
      operationId: ping
`
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, []byte(specYAML), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

func TestValidateBasicAuthConfig_WithYAMLSpec(t *testing.T) {
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_file": writeTempYAMLSpec(t),
		"auth": map[string]any{
			"type":     "basic",
			"username": "yamluser",
			"password": "yamlpass",
		},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.Type != AuthTypeBasic {
		t.Fatalf("Auth.Type = %v, want basic", cfg.Auth)
	}
	if validErr := validateAuthConfig(cfg.Auth); validErr != nil {
		t.Errorf("validateAuthConfig: %v", validErr)
	}
	doc, err := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg)
	if err != nil {
		t.Fatalf("loadSpec: %v", err)
	}
	if doc.Info == nil || doc.Info.Title != "YAML Spec" {
		t.Errorf("spec title = %v, want YAML Spec", doc.Info)
	}
}

func TestValidateBearerAuthConfig_WithYAMLSpec(t *testing.T) {
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_file": writeTempYAMLSpec(t),
		"auth": map[string]any{
			"type":  "bearer",
			"token": "yaml-bearer-token",
		},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.Type != AuthTypeBearer {
		t.Fatalf("Auth.Type = %v, want bearer", cfg.Auth)
	}
	if cfg.Auth.Token != "yaml-bearer-token" {
		t.Errorf("Token = %q, want yaml-bearer-token", cfg.Auth.Token)
	}
	if validErr := validateAuthConfig(cfg.Auth); validErr != nil {
		t.Errorf("validateAuthConfig: %v", validErr)
	}
	if _, loadErr := NewManager(slog.Default(), nil).loadSpec(t.Context(), cfg); loadErr != nil {
		t.Fatalf("loadSpec: %v", loadErr)
	}
}

func TestNewAuthHandlerForOpenAPI_Bearer_ReturnsNil(t *testing.T) {
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com/spec.json",
		"auth": map[string]any{
			"type":  "bearer",
			"token": "test-bearer-token-12345",
		},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.Type != AuthTypeBearer {
		t.Fatalf("Auth.Type = %v, want bearer", cfg.Auth)
	}

	h, err := NewAuthHandlerForOpenAPI(cfg.Auth, slog.Default())
	if err != nil {
		t.Fatalf("NewAuthHandlerForOpenAPI: %v", err)
	}
	if h != nil {
		t.Errorf("bearer auth should not produce an AuthHandler; got %+v", h)
	}
}

func TestParseToolsetConfig_NilConfig(t *testing.T) {
	_, err := ParseToolsetConfig(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "required") {
		t.Errorf("err should mention 'required', got: %v", err)
	}
}

func TestParseScopes(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []string
	}{
		{"nil", nil, nil},
		{"empty list", []any{}, nil},
		{"list of strings", []any{"a", "b"}, []string{"a", "b"}},
		{"list with non-string", []any{"a", 42, "b"}, []string{"a", "b"}},
		{"empty string", "", nil},
		{"single space", "   ", nil},
		{"space-separated", "read write", []string{"read", "write"}},
		{"tab-separated", "a\tb\nc", []string{"a", "b", "c"}},
		{"go []string", []string{"x", "", "y"}, []string{"x", "y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseScopes(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
