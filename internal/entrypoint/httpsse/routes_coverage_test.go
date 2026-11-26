// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func newCoverageAdapter(t *testing.T, configure ...func(*entrypoint.GatewayCore)) *HTTPSSEAdapter {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()

	appConfig := map[string]any{
		"namespace":          "test-ns",
		"session_secret_key": "test-secret",
		"gateway_id":         "test-gw",
		"fastapi_host":       "127.0.0.1",
		"fastapi_port":       0,
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": testDBURL(t, "coverage"),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
		"speech": map[string]any{
			"stt": map[string]any{
				"openai": map[string]any{
					"api_key": "sk-coverage-fake",
				},
			},
		},
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	memStore := memory.New()
	artSvc := &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	volSvc := volume.NewMemoryStore()

	featReg := features.NewRegistry()
	featReg.Register(features.Definition{Key: features.KeyObservabilityAgent, Name: "sentinel", Default: true})
	featReg.Register(features.Definition{Key: features.KeyActivityMonitor, Name: "activity", Default: true})
	featReg.Register(features.Definition{Key: "volumes", Name: "volumes", Default: true})
	featReg.Register(features.Definition{Key: featureKeyVoiceInteraction, Name: "voice", Default: true})

	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithArtifacts(artSvc),
		runtime.WithVolumes(volSvc),
		runtime.WithDefaultLLM(&mockLLMClient{response: "unused"}),
		runtime.WithFeatureRegistry(featReg),
		testProductAnalyticsOption(t, "sqlite:///"+t.TempDir()+"/product-analytics.db"),
	)
	core.SetServices(rt.Services())
	core.SetActivation(activation.NewService(activation.NewStore(t.TempDir()), nil, slog.Default()))
	for _, c := range configure {
		c(core)
	}

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	db := core.Store()
	if db == nil {
		t.Fatal("expected DB to be initialized")
	}
	t.Cleanup(func() { _ = db.Close() })

	return adapter
}

func TestRoutesAreGatedOrPublic(t *testing.T) {
	adapter := newCoverageAdapter(t)

	routes := adapter.Routes()
	if len(routes) == 0 {
		t.Fatal("expected adapter.Routes() to be populated; got empty registry")
	}

	for _, r := range routes {
		if r.Scope == "" && r.IsPublic == "" {
			t.Errorf("route %s %s has no Scope and no IsPublic reason — gate it with scoped(), or declare why it is safe (publicReasonOwnerScoped / publicReasonActivityScoped / another documented reason)", r.Method, r.Path)
		}
	}
}

func TestActivityRoutesAreScopeGated(t *testing.T) {
	adapter := newCoverageAdapter(t)
	seen := 0
	for _, r := range adapter.Routes() {
		if !strings.HasPrefix(r.Path, "/api/v1/activity/") {
			continue
		}
		seen++
		if r.Scope == rbac.ScopeAgentInvokeAny {
			t.Errorf("activity route %s %s is gated on ScopeAgentInvokeAny — that wildcard is an internal system-identity grant, not a route gate; ordinary users would 403",
				r.Method, r.Path)
		}
		if r.IsPublic != publicReasonActivityScoped {
			t.Errorf("activity route %s %s IsPublic = %q, want %q (dynamic own/cross-user authz in the handler, no route scope)",
				r.Method, r.Path, r.IsPublic, publicReasonActivityScoped)
		}
	}
	if seen == 0 {
		t.Fatal("no /api/v1/activity/* routes registered — coverage adapter did not wire the activity surface")
	}
}

func TestRoutesRegistryNoDuplicates(t *testing.T) {
	adapter := newCoverageAdapter(t)
	seen := make(map[string]bool)
	for _, r := range adapter.Routes() {
		key := r.Method + " " + r.Path
		if seen[key] {
			t.Errorf("duplicate route registration: %s", key)
		}
		seen[key] = true
	}
}

func TestPublicReasonAuthSkipRoutesAreInSkipPaths(t *testing.T) {
	adapter := newCoverageAdapter(t)
	for _, r := range adapter.Routes() {
		if r.IsPublic != publicReasonAuthSkip {
			continue
		}
		if !auth.SkipPaths[r.Path] {
			t.Errorf("route %s %s declares IsPublic=publicReasonAuthSkip but path is not in auth.SkipPaths — runtime auth gate will reject it", r.Method, r.Path)
		}
	}
}

func TestRoutesRegistryWellFormed(t *testing.T) {
	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
	}
	adapter := newCoverageAdapter(t)
	for _, r := range adapter.Routes() {
		if !validMethods[r.Method] {
			t.Errorf("route %q has unrecognized method %q", r.Path, r.Method)
		}
		if !strings.HasPrefix(r.Path, "/") {
			t.Errorf("route %s %q must start with '/'", r.Method, r.Path)
		}
		if r.Scope != "" && r.IsPublic != "" {
			t.Errorf("route %s %s declares both Scope=%q and IsPublic=%q — pick one", r.Method, r.Path, r.Scope, r.IsPublic)
		}
	}
}

func TestGetGrandfatherDoesNotGrow(t *testing.T) {
	const ceiling = 3

	adapter := newCoverageAdapter(t)
	var grandfathered []string
	for _, r := range adapter.Routes() {
		if r.IsPublic == publicReasonGetNoScopeDeclared {
			grandfathered = append(grandfathered, r.Method+" "+r.Path)
		}
	}
	if len(grandfathered) > ceiling {
		t.Errorf("%d routes carry publicReasonGetNoScopeDeclared, ceiling is %d — a new read route must declare real authorization, not the grandfather:\n  %s",
			len(grandfathered), ceiling, strings.Join(grandfathered, "\n  "))
	}
}

func TestAuthenticatedOnlyRoutesDoNotGrow(t *testing.T) {
	const ceiling = 53

	adapter := newCoverageAdapter(t)
	var grandfathered []string
	for _, r := range adapter.Routes() {
		if r.IsPublic == publicReasonAuthenticatedOnly {
			grandfathered = append(grandfathered, r.Method+" "+r.Path)
		}
	}
	if len(grandfathered) > ceiling {
		t.Errorf("%d routes carry publicReasonAuthenticatedOnly, ceiling is %d — a new route must declare a Scope or a per-caller marker rather than join this set; if it truly has to, raise the ceiling here and say why:\n  %s",
			len(grandfathered), ceiling, strings.Join(grandfathered, "\n  "))
	}
}

func TestCoverageAdapterWiresConditionalBlocks(t *testing.T) {
	conditional := map[string]string{
		"/api/v1/volumes":        "feature flag \"volumes\" plus a non-nil Volumes service",
		"/api/v1/activity/":      "feature flag KeyActivityMonitor",
		"/api/v1/analytics":      "feature flag KeyObservabilityAgent",
		"/api/v1/artifacts":      "a non-nil Artifacts service",
		"/api/v1/speech/stt":     "feature flag voice_interaction plus a speech config block whose credentials parse as valid",
		"/api/v1/scheduledTasks": "scheduler_service.enabled",
		"/api/v1/tasks":          "a configured session_service (DB-backed routes)",
	}

	adapter := newCoverageAdapter(t)
	routes := adapter.Routes()
	for prefix, why := range conditional {
		found := false
		for _, r := range routes {
			if strings.HasPrefix(r.Path, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no route under %s reached the registry — that block is gated on %s, so the coverage invariant is not examining it; wire it in newCoverageAdapter",
				prefix, why)
		}
	}
}
