// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func registerTestRoutes(t *testing.T) *RouteDeps {
	t.Helper()
	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": platformCoreDBURL(t)},
		platpkg.ExtensionPoints{},
	)
	rt := runtime.NewRuntime(connectedDevBroker(t), nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	deps := &RouteDeps{IsRBACAuthority: true}
	RegisterRoutesWithDeps(http.NewServeMux(), core, deps)
	return deps
}

func TestRegisterRoutes_RunsTheSTRRuntimeListenerOnTheEndpointRegistry(t *testing.T) {
	t.Parallel()
	deps := registerTestRoutes(t)

	var listeners []*STRRuntimeListener
	for _, svc := range deps.BackgroundServices {
		if l, ok := svc.(*STRRuntimeListener); ok {
			listeners = append(listeners, l)
		}
	}
	if len(listeners) != 1 {
		t.Fatalf("STR runtime listeners = %d, want 1", len(listeners))
	}
	if listeners[0].registry != deps.STRRuntimeRegistry {
		t.Error("the STR runtime listener fills a registry the build-target endpoint does not read")
	}
}
