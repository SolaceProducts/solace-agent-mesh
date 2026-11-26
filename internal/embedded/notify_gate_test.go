// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestNotifyEnabled(t *testing.T) {
	t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "")

	newInst := func(reg *features.Registry) *Instance {
		inst := &Instance{sharedBroker: broker.NewDevBroker()}
		if reg != nil {
			inst.rt = runtime.NewRuntime(inst.sharedBroker, nil, runtime.WithFeatureRegistry(reg))
		} else {
			inst.rt = runtime.NewRuntime(inst.sharedBroker, nil)
		}
		return inst
	}
	regWith := func(def bool) *features.Registry {
		r := features.NewRegistry()
		r.Register(features.Definition{Key: "notify_service", Default: def})
		return r
	}

	if newInst(nil).notifyEnabled() {
		t.Error("absent feature registry: notifyEnabled() = true, want false")
	}
	if newInst(regWith(false)).notifyEnabled() {
		t.Error("flag default off: notifyEnabled() = true, want false")
	}
	if !newInst(regWith(true)).notifyEnabled() {
		t.Error("flag on: notifyEnabled() = false, want true")
	}
}

func TestFirstNotifyConfig(t *testing.T) {
	notifyBlock := map[string]any{"directory": []any{map[string]any{"user_id": "alice@example.com"}}}

	t.Run("none present", func(t *testing.T) {
		got := firstNotifyConfig(EmbeddedApps{
			Gateway:  []samapp.AppEntry{{AppConfig: map[string]any{"foo": 1}}},
			Platform: []samapp.AppEntry{{AppConfig: map[string]any{"bar": 2}}},
		})
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("found on gateway", func(t *testing.T) {
		got := firstNotifyConfig(EmbeddedApps{
			Gateway: []samapp.AppEntry{{AppConfig: map[string]any{"notify": notifyBlock}}},
		})
		if got == nil || got["directory"] == nil {
			t.Errorf("expected gateway notify block, got %v", got)
		}
	})

	t.Run("found on platform when gateway has none", func(t *testing.T) {
		got := firstNotifyConfig(EmbeddedApps{
			Gateway:  []samapp.AppEntry{{AppConfig: map[string]any{"foo": 1}}},
			Platform: []samapp.AppEntry{{AppConfig: map[string]any{"notify": notifyBlock}}},
		})
		if got == nil || got["directory"] == nil {
			t.Errorf("expected platform notify block, got %v", got)
		}
	})

	t.Run("gateway takes precedence over platform", func(t *testing.T) {
		gw := map[string]any{"directory": "gw"}
		pf := map[string]any{"directory": "pf"}
		got := firstNotifyConfig(EmbeddedApps{
			Gateway:  []samapp.AppEntry{{AppConfig: map[string]any{"notify": gw}}},
			Platform: []samapp.AppEntry{{AppConfig: map[string]any{"notify": pf}}},
		})
		if got["directory"] != "gw" {
			t.Errorf("expected gateway block to win, got %v", got)
		}
	})
}

func registeredComponent(inst *Instance, name string) runtime.Component {
	for _, c := range inst.rt.Components() {
		if c.Name == name {
			return c.Component
		}
	}
	return nil
}

func TestEmbedded_MountsNotifyAndTellsTheGatewaysItIsAvailable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		override  string
		wantMount bool
	}{
		{"flag at its default", "", true},
		{"flag turned off", "false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", tc.override)
			t.Setenv("SAM_DATA_DIR", t.TempDir())
			inst := newTestInstance(Config{})
			inst.sharedBroker = broker.NewDevBroker()
			classified := budgetWiringApps()
			if err := inst.buildRuntime(t.Context(), map[string]any{}, classified, "test-ns"); err != nil {
				t.Fatalf("buildRuntime: %v", err)
			}
			t.Cleanup(inst.bindings.Close)

			proxy, err := inst.registerGatewayPlatformProxy(classified, "test-ns")
			if err != nil {
				t.Fatalf("registerGatewayPlatformProxy: %v", err)
			}

			exe, ok := registeredComponent(inst, "gateway").(*entrypoint.GatewayExecutor)
			if !ok || len(exe.Gateways()) == 0 {
				t.Fatal("no gateway cores registered on the runtime")
			}
			for _, gw := range exe.Gateways() {
				if !gw.PlatformMounted() {
					t.Errorf("gateway %s: PlatformMounted = false, want true", gw.GatewayID())
				}
				if gw.NotifyMounted() != tc.wantMount {
					t.Errorf("gateway %s: NotifyMounted = %v, want %v", gw.GatewayID(), gw.NotifyMounted(), tc.wantMount)
				}
			}

			notifyComponent := registeredComponent(inst, "notify")
			if (notifyComponent != nil) != tc.wantMount {
				t.Fatalf("notify component registered = %v, want %v", notifyComponent != nil, tc.wantMount)
			}
			if !tc.wantMount {
				return
			}
			if err := notifyComponent.Init(t.Context(), nil); err != nil {
				t.Fatalf("notify Init: %v", err)
			}
			t.Cleanup(func() { _ = notifyComponent.Stop(context.Background()) })
			rec := httptest.NewRecorder()
			proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("GET /api/v1/notify/inbox through the proxy = %d, want 200 once the core is initialised; body %s", rec.Code, rec.Body)
			}
		})
	}
}
