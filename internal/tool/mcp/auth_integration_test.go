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

func TestOAuth2FullFlow(t *testing.T) {
	var tokenRequests int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}

		grantType := r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")

		switch grantType {
		case "authorization_code":
			code := r.Form.Get("code")
			if code != "test-auth-code" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":             "invalid_grant",
					"error_description": "invalid code",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-token-from-exchange",
				"refresh_token": "refresh-token-from-exchange",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"scope":         "read write",
			})
		case "refresh_token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "refreshed-access-token",
				"refresh_token": "refreshed-refresh-token",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		default:
			http.Error(w, "unsupported grant_type", 400)
		}
	}))
	defer tokenSrv.Close()

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	cfg := &MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_mcp_oauth",
		Scheme: &OAuthScheme{
			AuthorizationURL:        "https://auth.example.com/authorize",
			TokenURL:                tokenSrv.URL + "/token",
			Scopes:                  []string{"read", "write"},
			TokenEndpointAuthMethod: "client_secret_post",
		},
		Credential: &MCPAuthCred{
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
		},
	}

	handler := NewAuthHandler(cfg, slog.Default())
	credSvc := newMockCredentialService()
	ctx := context.Background()

	toolCtx := &tool.Context{
		AgentName:         "TestAgent",
		UserID:            "user-123",
		ClientID:          "gateway-client-1",
		ParentTaskID:      "task-001",
		ToolCallID:        "call-001",
		CredentialService: credSvc,
	}

	t.Run("Step1_NoToken_AuthRequired", func(t *testing.T) {
		_, err := handler.GetAuthHeaders(ctx, toolCtx)
		if err != ErrAuthRequired {
			t.Fatalf("expected ErrAuthRequired, got: %v", err)
		}
	})

	var authSignal *a2a.AuthenticationRequiredData
	t.Run("Step2_BuildAuthSignal", func(t *testing.T) {
		var err error
		authSignal, err = handler.BuildAuthSignal(ctx, toolCtx)
		if err != nil {
			t.Fatalf("BuildAuthSignal() error: %v", err)
		}

		if authSignal.Type != "authentication_required" {
			t.Errorf("Type = %q", authSignal.Type)
		}
		if authSignal.TargetAgent != "TestAgent" {
			t.Errorf("TargetAgent = %q", authSignal.TargetAgent)
		}
		if authSignal.UserID != "user-123" {
			t.Errorf("UserID = %q", authSignal.UserID)
		}
		if authSignal.CredentialKey != "test_mcp_oauth" {
			t.Errorf("CredentialKey = %q", authSignal.CredentialKey)
		}
		if authSignal.CodeVerifier == "" {
			t.Fatal("CodeVerifier should not be empty")
		}
		if !strings.Contains(authSignal.AuthURI, "auth.example.com/authorize") {
			t.Errorf("AuthURI should contain auth server: %s", authSignal.AuthURI)
		}
		if !strings.Contains(authSignal.AuthURI, "client_id=test-client-id") {
			t.Errorf("AuthURI should contain client_id: %s", authSignal.AuthURI)
		}
		if !strings.Contains(authSignal.AuthURI, "code_challenge_method=S256") {
			t.Errorf("AuthURI should contain PKCE method: %s", authSignal.AuthURI)
		}
		if !strings.Contains(authSignal.AuthURI, "scope=read+write") {
			t.Errorf("AuthURI should contain scopes: %s", authSignal.AuthURI)
		}
	})

	t.Run("Step3_HandleAuthResponse", func(t *testing.T) {
		authResp := &a2a.AuthResponseData{
			Type:              "auth_response",
			TargetAgent:       authSignal.TargetAgent,
			TargetAgentTaskID: authSignal.TargetAgentTaskID,
			UserID:            authSignal.UserID,
			ClientID:          authSignal.ClientID,
			FunctionCallID:    authSignal.AuthRequestFunctionCallID,
			Code:              "test-auth-code",
			CredentialKey:     authSignal.CredentialKey,
			CodeVerifier:      authSignal.CodeVerifier,
			IsCredentialSave:  true,
		}

		err := handler.HandleAuthResponse(ctx, authResp, toolCtx)
		if err != nil {
			t.Fatalf("HandleAuthResponse() error: %v", err)
		}

		if tokenRequests != 1 {
			t.Errorf("expected 1 token request, got %d", tokenRequests)
		}

		stored, _ := credSvc.Get(ctx, "TestAgent", "TestAgent", "user-123", "test_mcp_oauth")
		if stored == "" {
			t.Fatal("token should be stored after HandleAuthResponse")
		}

		var token OAuthToken
		if err := json.Unmarshal([]byte(stored), &token); err != nil {
			t.Fatalf("unmarshal stored token: %v", err)
		}
		if token.AccessToken != "access-token-from-exchange" {
			t.Errorf("stored AccessToken = %q", token.AccessToken)
		}
		if token.RefreshToken != "refresh-token-from-exchange" {
			t.Errorf("stored RefreshToken = %q", token.RefreshToken)
		}
	})

	t.Run("Step4_AuthHeaders_WithToken", func(t *testing.T) {
		headers, err := handler.GetAuthHeaders(ctx, toolCtx)
		if err != nil {
			t.Fatalf("GetAuthHeaders() error: %v", err)
		}

		if got := headers.Get("Authorization"); got != "Bearer access-token-from-exchange" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer access-token-from-exchange")
		}
	})

	t.Run("Step5_AuthError_Detection", func(t *testing.T) {
		result := &rawCallToolResult{
			IsError: true,
			Content: []json.RawMessage{
				mustJSON(map[string]any{
					"type": "text",
					"text": "Error: 401 Unauthorized - Token expired",
				}),
			},
		}

		if !IsAuthError(result) {
			t.Error("expected IsAuthError to return true for 401 response")
		}

		if err := handler.ClearCredentialForReauth(ctx, toolCtx, "access-token-from-exchange"); err != nil {
			t.Fatalf("ClearCredentialForReauth() error: %v", err)
		}

		stored, _ := credSvc.Get(ctx, "TestAgent", "TestAgent", "user-123", "test_mcp_oauth")
		if stored != "" {
			t.Errorf("token should be cleared after ClearCredentialForReauth, got %q", stored)
		}
	})

	t.Run("Step6_AfterClear_AuthRequired", func(t *testing.T) {
		_, err := handler.GetAuthHeaders(ctx, toolCtx)
		if err != ErrAuthRequired {
			t.Fatalf("expected ErrAuthRequired after clear, got: %v", err)
		}
	})
}

func TestOAuth2TokenRefreshFlow(t *testing.T) {
	refreshCalled := false
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")

		if r.Form.Get("grant_type") == "refresh_token" {
			refreshCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "refreshed-token",
				"refresh_token": "new-refresh-token",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
			return
		}
		http.Error(w, "unexpected grant_type", 400)
	}))
	defer tokenSrv.Close()

	handler := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "refresh_test",
		Scheme: &OAuthScheme{
			TokenURL:                tokenSrv.URL + "/token",
			TokenEndpointAuthMethod: "client_secret_post",
		},
		Credential: &MCPAuthCred{
			ClientID:     "cid",
			ClientSecret: "csecret",
		},
	}, slog.Default())

	credSvc := newMockCredentialService()
	ctx := context.Background()
	toolCtx := &tool.Context{
		AgentName:         "RefreshAgent",
		UserID:            "user1",
		CredentialService: credSvc,
	}

	expiredToken := &OAuthToken{
		AccessToken:  "expired-access-token",
		RefreshToken: "old-refresh-token",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	}
	tokenJSON, _ := json.Marshal(expiredToken)
	_ = credSvc.Set(ctx, "RefreshAgent", "RefreshAgent", "user1", "refresh_test", string(tokenJSON))

	headers, err := handler.GetAuthHeaders(ctx, toolCtx)
	if err != nil {
		t.Fatalf("GetAuthHeaders() error: %v", err)
	}

	if !refreshCalled {
		t.Error("expected refresh_token request to be made")
	}

	if got := headers.Get("Authorization"); got != "Bearer refreshed-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer refreshed-token")
	}

	stored, _ := credSvc.Get(ctx, "RefreshAgent", "RefreshAgent", "user1", "refresh_test")
	var storedToken OAuthToken
	_ = json.Unmarshal([]byte(stored), &storedToken)
	if storedToken.AccessToken != "refreshed-token" {
		t.Errorf("stored AccessToken = %q", storedToken.AccessToken)
	}
	if storedToken.RefreshToken != "new-refresh-token" {
		t.Errorf("stored RefreshToken = %q", storedToken.RefreshToken)
	}
}

func TestOAuth2MultiUserIsolation(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code := r.Form.Get("code")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-for-" + code,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenSrv.Close()

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	handler := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "multi_user_test",
		Scheme: &OAuthScheme{
			AuthorizationURL:        "https://auth.example.com/authorize",
			TokenURL:                tokenSrv.URL + "/token",
			TokenEndpointAuthMethod: "client_secret_post",
		},
		Credential: &MCPAuthCred{ClientID: "cid", ClientSecret: "cs"},
	}, slog.Default())

	credSvc := newMockCredentialService()
	ctx := context.Background()

	toolCtxA := &tool.Context{
		AgentName: "Agent", UserID: "userA", ClientID: "c",
		CredentialService: credSvc,
	}
	_ = handler.HandleAuthResponse(ctx, &a2a.AuthResponseData{
		Code: "codeA", CodeVerifier: "vA", CredentialKey: "multi_user_test",
	}, toolCtxA)

	toolCtxB := &tool.Context{
		AgentName: "Agent", UserID: "userB", ClientID: "c",
		CredentialService: credSvc,
	}
	_ = handler.HandleAuthResponse(ctx, &a2a.AuthResponseData{
		Code: "codeB", CodeVerifier: "vB", CredentialKey: "multi_user_test",
	}, toolCtxB)

	headersA, _ := handler.GetAuthHeaders(ctx, toolCtxA)
	headersB, _ := handler.GetAuthHeaders(ctx, toolCtxB)

	if got := headersA.Get("Authorization"); got != "Bearer token-for-codeA" {
		t.Errorf("User A Authorization = %q, want %q", got, "Bearer token-for-codeA")
	}
	if got := headersB.Get("Authorization"); got != "Bearer token-for-codeB" {
		t.Errorf("User B Authorization = %q, want %q", got, "Bearer token-for-codeB")
	}
}

func TestAuthSignalWireFormat(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	handler := NewAuthHandler(&MCPAuthConfig{
		Type:          "oauth2",
		CredentialKey: "wire_test",
		Scheme: &OAuthScheme{
			AuthorizationURL: "https://auth.example.com/authorize",
			TokenURL:         "https://auth.example.com/token",
		},
		Credential: &MCPAuthCred{ClientID: "cid"},
	}, slog.Default())

	toolCtx := &tool.Context{
		AgentName:    "MyAgent",
		UserID:       "user1",
		ClientID:     "client1",
		ParentTaskID: "task-1",
		ToolCallID:   "call-1",
	}

	signal, err := handler.BuildAuthSignal(context.Background(), toolCtx)
	if err != nil {
		t.Fatalf("BuildAuthSignal() error: %v", err)
	}

	data, _ := json.Marshal(signal)
	var wireMap map[string]any
	_ = json.Unmarshal(data, &wireMap)

	requiredFields := []string{
		"type", "target_agent", "target_agent_task_id",
		"user_id", "client_id", "auth_uri",
		"auth_request_function_call_id", "credential_key",
		"gateway_task_id", "code_verifier",
	}
	for _, field := range requiredFields {
		if _, ok := wireMap[field]; !ok {
			t.Errorf("missing required field %q in wire format", field)
		}
	}

	if wireMap["gateway_task_id"] != nil {
		t.Errorf("gateway_task_id should be null, got %v", wireMap["gateway_task_id"])
	}

	if wireMap["type"] != "authentication_required" {
		t.Errorf("type = %v", wireMap["type"])
	}
}
