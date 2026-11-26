// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func signAndVerifyAnalyticsIdentity(t *testing.T, scopes []string) *auth.Identity {
	t.Helper()
	km, err := trust.NewKeyManager("test-gw")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := trust.NewSigner(km, "test-gw", time.Hour, 24*time.Hour)
	registry := trust.NewRegistry()
	registry.AddOrUpdate(&trust.TrustCard{
		ComponentType: "gateway",
		ComponentID:   "test-gw",
		Namespace:     "test-ns",
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	})
	verifier := trust.NewVerifier(registry, 5*time.Second, slog.Default())

	token, err := signer.Sign(trust.SignClaims{Subject: "sentinel-monitor", TaskID: "task-1", Scopes: scopes})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	id, err := verifier.Authenticate(context.Background(), token, "task-1")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return id
}

func TestAnalyticsGate_SignedScopesRoundTrip(t *testing.T) {
	exe := newAuthzAnalyticsExec(t)
	path := []string{"analytics", "feedback", "summary"}

	withScope := signAndVerifyAnalyticsIdentity(t, []string{rbac.ScopeAnalyticsRead})
	if len(withScope.Scopes) != 1 || withScope.Scopes[0] != rbac.ScopeAnalyticsRead {
		t.Fatalf("verified scopes = %v, want [%s] (scopes lost in JWT round trip)", withScope.Scopes, rbac.ScopeAnalyticsRead)
	}
	if _, err := exe.HandleManagement(context.Background(), "get", path, nil, withScope); err == nil ||
		!strings.Contains(err.Error(), "analytics unavailable") {
		t.Errorf("signed-scope identity error = %v, want 'analytics unavailable' (gate passed)", err)
	}

	noScope := signAndVerifyAnalyticsIdentity(t, nil)
	if _, err := exe.HandleManagement(context.Background(), "get", path, nil, noScope); err == nil ||
		!strings.Contains(err.Error(), "insufficient permissions") {
		t.Errorf("signed scope-less identity error = %v, want 'insufficient permissions'", err)
	}
}
