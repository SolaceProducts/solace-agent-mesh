// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func syntheticProvider(t *testing.T, cfg *SyntheticAuthConfig) *Provider {
	t.Helper()
	p := newTestProvider(t, nil)
	p.config.SyntheticAuth = cfg
	return p
}

func TestMiddleware_SyntheticValidTokenAllowsRequest(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	p := syntheticProvider(t, cfg)
	token := mintSyntheticToken(t, priv, nil)

	var gotIdentity *auth.Subject
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := auth.SubjectFromContext(r.Context())
		gotIdentity = id
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotIdentity == nil {
		t.Fatal("expected synthetic identity on context")
	}
	if gotIdentity.AuthMethod() != auth.SyntheticAuthMethod {
		t.Errorf("AuthMethod = %q, want %q", gotIdentity.AuthMethod(), auth.SyntheticAuthMethod)
	}
	if gotIdentity.UserID() != auth.SyntheticUserID {
		t.Errorf("UserID = %q, want %q", gotIdentity.UserID(), auth.SyntheticUserID)
	}
}

func TestMiddleware_SyntheticValidTokenNotAllowlistedEndpointReturns403(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	p := syntheticProvider(t, cfg)
	token := mintSyntheticToken(t, priv, nil)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler should not be called when synthetic identity is denied")
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMiddleware_SyntheticInvalidTokenReturns401(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	p := syntheticProvider(t, cfg)
	token := mintSyntheticToken(t, priv, map[string]any{"appidacr": "0"})

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler should not run on synthetic hard-reject")
	}))

	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMiddleware_SyntheticUserClaimReturns401(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	p := syntheticProvider(t, cfg)
	token := mintSyntheticToken(t, priv, map[string]any{"upn": "real.user@example.com"})

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler should not run on user-claim hard-reject")
	}))

	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_SyntheticInvalidTokenOnSoftAuthPathFallsThrough(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	p := syntheticProvider(t, cfg)
	token := mintSyntheticToken(t, priv, map[string]any{"appidacr": "0"})

	called := false
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	const shareID = "abc123def456ghi789jkl"
	req := httptest.NewRequest("GET", "/api/v1/share/"+shareID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("inner handler should be called on soft-auth synthetic-token failure (got status %d)", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 from passthrough", rec.Code)
	}
}

func TestMiddleware_SyntheticNonSyntheticTokenFallsThrough(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	p := syntheticProvider(t, cfg)
	token := mintSyntheticToken(t, priv, map[string]any{
		"appid": syntOtherApp,
		"azp":   syntOtherApp,
		"roles": []any{"User.Read"},
	})

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler should not be called when token is not synthetic and SAM/IdP validation fails")
	}))

	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (after fall-through), body=%s", rec.Code, rec.Body.String())
	}
}
