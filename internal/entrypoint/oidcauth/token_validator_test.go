// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestEnterpriseTokenValidator_FastPathSAMToken(t *testing.T) {
	signer := setupTestSigner(t)
	authz, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "none"}, "gw-test", slog.Default())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	token, err := MintSAMToken(signer, MintClaims{
		UserID: "u-1",
		Email:  "u1@example.com",
		Name:   "User One",
		Roles:  []string{"viewer"},
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	v := NewTokenValidator(signer, authz, nil, "gw-1", slog.Default())
	id, err := v.Validate(t.Context(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.UserID() != "u-1" {
		t.Errorf("UserID = %q, want %q", id.UserID(), "u-1")
	}
	if id.AuthMethod() != "sam_access_token" {
		t.Errorf("AuthMethod = %q, want %q", id.AuthMethod(), "sam_access_token")
	}
	if !id.Authenticated() {
		t.Error("Authenticated = false, want true")
	}
}

func TestEnterpriseTokenValidator_FallbackToIDPToken(t *testing.T) {
	const idpToken = "idp-bearer-xyz"
	fake := &fakeOIDCClient{
		expectedToken: idpToken,
		userInfo: &UserInfo{
			Sub:    "user-uuid-1",
			Email:  "alice@example.com",
			Name:   "Alice",
			Claims: map[string]any{"sub": "user-uuid-1"},
		},
	}
	provider := newProviderWithFakeOIDC(t, fake)

	v := NewTokenValidator(provider.signer, provider.authzSvc, provider, "gw-1", slog.Default())

	id, err := v.Validate(t.Context(), idpToken)
	if err != nil {
		t.Fatalf("Validate(idp-bearer) on SAM-fail-then-IdP-success path: %v", err)
	}
	if id == nil {
		t.Fatal("expected non-nil identity on IdP fallback")
	}
	if id.UserID() == "" {
		t.Errorf("IdP fallback returned empty UserID; want non-empty (sub=%q, email=%q)",
			fake.userInfo.Sub, fake.userInfo.Email)
	}
	if id.Email() != "alice@example.com" {
		t.Errorf("Email = %q, want alice@example.com", id.Email())
	}
	if !id.Authenticated() {
		t.Error("Authenticated = false on IdP-fallback success")
	}
	if fake.calls == 0 {
		t.Error("fake.GetUserInfo never called — fallback path did not run")
	}
}

func TestEnterpriseTokenValidator_BothFail(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "idp-bearer-xyz",
		userInfo:      nil,
		userInfoErr:   errBadToken,
	}
	provider := newProviderWithFakeOIDC(t, fake)
	v := NewTokenValidator(provider.signer, provider.authzSvc, provider, "gw-1", slog.Default())

	if _, err := v.Validate(t.Context(), "garbage-token"); err == nil {
		t.Fatal("Validate should error when both SAM and IdP paths fail")
	}
}

func TestEnterpriseTokenValidator_APIKeyNeverReachesTheIdP(t *testing.T) {
	const key = "samk_ZZZZYYYYXXXXWWWWVVVVUUUUTTTTSSSSRRRRQQQ"
	fake := &fakeOIDCClient{
		expectedToken: key,
		userInfo:      &UserInfo{Sub: "should-not-resolve", Claims: map[string]any{"sub": "should-not-resolve"}},
	}
	provider := newProviderWithFakeOIDC(t, fake)
	v := NewTokenValidator(provider.signer, provider.authzSvc, provider, "gw-1", slog.Default())

	id, err := v.Validate(t.Context(), key)
	if err == nil || id != nil {
		t.Fatalf("Validate(api key) = (%v, %v), want a refusal", id, err)
	}
	if fake.calls != 0 {
		t.Errorf("GetUserInfo ran %d times: the key was sent to the IdP", fake.calls)
	}
	if strings.Contains(err.Error(), key) {
		t.Error("the refusal error echoes the key")
	}
	if !errors.Is(err, errAPIKeyNotAccepted) {
		t.Errorf("error = %v, want errAPIKeyNotAccepted, so a caller can tell this refusal from a bad token", err)
	}
}

func TestEnterpriseTokenValidator_APIKeyRefusalHasItsOwnMetricReason(t *testing.T) {
	rec := installRecordingMetrics(t)
	provider := newProviderWithFakeOIDC(t, &fakeOIDCClient{})
	v := NewTokenValidator(provider.signer, provider.authzSvc, provider, "gw-1", slog.Default())

	_, _ = v.Validate(t.Context(), validKeyPlaintext)

	if got := rec.CounterSum("auth.token.validation", "gateway", "gw-1", "reason", auth.APIKeyReasonSurfaceNotSupported); got != 1 {
		t.Errorf("auth.token.validation{reason=%s} = %d, want 1; got %+v",
			auth.APIKeyReasonSurfaceNotSupported, got, rec.Counters("auth.token.validation"))
	}
	if got := rec.CounterSum("auth.token.validation", "reason", auth.APIKeyReasonNotEnabled); got != 0 {
		t.Errorf("the refusal was also counted as %s", auth.APIKeyReasonNotEnabled)
	}
}

func TestEnterpriseTokenValidator_APIKeyBehindWhitespaceNeverReachesTheIdP(t *testing.T) {
	for name, token := range map[string]string{
		"a space":  " " + validKeyPlaintext,
		"two":      "  " + validKeyPlaintext,
		"a tab":    "\t" + validKeyPlaintext,
		"trailing": validKeyPlaintext + " ",
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeOIDCClient{userInfo: &UserInfo{Sub: "should-not-resolve"}}
			provider := newProviderWithFakeOIDC(t, fake)
			v := NewTokenValidator(provider.signer, provider.authzSvc, provider, "gw-1", slog.Default())

			if _, err := v.Validate(t.Context(), token); !errors.Is(err, errAPIKeyNotAccepted) {
				t.Errorf("error = %v, want errAPIKeyNotAccepted", err)
			}
			if fake.calls != 0 {
				t.Errorf("GetUserInfo ran %d times: the key was sent to the IdP", fake.calls)
			}
		})
	}
}

func TestEnterpriseTokenValidator_Reject(t *testing.T) {
	signer := setupTestSigner(t)
	authz, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "none"}, "gw-test", slog.Default())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	v := NewTokenValidator(signer, authz, nil, "gw-1", slog.Default())
	if _, err := v.Validate(t.Context(), "not-a-token"); err == nil {
		t.Fatal("expected error for invalid token with no IdP fallback")
	}
}

func TestEnterpriseTokenValidator_PrefersBakedScopes(t *testing.T) {
	signer := setupTestSigner(t)
	authz, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "none"}, "gw-test", slog.Default())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	token, err := MintSAMToken(signer, MintClaims{
		UserID: "u-1",
		Roles:  []string{"viewer"},
		Scopes: []string{"agent:sales:invoke"},
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	v := NewTokenValidator(signer, authz, nil, "gw-1", slog.Default())
	id, err := v.Validate(t.Context(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(id.Scopes()) != 1 || id.Scopes()[0] != "agent:sales:invoke" {
		t.Errorf("Scopes = %v, want the baked-in set — the resolver would have returned [\"*\"]", id.Scopes())
	}
}

func TestEnterpriseTokenValidator_FallsBackWhenNoBakedScopes(t *testing.T) {
	signer := setupTestSigner(t)
	authz, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "none"}, "gw-test", slog.Default())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	token, err := MintSAMToken(signer, MintClaims{UserID: "u-1", Roles: []string{"viewer"}}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}

	v := NewTokenValidator(signer, authz, nil, "gw-1", slog.Default())
	id, err := v.Validate(t.Context(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(id.Scopes()) != 1 || id.Scopes()[0] != "*" {
		t.Errorf("Scopes = %v, want the expander's [\"*\"]", id.Scopes())
	}
}
