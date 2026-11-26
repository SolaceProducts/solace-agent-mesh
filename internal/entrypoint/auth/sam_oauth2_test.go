// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewSAMOAuth2Handler_Valid(t *testing.T) {
	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: "https://webui.example.com",
		CallbackURL:            "https://gateway.example.com/callback",
		Provider:               "azure",
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.externalAuthServiceURL != "https://webui.example.com" {
		t.Errorf("externalAuthServiceURL = %q, want no trailing slash", h.externalAuthServiceURL)
	}
	if h.callbackURL != "https://gateway.example.com/callback" {
		t.Errorf("callbackURL = %q", h.callbackURL)
	}
	if h.provider != "azure" {
		t.Errorf("provider = %q, want %q", h.provider, "azure")
	}
}

func TestNewSAMOAuth2Handler_TrailingSlash(t *testing.T) {
	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: "https://webui.example.com///",
		CallbackURL:            "https://gateway.example.com/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.externalAuthServiceURL != "https://webui.example.com" {
		t.Errorf("trailing slashes not stripped: %q", h.externalAuthServiceURL)
	}
}

func TestNewSAMOAuth2Handler_DefaultProvider(t *testing.T) {
	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: "https://webui.example.com",
		CallbackURL:            "https://gateway.example.com/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.provider != "unknown" {
		t.Errorf("default provider = %q, want %q", h.provider, "unknown")
	}
}

func TestNewSAMOAuth2Handler_MissingServiceURL(t *testing.T) {
	_, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		CallbackURL: "https://gateway.example.com/callback",
	})
	if err == nil {
		t.Fatal("expected error for missing external_auth_service_url")
	}
	if !strings.Contains(err.Error(), "external_auth_service_url") {
		t.Errorf("error should mention field: %v", err)
	}
}

func TestNewSAMOAuth2Handler_MissingCallbackURL(t *testing.T) {
	_, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: "https://webui.example.com",
	})
	if err == nil {
		t.Fatal("expected error for missing callback_url")
	}
	if !strings.Contains(err.Error(), "callback_url") {
		t.Errorf("error should mention field: %v", err)
	}
}

func TestAuthorizeURL_WithState(t *testing.T) {
	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: "https://webui.example.com",
		CallbackURL:            "https://gateway.example.com/oauth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := h.AuthorizeURL("csrf-token-123")

	if !strings.HasPrefix(got, "https://webui.example.com/api/v1/gateway-oauth/authorize?") {
		t.Errorf("URL prefix wrong: %s", got)
	}
	if !strings.Contains(got, "gateway_uri=") {
		t.Error("missing gateway_uri param")
	}
	if !strings.Contains(got, "state=csrf-token-123") {
		t.Error("missing state param")
	}
	if !strings.Contains(got, "gateway_uri=https%3A%2F%2Fgateway.example.com%2Foauth%2Fcallback") {
		t.Errorf("gateway_uri not properly encoded in: %s", got)
	}
}

func TestAuthorizeURL_WithoutState(t *testing.T) {
	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: "https://webui.example.com",
		CallbackURL:            "https://gateway.example.com/callback",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := h.AuthorizeURL("")

	if strings.Contains(got, "state=") {
		t.Error("state param should be omitted when empty")
	}
	if !strings.Contains(got, "gateway_uri=") {
		t.Error("missing gateway_uri param")
	}
}

func TestExchangeCode_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/gateway-oauth/exchange" {
			t.Errorf("path = %s, want /api/v1/gateway-oauth/exchange", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["code"] != "gw-code-xyz" {
			t.Errorf("code = %q, want %q", body["code"], "gw-code-xyz")
		}
		if body["gateway_uri"] != "https://gateway.example.com/callback" {
			t.Errorf("gateway_uri = %q", body["gateway_uri"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "at-123",
			"refresh_token": "rt-456",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: server.URL,
		CallbackURL:            "https://gateway.example.com/callback",
		HTTPClient:             server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	tokens, err := h.ExchangeCode(t.Context(), "gw-code-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "at-123" {
		t.Errorf("AccessToken = %q", tokens.AccessToken)
	}
	if tokens.RefreshToken != "rt-456" {
		t.Errorf("RefreshToken = %q", tokens.RefreshToken)
	}
	if tokens.TokenType != "Bearer" {
		t.Errorf("TokenType = %q", tokens.TokenType)
	}
	if tokens.ExpiresIn != 3600 {
		t.Errorf("ExpiresIn = %d", tokens.ExpiresIn)
	}
}

func TestExchangeCode_EmptyCode(t *testing.T) {
	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: "https://webui.example.com",
		CallbackURL:            "https://gateway.example.com/callback",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.ExchangeCode(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty code")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention empty: %v", err)
	}
}

func TestExchangeCode_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_code"})
	}))
	defer server.Close()

	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: server.URL,
		CallbackURL:            "https://gateway.example.com/callback",
		HTTPClient:             server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.ExchangeCode(t.Context(), "bad-code")
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should contain status code: %v", err)
	}
}

func TestExchangeCode_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	serverURL := server.URL
	server.Close()

	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: serverURL,
		CallbackURL:            "https://gateway.example.com/callback",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.ExchangeCode(t.Context(), "some-code")
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestExchangeCode_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: server.URL,
		CallbackURL:            "https://gateway.example.com/callback",
		HTTPClient:             server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.ExchangeCode(t.Context(), "some-code")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error should mention decode: %v", err)
	}
}

func TestExchangeCode_EmptyAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token_type": "Bearer",
			"expires_in": 3600,
		})
	}))
	defer server.Close()

	h, err := NewSAMOAuth2Handler(SAMOAuth2Config{
		ExternalAuthServiceURL: server.URL,
		CallbackURL:            "https://gateway.example.com/callback",
		HTTPClient:             server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.ExchangeCode(t.Context(), "some-code")
	if err == nil {
		t.Fatal("expected error for missing access_token")
	}
	if !strings.Contains(err.Error(), "access_token") {
		t.Errorf("error should mention access_token: %v", err)
	}
}
