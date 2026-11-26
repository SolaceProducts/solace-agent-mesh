// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func newTestProvider(t *testing.T, fake *fakeOIDCClient) *Provider {
	t.Helper()
	km, err := trust.NewKeyManager("test-gateway")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := trust.NewSigner(km, "test-gateway", time.Hour, 2*time.Hour)

	cfg := &Config{
		ExternalAuthProvider: "azure",
		FrontendServerURL:    "http://localhost:3000",
		SAMAccessToken:       SAMAccessTokenConfig{TTLSeconds: 3600},
		AuthorizationService: AuthorizationServiceConfig{
			Type: "none",
		},
	}

	authzSvc, err := NewAuthorizationService(cfg.AuthorizationService, "gw-1", slog.Default())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	if fake == nil {
		fake = &fakeOIDCClient{}
	}

	return &Provider{
		oidcClient:  fake,
		signer:      signer,
		authzSvc:    authzSvc,
		auditLogger: coreauth.NoopAuditLogger{},
		config:      cfg,
		gatewayID:   "gw-1",
		logger:      slog.Default().With("logger_name", "enterprise-auth"),
	}
}

func TestMiddleware_MissingHeader(t *testing.T) {
	p := newTestProvider(t, nil)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_InvalidFormat(t *testing.T) {
	p := newTestProvider(t, nil)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_InvalidToken(t *testing.T) {
	p := newTestProvider(t, nil)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_ValidSAMToken(t *testing.T) {
	p := newTestProvider(t, nil)
	signer := p.signer

	token, err := MintSAMToken(signer, MintClaims{
		UserID: "alice",
		Email:  "alice@example.com",
		Roles:  []string{"admin"},
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	var gotIdentity *auth.Subject
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.SubjectFromContext(r.Context())
		if ok {
			gotIdentity = id
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotIdentity == nil {
		t.Fatal("expected identity in context")
	}
	if gotIdentity.UserID() != "alice" {
		t.Errorf("UserID = %q", gotIdentity.UserID())
	}
	if !gotIdentity.Authenticated() {
		t.Error("expected Authenticated=true")
	}
	if gotIdentity.AuthMethod() != "sam_access_token" {
		t.Errorf("AuthMethod = %q", gotIdentity.AuthMethod())
	}
	if len(gotIdentity.Scopes()) != 1 || gotIdentity.Scopes()[0] != "*" {
		t.Errorf("Scopes = %v, want [*]", gotIdentity.Scopes())
	}
}

func TestMiddleware_SkipsOptions(t *testing.T) {
	p := newTestProvider(t, nil)

	called := false
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("OPTIONS", "/api/v1/something", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("OPTIONS request should skip auth and call next handler")
	}
}

func TestMiddleware_VersionProbeNeedsNoCredentials(t *testing.T) {
	p := newTestProvider(t, nil)

	called := false
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/version", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (version probe must not require a login)", rec.Code)
	}
	if !called {
		t.Error("expected next handler to be called for the version probe")
	}
}

func TestMiddleware_SoftAuth_MissingHeader(t *testing.T) {
	p := newTestProvider(t, nil)

	called := false
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/share/abcdefghijklmnopqrstu", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (soft auth should pass through)", rec.Code)
	}
	if !called {
		t.Error("expected next handler to be called on soft-auth path")
	}
}

func TestMiddleware_SoftAuth_InvalidToken(t *testing.T) {
	p := newTestProvider(t, nil)

	called := false
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/share/abcdefghijklmnopqrstu", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (soft auth should pass through)", rec.Code)
	}
	if !called {
		t.Error("expected next handler to be called on soft-auth path with invalid token")
	}
}

func TestHandleCallback_MissingCode(t *testing.T) {
	p := newTestProvider(t, nil)

	req := httptest.NewRequest("GET", "/api/v1/auth/callback", nil)
	rec := httptest.NewRecorder()
	p.handleCallback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCallback_Success(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo: &UserInfo{
			Sub:   "user-123",
			Email: "user@example.com",
			Name:  "Test User",
			RawResponse: map[string]any{
				"sub":           "user-123",
				"email":         "user@example.com",
				"name":          "Test User",
				"oidc_provider": "azure",
			},
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "idp-at",
			RefreshToken: "idp-rt",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	}

	p := newTestProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000"

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code-123", nil)
	rec := httptest.NewRecorder()
	p.handleCallback(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}

	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "http://localhost:3000#") {
		t.Errorf("redirect location = %q, want prefix http://localhost:3000#", location)
	}
	if !strings.Contains(location, "sam_access_token=") {
		t.Error("redirect should contain sam_access_token")
	}
	if !strings.Contains(location, "access_token=idp-at") {
		t.Error("redirect should contain idp access_token")
	}
}

func TestHandleCallback_ExchangeFailure(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=bad-code", nil)
	rec := httptest.NewRecorder()
	p.handleCallback(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestHandleRefresh_MissingBody(t *testing.T) {
	p := newTestProvider(t, nil)

	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleRefresh_Success(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "refreshed-at",
		userInfo: &UserInfo{
			Sub:   "user-123",
			Email: "user@example.com",
			Name:  "Test User",
			RawResponse: map[string]any{
				"sub":   "user-123",
				"email": "user@example.com",
				"name":  "Test User",
			},
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "refreshed-at",
			RefreshToken: "refreshed-rt",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	}
	p := newTestProvider(t, fake)

	body := `{"refresh_token":"old-rt"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["access_token"] != "refreshed-at" {
		t.Errorf("access_token = %v", resp["access_token"])
	}
	if resp["sam_access_token"] == nil || resp["sam_access_token"] == "" {
		t.Error("expected sam_access_token in response")
	}
	if resp["expires_in"] != float64(3600) {
		t.Errorf("expires_in = %v, want 3600", resp["expires_in"])
	}
}

func TestHandleRefresh_Failure(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})

	body := `{"refresh_token":"bad-rt"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestHandleRefresh_BrowserCancelled_WritesNoErrorResponseAndLogsNoError(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	p := newTestProvider(t, &fakeOIDCClient{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, "POST", "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"rt"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)

	if rec.Body.Len() != 0 {
		t.Errorf("wrote an error response to a browser that already went away: %q", rec.Body.String())
	}
	if strings.Contains(logged.String(), "level=ERROR") {
		t.Errorf("a browser-cancelled refresh logged at ERROR: %s", logged.String())
	}
}

func TestHandleLogout(t *testing.T) {
	p := newTestProvider(t, nil)

	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	p.handleLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestNewProvider_NilSigner(t *testing.T) {
	_, err := NewProvider(&Config{
		AuthorizationService: AuthorizationServiceConfig{Type: "none"},
	}, nil, "gw-1", nil, slog.Default())
	if err == nil {
		t.Error("expected error for nil signer")
	}
}

type rejectingIDTokenVerifier struct{}

func (rejectingIDTokenVerifier) VerifyIDToken(_ context.Context, idToken string) (map[string]any, error) {
	if idToken == "" {
		return nil, nil
	}
	return nil, &IDTokenVerificationError{Reason: IDTokenFailAudience}
}

func TestHandleCallback_RejectsBadIDTokenWithoutUserinfoFallback(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo: &UserInfo{
			Sub:   "user-123",
			Email: "user@example.com",
			Name:  "Test User",
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "idp-at",
			RefreshToken: "idp-rt",
			IDToken:      "header.payload.sig",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	}
	p := newTestProvider(t, fake)
	p.idTokenVerifier = rejectingIDTokenVerifier{}

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code-xyz", nil)
	rec := httptest.NewRecorder()
	p.handleCallback(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (id_token verification must reject the login)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "invalid_token") {
		t.Errorf("response body missing invalid_token error_type: %s", body)
	}
	if !strings.Contains(body, "id_token verification failed") {
		t.Errorf("response body missing id_token verification failed detail: %s", body)
	}
}

func TestHandleRefresh_RejectsBadIDToken(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "refreshed-at",
		userInfo: &UserInfo{
			Sub:   "user-123",
			Email: "user@example.com",
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "refreshed-at",
			RefreshToken: "refreshed-rt",
			IDToken:      "header.payload.sig",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	}
	p := newTestProvider(t, fake)
	p.idTokenVerifier = rejectingIDTokenVerifier{}

	body := `{"refresh_token":"old-rt"}`
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestProviderType(t *testing.T) {
	p := newTestProvider(t, nil)
	if got := p.ProviderType(); got != "enterprise" {
		t.Errorf("ProviderType() = %q, want %q", got, "enterprise")
	}
}

func TestHandleLocalCallback_LogsFlowWebUILogin(t *testing.T) {
	var buf bytes.Buffer
	fakeLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p := newTestProvider(t, &fakeOIDCClient{})
	p.logger = fakeLogger

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=bad-code", nil)
	w := httptest.NewRecorder()
	p.handleLocalCallback(w, req)

	if !strings.Contains(buf.String(), "flow=webui_login") {
		t.Errorf("expected flow=webui_login label in logs, got: %s", buf.String())
	}
}

type fakeStateDispatcher struct {
	called bool
}

func (f *fakeStateDispatcher) Handle(w http.ResponseWriter, r *http.Request) {
	f.called = true
	w.WriteHeader(http.StatusOK)
}

func TestHandleCallback_Demux_JWSShape_RoutesToDispatcher(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})
	fake := &fakeStateDispatcher{}
	p.SetStateDispatcher(fake)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=X&state=eyJ.payload.sig", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)
	if !fake.called {
		t.Error("expected dispatcher.Handle to be called for JWS-shaped state")
	}
}

func TestHandleCallback_Demux_OpaqueState_RoutesToLocal(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})
	fake := &fakeStateDispatcher{}
	p.SetStateDispatcher(fake)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=X&state=randomopaque", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)
	if fake.called {
		t.Error("dispatcher.Handle should NOT be called for opaque state")
	}
}

func TestHandleCallback_Demux_NilDispatcher_AlwaysRoutesLocal(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=X&state=eyJ.payload.sig", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)
	_ = w.Result()
}

func TestHandleCallback_Demux_EYJPrefixOneDot_RoutesToLocal(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})
	fake := &fakeStateDispatcher{}
	p.SetStateDispatcher(fake)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=X&state=eyJfoo.bar", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)
	if fake.called {
		t.Error("dispatcher.Handle should NOT be called for eyJ-prefixed state with one dot")
	}
}

func TestHandleCallback_Demux_EYJPrefixThreeDots_RoutesToLocal(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})
	fake := &fakeStateDispatcher{}
	p.SetStateDispatcher(fake)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=X&state=eyJa.b.c.d", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)
	if fake.called {
		t.Error("dispatcher.Handle should NOT be called for eyJ-prefixed state with three dots (JWE-shaped)")
	}
}

func TestHandleCallback_Demux_EmptyState_RoutesToLocal(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})
	fake := &fakeStateDispatcher{}
	p.SetStateDispatcher(fake)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=X", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)
	if fake.called {
		t.Error("dispatcher.Handle should NOT be called for empty state")
	}
}
