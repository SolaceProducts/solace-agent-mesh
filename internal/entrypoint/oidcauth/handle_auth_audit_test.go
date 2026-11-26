// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type recordingAuditLogger struct {
	mu     sync.Mutex
	events []coreauth.AuthEvent
}

func (r *recordingAuditLogger) LogAuth(_ context.Context, ev coreauth.AuthEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingAuditLogger) LogToolAccess(_ context.Context, _ coreauth.ToolAccessEvent)       {}
func (r *recordingAuditLogger) LogAgentAccess(_ context.Context, _ coreauth.AgentAccessEvent)     {}
func (r *recordingAuditLogger) LogToolExecution(_ context.Context, _ coreauth.ToolExecutionEvent) {}
func (r *recordingAuditLogger) LogControlAccess(_ context.Context, _ coreauth.ControlAccessEvent) {}
func (r *recordingAuditLogger) LogShareGrant(_ context.Context, _ coreauth.ShareGrantEvent)       {}
func (r *recordingAuditLogger) LogShareLevelChange(_ context.Context, _ coreauth.ShareLevelChangeEvent) {
}
func (r *recordingAuditLogger) LogShareRevoke(_ context.Context, _ coreauth.ShareRevokeEvent) {}
func (r *recordingAuditLogger) LogTemplateDeleted(_ context.Context, _ coreauth.TemplateDeletedEvent) {
}

func (r *recordingAuditLogger) LogActivationChange(_ context.Context, _ coreauth.ActivationChangeEvent) {
}
func (r *recordingAuditLogger) LogConsentChange(_ context.Context, _ coreauth.ConsentChangeEvent) {
}

func (r *recordingAuditLogger) snapshot() []coreauth.AuthEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]coreauth.AuthEvent, len(r.events))
	copy(out, r.events)
	return out
}

func newAuditingProvider(t *testing.T, fake *fakeOIDCClient) (*Provider, *recordingAuditLogger) {
	t.Helper()
	rec := &recordingAuditLogger{}
	p := newTestProvider(t, fake)
	p.auditLogger = rec
	return p, rec
}

func assertSingleAuthEvent(t *testing.T, events []coreauth.AuthEvent, wantSuccess bool, wantReason string) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("LogAuth events = %d, want exactly 1: %+v", len(events), events)
	}
	got := events[0]
	if got.Success != wantSuccess {
		t.Errorf("event.Success = %v, want %v", got.Success, wantSuccess)
	}
	if got.Reason != wantReason {
		t.Errorf("event.Reason = %q, want %q", got.Reason, wantReason)
	}
}

func assertNoAuthEvents(t *testing.T, events []coreauth.AuthEvent) {
	t.Helper()
	if len(events) != 0 {
		t.Fatalf("LogAuth events = %d, want 0: %+v", len(events), events)
	}
}

func TestOIDCCallback_Success_EmitsLogAuthLogin(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo: &UserInfo{
			Sub:   "user-123",
			Email: "alice@example.com",
			Name:  "Alice",
			RawResponse: map[string]any{
				"sub":   "user-123",
				"email": "alice@example.com",
				"name":  "Alice",
			},
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "idp-at",
			RefreshToken: "idp-rt",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	}
	p, rec := newAuditingProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000"

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	rec2 := httptest.NewRecorder()
	p.handleCallback(rec2, req)

	if rec2.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec2.Code)
	}

	events := rec.snapshot()
	assertSingleAuthEvent(t, events, true, authReasonOIDCLogin)
	if events[0].UserID != "alice@example.com" {
		t.Errorf("UserID = %q, want %q", events[0].UserID, "alice@example.com")
	}
}

func TestOIDCCallback_BadState_EmitsLogAuthFailure(t *testing.T) {
	fake := &fakeOIDCClient{validateState: true}
	p, rec := newAuditingProvider(t, fake)

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code&state=tampered", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonOIDCStateMismatch)
}

func TestOIDCCallback_StateMismatch_EmitsLogAuthFailure(t *testing.T) {
	fake := &fakeOIDCClient{validateState: true}
	p, rec := newAuditingProvider(t, fake)

	w := httptest.NewRecorder()
	setOIDCStateCookie(w, "expected-state", p.config.SessionSecretKey, false)
	cookie := w.Result().Cookies()[0]

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code&state=wrong-state", nil)
	req.AddCookie(cookie)

	w2 := httptest.NewRecorder()
	p.handleCallback(w2, req)

	if w2.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w2.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonOIDCStateMismatch)
}

func TestOIDCCallback_CodeExchangeFailure_EmitsLogAuthFailure(t *testing.T) {
	p, rec := newAuditingProvider(t, &fakeOIDCClient{})

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=bad-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonOIDCCodeExchangeFail)
}

func TestOIDCCallback_UserinfoFailure_EmitsLogAuthFailure(t *testing.T) {
	fake := &fakeOIDCClient{
		tokenResponse: &TokenResponse{
			AccessToken:  "idp-at",
			RefreshToken: "idp-rt",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	}
	p, rec := newAuditingProvider(t, fake)

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonOIDCUserinfoFail)
}

func TestOIDCCallback_SAMMintFailure_EmitsLogAuthFailure(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo: &UserInfo{
			Sub:   "unknown",
			Email: "unknown",
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "idp-at",
			RefreshToken: "idp-rt",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	}
	p, rec := newAuditingProvider(t, fake)

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonOIDCSAMMintFail)
}

func TestOIDCCallback_IDTokenVerifyFailure_EmitsLogAuthFailure(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo:      &UserInfo{Sub: "user-123", Email: "user@example.com"},
		tokenResponse: &TokenResponse{
			AccessToken:  "idp-at",
			RefreshToken: "idp-rt",
			IDToken:      "header.payload.sig",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	}
	p, rec := newAuditingProvider(t, fake)
	p.idTokenVerifier = rejectingIDTokenVerifier{}

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code-xyz", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonOIDCIDTokenFail)
}

func TestMiddleware_MissingToken_EmitsLogAuth(t *testing.T) {
	p, rec := newAuditingProvider(t, nil)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("downstream handler must not be invoked on a 401 reject")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonMissingToken)
}

func TestMiddleware_InvalidTokenFormat_EmitsLogAuth(t *testing.T) {
	p, rec := newAuditingProvider(t, nil)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("downstream handler must not be invoked on a 401 reject")
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonInvalidToken)
}

func TestMiddleware_ExpiredToken_EmitsLogAuthExpired(t *testing.T) {
	p, rec := newAuditingProvider(t, &fakeOIDCClient{})

	expiredToken, err := MintSAMToken(p.signer, MintClaims{UserID: "alice"}, time.Millisecond)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if _, vErr := ValidateSAMToken(expiredToken, p.signer); vErr == nil || !errors.Is(vErr, jwt.ErrTokenExpired) {
		t.Fatalf("setup: expected wrapped jwt.ErrTokenExpired, got %v", vErr)
	}

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("downstream handler must not be invoked on a 401 reject")
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Bearer "+expiredToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonExpiredToken)
}

func TestMiddleware_BadSAMToken_EmitsLogAuthInvalid(t *testing.T) {
	p, rec := newAuditingProvider(t, &fakeOIDCClient{})

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("downstream handler must not be invoked on a 401 reject")
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), false, authReasonInvalidToken)
}

func TestMiddleware_ValidToken_NoLogAuth(t *testing.T) {
	p, rec := newAuditingProvider(t, nil)

	token, err := MintSAMToken(p.signer, MintClaims{
		UserID: "alice",
		Email:  "alice@example.com",
		Roles:  []string{"admin"},
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	called := false
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !called {
		t.Fatalf("expected 200 with downstream invoked, got code=%d called=%v", w.Code, called)
	}
	assertNoAuthEvents(t, rec.snapshot())
}

func TestHandleLogout_EmitsLogAuth(t *testing.T) {
	p, rec := newAuditingProvider(t, nil)

	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()
	p.handleLogout(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	assertSingleAuthEvent(t, rec.snapshot(), true, authReasonLogout)
}

func TestHandleLogout_WithIdentity_PopulatesUserID(t *testing.T) {
	p, rec := newAuditingProvider(t, nil)

	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	ctx := auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "alice"}))
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	p.handleLogout(w, req)

	events := rec.snapshot()
	assertSingleAuthEvent(t, events, true, authReasonLogout)
	if events[0].UserID != "alice" {
		t.Errorf("UserID = %q, want %q", events[0].UserID, "alice")
	}
}

func TestProvider_WithNoopAuditLogger_DoesNotPanic(t *testing.T) {
	p := newTestProvider(t, &fakeOIDCClient{})
	p.auditLogger = coreauth.NoopAuditLogger{}

	logoutReq := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	p.handleLogout(httptest.NewRecorder(), logoutReq)

	cbReq := httptest.NewRequest("GET", "/api/v1/auth/callback", nil)
	p.handleCallback(httptest.NewRecorder(), cbReq)

	cb2 := httptest.NewRequest("GET", "/api/v1/auth/callback?code=x", nil)
	p.handleCallback(httptest.NewRecorder(), cb2)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	mwReq := httptest.NewRequest("GET", "/api/v1/something", nil)
	handler.ServeHTTP(httptest.NewRecorder(), mwReq)

	mwReq2 := httptest.NewRequest("GET", "/api/v1/something", nil)
	mwReq2.Header.Set("Authorization", "Basic xyz")
	handler.ServeHTTP(httptest.NewRecorder(), mwReq2)
}
