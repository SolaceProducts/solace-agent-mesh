// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

const (
	msgResolvedTitle         = "Approval request answered"
	msgNoLongerOpen          = "Your answer was not counted: this request is no longer open."
	msgAnswerTooLate         = "Your answer was not counted: this request was already settled. "
	msgNotRecorded           = "Your answer could not be recorded just now. If the card still shows buttons in a minute, please answer again."
	msgCountedPassOnRetrying = "Your answer was counted, but passing it on to the requester failed and is being retried."
	msgApproversUnreadable   = "Your answer was not counted: the approvers of this request could not be read."
	msgNotAnApprover         = "Your answer was not counted: you are not one of the approvers of this request."
	msgUnverifiedUser        = "Your answer was not counted: Agent Mesh could not tell who you are."
)

type hilCardStore interface {
	Add(ctx context.Context, requestID, channelType, card string) error
	List(ctx context.Context, requestID string) ([]store.HILCard, error)
	DeleteListed(ctx context.Context, cards []store.HILCard) error
	DeleteOrphans(ctx context.Context) (int64, error)
	ResolvedRequestIDs(ctx context.Context, resolvedBefore int64, limit int) ([]string, error)
}

func refusalNotCounted(reason string) string {
	switch reason {
	case refusalNotAnApprover, refusalSystemAnswerer:
		return msgNotAnApprover
	case refusalUnverifiedAnswerer:
		return msgUnverifiedUser
	default:
		return msgNoLongerOpen
	}
}

func answerOutcome(resp a2a.UserInputResponseData) string {
	if resp.Status == "cancelled" {
		return a2a.NotifyOutcomeCancelled
	}
	switch approved, ok := resp.Data["approved"].(bool); {
	case ok && approved:
		return a2a.NotifyOutcomeApproved
	case ok:
		return a2a.NotifyOutcomeDeclined
	default:
		return a2a.NotifyOutcomeAnswered
	}
}

func outcomeOf(status, decision string) string {
	switch {
	case decision == a2a.NotifyOutcomeWithdrawn:
		return a2a.NotifyOutcomeWithdrawn
	case status == "timed_out":
		return a2a.NotifyOutcomeExpired
	case status == "failed":
		return a2a.NotifyOutcomeFailed
	case status == "cancelled":
		return a2a.NotifyOutcomeCancelled
	case decision == a2a.NotifyOutcomeApproved, decision == a2a.NotifyOutcomeDeclined:
		return decision
	default:
		return a2a.NotifyOutcomeAnswered
	}
}

func outcomeText(outcome, name string) string {
	by := func(verb string) string {
		if name == "" {
			return verb
		}
		return verb + " by " + name
	}
	switch outcome {
	case a2a.NotifyOutcomeApproved:
		return by("Approved")
	case a2a.NotifyOutcomeDeclined:
		return by("Declined")
	case a2a.NotifyOutcomeCancelled:
		return by("Cancelled")
	case a2a.NotifyOutcomeExpired:
		return "Expired"
	case a2a.NotifyOutcomeWithdrawn:
		return "No longer needed: the run ended"
	case a2a.NotifyOutcomeFailed:
		return "Could not be delivered"
	default:
		return by("Answered")
	}
}

func approverName(u approverUser, a answerer) string {
	return cmp.Or(u.Name, emailOnly(a.userID), emailOnly(u.UserID))
}

func emailOnly(id string) string {
	if strings.Contains(id, "@") {
		return id
	}
	return ""
}

func resolvedOutcome(pr *store.PendingRequest) a2a.NotifyPromptOutcome {
	outcome := outcomeOf(pr.Status, deref(pr.Decision))
	name := cmp.Or(deref(pr.AnsweredByName), emailOnly(deref(pr.AnsweredBy)))
	return a2a.NotifyPromptOutcome{Outcome: outcome, Text: outcomeText(outcome, name)}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (n *NotifyCore) resolveCards(ctx context.Context, requestID string, settled a2a.NotifyPromptOutcome) bool {
	if n.cards == nil {
		return false
	}
	cards, err := n.cards.List(ctx, requestID)
	if err != nil {
		n.logger.Warn("notify: posted HIL prompts not read; they keep their buttons", "requestID", requestID, "error", err)
		return false
	}
	if len(cards) == 0 {
		return false
	}
	allPublished := true
	for _, c := range cards {
		if n.publishOutcome(ctx, requestID, c.ChannelType, json.RawMessage(c.Card), settled) != nil {
			allPublished = false
		}
	}
	if !allPublished {
		return true
	}
	if err := n.cards.DeleteListed(ctx, cards); err != nil {
		n.logger.Warn("notify: posted HIL prompts not forgotten; the retention sweep removes them", "requestID", requestID, "error", err)
	}
	return true
}

const leftoverCardGrace = 30 * time.Second

func (n *NotifyCore) settleLeftoverCards(ctx context.Context, resolvedBefore int64) {
	if n.cards == nil || n.pending == nil {
		return
	}
	requestIDs, err := n.cards.ResolvedRequestIDs(ctx, resolvedBefore, timeoutSweepBatch)
	if err != nil {
		n.logger.Warn("notify: unsettled HIL prompts not listed", "error", err)
		return
	}
	for _, requestID := range requestIDs {
		pr, err := n.pending.Get(ctx, requestID)
		if err != nil || pr == nil || pr.Status == "pending" {
			continue
		}
		n.resolveCards(ctx, requestID, resolvedOutcome(pr))
	}
}

func (n *NotifyCore) publishOutcome(ctx context.Context, requestID, channelType string, card json.RawMessage, settled a2a.NotifyPromptOutcome) error {
	d := a2a.NotifyDeliver{
		RequestID: requestID, Mode: a2a.NotifyModeNotify, ChannelType: channelType, Address: card, ApproverSet: true,
		Message: settled.Refusal, PromptOutcome: &settled,
	}
	if settled.Refusal == "" {
		d.Title, d.Message = msgResolvedTitle, settled.Text+"."
	}
	err := n.PublishDeliver(ctx, channelType, d)
	if err != nil {
		n.logger.Warn("notify: prompt outcome not sent to its gateway", "requestID", requestID, "channelType", channelType, "error", err)
	}
	return err
}

func (n *NotifyCore) tellClicker(ctx context.Context, msg *broker.Message, requestID string, settled a2a.NotifyPromptOutcome) {
	card, via := msg.UserProperties[a2a.PropNotifyCard], msg.UserProperties[a2a.PropChannel]
	if card == "" || !gatewayDeliverable(via) || !isJSONObject(json.RawMessage(card)) {
		return
	}
	_ = n.publishOutcome(ctx, requestID, via, json.RawMessage(card), settled)
}

func (n *NotifyCore) recordCard(ctx context.Context, msg *broker.Message, resp a2a.UserInputResponseData, pr *store.PendingRequest) (recorded bool) {
	via := msg.UserProperties[a2a.PropChannel]
	card, err := json.Marshal(resp.Data["card"])
	if n.cards == nil || pr == nil || pr.Approvers == nil || err != nil || !gatewayDeliverable(via) || !isJSONObject(card) {
		n.logger.Debug("notify: HIL delivery receipt", "requestID", resp.RequestID)
		return true
	}
	if pr.Status != "pending" && n.publishOutcome(ctx, resp.RequestID, via, card, resolvedOutcome(pr)) == nil {
		return true
	}
	addErr := n.retryTransient(ctx, "record posted card", resp.RequestID, func() error {
		return n.cards.Add(ctx, resp.RequestID, via, string(card))
	})
	if addErr != nil {
		n.logger.Error("notify: posted HIL prompt not recorded after retries; returning the receipt to the queue",
			"requestID", resp.RequestID, "channelType", via, "error", addErr)
		return false
	}
	now, err := n.pending.Get(ctx, resp.RequestID)
	switch {
	case err != nil:
		n.logger.Warn("notify: request state not re-read after recording a posted HIL prompt; the sweep settles it if the request resolved",
			"requestID", resp.RequestID, "error", err)
	case now != nil && now.Status != "pending":
		n.resolveCards(ctx, resp.RequestID, resolvedOutcome(now))
	}
	return true
}

func (n *NotifyCore) withdraw(ctx context.Context, msg *broker.Message, requestID string, pr *store.PendingRequest) {
	requester := msg.UserProperties[a2a.PropReplyTo]
	if pr == nil || pr.Status != "pending" || requester == "" || requester != pr.RequesterAddr {
		n.logger.Debug("notify: HIL withdrawal ignored", "requestID", requestID, "known", pr != nil)
		msg.Ack()
		return
	}
	_, claimed, err := n.claimResolutionWithRetry(ctx, requestID, store.RequesterAt(requester),
		store.Resolution{Status: "cancelled", Decision: a2a.NotifyOutcomeWithdrawn})
	switch {
	case err != nil:
		n.logger.Error("notify: claim on HIL withdrawal failed after retries; nacking for redelivery", "requestID", requestID, "error", err)
		msg.Nack()
		return
	case !claimed:
		msg.Ack()
		return
	}
	n.logger.Info("notify: HIL request withdrawn by its requester", "requestID", requestID)
	if pr.Approvers != nil {
		var rec approverRecord
		if json.Unmarshal([]byte(*pr.Approvers), &rec) == nil {
			n.auditApproval(ctx, auth.HILApprovalEvent{RequestID: requestID, Outcome: auth.HILApprovalWithdrawn}, rec)
			n.announceResolution(ctx, requestID, rec, nil, a2a.NotifyOutcomeWithdrawn, "")
		}
	}
	msg.Ack()
}
