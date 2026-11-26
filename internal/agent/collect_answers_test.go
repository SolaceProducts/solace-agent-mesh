// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

const collectFailedLog = "could not check whether another replica recorded an answer for a paused task"

type readCountingStore struct {
	session.Store

	checkpointReads atomic.Int32
	lookups         atomic.Int32
}

func (s *readCountingStore) GetCheckpoint(ctx context.Context, taskID string) (*session.TaskCheckpoint, error) {
	s.checkpointReads.Add(1)
	return s.Store.GetCheckpoint(ctx, taskID)
}

func (s *readCountingStore) LookupPendingCall(ctx context.Context, agentName, corrID string) (*session.PendingCall, error) {
	s.lookups.Add(1)
	return s.Store.LookupPendingCall(ctx, agentName, corrID)
}

func TestCollectingAnswersDoesNotReadTheCheckpointOfATaskStillWaiting(t *testing.T) {
	store := &readCountingStore{Store: session.NewMemoryStore()}
	holder, _, _ := replicaOf(t, store, "holder")
	pausedOnQuestion(t, store, holder, "task-ask")
	store.checkpointReads.Store(0)

	holder.collectRecordedAnswers(t.Context())

	if got := store.checkpointReads.Load(); got != 0 {
		t.Errorf("checkpoint reads while the answer was outstanding = %d, want 0: the pending row says nothing was recorded", got)
	}
}

func TestCollectingAnswersStopsWhenTheTickBudgetIsSpent(t *testing.T) {
	store := &readCountingStore{Store: session.NewMemoryStore()}
	holder, _, _ := replicaOf(t, store, "holder")
	pausedOnQuestion(t, store, holder, "task-ask")
	store.checkpointReads.Store(0)
	store.lookups.Store(0)
	spent, cancel := context.WithCancel(t.Context())
	cancel()

	holder.collectRecordedAnswers(spent)

	if reads, lookups := store.checkpointReads.Load(), store.lookups.Load(); reads != 0 || lookups != 0 {
		t.Errorf("store reads after the budget was spent: %d checkpoint reads and %d lookups, want 0 and 0", reads, lookups)
	}
}

func TestCollectingAnswersLogsAStoreReadThatFails(t *testing.T) {
	tests := []struct {
		name           string
		lookupFailures int32
		checkpointFail int32
	}{
		{name: "the pending call lookup fails", lookupFailures: 1},
		{name: "the checkpoint read fails", checkpointFail: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &readFailingStore{Store: session.NewMemoryStore()}
			logs := &syncLogBuffer{}
			holder, holderLLM, holderBrk := replicaOf(t, store, "holder", capturingLogs(logs))
			other, _, _ := replicaOf(t, store, "other")
			state := pausedOnQuestion(t, store, holder, "task-ask")
			if got := other.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
				t.Fatalf("delivery = %v, want delivered", got)
			}
			store.lookupFailures.Store(tt.lookupFailures)
			store.checkpointFailures.Store(tt.checkpointFail)

			holder.collectRecordedAnswers(t.Context())

			warned := logs.records(collectFailedLog)
			if len(warned) != 1 {
				t.Fatalf("Warn records %q = %d, want 1", collectFailedLog, len(warned))
			}
			want := map[string]any{"level": "WARN", "taskID": "task-ask", "traceID": state.UserProps.TraceID}
			got := map[string]any{"level": warned[0]["level"], "taskID": warned[0]["taskID"], "traceID": warned[0]["traceID"]}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Warn record mismatch (-want +got):\n%s", diff)
			}
			holder.collectRecordedAnswers(t.Context())
			assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-ask", "EMEA")
		})
	}
}
