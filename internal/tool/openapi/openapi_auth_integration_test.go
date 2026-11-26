// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func rsaKeyGen() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

func marshalPKCS8(key *rsa.PrivateKey) ([]byte, error) {
	return x509.MarshalPKCS8PrivateKey(key)
}

type memCredSvc struct {
	store map[string]string
}

func newMemCredSvc() *memCredSvc {
	return &memCredSvc{store: make(map[string]string)}
}

func (m *memCredSvc) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	k := agentName + "|" + appName + "|" + userID + "|" + key
	return m.store[k], nil
}

func (m *memCredSvc) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	k := agentName + "|" + appName + "|" + userID + "|" + key
	m.store[k] = value
	return nil
}

func (m *memCredSvc) Delete(_ context.Context, agentName, appName, userID, key string) error {
	k := agentName + "|" + appName + "|" + userID + "|" + key
	delete(m.store, k)
	return nil
}

func (m *memCredSvc) List(_ context.Context, agentName, appName, userID string) ([]string, error) {
	prefix := agentName + "|" + appName + "|" + userID + "|"
	var keys []string
	for k := range m.store {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, strings.TrimPrefix(k, prefix))
		}
	}
	return keys, nil
}

func makeToolCtx(credSvc *memCredSvc) *tool.Context {
	return &tool.Context{
		AgentName:         "TestAgent",
		UserID:            "user-1",
		ClientID:          "client-1",
		ParentTaskID:      "task-1",
		ToolCallID:        "call-1",
		CredentialService: credSvc,
	}
}

func TestNewAuthHandlerForOpenAPI_StaticPathsReturnNil(t *testing.T) {
	for _, authType := range []AuthType{AuthTypeBearer, AuthTypeAPIKey, AuthTypeBasic, AuthTypeUnset} {
		t.Run(string(authType), func(t *testing.T) {
			h, err := NewAuthHandlerForOpenAPI(&AuthConfig{Type: authType}, slog.Default())
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if h != nil {
				t.Errorf("expected nil handler for static auth type %q, got %+v", authType, h)
			}
		})
	}
}

func TestNewAuthHandlerForOpenAPI_AuthorizationCode_ConstructsInner(t *testing.T) {
	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:             AuthTypeOAuth2AuthorizationCode,
		AuthorizationURL: "https://idp.example.com/authorize",
		TokenURL:         "https://idp.example.com/token",
		ClientID:         "cid",
		ClientSecret:     "csecret",
		Scopes:           []string{"read"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if h == nil || h.inner == nil {
		t.Fatal("expected non-nil handler with inner toolauth")
	}
	if h.CredentialKey() == "" {
		t.Error("CredentialKey must default to something when not set")
	}
}

func TestNewAuthHandlerForOpenAPI_AuthorizationCode_ConsentPrompt(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")
	cases := []struct {
		name                 string
		disableConsentPrompt bool
		wantPrompt           string
	}{
		{"default sends prompt=consent", false, "consent"},
		{"disable_consent_prompt omits prompt", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
				Type:                 AuthTypeOAuth2AuthorizationCode,
				AuthorizationURL:     "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize",
				TokenURL:             "https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
				ClientID:             "cid",
				ClientSecret:         "csecret",
				Scopes:               []string{"offline_access"},
				DisableConsentPrompt: c.disableConsentPrompt,
			}, slog.Default())
			if err != nil {
				t.Fatalf("NewAuthHandlerForOpenAPI: %v", err)
			}

			signal, err := h.BuildAuthSignal(context.Background(), makeToolCtx(newMemCredSvc()))
			if err != nil {
				t.Fatalf("BuildAuthSignal: %v", err)
			}
			u, err := url.Parse(signal.AuthURI)
			if err != nil {
				t.Fatalf("parse AuthURI: %v", err)
			}
			if got := u.Query().Get("prompt"); got != c.wantPrompt {
				t.Errorf("prompt = %q, want %q", got, c.wantPrompt)
			}
		})
	}
}

func TestNewAuthHandlerForOpenAPI_ClientCredentials_NoInner(t *testing.T) {
	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:         AuthTypeOAuth2ClientCredentials,
		TokenURL:     "https://idp.example.com/token",
		ClientID:     "svc",
		ClientSecret: "ssecret",
	}, slog.Default())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.inner != nil {
		t.Error("client_credentials should not have an inner redirect handler")
	}
	if h.authType != AuthTypeOAuth2ClientCredentials {
		t.Errorf("authType = %q", h.authType)
	}
}

func TestNewAuthHandlerForOpenAPI_ClientCredentials_DefaultsToBasic(t *testing.T) {
	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:         AuthTypeOAuth2ClientCredentials,
		TokenURL:     "https://idp.example.com/token",
		ClientID:     "svc",
		ClientSecret: "ssecret",
	}, slog.Default())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.tokenEndpointAuthMethod != "client_secret_basic" {
		t.Errorf("tokenEndpointAuthMethod = %q, want client_secret_basic",
			h.tokenEndpointAuthMethod)
	}
}

func TestNewAuthHandlerForOpenAPI_ClientCredentials_ExplicitMethodWins(t *testing.T) {
	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:                    AuthTypeOAuth2ClientCredentials,
		TokenURL:                "https://idp.example.com/token",
		ClientID:                "svc",
		ClientSecret:            "ssecret",
		TokenEndpointAuthMethod: "client_secret_post",
	}, slog.Default())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.tokenEndpointAuthMethod != "client_secret_post" {
		t.Errorf("tokenEndpointAuthMethod = %q, want client_secret_post",
			h.tokenEndpointAuthMethod)
	}
}

func TestNewAuthHandlerForOpenAPI_ServiceAccount_NoInner(t *testing.T) {
	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:               AuthTypeServiceAccount,
		ServiceAccountJSON: `{"type":"service_account"}`,
		Scopes:             []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
	if h.authType != AuthTypeServiceAccount {
		t.Errorf("authType = %q", h.authType)
	}
}

func TestOpenAPIAuth_ClientCredentials_EndToEnd(t *testing.T) {
	var tokenRequests int
	var apiRequests int
	var seenAPIToken string

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" {
			http.Error(w, "unsupported_grant_type", http.StatusBadRequest)
			return
		}
		if r.Form.Get("client_id") != "svc-cid" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "cc-token-xyz",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiRequests++
		seenAPIToken = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer apiSrv.Close()

	spec := map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"servers": []any{map[string]any{"url": apiSrv.URL}},
		"paths": map[string]any{
			"/items": map[string]any{
				"get": map[string]any{
					"operationId": "listItems",
					"responses": map[string]any{
						"200": map[string]any{"description": "ok"},
					},
				},
			},
		},
	}
	specBytes, _ := json.Marshal(spec)
	cfg := &ToolsetConfig{
		SpecInline: string(specBytes),
		SpecFormat: "json",
		Auth: &AuthConfig{
			Type:                    AuthTypeOAuth2ClientCredentials,
			TokenURL:                tokenSrv.URL + "/token",
			ClientID:                "svc-cid",
			ClientSecret:            "svc-csecret",
			Scopes:                  []string{"api.read"},
			TokenEndpointAuthMethod: "client_secret_post",
		},
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tl := tools[0]
	if tl.AuthHandler() == nil {
		t.Fatal("AuthHandler() should be non-nil for client_credentials")
	}

	credSvc := newMemCredSvc()
	ctx := context.Background()
	toolCtx := makeToolCtx(credSvc)

	result, err := tl.Execute(ctx, nil, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("unexpected status %q: %s", result.Status, result.Message)
	}
	if tokenRequests != 1 {
		t.Errorf("tokenRequests = %d, want 1 (exchange on first call)", tokenRequests)
	}
	if apiRequests != 1 {
		t.Errorf("apiRequests = %d, want 1", apiRequests)
	}
	if seenAPIToken != "Bearer cc-token-xyz" {
		t.Errorf("API Authorization header = %q", seenAPIToken)
	}

	result, err = tl.Execute(ctx, nil, toolCtx)
	if err != nil {
		t.Fatalf("Execute #2: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status #2 = %q: %s", result.Status, result.Message)
	}
	if tokenRequests != 1 {
		t.Errorf("tokenRequests = %d, want 1 (second call should reuse cache)", tokenRequests)
	}
	if apiRequests != 2 {
		t.Errorf("apiRequests = %d, want 2", apiRequests)
	}
}

func TestOpenAPIAuth_ClientCredentials_RejectedByServer_SurfacesToolError(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer tokenSrv.Close()

	spec := `{
  "openapi": "3.0.0",
  "info": {"title":"t","version":"1"},
  "servers": [{"url":"http://unreachable.invalid"}],
  "paths": {"/a": {"get": {"operationId":"opA","responses":{"200":{"description":"ok"}}}}}
}`
	cfg := &ToolsetConfig{
		SpecInline: spec,
		SpecFormat: "json",
		Auth: &AuthConfig{
			Type:         AuthTypeOAuth2ClientCredentials,
			TokenURL:     tokenSrv.URL + "/token",
			ClientID:     "cid",
			ClientSecret: "wrong",
		},
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	credSvc := newMemCredSvc()
	result, err := tools[0].Execute(context.Background(), nil, makeToolCtx(credSvc))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "invalid_client") && !strings.Contains(result.Message, "client_credentials") {
		t.Errorf("expected error hinting at invalid_client, got %q", result.Message)
	}
}

func TestOpenAPIAuth_AuthorizationCode_FirstTime_TriggersAuthRequired(t *testing.T) {
	var apiRequests int
	apiSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		apiRequests++
	}))
	defer apiSrv.Close()

	spec := map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"servers": []any{map[string]any{"url": apiSrv.URL}},
		"paths": map[string]any{
			"/items": map[string]any{
				"get": map[string]any{
					"operationId": "listItems",
					"responses":   map[string]any{"200": map[string]any{"description": "ok"}},
				},
			},
		},
	}
	specBytes, _ := json.Marshal(spec)

	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")

	cfg := &ToolsetConfig{
		SpecInline: string(specBytes),
		SpecFormat: "json",
		Auth: &AuthConfig{
			Type:             AuthTypeOAuth2AuthorizationCode,
			AuthorizationURL: "https://idp.example.com/authorize",
			TokenURL:         "https://idp.example.com/token",
			ClientID:         "user-cid",
			ClientSecret:     "user-csecret",
			Scopes:           []string{"read"},
		},
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	credSvc := newMemCredSvc()
	toolCtx := makeToolCtx(credSvc)

	var sentSignal *a2a.AuthenticationRequiredData
	toolCtx.SendSignal = func(sig any) error {
		sentSignal = sig.(*a2a.AuthenticationRequiredData)
		return nil
	}

	result, err := tools[0].Execute(context.Background(), nil, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusPending {
		t.Fatalf("status = %q, want pending (auth-required pause)", result.Status)
	}
	if !toolauth.IsPendingAuthResult(result) {
		t.Errorf("expected pending-auth result; dispatcher will not pause without it")
	}
	if apiRequests != 0 {
		t.Errorf("apiRequests = %d, want 0 (must short-circuit before calling API)", apiRequests)
	}
	if sentSignal == nil {
		t.Fatal("expected SendSignal to be called with authentication_required")
	}
	if sentSignal.Type != string(a2a.SignalAuthenticationRequired) {
		t.Errorf("signal.Type = %q", sentSignal.Type)
	}
	if sentSignal.TargetAgent != "TestAgent" {
		t.Errorf("signal.TargetAgent = %q", sentSignal.TargetAgent)
	}
	u, parseErr := url.Parse(sentSignal.AuthURI)
	if parseErr != nil {
		t.Fatalf("parse auth URI: %v", parseErr)
	}
	q := u.Query()
	if q.Get("client_id") != "user-cid" {
		t.Errorf("auth URI client_id = %q", q.Get("client_id"))
	}
	if q.Get("state") == "" {
		t.Error("auth URI must include a state parameter")
	}
}

func TestOpenAPIAuth_AuthorizationCode_WithValidToken_InjectsHeader(t *testing.T) {
	var apiRequests int
	var seenAuth string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiRequests++
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer apiSrv.Close()

	spec := map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"servers": []any{map[string]any{"url": apiSrv.URL}},
		"paths": map[string]any{
			"/x": map[string]any{
				"get": map[string]any{
					"operationId": "getX",
					"responses":   map[string]any{"200": map[string]any{"description": "ok"}},
				},
			},
		},
	}
	specBytes, _ := json.Marshal(spec)

	cfg := &ToolsetConfig{
		SpecInline: string(specBytes),
		SpecFormat: "json",
		Auth: &AuthConfig{
			Type:             AuthTypeOAuth2AuthorizationCode,
			AuthorizationURL: "https://idp.example.com/authorize",
			TokenURL:         "https://idp.example.com/token",
			ClientID:         "cid",
			ClientSecret:     "csecret",
			CredentialKey:    "openapi_oauth2",
		},
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tl := tools[0]

	credSvc := newMemCredSvc()
	stored := toolauth.OAuthToken{
		AccessToken:  "already-have-this-token",
		RefreshToken: "rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}
	raw, _ := json.Marshal(stored)
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user-1", "openapi_oauth2", string(raw))

	result, err := tl.Execute(context.Background(), nil, makeToolCtx(credSvc))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("unexpected status %q: %s", result.Status, result.Message)
	}
	if apiRequests != 1 {
		t.Errorf("apiRequests = %d, want 1", apiRequests)
	}
	if seenAuth != "Bearer already-have-this-token" {
		t.Errorf("Authorization = %q", seenAuth)
	}
}

func TestOpenAPIAuth_AuthorizationCode_RefreshOnExpiry_ReusesRefreshToken(t *testing.T) {
	var refreshCount int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "unexpected_grant", http.StatusBadRequest)
			return
		}
		if r.Form.Get("refresh_token") != "stored-rt" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		refreshCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-access-token",
			"refresh_token": "new-rt",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	var seenAuth string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer apiSrv.Close()

	spec := map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"servers": []any{map[string]any{"url": apiSrv.URL}},
		"paths": map[string]any{
			"/p": map[string]any{
				"get": map[string]any{
					"operationId": "getP",
					"responses":   map[string]any{"200": map[string]any{"description": "ok"}},
				},
			},
		},
	}
	specBytes, _ := json.Marshal(spec)

	cfg := &ToolsetConfig{
		SpecInline: string(specBytes),
		SpecFormat: "json",
		Auth: &AuthConfig{
			Type:                    AuthTypeOAuth2AuthorizationCode,
			AuthorizationURL:        "https://idp.example.com/authorize",
			TokenURL:                tokenSrv.URL + "/token",
			ClientID:                "cid",
			ClientSecret:            "csecret",
			TokenEndpointAuthMethod: "client_secret_post",
			CredentialKey:           "oaac_refresh",
		},
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	credSvc := newMemCredSvc()
	stored := toolauth.OAuthToken{
		AccessToken:  "stale-at",
		RefreshToken: "stored-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-30 * time.Second).Unix(),
	}
	raw, _ := json.Marshal(stored)
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user-1", "oaac_refresh", string(raw))

	result, err := tools[0].Execute(context.Background(), nil, makeToolCtx(credSvc))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q: %s", result.Status, result.Message)
	}
	if refreshCount != 1 {
		t.Errorf("refreshCount = %d, want 1", refreshCount)
	}
	if seenAuth != "Bearer refreshed-access-token" {
		t.Errorf("Authorization = %q", seenAuth)
	}
}

func TestOpenAPIAuth_AuthorizationCode_RefreshDuringProviderOutage_KeepsCredential(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
	}))
	defer tokenSrv.Close()

	apiRequests := 0
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer apiSrv.Close()

	spec := map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"servers": []any{map[string]any{"url": apiSrv.URL}},
		"paths": map[string]any{
			"/p": map[string]any{
				"get": map[string]any{
					"operationId": "getP",
					"responses":   map[string]any{"200": map[string]any{"description": "ok"}},
				},
			},
		},
	}
	specBytes, _ := json.Marshal(spec)

	cfg := &ToolsetConfig{
		SpecInline: string(specBytes),
		SpecFormat: "json",
		Auth: &AuthConfig{
			Type:                    AuthTypeOAuth2AuthorizationCode,
			AuthorizationURL:        "https://idp.example.com/authorize",
			TokenURL:                tokenSrv.URL + "/token",
			ClientID:                "cid",
			ClientSecret:            "csecret",
			TokenEndpointAuthMethod: "client_secret_post",
			CredentialKey:           "oaac_outage",
		},
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	credSvc := newMemCredSvc()
	raw, _ := json.Marshal(toolauth.OAuthToken{
		AccessToken:  "stale-at",
		RefreshToken: "stored-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-30 * time.Second).Unix(),
	})
	_ = credSvc.Set(context.Background(), "TestAgent", "TestAgent", "user-1", "oaac_outage", string(raw))

	toolCtx := makeToolCtx(credSvc)
	signalled := false
	toolCtx.SendSignal = func(any) error { signalled = true; return nil }

	result, err := tools[0].Execute(context.Background(), nil, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want %q: %s", result.Status, tool.StatusError, result.Message)
	}
	if want := tool.HTTPFailureCode(http.StatusServiceUnavailable); result.ErrorCode != want {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, want)
	}
	if signalled {
		t.Error("an outage at the sign-in provider must not prompt the user to sign in")
	}
	if apiRequests != 0 {
		t.Errorf("apiRequests = %d, want 0", apiRequests)
	}
	got, _ := credSvc.Get(context.Background(), "TestAgent", "TestAgent", "user-1", "oaac_outage")
	var kept toolauth.OAuthToken
	_ = json.Unmarshal([]byte(got), &kept)
	if kept.RefreshToken != "stored-rt" {
		t.Errorf("stored RefreshToken = %q, want the credential kept through the outage", kept.RefreshToken)
	}
}

func TestOpenAPIAuth_AuthorizationCode_HandleAuthResponse_StoresToken(t *testing.T) {
	var exchangeCount int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" {
			http.Error(w, "grant", http.StatusBadRequest)
			return
		}
		if r.Form.Get("code") != "user-granted-code" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		exchangeCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ex-at",
			"refresh_token": "ex-rt",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}))
	defer tokenSrv.Close()

	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:                    AuthTypeOAuth2AuthorizationCode,
		AuthorizationURL:        "https://idp.example.com/authorize",
		TokenURL:                tokenSrv.URL + "/token",
		ClientID:                "cid",
		ClientSecret:            "csecret",
		TokenEndpointAuthMethod: "client_secret_post",
		CredentialKey:           "oaac_exchange",
	}, slog.Default())
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	credSvc := newMemCredSvc()
	toolCtx := makeToolCtx(credSvc)
	ctx := context.Background()

	if _, err := h.BuildAuthSignal(ctx, toolCtx); err != nil {
		t.Fatalf("BuildAuthSignal: %v", err)
	}

	pendingRaw, _ := credSvc.Get(ctx, "TestAgent", "TestAgent", "user-1", "oaac_exchange.pending")
	var pending struct {
		CodeVerifier string `json:"code_verifier"`
		State        string `json:"state"`
	}
	_ = json.Unmarshal([]byte(pendingRaw), &pending)

	resp := &a2a.AuthResponseData{
		Type:          "auth_response",
		CredentialKey: "oaac_exchange",
		Code:          "user-granted-code",
		State:         pending.State,
		CodeVerifier:  pending.CodeVerifier,
	}
	if err := h.HandleAuthResponse(ctx, resp, toolCtx); err != nil {
		t.Fatalf("HandleAuthResponse: %v", err)
	}
	if exchangeCount != 1 {
		t.Errorf("exchangeCount = %d", exchangeCount)
	}

	stored, _ := credSvc.Get(ctx, "TestAgent", "TestAgent", "user-1", "oaac_exchange")
	if stored == "" {
		t.Fatal("token should have been persisted")
	}
	var tok toolauth.OAuthToken
	if err := json.Unmarshal([]byte(stored), &tok); err != nil {
		t.Fatalf("unmarshal stored token: %v", err)
	}
	if tok.AccessToken != "ex-at" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
}

func TestOpenAPIAuth_ServiceAccount_BadJSON_FailsFast(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer apiSrv.Close()

	spec := `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"servers":[{"url":"` + apiSrv.URL + `"}],"paths":{"/x":{"get":{"operationId":"opX","responses":{"200":{"description":"ok"}}}}}}`
	cfg := &ToolsetConfig{
		SpecInline: spec,
		SpecFormat: "json",
		Auth: &AuthConfig{
			Type:               AuthTypeServiceAccount,
			ServiceAccountJSON: `{"not":"a real service account"}`,
			Scopes:             []string{"https://www.googleapis.com/auth/cloud-platform"},
		},
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	result, err := tools[0].Execute(context.Background(), nil, makeToolCtx(newMemCredSvc()))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("expected error, got %q: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "serviceaccount") {
		t.Errorf("expected error to mention serviceaccount, got %q", result.Message)
	}
}

func TestOpenAPIAuth_ServiceAccount_ValidJSON_ExchangesAndCalls(t *testing.T) {
	saJSON, saKey := mintServiceAccountJSONForTest(t)

	var exchangeCount int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchangeCount++
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			http.Error(w, "bad grant: "+r.Form.Get("grant_type"), http.StatusBadRequest)
			return
		}
		if r.Form.Get("assertion") == "" {
			http.Error(w, "missing assertion", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"sa-access-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	var sa map[string]any
	_ = json.Unmarshal([]byte(saJSON), &sa)
	sa["token_uri"] = tokenSrv.URL + "/token"
	rewritten, _ := json.Marshal(sa)

	var seenAuth string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer apiSrv.Close()

	spec := `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"servers":[{"url":"` + apiSrv.URL + `"}],"paths":{"/q":{"get":{"operationId":"getQ","responses":{"200":{"description":"ok"}}}}}}`
	cfg := &ToolsetConfig{
		SpecInline: spec,
		SpecFormat: "json",
		Auth: &AuthConfig{
			Type:               AuthTypeServiceAccount,
			ServiceAccountJSON: string(rewritten),
			Scopes:             []string{"https://www.googleapis.com/auth/cloud-platform"},
		},
	}
	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	credSvc := newMemCredSvc()
	result, err := tools[0].Execute(context.Background(), nil, makeToolCtx(credSvc))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q: %s", result.Status, result.Message)
	}
	if exchangeCount != 1 {
		t.Errorf("exchangeCount = %d, want 1", exchangeCount)
	}
	if seenAuth != "Bearer sa-access-token" {
		t.Errorf("Authorization = %q", seenAuth)
	}

	_, _ = tools[0].Execute(context.Background(), nil, makeToolCtx(credSvc))
	if exchangeCount != 1 {
		t.Errorf("exchangeCount = %d after second call, want 1 (reuse cache)", exchangeCount)
	}
	_ = saKey
}

func mintServiceAccountJSONForTest(t *testing.T) (string, []byte) {
	t.Helper()
	pemBlock := generateRSAKeyPEMForTest(t)
	blob := map[string]any{
		"type":         "service_account",
		"project_id":   "test-project",
		"private_key":  string(pemBlock),
		"client_email": "svc@test-project.iam.gserviceaccount.com",
		"token_uri":    "https://oauth2.googleapis.com/token",
	}
	out, err := json.Marshal(blob)
	if err != nil {
		t.Fatalf("marshal sa json: %v", err)
	}
	return string(out), pemBlock
}

func generateRSAKeyPEMForTest(t *testing.T) []byte {
	t.Helper()
	key, err := rsaKeyGen()
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	der, err := marshalPKCS8(key)
	if err != nil {
		t.Fatalf("pkcs8: %v", err)
	}
	pem := "-----BEGIN PRIVATE KEY-----\n" +
		base64.StdEncoding.EncodeToString(der) + "\n" +
		"-----END PRIVATE KEY-----\n"
	return []byte(pem)
}
