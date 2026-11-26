// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var errStoreBlip = errors.New("database blip")

type flakyStore struct {
	session.Store

	recordFailures     atomic.Int32
	checkpointFailures atomic.Int32
}

func (s *flakyStore) RecordToolResult(ctx context.Context, taskID, corrID string, result *session.ToolCallResult) (int, error) {
	if s.recordFailures.Add(-1) >= 0 {
		return 0, errStoreBlip
	}
	return s.Store.RecordToolResult(ctx, taskID, corrID, result)
}

func (s *flakyStore) GetCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	if s.checkpointFailures.Add(-1) >= 0 {
		return nil, errStoreBlip
	}
	return s.Store.GetCheckpoint(ctx, taskID)
}

func capturingLogs(logs *syncLogBuffer) func(*Agent) {
	return func(a *Agent) {
		a.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
}

type ackCounts struct {
	acked, nacked atomic.Int32
}

func answerDelivery(t *testing.T, resp *a2a.UserInputResponseData, taskID, userID string, redelivered bool) (*broker.Message, *a2a.Request, *ackCounts) {
	t.Helper()
	wire, req := controlRequestOnTheWire(t, taskID, userID, resp)
	counts := &ackCounts{}
	msg := broker.NewDeliveredMessage("delivery-"+taskID, wire.Topic, wire.Payload, func() { counts.acked.Add(1) }, func() { counts.nacked.Add(1) })
	msg.UserProperties = wire.UserProperties
	msg.Redelivered = redelivered
	return msg, req, counts
}

func TestAnswerThatCouldNotBeRecordedIsLeftForRedelivery(t *testing.T) {
	base := session.NewMemoryStore()
	store := &flakyStore{Store: base}
	holder, _, _ := replicaOf(t, store, "holder")
	other, _, otherBrk := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	userID := state.UserProps.UserID
	store.recordFailures.Store(orphanAdoptionMaxAttempts)

	first, firstReq, firstCounts := answerDelivery(t, regionAnswer(), "task-ask", userID, false)
	other.processMessage(t.Context(), first, firstReq)

	if firstCounts.acked.Load() != 0 || firstCounts.nacked.Load() != 1 {
		t.Errorf("first delivery: acked %d, nacked %d, want 0 and 1", firstCounts.acked.Load(), firstCounts.nacked.Load())
	}
	if recorded := recordedAnswers(t, base, "task-ask"); len(recorded) != 0 {
		t.Errorf("recorded results after the failed write = %+v, want none", recorded)
	}
	if relayed := republishedAnswers(otherBrk); relayed != 0 {
		t.Errorf("the answer was republished %d times before it was recorded, want 0", relayed)
	}

	second, secondReq, secondCounts := answerDelivery(t, regionAnswer(), "task-ask", userID, true)
	other.processMessage(t.Context(), second, secondReq)

	if secondCounts.acked.Load() != 1 || secondCounts.nacked.Load() != 0 {
		t.Errorf("redelivery: acked %d, nacked %d, want 1 and 0", secondCounts.acked.Load(), secondCounts.nacked.Load())
	}
	if recorded := recordedAnswers(t, base, "task-ask"); len(recorded) != 1 {
		t.Errorf("recorded results after the redelivery = %+v, want the answer once", recorded)
	}
	if relayed := republishedAnswers(otherBrk); relayed != 1 {
		t.Errorf("the answer was republished %d times after it was recorded, want 1", relayed)
	}
}

func TestPeerReplyWhoseRecordFailsOnceIsStillRecorded(t *testing.T) {
	base := session.NewMemoryStore()
	store := &flakyStore{Store: base}
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	const taskID, subTaskID = "task-peer-blip", "sub-peer-blip"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	owner.pausedTasks.Delete(taskID)
	loseOwner(t, store, state, 1)
	store.recordFailures.Store(1)

	survivor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "peer finished the work"))

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "peer finished the work")
}

func TestNotifiedAnswerWhoseCheckpointReadFailsOnceIsStillRecorded(t *testing.T) {
	base := session.NewMemoryStore()
	store := &flakyStore{Store: base}
	owner, _, _ := replicaOf(t, store, "owner")
	logs := &syncLogBuffer{}
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor", capturingLogs(logs))
	state := pausedOnQuestion(t, store, owner, "task-ask")
	owner.pausedTasks.Delete("task-ask")
	loseOwner(t, store, state, 1)
	store.checkpointFailures.Store(1)

	survivor.handleNotifyHILResponse(notifyAnswer(t, regionAnswer()))

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, "task-ask", "EMEA")
	warned := logs.records("could not build the result for an orphaned reply; will retry")
	if len(warned) == 0 {
		t.Fatal("no Warn was logged for the failed read")
	}
	if warned[0]["corrID"] != askRequestID || warned[0]["source"] != "user answer" || warned[0]["level"] != "WARN" {
		t.Errorf("Warn record = %v, want corrID %q, source %q at WARN", warned[0], askRequestID, "user answer")
	}
}

func TestOrphanReplyThatCannotBeRecordedGivesUpLoudly(t *testing.T) {
	base := session.NewMemoryStore()
	store := &flakyStore{Store: base}
	owner, _, _ := replicaOf(t, store, "owner")
	logs := &syncLogBuffer{}
	survivor, _, _ := replicaOf(t, store, "survivor", capturingLogs(logs))
	const taskID, subTaskID = "task-peer-down", "sub-peer-down"
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := owner.checkpoints.save(context.Background(), state); err != nil {
		t.Fatalf("owner checkpoint: %v", err)
	}
	owner.pausedTasks.Delete(taskID)
	loseOwner(t, store, state, 1)
	store.recordFailures.Store(1 << 20)

	survivor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "peer finished the work"))

	gaveUp := func() bool {
		return len(logs.records(gaveUpErrorLog)) > 0
	}
	if !waitUntilWithin(10*time.Second, gaveUp) {
		t.Fatal("no Error was logged when the reply could not be recorded")
	}
	record := logs.records(gaveUpErrorLog)[0]
	if record["level"] != "ERROR" || record["corrID"] != subTaskID || record["source"] != "peer response" {
		t.Errorf("give-up record = %v, want corrID %q, source %q at ERROR", record, subTaskID, "peer response")
	}
}
