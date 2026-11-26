// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func installRecordingMetrics(t *testing.T) *observability.RecordingMetrics {
	t.Helper()
	rec := observability.NewRecordingMetrics()
	runtime.Init(rec)
	t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })
	return rec
}

func TestAuthLoginMetric_CallbackSuccess(t *testing.T) {
	rec := installRecordingMetrics(t)

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
	p := newTestProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000"

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if !rec.HasCounter("auth.login",
		"gateway", "gw-1", "result", observability.AuthResultSuccess, "reason", authReasonOIDCLogin) {
		t.Errorf("expected auth.login{result=success, reason=%s}; got %+v",
			authReasonOIDCLogin, rec.Counters("auth.login"))
	}
}

func TestAuthLoginMetric_CallbackMissingCode(t *testing.T) {
	rec := installRecordingMetrics(t)

	p := newTestProvider(t, &fakeOIDCClient{})
	p.config.FrontendRedirectURL = "http://localhost:3000"

	req := httptest.NewRequest("GET", "/api/v1/auth/callback", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)

	if !rec.HasCounter("auth.login",
		"gateway", "gw-1", "result", observability.AuthResultFailure, "reason", authReasonOIDCMissingCode) {
		t.Errorf("expected auth.login{result=failure, reason=%s}; got %+v",
			authReasonOIDCMissingCode, rec.Counters("auth.login"))
	}
}

func TestAuthLoginMetric_RejectReasons(t *testing.T) {
	rec := installRecordingMetrics(t)
	p := newTestProvider(t, &fakeOIDCClient{})

	reasons := []string{
		authReasonInvalidToken,
		authReasonMissingToken,
		authReasonOIDCStateMismatch,
		authReasonOIDCCodeExchangeFail,
		authReasonOIDCSAMMintFail,
	}
	for _, reason := range reasons {
		p.logAuthEvent(t.Context(), "", false, reason)
		if !rec.HasCounter("auth.login",
			"gateway", "gw-1", "result", observability.AuthResultFailure, "reason", reason) {
			t.Errorf("expected auth.login{result=failure, reason=%s}", reason)
		}
	}
}

func TestAuthTokenValidationMetric_Expired(t *testing.T) {
	rec := installRecordingMetrics(t)

	fake := &fakeOIDCClient{expectedToken: "idp-bearer", userInfoErr: errBadToken}
	provider := newProviderWithFakeOIDC(t, fake)

	expired, err := MintSAMToken(provider.signer, MintClaims{UserID: "alice"}, time.Millisecond)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	v := NewTokenValidator(provider.signer, provider.authzSvc, provider, "gw-1", nil)
	if _, err := v.Validate(t.Context(), expired); err == nil {
		t.Fatal("Validate should fail for an expired SAM token with a rejecting IdP")
	}

	if !rec.HasCounter("auth.token.validation",
		"gateway", "gw-1", "result", observability.AuthResultFailure, "reason", authReasonExpiredToken) {
		t.Errorf("expected auth.token.validation{result=failure, reason=%s}; got %+v",
			authReasonExpiredToken, rec.Counters("auth.token.validation"))
	}
}

func TestAuthTokenValidationMetric_Malformed(t *testing.T) {
	rec := installRecordingMetrics(t)

	fake := &fakeOIDCClient{expectedToken: "idp-bearer", userInfoErr: errBadToken}
	provider := newProviderWithFakeOIDC(t, fake)

	v := NewTokenValidator(provider.signer, provider.authzSvc, provider, "gw-1", nil)
	if _, err := v.Validate(t.Context(), "not-a-jwt"); err == nil {
		t.Fatal("Validate should fail for a malformed SAM token with a rejecting IdP")
	}

	if !rec.HasCounter("auth.token.validation",
		"gateway", "gw-1", "result", observability.AuthResultFailure, "reason", authReasonInvalidToken) {
		t.Errorf("expected auth.token.validation{result=failure, reason=%s}; got %+v",
			authReasonInvalidToken, rec.Counters("auth.token.validation"))
	}
}

func TestAuthMetrics_NoProvider(t *testing.T) {
	runtime.Init(runtime.NoopMetrics{})
	p := newTestProvider(t, &fakeOIDCClient{})
	p.logAuthEvent(t.Context(), "", false, authReasonInvalidToken)

	v := NewTokenValidator(p.signer, p.authzSvc, nil, "gw-1", nil)
	if _, err := v.Validate(t.Context(), "not-a-jwt"); err == nil {
		t.Fatal("Validate should fail for a malformed token with no IdP fallback")
	}
}
