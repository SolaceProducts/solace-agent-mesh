// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestSSRF_ProductionRouteWiring(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	core, _, deps := newProductionPlatform(t)

	t.Run("control-plane detectAuth action", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"specUrl": target.srv.URL + "/spec.json"})
		_, err := NewManageablePlatform(core, deps, slog.Default()).
			handleConnectorManagement(t.Context(), "post", []string{"openapi", "detectAuth"}, body, nil)
		ext, ok := errors.AsType[*ExternalServiceError](err)
		if !ok || ext.Message != specBlockedMessage {
			t.Fatalf("err = %T %v, want the blocked-target ExternalServiceError", err, err)
		}
	})

	t.Run("control-plane parse action", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"specUrl": target.srv.URL + "/spec.json"})
		_, err := NewManageablePlatform(core, deps, slog.Default()).
			handleConnectorManagement(t.Context(), "post", []string{"openapi", "parse"}, body, nil)
		ext, ok := errors.AsType[*ExternalServiceError](err)
		if !ok || ext.Message != specBlockedMessage {
			t.Fatalf("err = %T %v, want the blocked-target ExternalServiceError", err, err)
		}
	})

	if hits := target.hits.Load(); hits != 0 {
		t.Fatalf("internal target received %d request(s)", hits)
	}
}

func newProductionPlatform(t *testing.T) (*platpkg.PlatformCore, *http.ServeMux, *RouteDeps) {
	t.Helper()
	core := platpkg.NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": platformCoreDBURL(t),
	}, platpkg.ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(nil, nil, runtime.WithLogger(slog.Default())).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.DB().Close() })

	mux := http.NewServeMux()
	deps := &RouteDeps{}
	RegisterRoutesWithDeps(mux, core, deps)
	return core, mux, deps
}
