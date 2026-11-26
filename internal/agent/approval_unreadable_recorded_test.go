// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

func TestApprovalThatFindsItsCallResolvedAndCannotReadTheRecordedAnswerDoesNotRun(t *testing.T) {
	store := &flakyStore{Store: session.NewMemoryStore()}
	holder, mock, brk, holderRuns := approvalReplica(t, store, "holder", time.Hour)
	other, _, _, otherRuns := approvalReplica(t, store, "other", time.Hour)
	_, requestID := startGatedTask(t, holder, mock, "task-deny-then-approve")
	if got := other.routeUserInputResponse(t.Context(), approve(requestID, false), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("the denial was not recorded: delivery = %v", got)
	}
	store.checkpointFailures.Store(1 << 20)

	if got := holder.routeUserInputResponse(t.Context(), approve(requestID, true), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("the second answer was not delivered to the holder: delivery = %v", got)
	}

	assertResumedToCompletion(t, holder, mock, brk, "task-deny-then-approve", approvalAnswerUnreadableMessage)
	if holderRuns.Load()+otherRuns.Load() != 0 {
		t.Errorf("a call whose recorded answer was a denial ran %d times after a second, approving answer", holderRuns.Load()+otherRuns.Load())
	}
}

func TestApprovalThatFindsItsCallResolvedWithNoRecordedAnswerDoesNotRun(t *testing.T) {
	store := session.NewMemoryStore()
	holder, mock, brk, runs := approvalReplica(t, store, "holder", time.Hour)
	_, requestID := startGatedTask(t, holder, mock, "task-row-gone")
	if err := store.DeletePendingCall(t.Context(), requestID); err != nil {
		t.Fatalf("remove the call's correlation row: %v", err)
	}

	if got := holder.routeUserInputResponse(t.Context(), approve(requestID, true), hilSenderUser(concurrentUser.UserID)); got != hilDelivered {
		t.Fatalf("the answer was not delivered to the holder: delivery = %v", got)
	}

	assertResumedToCompletion(t, holder, mock, brk, "task-row-gone", approvalAnswerUnreadableMessage)
	if runs.Load() != 0 {
		t.Errorf("an approval whose call was resolved with no recorded answer ran the call %d times", runs.Load())
	}
}
