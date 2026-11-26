// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type mockCredentialService struct {
	store    map[string]string
	getCount atomic.Int64
	setCount atomic.Int64
}

func newMockCredentialService() *mockCredentialService {
	return &mockCredentialService{store: make(map[string]string)}
}

func (m *mockCredentialService) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	m.getCount.Add(1)
	k := agentName + "|" + appName + "|" + userID + "|" + key
	return m.store[k], nil
}

func (m *mockCredentialService) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	m.setCount.Add(1)
	k := agentName + "|" + appName + "|" + userID + "|" + key
	m.store[k] = value
	return nil
}

func (m *mockCredentialService) Delete(_ context.Context, agentName, appName, userID, key string) error {
	k := agentName + "|" + appName + "|" + userID + "|" + key
	delete(m.store, k)
	return nil
}

func (m *mockCredentialService) resetCounters() {
	m.getCount.Store(0)
	m.setCount.Store(0)
}

func (m *mockCredentialService) List(_ context.Context, agentName, appName, userID string) ([]string, error) {
	prefix := agentName + "|" + appName + "|" + userID + "|"
	var keys []string
	for k := range m.store {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, strings.TrimPrefix(k, prefix))
		}
	}
	return keys, nil
}

func testToolCtx(credSvc *mockCredentialService) *tool.Context {
	return &tool.Context{
		AgentName:         "TestAgent",
		UserID:            "user1",
		ClientID:          "client1",
		ParentTaskID:      "task-123",
		ToolCallID:        "call-456",
		CredentialService: credSvc,
	}
}

func storeToken(t *testing.T, credSvc *mockCredentialService, token *OAuthToken) {
	t.Helper()
	data, _ := json.Marshal(token)
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user1", "test_key", string(data))
}

func TestGetAuthHeaders_Basic_WithCredential(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "basic",
		CredentialKey: "test_key",
		Credential:    &AuthCredential{Username: "admin", Password: "secret"},
	}, slog.Default())

	headers, err := h.GetAuthHeaders(context.Background(), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	auth := headers.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("expected Basic auth, got %q", auth)
	}
}

func TestGetAuthHeaders_Basic_NoCredential(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "basic",
		CredentialKey: "test_key",
	}, slog.Default())

	_, err := h.GetAuthHeaders(context.Background(), nil)
	if err == nil {
		t.Error("expected error for basic auth without credentials")
	}
}

func TestGetAuthHeaders_Bearer_WithToken(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "bearer",
		CredentialKey: "test_key",
		Credential:    &AuthCredential{Token: "my-token"},
	}, slog.Default())

	headers, err := h.GetAuthHeaders(context.Background(), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got := headers.Get("Authorization"); got != "Bearer my-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer my-token")
	}
}

func TestGetAuthHeaders_Bearer_NoToken(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "bearer",
		CredentialKey: "test_key",
	}, slog.Default())

	_, err := h.GetAuthHeaders(context.Background(), nil)
	if err == nil {
		t.Error("expected error for bearer auth without token")
	}
}

func TestGetAuthHeaders_OAuth2_ValidToken(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken: "valid-at",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	headers, err := h.GetAuthHeaders(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got := headers.Get("Authorization"); got != "Bearer valid-at" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestGetAuthHeaders_OAuth2_ExpiredWithRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "refreshed-at",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme: &OAuthScheme{
			TokenURL: srv.URL + "/token",
		},
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "old-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	headers, err := h.GetAuthHeaders(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got := headers.Get("Authorization"); got != "Bearer refreshed-at" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestGetAuthHeaders_OAuth2_MultiAgentIsolation(t *testing.T) {
	credSvc := newMockCredentialService()
	const sharedUserID = "shared-user"

	handlerA := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "cred_a"}, slog.Default())
	handlerB := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "cred_b"}, slog.Default())

	futureExpiry := time.Now().Add(1 * time.Hour).Unix()
	tokenA, _ := json.Marshal(&OAuthToken{AccessToken: "token-for-A", TokenType: "Bearer", ExpiresAt: futureExpiry})
	tokenB, _ := json.Marshal(&OAuthToken{AccessToken: "token-for-B", TokenType: "Bearer", ExpiresAt: futureExpiry})

	if err := credSvc.Set(context.Background(), "AgentA", "AgentA", sharedUserID, "cred_a", string(tokenA)); err != nil {
		t.Fatalf("store token A: %v", err)
	}
	if err := credSvc.Set(context.Background(), "AgentB", "AgentB", sharedUserID, "cred_b", string(tokenB)); err != nil {
		t.Fatalf("store token B: %v", err)
	}

	ctxA := &tool.Context{AgentName: "AgentA", UserID: sharedUserID, CredentialService: credSvc}
	ctxB := &tool.Context{AgentName: "AgentB", UserID: sharedUserID, CredentialService: credSvc}

	headersA, err := handlerA.GetAuthHeaders(context.Background(), ctxA)
	if err != nil {
		t.Fatalf("GetAuthHeaders(A): %v", err)
	}
	headersB, err := handlerB.GetAuthHeaders(context.Background(), ctxB)
	if err != nil {
		t.Fatalf("GetAuthHeaders(B): %v", err)
	}

	if got, want := headersA.Get("Authorization"), "Bearer token-for-A"; got != want {
		t.Errorf("agent A Authorization = %q, want %q", got, want)
	}
	if got, want := headersB.Get("Authorization"), "Bearer token-for-B"; got != want {
		t.Errorf("agent B Authorization = %q, want %q", got, want)
	}

	_, err = handlerA.GetAuthHeaders(context.Background(), ctxB)
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("handlerA on AgentB context: expected ErrAuthRequired, got %v", err)
	}
	_, err = handlerB.GetAuthHeaders(context.Background(), ctxA)
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("handlerB on AgentA context: expected ErrAuthRequired, got %v", err)
	}
}

func TestGetAuthHeaders_OAuth2_ExpiredNoRefresh(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken: "expired-at",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetAuthHeaders(context.Background(), toolCtx)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired, got %v", err)
	}
}

func TestGetAuthHeaders_UnknownType(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "custom",
		CredentialKey: "test_key",
	}, slog.Default())

	headers, err := h.GetAuthHeaders(context.Background(), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if headers != nil {
		t.Errorf("expected nil headers for unknown type, got %v", headers)
	}
}

func TestGetOAuthToken_NonOAuth2Type(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "basic",
		CredentialKey: "test_key",
	}, slog.Default())

	tok, err := h.GetOAuthToken(context.Background(), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tok != nil {
		t.Errorf("expected nil token for non-oauth2 type")
	}
}

func TestGetOAuthToken_NilToolCtx(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	_, err := h.GetOAuthToken(context.Background(), nil)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired for nil toolCtx, got %v", err)
	}
}

func TestGetOAuthToken_NilCredService(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	toolCtx := &tool.Context{AgentName: "TestAgent", UserID: "user1"}

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired for nil credService, got %v", err)
	}
}

func TestGetOAuthToken_ValidStored(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken: "stored-at",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	tok, err := h.GetOAuthToken(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tok.AccessToken != "stored-at" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
}

func TestGetOAuthToken_NoStoredToken(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired, got %v", err)
	}
}

func TestGetOAuthToken_CorruptedJSON(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user1", "test_key", "not-json")
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired for corrupted token, got %v", err)
	}
}

func TestGetOAuthToken_ExpiredWithRefreshSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-at",
			"refresh_token": "new-rt",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "old-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	tok, err := h.GetOAuthToken(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tok.AccessToken != "refreshed-at" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	var storedTok OAuthToken
	_ = json.Unmarshal([]byte(stored), &storedTok)
	if storedTok.AccessToken != "refreshed-at" {
		t.Errorf("stored token = %q", storedTok.AccessToken)
	}
}

func TestGetOAuthToken_ExpiredRefreshFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "old-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired after refresh failure, got %v", err)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored != "" {
		t.Errorf("expected credential cleared after refresh failure, got %q", stored)
	}
}

func TestGetOAuthToken_ExpiredRefreshFails_EvalPreservesCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "old-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)
	toolCtx.Trigger = a2a.TriggerEval

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired after refresh failure, got %v", err)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Error("eval-triggered refresh rejection must preserve the stored credential, but it was cleared")
	}
}

func TestBuildAuthSignal(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "my_cred_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://auth.example.com/authorize",
			TokenURL:         "https://auth.example.com/token",
			Scopes:           []string{"read"},
		},
		Credential: &AuthCredential{
			ClientID:     "test-client-id",
			ClientSecret: "test-secret",
		},
	}, slog.Default())

	toolCtx := testToolCtx(newMockCredentialService())

	signal, err := h.BuildAuthSignal(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if signal.Type != string(a2a.SignalAuthenticationRequired) {
		t.Errorf("Type = %q", signal.Type)
	}
	if signal.TargetAgent != "TestAgent" {
		t.Errorf("TargetAgent = %q", signal.TargetAgent)
	}
	if signal.TargetAgentTaskID != "task-123" {
		t.Errorf("TargetAgentTaskID = %q", signal.TargetAgentTaskID)
	}
	if signal.UserID != "user1" {
		t.Errorf("UserID = %q", signal.UserID)
	}
	if signal.ClientID != "client1" {
		t.Errorf("ClientID = %q", signal.ClientID)
	}
	if signal.CredentialKey != "my_cred_key" {
		t.Errorf("CredentialKey = %q", signal.CredentialKey)
	}
	if signal.AuthRequestFunctionCallID != "call-456" {
		t.Errorf("AuthRequestFunctionCallID = %q", signal.AuthRequestFunctionCallID)
	}
	if signal.CodeVerifier == "" {
		t.Error("CodeVerifier should not be empty")
	}
	if signal.GatewayTaskID != nil {
		t.Errorf("GatewayTaskID should be nil, got %v", signal.GatewayTaskID)
	}

	if !strings.Contains(signal.AuthURI, "auth.example.com/authorize") {
		t.Errorf("AuthURI missing auth server: %s", signal.AuthURI)
	}
	if !strings.Contains(signal.AuthURI, "client_id=test-client-id") {
		t.Errorf("AuthURI missing client_id: %s", signal.AuthURI)
	}
	if !strings.Contains(signal.AuthURI, "scope=read") {
		t.Errorf("AuthURI missing scope: %s", signal.AuthURI)
	}
}

func TestBuildAuthSignal_ToolDisplayName(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	scheme := func() *OAuthScheme {
		return &OAuthScheme{
			AuthorizationURL: "https://auth.example.com/authorize",
			TokenURL:         "https://auth.example.com/token",
			Scopes:           []string{"read"},
		}
	}
	cred := func() *AuthCredential {
		return &AuthCredential{ClientID: "test-client-id", ClientSecret: "test-secret"}
	}

	tests := []struct {
		name        string
		displayName string
		resourceURL string
		want        string
	}{
		{name: "explicit display name wins", displayName: "Atlassian", resourceURL: "https://mcp.atlassian.com/v1/sse", want: "Atlassian"},
		{name: "falls back to resource url host", displayName: "", resourceURL: "https://mcp.atlassian.com/v1/sse", want: "mcp.atlassian.com"},
		{name: "empty when neither set", displayName: "", resourceURL: "", want: ""},
		{name: "schemeless resource url yields no host", displayName: "", resourceURL: "mcp.atlassian.com/v1", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAuthHandler(&AuthConfig{
				Type:          "oauth2",
				CredentialKey: "my_cred_key",
				DisplayName:   tc.displayName,
				ResourceURL:   tc.resourceURL,
				Scheme:        scheme(),
				Credential:    cred(),
			}, slog.Default())

			signal, err := h.BuildAuthSignal(context.Background(), testToolCtx(newMockCredentialService()))
			if err != nil {
				t.Fatalf("BuildAuthSignal error: %v", err)
			}
			if signal.ToolDisplayName != tc.want {
				t.Errorf("ToolDisplayName = %q, want %q", signal.ToolDisplayName, tc.want)
			}
		})
	}
}

func TestBuildAuthSignal_MissingScheme(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	toolCtx := testToolCtx(newMockCredentialService())
	_, err := h.BuildAuthSignal(context.Background(), toolCtx)
	if err == nil {
		t.Error("expected error when no scheme available")
	}
}

func clearDiscoveryCacheForTest(t *testing.T) {
	t.Helper()
	DiscoveryCacheMu.Lock()
	DiscoveryCache = make(map[string]*OAuthMetadata)
	DiscoveryCacheMu.Unlock()
}

func newDiscoveryMockServer(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	var metaHits int64
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "mock",
				"authorization_servers": []string{srv.URL},
			})
		case "/.well-known/oauth-authorization-server":
			atomic.AddInt64(&metaHits, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
				"registration_endpoint":  srv.URL + "/register",
				"scopes_supported":       []string{"default"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, &metaHits
}

func TestEnsureScheme_StaticSchemeShortCircuits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request during ensureScheme: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type: "oauth2",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://static.example.com/authorize",
			TokenURL:         "https://static.example.com/token",
		},
		ResourceURL: srv.URL,
	}, slog.Default())

	got, err := h.ensureScheme(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got == nil || got.AuthorizationURL != "https://static.example.com/authorize" {
		t.Errorf("unexpected scheme: %+v", got)
	}
}

func TestEnsureScheme_MCPEnvVars(t *testing.T) {
	t.Setenv("MCP_AUTHORIZATION_URL", "https://static-env.example.com/authorize")
	t.Setenv("MCP_TOKEN_URL", "https://static-env.example.com/token")
	t.Setenv("MCP_REFRESH_URL", "https://static-env.example.com/refresh")
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request (env vars should short-circuit): %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: srv.URL,
	}, slog.Default())

	got, err := h.ensureScheme(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got == nil {
		t.Fatal("scheme should be populated from env vars")
	}
	if got.AuthorizationURL != "https://static-env.example.com/authorize" {
		t.Errorf("AuthorizationURL = %q, want https://static-env.example.com/authorize", got.AuthorizationURL)
	}
	if got.TokenURL != "https://static-env.example.com/token" {
		t.Errorf("TokenURL = %q, want https://static-env.example.com/token", got.TokenURL)
	}
	if got.RefreshURL != "https://static-env.example.com/refresh" {
		t.Errorf("RefreshURL = %q, want https://static-env.example.com/refresh", got.RefreshURL)
	}
}

func TestEnsureScheme_PartialEnvVars_DoesNotShortCircuit(t *testing.T) {
	t.Setenv("MCP_AUTHORIZATION_URL", "https://static-env.example.com/authorize")
	srv, _ := newDiscoveryMockServer(t)
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: srv.URL,
	}, slog.Default())

	got, err := h.ensureScheme(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got == nil || got.AuthorizationURL == "https://static-env.example.com/authorize" {
		t.Errorf("expected discovery to run (partial env vars should not short-circuit); got %+v", got)
	}
}

func TestEnsureScheme_DiscoveryFromResourceURL(t *testing.T) {
	srv, _ := newDiscoveryMockServer(t)
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: srv.URL,
	}, slog.Default())

	scheme, err := h.ensureScheme(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if scheme == nil {
		t.Fatal("expected discovered scheme, got nil")
	}
	if scheme.AuthorizationURL != srv.URL+"/authorize" {
		t.Errorf("AuthorizationURL = %q, want %q", scheme.AuthorizationURL, srv.URL+"/authorize")
	}
	if scheme.TokenURL != srv.URL+"/token" {
		t.Errorf("TokenURL = %q, want %q", scheme.TokenURL, srv.URL+"/token")
	}
	if h.discoveredMeta == nil {
		t.Error("h.discoveredMeta should be populated after discovery")
	}
	if h.discoveredMeta.RegistrationEndpoint != srv.URL+"/register" {
		t.Errorf("discoveredMeta.RegistrationEndpoint = %q", h.discoveredMeta.RegistrationEndpoint)
	}
}

func TestEnsureScheme_NoResourceURL_NoScheme_Errors(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type: "oauth2",
	}, slog.Default())

	_, err := h.ensureScheme(context.Background())
	if err == nil {
		t.Fatal("expected error when scheme and ResourceURL are both absent")
	}
	if !strings.Contains(err.Error(), "ResourceURL") {
		t.Errorf("error message should mention ResourceURL, got: %v", err)
	}
}

func TestEnsureScheme_DiscoveryFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: srv.URL,
	}, slog.Default())

	_, err := h.ensureScheme(context.Background())
	if err == nil {
		t.Fatal("expected error when discovery fails")
	}
	if !strings.Contains(err.Error(), srv.URL) {
		t.Errorf("error should reference resource URL %q, got: %v", srv.URL, err)
	}
}

func TestEnsureScheme_ConcurrentCallers(t *testing.T) {
	srv, metaHits := newDiscoveryMockServer(t)
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: srv.URL,
	}, slog.Default())

	const N = 10
	type result struct {
		scheme *OAuthScheme
		err    error
	}
	results := make(chan result, N)
	start := make(chan struct{})
	for range N {
		go func() {
			<-start
			s, err := h.ensureScheme(context.Background())
			results <- result{s, err}
		}()
	}
	close(start)

	for range N {
		r := <-results
		if r.err != nil {
			t.Errorf("concurrent call error: %v", r.err)
		}
		if r.scheme == nil {
			t.Error("concurrent call returned nil scheme")
		}
	}

	if got := atomic.LoadInt64(metaHits); got != 1 {
		t.Errorf("expected exactly 1 metadata fetch across %d concurrent callers, got %d", N, got)
	}
}

func TestEnsureScheme_MergeScopesAndAuthMethod(t *testing.T) {
	srv, _ := newDiscoveryMockServer(t)
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type: "oauth2",
		Scheme: &OAuthScheme{
			Scopes:                  []string{"read:custom", "write:custom"},
			TokenEndpointAuthMethod: "client_secret_basic",
		},
		ResourceURL: srv.URL,
	}, slog.Default())

	scheme, err := h.ensureScheme(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if scheme.AuthorizationURL != srv.URL+"/authorize" {
		t.Errorf("AuthorizationURL should come from discovery, got %q", scheme.AuthorizationURL)
	}
	if len(scheme.Scopes) != 2 || scheme.Scopes[0] != "read:custom" {
		t.Errorf("Scopes should survive discovery, got %v", scheme.Scopes)
	}
	if scheme.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Errorf("TokenEndpointAuthMethod should survive discovery, got %q", scheme.TokenEndpointAuthMethod)
	}
}

func TestEnsureScheme_DoesNotHoldLockDuringHTTP(t *testing.T) {
	clearDiscoveryCacheForTest(t)

	release := make(chan struct{})
	var closeOnce sync.Once
	signalRelease := func() { closeOnce.Do(func() { close(release) }) }

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			<-release
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(signalRelease)

	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: srv.URL,
	}, slog.Default())

	discoveryDone := make(chan struct{})
	go func() {
		_, _ = h.ensureScheme(t.Context())
		close(discoveryDone)
	}()

	time.Sleep(50 * time.Millisecond)

	resolved := make(chan struct{})
	go func() {
		_ = h.resolveScheme()
		close(resolved)
	}()

	select {
	case <-resolved:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("resolveScheme stalled while ensureScheme was mid-HTTP — h.mu held across network I/O")
	}

	signalRelease()
	<-discoveryDone
}

func TestEnsureScheme_ClearsRegisteredCredsOnAuthServerMigration(t *testing.T) {
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: "https://mcp.example.com/tool",
	}, slog.Default())
	h.discoveredMeta = &OAuthMetadata{
		Issuer:                "https://auth-a.example.com",
		AuthorizationEndpoint: "https://auth-a.example.com/authorize",
		TokenEndpoint:         "https://auth-a.example.com/token",
		RegistrationEndpoint:  "https://auth-a.example.com/register",
		DiscoveredAt:          time.Now().Add(-2 * discoveryTTL),
	}
	h.registeredCred = &AuthCredential{ClientID: "client-from-A"}
	h.registeredAuthMethod = "client_secret_basic"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 srv.URL + "/new-issuer",
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
				"registration_endpoint":  srv.URL + "/register-new",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	h.cfg.ResourceURL = srv.URL

	if _, err := h.ensureScheme(t.Context()); err != nil {
		t.Fatalf("ensureScheme: %v", err)
	}

	if h.registeredCred != nil {
		t.Errorf("registeredCred should be cleared when the auth server migrates (issuer/registration endpoint changed), still have %+v", h.registeredCred)
	}
	if h.registeredAuthMethod != "" {
		t.Errorf("registeredAuthMethod should be cleared, still have %q", h.registeredAuthMethod)
	}
}

func TestEnsureScheme_KeepsRegisteredCredsOnUnchangedRefresh(t *testing.T) {
	clearDiscoveryCacheForTest(t)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 "stable-issuer",
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
				"registration_endpoint":  srv.URL + "/register",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: srv.URL,
	}, slog.Default())
	h.discoveredMeta = &OAuthMetadata{
		Issuer:               "stable-issuer",
		RegistrationEndpoint: srv.URL + "/register",
		DiscoveredAt:         time.Now().Add(-2 * discoveryTTL),
	}
	h.registeredCred = &AuthCredential{ClientID: "client-from-stable"}
	h.registeredAuthMethod = "client_secret_post"

	if _, err := h.ensureScheme(t.Context()); err != nil {
		t.Fatalf("ensureScheme: %v", err)
	}

	if h.registeredCred == nil || h.registeredCred.ClientID != "client-from-stable" {
		t.Errorf("registeredCred should survive unchanged refresh, got %+v", h.registeredCred)
	}
	if h.registeredAuthMethod != "client_secret_post" {
		t.Errorf("registeredAuthMethod should survive unchanged refresh, got %q", h.registeredAuthMethod)
	}
}

type capturingLogHandler struct {
	records []slog.Record
}

func (h *capturingLogHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= slog.LevelInfo
}

func (h *capturingLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *capturingLogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *capturingLogHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *capturingLogHandler) firstWithMessage(msg string) (slog.Record, bool) {
	for _, r := range h.records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

func recordHasAttr(r slog.Record, key string, want any) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			if want == nil || a.Value.Any() == want {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func TestEnsureCred_RegistrationServerSuccess_Logs(t *testing.T) {
	handler := &capturingLogHandler{}
	logger := slog.New(handler)

	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "mock-client-id",
			"client_secret":              "mock-secret",
			"token_endpoint_auth_method": "client_secret_post",
		})
	}))
	defer regSrv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type: "oauth2",
	}, logger)
	h.discoveredMeta = &OAuthMetadata{
		RegistrationEndpoint: regSrv.URL,
		DiscoveredAt:         time.Now(),
	}

	_, err := h.ensureCred(context.Background(), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	rec, ok := handler.firstWithMessage("oauth2 dynamic client registration")
	if !ok {
		t.Fatal("expected 'oauth2 dynamic client registration' INFO record")
	}
	if !recordHasAttr(rec, "registration_endpoint", regSrv.URL) {
		t.Errorf("record missing registration_endpoint=%q", regSrv.URL)
	}
	if !recordHasAttr(rec, "clientID", "mock-client-id") {
		t.Errorf("record missing clientID=mock-client-id")
	}
	if !recordHasAttr(rec, "token_endpoint_auth_method", "client_secret_post") {
		t.Errorf("record missing token_endpoint_auth_method")
	}
}

func TestEnsureCred_EnvFallback_Logs(t *testing.T) {
	handler := &capturingLogHandler{}
	logger := slog.New(handler)

	t.Setenv("TOOL_OAUTH_CLIENT_ID", "env-client-id")
	t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "env-secret")

	h := NewAuthHandler(&AuthConfig{
		Type: "oauth2",
	}, logger)

	_, err := h.ensureCred(context.Background(), nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	_, ok := handler.firstWithMessage("oauth2 using env-var client credentials")
	if !ok {
		t.Fatal("expected 'oauth2 using env-var client credentials' INFO record")
	}
}

func TestEnsureCred_RegisteredAuthMethod_SurvivesResolveScheme(t *testing.T) {
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "public-client",
			"token_endpoint_auth_method": "none",
		})
	}))
	defer regSrv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type: "oauth2",
	}, slog.Default())
	h.discoveredMeta = &OAuthMetadata{
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         "https://auth.example.com/token",
		RegistrationEndpoint:  regSrv.URL,
		DiscoveredAt:          time.Now(),
	}

	_, err := h.ensureCred(context.Background(), nil)
	if err != nil {
		t.Fatalf("ensureCred: %v", err)
	}

	got := h.resolveScheme()
	if got == nil {
		t.Fatal("resolveScheme returned nil")
	}
	if got.TokenEndpointAuthMethod != "none" {
		t.Errorf("TokenEndpointAuthMethod = %q, want %q (registered method must survive the ensureCred → resolveScheme boundary)",
			got.TokenEndpointAuthMethod, "none")
	}
}

func TestEnsureCred_CfgSchemeAuthMethod_WinsOverRegistered(t *testing.T) {
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "some-client",
			"token_endpoint_auth_method": "none",
		})
	}))
	defer regSrv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type: "oauth2",
		Scheme: &OAuthScheme{
			TokenEndpointAuthMethod: "client_secret_basic",
		},
	}, slog.Default())
	h.discoveredMeta = &OAuthMetadata{
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         "https://auth.example.com/token",
		RegistrationEndpoint:  regSrv.URL,
		DiscoveredAt:          time.Now(),
	}

	_, err := h.ensureCred(context.Background(), nil)
	if err != nil {
		t.Fatalf("ensureCred: %v", err)
	}

	got := h.resolveScheme()
	if got.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Errorf("TokenEndpointAuthMethod = %q, want %q (cfg.Scheme must win over registered)",
			got.TokenEndpointAuthMethod, "client_secret_basic")
	}
}

func TestEnsureCred_FastPath_DoesNotCorruptDeployerScheme(t *testing.T) {
	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "public-client",
			"token_endpoint_auth_method": "none",
		})
	}))
	defer regSrv.Close()

	cfg := &AuthConfig{
		Type: "oauth2",
		Scheme: &OAuthScheme{
			AuthorizationURL:        "https://auth.example.com/authorize",
			TokenURL:                "https://auth.example.com/token",
			TokenEndpointAuthMethod: "client_secret_basic",
		},
	}
	h := NewAuthHandler(cfg, slog.Default())
	h.discoveredMeta = &OAuthMetadata{
		RegistrationEndpoint: regSrv.URL,
		DiscoveredAt:         time.Now(),
	}

	scheme, err := h.ensureScheme(t.Context())
	if err != nil {
		t.Fatalf("ensureScheme: %v", err)
	}
	if scheme != cfg.Scheme {
		t.Fatal("expected fast path to return cfg.Scheme pointer")
	}

	if _, err := h.ensureCred(t.Context(), nil); err != nil {
		t.Fatalf("ensureCred: %v", err)
	}

	if cfg.Scheme.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Errorf("cfg.Scheme.TokenEndpointAuthMethod corrupted by registration: got %q, want %q",
			cfg.Scheme.TokenEndpointAuthMethod, "client_secret_basic")
	}
	got := h.resolveScheme()
	if got.TokenEndpointAuthMethod != "client_secret_basic" {
		t.Errorf("resolveScheme().TokenEndpointAuthMethod: got %q, want %q (deployer pin must survive)",
			got.TokenEndpointAuthMethod, "client_secret_basic")
	}
}

func TestEnsureCred_PartialScheme_RegisteredAuthMethodSurvives(t *testing.T) {
	raw := map[string]any{
		"type": "oauth2",
		"scheme": map[string]any{
			"scopes": []any{"read:content"},
		},
	}
	parsed := ParseAuthConfig(raw, "my_tool")
	if parsed == nil || parsed.Scheme == nil {
		t.Fatalf("ParseAuthConfig returned nil or nil Scheme: %+v", parsed)
	}
	if parsed.Scheme.TokenEndpointAuthMethod != "" {
		t.Fatalf("parsed scheme should have empty TokenEndpointAuthMethod when deployer omits it, got %q", parsed.Scheme.TokenEndpointAuthMethod)
	}

	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "public-client",
			"token_endpoint_auth_method": "none",
		})
	}))
	defer regSrv.Close()

	h := NewAuthHandler(parsed, slog.Default())
	h.discoveredMeta = &OAuthMetadata{
		AuthorizationEndpoint: "https://auth.example.com/authorize",
		TokenEndpoint:         "https://auth.example.com/token",
		RegistrationEndpoint:  regSrv.URL,
		DiscoveredAt:          time.Now(),
	}

	_, err := h.ensureCred(context.Background(), nil)
	if err != nil {
		t.Fatalf("ensureCred: %v", err)
	}

	got := h.resolveScheme()
	if got.TokenEndpointAuthMethod != "none" {
		t.Errorf("TokenEndpointAuthMethod = %q, want %q (registered method must survive when deployer didn't pin a value)",
			got.TokenEndpointAuthMethod, "none")
	}
	if len(got.Scopes) != 1 || got.Scopes[0] != "read:content" {
		t.Errorf("Scopes = %v, want [read:content]", got.Scopes)
	}
}

func TestHandleAuthResponse_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" {
			http.Error(w, "wrong grant_type", 400)
			return
		}
		if r.Form.Get("code") != "auth-code" {
			http.Error(w, "wrong code", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-token",
			"refresh_token": "new-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme: &OAuthScheme{
			TokenURL: srv.URL + "/token",
		},
		Credential: &AuthCredential{ClientID: "cid", ClientSecret: "csecret"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{
		Code:         "auth-code",
		CodeVerifier: "test-verifier",
	}, toolCtx)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Fatal("token should be stored")
	}
	var tok OAuthToken
	_ = json.Unmarshal([]byte(stored), &tok)
	if tok.AccessToken != "new-token" {
		t.Errorf("stored AccessToken = %q", tok.AccessToken)
	}
}

func TestHandleAuthResponse_RecordsWhichSignInGrantedTheToken(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  string
	}{
		{"state of the sign-in", "CANARYstate0190f5f1", "7b981ede8114805c3ee5e473880c731f95c23e7e456c5dc636db3bd26a05522d"},
		{"no state to record", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer"})
			}))
			defer srv.Close()
			h := NewAuthHandler(&AuthConfig{
				Type:          "oauth2",
				CredentialKey: "test_key",
				Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
				Credential:    &AuthCredential{ClientID: "cid", ClientSecret: "csecret"},
			}, slog.Default())
			toolCtx := testToolCtx(newMockCredentialService())

			if err := h.HandleAuthResponse(t.Context(), &a2a.AuthResponseData{Code: "auth-code", CodeVerifier: "v", State: tt.state}, toolCtx); err != nil {
				t.Fatalf("HandleAuthResponse: %v", err)
			}

			got := h.CheckStoredCredential(t.Context(), toolCtx)
			if want := (tool.StoredCredential{Connected: true, GrantRef: tt.want}); got != want {
				t.Errorf("CheckStoredCredential() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestGetOAuthToken_RefreshKeepsTheSignInThatGrantedTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "refreshed-at", "refresh_token": "new-rt", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer srv.Close()
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
	}, slog.Default())
	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "old-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
		GrantRef:     "grant-of-the-first-sign-in",
	})
	toolCtx := testToolCtx(credSvc)

	if _, err := h.GetOAuthToken(t.Context(), toolCtx); err != nil {
		t.Fatalf("GetOAuthToken: %v", err)
	}

	got := h.CheckStoredCredential(t.Context(), toolCtx)
	if got.GrantRef != "grant-of-the-first-sign-in" || !got.Connected {
		t.Errorf("CheckStoredCredential() after a refresh = %+v, want connected with the first sign-in's grant", got)
	}
}

func TestHandleAuthResponse_NoScheme(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	toolCtx := testToolCtx(newMockCredentialService())

	err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{
		Code: "code",
	}, toolCtx)
	if err == nil {
		t.Error("expected error when no scheme available")
	}
}

func TestHandleAuthResponse_MissingUserID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		Scheme:        &OAuthScheme{AuthorizationURL: "https://x/auth", TokenURL: srv.URL},
		Credential:    &AuthCredential{ClientID: "c", ClientSecret: "s"},
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := &tool.Context{
		AgentName:         "TestAgent",
		UserID:            "",
		CredentialService: credSvc,
	}

	err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{
		Code: "code",
	}, toolCtx)
	if err == nil {
		t.Error("expected error when user_id is empty")
	}
	if !strings.Contains(err.Error(), "user_id") {
		t.Errorf("expected error to mention user_id, got %q", err.Error())
	}
}

func TestRefresh_PostRefreshExpired_ForcesReauth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":-3600}`))
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		Scheme:        &OAuthScheme{AuthorizationURL: "https://x/auth", TokenURL: srv.URL},
		Credential:    &AuthCredential{ClientID: "c", ClientSecret: "s"},
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	storeToken(t, credSvc, &OAuthToken{AccessToken: "stale", RefreshToken: "r", ExpiresAt: 1})

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("expected ErrAuthRequired when refresh returns already-expired token, got %v", err)
	}
}

func TestClearCredential_Success(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user1", "test_key", `{"access_token":"tok"}`)

	if err := h.ClearCredential(context.Background(), toolCtx); err != nil {
		t.Fatalf("error: %v", err)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored != "" {
		t.Errorf("credential should be cleared, got %q", stored)
	}
}

func TestClearCredential_NilToolCtx(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	if err := h.ClearCredential(context.Background(), nil); err != nil {
		t.Errorf("expected nil error for nil toolCtx, got %v", err)
	}
}

func TestClearCredential_NilCredService(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	toolCtx := &tool.Context{AgentName: "TestAgent", UserID: "user1"}

	if err := h.ClearCredential(context.Background(), toolCtx); err != nil {
		t.Errorf("expected nil error for nil credService, got %v", err)
	}
}

func TestClearCredentialForReauth_EvalPreservesCredential(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	toolCtx.Trigger = a2a.TriggerEval

	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user1", "test_key", `{"access_token":"tok"}`)

	if err := h.ClearCredentialForReauth(context.Background(), toolCtx, "tok"); err != nil {
		t.Fatalf("error: %v", err)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Error("eval-triggered re-auth must preserve the stored credential, but it was cleared")
	}
}

func TestClearCredentialForReauth_CannotPromptPreservesCredential(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	toolCtx.Trigger = a2a.TriggerScheduled
	toolCtx.AuthPromptUnavailable = true

	_ = credSvc.Set(t.Context(), "TestAgent", "TestAgent", "user1", "test_key", `{"access_token":"tok"}`)

	if err := h.ClearCredentialForReauth(t.Context(), toolCtx, "tok"); err != nil {
		t.Fatalf("error: %v", err)
	}

	stored, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Error("a caller that cannot prompt had its stored credential cleared")
	}
}

func TestClearCredentialForReauth_NonEvalClears(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user1", "test_key", `{"access_token":"tok"}`)

	if err := h.ClearCredentialForReauth(context.Background(), toolCtx, "tok"); err != nil {
		t.Fatalf("error: %v", err)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored != "" {
		t.Errorf("credential should be cleared, got %q", stored)
	}
}

func TestCredentialKey(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "my_key",
	}, slog.Default())
	if got := h.CredentialKey(); got != "my_key" {
		t.Errorf("CredentialKey() = %q, want %q", got, "my_key")
	}
}

func TestConfig(t *testing.T) {
	cfg := &AuthConfig{Type: "bearer", CredentialKey: "key"}
	h := NewAuthHandler(cfg, slog.Default())
	if h.Config() != cfg {
		t.Error("Config() should return the original config")
	}
}

func newPureDiscoveryServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "mock",
				"authorization_servers": []string{srv.URL},
			})
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
				"registration_endpoint":  srv.URL + "/register",
				"scopes_supported":       []string{"default"},
			})
		case "/register":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  "mock-client",
				"client_secret":              "mock-secret",
				"token_endpoint_auth_method": "client_secret_post",
			})
		case "/token":
			_ = r.ParseForm()
			w.Header().Set("Content-Type", "application/json")
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "exchanged-at",
					"refresh_token": "rt-from-exchange",
					"token_type":    "Bearer",
					"expires_in":    3600,
				})
			case "refresh_token":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "refreshed-at",
					"refresh_token": "rt-from-refresh",
					"token_type":    "Bearer",
					"expires_in":    3600,
				})
			default:
				http.Error(w, "unsupported grant_type", http.StatusBadRequest)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv
}

func TestHandleAuthResponse_ColdStartDiscovery(t *testing.T) {
	srv := newPureDiscoveryServer(t)
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		ResourceURL:   srv.URL,
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	err := h.HandleAuthResponse(t.Context(), &a2a.AuthResponseData{
		Code:         "auth-code",
		CodeVerifier: "test-verifier",
	}, toolCtx)
	if err != nil {
		t.Fatalf("HandleAuthResponse should succeed via cold-start discovery, got: %v", err)
	}

	stored, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Fatal("token should be stored after code exchange")
	}
	var tok OAuthToken
	if err := json.Unmarshal([]byte(stored), &tok); err != nil {
		t.Fatalf("unmarshal stored token: %v", err)
	}
	if tok.AccessToken != "exchanged-at" {
		t.Errorf("stored AccessToken = %q, want %q", tok.AccessToken, "exchanged-at")
	}
}

func TestBuildAuthSignal_NoRegistrationEndpoint_NoEnvVars_NoCredential(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "mock",
				"authorization_servers": []string{srv.URL},
			})
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
				"scopes_supported":       []string{"default"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	t.Setenv("TOOL_OAUTH_CLIENT_ID", "")
	t.Setenv("MCP_CLIENT_ID", "")

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		ResourceURL:   srv.URL,
	}, slog.Default())

	toolCtx := testToolCtx(newMockCredentialService())

	_, err := h.BuildAuthSignal(t.Context(), toolCtx)
	if err == nil {
		t.Fatal("expected error when no client credentials can be resolved, got nil")
	}
	if !strings.Contains(err.Error(), "client") {
		t.Errorf("error message should reference missing client credentials, got: %v", err)
	}
}

func TestGetOAuthToken_ColdStartRefresh_PreservesRefreshToken(t *testing.T) {
	srv := newPureDiscoveryServer(t)
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		ResourceURL:   srv.URL,
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "valid-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	token, err := h.GetOAuthToken(t.Context(), toolCtx)
	if err != nil {
		t.Fatalf("GetOAuthToken should refresh via cold-start discovery, got: %v", err)
	}
	if token == nil || token.AccessToken != "refreshed-at" {
		t.Fatalf("expected refreshed token, got %+v", token)
	}

	stored, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Fatal("refreshed token should remain persisted")
	}
}

func TestGetOAuthToken_TransientInfraFailure_PreservesRefreshToken(t *testing.T) {
	srv := newPureDiscoveryServer(t)
	srv.Close()
	clearDiscoveryCacheForTest(t)

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		ResourceURL:   srv.URL,
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "valid-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetOAuthToken(t.Context(), toolCtx)
	if !errors.Is(err, ErrTransientRefresh) || errors.Is(err, ErrAuthRequired) {
		t.Fatalf("GetOAuthToken: got %v, want ErrTransientRefresh without ErrAuthRequired", err)
	}

	stored, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Fatal("stored refresh_token must be preserved on transient infra failure — the user's next request should retry, not re-auth")
	}
	var tok OAuthToken
	if jsonErr := json.Unmarshal([]byte(stored), &tok); jsonErr != nil {
		t.Fatalf("unmarshal stored token: %v", jsonErr)
	}
	if tok.RefreshToken != "valid-rt" {
		t.Errorf("stored refresh_token = %q, want %q", tok.RefreshToken, "valid-rt")
	}
}

func TestHandleAuthResponse_ColdStart_UsesRegisteredAuthMethod(t *testing.T) {
	var tokenAuthHeader atomic.Value
	tokenAuthHeader.Store("")

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "mock",
				"authorization_servers": []string{srv.URL},
			})
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                srv.URL,
				"authorization_endpoint":                srv.URL + "/authorize",
				"token_endpoint":                        srv.URL + "/token",
				"registration_endpoint":                 srv.URL + "/register",
				"scopes_supported":                      []string{"default"},
				"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
			})
		case "/register":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  "basic-client",
				"client_secret":              "basic-secret",
				"token_endpoint_auth_method": "client_secret_basic",
			})
		case "/token":
			tokenAuthHeader.Store(r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	clearDiscoveryCacheForTest(t)

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		ResourceURL:   srv.URL,
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	if err := h.HandleAuthResponse(t.Context(), &a2a.AuthResponseData{
		Code:         "auth-code",
		CodeVerifier: "test-verifier",
	}, toolCtx); err != nil {
		t.Fatalf("HandleAuthResponse: %v", err)
	}

	got, _ := tokenAuthHeader.Load().(string)
	if !strings.HasPrefix(got, "Basic ") {
		t.Errorf("expected Basic Authorization header on /token (auth method from dynamic registration), got %q — scheme was captured before ensureCred populated registeredAuthMethod", got)
	}
}

func TestBuildAuthSignal_PersistsPending(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "csrf_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://auth.example.com/authorize",
			TokenURL:         "https://auth.example.com/token",
		},
		Credential: &AuthCredential{ClientID: "cid"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	signal, err := h.BuildAuthSignal(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("BuildAuthSignal: %v", err)
	}

	raw, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "csrf_key:task-123:call-456.pending")
	if raw == "" {
		t.Fatal("pending auth entry should be persisted under {credential_key}:{taskID}:{funcCallID}.pending")
	}
	var pending pendingAuth
	if err := json.Unmarshal([]byte(raw), &pending); err != nil {
		t.Fatalf("unmarshal pending: %v", err)
	}
	if pending.CodeVerifier == "" {
		t.Error("pending.CodeVerifier should not be empty")
	}
	if pending.State == "" {
		t.Error("pending.State should not be empty")
	}
	if pending.CodeVerifier != signal.CodeVerifier {
		t.Errorf("pending.CodeVerifier (%q) should match signal.CodeVerifier (%q)", pending.CodeVerifier, signal.CodeVerifier)
	}
	u, _ := url.Parse(signal.AuthURI)
	if got := u.Query().Get("state"); got != pending.State {
		t.Errorf("auth_uri state = %q, want %q (matching pending)", got, pending.State)
	}
}

func TestHandleAuthResponse_StateMismatch_ReturnsErrAndClearsPending(t *testing.T) {
	tokenCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tokenCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"x","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "csrf_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: srv.URL + "/authorize",
			TokenURL:         srv.URL + "/token",
		},
		Credential: &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	if err := h.storePending(context.Background(), toolCtx, promptFlow(toolCtx), pendingAuth{
		CodeVerifier: "correct-verifier",
		State:        "GOOD",
		CreatedAt:    time.Now(),
	}); err != nil {
		t.Fatalf("storePending: %v", err)
	}

	err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{
		FunctionCallID:    toolCtx.ToolCallID,
		TargetAgentTaskID: toolCtx.ParentTaskID,
		Code:              "auth-code",
		State:             "EVIL",
	}, toolCtx)
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("expected ErrStateMismatch, got %v", err)
	}
	if tokenCalled {
		t.Error("token endpoint must not be called when state is mismatched")
	}
	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "csrf_key")
	if stored != "" {
		t.Errorf("credential should be empty on state mismatch, got %q", stored)
	}
	pendingRaw, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "csrf_key:call-456.pending")
	if pendingRaw != "" {
		t.Errorf("pending entry should be deleted on state mismatch, got %q", pendingRaw)
	}
}

func TestHandleAuthResponse_StateMatch_UsesStoredVerifier_DeletesPending(t *testing.T) {
	var seenVerifier string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seenVerifier = r.Form.Get("code_verifier")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "csrf_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: srv.URL + "/authorize",
			TokenURL:         srv.URL + "/token",
		},
		Credential: &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	if err := h.storePending(context.Background(), toolCtx, promptFlow(toolCtx), pendingAuth{
		CodeVerifier: "stored-verifier",
		State:        "GOOD",
		CreatedAt:    time.Now(),
	}); err != nil {
		t.Fatalf("storePending: %v", err)
	}

	err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{
		FunctionCallID:    toolCtx.ToolCallID,
		TargetAgentTaskID: toolCtx.ParentTaskID,
		Code:              "auth-code",
		CodeVerifier:      "tampered-verifier",
		State:             "GOOD",
	}, toolCtx)
	if err != nil {
		t.Fatalf("HandleAuthResponse: %v", err)
	}
	if seenVerifier != "stored-verifier" {
		t.Errorf("token endpoint saw code_verifier %q, want %q (stored verifier must win)", seenVerifier, "stored-verifier")
	}
	pendingRaw, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "csrf_key:call-456.pending")
	if pendingRaw != "" {
		t.Errorf("pending entry should be deleted after successful exchange, got %q", pendingRaw)
	}
}

func TestHandleAuthResponse_NoPending_UsesResponseVerifier(t *testing.T) {
	var seenVerifier string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seenVerifier = r.Form.Get("code_verifier")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
		})
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "cold_key",
		Scheme: &OAuthScheme{
			TokenURL: srv.URL + "/token",
		},
		Credential: &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{
		Code:         "auth-code",
		CodeVerifier: "response-verifier",
		State:        "whatever",
	}, toolCtx)
	if err != nil {
		t.Fatalf("HandleAuthResponse: %v", err)
	}
	if seenVerifier != "response-verifier" {
		t.Errorf("seenVerifier = %q, want response-verifier (cold-start fallback)", seenVerifier)
	}
}

func TestHandleAuthResponse_EmptyStateInResponse_AllowsExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "ok_key",
		Scheme: &OAuthScheme{
			TokenURL: srv.URL + "/token",
		},
		Credential: &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	_ = h.storePending(context.Background(), toolCtx, promptFlow(toolCtx), pendingAuth{
		CodeVerifier: "v",
		State:        "S",
		CreatedAt:    time.Now(),
	})

	err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{
		FunctionCallID:    toolCtx.ToolCallID,
		TargetAgentTaskID: toolCtx.ParentTaskID,
		Code:              "auth-code",
		State:             "",
	}, toolCtx)
	if err != nil {
		t.Fatalf("empty state should be tolerated: %v", err)
	}
}

func TestRefresh_InvalidClient_ResetsRegistration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"client revoked"}`))
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: srv.URL + "/authorize",
			TokenURL:         srv.URL + "/token",
		},
		Credential: &AuthCredential{ClientID: "revoked-cid", ClientSecret: "revoked-secret"},
	}, slog.Default())

	h.mu.Lock()
	h.discoveredMeta = &OAuthMetadata{
		Issuer:                srv.URL,
		AuthorizationEndpoint: srv.URL + "/authorize",
		TokenEndpoint:         srv.URL + "/token",
		DiscoveredAt:          time.Now(),
	}
	h.registeredCred = &AuthCredential{ClientID: "revoked-cid", ClientSecret: "revoked-secret"}
	h.registeredAuthMethod = "client_secret_post"
	h.mu.Unlock()

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired",
		RefreshToken: "rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("GetOAuthToken: got %v, want ErrAuthRequired", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.discoveredMeta != nil {
		t.Error("discoveredMeta should be nil after invalid_client refresh error")
	}
	if h.registeredCred != nil {
		t.Error("registeredCred should be nil after invalid_client refresh error")
	}
	if h.registeredAuthMethod != "" {
		t.Errorf("registeredAuthMethod should be empty, got %q", h.registeredAuthMethod)
	}
}

func TestRefresh_InvalidGrant_PreservesRegistration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired"}`))
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: srv.URL + "/authorize",
			TokenURL:         srv.URL + "/token",
		},
		Credential: &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())

	h.mu.Lock()
	h.discoveredMeta = &OAuthMetadata{
		Issuer:                srv.URL,
		AuthorizationEndpoint: srv.URL + "/authorize",
		TokenEndpoint:         srv.URL + "/token",
		DiscoveredAt:          time.Now(),
	}
	h.registeredCred = &AuthCredential{ClientID: "cid", ClientSecret: "secret"}
	h.mu.Unlock()

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired",
		RefreshToken: "dead-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("GetOAuthToken: got %v, want ErrAuthRequired", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.discoveredMeta == nil {
		t.Error("discoveredMeta should be preserved on invalid_grant refresh error")
	}
	if h.registeredCred == nil {
		t.Error("registeredCred should be preserved on invalid_grant refresh error")
	}
}

func TestRefresh_NetworkError_WrapsErrTransientRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srvURL := srv.URL
	srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: srvURL + "/authorize",
			TokenURL:         srvURL + "/token",
		},
		Credential: &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())

	h.mu.Lock()
	h.registeredCred = &AuthCredential{ClientID: "cid", ClientSecret: "secret"}
	h.mu.Unlock()

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired",
		RefreshToken: "valid-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetOAuthToken(context.Background(), toolCtx)
	if !errors.Is(err, ErrTransientRefresh) || errors.Is(err, ErrAuthRequired) {
		t.Fatalf("GetOAuthToken: got %v, want ErrTransientRefresh without ErrAuthRequired", err)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Fatal("stored token should be preserved on network error")
	}
	var tok OAuthToken
	_ = json.Unmarshal([]byte(stored), &tok)
	if tok.RefreshToken != "valid-rt" {
		t.Errorf("refresh_token = %q, want valid-rt", tok.RefreshToken)
	}
}

func TestGetOAuthToken_RefreshAnswer_ClassifiesOutageVersusRejection(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body          string
		wantTransient bool
	}{
		{name: "503 server_error", status: http.StatusServiceUnavailable, body: `{"error":"server_error"}`, wantTransient: true},
		{name: "500 without a body", status: http.StatusInternalServerError, wantTransient: true},
		{name: "429 rate limited", status: http.StatusTooManyRequests, body: `{"error":"slow_down"}`, wantTransient: true},
		{name: "400 temporarily_unavailable", status: http.StatusBadRequest, body: `{"error":"temporarily_unavailable"}`, wantTransient: true},
		{name: "400 slow_down", status: http.StatusBadRequest, body: `{"error":"slow_down"}`, wantTransient: true},
		{name: "400 invalid_grant", status: http.StatusBadRequest, body: `{"error":"invalid_grant"}`},
		{name: "408 request timeout", status: http.StatusRequestTimeout, wantTransient: true},
		{name: "500 invalid_grant", status: http.StatusInternalServerError, body: `{"error":"invalid_grant"}`},
		{name: "503 invalid_client", status: http.StatusServiceUnavailable, body: `{"error":"invalid_client"}`},
		{name: "401 without a code", status: http.StatusUnauthorized, body: `unauthorized`},
		{name: "403 maintenance page", status: http.StatusForbidden, body: `<html>blocked</html>`, wantTransient: true},
		{name: "404 without a code", status: http.StatusNotFound, body: `not found`, wantTransient: true},
		{name: "403 unauthorized_client", status: http.StatusForbidden, body: `{"error":"unauthorized_client"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.body != "" && tc.body[0] == '{' {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			h := NewAuthHandler(&AuthConfig{
				Type:          "oauth2",
				CredentialKey: "test_key",
				Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
			}, slog.Default())
			credSvc := newMockCredentialService()
			storeToken(t, credSvc, &OAuthToken{
				AccessToken:  "expired-at",
				RefreshToken: "valid-rt",
				TokenType:    "Bearer",
				ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
			})
			toolCtx := testToolCtx(credSvc)

			_, err := h.GetOAuthToken(t.Context(), toolCtx)
			stored, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key")

			if tc.wantTransient {
				if !errors.Is(err, ErrTransientRefresh) || errors.Is(err, ErrAuthRequired) {
					t.Fatalf("GetOAuthToken: got %v, want ErrTransientRefresh without ErrAuthRequired", err)
				}
				if stored == "" {
					t.Fatal("an outage at the token endpoint must keep the stored credential")
				}
				return
			}
			if !errors.Is(err, ErrAuthRequired) {
				t.Fatalf("GetOAuthToken: got %v, want ErrAuthRequired", err)
			}
			if stored != "" {
				t.Errorf("a rejected refresh must clear the stored credential, got %q", stored)
			}
		})
	}
}

func TestGetOAuthToken_FreshToken_NoRedundantSave(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken: "stored-at",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
	})
	toolCtx := testToolCtx(credSvc)

	credSvc.resetCounters()

	const calls = 5
	for i := range calls {
		tok, err := h.GetOAuthToken(context.Background(), toolCtx)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if tok == nil || tok.AccessToken != "stored-at" {
			t.Fatalf("call %d: unexpected token %+v", i, tok)
		}
	}

	if got := credSvc.setCount.Load(); got != 0 {
		t.Errorf("CredentialService.Set called %d times for %d fresh-token loads, want 0", got, calls)
	}
	if got := credSvc.getCount.Load(); got != calls {
		t.Errorf("CredentialService.Get called %d times for %d loads, want %d", got, calls, calls)
	}
}

func TestResetRegistration_IsIdempotentAndSafe(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	h.resetRegistration()

	h.mu.Lock()
	h.discoveredMeta = &OAuthMetadata{Issuer: "x"}
	h.registeredCred = &AuthCredential{ClientID: "y"}
	h.registeredAuthMethod = "client_secret_post"
	h.mu.Unlock()

	h.resetRegistration()

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.discoveredMeta != nil || h.registeredCred != nil || h.registeredAuthMethod != "" {
		t.Error("resetRegistration did not clear all fields")
	}
}

func TestHandleAuthResponse_ConcurrentFlows_DistinctFuncCallIDs(t *testing.T) {
	var (
		mu            sync.Mutex
		seenVerifiers []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		seenVerifiers = append(seenVerifiers, r.Form.Get("code_verifier"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "shared_key",
		Scheme:        &OAuthScheme{AuthorizationURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token"},
		Credential:    &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())
	credSvc := newMockCredentialService()

	ctxA := &tool.Context{AgentName: "TestAgent", UserID: "user1", ToolCallID: "call-A", CredentialService: credSvc}
	ctxB := &tool.Context{AgentName: "TestAgent", UserID: "user1", ToolCallID: "call-B", CredentialService: credSvc}

	if err := h.storePending(context.Background(), ctxA, promptFlow(ctxA), pendingAuth{CodeVerifier: "verifier-A", State: "STATE_A", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("storePending A: %v", err)
	}
	if err := h.storePending(context.Background(), ctxB, promptFlow(ctxB), pendingAuth{CodeVerifier: "verifier-B", State: "STATE_B", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("storePending B: %v", err)
	}

	if err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{FunctionCallID: "call-A", Code: "code-A", State: "STATE_A"}, ctxA); err != nil {
		t.Fatalf("callback A should succeed, got %v", err)
	}
	if err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{FunctionCallID: "call-B", Code: "code-B", State: "STATE_B"}, ctxB); err != nil {
		t.Fatalf("callback B should succeed, got %v", err)
	}
	mu.Lock()
	got := seenVerifiers
	mu.Unlock()
	if len(got) != 2 || got[0] != "verifier-A" || got[1] != "verifier-B" {
		t.Errorf("each flow must exchange with its own stored verifier, got %v", got)
	}
}

func TestHandleAuthResponse_PerFlowKey_StillRejectsForgedState(t *testing.T) {
	tokenCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { tokenCalled = true }))
	defer srv.Close()

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "shared_key",
		Scheme:        &OAuthScheme{AuthorizationURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token"},
		Credential:    &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
	}, slog.Default())
	credSvc := newMockCredentialService()
	ctx := &tool.Context{AgentName: "TestAgent", UserID: "user1", ToolCallID: "call-A", CredentialService: credSvc}

	if err := h.storePending(context.Background(), ctx, promptFlow(ctx), pendingAuth{CodeVerifier: "v", State: "GOOD", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("storePending: %v", err)
	}
	err := h.HandleAuthResponse(context.Background(), &a2a.AuthResponseData{FunctionCallID: "call-A", Code: "c", State: "EVIL"}, ctx)
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("forged state must be rejected with ErrStateMismatch, got %v", err)
	}
	if tokenCalled {
		t.Error("token endpoint must not be hit on state mismatch")
	}
}

func TestNewAuthHandler_DerivesKeyOnCopyWithoutMutatingCaller(t *testing.T) {
	cfg := &AuthConfig{
		Type:   "oauth2",
		Scheme: &OAuthScheme{TokenURL: "https://idp.example.com/token"},
	}
	h := NewAuthHandler(cfg, nil)

	if cfg.CredentialKey != "" {
		t.Errorf("caller cfg.CredentialKey was mutated to %q; NewAuthHandler must derive onto a copy", cfg.CredentialKey)
	}
	got := h.CredentialKey()
	if !strings.HasPrefix(got, "oauth2_") {
		t.Errorf("handler key %q was not derived from the auth identity", got)
	}
}

func TestNewAuthHandler_KeyDerivationGuards(t *testing.T) {
	if got := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "explicit_key"}, nil).CredentialKey(); got != "explicit_key" {
		t.Errorf("explicit key changed: got %q, want %q", got, "explicit_key")
	}
	if got := NewAuthHandler(&AuthConfig{Type: "basic"}, nil).CredentialKey(); got != "" {
		t.Errorf("non-oauth2 key was derived unexpectedly: got %q, want empty", got)
	}
}

func TestResourceIndicatorPrecedence(t *testing.T) {
	t.Run("scheme override wins", func(t *testing.T) {
		h := NewAuthHandler(&AuthConfig{
			Type:        "oauth2",
			ResourceURL: "https://mcp.example.com/mcp",
		}, slog.Default())
		h.discoveredMeta = &OAuthMetadata{Resource: "https://discovered.example.com"}
		got := h.resourceIndicator(&OAuthScheme{Resource: "https://override.example.com"})
		if got != "https://override.example.com" {
			t.Errorf("resourceIndicator = %q, want the scheme override", got)
		}
	})

	t.Run("discovered PRM resource beats ResourceURL", func(t *testing.T) {
		h := NewAuthHandler(&AuthConfig{
			Type:        "oauth2",
			ResourceURL: "https://mcp.example.com/mcp",
		}, slog.Default())
		h.discoveredMeta = &OAuthMetadata{Resource: "https://discovered.example.com"}
		got := h.resourceIndicator(&OAuthScheme{})
		if got != "https://discovered.example.com" {
			t.Errorf("resourceIndicator = %q, want the discovered PRM resource", got)
		}
	})

	t.Run("falls back to ResourceURL", func(t *testing.T) {
		h := NewAuthHandler(&AuthConfig{
			Type:        "oauth2",
			ResourceURL: "https://mcp.example.com/mcp",
		}, slog.Default())
		got := h.resourceIndicator(&OAuthScheme{})
		if got != "https://mcp.example.com/mcp" {
			t.Errorf("resourceIndicator = %q, want the configured ResourceURL", got)
		}
	})

	t.Run("cfg.Scheme override survives the discovery path", func(t *testing.T) {
		h := NewAuthHandler(&AuthConfig{
			Type:        "oauth2",
			ResourceURL: "https://mcp.example.com/mcp",
			Scheme:      &OAuthScheme{Resource: "https://override.example.com"},
		}, slog.Default())
		h.discoveredMeta = &OAuthMetadata{Resource: "https://discovered.example.com"}
		got := h.resourceIndicator(&OAuthScheme{TokenURL: "https://auth/token"})
		if got != "https://override.example.com" {
			t.Errorf("resourceIndicator = %q, want the cfg.Scheme override to win over the discovered resource", got)
		}
	})
}

func TestWithResourceIndicator_DoesNotMutateSharedScheme(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: "https://mcp.example.com/mcp",
	}, slog.Default())
	shared := &OAuthScheme{TokenURL: "https://auth.example.com/token"}
	got := h.withResourceIndicator(shared)
	if shared.Resource != "" {
		t.Errorf("shared scheme mutated: Resource = %q", shared.Resource)
	}
	if got.Resource != "https://mcp.example.com/mcp" {
		t.Errorf("resolved scheme Resource = %q, want the ResourceURL fallback", got.Resource)
	}
}

type resourceRecordingServer struct {
	*httptest.Server
	declaredResource string

	mu               sync.Mutex
	exchangeResource string
	refreshResource  string
}

func (s *resourceRecordingServer) exchangeResourceSeen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exchangeResource
}

func (s *resourceRecordingServer) refreshResourceSeen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshResource
}

func newResourceRecordingServer(t *testing.T, declaredResource string) *resourceRecordingServer {
	t.Helper()
	rec := &resourceRecordingServer{declaredResource: declaredResource}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              rec.declaredResource,
				"authorization_servers": []string{rec.URL},
			})
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 rec.URL,
				"authorization_endpoint": rec.URL + "/authorize",
				"token_endpoint":         rec.URL + "/token",
				"registration_endpoint":  rec.URL + "/register",
				"scopes_supported":       []string{"default"},
			})
		case "/register":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  "mock-client",
				"client_secret":              "mock-secret",
				"token_endpoint_auth_method": "client_secret_post",
			})
		case "/token":
			_ = r.ParseForm()
			rec.mu.Lock()
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				rec.exchangeResource = r.Form.Get("resource")
			case "refresh_token":
				rec.refreshResource = r.Form.Get("resource")
			}
			rec.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "assembled-at",
				"refresh_token": "assembled-rt",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return rec
}

func TestAuthHandler_SendsDiscoveredResourceIndicator(t *testing.T) {
	const declaredResource = "https://declared.example.com/canonical"
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	t.Run("authorization_code exchange", func(t *testing.T) {
		srv := newResourceRecordingServer(t, declaredResource)
		defer srv.Close()
		clearDiscoveryCacheForTest(t)

		if srv.URL == declaredResource {
			t.Fatal("test invariant: ResourceURL must differ from the declared PRM resource")
		}

		h := NewAuthHandler(&AuthConfig{
			Type:          "oauth2",
			CredentialKey: "test_key",
			ResourceURL:   srv.URL,
		}, slog.Default())

		credSvc := newMockCredentialService()
		if err := h.HandleAuthResponse(t.Context(), &a2a.AuthResponseData{
			Code:         "auth-code",
			CodeVerifier: "test-verifier",
		}, testToolCtx(credSvc)); err != nil {
			t.Fatalf("HandleAuthResponse: %v", err)
		}

		if got := srv.exchangeResourceSeen(); got != declaredResource {
			t.Errorf("token exchange resource = %q, want the discovered PRM resource %q", got, declaredResource)
		}
	})

	t.Run("refresh_token grant", func(t *testing.T) {
		srv := newResourceRecordingServer(t, declaredResource)
		defer srv.Close()
		clearDiscoveryCacheForTest(t)

		h := NewAuthHandler(&AuthConfig{
			Type:          "oauth2",
			CredentialKey: "test_key",
			ResourceURL:   srv.URL,
		}, slog.Default())

		credSvc := newMockCredentialService()
		storeToken(t, credSvc, &OAuthToken{
			AccessToken:  "stale-at",
			RefreshToken: "stale-rt",
			TokenType:    "Bearer",
			ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
		})

		if _, err := h.GetOAuthToken(t.Context(), testToolCtx(credSvc)); err != nil {
			t.Fatalf("GetOAuthToken (should refresh): %v", err)
		}

		if got := srv.refreshResourceSeen(); got != declaredResource {
			t.Errorf("token refresh resource = %q, want the discovered PRM resource %q", got, declaredResource)
		}
	})
}

func TestBuildAuthSignal_EvalSystemUserIsClassifyOnly(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "jira_token",
		DisplayName:   "Atlassian Jira",
		ResourceURL:   "https://127.0.0.1:1/unreachable",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	toolCtx.UserID = rbac.EvalSystemUserSub
	toolCtx.Trigger = a2a.TriggerEval
	credSvc.resetCounters()

	signal, err := h.BuildAuthSignal(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("BuildAuthSignal: %v", err)
	}
	if signal.AuthURI != "" {
		t.Errorf("auth_uri = %q, want empty — a shared principal's prompt must not be completable", signal.AuthURI)
	}
	if signal.CodeVerifier != "" {
		t.Errorf("code_verifier = %q, want empty", signal.CodeVerifier)
	}
	if n := credSvc.setCount.Load(); n != 0 {
		t.Errorf("credential service writes = %d, want 0 — no pending-auth row may be persisted for a trial nobody completes", n)
	}
	if signal.Type != string(a2a.SignalAuthenticationRequired) {
		t.Errorf("type = %q, want %q", signal.Type, a2a.SignalAuthenticationRequired)
	}
	if signal.ToolDisplayName != "Atlassian Jira" {
		t.Errorf("tool_display_name = %q, want %q — eval attribution names the service from it", signal.ToolDisplayName, "Atlassian Jira")
	}
	if signal.CredentialKey != "jira_token" {
		t.Errorf("credential_key = %q, want %q", signal.CredentialKey, "jira_token")
	}
	if signal.UserID != rbac.EvalSystemUserSub {
		t.Errorf("user_id = %q, want %q", signal.UserID, rbac.EvalSystemUserSub)
	}
}

func TestBuildAuthSignal_CannotPromptIsClassifyOnly(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "jira_token",
		DisplayName:   "Atlassian Jira",
		ResourceURL:   "https://127.0.0.1:1/unreachable",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	toolCtx.UserID = "alice@example.com"
	toolCtx.Trigger = a2a.TriggerScheduled
	toolCtx.AuthPromptUnavailable = true
	credSvc.resetCounters()

	signal, err := h.BuildAuthSignal(t.Context(), toolCtx)
	if err != nil {
		t.Fatalf("BuildAuthSignal: %v", err)
	}
	if signal.AuthURI != "" || signal.CodeVerifier != "" {
		t.Errorf("auth_uri %q, code_verifier %q; want a report-only signal", signal.AuthURI, signal.CodeVerifier)
	}
	if n := credSvc.setCount.Load(); n != 0 {
		t.Errorf("credential service writes = %d, want 0 — no pending-auth row for a prompt nobody can see", n)
	}
	if signal.CredentialKey != "jira_token" {
		t.Errorf("credential_key = %q, want %q", signal.CredentialKey, "jira_token")
	}
}

func TestBuildAuthSignal_HumanPrincipalStillStartsRealFlow(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "jira_token",
		ResourceURL:   "https://127.0.0.1:1/unreachable",
	}, slog.Default())

	toolCtx := testToolCtx(newMockCredentialService())
	toolCtx.Trigger = a2a.TriggerEval

	if _, err := h.BuildAuthSignal(context.Background(), toolCtx); err == nil {
		t.Fatal("BuildAuthSignal returned a signal for a human principal without reachable OAuth endpoints; the classify-only branch widened past system users")
	}
}

func TestAnswersPendingPromptComparesTheStoredState(t *testing.T) {
	h := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "prompt_key"}, slog.Default())
	toolCtx := testToolCtx(newMockCredentialService())
	answer := func(state string) *a2a.AuthResponseData {
		return &a2a.AuthResponseData{FunctionCallID: toolCtx.ToolCallID, TargetAgentTaskID: toolCtx.ParentTaskID, State: state}
	}

	if _, known := h.AnswersPendingPrompt(t.Context(), answer("S2"), toolCtx); known {
		t.Fatal("claimed to know with no prompt stored")
	}
	if err := h.storePending(t.Context(), toolCtx, promptFlow(toolCtx), pendingAuth{State: "S2", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("storePending: %v", err)
	}
	if matches, known := h.AnswersPendingPrompt(t.Context(), answer("S2"), toolCtx); !known || !matches {
		t.Fatalf("current prompt's answer: matches=%v known=%v", matches, known)
	}
	if matches, known := h.AnswersPendingPrompt(t.Context(), answer("S1"), toolCtx); !known || matches {
		t.Fatalf("earlier prompt's answer: matches=%v known=%v", matches, known)
	}
	if _, known := h.AnswersPendingPrompt(t.Context(), answer(""), toolCtx); known {
		t.Fatal("claimed to know for an answer without state")
	}
}

func TestPendingPromptBelongsToItsTask(t *testing.T) {
	tests := []struct {
		name          string
		storedByOlder bool
	}{
		{name: "two tasks of one user with the same tool call id each keep their own prompt"},
		{name: "an answer finds a prompt an older replica stored under the call-only key", storedByOlder: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAuthHandler(&AuthConfig{Type: "oauth2", CredentialKey: "csrf_key"}, slog.Default())
			credSvc := newMockCredentialService()
			taskCtx := func(taskID string) *tool.Context {
				return &tool.Context{AgentName: "TestAgent", UserID: "user1", ParentTaskID: taskID, ToolCallID: "call_0_0", CredentialService: credSvc}
			}
			first, second := taskCtx("task-A"), taskCtx("task-B")
			if tt.storedByOlder {
				data, _ := json.Marshal(pendingAuth{State: "STATE_A", CreatedAt: time.Now()})
				if err := credSvc.Set(t.Context(), "TestAgent", "TestAgent", "user1", "csrf_key:call_0_0.pending", string(data)); err != nil {
					t.Fatalf("Set: %v", err)
				}
			} else {
				prompts := []struct {
					ctx   *tool.Context
					state string
				}{{first, "STATE_A"}, {second, "STATE_B"}}
				for _, p := range prompts {
					if err := h.storePending(t.Context(), p.ctx, promptFlow(p.ctx), pendingAuth{State: p.state, CreatedAt: time.Now()}); err != nil {
						t.Fatalf("storePending: %v", err)
					}
				}
			}

			answer := &a2a.AuthResponseData{FunctionCallID: "call_0_0", TargetAgentTaskID: "task-A", State: "STATE_A"}
			if matches, known := h.AnswersPendingPrompt(t.Context(), answer, first); !known || !matches {
				t.Errorf("task A's answer: matches=%v known=%v, want both true", matches, known)
			}
		})
	}
}

func TestPromptsOfTwoReleasesForOneCall(t *testing.T) {
	tests := []struct {
		name         string
		state        string
		wantMatch    bool
		wantVerifier string
	}{
		{name: "the answer to this release's prompt", state: "NEW", wantMatch: true, wantVerifier: "verifier-new"},
		{name: "the answer to the older release's prompt", state: "OLD", wantMatch: true, wantVerifier: "verifier-old"},
		{name: "an answer to neither prompt", state: "OTHER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seenVerifier string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				seenVerifier = r.Form.Get("code_verifier")
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
			}))
			defer srv.Close()
			h := NewAuthHandler(&AuthConfig{
				Type:          "oauth2",
				CredentialKey: "csrf_key",
				Scheme:        &OAuthScheme{AuthorizationURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token"},
				Credential:    &AuthCredential{ClientID: "cid", ClientSecret: "secret"},
			}, slog.Default())
			credSvc := newMockCredentialService()
			toolCtx := testToolCtx(credSvc)
			if err := h.storePending(t.Context(), toolCtx, promptFlow(toolCtx), pendingAuth{CodeVerifier: "verifier-new", State: "NEW", CreatedAt: time.Now()}); err != nil {
				t.Fatalf("storePending: %v", err)
			}
			older, _ := json.Marshal(pendingAuth{CodeVerifier: "verifier-old", State: "OLD", CreatedAt: time.Now()})
			if err := credSvc.Set(t.Context(), "TestAgent", "TestAgent", "user1", "csrf_key:call-456.pending", string(older)); err != nil {
				t.Fatalf("Set: %v", err)
			}
			answer := &a2a.AuthResponseData{
				FunctionCallID:    toolCtx.ToolCallID,
				TargetAgentTaskID: toolCtx.ParentTaskID,
				Code:              "auth-code",
				CodeVerifier:      "response-verifier",
				State:             tt.state,
			}

			matches, known := h.AnswersPendingPrompt(t.Context(), answer, toolCtx)
			if !known || matches != tt.wantMatch {
				t.Fatalf("AnswersPendingPrompt: matches=%v known=%v, want matches=%v known=true", matches, known, tt.wantMatch)
			}
			err := h.HandleAuthResponse(t.Context(), answer, toolCtx)
			if !tt.wantMatch {
				if !errors.Is(err, ErrStateMismatch) {
					t.Fatalf("HandleAuthResponse: %v, want ErrStateMismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("HandleAuthResponse: %v", err)
			}
			if seenVerifier != tt.wantVerifier {
				t.Errorf("token endpoint saw code_verifier %q, want %q", seenVerifier, tt.wantVerifier)
			}
		})
	}
}

func TestPromptDeletesTheOlderReleasesRowForTheCall(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "csrf_key",
		Scheme:        &OAuthScheme{AuthorizationURL: "https://auth.example.com/authorize", TokenURL: "https://auth.example.com/token"},
		Credential:    &AuthCredential{ClientID: "cid"},
	}, slog.Default())
	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)
	older, _ := json.Marshal(pendingAuth{CodeVerifier: "verifier-old", State: "OLD", CreatedAt: time.Now()})
	if err := credSvc.Set(t.Context(), "TestAgent", "TestAgent", "user1", "csrf_key:call-456.pending", string(older)); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, err := h.BuildAuthSignal(t.Context(), toolCtx); err != nil {
		t.Fatalf("BuildAuthSignal: %v", err)
	}

	if raw, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "csrf_key:call-456.pending"); raw != "" {
		t.Errorf("the older release's row for this call is still stored: %q", raw)
	}
	if raw, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "csrf_key:task-123:call-456.pending"); raw == "" {
		t.Error("this prompt's row was not stored")
	}
}
