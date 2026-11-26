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
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/visualization"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func quietSlog(tb testing.TB) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	tb.Cleanup(func() { slog.SetDefault(prev) })
}

type activityBenchAdapter struct{ noopAdapter }

func (activityBenchAdapter) OnActivityEvent(_ context.Context, taskID, topic string, payload map[string]any) {
	if visualization.ShouldFilterWorkingState(payload) || visualization.IsVisualizationExcluded(payload) {
		return
	}
	event := visualization.InferEventDetails(topic, payload)
	event.TaskID = taskID
	_, _ = json.Marshal(event)
}

var _ ActivityEventEmitter = activityBenchAdapter{}

type noopAdapter struct{}

func (noopAdapter) Init(context.Context, *GatewayCore) error                                   { return nil }
func (noopAdapter) Start(context.Context) error                                                { return nil }
func (noopAdapter) Stop(context.Context) error                                                 { return nil }
func (noopAdapter) MountIntent() MountSpec                                                     { return MountSpec{Kind: MountNone} }
func (noopAdapter) OnStatusUpdate(context.Context, string, *a2a.TaskStatusUpdateEvent, []byte) {}
func (noopAdapter) OnArtifactUpdate(context.Context, string, *a2a.TaskArtifactUpdateEvent, []byte) {
}
func (noopAdapter) OnFinalResponse(context.Context, string, *a2a.Task, []byte) {}
func (noopAdapter) OnError(context.Context, string, *a2a.Error, []byte)        {}
func (noopAdapter) OnAgentDiscovered(*a2a.AgentCard)                           {}
func (noopAdapter) OnAgentRemoved(string)                                      {}

var _ GatewayAdapter = noopAdapter{}

func benchStatusPayload(taskID string) []byte {
	text := "Streaming a moderately sized assistant delta that represents a typical " +
		"token chunk forwarded to the client during a long answer. " +
		"It carries no embed markers, which is the overwhelmingly common case."
	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    taskID,
		ContextID: "ctx-" + taskID,
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateWorking,
			Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart(text)}},
		},
		Metadata: map[string]any{"agent_name": "BenchAgent"},
	}
	payload, _ := json.Marshal(a2a.NewResponse(taskID, event))
	return payload
}

func BenchmarkHandleStatusEvent(b *testing.B) {
	quietSlog(b)
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	core := NewGatewayCore(noopAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		b.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)

	const taskID = "task-bench"
	_ = core.tasks.Register(taskID, "BenchAgent", "user-1", TaskIdentity{SessionID: "ctx-" + taskID})
	msg := &broker.Message{
		Topic:   a2a.GatewayStatusTopic("test-ns", "test-gw", taskID),
		Payload: benchStatusPayload(taskID),
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		core.handleStatusEvent(ctx, msg)
	}
}

func BenchmarkHandleStatusEvent_Subscribed(b *testing.B) {
	quietSlog(b)
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	core := NewGatewayCore(activityBenchAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		b.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)

	const taskID = "task-bench"
	_ = core.tasks.Register(taskID, "BenchAgent", "user-1", TaskIdentity{SessionID: "ctx-" + taskID})
	msg := &broker.Message{
		Topic:   a2a.GatewayStatusTopic("test-ns", "test-gw", taskID),
		Payload: benchStatusPayload(taskID),
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		core.handleStatusEvent(ctx, msg)
	}
}

type watchedBenchView struct {
	activityBenchAdapter
	watching bool
}

func (v watchedBenchView) WatchingTask(string) bool { return v.watching }

func BenchmarkHandleStatusEvent_EntrypointFallback(b *testing.B) {
	quietSlog(b)
	const taskID = "task-bench"
	payload, err := json.Marshal(a2a.NewResponse(taskID, &a2a.TaskStatusUpdateEvent{
		TaskID: taskID, ContextID: "ctx-" + taskID, Kind: "status-update",
		Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("node finished")}}},
		Metadata: map[string]any{"agent_name": "BenchAgent"},
	}))
	if err != nil {
		b.Fatal(err)
	}
	cases := []struct {
		name     string
		fallback func() ActivityEventEmitter
	}{
		{"no_fallback", nil},
		{"unwatched", func() ActivityEventEmitter { return watchedBenchView{} }},
		{"watched", func() ActivityEventEmitter { return watchedBenchView{watching: true} }},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			brk := broker.NewDevBroker()
			if err := brk.Connect(context.Background()); err != nil {
				b.Fatal(err)
			}
			core := NewGatewayCore(noopAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "event-mesh-gw"})
			core.SetServices(runtime.NewRuntime(brk, nil).Services())
			if err := core.Init(context.Background(), nil); err != nil {
				b.Fatal(err)
			}
			core.logger = slog.New(slog.DiscardHandler)
			core.setActivityFallback(tc.fallback)
			_ = core.tasks.Register(taskID, "BenchAgent", "system", TaskIdentity{SessionID: "ctx-" + taskID})
			msg := &broker.Message{Topic: a2a.GatewayStatusTopic("test-ns", "event-mesh-gw", taskID), Payload: payload}
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				core.handleStatusEvent(ctx, msg)
			}
		})
	}
}

func BenchmarkTaskLoggerHandleStatusMessage(b *testing.B) {
	quietSlog(b)
	cfg := &TaskLoggerConfig{
		Enabled:          true,
		LogStatusUpdates: true,
		QueueSize:        1 << 16,
	}
	s := NewTaskLoggerService(cfg, nil, nil, nil, "test-ns", "test-gw", slog.New(slog.DiscardHandler))
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-s.ch:
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	const taskID = "task-bench"
	topic := a2a.GatewayStatusTopic("test-ns", "test-gw", taskID)
	msg := &broker.Message{Topic: topic, Payload: benchStatusPayload(taskID)}
	userProps := a2a.UserProperties{}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s.handleStatusMessage(msg, topic, userProps)
	}
}
