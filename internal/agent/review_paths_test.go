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
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func adoptedForRebuild(t *testing.T, agent *Agent, store session.Store, state *TaskExecutionState) (*TaskExecutionState, *taskRunContext) {
	t.Helper()
	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	adopted, err := unmarshalTaskState(blob)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sess, err := store.Get(t.Context(), "test-agent", state.UserProps.UserID, state.EffectiveSessionID)
	if err != nil || sess == nil {
		t.Fatalf("load session: %v", err)
	}
	sp := NewStatusPublisher(agent.svc.Broker(), StatusPublisherConfig{TaskID: adopted.TaskID, AgentName: agent.cfg.Name}, agent.logger)
	return adopted, &taskRunContext{sess: sess, sp: sp, logger: agent.logger}
}

func hostNotifyAndPeerTurn(state *TaskExecutionState, extra ...llm.ToolCall) {
	calls := append([]llm.ToolCall{{ID: hostNotifyCallIDPrefix + "1", Name: "_notify_artifact_save"}, {ID: "tc-1", Name: "peer_agent"}}, extra...)
	state.PausedAssistantMsg = llm.ChatMessage{Role: "assistant", ToolCalls: calls}
}

func TestAdoptionIgnoresAStoredFileNoticeWhenLookingForTheAnswers(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "adopter")
	state := pausedOnPeerCall(t, store, "task-notice", "sub-notice")
	hostNotifyAndPeerTurn(state)
	storeOwnRows(t, store, state,
		session.Message{Role: "user", Content: "delegate this"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: hostNotifyCallIDPrefix + "1", Name: "_notify_artifact_save"}, {ID: "tc-1", Name: "peer_agent"}}},
		session.Message{Role: "tool", Content: "saved", ToolCallResult: session.ToolCallResult{ToolCallID: hostNotifyCallIDPrefix + "1"}},
	)
	adopted, rc := adoptedForRebuild(t, agent, store, state)

	agent.rebuildAdoptedPrompt(t.Context(), adopted, rc)

	if adopted.pauseReplayed.Load() {
		t.Fatal("a stored file notice was taken for the peer's answer, so the answer would be skipped")
	}
	for _, m := range adopted.Messages {
		if m.ToolCallID == "tc-1" && m.Content == danglingToolResultContent {
			t.Fatal("the pending peer call was repaired as unanswered")
		}
	}
}

func TestAdoptionCountsThePausedBatchsSearches(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "adopter")
	state := pausedOnPeerCall(t, store, "task-searches", "sub-searches")
	state.PausedAssistantMsg = llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "peer_agent"}, {ID: "ws-1", Name: "web_search_google"}}}
	storeOwnRows(t, store, state,
		session.Message{Role: "user", Content: "delegate this"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "ws-0", Name: "web_search_google"}}},
		session.Message{Role: "tool", ToolCallResult: session.ToolCallResult{ToolCallID: "ws-0", ToolName: "web_search_google"}},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "peer_agent"}, {ID: "ws-1", Name: "web_search_google"}}},
	)
	adopted, rc := adoptedForRebuild(t, agent, store, state)

	agent.rebuildAdoptedPrompt(t.Context(), adopted, rc)

	if got := adopted.searchTurns.Next("web_search_google"); got != 2 {
		t.Fatalf("next search turn = %d, want 2: one stored search and one in the paused batch", got)
	}
}

func TestAdoptionCountsSearchesOfDifferentSearchToolsOnOneCounter(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "adopter")
	state := pausedOnPeerCall(t, store, "task-mixed-searches", "sub-mixed-searches")
	state.PausedAssistantMsg = llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "peer_agent"}, {ID: "ws-2", Name: "web_search_cd34ef56"}}}
	storeOwnRows(t, store, state,
		session.Message{Role: "user", Content: "delegate this"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "ws-0", Name: "web_search_google"}}},
		session.Message{Role: "tool", ToolCallResult: session.ToolCallResult{ToolCallID: "ws-0", ToolName: "web_search_google"}},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "ws-1", Name: "web_search_ab12cd34"}}},
		session.Message{Role: "tool", ToolCallResult: session.ToolCallResult{ToolCallID: "ws-1", ToolName: "web_search_ab12cd34"}},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "peer_agent"}, {ID: "ws-2", Name: "web_search_cd34ef56"}}},
	)
	adopted, rc := adoptedForRebuild(t, agent, store, state)

	agent.rebuildAdoptedPrompt(t.Context(), adopted, rc)

	if got := adopted.searchTurns.Next("web_search_google"); got != 3 {
		t.Fatalf("next search turn = %d, want 3: two stored searches and one in the paused batch, across three search tools, so citation ids do not repeat", got)
	}
}

func TestAdoptionKeepsTheCheckpointsTotalsWhenThePausedTurnIsUnstored(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "adopter")
	state := pausedOnPeerCall(t, store, "task-unstored-totals", "sub-unstored-totals")
	hostNotifyAndPeerTurn(state)
	state.TotalUsage = llm.Usage{PromptTokens: 500}
	storeOwnRows(t, store, state, session.Message{Role: "user", Content: "delegate this"})
	adopted, rc := adoptedForRebuild(t, agent, store, state)

	agent.rebuildAdoptedPrompt(t.Context(), adopted, rc)

	if adopted.TotalUsage.PromptTokens != 500 {
		t.Fatalf("prompt tokens = %d, want the checkpoint's 500: the rows lack the paused turn", adopted.TotalUsage.PromptTokens)
	}
	last := adopted.Messages[len(adopted.Messages)-1]
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].ID != "tc-1" {
		t.Fatalf("paused turn put back = %+v, want only the dispatched call: its file notice result was never stored", last.ToolCalls)
	}
}

func TestAdoptedTaskThatStoredItsFinalAnswerRepublishesIt(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "adopter")
	state := pausedOnPeerCall(t, store, "task-final", "sub-final")
	storeOwnRows(t, store, state,
		session.Message{Role: "user", Content: "delegate this"},
		session.Message{Role: "assistant", Content: "answer with ```save_artifact``` markup", RunFacts: &session.RunFacts{Final: true, Answer: "answer as shown"}},
	)
	adopted, rc := adoptedForRebuild(t, agent, store, state)

	agent.rebuildAdoptedPrompt(t.Context(), adopted, rc)

	if rc.storedFinalAnswer == nil || *rc.storedFinalAnswer != "answer as shown" {
		t.Fatalf("stored final answer = %v, want the answer the user saw", rc.storedFinalAnswer)
	}
}

func TestAdoptedTaskThatStoredItsFinalAnswerRepublishesItWithItsOutcome(t *testing.T) {
	tests := map[string]struct {
		facts       session.RunFacts
		wantOutcome any
	}{
		"an adopted quiet final keeps its outcome":     {session.RunFacts{Final: true, Quiet: true}, "quiet"},
		"an adopted ordinary final carries no outcome": {session.RunFacts{Final: true}, nil},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			store := session.NewMemoryStore()
			agent, _, _ := replicaOf(t, store, "adopter")
			state := pausedOnPeerCall(t, store, "task-final-outcome", "sub-final-outcome")
			storeOwnRows(t, store, state,
				session.Message{Role: "user", Content: "delegate this"},
				session.Message{Role: "assistant", Content: "Nothing to report.", RunFacts: &tc.facts},
			)
			adopted, rc := adoptedForRebuild(t, agent, store, state)

			agent.rebuildAdoptedPrompt(t.Context(), adopted, rc)
			resp := agent.republishStoredFinal(t.Context(), adopted, rc)

			task, ok := resp.Result.(a2a.Task)
			if !ok {
				t.Fatalf("result = %T, want a Task", resp.Result)
			}
			if got := task.Metadata["runOutcome"]; got != tc.wantOutcome {
				t.Fatalf("republished runOutcome = %v, want %v", got, tc.wantOutcome)
			}
		})
	}
}

func TestMarkFinalKeepsTheVisibleAnswerOnlyWhenItDiffers(t *testing.T) {
	var p pendingRunFacts
	p.markFinal("same", "same")
	if f := p.take(); f == nil || !f.Final || f.Answer != "" {
		t.Fatalf("facts = %+v, want Final with no separate answer", f)
	}
	p.markFinal("shown", "stored with blocks")
	if f := p.take(); f == nil || f.Answer != "shown" {
		t.Fatalf("facts = %+v, want the shown answer kept", f)
	}
}

func TestAdoptedLoginWhoseResultIsStoredIsNotRunAgain(t *testing.T) {
	var runs atomic.Int32
	agent, _ := newLoginAgent(t, func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
		runs.Add(1)
		return tool.OK("ran"), nil
	})
	state := loginPausedState(t, agent)
	if _, err := agent.claimLoginAnswer(t.Context(), loginTaskID, loginCallID, "stubtool"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	cp, _ := agent.checkpoints.store.GetCheckpoint(t.Context(), loginTaskID)
	agent.applyCheckpointResults(state, cp.ToolResults)

	repaused := agent.retryLoginsOnAdoption(t.Context(), state, map[string]bool{loginCallID: true}, agent.logger)

	if runs.Load() != 0 {
		t.Fatal("a login-gated tool whose result the previous owner stored ran again")
	}
	if repaused || len(state.PendingCalls) != 0 {
		t.Fatalf("repaused=%v pending=%d, want the call settled from the stored row", repaused, len(state.PendingCalls))
	}
}

func TestSecondSignInPromptGetsAPendingRowAgain(t *testing.T) {
	agent, _ := newLoginAgent(t, nil)
	state := loginPausedState(t, agent)
	if _, err := agent.claimLoginAnswer(t.Context(), loginTaskID, loginCallID, "stubtool"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	agent.pausedTasks.Store(state)
	state.mu.Lock()
	claimed := *state.PendingCalls[loginCallID]
	delete(state.PendingCalls, loginCallID)
	state.mu.Unlock()

	state.authMu.Lock()
	rearmed := agent.rearmPendingAuth(state, &claimed)
	state.authMu.Unlock()
	if !rearmed {
		t.Fatal("first retry did not re-arm")
	}
	if pc := state.PendingCalls[loginCallID]; pc != nil && pc.TimeoutTimer != nil {
		pc.TimeoutTimer.Stop()
	}
	if !loginRowExists(t, agent) {
		t.Fatal("the second prompt has no pending row, so its answer on another AWE is neither exchanged nor passed on")
	}
}

func TestPendingRowsSkipCallsAlreadyAnswered(t *testing.T) {
	c, _ := newEnabledCheckpointer(t)
	state := newTestTaskState(&Agent{cfg: AgentConfig{MaxLLMCalls: 5}})
	state.PendingCalls["peer"] = &PendingToolCall{ToolCallID: "peer", Kind: PendingCallPeer, SubTaskID: "sub", Result: tool.OK("came")}
	state.PendingCalls["login"] = &PendingToolCall{ToolCallID: "login", Kind: PendingCallAuth, Timeout: time.Minute}
	rows, _ := c.correlationsFor(state)
	if len(rows) != 1 || rows[0].ToolCallID != "login" {
		t.Fatalf("rows = %+v, want only the unanswered login", rows)
	}
}

func TestForwardTopicMessageReachesTheWaitingToolAndIsNotPassedOnAgain(t *testing.T) {
	agent, brk := newLoginAgent(t, nil)
	ch := agent.registerHILRequest("req-routed", "u1", "")
	msg := forwardedMessage(t, "task-hil", userInputResponseMessage(t, "task-hil", "req-routed"), "u1")
	msg.Topic = agent.forwardTopic()

	agent.routeMessage(t.Context(), msg)

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("a message on the forward topic did not reach the waiting tool")
	}
	for _, p := range brk.getPublished() {
		if p.topic == agent.forwardTopic() {
			t.Fatal("a passed-on message was passed on again")
		}
	}
}

func TestRedeliveredCopyOfATaskRunningHereIsDropped(t *testing.T) {
	agent, mock, _ := newContinuingAgent(t, &llm.ChatResponse{Content: "x", FinishReason: "stop"})
	running := &activeTaskEntry{}
	agent.activeTasksMu.Lock()
	agent.activeTasks[continuedTaskID] = running
	agent.activeTasksMu.Unlock()
	payload, _ := json.Marshal(a2a.NewRequest(continuedTaskID, "message/send", map[string]any{"message": newUserMessage("again")}))
	var req a2a.Request
	_ = json.Unmarshal(payload, &req)

	agent.processMessage(t.Context(), &broker.Message{Payload: payload, Redelivered: true}, &req)

	if len(mock.calls) != 0 {
		t.Fatal("a redelivered copy of a task running in this process ran a second time")
	}
	agent.activeTasksMu.Lock()
	still := agent.activeTasks[continuedTaskID]
	agent.activeTasksMu.Unlock()
	if still != running {
		t.Fatal("the copy replaced the running task's entry")
	}
}

func TestVersionOneCheckpointResumesWithItsConversation(t *testing.T) {
	blob, err := marshalTaskState(fullTaskState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(blob, &raw)
	raw["version"] = 1
	raw["messages"] = []map[string]any{{"Role": "user", "Content": "from the old blob"}}
	raw["history_offset"] = 1
	v1, _ := json.Marshal(raw)

	state, err := unmarshalTaskState(v1)
	if err != nil {
		t.Fatalf("a version 1 checkpoint was refused: %v", err)
	}
	if state.rebuildFromRows || len(state.Messages) != 1 || state.Messages[0].Content != "from the old blob" || state.HistoryOffset != 1 {
		t.Fatalf("version 1 state = rebuild %v, %d messages, offset %d; want its own conversation", state.rebuildFromRows, len(state.Messages), state.HistoryOffset)
	}
}

func TestSeedRunFactsRestoresEveryKindOfFact(t *testing.T) {
	tokens := func(n int) json.RawMessage { b, _ := json.Marshal(llm.Usage{PromptTokens: n}); return b }
	state := &TaskExecutionState{}
	state.seedRunFacts(&session.RunFacts{
		Usage: []session.CallUsage{
			{Model: "m", Source: usageSourceAgent, Tokens: tokens(10)},
			{Model: "t", Source: usageSourceTool, Tokens: tokens(3)},
			{Source: usageSourceUnattributed, Tokens: tokens(2)},
		},
		Files:   []session.ProducedFile{{Filename: "block.md", Source: producedBySourceBlock}, {Filename: "tool.pdf", Source: producedBySourceTool}},
		Failure: &session.ToolFailure{Code: "TOOL_EXEC_FAILED"},
	})
	if state.TotalUsage.PromptTokens != 15 || state.ToolUsageByModel["t"].PromptTokens != 3 || state.UsageByModel["m"].PromptTokens != 10 || state.LastInputTokens != 10 {
		t.Fatalf("usage = total %d, tool %+v, by model %+v, last %d", state.TotalUsage.PromptTokens, state.ToolUsageByModel, state.UsageByModel, state.LastInputTokens)
	}
	if len(state.CreatedArtifacts) != 1 || state.CreatedArtifacts[0].Filename != "block.md" || len(state.toolSavesSnapshot()) != 1 {
		t.Fatalf("files = blocks %+v, tool saves %+v", state.CreatedArtifacts, state.toolSavesSnapshot())
	}
	if !state.HadToolFailure || state.ToolFailureDetail == nil || state.ToolFailureDetail.Code != "TOOL_EXEC_FAILED" {
		t.Fatalf("failure = %v %+v", state.HadToolFailure, state.ToolFailureDetail)
	}
}

func TestRepublishedRunBasedAnswerIsAlsoPublishedOnTheStatusStream(t *testing.T) {
	agent, brk := newLoginAgent(t, nil)
	state := newTestTaskState(agent)
	state.IsRunBased = true
	answer := "stored answer"
	rc := &taskRunContext{
		sp:                NewStatusPublisher(brk, StatusPublisherConfig{StatusTopic: "test-ns/status/caller", TaskID: state.TaskID, AgentName: agent.cfg.Name}, agent.logger),
		storedFinalAnswer: &answer,
		logger:            agent.logger,
	}

	resp := agent.republishStoredFinal(t.Context(), state, rc)

	if resp == nil || !strings.Contains(mustJSON(t, resp.Result), answer) {
		t.Fatalf("response = %+v", resp)
	}
	var onStream bool
	for _, p := range brk.getPublished() {
		if p.topic == "test-ns/status/caller" && strings.Contains(string(p.payload), answer) {
			onStream = true
		}
	}
	if !onStream {
		t.Fatal("a run-based task's republished answer did not reach the caller's status stream")
	}
}
