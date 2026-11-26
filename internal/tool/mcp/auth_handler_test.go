// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type mockCredentialService struct {
	store map[string]string
}

func newMockCredentialService() *mockCredentialService {
	return &mockCredentialService{store: make(map[string]string)}
}

func (m *mockCredentialService) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	k := agentName + "|" + appName + "|" + userID + "|" + key
	return m.store[k], nil
}

func (m *mockCredentialService) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	k := agentName + "|" + appName + "|" + userID + "|" + key
	m.store[k] = value
	return nil
}

func (m *mockCredentialService) Delete(_ context.Context, agentName, appName, userID, key string) error {
	k := agentName + "|" + appName + "|" + userID + "|" + key
	delete(m.store, k)
	return nil
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

func TestGetAuthHeaders_Basic(t *testing.T) {
	h := NewAuthHandler(&MCPAuthConfig{
		Type: "basic",
		Credential: &MCPAuthCred{
			Username: "admin",
			Password: "secret",
		},
	}, slog.Default())

	headers, err := h.GetAuthHeaders(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetAuthHeaders() error: %v", err)
	}

	auth := headers.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("expected Basic auth, got %q", auth)
	}
}

func TestGetAuthHeaders_Bearer(t *testing.T) {
	h := NewAuthHandler(&MCPAuthConfig{
		Type: "bearer",
		Credential: &MCPAuthCred{
			Token: "my-static-token",
		},
	}, slog.Default())

	headers, err := h.GetAuthHeaders(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetAuthHeaders() error: %v", err)
	}

	if got := headers.Get("Authorization"); got != "Bearer my-static-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer my-static-token")
	}
}

func TestGetAuthHeaders_OAuth2_NoCredential(t *testing.T) {
	h := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	_, err := h.GetAuthHeaders(context.Background(), toolCtx)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired, got %v", err)
	}
}

func TestGetAuthHeaders_OAuth2_WithValidToken(t *testing.T) {
	h := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	token := &OAuthToken{
		AccessToken: "stored-access-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour).Unix(),
	}
	tokenJSON, _ := json.Marshal(token)
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user1", "test_key", string(tokenJSON))

	headers, err := h.GetAuthHeaders(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("GetAuthHeaders() error: %v", err)
	}

	if got := headers.Get("Authorization"); got != "Bearer stored-access-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer stored-access-token")
	}
}

func TestGetAuthHeaders_OAuth2_ExpiredToken_NoRefresh(t *testing.T) {
	h := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	token := &OAuthToken{
		AccessToken: "expired-token",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(-1 * time.Hour).Unix(),
	}
	tokenJSON, _ := json.Marshal(token)
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user1", "test_key", string(tokenJSON))

	_, err := h.GetAuthHeaders(context.Background(), toolCtx)
	if err != ErrAuthRequired {
		t.Errorf("expected ErrAuthRequired for expired token without refresh, got %v", err)
	}
}

func TestGetAuthHeaders_OAuth2_ExpiredToken_WithRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-access-token",
			"refresh_token": "new-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	h := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme: &OAuthScheme{
			TokenURL:                srv.URL + "/token",
			TokenEndpointAuthMethod: "client_secret_post",
		},
		Credential: &MCPAuthCred{
			ClientID:     "cid",
			ClientSecret: "csecret",
		},
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	token := &OAuthToken{
		AccessToken:  "expired-token",
		RefreshToken: "old-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	}
	tokenJSON, _ := json.Marshal(token)
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user1", "test_key", string(tokenJSON))

	headers, err := h.GetAuthHeaders(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("GetAuthHeaders() error: %v", err)
	}

	if got := headers.Get("Authorization"); got != "Bearer refreshed-access-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer refreshed-access-token")
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	var storedToken OAuthToken
	_ = json.Unmarshal([]byte(stored), &storedToken)
	if storedToken.AccessToken != "refreshed-access-token" {
		t.Errorf("stored token = %q", storedToken.AccessToken)
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		name   string
		result *rawCallToolResult
		want   bool
	}{
		{"nil result", nil, false},
		{"not error", &rawCallToolResult{IsError: false}, false},
		{
			"401 in code field",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"code": 401, "text": "Unauthorized access"})},
			},
			true,
		},
		{
			"403 in status field",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"status": "403", "text": "Forbidden"})},
			},
			true,
		},
		{
			"401 unauthorized in text",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "Error 401 Unauthorized: invalid token"})},
			},
			true,
		},
		{
			"403 forbidden in text",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "Error 403 Forbidden: insufficient scope"})},
			},
			true,
		},
		{
			"500 error (not auth)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"code": 500, "text": "Internal Server Error"})},
			},
			false,
		},
		{
			"text without auth indicators",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "Something went wrong"})},
			},
			false,
		},
		{
			"string-typed 401 in code field is an auth error (intentional Go divergence from Python)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"code": "401", "text": "bad token"})},
			},
			true,
		},
		{
			"large legitimate content with substring (no flag)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": strings.Repeat("x", 5000) +
						" 401 Unauthorized in some embedded log " +
						strings.Repeat("y", 5000),
				})},
			},
			false,
		},
		{
			"non-error result with substring (no flag)",
			&rawCallToolResult{
				IsError: false,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": "Error 401 Unauthorized: invalid token",
				})},
			},
			false,
		},
		{
			"non-error result with structured code 401 (always flag)",
			&rawCallToolResult{
				IsError: false,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"code": 401, "text": "Unauthorized",
				})},
			},
			true,
		},
		{
			"non-error result with structured status 403 (always flag)",
			&rawCallToolResult{
				IsError: false,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"status": 403, "text": "Forbidden",
				})},
			},
			true,
		},
		{
			"structured code 401 inside text JSON (always flag)",
			&rawCallToolResult{
				IsError: false,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": `{"code":401,"message":"Unauthorized","extra":"` +
						strings.Repeat(" padding", 500) + `"}`,
				})},
			},
			true,
		},
		{
			"structured status 403 inside text JSON (always flag)",
			&rawCallToolResult{
				IsError: false,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": `{"status":403,"message":"Forbidden"}`,
				})},
			},
			true,
		},
		{
			"empty payload (no flag)",
			&rawCallToolResult{IsError: true, Content: nil},
			false,
		},
		{
			"substring at exact size boundary (flag)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": "401 Unauthorized" +
						strings.Repeat(" ", substringAuthCheckMaxTextLen-len("401 Unauthorized")),
				})},
			},
			true,
		},
		{
			"substring one byte over boundary (no flag)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": "401 Unauthorized" +
						strings.Repeat(" ", substringAuthCheckMaxTextLen-len("401 Unauthorized")+1),
				})},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAuthError(tt.result); got != tt.want {
				t.Errorf("IsAuthError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsScopeError(t *testing.T) {
	tests := []struct {
		name   string
		result *rawCallToolResult
		want   bool
	}{
		{"nil result", nil, false},
		{"not error", &rawCallToolResult{IsError: true, Content: nil}, false},
		{
			"missing_scope in structured error field",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"error": "missing_scope"})},
			},
			true,
		},
		{
			"missing_scope token in text",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "slack api error: missing_scope"})},
			},
			true,
		},
		{
			"missing_scope in structured code field",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"code": "missing_scope"})},
			},
			true,
		},
		{
			"structured missing_scope inside text JSON",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": `{"ok":false,"error":"missing_scope"}`})},
			},
			true,
		},
		{
			"insufficient_scope in structured error field",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"error": "insufficient_scope"})},
			},
			true,
		},
		{
			"insufficient_scope token in text",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "oauth error: insufficient_scope"})},
			},
			true,
		},
		{
			"adjacent prose missing scope",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "the request failed: missing scope for this action"})},
			},
			true,
		},
		{
			"non-adjacent missing ... scope (no flag)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "the bot is missing the chat:write scope"})},
			},
			false,
		},
		{
			"unrelated error (no flag)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "channel_not_found"})},
			},
			false,
		},
		{
			"non-error result with scope words (no flag)",
			&rawCallToolResult{
				IsError: false,
				Content: []json.RawMessage{mustJSON(map[string]any{"text": "the missing scope guide explains OAuth scopes"})},
			},
			false,
		},
		{
			"large content with scope words (no flag)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": strings.Repeat("x", 3000) + " missing scope " + strings.Repeat("y", 3000),
				})},
			},
			false,
		},
		{
			"prose at exact size boundary (flag)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": "missing scope" +
						strings.Repeat(" ", substringAuthCheckMaxTextLen-len("missing scope")),
				})},
			},
			true,
		},
		{
			"prose one byte over boundary (no flag)",
			&rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{mustJSON(map[string]any{
					"text": "missing scope" +
						strings.Repeat(" ", substringAuthCheckMaxTextLen-len("missing scope")+1),
				})},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsScopeError(tt.result); got != tt.want {
				t.Errorf("IsScopeError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractErrorText(t *testing.T) {
	t.Run("joins text content", func(t *testing.T) {
		r := &rawCallToolResult{Content: []json.RawMessage{
			mustJSON(map[string]any{"type": "text", "text": "boom"}),
		}}
		if got := extractErrorText(r); got != "boom" {
			t.Errorf("got %q, want %q", got, "boom")
		}
	})

	t.Run("falls back to raw content when no text part", func(t *testing.T) {
		r := &rawCallToolResult{Content: []json.RawMessage{
			json.RawMessage(`{"ok":false,"error":"missing_scope"}`),
		}}
		got := extractErrorText(r)
		if !strings.Contains(got, "missing_scope") {
			t.Errorf("raw error detail dropped: got %q", got)
		}
	})

	t.Run("generic only when truly empty", func(t *testing.T) {
		if got := extractErrorText(&rawCallToolResult{Content: nil}); got != "MCP tool returned an error" {
			t.Errorf("got %q, want generic fallback", got)
		}
	})
}

func TestBuildAuthSignal(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	h := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "my_cred_key",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://auth.example.com/authorize",
			TokenURL:         "https://auth.example.com/token",
			Scopes:           []string{"read"},
		},
		Credential: &MCPAuthCred{
			ClientID:     "test-client-id",
			ClientSecret: "test-secret",
		},
	}, slog.Default())

	toolCtx := testToolCtx(newMockCredentialService())

	signal, err := h.BuildAuthSignal(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("BuildAuthSignal() error: %v", err)
	}

	if signal.Type != "authentication_required" {
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
	if !strings.Contains(signal.AuthURI, "code_challenge_method=S256") {
		t.Errorf("AuthURI missing PKCE method: %s", signal.AuthURI)
	}
	if !strings.Contains(signal.AuthURI, "state=") {
		t.Errorf("AuthURI missing state: %s", signal.AuthURI)
	}
}

func TestHandleAuthResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" {
			http.Error(w, "wrong grant_type", 400)
			return
		}
		if r.Form.Get("code") != "auth-code-123" {
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

	h := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme: &OAuthScheme{
			TokenURL:                srv.URL + "/token",
			TokenEndpointAuthMethod: "client_secret_post",
		},
		Credential: &MCPAuthCred{
			ClientID:     "cid",
			ClientSecret: "csecret",
		},
	}, slog.Default())

	credSvc := newMockCredentialService()
	toolCtx := testToolCtx(credSvc)

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	resp := &a2a.AuthResponseData{
		Code:         "auth-code-123",
		CodeVerifier: "test-verifier",
	}

	err := h.HandleAuthResponse(context.Background(), resp, toolCtx)
	if err != nil {
		t.Fatalf("HandleAuthResponse() error: %v", err)
	}

	stored, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user1", "test_key")
	if stored == "" {
		t.Fatal("token should be stored after HandleAuthResponse")
	}

	var storedToken OAuthToken
	if err := json.Unmarshal([]byte(stored), &storedToken); err != nil {
		t.Fatalf("unmarshal stored token: %v", err)
	}
	if storedToken.AccessToken != "new-token" {
		t.Errorf("stored AccessToken = %q", storedToken.AccessToken)
	}
	if storedToken.RefreshToken != "new-refresh" {
		t.Errorf("stored RefreshToken = %q", storedToken.RefreshToken)
	}
}

func TestCredentialKey(t *testing.T) {
	h := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "my_key",
	}, slog.Default())

	if got := h.CredentialKey(); got != "my_key" {
		t.Errorf("CredentialKey() = %q, want %q", got, "my_key")
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
