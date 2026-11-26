// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestNewGatewayAdapter_Default(t *testing.T) {
	adapter := NewGatewayAdapter(map[string]any{
		"namespace": "test",
	})
	if _, ok := adapter.(*httpsse.HTTPSSEAdapter); !ok {
		t.Errorf("default adapter = %T, want *httpsse.HTTPSSEAdapter", adapter)
	}
}

func TestNewGatewayAdapter_LegacyEventMesh(t *testing.T) {
	adapter := NewGatewayAdapter(map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
	})
	if _, ok := adapter.(*eventmesh.EventMeshAdapter); !ok {
		t.Errorf("event mesh adapter = %T, want *eventmesh.EventMeshAdapter", adapter)
	}
}

func TestNewGatewayAdapter_RegistryHTTPSSE(t *testing.T) {
	adapter := NewGatewayAdapter(map[string]any{
		"gateway_adapter": map[string]any{
			"type": "httpsse",
		},
	})
	if _, ok := adapter.(*httpsse.HTTPSSEAdapter); !ok {
		t.Errorf("registry httpsse = %T, want *httpsse.HTTPSSEAdapter", adapter)
	}
}

func TestNewGatewayAdapter_RegistryEventMesh(t *testing.T) {
	adapter := NewGatewayAdapter(map[string]any{
		"gateway_adapter": map[string]any{
			"type": "eventmesh",
		},
	})
	if _, ok := adapter.(*eventmesh.EventMeshAdapter); !ok {
		t.Errorf("registry eventmesh = %T, want *eventmesh.EventMeshAdapter", adapter)
	}
}

func TestNewGatewayAdapter_RegistryWithAdapterConfig(t *testing.T) {
	adapter := NewGatewayAdapter(map[string]any{
		"gateway_adapter": map[string]any{
			"type": "httpsse",
			"adapter_config": map[string]any{
				"some_setting": true,
			},
		},
	})
	if _, ok := adapter.(*httpsse.HTTPSSEAdapter); !ok {
		t.Errorf("registry httpsse with config = %T, want *httpsse.HTTPSSEAdapter", adapter)
	}
}

func TestNewGatewayAdapter_UnknownTypeFallback(t *testing.T) {
	adapter := NewGatewayAdapter(map[string]any{
		"gateway_adapter": map[string]any{
			"type": "nonexistent",
		},
	})
	if _, ok := adapter.(*httpsse.HTTPSSEAdapter); !ok {
		t.Errorf("unknown type fallback = %T, want *httpsse.HTTPSSEAdapter", adapter)
	}
}

func TestNewGatewayAdapter_EmptyType(t *testing.T) {
	adapter := NewGatewayAdapter(map[string]any{
		"gateway_adapter": map[string]any{
			"type": "",
		},
	})
	if _, ok := adapter.(*httpsse.HTTPSSEAdapter); !ok {
		t.Errorf("empty type = %T, want *httpsse.HTTPSSEAdapter", adapter)
	}
}

func TestNewGatewayAdapter_RegistryOverridesLegacy(t *testing.T) {
	adapter := NewGatewayAdapter(map[string]any{
		"gateway_adapter": map[string]any{
			"type": "httpsse",
		},
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://localhost:55555",
		},
	})
	if _, ok := adapter.(*httpsse.HTTPSSEAdapter); !ok {
		t.Errorf("registry should override legacy, got %T, want *httpsse.HTTPSSEAdapter", adapter)
	}
}

func mountTestServices(t *testing.T) runtime.Services {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil)
	return rt.Services()
}

type fakeAdapter struct {
	intent   entrypoint.MountSpec
	body     string
	callback func(http.Handler)
}

func (a *fakeAdapter) Init(context.Context, *entrypoint.GatewayCore) error { return nil }
func (a *fakeAdapter) Start(context.Context) error                         { return nil }
func (a *fakeAdapter) Stop(context.Context) error                          { return nil }
func (a *fakeAdapter) MountIntent() entrypoint.MountSpec                   { return a.intent }
func (a *fakeAdapter) SetOnHandlerReady(cb func(http.Handler))             { a.callback = cb }

func (a *fakeAdapter) fire() {
	if a.callback != nil {
		a.callback(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, a.body)
		}))
	}
}
func (a *fakeAdapter) OnStatusUpdate(context.Context, string, *a2a.TaskStatusUpdateEvent, []byte) {
}
func (a *fakeAdapter) OnArtifactUpdate(context.Context, string, *a2a.TaskArtifactUpdateEvent, []byte) {
}
func (a *fakeAdapter) OnFinalResponse(context.Context, string, *a2a.Task, []byte) {}
func (a *fakeAdapter) OnError(context.Context, string, *a2a.Error, []byte)        {}
func (a *fakeAdapter) OnAgentDiscovered(*a2a.AgentCard)                           {}
func (a *fakeAdapter) OnAgentRemoved(string)                                      {}

func buildExecutor(t *testing.T, adapters map[string]entrypoint.GatewayAdapter) *entrypoint.GatewayExecutor {
	t.Helper()
	svc := mountTestServices(t)

	factory := func(appConfig map[string]any, svc runtime.Services) (*entrypoint.GatewayCore, error) {
		name, _ := appConfig["__test_name"].(string)
		adapter := adapters[name]
		if adapter == nil {
			t.Fatalf("no adapter registered for %q", name)
		}
		core := entrypoint.NewGatewayCore(adapter, appConfig)
		core.SetServices(svc)
		return core, nil
	}
	exe := entrypoint.NewGatewayExecutor(svc, factory)

	for name := range adapters {
		if err := exe.LoadConfig(name, map[string]any{
			"namespace":   "test",
			"__test_name": name,
			"gateway_id":  name,
		}); err != nil {
			t.Fatalf("LoadConfig %q: %v", name, err)
		}
	}
	return exe
}

var _ = eventmesh.NewEventMeshAdapter

func TestBuildGatewayRuntimeOptions_LLMScansAppsForFirstModel(t *testing.T) {
	apps := []samapp.AppEntry{
		{
			Name:      "first-no-model",
			AppConfig: map[string]any{},
			ModelRaw:  nil,
		},
		{
			Name:      "second-with-model",
			AppConfig: map[string]any{},
			ModelRaw:  12345,
		},
	}

	_, err := BuildGatewayRuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err == nil {
		t.Fatal("expected error because apps[1].ModelRaw is invalid; got nil — scan likely stopped at apps[0]")
	}
	if !strings.Contains(err.Error(), "second-with-model") {
		t.Errorf("error should name the app that failed (proving apps[1] was reached): %v", err)
	}
}

func TestBuildGatewayRuntimeOptions_VolumeServiceConstructionErrorPropagates(t *testing.T) {
	apps := []samapp.AppEntry{
		{
			Name: "gw",
			AppConfig: map[string]any{
				"volume_service": map[string]any{
					"type": "fileystem",
				},
			},
		},
	}

	_, err := BuildGatewayRuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err == nil {
		t.Fatal("expected error from bad volume_service.type; got nil")
	}
	if !strings.Contains(err.Error(), "volume service") {
		t.Errorf("error should mention volume service, got %q", err.Error())
	}
}

func TestBuildGatewayRuntimeOptions_ArtifactServiceScansAppsForFirstBlock(t *testing.T) {
	apps := []samapp.AppEntry{
		{
			Name:      "first-no-artifact-service",
			AppConfig: map[string]any{},
		},
		{
			Name: "second-with-artifact-service",
			AppConfig: map[string]any{
				"artifact_service": map[string]any{
					"type": "memory",
				},
			},
		},
	}

	opts, err := BuildGatewayRuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err != nil {
		t.Fatalf("BuildGatewayRuntimeOptions: %v", err)
	}

	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)
	if rt.Services().Artifacts() == nil {
		t.Error("Artifacts() is nil — scan did not pick up apps[1].artifact_service")
	}
}

func TestBuildGatewayRuntimeOptions_VolumeServiceEnabledToggle(t *testing.T) {
	tests := []struct {
		name      string
		volumeCfg map[string]any
		wantStore bool
	}{
		{"enabled:false yields no store", map[string]any{"enabled": false, "type": "memory"}, false},
		{"enabled:true wires a store", map[string]any{"enabled": true, "type": "memory"}, true},
		{"absent enabled key wires a store (legacy)", map[string]any{"type": "memory"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			apps := []samapp.AppEntry{{Name: "gw", AppConfig: map[string]any{"volume_service": tc.volumeCfg}}}
			opts, err := BuildGatewayRuntimeOptions(context.Background(), nil, apps, slog.Default())
			if err != nil {
				t.Fatalf("BuildGatewayRuntimeOptions: %v", err)
			}
			rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)
			if got := rt.Services().Volumes() != nil; got != tc.wantStore {
				t.Errorf("Volumes() != nil = %v, want %v", got, tc.wantStore)
			}
		})
	}
}
