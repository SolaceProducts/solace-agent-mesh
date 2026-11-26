// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func registerRoutesOnDevBroker(t *testing.T, brk broker.Broker, deps *RouteDeps) *http.ServeMux {
	t.Helper()
	core := platpkg.NewPlatformCore(
		map[string]any{
			"namespace":    "test-ns",
			"database_url": platformCoreDBURL(t),
		},
		platpkg.ExtensionPoints{},
	)
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	mux := http.NewServeMux()
	RegisterRoutesWithDeps(mux, core, deps)
	return mux
}

func legacyPythonModelBootstrapRegistry(t *testing.T, enabled bool) *features.Registry {
	t.Helper()
	t.Setenv("SAM_FEATURE_LEGACY_PYTHON_MODEL_BOOTSTRAP", "")
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyLegacyPythonModelBootstrap, Default: enabled})
	return reg
}

func TestRegisterRoutes_ModelBootstrapListenerNeedsLegacyFlag(t *testing.T) {
	tests := []struct {
		name         string
		registry     func(t *testing.T) *features.Registry
		wantListener bool
	}{
		{name: "no registry", registry: func(*testing.T) *features.Registry { return nil }},
		{name: "flag off", registry: func(t *testing.T) *features.Registry { return legacyPythonModelBootstrapRegistry(t, false) }},
		{name: "flag on", registry: func(t *testing.T) *features.Registry { return legacyPythonModelBootstrapRegistry(t, true) }, wantListener: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := &RouteDeps{FeatureRegistry: tc.registry(t)}
			registerRoutesOnDevBroker(t, connectedDevBroker(t), deps)
			gotListener := false
			for _, svc := range deps.BackgroundServices {
				if _, ok := svc.(*ModelConfigBootstrapListener); ok {
					gotListener = true
				}
			}
			if gotListener != tc.wantListener {
				t.Errorf("bootstrap listener registered = %v, want %v", gotListener, tc.wantListener)
			}
		})
	}
}
