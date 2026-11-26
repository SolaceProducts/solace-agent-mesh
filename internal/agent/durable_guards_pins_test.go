// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func runningTurnEntry(checkpointed *atomic.Bool) *activeTaskEntry {
	return &activeTaskEntry{
		cancel:       func() {},
		replyTo:      resumedReplyTopic,
		publishOnce:  &sync.Once{},
		checkpointed: checkpointed,
	}
}

func TestCancelOfATurnWhoseTaskIsNotCheckpointedIsAnswered(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	state := pausedOnQuestion(t, store, owner, "task-ask")

	if !owner.answersCancelOfRunningTurn(state.TaskID, runningTurnEntry(nil)) {
		t.Error("answersCancelOfRunningTurn() = false, want true for a turn with no checkpoint")
	}
	if cp, err := store.GetCheckpoint(t.Context(), state.TaskID); err != nil || cp == nil {
		t.Errorf("checkpoint after the cancel = %v (err %v), want it untouched", cp, err)
	}
}

func TestCancelOfAResumedTurnWhoseTaskAnotherReplicaTookIsNotAnsweredAgain(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	state := pausedOnQuestion(t, store, owner, "task-ask")
	owner.checkpoints.recordResult(t.Context(), state.TaskID, state.PendingCalls["tc-1"], tool.OK("EMEA"))
	if cp, err := takeOver(t, store, state.TaskID, "successor"); err != nil || cp == nil {
		t.Fatalf("setup: the successor did not take the task over: %v, %v", cp, err)
	}
	checkpointed := &atomic.Bool{}
	checkpointed.Store(true)

	if owner.answersCancelOfRunningTurn(state.TaskID, runningTurnEntry(checkpointed)) {
		t.Error("answersCancelOfRunningTurn() = true, want false: the successor owns the task")
	}
	if cp, err := store.GetCheckpoint(t.Context(), state.TaskID); err != nil || cp == nil {
		t.Errorf("checkpoint after the cancel = %v (err %v), want the successor's row kept", cp, err)
	}
}

func TestResumedTurnIsRegisteredAsCheckpointed(t *testing.T) {
	store := session.NewMemoryStore()
	holder, mock, _ := replicaOf(t, store, "holder")
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	err := holder.Tools().Register(&testBuiltinTool{name: "block_tool", description: "stub", execFn: func(ctx context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return tool.OK("done"), nil
	}})
	if err != nil {
		t.Fatalf("register block_tool: %v", err)
	}
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-block", "block_tool"), {Content: "ok"}}
	state := pausedOnQuestion(t, store, holder, "task-ask")

	if got := holder.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("routeUserInputResponse() = %v, want delivered", got)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the resumed turn never reached its tool")
	}

	holder.activeTasksMu.Lock()
	entry := holder.activeTasks["task-ask"]
	holder.activeTasksMu.Unlock()
	if entry == nil || entry.checkpointed == nil || !entry.checkpointed.Load() {
		t.Errorf("active entry of the resumed turn = %+v, want it marked as checkpointed", entry)
	}
}

func TestCancellingTheActiveTaskSpendsItsCancelledResponse(t *testing.T) {
	agent, _, _ := replicaOf(t, session.NewMemoryStore(), "replica")
	var cancels atomic.Int32
	entry := &activeTaskEntry{cancel: func() { cancels.Add(1) }, replyTo: resumedReplyTopic, publishOnce: &sync.Once{}}
	agent.activeTasksMu.Lock()
	agent.activeTasks["task-fenced"] = entry
	agent.activeTasksMu.Unlock()
	t.Cleanup(func() {
		agent.activeTasksMu.Lock()
		delete(agent.activeTasks, "task-fenced")
		agent.activeTasksMu.Unlock()
	})

	agent.cancelActiveTask("task-fenced")

	if cancels.Load() != 1 {
		t.Errorf("cancel calls = %d, want 1", cancels.Load())
	}
	published := false
	entry.publishOnce.Do(func() { published = true })
	if published {
		t.Error("the entry could still publish a cancelled response after the task was fenced; the new owner answers it")
	}
}

func TestRecordedAnswerIsNotDeliveredToACancelledTask(t *testing.T) {
	cases := map[string]struct {
		cancelled     bool
		wantDelivered bool
		wantPending   int
		wantResumes   int32
	}{
		"the task was cancelled":     {cancelled: true, wantDelivered: false, wantPending: 1, wantResumes: 0},
		"the task was not cancelled": {cancelled: false, wantDelivered: true, wantPending: 0, wantResumes: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var resumes atomic.Int32
			useResumeHook(t, func(*Agent, *TaskExecutionState) { resumes.Add(1) })
			store := session.NewMemoryStore()
			owner, _, _ := replicaOf(t, store, "owner")
			state := pausedOnQuestion(t, store, owner, "task-ask")
			state.Cancelled.Store(tc.cancelled)
			pc := state.PendingCalls["tc-1"]

			delivered := owner.deliverRecordedAnswer(state, pc, tool.OK("EMEA"))

			if delivered != tc.wantDelivered {
				t.Errorf("deliverRecordedAnswer() = %v, want %v", delivered, tc.wantDelivered)
			}
			state.mu.Lock()
			pending := len(state.PendingCalls)
			state.mu.Unlock()
			if pending != tc.wantPending || resumes.Load() != tc.wantResumes {
				t.Errorf("pending calls %d, resumes %d; want %d and %d", pending, resumes.Load(), tc.wantPending, tc.wantResumes)
			}
		})
	}
}

type lookupIgnoringAgentStore struct {
	session.Store
}

func (s lookupIgnoringAgentStore) LookupPendingCall(ctx context.Context, _, corrID string) (*session.PendingCall, error) {
	return s.Store.LookupPendingCall(ctx, "test-agent", corrID)
}

func TestAnswerIsNotRelayedForAnotherAgentsPausedCall(t *testing.T) {
	base := session.NewMemoryStore()
	store := lookupIgnoringAgentStore{Store: base}
	owner, _, _ := replicaOf(t, store, "owner")
	state := pausedOnQuestion(t, store, owner, "task-ask")
	foreign, _, foreignBrk := replicaOf(t, store, "foreign", func(a *Agent) { a.cfg.Name = "foreign-agent" })

	got := foreign.relayUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID), nil)

	if got != hilUnmatched {
		t.Errorf("relayUserInputResponse() = %v, want unmatched", got)
	}
	if recorded := recordedAnswers(t, base, "task-ask"); len(recorded) != 0 {
		t.Errorf("recorded results = %+v, want none", recorded)
	}
	if relayed := len(publishedOn(foreignBrk, a2a.NotifyResponseTopic("test-ns", "foreign-agent", askRequestID))); relayed != 0 {
		t.Errorf("the answer was republished %d times, want 0", relayed)
	}
}

func TestApprovedRemoteToolCallRunsToCompletionInsteadOfPausingAgain(t *testing.T) {
	store := session.NewMemoryStore()
	holder, mock, brk := replicaOf(t, store, "holder")
	strBrk := strBroker(t)
	registerRemoteTool(t, holder, strBrk)
	holder.SetHILToolConfigs(map[string]HILToolConfig{remoteReportTool: {RequireApproval: true, ShowArgs: true, Timeout: time.Hour}})
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool), {Content: "The report is ready."}}
	ids, release := heldSTR(t, strBrk, "report ready")

	if resp, err := holder.HandleTask(t.Context(), "task-approved-remote", concUserMessage("build the report", idgen.New()), approvalUser("task-approved-remote")); err != nil || resp != nil {
		t.Fatalf("the task should pause on the approval: resp=%v err=%v", resp, err)
	}
	var requestID string
	if !waitUntil(func() bool { requestID = publishedApprovalRequestID(brk); return requestID != "" }) {
		t.Fatal("the approval request was never published")
	}
	if got := holder.routeUserInputResponse(t.Context(), approve(requestID, true), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("routeUserInputResponse() = %v, want delivered", got)
	}
	waitForCorrID(t, ids)
	release()

	assertResumedToCompletion(t, holder, mock, brk, "task-approved-remote", "report ready")
}

func twoLoginCallsPaused(t *testing.T, agent *Agent) *TaskExecutionState {
	t.Helper()
	state := newTestTaskState(agent)
	state.TaskID = loginTaskID
	state.UserProps.UserID = "u1"
	state.TotalPending = 2
	state.PendingCalls[loginCallID] = &PendingToolCall{
		ToolCallID: loginCallID, ToolName: "stubtool", Kind: PendingCallAuth, Index: 0,
		OriginalArgs: map[string]any{"q": "x"}, CredentialKey: loginKey, Timeout: time.Minute,
	}
	state.PendingCalls["call-follower"] = &PendingToolCall{
		ToolCallID: "call-follower", ToolName: "stubtool", Kind: PendingCallAuth, Index: 1,
		OriginalArgs: map[string]any{"q": "y"}, CredentialKey: loginKey, Timeout: time.Minute, IsAuthFollower: true,
	}
	state.PausedAssistantMsg = llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: loginCallID, Name: "stubtool", Arguments: `{"q":"x"}`},
		{ID: "call-follower", Name: "stubtool", Arguments: `{"q":"y"}`},
	}}
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

func TestSignedInLoginCallBanksItsRealResultOnTheCheckpoint(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("ran after sign-in"), nil
	})
	state := loginPausedState(t, agent)
	state.mu.Lock()
	pc := *state.PendingCalls[loginCallID]
	delete(state.PendingCalls, loginCallID)
	state.mu.Unlock()
	state.authMu.Lock()
	unlocked := false

	agent.completeLoginCall(t.Context(), state, &pc, authResp(loginTaskID, loginCallID, loginKey), a2a.UserProperties{UserID: "u1"}, nil, &unlocked)

	cp, err := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	if err != nil || cp == nil || len(cp.ToolResults) != 1 || cp.ToolResults[0].Content != "ran after sign-in" {
		t.Errorf("checkpoint = %+v (err %v), want the tool's own result banked", cp, err)
	}
}

func TestLoginFollowerCallBanksItsResultOnTheCheckpoint(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		return tool.OK("follower ran after sign-in"), nil
	})
	state := twoLoginCallsPaused(t, agent)

	agent.resumeFollowersInTask(t.Context(), state, loginKey, loginCallID, false)

	cp, err := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	if err != nil || cp == nil || len(cp.ToolResults) != 1 || cp.ToolResults[0].ToolCallID != "call-follower" || cp.ToolResults[0].Content != "follower ran after sign-in" {
		t.Errorf("checkpoint = %+v (err %v), want the follower's result banked", cp, err)
	}
}
