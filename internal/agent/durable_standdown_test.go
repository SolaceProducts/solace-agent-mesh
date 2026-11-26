// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestStopMidTurnLeavesTheTaskAsACrashDoes(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, brk := replicaOf(t, store, "leaving")
	ctx := context.Background()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)

	stopped, cancelRun := context.WithCancel(context.Background())
	agent.runCtx = stopped
	cancelRun()

	agent.resumeTaskExecution(state)

	cp, err := store.GetCheckpoint(ctx, taskID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cp == nil {
		t.Fatal("a graceful shutdown destroyed a mid-turn task's checkpoint — the publish fails on the dead context but the detached delete succeeds, so nobody answers the caller and no successor can pick it up. kill -9 recovers this task; SIGTERM loses it")
	}
	if cp.Final != nil {
		t.Error("the stop marked the cut turn answering; a successor would publish an answer to a turn that never finished")
	}
	if replyOnTopic(brk, resumedReplyTopic) != nil {
		t.Error("the stop published a response for a turn it cut short")
	}
}

func TestTurnStoppedByProcessExitBeforeItRanReleasesItsRow(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, brk := replicaOf(t, store, "exiting")
	ctx := t.Context()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)

	agent.exitingProcess.Store(true)
	stopped, cancelRun := context.WithCancel(ctx)
	agent.runCtx = stopped
	cancelRun()

	agent.resumeTaskExecution(state)

	assertReleasedForTakeover(t, store, agent, brk, taskID)
}

func TestTurnCutByProcessExitReleasesItsRow(t *testing.T) {
	store := session.NewMemoryStore()
	agent, model, brk := replicaOf(t, store, "exiting")
	ctx := t.Context()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)

	running, cutRun := context.WithCancel(ctx)
	agent.runCtx = running
	model.preCallHook = func(int) {
		agent.exitingProcess.Store(true)
		cutRun()
	}

	agent.resumeTaskExecution(state)

	if len(model.getCalls()) == 0 {
		t.Fatal("the turn never reached the model, so the cut was not mid-turn")
	}
	assertReleasedForTakeover(t, store, agent, brk, taskID)
}

func assertReleasedForTakeover(t *testing.T, store *session.MemoryStore, agent *Agent, brk *mockBroker, taskID string) {
	t.Helper()
	ctx := t.Context()
	owner, exists, err := store.CheckpointOwner(ctx, taskID)
	if err != nil {
		t.Fatalf("read owner: %v", err)
	}
	if !exists {
		t.Fatal("the exit deleted the cut turn's row")
	}
	if owner != "" {
		t.Errorf("row still owned by %q; the next process waits for this owner's silence", owner)
	}
	if agent.pausedTasks.Load(taskID) != nil {
		t.Error("the released task is still held here, so the renewal tick keeps counting it")
	}
	cp, err := store.GetCheckpoint(ctx, taskID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cp == nil || cp.Final != nil {
		t.Error("the released row lost its state or was marked answering")
	}
	if replyOnTopic(brk, resumedReplyTopic) != nil {
		t.Error("the exit published a response for a turn it cut short")
	}
}

type cancelAwareStore struct{ *session.MemoryStore }

func (cancelAwareStore) OutlivesProcess() bool { return true }

func (s cancelAwareStore) Get(ctx context.Context, appName, userID, sessionID string) (*session.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.MemoryStore.Get(ctx, appName, userID, sessionID)
}

func TestResumedTurnStartingAfterTheStopLeavesTheTaskAsACrashDoes(t *testing.T) {
	mem := session.NewMemoryStore()
	agent, _, brk := replicaOf(t, cancelAwareStore{mem}, "late-resume")
	ctx := context.Background()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, mem, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)

	stopped, cancelRun := context.WithCancel(context.Background())
	agent.runCtx = stopped
	cancelRun()

	agent.resumeTaskExecution(state)

	cp, err := mem.GetCheckpoint(ctx, taskID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cp == nil || cp.Final != nil {
		t.Errorf("checkpoint = %+v, want it left unanswered for the process that takes the task over", cp)
	}
	if replyOnTopic(brk, resumedReplyTopic) != nil {
		t.Error("the stop published a response for a turn that never started")
	}
}

func TestAPanickingResumedTurnRemovesItsCheckpoint(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "panicker")
	ctx := context.Background()

	taskID, sessionID := idgen.New(), idgen.New()
	soon := time.Now().Add(time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", AgentName: "test-agent",
		UserID: "u", SessionID: sessionID, PendingCount: 0,
		State:   []byte(`{"version":1}`),
		OwnerID: agent.checkpoints.ownerID, LeaseExpiresAt: &soon,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	state := fullTaskState()
	state.TaskID = taskID
	state.EffectiveSessionID = sessionID
	state.ContextID = sessionID
	state.ReplyTo = ""
	state.UserProps.ReplyTo = ""
	state.checkpointed.Store(true)
	agent.pausedTasks.Store(state)
	claimStateTask(agent, state)

	func() {
		defer agent.recoverResumePanic(state)
		panic("synthetic panic in a resumed turn")
	}()

	assertClaimReleased(t, store, agent, state, "a panicking resumed turn")

	if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
		t.Error("the panicking turn published its error and left the checkpoint behind — lease renewal stops with the paused entry, so the row ripens and a replica re-runs the turn, republishing on every lease period for as long as the panic reproduces")
	}
	if agent.pausedTasks.Load(taskID) != nil {
		t.Error("the panicking task is still parked in pausedTasks")
	}
}

func TestAResumeThatCannotLoadItsSessionEndsTheTaskOnce(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, brk := replicaOf(t, store, "sessionless")
	ctx := context.Background()

	taskID, sessionID := idgen.New(), idgen.New()
	soon := time.Now().Add(time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", AgentName: "test-agent",
		UserID: "u", SessionID: sessionID, PendingCount: 0,
		State:   []byte(`{"version":1}`),
		OwnerID: agent.checkpoints.ownerID, LeaseExpiresAt: &soon,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	state := fullTaskState()
	state.TaskID = taskID
	state.EffectiveSessionID = sessionID
	state.ContextID = sessionID
	state.OriginalSessionID = sessionID
	state.IsRunBased = false
	state.StructuredCtx = nil
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "go"}}
	state.PausedAssistantMsg = llm.ChatMessage{}
	state.PendingCalls = map[string]*PendingToolCall{}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 0
	state.checkpointed.Store(true)
	state.ReplyTo = resumedReplyTopic
	state.UserProps.ReplyTo = resumedReplyTopic
	agent.pausedTasks.Store(state)
	claimStateTask(agent, state)

	agent.resumeTaskExecution(state)

	if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
		t.Error("the unresumable task kept its checkpoint — every replica that adopts it re-enters this same failure, one ERROR per lease period, and the caller is never answered")
	}
	if replyOnTopic(brk, resumedReplyTopic) == nil {
		t.Error("no terminal response was published for a task that can never resume, so the caller waits out the gateway's hard timeout instead of being told")
	}
	assertClaimReleased(t, store, agent, state, "abandoning an unresumable task")
}

func TestAnUnresumableTaskTakenOverElsewhereStillReleasesItsClaim(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, brk := replicaOf(t, store, "sessionless")
	ctx := context.Background()

	taskID, sessionID := idgen.New(), idgen.New()
	soon := time.Now().Add(time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", AgentName: "test-agent",
		UserID: "u", SessionID: sessionID, PendingCount: 0,
		State:   []byte(`{"version":1}`),
		OwnerID: "successor-replica", LeaseExpiresAt: &soon,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	state := fullTaskState()
	state.TaskID = taskID
	state.EffectiveSessionID = sessionID
	state.ContextID = sessionID
	state.OriginalSessionID = sessionID
	state.StructuredCtx = nil
	state.checkpointed.Store(true)
	state.ReplyTo = resumedReplyTopic
	state.UserProps.ReplyTo = resumedReplyTopic
	agent.pausedTasks.Store(state)
	claimStateTask(agent, state)

	agent.resumeTaskExecution(state)

	if replyOnTopic(brk, resumedReplyTopic) != nil {
		t.Error("a replica that lost the task published its own failure; the new owner answers the caller")
	}
	assertClaimReleased(t, store, agent, state, "standing down from a task taken over while it failed to resume")
}

func TestAnsweredTaskIsNotResumedWhileTheProcessExits(t *testing.T) {
	store := session.NewMemoryStore()
	agent, model, brk := replicaOf(t, store, "exiting")
	ctx := context.Background()

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	state.SyncResults[0] = tool.OK("the peer answered")
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)
	agent.exitingProcess.Store(true)

	agent.ResumeTask(taskID)

	if waitUntilWithin(time.Second, func() bool {
		return len(model.getCalls()) > 0 || replyOnTopic(brk, resumedReplyTopic) != nil
	}) {
		t.Error("the turn ran although the exit would cut it")
	}
	if cp, err := store.GetCheckpoint(ctx, taskID); err != nil || cp == nil {
		t.Errorf("checkpoint = %v (err %v), want it kept for the takeover", cp, err)
	}
}
