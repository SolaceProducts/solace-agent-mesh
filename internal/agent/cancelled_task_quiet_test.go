// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

const (
	quietTaskID      = "task-cancelled-quiet"
	quietReplyTo     = "test-ns/a2a/v1/gateway/response/web-1/" + quietTaskID
	quietStatusTopic = "test-ns/a2a/v1/gateway/status/web-1/" + quietTaskID
	toolUnwind       = 200 * time.Millisecond
)

func TestACancelledTaskPublishesNothingAfterItsCancel(t *testing.T) {
	agent, mock, _ := newTestAgent(slowToolTurns()...)
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	beforeCancel := cancelDuringSlowTool(t, agent, brk, nil)

	afterCancel := brk.getPublished()[beforeCancel:]
	for _, p := range afterCancel {
		if p.topic == quietStatusTopic {
			t.Errorf("status event published after the cancel: %s", describeStatusEvent(p.payload))
		}
	}
	if got := cancelledResponsesOn(brk, quietReplyTo); got != 1 {
		t.Errorf("canceled responses on the reply topic = %d, want 1", got)
	}
	if calls := len(mock.getCalls()); calls != 1 {
		t.Errorf("model calls = %d, want 1: a cancelled task must not call the model again", calls)
	}
}

func TestACancelledRunBasedTaskDeletesItsSession(t *testing.T) {
	agent, _, _ := newTestAgent(slowToolTurns()...)
	brk := newMockBroker()
	store := newSQLiteSessionStore(t)
	agent.svc.(*mockServices).brk = brk
	agent.svc.(*mockServices).sessions = store

	cancelDuringSlowTool(t, agent, brk, map[string]any{"sessionBehavior": "RUN_BASED"})

	sessions, err := store.ListSessions(t.Context(), agent.cfg.Name, quietUserID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("the cancelled run-based task left %d session(s) behind", len(sessions))
	}
}

func TestACancelledRunBasedTaskRunToCompletionDeletesItsSession(t *testing.T) {
	agent, _, _ := newTestAgent(slowToolTurns()...)
	agent.svc.(*mockServices).brk = newMockBroker()
	store := newSQLiteSessionStore(t)
	agent.svc.(*mockServices).sessions = store
	started := registerSlowTool(t, agent)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = agent.HandleTaskToCompletion(ctx, quietTaskID, a2a.Message{
			Kind: "message", MessageID: "m1", Role: a2a.RoleUser,
			Parts: []a2a.Part{a2a.NewTextPart("look it up")}, ContextID: "ctx-1",
			Metadata: map[string]any{"sessionBehavior": "RUN_BASED"},
		}, a2a.UserProperties{Timestamp: time.Now().UnixMilli(), UserID: quietUserID})
	}()
	waitOrFail(t, started, "the tool to start")
	cancel()
	waitOrFail(t, done, "the cancelled task to stop")

	assertNoSessionsLeft(t, store, agent)
}

func TestDeleteRunBasedSessionOutlivesACancelledContext(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	agent.svc.(*mockServices).sessions = store
	sessionID := idgen.New()
	if err := store.Create(t.Context(), &session.Session{
		AppName: agent.cfg.Name, UserID: quietUserID, SessionID: sessionID, ContextID: sessionID,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	state := &TaskExecutionState{EffectiveSessionID: sessionID}
	state.UserProps.UserID = quietUserID
	if err := agent.deleteRunBasedSession(cancelled, state); err != nil {
		t.Fatalf("delete on a cancelled context: %v", err)
	}
	assertNoSessionsLeft(t, store, agent)
}

func assertNoSessionsLeft(t *testing.T, store session.Store, agent *Agent) {
	t.Helper()
	sessions, err := store.ListSessions(t.Context(), agent.cfg.Name, quietUserID)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("the cancelled run-based task left %d session(s) behind", len(sessions))
	}
}

func TestACancelledResumedTaskPublishesNothingAfterItsCancel(t *testing.T) {
	store := session.NewMemoryStore()
	agent, mock, brk := replicaOf(t, store, "owner")
	mock.mu.Lock()
	mock.responses = slowToolTurns()
	mock.mu.Unlock()
	started := registerSlowTool(t, agent)

	ctx := t.Context()
	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	state.UserProps.A2AStatusTopic = quietStatusTopic
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := store.RecordToolResult(ctx, taskID, subTaskID, &session.ToolCallResult{
		ToolCallID: "tc-1", ToolName: "peer_agent", Content: "the peer answered",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	agent.pausedTasks.Store(state)

	agent.ResumeTask(taskID)
	waitOrFail(t, started, "the resumed turn's tool to start")
	beforeCancel := len(brk.getPublished())
	cancelOnTheWire(agent, taskID)
	if !waitUntil(func() bool { return agent.pausedTasks.Load(taskID) == nil && !hasActiveTask(agent, taskID) }) {
		t.Fatal("the cancelled resumed task never finished its teardown")
	}

	for _, p := range brk.getPublished()[beforeCancel:] {
		if p.topic == quietStatusTopic {
			t.Errorf("status event published after the cancel: %s", describeStatusEvent(p.payload))
		}
	}
}

func TestAnEventQueuedOnThePublishLockIsDroppedWhenTheTaskIsStopped(t *testing.T) {
	brk := newMockBroker()
	sp := NewStatusPublisher(brk, StatusPublisherConfig{
		StatusTopic: quietStatusTopic, TaskID: quietTaskID, AgentName: "test-agent",
	}, testLogger())
	sp.ClassifyModel(usagefacts.Model{Family: "f", Provider: "p"})
	ctx, endTurn, stopTask := cancellableTaskContext(t.Context())
	defer endTurn()

	sp.publishMu.Lock()
	published := make(chan struct{})
	go func() {
		defer close(published)
		sp.PublishGenericSignal(ctx, a2a.BuilderComponentProgressData{
			Type: string(a2a.SignalBuilderComponentProgress), ComponentID: "c1", State: "completed",
		}, string(a2a.SignalBuilderComponentProgress))
	}()
	if !waitUntil(blockedOnPublishLock) {
		sp.publishMu.Unlock()
		t.Fatal("the event never reached the publish lock")
	}
	stopTask()
	sp.publishMu.Unlock()
	waitOrFail(t, published, "the queued event to finish")

	if got := len(brk.getPublished()); got != 0 {
		t.Errorf("published %d status events after the task was stopped, want 0", got)
	}
	sp.modelMu.Lock()
	defer sp.modelMu.Unlock()
	if sp.modelStamped {
		t.Error("the dropped event used up the model announcement; the next event must carry it")
	}
}

func blockedOnPublishLock() bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "publishWithState") && strings.Contains(g, "sync.(*Mutex).Lock") {
			return true
		}
	}
	return false
}

func TestAStatusPublisherDropsOnlyAStoppedTasksEvents(t *testing.T) {
	cases := map[string]struct {
		end           func(endTurn context.CancelFunc, stopTask stopTaskFunc)
		wantPublished int
		wantStopped   bool
	}{
		"turn running":       {end: func(context.CancelFunc, stopTaskFunc) {}, wantPublished: 1},
		"turn ended, paused": {end: func(endTurn context.CancelFunc, _ stopTaskFunc) { endTurn() }, wantPublished: 1},
		"task stopped":       {end: func(_ context.CancelFunc, stopTask stopTaskFunc) { stopTask() }, wantStopped: true},
		"stopped, then turn ended": {
			end:         func(endTurn context.CancelFunc, stopTask stopTaskFunc) { stopTask(); endTurn() },
			wantStopped: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			brk := newMockBroker()
			sp := NewStatusPublisher(brk, StatusPublisherConfig{
				StatusTopic: quietStatusTopic, TaskID: quietTaskID, AgentName: "test-agent",
			}, testLogger())
			ctx, endTurn, stopTask := cancellableTaskContext(t.Context())
			defer endTurn()
			tc.end(endTurn, stopTask)

			sp.PublishGenericSignal(ctx, a2a.BuilderComponentProgressData{
				Type: string(a2a.SignalBuilderComponentProgress), ComponentID: "c1", State: "completed",
			}, string(a2a.SignalBuilderComponentProgress))

			if got := len(brk.getPublished()); got != tc.wantPublished {
				t.Errorf("published %d status events, want %d", got, tc.wantPublished)
			}
			if got := taskWasCancelled(ctx); got != tc.wantStopped {
				t.Errorf("taskWasCancelled = %v, want %v", got, tc.wantStopped)
			}
		})
	}
}

func TestAStoppedTasksHTTPCallReadsAsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, endTurn, stopTask := cancellableTaskContext(t.Context())
	defer endTurn()
	go func() {
		time.Sleep(50 * time.Millisecond)
		stopTask()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the request outlived the task's stop")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped task's HTTP call failed with %v, which does not match context.Canceled", err)
	}
}

const quietUserID = "alice@example.com"

func slowToolTurns() []*llm.ChatResponse {
	return []*llm.ChatResponse{
		{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "slow", Arguments: "{}"}}},
		{Content: "done", FinishReason: "stop"},
	}
}

func cancelDuringSlowTool(t *testing.T, agent *Agent, brk *mockBroker, metadata map[string]any) int {
	t.Helper()
	started := registerSlowTool(t, agent)

	done := startTaskOnTheWire(t, agent, quietTaskID, a2a.UserProperties{
		Timestamp: time.Now().UnixMilli(), ClientID: "web-1", UserID: quietUserID,
		ReplyTo: quietReplyTo, A2AStatusTopic: quietStatusTopic,
	}, a2a.Message{
		Kind: "message", MessageID: "m1", Role: a2a.RoleUser,
		Parts: []a2a.Part{a2a.NewTextPart("look it up")}, ContextID: "ctx-1",
		Metadata: metadata,
	})
	waitOrFail(t, started, "the tool to start")
	beforeCancel := len(brk.getPublished())

	cancelOnTheWire(agent, quietTaskID)
	waitOrFail(t, done, "the cancelled task to stop")
	return beforeCancel
}

func registerSlowTool(t *testing.T, agent *Agent) <-chan struct{} {
	t.Helper()
	started := make(chan struct{})
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "slow",
		description: "works until cancelled, then takes a moment to unwind",
		execFn: func(ic context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			close(started)
			<-ic.Done()
			time.Sleep(toolUnwind)
			return &tool.Result{Data: map[string]any{"rows": 3}}, nil
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	return started
}

func describeStatusEvent(payload []byte) string {
	var ev struct {
		Result struct {
			Status struct {
				Message struct {
					Parts []struct {
						Data map[string]any `json:"data"`
					} `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if json.Unmarshal(payload, &ev) != nil {
		return string(payload)
	}
	for _, part := range ev.Result.Status.Message.Parts {
		if typ, ok := part.Data["type"].(string); ok {
			return typ
		}
	}
	return string(payload)
}
