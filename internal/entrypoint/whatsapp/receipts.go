// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const metricWhatsAppDeliveryStatus = "whatsapp_delivery_status"

func receiptStateFor(status string) (string, bool) {
	switch strings.ToLower(status) {
	case "sent":
		return store.WhatsAppOutboundSent, true
	case "delivered":
		return store.WhatsAppOutboundDelivered, true
	case "read":
		return store.WhatsAppOutboundRead, true
	case "failed", "undelivered":
		return store.WhatsAppOutboundFailed, true
	default:
		return "", false
	}
}

func receiptFailureDetail(errs []statusError) (int, string) {
	if len(errs) == 0 {
		return 0, ""
	}
	first := errs[0]
	detail := first.ErrorData.Details
	if detail == "" {
		detail = first.Message
	}
	if detail == "" {
		detail = first.Title
	}
	return first.Code, detail
}

func (a *Adapter) handleStatus(value *webhookChangeValue, status *messageStatus) {
	if a.forDifferentNumber(value) {
		a.logForeignNumberDrop("ignoring status for a different phone number", status.ID, value)
		return
	}
	state, known := receiptStateFor(status.Status)
	if !known {
		a.logger.Debug("ignoring unrecognized whatsapp message status",
			"wamid", status.ID, "messageStatus", status.Status)
		return
	}
	code, detail := receiptFailureDetail(status.Errors)

	moved, row, err := a.outbound.UpdateFromReceipt(a.ctx, a.gatewayID(),
		store.WhatsAppReceipt{Wamid: status.ID, State: state, ErrorCode: code, ErrorDetail: detail},
		nowMillis())
	if err != nil {
		a.logger.Error("failed to record whatsapp delivery receipt",
			"wamid", status.ID, "messageStatus", state, "error", err)
		return
	}
	if row == nil {
		a.logger.Debug("delivery receipt for an unknown message",
			"wamid", status.ID, "messageStatus", state)
		return
	}
	if !moved {
		a.logger.Debug("ignoring out-of-order or duplicate delivery receipt",
			"wamid", status.ID, "messageStatus", state, "rowState", row.State, "outboundID", row.ID)
		return
	}

	a.meter().Counter(metricWhatsAppDeliveryStatus, 1, "gatewayId", a.gatewayID(), "state", state)
	fields := []any{
		"wamid", status.ID,
		"messageStatus", state,
		"outboundID", row.ID,
		"kind", row.Kind,
		"taskID", row.TaskID,
		"traceID", row.TraceID,
		"userFP", strutil.PIIFingerprint("whatsapp:" + status.RecipientID),
		"conversationID", status.Conversation.ID,
	}
	if state == store.WhatsAppOutboundFailed {
		a.logger.Error("whatsapp message delivery failed",
			append(fields, "metaErrorCode", code, "errorDetailLen", len(detail))...)
		a.onDeliveryFailed(row, true)
		return
	}
	a.logger.Debug("whatsapp delivery receipt recorded", fields...)
}
