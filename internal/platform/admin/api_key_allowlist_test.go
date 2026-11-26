// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var pathWildcard = regexp.MustCompile(`\{[^}]+\}`)

const allowlistPathID = "0192f1e4-7a3b-7c2d-9e8f-123456789abc"

func newAllowlistPlatform(t *testing.T, ext func(*RouteDeps) platpkg.ExtensionPoints) *platpkg.PlatformCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyMachineTokens, Default: true})
	reg.Register(features.Definition{Key: "rbac", Default: true})

	core := platpkg.NewPlatformCore(
		map[string]any{
			"namespace":    "test-ns",
			"database_url": platformCoreDBURL(t),
		},
		ext(&RouteDeps{FeatureRegistry: reg, IsRBACAuthority: true}),
	)
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default())).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	return core
}

func TestAPIKeyRoutesAreExactlyTheAllowlist(t *testing.T) {
	t.Parallel()

	got := platpkg.APIKeyRoutePatterns()
	want := []string{
		"/api/v1/platform/",
	}
	if !slices.Equal(got, want) {
		t.Errorf("routes open to API keys = %v, want %v", got, want)
	}
}

func TestAPIKeyRoutesAreRegistered(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	newAllowlistPlatform(t, func(deps *RouteDeps) platpkg.ExtensionPoints {
		return platpkg.ExtensionPoints{RegisterRoutes: func(_ *http.ServeMux, core *platpkg.PlatformCore) {
			RegisterRoutesWithDeps(mux, core, deps)
		}}
	})

	patterns := platpkg.APIKeyRoutePatterns()
	if len(patterns) == 0 {
		t.Fatal("API-key allowlist is empty")
	}
	for _, pattern := range patterns {
		method, path, hasMethod := strings.Cut(pattern, " ")
		if !hasMethod {
			continue
		}
		concrete := pathWildcard.ReplaceAllString(path, allowlistPathID)
		_, got := mux.Handler(httptest.NewRequest(method, concrete, nil))
		if got != pattern {
			t.Errorf("%s resolves to %q, want the allowlisted pattern", pattern, got)
		}
	}
}

func newPresentedKeyPlatform(t *testing.T, scopes []string) *platpkg.PlatformCore {
	t.Helper()
	authority := &middlewareKeyAuthority{resolution: apikey.Resolution{
		KeyID:    "key-1",
		OwnerID:  "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77",
		OwnerSub: "system:ci-deployer",
		Scopes:   scopes,
	}}
	return newAllowlistPlatform(t, func(deps *RouteDeps) platpkg.ExtensionPoints {
		return platpkg.ExtensionPoints{
			RegisterRoutes: func(mux *http.ServeMux, core *platpkg.PlatformCore) {
				RegisterRoutesWithDeps(mux, core, deps)
			},
			AuthMiddleware: NewAuthMiddleware(&countingAuthenticator{}, nil, authority, slog.Default()),
		}
	})
}

func TestPlatformHandler_PresentedKeyReachesOnlyTheAllowlist(t *testing.T) {
	t.Parallel()

	core := newPresentedKeyPlatform(t, []string{"*:*:*"})

	for _, tc := range []struct {
		method, path string
		admitted     bool
	}{
		{http.MethodGet, "/api/v1/platform/agents", true},
		{http.MethodGet, "/api/v1/platform/evaluations/runs/" + allowlistPathID, true},
		{http.MethodPost, "/api/v1/platform/evaluations/experiments/" + allowlistPathID + "/trigger", true},
		{http.MethodGet, "/api/v1/platform/evaluations/runs/" + allowlistPathID + "/results", true},
		{http.MethodPost, "/api/v1/platform/evaluations/runs/" + allowlistPathID + "/cancel", true},
		{http.MethodGet, "/api/v1/platform/evaluations/runResults/" + allowlistPathID + "/executionData", true},
		{http.MethodGet, "/api/v1/platform/evaluations/runResults/" + allowlistPathID + "/taskEvents", true},
		{http.MethodGet, "/api/v1/platform/evaluations/experiments", true},
		{http.MethodGet, "/api/v1/platform/user/capabilities", true},
		{http.MethodPost, "/api/v1/platform/agentDeployments", true},
		{http.MethodPut, "/api/v1/platform/webuiSettings", true},
		{http.MethodGet, "/api/v2/platform/health", true},
		{http.MethodGet, "/api/v2/platform/version", true},
		{http.MethodGet, "/api/v1/platform/remoteAgents", true},
		{http.MethodPost, "/api/v1/platform/aiAssistant/generateAgent", false},
		{http.MethodPost, "/api/internal/platform/aiAssistant/generateAgent", false},
		{http.MethodPost, "/api/internal/platform/builder/sessions/" + allowlistPathID + "/test-agent", false},
		{http.MethodGet, "/api/internal/platform/builder/sessions/" + allowlistPathID + "/test-agent", false},
		{http.MethodDelete, "/api/internal/platform/builder/sessions/" + allowlistPathID + "/test-agent", false},
		{http.MethodGet, "/api/internal/platform/builder/test-agents/lookup", false},
		{http.MethodGet, "/api/v1/platform/users", true},
		{http.MethodGet, "/api/v1/platform/evaluations/runs/" + allowlistPathID + "/export", true},
		{http.MethodDelete, "/api/v1/platform/evaluations/runs/" + allowlistPathID, true},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := keyRequest(tc.path)
			req.Method = tc.method
			resp := httptest.NewRecorder()
			core.Handler().ServeHTTP(resp, req)

			if tc.admitted && strings.HasPrefix(tc.path, "/api/v1/platform/") {
				if resp.Code != http.StatusGone || !strings.Contains(resp.Body.String(), platpkg.UpgradeCLIMessage) {
					t.Errorf("removed route status = %d, want 410 with the upgrade message; body=%s", resp.Code, resp.Body.String())
				}
			}
			refused := resp.Code == http.StatusForbidden && strings.Contains(resp.Body.String(), "API keys can only call the Platform configuration API")
			if refused == tc.admitted {
				t.Errorf("status = %d, admitted = %v; body=%s", resp.Code, tc.admitted, resp.Body.String())
			}
		})
	}
}

func TestPlatformHandler_ScopelessKeyIsRefusedByEachRoutesOwnGate(t *testing.T) {
	t.Parallel()

	core := newPresentedKeyPlatform(t, nil)
	openReads := map[string]bool{
		"GET /api/v1/platform/models":            true,
		"GET /api/v1/platform/user/capabilities": true,
	}

	for _, pattern := range platpkg.APIKeyRoutePatterns() {
		if !strings.Contains(pattern, " ") {
			continue
		}
		t.Run(pattern, func(t *testing.T) {
			method, path, _ := strings.Cut(pattern, " ")
			req := keyRequest(pathWildcard.ReplaceAllString(path, allowlistPathID))
			req.Method = method
			resp := httptest.NewRecorder()
			core.Handler().ServeHTTP(resp, req)

			body := resp.Body.String()
			switch {
			case openReads[pattern] && resp.Code == http.StatusForbidden:
				t.Errorf("status = 403, want the open read to answer a scopeless key; body=%s", body)
			case !openReads[pattern] && resp.Code != http.StatusForbidden:
				t.Errorf("status = %d, want 403 from the route's own gate; body=%s", resp.Code, body)
			case !openReads[pattern] && strings.Contains(body, "API keys can only call the Platform configuration API"):
				t.Errorf("refused by the allowlist, want the route's own gate; body=%s", body)
			}
		})
	}
}
