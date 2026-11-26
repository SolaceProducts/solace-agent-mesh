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
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func mintProviderWithCache(t *testing.T, res auth.AuthorizationResolution) (*Provider, *auth.ProfileCache) {
	t.Helper()
	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo: &UserInfo{
			Sub:         "user-123",
			Email:       "alice@example.com",
			Name:        "Alice",
			RawResponse: map[string]any{"sub": "user-123", "email": "alice@example.com", "name": "Alice"},
		},
		tokenResponse: &TokenResponse{AccessToken: "idp-at", TokenType: "Bearer", ExpiresIn: 3600},
	}
	p, _ := newAuditingProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000"

	svc, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	p.authzSvc = svc
	p.SetAuthorizationClient(&stubAuthzClient{resolution: res})

	cache := auth.NewProfileCache(time.Hour)
	p.SetProfileCache(cache)
	return p, cache
}

func runCallback(t *testing.T, p *Provider) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	w := httptest.NewRecorder()
	p.handleCallback(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", w.Code)
	}
	_, fragment, _ := strings.Cut(w.Header().Get("Location"), "#")
	minted, err := url.ParseQuery(fragment)
	if err != nil {
		t.Fatalf("callback redirect fragment is not parseable: %v", err)
	}
	if minted.Get("sam_access_token") == "" {
		t.Fatalf("callback minted no token, so the cache-write path never ran: error=%q description=%q",
			minted.Get("error"), minted.Get("error_description"))
	}
}

func TestMintFromOIDC_DoesNotCacheDegradedEnrichment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status identity.EnrichmentStatus
	}{
		{"unavailable", identity.EnrichmentUnavailable},
		{"stale", identity.EnrichmentStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, cache := mintProviderWithCache(t, auth.AuthorizationResolution{
				Roles:            []string{"role"},
				Scopes:           []string{"scope"},
				EnrichmentStatus: tc.status,
				EnrichmentReason: "provider timed out",
			})

			runCallback(t, p)

			if entry, ok := cache.Get("alice@example.com"); ok {
				t.Errorf("cache holds a %q entry after a degraded lookup; the middleware would serve it without retrying (entry=%+v)", tc.status, entry)
			}
		})
	}
}

func TestMintFromOIDC_CachesSuccessfulEnrichment(t *testing.T) {
	profile := &identity.Profile{ID: "alice@example.com", DisplayName: "Alice Anderson"}
	p, cache := mintProviderWithCache(t, auth.AuthorizationResolution{
		Roles:            []string{"role"},
		Scopes:           []string{"scope"},
		Profile:          profile,
		EnrichmentStatus: identity.EnrichmentOK,
	})

	runCallback(t, p)

	entry, ok := cache.Get("alice@example.com")
	if !ok {
		t.Fatal("cache was not populated by a successful enrichment")
	}
	if entry.EnrichmentStatus != identity.EnrichmentOK {
		t.Errorf("EnrichmentStatus = %q, want ok", entry.EnrichmentStatus)
	}
	if entry.Profile == nil || entry.Profile.DisplayName != "Alice Anderson" {
		t.Errorf("cached profile = %+v, want the resolved profile", entry.Profile)
	}
}

func TestMintFromOIDC_DegradedLookupKeepsEarlierProfile(t *testing.T) {
	p, cache := mintProviderWithCache(t, auth.AuthorizationResolution{
		Roles:            []string{"role"},
		Scopes:           []string{"scope"},
		EnrichmentStatus: identity.EnrichmentUnavailable,
		EnrichmentReason: "provider timed out",
	})
	cache.Put("alice@example.com", auth.CachedProfile{
		Profile:          &identity.Profile{ID: "alice@example.com", DisplayName: "Alice Anderson"},
		EnrichmentStatus: identity.EnrichmentOK,
	})

	runCallback(t, p)

	entry, ok := cache.Get("alice@example.com")
	if !ok {
		t.Fatal("earlier profile was evicted by a degraded lookup")
	}
	if entry.Profile == nil || entry.Profile.DisplayName != "Alice Anderson" {
		t.Errorf("cached profile = %+v, want the earlier good profile preserved", entry.Profile)
	}
}

func TestIdentityFromIDPToken_DoesNotCacheDegradedEnrichment(t *testing.T) {
	p, cache := mintProviderWithCache(t, auth.AuthorizationResolution{
		Roles:            []string{"role"},
		Scopes:           []string{"scope"},
		EnrichmentStatus: identity.EnrichmentUnavailable,
		EnrichmentReason: "provider timed out",
	})
	cache.Put("alice@example.com", auth.CachedProfile{
		Profile:          &identity.Profile{ID: "alice@example.com", DisplayName: "Alice Anderson"},
		EnrichmentStatus: identity.EnrichmentOK,
	})

	id, err := p.IdentityFromIDPToken(t.Context(), "idp-at")
	if err != nil {
		t.Fatalf("IdentityFromIDPToken: %v", err)
	}
	if id.EnrichmentStatus() != identity.EnrichmentUnavailable {
		t.Errorf("identity EnrichmentStatus = %q, want unavailable", id.EnrichmentStatus())
	}

	entry, ok := cache.Get("alice@example.com")
	if !ok {
		t.Fatal("earlier profile was evicted by a degraded IdP-bearer lookup")
	}
	if entry.Profile == nil || entry.Profile.DisplayName != "Alice Anderson" {
		t.Errorf("cached profile = %+v, want the earlier good profile preserved", entry.Profile)
	}
}

func TestIdentityFromIDPToken_CachesSuccessfulEnrichment(t *testing.T) {
	p, cache := mintProviderWithCache(t, auth.AuthorizationResolution{
		Roles:            []string{"role"},
		Scopes:           []string{"scope"},
		Profile:          &identity.Profile{ID: "alice@example.com", DisplayName: "Alice Anderson"},
		EnrichmentStatus: identity.EnrichmentOK,
	})

	if _, err := p.IdentityFromIDPToken(t.Context(), "idp-at"); err != nil {
		t.Fatalf("IdentityFromIDPToken: %v", err)
	}

	entry, ok := cache.Get("alice@example.com")
	if !ok {
		t.Fatal("cache was not populated by a successful IdP-bearer enrichment")
	}
	if entry.EnrichmentStatus != identity.EnrichmentOK {
		t.Errorf("EnrichmentStatus = %q, want ok", entry.EnrichmentStatus)
	}
	if entry.Profile == nil || entry.Profile.DisplayName != "Alice Anderson" {
		t.Errorf("cached profile = %+v, want the resolved profile", entry.Profile)
	}
}

func TestIdentityResolver_DegradedLookupKeepsInteractiveProfile(t *testing.T) {
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1", testLogger())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	svc.SetAuthorizationClient(&stubAuthzClient{resolution: auth.AuthorizationResolution{
		Roles:            []string{"delegator"},
		Scopes:           []string{rbac.AgentInvokeScope("PeerAgent")},
		EnrichmentStatus: identity.EnrichmentUnavailable,
		EnrichmentReason: "provider timed out",
	}})

	cache := auth.NewProfileCache(time.Hour)
	cache.Put("alice", auth.CachedProfile{
		Profile:          &identity.Profile{ID: "alice", DisplayName: "Alice Anderson"},
		EnrichmentStatus: identity.EnrichmentOK,
	})

	id, err := NewIdentityResolver(svc, cache).ResolveIdentity(t.Context(), "alice")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.EnrichmentStatus() != identity.EnrichmentUnavailable {
		t.Errorf("identity EnrichmentStatus = %q, want unavailable", id.EnrichmentStatus())
	}

	entry, ok := cache.Get("alice")
	if !ok {
		t.Fatal("interactive profile was evicted by a scheduled-task fire")
	}
	if entry.Profile == nil || entry.Profile.DisplayName != "Alice Anderson" {
		t.Errorf("cached profile = %+v, want the interactive profile preserved", entry.Profile)
	}
}

func TestIdentityResolver_DegradedLookupIsLogged(t *testing.T) {
	buf := &bytes.Buffer{}
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1",
		slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	svc.SetAuthorizationClient(&stubAuthzClient{resolution: auth.AuthorizationResolution{
		Roles:            []string{"delegator"},
		Scopes:           []string{rbac.AgentInvokeScope("PeerAgent")},
		EnrichmentStatus: identity.EnrichmentUnavailable,
		EnrichmentReason: "provider timed out",
	}})

	if _, err := NewIdentityResolver(svc, nil).ResolveIdentity(t.Context(), "alice"); err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}

	logs := buf.String()
	for _, want := range []string{"profile enrichment degraded", "unavailable", "scheduled_task"} {
		if !strings.Contains(logs, want) {
			t.Errorf("degraded scheduled-task fire did not log %q:\n%s", want, logs)
		}
	}
}

func TestIdentityResolver_SuccessfulLookupIsNotLogged(t *testing.T) {
	buf := &bytes.Buffer{}
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1",
		slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	svc.SetAuthorizationClient(&stubAuthzClient{resolution: auth.AuthorizationResolution{
		Roles:            []string{"delegator"},
		Scopes:           []string{rbac.AgentInvokeScope("PeerAgent")},
		Profile:          &identity.Profile{ID: "alice", DisplayName: "Alice Anderson"},
		EnrichmentStatus: identity.EnrichmentOK,
	}})

	if _, err := NewIdentityResolver(svc, nil).ResolveIdentity(t.Context(), "alice"); err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}

	if strings.Contains(buf.String(), "profile enrichment degraded") {
		t.Errorf("a healthy lookup logged a degradation WARN:\n%s", buf.String())
	}
}
