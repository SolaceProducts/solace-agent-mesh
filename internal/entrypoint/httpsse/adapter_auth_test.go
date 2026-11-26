// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestAdapterInit_FailsLoudWhenEnterpriseProviderMissing(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()
	appConfig := map[string]any{
		"namespace":                  "test-ns",
		"session_secret_key":         "test-secret",
		"gateway_id":                 "test-gw",
		"fastapi_host":               "127.0.0.1",
		"fastapi_port":               0,
		"frontend_use_authorization": true,
		"external_auth_provider":     "enterprise",
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())

	err := core.Init(context.Background(), nil)
	if err == nil {
		t.Fatal("Init must return an error when enterprise provider is missing — silent Noop fallback is a security regression")
	}
	if !strings.Contains(err.Error(), "enterprise") {
		t.Errorf("error should mention the provider type: %v", err)
	}
}

func TestCreateAuthProvider_NoopResolvesDevScopes(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)
	core.SetScopeResolver(scopeResolverFunc(func(string, []string) []string {
		return []string{"agent:Orchestrator:invoke"}
	}))

	p, err := adapter.createAuthProvider(&entrypoint.GatewayConfig{}, core)
	if err != nil {
		t.Fatalf("createAuthProvider: %v", err)
	}

	var got *auth.Subject
	h := p.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))

	if got == nil {
		t.Fatal("no identity injected by the noop provider")
	}
	if len(got.Scopes()) != 1 || got.Scopes()[0] != "agent:Orchestrator:invoke" {
		t.Errorf("dev identity should carry resolver-resolved scopes, got %v", got.Scopes())
	}

	fallback, err := adapter.createAuthProvider(&entrypoint.GatewayConfig{FrontendUseAuthorization: true}, core)
	if err != nil {
		t.Fatalf("createAuthProvider (no provider): %v", err)
	}
	if fallback.ProviderType() != auth.ProviderTypeNoop {
		t.Errorf("auth-on-no-provider should fall back to noop, got %q", fallback.ProviderType())
	}
}

func TestCreateAuthProvider_NoopFailsClosedWhenEnforcedWithoutResolver(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)
	core.SetAuthzEnabled(true)

	p, err := adapter.createAuthProvider(&entrypoint.GatewayConfig{}, core)
	if err != nil {
		t.Fatalf("createAuthProvider: %v", err)
	}

	var got *auth.Subject
	h := p.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))

	if got == nil {
		t.Fatal("no identity injected by the noop provider")
	}
	if len(got.Scopes()) != 0 {
		t.Errorf("enforced + no resolver must fail closed (empty scopes), got %v", got.Scopes())
	}
}

func TestCreateAuthProvider_NoopKeepsWildcardWhenNotEnforced(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	p, err := adapter.createAuthProvider(&entrypoint.GatewayConfig{}, core)
	if err != nil {
		t.Fatalf("createAuthProvider: %v", err)
	}

	var got *auth.Subject
	h := p.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))

	if got == nil {
		t.Fatal("no identity injected by the noop provider")
	}
	if len(got.Scopes()) != 1 || got.Scopes()[0] != "*" {
		t.Errorf("auth off + no resolver + not enforced must keep DevIdentity's \"*\", got %v", got.Scopes())
	}
}
