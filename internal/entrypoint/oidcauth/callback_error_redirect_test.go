// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func parseRedirectFragment(t *testing.T, location string) url.Values {
	t.Helper()
	i := strings.Index(location, "#")
	if i < 0 {
		t.Fatalf("redirect Location has no fragment: %q", location)
	}
	frag, err := url.ParseQuery(location[i+1:])
	if err != nil {
		t.Fatalf("parse fragment %q: %v", location, err)
	}
	return frag
}

func TestOIDCCallback_TransientFailure_RedirectsToErrorScreen(t *testing.T) {
	fake := &fakeOIDCClient{
		tokenResponse: &TokenResponse{AccessToken: "idp-at", TokenType: "Bearer", ExpiresIn: 3600},
	}
	p, _ := newAuditingProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000/auth-callback"

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect to the SPA error screen", w.Code)
	}
	frag := parseRedirectFragment(t, w.Header().Get("Location"))
	if tok := frag.Get("sam_access_token"); tok != "" {
		t.Fatalf("fail-closed: no SAM token may be minted on a failed mint, got %q", tok)
	}
	if frag.Get("error") == "" {
		t.Error("redirect fragment missing error type")
	}
	if frag.Get("retryable") != "1" {
		t.Errorf("transient failure should set retryable=1, got %q", frag.Get("retryable"))
	}
	if frag.Get("errorId") == "" {
		t.Error("redirect fragment missing errorId for support correlation")
	}
}

func TestOIDCCallback_NonTransientFailure_RedirectsWithoutRetryable(t *testing.T) {
	fake := &fakeOIDCClient{validateState: true}
	p, _ := newAuditingProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000/auth-callback"

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code&state=tampered", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect", w.Code)
	}
	frag := parseRedirectFragment(t, w.Header().Get("Location"))
	if tok := frag.Get("sam_access_token"); tok != "" {
		t.Fatalf("fail-closed: no SAM token may be minted on a rejected callback, got %q", tok)
	}
	if frag.Get("error") == "" {
		t.Error("redirect fragment missing error type")
	}
	if frag.Get("retryable") != "" {
		t.Errorf("state failure must not be auto-retryable, got retryable=%q", frag.Get("retryable"))
	}
}

func TestOIDCCallback_NoRedirectTarget_FallsBackToJSON(t *testing.T) {
	fake := &fakeOIDCClient{
		tokenResponse: &TokenResponse{AccessToken: "idp-at", TokenType: "Bearer", ExpiresIn: 3600},
	}
	p, _ := newAuditingProvider(t, fake)
	p.config.FrontendRedirectURL = ""

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 JSON fallback when no redirect target", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("fallback should be JSON, got Content-Type %q", ct)
	}
}
