// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type failingAppendStore struct {
	session.Store
	fail atomic.Bool
}

func (s *failingAppendStore) AppendMessages(ctx context.Context, appName, userID, sessionID string, msgs []session.Message) error {
	if s.fail.Load() {
		return errors.New("synthetic append failure")
	}
	return s.Store.AppendMessages(ctx, appName, userID, sessionID, msgs)
}

type scriptedStore struct {
	session.Store
	saveSeq []error
	callIdx int
	getErr  error
	getNil  bool
}

func (s *scriptedStore) Save(ctx context.Context, sess *session.Session) error {
	var e error
	if s.callIdx < len(s.saveSeq) {
		e = s.saveSeq[s.callIdx]
	}
	s.callIdx++
	if e != nil {
		return e
	}
	return s.Store.Save(ctx, sess)
}

func (s *scriptedStore) Get(ctx context.Context, appName, userID, sessionID string) (*session.Session, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.getNil {
		return nil, nil
	}
	return s.Store.Get(ctx, appName, userID, sessionID)
}

func TestSaveSessionWithRetry_WritesBackInPlace(t *testing.T) {
	agent, _, store := newTestAgent()
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}

	saved, _, err := agent.saveSessionWithRetry(t.Context(), sess, func(s *session.Session) {
		s.Messages = append(s.Messages, session.Message{Role: "user", Content: "x"})
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved != sess {
		t.Fatal("returned a different pointer; external holders of the original session would be orphaned")
	}
	if len(sess.Messages) != 1 {
		t.Fatalf("caller's session not written back in place: %d messages", len(sess.Messages))
	}
}

func TestSaveSessionWithRetry_Paths(t *testing.T) {
	lock := session.ErrOptimisticLock
	dbDown := errors.New("db down")
	getFailed := errors.New("reload boom")
	sixLocks := []error{lock, lock, lock, lock, lock, lock}

	cases := []struct {
		name         string
		saveSeq      []error
		getErr       error
		getNil       bool
		wantErr      string
		wantReloaded bool
	}{
		{"commits on the first save", []error{nil}, nil, false, "", false},
		{"non-lock fault surfaces immediately", []error{dbDown}, nil, false, "db down", false},
		{"commits after conflicts", []error{lock, lock, nil}, nil, false, "", true},
		{"non-lock fault during retry", []error{lock, dbDown}, nil, false, "db down", false},
		{"reload error during retry", []error{lock}, getFailed, false, "reload session", false},
		{"row missing during retry", []error{lock}, nil, true, "row missing", false},
		{"gives up after the retry cap", sixLocks, nil, false, "after 5 retries", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			store := newSQLiteSessionStore(t)
			sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
			if err := store.Create(t.Context(), sess); err != nil {
				t.Fatalf("seed: %v", err)
			}
			agent.svc.(*mockServices).sessions = &scriptedStore{Store: store, saveSeq: tc.saveSeq, getErr: tc.getErr, getNil: tc.getNil}

			saved, reloaded, err := agent.saveSessionWithRetry(t.Context(), sess, func(s *session.Session) {
				s.Metadata = map[string]any{"k": "v"}
			})

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if reloaded != tc.wantReloaded {
				t.Fatalf("reloaded = %v, want %v", reloaded, tc.wantReloaded)
			}
			if saved != sess {
				t.Fatal("success must return the same session pointer")
			}
			persisted, _ := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
			if persisted.Metadata["k"] != "v" {
				t.Fatalf("metadata not persisted: %+v", persisted.Metadata)
			}
		})
	}
}

func TestFlushSessionMessagesFinal_ReportsUnrecoverableLoss(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs := &failingAppendStore{Store: store}
	fs.fail.Store(true)
	agent.svc.(*mockServices).sessions = fs

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "lost"}}
	rc := &taskRunContext{sess: sess}

	agent.flushSessionMessagesFinal(t.Context(), state, rc)

	persisted, _ := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if len(persisted.Messages) != 0 {
		t.Fatalf("expected nothing persisted on unrecoverable loss, got %d", len(persisted.Messages))
	}
}

type timeoutOnceAppendStore struct {
	session.Store
	calls atomic.Int32
}

func (s *timeoutOnceAppendStore) AppendMessages(ctx context.Context, appName, userID, sessionID string, msgs []session.Message) error {
	if s.calls.Add(1) == 1 {
		return context.DeadlineExceeded
	}
	return s.Store.AppendMessages(ctx, appName, userID, sessionID, msgs)
}

type slowFailingAppendStore struct {
	session.Store
	failFor  time.Duration
	slowCall int32
	calls    atomic.Int32
}

func (s *slowFailingAppendStore) AppendMessages(ctx context.Context, _, _, _ string, _ []session.Message) error {
	if s.calls.Add(1) == s.slowCall {
		time.Sleep(s.failFor)
	}
	return context.DeadlineExceeded
}

type saveLostCounter struct {
	runtime.NoopMetrics
	paths []string
}

func (m *saveLostCounter) Counter(name string, _ int64, labels ...string) {
	if name != metricSessionSaveLost {
		return
	}
	for i := 0; i+1 < len(labels); i += 2 {
		if labels[i] == "path" {
			m.paths = append(m.paths, labels[i+1])
		}
	}
}

func TestFlushSessionMessagesFinal_RetriesATimedOutAppend(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	slow := &timeoutOnceAppendStore{Store: store}
	metrics := &saveLostCounter{}
	svc := agent.svc.(*mockServices)
	svc.sessions = slow
	svc.metrics = metrics

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer"},
	}
	rc := &taskRunContext{sess: sess}

	agent.flushSessionMessagesFinal(t.Context(), state, rc)

	persisted, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(persisted.Messages) != 2 {
		t.Fatalf("persisted %d messages, want the 2-message turn tail: %+v", len(persisted.Messages), persisted.Messages)
	}
	if len(metrics.paths) != 0 {
		t.Fatalf("session.save.lost recorded %v, want none after a successful retry", metrics.paths)
	}
	if state.HistoryOffset != 2 {
		t.Fatalf("HistoryOffset = %d, want 2", state.HistoryOffset)
	}
}

func TestFlushSessionMessagesFinal_ReportsLossAfterRetriesExhausted(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs := &failingAppendStore{Store: store}
	fs.fail.Store(true)
	metrics := &saveLostCounter{}
	svc := agent.svc.(*mockServices)
	svc.sessions = fs
	svc.metrics = metrics

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "lost"}}

	agent.flushSessionMessagesFinal(t.Context(), state, &taskRunContext{sess: sess})

	if len(metrics.paths) != 1 || metrics.paths[0] != "flush" {
		t.Fatalf("session.save.lost paths = %v, want exactly [flush]", metrics.paths)
	}
}

func TestFlushSessionMessagesFinal_SkipsRetriesDuringShutdown(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	slow := &timeoutOnceAppendStore{Store: store}
	metrics := &saveLostCounter{}
	svc := agent.svc.(*mockServices)
	svc.sessions = slow
	svc.metrics = metrics
	stopped, cancel := context.WithCancel(t.Context())
	cancel()
	agent.runCtx = stopped

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "question"}}

	agent.flushSessionMessagesFinal(t.Context(), state, &taskRunContext{sess: sess})

	if got := slow.calls.Load(); got != 1 {
		t.Fatalf("AppendMessages called %d times, want 1 (no retry during shutdown)", got)
	}
	if len(metrics.paths) != 1 || metrics.paths[0] != "flush" {
		t.Fatalf("session.save.lost paths = %v, want exactly [flush]", metrics.paths)
	}
}

func TestFlushSessionMessagesFinal_StopsRetryingPastTheBudget(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	slow := &slowFailingAppendStore{Store: store, failFor: 3600 * time.Millisecond, slowCall: 2}
	metrics := &saveLostCounter{}
	svc := agent.svc.(*mockServices)
	svc.sessions = slow
	svc.metrics = metrics

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "question"}}

	agent.flushSessionMessagesFinal(t.Context(), state, &taskRunContext{sess: sess})

	if got := slow.calls.Load(); got != 2 {
		t.Fatalf("AppendMessages called %d times, want 2 (no third attempt once the budget is spent)", got)
	}
	if len(metrics.paths) != 1 || metrics.paths[0] != "flush" {
		t.Fatalf("session.save.lost paths = %v, want exactly [flush]", metrics.paths)
	}
}

func TestFlushPausedTaskSessionMessages_ReportsLossOnSaveFailure(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs := &failingAppendStore{Store: store}
	fs.fail.Store(true)
	agent.svc.(*mockServices).sessions = fs

	state := newTestTaskState(agent)
	state.TaskID = "task-paused-loss"
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "lost"}}

	agent.flushPausedTaskSessionMessages(state)

	persisted, _ := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if len(persisted.Messages) != 0 {
		t.Fatalf("expected nothing persisted on failed paused flush, got %d", len(persisted.Messages))
	}
}

func TestFlushSessionMessages_FailedInTaskFlushRetriedNextFlush(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs := &failingAppendStore{Store: store}
	fs.fail.Store(true)
	agent.svc.(*mockServices).sessions = fs

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "m1"}}
	rc := &taskRunContext{sess: sess}

	if err := agent.flushSessionMessages(t.Context(), state, rc); err == nil {
		t.Fatal("expected error from failed in-task flush")
	}
	if state.HistoryOffset != 0 {
		t.Fatalf("HistoryOffset advanced on failure: %d", state.HistoryOffset)
	}
	if len(rc.sess.Messages) != 0 {
		t.Fatalf("rc.sess mutated on failure: %d messages", len(rc.sess.Messages))
	}

	fs.fail.Store(false)
	state.Messages = append(state.Messages, llm.ChatMessage{Role: "assistant", Content: "m2"})
	if err := agent.flushSessionMessages(t.Context(), state, rc); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	saved, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(saved.Messages) != 2 {
		t.Fatalf("persisted %d messages, want 2 (m1 retried + m2)", len(saved.Messages))
	}
}

type panicLLM struct{}

func (panicLLM) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	panic("synthetic llm panic")
}
func (panicLLM) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	panic("synthetic llm stream panic")
}

func TestFlushSessionMessages_PersistsNewMessagesAndIsIdempotent(t *testing.T) {
	agent, _, store := newTestAgent()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u1",
		SessionID: "s1",
		ContextID: "s1",
		Messages:  []session.Message{{Role: "user", Content: "earlier-turn"}},
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "new-user-msg"},
		{Role: "assistant", Content: "new-asst-msg"},
	}
	state.HistoryOffset = 0
	rc := &taskRunContext{sess: sess}

	_ = agent.flushSessionMessages(t.Context(), state, rc)

	if state.HistoryOffset != 2 {
		t.Fatalf("HistoryOffset = %d, want 2", state.HistoryOffset)
	}
	versionAfterFirst := sess.Version
	_ = agent.flushSessionMessages(t.Context(), state, rc)
	if sess.Version != versionAfterFirst {
		t.Errorf("second flush re-saved session (version %d -> %d)", versionAfterFirst, sess.Version)
	}

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if got := len(saved.Messages); got != 3 {
		t.Fatalf("saved messages = %d, want 3 (1 prior + 2 new)", got)
	}
	if saved.Messages[1].Content != "new-user-msg" || saved.Messages[2].Content != "new-asst-msg" {
		t.Errorf("appended messages = %+v", saved.Messages[1:])
	}
}

func TestFlushSessionMessages_SurvivesCancelledContext(t *testing.T) {
	agent, _, store := newTestAgent()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-cancel",
		SessionID: "s-cancel",
		ContextID: "s-cancel",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s-cancel"
	state.UserProps.UserID = "u-cancel"
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "what is foo?"},
		{Role: "assistant", Content: "let me check", ToolCalls: []llm.ToolCall{{ID: "tc1", Name: "lookup", Arguments: "{}"}}},
		{Role: "tool", Content: "foo=bar"},
	}
	rc := &taskRunContext{sess: sess}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_ = agent.flushSessionMessages(ctx, state, rc)

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u-cancel", "s-cancel")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(saved.Messages) != 4 {
		t.Fatalf("cancelled-context flush messages: got %d, want 4 (3 + marker)", len(saved.Messages))
	}
	last := saved.Messages[3]
	if last.Role != "assistant" || last.Content != cancellationMarkerContent {
		t.Errorf("expected cancellation marker as last msg, got %+v", last)
	}
}

func TestCancelPausedTask_PersistsAccumulatedMessages(t *testing.T) {
	agent, _, store := newTestAgent()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-paused",
		SessionID: "s-paused",
		ContextID: "s-paused",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-paused"
	state.EffectiveSessionID = "s-paused"
	state.UserProps.UserID = "u-paused"
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "delegate to peer"},
		{Role: "assistant", Content: "calling peer", ToolCalls: []llm.ToolCall{{ID: "p1", Name: "peer_X", Arguments: "{}"}}},
	}
	state.HistoryOffset = 0
	state.PausedAt = time.Now()
	agent.pausedTasks.Store(state)

	agent.cancelPausedTask(state, cancelFromElsewhere)

	if agent.pausedTasks.Load(state.TaskID) != nil {
		t.Errorf("paused task should be removed after cancel")
	}

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u-paused", "s-paused")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(saved.Messages) != 3 {
		t.Fatalf("paused-cancel persisted messages: got %d, want 3 (2 + marker)", len(saved.Messages))
	}
	if saved.Messages[0].Content != "delegate to peer" || saved.Messages[1].Content != "calling peer" {
		t.Errorf("persisted messages = %+v", saved.Messages)
	}
	if saved.Messages[2].Role != "assistant" || saved.Messages[2].Content != cancellationMarkerContent {
		t.Errorf("expected cancellation marker, got %+v", saved.Messages[2])
	}
}

func TestCancelPausedTask_SynthesizesCancelledResultsAndSetsCancelledFlag(t *testing.T) {
	agent, _, store := newTestAgent()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-paused",
		SessionID: "s-paused",
		ContextID: "s-paused",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-paused-with-pending"
	state.EffectiveSessionID = "s-paused"
	state.UserProps.UserID = "u-paused"
	pausedAssistant := llm.ChatMessage{Role: "assistant", Content: "delegating", ToolCalls: []llm.ToolCall{
		{ID: "call-A", Name: "peer_alpha", Arguments: "{}"},
		{ID: "call-B", Name: "peer_beta", Arguments: "{}"},
	}}
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "do parallel work"},
		pausedAssistant,
	}
	state.PausedAssistantMsg = pausedAssistant
	state.HistoryOffset = 0
	state.PendingCalls = map[string]*PendingToolCall{
		"call-A": {ToolCallID: "call-A", ToolName: "peer_alpha", Kind: PendingCallPeer, Index: 0},
		"call-B": {ToolCallID: "call-B", ToolName: "peer_beta", Kind: PendingCallPeer, Index: 1},
	}
	state.TotalPending = 2
	state.PausedAt = time.Now()
	agent.pausedTasks.Store(state)

	agent.cancelPausedTask(state, cancelFromElsewhere)

	if !state.Cancelled.Load() {
		t.Error("expected state.Cancelled set to true after cancelPausedTask")
	}
	if agent.pausedTasks.Load(state.TaskID) != nil {
		t.Errorf("paused task should be removed after cancel")
	}

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u-paused", "s-paused")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(saved.Messages) != 5 {
		t.Fatalf("expected 5 persisted messages, got %d: %+v", len(saved.Messages), saved.Messages)
	}
	if saved.Messages[2].Role != "tool" || saved.Messages[2].ToolCallID != "call-A" {
		t.Errorf("expected synthetic tool(call-A) at index 2, got %+v", saved.Messages[2])
	}
	if saved.Messages[3].Role != "tool" || saved.Messages[3].ToolCallID != "call-B" {
		t.Errorf("expected synthetic tool(call-B) at index 3, got %+v", saved.Messages[3])
	}
	if saved.Messages[2].Content != cancelledToolResultContent {
		t.Errorf("expected cancelled content at index 2, got %q", saved.Messages[2].Content)
	}
	if saved.Messages[4].Role != "assistant" || saved.Messages[4].Content != cancellationMarkerContent {
		t.Errorf("expected cancellation marker at index 4, got %+v", saved.Messages[4])
	}
}

func TestCancelPausedTask_SynthesizesForMixedSyncAndPendingBatch(t *testing.T) {
	agent, _, store := newTestAgent()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-mix",
		SessionID: "s-mix",
		ContextID: "s-mix",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-mix"
	state.EffectiveSessionID = "s-mix"
	state.UserProps.UserID = "u-mix"
	pausedAssistant := llm.ChatMessage{Role: "assistant", Content: "two-tool batch", ToolCalls: []llm.ToolCall{
		{ID: "call-sync", Name: "fast_tool", Arguments: "{}"},
		{ID: "call-pending", Name: "peer_slow", Arguments: "{}"},
	}}
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "do mix"},
		pausedAssistant,
	}
	state.PausedAssistantMsg = pausedAssistant
	state.HistoryOffset = 0
	state.SyncResults = map[int]*tool.Result{
		0: {Status: tool.StatusSuccess, Message: "all done", Data: map[string]any{"value": 42.0}},
	}
	state.PendingCalls = map[string]*PendingToolCall{
		"call-pending": {ToolCallID: "call-pending", ToolName: "peer_slow", Kind: PendingCallPeer, Index: 1},
	}
	state.TotalPending = 1
	state.PausedAt = time.Now()
	agent.pausedTasks.Store(state)

	agent.cancelPausedTask(state, cancelFromElsewhere)

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u-mix", "s-mix")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(saved.Messages) != 5 {
		t.Fatalf("expected 5 persisted messages, got %d", len(saved.Messages))
	}
	if saved.Messages[2].ToolCallID != "call-sync" {
		t.Errorf("expected tool(call-sync) at index 2, got %+v", saved.Messages[2])
	}
	if !strings.Contains(saved.Messages[2].Content, `"status":"success"`) ||
		!strings.Contains(saved.Messages[2].Content, `"value":42`) {
		t.Errorf("expected sync result payload preserved at index 2, got %q", saved.Messages[2].Content)
	}
	if saved.Messages[3].ToolCallID != "call-pending" {
		t.Errorf("expected tool(call-pending) at index 3, got %+v", saved.Messages[3])
	}
	if saved.Messages[3].Content != cancelledToolResultContent {
		t.Errorf("expected cancelled stub at index 3, got %q", saved.Messages[3].Content)
	}
}

func TestHandleTask_PanicRecoveryStillFlushesSession(t *testing.T) {
	agent, _, store := newTestAgent()
	agent.cfg.InjectCurrentTime = new(bool)
	agent.svc.(*mockServices).llmMap[""] = panicLLM{}

	resp, err := agent.HandleTask(
		t.Context(),
		"task-panic",
		newUserMessage("save me"),
		a2a.UserProperties{ClientID: "u-panic", UserID: "u-panic"},
	)

	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic-derived error, got %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("expected error response, got %+v", resp)
	}

	sess, getErr := store.Get(t.Context(), agent.cfg.Name, "u-panic", "ctx-1")
	if getErr != nil {
		t.Fatalf("load session: %v", getErr)
	}
	if sess == nil {
		t.Fatal("session row missing after panic recovery")
	}
	var foundUser bool
	for _, m := range sess.Messages {
		if m.Role == "user" && m.Content == "save me" {
			foundUser = true
		}
	}
	if !foundUser {
		t.Errorf("user message lost during panic unwind; session.Messages = %+v", sess.Messages)
	}
}

func TestStop_FlushesPausedTaskSessions(t *testing.T) {
	agent, _, store := newTestAgent()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-shutdown",
		SessionID: "s-shutdown",
		ContextID: "s-shutdown",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-shutdown"
	state.EffectiveSessionID = "s-shutdown"
	state.UserProps.UserID = "u-shutdown"
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "long-running thing"},
		{Role: "assistant", Content: "delegating", ToolCalls: []llm.ToolCall{{ID: "p1", Name: "peer", Arguments: "{}"}}},
	}
	state.HistoryOffset = 0
	agent.pausedTasks.Store(state)

	agent.flushPausedTasksOnShutdown(t.Context())

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u-shutdown", "s-shutdown")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if len(saved.Messages) != 3 {
		t.Fatalf("shutdown flush persisted messages: got %d, want 3 (2 + marker)", len(saved.Messages))
	}
	if saved.Messages[0].Content != "long-running thing" || saved.Messages[1].Content != "delegating" {
		t.Errorf("persisted messages = %+v", saved.Messages)
	}
	if saved.Messages[2].Role != "assistant" || saved.Messages[2].Content != cancellationMarkerContent {
		t.Errorf("expected cancellation marker, got %+v", saved.Messages[2])
	}
}

func TestResumeTaskExecution_PanicRecoveryDoesNotCrash(t *testing.T) {
	agent, _, store := newTestAgent()
	agent.svc.(*mockServices).llmMap[""] = panicLLM{}
	agent.runCtx = t.Context()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-resume",
		SessionID: "s-resume",
		ContextID: "s-resume",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-resume-panic"
	state.EffectiveSessionID = "s-resume"
	state.UserProps.UserID = "u-resume"
	state.PausedAssistantMsg = assistantMsgWithOneToolCall("call-r1", "peer", `{}`)
	state.SyncResults[0] = tool.OK("peer answered")
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "ask peer"},
		state.PausedAssistantMsg,
	}
	state.HistoryOffset = 0
	agent.pausedTasks.Store(state)

	agent.resumeTaskExecution(state)

	select {
	case resp := <-state.DoneCh:
		if resp == nil || resp.Error == nil {
			t.Errorf("DoneCh response = %+v, want error response", resp)
		}
	default:
		t.Error("DoneCh was not signaled — in-process waiters would hang")
	}

	if agent.pausedTasks.Load(state.TaskID) != nil {
		t.Error("paused task should be removed after panic recovery")
	}
}

func TestResumeTaskExecution_PreservesWorkflowDispatchExemptionAfterResume(t *testing.T) {
	artifactContent := BlockPrefixSaveArtifact + " filename=\"quarterly_report.md\" mime_type=\"text/markdown\"\n# Report\n" + BlockDelimiterClose
	agent, _, store := newTestAgent(&llm.ChatResponse{
		Content:      artifactContent,
		FinishReason: "stop",
	})
	agent.runCtx = t.Context()
	svc := agent.svc.(*mockServices)
	svc.arts = &artifact.ScopedService{Inner: memoryArtifactStore()}

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-resume",
		SessionID: "s-resume",
		ContextID: "s-resume",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-resume-workflow"
	state.EffectiveSessionID = "s-resume"
	state.OriginalSessionID = "s-resume"
	state.UserProps.UserID = "u-resume"
	state.kind = a2a.TaskKindWorkflowAgent
	state.StructuredCtx = &structured.Context{
		Request: &a2a.StructuredInvocationRequest{WorkflowName: "wf", NodeID: "step_1"},
	}
	state.PausedAssistantMsg = assistantMsgWithOneToolCall("call-r1", "peer", `{}`)
	state.SyncResults[0] = tool.OK("peer answered")
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "write the report"},
		state.PausedAssistantMsg,
	}
	state.HistoryOffset = 0
	agent.pausedTasks.Store(state)

	agent.resumeTaskExecution(state)

	meta := artifact.LoadCompanionMetadata(context.Background(), svc.arts, agent.cfg.Name, "u-resume", "s-resume", "quarterly_report.md", nil)
	if meta == nil {
		t.Fatal("expected metadata for the saved report artifact")
	}
	rawTags, _ := meta["tags"].([]any)
	for _, tag := range rawTags {
		if tag == a2a.ArtifactTagWorking {
			t.Errorf("workflow node's report artifact must not be tagged %q after resume, got tags %v",
				a2a.ArtifactTagWorking, rawTags)
		}
	}
}

func TestFlushSessionMessages_AddsCancellationMarkerOnCancelledContext(t *testing.T) {
	agent, _, store := newTestAgent()

	sess := &session.Session{
		AppName:   agent.cfg.Name,
		UserID:    "u-marker",
		SessionID: "s-marker",
		ContextID: "s-marker",
	}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s-marker"
	state.UserProps.UserID = "u-marker"
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "do 10 ping tests in series"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "p1", Name: "ping1"}}},
		{Role: "tool", ToolCallID: "p1", Content: "ok"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "p2", Name: "ping2"}}},
		{Role: "tool", ToolCallID: "p2", Content: "ok"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "p3", Name: "ping3"}}},
		{Role: "tool", ToolCallID: "p3", Content: "ok"},
	}
	rc := &taskRunContext{sess: sess}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_ = agent.flushSessionMessages(ctx, state, rc)

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u-marker", "s-marker")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if got := len(saved.Messages); got != 8 {
		t.Fatalf("messages persisted = %d, want 8 (7 + marker)", got)
	}
	last := saved.Messages[7]
	if last.Role != "assistant" || last.Content != cancellationMarkerContent {
		t.Errorf("expected cancellation marker as last msg, got %+v", last)
	}

	_ = agent.flushSessionMessages(ctx, state, rc)
	saved2, _ := store.Get(t.Context(), agent.cfg.Name, "u-marker", "s-marker")
	if len(saved2.Messages) != 8 {
		t.Errorf("second cancelled flush re-appended marker: now %d msgs", len(saved2.Messages))
	}
}

func TestAppendCancellationMarker_Idempotent(t *testing.T) {
	state := &TaskExecutionState{
		Messages: []llm.ChatMessage{
			{Role: "user", Content: "q"},
		},
	}
	appendCancellationMarker(state)
	appendCancellationMarker(state)
	appendCancellationMarker(state)
	if len(state.Messages) != 2 {
		t.Fatalf("expected 2 messages (user + 1 marker), got %d: %+v", len(state.Messages), state.Messages)
	}
	if state.Messages[1].Role != "assistant" || state.Messages[1].Content != cancellationMarkerContent {
		t.Errorf("marker shape wrong: %+v", state.Messages[1])
	}

	appendCancellationMarker(nil)

	empty := &TaskExecutionState{}
	appendCancellationMarker(empty)
	if len(empty.Messages) != 1 {
		t.Errorf("expected 1 msg on empty state, got %d", len(empty.Messages))
	}
}

func TestFlushSessionMessages_NoOpWhenNilState(t *testing.T) {
	agent, _, _ := newTestAgent()
	_ = agent.flushSessionMessages(t.Context(), nil, &taskRunContext{})
	_ = agent.flushSessionMessages(t.Context(), &TaskExecutionState{}, nil)
	_ = agent.flushSessionMessages(t.Context(), &TaskExecutionState{}, &taskRunContext{sess: nil})
}

func TestFlushPausedTaskSessionMessages_SkipsRunBased(t *testing.T) {
	agent, _, store := newTestAgent()
	state := newTestTaskState(agent)
	state.TaskID = "task-runbased"
	state.EffectiveSessionID = "s-runbased"
	state.UserProps.UserID = "u-runbased"
	state.IsRunBased = true
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "fork"}}

	agent.flushPausedTaskSessionMessages(state)

	sess, _ := store.Get(t.Context(), agent.cfg.Name, "u-runbased", "s-runbased")
	if sess != nil {
		t.Errorf("run-based session unexpectedly persisted: %+v", sess)
	}
}

func TestFlushPausedTaskSessionMessages_MissingSession(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.TaskID = "task-missing-sess"
	state.EffectiveSessionID = "s-does-not-exist"
	state.UserProps.UserID = "u-missing"
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "orphan"}}

	agent.flushPausedTaskSessionMessages(state)
}

func TestSignalTaskDone_EdgeCases(t *testing.T) {
	signalTaskDone(nil, &a2a.Response{})

	state := &TaskExecutionState{}
	signalTaskDone(state, &a2a.Response{})

	full := &TaskExecutionState{DoneCh: make(chan *a2a.Response, 1)}
	full.DoneCh <- &a2a.Response{}
	signalTaskDone(full, &a2a.Response{})
}

func TestContinueTaskLoop_PerTurnSaveVisibleToSecondLLMCall(t *testing.T) {
	mock := newMockLLM(
		&llm.ChatResponse{
			ToolCalls: []llm.ToolCall{
				{ID: "c1", Name: "stubtool", Arguments: `{}`},
			},
			FinishReason: "tool_calls",
		},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	store := newMemSessionStore()

	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("tool ran"), nil
	})
	agent.cfg.InjectCurrentTime = new(bool)
	agent.svc.(*mockServices).llmMap[""] = mock
	agent.svc.(*mockServices).sessions = store

	var sessAtCall2 *session.Session
	mock.preCallHook = func(callIdx int) {
		if callIdx == 1 {
			s, err := store.Get(context.Background(), agent.cfg.Name, "u-perturn", "ctx-perturn")
			if err != nil {
				t.Errorf("intra-loop session lookup: %v", err)
				return
			}
			sessAtCall2 = s
		}
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-1",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("invoke a tool then finish")},
		ContextID: "ctx-perturn",
	}
	_, err := agent.HandleTask(
		t.Context(),
		"task-perturn",
		msg,
		a2a.UserProperties{UserID: "u-perturn", ClientID: "c-perturn"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	if sessAtCall2 == nil {
		t.Fatal("session was not yet created when second LLM call fired — per-turn save did not run")
	}
	if len(sessAtCall2.Messages) < 3 {
		t.Fatalf("per-turn save: session.Messages = %d, want >= 3 (user, assistant, tool)\nmessages: %+v",
			len(sessAtCall2.Messages), sessAtCall2.Messages)
	}
	var sawUser, sawTool bool
	for _, m := range sessAtCall2.Messages {
		if m.Role == "user" && m.Content == "invoke a tool then finish" {
			sawUser = true
		}
		if m.Role == "tool" {
			sawTool = true
		}
	}
	if !sawUser {
		t.Error("first turn's user message missing from session at call 2")
	}
	if !sawTool {
		t.Error("first turn's tool result missing from session at call 2")
	}
}

func TestHandleStreamCallError_SavesPartialTextOnCancel(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "write a long poem"},
	}
	rc := &taskRunContext{sess: &session.Session{}, logger: agent.logger}
	var allText replyText

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	streamResp := &llm.ChatResponse{
		Content: "Once upon a midnight dreary, while I pondered, weak and weary",
	}

	resp, outcome, retErr := agent.handleStreamCallError(ctx, state, rc, streamResp, ctx.Err(), &allText)

	if outcome != loopReturn || retErr != nil {
		t.Fatalf("expected loopReturn / nil err, got outcome=%v err=%v", outcome, retErr)
	}
	if resp == nil || resp.Result == nil {
		t.Fatalf("expected cancelled response, got %+v", resp)
	}

	if got := len(state.Messages); got != 2 {
		t.Fatalf("state.Messages len = %d, want 2 (user + partial assistant)", got)
	}
	partial := state.Messages[1]
	if partial.Role != "assistant" || !strings.Contains(partial.Content, "midnight dreary") {
		t.Errorf("expected partial poem at index 1, got %+v", partial)
	}
	if !strings.Contains(allText.String(), "midnight dreary") {
		t.Errorf("allText missing partial content: %q", allText.String())
	}
}

func TestHandleStreamCallError_SkipsPartialSaveWhenToolCallsInFlight(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "look up foo"},
	}
	rc := &taskRunContext{sess: &session.Session{}}
	var allText replyText

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	streamResp := &llm.ChatResponse{
		Content: "Looking that up for you...",
		ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "lookup", Arguments: `{"q":"foo"}`},
		},
	}

	_, _, _ = agent.handleStreamCallError(ctx, state, rc, streamResp, ctx.Err(), &allText)

	if len(state.Messages) != 1 {
		t.Errorf("state.Messages must not grow when tool calls were in flight; got %+v", state.Messages)
	}
	if strings.Contains(allText.String(), "Looking that up") {
		t.Errorf("allText must not include partial text when tool calls were in flight: %q", allText.String())
	}
}

func TestHandleStreamCallError_NilStreamRespOnCancel(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "q"}}
	rc := &taskRunContext{sess: &session.Session{}}
	var allText replyText

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	resp, outcome, retErr := agent.handleStreamCallError(ctx, state, rc, nil, ctx.Err(), &allText)

	if outcome != loopReturn || retErr != nil {
		t.Fatalf("expected loopReturn / nil err, got outcome=%v err=%v", outcome, retErr)
	}
	if resp == nil {
		t.Fatal("expected non-nil cancelled response")
	}
	if len(state.Messages) != 1 {
		t.Errorf("state.Messages should not change when streamResp is nil; got %+v", state.Messages)
	}
}

func TestHandleTask_CancelledContextStillSavesUserTurn(t *testing.T) {
	agent, _, store := newTestAgent(&llm.ChatResponse{Content: "ignored"})
	agent.cfg.InjectCurrentTime = new(bool)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _ = agent.HandleTask(
		ctx,
		"task-cancel",
		newUserMessage("what is the meaning of life?"),
		a2a.UserProperties{ClientID: "user-c", UserID: "user-c"},
	)

	sess, err := store.Get(t.Context(), agent.cfg.Name, "user-c", "ctx-1")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if sess == nil {
		t.Fatal("session row missing — setupTask never created it")
	}
	var foundUser bool
	for _, m := range sess.Messages {
		if m.Role == "user" && m.Content == "what is the meaning of life?" {
			foundUser = true
		}
	}
	if !foundUser {
		t.Errorf("user message lost on cancel; session.Messages = %+v", sess.Messages)
	}
}

func TestHandleContextLimitRetry_SingleOversizedTurnActionableMessage(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	state := newTestTaskState(agent)
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "one giant oversized user turn"}}
	state.TotalUsage = llm.Usage{PromptTokens: 190000, CompletionTokens: 120, CachedTokens: 188000}
	rc := &taskRunContext{sess: &session.Session{}, logger: agent.logger}

	resp, outcome, err := agent.handleContextLimitRetry(t.Context(), state, rc)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if outcome != loopReturn {
		t.Fatalf("outcome = %v, want loopReturn", outcome)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected an error response")
	}
	msg := resp.Error.Message
	if !strings.Contains(msg, "too large for this model's context window") {
		t.Errorf("want actionable oversized-input message, got %q", msg)
	}
	if strings.Contains(msg, "start a new conversation") {
		t.Errorf("must not give the misleading 'start a new conversation' advice: %q", msg)
	}
	if strings.Contains(msg, "conversation too short") || strings.Contains(msg, "need at least 2 user turns") {
		t.Errorf("must not leak the raw internal compaction error: %q", msg)
	}
	if len(rc.sess.Messages) != 0 {
		t.Errorf("session not restored to original length after failed compaction, len = %d", len(rc.sess.Messages))
	}

	data, _ := resp.Error.Data.(map[string]any)
	tu, _ := data["token_usage"].(map[string]any)
	if tu == nil {
		t.Fatalf("abort response carries no token_usage: Data = %#v", resp.Error.Data)
	}
	if got, _ := tu["total_input_tokens"].(int); got != 190000 {
		t.Errorf("total_input_tokens = %v, want 190000", tu["total_input_tokens"])
	}
	if got, _ := tu["total_cached_input_tokens"].(int); got != 188000 {
		t.Errorf("total_cached_input_tokens = %v, want 188000", tu["total_cached_input_tokens"])
	}
	if got, ok := data[a2a.ErrorDataLLMErrorCategory]; ok {
		t.Errorf("llm_error_category = %v, want absent on an oversized single turn", got)
	}
}

func TestHandleContextLimitRetry_NoUsageOmitsUsageData(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	state := newTestTaskState(agent)
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "one giant oversized user turn"}}
	rc := &taskRunContext{sess: &session.Session{}, logger: agent.logger}

	resp, _, err := agent.handleContextLimitRetry(t.Context(), state, rc)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatal("expected an error response")
	}
	if resp.Error.Data != nil {
		t.Errorf("no tokens were billed, so Data must stay nil; got %#v", resp.Error.Data)
	}
}
