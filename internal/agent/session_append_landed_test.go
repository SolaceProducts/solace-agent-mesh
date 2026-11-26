// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type committedTimeoutStore struct {
	session.Store
	failOn  int32
	appends atomic.Int32
}

func (s *committedTimeoutStore) AppendMessages(ctx context.Context, appName, userID, sessionID string, msgs []session.Message) error {
	if err := s.Store.AppendMessages(ctx, appName, userID, sessionID, msgs); err != nil {
		return err
	}
	if s.appends.Add(1) == s.failOn {
		return fmt.Errorf("append messages: %w", context.DeadlineExceeded)
	}
	return nil
}

func toolCallIDCount(msgs []session.Message, id string) (calls, results int) {
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID == id {
				calls++
			}
		}
		if m.Role == "tool" && m.ToolCallID == id {
			results++
		}
	}
	return calls, results
}

func TestCommittedTimeoutDoesNotDuplicateTheToolTurn(t *testing.T) {
	mock := newMockLLM(
		&llm.ChatResponse{
			ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "stubtool", Arguments: `{}`}},
			FinishReason: "tool_calls",
		},
		&llm.ChatResponse{Content: "first answer", FinishReason: "stop"},
		&llm.ChatResponse{Content: "second answer", FinishReason: "stop"},
	)
	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("tool ran"), nil
	})
	agent.cfg.InjectCurrentTime = new(bool)
	agent.svc.(*mockServices).llmMap[""] = mock
	store := newSQLiteSessionStore(t)
	agent.svc.(*mockServices).sessions = &committedTimeoutStore{Store: store, failOn: 2}

	const ctxID = "ctx-committed-timeout"
	up := a2a.UserProperties{UserID: "u1", ClientID: "c1"}
	if _, err := agent.HandleTask(t.Context(), "task-1", concUserMessage("use the tool", ctxID), up); err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	sess, err := store.Get(t.Context(), agent.cfg.Name, "u1", ctxID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	wantRoles := []string{"user", "assistant", "tool", "assistant"}
	if len(sess.Messages) != len(wantRoles) {
		t.Fatalf("stored %d messages, want %d (each once): %+v", len(sess.Messages), len(wantRoles), sess.Messages)
	}
	for i, role := range wantRoles {
		if sess.Messages[i].Role != role {
			t.Fatalf("message %d role = %q, want %q: %+v", i, sess.Messages[i].Role, role, sess.Messages)
		}
	}
	if calls, results := toolCallIDCount(sess.Messages, "c1"); calls != 1 || results != 1 {
		t.Fatalf("stored tool call c1 %d times and its result %d times, want once each", calls, results)
	}

	if _, err := agent.HandleTask(t.Context(), "task-2", concUserMessage("and now?", ctxID), up); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if len(mock.calls) != 3 {
		t.Fatalf("LLM calls = %d, want 3", len(mock.calls))
	}
	var calls, results int
	for _, m := range mock.calls[2].Messages {
		for _, tc := range m.ToolCalls {
			if tc.ID == "c1" {
				calls++
			}
		}
		if m.Role == "tool" && m.ToolCallID == "c1" {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("next turn's LLM request carries tool call c1 %d times and its result %d times, want once each", calls, results)
	}
}

func TestPausedFlushSkipsACommittedTimeout(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agent.svc.(*mockServices).sessions = &committedTimeoutStore{Store: store, failOn: 1}

	state := newTestTaskState(agent)
	state.TaskID = "task-paused-committed"
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "delegate"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "p1", Name: "peer", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "p1", Content: "peer result"},
	}
	rc := &taskRunContext{sess: sess}
	if err := agent.flushSessionMessages(t.Context(), state, rc); err == nil {
		t.Fatal("expected the committed save to report its timeout")
	}

	agent.flushPausedTaskSessionMessages(state)

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(saved.Messages) != 4 {
		t.Fatalf("stored %d messages, want 4 (3 once + cancellation marker): %+v", len(saved.Messages), saved.Messages)
	}
	if calls, results := toolCallIDCount(saved.Messages, "p1"); calls != 1 || results != 1 {
		t.Fatalf("stored tool call p1 %d times and its result %d times, want once each", calls, results)
	}
	if saved.Messages[3].Content != cancellationMarkerContent {
		t.Fatalf("last message = %+v, want the cancellation marker", saved.Messages[3])
	}
}

func TestResumedTaskSkipsACommittedTimeout(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agent.svc.(*mockServices).sessions = &committedTimeoutStore{Store: store, failOn: 1}

	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "delegate"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "p1", Name: "peer", Arguments: "{}"}}},
	}
	if err := agent.flushSessionMessages(t.Context(), state, &taskRunContext{sess: sess}); err == nil {
		t.Fatal("expected the committed save to report its timeout")
	}

	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resumed, err := unmarshalTaskState(blob)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	resumed.Messages = append(resumed.Messages, llm.ChatMessage{Role: "tool", ToolCallID: "p1", Content: "peer result"})
	fresh, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	rc := &taskRunContext{sess: fresh}
	if err = agent.flushSessionMessages(t.Context(), resumed, rc); err != nil {
		t.Fatalf("resume flush: %v", err)
	}

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(saved.Messages) != 3 {
		t.Fatalf("stored %d messages, want 3 (each once): %+v", len(saved.Messages), saved.Messages)
	}
	if calls, results := toolCallIDCount(saved.Messages, "p1"); calls != 1 || results != 1 {
		t.Fatalf("stored tool call p1 %d times and its result %d times, want once each", calls, results)
	}
	if len(rc.sess.Messages) != 3 {
		t.Fatalf("in-memory session holds %d messages, want 3", len(rc.sess.Messages))
	}
}

func TestCommittedTimeoutWithSchedulerHistoryDoesNotDuplicate(t *testing.T) {
	cases := []struct {
		name   string
		failOn int32
	}{
		{name: "user message append", failOn: 1},
		{name: "tool turn append", failOn: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockLLM(
				&llm.ChatResponse{
					ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "stubtool", Arguments: `{}`}},
					FinishReason: "tool_calls",
				},
				&llm.ChatResponse{Content: "first answer", FinishReason: "stop"},
			)
			agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
				return tool.OK("tool ran"), nil
			})
			agent.cfg.InjectCurrentTime = new(bool)
			agent.svc.(*mockServices).llmMap[""] = mock
			store := newSQLiteSessionStore(t)
			agent.svc.(*mockServices).sessions = &committedTimeoutStore{Store: store, failOn: tc.failOn}

			const ctxID = "ctx-committed-timeout-scheduler"
			msg := concUserMessage("use the tool", ctxID)
			msg.Metadata = schedulerHistoryMeta(
				[2]string{"user", "prior run"},
				[2]string{"assistant", "prior answer"},
			)
			up := a2a.UserProperties{UserID: "u1", ClientID: "c1"}
			if _, err := agent.HandleTask(t.Context(), "task-1", msg, up); err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			if len(mock.calls) == 0 || !llmRequestHasContent(mock.calls[0].Messages, "prior run") {
				t.Fatal("the first LLM request did not carry the injected scheduler history")
			}

			sess, err := store.Get(t.Context(), agent.cfg.Name, "u1", ctxID)
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			wantRoles := []string{"user", "assistant", "tool", "assistant"}
			if len(sess.Messages) != len(wantRoles) {
				t.Fatalf("stored %d messages, want %d (each once, no injected history): %+v", len(sess.Messages), len(wantRoles), sess.Messages)
			}
			for i, role := range wantRoles {
				if sess.Messages[i].Role != role {
					t.Fatalf("message %d role = %q, want %q: %+v", i, sess.Messages[i].Role, role, sess.Messages)
				}
			}
			if sess.Messages[0].Content != "use the tool" {
				t.Fatalf("first stored message = %+v, want the task's own user message", sess.Messages[0])
			}
			if calls, results := toolCallIDCount(sess.Messages, "c1"); calls != 1 || results != 1 {
				t.Fatalf("stored tool call c1 %d times and its result %d times, want once each", calls, results)
			}
		})
	}
}

func llmRequestHasContent(msgs []llm.ChatMessage, content string) bool {
	for _, m := range msgs {
		if m.Content == content {
			return true
		}
	}
	return false
}

func TestContextLimitRetryReloadDoesNotMirrorALandedAppendTwice(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "condensed history"})
	agent.cfg.InjectCurrentTime = new(bool)
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	store := newSQLiteSessionStore(t)
	history := []session.Message{
		{Role: "user", Content: "first question with enough words to weigh something"},
		{Role: "assistant", Content: "first answer with enough words to weigh something"},
		{Role: "user", Content: "second question with enough words to weigh something"},
		{Role: "assistant", Content: "second answer with enough words to weigh something"},
	}
	seed := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1", Messages: history}
	if err := store.Create(t.Context(), seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agent.svc.(*mockServices).sessions = &scriptedStore{
		Store:   &committedTimeoutStore{Store: store, failOn: 1},
		saveSeq: []error{session.ErrOptimisticLock},
	}

	sess, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	const userText = "third question that overflows the context window"
	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s1"
	state.UserProps.UserID = "u1"
	state.Messages, state.HistoryOffset = agent.buildMessages(t.Context(), state.Identity, sess, userText, gatewayContext{})
	rc := &taskRunContext{sess: sess, logger: agent.logger, sessMetaFP: metadataFingerprint(sess.Metadata)}

	if err = agent.flushSessionMessages(t.Context(), state, rc); err == nil {
		t.Fatal("expected the committed append to report its timeout")
	}
	if state.unconfirmedAppend == nil {
		t.Fatal("the failed append was not recorded as unconfirmed")
	}
	if resp, outcome, retryErr := agent.handleContextLimitRetry(t.Context(), state, rc); retryErr != nil || resp != nil || outcome != loopContinue {
		t.Fatalf("context-limit retry = (%+v, %v, %v), want a successful compaction", resp, outcome, retryErr)
	}
	if err = agent.flushSessionMessages(t.Context(), state, rc); err != nil {
		t.Fatalf("flush after the reload: %v", err)
	}

	stored, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var userCount int
	for _, m := range stored.Messages {
		if m.Role == "user" && m.Content == userText {
			userCount++
		}
	}
	if userCount != 1 {
		t.Fatalf("stored the user message %d times, want once: %+v", userCount, stored.Messages)
	}
	if len(rc.sess.Messages) != len(stored.Messages) {
		t.Fatalf("in-memory session holds %d messages, store holds %d", len(rc.sess.Messages), len(stored.Messages))
	}
}

func compactingAgentOverFailingStore(t *testing.T) (*Agent, *failingAppendStore, session.Store) {
	t.Helper()
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "condensed history"})
	agent.cfg.InjectCurrentTime = new(bool)
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	store := newSQLiteSessionStore(t)
	seed := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1", Messages: []session.Message{
		{Role: "user", Content: "first question with enough words to weigh something"},
		{Role: "assistant", Content: "first answer with enough words to weigh something"},
		{Role: "user", Content: "second question with enough words to weigh something"},
		{Role: "assistant", Content: "second answer with enough words to weigh something"},
	}}
	if err := store.Create(t.Context(), seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	failing := &failingAppendStore{Store: store}
	agent.svc.(*mockServices).sessions = failing
	return agent, failing, store
}

func countStored(t *testing.T, store session.Store, agent *Agent, match func(session.Message) bool) int {
	t.Helper()
	stored, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	n := 0
	for _, m := range stored.Messages {
		if match(m) {
			n++
		}
	}
	return n
}

func TestContextLimitRetryKeepsTurnsThatAFailedFlushDidNotStore(t *testing.T) {
	for _, questionStored := range []bool{false, true} {
		t.Run(fmt.Sprintf("question stored %v", questionStored), func(t *testing.T) {
			agent, failing, store := compactingAgentOverFailingStore(t)
			sess, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			const userText = "third question that overflows the context window"
			state := newTestTaskState(agent)
			state.EffectiveSessionID = "s1"
			state.UserProps.UserID = "u1"
			state.Messages, state.HistoryOffset = agent.buildMessages(t.Context(), state.Identity, sess, userText, gatewayContext{})
			state.Question = state.Messages[state.HistoryOffset]
			rc := &taskRunContext{sess: sess, logger: agent.logger, sessMetaFP: metadataFingerprint(sess.Metadata)}
			if questionStored {
				if err := agent.flushSessionMessages(t.Context(), state, rc); err != nil {
					t.Fatalf("flush the question: %v", err)
				}
			}
			state.Messages = append(state.Messages,
				llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-kept", Name: "lookup", Arguments: "{}"}}},
				llm.ChatMessage{Role: "tool", ToolCallID: "tc-kept", ToolName: "lookup", Content: "kept result"})
			failing.fail.Store(true)

			if resp, outcome, retryErr := agent.handleContextLimitRetry(t.Context(), state, rc); retryErr != nil || resp != nil || outcome != loopContinue {
				t.Fatalf("context-limit retry = (%+v, %v, %v), want a successful compaction", resp, outcome, retryErr)
			}
			failing.fail.Store(false)
			if err := agent.flushSessionMessages(t.Context(), state, rc); err != nil {
				t.Fatalf("flush after the retry: %v", err)
			}

			if n := countStored(t, store, agent, func(m session.Message) bool { return m.Role == "tool" && m.Content == "kept result" }); n != 1 {
				t.Fatalf("tool result stored %d times, want once: the failed flush's turns were dropped", n)
			}
			if n := countStored(t, store, agent, func(m session.Message) bool { return m.Role == "user" && strings.Contains(m.Content, userText) }); n != 1 {
				t.Fatalf("question stored %d times, want once", n)
			}
			if n := countInPrompt(llm.ChatRequest{Messages: state.Messages}, func(m llm.ChatMessage) bool { return m.Role == "user" && strings.Contains(m.Content, userText) }); n != 1 {
				t.Fatalf("question in the prompt %d times, want once", n)
			}
		})
	}
}
