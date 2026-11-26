// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type stubAdapter struct{ name string }

func (s *stubAdapter) Init(context.Context, *GatewayCore) error { return nil }
func (s *stubAdapter) Start(context.Context) error              { return nil }
func (s *stubAdapter) Stop(context.Context) error               { return nil }
func (s *stubAdapter) MountIntent() MountSpec                   { return MountSpec{Kind: MountNone} }
func (s *stubAdapter) OnStatusUpdate(context.Context, string, *a2a.TaskStatusUpdateEvent, []byte) {
}
func (s *stubAdapter) OnArtifactUpdate(context.Context, string, *a2a.TaskArtifactUpdateEvent, []byte) {
}
func (s *stubAdapter) OnFinalResponse(context.Context, string, *a2a.Task, []byte) {}
func (s *stubAdapter) OnError(context.Context, string, *a2a.Error, []byte)        {}
func (s *stubAdapter) OnAgentDiscovered(*a2a.AgentCard)                           {}
func (s *stubAdapter) OnAgentRemoved(string)                                      {}

func init() {
}

func TestRegisterAndCreate(t *testing.T) {
	RegisterAdapterFactory("test-adapter", func(_ map[string]any) (GatewayAdapter, error) {
		return &stubAdapter{name: "test"}, nil
	})
	defer func() {
		adapterRegistryMu.Lock()
		delete(adapterFactories, "test-adapter")
		adapterRegistryMu.Unlock()
	}()

	adapter, err := CreateAdapter("test-adapter", nil)
	if err != nil {
		t.Fatalf("CreateAdapter: %v", err)
	}
	sa, ok := adapter.(*stubAdapter)
	if !ok {
		t.Fatal("expected *stubAdapter")
	}
	if sa.name != "test" {
		t.Errorf("name = %q, want %q", sa.name, "test")
	}
}

func TestCreateUnknownAdapter(t *testing.T) {
	_, err := CreateAdapter("nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown adapter")
	}
}

func TestRegisteredAdapters(t *testing.T) {
	RegisterAdapterFactory("zzz-test", func(_ map[string]any) (GatewayAdapter, error) {
		return &stubAdapter{}, nil
	})
	RegisterAdapterFactory("aaa-test", func(_ map[string]any) (GatewayAdapter, error) {
		return &stubAdapter{}, nil
	})
	defer func() {
		adapterRegistryMu.Lock()
		delete(adapterFactories, "zzz-test")
		delete(adapterFactories, "aaa-test")
		adapterRegistryMu.Unlock()
	}()

	names := RegisteredAdapters()
	found := false
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("RegisteredAdapters not sorted: %v", names)
			break
		}
		if names[i] == "zzz-test" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected zzz-test in %v", names)
	}
}

func TestDoubleRegistrationOverwrites(t *testing.T) {
	RegisterAdapterFactory("overwrite-test", func(_ map[string]any) (GatewayAdapter, error) {
		return &stubAdapter{name: "first"}, nil
	})
	RegisterAdapterFactory("overwrite-test", func(_ map[string]any) (GatewayAdapter, error) {
		return &stubAdapter{name: "second"}, nil
	})
	defer func() {
		adapterRegistryMu.Lock()
		delete(adapterFactories, "overwrite-test")
		adapterRegistryMu.Unlock()
	}()

	adapter, err := CreateAdapter("overwrite-test", nil)
	if err != nil {
		t.Fatalf("CreateAdapter: %v", err)
	}
	sa := adapter.(*stubAdapter)
	if sa.name != "second" {
		t.Errorf("name = %q, want %q (second registration should win)", sa.name, "second")
	}
}
