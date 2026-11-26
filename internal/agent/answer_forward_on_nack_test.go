// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestAnswerThatCannotBeRecordedIsPassedToTheLiveHolderBeforeItIsNacked(t *testing.T) {
	store := &flakyStore{Store: session.NewMemoryStore()}
	holder, holderLLM, holderBrk := replicaOf(t, store, "holder")
	other, _, otherBrk := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	store.recordFailures.Store(orphanAdoptionMaxAttempts)
	wire, req := controlRequestOnTheWire(t, "task-ask", state.UserProps.UserID, regionAnswer())
	var forwardedAtNack, nacks atomic.Int32
	msg := broker.NewDeliveredMessage("delivery-task-ask", wire.Topic, wire.Payload, func() {}, func() {
		nacks.Add(1)
		forwardedAtNack.Store(int32(len(publishedOn(otherBrk, other.forwardTopic()))))
	})
	msg.UserProperties = wire.UserProperties

	other.processMessage(t.Context(), msg, req)

	if nacks.Load() != 1 || forwardedAtNack.Load() != 1 {
		t.Fatalf("nacks = %d with %d answers passed on by then, want 1 nack after 1 answer was passed on", nacks.Load(), forwardedAtNack.Load())
	}
	passedOn := publishedOn(otherBrk, other.forwardTopic())[0]
	holder.handleForwardedControl(t.Context(), &broker.Message{Topic: other.forwardTopic(), Payload: passedOn.payload, UserProperties: passedOn.userProps})
	assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-ask", "EMEA")
}
