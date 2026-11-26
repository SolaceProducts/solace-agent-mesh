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

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type stubPlatformManageable struct {
	configs map[string]map[string]any
}

func (s *stubPlatformManageable) Init(_ context.Context, _ config.Config) error { return nil }
func (s *stubPlatformManageable) Start(_ context.Context) error                 { return nil }
func (s *stubPlatformManageable) Stop(_ context.Context) error                  { return nil }
func (s *stubPlatformManageable) Health() error                                 { return nil }

func (s *stubPlatformManageable) ManagementEndpoints() []control.Endpoint {
	return []control.Endpoint{
		{Method: "get", Path: "model-configs/{alias}", Description: "Resolve model alias"},
	}
}

func (s *stubPlatformManageable) HandleManagement(_ context.Context, method string, path []string, _ json.RawMessage, _ *auth.Identity) (any, error) {
	if method != "get" || len(path) != 2 || path[0] != "model-configs" {
		return nil, errors.New("unexpected request")
	}
	cfg, ok := s.configs[path[1]]
	if !ok {
		return nil, control.ErrResourceNotFound
	}
	return cfg, nil
}

func bootstrapControlTest(t *testing.T, configs map[string]map[string]any) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	rt.Register("platform", &stubPlatformManageable{configs: configs})
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

func TestNewPlatformModelFetcher_ResolvesAlias(t *testing.T) {
	brk := bootstrapControlTest(t, map[string]map[string]any{
		"planning": {"model": "openai/gpt-4o", "api_base": "https://example"},
	})

	fetcher := NewPlatformModelFetcher(brk, "test-ns", slog.Default())
	cfg, err := fetcher(t.Context(), "planning")
	if err != nil {
		t.Fatal(err)
	}
	if cfg["model"] != "openai/gpt-4o" {
		t.Errorf("model = %v, want openai/gpt-4o", cfg["model"])
	}
	if cfg["api_base"] != "https://example" {
		t.Errorf("api_base = %v, want https://example", cfg["api_base"])
	}
}

func TestNewPlatformModelFetcher_UnknownAliasReturnsNilConfigNotError(t *testing.T) {
	brk := bootstrapControlTest(t, map[string]map[string]any{})

	fetcher := NewPlatformModelFetcher(brk, "test-ns", slog.Default())
	cfg, err := fetcher(t.Context(), "unknown")
	if err != nil {
		t.Fatalf("want nil error for unknown alias, got %v", err)
	}
	if cfg != nil {
		t.Errorf("want nil cfg for unknown alias, got %v", cfg)
	}
}

func TestNewBindingsRegistry_NoBroker_ReturnsRegistryWithoutFetcher(t *testing.T) {
	reg := NewBindingsRegistry(nil, "test-ns", slog.Default())
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	_, err := reg.ResolveOrFetch(t.Context(), "planning")
	if err == nil {
		t.Fatal("expected ErrAliasUnbound when no broker/fetcher is configured")
	}
}
