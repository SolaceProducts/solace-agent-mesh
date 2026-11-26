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
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestMetricsMiddleware_RecordsRealRouteTemplate(t *testing.T) {
	reg, err := observability.NewRegistry(observability.Config{
		Enabled:      true,
		MetricPrefix: "sam",
		Path:         "/metrics",
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	brk := broker.NewDevBroker()
	if connErr := brk.Connect(t.Context()); connErr != nil {
		t.Fatal(connErr)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	appConfig := map[string]any{
		"namespace":    "test-ns",
		"fastapi_host": "127.0.0.1",
		"fastapi_port": 0,
		"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "platform.db"),
	}
	core := NewPlatformCore(appConfig, ExtensionPoints{})

	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithMetrics(reg),
	)
	core.SetServices(rt.Services())
	if initErr := core.Init(t.Context(), nil); initErr != nil {
		t.Fatal(initErr)
	}

	srv := httptest.NewServer(core.Handler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/v1/platform/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	got := grepLines(rec.Body.String(), "entrypoint_requests")

	if !strings.Contains(got, `route_template="/api/v1/platform/health"`) {
		t.Errorf("expected entrypoint.requests labeled with the real route template; got:\n%s", got)
	}
	if strings.Contains(got, `route_template="unmatched"`) {
		t.Errorf("regression: entrypoint.requests labeled route_template=\"unmatched\" for a registered route:\n%s", got)
	}
}

func grepLines(s, substr string) string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(ln, substr) {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}
