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
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestRouteDeps_AuthzLookupHandler_AuthorityGate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		isAuthority bool
		wantHandler bool
	}{
		{"non-authoritative pod skips handler", false, false},
		{"authoritative pod starts handler", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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

			deps := &RouteDeps{
				IsRBACAuthority: tc.isAuthority,
			}
			RegisterRoutesWithDeps(http.NewServeMux(), core, deps)

			got := deps.AuthzLookupHandler != nil
			if got != tc.wantHandler {
				t.Errorf("AuthzLookupHandler started = %v, want %v (IsRBACAuthority=%v)",
					got, tc.wantHandler, tc.isAuthority)
			}

			foundInBG := false
			for _, s := range deps.BackgroundServices {
				if _, ok := s.(*AuthzLookupHandler); ok {
					foundInBG = true
					break
				}
			}
			if foundInBG != tc.wantHandler {
				t.Errorf("AuthzLookupHandler registered in BackgroundServices = %v, want %v",
					foundInBG, tc.wantHandler)
			}
		})
	}
}
