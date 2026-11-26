// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestCancelLeavesACheckpointItCannotDecode(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "canceller")

	taskID, sessionID := idgen.New(), idgen.New()
	ctx := context.Background()
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", UserID: "u", SessionID: sessionID,
		AgentName: "test-agent", PendingCount: 1,
		State:   []byte(`{"version":9999,"somethingNew":{}}`),
		OwnerID: "the-owner",
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	agent.cancelCheckpointedTask(taskID)

	cp, err := store.GetCheckpoint(ctx, taskID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if cp == nil {
		t.Fatal("a cancel destroyed a checkpoint it could not decode — the owner fences on the missing row and publishes nothing, so the caller gets no terminal event and waits out the gateway timeout")
	}
}

func TestDiscardIfOwnedReportsATakeoverByAnotherReplica(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "loser")

	taskID, sessionID := idgen.New(), idgen.New()
	ctx := context.Background()
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", UserID: "u", SessionID: sessionID,
		AgentName: "test-agent", State: []byte(`{}`), OwnerID: "some-other-replica",
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	if !agent.checkpoints.discardIfOwned(ctx, taskID) {
		t.Error("a checkpoint owned by another replica did not report a takeover — this replica goes on to publish a terminal response for a task its successor is running, so the caller gets two answers")
	}
	if cp, _ := store.GetCheckpoint(ctx, taskID); cp == nil {
		t.Error("the row another replica owns was deleted — its new owner loses the task it is working on")
	}
}

func TestReaperGivingUpRemovesTheWholeTaskNotJustTheCorrelation(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "reaper")

	taskID, sessionID := idgen.New(), idgen.New()
	ctx := context.Background()
	lapsed := time.Now().Add(-time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", UserID: "u", SessionID: sessionID,
		AgentName: "test-agent", PendingCount: 1, State: []byte(`{}`), OwnerID: "dead-replica",
		LeaseExpiresAt: &lapsed,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []session.PendingCall{{
		CorrID: "corr-old", TaskID: taskID, AgentName: "test-agent",
		ToolCallID: "", Kind: session.PendingKindPeer, ExpiresAt: &past,
	}}); err != nil {
		t.Fatalf("register pending call: %v", err)
	}

	agent.reapExpiredPendingCalls(ctx, time.Now())

	if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
		t.Errorf("the checkpoint outlived the correlation the reaper gave up on (pending=%d) — no reaper tick and no sweep can ever see it again, so the row is immortal", cp.PendingCount)
	}
	if row, _ := store.LookupPendingCall(ctx, "test-agent", "corr-old"); row != nil {
		t.Error("the correlation row survived, so every tick from now on logs the same give-up for a task that is already gone")
	}
}

func TestOversizedSaveRemovesAnEarlierRowItDidNotFlagAsSaved(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "grower")

	taskID, sessionID := idgen.New(), idgen.New()
	ctx := context.Background()
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", UserID: "u", SessionID: sessionID,
		AgentName: "test-agent", State: []byte(`{"stale":true}`),
		OwnerID: agent.checkpoints.ownerID,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	state := fullTaskState()
	state.TaskID = taskID
	state.EffectiveSessionID = sessionID
	state.checkpointed.Store(false)
	state.Question = oversizedMessage()

	if err := agent.checkpoints.save(ctx, state); err == nil {
		t.Fatal("an oversized save reported success")
	}
	if cp, _ := store.GetCheckpoint(ctx, taskID); cp != nil {
		t.Error("the stale row survived an oversized save — another replica adopts state several turns old and answers the user from it, which is the opposite of what the error says happened")
	}
}

func TestReaperCannotTimeOutACallAlreadyAnswered(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "racer")

	taskID, sessionID := idgen.New(), idgen.New()
	ctx := context.Background()
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", UserID: "u", SessionID: sessionID,
		AgentName: "test-agent", PendingCount: 1, State: []byte(`{}`), OwnerID: "owner",
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []session.PendingCall{{
		CorrID: "corr-answered", TaskID: taskID, AgentName: "test-agent",
		ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer, ExpiresAt: &past,
	}}); err != nil {
		t.Fatalf("register pending call: %v", err)
	}

	if _, err := store.RecordToolResult(ctx, taskID, "corr-answered", &session.ToolCallResult{
		ToolCallID: "tc-1", ToolName: "peer_agent", Content: "the real answer",
	}); err != nil {
		t.Fatalf("record the real answer: %v", err)
	}

	stillExpired, err := store.ListExpiredPendingCalls(ctx, "test-agent", time.Now(), 10)
	if err != nil {
		t.Fatalf("list expired: %v", err)
	}
	if len(stillExpired) != 0 {
		t.Errorf("the correlation row for an answered call is still reapable (%d rows) — the reaper will synthesise a timeout over a real answer", len(stillExpired))
	}

	agent.reapExpiredPendingCalls(ctx, time.Now())

	cp, err := store.GetCheckpoint(ctx, taskID)
	if err != nil || cp == nil {
		t.Fatalf("read back: %v, %v", cp, err)
	}
	if len(cp.ToolResults) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(cp.ToolResults), cp.ToolResults)
	}
	if cp.ToolResults[0].Content != "the real answer" {
		t.Errorf("recorded content = %q, want the real answer — the reaper's synthesised timeout replaced an answer that had already arrived, turning a completed call into a failed one", cp.ToolResults[0].Content)
	}
}

func oversizedMessage() llm.ChatMessage {
	return llm.ChatMessage{Role: "user", Content: strings.Repeat("x", maxCheckpointBlobBytes+1)}
}

func TestConcurrentCancelsPublishExactlyOnce(t *testing.T) {
	store := &readBarrierStore{Store: session.NewMemoryStore(), barrier: make(chan struct{})}
	agent, _, brk := replicaOf(t, store, "canceller")

	ctx := context.Background()
	taskID, sessionID := idgen.New(), idgen.New()
	replyTo := "test-ns/reply/cancel-race"

	state := fullTaskState()
	state.TaskID = taskID
	state.EffectiveSessionID = sessionID
	state.ContextID = sessionID
	state.OriginalSessionID = sessionID
	state.UserProps.ReplyTo = replyTo
	state.ReplyTo = replyTo
	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	soon := time.Now().Add(time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", UserID: state.UserProps.UserID,
		SessionID: sessionID, AgentName: "test-agent", State: blob,
		OwnerID: "the-replica-running-it", LeaseExpiresAt: &soon,
		TraceID: state.UserProps.TraceID,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	const racers = 2
	var wg sync.WaitGroup
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agent.cancelCheckpointedTask(taskID)
		}()
	}
	wg.Wait()

	published := 0
	for _, m := range brk.getPublished() {
		if m.topic == replyTo {
			published++
		}
	}
	if published != 1 {
		t.Errorf("%d cancels published a terminal response for one task, want exactly 1 — the gateway receives the extras as duplicate terminal events for a task it has already closed", published)
	}
}

func TestReaperGivingUpLeavesATaskAnotherReplicaHolds(t *testing.T) {
	store := session.NewMemoryStore()
	agent, _, _ := replicaOf(t, store, "reaper")

	ctx := context.Background()
	taskID, sessionID := idgen.New(), idgen.New()
	live := time.Now().Add(time.Hour)
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
		TaskID: taskID, AppName: "test-agent", UserID: "u", SessionID: sessionID,
		AgentName: "test-agent", PendingCount: 1, State: []byte(`{"version":1}`),
		OwnerID: "a-replica-that-is-very-much-alive", LeaseExpiresAt: &live,
	}); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []session.PendingCall{{
		CorrID: "corr-old-live", TaskID: taskID, AgentName: "test-agent",
		ToolCallID: "", Kind: session.PendingKindPeer, ExpiresAt: &past,
	}}); err != nil {
		t.Fatalf("register pending call: %v", err)
	}

	agent.reapExpiredPendingCalls(ctx, time.Now())

	if cp, _ := store.GetCheckpoint(ctx, taskID); cp == nil {
		t.Error("the reaper destroyed a checkpoint another replica holds a live lease on — that replica loses the task it is running, mid-turn, and the user gets no answer at all")
	}
	if row, _ := store.LookupPendingCall(ctx, "test-agent", "corr-old-live"); row == nil {
		t.Error("the reaper consumed the correlation of a task it did not take, so the live owner can no longer resolve that call")
	}
}

type readBarrierStore struct {
	session.Store
	once    sync.Once
	mu      sync.Mutex
	waiting int
	barrier chan struct{}
}

func (s *readBarrierStore) GetCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	cp, err := s.Store.GetCheckpoint(ctx, taskID)
	s.hold()
	return cp, err
}

func (s *readBarrierStore) PopCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	s.hold()
	return s.Store.PopCheckpoint(ctx, taskID)
}

func (s *readBarrierStore) hold() {
	s.mu.Lock()
	s.waiting++
	reached := s.waiting >= 2
	s.mu.Unlock()
	if reached {
		s.once.Do(func() { close(s.barrier) })
	}
	select {
	case <-s.barrier:
	case <-time.After(2 * time.Second):
	}
}
