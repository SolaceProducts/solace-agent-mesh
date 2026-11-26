// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type headlessAdapter struct{ statuses chan string }

func (headlessAdapter) Init(context.Context, *entrypoint.GatewayCore) error { return nil }
func (headlessAdapter) Start(context.Context) error                         { return nil }
func (headlessAdapter) Stop(context.Context) error                          { return nil }
func (headlessAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNone}
}
func (h headlessAdapter) OnStatusUpdate(_ context.Context, taskID string, _ *a2a.TaskStatusUpdateEvent, _ []byte) {
	if h.statuses != nil {
		h.statuses <- taskID
	}
}
func (headlessAdapter) OnArtifactUpdate(context.Context, string, *a2a.TaskArtifactUpdateEvent, []byte) {
}
func (headlessAdapter) OnFinalResponse(context.Context, string, *a2a.Task, []byte) {}
func (headlessAdapter) OnError(context.Context, string, *a2a.Error, []byte)        {}
func (headlessAdapter) OnAgentDiscovered(*a2a.AgentCard)                           {}
func (headlessAdapter) OnAgentRemoved(string)                                      {}

var _ entrypoint.GatewayAdapter = headlessAdapter{}

type entrypointBesideWebUI struct {
	brk    broker.Broker
	webUI  *HTTPSSEAdapter
	taskID string
}

func newEntrypointBesideWebUI(t *testing.T, entrypointAdapter headlessAdapter) entrypointBesideWebUI {
	t.Helper()
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatal(err)
	}
	svc := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.New(slog.DiscardHandler)), runtime.WithFeatureRegistry(flags)).Services()

	webUIAdapter := NewHTTPSSEAdapter()
	webUI := entrypoint.NewGatewayCore(webUIAdapter, map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "webui-gw",
		"default_user_identity": "sam_dev_user",
		"session_service":       map[string]any{"type": "sql", "database_url": testDBURL(t, "entrypoint-activity")},
	})
	eventMesh := entrypoint.NewGatewayCore(entrypointAdapter, map[string]any{"namespace": "test-ns", "gateway_id": "event-mesh-gw"})

	exec := entrypoint.NewGatewayExecutor(svc, nil)
	exec.AddGateway("webui", webUI, map[string]any{})
	exec.AddGateway("event-mesh", eventMesh, map[string]any{})
	for _, core := range []*entrypoint.GatewayCore{webUI, eventMesh} {
		core.SetServices(svc)
		if err := core.Init(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if err := core.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = core.Stop(context.Background()) })
	}
	db := webUI.Store()
	t.Cleanup(func() { _ = db.Close() })

	taskID := idgen.New()
	if err := eventMesh.Tasks().Register(taskID, "SomeWorkflow", "system", entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if err := store.NewTaskRepo(db).Create(ctx, &store.Task{ID: taskID, UserID: "system", StartTime: now, LastActivityTime: &now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	return entrypointBesideWebUI{brk: brk, webUI: webUIAdapter, taskID: taskID}
}

func (e entrypointBesideWebUI) publishNodeStatus(t *testing.T) {
	t.Helper()
	payload, err := json.Marshal(a2a.NewResponse(e.taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: e.taskID, ContextID: "c", Kind: "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{
			Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("node done")},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	topic := a2a.GatewayStatusTopic("test-ns", "event-mesh-gw", e.taskID)
	if err := e.brk.PublishGuaranteed(t.Context(), topic, &broker.Message{Topic: topic, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func TestEntrypointStartedTask_StreamsActivityToTheWebUI(t *testing.T) {
	e := newEntrypointBesideWebUI(t, headlessAdapter{})
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: e.webUI.sseManager, eventLog: e.webUI.eventLog})
	frames, _ := liveFeed(t.Context(), t, ts.URL+"/api/v1/sse/subscribe/"+e.taskID)
	waitFor(t, "the Activities stream did not open for the entrypoint's task", func() bool { return e.webUI.sseManager.HasTask(e.taskID) })

	e.publishNodeStatus(t)

	fr := nextFrame(t, frames, 5*time.Second)
	if fr.eventType != "activity_event" || !strings.Contains(fr.data, e.taskID) {
		t.Fatalf("frame = %+v, want an activity frame for the task", fr)
	}
}

func TestEntrypointStartedTask_NotForwardedWhileNobodyWatches(t *testing.T) {
	statuses := make(chan string, 4)
	e := newEntrypointBesideWebUI(t, headlessAdapter{statuses: statuses})
	pub := &recordingPublisher{}
	e.webUI.streamPub = pub

	e.publishNodeStatus(t)

	select {
	case <-statuses:
	case <-time.After(5 * time.Second):
		t.Fatal("the entrypoint never handled the status update")
	}
	if got := pub.published(); len(got) != 0 {
		t.Fatalf("published %+v for a task nobody watches, want nothing", got)
	}
}
