// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func registryFromOpts(opts []runtime.Option) *observability.Registry {
	rt := runtime.NewRuntime(nil, nil, opts...)
	if reg, ok := rt.Services().Metrics().(*observability.Registry); ok {
		return reg
	}
	return nil
}

type fakePlatformMountable struct {
	onReady func(http.Handler)
}

func (p *fakePlatformMountable) SetOnHandlerReady(cb func(http.Handler)) { p.onReady = cb }

func TestMountPlatform_RegistersPlatformPrefixOnly(t *testing.T) {
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())
	fake := &fakePlatformMountable{}
	MountPlatform(proxy, fake)

	if fake.onReady == nil {
		t.Fatal("MountPlatform must register an on-handler-ready callback")
	}

	platformHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "platform:%s", r.URL.Path)
	})
	fake.onReady(platformHandler)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/api/v1/platform/health", "platform:/api/v1/platform/health"},
		{"/api/v1/platform/agents", "platform:/api/v1/platform/agents"},
		{"/api/v2/platform/agents", "platform:/api/v2/platform/agents"},
		{"/api/v2/openapi.yaml", "platform:/api/v2/openapi.yaml"},
		{"/api/internal/platform/builder/builds", "platform:/api/internal/platform/builder/builds"},
		{"/api/internal/platform/aiAssistant/generateAgent", "platform:/api/internal/platform/aiAssistant/generateAgent"},
		{"/redoc", "platform:/redoc"},
		{"/redoc/redoc.standalone.js", "platform:/redoc/redoc.standalone.js"},
		{"/redoc/stylesheet.css", "platform:/redoc/stylesheet.css"},
	} {
		rec := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := rec.Body.String(); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.path, got, tc.want)
		}
	}

	for _, path := range []string{"/api/v1/control/apps", "/api/v1/control"} {
		rec := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if strings.HasPrefix(rec.Body.String(), "platform:") {
			t.Errorf("%s: reached the platform handler (%q), want the control plane unmounted", path, rec.Body.String())
		}
	}

	for _, path := range []string{"/api/v2/sessions", "/api/v2"} {
		rec := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if strings.HasPrefix(rec.Body.String(), "platform:") {
			t.Errorf("%s: reached the platform handler (%q), want only %s forwarded from the /api/v2 root", path, rec.Body.String(), "/api/v2/openapi.yaml")
		}
	}
}

func TestBuildPlatformRuntimeOptions_WiresObservability(t *testing.T) {
	app := samapp.AppEntry{
		Name: "platform",
		AppConfig: map[string]any{
			"management_server": map[string]any{
				"observability": map[string]any{
					"enabled": true,
				},
			},
		},
	}

	opts := BuildPlatformRuntimeOptions(context.Background(), nil, []samapp.AppEntry{app}, slog.Default())
	reg := registryFromOpts(opts)
	if reg == nil {
		t.Fatal("expected observability registry when config enables it, got none")
	}
}

func TestBuildPlatformRuntimeOptions_NoObservabilityWhenDisabled(t *testing.T) {
	app := samapp.AppEntry{
		Name:      "platform",
		AppConfig: map[string]any{},
	}

	opts := BuildPlatformRuntimeOptions(context.Background(), nil, []samapp.AppEntry{app}, slog.Default())
	if reg := registryFromOpts(opts); reg != nil {
		t.Errorf("expected no registry when observability not configured, got %v", reg)
	}
}

func TestMountPlatform_DoesNotRegisterMetrics(t *testing.T) {
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())
	fake := &fakePlatformMountable{}
	MountPlatform(proxy, fake)

	fake.onReady(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "platform")
	}))

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 — /metrics must not be served by the proxy; got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestMountPlatform_UnrelatedPathsBypassPlatform(t *testing.T) {
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))
	fake := &fakePlatformMountable{}
	MountPlatform(proxy, fake)
	fake.onReady(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "platform")
	}))

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other/path", nil))
	if got := rec.Body.String(); got != "primary" {
		t.Errorf("unrelated path should hit primary; got %q", got)
	}
}
