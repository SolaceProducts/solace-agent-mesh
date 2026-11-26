// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const loginTaskID, loginCallID, loginKey = "task-login", "call-login", "login-key"

func newLoginAgent(t *testing.T, exec stubExecFn) (*Agent, *mockBroker) {
	t.Helper()
	agent := newTestAgentWithStubTool(t, "stubtool", exec)
	svc := agent.svc.(*mockServices)
	svc.featureRegistry = durableResumeRegistry(true)
	brk := newMockBroker()
	svc.brk = brk
	agent.checkpoints = newTaskCheckpointer(svc, agent.cfg.Name, newTestAgentBackstops(), agent.logger)
	if agent.checkpoints == nil {
		t.Fatal("expected durable resume to be enabled")
	}
	return agent, brk
}

func loginPausedState(t *testing.T, agent *Agent) *TaskExecutionState {
	t.Helper()
	state := newTestTaskState(agent)
	state.TaskID = loginTaskID
	state.UserProps.UserID = "u1"
	state.TotalPending = 1
	state.PendingCalls[loginCallID] = &PendingToolCall{
		ToolCallID: loginCallID, ToolName: "stubtool", Kind: PendingCallAuth, Index: 0,
		OriginalArgs: map[string]any{"q": "x"}, CredentialKey: loginKey, Timeout: time.Minute,
	}
	state.PausedAssistantMsg = assistantMsgWithOneToolCall(loginCallID, "stubtool", `{"q":"x"}`)
	if err := agent.svc.Sessions().Create(t.Context(), &session.Session{
		AppName: agent.cfg.Name, UserID: "u1", SessionID: state.EffectiveSessionID, ContextID: state.ContextID,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	state.mu.Lock()
	err := agent.checkpoints.save(t.Context(), state)
	state.mu.Unlock()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	return state
}

func loginRowExists(t *testing.T, agent *Agent) bool {
	t.Helper()
	pc, err := agent.checkpoints.lookup(t.Context(), authCorrelationID(loginTaskID, loginCallID))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	return pc != nil
}

func TestLoginPauseWritesAPendingRowWithItsDeadline(t *testing.T) {
	agent, _ := newLoginAgent(t, nil)
	loginPausedState(t, agent)

	pc, err := agent.checkpoints.lookup(t.Context(), authCorrelationID(loginTaskID, loginCallID))
	if err != nil || pc == nil {
		t.Fatalf("login pause wrote no pending row: %v", err)
	}
	if pc.Kind != session.PendingKindAuth || pc.ExpiresAt == nil {
		t.Fatalf("pending row = %+v, want an auth row with a deadline the reaper can see", pc)
	}
	cp, _ := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	if cp == nil || cp.PendingCount != 1 {
		t.Fatalf("checkpoint pending_count = %+v, want 1 so no process takes the task over before the answer", cp)
	}
}

func TestLoginFollowerGetsNoPendingRow(t *testing.T) {
	if _, _, ok := pendingCallCorrelation("t", &PendingToolCall{Kind: PendingCallAuth, ToolCallID: "c", IsAuthFollower: true}); ok {
		t.Fatal("a call waiting behind another call's login prompt got a row nothing would ever answer")
	}
}

func TestLoginAnswerOnTheHolderIsRecordedBeforeTheExchange(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("ran"), nil
	})
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)
	var rowAtExchange atomic.Bool
	agent.RegisterAuthHandler(&stubAuthHandler{key: loginKey, handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		rowAtExchange.Store(loginRowExists(t, agent))
		return nil
	}})

	if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, authResp(loginTaskID, loginCallID, loginKey), a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	if rowAtExchange.Load() {
		t.Fatal("the code was exchanged before the answer was claimed on the row")
	}
	cp, _ := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	if cp == nil || len(cp.ToolResults) != 1 || !isAuthAnsweredPayload(cp.ToolResults[0]) {
		t.Fatalf("checkpoint results = %+v, want the user-signed-in record a takeover retries from", cp)
	}
}

func TestLoginAnswerOnAnotherProcessIsExchangedOnceAndPassedOn(t *testing.T) {
	agent, brk := newLoginAgent(t, nil)
	loginPausedState(t, agent)
	var exchanges atomic.Int32
	agent.RegisterAuthHandler(&stubAuthHandler{key: loginKey, handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		exchanges.Add(1)
		return nil
	}})

	first := authResp(loginTaskID, loginCallID, loginKey)
	if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, first, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}
	again := authResp(loginTaskID, loginCallID, loginKey)
	again.Code = "a-redelivered-copy"
	if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, again, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	if got := exchanges.Load(); got != 1 {
		t.Fatalf("code exchanges = %d, want 1", got)
	}
	if loginRowExists(t, agent) {
		t.Fatal("the pending login row was not claimed")
	}
	var forwarded []publishedMsg
	for _, p := range brk.getPublished() {
		if p.topic == agent.forwardTopic() {
			forwarded = append(forwarded, p)
		}
	}
	if len(forwarded) != 1 {
		t.Fatalf("passed-on login answers = %d, want 1", len(forwarded))
	}
	if strings.Contains(string(forwarded[0].payload), "test-auth-code") {
		t.Fatal("the passed-on login answer carries the authorization code")
	}
}

func TestLoginAnswerFromSomeoneElseIsRefusedOnAnotherProcess(t *testing.T) {
	agent, _ := newLoginAgent(t, nil)
	loginPausedState(t, agent)
	var exchanges atomic.Int32
	agent.RegisterAuthHandler(&stubAuthHandler{key: loginKey, handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		exchanges.Add(1)
		return nil
	}})

	if _, err := agent.handleAuthResponse(t.Context(), loginTaskID, authResp(loginTaskID, loginCallID, loginKey), a2a.UserProperties{UserID: "mallory"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}
	if exchanges.Load() != 0 || !loginRowExists(t, agent) {
		t.Fatal("another user's login answer was exchanged or claimed the row")
	}
}

func TestAdoptedLoginIsRetriedNotAnsweredWithTheRecord(t *testing.T) {
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("ran with the saved credential"), nil
	})
	state := loginPausedState(t, agent)
	if _, err := agent.claimLoginAnswer(t.Context(), loginTaskID, loginCallID, "stubtool"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	cp, _ := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	agent.applyCheckpointResults(state, cp.ToolResults)
	if _, pending := state.PendingCalls[loginCallID]; !pending {
		t.Fatal("the user-signed-in record was applied as the tool's result")
	}

	if repaused := agent.retryLoginsOnAdoption(t.Context(), state, nil, agent.logger); repaused {
		t.Fatal("the task paused again although the tool ran")
	}
	if r := state.SyncResults[0]; r == nil || r.Message != "ran with the saved credential" {
		t.Fatalf("result = %+v, want the tool's own result", r)
	}
	if o := state.takePendingAuthOutcome(); o == nil || !o.success {
		t.Fatalf("auth outcome = %+v, want success so the login card resolves", o)
	}
}

func TestAdoptedLoginWithoutACredentialAsksAgain(t *testing.T) {
	agent, _ := newLoginAgent(t, stubReturnsPendingAuth(loginCallID))
	state := loginPausedState(t, agent)
	if _, err := agent.claimLoginAnswer(t.Context(), loginTaskID, loginCallID, "stubtool"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	cp, _ := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	agent.applyCheckpointResults(state, cp.ToolResults)
	agent.pausedTasks.Store(state)

	if repaused := agent.retryLoginsOnAdoption(t.Context(), state, nil, agent.logger); !repaused {
		t.Fatal("a tool with no credential did not pause the task for a new sign-in")
	}
	pc := state.PendingCalls[loginCallID]
	if pc == nil || pc.AuthRetries != 1 {
		t.Fatalf("pending call = %+v, want it waiting again with one retry used", pc)
	}
	if pc.TimeoutTimer != nil {
		pc.TimeoutTimer.Stop()
	}
	if !loginRowExists(t, agent) {
		t.Fatal("the new wait has no pending row, so it would not survive a restart")
	}
}

func forwardedMessage(t *testing.T, taskID string, msg a2a.Message, userID string) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(a2a.NewRequest(taskID, "message/send", map[string]any{"message": msg}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &broker.Message{Payload: payload, UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{UserID: userID})}
}

func TestPassedOnAnswerReachesTheWaitingTool(t *testing.T) {
	agent, _ := newLoginAgent(t, nil)
	ch := agent.registerHILRequest("req-1", "u1", "")

	agent.handleForwardedControl(t.Context(), forwardedMessage(t, "task-hil", userInputResponseMessage(t, "task-hil", "req-1"), "u1"))

	select {
	case resp := <-ch:
		if resp.Status != "answered" {
			t.Fatalf("status = %q", resp.Status)
		}
	default:
		t.Fatal("the passed-on answer did not reach the waiting tool")
	}
}

func TestPassedOnAnswerFromSomeoneElseIsRefused(t *testing.T) {
	agent, _ := newLoginAgent(t, nil)
	ch := agent.registerHILRequest("req-2", "u1", "")

	agent.handleForwardedControl(t.Context(), forwardedMessage(t, "task-hil", userInputResponseMessage(t, "task-hil", "req-2"), "mallory"))

	select {
	case <-ch:
		t.Fatal("another user's passed-on answer reached the tool")
	default:
	}
}

func TestUnmatchedAnswerIsPassedOn(t *testing.T) {
	agent, brk := newLoginAgent(t, nil)
	msg := userInputResponseMessage(t, "task-hil", "req-missing")

	_, _, _ = agent.interceptControlMessage(t.Context(), "task-hil", msg, a2a.UserProperties{UserID: "u1"}, agent.logger)

	var forwarded int
	for _, p := range brk.getPublished() {
		if p.topic == agent.forwardTopic() {
			forwarded++
		}
	}
	if forwarded != 1 {
		t.Fatalf("passed-on answers = %d, want 1", forwarded)
	}
}

func TestPassedOnLoginRunsTheHeldCallWithoutExchangingAgain(t *testing.T) {
	var resumed atomic.Int32
	useResumeHook(t, func(*Agent, *TaskExecutionState) { resumed.Add(1) })
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("ran with the credential saved elsewhere"), nil
	})
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)
	var exchanges atomic.Int32
	agent.RegisterAuthHandler(&stubAuthHandler{key: loginKey, handleFn: func(context.Context, *a2a.AuthResponseData, *tool.Context) error {
		exchanges.Add(1)
		return nil
	}})
	resp := authResp(loginTaskID, loginCallID, loginKey)
	resp.Code = ""
	raw, _ := json.Marshal(resp)
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	msg := a2a.Message{Kind: "message", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewDataPart(data)}}

	agent.handleForwardedControl(t.Context(), forwardedMessage(t, loginTaskID, msg, "u1"))

	if exchanges.Load() != 0 {
		t.Fatal("a passed-on login answer was exchanged again")
	}
	state.mu.Lock()
	r := state.SyncResults[0]
	state.mu.Unlock()
	if r == nil || r.Message != "ran with the credential saved elsewhere" {
		t.Fatalf("result = %+v, want the tool run with the saved credential", r)
	}
	if resumed.Load() != 1 {
		t.Fatalf("resumes = %d, want 1", resumed.Load())
	}
}

func TestPassedOnMessagesThatDoNotParseAreDropped(t *testing.T) {
	agent, _ := newLoginAgent(t, nil)
	agent.handleForwardedControl(t.Context(), &broker.Message{Payload: []byte("not json")})
	agent.handleForwardedControl(t.Context(), &broker.Message{Payload: []byte(`{"jsonrpc":"2.0","id":"x","method":"message/send","params":"nope"}`)})
	agent.applyForwardedLogin(t.Context(), authResp("no-such-task", "c", loginKey), a2a.UserProperties{UserID: "u1"})
}
