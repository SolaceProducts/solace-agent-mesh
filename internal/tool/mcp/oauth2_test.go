// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestGenerateVerifier(t *testing.T) {
	v := GenerateVerifier()
	if len(v) != 43 {
		t.Errorf("verifier length = %d, want 43", len(v))
	}
	v2 := GenerateVerifier()
	if v2 == v {
		t.Error("two calls produced identical verifiers")
	}
}

func TestGenerateState(t *testing.T) {
	state := GenerateState()
	if len(state) != 43 {
		t.Errorf("state length = %d, want 43", len(state))
	}
	s2 := GenerateState()
	if s2 == state {
		t.Error("two calls produced identical states")
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	scheme := &OAuthScheme{
		AuthorizationURL: "https://auth.example.com/authorize",
		Scopes:           []string{"read", "write"},
	}

	verifier := "test-verifier-for-mcp-oauth2-url-building!"
	authURL, err := BuildAuthorizationURL(scheme, "client123", "state456", verifier, "http://localhost:8000/callback")
	if err != nil {
		t.Fatalf("BuildAuthorizationURL() error: %v", err)
	}

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}

	q := u.Query()
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             "client123",
		"redirect_uri":          "http://localhost:8000/callback",
		"state":                 "state456",
		"code_challenge_method": "S256",
		"scope":                 "read write",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query param %q = %q, want %q", k, got, want)
		}
	}
	expectedChallenge := oauth2.S256ChallengeFromVerifier(verifier)
	if q.Get("code_challenge") != expectedChallenge {
		t.Errorf("code_challenge = %q, want %q", q.Get("code_challenge"), expectedChallenge)
	}
}

func TestBuildAuthorizationURL_MissingScheme(t *testing.T) {
	_, err := BuildAuthorizationURL(nil, "c", "s", "v", "r")
	if err == nil {
		t.Error("expected error for nil scheme")
	}

	_, err = BuildAuthorizationURL(&OAuthScheme{}, "c", "s", "v", "r")
	if err == nil {
		t.Error("expected error for empty authorization_url")
	}
}

func TestOAuthToken_IsExpired(t *testing.T) {
	tests := []struct {
		name    string
		token   *OAuthToken
		expired bool
	}{
		{"nil token", nil, true},
		{"empty access token", &OAuthToken{}, true},
		{"no expiry (valid)", &OAuthToken{AccessToken: "tok"}, false},
		{"future expiry", &OAuthToken{AccessToken: "tok", ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}, false},
		{"past expiry", &OAuthToken{AccessToken: "tok", ExpiresAt: time.Now().Add(-1 * time.Minute).Unix()}, true},
		{"within buffer", &OAuthToken{AccessToken: "tok", ExpiresAt: time.Now().Add(30 * time.Second).Unix()}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.token.IsExpired(); got != tt.expired {
				t.Errorf("IsExpired() = %v, want %v", got, tt.expired)
			}
		})
	}
}

func TestExchangeCode_ClientSecretPost(t *testing.T) {
	var receivedBody url.Values
	var receivedAuthHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		receivedBody = r.Form
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         "read write",
		})
	}))
	defer srv.Close()

	scheme := &OAuthScheme{
		TokenURL:                srv.URL + "/token",
		TokenEndpointAuthMethod: "client_secret_post",
	}
	cred := &MCPAuthCred{ClientID: "myid", ClientSecret: "mysecret"}

	token, err := ExchangeCode(context.Background(), scheme, cred, "authcode", "verifier123", "http://localhost/callback")
	if err != nil {
		t.Fatalf("ExchangeCode() error: %v", err)
	}

	if token.AccessToken != "new-access-token" {
		t.Errorf("AccessToken = %q", token.AccessToken)
	}
	if token.RefreshToken != "new-refresh-token" {
		t.Errorf("RefreshToken = %q", token.RefreshToken)
	}
	if token.ExpiresAt == 0 {
		t.Error("ExpiresAt should be set")
	}

	if receivedAuthHeader != "" {
		t.Errorf("client_secret_post should not send Authorization header, got %q", receivedAuthHeader)
	}
	if receivedBody.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", receivedBody.Get("grant_type"))
	}
	if receivedBody.Get("code") != "authcode" {
		t.Errorf("code = %q", receivedBody.Get("code"))
	}
	if receivedBody.Get("code_verifier") != "verifier123" {
		t.Errorf("code_verifier = %q", receivedBody.Get("code_verifier"))
	}
	if receivedBody.Get("client_id") != "myid" {
		t.Errorf("client_id = %q", receivedBody.Get("client_id"))
	}
	if receivedBody.Get("client_secret") != "mysecret" {
		t.Errorf("client_secret = %q", receivedBody.Get("client_secret"))
	}
}

func TestExchangeCode_ClientSecretBasic(t *testing.T) {
	var receivedAuthHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	scheme := &OAuthScheme{
		TokenURL:                srv.URL + "/token",
		TokenEndpointAuthMethod: "client_secret_basic",
	}
	cred := &MCPAuthCred{ClientID: "myid", ClientSecret: "mysecret"}

	_, err := ExchangeCode(context.Background(), scheme, cred, "code", "verifier", "http://localhost/callback")
	if err != nil {
		t.Fatalf("ExchangeCode() error: %v", err)
	}

	expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("myid:mysecret"))
	if receivedAuthHeader != expectedAuth {
		t.Errorf("Authorization header = %q, want %q", receivedAuthHeader, expectedAuth)
	}
}

func TestExchangeCode_PublicClient(t *testing.T) {
	var receivedBody url.Values
	var receivedAuthHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		_ = r.ParseForm()
		receivedBody = r.Form
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	scheme := &OAuthScheme{
		TokenURL:                srv.URL + "/token",
		TokenEndpointAuthMethod: "none",
	}
	cred := &MCPAuthCred{ClientID: "public-client"}

	_, err := ExchangeCode(context.Background(), scheme, cred, "code", "verifier", "http://localhost/callback")
	if err != nil {
		t.Fatalf("ExchangeCode() error: %v", err)
	}

	if receivedAuthHeader != "" {
		t.Errorf("public client should not send Authorization header, got %q", receivedAuthHeader)
	}
	if receivedBody.Get("client_id") != "public-client" {
		t.Errorf("client_id = %q", receivedBody.Get("client_id"))
	}
	if receivedBody.Get("client_secret") != "" {
		t.Errorf("client_secret should be empty, got %q", receivedBody.Get("client_secret"))
	}
}

func TestExchangeCode_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_grant",
			"error_description": "code expired",
		})
	}))
	defer srv.Close()

	scheme := &OAuthScheme{TokenURL: srv.URL + "/token"}
	_, err := ExchangeCode(context.Background(), scheme, nil, "code", "verifier", "http://localhost/callback")
	if err == nil {
		t.Fatal("expected error for OAuth error response")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error should mention error code, got: %s", err)
	}
}

func TestExchangeCode_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer srv.Close()

	scheme := &OAuthScheme{TokenURL: srv.URL + "/token"}
	_, err := ExchangeCode(context.Background(), scheme, nil, "code", "verifier", "http://localhost/callback")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestRefreshToken_Success(t *testing.T) {
	var receivedBody url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		receivedBody = r.Form
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-token",
			"refresh_token": "new-refresh",
			"token_type":    "Bearer",
			"expires_in":    7200,
		})
	}))
	defer srv.Close()

	scheme := &OAuthScheme{
		TokenURL: srv.URL + "/token",
		Scopes:   []string{"read"},
	}
	cred := &MCPAuthCred{ClientID: "c", ClientSecret: "s"}

	token, err := RefreshToken(context.Background(), scheme, cred, "old-refresh")
	if err != nil {
		t.Fatalf("RefreshToken() error: %v", err)
	}

	if token.AccessToken != "refreshed-token" {
		t.Errorf("AccessToken = %q", token.AccessToken)
	}
	if receivedBody.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q", receivedBody.Get("grant_type"))
	}
	if receivedBody.Get("refresh_token") != "old-refresh" {
		t.Errorf("refresh_token = %q", receivedBody.Get("refresh_token"))
	}
}

func TestResolveRedirectURI(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
	t.Setenv("FRONTEND_REDIRECT_URI", "")
	if got := ResolveRedirectURI(); got != "http://127.0.0.1:8800/api/v1/auth/tool/callback" {
		t.Errorf("default = %q", got)
	}

	t.Setenv("FRONTEND_REDIRECT_URI", "https://myapp.com")
	if got := ResolveRedirectURI(); got != "https://myapp.com/api/v1/auth/tool/callback" {
		t.Errorf("with FRONTEND = %q", got)
	}

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "https://custom.com/callback")
	if got := ResolveRedirectURI(); got != "https://custom.com/callback" {
		t.Errorf("with OAUTH = %q", got)
	}
}
