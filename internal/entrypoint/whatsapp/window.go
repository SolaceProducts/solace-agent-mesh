// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const conversationWindow = 24 * time.Hour

const windowSkewMargin = 5 * time.Minute

const parkTTL = 24 * time.Hour

const (
	parkRetireDisplaced       = "displaced"
	parkRetireStale           = "stale"
	parkRetireQuestionExpired = "question_expired"
	parkRetireUnreadable      = "unreadable"
	parkRetireYielded         = "yielded"
)

const (
	metricWhatsAppParkedReplies    = "whatsapp_parked_replies"
	metricWhatsAppParkedSuperseded = "whatsapp_parked_superseded"
)

const sessionEpochUnavailable int64 = 0

var errWindowClosed = errors.New("whatsapp customer-service window closed")

func isWindowClosed(err error) bool {
	if errors.Is(err, errWindowClosed) {
		return true
	}
	ge, ok := errors.AsType[*graphAPIError](err)
	return ok && ge.GraphErr.Code == codeWindowClosed
}

func yieldsToOwnTask(row *store.WhatsAppOutbound) string {
	if row.Kind != kindMedia || row.ArtifactRef == "" {
		return ""
	}
	return row.TaskID
}

func contactPhone(value *webhookChangeValue, msg *inboundMessage) string {
	c, _ := contactFor(value, msg)
	if c == nil {
		return ""
	}
	return c.WaID
}

func (a *Adapter) recordInbound(ctx context.Context, value *webhookChangeValue, msg *inboundMessage) int64 {
	nowMs := a.nowMs()
	epoch, err := a.conversations.RecordInbound(ctx, &store.WhatsAppConversation{
		GatewayID:     a.gatewayID(),
		SenderID:      msg.senderID(),
		WaID:          contactPhone(value, msg),
		FromID:        msg.senderID(),
		LastInboundAt: nowMs,
		CreatedAt:     nowMs,
		UpdatedAt:     nowMs,
	}, sessionIdleReset)
	if err != nil {
		a.logger.Error("failed to record the whatsapp conversation window", "wamid", msg.ID, "error", err)
		return sessionEpochUnavailable
	}
	a.releaseParkedReply(ctx, msg.senderID(), nowMs)
	return epoch
}

func (a *Adapter) nowMs() int64 {
	if a.now != nil {
		return a.now().UnixMilli()
	}
	return nowMillis()
}

func (a *Adapter) releaseParkedReply(ctx context.Context, senderID string, nowMs int64) {
	row, err := a.outbound.GetParked(ctx, a.gatewayID(), senderID)
	if err != nil {
		a.logger.Error("failed to look up a parked whatsapp reply", "error", err)
		return
	}
	if row == nil {
		return
	}

	if retire := parkRetireReason(row, nowMs); retire != "" {
		a.retirePark(ctx, row, retire, nowMs)
		return
	}
	moved, err := a.outbound.Unpark(ctx, row.ID, nowMs)
	if err != nil {
		a.logger.Error("failed to release a parked whatsapp reply",
			"outboundID", row.ID, "taskID", row.TaskID, "error", err)
		return
	}
	if !moved {
		return
	}
	a.logger.Info("released a parked whatsapp reply now the customer-service window has reopened",
		"outboundID", row.ID, "taskID", row.TaskID, "traceID", row.TraceID,
		"kind", row.Kind, "parkedForMs", nowMs-row.ParkedAt)
	a.meter().Counter(metricWhatsAppParkedReplies, 1, "gatewayId", a.gatewayID(), "outcome", "released")
	a.wakeSender(senderID)
}

func (a *Adapter) retirePark(ctx context.Context, row *store.WhatsAppOutbound, reason string, nowMs int64) {
	retired, err := a.outbound.SupersedePark(ctx, row.ID, nowMs)
	if err != nil {
		a.logger.Error("failed to retire a parked whatsapp reply",
			"outboundID", row.ID, "taskID", row.TaskID, "error", err)
		return
	}
	if !retired {
		return
	}
	a.logger.Info("retired a parked whatsapp reply without delivering it",
		"outboundID", row.ID, "taskID", row.TaskID, "traceID", row.TraceID,
		"kind", row.Kind, "reason", reason, "parkedForMs", nowMs-row.ParkedAt)
	a.meter().Counter(metricWhatsAppParkedSuperseded, 1, "gatewayId", a.gatewayID(), "reason", reason)
	a.tellAgentAParkedPromptDied(row)
}

func parkRetireReason(row *store.WhatsAppOutbound, nowMs int64) string {
	if nowMs-row.ParkedAt > parkTTL.Milliseconds() || staleForDelivery(row, nowMs) {
		return parkRetireStale
	}
	var p outboundPayload
	if err := json.Unmarshal([]byte(row.Payload), &p); err != nil {
		return parkRetireUnreadable
	}
	if p.Prompt != nil && p.Prompt.ExpiresAt > 0 && p.Prompt.ExpiresAt <= nowMs {
		return parkRetireQuestionExpired
	}
	return ""
}

func wasParked(row *store.WhatsAppOutbound) bool { return row.ParkedAt > 0 }

func staleForDelivery(row *store.WhatsAppOutbound, nowMs int64) bool {
	return nowMs-row.CreatedAt > parkTTL.Milliseconds()
}

func (a *Adapter) sweepStaleParks() {
	nowMs := a.nowMs()
	ctx, cancel := a.settleCtx()
	defer cancel()
	rows, err := a.outbound.ListStaleParks(ctx, a.gatewayID(), nowMs-parkTTL.Milliseconds(), promptSweepBatch)
	if err != nil {
		a.logger.Error("failed to list stale parked whatsapp replies", "error", err)
		return
	}
	for i := range rows {
		if a.stopped.Load() {
			return
		}
		a.retirePark(ctx, &rows[i], parkRetireStale, nowMs)
	}
}

func (a *Adapter) windowOpen(ctx context.Context, recipient string) bool {
	conv, err := a.conversations.Get(ctx, a.gatewayID(), recipient)
	if err != nil {
		a.logger.Warn("failed to read the whatsapp conversation window; sending and letting Meta decide", "error", err)
		return true
	}
	if conv == nil {
		return true
	}
	return a.nowMs()-conv.LastInboundAt < (conversationWindow - windowSkewMargin).Milliseconds()
}
