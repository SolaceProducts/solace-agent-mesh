// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestParseProxyConfig_NilLoggerFallsBackToDefault(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"url": "http://example.com",
		"authentication": map[string]any{
			"scheme": "bearer",
			"token":  "t",
		},
	})

	pc, err := ParseProxyConfig("test", cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pc.LegacyAuth == nil || pc.LegacyAuth.Type != "static_bearer" {
		t.Fatalf("expected legacy auth to be mapped to static_bearer; got %+v", pc.LegacyAuth)
	}
}

func TestParseProxyConfig_ProtocolVersion(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{"absent defaults to auto", nil, ProtocolVersionAuto},
		{"auto", "auto", ProtocolVersionAuto},
		{"quoted 1.0", "1.0", ProtocolVersion10},
		{"unquoted YAML 1.0 decodes as a float", 1.0, ProtocolVersion10},
		{"0.3", "0.3", ProtocolVersion03},
		{"unquoted YAML 0.3 decodes as a float", 0.3, ProtocolVersion03},
		{"0.3.0", "0.3.0", ProtocolVersion03},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]any{"url": "http://downstream.example.com"}
			if tt.raw != nil {
				m["protocol_version"] = tt.raw
			}
			pc, err := ParseProxyConfig("my-proxy", config.FromMap(m), discardLogger())
			if err != nil {
				t.Fatal(err)
			}
			if pc.ProtocolVersion != tt.want {
				t.Errorf("ProtocolVersion = %q, want %q", pc.ProtocolVersion, tt.want)
			}
		})
	}
}

func TestParseProxyConfig_ProtocolVersionInvalid(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"url":              "http://downstream.example.com",
		"protocol_version": "0.2",
	})
	_, err := ParseProxyConfig("my-proxy", cfg, discardLogger())
	if err == nil || !strings.Contains(err.Error(), "protocol_version") {
		t.Fatalf("err = %v, want a protocol_version error", err)
	}
}

func TestParseProxyConfig_CardTrust(t *testing.T) {
	t.Run("trusted URLs and require", func(t *testing.T) {
		pc, err := ParseProxyConfig("my-proxy", config.FromMap(map[string]any{
			"url":                    "http://downstream.example.com",
			"trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json", " http://127.0.0.1:8080/keys "},
			"require_signed_card":    " TRUE ",
		}), discardLogger())
		if err != nil {
			t.Fatal(err)
		}
		if len(pc.TrustedCardJWKSURLs) != 2 || pc.TrustedCardJWKSURLs[1] != "http://127.0.0.1:8080/keys" || !pc.RequireSignedCard {
			t.Errorf("TrustedCardJWKSURLs = %q RequireSignedCard = %v", pc.TrustedCardJWKSURLs, pc.RequireSignedCard)
		}
	})

	t.Run("require spelled false", func(t *testing.T) {
		pc, err := ParseProxyConfig("my-proxy", config.FromMap(map[string]any{
			"url":                    "http://downstream.example.com",
			"trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json"},
			"require_signed_card":    "off",
		}), discardLogger())
		if err != nil {
			t.Fatal(err)
		}
		if pc.RequireSignedCard {
			t.Error("RequireSignedCard = true, want false")
		}
	})

	t.Run("absent leaves signatures unchecked", func(t *testing.T) {
		pc, err := ParseProxyConfig("my-proxy", config.FromMap(map[string]any{"url": "http://downstream.example.com"}), discardLogger())
		if err != nil {
			t.Fatal(err)
		}
		if pc.TrustedCardJWKSURLs != nil || pc.RequireSignedCard {
			t.Errorf("TrustedCardJWKSURLs = %q RequireSignedCard = %v", pc.TrustedCardJWKSURLs, pc.RequireSignedCard)
		}
	})

	invalid := []struct {
		name string
		m    map[string]any
		want string
	}{
		{"not a list", map[string]any{"trusted_card_jwks_urls": "https://keys.example.com/jwks.json"}, "must be a list"},
		{"not a string", map[string]any{"trusted_card_jwks_urls": []any{42}}, "entry 1 must be a string"},
		{"relative URL", map[string]any{"trusted_card_jwks_urls": []any{"/jwks.json"}}, "entry 1 must be an absolute"},
		{"other scheme", map[string]any{"trusted_card_jwks_urls": []any{"https://ok.example.com", "file:///etc/keys"}}, "entry 2 must be an absolute"},
		{"require without trusted URLs", map[string]any{"require_signed_card": true}, "needs 'trusted_card_jwks_urls'"},
		{"plain http off the loopback host", map[string]any{"trusted_card_jwks_urls": []any{"http://keys.example.com/jwks.json"}}, "entry 1 must be an absolute https URL"},
		{"require is not a boolean", map[string]any{"trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json"}, "require_signed_card": "enabled"}, "'require_signed_card' must be true or false"},
		{"require is a number", map[string]any{"trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json"}, "require_signed_card": 1}, "'require_signed_card' must be true or false"},
		{"static card", map[string]any{"agent_card_data": map[string]any{"name": "static"}, "trusted_card_jwks_urls": []any{"https://keys.example.com/jwks.json"}}, "cannot be used with 'agent_card_data'"},
		{"static card with require alone", map[string]any{"agent_card_data": map[string]any{"name": "static"}, "require_signed_card": true}, "cannot be used with 'agent_card_data'"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			tt.m["url"] = "http://downstream.example.com"
			_, err := ParseProxyConfig("my-proxy", config.FromMap(tt.m), discardLogger())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestEffectiveProtocolVersion_EmptyIsAuto(t *testing.T) {
	if got := (&ProxyConfig{}).EffectiveProtocolVersion(); got != ProtocolVersionAuto {
		t.Errorf("EffectiveProtocolVersion = %q, want %q", got, ProtocolVersionAuto)
	}
}

func TestParseProxyConfig_Basic(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"url":       "http://downstream.example.com",
		"namespace": "test-ns",
	})

	pc, err := ParseProxyConfig("my-proxy", cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	if pc.Name != "my-proxy" {
		t.Errorf("Name = %q", pc.Name)
	}
	if pc.Namespace != "test-ns" {
		t.Errorf("Namespace = %q", pc.Namespace)
	}
	if pc.URL != "http://downstream.example.com" {
		t.Errorf("URL = %q", pc.URL)
	}
}

func TestParseProxyConfig_Defaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"url": "http://example.com",
	})

	pc, err := ParseProxyConfig("test", cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	if pc.Namespace != "solace-agent-mesh" {
		t.Errorf("default namespace = %q", pc.Namespace)
	}
	if pc.AgentCardPath != "/.well-known/agent-card.json" {
		t.Errorf("default agent card path = %q", pc.AgentCardPath)
	}
	if !pc.SSLVerify {
		t.Error("expected SSL verify to default to true")
	}
	if pc.DefaultRequestTimeoutSeconds != 300 {
		t.Errorf("default timeout = %d, want 300", pc.DefaultRequestTimeoutSeconds)
	}
	if pc.DiscoveryIntervalSeconds != 60 {
		t.Errorf("default discovery interval = %d", pc.DiscoveryIntervalSeconds)
	}
	if !pc.UseAgentCardURL {
		t.Error("expected use_agent_card_url to default to true")
	}
	if pc.ArtifactHandlingMode != "reference" {
		t.Errorf("default artifact mode = %q", pc.ArtifactHandlingMode)
	}
	if pc.TaskStateTTLMinutes != 60 {
		t.Errorf("default task TTL = %d", pc.TaskStateTTLMinutes)
	}
	if pc.TaskCleanupIntervalMinutes != 10 {
		t.Errorf("default cleanup interval = %d", pc.TaskCleanupIntervalMinutes)
	}
}

func TestParseProxyConfig_MissingURL(t *testing.T) {
	cfg := config.FromMap(map[string]any{})

	_, err := ParseProxyConfig("test", cfg, discardLogger())
	if err == nil {
		t.Error("expected error for missing URL")
	}
}

func TestParseProxyConfig_StaticCardNoURL(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"agent_card_data": map[string]any{
			"name":        "static-agent",
			"description": "A static agent",
		},
	})

	pc, err := ParseProxyConfig("test", cfg, discardLogger())
	if err != nil {
		t.Fatal("expected success with static card data:", err)
	}
	if pc.AgentCardData == nil {
		t.Error("expected agent card data")
	}
}

func TestParseProxyConfig_EmptyName(t *testing.T) {
	cfg := config.FromMap(map[string]any{"url": "http://example.com"})
	_, err := ParseProxyConfig("", cfg, discardLogger())
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestParseProxyConfig_AuthConfigs(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"url": "http://example.com",
		"task_authentication": map[string]any{
			"type":  "static_bearer",
			"token": "my-token",
		},
		"agent_card_authentication": map[string]any{
			"type":   "static_apikey",
			"token":  "card-key",
			"header": "X-API-Key",
		},
	})

	pc, err := ParseProxyConfig("test", cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	if pc.TaskAuth == nil {
		t.Fatal("expected task auth")
	}
	if pc.TaskAuth.Type != "static_bearer" {
		t.Errorf("task auth type = %q", pc.TaskAuth.Type)
	}
	if pc.TaskAuth.Token != "my-token" {
		t.Errorf("task auth token = %q", pc.TaskAuth.Token)
	}

	if pc.AgentCardAuth == nil {
		t.Fatal("expected agent card auth")
	}
	if pc.AgentCardAuth.Type != "static_apikey" {
		t.Errorf("card auth type = %q", pc.AgentCardAuth.Type)
	}
}

func TestParseProxyConfig_CustomHeaders(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"url": "http://example.com",
		"task_headers": []any{
			map[string]any{"name": "X-Custom", "value": "val1"},
			map[string]any{"name": "X-Other", "value": "val2"},
		},
	})

	pc, err := ParseProxyConfig("test", cfg, discardLogger())
	if err != nil {
		t.Fatal(err)
	}

	if len(pc.TaskHeaders) != 2 {
		t.Fatalf("got %d task headers, want 2", len(pc.TaskHeaders))
	}
	if pc.TaskHeaders[0].Name != "X-Custom" {
		t.Errorf("header[0].Name = %q", pc.TaskHeaders[0].Name)
	}
}

func TestEffectiveRequestTimeout(t *testing.T) {
	pc := &ProxyConfig{RequestTimeoutSeconds: 60, DefaultRequestTimeoutSeconds: 120}
	if got := pc.EffectiveRequestTimeout(); got != 60 {
		t.Errorf("got %d, want 60", got)
	}

	pc = &ProxyConfig{DefaultRequestTimeoutSeconds: 120}
	if got := pc.EffectiveRequestTimeout(); got != 120 {
		t.Errorf("got %d, want 120", got)
	}

	pc = &ProxyConfig{}
	if got := pc.EffectiveRequestTimeout(); got != 300 {
		t.Errorf("got %d, want 300", got)
	}
}

func TestEffectiveTaskAuth(t *testing.T) {
	legacy := &AuthConfig{Type: "static_bearer", Token: "legacy"}
	task := &AuthConfig{Type: "static_bearer", Token: "task"}

	pc := &ProxyConfig{TaskAuth: task, LegacyAuth: legacy}
	if got := pc.EffectiveTaskAuth(); got != task {
		t.Error("expected task auth to take priority")
	}

	pc = &ProxyConfig{LegacyAuth: legacy}
	if got := pc.EffectiveTaskAuth(); got != legacy {
		t.Error("expected legacy fallback")
	}

	pc = &ProxyConfig{}
	if got := pc.EffectiveTaskAuth(); got != nil {
		t.Error("expected nil")
	}
}

func TestEffectiveCardAuth(t *testing.T) {
	legacy := &AuthConfig{Type: "static_bearer", Token: "legacy"}
	card := &AuthConfig{Type: "static_apikey", Token: "card"}

	pc := &ProxyConfig{AgentCardAuth: card, LegacyAuth: legacy}
	if got := pc.EffectiveCardAuth(); got != card {
		t.Error("expected card auth to take priority")
	}

	pc = &ProxyConfig{LegacyAuth: legacy, UseAuthForAgentCard: true}
	if got := pc.EffectiveCardAuth(); got != legacy {
		t.Error("expected legacy with flag")
	}

	pc = &ProxyConfig{LegacyAuth: legacy, UseAuthForAgentCard: false}
	if got := pc.EffectiveCardAuth(); got != nil {
		t.Error("expected nil without flag")
	}
}

func TestParseHeaders_Nil(t *testing.T) {
	headers := parseHeaders(nil)
	if headers != nil {
		t.Error("expected nil")
	}
}

func TestParseHeaders_InvalidType(t *testing.T) {
	headers := parseHeaders("not-a-list")
	if headers != nil {
		t.Error("expected nil for non-list")
	}
}

func TestParseAuthConfig_NewOAuth2CCFields(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"type":                         "oauth2_client_credentials",
		"token_url":                    "https://auth.example.com/token",
		"client_id":                    "my-id",
		"client_secret":                "my-secret",
		"ca_cert_path":                 "/etc/ssl/ca.pem",
		"max_retries":                  5,
		"token_refresh_buffer_seconds": 120,
		"insecure_skip_verify":         true,
	})

	ac := parseAuthConfig(cfg, discardLogger())
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.CACertPath != "/etc/ssl/ca.pem" {
		t.Errorf("CACertPath = %q, want /etc/ssl/ca.pem", ac.CACertPath)
	}
	if ac.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", ac.MaxRetries)
	}
	if ac.TokenRefreshBufferSeconds != 120 {
		t.Errorf("TokenRefreshBufferSeconds = %d, want 120", ac.TokenRefreshBufferSeconds)
	}
	if !ac.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}
}

func TestParseAuthConfig_DeprecationMapping(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"type":                         "oauth2_client_credentials",
		"token_url":                    "https://auth.example.com/token",
		"client_id":                    "my-id",
		"client_secret":                "my-secret",
		"token_cache_duration_seconds": 3300,
	})

	ac := parseAuthConfig(cfg, discardLogger())
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.TokenRefreshBufferSeconds != 300 {
		t.Errorf("TokenRefreshBufferSeconds = %d, want 300 (derived from deprecated field)", ac.TokenRefreshBufferSeconds)
	}
}

func TestParseAuthConfig_DeprecationMapping_LargeValue(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"type":                         "oauth2_client_credentials",
		"token_url":                    "https://auth.example.com/token",
		"client_id":                    "my-id",
		"client_secret":                "my-secret",
		"token_cache_duration_seconds": 7200,
	})

	ac := parseAuthConfig(cfg, discardLogger())
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.TokenRefreshBufferSeconds != 300 {
		t.Errorf("TokenRefreshBufferSeconds = %d, want 300 (fallback for large deprecated value)", ac.TokenRefreshBufferSeconds)
	}
}

func TestParseAuthConfig_NoDeprecatedFieldLeavesBufferUnset(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"type":          "oauth2_client_credentials",
		"token_url":     "https://auth.example.com/token",
		"client_id":     "my-id",
		"client_secret": "my-secret",
	})

	ac := parseAuthConfig(cfg, discardLogger())
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.TokenRefreshBufferSeconds != 0 {
		t.Errorf("TokenRefreshBufferSeconds = %d, want 0 (unset; oauth2cc defaults to 300)", ac.TokenRefreshBufferSeconds)
	}
}

func TestParseAuthConfig_DeprecationMapping_SubFloorValue(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"type":                         "oauth2_client_credentials",
		"token_url":                    "https://auth.example.com/token",
		"client_id":                    "my-id",
		"client_secret":                "my-secret",
		"token_cache_duration_seconds": 3500,
	})

	ac := parseAuthConfig(cfg, discardLogger())
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.TokenRefreshBufferSeconds != 100 {
		t.Errorf("TokenRefreshBufferSeconds = %d, want 100 (3600-3500, no floor applied)", ac.TokenRefreshBufferSeconds)
	}
}

func TestParseAuthConfig_NewFieldOverridesDeprecated(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"type":                         "oauth2_client_credentials",
		"token_url":                    "https://auth.example.com/token",
		"client_id":                    "my-id",
		"client_secret":                "my-secret",
		"token_cache_duration_seconds": 3300,
		"token_refresh_buffer_seconds": 60,
	})

	ac := parseAuthConfig(cfg, discardLogger())
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.TokenRefreshBufferSeconds != 60 {
		t.Errorf("TokenRefreshBufferSeconds = %d, want 60 (new field takes priority)", ac.TokenRefreshBufferSeconds)
	}
}

func TestParseAuthConfig_ClientCredentials_ScopesListIgnored(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	cfg := config.FromMap(map[string]any{
		"type":          "oauth2_client_credentials",
		"token_url":     "https://auth.example.com/token",
		"client_id":     "my-id",
		"client_secret": "my-secret",
		"scopes":        []any{"profile", "email"},
	})

	ac := parseAuthConfig(cfg, logger)
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.Scope != "" {
		t.Errorf("Scope = %q, want empty (list was set, not string)", ac.Scope)
	}
	if len(ac.Scopes) != 2 || ac.Scopes[0] != "profile" || ac.Scopes[1] != "email" {
		t.Errorf("Scopes = %v, want [profile email] (parsed but unused for this type)", ac.Scopes)
	}

	out := buf.String()
	if !strings.Contains(out, "ignores the list 'scopes' key") {
		t.Errorf("expected scopes-list warning in log output; got: %q", out)
	}
}

func TestParseAuthConfig_AuthorizationCode_Minimal(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"type":              "oauth2_authorization_code",
		"authorization_url": "https://auth.example.com/authorize",
		"token_url":         "https://auth.example.com/token",
		"client_id":         "my-id",
		"client_secret":     "my-secret",
		"redirect_uri":      "https://app.example.com/cb",
		"scopes":            []any{"profile", "email"},
	})

	ac := parseAuthConfig(cfg, discardLogger())
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.Type != "oauth2_authorization_code" {
		t.Errorf("Type = %q", ac.Type)
	}
	if ac.AuthorizationURL != "https://auth.example.com/authorize" {
		t.Errorf("AuthorizationURL = %q", ac.AuthorizationURL)
	}
	if ac.TokenURL != "https://auth.example.com/token" {
		t.Errorf("TokenURL = %q", ac.TokenURL)
	}
	if ac.ClientID != "my-id" {
		t.Errorf("ClientID = %q", ac.ClientID)
	}
	if ac.RedirectURI != "https://app.example.com/cb" {
		t.Errorf("RedirectURI = %q", ac.RedirectURI)
	}
	if len(ac.Scopes) != 2 {
		t.Errorf("Scopes = %v", ac.Scopes)
	}
}

func TestParseAuthConfig_AuthorizationCode_MissingRequiredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := config.FromMap(map[string]any{
		"type": "oauth2_authorization_code",
	})

	ac := parseAuthConfig(cfg, logger)
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig even when required fields are missing")
	}
	if ac.Type != "oauth2_authorization_code" {
		t.Errorf("Type = %q", ac.Type)
	}
	if ac.ClientID != "" || ac.AuthorizationURL != "" || ac.TokenURL != "" || ac.RedirectURI != "" {
		t.Errorf("expected all required fields empty; got %+v", ac)
	}

	out := buf.String()
	for _, want := range []string{
		"requires 'client_id'",
		"requires 'authorization_url'",
		"requires 'token_url'",
		"'redirect_uri' not set",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected log output to contain %q; got: %q", want, out)
		}
	}
}

func TestParseAuthConfig_AuthorizationCode_ScopeStringWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	cfg := config.FromMap(map[string]any{
		"type":              "oauth2_authorization_code",
		"authorization_url": "https://auth.example.com/authorize",
		"token_url":         "https://auth.example.com/token",
		"client_id":         "my-id",
		"client_secret":     "my-secret",
		"redirect_uri":      "https://app.example.com/cb",
		"scope":             "profile email",
	})

	ac := parseAuthConfig(cfg, logger)
	if ac == nil {
		t.Fatal("expected non-nil AuthConfig")
	}
	if ac.Scope != "profile email" {
		t.Errorf("Scope = %q", ac.Scope)
	}
	if len(ac.Scopes) != 0 {
		t.Errorf("Scopes = %v, want empty (list form not used)", ac.Scopes)
	}

	out := buf.String()
	if !strings.Contains(out, "ignores the string 'scope' key") {
		t.Errorf("expected scope/scopes mismatch warning in log output; got: %q", out)
	}
}

func TestParseHeaders_SkipsInvalid(t *testing.T) {
	headers := parseHeaders([]any{
		"not-a-map",
		map[string]any{"value": "no-name"},
		map[string]any{"name": "Valid", "value": "yes"},
	})
	if len(headers) != 1 {
		t.Fatalf("got %d headers, want 1", len(headers))
	}
	if headers[0].Name != "Valid" {
		t.Errorf("name = %q", headers[0].Name)
	}
}

func TestParseProxyConfig_TrustedKeySetHosts(t *testing.T) {
	tests := []struct {
		url   string
		valid bool
	}{
		{"https://keys.example.com/jwks.json", true},
		{"https://10.0.0.5/jwks.json", true},
		{"http://127.0.0.1/jwks.json", true},
		{"http://127.0.0.2:9000/jwks.json", true},
		{"http://[::1]:9000/jwks.json", true},
		{"http://localhost/jwks.json", true},
		{"http://LOCALHOST/jwks.json", true},
		{"http://10.0.0.5/jwks.json", false},
		{"http://192.168.1.1/jwks.json", false},
		{"http://localhost.evil.com/jwks.json", false},
		{"http://127.0.0.1.evil.com/jwks.json", false},
		{"http://keys.example.com/jwks.json", false},
		{"ftp://localhost/jwks.json", false},
		{"http://0.0.0.0/jwks.json", false},
		{"http://localhost./jwks.json", false},
		{"http://notlocalhost/jwks.json", false},
		{"http://127.0.0.1@evil.com/jwks.json", false},
		{"http://localhost@evil.com/jwks.json", false},
		{"https:///jwks.json", false},
	}
	for _, tt := range tests {
		_, err := ParseProxyConfig("my-proxy", config.FromMap(map[string]any{
			"url":                    "http://downstream.example.com",
			"trusted_card_jwks_urls": []any{tt.url},
		}), discardLogger())
		if (err == nil) != tt.valid {
			t.Errorf("%s: err = %v, want valid = %v", tt.url, err, tt.valid)
		}
	}
}

func TestParseProxyConfig_CardTrustValues(t *testing.T) {
	parse := func(m map[string]any) (*ProxyConfig, error) {
		m["url"] = "http://downstream.example.com"
		return ParseProxyConfig("my-proxy", config.FromMap(m), discardLogger())
	}
	for _, spelling := range []any{"yes", "On", true} {
		pc, err := parse(map[string]any{"trusted_card_jwks_urls": []any{"https://k.example.com/j"}, "require_signed_card": spelling})
		if err != nil || !pc.RequireSignedCard {
			t.Errorf("require_signed_card %v: RequireSignedCard = %v, err = %v, want true", spelling, pc != nil && pc.RequireSignedCard, err)
		}
	}
	for _, spelling := range []any{"no", " NO ", "false", "off", false} {
		pc, err := parse(map[string]any{"trusted_card_jwks_urls": []any{"https://k.example.com/j"}, "require_signed_card": spelling})
		if err != nil || pc.RequireSignedCard {
			t.Errorf("require_signed_card %v: RequireSignedCard = %v, err = %v, want false", spelling, pc != nil && pc.RequireSignedCard, err)
		}
	}
	for _, key := range []string{"require_signed_card", "trusted_card_jwks_urls"} {
		if _, err := parse(map[string]any{key: nil}); err == nil || !strings.Contains(err.Error(), "present but blank") {
			t.Errorf("%s left blank: err = %v, want the blank-value error", key, err)
		}
	}

	pc, err := parse(map[string]any{"trusted_card_jwks_urls": []any{"https://k.example.com/j", " https://k.example.com/j ", "https://other.example.com/j"}})
	if err != nil || !slices.Equal(pc.TrustedCardJWKSURLs, []string{"https://k.example.com/j", "https://other.example.com/j"}) {
		t.Errorf("duplicates: TrustedCardJWKSURLs = %v, err = %v, want each URL once", pc.TrustedCardJWKSURLs, err)
	}
	tooMany := make([]any, MaxTrustedCardJWKSURLs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("https://k%d.example.com/j", i)
	}
	if pc, err := parse(map[string]any{"trusted_card_jwks_urls": tooMany[:MaxTrustedCardJWKSURLs]}); err != nil || len(pc.TrustedCardJWKSURLs) != MaxTrustedCardJWKSURLs {
		t.Errorf("%d URLs: err = %v, want all accepted", MaxTrustedCardJWKSURLs, err)
	}
	if _, err := parse(map[string]any{"trusted_card_jwks_urls": tooMany}); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("%d URLs: err = %v, want the limit error", len(tooMany), err)
	}
}
