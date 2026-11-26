// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func registerWithDeps(t *testing.T, deps *RouteDeps) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
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
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	RegisterRoutesWithDeps(http.NewServeMux(), core, deps)
}

func machineTokensRegistry(enabled bool) *features.Registry {
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyMachineTokens, Default: enabled})
	return reg
}

func TestRegisterRoutes_AlwaysResolvesTheKeyAuthorityHolder(t *testing.T) {
	for name, tc := range map[string]struct {
		deps    func(*apikey.LazyAuthority) *RouteDeps
		wantErr error
	}{
		"serving keys": {
			deps: func(holder *apikey.LazyAuthority) *RouteDeps {
				return &RouteDeps{
					IsRBACAuthority:   true,
					FeatureRegistry:   machineTokensRegistry(true),
					KeyAuthority:      holder,
					AuthorizationMode: authorizationModeDefaultRBAC,
				}
			},
			wantErr: apikey.ErrInvalidKey,
		},
		"feature off": {
			deps: func(holder *apikey.LazyAuthority) *RouteDeps {
				return &RouteDeps{
					IsRBACAuthority:   true,
					FeatureRegistry:   machineTokensRegistry(false),
					KeyAuthority:      holder,
					AuthorizationMode: authorizationModeDefaultRBAC,
				}
			},
			wantErr: apikey.ErrNotEnabled,
		},
		"not the rbac authority": {
			deps: func(holder *apikey.LazyAuthority) *RouteDeps {
				return &RouteDeps{
					IsRBACAuthority:   false,
					FeatureRegistry:   machineTokensRegistry(true),
					KeyAuthority:      holder,
					AuthorizationMode: authorizationModeDefaultRBAC,
				}
			},
			wantErr: apikey.ErrNotEnabled,
		},
		"no feature registry": {
			deps: func(holder *apikey.LazyAuthority) *RouteDeps {
				return &RouteDeps{IsRBACAuthority: true, KeyAuthority: holder}
			},
			wantErr: apikey.ErrNotEnabled,
		},
		"authorization mode unset": {
			deps: func(holder *apikey.LazyAuthority) *RouteDeps {
				return &RouteDeps{
					IsRBACAuthority: true,
					FeatureRegistry: machineTokensRegistry(true),
					KeyAuthority:    holder,
				}
			},
			wantErr: apikey.ErrAuthorizationModeDisabled,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			holder := apikey.NewLazyAuthority()
			registerWithDeps(t, tc.deps(holder))

			if !holder.Bound() {
				t.Fatal("route registration left the holder pending, so every key would answer 503 forever")
			}
			_, err := holder.Validate(t.Context(), "a-hash-no-key-has")
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Validate error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRegisterRoutes_NilKeyAuthorityHolderIsTolerated(t *testing.T) {
	t.Parallel()
	registerWithDeps(t, &RouteDeps{
		IsRBACAuthority:   true,
		FeatureRegistry:   machineTokensRegistry(true),
		AuthorizationMode: authorizationModeDefaultRBAC,
	})
}

func TestAPIKeyAuthority_NilOwnerResolverFailsClosedWithoutPanicking(t *testing.T) {
	ctx := t.Context()
	f := newAuthorityFixture(t, []string{"agent:*:invoke"})
	owner := f.seedSystemOwner(ctx, t, "system:ci-nobroker")
	f.seedKey(ctx, t, owner.ID, "nobroker-hash", []string{"agent:*:invoke"}, nil)

	authority := NewAPIKeyAuthority(
		f.keys, f.users, (*AuthzLookupHandler)(nil), authorizationModeDefaultRBAC, slog.Default())

	_, err := authority.Validate(ctx, "nobroker-hash")
	if err == nil {
		t.Fatal("a key validated with no owner-scope resolver wired")
	}
	if !errors.Is(err, apikey.ErrAuthorityUnavailable) {
		t.Errorf("error = %v, want ErrAuthorityUnavailable (503): an unwired resolver is a degraded "+
			"deployment, not a bad credential", err)
	}
	if errors.Is(err, apikey.ErrInvalidKey) {
		t.Error("an unwired resolver was reported as an invalid credential")
	}
}

func TestRegisterRoutes_FlagOffRefusesAKeyThroughTheRealMiddleware(t *testing.T) {
	t.Parallel()
	holder := apikey.NewLazyAuthority()
	registerWithDeps(t, &RouteDeps{
		IsRBACAuthority:   true,
		FeatureRegistry:   machineTokensRegistry(false),
		AuthorizationMode: authorizationModeDefaultRBAC,
		KeyAuthority:      holder,
	})

	mw := NewAuthMiddleware(&countingAuthenticator{}, nil, holder, slog.Default())
	resp := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler ran for a key on a deployment that does not serve keys")
	})).ServeHTTP(resp, keyRequest("/api/v1/platform/agents"))

	if resp.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (503 would read as an outage); body=%s", resp.Code, resp.Body.String())
	}
}

func TestRegisterRoutes_AnUnopenedDatabaseStillSettlesTheHolder(t *testing.T) {
	t.Parallel()
	holder := apikey.NewLazyAuthority()
	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns"},
		platpkg.ExtensionPoints{},
	)

	RegisterRoutesWithDeps(http.NewServeMux(), core, &RouteDeps{KeyAuthority: holder})

	if !holder.Bound() {
		t.Fatal("the early return left the holder pending")
	}
	if _, err := holder.Validate(t.Context(), "a-hash-no-key-has"); !errors.Is(err, apikey.ErrNotEnabled) {
		t.Errorf("Validate error = %v, want ErrNotEnabled", err)
	}
}
