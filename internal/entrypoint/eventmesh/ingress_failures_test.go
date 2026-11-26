// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

var errStoreDown = errors.New("store down")

type faultyStore struct {
	*memIngressStore
	failStore, failRouting, failRoutingRead, failMark, failOutcome, failForget bool
	isShared                                                                   bool
}

func (s *faultyStore) store(ctx context.Context, messageID, taskID, payload string) (bool, error) {
	if s.failStore {
		return false, errStoreDown
	}
	return s.memIngressStore.store(ctx, messageID, taskID, payload)
}

func (s *faultyStore) putRouting(ctx context.Context, taskID string, r taskRouting) error {
	if s.failRouting {
		return errStoreDown
	}
	return s.memIngressStore.putRouting(ctx, taskID, r)
}

func (s *faultyStore) routing(ctx context.Context, taskID string) (taskRouting, bool, error) {
	if s.failRoutingRead {
		return taskRouting{}, false, errStoreDown
	}
	return s.memIngressStore.routing(ctx, taskID)
}

func (s *faultyStore) markSubmitted(ctx context.Context, messageID, taskID, outcome string) error {
	if s.failMark {
		return errStoreDown
	}
	return s.memIngressStore.markSubmitted(ctx, messageID, taskID, outcome)
}

func (s *faultyStore) setOutcome(ctx context.Context, messageID, taskID, outcome string) (bool, error) {
	if s.failOutcome {
		return false, errStoreDown
	}
	return s.memIngressStore.setOutcome(ctx, messageID, taskID, outcome)
}

func (s *faultyStore) forget(ctx context.Context, messageID string) error {
	if s.failForget {
		return errStoreDown
	}
	return s.memIngressStore.forget(ctx, messageID)
}

func (s *faultyStore) shared() bool { return s.isShared }

func faultyGWE(t *testing.T, ackPolicy map[string]any, shared bool) (*EventMeshAdapter, *faultyStore) {
	t.Helper()
	gwes, _, _ := newEntrypointGWEs(t, 1, ackPolicy)
	a := gwes[0].adapter
	s := &faultyStore{memIngressStore: newMemIngressStore(), isShared: shared}
	a.ingress = s
	a.held = newHeldEvents(s, a.logger)
	return a, s
}

func storedRows(t *testing.T, s ingressStore) []store.StoredInbound {
	t.Helper()
	rows, err := s.stored(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestStoreEvent_StoreFailure_NacksForRedelivery(t *testing.T) {
	a, s := faultyGWE(t, onCompletion("ack"), true)
	s.failStore = true
	var acked, nacked atomic.Bool

	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked))

	if acked.Load() || !nacked.Load() || a.held.count() != 0 {
		t.Errorf("acked=%v nacked=%v held=%d, want nacked and not held", acked.Load(), nacked.Load(), a.held.count())
	}
}

func TestStoreEvent_UnencodableEvent_AppliesTheFailurePolicy(t *testing.T) {
	a, s := faultyGWE(t, onCompletion("ack"), true)
	var acked, nacked atomic.Bool

	reason := a.storeEvent(t.Context(), a.logger, inputEvent("evt-1", &acked, &nacked),
		storedEvent{Handler: "h1", ForwardContext: map[string]any{"x": math.Inf(1)}}, "task-1", a.getAckPolicy(a.cfg.EventHandlers[0]))

	if reason == "" || !acked.Load() || nacked.Load() {
		t.Errorf("reason=%q acked=%v nacked=%v, want refused and acked under on_failure ack", reason, acked.Load(), nacked.Load())
	}
	if rows := storedRows(t, s); len(rows) != 0 {
		t.Errorf("stored = %v, want nothing", rows)
	}
}

func TestStoreEvent_MessageWithoutBrokerID_IsStoredUnderItsOwnKey(t *testing.T) {
	a, s := faultyGWE(t, map[string]any{"mode": "on_receive"}, true)
	var acked, nacked atomic.Bool
	for range 2 {
		msg := inputEvent("", &acked, &nacked)
		a.transformMessage(t.Context(), a.cfg.EventHandlers[0], msg)
	}
	if rows := storedRows(t, s); len(rows) != 2 {
		t.Errorf("stored = %d events, want two: without an id a copy is not recognised", len(rows))
	}
}

func TestSubmitStored_UndecodableEvent_IsRejected(t *testing.T) {
	a, s := faultyGWE(t, onCompletion("nack"), true)
	if _, err := s.store(t.Context(), "evt-1", "task-1", "not json"); err != nil {
		t.Fatal(err)
	}

	if reason := a.submitStored(t.Context(), storedRows(t, s)[0]); reason == "" {
		t.Error("an undecodable event was not refused")
	}
	if got, _ := s.outcomes(t.Context(), []string{"evt-1"}); got["evt-1"] != store.InboundOutcomeReject {
		t.Errorf("outcome = %q, want reject", got["evt-1"])
	}
}

func TestSubmitStored_HandlerNoLongerConfigured_IsRejected(t *testing.T) {
	a, s := faultyGWE(t, onCompletion("nack"), true)
	var acked, nacked atomic.Bool
	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked))
	a.cfg.EventHandlers[0].Name = "renamed"

	if reason := a.submitStored(t.Context(), storedRows(t, s)[0]); reason == "" {
		t.Error("an event of a removed handler was not refused")
	}
	a.held.poll(t.Context())
	if !nacked.Load() {
		t.Error("the held event was not settled through on_failure")
	}
}

func TestSubmitStored_RoutingNotRecorded_LeavesTheEventForTheNextPass(t *testing.T) {
	a, s := faultyGWE(t, map[string]any{"mode": "on_receive"}, true)
	var acked, nacked atomic.Bool
	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked))
	s.failRouting = true
	row := storedRows(t, s)[0]

	a.submitPass(t.Context())

	if rows := storedRows(t, s); len(rows) != 1 {
		t.Fatalf("stored = %v, want the event kept", rows)
	}
	if a.core.Tasks().Get(row.TaskID) != nil {
		t.Error("the task record of an unsubmitted event was kept")
	}
}

func TestSubmitStored_MarkFails_TheEventIsSubmittedAgainUnderTheSameTask(t *testing.T) {
	a, s := faultyGWE(t, map[string]any{"mode": "on_receive"}, true)
	requests := tapTopic(t, a.dataBroker, "agent-requests", a2a.AgentRequestTopic("test", "TestAgent"))
	var acked, nacked atomic.Bool
	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked))
	s.failMark = true

	a.submitPass(t.Context())
	a.submitPass(t.Context())

	ids := requestTaskIDs(t, receiveAll(t, a.dataBroker, requests))
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Errorf("agent requests = %v, want the same task twice", ids)
	}
}

func TestSubmitStored_PublishFails_LeavesTheEventForTheNextPass(t *testing.T) {
	a, s := faultyGWE(t, map[string]any{"mode": "on_receive"}, true)
	var acked, nacked atomic.Bool
	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked))
	if err := a.dataBroker.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}

	a.submitPass(t.Context())

	if rows := storedRows(t, s); len(rows) != 1 {
		t.Errorf("stored = %v, want the event kept for the next pass", rows)
	}
}

func TestRunIngress_SubmitsWhileLeadingAndStopsWithTheTerm(t *testing.T) {
	a, s := faultyGWE(t, map[string]any{"mode": "on_receive"}, true)
	requests := tapTopic(t, a.dataBroker, "agent-requests", a2a.AgentRequestTopic("test", "TestAgent"))
	runCtx, stop := context.WithCancel(t.Context())
	defer stop()
	a.runCtx = runCtx

	var acked, nacked atomic.Bool
	a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent("evt-1", &acked, &nacked))
	if len(storedRows(t, s)) != 1 {
		t.Fatal("event not stored")
	}
	term1, endTerm1 := context.WithCancel(t.Context())
	a.RunIngress(term1, 1)
	a.RunIngress(term1, 1)

	deadline := time.Now().Add(3 * submitPassInterval)
	for len(storedRows(t, s)) != 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if ids := requestTaskIDs(t, receiveAll(t, a.dataBroker, requests)); len(ids) != 1 {
		t.Fatalf("agent requests = %v, want the stored event submitted once", ids)
	}
	endTerm1()
	a.RunIngress(t.Context(), 2)
	a.runMu.Lock()
	term := a.submitRun.term
	a.runMu.Unlock()
	if term != 2 {
		t.Errorf("submit loop term = %d, want 2", term)
	}
	stop()
	a.wg.Wait()
}

func TestRunIngress_WithoutASharedStore_DoesNothing(t *testing.T) {
	a, _ := faultyGWE(t, map[string]any{"mode": "on_receive"}, false)
	a.runCtx = t.Context()
	a.RunIngress(t.Context(), 1)
	if a.submitRun != nil {
		t.Error("a GWE without a shared store started a leader loop")
	}
}

func TestAnswer_RoutingUnreadable_KeepsTheAnswerEvent(t *testing.T) {
	a, s := faultyGWE(t, onCompletion("nack"), true)
	s.failRoutingRead = true
	ctx, d := delivery.Attach(t.Context(), "answer-1", false, func() {})

	a.handleFinalResponse(ctx, "task-1", answered("approved"))
	a.handleErrorResponse(ctx, "task-2", &a2a.Error{Code: -1, Message: "failed"})

	if !d.Kept() {
		t.Error("the answer event was acknowledged although its routing could not be read")
	}
}

func TestAnswer_OutcomeNotRecorded_KeepsTheAnswerEvent(t *testing.T) {
	a, s := faultyGWE(t, onCompletion("nack"), true)
	a.seedPending(t, "task-1", &pendingTask{handlerCfg: a.cfg.EventHandlers[0], deferredACK: true})
	s.failOutcome = true
	ctx, d := delivery.Attach(t.Context(), "answer-1", false, func() {})

	a.handleFinalResponse(ctx, "task-1", answered("approved"))

	if !d.Kept() {
		t.Error("the answer event was acknowledged although the outcome was not recorded")
	}
	if _, busy := a.answering.Load("task-1"); busy {
		t.Error("the task stays marked answered, so its redelivered answer would be dropped")
	}
}

func TestStoreEvent_OneMessageForTwoHandlers_IsStoredForEach(t *testing.T) {
	a, s := faultyGWE(t, map[string]any{"mode": "on_receive"}, true)
	second := *a.cfg.EventHandlers[0]
	second.Name = "h2"
	a.cfg.EventHandlers = append(a.cfg.EventHandlers, &second)
	var acked, nacked atomic.Bool

	for _, h := range a.cfg.EventHandlers {
		a.transformMessage(t.Context(), h, inputEvent("evt-1", &acked, &nacked))
	}

	if rows := storedRows(t, s); len(rows) != 2 {
		t.Errorf("stored = %v, want one event per handler", rows)
	}
}

func TestAnswer_LateAnswerOfATimedOutTask_LeavesTheRerunHeld(t *testing.T) {
	a, s := faultyGWE(t, onCompletion("nack"), true)
	a.seedPending(t, "task-A", &pendingTask{handlerCfg: a.cfg.EventHandlers[0], deferredACK: true})
	key := inboundIDFor("task-A")
	if err := s.forget(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store(t.Context(), key, "task-B", "{}"); err != nil {
		t.Fatal(err)
	}
	var acked, nacked atomic.Bool
	a.held.hold(key, testMsg(&acked, &nacked), time.Hour, AckFailurePolicy{Action: "nack"})

	a.handleFinalResponse(t.Context(), "task-A", answered("approved"))

	if acked.Load() || nacked.Load() || a.held.count() != 1 {
		t.Errorf("acked=%v nacked=%v held=%d, want the rerun still held", acked.Load(), nacked.Load(), a.held.count())
	}
}

func TestSubmitPasses_Backlog_IsSubmittedInOneTick(t *testing.T) {
	a, s := faultyGWE(t, map[string]any{"mode": "on_receive"}, true)
	var acked, nacked atomic.Bool
	for i := range submitPassBatch + 1 {
		a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent(fmt.Sprintf("evt-%d", i), &acked, &nacked))
	}

	a.submitPasses(t.Context())

	if rows := storedRows(t, s); len(rows) != 0 {
		t.Errorf("stored = %d events, want the whole backlog submitted", len(rows))
	}
}

func TestSubmitPasses_FailingBatch_StopsUntilTheNextTick(t *testing.T) {
	a, s := faultyGWE(t, map[string]any{"mode": "on_receive"}, true)
	var acked, nacked atomic.Bool
	for i := range submitPassBatch {
		a.transformMessage(t.Context(), a.cfg.EventHandlers[0], inputEvent(fmt.Sprintf("evt-%d", i), &acked, &nacked))
	}
	s.failRouting = true

	a.submitPasses(t.Context())

	if rows, err := s.stored(t.Context(), 2*submitPassBatch); err != nil || len(rows) != submitPassBatch {
		t.Errorf("stored = %d events (err %v), want the failed batch kept", len(rows), err)
	}
}

func TestHeldEvents_RejectNotReset_StaysHeldUntilTheDeleteSucceeds(t *testing.T) {
	a, s := faultyGWE(t, onCompletion("nack"), true)
	if _, err := s.store(t.Context(), "evt-1", "task-1", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.markSubmitted(t.Context(), "evt-1", "task-1", store.InboundOutcomeReject); err != nil {
		t.Fatal(err)
	}
	var acked, nacked atomic.Bool
	a.held.hold("evt-1", testMsg(&acked, &nacked), time.Hour, AckFailurePolicy{Action: "nack"})
	s.failForget = true

	a.held.poll(t.Context())
	if nacked.Load() || a.held.count() != 1 {
		t.Fatalf("nacked=%v held=%d, want the event held while its stored row stays", nacked.Load(), a.held.count())
	}

	s.failForget = false
	a.held.poll(t.Context())
	if !nacked.Load() || a.held.count() != 0 {
		t.Errorf("nacked=%v held=%d, want nacked once the row is deleted", nacked.Load(), a.held.count())
	}
}
