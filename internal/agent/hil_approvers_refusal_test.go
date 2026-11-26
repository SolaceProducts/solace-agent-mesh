// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type notifyPublishFailsBroker struct {
	broker.Broker
}

func (b notifyPublishFailsBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if strings.Contains(topic, "/notify/request/") {
		return errors.New("broker unavailable")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

func approvalOutcomeWithin(t *testing.T, agent *Agent, gate HILToolConfig) approvalOutcome {
	t.Helper()
	gate.Timeout = 5 * time.Second
	done := make(chan approvalOutcome, 1)
	go func() {
		result, refusal := agent.checkToolApproval(t.Context(), &StatusPublisher{}, "reroute_shipment",
			map[string]any{"shipment": "S-1"}, gate, &tool.Context{UserID: approverTaskOwner})
		done <- approvalOutcome{result, refusal}
	}()
	select {
	case out := <-done:
		return out
	case <-time.After(4 * time.Second):
		t.Fatal("the approval gate did not settle")
		return approvalOutcome{}
	}
}

func TestApproverGate_ARefusalFromTheNotificationServiceReachesTheModelWithItsReason(t *testing.T) {
	brk := connectedBroker(t)
	startNotifyService(t, brk, approverRegistry(true, false))
	agent := startApproverAgent(t, brk, approverRegistry(true, true))

	out := approvalOutcomeWithin(t, agent, gateWithApprovers())

	if out.result == nil || out.refusal != tool.RefusalNoApprovalRoute {
		t.Fatalf("outcome = (%+v, %q), want a refusal for having no approval route", out.result, out.refusal)
	}
	if strings.Contains(out.result.Message, "cancelled by user") || !strings.Contains(out.result.Message, "approver_sets_disabled") {
		t.Errorf("message = %q, want the notification service's reason and no claim that a person cancelled", out.result.Message)
	}
}

func TestApproverGate_AnApproverCancellingStillReadsAsCancelledByUser(t *testing.T) {
	brk := connectedBroker(t)
	reg := approverRegistry(true, true)
	startNotifyService(t, brk, reg)
	agent := startApproverAgent(t, brk, reg)
	done := make(chan approvalOutcome, 1)
	go func() {
		result, refusal := agent.checkToolApproval(t.Context(), &StatusPublisher{}, "reroute_shipment",
			map[string]any{"shipment": "S-1"}, gateWithApprovers(), &tool.Context{UserID: approverTaskOwner})
		done <- approvalOutcome{result, refusal}
	}()
	requestID, _ := pendingApprovalID(t, agent)
	cancel, _ := json.Marshal(a2a.UserInputResponseData{RequestID: requestID, Status: "cancelled"})

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := brk.PublishGuaranteed(t.Context(), a2a.NotifyReplyTopic("test-ns", requestID), &broker.Message{
			Payload: cancel, UserProperties: map[string]string{a2a.PropUserID: approverBob, a2a.PropChannel: "slack"},
		}); err != nil {
			t.Fatalf("publish bob's cancellation of %s: %v", requestID, err)
		}
		select {
		case out := <-done:
			if out.result == nil || out.refusal != tool.RefusalCancelled || !strings.Contains(out.result.Message, "cancelled by user") {
				t.Fatalf("outcome = (%+v, %q), want the approver's own cancellation", out.result, out.refusal)
			}
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	t.Fatal("the approver's cancellation never reached the gate")
}

func TestApproverGate_APublishFailureIsNotReportedAsACancellation(t *testing.T) {
	brk := connectedBroker(t)
	agent := startApproverAgent(t, notifyPublishFailsBroker{Broker: brk}, approverRegistry(true, true))

	out := approvalOutcomeWithin(t, agent, gateWithApprovers())

	if out.result == nil || out.refusal != tool.RefusalPromptFailed {
		t.Fatalf("outcome = (%+v, %q), want a refusal because the prompt could not be sent", out.result, out.refusal)
	}
	if strings.Contains(out.result.Message, "cancelled by user") {
		t.Errorf("message = %q, which blames a person for a publish failure", out.result.Message)
	}
}

func TestApproverRefusalMessage_ALookupOutageIsSaidToBeTemporary(t *testing.T) {
	resp := &a2a.UserInputResponseData{Status: "cancelled", Data: map[string]any{"reason": "approver_lookup_unavailable"}}

	message, refused := approverRefusalMessage("reroute_shipment", resp)

	if !refused || !strings.Contains(message, "Try again shortly") || strings.Contains(message, "refused the request") {
		t.Errorf("message = %q (refused %v), want a temporary-outage message rather than a policy refusal", message, refused)
	}
}

func TestApproverRefusalMessage_AStoreOutageIsSaidToBeTemporary(t *testing.T) {
	resp := &a2a.UserInputResponseData{Status: "cancelled", Data: map[string]any{"reason": "approver_store_unavailable"}}

	message, refused := approverRefusalMessage("reroute_shipment", resp)

	if !refused || !strings.Contains(message, "Try again shortly") || strings.Contains(message, "refused the request") {
		t.Errorf("message = %q (refused %v), want a temporary-outage message rather than a policy refusal", message, refused)
	}
}
