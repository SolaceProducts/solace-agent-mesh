// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func TestNewAuthProvider_NilConfig(t *testing.T) {
	p, err := newAuthProvider(nil, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p != nil {
		t.Error("expected nil provider for nil config")
	}
}

func TestApplyAuth_NilProvider(t *testing.T) {
	var p *authProvider
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := p.applyAuth(t.Context(), req); err != nil {
		t.Errorf("nil provider applyAuth should be no-op, got error: %v", err)
	}
}

func TestApplyAuth_StaticBearer(t *testing.T) {
	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:  "static_bearer",
		Token: "my-secret-token",
	}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := p.applyAuth(t.Context(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := req.Header.Get("Authorization")
	want := "Bearer my-secret-token"
	if got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}

func TestApplyAuth_StaticAPIKey_DefaultHeader(t *testing.T) {
	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:  "static_apikey",
		Token: "key-123",
	}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := p.applyAuth(t.Context(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := req.Header.Get("X-Api-Key")
	if got != "key-123" {
		t.Errorf("X-Api-Key = %q, want %q", got, "key-123")
	}
}

func TestApplyAuth_StaticAPIKey_CustomHeader(t *testing.T) {
	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:   "static_apikey",
		Token:  "key-456",
		Header: "X-Custom-Key",
	}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := p.applyAuth(t.Context(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := req.Header.Get("X-Custom-Key")
	if got != "key-456" {
		t.Errorf("X-Custom-Key = %q, want %q", got, "key-456")
	}
}

func TestApplyAuth_OAuth2AuthorizationCode(t *testing.T) {
	p, err := newAuthProvider(&proxy.AuthConfig{
		Type: "oauth2_authorization_code",
	}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	err = p.applyAuth(t.Context(), req)
	if err != ErrAuthCodeRequired {
		t.Errorf("expected ErrAuthCodeRequired, got %v", err)
	}
}

func TestApplyAuth_UnsupportedType(t *testing.T) {
	p, err := newAuthProvider(&proxy.AuthConfig{
		Type: "magic_auth",
	}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	err = p.applyAuth(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for unsupported auth type")
	}
	if !strings.Contains(err.Error(), "unsupported auth type") {
		t.Errorf("error = %v, want 'unsupported auth type'", err)
	}
}

func TestApplyAuth_OAuth2ClientCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.FormValue("grant_type") != "client_credentials" {
			t.Errorf("grant_type = %q", r.FormValue("grant_type"))
		}
		if r.FormValue("client_id") != "my-client" {
			t.Errorf("client_id = %q", r.FormValue("client_id"))
		}
		if r.FormValue("scope") != "read write" {
			t.Errorf("scope = %q", r.FormValue("scope"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "oauth-token-abc",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:                  "oauth2_client_credentials",
		TokenURL:              srv.URL,
		ClientID:              "my-client",
		ClientSecret:          "my-secret",
		Scope:                 "read write",
		AllowInsecureTokenURL: true,
	}, slog.Default())
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := p.applyAuth(t.Context(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := req.Header.Get("Authorization")
	if got != "Bearer oauth-token-abc" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer oauth-token-abc")
	}
}

func TestOAuth2TokenCaching(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-" + strings.Repeat("x", callCount),
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:                  "oauth2_client_credentials",
		TokenURL:              srv.URL,
		ClientID:              "c",
		ClientSecret:          "s",
		AllowInsecureTokenURL: true,
	}, slog.Default())
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}

	req1, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := p.applyAuth(t.Context(), req1); err != nil {
		t.Fatalf("first call: %v", err)
	}

	req2, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := p.applyAuth(t.Context(), req2); err != nil {
		t.Fatalf("second call: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 token request, got %d", callCount)
	}

	if req1.Header.Get("Authorization") != req2.Header.Get("Authorization") {
		t.Error("cached token should be the same for both requests")
	}
}

func TestOAuth2TokenInvalidation(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-v" + strings.Repeat("x", callCount),
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:                  "oauth2_client_credentials",
		TokenURL:              srv.URL,
		ClientID:              "c",
		ClientSecret:          "s",
		AllowInsecureTokenURL: true,
	}, slog.Default())
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}

	req1, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_ = p.applyAuth(t.Context(), req1)

	p.invalidateToken()

	req2, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_ = p.applyAuth(t.Context(), req2)

	if callCount != 2 {
		t.Errorf("expected 2 token requests after invalidation, got %d", callCount)
	}
}

func TestOAuth2TokenEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()

	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:                  "oauth2_client_credentials",
		TokenURL:              srv.URL,
		ClientID:              "bad",
		ClientSecret:          "secret",
		AllowInsecureTokenURL: true,
	}, slog.Default())
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	err = p.applyAuth(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for failed token endpoint")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %v, want status 400 mentioned", err)
	}
}

func TestOAuth2EmptyAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:                  "oauth2_client_credentials",
		TokenURL:              srv.URL,
		ClientID:              "c",
		ClientSecret:          "s",
		MaxRetries:            1,
		AllowInsecureTokenURL: true,
	}, slog.Default())
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	err = p.applyAuth(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for empty access_token")
	}
	if !strings.Contains(err.Error(), "empty access_token") {
		t.Errorf("error = %v, want 'empty access_token'", err)
	}
}

func TestOAuth2RejectsHTTPTokenURL(t *testing.T) {
	_, err := newAuthProvider(&proxy.AuthConfig{
		Type:         "oauth2_client_credentials",
		TokenURL:     "http://example.com/token",
		ClientID:     "c",
		ClientSecret: "s",
	}, slog.Default())
	if err == nil {
		t.Fatal("expected error for non-HTTPS token URL")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("error = %v, want 'HTTPS' mentioned", err)
	}
}

func TestApplyHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	headers := []proxy.HeaderConfig{
		{Name: "X-Custom-1", Value: "val1"},
		{Name: "X-Custom-2", Value: "val2"},
	}
	applyHeaders(req, headers)

	if got := req.Header.Get("X-Custom-1"); got != "val1" {
		t.Errorf("X-Custom-1 = %q, want %q", got, "val1")
	}
	if got := req.Header.Get("X-Custom-2"); got != "val2" {
		t.Errorf("X-Custom-2 = %q, want %q", got, "val2")
	}
}

func TestApplyHeaders_Empty(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	applyHeaders(req, nil)
}

func TestInvalidateToken_NilProvider(t *testing.T) {
	var p *authProvider
	p.invalidateToken()
}

func TestNewAuthProvider_InsecureSkipVerify_EmitsWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "expires_in": 3600})
	}))
	defer srv.Close()

	p, err := newAuthProvider(&proxy.AuthConfig{
		Type:               "oauth2_client_credentials",
		TokenURL:           srv.URL,
		ClientID:           "c",
		ClientSecret:       "s",
		CACertPath:         "/nonexistent/ca.pem",
		InsecureSkipVerify: true,
	}, logger)
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}
	if p == nil || p.tm == nil {
		t.Fatal("expected a non-nil provider with a token manager")
	}

	out := buf.String()
	if !strings.Contains(out, "TLS certificate verification disabled") {
		t.Errorf("expected TLS-disabled warning in log output; got: %q", out)
	}
	if !strings.Contains(out, "ca_cert_path is ignored") {
		t.Errorf("expected ca_cert_path-ignored warning in log output; got: %q", out)
	}
}

func TestNewAuthProvider_InsecureSkipVerify_NoCACert(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "expires_in": 3600})
	}))
	defer srv.Close()

	_, err := newAuthProvider(&proxy.AuthConfig{
		Type:               "oauth2_client_credentials",
		TokenURL:           srv.URL,
		ClientID:           "c",
		ClientSecret:       "s",
		InsecureSkipVerify: true,
	}, logger)
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "TLS certificate verification disabled") {
		t.Errorf("expected TLS-disabled warning; got: %q", out)
	}
	if strings.Contains(out, "ca_cert_path is ignored") {
		t.Errorf("did not expect ca_cert_path-ignored warning when CACertPath is empty; got: %q", out)
	}
}
