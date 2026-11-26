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

type settledNotifyAnswer struct {
	msg    *broker.Message
	acked  chan bool
	nacked chan struct{}
}

func queuedNotifyAnswer(t *testing.T, store session.Store, taskID string) *settledNotifyAnswer {
	t.Helper()
	settled := &settledNotifyAnswer{acked: make(chan bool, 4), nacked: make(chan struct{}, 4)}
	plain := notifyAnswer(t, regionAnswer())
	settled.msg = broker.NewDeliveredMessage("notify-"+taskID, plain.Topic, plain.Payload,
		func() { settled.acked <- len(recordedAnswers(t, store, taskID)) == 1 },
		func() { settled.nacked <- struct{}{} },
	)
	return settled
}

func TestNotifyAnswerOnTheRequestQueueIsAckedOnlyOnceRecorded(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, _, _ := replicaOf(t, store, "survivor")
	state := pausedOnQuestion(t, store, owner, "task-ask")
	owner.pausedTasks.Delete("task-ask")
	loseOwner(t, store, state, 1)
	swallowResumes(t)
	answer := queuedNotifyAnswer(t, store, "task-ask")

	survivor.routeMessageFrom(t.Context(), answer.msg, true)

	select {
	case recordedAtAck := <-answer.acked:
		if !recordedAtAck {
			t.Error("the answer was acked before it was recorded, so a crash right after the ack loses it")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answer was never acked")
	}
}

func TestNotifyAnswerThatCannotBeRecordedIsRedeliveredOnlyFromTheRequestQueue(t *testing.T) {
	for _, tc := range []struct {
		name           string
		onRequestQueue bool
		wantNacked     bool
	}{
		{name: "request queue answer is nacked so the broker delivers it again", onRequestQueue: true, wantNacked: true},
		{name: "direct subscription answer is acked because nothing redelivers it", onRequestQueue: false, wantNacked: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := session.NewMemoryStore()
			store := &flakyStore{Store: base}
			owner, _, _ := replicaOf(t, store, "owner")
			survivor, _, _ := replicaOf(t, store, "survivor")
			state := pausedOnQuestion(t, store, owner, "task-ask")
			owner.pausedTasks.Delete("task-ask")
			loseOwner(t, store, state, 1)
			store.recordFailures.Store(1 << 20)
			answer := queuedNotifyAnswer(t, base, "task-ask")

			survivor.routeMessageFrom(t.Context(), answer.msg, tc.onRequestQueue)

			var gotNacked bool
			select {
			case <-answer.nacked:
				gotNacked = true
			case <-answer.acked:
			case <-time.After(10 * time.Second):
				t.Fatal("the answer was neither acked nor nacked")
			}
			if gotNacked != tc.wantNacked {
				t.Errorf("nacked = %v, want %v", gotNacked, tc.wantNacked)
			}
			if recorded := recordedAnswers(t, base, "task-ask"); len(recorded) != 0 {
				t.Errorf("recorded results = %+v, want none while the store fails", recorded)
			}
		})
	}
}

func TestNotifyAnswerForACheckpointThisReplicaCannotReadIsNackedSoAnotherReplicaCanTakeIt(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, _, _ := replicaOf(t, store, "survivor")
	state := pausedOnQuestion(t, store, owner, "task-ask")
	owner.pausedTasks.Delete("task-ask")
	loseOwner(t, store, state, 1)
	if err := store.SaveCheckpoint(t.Context(), &session.TaskCheckpoint{
		TaskID: "task-ask", AppName: "test-agent", AgentName: "test-agent", UserID: state.UserProps.UserID,
		SessionID: state.EffectiveSessionID, PendingCount: 1, State: []byte("not a checkpoint this build can read"),
	}); err != nil {
		t.Fatalf("overwrite the checkpoint: %v", err)
	}
	answer := queuedNotifyAnswer(t, store, "task-ask")

	survivor.routeMessageFrom(t.Context(), answer.msg, true)

	select {
	case <-answer.nacked:
	case <-answer.acked:
		t.Fatal("the answer was acked although this replica could not read the checkpoint, so no other replica can take it")
	case <-time.After(5 * time.Second):
		t.Fatal("the answer was neither acked nor nacked")
	}
}
