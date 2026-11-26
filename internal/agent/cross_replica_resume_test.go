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
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func replicaOf(t *testing.T, store session.Store, name string, configure ...func(*Agent)) (*Agent, *mockLLM, *mockBroker) {
	t.Helper()
	mock := newMockLLM()
	brk := newMockBroker()
	agent := New(AgentConfig{
		Name:         "test-agent",
		SystemPrompt: "You are a helpful assistant.",
		Model:        "test-model",
		MaxTokens:    1000,
		MaxLLMCalls:  5,
		Namespace:    "test-ns",
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	svc.featureRegistry = durableResumeRegistry(true)
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init %s: %v", name, err)
	}
	if agent.checkpoints == nil {
		t.Fatalf("%s: expected durable resume to be enabled", name)
	}
	databaseAnswering(agent.checkpoints)
	for _, c := range configure {
		c(agent)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() { _ = agent.Stop(context.Background()) })
	return agent, mock, brk
}

const resumedReplyTopic = "test-ns/reply/cross-replica"

func pausedOnPeerCall(t *testing.T, store session.Store, taskID, subTaskID string) *TaskExecutionState {
	t.Helper()
	state := fullTaskState()
	state.TaskID = taskID
	sessionID := idgen.New()
	state.EffectiveSessionID = sessionID
	state.ContextID = sessionID
	state.OriginalSessionID = sessionID
	state.IsRunBased = false
	state.LoopIteration = 0
	state.MaxCalls = 5
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "delegate this"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
	}
	state.PausedAssistantMsg = state.Messages[1]
	state.Question = state.Messages[0]
	state.SyntheticToolErrors = nil
	state.pauseReplayed.Store(false)
	state.StructuredCtx = nil
	state.UserProps.ReplyTo = resumedReplyTopic
	state.ReplyTo = resumedReplyTopic
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-1": {ToolCallID: "tc-1", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: subTaskID},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1

	if err := store.Create(context.Background(), &session.Session{
		AppName:   "test-agent",
		UserID:    state.UserProps.UserID,
		SessionID: state.EffectiveSessionID,
		ContextID: state.ContextID,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return state
}

func loseOwner(t *testing.T, store session.Store, state *TaskExecutionState, pendingCount int) {
	t.Helper()
	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ctx := context.Background()
	if owner := heldOwner(t, store, state.TaskID); owner != "" {
		if err := store.ReleaseOwnership(ctx, state.TaskID, owner); err != nil {
			t.Fatalf("release: %v", err)
		}
	}
	lapsed := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID:         state.TaskID,
		AppName:        "test-agent",
		AgentName:      "test-agent",
		UserID:         state.UserProps.UserID,
		SessionID:      state.EffectiveSessionID,
		PendingCount:   pendingCount,
		State:          blob,
		LeaseExpiresAt: &lapsed,
	}); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
}

func waitUntil(cond func() bool) bool {
	return waitUntilWithin(5*time.Second, cond)
}

func brieflyBecomes(cond func() bool) bool {
	return waitUntilWithin(250*time.Millisecond, cond)
}

func waitUntilWithin(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func assertResumedToCompletion(t *testing.T, replica *Agent, replicaLLM *mockLLM, brk *mockBroker, taskID, wantToolText string) {
	t.Helper()
	if !waitUntil(func() bool { return len(replicaLLM.getCalls()) > 0 }) {
		t.Fatal("the replica never called the LLM, so it did not actually resume the turn")
	}

	if !waitUntil(func() bool { return replyOnTopic(brk, resumedReplyTopic) != nil }) {
		t.Fatal("the resumed task published no terminal response — nothing reached the caller")
	}
	resp := replyOnTopic(brk, resumedReplyTopic)
	if resp.Error != nil {
		t.Errorf("the resumed task published an error response (%d %s) — it did not complete normally",
			resp.Error.Code, resp.Error.Message)
	}
	if !waitUntil(func() bool { return replica.pausedTasks.Load(taskID) == nil }) {
		t.Error("the resumed task published a response but is still parked in pausedTasks")
	}

	calls := replicaLLM.getCalls()
	for _, req := range calls {
		for _, msg := range req.Messages {
			if msg.Role == "tool" && strings.Contains(msg.Content, wantToolText) {
				return
			}
		}
	}
	t.Errorf("no tool-role message in the resumed request carried %q — the recorded result never reached the LLM context, so the task finished without the answer it was waiting for (messages: %+v)",
		wantToolText, calls[len(calls)-1].Messages)
}

func replyOnTopic(brk *mockBroker, topic string) *a2a.Response {
	for _, msg := range brk.getPublished() {
		if msg.topic != topic {
			continue
		}
		var resp a2a.Response
		if err := json.Unmarshal(msg.payload, &resp); err != nil {
			continue
		}
		return &resp
	}
	return nil
}

func TestTaskPausedOnOneReplicaCompletesOnAnother(t *testing.T) {
	store := session.NewMemoryStore()
	owner, ownerLLM, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")

	const taskID, subTaskID = "task-cross", "sub-cross"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)

	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)

	owner.pausedTasks.Delete(taskID)
	loseOwner(t, store, state, 1)

	survivor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "peer finished the work"))

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "peer finished the work")

	if len(ownerLLM.getCalls()) != 0 {
		t.Errorf("the dead owner ran %d LLM calls; the turn must run on the survivor", len(ownerLLM.getCalls()))
	}
}

func TestSweepDrivesAbandonedTaskToCompletion(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")

	const taskID, subTaskID = "task-sweep", "sub-sweep"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)

	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}

	loseOwner(t, store, state, 1)
	owner.checkpoints.recordResult(context.Background(), taskID,
		state.PendingCalls["tc-1"], tool.OK("peer finished the work"))

	survivor.resumeAbandonedCheckpoints(context.Background(), time.Now())

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "peer finished the work")
}

func TestStartRunsCheckpointMaintenance(t *testing.T) {
	store := session.NewMemoryStore()
	brk := newMockBroker()
	mock := newMockLLM()

	agent := New(AgentConfig{
		Name:        "test-agent",
		Namespace:   "test-ns",
		Model:       "test-model",
		MaxLLMCalls: 5,
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	svc.featureRegistry = durableResumeRegistry(true)
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	agent.checkpoints.interval = 10 * time.Millisecond
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test teardown

	const taskID, subTaskID = "task-renew", "sub-renew"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	agent.pausedTasks.Store(state)

	lapsed := time.Now().Add(-time.Hour)
	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.SaveCheckpoint(context.Background(), &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", AgentName: "test-agent",
		UserID: state.UserProps.UserID, SessionID: state.EffectiveSessionID,
		PendingCount: 0, State: blob,
		OwnerID: agent.checkpoints.ownerID, LeaseExpiresAt: &lapsed,
	}); err != nil {
		t.Fatalf("backdate lease: %v", err)
	}

	renewed := waitUntil(func() bool {
		lapsedRows, probeErr := store.ListResumableCheckpoints(context.Background(), "test-agent", time.Now(), testTakeoverWindow, 10, false)
		if probeErr != nil {
			return false
		}
		for _, row := range lapsedRows {
			if row.TaskID == taskID {
				return false
			}
		}
		return true
	})
	if !renewed {
		t.Error("the maintenance loop never renewed the lease — Start did not launch it, and an unrenewed lease makes every long pause adoptable, which is worse than running without the feature")
	}
}

func TestShutdownPastDeadlineLeavesTaskToLeaseExpiry(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	taskID, state := pausedAndCheckpointed(t, store, owner)

	expired, cancel := context.WithCancel(t.Context())
	cancel()
	owner.flushPausedTasksOnShutdown(expired)

	assertLeftToLeaseExpiry(t, store, owner, taskID, state)
}

func pausedAndCheckpointed(t *testing.T, store session.Store, owner *Agent) (string, *TaskExecutionState) {
	t.Helper()
	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(t.Context(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)
	return taskID, state
}

func assertLeftToLeaseExpiry(t *testing.T, store session.Store, owner *Agent, taskID string, state *TaskExecutionState) {
	t.Helper()
	sess, err := store.Get(t.Context(), "test-agent", state.UserProps.UserID, state.EffectiveSessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	for _, msg := range sess.Messages {
		if msg.Role == "assistant" && msg.Content == cancellationMarkerContent {
			t.Fatal("a handoff out of time marked the turn cancelled, contradicting the successor that resumes it")
		}
	}
	ownerID, exists, err := store.CheckpointOwner(t.Context(), taskID)
	if err != nil {
		t.Fatalf("read owner: %v", err)
	}
	if !exists || ownerID != owner.checkpoints.ownerID {
		t.Errorf("checkpoint owner = %q (exists=%v), want the row left to %q until its lease lapses", ownerID, exists, owner.checkpoints.ownerID)
	}
}

func TestShutdownHandsOffWithoutLosingRecordedResults(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")

	const taskID, subTaskID = "task-handoff", "sub-handoff"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)

	owner.checkpoints.recordResult(context.Background(), taskID,
		state.PendingCalls["tc-1"], tool.OK("peer finished the work"))

	owner.flushPausedTasksOnShutdown(t.Context())
	if owner.pausedTasks.Load(taskID) != nil {
		t.Error("a released task is still held in the paused set, so the renewal tick reports it lost")
	}

	cp, err := store.AdoptCheckpoint(context.Background(), taskID, "successor", time.Now(), time.Minute, testTakeoverWindow, false)
	if err != nil {
		t.Fatalf("adopt after shutdown: %v", err)
	}
	if cp == nil {
		t.Fatal("a handed-off task was not adoptable by a successor")
	}
	if cp.PendingCount != 0 {
		t.Errorf("PendingCount = %d after handoff, want 0 — the shutdown overwrote it and the task can never resume", cp.PendingCount)
	}
	found := false
	for _, r := range cp.ToolResults {
		if strings.Contains(r.Content, "peer finished the work") {
			found = true
		}
	}
	if !found {
		t.Errorf("the recorded result did not survive the shutdown handoff (%d results present) — the successor resumes without the answer", len(cp.ToolResults))
	}
}

func TestStartRunsTheAbandonedCheckpointSweep(t *testing.T) {
	store := session.NewMemoryStore()
	brk := newMockBroker()
	mock := newMockLLM()

	agent := New(AgentConfig{
		Name:        "test-agent",
		Namespace:   "test-ns",
		Model:       "test-model",
		MaxLLMCalls: 5,
	})
	svc := newMockServices(mock, store)
	svc.brk = brk
	svc.featureRegistry = durableResumeRegistry(true)
	agent.svc = svc
	if err := agent.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("init: %v", err)
	}
	agent.checkpoints.interval = 10 * time.Millisecond
	agent.checkpoints.takeoverAfter = 30 * time.Millisecond

	const taskID, subTaskID = "task-swept-by-loop", "sub-swept"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	loseOwner(t, store, state, 1)
	agent.checkpoints.recordResult(context.Background(), taskID,
		state.PendingCalls["tc-1"], tool.OK("peer finished the work"))

	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer agent.Stop(context.Background()) //nolint:errcheck // test teardown

	if !waitUntil(func() bool { return len(mock.getCalls()) > 0 }) {
		t.Fatal("the maintenance loop never swept up an abandoned checkpoint — a task whose last reply was already consumed would never finish")
	}
}

func TestResumeCompletionDoesNotDeleteAnotherReplicasCheckpoint(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	successorID := "successor-replica"

	const taskID, subTaskID = "task-terminal", "sub-terminal"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	moved, err := takeOver(t, store, taskID, successorID)
	if err != nil {
		t.Fatalf("successor adopt: %v", err)
	}
	if moved == nil {
		t.Fatal("setup: the successor did not take the checkpoint, so this proves nothing")
	}

	owner.pausedTasks.Store(state)
	owner.resumeTaskExecution(state)

	owner2, exists, ownerErr := store.CheckpointOwner(context.Background(), taskID)
	if ownerErr != nil {
		t.Fatalf("owner: %v", ownerErr)
	}
	if !exists {
		t.Fatal("the terminal path deleted a checkpoint owned by another replica — its successor loses the task")
	}
	if owner2 != successorID {
		t.Errorf("owner = %q, want the successor still holding it", owner2)
	}
}

func TestFencedReplicaPublishesNoTerminalResponse(t *testing.T) {
	store := session.NewMemoryStore()
	replica, replicaLLM, brk := replicaOf(t, store, "fenced")

	const taskID, subTaskID = "task-fenced-publish", "sub-fenced-publish"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	state.Fenced.Store(true)
	replica.pausedTasks.Store(state)

	replica.resumeTaskExecution(state)

	if resp := replyOnTopic(brk, resumedReplyTopic); resp != nil {
		t.Errorf("a fenced replica published a terminal response (error=%v) — the owner's response is now a duplicate", resp.Error)
	}
	if len(replicaLLM.getCalls()) == 0 {
		t.Fatal("the resumed turn never ran, so this proves nothing about suppression")
	}
}

func TestCancellationMarkerSuppressedOnlyWhenFenced(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name       string
		fenced     bool
		wantMarker bool
	}{
		{"fenced writes nothing", true, false},
		{"unfenced records the interruption", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			replica, _, _ := replicaOf(t, store, "replica")

			state := pausedOnPeerCall(t, store, "task-marker-"+tc.name, "sub-marker")
			state.Messages = append(state.Messages, llm.ChatMessage{Role: "user", Content: "pending work"})
			state.HistoryOffset = 0
			state.Fenced.Store(tc.fenced)

			sess, err := store.Get(context.Background(), "test-agent",
				state.UserProps.UserID, state.EffectiveSessionID)
			if err != nil {
				t.Fatalf("get session: %v", err)
			}
			rc := &taskRunContext{sess: sess, logger: replica.logger}

			_ = replica.flushSessionMessages(cancelled, state, rc)

			var sawMarker bool
			for _, msg := range state.Messages {
				if msg.Role == "assistant" && msg.Content == cancellationMarkerContent {
					sawMarker = true
				}
			}
			if sawMarker != tc.wantMarker {
				if tc.wantMarker {
					t.Error("an unfenced interrupted task recorded no cancellation marker — the next turn's LLM will treat the abandoned work as current")
				} else {
					t.Error("a fenced replica recorded a cancellation marker into the session its successor is completing")
				}
			}
		})
	}
}

func TestCrossReplicaCancelPublishesATerminalResponse(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	other, _, otherBrk := replicaOf(t, store, "other")

	const taskID, subTaskID = "task-cancel-answer", "sub-cancel-answer"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)

	other.handleCancelRequest(&a2a.Request{
		ID:     "req-cancel",
		Params: map[string]any{"id": taskID},
	})

	resp := replyOnTopic(otherBrk, resumedReplyTopic)
	if resp == nil {
		t.Fatal("a cancel on a non-owner published nothing — the caller sees a timeout instead of a cancellation")
	}
	if resp.Error != nil {
		t.Errorf("the cancel published an error response (%d %s), not a cancellation",
			resp.Error.Code, resp.Error.Message)
	}
}

func TestCancelOnAnotherReplicaPreventsLaterResume(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	other, otherLLM, _ := replicaOf(t, store, "other")

	const taskID, subTaskID = "task-cancelled", "sub-cancelled"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)

	owner.pausedTasks.Delete(taskID)
	loseOwner(t, store, state, 1)

	other.handleCancelRequest(&a2a.Request{
		ID:     "req-cancel",
		Params: map[string]any{"id": taskID},
	})

	other.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "peer finished anyway"))

	if brieflyBecomes(func() bool { return len(otherLLM.getCalls()) > 0 }) {
		t.Error("a cancelled task was resumed and would report completed — the cancel did not remove its durable record")
	}
	if other.pausedTasks.Load(taskID) != nil {
		t.Error("a cancelled task was adopted after cancellation")
	}
}

func TestBystanderDoesNotStealFromLiveOwner(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	bystander, bystanderLLM, _ := replicaOf(t, store, "bystander")

	const taskID, subTaskID = "task-live", "sub-live"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)

	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)

	bystander.resumeOrphanPeerResponse(subTaskID, peerReplyMessage(t, "peer finished"))
	bystander.resumeAbandonedCheckpoints(context.Background(), time.Now())

	if len(bystanderLLM.getCalls()) != 0 {
		t.Errorf("a bystander ran %d LLM calls for a task owned by a live replica — the turn is running twice", len(bystanderLLM.getCalls()))
	}
	if bystander.pausedTasks.Load(taskID) != nil {
		t.Error("a bystander took over a task whose owner still holds the lease")
	}
}

func TestAdoptedTaskStandsDownWhenItsRowDisappearsMidTurn(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")

	const taskID, subTaskID = "task-adopted-deleted", "sub-adopted-deleted"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	loseOwner(t, store, state, 1)
	owner.checkpoints.recordResult(context.Background(), taskID,
		state.PendingCalls["tc-1"], tool.OK("peer finished the work"))

	var adopted atomic.Pointer[TaskExecutionState]
	survivorLLM.preCallHook = func(int) {
		adopted.Store(survivor.pausedTasks.Load(taskID))
		if err := store.DeleteCheckpoint(context.Background(), taskID); err != nil {
			t.Errorf("delete mid-turn: %v", err)
		}
		survivor.renewOwnedLeases(context.Background(), time.Now())
	}

	survivor.resumeAbandonedCheckpoints(context.Background(), time.Now())

	if !waitUntil(func() bool { return len(survivorLLM.getCalls()) > 0 }) {
		t.Fatal("the survivor never adopted and resumed the task, so this proves nothing")
	}
	if !waitUntil(func() bool { return survivor.pausedTasks.Load(taskID) == nil }) {
		t.Fatal("the adopted task is still parked; the turn never finished")
	}
	fenced := adopted.Load()
	if fenced == nil {
		t.Fatal("the adopted task was not parked while its own turn ran")
	}
	if !fenced.Fenced.Load() {
		t.Error("an adopted task whose checkpoint vanished was not fenced — it read the missing row as \"no durable backup\" and kept going")
	}
	if brieflyBecomes(func() bool { return replyOnTopic(survivorBrk, resumedReplyTopic) != nil }) {
		t.Error("the survivor published a terminal response for a task whose checkpoint was already deleted elsewhere — the caller sees two")
	}
}

func TestStopReleasesPausedCheckpointOwnership(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "draining")

	const taskID, subTaskID = "task-drain", "sub-drain"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	agent.pausedTasks.Store(state)

	owner, exists, err := store.CheckpointOwner(context.Background(), taskID)
	if err != nil || !exists || owner != agent.checkpoints.ownerID {
		t.Fatalf("precondition: owner=%q exists=%v err=%v, want this replica to own the checkpoint", owner, exists, err)
	}

	if stopErr := agent.Stop(context.Background()); stopErr != nil {
		t.Fatalf("stop: %v", stopErr)
	}

	owner, exists, err = store.CheckpointOwner(context.Background(), taskID)
	if err != nil {
		t.Fatalf("owner after stop: %v", err)
	}
	if !exists {
		t.Fatal("the shutdown deleted the checkpoint instead of handing it off — the task is gone")
	}
	if owner != "" {
		t.Errorf("owner = %q after Stop, want released — a successor now waits out a lease this process will never renew", owner)
	}
}

type aliasResolver struct {
	alias    string
	resolved map[string]any
	calls    atomic.Int64
}

func (r *aliasResolver) Resolve(_ context.Context, alias string) (map[string]any, error) {
	if alias != r.alias {
		return nil, fmt.Errorf("unknown alias %q", alias)
	}
	r.calls.Add(1)
	return r.resolved, nil
}

func TestAdoptedTaskReResolvesItsModelOverride(t *testing.T) {
	store := session.NewMemoryStore()
	resolver := &aliasResolver{
		alias:    "fast-model",
		resolved: map[string]any{"model": "anthropic/claude-haiku-4-5", "api_key": "sk-not-persisted"},
	}

	agent, _, _ := replicaOf(t, store, "survivor")
	agent.svc.(*mockServices).modelResolver = resolver

	const taskID, subTaskID = "task-model-alias", "sub-model-alias"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	state.ModelOverrideAlias = resolver.alias
	state.ModelOverride = &llm.BifrostConfig{Model: "anthropic/claude-haiku-4-5"}

	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("save: %v", err)
	}
	loseOwner(t, store, state, 1)
	agent.checkpoints.recordResult(context.Background(), taskID,
		state.PendingCalls["tc-1"], tool.OK("peer finished the work"))

	agent.resumeAbandonedCheckpoints(context.Background(), time.Now())

	if !waitUntil(func() bool { return resolver.calls.Load() > 0 }) {
		t.Fatal("the adopted task never re-resolved its model alias — it is finishing on the agent default, silently switching models mid-task")
	}
}

func TestMaintenanceTickRetriesFailedDiscards(t *testing.T) {
	inner := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, inner, "owner", func(a *Agent) {
		a.checkpoints.store = &failOnceStore{Store: inner}
		a.checkpoints.interval = 10 * time.Millisecond
	})

	state := fullTaskState()
	state.TaskID = "task-tick-retry"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("save: %v", err)
	}

	agent.checkpoints.discardIfOwned(context.Background(), state.TaskID)
	if got, err := inner.GetCheckpoint(context.Background(), state.TaskID); err != nil || got == nil {
		t.Fatalf("the delete did not fail, so this test proves nothing: %v, %v", got, err)
	}

	cleared := waitUntil(func() bool {
		got, err := inner.GetCheckpoint(context.Background(), state.TaskID)
		return err == nil && got == nil
	})
	if !cleared {
		t.Error("the maintenance loop never retried the failed delete — a finished task stays resumable for the life of the process and answers twice")
	}
}

func TestOwnerStandsDownWhenItsRowWasTakenMidTurn(t *testing.T) {
	store := session.NewMemoryStore()
	owner, ownerLLM, ownerBrk := replicaOf(t, store, "owner")

	const taskID, subTaskID = "task-taken-midturn", "sub-taken-midturn"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("save: %v", err)
	}
	owner.pausedTasks.Store(state)
	owner.checkpoints.recordResult(context.Background(), taskID,
		state.PendingCalls["tc-1"], tool.OK("peer finished the work"))

	ownerLLM.preCallHook = func(int) {
		if _, err := store.PopCheckpoint(context.Background(), taskID); err != nil {
			t.Errorf("cancel take: %v", err)
		}
	}

	owner.resumeTaskExecution(state)

	if len(ownerLLM.getCalls()) == 0 {
		t.Fatal("the owner never ran the turn, so this proves nothing")
	}
	if brieflyBecomes(func() bool { return replyOnTopic(ownerBrk, resumedReplyTopic) != nil }) {
		t.Error("the owner published a terminal response for a task whose row had already been taken — the canceller published too, so the caller sees two")
	}
	if !state.Fenced.Load() {
		t.Error("the owner did not fence itself, so its session flush will also contradict the canceller")
	}
}

type slowRecordStore struct {
	session.Store
	delay   time.Duration
	started chan struct{}
	once    sync.Once
}

func (s *slowRecordStore) RecordToolResult(ctx context.Context, taskID, corrID string, result *session.ToolCallResult) (int, error) {
	s.once.Do(func() { close(s.started) })
	time.Sleep(s.delay)
	return s.Store.RecordToolResult(ctx, taskID, corrID, result)
}

func TestResumeWaitsForThisTurnsDurableWrites(t *testing.T) {
	inner := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, inner, "owner", func(a *Agent) {
		a.checkpoints.store = &slowRecordStore{
			Store:   inner,
			delay:   150 * time.Millisecond,
			started: make(chan struct{}),
		}
	})
	slow := agent.checkpoints.store.(*slowRecordStore)

	const taskID, subTaskID = "task-lagging-write", "sub-lagging-write"
	state := pausedOnPeerCall(t, inner, taskID, subTaskID)
	if err := agent.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("save: %v", err)
	}
	agent.pausedTasks.Store(state)

	state.mu.Lock()
	state.durableWrites.Add(1)
	state.mu.Unlock()

	recorded := make(chan struct{})
	go func() {
		agent.checkpoints.recordResult(context.Background(), taskID,
			state.PendingCalls["tc-1"], tool.OK("peer finished the work"))
		close(recorded)
		state.durableWrites.Done()
	}()

	<-slow.started

	resumed := make(chan struct{})
	go func() {
		agent.resumeTaskExecution(state)
		close(resumed)
	}()

	select {
	case <-resumed:
	case <-time.After(5 * time.Second):
		t.Fatal("the resume never proceeded after its barrier cleared — the barrier does not release")
	}
	select {
	case <-recorded:
	default:
		t.Error("the resumed turn ran while a durable write from the previous turn was still in flight — that write lands on the row this turn re-saves and drives pending_count to zero with a call outstanding")
	}
}

type ownerErrStore struct {
	session.Store
}

func (s *ownerErrStore) CheckpointOwner(context.Context, string) (string, bool, error) {
	return "", false, errors.New("transient failure")
}

func TestOwnershipReadFailureKeepsTheTaskLocal(t *testing.T) {
	inner := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, inner, "owner", func(a *Agent) {
		a.checkpoints.store = &ownerErrStore{Store: inner}
	})

	state := fullTaskState()
	state.TaskID = "task-store-blip"
	state.PendingCalls = map[string]*PendingToolCall{}
	state.TotalPending = 0
	agent.pausedTasks.Store(state)
	state.checkpointed.Store(true)

	agent.dropFencedTasks(context.Background(), []string{state.TaskID})

	if agent.pausedTasks.Load(state.TaskID) == nil {
		t.Error("a store blip on the ownership read dropped a live task — nobody else has taken it, so the caller gets nothing")
	}
	if state.Fenced.Load() {
		t.Error("a store blip fenced a live task, suppressing the publish and the session write of work that is still running here")
	}
}

func realSQLStore(t *testing.T) session.Store {
	t.Helper()
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_SESSION_DB_URL"),
		"agent_resume",
		func(u string) error {
			db, openErr := dbutil.Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return session.NewSQLStore(db).Migrate(context.Background())
		},
	)
	if err != nil {
		t.Fatalf("dbtest template: %v", err)
	}
	t.Cleanup(tmpl.Close)

	db, err := dbutil.Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return session.NewSQLStore(db)
}

func TestCrossReplicaResumeAgainstRealDatabase(t *testing.T) {
	store := realSQLStore(t)
	owner, ownerLLM, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")

	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)

	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	owner.pausedTasks.Store(state)

	owner.pausedTasks.Delete(taskID)
	loseOwner(t, store, state, 1)

	survivor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "peer finished the work"))

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "peer finished the work")

	if len(ownerLLM.getCalls()) != 0 {
		t.Errorf("the dead owner ran %d LLM calls; the turn must run on the survivor", len(ownerLLM.getCalls()))
	}

	cp, err := store.GetCheckpoint(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if cp != nil {
		t.Error("the checkpoint outlived the completed task — a later sweep could resume finished work")
	}
}

func TestOnlyOneReplicaAdoptsAgainstRealDatabase(t *testing.T) {
	store := realSQLStore(t)
	ctx := context.Background()

	taskID, subTaskID := idgen.New(), idgen.New()
	seed, _, _ := replicaOf(t, store, "seed")
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := seed.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	loseOwner(t, store, state, 0)

	const racers = 12
	var wg sync.WaitGroup
	won := make([]bool, racers)
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cp, err := store.AdoptCheckpoint(ctx, taskID, "replica-"+strconv.Itoa(i),
				adoptionCutoff(time.Now()), checkpointLease, testTakeoverWindow, false)
			if err != nil {
				t.Errorf("racer %d: %v", i, err)
				return
			}
			won[i] = cp != nil
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, w := range won {
		if w {
			winners++
		}
	}
	if winners != 1 {
		t.Errorf("%d replicas adopted the same checkpoint, want exactly 1 — each one runs the turn and answers the caller", winners)
	}
}

func TestSeedAbandonedCheckpointForLiveProcess(t *testing.T) {
	dbURL := os.Getenv("SAM_LIVE_SEED_DB")
	if dbURL == "" {
		t.Skip("set SAM_LIVE_SEED_DB to seed a checkpoint for the live two-process rig")
	}
	agentName := os.Getenv("SAM_LIVE_SEED_AGENT")
	if agentName == "" {
		agentName = "test-agent"
	}

	db, err := dbutil.Open(dbURL, slog.Default())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	store := session.NewSQLStore(db)
	if migErr := store.Migrate(context.Background()); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	ctx := context.Background()
	taskID, subTaskID := idgen.New(), idgen.New()
	sessionID := idgen.New()

	state := fullTaskState()
	state.TaskID = taskID
	state.EffectiveSessionID = sessionID
	state.ContextID = sessionID
	state.OriginalSessionID = sessionID
	state.IsRunBased = false
	state.StructuredCtx = nil
	state.LoopIteration = 0
	state.MaxCalls = 5
	state.Messages = []llm.ChatMessage{
		{Role: "user", Content: "delegate this to the peer"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-live", Name: "peer_agent"}}},
	}
	state.PausedAssistantMsg = state.Messages[1]
	state.SyntheticToolErrors = nil
	state.pauseReplayed.Store(false)
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-live": {ToolCallID: "tc-live", ToolName: "peer_agent", Kind: PendingCallPeer, Index: 0, SubTaskID: subTaskID},
	}
	state.SyncResults = map[int]*tool.Result{}
	state.TotalPending = 1
	if replyTo := os.Getenv("SAM_LIVE_SEED_REPLY"); replyTo != "" {
		state.UserProps.ReplyTo = replyTo
		state.ReplyTo = replyTo
	}

	if seedErr := store.Create(ctx, &session.Session{
		AppName: agentName, UserID: state.UserProps.UserID,
		SessionID: sessionID, ContextID: sessionID,
	}); seedErr != nil {
		t.Fatalf("seed session: %v", seedErr)
	}

	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	lapsed := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: agentName, AgentName: agentName,
		UserID: state.UserProps.UserID, SessionID: sessionID,
		PendingCount: 0, State: blob,
		OwnerID: "replica-that-was-killed", LeaseExpiresAt: &lapsed,
		TraceID: state.UserProps.TraceID,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	if _, err := store.RecordToolResult(ctx, taskID, "", &session.ToolCallResult{
		ToolCallID: "tc-live", ToolName: "peer_agent",
		Content: "the peer finished the work before the owner died", IsError: false,
	}); err != nil {
		t.Fatalf("record result: %v", err)
	}

	t.Logf("SEEDED_TASK_ID=%s", taskID)
	t.Logf("SEEDED_AGENT=%s", agentName)
}
