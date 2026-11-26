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
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestRouteDeps_IdentityServiceWiring(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		withBrk   bool
		wantBuilt bool
	}{
		{"no broker — service skipped", false, false},
		{"broker present — service built + callback fires", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var brk broker.Broker
			if tc.withBrk {
				devBrk := broker.NewDevBroker()
				if err := devBrk.Connect(context.Background()); err != nil {
					t.Fatalf("connect broker: %v", err)
				}
				t.Cleanup(func() { _ = devBrk.Disconnect(context.Background()) })
				brk = devBrk
			}

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

			var callbackFired bool
			deps := &RouteDeps{
				OnIdentityServiceReady: func(_ identity.IdentityService) {
					callbackFired = true
				},
			}
			RegisterRoutesWithDeps(http.NewServeMux(), core, deps)

			gotBuilt := deps.IdentityService != nil
			if gotBuilt != tc.wantBuilt {
				t.Errorf("IdentityService built = %v, want %v", gotBuilt, tc.wantBuilt)
			}
			if callbackFired != tc.wantBuilt {
				t.Errorf("OnIdentityServiceReady fired = %v, want %v", callbackFired, tc.wantBuilt)
			}
		})
	}
}
