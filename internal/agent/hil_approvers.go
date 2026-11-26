// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func (a *Agent) askForApproval(ctx context.Context, sp *StatusPublisher, hr hilRequest, toolName string, gate HILToolConfig, timeout time.Duration) (resp *a2a.UserInputResponseData, refused *tool.Result, refusal tool.ApprovalRefusal) {
	if !gate.Approvers.Configured() {
		return a.waitForUserInput(ctx, sp, hr, timeout), nil, ""
	}
	if reason := a.approverSetUnavailable(); reason != "" {
		a.logger.Warn("tool approval refused: the gate names approvers but they cannot be asked",
			"tool", toolName, "requestID", hr.requestID, "reason", reason)
		return nil, tool.Error(fmt.Sprintf("Tool %q requires approval from its configured approvers, but %s. "+
			"Denied for safety.", toolName, reason)), tool.RefusalNoApprovalRoute
	}
	resp, sent := a.waitForApprovers(ctx, sp, hr, gate.Approvers, timeout)
	if !sent {
		return nil, tool.Error(fmt.Sprintf("Tool %q requires approval from its configured approvers, but the request to them "+
			"could not be sent. Denied for safety.", toolName)), tool.RefusalPromptFailed
	}
	return resp, nil, ""
}

func approverRefusalMessage(toolName string, resp *a2a.UserInputResponseData) (message string, refused bool) {
	reason, _ := resp.Data["reason"].(string)
	switch {
	case resp.Status != "cancelled" || !isReasonCode(reason):
		return "", false
	case reason == a2a.NotifyRefusalApproverLookupDown:
		return fmt.Sprintf("Tool %q requires approval from its configured approvers, but the notification service could not look up "+
			"its approvers just now. Try again shortly. Denied for safety.", toolName), true
	case reason == a2a.NotifyRefusalApproverStoreDown:
		return fmt.Sprintf("Tool %q requires approval from its configured approvers, but the notification service could not record "+
			"the request just now. Try again shortly. Denied for safety.", toolName), true
	case reason == a2a.NotifyRefusalDeliveryFailed:
		return fmt.Sprintf("Tool %q requires approval from its configured approvers, but none of them could be reached. "+
			"Denied for safety.", toolName), true
	default:
		return fmt.Sprintf("Tool %q requires approval from its configured approvers, but the notification service refused "+
			"the request (%s). Denied for safety.", toolName, reason), true
	}
}

const maxReasonCodeLen = 64

func isReasonCode(s string) bool {
	if s == "" || len(s) > maxReasonCodeLen {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	return true
}

func (a *Agent) approverSetUnavailable() string {
	reg := a.svc.FeatureRegistry()
	switch {
	case reg == nil || !reg.IsEnabled(features.KeyHILApproverSets):
		return "approver sets are not enabled on this agent (hil_approver_sets)"
	case !a.notifyHILEnabled():
		return "the notification service is not enabled on this agent (notify_service)"
	default:
		return ""
	}
}

func (a *Agent) waitForApprovers(ctx context.Context, sp *StatusPublisher, hr hilRequest, approvers tool.HILApprovers, timeout time.Duration) (resp *a2a.UserInputResponseData, sent bool) {
	ch := a.registerPendingHIL(hr.requestID, pendingHILRequest{ownerUserID: hr.recipientUserID, taskID: sp.TaskID(), approverSet: true})
	defer a.unregisterHILRequest(hr.requestID)

	expiresAt := time.Now().Add(timeout).UTC().Format(time.RFC3339)
	req, err := approverSetNotifyRequest(a.cfg.Namespace, a.cfg.Name, hr, approvers, expiresAt)
	if err != nil {
		a.logger.Error("tool approval: could not build the approver request; refusing the call",
			"requestID", hr.requestID, "error", err)
		return nil, false
	}
	if a.publishNotification(ctx, req) != nil {
		return nil, false
	}
	sp.PublishProgressUpdate(ctx, "Waiting for one of the configured approvers to answer.")
	a.logger.Info("tool approval: waiting on an approver set",
		"requestID", hr.requestID, "approverCount", len(approvers.Users), "placeCount", len(approvers.Places))
	resp = awaitHILResponse(ctx, ch, hr.requestID, timeout)
	if ctx.Err() != nil {
		a.withdrawApproval(ctx, req)
	}
	return resp, true
}

const withdrawApprovalTimeout = 5 * time.Second

func (a *Agent) withdrawApproval(ctx context.Context, req a2a.NotifyRequest) {
	payload, err := json.Marshal(a2a.UserInputResponseData{
		Type: string(a2a.SignalUserInputResponse), RequestID: req.RequestID, Status: a2a.HILStatusWithdrawn,
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), withdrawApprovalTimeout)
	defer cancel()
	msg := &broker.Message{Payload: payload, UserProperties: map[string]string{a2a.PropReplyTo: req.RequesterAddr}}
	if err := a.svc.Broker().PublishGuaranteed(ctx, a2a.NotifyReplyTopic(a.cfg.Namespace, req.RequestID), msg); err != nil {
		a.logger.Warn("tool approval: could not withdraw the prompt of an ended run; it stays open until it expires",
			"requestID", req.RequestID, "error", err)
		return
	}
	a.logger.Info("tool approval: prompt withdrawn because the run ended", "requestID", req.RequestID)
}

func approverSetNotifyRequest(namespace, agentName string, hr hilRequest, approvers tool.HILApprovers, expiresAt string) (a2a.NotifyRequest, error) {
	set := &a2a.NotifyApprovers{UserIDs: approvers.Users}
	for _, p := range approvers.Places {
		address, err := json.Marshal(p.Address)
		if err != nil {
			return a2a.NotifyRequest{}, err
		}
		set.Places = append(set.Places, a2a.NotifyPlace{ChannelType: p.ChannelType, Address: address})
	}
	metadata := map[string]any{"source": hr.source}
	if hr.traceID != "" {
		metadata["trace_id"] = hr.traceID
	}
	return a2a.NotifyRequest{
		RequestID:     hr.requestID,
		Mode:          a2a.NotifyModeInput,
		SenderUserID:  hr.recipientUserID,
		SenderAgent:   agentName,
		Category:      a2a.CategoryHIL,
		RequesterAddr: a2a.NotifyResponseTopic(namespace, agentName, hr.requestID),
		Title:         "Your approval is requested",
		Surface:       hr.surface,
		ExpiresAt:     expiresAt,
		Metadata:      metadata,
		Approvers:     set,
	}, nil
}
