// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"strings"
	"testing"
)

func TestParseConfig_CORSFields(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"transport": "sse",
		"cors_allowed_origins": []any{
			"http://127.0.0.1:3010",
			"http://localhost:6274",
		},
		"cors_allowed_origin_regex": `^http://localhost:\d+$`,
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(cfg.CORSAllowedOrigins) != 2 ||
		cfg.CORSAllowedOrigins[0] != "http://127.0.0.1:3010" ||
		cfg.CORSAllowedOrigins[1] != "http://localhost:6274" {
		t.Errorf("CORSAllowedOrigins = %v", cfg.CORSAllowedOrigins)
	}
	if cfg.CORSAllowedOriginRegex != `^http://localhost:\d+$` {
		t.Errorf("CORSAllowedOriginRegex = %q", cfg.CORSAllowedOriginRegex)
	}
	if cfg.compiledCORSRegex == nil {
		t.Fatal("compiledCORSRegex is nil; regex should compile at parse time")
	}
	if !cfg.compiledCORSRegex.MatchString("http://localhost:6274") {
		t.Error("compiled regex should match http://localhost:6274")
	}
	if cfg.compiledCORSRegex.MatchString("http://evil.com") {
		t.Error("compiled regex should NOT match http://evil.com")
	}
}

func TestParseConfig_CORSRegex_Invalid(t *testing.T) {
	_, err := parseConfig(map[string]any{
		"transport":                 "sse",
		"cors_allowed_origin_regex": "([unterminated",
	})
	if err == nil {
		t.Fatal("expected error for malformed cors_allowed_origin_regex, got nil")
	}
	if !strings.Contains(err.Error(), "cors_allowed_origin_regex") {
		t.Errorf("error should mention cors_allowed_origin_regex: %v", err)
	}
}

func TestParseConfig_AuthFieldsExplicit(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"transport":               "sse",
		"enable_auth":             true,
		"external_auth_provider":  "azure",
		"require_pkce":            false,
		"oauth_code_ttl_seconds":  600,
		"oauth_state_ttl_seconds": 900,
		"issuer":                  "https://mcp.example.com",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if !cfg.EnableAuth {
		t.Error("EnableAuth = false (want true)")
	}
	if cfg.ExternalAuthProvider != "azure" {
		t.Errorf("ExternalAuthProvider = %q (want azure)", cfg.ExternalAuthProvider)
	}
	if cfg.RequirePKCE {
		t.Error("RequirePKCE = true (want false; explicit override)")
	}
	if cfg.OAuthCodeTTLSeconds != 600 {
		t.Errorf("OAuthCodeTTLSeconds = %d (want 600)", cfg.OAuthCodeTTLSeconds)
	}
	if cfg.OAuthStateTTLSeconds != 900 {
		t.Errorf("OAuthStateTTLSeconds = %d (want 900)", cfg.OAuthStateTTLSeconds)
	}
	if cfg.Issuer != "https://mcp.example.com" {
		t.Errorf("Issuer = %q", cfg.Issuer)
	}
}

func TestParseConfig_AuthDefaults(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"transport":              "sse",
		"enable_auth":            true,
		"external_auth_provider": "keycloak",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if !cfg.RequirePKCE {
		t.Error("RequirePKCE = false (want true; default-on)")
	}
	if cfg.OAuthCodeTTLSeconds != 300 {
		t.Errorf("OAuthCodeTTLSeconds = %d (want 300; default)", cfg.OAuthCodeTTLSeconds)
	}
	if cfg.OAuthStateTTLSeconds != 300 {
		t.Errorf("OAuthStateTTLSeconds = %d (want 300; default)", cfg.OAuthStateTTLSeconds)
	}
	if cfg.Issuer != "" {
		t.Errorf("Issuer = %q (want empty; defaults to host:port at runtime)", cfg.Issuer)
	}
}

func TestParseConfig_AuthDisabledByDefault(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"transport": "sse",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.EnableAuth {
		t.Error("EnableAuth = true (want false; default-off)")
	}
	if !cfg.RequirePKCE {
		t.Error("RequirePKCE = false (want true; default-on)")
	}
}

func TestParseConfig_TransportHTTPAliasesSSE(t *testing.T) {
	cfg, err := parseConfig(map[string]any{"transport": "http"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.Transport != "sse" {
		t.Errorf("Transport = %q, want %q (http should normalize to sse)", cfg.Transport, "sse")
	}
}

func TestParseConfig_TransportUnknownRejected(t *testing.T) {
	_, err := parseConfig(map[string]any{"transport": "websocket"})
	if err == nil {
		t.Fatal("expected error for unknown transport, got nil")
	}
	if !strings.Contains(err.Error(), "websocket") {
		t.Errorf("error should mention the bad value, got: %v", err)
	}
}

func TestParseConfig_DefaultUserIDClaim(t *testing.T) {
	cfg, err := parseConfig(map[string]any{"transport": "sse"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.UserIDClaim != "email" {
		t.Errorf("default UserIDClaim: got %q, want %q", cfg.UserIDClaim, "email")
	}
	if cfg.DevMode != false {
		t.Errorf("default DevMode: got %v, want false", cfg.DevMode)
	}
	if cfg.DefaultUserIdentity != "" {
		t.Errorf("default DefaultUserIdentity: got %q, want \"\"", cfg.DefaultUserIdentity)
	}
}

func TestConfig_Validate_RejectsUnknownUserIDClaim(t *testing.T) {
	_, err := parseConfig(map[string]any{
		"transport":     "sse",
		"user_id_claim": "username",
	})
	if err == nil {
		t.Fatal("expected validation error for unknown user_id_claim")
	}
	if !strings.Contains(err.Error(), "user_id_claim") {
		t.Errorf("error should mention user_id_claim: %v", err)
	}
}

func TestConfig_Validate_AcceptsAllValidClaims(t *testing.T) {
	for _, claim := range []string{"email", "sub", "upn", "preferred_username"} {
		t.Run(claim, func(t *testing.T) {
			_, err := parseConfig(map[string]any{
				"transport":     "sse",
				"user_id_claim": claim,
			})
			if err != nil {
				t.Errorf("claim %q rejected: %v", claim, err)
			}
		})
	}
}

func TestConfig_Validate_RejectsDefaultUserIdentityWithEnableAuth(t *testing.T) {
	_, err := parseConfig(map[string]any{
		"transport":              "sse",
		"enable_auth":            true,
		"default_user_identity":  "alice",
		"external_auth_provider": "keycloak",
	})
	if err == nil {
		t.Fatal("expected validation error for enable_auth + default_user_identity")
	}
	if !strings.Contains(err.Error(), "default_user_identity") {
		t.Errorf("error should mention default_user_identity: %v", err)
	}
	if !strings.Contains(err.Error(), "enable_auth") {
		t.Errorf("error should mention enable_auth: %v", err)
	}
}

func TestConfig_Validate_AllowsDefaultUserIdentityWithStdio(t *testing.T) {
	_, err := parseConfig(map[string]any{
		"transport":             "stdio",
		"enable_auth":           true,
		"default_user_identity": "alice",
	})
	if err != nil {
		t.Errorf("stdio + default_user_identity should pass: %v", err)
	}
}

func TestConfig_Validate_AllowsDefaultUserIdentityWithoutAuth(t *testing.T) {
	_, err := parseConfig(map[string]any{
		"transport":             "sse",
		"default_user_identity": "alice",
	})
	if err != nil {
		t.Errorf("default_user_identity without enable_auth should pass: %v", err)
	}
}

func TestConfig_Validate_RejectsSystemPrefixDefaultUserIdentity(t *testing.T) {
	for _, identity := range []string{"system:default", "system:orders-bot"} {
		_, err := parseConfig(map[string]any{
			"transport":             "sse",
			"default_user_identity": identity,
		})
		if err == nil {
			t.Fatalf("default_user_identity %q should be rejected", identity)
		}
		if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("unexpected error for %q: %v", identity, err)
		}
	}
}

func TestParseConfig_UserIDClaimRoundTrip(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"transport":     "sse",
		"user_id_claim": "upn",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.UserIDClaim != "upn" {
		t.Errorf("UserIDClaim: got %q, want %q", cfg.UserIDClaim, "upn")
	}
}

func TestParseConfig_DevModeRoundTrip(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"transport": "sse",
		"dev_mode":  true,
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if !cfg.DevMode {
		t.Error("DevMode: got false, want true")
	}
}

func TestConfig_StateRedirectionParsing(t *testing.T) {
	raw := map[string]any{
		"transport":                 "sse",
		"state_redirection_enabled": true,
		"oauth_dispatcher_url":      "https://webui.example.com/api/v1/auth/callback",
		"enable_auth":               true,
		"external_auth_provider":    "azure",
		"allowed_redirect_uris":     []any{"http://localhost:8081/cb"},
	}
	cfg, err := parseConfig(raw)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if !cfg.StateRedirectionEnabled {
		t.Fatal("StateRedirectionEnabled not parsed")
	}
	if cfg.OAuthDispatcherURL != "https://webui.example.com/api/v1/auth/callback" {
		t.Fatalf("OAuthDispatcherURL: got %q", cfg.OAuthDispatcherURL)
	}
}

func TestConfig_StateRedirectionDefaultsOff(t *testing.T) {
	cfg, err := parseConfig(map[string]any{"transport": "sse"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.StateRedirectionEnabled {
		t.Fatal("StateRedirectionEnabled should default to false")
	}
}

func TestConfig_StateRedirectionRequiresDispatcherURL(t *testing.T) {
	raw := map[string]any{
		"transport":                 "sse",
		"state_redirection_enabled": true,
		"enable_auth":               true,
		"external_auth_provider":    "azure",
	}
	_, err := parseConfig(raw)
	if err == nil {
		t.Fatal("expected validation error when state_redirection_enabled=true and oauth_dispatcher_url is empty")
	}
}

func TestConfig_StateRedirectionRequiresEnableAuth(t *testing.T) {
	raw := map[string]any{
		"transport":                 "sse",
		"state_redirection_enabled": true,
		"oauth_dispatcher_url":      "https://webui.example.com/api/v1/auth/callback",
		"enable_auth":               false,
	}
	_, err := parseConfig(raw)
	if err == nil {
		t.Fatal("expected validation error when state_redirection_enabled=true and enable_auth=false")
	}
}

func TestParseConfig_ArtifactDefaults(t *testing.T) {
	cfg, err := parseConfig(map[string]any{"transport": "sse"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.InlineImageMaxBytes != 5_242_880 {
		t.Errorf("InlineImageMaxBytes = %d, want 5242880", cfg.InlineImageMaxBytes)
	}
	if cfg.InlineAudioMaxBytes != 10_485_760 {
		t.Errorf("InlineAudioMaxBytes = %d, want 10485760", cfg.InlineAudioMaxBytes)
	}
	if cfg.InlineTextMaxBytes != 1_048_576 {
		t.Errorf("InlineTextMaxBytes = %d, want 1048576", cfg.InlineTextMaxBytes)
	}
	if cfg.InlineBinaryMaxBytes != 524_288 {
		t.Errorf("InlineBinaryMaxBytes = %d, want 524288", cfg.InlineBinaryMaxBytes)
	}
	if cfg.ResourceURIPrefix != "artifact" {
		t.Errorf("ResourceURIPrefix = %q, want artifact", cfg.ResourceURIPrefix)
	}
	if !cfg.EnableArtifactResources {
		t.Error("EnableArtifactResources = false, want true (default-on)")
	}
}

func TestParseConfig_ArtifactOverrides(t *testing.T) {
	cfg, err := parseConfig(map[string]any{
		"transport":                 "sse",
		"inline_image_max_bytes":    1024,
		"inline_audio_max_bytes":    float64(2048),
		"inline_text_max_bytes":     4096,
		"inline_binary_max_bytes":   8192,
		"resource_uri_prefix":       "samfile",
		"enable_artifact_resources": false,
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.InlineImageMaxBytes != 1024 {
		t.Errorf("InlineImageMaxBytes = %d, want 1024", cfg.InlineImageMaxBytes)
	}
	if cfg.InlineAudioMaxBytes != 2048 {
		t.Errorf("InlineAudioMaxBytes = %d, want 2048 (from float64)", cfg.InlineAudioMaxBytes)
	}
	if cfg.ResourceURIPrefix != "samfile" {
		t.Errorf("ResourceURIPrefix = %q, want samfile", cfg.ResourceURIPrefix)
	}
	if cfg.EnableArtifactResources {
		t.Error("EnableArtifactResources = true, want false (explicitly disabled)")
	}
}
