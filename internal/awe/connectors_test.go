// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/connector"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeConnector struct {
	name    string
	typ     string
	started bool
	stopped bool
}

func (c *fakeConnector) Name() string           { return c.name }
func (c *fakeConnector) Type() string           { return c.typ }
func (c *fakeConnector) Tool() tool.BuiltinTool { return &fakeTool{name: "tool_" + c.name} }
func (c *fakeConnector) Start(_ context.Context) error {
	c.started = true
	return nil
}
func (c *fakeConnector) Stop(_ context.Context) error {
	c.stopped = true
	return nil
}
func (c *fakeConnector) Health() error { return nil }

type fakeTool struct{ name string }

func (t *fakeTool) Name() string                 { return t.name }
func (t *fakeTool) Description() string          { return "" }
func (t *fakeTool) Parameters() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (t *fakeTool) Instructions() string         { return "" }
func (t *fakeTool) IsLongRunning() bool          { return false }
func (t *fakeTool) RequiredScopes() []string     { return []string{"tool:underlying:invoke"} }
func (t *fakeTool) RawStringArgs() []string      { return nil }
func (t *fakeTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return nil
}
func (t *fakeTool) ContextParam() string { return "" }
func (t *fakeTool) Execute(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	return &tool.Result{}, nil
}

func withTestConnectorType(t *testing.T, typ string) *[]*fakeConnector {
	t.Helper()
	var built []*fakeConnector
	factory := connector.Factory(func(_ context.Context, cfg connector.Config, _ runtime.Services) (connector.Connector, error) {
		fc := &fakeConnector{name: cfg.Name, typ: cfg.Type}
		built = append(built, fc)
		return fc, nil
	})
	if err := connector.DefaultRegistry().Register(typ, factory); err != nil {
		t.Fatalf("register connector type %q: %v", typ, err)
	}
	return &built
}

func TestLoadAndRegisterConnectors_NoSectionIsNoOp(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "no-conn"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	appCfg := config.FromMap(map[string]any{})
	if err := LoadAndRegisterConnectors(t.Context(), ag, appCfg, svc); err != nil {
		t.Fatalf("unexpected error for empty config: %v", err)
	}
}

func TestLoadAndRegisterConnectors_RegistersToolAndSubcomponent(t *testing.T) {
	t.Parallel()
	built := withTestConnectorType(t, "fake-basic")

	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "with-conn"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	appCfg := config.FromMap(map[string]any{
		"connectors": []any{
			map[string]any{
				"name": "ops",
				"type": "fake-basic",
			},
		},
	})
	if err := LoadAndRegisterConnectors(t.Context(), ag, appCfg, svc); err != nil {
		t.Fatalf("LoadAndRegisterConnectors: %v", err)
	}
	if ag.Tools().GetTool("tool_ops") == nil {
		t.Fatal("connector tool not registered in agent.Tools()")
	}
	if len(*built) != 1 {
		t.Fatalf("expected 1 connector built, got %d", len(*built))
	}
}

func TestLoadAndRegisterConnectors_StopReachesUnderlyingConnector(t *testing.T) {
	t.Parallel()
	built := withTestConnectorType(t, "fake-stop")

	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "stop-conn"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	appCfg := config.FromMap(map[string]any{
		"connectors": []any{
			map[string]any{"name": "x", "type": "fake-stop"},
		},
	})
	if err := LoadAndRegisterConnectors(t.Context(), ag, appCfg, svc); err != nil {
		t.Fatalf("LoadAndRegisterConnectors: %v", err)
	}
	fc := (*built)[0]
	stub := connectorStartable{conn: fc, logger: slogDiscard()}
	if err := stub.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	stub.Stop()
	if !fc.started {
		t.Error("Start did not propagate to underlying connector")
	}
	if !fc.stopped {
		t.Error("Stop did not propagate to underlying connector")
	}
}

func TestLoadAndRegisterConnectors_BadEntries(t *testing.T) {
	t.Parallel()
	withTestConnectorType(t, "fake-bad")

	cases := []struct {
		name   string
		entry  any
		errSub string
	}{
		{
			name:   "missing name",
			entry:  map[string]any{"type": "fake-bad"},
			errSub: "missing required field `name`",
		},
		{
			name:   "missing type",
			entry:  map[string]any{"name": "x"},
			errSub: "missing required field `type`",
		},
		{
			name:   "unknown type",
			entry:  map[string]any{"name": "x", "type": "no-such-type"},
			errSub: "unknown type",
		},
		{
			name:   "scopes key no longer accepted",
			entry:  map[string]any{"name": "x", "type": "fake-bad", "scopes": []any{"tool:x:invoke"}},
			errSub: "unknown field",
		},
		{
			name:   "unknown key (typo)",
			entry:  map[string]any{"name": "x", "type": "fake-bad", "scope": []any{"x"}},
			errSub: "unknown field",
		},
		{
			name:   "non-map entry",
			entry:  "just-a-string",
			errSub: "entry must be a map",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestServices()
			ag := agent.New(agent.AgentConfig{Name: "bad"})
			if err := ag.Init(t.Context(), nil, svc); err != nil {
				t.Fatal(err)
			}
			appCfg := config.FromMap(map[string]any{
				"connectors": []any{tc.entry},
			})
			err := LoadAndRegisterConnectors(t.Context(), ag, appCfg, svc)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.errSub)
			}
		})
	}
}

func TestLoadAndRegisterConnectors_FactoryError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	failing := connector.Factory(func(_ context.Context, _ connector.Config, _ runtime.Services) (connector.Connector, error) {
		return nil, wantErr
	})
	if err := connector.DefaultRegistry().Register("fake-fail", failing); err != nil {
		t.Fatalf("register: %v", err)
	}

	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "fail-conn"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	appCfg := config.FromMap(map[string]any{
		"connectors": []any{map[string]any{"name": "x", "type": "fake-fail"}},
	})
	err := LoadAndRegisterConnectors(t.Context(), ag, appCfg, svc)
	if err == nil {
		t.Fatal("expected error from factory, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error chain does not include factory error: %v", err)
	}
}
