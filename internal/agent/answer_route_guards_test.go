// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	questionOwnerID = "user-1"
	strangerID      = "bob"

	passedOnQueuedLog = "could not record a passed-on answer; queued it for retry on the adoption workers"
)

func passedOnCopies(brk *mockBroker, topic string) []*broker.Message {
	var copies []*broker.Message
	for _, m := range brk.getPublished() {
		if m.topic == topic {
			copies = append(copies, &broker.Message{Topic: m.topic, Payload: m.payload, UserProperties: m.userProps})
		}
	}
	return copies
}

func TestStrangersAnswerIsNeverRecordedWhenAStoreReadFailsOnce(t *testing.T) {
	tests := []struct {
		name           string
		lookupFailures int32
		checkpointFail int32
	}{
		{name: "the pending call lookup fails once", lookupFailures: 1},
		{name: "the checkpoint read fails once", checkpointFail: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := session.NewMemoryStore()
			store := &readFailingStore{Store: base}
			holder, _, _ := replicaOf(t, store, "holder")
			logs := &syncLogBuffer{}
			other, _, otherBrk := replicaOf(t, store, "other", capturingLogs(logs))
			pausedOnQuestion(t, store, holder, "task-ask")
			store.lookupFailures.Store(tt.lookupFailures)
			store.checkpointFailures.Store(tt.checkpointFail)
			msg, _, _ := answerDelivery(t, regionAnswer(), "task-ask", strangerID, false)

			other.handleForwardedControl(t.Context(), msg)

			if !waitUntilWithin(5*time.Second, func() bool { return len(logs.records(refusedAnswerLog)) == 1 }) {
				t.Errorf("records %q = %d, want the retry on the adoption workers to refuse the stranger's answer once", refusedAnswerLog, len(logs.records(refusedAnswerLog)))
			}
			if recorded := recordedAnswers(t, base, "task-ask"); len(recorded) != 0 {
				t.Errorf("recorded results = %+v, want none: %s does not own the paused question", recorded, strangerID)
			}
			if relayed := republishedAnswers(otherBrk); relayed != 0 {
				t.Errorf("the answer was republished %d times, want 0", relayed)
			}
		})
	}
}

func TestAnswerForABlockingWaitReachesItsHolderWhenTheRelayingReplicaCannotLookItUp(t *testing.T) {
	store := &readFailingStore{Store: session.NewMemoryStore()}
	holder, _, _ := replicaOf(t, store, "holder")
	other, _, otherBrk := replicaOf(t, store, "other")
	waiting := holder.registerHILRequest(askRequestID, questionOwnerID, "")
	store.lookupFailures.Store(1)
	msg, req, counts := answerDelivery(t, regionAnswer(), "task-ask", questionOwnerID, false)

	other.processMessage(t.Context(), msg, req)

	if counts.acked.Load() != 1 || counts.nacked.Load() != 0 {
		t.Errorf("delivery: acked %d, nacked %d, want 1 and 0: the holder, not this replica, can apply the answer", counts.acked.Load(), counts.nacked.Load())
	}
	copies := passedOnCopies(otherBrk, other.forwardTopic())
	if len(copies) != 1 {
		t.Fatalf("answers passed on = %d, want 1", len(copies))
	}
	holder.handleForwardedControl(t.Context(), copies[0])
	select {
	case got := <-waiting:
		if got.Status != "answered" {
			t.Errorf("the waiting tool got status %q, want answered", got.Status)
		}
	case <-time.After(5 * time.Second):
		t.Error("the blocking wait never received the answer")
	}
}

func TestAnswerForAPausedTaskReachesItsHolderWhenTheRelayingReplicaCannotReadTheCheckpoint(t *testing.T) {
	base := session.NewMemoryStore()
	store := &readFailingStore{Store: base}
	holder, holderLLM, holderBrk := replicaOf(t, store, "holder")
	other, _, otherBrk := replicaOf(t, store, "other")
	pausedOnQuestion(t, store, holder, "task-ask")
	store.checkpointFailures.Store(1)
	msg, req, counts := answerDelivery(t, regionAnswer(), "task-ask", questionOwnerID, false)

	other.processMessage(t.Context(), msg, req)

	if counts.acked.Load() != 1 || counts.nacked.Load() != 0 {
		t.Errorf("delivery: acked %d, nacked %d, want 1 and 0", counts.acked.Load(), counts.nacked.Load())
	}
	copies := passedOnCopies(otherBrk, other.forwardTopic())
	if len(copies) != 1 {
		t.Fatalf("answers passed on = %d, want 1", len(copies))
	}
	holder.handleForwardedControl(t.Context(), copies[0])
	assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-ask", "EMEA")
}
