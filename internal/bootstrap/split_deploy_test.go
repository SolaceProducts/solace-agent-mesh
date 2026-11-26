// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestSplit_PlatformAlone(t *testing.T) {
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	fake := &fakePlatformMountable{}
	MountPlatform(proxy, fake)
	fake.onReady(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "platform:%s", r.URL.Path)
	}))

	for _, tc := range []struct {
		path     string
		wantCode int
		wantBody string
	}{
		{"/api/v1/platform/health", http.StatusOK, "platform:/api/v1/platform/health"},
		{"/gw/some-gateway/hook", http.StatusNotFound, ""},
	} {
		rec := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.wantCode {
			t.Errorf("%s: status %d, want %d", tc.path, rec.Code, tc.wantCode)
		}
		if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
			t.Errorf("%s: body %q, want %q", tc.path, rec.Body.String(), tc.wantBody)
		}
	}
}

func TestSplit_GatewayAlone(t *testing.T) {
	root := &fakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountAtPrefix, Prefix: "/"},
		body:   "root-handler",
	}
	exe := buildExecutor(t, map[string]entrypoint.GatewayAdapter{"web": root})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}
	root.fire()

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "root-handler" {
		t.Errorf("root: got %d %q, want 200 %q", rec.Code, rec.Body.String(), "root-handler")
	}

	rec = httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/health", nil))
	if rec.Body.String() != "root-handler" {
		t.Errorf("platform-shaped path without platform mount: body %q, want %q",
			rec.Body.String(), "root-handler")
	}
}

func TestSplit_GatewayAlone_NoRoot(t *testing.T) {
	named := &fakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountNamed},
		body:   "teams-handler",
	}
	exe := buildExecutor(t, map[string]entrypoint.GatewayAdapter{"teams": named})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}
	named.fire()

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gw/teams/webhook", nil))
	if rec.Body.String() != "teams-handler" {
		t.Errorf("named: body %q, want %q", rec.Body.String(), "teams-handler")
	}

	rec = httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unmounted platform prefix: status %d, want 503", rec.Code)
	}
}

func TestSplit_Combined(t *testing.T) {
	root := &fakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountAtPrefix, Prefix: "/"},
		body:   "root",
	}
	named := &fakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountNamed},
		body:   "teams",
	}
	exe := buildExecutor(t, map[string]entrypoint.GatewayAdapter{
		"web":   root,
		"teams": named,
	})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}
	root.fire()
	named.fire()

	plat := &fakePlatformMountable{}
	MountPlatform(proxy, plat)
	plat.onReady(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "platform")
	}))

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/", "root"},
		{"/some-spa-route", "root"},
		{"/gw/teams/webhook", "teams"},
		{"/api/v1/platform/health", "platform"},
		{"/api/v1/control/apps", "root"},
	} {
		rec := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := rec.Body.String(); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSplit_NeitherMounted(t *testing.T) {
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/health: status %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/platform/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/api/v1/platform/health with nothing mounted: status %d, want 503", rec.Code)
	}
}
