// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package connector

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type fakeTool struct{ name string }

func (t *fakeTool) Name() string                 { return t.name }
func (t *fakeTool) Description() string          { return "fake tool" }
func (t *fakeTool) Parameters() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (t *fakeTool) Instructions() string         { return "" }
func (t *fakeTool) IsLongRunning() bool          { return false }
func (t *fakeTool) RawStringArgs() []string      { return nil }
func (t *fakeTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return nil
}
func (t *fakeTool) ContextParam() string { return "" }
func (t *fakeTool) Execute(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	return &tool.Result{}, nil
}

type fakeConnector struct {
	name    string
	cfg     Config
	started bool
	stopped bool
	healthy error
}

func (c *fakeConnector) Name() string           { return c.name }
func (c *fakeConnector) Type() string           { return c.cfg.Type }
func (c *fakeConnector) Tool() tool.BuiltinTool { return &fakeTool{name: "fake_" + c.name} }
func (c *fakeConnector) Start(_ context.Context) error {
	c.started = true
	return nil
}
func (c *fakeConnector) Stop(_ context.Context) error {
	c.stopped = true
	return nil
}
func (c *fakeConnector) Health() error { return c.healthy }

func newFakeFactory() (Factory, *[]*fakeConnector) {
	var built []*fakeConnector
	return func(_ context.Context, cfg Config, _ runtime.Services) (Connector, error) {
		fc := &fakeConnector{name: cfg.Name, cfg: cfg}
		built = append(built, fc)
		return fc, nil
	}, &built
}

func TestRegistry_Register(t *testing.T) {
	t.Run("empty type rejected", func(t *testing.T) {
		r := NewRegistry()
		factory, _ := newFakeFactory()
		if err := r.Register("", factory); err == nil {
			t.Fatal("expected error for empty type, got nil")
		}
	})

	t.Run("nil factory rejected", func(t *testing.T) {
		r := NewRegistry()
		if err := r.Register("fake", nil); err == nil {
			t.Fatal("expected error for nil factory, got nil")
		}
	})

	t.Run("duplicate type rejected", func(t *testing.T) {
		r := NewRegistry()
		factory, _ := newFakeFactory()
		if err := r.Register("fake", factory); err != nil {
			t.Fatalf("first register failed: %v", err)
		}
		err := r.Register("fake", factory)
		if err == nil {
			t.Fatal("expected duplicate-type error, got nil")
		}
		if !strings.Contains(err.Error(), "already registered") {
			t.Errorf("expected error to mention 'already registered', got %q", err.Error())
		}
	})
}

func TestRegistry_Build_Success(t *testing.T) {
	r := NewRegistry()
	factory, built := newFakeFactory()
	if err := r.Register("fake", factory); err != nil {
		t.Fatalf("register: %v", err)
	}

	conn, err := r.Build(context.Background(), Config{
		Name: "instance1",
		Type: "fake",
	}, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if conn.Name() != "instance1" {
		t.Errorf("Name() = %q, want instance1", conn.Name())
	}
	if conn.Type() != "fake" {
		t.Errorf("Type() = %q, want fake", conn.Type())
	}
	if len(*built) != 1 {
		t.Errorf("factory called %d times, want 1", len(*built))
	}
}

func TestRegistry_Build_MissingType(t *testing.T) {
	r := NewRegistry()

	t.Run("missing name", func(t *testing.T) {
		_, err := r.Build(context.Background(), Config{Type: "fake"}, nil)
		if err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Errorf("expected 'name is required' error, got %v", err)
		}
	})

	t.Run("missing type", func(t *testing.T) {
		_, err := r.Build(context.Background(), Config{Name: "x"}, nil)
		if err == nil || !strings.Contains(err.Error(), "type is required") {
			t.Errorf("expected 'type is required' error, got %v", err)
		}
	})

	t.Run("unknown type", func(t *testing.T) {
		_, err := r.Build(context.Background(), Config{Name: "x", Type: "nope"}, nil)
		if err == nil || !strings.Contains(err.Error(), "unknown type") {
			t.Errorf("expected unknown-type error, got %v", err)
		}
	})
}

func TestRegistry_Build_FactoryError(t *testing.T) {
	r := NewRegistry()
	wantErr := errors.New("provider unavailable")
	failing := Factory(func(_ context.Context, _ Config, _ runtime.Services) (Connector, error) {
		return nil, wantErr
	})
	if err := r.Register("bad", failing); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := r.Build(context.Background(), Config{Name: "x", Type: "bad"}, nil)
	if err == nil {
		t.Fatal("expected factory error to propagate, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("expected wrapped factory error, got %v", err)
	}
}

func TestRegistry_Build_FactoryReturnsNil(t *testing.T) {
	r := NewRegistry()
	bad := Factory(func(_ context.Context, _ Config, _ runtime.Services) (Connector, error) {
		return nil, nil
	})
	if err := r.Register("nilret", bad); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := r.Build(context.Background(), Config{Name: "x", Type: "nilret"}, nil)
	if err == nil || !strings.Contains(err.Error(), "nil connector") {
		t.Errorf("expected nil-connector error, got %v", err)
	}
}

func TestParseEntry_RejectsScopesKey(t *testing.T) {
	_, err := ParseEntry(map[string]any{
		"name":   "ops_email",
		"type":   "email",
		"scopes": []any{"tool:ops_email:invoke"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field \"scopes\"") {
		t.Fatalf("expected unknown-field error for `scopes:`, got %v", err)
	}
}

func TestRegistry_RegisteredTypes(t *testing.T) {
	r := NewRegistry()
	factory, _ := newFakeFactory()
	for _, typ := range []string{"alpha", "beta"} {
		if err := r.Register(typ, factory); err != nil {
			t.Fatalf("register %s: %v", typ, err)
		}
	}
	got := r.RegisteredTypes()
	slices.Sort(got)
	want := []string{"alpha", "beta"}
	if !slices.Equal(got, want) {
		t.Errorf("RegisteredTypes = %v, want %v", got, want)
	}
}

func TestRegistry_LifecycleHooksReachUnderlying(t *testing.T) {
	r := NewRegistry()
	factory, built := newFakeFactory()
	if err := r.Register("fake", factory); err != nil {
		t.Fatalf("register: %v", err)
	}
	conn, err := r.Build(context.Background(), Config{
		Name: "x",
		Type: "fake",
	}, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if err := conn.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := conn.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if len(*built) != 1 {
		t.Fatalf("expected 1 built connector, got %d", len(*built))
	}
	fc := (*built)[0]
	if !fc.started {
		t.Error("Start() did not reach underlying connector")
	}
	if !fc.stopped {
		t.Error("Stop() did not reach underlying connector")
	}
}

func TestDefaultRegistry_ProcessGlobal(t *testing.T) {
	a := DefaultRegistry()
	b := DefaultRegistry()
	if a != b {
		t.Error("DefaultRegistry() returned different instances on subsequent calls")
	}
}

func TestMustRegister_PanicOnDuplicate(t *testing.T) {
	r := NewRegistry()
	factory, _ := newFakeFactory()
	r.MustRegister("once", factory)
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate MustRegister, got none")
		}
	}()
	r.MustRegister("once", factory)
}
