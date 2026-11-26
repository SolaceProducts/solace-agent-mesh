// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func lookupTurns(n int) []session.Message {
	var rows []session.Message
	for i := range n {
		id := fmt.Sprintf("s%d", i)
		rows = append(rows,
			session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: id, Name: "lookup"}}},
			session.Message{Role: "tool", Content: "found", ToolCallResult: session.ToolCallResult{ToolCallID: id, ToolName: "lookup"}})
	}
	return rows
}

func pausedPeerTurnRow() session.Message {
	return session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "peer_agent"}}}
}

func TestTakeoverOfATurnPausedAtTheSecondToLastCallStillCallsTheModel(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-late-pause", "sub-late-pause")
	state.LoopIteration = state.MaxCalls - 2
	rows := append([]session.Message{{Role: "user", Content: "delegate this"}}, lookupTurns(state.MaxCalls-2)...)
	storeOwnRows(t, store, state, append(rows, pausedPeerTurnRow())...)

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	if n := len(survivorLLM.getCalls()); n != 1 {
		t.Fatalf("model calls after the takeover = %d, want 1, as an in-process resume makes", n)
	}
}

func TestTakeoverOfATurnOnlyInTheCheckpointKeepsItsPosition(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-late-unstored", "sub-late-unstored")
	state.LoopIteration = state.MaxCalls - 2
	storeOwnRows(t, store, state, append([]session.Message{{Role: "user", Content: "delegate this"}}, lookupTurns(state.MaxCalls-2)...)...)

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	if n := len(survivorLLM.getCalls()); n != 1 {
		t.Fatalf("model calls after the takeover = %d, want 1", n)
	}
}

func TestTakeoverAfterTheResumedTaskStoredTurnsGoesOnFromTheRows(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-resumed-then-lost", "sub-resumed-then-lost")
	state.LoopIteration = 1
	rows := append([]session.Message{{Role: "user", Content: "delegate this"}}, lookupTurns(1)...)
	rows = append(rows, pausedPeerTurnRow(),
		session.Message{Role: "tool", Content: "peer finished the work", ToolCallResult: session.ToolCallResult{ToolCallID: "tc-1", ToolName: "peer_agent"}})
	rows = append(rows,
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "after-1", Name: "lookup"}}},
		session.Message{Role: "tool", Content: "found", ToolCallResult: session.ToolCallResult{ToolCallID: "after-1", ToolName: "lookup"}},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "after-2", Name: "lookup"}}},
		session.Message{Role: "tool", Content: "found", ToolCallResult: session.ToolCallResult{ToolCallID: "after-2", ToolName: "lookup"}})
	storeOwnRows(t, store, state, rows...)

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	if n := len(survivorLLM.getCalls()); n != 1 {
		t.Fatalf("model calls after the takeover = %d, want 1: four calls are stored and the limit is five", n)
	}
}

func TestPendingRunFactsGoOnThePausedTurnTheRowsLacked(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-facts-unstored", "sub-facts-unstored")
	state.runFacts.take()
	state.runFacts.addUsage("m", usageSourceAgent, llm.Usage{PromptTokens: 41})
	storeOwnRows(t, store, state, session.Message{Role: "user", Content: "delegate this"})

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	if n := storedPromptTokenEntries(t, store, state, 41); n != 1 {
		t.Fatalf("stored rows carry the paused turn's usage %d times, want once", n)
	}
}

func TestPendingRunFactsAlreadyOnTheRowsAreNotStoredAgain(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-facts-stored", "sub-facts-stored")
	state.runFacts.take()
	state.runFacts.addUsage("m", usageSourceAgent, llm.Usage{PromptTokens: 41})
	turn := pausedPeerTurnRow()
	turn.RunFacts = state.runFacts.snapshot()
	storeOwnRows(t, store, state, session.Message{Role: "user", Content: "delegate this"}, turn)

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	if n := storedPromptTokenEntries(t, store, state, 41); n != 1 {
		t.Fatalf("stored rows carry the paused turn's usage %d times, want once", n)
	}
}

func storedPromptTokenEntries(t *testing.T, store session.Store, state *TaskExecutionState, promptTokens int) int {
	t.Helper()
	sess, err := store.Get(t.Context(), "test-agent", state.UserProps.UserID, state.EffectiveSessionID)
	if err != nil || sess == nil {
		t.Fatalf("load session: %v", err)
	}
	n := 0
	for _, m := range ownRows(sess.Messages, state.TaskID) {
		if m.RunFacts == nil {
			continue
		}
		for _, u := range m.RunFacts.Usage {
			var tokens llm.Usage
			if json.Unmarshal(u.Tokens, &tokens) == nil && tokens.PromptTokens == promptTokens {
				n++
			}
		}
	}
	return n
}

func TestTakeoverFromACheckpointWithoutTheQuestionUsesTheStoredOne(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-no-question", "sub-no-question")
	state.Question = llm.ChatMessage{}
	storeOwnRows(t, store, state, session.Message{Role: "user", Content: "delegate this"}, pausedPeerTurnRow())

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	req := survivorLLM.getCalls()[0]
	if n := countInPrompt(req, func(m llm.ChatMessage) bool { return m.Role == "user" && m.Content == "delegate this" }); n != 1 {
		t.Fatalf("question in the prompt %d times, want 1", n)
	}
	if n := countInPrompt(req, func(m llm.ChatMessage) bool { return m.Role == "user" && strings.TrimSpace(m.Content) == "" }); n != 0 {
		t.Fatal("the prompt carries an empty user turn in place of the question")
	}
}

func TestTakeoverEndsQuietlyListingAFileAPeerSavedBeforeThePause(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	survivorLLM.responses = []*llm.ChatResponse{{
		ToolCalls:    []llm.ToolCall{quietCall("call-quiet", `{"summary": "Nothing needs attention."}`)},
		FinishReason: "tool_calls",
	}}
	state := pausedOnPeerCall(t, store, "task-quiet-takeover", "sub-quiet-takeover")
	state.QuietEndAllowed = true
	peerResult := survivor.buildToolResultMessage(t.Context(), deckPeerResult(t), nil, nil, llm.ToolCall{ID: "call-earlier", Name: peerToolPrefix + "analyst"})
	storeOwnRows(t, store, state,
		session.Message{Role: "user", Content: "delegate this"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "call-earlier", Name: peerToolPrefix + "analyst"}}},
		session.Message{Role: "tool", Content: peerResult.Content, ToolCallResult: session.ToolCallResult{ToolCallID: "call-earlier", ToolName: peerResult.ToolName}},
		pausedPeerTurnRow(),
	)

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")

	var final string
	if !waitUntil(func() bool {
		final = publishedContaining(survivorBrk, resumedReplyTopic, "runOutcome")
		return final != ""
	}) {
		t.Fatal("the adopted task published no quiet final response")
	}
	var resp struct {
		Result struct {
			Metadata map[string]any `json:"metadata"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(final), &resp); err != nil {
		t.Fatalf("decode the final response: %v", err)
	}
	want := []any{map[string]any{"filename": "deck.pptx"}}
	if diff := cmp.Diff(want, resp.Result.Metadata["produced_artifacts"]); diff != "" {
		t.Fatalf("produced_artifacts differ (-want +got):\n%s", diff)
	}
}

func TestLoginRetryOnTakeoverKeepsWhatTheToolSaved(t *testing.T) {
	agent, brk := newLoginAgent(t, func(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
		toolCtx.NotifyArtifactSaved(ctx, tool.ArtifactSavedInfo{Filename: "after-login.csv", Version: 1})
		return tool.OK("ran"), nil
	})
	state := loginPausedState(t, agent)
	state.ReplyTo, state.UserProps.ReplyTo = resumedReplyTopic, resumedReplyTopic
	state.mu.Lock()
	err := agent.checkpoints.save(t.Context(), state)
	state.mu.Unlock()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	storeOwnRows(t, agent.svc.Sessions(), state,
		session.Message{Role: "user", Content: "list the items"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: loginCallID, Name: "stubtool"}},
			RunFacts: &session.RunFacts{Files: []session.ProducedFile{{Filename: "before-login.csv", Version: 1, Source: producedBySourceTool, Status: "success"}}}},
	)
	if _, err = agent.claimLoginAnswer(t.Context(), loginTaskID, loginCallID, "stubtool"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	cp, _ := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	adopted, err := unmarshalTaskState(cp.State)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	agent.runCtx = t.Context()
	agent.startAdoptedTask(t.Context(), adopted, cp.ToolResults, agent.logger)

	var final string
	if !waitUntil(func() bool {
		final = publishedContaining(brk, resumedReplyTopic, "produced_artifacts")
		return final != ""
	}) {
		t.Fatal("the adopted task published no final response")
	}
	for _, file := range []string{"before-login.csv", "after-login.csv"} {
		if !strings.Contains(final, file) {
			t.Errorf("final response lists no %s: %s", file, final)
		}
	}
}

func TestTakeoverGivesTheAdoptedTaskTheCallersToken(t *testing.T) {
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("ran"), nil
	})
	svc := agent.svc.(*mockServices)
	svc.credSvc = identityKeySealer(t, "primary-key-1234567890ab").(auth.CredentialService)
	agent.checkpoints = newTaskCheckpointer(svc, agent.cfg.Name, newTestAgentBackstops(), agent.logger)
	state := loginPausedState(t, agent)
	state.UserProps.AuthToken = "callers-token"
	state.mu.Lock()
	err := agent.checkpoints.save(t.Context(), state)
	state.mu.Unlock()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	cp, _ := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	if strings.Contains(string(cp.State), "callers-token") {
		t.Fatal("the raw token reached the stored checkpoint")
	}
	adopted, err := unmarshalTaskState(cp.State)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	agent.runCtx = t.Context()
	agent.startAdoptedTask(t.Context(), adopted, cp.ToolResults, agent.logger)

	if adopted.UserProps.AuthToken != "callers-token" {
		t.Errorf("adopted task token = %q, want the caller's", adopted.UserProps.AuthToken)
	}
}

func publishedContaining(brk *mockBroker, topic, part string) string {
	for _, p := range brk.getPublished() {
		if p.topic == topic && strings.Contains(string(p.payload), part) {
			return string(p.payload)
		}
	}
	return ""
}

type savedCredentialHandler struct {
	stubAuthHandler
	connected bool
}

func (h *savedCredentialHandler) CheckStoredCredential(context.Context, *tool.Context) tool.StoredCredential {
	return tool.StoredCredential{Connected: h.connected}
}

func TestHolderRunsTheCallWithACredentialAnotherAWEExchanged(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	var runs atomic.Int32
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		runs.Add(1)
		return tool.OK("ran"), nil
	})
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)
	if _, err := agent.claimLoginAnswer(t.Context(), loginTaskID, loginCallID, "stubtool"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	var exchanges atomic.Int32
	agent.RegisterAuthHandler(&savedCredentialHandler{connected: true, stubAuthHandler: stubAuthHandler{key: loginKey, handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		exchanges.Add(1)
		return errors.New("invalid_grant")
	}}})

	if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, authResp(loginTaskID, loginCallID, loginKey), a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	if exchanges.Load() != 0 {
		t.Fatal("the holder exchanged a code another AWE had already exchanged")
	}
	if runs.Load() != 1 {
		t.Fatalf("tool runs = %d, want 1 with the saved credential", runs.Load())
	}
}

func TestHolderExchangesAnAnswerWhoseRowAnEarlierPromptClaimed(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("ran"), nil
	})
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)
	if _, err := agent.claimLoginAnswer(t.Context(), loginTaskID, loginCallID, "stubtool"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	var exchanges atomic.Int32
	agent.RegisterAuthHandler(&savedCredentialHandler{connected: false, stubAuthHandler: stubAuthHandler{key: loginKey, handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		exchanges.Add(1)
		return nil
	}}})

	if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, authResp(loginTaskID, loginCallID, loginKey), a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	if exchanges.Load() != 1 {
		t.Fatalf("code exchanges = %d, want 1: no credential is saved, so the claim is a stale one", exchanges.Load())
	}
}

func TestCancelAfterTheAnswerLeavesTheAnswerAsTheStoredFinalRow(t *testing.T) {
	agent, _, store := newTestAgent()
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u-answered", SessionID: "s-answered", ContextID: "s-answered"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	state := newTestTaskState(agent)
	state.EffectiveSessionID = "s-answered"
	state.UserProps.UserID = "u-answered"
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "what is foo?"}, {Role: "assistant", Content: "foo is bar"}}
	state.answered.Store(true)
	state.runFacts.markFinal("foo is bar", "foo is bar")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_ = agent.flushSessionMessages(ctx, state, &taskRunContext{sess: sess})

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u-answered", "s-answered")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	answer := storedFinalAnswer(ownRows(saved.Messages, state.TaskID))
	if answer == nil || *answer != "foo is bar" {
		t.Fatalf("stored final answer = %v, want the answer; rows: %+v", answer, saved.Messages)
	}
}

type unreadableCheckpointStore struct {
	session.Store
}

func (unreadableCheckpointStore) GetCheckpoint(context.Context, string) (*session.TaskCheckpoint, error) {
	return nil, errors.New("connection reset")
}

func TestRedeliveredTaskWhoseCheckpointCannotBeReadFailsInsteadOfRunning(t *testing.T) {
	agent, mock, store := newContinuingAgent(t, &llm.ChatResponse{Content: "x", FinishReason: "stop"})
	agent.checkpoints.store = unreadableCheckpointStore{Store: agent.checkpoints.store}
	seedTaskRows(t, agent, store,
		session.Message{Role: "user", Content: "again"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "s0", Name: "lookup"}}},
		session.Message{Role: "tool", Content: "found", ToolCallResult: session.ToolCallResult{ToolCallID: "s0", ToolName: "lookup"}},
	)
	payload, _ := json.Marshal(a2a.NewRequest(continuedTaskID, "message/send", map[string]any{"message": newUserMessage("again")}))
	var req a2a.Request
	_ = json.Unmarshal(payload, &req)

	agent.processMessage(t.Context(), &broker.Message{Payload: payload, Redelivered: true}, &req)

	if len(mock.calls) != 0 {
		t.Fatal("a redelivered task ran although its checkpoint, which may own it, could not be read")
	}
}

func passedOnLogin() a2a.Message {
	resp := authResp(loginTaskID, loginCallID, loginKey)
	resp.Code = ""
	raw, _ := json.Marshal(resp)
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	return a2a.Message{Kind: "message", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewDataPart(data)}}
}

func TestPassedOnMessagesThatDoNotAuthenticateAreRefused(t *testing.T) {
	var runs atomic.Int32
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		runs.Add(1)
		return tool.OK("ran"), nil
	})
	agent.svc.(*mockServices).authn = &stubAuthenticator{rejectErr: errors.New("bad token")}
	ch := agent.registerHILRequest("req-forged", "u1", "")
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)

	for _, token := range []string{"", "forged"} {
		answer := forwardedMessage(t, "task-hil", userInputResponseMessage(t, "task-hil", "req-forged"), "u1")
		login := forwardedMessage(t, loginTaskID, passedOnLogin(), "u1")
		for _, m := range []*broker.Message{answer, login} {
			props := a2a.DecodeUserProperties(m.UserProperties)
			props.AuthToken = token
			m.UserProperties = a2a.EncodeUserProperties(&props)
			agent.handleForwardedControl(t.Context(), m)
		}
	}

	select {
	case <-ch:
		t.Fatal("a passed-on answer that did not authenticate reached the waiting tool")
	case <-time.After(200 * time.Millisecond):
	}
	if runs.Load() != 0 {
		t.Fatal("a passed-on login that did not authenticate ran the held call")
	}
}

func TestTakeoverThatCannotReadTheStoredTurnsLeavesTheCheckpoint(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, _ := replicaOf(t, &scriptedStore{Store: store, getErr: errors.New("connection reset")}, "survivor")
	state := pausedOnPeerCall(t, store, "task-unreadable-rows", "sub-unreadable-rows")
	storeOwnRows(t, store, state, session.Message{Role: "user", Content: "delegate this"}, pausedPeerTurnRow())

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")

	if cp, _ := store.GetCheckpoint(t.Context(), state.TaskID); cp == nil {
		t.Fatal("the checkpoint was lost when the takeover could not read the stored turns")
	}
	if survivor.pausedTasks.Load(state.TaskID) != nil || len(survivorLLM.getCalls()) != 0 {
		t.Fatal("the task ran without its stored turns")
	}
}

func TestUnsavedTurnsLeaveOutABatchThatLanded(t *testing.T) {
	q := llm.ChatMessage{Role: "user", Content: "question"}
	call := llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "lookup"}}}
	result := llm.ChatMessage{Role: "tool", ToolCallID: "c1", ToolName: "lookup", Content: "found"}
	next := llm.ChatMessage{Role: "assistant", Content: "thinking"}
	state := &TaskExecutionState{TaskID: "t1", Messages: []llm.ChatMessage{q, call, result, next}, HistoryOffset: 1}
	landed := llmMessagesToSessionMessages([]llm.ChatMessage{call, result}, "t1")
	state.unconfirmedAppend = &unconfirmedSessionAppend{storedBefore: 1, msgs: landed}
	sess := &session.Session{Messages: append(llmMessagesToSessionMessages([]llm.ChatMessage{q}, "t1"), landed...)}

	tail := unsavedTurns(state, sess)

	if len(tail) != 1 || tail[0].Content != "thinking" {
		t.Fatalf("unsaved turns = %+v, want only the turn after the batch that landed", tail)
	}
	if state.unconfirmedAppend != nil {
		t.Fatal("the landed batch is still recorded as unconfirmed, so its facts would be given back")
	}
}

type promptStateHandler struct {
	stubAuthHandler
	current string
}

func (h *promptStateHandler) AnswersPendingPrompt(_ context.Context, resp *a2a.AuthResponseData, _ *tool.Context) (matches, known bool) {
	return resp.State == h.current, true
}

func answerWithState(state string) *a2a.AuthResponseData {
	resp := authResp(loginTaskID, loginCallID, loginKey)
	resp.State = state
	resp.Code = "code-" + state
	return resp
}

func TestAnswerToAnEarlierPromptDoesNotTakeTheLoginRow(t *testing.T) {
	agent, _ := newLoginAgent(t, nil)
	loginPausedState(t, agent)
	var exchanged []string
	agent.RegisterAuthHandler(&promptStateHandler{current: "S2", stubAuthHandler: stubAuthHandler{key: loginKey, handleFn: func(_ context.Context, resp *a2a.AuthResponseData, _ *tool.Context) error {
		exchanged = append(exchanged, resp.State)
		return nil
	}}})

	for _, state := range []string{"S1", "S2"} {
		if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, answerWithState(state), a2a.UserProperties{UserID: "u1"}); err != nil {
			t.Fatalf("handleAuthResponse %s: %v", state, err)
		}
	}

	if len(exchanged) != 1 || exchanged[0] != "S2" {
		t.Fatalf("exchanged answers = %v, want only the current prompt's", exchanged)
	}
	if loginRowExists(t, agent) {
		t.Fatal("the current prompt's answer did not claim the login row")
	}
}

func TestHolderIgnoresAnAnswerToAnEarlierPrompt(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	var runs atomic.Int32
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		runs.Add(1)
		return tool.OK("ran"), nil
	})
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)
	var exchanges atomic.Int32
	agent.RegisterAuthHandler(&promptStateHandler{current: "S2", stubAuthHandler: stubAuthHandler{key: loginKey, handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		exchanges.Add(1)
		return nil
	}}})

	if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, answerWithState("S1"), a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	if exchanges.Load() != 0 || runs.Load() != 0 {
		t.Fatalf("exchanges=%d runs=%d, want neither for an earlier prompt's answer", exchanges.Load(), runs.Load())
	}
	state.mu.Lock()
	_, waiting := state.PendingCalls[loginCallID]
	state.mu.Unlock()
	if !waiting || !loginRowExists(t, agent) {
		t.Fatal("the call stopped waiting for the current prompt's answer")
	}
}

func TestHolderDoesNotExchangeThisPromptsAnswerAnotherAWEClaimed(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	var runs atomic.Int32
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		runs.Add(1)
		return tool.OK("ran"), nil
	})
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)
	if _, err := agent.claimLoginAnswer(t.Context(), loginTaskID, loginCallID, "stubtool"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	var exchanges atomic.Int32
	agent.RegisterAuthHandler(&promptStateHandler{current: "S2", stubAuthHandler: stubAuthHandler{key: loginKey, handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		exchanges.Add(1)
		return errors.New("invalid_grant")
	}}})

	if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, answerWithState("S2"), a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	if exchanges.Load() != 0 {
		t.Fatal("the holder exchanged a code the AWE that claimed it had already sent to the identity provider")
	}
	if runs.Load() != 1 {
		t.Fatalf("tool runs = %d, want 1: it uses a saved credential or asks again", runs.Load())
	}
}
