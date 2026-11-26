// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type recordingAdapter struct {
	mu                sync.Mutex
	statusUpdates     []*a2a.TaskStatusUpdateEvent
	statusRawPayloads [][]byte
	artifactUpdates   []*a2a.TaskArtifactUpdateEvent
	finalResponses    []*a2a.Task
	errors            []*a2a.Error
	discovered        []*a2a.AgentCard
	removed           []string
}

func (a *recordingAdapter) HostsScheduler() bool { return true }

func (a *recordingAdapter) Init(_ context.Context, _ *GatewayCore) error { return nil }
func (a *recordingAdapter) Start(_ context.Context) error                { return nil }
func (a *recordingAdapter) Stop(_ context.Context) error                 { return nil }
func (a *recordingAdapter) MountIntent() MountSpec                       { return MountSpec{Kind: MountNone} }

func (a *recordingAdapter) OnStatusUpdate(_ context.Context, _ string, event *a2a.TaskStatusUpdateEvent, raw []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.statusUpdates = append(a.statusUpdates, event)
	a.statusRawPayloads = append(a.statusRawPayloads, raw)
}

func (a *recordingAdapter) OnArtifactUpdate(_ context.Context, _ string, event *a2a.TaskArtifactUpdateEvent, _ []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.artifactUpdates = append(a.artifactUpdates, event)
}

func (a *recordingAdapter) OnFinalResponse(_ context.Context, _ string, task *a2a.Task, _ []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finalResponses = append(a.finalResponses, task)
}

func (a *recordingAdapter) OnError(_ context.Context, _ string, err *a2a.Error, _ []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errors = append(a.errors, err)
}

func (a *recordingAdapter) OnAgentDiscovered(card *a2a.AgentCard) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.discovered = append(a.discovered, card)
}

func (a *recordingAdapter) OnAgentRemoved(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.removed = append(a.removed, name)
}

var _ GatewayAdapter = (*recordingAdapter)(nil)

func setupTestGateway(t *testing.T) (*GatewayCore, *broker.DevBroker, *recordingAdapter) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	adapter := &recordingAdapter{}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	}

	core := NewGatewayCore(adapter, appConfig)
	core.eventBindActive.Store(true)

	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())

	return core, brk, adapter
}

func TestGatewayCore_InitAndStart(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if core.GatewayID() != "test-gw" {
		t.Errorf("GatewayID = %q, want %q", core.GatewayID(), "test-gw")
	}
	if core.Namespace() != "test-ns" {
		t.Errorf("Namespace = %q, want %q", core.Namespace(), "test-ns")
	}

	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !brk.IsConnected() {
		t.Error("broker should be connected")
	}

	if err := core.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestGatewayCoreStart_ClearsHealth(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}

	core.health.Set("queue:leftover", health.Fatal, "queue no longer exists")

	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	if problems := core.health.Snapshot(); problems != nil {
		t.Errorf("health.Snapshot() after Start = %v, want nil — a restarted gateway must not carry a permanently orphaned problem from its previous run", problems)
	}
}

func TestGatewayCore_DispatchStatusUpdate(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	statusEvent := &a2a.TaskStatusUpdateEvent{
		TaskID: "task-123",
		Kind:   "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					a2a.NewTextPart("Working on it..."),
				},
			},
		},
	}

	resp := a2a.NewResponse("task-123", statusEvent)
	payload, _ := json.Marshal(resp)

	topic := a2a.GatewayStatusTopic("test-ns", "test-gw", "task-123")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish status: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.statusUpdates) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.statusUpdates) != 1 {
		t.Fatalf("got %d status updates, want 1", len(adapter.statusUpdates))
	}
	if adapter.statusUpdates[0].Status.State != a2a.TaskStateWorking {
		t.Errorf("state = %q, want %q", adapter.statusUpdates[0].Status.State, a2a.TaskStateWorking)
	}
}

func TestGatewayCore_DispatchArtifactUpdate(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	artifactEvent := &a2a.TaskArtifactUpdateEvent{
		TaskID: "task-123",
		Kind:   "artifact-update",
		Artifact: a2a.Artifact{
			ArtifactID: "art-1",
			Name:       "test.txt",
			Parts:      []a2a.Part{a2a.NewTextPart("file content")},
		},
	}

	resp := a2a.NewResponse("task-123", artifactEvent)
	payload, _ := json.Marshal(resp)

	topic := a2a.GatewayStatusTopic("test-ns", "test-gw", "task-123")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish artifact: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.artifactUpdates) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.artifactUpdates) != 1 {
		t.Fatalf("got %d artifact updates, want 1", len(adapter.artifactUpdates))
	}
	if adapter.artifactUpdates[0].Artifact.Name != "test.txt" {
		t.Errorf("artifact name = %q, want %q", adapter.artifactUpdates[0].Artifact.Name, "test.txt")
	}
}

func TestGatewayCore_DispatchFinalResponse(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	task := &a2a.Task{
		ID:   "task-123",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewTextPart("Done!")},
			},
		},
	}

	resp := a2a.NewResponse("task-123", task)
	payload, _ := json.Marshal(resp)

	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", "task-123")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish response: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.finalResponses) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.finalResponses) != 1 {
		t.Fatalf("got %d final responses, want 1", len(adapter.finalResponses))
	}
	if adapter.finalResponses[0].Status.State != a2a.TaskStateCompleted {
		t.Errorf("state = %q, want %q", adapter.finalResponses[0].Status.State, a2a.TaskStateCompleted)
	}
}

func TestGatewayCore_DispatchError(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	resp := a2a.NewErrorResponse("task-err", -32000, "agent failed")
	payload, _ := json.Marshal(resp)

	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", "task-err")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish error: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.errors) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(adapter.errors))
	}
	if adapter.errors[0].Code != -32000 {
		t.Errorf("error code = %d, want %d", adapter.errors[0].Code, -32000)
	}
}

func TestGatewayCore_DiscoverAgent(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	card := a2a.AgentCard{
		Name:        "test-agent",
		Description: "A test agent",
		Skills: []a2a.AgentSkill{
			{ID: "s1", Name: "skill1", Description: "First skill"},
		},
	}
	cardBytes, _ := json.Marshal(card)

	topic := a2a.DiscoveryAgentCardsTopic("test-ns")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: cardBytes}); err != nil {
		t.Fatalf("publish card: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.discovered) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.discovered) != 1 {
		t.Fatalf("got %d discovered, want 1", len(adapter.discovered))
	}
	if adapter.discovered[0].Name != "test-agent" {
		t.Errorf("discovered name = %q, want %q", adapter.discovered[0].Name, "test-agent")
	}

	if core.AgentRegistry().Count() != 1 {
		t.Errorf("registry count = %d, want 1", core.AgentRegistry().Count())
	}
}

func TestGatewayDiscoveryFanOutAcrossPods(t *testing.T) {
	t.Parallel()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	const namespace = "test-ns"
	const gatewayID = "fanout-gw"

	newPod := func() *GatewayCore {
		appConfig := map[string]any{
			"namespace":  namespace,
			"gateway_id": gatewayID,
		}
		core := NewGatewayCore(&recordingAdapter{}, appConfig)
		rt := runtime.NewRuntime(brk, nil)
		core.SetServices(rt.Services())
		return core
	}

	coreA := newPod()
	coreB := newPod()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := coreA.Init(ctx, nil); err != nil {
		t.Fatalf("init coreA: %v", err)
	}
	if err := coreA.Start(ctx); err != nil {
		t.Fatalf("start coreA: %v", err)
	}
	t.Cleanup(func() { _ = coreA.Stop(ctx) })

	if err := coreB.Init(ctx, nil); err != nil {
		t.Fatalf("init coreB: %v", err)
	}
	if err := coreB.Start(ctx); err != nil {
		t.Fatalf("start coreB: %v", err)
	}
	t.Cleanup(func() { _ = coreB.Stop(ctx) })

	card := a2a.AgentCard{
		Name:        "agent-fanout",
		Description: "test agent for gateway fan-out",
	}
	cardBytes, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	topic := a2a.DiscoveryAgentCardsTopic(namespace)
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: cardBytes}); err != nil {
		t.Fatalf("publish card: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		gotA := coreA.AgentRegistry().Get(card.Name) != nil
		gotB := coreB.AgentRegistry().Get(card.Name) != nil
		if gotA && gotB {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("discovery card fan-out failed: coreA received=%v, coreB received=%v "+
				"(both must be true for fan-out across gateway replicas sharing GatewayID)",
				gotA, gotB)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestGatewayCore_SubmitTask(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	agentQueue, err := brk.CreateQueue("test-agent-q", broker.QueueOptions{
		Named: true,
	})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	agentTopic := a2a.AgentRequestTopic("test-ns", "target-agent")
	if err = brk.Subscribe(agentQueue, agentTopic); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Hello agent")},
		ContextID: "session-1",
	}
	if err = core.SubmitTask(ctx, "target-agent", "task-submit-1", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, agentQueue)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received == nil {
		t.Fatal("received nil message")
	}

	var req a2a.Request
	if err := json.Unmarshal(received.Payload, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.ID != "task-submit-1" {
		t.Errorf("request ID = %q, want %q", req.ID, "task-submit-1")
	}
	if req.Method != "message/stream" {
		t.Errorf("request method = %q, want %q", req.Method, "message/stream")
	}

	if got := wireAgentNameMetadata(t, received.Payload); got != "target-agent" {
		t.Errorf("metadata agent_name = %q, want %q (stamped by SubmitTask)", got, "target-agent")
	}

	if received.UserProperties["clientId"] != "test-gw" {
		t.Errorf("clientId = %q, want %q", received.UserProperties["clientId"], "test-gw")
	}
	if received.UserProperties["userId"] != "user-1" {
		t.Errorf("userId = %q, want %q", received.UserProperties["userId"], "user-1")
	}
	if received.UserProperties["replyTo"] == "" {
		t.Error("replyTo should be set")
	}
	if received.UserProperties["a2aStatusTopic"] == "" {
		t.Error("a2aStatusTopic should be set")
	}

	if received.TTL != time.Hour {
		t.Errorf("TTL = %v, want %v", received.TTL, time.Hour)
	}
}

func wireAgentNameMetadata(t *testing.T, payload []byte) string {
	t.Helper()
	var envelope struct {
		Params struct {
			Message struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal published request: %v", err)
	}
	name, _ := envelope.Params.Message.Metadata["agent_name"].(string)
	return name
}

func TestGatewayCore_SubmitTask_PreservesCallerAgentNameMetadata(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	agentQueue, err := brk.CreateQueue("test-agent-q-meta", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err = brk.Subscribe(agentQueue, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-meta-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Hello agent")},
		ContextID: "session-1",
		Metadata:  map[string]any{"agent_name": "caller-chosen-name"},
	}
	if err = core.SubmitTask(ctx, "target-agent", "task-meta-1", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, agentQueue)
	if err != nil || received == nil {
		t.Fatalf("receive: %v", err)
	}
	if got := wireAgentNameMetadata(t, received.Payload); got != "caller-chosen-name" {
		t.Errorf("metadata agent_name = %q, want caller value preserved", got)
	}

	empty := a2a.Message{
		Kind:      "message",
		MessageID: "msg-meta-2",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Hello again")},
		ContextID: "session-1",
		Metadata:  map[string]any{"agent_name": ""},
	}
	if err = core.SubmitTask(ctx, "target-agent", "task-meta-2", empty, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	recvCtx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	received2, err := brk.Receive(recvCtx2, agentQueue)
	if err != nil || received2 == nil {
		t.Fatalf("receive: %v", err)
	}
	if got := wireAgentNameMetadata(t, received2.Payload); got != "target-agent" {
		t.Errorf("metadata agent_name = %q, want empty caller stamp replaced with resolved name", got)
	}
}

func TestGatewayCore_SubmitUserInputResponse_PropagatesScopes(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	agentQueue, err := brk.CreateQueue("hil-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err = brk.Subscribe(agentQueue, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	wantScopes := []string{"agent:target-agent:invoke", "tool:foo:read"}
	ctxWithAuth := auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        wantScopes,
	}))
	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-1", Status: "answered"}
	if err = core.SubmitUserInputResponse(ctxWithAuth, "target-agent", "task-hil-1", resp, stagedCaller(ctxWithAuth)); err != nil {
		t.Fatalf("SubmitUserInputResponse: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, agentQueue)
	if err != nil || received == nil {
		t.Fatalf("receive: err=%v msg=%v", err, received)
	}

	props := a2a.DecodeUserProperties(received.UserProperties)
	if props.A2AUserConfig == "" {
		t.Fatal("A2AUserConfig empty; the HIL leg dropped _enterprise_capabilities (regression)")
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(props.A2AUserConfig), &cfg); err != nil {
		t.Fatalf("unmarshal A2AUserConfig: %v", err)
	}
	gotCaps := map[string]bool{}
	if rawCaps, ok := cfg["_enterprise_capabilities"].([]any); ok {
		for _, x := range rawCaps {
			if s, ok := x.(string); ok {
				gotCaps[s] = true
			}
		}
	}
	for _, want := range wantScopes {
		if !gotCaps[want] {
			t.Errorf("HIL _enterprise_capabilities missing %q; got %v", want, cfg["_enterprise_capabilities"])
		}
	}
}

func TestGatewayCore_SubmitTask_RespectsTaskRequestTTLOverride(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":                "test-ns",
		"gateway_id":               "test-gw",
		"task_request_ttl_seconds": 0,
	})
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())

	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	agentQueue, err := brk.CreateQueue("test-agent-q-ttl-off", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	agentTopic := a2a.AgentRequestTopic("test-ns", "target-agent")
	if err = brk.Subscribe(agentQueue, agentTopic); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Hello agent")},
		ContextID: "session-1",
	}
	if err = core.SubmitTask(ctx, "target-agent", "task-submit-ttl-off", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, agentQueue)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received.TTL != 0 {
		t.Errorf("TTL = %v, want 0 (disabled)", received.TTL)
	}
}

func TestGatewayCore_SubmitTask_ResolvesDisplayNameToBrokerName(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	const (
		brokerName  = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
		displayName = "Uppercaser"
	)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: brokerName,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIDisplayName,
				Params: map[string]any{"display_name": displayName},
			}},
		},
	})

	agentQueue, err := brk.CreateQueue("test-resolve-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err = brk.Subscribe(agentQueue, a2a.AgentRequestTopic("test-ns", brokerName)); err != nil {
		t.Fatalf("subscribe broker-name topic: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-resolve",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hi")},
		ContextID: "session-resolve",
	}
	if err = core.SubmitTask(ctx, displayName, "task-resolve-1", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, agentQueue)
	if err != nil {
		t.Fatalf("receive on broker-name topic: %v (resolver did not map %q -> %q)", err, displayName, brokerName)
	}
	if received == nil {
		t.Fatal("received nil on broker-name topic — resolver dropped the message")
	}

	if got := core.Tasks().AgentName("task-resolve-1"); got != brokerName {
		t.Errorf("Tasks().AgentName(task-resolve-1) = %q; want %q", got, brokerName)
	}
}

func TestGatewayCore_SubmitTask_DisplayNameSkipsTestModeWrapper(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	const (
		workflowName = "workflow_01a1d59c_c06d_717c_85fc_d067b3322cdd"
		wrapperName  = "agent_01a1d59c_c522_73d4_86b1_60f1c71fe0f8"
		displayName  = "MessageResolutionWorkflow"
	)
	displayExt := a2a.AgentExtension{
		URI:    a2a.ExtensionURIDisplayName,
		Params: map[string]any{"display_name": displayName},
	}
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name:         workflowName,
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{displayExt}},
	})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: wrapperName,
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
			displayExt,
			{URI: a2a.EphemeralAgentExtensionURI},
		}},
	})

	workflowQueue, err := brk.CreateQueue("test-workflow-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create workflow queue: %v", err)
	}
	if err = brk.Subscribe(workflowQueue, a2a.AgentRequestTopic("test-ns", workflowName)); err != nil {
		t.Fatalf("subscribe workflow topic: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-wrapper",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart(`{"payload": {}}`)},
		ContextID: "session-wrapper",
	}
	if err = core.SubmitTask(ctx, displayName, "task-wrapper-1", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "system"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if received, err := brk.Receive(recvCtx, workflowQueue); err != nil || received == nil {
		t.Fatalf("receive on workflow topic: %v (task went to the Test-mode wrapper instead)", err)
	}
	if got := core.Tasks().AgentName("task-wrapper-1"); got != workflowName {
		t.Errorf("Tasks().AgentName(task-wrapper-1) = %q; want %q", got, workflowName)
	}
}

func TestGatewayCore_SubmitTask_FallsThroughUnknownName(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	agentQueue, err := brk.CreateQueue("test-fallthrough-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err = brk.Subscribe(agentQueue, a2a.AgentRequestTopic("test-ns", "Concierge")); err != nil {
		t.Fatalf("subscribe literal-name topic: %v", err)
	}

	msg := a2a.Message{Kind: "message", MessageID: "m", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("x")}, ContextID: "s"}
	if err = core.SubmitTask(ctx, "Concierge", "task-fallthrough", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err = brk.Receive(recvCtx, agentQueue); err != nil {
		t.Fatalf("receive on literal-name topic: %v (resolver dropped a pre-discovery dispatch)", err)
	}
}

func TestGatewayCore_SubmitTask_WarnsOnUndiscoveredTarget(t *testing.T) {
	const (
		brokerName  = "workflow_01a0d2c2_c7fc_70ea_978d_f896ae5ad7c2"
		displayName = "MuleDetectionWorkflow"
	)
	cases := []struct {
		target   string
		wantWarn bool
	}{
		{target: "mule_detection_workflow", wantWarn: true},
		{target: displayName, wantWarn: false},
		{target: brokerName, wantWarn: false},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			core, _, _ := setupTestGateway(t)
			ctx := t.Context()
			if err := core.Init(ctx, nil); err != nil {
				t.Fatalf("Init: %v", err)
			}
			var logs safeBuffer
			core.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
			core.targetDiscoveryWarmup = 0
			if err := core.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = core.Stop(ctx) }()

			core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
				Name: brokerName,
				Capabilities: a2a.AgentCapabilities{
					Extensions: []a2a.AgentExtension{{
						URI:    a2a.ExtensionURIDisplayName,
						Params: map[string]any{"display_name": displayName},
					}},
				},
			})

			msg := a2a.Message{Kind: "message", MessageID: "m", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("x")}, ContextID: "s"}
			if err := core.SubmitTask(ctx, tc.target, "task-undiscovered", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
				t.Fatalf("SubmitTask: %v", err)
			}

			warned := strings.Contains(logs.String(), "task target matches no discovered agent or workflow")
			if warned != tc.wantWarn {
				t.Fatalf("warned = %v, want %v; logs:\n%s", warned, tc.wantWarn, logs.String())
			}
			if tc.wantWarn && !strings.Contains(logs.String(), `"agentName":"`+tc.target+`"`) {
				t.Errorf("warning does not name the unresolved target %q; logs:\n%s", tc.target, logs.String())
			}
			if tc.wantWarn && !strings.Contains(logs.String(), `"traceID":"`) {
				t.Errorf("warning carries no traceID; logs:\n%s", logs.String())
			}
			if tc.wantWarn && !strings.Contains(logs.String(), `"registryCount":1`) {
				t.Errorf("warning does not report the registry size; logs:\n%s", logs.String())
			}
		})
	}
}

func TestGatewayCore_SubmitTask_UndiscoveredTargetIsDebugDuringWarmup(t *testing.T) {
	const msgText = "task target matches no discovered agent or workflow"
	core, _, _ := setupTestGateway(t)
	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	var logs safeBuffer
	core.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	msg := a2a.Message{Kind: "message", MessageID: "m", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("x")}, ContextID: "s"}
	if err := core.SubmitTask(ctx, "NotYetDiscovered", "task-warmup", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	var sawDebug bool
	for line := range strings.SplitSeq(logs.String(), "\n") {
		if !strings.Contains(line, msgText) {
			continue
		}
		if !strings.Contains(line, `"level":"DEBUG"`) {
			t.Fatalf("undiscovered target during warm-up logged above DEBUG: %s", line)
		}
		sawDebug = true
	}
	if !sawDebug {
		t.Fatalf("undiscovered target during warm-up not logged at DEBUG; logs:\n%s", logs.String())
	}
}

func TestGatewayCore_SubmitTask_AuthorizesResolvedBrokerName(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	const (
		brokerName  = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
		displayName = "Uppercaser"
	)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: brokerName,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIDisplayName,
				Params: map[string]any{"display_name": displayName},
			}},
		},
	})

	agentQueue, err := brk.CreateQueue("authz-order-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(agentQueue, a2a.AgentRequestTopic("test-ns", brokerName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        []string{"agent:" + displayName + ":invoke"},
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-authz-order",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hi")},
	}
	err = core.SubmitTask(ctxWithAuth, displayName, "task-authz-order", msg, stagedCaller(ctxWithAuth))
	if err == nil {
		t.Fatal("expected ErrAgentDelegationDenied (resolved broker name lacks scope), got nil")
	}
	if !errors.Is(err, ErrAgentDelegationDenied) {
		t.Fatalf("expected ErrAgentDelegationDenied, got %v", err)
	}
	if !strings.Contains(err.Error(), brokerName) {
		t.Errorf("error must surface the resolved broker name to prove ordering, got: %q", err.Error())
	}

	recvCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := brk.Receive(recvCtx, agentQueue); err == nil {
		t.Error("a message was published despite the deny — SubmitTask path is broken")
	}
}

func TestGatewayCore_CanDelegate(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	ctx := context.Background()

	if !core.CanDelegate(ctx, stagedCaller(ctx), "research") {
		t.Error("no identity in ctx should be allowed when authz is not enforced")
	}

	granted := auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        []string{"agent:research:invoke"},
	}))
	if !core.CanDelegate(granted, stagedCaller(granted), "research") {
		t.Error("research: identity holds agent:research:invoke, should be allowed")
	}
	if core.CanDelegate(granted, stagedCaller(granted), "billing") {
		t.Error("billing: identity lacks agent:billing:invoke, should be denied (hidden from dropdown)")
	}

	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"
	agentWildcard := auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
		UserID: "bob", Authenticated: true, Scopes: []string{"agent:*:invoke"},
	}))
	if core.CanDelegate(agentWildcard, stagedCaller(agentWildcard), wfCard) {
		t.Error("workflow card: agent:*:invoke must not cover the workflow category")
	}
	wfGranted := auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
		UserID: "carol", Authenticated: true, Scopes: []string{"workflow:" + dashedUUID + ":invoke"},
	}))
	if !core.CanDelegate(wfGranted, stagedCaller(wfGranted), wfCard) {
		t.Error("workflow card: identity holds workflow:<uuid>:invoke, should be allowed")
	}
}

func TestGatewayCore_CanAccessAgent(t *testing.T) {
	core, _, _ := setupTestGateway(t)

	adminOnly := a2a.AgentCard{
		Name: "observability-agent",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIRequiredScopes,
				Params: map[string]any{"scopes": []any{"analytics:_:read"}},
			}},
		},
	}
	open := a2a.AgentCard{Name: "research"}

	invokeOnly := auth.ContextWithSubject(context.Background(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice", Authenticated: true, Scopes: []string{"agent:*:invoke"},
	}))
	fullyScoped := auth.ContextWithSubject(context.Background(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "bob", Authenticated: true, Scopes: []string{"agent:*:invoke", "analytics:_:read"},
	}))

	if !core.CanAccessAgent(invokeOnly, stagedCaller(invokeOnly), open) {
		t.Error("open agent (no required_scopes) should be accessible to an invoke holder")
	}
	if core.CanAccessAgent(invokeOnly, stagedCaller(invokeOnly), adminOnly) {
		t.Error("admin-only agent must be denied: caller has agent:*:invoke but lacks analytics:_:read")
	}
	if !core.CanAccessAgent(fullyScoped, stagedCaller(fullyScoped), adminOnly) {
		t.Error("admin-only agent should be accessible to a caller holding analytics:_:read")
	}

	noInvoke := auth.ContextWithSubject(context.Background(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "carol", Authenticated: true, Scopes: []string{"agent:other:invoke"},
	}))
	if core.CanAccessAgent(noInvoke, stagedCaller(noInvoke), open) {
		t.Error("agent must be denied: caller lacks agent:research:invoke")
	}

	if !core.CanAccessAgent(context.Background(), stagedCaller(context.Background()), adminOnly) {
		t.Error("no-identity caller should be allowed when authz is not enforced")
	}
}

func TestGatewayCore_CanDelegate_Workflow(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	ctx := context.Background()

	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"

	withScopes := func(scopes ...string) context.Context {
		return auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: scopes}))
	}

	if !core.CanDelegate(withScopes("workflow:"+dashedUUID+":invoke"), stagedCaller(withScopes("workflow:"+dashedUUID+":invoke")), wfCard) {
		t.Error("workflow:<uuid>:invoke should allow invoking the deployed workflow")
	}
	if !core.CanDelegate(withScopes("workflow:*:invoke"), stagedCaller(withScopes("workflow:*:invoke")), wfCard) {
		t.Error("workflow:*:invoke should allow invoking the workflow")
	}
	if core.CanDelegate(withScopes("agent:*:invoke"), stagedCaller(withScopes("agent:*:invoke")), wfCard) {
		t.Error("agent:*:invoke must NOT reach a workflow target (workflow category not covered)")
	}
}

func TestGatewayCore_CanDelegate_WorkflowByAgentType(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	ctx := context.Background()

	const wf = "data-pipeline"
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: wf,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIAgentType,
				Params: map[string]any{"type": "workflow"},
			}},
		},
	})

	withScopes := func(scopes ...string) context.Context {
		return auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: scopes}))
	}

	if core.CanDelegate(withScopes("agent:*:invoke"), stagedCaller(withScopes("agent:*:invoke")), wf) {
		t.Error("agent:*:invoke must NOT reach a friendly-named workflow (workflow category)")
	}
	if core.CanDelegate(withScopes("agent:"+wf+":invoke"), stagedCaller(withScopes("agent:"+wf+":invoke")), wf) {
		t.Error("agent:data-pipeline:invoke must NOT reach the workflow; the receiver requires the workflow scope")
	}
	if !core.CanDelegate(withScopes("workflow:"+wf+":invoke"), stagedCaller(withScopes("workflow:"+wf+":invoke")), wf) {
		t.Error("workflow:data-pipeline:invoke should allow invoking the friendly-named workflow")
	}
}

func TestGatewayCore_ResolveAgentName_CollisionPicksLowestBrokerName(t *testing.T) {
	core, _, _ := setupTestGateway(t)

	const displayName = "Echo"
	const lower = "agent_aaa00000_0000_0000_0000_000000000000"
	const higher = "agent_bbb00000_0000_0000_0000_000000000000"

	for _, name := range []string{higher, lower} {
		core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
			Name: name,
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI:    a2a.ExtensionURIDisplayName,
					Params: map[string]any{"display_name": displayName},
				}},
			},
		})
	}

	for i := range 50 {
		if got, _ := core.resolveAgentName(displayName); got != lower {
			t.Fatalf("iter %d: resolveAgentName(%q) = %q; want %q (lowest broker name)", i, displayName, got, lower)
		}
	}
}

func TestExtractTaskIDFromTopic(t *testing.T) {
	tests := []struct {
		topic string
		want  string
	}{
		{"ns/a2a/v1/gateway/status/gw1/task-abc", "task-abc"},
		{"ns/a2a/v1/gateway/response/gw1/task-xyz", "task-xyz"},
		{"single", "single"},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractTaskIDFromTopic(tt.topic)
		if got != tt.want {
			t.Errorf("extractTaskIDFromTopic(%q) = %q, want %q", tt.topic, got, tt.want)
		}
	}
}

type mockTaskSigner struct {
	lastUserID string
	lastTaskID string
}

func (m *mockTaskSigner) SignTask(identity *auth.Subject, taskID string) (string, error) {
	m.lastUserID = identity.UserID()
	m.lastTaskID = taskID
	return "signed-token-for-" + taskID, nil
}

func TestGatewayCore_SubmitTask_WithTaskSigner(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	signer := &mockTaskSigner{}
	core.SetTaskSigner(signer)

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	agentQueue, err := brk.CreateQueue("test-agent-signer-q", broker.QueueOptions{
		Named: true,
	})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	agentTopic := a2a.AgentRequestTopic("test-ns", "target-agent")
	if err = brk.Subscribe(agentQueue, agentTopic); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Email:         "alice@example.com",
		Authenticated: true,
		Roles:         []string{"admin"},
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-signer-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Hello with auth")},
	}
	if err = core.SubmitTask(ctxWithAuth, "target-agent", "task-signed-1", msg, stagedCaller(ctxWithAuth)); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, agentQueue)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received == nil {
		t.Fatal("received nil message")
	}

	if signer.lastUserID != "alice" {
		t.Errorf("signer UserID = %q, want %q", signer.lastUserID, "alice")
	}
	if signer.lastTaskID != "task-signed-1" {
		t.Errorf("signer TaskID = %q, want %q", signer.lastTaskID, "task-signed-1")
	}

	authToken := received.UserProperties["authToken"]
	if authToken != "signed-token-for-task-signed-1" {
		t.Errorf("authToken = %q, want %q", authToken, "signed-token-for-task-signed-1")
	}
}

func TestGatewayCore_SubmitTask_WithoutTaskSigner(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	agentQueue, err := brk.CreateQueue("test-agent-nosign-q", broker.QueueOptions{
		Named: true,
	})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	agentTopic := a2a.AgentRequestTopic("test-ns", "target-agent")
	if err = brk.Subscribe(agentQueue, agentTopic); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-nosign-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Hello no auth")},
	}
	if err = core.SubmitTask(ctx, "target-agent", "task-nosign-1", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, agentQueue)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received == nil {
		t.Fatal("received nil message")
	}

	if authToken, exists := received.UserProperties["authToken"]; exists && authToken != "" {
		t.Errorf("authToken should be empty without TaskSigner, got %q", authToken)
	}
}

func TestGatewayCore_SubmitTask_DenyMissingDelegateScope(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("deny-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "restricted-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        []string{"tool:artifact:create", "agent:other-agent:invoke"},
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-deny",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hi")},
	}
	err = core.SubmitTask(ctxWithAuth, "restricted-agent", "task-deny", msg, stagedCaller(ctxWithAuth))
	if err == nil {
		t.Fatal("expected ErrAgentDelegationDenied, got nil")
	}
	if !errors.Is(err, ErrAgentDelegationDenied) {
		t.Fatalf("expected ErrAgentDelegationDenied, got %v", err)
	}
	if !strings.Contains(err.Error(), "restricted-agent") {
		t.Errorf("expected error to name the agent, got %q", err.Error())
	}

	recvCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received != nil {
		t.Errorf("expected no message published, got payload=%s", string(received.Payload))
	}
}

func TestGatewayCore_SubmitTask_AllowWithDelegateScope(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("allow-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        []string{"agent:target-agent:invoke"},
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-allow",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hi")},
	}
	if err = core.SubmitTask(ctxWithAuth, "target-agent", "task-allow", msg, stagedCaller(ctxWithAuth)); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received == nil {
		t.Fatal("expected message to be published")
	}
}

func TestGatewayCore_SubmitTask_WorkflowRequiresWorkflowScope(t *testing.T) {
	const wfCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	const dashedUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"

	tests := []struct {
		name        string
		scopes      []string
		wantAllowed bool
	}{
		{"agent wildcard does not cover workflow", []string{"agent:*:invoke"}, false},
		{"workflow dashed-uuid scope allows", []string{"workflow:" + dashedUUID + ":invoke"}, true},
		{"workflow wildcard allows", []string{"workflow:*:invoke"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, brk, _ := setupTestGateway(t)
			ctx := context.Background()
			if err := core.Init(ctx, nil); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if err := core.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = core.Stop(ctx) }()

			q, err := brk.CreateQueue("wf-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", wfCard)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			identity := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true, Scopes: tt.scopes})
			ctxWithAuth := auth.ContextWithSubject(ctx, identity)
			msg := a2a.Message{
				Kind:      "message",
				MessageID: "msg-wf",
				Role:      a2a.RoleUser,
				Parts:     []a2a.Part{a2a.NewTextPart("hi")},
			}
			err = core.SubmitTask(ctxWithAuth, wfCard, "task-wf", msg, stagedCaller(ctxWithAuth))
			if tt.wantAllowed {
				if err != nil {
					t.Fatalf("expected allow, got %v", err)
				}
				return
			}
			if !errors.Is(err, ErrAgentDelegationDenied) {
				t.Fatalf("expected ErrAgentDelegationDenied, got %v", err)
			}
		})
	}
}

func TestGatewayCore_SubmitTask_BuilderRequiresBuilderScope(t *testing.T) {
	tests := []struct {
		name        string
		scopes      []string
		wantAllowed bool
	}{
		{
			name:        "invoke scope without builder scope is denied",
			scopes:      []string{"agent:Builder:invoke"},
			wantAllowed: false,
		},
		{
			name:        "builder scope is allowed",
			scopes:      []string{rbac.ScopeBuilderUse},
			wantAllowed: true,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, brk, _ := setupTestGateway(t)
			ctx := context.Background()

			if err := core.Init(ctx, nil); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if err := core.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = core.Stop(ctx) }()

			qName := fmt.Sprintf("builder-q-%d", i)
			q, err := brk.CreateQueue(qName, broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", BuilderAgentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			identity := auth.NewTestSubject(auth.SubjectFields{
				UserID:        "alice",
				Authenticated: true,
				Scopes:        tc.scopes,
			})
			ctxWithAuth := auth.ContextWithSubject(ctx, identity)

			msg := a2a.Message{
				Kind:      "message",
				MessageID: "msg-builder",
				Role:      a2a.RoleUser,
				Parts:     []a2a.Part{a2a.NewTextPart("hi")},
			}
			err = core.SubmitTask(ctxWithAuth, BuilderAgentName, "task-builder", msg, stagedCaller(ctxWithAuth))

			if tc.wantAllowed {
				if err != nil {
					t.Fatalf("SubmitTask: expected allow, got %v", err)
				}
				recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				received, rerr := brk.Receive(recvCtx, q)
				if rerr != nil {
					t.Fatalf("receive: %v", rerr)
				}
				if received == nil {
					t.Fatal("expected message to be published")
				}
				return
			}

			if !errors.Is(err, ErrAgentDelegationDenied) {
				t.Fatalf("expected ErrAgentDelegationDenied, got %v", err)
			}
			recvCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			received, _ := brk.Receive(recvCtx, q)
			if received != nil {
				t.Errorf("expected no message published, got payload=%s", string(received.Payload))
			}
		})
	}
}

func TestGatewayCore_SubmitTask_BuilderCommunityMode(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("builder-community-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", BuilderAgentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-builder-community",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hi")},
	}
	if err = core.SubmitTask(ctxWithAuth, BuilderAgentName, "task-builder-community", msg, stagedCaller(ctxWithAuth)); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received == nil {
		t.Fatal("expected message to be published")
	}
}

func TestGatewayCore_SubmitTask_PropagatesEnrichment(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("propagate-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:           "alice",
		Authenticated:    true,
		Scopes:           []string{"agent:target-agent:invoke", "tool:foo"},
		EnrichmentStatus: identity.EnrichmentOK,
		Profile: &identity.Profile{
			ID:          "u-1",
			DisplayName: "Alice",
			WorkEmail:   "alice@example.com",
			JobTitle:    "engineer",
			Extra: map[string]any{
				"mobilePhone":  "+1-555-0100",
				"supervisorId": "bob-id",
				"hireDate":     "2020-01-01",
				"costCenter":   "CC-42",
			},
		},
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, id)

	msg := a2a.Message{Kind: "message", MessageID: "msg-prop", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	if err = core.SubmitTask(ctxWithAuth, "target-agent", "task-prop", msg, stagedCaller(ctxWithAuth)); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	cfg := received.UserProperties["a2aUserConfig"]
	if cfg == "" {
		t.Fatalf("a2aUserConfig missing in user properties: %#v", received.UserProperties)
	}
	var extras map[string]any
	if err := json.Unmarshal([]byte(cfg), &extras); err != nil {
		t.Fatalf("a2aUserConfig parse: %v", err)
	}
	if extras["_enrichment_status"] != "ok" {
		t.Errorf("_enrichment_status = %v, want ok", extras["_enrichment_status"])
	}
	profile, ok := extras["user_profile"].(map[string]any)
	if !ok {
		t.Fatalf("user_profile missing or wrong type: %T", extras["user_profile"])
	}
	if profile["id"] != "u-1" || profile["displayName"] != "Alice" {
		t.Errorf("user_profile = %v; want id=u-1, displayName=Alice", profile)
	}
	if profile["workEmail"] != "alice@example.com" {
		t.Errorf("user_profile[workEmail] = %v, want alice@example.com", profile["workEmail"])
	}
	if profile["jobTitle"] != "engineer" {
		t.Errorf("user_profile[jobTitle] = %v, want engineer", profile["jobTitle"])
	}
	for field, want := range map[string]string{
		"mobilePhone":  "+1-555-0100",
		"supervisorId": "bob-id",
		"hireDate":     "2020-01-01",
		"costCenter":   "CC-42",
	} {
		if profile[field] != want {
			t.Errorf("user_profile[%s] = %v, want %q (must pass through unfiltered)", field, profile[field], want)
		}
	}
	caps, _ := extras["_enterprise_capabilities"].([]any)
	if len(caps) != 2 {
		t.Errorf("_enterprise_capabilities len = %d, want 2", len(caps))
	}
}

func TestGatewayCore_SubmitTask_PropagatesUnavailable(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("propagate-unavail-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	id := auth.NewTestSubject(auth.SubjectFields{
		UserID:           "alice",
		Authenticated:    true,
		Scopes:           []string{"agent:target-agent:invoke"},
		EnrichmentStatus: identity.EnrichmentUnavailable,
		EnrichmentReason: "profile lookup timed out",
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, id)

	msg := a2a.Message{Kind: "message", MessageID: "msg-prop-u", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	if err = core.SubmitTask(ctxWithAuth, "target-agent", "task-prop-u", msg, stagedCaller(ctxWithAuth)); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	cfg := received.UserProperties["a2aUserConfig"]
	var extras map[string]any
	if err := json.Unmarshal([]byte(cfg), &extras); err != nil {
		t.Fatalf("a2aUserConfig parse: %v", err)
	}
	if extras["_enrichment_status"] != "unavailable" {
		t.Errorf("_enrichment_status = %v, want unavailable", extras["_enrichment_status"])
	}
	if extras["_enrichment_reason"] != "profile lookup timed out" {
		t.Errorf("_enrichment_reason = %v", extras["_enrichment_reason"])
	}
	if _, hasProfile := extras["user_profile"]; hasProfile {
		t.Error("user_profile should be absent when enrichment unavailable")
	}
}

func TestGatewayCore_SubmitTask_AllowEmptyScopesCommunityMode(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("community-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "any-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "sam_dev_user",
		Authenticated: true,
		AuthMethod:    "development",
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-community",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hi")},
	}
	if err = core.SubmitTask(ctxWithAuth, "any-agent", "task-community", msg, stagedCaller(ctxWithAuth)); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received == nil {
		t.Fatal("expected message to be published in community mode")
	}
}

func TestGatewayCore_SubmitTask_BuilderDenyEmptyScopesEnterpriseMode(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	core.SetScopeResolver(&fixedScopeResolver{scopes: nil})
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("builder-denyall-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", BuilderAgentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-builder-denyall",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hi")},
	}
	err = core.SubmitTask(ctxWithAuth, BuilderAgentName, "task-builder-denyall", msg, stagedCaller(ctxWithAuth))
	if !errors.Is(err, ErrAgentDelegationDenied) {
		t.Fatalf("expected ErrAgentDelegationDenied, got %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received != nil {
		t.Errorf("expected no message published, got payload=%s", string(received.Payload))
	}
}

type fixedScopeResolver struct {
	scopes []string
}

func (r *fixedScopeResolver) ResolveScopes(_ string, _ []string) []string {
	return r.scopes
}

type spyAuthorizer struct {
	coreauth.NoopAgentAuthorizer
	mu       sync.Mutex
	calls    int
	lastArgs []string
	allow    bool
}

func (s *spyAuthorizer) FilterAgents(_ context.Context, _ *coreauth.Identity, names []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastArgs = names
	if s.allow {
		return names
	}
	return nil
}

func TestGatewayCore_CanDelegate_EnforcesWithoutTrust(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.SetAuthzEnabled(true)

	if _, isNoop := core.Services().AgentAuthorizer().(coreauth.NoopAgentAuthorizer); !isNoop {
		t.Fatalf("precondition: runtime AgentAuthorizer should be the trust-off Noop, got %T",
			core.Services().AgentAuthorizer())
	}

	denyCtx := auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice", Authenticated: true, Scopes: []string{"agent:other:invoke"},
	}))
	if core.CanDelegate(denyCtx, stagedCaller(denyCtx), "restricted") {
		t.Error("caller without agent:restricted:invoke must be denied under RBAC even with trust off")
	}

	allowCtx := auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice", Authenticated: true, Scopes: []string{"agent:restricted:invoke"},
	}))
	if !core.CanDelegate(allowCtx, stagedCaller(allowCtx), "restricted") {
		t.Error("caller holding agent:restricted:invoke must be allowed")
	}
}

func TestGatewayCore_CanDelegate_ConsultsInjectedAuthorizer(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.SetAuthzEnabled(true)
	spy := &spyAuthorizer{allow: true}
	core.SetAgentAuthorizer(spy)

	idCtx := auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
		UserID: "alice", Authenticated: true, Scopes: []string{"agent:x:invoke"},
	}))

	if !core.CanDelegate(idCtx, stagedCaller(idCtx), "some-agent") {
		t.Error("expected allow when injected authorizer permits")
	}
	if spy.calls == 0 {
		t.Fatal("CanDelegate did not consult the injected authorizer")
	}

	spy.allow = false
	if core.CanDelegate(idCtx, stagedCaller(idCtx), "some-agent") {
		t.Error("expected deny when injected authorizer refuses")
	}
}

func TestGatewayCore_SubmitTask_DenyEmptyScopesEnterpriseMode(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	core.SetScopeResolver(&fixedScopeResolver{scopes: nil})
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("denyall-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "restricted-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-denyall",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hi")},
	}
	err = core.SubmitTask(ctxWithAuth, "restricted-agent", "task-denyall", msg, stagedCaller(ctxWithAuth))
	if err == nil {
		t.Fatal("expected ErrAgentDelegationDenied in enterprise/deny_all mode, got nil")
	}
	if !errors.Is(err, ErrAgentDelegationDenied) {
		t.Fatalf("expected ErrAgentDelegationDenied, got %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if received, _ := brk.Receive(recvCtx, q); received != nil {
		t.Errorf("expected no message published in deny_all mode, got payload=%s", string(received.Payload))
	}
}

func TestGatewayCore_SubmitUserInputResponse_DenyMissingScope(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("hil-deny-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "restricted-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        []string{"agent:other-agent:invoke"},
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "req-1",
		Status:    "answered",
	}
	err = core.SubmitUserInputResponse(ctxWithAuth, "restricted-agent", "task-hil", resp, stagedCaller(ctxWithAuth))
	if err == nil {
		t.Fatal("expected ErrAgentDelegationDenied, got nil")
	}
	if !errors.Is(err, ErrAgentDelegationDenied) {
		t.Fatalf("expected ErrAgentDelegationDenied, got %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if received, _ := brk.Receive(recvCtx, q); received != nil {
		t.Errorf("expected no message published, got payload=%s", string(received.Payload))
	}
}

func TestGatewayCore_SubmitUserInputResponse_AllowWithScope(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	q, err := brk.CreateQueue("hil-allow-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	identity := auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
		Scopes:        []string{"agent:target-agent:invoke"},
	})
	ctxWithAuth := auth.ContextWithSubject(ctx, identity)

	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "req-1",
		Status:    "answered",
	}
	if err = core.SubmitUserInputResponse(ctxWithAuth, "target-agent", "task-hil-allow", resp, stagedCaller(ctxWithAuth)); err != nil {
		t.Fatalf("SubmitUserInputResponse: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received == nil {
		t.Fatal("expected message to be published")
	}
}

func TestGatewayCore_SubmitUserInputResponse_NoIdentityDeniedWhenEnforced(t *testing.T) {
	resp := a2a.UserInputResponseData{
		Type:      "user_input_response",
		RequestID: "req-1",
		Status:    "answered",
	}

	t.Run("enforcing denies and publishes nothing", func(t *testing.T) {
		core := newCanDelegateCore(t, "default_rbac")
		brk := core.Services().Broker()
		q, err := brk.CreateQueue("hil-noid-deny-q", broker.QueueOptions{Named: true})
		if err != nil {
			t.Fatalf("create queue: %v", err)
		}
		if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "some-agent")); err != nil {
			t.Fatalf("subscribe: %v", err)
		}

		err = core.SubmitUserInputResponse(context.Background(), "some-agent", "task-noid", resp, auth.ForUserIDOnly("alice@example.com"))
		if !errors.Is(err, ErrAgentDelegationDenied) {
			t.Fatalf("expected ErrAgentDelegationDenied, got %v", err)
		}

		recvCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if received, _ := brk.Receive(recvCtx, q); received != nil {
			t.Errorf("expected no message published, got payload=%s", string(received.Payload))
		}
	})

	t.Run("unenforced core allows and signs a bare identity from userID", func(t *testing.T) {
		core := newCanDelegateCore(t, "")
		signer := &mockTaskSigner{}
		core.SetTaskSigner(signer)

		if err := core.SubmitUserInputResponse(context.Background(), "some-agent", "task-noid", resp, auth.ForUserIDOnly("alice@example.com")); err != nil {
			t.Fatalf("community mode must not deny: %v", err)
		}
		if signer.lastUserID != "alice@example.com" {
			t.Errorf("the bare identity synthesized from userID must be what gets signed; signed userID = %q", signer.lastUserID)
		}
	})
}

func TestGatewayCore_DelegationDenialReason(t *testing.T) {
	const agent = "some-agent"
	invokeScope := rbac.AgentInvokeScope(agent)

	for _, tt := range []struct {
		name       string
		authzType  string
		noIdentity bool
		idScopes   []string
		want       string
	}{
		{name: "no identity under enforcement", authzType: "default_rbac", noIdentity: true, want: denyReasonNoIdentity},
		{name: "identity with no scopes under enforcement", authzType: "default_rbac", want: denyReasonNoScopes},
		{name: "identity holding another agent's scope", authzType: "default_rbac", idScopes: []string{rbac.AgentInvokeScope("other")}, want: denyReasonScope},
		{name: "identity holding the invoke scope is allowed", authzType: "default_rbac", idScopes: []string{invokeScope}, want: ""},
		{name: "no identity on an unenforced core is allowed", noIdentity: true, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			core := newCanDelegateCore(t, tt.authzType)
			ctx := context.Background()
			if !tt.noIdentity {
				ctx = auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
					UserID: "alice", Authenticated: true, Scopes: tt.idScopes,
				}))
			}
			if got := core.delegationDenialReason(ctx, stagedCaller(ctx), agent); got != tt.want {
				t.Errorf("reason = %q, want %q", got, tt.want)
			}
			if allowed, wantAllowed := core.CanDelegate(ctx, stagedCaller(ctx), agent), tt.want == ""; allowed != wantAllowed {
				t.Errorf("CanDelegate = %v but reason %q implies %v — the two have drifted", allowed, tt.want, wantAllowed)
			}
		})
	}
}

func TestGatewayCore_NilIdentityInContext(t *testing.T) {
	nilIdentityCtx := func() context.Context {
		return auth.ContextWithSubject(context.Background(), nil)
	}
	card := a2a.AgentCard{Name: "some-agent"}

	t.Run("enforcing core denies without panicking", func(t *testing.T) {
		core := newCanDelegateCore(t, "default_rbac")
		if core.CanDelegate(nilIdentityCtx(), stagedCaller(nilIdentityCtx()), "some-agent") {
			t.Error("a nil identity must be treated as no identity and denied under enforcement")
		}
		if core.CanAccessAgent(nilIdentityCtx(), stagedCaller(nilIdentityCtx()), card) {
			t.Error("CanAccessAgent must deny a nil identity under enforcement")
		}
		if err := core.authorizeAgentDelegation(nilIdentityCtx(), nil, "some-agent"); err == nil {
			t.Error("authorizeAgentDelegation must deny a nil identity under enforcement")
		}
	})

	t.Run("unenforced core allows and signs the explicit caller", func(t *testing.T) {
		core := newCanDelegateCore(t, "")
		signer := &mockTaskSigner{}
		core.SetTaskSigner(signer)
		resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-nil", Status: "answered"}

		if !core.CanAccessAgent(nilIdentityCtx(), stagedCaller(nilIdentityCtx()), card) {
			t.Error("an unenforced core must allow a nil identity, not deny it")
		}

		if err := core.SubmitUserInputResponse(nilIdentityCtx(), "some-agent", "task-nilid", resp, auth.ForUserIDOnly("alice@example.com")); err != nil {
			t.Fatalf("an unenforced core must not deny: %v", err)
		}
		if signer.lastUserID != "alice@example.com" {
			t.Errorf("the explicit caller must be signed, not the nil in ctx; signed userID = %q", signer.lastUserID)
		}
	})
}

func stagedCaller(ctx context.Context) *auth.Subject {
	id, _ := auth.SubjectFromContext(ctx)
	return id
}

func newCanDelegateCore(t *testing.T, authzType string) *GatewayCore {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	}
	if authzType != "" {
		appConfig["authorization_service"] = map[string]any{"type": authzType}
	}
	core := NewGatewayCore(&recordingAdapter{}, appConfig)
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return core
}

func TestGatewayCore_CanDelegate_Enforcement(t *testing.T) {
	const agent = "some-agent"
	invokeScope := rbac.AgentInvokeScope(agent)

	tests := []struct {
		name         string
		authzType    string
		resolver     *fixedScopeResolver
		authzEnabled bool
		idScopes     []string
		noIdentity   bool
		want         bool
	}{
		{
			name: "dev/community: no authz, no resolver, empty scopes allows",
			want: true,
		},
		{
			name:         "executor-set authzEnabled (resolver build failed), empty scopes denies",
			authzEnabled: true,
			want:         false,
		},
		{
			name:      "enforcing, identity holds wildcard agent scope allows",
			authzType: "default_rbac",
			idScopes:  []string{"agent:*:invoke"},
			want:      true,
		},
		{
			name:      "config default_rbac, no resolver, empty scopes denies (the bug)",
			authzType: "default_rbac",
			want:      false,
		},
		{
			name:      "config deny_all, empty scopes denies",
			authzType: "deny_all",
			want:      false,
		},
		{
			name:      "config none is non-enforcing, empty scopes allows",
			authzType: "none",
			want:      true,
		},
		{
			name:     "resolver present but resolves empty denies",
			resolver: &fixedScopeResolver{scopes: nil},
			want:     false,
		},
		{
			name:     "resolver resolves the invoke scope allows",
			resolver: &fixedScopeResolver{scopes: []string{invokeScope}},
			want:     true,
		},
		{
			name:      "enforcing, identity already holds invoke scope allows",
			authzType: "default_rbac",
			idScopes:  []string{invokeScope},
			want:      true,
		},
		{
			name:      "enforcing, identity holds only a different agent's scope denies",
			authzType: "default_rbac",
			idScopes:  []string{rbac.AgentInvokeScope("other-agent")},
			want:      false,
		},
		{
			name:       "no identity in ctx denies when enforcing",
			authzType:  "default_rbac",
			noIdentity: true,
			want:       false,
		},
		{
			name:       "no identity in ctx allows on an unenforced core (fixtures and embedders only)",
			noIdentity: true,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core := newCanDelegateCore(t, tt.authzType)
			if tt.resolver != nil {
				core.SetScopeResolver(tt.resolver)
			}
			if tt.authzEnabled {
				core.SetAuthzEnabled(true)
			}
			ctx := context.Background()
			if !tt.noIdentity {
				ctx = auth.ContextWithSubject(ctx, auth.NewTestSubject(auth.SubjectFields{
					UserID:        "alice",
					Authenticated: true,
					Scopes:        tt.idScopes,
				}))
			}
			if got := core.CanDelegate(ctx, stagedCaller(ctx), agent); got != tt.want {
				t.Fatalf("CanDelegate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGatewayCore_FailedTaskRoutedAsFinalResponse(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	task := &a2a.Task{
		ID:   "task-fail",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateFailed,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewTextPart("Something went wrong with the agent")},
			},
		},
	}

	resp := a2a.NewResponse("task-fail", task)
	payload, _ := json.Marshal(resp)

	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", "task-fail")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish response: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.finalResponses) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	if len(adapter.finalResponses) != 1 {
		t.Fatalf("got %d final responses, want 1", len(adapter.finalResponses))
	}
	if len(adapter.errors) != 0 {
		t.Fatalf("got %d errors, want 0 (failed task carries state in task body, not JSON-RPC error)", len(adapter.errors))
	}
	if adapter.finalResponses[0].Status.State != a2a.TaskStateFailed {
		t.Errorf("task state = %q, want %q", adapter.finalResponses[0].Status.State, a2a.TaskStateFailed)
	}
}

func TestGatewayCore_FailedTaskDataPartFinalResponse(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	task := &a2a.Task{
		ID:   "task-fail-data",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateFailed,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
					"type":          "structured_invocation_result",
					"status":        "error",
					"error_message": "Validation failed: missing required field",
				})},
			},
		},
	}

	resp := a2a.NewResponse("task-fail-data", task)
	payload, _ := json.Marshal(resp)

	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", "task-fail-data")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish response: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.finalResponses) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	if len(adapter.finalResponses) != 1 {
		t.Fatalf("got %d final responses, want 1", len(adapter.finalResponses))
	}
	if len(adapter.errors) != 0 {
		t.Fatalf("got %d errors, want 0", len(adapter.errors))
	}
	if adapter.finalResponses[0].Status.State != a2a.TaskStateFailed {
		t.Errorf("task state = %q, want %q", adapter.finalResponses[0].Status.State, a2a.TaskStateFailed)
	}
}

func TestGatewayCore_FailedTaskNoMessage(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	task := &a2a.Task{
		ID:   "task-fail-nomsg",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateFailed,
		},
	}

	resp := a2a.NewResponse("task-fail-nomsg", task)
	payload, _ := json.Marshal(resp)

	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", "task-fail-nomsg")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish response: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.finalResponses) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	if len(adapter.finalResponses) != 1 {
		t.Fatalf("got %d final responses, want 1", len(adapter.finalResponses))
	}
	if len(adapter.errors) != 0 {
		t.Fatalf("got %d errors, want 0", len(adapter.errors))
	}
	if adapter.finalResponses[0].Status.State != a2a.TaskStateFailed {
		t.Errorf("task state = %q, want %q", adapter.finalResponses[0].Status.State, a2a.TaskStateFailed)
	}
}

func TestGatewayCore_CompletedTaskNotRoutedAsError(t *testing.T) {
	core, brk, adapter := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	task := &a2a.Task{
		ID:   "task-ok",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewTextPart("All good")},
			},
		},
	}

	resp := a2a.NewResponse("task-ok", task)
	payload, _ := json.Marshal(resp)

	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", "task-ok")
	if err := brk.Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish response: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		return len(adapter.finalResponses) > 0
	})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	if len(adapter.finalResponses) != 1 {
		t.Fatalf("got %d final responses, want 1", len(adapter.finalResponses))
	}
	if len(adapter.errors) != 0 {
		t.Fatalf("got %d errors, want 0 (completed task should not be an error)", len(adapter.errors))
	}
}

func TestGatewayCore_DeprovisionEmitsSuccessLog(t *testing.T) {
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	originalDefault := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	core, _, _ := setupTestGateway(t)
	ctx := context.Background()

	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := core.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	logs := buf.String()

	wantEventQueue := "test-ns/q/gdk/gateway/test-gw"

	if !strings.Contains(logs, `level=INFO`) ||
		!strings.Contains(logs, `msg="deprovisioned queue"`) {
		t.Fatalf("expected an INFO \"deprovisioned queue\" log line; got:\n%s", logs)
	}
	if !strings.Contains(logs, wantEventQueue) {
		t.Fatalf("expected event queue %q in logs; got:\n%s", wantEventQueue, logs)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

type registerRefusingRegistry struct {
	TaskRegistry
}

func (r *registerRefusingRegistry) Register(string, string, string, TaskIdentity) error {
	return errors.New("database unreachable")
}

type publishRefusingBroker struct {
	broker.Broker
}

func (b *publishRefusingBroker) PublishGuaranteed(context.Context, string, *broker.Message) error {
	return errors.New("broker unreachable")
}

func TestGatewayCore_SubmitTask_RegisterFailureRefusesBeforePublish(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()
	core.tasks = &registerRefusingRegistry{TaskRegistry: core.tasks}

	agentQueue, err := brk.CreateQueue("refuse-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err := brk.Subscribe(agentQueue, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}

	msg := a2a.Message{Kind: "message", MessageID: "msg-refuse", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	if err := core.SubmitTask(ctx, "target-agent", "task-refuse", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err == nil {
		t.Fatal("SubmitTask must fail when the task cannot be recorded")
	}
	recvCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if got, err := brk.Receive(recvCtx, agentQueue); err == nil {
		t.Fatalf("nothing may be published for a task that was not recorded, got %s", got.Topic)
	}
}

func TestGatewayCore_SubmitTask_PublishFailureForgetsTheTask(t *testing.T) {
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()
	core.SetServices(runtime.NewRuntime(&publishRefusingBroker{Broker: brk}, nil).Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	msg := a2a.Message{Kind: "message", MessageID: "msg-pubfail", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	if err := core.SubmitTask(ctx, "target-agent", "task-pubfail", msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err == nil {
		t.Fatal("SubmitTask must report the publish failure")
	}
	if core.IsTaskRunning("task-pubfail") {
		t.Fatal("a task the broker never received must not stay recorded as running")
	}
}

func setupDBTestGateway(t *testing.T) (*GatewayCore, *broker.DevBroker, *recordingAdapter) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	adapter := &recordingAdapter{}
	core := NewGatewayCore(adapter, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestDBURL(t),
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	return core, brk, adapter
}

func TestGatewayCore_TaskRecord_FollowsTheTaskThroughTheCore(t *testing.T) {
	core, brk, adapter := setupDBTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()
	if _, ok := core.tasks.(*dbTaskRegistry); !ok {
		t.Fatalf("a gateway with a database must track tasks in it, got %T", core.tasks)
	}
	records := store.NewTaskRecordRepo(core.db, "test-gw")
	taskID := idgen.New()

	msg := a2a.Message{Kind: "message", MessageID: "msg-record", ContextID: "session-record", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	if err := core.SubmitTask(ctx, "target-agent", taskID, msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	rec, err := records.Get(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("the row must exist while the task runs")
	}
	if rec.AgentName != "target-agent" || rec.UserID != "user-1" || rec.SessionID != "session-record" || rec.TraceID == "" || rec.Trigger != a2a.TriggerInteractive {
		t.Fatalf("the row must carry the identity SubmitTask resolved: %+v", rec)
	}

	final := &a2a.Task{ID: taskID, Kind: "task", Status: a2a.TaskStatus{
		State:   a2a.TaskStateCompleted,
		Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("Done!")}},
	}}
	payload, _ := json.Marshal(a2a.NewResponse(taskID, final))
	if err := brk.Publish(ctx, a2a.GatewayResponseTopic("test-ns", "test-gw", taskID), &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish response: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		exists, err := records.Exists(ctx, taskID)
		return err == nil && !exists
	})
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.finalResponses) != 1 {
		t.Fatalf("got %d final responses, want 1", len(adapter.finalResponses))
	}
}

func TestGatewayCore_DeletingASessionCancelsItsTasksThroughTheRow(t *testing.T) {
	core, brk, _ := setupDBTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()
	agentQueue, err := brk.CreateQueue("cancel-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err = brk.Subscribe(agentQueue, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}
	sessions := store.NewSessionRepo(core.db)
	sessionID := idgen.New()
	name, source := "s", ""
	if err = sessions.Create(ctx, &store.Session{ID: sessionID, UserID: "user-1", Name: &name, Source: &source}); err != nil {
		t.Fatal(err)
	}
	taskID := idgen.New()
	msg := a2a.Message{Kind: "message", MessageID: "msg-cancel", ContextID: sessionID, Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	if err = core.SubmitTask(ctx, "target-agent", taskID, msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	drainCtx, cancelDrain := context.WithTimeout(ctx, 2*time.Second)
	if _, err = brk.Receive(drainCtx, agentQueue); err != nil {
		t.Fatalf("the submit itself must reach the agent queue: %v", err)
	}
	cancelDrain()

	if _, err = sessions.SoftDelete(ctx, sessionID, "user-1"); err != nil {
		t.Fatal(err)
	}
	core.RunTaskSweepNow()

	recvCtx, cancelRecv := context.WithTimeout(ctx, 3*time.Second)
	defer cancelRecv()
	got, err := brk.Receive(recvCtx, agentQueue)
	if err != nil {
		t.Fatalf("no cancel reached the agent after the session delete: %v", err)
	}
	var req a2a.Request
	if err := json.Unmarshal(got.Payload, &req); err != nil {
		t.Fatalf("decode cancel: %v", err)
	}
	if req.Method != "tasks/cancel" || req.ID != taskID {
		t.Fatalf("agent received %s for %v, want tasks/cancel for %s", req.Method, req.ID, taskID)
	}

	core.RunTaskSweepNow()
	quietCtx, cancelQuiet := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancelQuiet()
	if dup, err := brk.Receive(quietCtx, agentQueue); err == nil {
		t.Fatalf("a claimed cancel must not be published twice, got %s", dup.Payload)
	}
}

func TestGatewayCore_SweepKickDropsEntriesOfTasksEndedElsewhere(t *testing.T) {
	core, _, _ := setupDBTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()
	mine := core.tasks.(*dbTaskRegistry)
	other := NewDBTaskRegistry(mine.repo, silentLogger())
	taskID := idgen.New()
	if err := mine.Register(taskID, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	other.Remove(taskID)

	core.RunTaskSweepNow()
	waitFor(t, 3*time.Second, func() bool { return mine.Get(taskID) == nil })
}

func TestGatewayCore_TakesTheAppsRowsAndReapsRowsOfAppsWithNoInstance(t *testing.T) {
	core, _, _ := setupDBTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.cfg.TaskTimeoutSeconds = 60
	records := store.NewTaskRecordRepo(core.db, "test-gw")
	otherApp := store.NewTaskRecordRepo(core.db, "other-gw")
	stale := time.Now().Add(-2 * taskRecordLivenessGrace).UnixMilli()

	deadPodsTask, nobodysTask := idgen.New(), idgen.New()
	if _, err := records.Upsert(ctx, &store.TaskRecord{
		TaskID: deadPodsTask, AgentName: "target-agent", UserID: "user-1", LastSeenAt: stale,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := otherApp.Upsert(ctx, &store.TaskRecord{
		TaskID: nobodysTask, AgentName: "target-agent", UserID: "user-1", LastSeenAt: stale,
	}); err != nil {
		t.Fatal(err)
	}
	mine := idgen.New()
	if err := core.tasks.Register(mine, "target-agent", "user-1", TaskIdentity{TraceID: "t"}); err != nil {
		t.Fatal(err)
	}

	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()
	waitFor(t, 3*time.Second, func() bool { return core.tasks.Holds(deadPodsTask) })
	if err := core.cleanUpTaskRecords(ctx); err != nil {
		t.Fatalf("leader task record cleanup: %v", err)
	}
	if exists, err := records.Exists(ctx, nobodysTask); err != nil || exists {
		t.Errorf("the row of an app with no instance = exists %v (%v), want reaped", exists, err)
	}
	if !idleTimerArmed(core, deadPodsTask) {
		t.Error("the dead pod's task of this app must be taken and timed, not reaped")
	}
	rec, err := records.Get(ctx, deadPodsTask)
	if err != nil || rec == nil || rec.LastSeenAt == stale || rec.GatewayInstanceID != core.instanceID {
		t.Fatalf("the taken row must be restamped and name this instance: %+v, %v", rec, err)
	}
	if exists, err := records.Exists(ctx, mine); err != nil || !exists {
		t.Fatal("a task this instance still holds must survive the reaper")
	}
}

func TestGatewayCore_TaskRecord_IsRemovedWhenThePublishFails(t *testing.T) {
	core, brk, _ := setupDBTestGateway(t)
	ctx := context.Background()
	core.SetServices(runtime.NewRuntime(&publishRefusingBroker{Broker: brk}, nil).Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()
	records := store.NewTaskRecordRepo(core.db, "test-gw")
	taskID := idgen.New()

	msg := a2a.Message{Kind: "message", MessageID: "msg-pubfail-db", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
	if err := core.SubmitTask(ctx, "target-agent", taskID, msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"})); err == nil {
		t.Fatal("SubmitTask must report the publish failure")
	}
	exists, err := records.Exists(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("the row of a task the broker never received must be deleted")
	}
}

func TestGatewayCore_EventTraceID(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	if err := core.tasks.Register("task-known", "agent", "user", TaskIdentity{TraceID: "trace-registered"}); err != nil {
		t.Fatal(err)
	}

	if got := core.eventTraceID("task-known", map[string]string{a2a.PropTraceID: "trace-on-message"}); got != "trace-on-message" {
		t.Fatalf("the message property wins: got %q", got)
	}
	if got := core.eventTraceID("task-known", nil); got != "trace-registered" {
		t.Fatalf("without the property the registered id is used: got %q", got)
	}
	if got := core.eventTraceID("task-unknown", nil); got == "" {
		t.Fatal("an untracked task without the property still gets a trace id")
	}
}
