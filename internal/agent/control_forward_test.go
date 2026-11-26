// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

type passOnEffects struct {
	PassedOn, Republished int
}

func TestPassedOnAnswerThatFindsTheStoreUnreadableIsRetriedNotPassedOnAgain(t *testing.T) {
	base := session.NewMemoryStore()
	store := &readFailingStore{Store: base}
	holder, _, _ := replicaOf(t, store, "holder")
	logs := &syncLogBuffer{}
	second, _, secondBrk := replicaOf(t, store, "second", capturingLogs(logs))
	state := pausedOnQuestion(t, store, holder, "task-ask")
	store.lookupFailures.Store(1)
	msg, _, _ := answerDelivery(t, regionAnswer(), "task-ask", state.UserProps.UserID, false)

	second.handleForwardedControl(t.Context(), msg)

	if !waitUntilWithin(10*time.Second, func() bool { return len(recordedAnswers(t, base, "task-ask")) == 1 }) {
		t.Fatalf("recorded results = %+v, want the passed-on answer recorded once by the retry", recordedAnswers(t, base, "task-ask"))
	}
	got := passOnEffects{PassedOn: len(passedOnCopies(secondBrk, second.forwardTopic())), Republished: republishedAnswers(secondBrk)}
	if diff := cmp.Diff(passOnEffects{}, got); diff != "" {
		t.Errorf("messages the second replica sent after a passed-on answer (-want +got):\n%s", diff)
	}
	queued := logs.records(passedOnQueuedLog)
	if diff := cmp.Diff(1, len(queued)); diff != "" {
		t.Errorf("records %q count mismatch (-want +got):\n%s", passedOnQueuedLog, diff)
	}
}

func TestPassedOnAnswerThatNeverFindsTheStoreReadableIsNotPassedOnAndGivesUpOnce(t *testing.T) {
	store := &readFailingStore{Store: session.NewMemoryStore()}
	holder, _, _ := replicaOf(t, store, "holder")
	logs := &syncLogBuffer{}
	second, _, secondBrk := replicaOf(t, store, "second", capturingLogs(logs))
	state := pausedOnQuestion(t, store, holder, "task-ask")
	store.lookupFailures.Store(1 << 20)
	msg, _, _ := answerDelivery(t, regionAnswer(), "task-ask", state.UserProps.UserID, false)

	second.handleForwardedControl(t.Context(), msg)

	if !waitUntilWithin(10*time.Second, func() bool { return len(logs.records(gaveUpErrorLog)) == 1 }) {
		t.Fatalf("records %q = %d, want the retries to end in one give-up", gaveUpErrorLog, len(logs.records(gaveUpErrorLog)))
	}
	got := passOnEffects{PassedOn: len(passedOnCopies(secondBrk, second.forwardTopic())), Republished: republishedAnswers(secondBrk)}
	if diff := cmp.Diff(passOnEffects{}, got); diff != "" {
		t.Errorf("messages the second replica sent for a passed-on answer it could not check (-want +got):\n%s", diff)
	}
}
