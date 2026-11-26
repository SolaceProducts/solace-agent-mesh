// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const fallbackTaskID = "task-fallback"

func newStatusTestCore(t *testing.T, adapter GatewayAdapter, gatewayID string) *GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	core := NewGatewayCore(adapter, map[string]any{"namespace": "test-ns", "gateway_id": gatewayID})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	_ = core.tasks.Register(fallbackTaskID, "SomeWorkflow", "system", TaskIdentity{TraceID: "c"})
	return core
}

func completedStatusMessage(t *testing.T, gatewayID string) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(a2a.NewResponse(fallbackTaskID, &a2a.TaskStatusUpdateEvent{
		TaskID: fallbackTaskID, ContextID: "c", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("node done")},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return &broker.Message{Topic: a2a.GatewayStatusTopic("test-ns", gatewayID, fallbackTaskID), Payload: payload}
}

func TestEmitActivityEvent_UsesFallbackWhenAdapterHasNoView(t *testing.T) {
	webUI := &activityGateRecorder{}
	core := newStatusTestCore(t, noopAdapter{}, "event-mesh-gw")
	core.setActivityFallback(func() ActivityEventEmitter { return webUI })

	core.handleStatusEvent(context.Background(), completedStatusMessage(t, "event-mesh-gw"))

	if webUI.calls != 1 {
		t.Fatalf("fallback emitter calls = %d, want 1", webUI.calls)
	}
}

func TestEmitActivityEvent_AdapterViewWinsOverFallback(t *testing.T) {
	own := &activityGateRecorder{}
	fallback := &activityGateRecorder{}
	core := newStatusTestCore(t, own, "webui-gw")
	core.setActivityFallback(func() ActivityEventEmitter { return fallback })

	core.handleStatusEvent(context.Background(), completedStatusMessage(t, "webui-gw"))

	if own.calls != 1 || fallback.calls != 0 {
		t.Fatalf("own calls = %d, fallback calls = %d; want 1 and 0", own.calls, fallback.calls)
	}
}

func TestEmitActivityEvent_ResolvesFallbackPerEvent(t *testing.T) {
	first, second := &activityGateRecorder{}, &activityGateRecorder{}
	var current ActivityEventEmitter = first
	core := newStatusTestCore(t, noopAdapter{}, "event-mesh-gw")
	core.setActivityFallback(func() ActivityEventEmitter { return current })

	core.handleStatusEvent(context.Background(), completedStatusMessage(t, "event-mesh-gw"))
	current = second
	core.handleStatusEvent(context.Background(), completedStatusMessage(t, "event-mesh-gw"))
	current = nil
	core.handleStatusEvent(context.Background(), completedStatusMessage(t, "event-mesh-gw"))

	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("first calls = %d, second calls = %d; want 1 each", first.calls, second.calls)
	}
}

type watchedRecorder struct {
	activityGateRecorder
	watching bool
}

func (w *watchedRecorder) WatchingTask(string) bool { return w.watching }

func TestEmitActivityEvent_SkipsATaskNobodyWatches(t *testing.T) {
	view := &watchedRecorder{}
	core := newStatusTestCore(t, noopAdapter{}, "event-mesh-gw")
	core.setActivityFallback(func() ActivityEventEmitter { return view })

	core.handleStatusEvent(context.Background(), completedStatusMessage(t, "event-mesh-gw"))
	if view.calls != 0 {
		t.Fatalf("unwatched task: view calls = %d, want 0", view.calls)
	}

	view.watching = true
	core.handleStatusEvent(context.Background(), completedStatusMessage(t, "event-mesh-gw"))
	if view.calls != 1 {
		t.Fatalf("watched task: view calls = %d, want 1", view.calls)
	}
}

type webUIRecorder struct {
	noopAdapter
	forwarded *activityGateRecorder
}

func (w *webUIRecorder) ForwardedActivity() ActivityEventEmitter { return w.forwarded }

var _ ForwardedActivityView = (*webUIRecorder)(nil)

func TestExecutor_EntrypointCoreFallsBackToWebUIActivityView(t *testing.T) {
	webUIView := &activityGateRecorder{}
	webUI := NewGatewayCore(&webUIRecorder{forwarded: webUIView}, map[string]any{"namespace": "test-ns", "gateway_id": "webui-gw"})
	webUI.SetServesWebUI(true)
	eventMesh := NewGatewayCore(noopAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "event-mesh-gw"})

	e := NewGatewayExecutor(nil, nil)
	e.AddGateway("event-mesh", eventMesh, map[string]any{})
	if got := eventMesh.activityEmitter(); got != nil {
		t.Fatalf("with no web UI in the process the emitter = %v, want nil", got)
	}

	e.AddGateway("webui", webUI, map[string]any{})
	if got := eventMesh.activityEmitter(); got != webUIView {
		t.Fatalf("entrypoint core emitter = %v, want the web UI adapter", got)
	}
}

func TestExecutor_WebUIActivityEmitterSkipsInitializingCore(t *testing.T) {
	webUI := NewGatewayCore(&webUIRecorder{forwarded: &activityGateRecorder{}}, map[string]any{"namespace": "test-ns", "gateway_id": "webui-gw"})
	webUI.SetServesWebUI(true)
	e := NewGatewayExecutor(nil, nil)
	e.gws = append(e.gws, &managedGateway{core: webUI, name: "webui", initializing: true})

	if got := e.webUIActivityEmitter(); got != nil {
		t.Fatalf("emitter while the web UI initializes = %v, want nil", got)
	}
	e.gws[0].initialized()
	if got := e.webUIActivityEmitter(); got == nil {
		t.Fatal("emitter once the web UI initialized = nil, want its adapter")
	}
}

func TestExecutor_WebUIActivityEmitterSkipsACoreWithoutTheView(t *testing.T) {
	plain := NewGatewayCore(noopAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "rest-gw"})
	plain.SetServesWebUI(true)
	view := &activityGateRecorder{}
	webUI := NewGatewayCore(&webUIRecorder{forwarded: view}, map[string]any{"namespace": "test-ns", "gateway_id": "webui-gw"})
	webUI.SetServesWebUI(true)
	e := NewGatewayExecutor(nil, nil)
	e.AddGateway("rest", plain, map[string]any{})
	e.AddGateway("webui", webUI, map[string]any{})

	if got := e.webUIActivityEmitter(); got != view {
		t.Fatalf("emitter = %v, want the view of the core that has one", got)
	}
}

func TestExecutor_ConfiguredAndDeployedCoresFallBackToWebUIActivityView(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	svc := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.New(slog.DiscardHandler))).Services()
	factory := func(appConfig map[string]any, svc runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(noopAdapter{}, appConfig)
		core.SetServices(svc)
		return core, nil
	}
	view := &activityGateRecorder{}
	webUI := NewGatewayCore(&webUIRecorder{forwarded: view}, map[string]any{"namespace": "test-ns", "gateway_id": "webui-gw"})
	webUI.SetServesWebUI(true)
	e := NewGatewayExecutor(svc, factory)
	e.AddGateway("webui", webUI, map[string]any{})

	if err := e.LoadConfig("configured", map[string]any{"namespace": "test-ns", "gateway_id": "configured-gw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddAndStart(context.Background(), "deployed", map[string]any{"namespace": "test-ns", "gateway_id": "deployed-gw"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.GetGateway("deployed").Stop(context.Background()) })

	for _, name := range []string{"configured", "deployed"} {
		if got := e.GetGateway(name).activityEmitter(); got != view {
			t.Fatalf("%s core emitter = %v, want the web UI's view", name, got)
		}
	}
}
