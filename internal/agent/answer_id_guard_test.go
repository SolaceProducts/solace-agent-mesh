// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

const (
	droppedUnusableAnswerIDLog = "dropping an answer whose request id is not usable"
	passedOnUnheldAnswerLog    = "passed-on answer is for a task this AWE does not hold"
	workerBarrierPrefix        = "worker-barrier-"
)

type lookupCountingStore struct {
	session.Store
	lookups atomic.Int32
	parked  atomic.Int32
	release chan struct{}
}

func newLookupCountingStore() *lookupCountingStore {
	return &lookupCountingStore{Store: session.NewMemoryStore(), release: make(chan struct{})}
}

func (s *lookupCountingStore) LookupPendingCall(ctx context.Context, agentName, corrID string) (*session.PendingCall, error) {
	if strings.HasPrefix(corrID, workerBarrierPrefix) {
		s.parked.Add(1)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	s.lookups.Add(1)
	return s.Store.LookupPendingCall(ctx, agentName, corrID)
}

func drainOrphanWorkers(t *testing.T, replica *Agent, store *lookupCountingStore) {
	t.Helper()
	defer close(store.release)
	for i := range orphanAdoptionWorkers {
		barrier := orphanAdoption{corrID: fmt.Sprintf("%s%d", workerBarrierPrefix, i), source: "barrier", userAnswer: true}
		if !replica.queueOrphanAdoption(barrier) {
			t.Fatalf("barrier %d was not queued", i)
		}
	}
	if !waitUntilWithin(5*time.Second, func() bool { return store.parked.Load() == orphanAdoptionWorkers }) {
		t.Fatalf("workers parked on the barrier = %d, want %d", store.parked.Load(), orphanAdoptionWorkers)
	}
}

type answerEntry struct {
	name             string
	viaNotify        bool
	wantUnusableLogs int
}

var answerEntries = []answerEntry{
	{name: "from the notification service", viaNotify: true, wantUnusableLogs: 1},
	{name: "from the user's own request"},
}

func answerThrough(t *testing.T, entry answerEntry, replica *Agent, answer *a2a.UserInputResponseData) hilDelivery {
	t.Helper()
	if entry.viaNotify {
		replica.handleNotifyHILResponse(notifyAnswer(t, answer))
		return hilUnmatched
	}
	return replica.routeUserInputResponse(t.Context(), answer, hilSenderUser("user-1"))
}

func TestAnAnswerWithAnIdNoToolCouldHaveRegisteredNeverReachesTheStore(t *testing.T) {
	ids := []struct {
		name string
		id   string
	}{
		{name: "an id with a NUL byte", id: "ask\x00id"},
		{name: "an id with a newline", id: "ask\nid"},
		{name: "an id with a C1 control character", id: "ask\u0085id"},
		{name: "an id over the length cap", id: strings.Repeat("a", maxHILRequestIDBytes+1)},
	}
	for _, entry := range answerEntries {
		for _, tt := range ids {
			t.Run(entry.name+" "+tt.name, func(t *testing.T) {
				counting := newLookupCountingStore()
				logs := &syncLogBuffer{}
				survivor, _, _ := replicaOf(t, counting, "survivor", capturingLogs(logs))
				answer := regionAnswer()
				answer.RequestID = tt.id

				if got := answerThrough(t, entry, survivor, answer); got != hilUnmatched {
					t.Errorf("answer delivery = %v, want hilUnmatched", got)
				}
				drainOrphanWorkers(t, survivor, counting)

				if got := counting.lookups.Load(); got != 0 {
					t.Errorf("store lookups = %d, want 0", got)
				}
				if got := len(logs.records(droppedUnusableAnswerIDLog)); got != entry.wantUnusableLogs {
					t.Errorf("records of %q = %d, want %d", droppedUnusableAnswerIDLog, got, entry.wantUnusableLogs)
				}
			})
		}
	}
}

func TestAnAnswerWithAnIdThatCouldBeRegisteredButMatchesNothingIsLookedUp(t *testing.T) {
	for _, entry := range answerEntries {
		t.Run(entry.name, func(t *testing.T) {
			counting := newLookupCountingStore()
			survivor, _, _ := replicaOf(t, counting, "survivor")
			answer := regionAnswer()
			answer.RequestID = "no-task-waits-on-this"

			answerThrough(t, entry, survivor, answer)
			drainOrphanWorkers(t, survivor, counting)

			if got := counting.lookups.Load(); got != 1 {
				t.Errorf("store lookups = %d, want 1", got)
			}
		})
	}
}

func TestTheUnmatchedAnswerWarningNamesAnIdOnlyWhenNoToolCouldHaveRegisteredIt(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want map[string]any
	}{
		{name: "an id that could be registered is named", id: "no-such-request", want: map[string]any{"requestID": "no-such-request"}},
		{name: "an id with a newline is only counted", id: "forged\nid", want: map[string]any{"requestIDBytes": float64(len("forged\nid"))}},
		{name: "an id over the length cap is only counted", id: strings.Repeat("a", maxHILRequestIDBytes+1), want: map[string]any{"requestIDBytes": float64(maxHILRequestIDBytes + 1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			logs := captureAgentLogs(agent)

			answerOnTheWire(t, agent, "task-bob", hilBob, userInputResponse(tt.id, "answered", nil))

			records := logs.records(noMatchingRequestMsg)
			if len(records) != 1 {
				t.Fatalf("records of %q = %d, want 1:\n%s", noMatchingRequestMsg, len(records), logs.String())
			}
			got := map[string]any{}
			for _, key := range []string{"requestID", "requestIDBytes"} {
				if value, ok := records[0][key]; ok {
					got[key] = value
				}
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("request id fields of the warning (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAnAnswerWithAnUnusableIdStillReachesAnInProcessWaiter(t *testing.T) {
	const unusableID = "ask\nid"
	for _, entry := range answerEntries {
		t.Run(entry.name, func(t *testing.T) {
			survivor, _, _ := replicaOf(t, session.NewMemoryStore(), "survivor")
			waiter := survivor.registerHILRequest(unusableID, "user-1", "task-waiting")
			answer := regionAnswer()
			answer.RequestID = unusableID

			answerThrough(t, entry, survivor, answer)

			select {
			case got := <-waiter:
				if got.RequestID != unusableID {
					t.Errorf("waiter received request id %q, want %q", got.RequestID, unusableID)
				}
			case <-time.After(5 * time.Second):
				t.Error("the in-process waiter never received the answer")
			}
		})
	}
}

func TestAPassedOnAnswerWithAnUnusableIdIsLoggedByLengthOnly(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "an id with a NUL byte", id: "ask\x00id"},
		{name: "an id over the length cap", id: strings.Repeat("a", maxHILRequestIDBytes+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := &syncLogBuffer{}
			replica, _, _ := replicaOf(t, session.NewMemoryStore(), "replica", capturingLogs(logs))
			answer := regionAnswer()
			answer.RequestID = tt.id
			msg, _, _ := answerDelivery(t, answer, "task-bob", hilBob, false)

			replica.handleForwardedControl(t.Context(), msg)

			records := logs.records(passedOnUnheldAnswerLog)
			if len(records) != 1 {
				t.Fatalf("records of %q = %d, want 1:\n%s", passedOnUnheldAnswerLog, len(records), logs.String())
			}
			got := map[string]any{}
			for _, key := range []string{"requestID", "requestIDBytes"} {
				if value, ok := records[0][key]; ok {
					got[key] = value
				}
			}
			want := map[string]any{"requestIDBytes": float64(len(tt.id))}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("request id fields of the log line (-want +got):\n%s", diff)
			}
		})
	}
}
