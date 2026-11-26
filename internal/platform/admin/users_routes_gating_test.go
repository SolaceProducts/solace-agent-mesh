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

func TestUserUpsertHandler_RunsWhateverTheRBACFlag(t *testing.T) {
	t.Parallel()

	for _, flagOn := range []bool{false, true} {
		brk := broker.NewDevBroker()
		if err := brk.Connect(context.Background()); err != nil {
			t.Fatalf("connect broker: %v", err)
		}
		t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

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

		reg := features.NewRegistry()
		reg.Register(features.Definition{Key: "rbac", Default: flagOn})

		deps := &RouteDeps{IsRBACAuthority: true, FeatureRegistry: reg}
		RegisterRoutesWithDeps(http.NewServeMux(), core, deps)

		foundHandler := false
		for _, s := range deps.BackgroundServices {
			if _, ok := s.(*UserUpsertHandler); ok {
				foundHandler = true
				break
			}
		}
		if !foundHandler {
			t.Errorf("UserUpsertHandler not registered (flagOn=%v); the claim projection a chat entrypoint resolves against would never be written", flagOn)
		}
	}
}
