// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type stubWebuiSettingsPlatform struct {
	settings map[string]any
}

func (s *stubWebuiSettingsPlatform) Init(_ context.Context, _ config.Config) error { return nil }
func (s *stubWebuiSettingsPlatform) Start(_ context.Context) error                 { return nil }
func (s *stubWebuiSettingsPlatform) Stop(_ context.Context) error                  { return nil }
func (s *stubWebuiSettingsPlatform) Health() error                                 { return nil }

func (s *stubWebuiSettingsPlatform) ManagementEndpoints() []control.Endpoint {
	return []control.Endpoint{{Method: "get", Path: "webui-settings", Description: "Resolve the instance's web UI settings overrides"}}
}

func (s *stubWebuiSettingsPlatform) HandleManagement(_ context.Context, method string, path []string, _ json.RawMessage, _ *auth.Identity) (any, error) {
	if method != "get" || len(path) != 1 || path[0] != "webui-settings" {
		return nil, errors.New("unexpected request")
	}
	if s.settings == nil {
		return nil, control.ErrResourceNotFound
	}
	return s.settings, nil
}

func webuiSettingsControlTest(t *testing.T, platform *stubWebuiSettingsPlatform) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	if platform != nil {
		rt.Register("platform", platform)
	}
	ctrlSvc := control.NewService(rt, "test-ns")
	rt.Register("control", ctrlSvc)

	if err := rt.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	return brk
}

func TestNewPlatformWebuiSettingsFetcher_ResolvesRow(t *testing.T) {
	const appName = "Acme Assistant"
	brk := webuiSettingsControlTest(t, &stubWebuiSettingsPlatform{settings: map[string]any{"appName": appName}})

	fetcher := NewPlatformWebuiSettingsFetcher(brk, "test-ns", slog.Default())
	settings, err := fetcher(t.Context())
	if err != nil {
		t.Fatalf("fetcher: %v", err)
	}
	if settings == nil || settings.AppName == nil || *settings.AppName != appName {
		t.Errorf("settings = %+v, want AppName %q", settings, appName)
	}
}

func TestNewPlatformWebuiSettingsFetcher_NotFoundMapsToNilNil(t *testing.T) {
	brk := webuiSettingsControlTest(t, &stubWebuiSettingsPlatform{settings: nil})

	fetcher := NewPlatformWebuiSettingsFetcher(brk, "test-ns", slog.Default())
	settings, err := fetcher(t.Context())
	if err != nil {
		t.Fatalf("fetcher: %v", err)
	}
	if settings != nil {
		t.Errorf("settings = %+v, want nil", settings)
	}
}

func TestNewPlatformWebuiSettingsFetcher_UnreachablePlatformTimesOutShort(t *testing.T) {
	brk := webuiSettingsControlTest(t, nil)

	fetcher := NewPlatformWebuiSettingsFetcher(brk, "test-ns", slog.Default())
	start := time.Now()
	_, err := fetcher(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error for an unowned platform app")
	}
	if elapsed < webuiSettingsControlTimeout {
		t.Errorf("returned after %s, before its own %s timeout even elapsed", elapsed, webuiSettingsControlTimeout)
	}
	if elapsed > webuiSettingsControlTimeout+2*time.Second {
		t.Errorf("returned after %s — too close to control.DefaultTimeout (30s); the short webuiSettingsControlTimeout is not being applied", elapsed)
	}
}
