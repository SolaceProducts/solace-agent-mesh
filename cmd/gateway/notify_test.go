// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestNotifyConfigBlock(t *testing.T) {
	notifyBlock := map[string]any{"directory": []any{map[string]any{"user_id": "alice@example.com"}}}

	t.Run("none present", func(t *testing.T) {
		if got := notifyConfigBlock(map[string]any{"foo": 1}, map[string]any{"bar": 2}); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("platform arg wins (first)", func(t *testing.T) {
		got := notifyConfigBlock(map[string]any{"notify": notifyBlock}, map[string]any{"notify": map[string]any{"directory": "gw"}})
		if got["directory"] == nil || got["directory"] == "gw" {
			t.Errorf("expected first (platform) block, got %v", got)
		}
	})

	t.Run("falls through to gateway", func(t *testing.T) {
		got := notifyConfigBlock(map[string]any{"foo": 1}, map[string]any{"notify": notifyBlock})
		if got == nil || got["directory"] == nil {
			t.Errorf("expected gateway block, got %v", got)
		}
	})

	t.Run("non-map notify value is ignored", func(t *testing.T) {
		if got := notifyConfigBlock(map[string]any{"notify": "not-a-map"}); got != nil {
			t.Errorf("expected nil for non-map notify, got %v", got)
		}
	})
}

func TestNotifyAppConfigFor(t *testing.T) {
	const platformDB = "postgres://platform.example.com/sam"
	const notifyDB = "postgres://notify.example.com/notify"
	dataDir := t.TempDir()
	t.Setenv("SAM_DATA_DIR", dataDir)
	platformDefault := "sqlite:///" + filepath.Join(dataDir, "platform.db")

	for _, tc := range []struct {
		name       string
		platform   map[string]any
		gateway    map[string]any
		wantDB     string
		wantNotify bool
	}{
		{"no block uses the platform database", map[string]any{"database_url": platformDB}, map[string]any{}, platformDB, false},
		{"block database wins", map[string]any{"database_url": platformDB, "notify": map[string]any{"database_url": notifyDB, "retention_days": 7}}, map[string]any{}, notifyDB, true},
		{"block with an empty database uses the platform's", map[string]any{"database_url": platformDB, "notify": map[string]any{"database_url": nil}}, map[string]any{}, platformDB, true},
		{"block on the gateway", map[string]any{"database_url": platformDB}, map[string]any{"notify": map[string]any{"database_url": notifyDB}}, notifyDB, true},
		{"no database configured uses the platform's SQLite default", map[string]any{}, map[string]any{}, platformDefault, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := notifyAppConfigFor("test-ns", tc.platform, tc.gateway)
			if got["namespace"] != "test-ns" {
				t.Errorf("namespace = %v, want test-ns", got["namespace"])
			}
			if got["database_url"] != tc.wantDB {
				t.Errorf("database_url = %v, want %v", got["database_url"], tc.wantDB)
			}
			if _, has := got["notify"]; has != tc.wantNotify {
				t.Errorf("notify block present = %v, want %v", has, tc.wantNotify)
			}
		})
	}
	if got := notifyAppConfigFor("ns", map[string]any{"notify": map[string]any{"retention_days": 7, "database_url": notifyDB}}, nil); got["retention_days"] != 7 {
		t.Errorf("retention_days = %v, want 7 lifted from the block", got["retention_days"])
	}
}

func shippedCatalog(t *testing.T) *features.Registry {
	t.Helper()
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		t.Fatalf("LoadFlags: %v", err)
	}
	return reg
}

func newNotifyMountFixture(t *testing.T, reg *features.Registry) (*entrypoint.GatewayProxy, runtime.Services) {
	t.Helper()
	t.Setenv("SAM_DATA_DIR", t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := bootstrap.NewGatewayProxy(bootstrap.ProxyOpts{ListenAddr: "127.0.0.1:0"}, logger)
	var opts []runtime.Option
	if reg != nil {
		opts = append(opts, runtime.WithFeatureRegistry(reg))
	}
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)
	return proxy, rt.Services()
}

func TestMountNotify_FollowsTheNotifyServiceFlag(t *testing.T) {
	t.Run("mounts with the shipped default", func(t *testing.T) {
		t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "")
		reg := shippedCatalog(t)
		proxy, svc := newNotifyMountFixture(t, reg)

		core := mountNotify(reg, proxy, svc, "test-ns", nil, nil, nil)
		if core == nil {
			t.Fatal("mountNotify returned nil with notify_service at its default; the service would not mount")
		}
		if err := core.Init(t.Context(), nil); err != nil {
			t.Fatalf("notify Init: %v", err)
		}
		t.Cleanup(func() { _ = core.Stop(context.Background()) })

		rec := httptest.NewRecorder()
		proxy.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /api/v1/notify/inbox through the proxy = %d, want 200 once the core is initialised; body %s", rec.Code, rec.Body)
		}
	})

	t.Run("does not mount when the flag is off", func(t *testing.T) {
		t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "false")
		reg := shippedCatalog(t)
		proxy, svc := newNotifyMountFixture(t, reg)

		if core := mountNotify(reg, proxy, svc, "test-ns", nil, nil, nil); core != nil {
			t.Error("mountNotify returned a core with SAM_FEATURE_NOTIFY_SERVICE=false")
		}
	})

	t.Run("does not mount without a registry", func(t *testing.T) {
		t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "")
		proxy, svc := newNotifyMountFixture(t, nil)

		if core := mountNotify(nil, proxy, svc, "test-ns", nil, nil, nil); core != nil {
			t.Error("mountNotify returned a core with a nil registry")
		}
	})
}

func TestStampMountedServices(t *testing.T) {
	for _, notifyMounted := range []bool{true, false} {
		gateways := []*entrypoint.GatewayCore{{}, {}}

		stampMountedServices(gateways, notifyMounted)

		for i, gw := range gateways {
			if !gw.PlatformMounted() {
				t.Errorf("gateway %d: PlatformMounted = false, want true", i)
			}
			if gw.NotifyMounted() != notifyMounted {
				t.Errorf("gateway %d: NotifyMounted = %v, want %v", i, gw.NotifyMounted(), notifyMounted)
			}
		}
	}
}
