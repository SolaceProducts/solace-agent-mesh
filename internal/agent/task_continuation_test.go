// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const continuedTaskID = "task-cont"

func newContinuingAgent(t *testing.T, resps ...*llm.ChatResponse) (*Agent, *mockLLM, *memSessionStore) {
	t.Helper()
	agent, mock, store := newTestAgent(resps...)
	svc := agent.svc.(*mockServices)
	svc.featureRegistry = durableResumeRegistry(true)
	agent.checkpoints = newTaskCheckpointer(svc, agent.cfg.Name, newTestAgentBackstops(), agent.logger)
	if agent.checkpoints == nil {
		t.Fatal("expected durable resume to be enabled")
	}
	return agent, mock, store
}

func seedTaskRows(t *testing.T, agent *Agent, store *memSessionStore, rows ...session.Message) {
	t.Helper()
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "user-1", SessionID: "ctx-1", ContextID: "ctx-1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for i := range rows {
		if rows[i].TaskID == "" {
			rows[i].TaskID = continuedTaskID
		}
		rows[i].Timestamp = time.Now()
	}
	if err := store.AppendMessages(t.Context(), sess.AppName, sess.UserID, sess.SessionID, rows); err != nil {
		t.Fatalf("seed rows: %v", err)
	}
}

func usageFact(model string, prompt int) session.CallUsage {
	tokens, _ := json.Marshal(llm.Usage{PromptTokens: prompt, TotalTokens: prompt})
	return session.CallUsage{Model: model, Source: usageSourceAgent, Tokens: tokens}
}

func runRedelivered(t *testing.T, agent *Agent) *a2a.Response {
	t.Helper()
	resp, err := agent.handleTask(t.Context(), continuedTaskID, newUserMessage("make me a report"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}, taskDelivery{redelivered: true})
	if err != nil {
		t.Fatalf("handleTask: %v", err)
	}
	return resp
}

func stored(t *testing.T, agent *Agent, store *memSessionStore) []session.Message {
	t.Helper()
	sess, err := store.Get(t.Context(), agent.cfg.Name, "user-1", "ctx-1")
	if err != nil || sess == nil {
		t.Fatalf("load session: %v", err)
	}
	return sess.Messages
}

func TestRedeliveredTaskContinuesFromItsStoredTurns(t *testing.T) {
	agent, mock, store := newContinuingAgent(t, &llm.ChatResponse{
		Content: "here is the report", FinishReason: "stop",
		Usage: &llm.Usage{PromptTokens: 50, TotalTokens: 50},
	})
	seedTaskRows(t, agent, store,
		session.Message{Role: "user", Content: "make me a report"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "c1", Name: "query_sales", Arguments: "{}"}},
			RunFacts: &session.RunFacts{Usage: []session.CallUsage{usageFact("test-model", 100)}}},
		session.Message{Role: "tool", Content: "the sales numbers", ToolCallResult: session.ToolCallResult{ToolCallID: "c1", ToolName: "query_sales"},
			RunFacts: &session.RunFacts{Files: []session.ProducedFile{{Filename: "sales.csv", Version: 1, Source: producedBySourceTool, Status: "success"}}}},
	)

	resp := runRedelivered(t, agent)

	if got := len(mock.calls); got != 1 {
		t.Fatalf("model calls = %d, want 1: the stored turn must not run again", got)
	}
	var questions int
	var toolResult string
	for _, m := range mock.calls[0].Messages {
		if m.Role == "user" && strings.Contains(m.Content, "make me a report") {
			questions++
		}
		if m.Role == "tool" && m.ToolCallID == "c1" {
			toolResult = m.Content
		}
	}
	if questions != 1 {
		t.Fatalf("the question appears %d times in the prompt, want 1", questions)
	}
	if toolResult != "the sales numbers" {
		t.Fatalf("the stored tool result reached the model as %q, want it whole", toolResult)
	}

	rows := ownRows(stored(t, agent, store), continuedTaskID)
	var storedQuestions int
	for _, m := range rows {
		if m.Role == "user" {
			storedQuestions++
		}
	}
	if storedQuestions != 1 {
		t.Fatalf("the question is stored %d times, want 1", storedQuestions)
	}
	if last := rows[len(rows)-1]; last.RunFacts == nil || !last.RunFacts.Final || last.Content != "here is the report" {
		t.Fatalf("last row = %+v, want the new final answer marked Final", last)
	}

	task, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("response result is %T", resp.Result)
	}
	usage, _ := task.Metadata["token_usage"].(map[string]any)
	if got := toIntAny(usage["total_input_tokens"]); got != 150 {
		t.Fatalf("total_input_tokens = %d, want 150 (100 before the cut + 50 after)", got)
	}
	if !strings.Contains(mustJSON(t, task.Metadata["produced_artifacts"]), "sales.csv") {
		t.Fatalf("produced_artifacts = %v, want the file saved before the cut", task.Metadata["produced_artifacts"])
	}
}

func TestRedeliveredTaskWithStoredFinalAnswerIsRepublished(t *testing.T) {
	agent, mock, store := newContinuingAgent(t)
	seedTaskRows(t, agent, store,
		session.Message{Role: "user", Content: "make me a report"},
		session.Message{Role: "assistant", Content: "the stored answer", RunFacts: &session.RunFacts{Final: true}},
	)

	resp := runRedelivered(t, agent)

	if len(mock.calls) != 0 {
		t.Fatalf("model calls = %d, want 0", len(mock.calls))
	}
	if resp == nil || !strings.Contains(mustJSON(t, resp.Result), "the stored answer") {
		t.Fatalf("response = %+v, want the stored answer", resp)
	}
	if got := len(ownRows(stored(t, agent, store), continuedTaskID)); got != 2 {
		t.Fatalf("stored rows = %d, want 2: republishing writes nothing", got)
	}
}

func TestRedeliveredTaskIsDroppedWhenACheckpointOwnsIt(t *testing.T) {
	agent, mock, store := newContinuingAgent(t, &llm.ChatResponse{Content: "x", FinishReason: "stop"})
	seedTaskRows(t, agent, store, session.Message{Role: "user", Content: "make me a report"})
	if err := store.SaveCheckpoint(t.Context(), &session.TaskCheckpoint{
		TaskID: continuedTaskID, AppName: agent.cfg.Name, UserID: "user-1", SessionID: "ctx-1", AgentName: agent.cfg.Name, State: []byte("{}"),
	}); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	if resp := runRedelivered(t, agent); resp != nil {
		t.Fatalf("response = %+v, want none", resp)
	}
	if len(mock.calls) != 0 {
		t.Fatalf("model calls = %d, want 0", len(mock.calls))
	}
}

func TestRedeliveredCancelledTaskIsDropped(t *testing.T) {
	agent, mock, store := newContinuingAgent(t, &llm.ChatResponse{Content: "x", FinishReason: "stop"})
	seedTaskRows(t, agent, store,
		session.Message{Role: "user", Content: "make me a report"},
		session.Message{Role: "assistant", Content: cancellationMarkerContent},
	)

	if resp := runRedelivered(t, agent); resp != nil {
		t.Fatalf("response = %+v, want none", resp)
	}
	if len(mock.calls) != 0 {
		t.Fatalf("model calls = %d, want 0", len(mock.calls))
	}
}

func TestRedeliveredTaskWithOnlyItsQuestionDoesNotStoreItTwice(t *testing.T) {
	agent, mock, store := newContinuingAgent(t, &llm.ChatResponse{Content: "answer", FinishReason: "stop"})
	seedTaskRows(t, agent, store, session.Message{Role: "user", Content: "make me a report"})

	runRedelivered(t, agent)

	var questions int
	for _, m := range mock.calls[0].Messages {
		if m.Role == "user" && strings.Contains(m.Content, "make me a report") {
			questions++
		}
		if m.Role == "assistant" && m.Content == cancellationMarkerContent {
			t.Fatal("the task's own question was repaired as an interrupted turn")
		}
	}
	if questions != 1 {
		t.Fatalf("question in the prompt %d times, want 1", questions)
	}
	var storedQuestions int
	for _, m := range ownRows(stored(t, agent, store), continuedTaskID) {
		if m.Role == "user" {
			storedQuestions++
		}
	}
	if storedQuestions != 1 {
		t.Fatalf("question stored %d times, want 1", storedQuestions)
	}
}

func TestRedeliveredTaskWithDurableResumeOffStartsAfresh(t *testing.T) {
	agent, mock, store := newTestAgent(&llm.ChatResponse{Content: "answer", FinishReason: "stop"})
	seedTaskRows(t, agent, store,
		session.Message{Role: "user", Content: "make me a report"},
		session.Message{Role: "assistant", Content: "the stored answer", RunFacts: &session.RunFacts{Final: true}},
	)

	runRedelivered(t, agent)

	if len(mock.calls) != 1 {
		t.Fatalf("model calls = %d, want 1: with the flag off a redelivery reruns as before", len(mock.calls))
	}
}

func TestStoredTurnWithoutItsPendingResultsReachesTheModelPaired(t *testing.T) {
	agent, mock, store := newContinuingAgent(t, &llm.ChatResponse{Content: "answer", FinishReason: "stop"})
	seedTaskRows(t, agent, store,
		session.Message{Role: "user", Content: "make me a report"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "p1", Name: "peer_Finance", Arguments: "{}"}}},
	)

	runRedelivered(t, agent)

	var paired bool
	for _, m := range mock.calls[0].Messages {
		if m.Role == "tool" && m.ToolCallID == "p1" {
			paired = true
		}
	}
	if !paired {
		t.Fatal("a stored tool call without a result reached the model unpaired")
	}
}

func toIntAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestHandleTask_CompactionRetryStoresTheQuestionOnce(t *testing.T) {
	client := &overflowOnceLLM{mockLLM: newMockLLM(
		&llm.ChatResponse{Content: "summary", FinishReason: "stop"},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)}
	store := newMemSessionStore()
	agent := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
	svc := newMockServices(client, store)
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	seedSession(store, "test-agent", "user-1", "ctx-1")

	if _, err := agent.HandleTask(t.Context(), "task-compact-once", newUserMessage("and italy?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	sess, err := store.Get(t.Context(), "test-agent", "user-1", "ctx-1")
	if err != nil || sess == nil {
		t.Fatalf("load session: %v", err)
	}
	var stored int
	for _, m := range ownRows(sess.Messages, "task-compact-once") {
		if m.Role == "user" && strings.Contains(m.Content, "and italy?") {
			stored++
		}
	}
	if stored != 1 {
		t.Fatalf("question stored %d times, want 1", stored)
	}
	calls := client.getCalls()
	var inPrompt int
	for _, m := range calls[len(calls)-1].Messages {
		if m.Role == "user" && strings.Contains(m.Content, "and italy?") {
			inPrompt++
		}
	}
	if inPrompt != 1 {
		t.Fatalf("question in the retried prompt %d times, want 1", inPrompt)
	}
}

func storeOwnRows(t *testing.T, store session.Store, state *TaskExecutionState, rows ...session.Message) {
	t.Helper()
	for i := range rows {
		rows[i].TaskID = state.TaskID
		rows[i].Timestamp = time.Now()
	}
	if err := store.AppendMessages(t.Context(), "test-agent", state.UserProps.UserID, state.EffectiveSessionID, rows); err != nil {
		t.Fatalf("store rows: %v", err)
	}
}

func adoptAfterAnswer(t *testing.T, store session.Store, owner, survivor *Agent, state *TaskExecutionState, answer string) {
	t.Helper()
	if err := owner.checkpoints.save(t.Context(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	loseOwner(t, store, state, 1)
	owner.checkpoints.recordResult(t.Context(), state.TaskID, state.PendingCalls["tc-1"], tool.OK(answer))
	survivor.resumeAbandonedCheckpoints(t.Context(), time.Now())
}

func countInPrompt(req llm.ChatRequest, match func(llm.ChatMessage) bool) int {
	n := 0
	for _, m := range req.Messages {
		if match(m) {
			n++
		}
	}
	return n
}

func TestAdoptedTaskRebuildsItsTurnsFromTheSession(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-rebuild", "sub-rebuild")
	storeOwnRows(t, store, state,
		session.Message{Role: "user", Content: "delegate this"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
	)

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	req := survivorLLM.getCalls()[0]
	if n := countInPrompt(req, func(m llm.ChatMessage) bool { return m.Role == "tool" && m.ToolCallID == "tc-1" }); n != 1 {
		t.Fatalf("results for tc-1 in the prompt = %d, want 1", n)
	}
	if n := countInPrompt(req, func(m llm.ChatMessage) bool { return m.Content == danglingToolResultContent }); n != 0 {
		t.Fatal("the paused call was repaired as unanswered although its answer was coming")
	}
	if n := countInPrompt(req, func(m llm.ChatMessage) bool { return m.Role == "user" && m.Content == "delegate this" }); n != 1 {
		t.Fatalf("question in the prompt %d times, want 1", n)
	}
}

func TestAdoptedTaskWhosePausedTurnWasNotStoredGetsItFromTheCheckpoint(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-unstored", "sub-unstored")
	storeOwnRows(t, store, state, session.Message{Role: "user", Content: "delegate this"})

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	req := survivorLLM.getCalls()[0]
	if n := countInPrompt(req, func(m llm.ChatMessage) bool {
		return m.Role == "assistant" && len(m.ToolCalls) == 1 && m.ToolCalls[0].ID == "tc-1"
	}); n != 1 {
		t.Fatalf("paused turn in the prompt %d times, want 1", n)
	}
	sess, err := store.Get(t.Context(), "test-agent", state.UserProps.UserID, state.EffectiveSessionID)
	if err != nil || sess == nil {
		t.Fatalf("load session: %v", err)
	}
	var storedTurn bool
	for _, m := range ownRows(sess.Messages, state.TaskID) {
		if len(m.ToolCalls) == 1 && m.ToolCalls[0].ID == "tc-1" {
			storedTurn = true
		}
	}
	if !storedTurn {
		t.Fatal("the paused turn taken from the checkpoint was never stored")
	}
}

func TestAdoptedTaskThatAlreadyStoredItsAnswerDoesNotReplayIt(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnPeerCall(t, store, "task-replayed", "sub-replayed")
	storeOwnRows(t, store, state,
		session.Message{Role: "user", Content: "delegate this"},
		session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
		session.Message{Role: "tool", Content: "peer finished the work", ToolCallResult: session.ToolCallResult{ToolCallID: "tc-1", ToolName: "peer_agent"}},
	)

	adoptAfterAnswer(t, store, owner, survivor, state, "peer finished the work")
	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, state.TaskID, "peer finished the work")

	if n := countInPrompt(survivorLLM.getCalls()[0], func(m llm.ChatMessage) bool { return m.Role == "tool" && m.ToolCallID == "tc-1" }); n != 1 {
		t.Fatalf("results for tc-1 in the prompt = %d, want 1: an answer already stored is not replayed", n)
	}
}

func TestContinuedTaskNumbersSearchesAfterTheStoredOnes(t *testing.T) {
	state := &TaskExecutionState{MaxCalls: 10, searchTurns: &tool.SearchTurns{}}
	state.seedFromOwnRows([]session.Message{
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "a", Name: "web_search_google"}}},
		{Role: "tool", ToolCallResult: session.ToolCallResult{ToolCallID: "a", ToolName: "web_search_google"}},
		{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "b", Name: "web_search_google"}}},
		{Role: "tool", ToolCallResult: session.ToolCallResult{ToolCallID: "b", ToolName: "web_search_google"}},
	})
	if got := state.searchTurns.Next("web_search_google"); got != 2 {
		t.Fatalf("next search turn = %d, want 2: citation ids from the stored searches must not repeat", got)
	}
	if state.LoopIteration != 2 || state.LLMCallCount != 2 {
		t.Fatalf("call counters = %d/%d, want 2/2 from the stored assistant turns", state.LoopIteration, state.LLMCallCount)
	}
}

func TestContinuedTaskStillReturnsTheFileADelegateSaved(t *testing.T) {
	tests := map[string]string{
		"a peer":     peerToolPrefix + "analyst",
		"a sub-task": subTaskToolName,
	}
	for label, toolName := range tests {
		t.Run(label, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			stored := agent.buildToolResultMessage(t.Context(), deckPeerResult(t), nil, nil, llm.ToolCall{ID: "a", Name: toolName})
			state := newTestTaskState(agent)

			state.seedFromOwnRows([]session.Message{
				{Role: "user", Content: "Build the deck"},
				{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "a", Name: toolName}}},
				{Role: "tool", Content: stored.Content, ToolCallResult: session.ToolCallResult{ToolCallID: "a", ToolName: toolName}},
			})

			want := "\n\n" + peerDeckEmbed
			if got := namedFileReturns("The delegate built deck.pptx.", producedFiles(state)); got != want {
				t.Fatalf("namedFileReturns after the continuation = %q, want %q", got, want)
			}
		})
	}
}

func TestUnreadableCheckpointIsRemovedAndANewerOneReleased(t *testing.T) {
	store := session.NewMemoryStore()
	survivor, _, _ := replicaOf(t, store, "survivor", func(a *Agent) { a.checkpoints.takeoverAfter = 20 * time.Millisecond })
	lapsed := time.Now().Add(-time.Hour)
	save := func(taskID string, blob []byte) {
		if err := store.SaveCheckpoint(t.Context(), &session.TaskCheckpoint{
			TaskID: taskID, AppName: "test-agent", AgentName: "test-agent", UserID: "u", SessionID: "s",
			State: blob, OwnerID: "owner-that-died", LeaseExpiresAt: &lapsed,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	save("task-corrupt", []byte("not json"))
	save("task-newer", []byte(`{"version": 99}`))
	time.Sleep(50 * time.Millisecond)

	survivor.resumeAbandonedCheckpoints(t.Context(), time.Now())

	if cp, _ := store.GetCheckpoint(t.Context(), "task-corrupt"); cp != nil {
		t.Fatal("a checkpoint no build can read was kept, to be taken and released every tick")
	}
	cp, _ := store.GetCheckpoint(t.Context(), "task-newer")
	if cp == nil {
		t.Fatal("a newer build's checkpoint was removed")
	}
	if cp.OwnerID == survivor.checkpoints.ownerID {
		t.Fatal("a newer build's checkpoint was kept by a process that cannot read it")
	}
}

func approvalPausedState() *TaskExecutionState {
	state := fullTaskState()
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", ToolName: approvalTool, Kind: PendingCallToolApproval, HILRequestID: "hil-approval-version", Timeout: time.Hour, OriginalArgs: map[string]any{"to": "Acme"}},
	}
	state.PausedApprovals = map[string]pausedApprovalCall{"tc-1": {requestID: "hil-approval-version", args: map[string]any{"to": "Acme"}, timeout: time.Hour}}
	state.TotalPending = 1
	return state
}

func TestCheckpointCarryingAnApprovalPauseIsVersionedAbovePreviousBuilds(t *testing.T) {
	const previousBuildVersion = 2
	blob, err := marshalTaskState(approvalPausedState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var header struct {
		Version int `json:"version"`
	}
	if err = json.Unmarshal(blob, &header); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if header.Version <= previousBuildVersion {
		t.Errorf("version = %d, want above %d so a previous build releases the checkpoint instead of resuming it without the approval", header.Version, previousBuildVersion)
	}

	asPreviousBuildWroteIt := strings.Replace(string(blob), fmt.Sprintf(`"version":%d`, header.Version), fmt.Sprintf(`"version":%d`, previousBuildVersion), 1)
	if asPreviousBuildWroteIt == string(blob) {
		t.Fatal("could not rewrite the version, so this test proves nothing")
	}
	state, err := unmarshalTaskState([]byte(asPreviousBuildWroteIt))
	if err != nil {
		t.Fatalf("a version %d checkpoint was refused: %v", previousBuildVersion, err)
	}
	if got := state.PendingCalls["tc-1"]; got == nil || got.Kind != PendingCallToolApproval {
		t.Errorf("pending call = %+v, want the tool approval kept", got)
	}
}

func TestCheckpointWithAPendingKindThisBuildDoesNotKnowIsReleasedNotDeleted(t *testing.T) {
	store := session.NewMemoryStore()
	survivor, _, _ := replicaOf(t, store, "survivor", func(a *Agent) { a.checkpoints.takeoverAfter = 20 * time.Millisecond })
	blob, err := marshalTaskState(approvalPausedState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	future := strings.Replace(string(blob), `"kind":"tool_approval"`, `"kind":"kind_from_a_later_build"`, 1)
	if future == string(blob) {
		t.Fatal("could not rewrite the pending kind, so this test proves nothing")
	}
	lapsed := time.Now().Add(-time.Hour)
	if err = store.SaveCheckpoint(t.Context(), &session.TaskCheckpoint{
		TaskID: "task-future-kind", AppName: "test-agent", AgentName: "test-agent", UserID: "u", SessionID: "s",
		State: []byte(future), OwnerID: "owner-that-died", LeaseExpiresAt: &lapsed,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	survivor.resumeAbandonedCheckpoints(t.Context(), time.Now())

	cp, err := store.GetCheckpoint(t.Context(), "task-future-kind")
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("a checkpoint with a pending kind from a later build was deleted; a build that knows the kind can still resume it")
	}
	if cp.OwnerID == survivor.checkpoints.ownerID {
		t.Fatal("a checkpoint with a pending kind from a later build was kept by a process that cannot read it")
	}
}
