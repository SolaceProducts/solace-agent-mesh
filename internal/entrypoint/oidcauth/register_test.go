// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func newTestTrustSigner(t *testing.T, gatewayID string) *trust.Signer {
	t.Helper()
	km, err := trust.NewKeyManager(gatewayID)
	if err != nil {
		t.Fatalf("trust.NewKeyManager: %v", err)
	}
	return trust.NewSigner(km, gatewayID, time.Hour, 2*time.Hour)
}

func TestRegisterFactory_CatalogKeysAreAliases(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer := newTestTrustSigner(t, "gw-1")

	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure.example.com/",
			ClientID:     "az-id",
			ClientSecret: "az-secret",
		},
		"keycloak": {
			Issuer:       "https://keycloak.example.com/realms/sam",
			ClientID:     "kc-id",
			ClientSecret: "kc-secret",
		},
	}

	RegisterFactory(signer, "gw-1", catalog, coreauth.NoopAuditLogger{}, logger)

	cases := []struct {
		name         string
		providerName string
	}{
		{name: "enterprise:azure", providerName: "azure"},
		{name: "enterprise:keycloak", providerName: "keycloak"},
		{name: "enterprise", providerName: "azure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{
				"external_auth_provider": tc.providerName,
				"session_secret_key":     "test-session-key",
			}
			p, err := auth.CreateProvider(tc.name, cfg)
			if err != nil {
				t.Fatalf("CreateProvider(%q) returned error: %v", tc.name, err)
			}
			if p == nil {
				t.Fatalf("CreateProvider(%q) returned nil provider", tc.name)
			}
		})
	}
}

func TestRegisterFactory_ValidatorAliases(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer := newTestTrustSigner(t, "gw-2")

	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure.example.com/",
			ClientID:     "az-id",
			ClientSecret: "az-secret",
		},
		"keycloak": {
			Issuer:       "https://keycloak.example.com/realms/sam",
			ClientID:     "kc-id",
			ClientSecret: "kc-secret",
		},
	}

	RegisterFactory(signer, "gw-2", catalog, coreauth.NoopAuditLogger{}, logger)

	cases := []struct {
		name         string
		providerName string
	}{
		{name: "enterprise:azure", providerName: "azure"},
		{name: "enterprise:keycloak", providerName: "keycloak"},
		{name: "enterprise", providerName: "azure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{
				"external_auth_provider": tc.providerName,
				"session_secret_key":     "test-session-key",
			}
			v, err := auth.CreateValidator(tc.name, cfg)
			if err != nil {
				t.Fatalf("CreateValidator(%q): %v", tc.name, err)
			}
			if v == nil {
				t.Fatalf("CreateValidator(%q) returned nil validator", tc.name)
			}
		})
	}
}

func TestRegisterFactory_PerGatewaySigners(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer1 := newTestTrustSigner(t, "webui-gw")
	signer2 := newTestTrustSigner(t, "mcp-gw")

	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure.example.com/",
			ClientID:     "az-id",
			ClientSecret: "az-secret",
		},
	}

	RegisterFactoryForGateway("webui-gw", "webui-gw", catalog, FactoryDeps{
		Signer:      signer1,
		AuditLogger: coreauth.NoopAuditLogger{},
		Logger:      logger,
	})
	RegisterFactoryForGateway("mcp-gw", "mcp-gw", catalog, FactoryDeps{
		Signer:      signer2,
		AuditLogger: coreauth.NoopAuditLogger{},
		Logger:      logger,
	})

	cfg := map[string]any{
		"external_auth_provider": "azure",
		"session_secret_key":     "test-session-key",
	}
	v1, err := auth.CreateValidator("webui-gw:azure", cfg)
	if err != nil {
		t.Fatalf("CreateValidator(webui-gw:azure): %v", err)
	}
	v2, err := auth.CreateValidator("mcp-gw:azure", cfg)
	if err != nil {
		t.Fatalf("CreateValidator(mcp-gw:azure): %v", err)
	}

	token, err := MintSAMToken(signer1, MintClaims{UserID: "u-1"}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}
	if _, err := v1.Validate(t.Context(), token); err != nil {
		t.Errorf("v1 should accept signer1 token: %v", err)
	}
	if _, err := v2.Validate(t.Context(), token); err == nil {
		t.Error("v2 should reject signer1 token (wrong signer)")
	}
}

func TestRegisterFactoryForGateway_OnProviderBuiltFires(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer := newTestTrustSigner(t, "test-gw")

	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure.example.com/",
			ClientID:     "az-id",
			ClientSecret: "az-secret",
		},
	}

	var called bool
	var captured *Provider
	deps := FactoryDeps{
		Signer:      signer,
		AuditLogger: coreauth.NoopAuditLogger{},
		Logger:      logger,
		OnProviderBuilt: func(p *Provider) {
			called = true
			captured = p
		},
	}
	RegisterFactoryForGateway("on-built-gw", "on-built-gw", catalog, deps)

	cfg := map[string]any{
		"external_auth_provider": "azure",
		"session_secret_key":     "test-session-key",
	}
	if _, err := auth.CreateProvider("on-built-gw", cfg); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	if !called {
		t.Error("OnProviderBuilt was not called")
	}
	if captured == nil {
		t.Error("captured Provider is nil")
	}
}

func TestRegisterFactoryForGateway_OnProviderBuiltFiresExactlyOnce_UnderConcurrency(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	signer := newTestTrustSigner(t, "race-gw")

	catalog := map[string]*OIDCConfig{
		"azure": {
			Issuer:       "https://azure.example.com/",
			ClientID:     "az-id",
			ClientSecret: "az-secret",
		},
	}

	var callCount atomic.Int32
	deps := FactoryDeps{
		Signer:      signer,
		AuditLogger: coreauth.NoopAuditLogger{},
		Logger:      logger,
		OnProviderBuilt: func(p *Provider) {
			callCount.Add(1)
		},
	}
	RegisterFactoryForGateway("race-gw", "race-gw", catalog, deps)

	cfg := map[string]any{
		"external_auth_provider": "azure",
		"session_secret_key":     "test-session-key",
	}

	const N = 50
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for range N {
		wg.Go(func() {
			if _, err := auth.CreateProvider("race-gw", cfg); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("CreateProvider: %v", err)
	}
	if got := callCount.Load(); got != 1 {
		t.Errorf("OnProviderBuilt fired %d times, want exactly 1", got)
	}
}
