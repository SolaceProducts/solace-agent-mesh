// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func mountServices(t *testing.T) runtime.Services {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	return runtime.NewRuntime(brk, nil).Services()
}

type mountFakeAdapter struct {
	intent   entrypoint.MountSpec
	body     string
	callback func(http.Handler)
}

func (a *mountFakeAdapter) Init(context.Context, *entrypoint.GatewayCore) error { return nil }
func (a *mountFakeAdapter) Start(context.Context) error                         { return nil }
func (a *mountFakeAdapter) Stop(context.Context) error                          { return nil }
func (a *mountFakeAdapter) MountIntent() entrypoint.MountSpec                   { return a.intent }
func (a *mountFakeAdapter) SetOnHandlerReady(cb func(http.Handler))             { a.callback = cb }

func (a *mountFakeAdapter) fire() {
	if a.callback != nil {
		a.callback(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, a.body)
		}))
	}
}
func (a *mountFakeAdapter) OnStatusUpdate(context.Context, string, *a2a.TaskStatusUpdateEvent, []byte) {
}
func (a *mountFakeAdapter) OnArtifactUpdate(context.Context, string, *a2a.TaskArtifactUpdateEvent, []byte) {
}
func (a *mountFakeAdapter) OnFinalResponse(context.Context, string, *a2a.Task, []byte) {}
func (a *mountFakeAdapter) OnError(context.Context, string, *a2a.Error, []byte)        {}
func (a *mountFakeAdapter) OnAgentDiscovered(*a2a.AgentCard)                           {}
func (a *mountFakeAdapter) OnAgentRemoved(string)                                      {}

func buildMountExecutor(t *testing.T, adapters map[string]entrypoint.GatewayAdapter) *entrypoint.GatewayExecutor {
	t.Helper()
	svc := mountServices(t)
	factory := func(appConfig map[string]any, svc runtime.Services) (*entrypoint.GatewayCore, error) {
		name, _ := appConfig["__test_name"].(string)
		adapter := adapters[name]
		if adapter == nil {
			t.Fatalf("no adapter registered for %q", name)
		}
		core := entrypoint.NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	}
	exe := entrypoint.NewGatewayExecutor(svc, factory)
	for name := range adapters {
		if err := exe.LoadConfig(name, map[string]any{
			"namespace":   "test",
			"__test_name": name,
			"gateway_id":  name,
		}); err != nil {
			t.Fatalf("LoadConfig %q: %v", name, err)
		}
	}
	return exe
}

func TestMountExecutor_RootPrefixMountsAtRoot(t *testing.T) {
	root := &mountFakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountAtPrefix, Prefix: "/"},
		body:   "root-handler",
	}
	exe := buildMountExecutor(t, map[string]entrypoint.GatewayAdapter{"web": root})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}
	root.fire()

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if body := rec.Body.String(); body != "root-handler" {
		t.Errorf("root body = %q, want %q", body, "root-handler")
	}
}

func TestMountExecutor_NamedGoesUnderGwPrefix(t *testing.T) {
	teams := &mountFakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountNamed},
		body:   "teams-handler",
	}
	exe := buildMountExecutor(t, map[string]entrypoint.GatewayAdapter{"teams": teams})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}
	teams.fire()

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gw/teams/webhook", nil))
	if body := rec.Body.String(); body != "teams-handler" {
		t.Errorf("named body = %q, want %q", body, "teams-handler")
	}
}

func TestMountExecutor_NoneIsSkipped(t *testing.T) {
	exe := buildMountExecutor(t, map[string]entrypoint.GatewayAdapter{
		"slack": &mountFakeAdapter{
			intent: entrypoint.MountSpec{Kind: entrypoint.MountNone},
			body:   "should-not-mount",
		},
	})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())
	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gw/slack/events", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("MountNone adapter should not register; got %d", rec.Code)
	}
}

func TestMountExecutor_EmptyPrefixErrors(t *testing.T) {
	exe := buildMountExecutor(t, map[string]entrypoint.GatewayAdapter{
		"broken": &mountFakeAdapter{
			intent: entrypoint.MountSpec{Kind: entrypoint.MountAtPrefix, Prefix: ""},
			body:   "unreachable",
		},
	})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	err := entrypoint.MountExecutor(proxy, exe)
	if err == nil {
		t.Fatal("expected error for MountAtPrefix with empty Prefix")
	}
}

func TestMountExecutor_HandlerReadyAfterFire(t *testing.T) {
	deferred := &mountFakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountAtPrefix, Prefix: "/"},
		body:   "deferred-root",
	}
	exe := buildMountExecutor(t, map[string]entrypoint.GatewayAdapter{"web": deferred})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("before fire expected 503, got %d", rec.Code)
	}

	deferred.fire()

	rec = httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if body := rec.Body.String(); body != "deferred-root" {
		t.Errorf("after fire body = %q, want %q", body, "deferred-root")
	}
}

func TestMountExecutor_EmptyExecutor(t *testing.T) {
	svc := mountServices(t)
	exe := entrypoint.NewGatewayExecutor(svc, func(map[string]any, runtime.Services) (*entrypoint.GatewayCore, error) {
		t.Fatal("factory should not be invoked")
		return nil, nil
	})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor with zero gateways: %v", err)
	}
	if exe.OnGatewayReady == nil {
		t.Error("OnGatewayReady callback must be wired even with zero gateways")
	}
	if exe.OnGatewayRemoved == nil {
		t.Error("OnGatewayRemoved callback must be wired even with zero gateways")
	}
}

func TestMountExecutor_DynamicRegistration(t *testing.T) {
	svc := mountServices(t)
	exe := entrypoint.NewGatewayExecutor(svc, func(map[string]any, runtime.Services) (*entrypoint.GatewayCore, error) {
		t.Fatal("factory should not be invoked")
		return nil, nil
	})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())
	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}

	exe.OnGatewayReady("late", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "late-handler")
	}))

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gw/late/hello", nil))
	if body := rec.Body.String(); body != "late-handler" {
		t.Errorf("dynamic registration body = %q, want %q", body, "late-handler")
	}

	exe.OnGatewayRemoved("late")

	rec = httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gw/late/hello", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("after removal expected 404, got %d", rec.Code)
	}
}

func TestMountExecutor_PrefixRemovalUnregistersPrefix(t *testing.T) {
	root := &mountFakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountAtPrefix, Prefix: "/"},
		body:   "root-handler",
	}
	exe := buildMountExecutor(t, map[string]entrypoint.GatewayAdapter{"web": root})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}
	root.fire()

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if body := rec.Body.String(); body != "root-handler" {
		t.Fatalf("before remove body = %q, want %q", body, "root-handler")
	}

	exe.OnGatewayRemoved("web")

	rec = httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("after prefix removal expected 503 (no handler), got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestMountExecutor_NamedRemovalUnregistersNamed(t *testing.T) {
	teams := &mountFakeAdapter{
		intent: entrypoint.MountSpec{Kind: entrypoint.MountNamed},
		body:   "teams-handler",
	}
	exe := buildMountExecutor(t, map[string]entrypoint.GatewayAdapter{"teams": teams})
	proxy := entrypoint.NewGatewayProxy(":0", slog.Default())

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		t.Fatalf("MountExecutor: %v", err)
	}
	teams.fire()

	exe.OnGatewayRemoved("teams")

	rec := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gw/teams/webhook", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("after named removal expected 404, got %d", rec.Code)
	}
}
