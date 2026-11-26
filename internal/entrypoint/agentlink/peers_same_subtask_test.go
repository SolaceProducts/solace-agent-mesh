// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const otherAgentName = "billing-assistant"

func TestTwoLinkAgentsThatChooseTheSameSubtaskIdTowardOnePeerDoNotCollide(t *testing.T) {
	const otherToken = "s3cret-other-token"
	r := newRigAgents(t, []any{
		map[string]any{"name": agentName, "token_sha256": digest(token), "peers": []any{peerName}},
		map[string]any{"name": otherAgentName, "token_sha256": digest(otherToken), "peers": []any{peerName}},
	}, nil)
	first := r.connect()
	r.consume(first)
	second := r.connectAs(otherAgentName, otherToken)
	r.consumeAs(second, otherAgentName)
	r.taskOn(first, "T1", nil)
	r.submitTo(otherAgentName, "T2", "tok:alice:T2", nil)
	asked := second.next(typeTask)
	second.send(map[string]any{"type": typeAck, "delivery": asked.Delivery})

	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	first.send(peerCall("S1", peerName, "How many claims are open?"))
	firstRequest := recv(t, requests)
	second.send(map[string]any{"type": typePeerCall, "parentTaskId": "T2", "subtaskId": "S1", "peer": peerName, "text": "How many invoices are open?"})
	secondRequest := recv(t, requests)

	firstID, secondID := peerRequestID(t, firstRequest), peerRequestID(t, secondRequest)
	firstReplyTo, secondReplyTo := a2a.DecodeUserProperties(firstRequest.UserProperties).ReplyTo, a2a.DecodeUserProperties(secondRequest.UserProperties).ReplyTo
	if firstID == secondID || firstReplyTo == secondReplyTo {
		t.Fatalf("the two calls share a peer-side id or reply topic: %s on %s, %s on %s", firstID, firstReplyTo, secondID, secondReplyTo)
	}
	if firstReplyTo != a2a.PeerResponseTopic(testNS, agentName, firstID) || secondReplyTo != a2a.PeerResponseTopic(testNS, otherAgentName, secondID) {
		t.Fatalf("reply topics %s and %s do not end in the requests' own ids %s and %s", firstReplyTo, secondReplyTo, firstID, secondID)
	}

	publishOn(t, r.brk, secondReplyTo, peerReply(secondID, a2a.TaskStateCompleted, a2a.NewTextPart("Three invoices")))
	publishOn(t, r.brk, firstReplyTo, peerReply(firstID, a2a.TaskStateCompleted, a2a.NewTextPart("Seven claims")))
	if f := first.next(typePeerResult); f.SubtaskID != "S1" || f.State != stateCompleted || f.Text == nil || *f.Text != "Seven claims" {
		t.Fatalf("the first agent got %s", f.Raw)
	}
	if f := second.next(typePeerResult); f.SubtaskID != "S1" || f.State != stateCompleted || f.Text == nil || *f.Text != "Three invoices" {
		t.Fatalf("the second agent got %s", f.Raw)
	}
	first.quiet()
	second.quiet()
}

func publishOn(t *testing.T, brk broker.Broker, topic string, payload json.RawMessage) {
	t.Helper()
	if err := brk.Publish(t.Context(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatal(err)
	}
}
