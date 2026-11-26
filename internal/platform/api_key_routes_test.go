// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestHandlerRestrictsAPIKeysToTheAllowlist(t *testing.T) {
	t.Parallel()

	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	const openedRoute = "GET /api/v1/platform/opened"
	newCore := func(t *testing.T, authMethod string) *PlatformCore {
		t.Helper()
		identity := auth.NewTestSubject(auth.SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: authMethod, Scopes: []string{"*"}})
		core := NewPlatformCore(map[string]any{
			"namespace":    "test-ns",
			"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "platform.db"),
		}, ExtensionPoints{
			RegisterRoutes: func(mux *http.ServeMux, core *PlatformCore) {
				mux.HandleFunc(openedRoute, ok)
				core.AllowAPIKeys(openedRoute)
				mux.HandleFunc("GET /api/v1/platform/remoteAgents", ok)
			},
			AuthMiddleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
				})
			},
		})
		core.SetServices(runtime.NewRuntime(nil, nil, runtime.WithLogger(slog.New(slog.DiscardHandler))).Services())
		if err := core.Init(t.Context(), nil); err != nil {
			t.Fatalf("platform init: %v", err)
		}
		return core
	}

	cases := []struct {
		name       string
		authMethod string
		method     string
		path       string
		want       int
	}{
		{"key on a route an extension opened", auth.APIKeyAuthMethod, http.MethodGet, "/api/v1/platform/opened", http.StatusOK},
		{"key on an unlisted route", auth.APIKeyAuthMethod, http.MethodGet, "/api/v1/platform/remoteAgents", http.StatusForbidden},
		{"person on an unlisted route", "sam_access_token", http.MethodGet, "/api/v1/platform/remoteAgents", http.StatusOK},
		{"key on a removed route", auth.APIKeyAuthMethod, http.MethodGet, "/api/v1/platform/agents", http.StatusGone},
		{"key on a removed budget route", auth.APIKeyAuthMethod, http.MethodGet, "/api/v1/platform/budgets", http.StatusGone},
		{"person on a removed usage route", "sam_access_token", http.MethodGet, "/api/v1/platform/usage/summary", http.StatusGone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			newCore(t, tc.authMethod).Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
