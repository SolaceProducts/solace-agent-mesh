// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestAPIKeyRoutesAreOnlyTheVersionProbe(t *testing.T) {
	got := slices.Sorted(maps.Keys(newCoverageAdapter(t).apiKeyRoutes()))
	want := []string{"GET /api/v1/version"}
	if !slices.Equal(got, want) {
		t.Errorf("routes open to API keys = %v, want %v", got, want)
	}
}

type stampingAuthProvider struct{ identity *auth.Subject }

func (p stampingAuthProvider) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), p.identity)))
		})
	}
}
func (stampingAuthProvider) RegisterRoutes(*http.ServeMux) {}
func (stampingAuthProvider) ProviderType() string          { return auth.ProviderTypeEnterprise }

func TestHandlerRestrictsAPIKeys(t *testing.T) {
	const providerName = "stamping-fake"
	identity := auth.NewTestSubject(auth.SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: auth.APIKeyAuthMethod, Scopes: []string{"*"}})
	auth.RegisterProviderFactory("test-gw:"+providerName, func(map[string]any) (auth.AuthProvider, error) {
		return stampingAuthProvider{identity: identity}, nil
	})
	t.Cleanup(func() { auth.UnregisterProviderFactory("test-gw:" + providerName) })

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	logs := &safeBuffer{}
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })
	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":                  "test-ns",
		"session_secret_key":         "test-secret",
		"gateway_id":                 "test-gw",
		"fastapi_host":               "127.0.0.1",
		"fastapi_port":               0,
		"frontend_use_authorization": true,
		"external_auth_provider":     providerName,
		"session_service":            map[string]any{"type": "sql", "database_url": testDBURL(t, "apikey")},
		"scheduler_service":          map[string]any{"enabled": true},
	})
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.New(slog.NewTextHandler(logs, nil))),
		runtime.WithArtifacts(&artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "test-ns"}),
	).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Store().Close() })
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/v1/version", http.StatusOK},
		{http.MethodGet, "/api/v1/version/", http.StatusOK},
		{http.MethodGet, "/api/v1/agentCards", http.StatusForbidden},
		{http.MethodGet, "/api/v1/user", http.StatusForbidden},
		{http.MethodPost, "/api/v1/message:stream", http.StatusForbidden},
		{http.MethodGet, "/api/v1/artifacts/scheduled/0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b/versions", http.StatusForbidden},
		{http.MethodGet, "/api/v1/artifacts/scheduled/0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b/report.txt", http.StatusNotFound},
		{http.MethodGet, "/api/v1/scheduledTasks/executions/0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", http.StatusNotFound},
		{http.MethodDelete, "/api/v1/scheduledTasks/executions/0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", http.StatusNotFound},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s: status = %d, want %d; body=%s", tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
		}
		if tc.want == http.StatusNotFound && !strings.HasPrefix(rec.Body.String(), "404 page not found") {
			t.Errorf("%s %s: body = %q, want the mux's own 404", tc.method, tc.path, rec.Body.String())
		}
	}
	if got := strings.Count(logs.String(), "task submit refused before the handler"); got != 1 {
		t.Errorf("refused submits reported = %d, want 1 (POST /api/v1/message:stream only)", got)
	}
}
