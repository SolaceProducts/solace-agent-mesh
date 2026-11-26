// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

func TestIdentityFromIDPToken_RejectsReservedSystemPrincipal(t *testing.T) {
	t.Run("email in reserved system namespace is rejected", func(t *testing.T) {
		fake := &fakeOIDCClient{
			expectedToken: "idp-at",
			userInfo: &UserInfo{
				Sub:   "real-sub-123",
				Email: "system:default",
				Name:  "Nefarious",
			},
		}
		p := newTestProvider(t, fake)

		id, err := p.IdentityFromIDPToken(t.Context(), "idp-at")
		if err == nil {
			t.Fatalf("expected rejection of reserved system: principal, got identity %+v", id)
		}
		if id != nil {
			t.Fatalf("expected nil identity on rejection, got %+v", id)
		}
	})

	t.Run("ordinary email still resolves", func(t *testing.T) {
		fake := &fakeOIDCClient{
			expectedToken: "idp-at",
			userInfo:      &UserInfo{Sub: "real-sub-123", Email: "alice@example.com", Name: "Alice"},
		}
		p := newTestProvider(t, fake)

		id, err := p.IdentityFromIDPToken(t.Context(), "idp-at")
		if err != nil {
			t.Fatalf("unexpected error for ordinary email: %v", err)
		}
		if id == nil || id.UserID() != "alice@example.com" {
			t.Fatalf("want UserID=alice@example.com, got %+v", id)
		}
	})
}

func TestHandleCallback_RejectsReservedSystemPrincipal(t *testing.T) {
	rec := installRecordingMetrics(t)

	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo: &UserInfo{
			Sub:   "real-sub-123",
			Email: "system:default",
			Name:  "Nefarious",
		},
		tokenResponse: &TokenResponse{AccessToken: "idp-at", TokenType: "Bearer", ExpiresIn: 3600},
	}
	p := newTestProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000"

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if !rec.HasCounter("auth.login",
		"gateway", "gw-1", "result", observability.AuthResultFailure, "reason", authReasonOIDCReservedSubject) {
		t.Errorf("expected auth.login{result=failure, reason=%s}; got %+v",
			authReasonOIDCReservedSubject, rec.Counters("auth.login"))
	}
}
