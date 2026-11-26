// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func TestStateDispatcher_RealVerifier_EndToEnd(t *testing.T) {
	t.Parallel()

	const (
		mcpComponentID = "gw-mcp-a"
		mcpPublicURL   = "https://gw-mcp-a.example.com"
		targetLinkURI  = "https://gw-mcp-a.example.com/gw/mcp-a/oauth/finish"
	)

	dispatcher, mcpSigner := newRealStateDispatcher(t, mcpComponentID, mcpPublicURL)

	ts := httptest.NewServer(http.HandlerFunc(dispatcher.Handle))
	t.Cleanup(ts.Close)

	rawState := mintRealStateJWT(t, mcpSigner, trust.StateClaims{
		TargetLinkURI: targetLinkURI,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    mcpComponentID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			ID:        "jti-e2e-happy",
		},
	})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=AUTH_CODE&state="+url.QueryEscape(rawState), nil)
	w := httptest.NewRecorder()
	dispatcher.Handle(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("Location header is not a URL: %v", err)
	}
	if loc.Scheme != "https" {
		t.Errorf("Location scheme = %q, want https", loc.Scheme)
	}
	if loc.Host != "gw-mcp-a.example.com" {
		t.Errorf("Location host = %q, want gw-mcp-a.example.com", loc.Host)
	}
	if loc.Path != "/gw/mcp-a/oauth/finish" {
		t.Errorf("Location path = %q, want /gw/mcp-a/oauth/finish", loc.Path)
	}
	if got := loc.Query().Get("code"); got != "AUTH_CODE" {
		t.Errorf("Location code = %q, want AUTH_CODE", got)
	}
	if got := loc.Query().Get("state"); got != rawState {
		t.Errorf("Location state did not match minted JWT; got %q want %q", got, rawState)
	}
}

func TestStateDispatcher_RealVerifier_WrongHost_400(t *testing.T) {
	t.Parallel()

	const (
		mcpComponentID = "gw-mcp-b"
		mcpPublicURL   = "https://gw-mcp-b.example.com"
	)

	dispatcher, mcpSigner := newRealStateDispatcher(t, mcpComponentID, mcpPublicURL)

	rawState := mintRealStateJWT(t, mcpSigner, trust.StateClaims{
		TargetLinkURI: "https://evil.example.org/gw/mcp-b/oauth/finish",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    mcpComponentID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			ID:        "jti-e2e-evil-host",
		},
	})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=AUTH_CODE&state="+url.QueryEscape(rawState), nil)
	w := httptest.NewRecorder()
	dispatcher.Handle(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown target host", w.Result().StatusCode)
	}
}

func newRealStateDispatcher(t *testing.T, mcpComponentID, mcpPublicURL string) (*StateDispatcher, *trust.Signer) {
	t.Helper()

	b := broker.NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("broker.Connect: %v", err)
	}
	t.Cleanup(func() { _ = b.Disconnect(t.Context()) })

	cfg := trust.DefaultConfig()
	cfg.Enabled = true
	cfg.MinRequiredGateways = 0
	cfg.CardPublishIntervalSeconds = 3600

	mcpMgr := trust.NewManager(cfg, "gateway", "gw-mcp-host", "test-ns", b, slog.Default())
	if err := mcpMgr.Init(); err != nil {
		t.Fatalf("mcpMgr.Init: %v", err)
	}
	dispMgr := trust.NewManager(cfg, "gateway", "gw-dispatcher", "test-ns", b, slog.Default())
	if err := dispMgr.Init(); err != nil {
		t.Fatalf("dispMgr.Init: %v", err)
	}

	if err := dispMgr.Start(t.Context()); err != nil {
		t.Fatalf("dispMgr.Start: %v", err)
	}
	t.Cleanup(func() { _ = dispMgr.Stop(t.Context()) })
	if err := mcpMgr.Start(t.Context()); err != nil {
		t.Fatalf("mcpMgr.Start: %v", err)
	}
	t.Cleanup(func() { _ = mcpMgr.Stop(t.Context()) })

	mcpKM, err := trust.NewKeyManager(mcpComponentID)
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	if err := mcpMgr.RegisterAdditionalSigner(t.Context(), mcpComponentID, mcpKM, mcpPublicURL); err != nil {
		t.Fatalf("RegisterAdditionalSigner: %v", err)
	}

	wantHost := mustParseHost(t, mcpPublicURL)

	sv := dispMgr.NewStateVerifier()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		hosts := sv.KnownHosts()
		if hosts[wantHost] {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sv.KnownHosts()[wantHost] {
		t.Fatalf("dispatcher manager did not pick up mcp PublicURL host %q within 5s", wantHost)
	}

	mcpSigner := trust.NewSigner(mcpKM, mcpComponentID, time.Hour, 2*time.Hour)
	d := NewStateDispatcher(sv, testLogger())
	return d, mcpSigner
}

func mintRealStateJWT(t *testing.T, signer *trust.Signer, claims trust.StateClaims) string {
	t.Helper()
	ss := trust.NewStateSigner(signer)
	raw, err := ss.Mint(claims)
	if err != nil {
		t.Fatalf("StateSigner.Mint: %v", err)
	}
	return raw
}

func mustParseHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		t.Fatalf("parse PublicURL %q: %v", raw, err)
	}
	return u.Host
}
