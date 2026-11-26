// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	samauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type webUIRoute struct {
	method, suffix, scope string
}

var webUIRoutes = []webUIRoute{
	{http.MethodPost, "/builder/sessions/" + allowlistPathID + "/test-agent", rbac.ScopeAgentBuilderTest},
	{http.MethodGet, "/builder/sessions/" + allowlistPathID + "/test-agent", rbac.ScopeAgentBuilderRead},
	{http.MethodDelete, "/builder/sessions/" + allowlistPathID + "/test-agent", rbac.ScopeAgentBuilderTest},
	{http.MethodGet, "/builder/test-agents/lookup?name=__test__abc__def", rbac.ScopeAgentBuilderRead},
	{http.MethodPost, "/aiAssistant/generateAgent", rbac.ScopeAgentBuilderCreate},
}

func TestWebUIRoutes_AreGatedOnEveryPrefix(t *testing.T) {
	t.Parallel()

	withoutScopes := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject := auth.NewTestSubject(auth.SubjectFields{UserID: "someone", Authenticated: true})
			next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), subject)))
		})
	}
	core := newAllowlistPlatform(t, func(deps *RouteDeps) platpkg.ExtensionPoints {
		return platpkg.ExtensionPoints{
			RegisterRoutes: func(mux *http.ServeMux, core *platpkg.PlatformCore) {
				RegisterRoutesWithDeps(mux, core, deps)
			},
			AuthMiddleware: withoutScopes,
		}
	})

	for _, route := range webUIRoutes {
		for _, prefix := range []string{platpkg.V1Prefix, platpkg.InternalPrefix} {
			t.Run(route.method+" "+prefix+route.suffix, func(t *testing.T) {
				req := httptest.NewRequest(route.method, prefix+route.suffix, strings.NewReader("{}"))
				req.Header.Set("Content-Type", "application/json")
				resp := httptest.NewRecorder()
				core.Handler().ServeHTTP(resp, req)

				if resp.Code != http.StatusForbidden {
					t.Errorf("status = %d, want 403 from the route's scope gate; body=%s", resp.Code, resp.Body.String())
				}
			})
		}
	}
}

func TestWebUIRoutes_AdmitTheirExactScopeThroughTheAuthMiddleware(t *testing.T) {
	t.Parallel()

	authenticator := &countingAuthenticator{id: &samauth.Identity{UserID: "someone"}}
	core := newAllowlistPlatform(t, func(deps *RouteDeps) platpkg.ExtensionPoints {
		return platpkg.ExtensionPoints{
			RegisterRoutes: func(mux *http.ServeMux, core *platpkg.PlatformCore) {
				RegisterRoutesWithDeps(mux, core, deps)
			},
			AuthMiddleware: NewAuthMiddleware(authenticator, nil, nil, slog.Default()),
		}
	})

	for _, route := range webUIRoutes {
		for _, prefix := range []string{platpkg.V1Prefix, platpkg.InternalPrefix} {
			for _, tc := range []struct {
				name      string
				scope     string
				forbidden bool
			}{
				{"holds exactly the route's scope", route.scope, false},
				{"holds a scope the route does not take", rbac.ScopeAgentBuilderDelete, true},
			} {
				t.Run(route.method+" "+prefix+route.suffix+" "+tc.name, func(t *testing.T) {
					authenticator.id = &samauth.Identity{UserID: "someone", Scopes: []string{tc.scope}}
					req := httptest.NewRequest(route.method, prefix+route.suffix, strings.NewReader("{}"))
					req.Header.Set("Authorization", "Bearer session-token")
					req.Header.Set("Content-Type", "application/json")
					resp := httptest.NewRecorder()
					core.Handler().ServeHTTP(resp, req)

					gated := resp.Code == http.StatusForbidden
					if gated != tc.forbidden || resp.Code == http.StatusUnauthorized {
						t.Errorf("status = %d, want forbidden=%v; body=%s", resp.Code, tc.forbidden, resp.Body.String())
					}
				})
			}
		}
	}
}
