// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

const (
	timeoutSweepInterval = 15 * time.Second
	timeoutSweepBatch    = 256
	claimMaxAttempts     = 4
	replyMaxRedelivery   = 3
)

func (n *NotifyCore) startHILRouter(loopCtx context.Context) error {
	brk := n.svc.Broker()
	queueName := a2a.QueueName(n.cfg.Namespace, "notify", "reply")
	q, err := brk.CreateQueue(queueName, n.replyQueueOptions())
	if err != nil {
		return fmt.Errorf("create notify reply queue %q: %w", queueName, err)
	}
	if err := brk.Subscribe(q, a2a.NotifyReplySubscription(n.cfg.Namespace)); err != nil {
		return fmt.Errorf("subscribe notify replies: %w", err)
	}
	n.wg.Go(func() { n.replyLoop(loopCtx, q) })
	n.wg.Go(func() { n.timeoutSweepLoop(loopCtx, timeoutSweepInterval, leftoverCardGrace) })
	n.logger.Info("notify HIL router started", "replyQueue", queueName)
	return nil
}

func (n *NotifyCore) replyQueueOptions() broker.QueueOptions {
	opts := broker.QueueOptions{Named: true, Exclusive: false}
	if n.approverSetsEnabled() {
		opts.MaxRedelivery = replyMaxRedelivery
	}
	return opts
}

func (n *NotifyCore) handleInputRequest(ctx context.Context, req a2a.NotifyRequest, refuseNow bool) (retry bool) {
	if req.Approvers != nil {
		return n.handleApproverSetRequest(ctx, req, refuseNow)
	}
	req, _ = n.dispatcher.ResolveRecipient(ctx, req)
	if req.RequesterAddr == "" {
		n.logger.Error("notify: input-mode request without requester_addr; cannot route a reply",
			"requestID", req.RequestID, "recipient", recipientKey(req))
		return false
	}
	expiresAt := parseExpiresAtMillis(req.ExpiresAt)
	if req.ExpiresAt != "" && expiresAt == nil {
		n.logger.Warn("notify: HIL request has unparseable expires_at; treating as no deadline",
			"requestID", req.RequestID, "expiresAt", req.ExpiresAt)
	}
	pr := store.PendingRequest{
		RequestID:       req.RequestID,
		RequesterAddr:   req.RequesterAddr,
		RecipientUserID: recipientKey(req),
		ExpiresAt:       expiresAt,
	}
	if req.Dedup != "" {
		pr.Dedup = &req.Dedup
	}
	switch err := n.insertPendingWithRetry(ctx, pr); {
	case errors.Is(err, store.ErrDuplicate):
		n.logger.Info("notify: duplicate HIL request dropped (dedup)",
			"requestID", req.RequestID, "dedup", req.Dedup)
		return false
	case err != nil:
		n.logger.Error("notify: failed to persist HIL pending request; returning it to the queue",
			"requestID", req.RequestID, "error", err)
		return true
	}

	records := n.dispatcher.Deliver(ctx, req)
	switch summarizeDeliveries(records) {
	case "delivered", "partial":
	default:
		n.logger.Error("notify: HIL prompt undeliverable; failing the request",
			"requestID", req.RequestID, "recipient", recipientKey(req), "deliveryCount", len(records))
		n.failUndeliverable(ctx, req.RequestID)
	}
	return false
}

func (n *NotifyCore) replyLoop(ctx context.Context, q broker.Queue) {
	const maxBackoff = 5 * time.Second
	backoff := 50 * time.Millisecond
	for {
		msg, err := n.svc.Broker().Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			n.logger.Warn("notify: reply receive error; backing off", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, maxBackoff)
			continue
		}
		backoff = 50 * time.Millisecond
		if msg == nil {
			continue
		}
		n.handleReply(ctx, msg)
	}
}

func (n *NotifyCore) handleReply(ctx context.Context, msg *broker.Message) {
	defer func() {
		if r := recover(); r != nil {
			n.logger.Error("notify: panic handling HIL reply; acking to avoid poison-pill redelivery",
				"panic", r, "topic", msg.Topic)
			msg.Ack()
		}
	}()

	var resp a2a.UserInputResponseData
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		n.logger.Error("notify: malformed HIL reply; dropping", "error", err, "topic", msg.Topic)
		msg.Ack()
		return
	}
	if resp.RequestID == "" {
		n.logger.Error("notify: HIL reply missing request_id; dropping", "topic", msg.Topic)
		msg.Ack()
		return
	}
	var pr *store.PendingRequest
	err := n.retryTransient(ctx, "look up HIL request", resp.RequestID, func() (gerr error) {
		pr, gerr = n.pending.Get(ctx, resp.RequestID)
		return gerr
	})
	if err != nil {
		n.logger.Error("notify: lookup on HIL reply failed after retries; nacking for redelivery",
			"requestID", resp.RequestID, "error", err)
		n.tellAnswererOnFirstDelivery(ctx, msg, resp)
		msg.Nack()
		return
	}
	n.dispatchReply(ctx, msg, resp, pr)
}

func (n *NotifyCore) dispatchReply(ctx context.Context, msg *broker.Message, resp a2a.UserInputResponseData, pr *store.PendingRequest) {
	switch {
	case resp.Status == a2a.HILStatusDelivered:
		if !n.recordCard(ctx, msg, resp, pr) {
			msg.Nack()
			return
		}
		msg.Ack()
		return
	case resp.Status == a2a.HILStatusWithdrawn && pr != nil && pr.Approvers != nil:
		n.withdraw(ctx, msg, resp.RequestID, pr)
		return
	case pr == nil && msg.UserProperties[a2a.PropNotifyCard] != "":
		n.logger.Info("notify: HIL answer for a request no longer on record; telling the answerer", "requestID", resp.RequestID)
		n.tellClicker(ctx, msg, resp.RequestID, a2a.NotifyPromptOutcome{Refusal: msgNoLongerOpen})
		msg.Ack()
		return
	}
	if pr != nil && pr.Approvers != nil {
		n.handleApproverReply(ctx, msg, resp, pr)
		return
	}

	addr, claimed, err := n.claimReplyWithRetry(ctx, resp.RequestID, resp.Status)
	if err != nil {
		n.logger.Error("notify: claim on HIL reply failed after retries; nacking for redelivery",
			"requestID", resp.RequestID, "error", err)
		msg.Nack()
		return
	}
	if !claimed {
		n.unclaimedReply(ctx, msg, resp)
		return
	}
	if rerr := n.routeResponse(ctx, addr, resp); rerr != nil {
		if oerr := n.pending.Reopen(ctx, resp.RequestID, resp.Status); oerr != nil {
			n.logger.Error("notify: failed to re-open HIL request after route failure",
				"requestID", resp.RequestID, "error", oerr)
		}
		msg.Nack()
		return
	}
	msg.Ack()
}

func (n *NotifyCore) claimReplyWithRetry(ctx context.Context, requestID, status string) (addr string, claimed bool, err error) {
	return retryClaim(ctx, func() (string, bool, error) {
		return n.pending.ClaimResolution(ctx, requestID, store.SingleRecipientOnly(), store.Resolution{Status: status})
	})
}

func (n *NotifyCore) unclaimedReply(ctx context.Context, msg *broker.Message, resp a2a.UserInputResponseData) {
	var pr *store.PendingRequest
	err := n.retryTransient(ctx, "look up HIL request", resp.RequestID, func() (gerr error) {
		pr, gerr = n.pending.Get(ctx, resp.RequestID)
		return gerr
	})
	switch {
	case err != nil:
		n.logger.Error("notify: lookup on HIL reply failed after retries; nacking for redelivery", "requestID", resp.RequestID, "error", err)
		n.tellAnswererOnFirstDelivery(ctx, msg, resp)
		msg.Nack()
	case pr != nil && pr.Approvers != nil:
		n.handleApproverReply(ctx, msg, resp, pr)
	case pr == nil:
		n.logger.Info("notify: HIL reply for a request not on record; dropping", "requestID", resp.RequestID, "responseStatus", resp.Status)
		msg.Ack()
	default:
		n.logger.Info("notify: HIL reply for an already-resolved request; dropping", "requestID", resp.RequestID, "responseStatus", resp.Status)
		msg.Ack()
	}
}

func (n *NotifyCore) tellAnswererOnFirstDelivery(ctx context.Context, msg *broker.Message, resp a2a.UserInputResponseData) {
	if resp.Status == a2a.HILStatusDelivered || resp.Status == a2a.HILStatusWithdrawn {
		return
	}
	n.tellOnFirstDelivery(ctx, msg, resp.RequestID, msgNotRecorded)
}

const transientAttempts = claimMaxAttempts

func (n *NotifyCore) retryTransient(ctx context.Context, operation, requestID string, op func() error) error {
	backoff := 50 * time.Millisecond
	var err error
	for attempt := 1; attempt <= transientAttempts; attempt++ {
		if err = op(); err == nil {
			return nil
		}
		if attempt == transientAttempts {
			break
		}
		n.logger.Warn("notify: transient failure; retrying",
			"operation", operation, "requestID", requestID, "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return err
}

func retryClaim(ctx context.Context, claim func() (string, bool, error)) (addr string, claimed bool, err error) {
	backoff := 50 * time.Millisecond
	for attempt := 0; attempt < claimMaxAttempts; attempt++ {
		addr, claimed, err = claim()
		if err == nil {
			return addr, claimed, nil
		}
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return "", false, err
}

func (n *NotifyCore) timeoutSweepLoop(ctx context.Context, interval, cardGrace time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.sweepTick(ctx, cardGrace)
		}
	}
}

func (n *NotifyCore) sweepTick(ctx context.Context, cardGrace time.Duration) {
	n.sweepTimeouts(ctx)
	n.settleLeftoverCards(ctx, time.Now().Add(-cardGrace).UnixMilli())
}

func (n *NotifyCore) sweepTimeouts(ctx context.Context) {
	now := time.Now().UnixMilli()
	rows, err := n.pending.Expired(ctx, now, timeoutSweepBatch)
	if err != nil {
		n.logger.Warn("notify: timeout sweep query failed", "error", err)
		return
	}
	for _, pr := range rows {
		addr, claimed, err := n.pending.Claim(ctx, pr.RequestID, "timed_out")
		if err != nil {
			n.logger.Warn("notify: timeout claim failed", "requestID", pr.RequestID, "error", err)
			continue
		}
		if !claimed {
			continue
		}
		if rerr := n.routeResponse(ctx, addr, a2a.UserInputResponseData{
			RequestID: pr.RequestID,
			Status:    "timed_out",
		}); rerr != nil {
			if oerr := n.pending.Reopen(ctx, pr.RequestID, "timed_out"); oerr != nil {
				n.logger.Error("notify: failed to re-open HIL request after timeout-route failure",
					"requestID", pr.RequestID, "error", oerr)
			}
			continue
		}
		n.logger.Info("notify: HIL request timed out", "requestID", pr.RequestID)
		if pr.Approvers != nil {
			var rec approverRecord
			if json.Unmarshal([]byte(*pr.Approvers), &rec) == nil {
				n.auditApproval(ctx, auth.HILApprovalEvent{RequestID: pr.RequestID, Outcome: auth.HILApprovalTimedOut}, rec)
				n.announceResolution(ctx, pr.RequestID, rec, nil, a2a.NotifyOutcomeExpired, "")
			}
		}
	}
}

func (n *NotifyCore) routeResponse(ctx context.Context, addr string, resp a2a.UserInputResponseData) error {
	if resp.Type == "" {
		resp.Type = string(a2a.SignalUserInputResponse)
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		n.logger.Error("notify: marshal HIL response failed", "requestID", resp.RequestID, "error", err)
		return fmt.Errorf("marshal HIL response: %w", err)
	}
	if err := n.svc.Broker().PublishGuaranteed(ctx, addr, &broker.Message{Payload: payload}); err != nil {
		n.logger.Error("notify: route HIL response failed",
			"requestID", resp.RequestID, "addr", addr, "error", err)
		return fmt.Errorf("publish HIL response: %w", err)
	}
	n.logger.Info("notify: routed HIL response to requester",
		"requestID", resp.RequestID, "responseStatus", resp.Status)
	return nil
}

func parseExpiresAtMillis(s string) *int64 {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

const pendingInsertAttempts = 4

func (n *NotifyCore) insertPendingWithRetry(ctx context.Context, pr store.PendingRequest) error {
	backoff := 100 * time.Millisecond
	var err error
	for attempt := range pendingInsertAttempts {
		if err = n.pending.Insert(ctx, pr); err == nil || errors.Is(err, store.ErrDuplicate) {
			return err
		}
		if attempt == pendingInsertAttempts-1 {
			break
		}
		n.logger.Warn("notify: storing a HIL pending request failed; retrying",
			"requestID", pr.RequestID, "attempt", attempt+1, "error", err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return err
}
