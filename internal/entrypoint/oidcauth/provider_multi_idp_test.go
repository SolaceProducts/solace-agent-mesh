// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
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

func newProviderWithFakeOIDCNamed(t *testing.T, fake *fakeOIDCClient, providerName, gatewayID string, logger *slog.Logger) *Provider {
	t.Helper()
	km, err := trust.NewKeyManager(gatewayID)
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := trust.NewSigner(km, gatewayID, time.Hour, 2*time.Hour)

	cfg := &Config{
		ExternalAuthProvider: providerName,
		FrontendServerURL:    "http://localhost:3000",
		SessionSecretKey:     "test-session-key-" + gatewayID,
		SAMAccessToken:       SAMAccessTokenConfig{TTLSeconds: 3600},
		AuthorizationService: AuthorizationServiceConfig{Type: "none"},
	}

	if logger == nil {
		logger = slog.Default()
	}

	authzSvc, err := NewAuthorizationService(cfg.AuthorizationService, gatewayID, logger)
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	return &Provider{
		oidcClient:  fake,
		signer:      signer,
		authzSvc:    authzSvc,
		auditLogger: coreauth.NoopAuditLogger{},
		config:      cfg,
		gatewayID:   gatewayID,
		logger: logger.With(
			"logger_name", "enterprise-auth",
			"gatewayID", gatewayID,
			"providerName", providerName,
		),
	}
}

func TestMultiIdP_TokenIsolation(t *testing.T) {
	const (
		tokenA = "idp-a-access-token"
		tokenB = "idp-b-access-token"
	)

	fakeA := &fakeOIDCClient{
		expectedToken: tokenA,
		userInfo: &UserInfo{
			Sub:         "user-a",
			Email:       "alice@idp-a.example.com",
			Name:        "Alice",
			RawResponse: map[string]any{"sub": "user-a", "email": "alice@idp-a.example.com"},
		},
	}
	fakeB := &fakeOIDCClient{
		expectedToken: tokenB,
		userInfo: &UserInfo{
			Sub:         "user-b",
			Email:       "bob@idp-b.example.com",
			Name:        "Bob",
			RawResponse: map[string]any{"sub": "user-b", "email": "bob@idp-b.example.com"},
		},
	}

	var logBufA, logBufB bytes.Buffer
	loggerA := slog.New(slog.NewJSONHandler(&logBufA, &slog.HandlerOptions{Level: slog.LevelDebug}))
	loggerB := slog.New(slog.NewJSONHandler(&logBufB, &slog.HandlerOptions{Level: slog.LevelDebug}))

	pA := newProviderWithFakeOIDCNamed(t, fakeA, "idp-a", "gw-a", loggerA)
	pB := newProviderWithFakeOIDCNamed(t, fakeB, "idp-b", "gw-b", loggerB)

	send := func(p *Provider, bearer string) (int, *auth.Subject) {
		var got *auth.Subject
		handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id, ok := auth.SubjectFromContext(r.Context()); ok {
				got = id
			}
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/something", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, got
	}

	codeBrejectA, _ := send(pB, tokenA)
	if codeBrejectA != http.StatusUnauthorized {
		t.Errorf("Provider-B accepted IdP-A's token (status=%d); want 401", codeBrejectA)
	}
	if fakeB.calls != 1 {
		t.Errorf("Provider-B's fake GetUserInfo call count = %d, want 1 (token must be checked against B's IdP)", fakeB.calls)
	}

	codeArejectB, _ := send(pA, tokenB)
	if codeArejectB != http.StatusUnauthorized {
		t.Errorf("Provider-A accepted IdP-B's token (status=%d); want 401", codeArejectB)
	}
	if fakeA.calls != 1 {
		t.Errorf("Provider-A's fake GetUserInfo call count = %d, want 1 (token must be checked against A's IdP)", fakeA.calls)
	}

	codeAacceptA, idA := send(pA, tokenA)
	if codeAacceptA != http.StatusOK {
		t.Fatalf("Provider-A rejected its own valid IdP-A token (status=%d); sanity check failed", codeAacceptA)
	}
	if idA == nil || idA.UserID() != "alice@idp-a.example.com" {
		t.Errorf("Provider-A identity = %+v, want UserID=alice@idp-a.example.com", idA)
	}

	codeBacceptB, idB := send(pB, tokenB)
	if codeBacceptB != http.StatusOK {
		t.Fatalf("Provider-B rejected its own valid IdP-B token (status=%d); sanity check failed", codeBacceptB)
	}
	if idB == nil || idB.UserID() != "bob@idp-b.example.com" {
		t.Errorf("Provider-B identity = %+v, want UserID=bob@idp-b.example.com", idB)
	}

	assertLogsCarryProviderName(t, &logBufA, "idp-a", "idp-b")
	assertLogsCarryProviderName(t, &logBufB, "idp-b", "idp-a")
}

func assertLogsCarryProviderName(t *testing.T, buf *bytes.Buffer, want, forbidden string) {
	t.Helper()
	out := buf.String()
	if out == "" {
		t.Errorf("no log records captured for providerName=%q (expected at least one enterprise-auth record)", want)
		return
	}
	const loggerNameAttr = `"logger_name":"enterprise-auth"`
	wantAttr := `"providerName":"` + want + `"`
	forbiddenAttr := `"providerName":"` + forbidden + `"`
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	sawWant := false
	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.Contains(line, loggerNameAttr) {
			continue
		}
		if !strings.Contains(line, wantAttr) {
			t.Errorf("enterprise-auth log record missing providerName=%q: %s", want, line)
		} else {
			sawWant = true
		}
		if strings.Contains(line, forbiddenAttr) {
			t.Errorf("enterprise-auth log record carries cross-provider name %q (expected %q): %s", forbidden, want, line)
		}
	}
	if !sawWant {
		t.Errorf("expected at least one enterprise-auth log record with providerName=%q; none found in %q", want, out)
	}
}
