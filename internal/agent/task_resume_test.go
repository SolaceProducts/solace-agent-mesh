// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func peerReplyMessage(t *testing.T, text string) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "sub-1",
		"result": map[string]any{
			"status": map[string]any{
				"message": map[string]any{
					"parts": []any{map[string]any{"kind": "text", "text": text}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal peer reply: %v", err)
	}
	return &broker.Message{Payload: payload, UserProperties: map[string]string{"agent_name": "peer-1"}}
}

func checkpointedAgent(t *testing.T, store session.Store) *Agent {
	t.Helper()
	agent, _, _ := newTestAgent()
	svc := newMockServices(newMockLLM(), store)
	svc.featureRegistry = durableResumeRegistry(true)
	agent.svc = svc
	agent.checkpoints = newTaskCheckpointer(svc, agent.cfg.Name, agent.delegationBackstops(), agent.logger)
	if agent.checkpoints == nil {
		t.Fatal("expected durable resume to be enabled")
	}
	databaseAnswering(agent.checkpoints)
	agent.runCtx = context.Background()
	return agent
}

func seedPausedCheckpoint(t *testing.T, agent *Agent, taskID, subTaskID string) {
	t.Helper()
	state := fullTaskState()
	state.TaskID = taskID
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: subTaskID, Timeout: time.Minute},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

func TestOrphanResponseDeclinedWhenCorrelationUnknown(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)

	agent.adoptOrphanReply(peerAdoption("no-such-sub-task", peerReplyMessage(t, "hi")))

	if len(agent.pausedTasks.Snapshot()) != 0 {
		t.Error("a reply with no correlation row produced a paused task out of nothing")
	}
}

func TestOrphanResponseDeclinedWhenTaskStillPausedLocally(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	seedPausedCheckpoint(t, agent, "task-local", "sub-local")

	local := fullTaskState()
	local.TaskID = "task-local"
	agent.pausedTasks.Store(local)

	agent.adoptOrphanReply(peerAdoption("sub-local", peerReplyMessage(t, "hi")))

	got, err := store.LookupPendingCall(context.Background(), "test-agent", "sub-local")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil {
		t.Error("the orphan path consumed the correlation of a task paused right here, racing its own in-process resolution")
	}
}

func TestOrphanResponseDeclinedWhenDisabled(t *testing.T) {
	agent, _, _ := newTestAgent()
	if agent.resumeOrphanPeerResponse("sub-1", peerReplyMessage(t, "hi")) {
		t.Error("expected the response to be declined when durable resume is off")
	}
}

func TestOrphanResponseDoesNoStoreWorkOnTheReceiveLoop(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	agent.orphanAdoptions = make(chan orphanAdoption, orphanAdoptionQueueDepth)
	seedPausedCheckpoint(t, agent, "task-async", "sub-async")

	if !agent.resumeOrphanPeerResponse("sub-async", peerReplyMessage(t, "peer finished")) {
		t.Fatal("the response was not taken")
	}

	got, err := store.LookupPendingCall(context.Background(), "test-agent", "sub-async")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil {
		t.Error("the correlation was consumed with no worker running — the store round-trips are still on the receive loop")
	}
	if len(agent.orphanAdoptions) != 1 {
		t.Errorf("queued %d adoptions, want 1", len(agent.orphanAdoptions))
	}
}

func TestOrphanResponseDeclinedWhenQueueIsFull(t *testing.T) {
	agent := checkpointedAgent(t, session.NewMemoryStore())
	agent.orphanAdoptions = make(chan orphanAdoption, 1)
	agent.orphanAdoptions <- orphanAdoption{corrID: "already-queued"}

	if agent.resumeOrphanPeerResponse("sub-overflow", peerReplyMessage(t, "hi")) {
		t.Error("a full queue reported the response as taken, so nothing will ever process it")
	}
}

func TestOrphanResponseAdoptsCheckpointedTask(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	seedPausedCheckpoint(t, agent, "task-orphan", "sub-orphan")

	agent.adoptOrphanReply(peerAdoption("sub-orphan", peerReplyMessage(t, "peer finished")))

	got, err := store.LookupPendingCall(context.Background(), "test-agent", "sub-orphan")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got != nil {
		t.Error("correlation row survived adoption — a duplicate reply could resume the task twice")
	}
}

func TestOnlyOneReplicaClaimsACheckpoint(t *testing.T) {
	store := session.NewMemoryStore()
	first := checkpointedAgent(t, store)
	second := checkpointedAgent(t, store)
	seedPausedCheckpoint(t, first, "task-race", "sub-race")

	msg := peerReplyMessage(t, "done")
	first.adoptOrphanReply(peerAdoption("sub-race", msg))
	second.adoptOrphanReply(peerAdoption("sub-race", msg))

	if second.pausedTasks.Load("task-race") != nil {
		t.Error("the second replica adopted an already-consumed correlation — the same turn now runs twice")
	}
	if got, err := store.LookupPendingCall(context.Background(), "test-agent", "sub-race"); err != nil || got != nil {
		t.Errorf("the correlation row outlived adoption: %v, %v", got, err)
	}
}

func TestUndecodableCheckpointIsRemoved(t *testing.T) {
	store := session.NewMemoryStore()
	agent := checkpointedAgent(t, store)
	ctx := context.Background()

	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID:       "task-bad",
		AppName:      agent.cfg.Name,
		SessionID:    "sess",
		PendingCount: 1,
		State:        []byte("{not json"),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.RegisterPendingCalls(ctx, "task-bad", heldOwner(t, store, "task-bad"), []session.PendingCall{
		{CorrID: "sub-bad", TaskID: "task-bad", AgentName: agent.cfg.Name,
			ToolCallID: "tc-bad", ToolName: "peer_agent", Kind: session.PendingKindPeer},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	agent.adoptOrphanReply(peerAdoption("sub-bad", peerReplyMessage(t, "x")))

	if cp, err := store.GetCheckpoint(ctx, "task-bad"); err != nil || cp != nil {
		t.Errorf("an unreadable checkpoint was kept (present=%v, err=%v) — it is taken and released every tick for good", cp != nil, err)
	}
}

func TestApplyCheckpointResultsResolvesPendingCalls(t *testing.T) {
	agent, _, _ := newTestAgent()

	state := fullTaskState()
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 2, SubTaskID: "sub-1"},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1

	agent.applyCheckpointResults(state, []session.ToolCallResult{
		{ToolCallID: "tc-1", ToolName: "peer", Content: "peer said hi"},
	})

	if len(state.PendingCalls) != 0 {
		t.Errorf("PendingCalls still holds %d entries", len(state.PendingCalls))
	}
	if state.TotalPending != 0 {
		t.Errorf("TotalPending = %d, want 0", state.TotalPending)
	}
	res, ok := state.SyncResults[2]
	if !ok {
		t.Fatal("no SyncResults entry at the pending call's dispatch index")
	}
	if res.Message != "peer said hi" {
		t.Errorf("result message = %q, want %q", res.Message, "peer said hi")
	}
}

func TestApplyCheckpointResultsKeepsUnresolvedCallsOutstanding(t *testing.T) {
	agent, _, _ := newTestAgent()

	state := fullTaskState()
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-done": {ToolCallID: "tc-done", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-done"},
		"tc-open": {ToolCallID: "tc-open", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 1, SubTaskID: "sub-open"},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 2

	agent.applyCheckpointResults(state, []session.ToolCallResult{
		{ToolCallID: "tc-done", ToolName: "peer_agent", Content: "the one that answered"},
	})

	if state.TotalPending != 1 {
		t.Errorf("TotalPending = %d, want 1 — the unresolved call was written off as answered", state.TotalPending)
	}
	if _, stillOpen := state.PendingCalls["tc-open"]; !stillOpen {
		t.Error("the unresolved call was dropped from PendingCalls, so nothing is waiting for its reply any more")
	}
	if _, resolved := state.PendingCalls["tc-done"]; resolved {
		t.Error("the resolved call is still listed as pending")
	}
	if state.SyncResults[0] == nil {
		t.Error("the recorded result was not placed at its dispatch index")
	}
	if state.SyncResults[1] != nil {
		t.Error("a result was invented for the call that never answered")
	}
}

func TestApplyCheckpointResultsMarksErrors(t *testing.T) {
	agent, _, _ := newTestAgent()

	state := fullTaskState()
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", Kind: PendingCallPeer, Index: 0, SubTaskID: "sub-1"},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1

	agent.applyCheckpointResults(state, []session.ToolCallResult{
		{ToolCallID: "tc-1", Content: "peer exploded", IsError: true},
	})

	res := state.SyncResults[0]
	if res == nil || res.Status != tool.StatusError {
		t.Errorf("expected an error result, got %+v", res)
	}
}

func TestAPeerResponseForAnotherAgentsCallIsRefused(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "victim")
	ctx := context.Background()

	taskID, sessionID := "task-theirs", "sess-theirs"
	lapsed := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "other-agent", AgentName: "other-agent",
		UserID: "their-user", SessionID: sessionID,
		PendingCount: 1, State: []byte(`{"version":1}`),
		OwnerID: "their-dead-replica", LeaseExpiresAt: &lapsed,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []session.PendingCall{{
		CorrID: "sub-theirs", TaskID: taskID, AgentName: "other-agent",
		ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer,
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}

	if row, err := store.LookupPendingCall(ctx, agent.cfg.Name, "sub-theirs"); err != nil || row != nil {
		t.Errorf("this agent resolved another agent's correlation id (%v, %v) — the predicate is the layer that makes the takeover unreachable rather than merely refused", row, err)
	}

	agent.adoptOrphanReply(peerAdoption("sub-theirs", peerReplyMessage(t, "an answer this agent must not record")))

	cp, err := store.GetCheckpoint(ctx, taskID)
	if err != nil || cp == nil {
		t.Fatalf("read back: %v, %v", cp, err)
	}
	if len(cp.ToolResults) != 0 {
		t.Errorf("this agent recorded a result into another agent's checkpoint (%d results) — at pending_count 0 it then adopts and runs their task under their caller's identity", len(cp.ToolResults))
	}
	if cp.PendingCount != 1 {
		t.Errorf("pending count on the other agent's task = %d, want 1 — this agent decremented it toward adoption", cp.PendingCount)
	}
	if cp.OwnerID != "their-dead-replica" {
		t.Errorf("this agent took ownership of another agent's task (owner now %q)", cp.OwnerID)
	}
	if agent.pausedTasks.Load(taskID) != nil {
		t.Error("this agent is running another agent's task")
	}
}

func TestTheAdoptionPathRefusesAForeignCallEvenIfTheStoreHandsItOne(t *testing.T) {
	store := &agentBlindLookupStore{Store: session.NewMemoryStore()}
	agent, _, _ := replicaOf(t, store, "victim")
	ctx := context.Background()

	lapsed := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: "task-foreign", AppName: "other-agent", AgentName: "other-agent",
		UserID: "their-user", SessionID: "sess-foreign",
		PendingCount: 1, State: []byte(`{"version":1}`),
		OwnerID: "their-replica", LeaseExpiresAt: &lapsed,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if err := store.RegisterPendingCalls(ctx, "task-foreign", heldOwner(t, store, "task-foreign"), []session.PendingCall{{
		CorrID: "sub-foreign", TaskID: "task-foreign", AgentName: "other-agent",
		ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer,
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}

	agent.adoptOrphanReply(peerAdoption("sub-foreign", peerReplyMessage(t, "not this agent's to record")))

	cp, err := store.GetCheckpoint(ctx, "task-foreign")
	if err != nil || cp == nil {
		t.Fatalf("read back: %v, %v", cp, err)
	}
	if len(cp.ToolResults) != 0 || cp.PendingCount != 1 {
		t.Errorf("the adoption path recorded into another agent's checkpoint when the store did not stop it (%d results, pending %d) — the store predicate is one layer, not the only one", len(cp.ToolResults), cp.PendingCount)
	}
}

type agentBlindLookupStore struct {
	session.Store
}

func (s *agentBlindLookupStore) LookupPendingCall(ctx context.Context, _, corrID string) (*session.PendingCall, error) {
	for _, agentName := range []string{"other-agent", "victim", "test-agent"} {
		if row, err := s.Store.LookupPendingCall(ctx, agentName, corrID); err != nil || row != nil {
			return row, err
		}
	}
	return nil, nil
}
