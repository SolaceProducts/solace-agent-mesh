// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

const (
	lookupFailedLog     = "failed to look up a paused call for a response"
	checkpointReadLog   = "failed to read the checkpoint of a task a response is for"
	redeliveredGaveUp   = "could not record a user's answer on its redelivery either"
	answerBankFailedLog = "could not bank a user's answer; resuming the task with it in hand"
	noMatchLog          = "received user_input_response with no matching pending request"
)

type readFailingStore struct {
	session.Store

	lookupFailures     atomic.Int32
	checkpointFailures atomic.Int32
}

func (s *readFailingStore) LookupPendingCall(ctx context.Context, agentName, corrID string) (*session.PendingCall, error) {
	if s.lookupFailures.Add(-1) >= 0 {
		return nil, errStoreBlip
	}
	return s.Store.LookupPendingCall(ctx, agentName, corrID)
}

func (s *readFailingStore) GetCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	if s.checkpointFailures.Add(-1) >= 0 {
		return nil, errStoreBlip
	}
	return s.Store.GetCheckpoint(ctx, taskID)
}

func TestPassedOnAnswerWhoseRecordFailsIsRetriedOnTheAdoptionWorkers(t *testing.T) {
	base := session.NewMemoryStore()
	store := &flakyStore{Store: base}
	holder, _, _ := replicaOf(t, store, "holder")
	other, _, _ := replicaOf(t, store, "other", capturingLogs(&syncLogBuffer{}))
	state := pausedOnQuestion(t, store, holder, "task-ask")
	store.recordFailures.Store(orphanAdoptionMaxAttempts + 1)
	msg, _, _ := answerDelivery(t, regionAnswer(), "task-ask", state.UserProps.UserID, false)

	other.handleForwardedControl(t.Context(), msg)

	if !waitUntilWithin(10*time.Second, func() bool { return len(recordedAnswers(t, base, "task-ask")) == 1 }) {
		t.Fatalf("recorded results = %+v, want the passed-on answer recorded once even though the first writes failed", recordedAnswers(t, base, "task-ask"))
	}
}

func TestAnswerWhoseRecordFailsOnceIsRecordedWithoutBouncingTheMessage(t *testing.T) {
	base := session.NewMemoryStore()
	store := &flakyStore{Store: base}
	holder, _, _ := replicaOf(t, store, "holder")
	other, _, otherBrk := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	store.recordFailures.Store(1)
	msg, req, counts := answerDelivery(t, regionAnswer(), "task-ask", state.UserProps.UserID, false)

	other.processMessage(t.Context(), msg, req)

	if counts.acked.Load() != 1 || counts.nacked.Load() != 0 {
		t.Errorf("delivery: acked %d, nacked %d, want 1 and 0", counts.acked.Load(), counts.nacked.Load())
	}
	if recorded := recordedAnswers(t, base, "task-ask"); len(recorded) != 1 {
		t.Errorf("recorded results = %+v, want the answer once", recorded)
	}
	if relayed := republishedAnswers(otherBrk); relayed != 1 {
		t.Errorf("the answer was republished %d times, want 1", relayed)
	}
}

func TestRedeliveredAnswerThatCannotBeRecordedIsLoggedAtError(t *testing.T) {
	base := session.NewMemoryStore()
	store := &flakyStore{Store: base}
	holder, _, _ := replicaOf(t, store, "holder")
	logs := &syncLogBuffer{}
	other, _, _ := replicaOf(t, store, "other", capturingLogs(logs))
	state := pausedOnQuestion(t, store, holder, "task-ask")
	store.recordFailures.Store(1 << 20)
	msg, req, counts := answerDelivery(t, regionAnswer(), "task-ask", state.UserProps.UserID, true)

	other.processMessage(t.Context(), msg, req)

	if counts.acked.Load() != 0 || counts.nacked.Load() != 1 {
		t.Errorf("delivery: acked %d, nacked %d, want 0 and 1", counts.acked.Load(), counts.nacked.Load())
	}
	gaveUp := logs.records(redeliveredGaveUp)
	if len(gaveUp) != 1 {
		t.Fatalf("Error records for a redelivered answer that failed again = %d, want 1", len(gaveUp))
	}
	want := map[string]any{"level": "ERROR", "taskID": "task-ask"}
	got := map[string]any{"level": gaveUp[0]["level"], "taskID": gaveUp[0]["taskID"]}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("redelivery give-up record mismatch (-want +got):\n%s", diff)
	}
}

func TestAnswerWhoseStoreReadFailsIsLoggedAndPassedOn(t *testing.T) {
	tests := []struct {
		name             string
		lookupFailures   int32
		checkpointFailed int32
		wantWarn         string
	}{
		{name: "pending call lookup fails", lookupFailures: 1, wantWarn: lookupFailedLog},
		{name: "checkpoint read fails", checkpointFailed: 1, wantWarn: checkpointReadLog},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &readFailingStore{Store: session.NewMemoryStore()}
			holder, _, _ := replicaOf(t, store, "holder")
			logs := &syncLogBuffer{}
			other, _, otherBrk := replicaOf(t, store, "other", capturingLogs(logs))
			state := pausedOnQuestion(t, store, holder, "task-ask")
			store.lookupFailures.Store(tt.lookupFailures)
			store.checkpointFailures.Store(tt.checkpointFailed)
			msg, req, counts := answerDelivery(t, regionAnswer(), "task-ask", state.UserProps.UserID, false)

			other.processMessage(t.Context(), msg, req)

			if counts.acked.Load() != 1 || counts.nacked.Load() != 0 {
				t.Errorf("delivery: acked %d, nacked %d, want 1 and 0", counts.acked.Load(), counts.nacked.Load())
			}
			if passedOn := len(passedOnCopies(otherBrk, other.forwardTopic())); passedOn != 1 {
				t.Errorf("answers passed on = %d, want 1", passedOn)
			}
			warned := logs.records(tt.wantWarn)
			if len(warned) != 1 {
				t.Fatalf("Warn records %q = %d, want 1", tt.wantWarn, len(warned))
			}
			if got := warned[0]["level"]; got != "WARN" {
				t.Errorf("record %q level = %v, want WARN", tt.wantWarn, got)
			}
			if got := warned[0]["corrID"]; got != askRequestID {
				t.Errorf("record %q corrID = %v, want %q", tt.wantWarn, got, askRequestID)
			}
			if blamed := logs.records(noMatchLog); len(blamed) != 0 {
				t.Errorf("records %q = %v, want none: the store could not be read, so nobody knows the answer has no pending request", noMatchLog, blamed)
			}
		})
	}
}

func TestAnswerWhoseBankWriteFailsStillResumesTheTaskAndLogsAnError(t *testing.T) {
	base := session.NewMemoryStore()
	store := &flakyStore{Store: base}
	logs := &syncLogBuffer{}
	holder, _, _ := replicaOf(t, store, "holder", capturingLogs(logs))
	state := pausedOnQuestion(t, store, holder, "task-ask")
	var resumes atomic.Int32
	useResumeHook(t, func(*Agent, *TaskExecutionState) { resumes.Add(1) })
	store.recordFailures.Store(1 << 20)

	delivered := holder.deliverHILResponse(regionAnswer(), hilSenderUser(state.UserProps.UserID))

	if delivered != hilDelivered {
		t.Errorf("deliverHILResponse() = %v, want delivered", delivered)
	}
	if resumes.Load() != 1 {
		t.Errorf("the task resumed %d times, want once with the answer in hand", resumes.Load())
	}
	failed := logs.records(answerBankFailedLog)
	if len(failed) != 1 {
		t.Fatalf("Error records for the failed bank = %d, want 1", len(failed))
	}
	want := map[string]any{"level": "ERROR", "taskID": "task-ask", "requestID": askRequestID}
	got := map[string]any{"level": failed[0]["level"], "taskID": failed[0]["taskID"], "requestID": failed[0]["requestID"]}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("bank failure record mismatch (-want +got):\n%s", diff)
	}
}
