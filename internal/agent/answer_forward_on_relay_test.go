// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestRelayedAnswerReachesAHolderThatOnlyListensOnTheForwardTopic(t *testing.T) {
	store := session.NewMemoryStore()
	holder, holderLLM, holderBrk := replicaOf(t, store, "holder")
	other, _, otherBrk := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	msg, req, counts := answerDelivery(t, regionAnswer(), "task-ask", state.UserProps.UserID, false)

	other.processMessage(t.Context(), msg, req)

	if counts.acked.Load() != 1 || counts.nacked.Load() != 0 {
		t.Fatalf("delivery: acked %d, nacked %d, want 1 and 0", counts.acked.Load(), counts.nacked.Load())
	}
	passedOn := publishedOn(otherBrk, other.forwardTopic())
	if len(passedOn) != 1 {
		t.Fatalf("answers passed on the forward topic = %d, want 1 for a holder that does not listen on the notify topic", len(passedOn))
	}
	holder.handleForwardedControl(t.Context(), &broker.Message{Topic: other.forwardTopic(), Payload: passedOn[0].payload, UserProperties: passedOn[0].userProps})
	assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-ask", "EMEA")
}

func TestAnswerTheHolderAppliesItselfIsNotPassedOn(t *testing.T) {
	store := session.NewMemoryStore()
	holder, _, holderBrk := replicaOf(t, store, "holder")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	swallowResumes(t)
	msg, req, _ := answerDelivery(t, regionAnswer(), "task-ask", state.UserProps.UserID, false)

	holder.processMessage(t.Context(), msg, req)

	if passedOn := publishedOn(holderBrk, holder.forwardTopic()); len(passedOn) != 0 {
		t.Errorf("answers passed on by the holder = %d, want 0 for an answer it applied itself", len(passedOn))
	}
}
