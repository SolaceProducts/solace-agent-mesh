// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

type deliverPublishFailsBroker struct {
	broker.Broker
	fail *atomic.Bool
}

func (b deliverPublishFailsBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if b.fail.Load() && strings.Contains(topic, "/notify/deliver/") {
		return errors.New("broker unavailable")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

func startHarnessWhereDeliverPublishCanFail(t *testing.T) (*approverHarness, *atomic.Bool) {
	t.Helper()
	var fail atomic.Bool
	auditor := &recordingAuditor{}
	h := startApproverHarness(t, true, auditor, func(b broker.Broker) broker.Broker {
		return deliverPublishFailsBroker{Broker: b, fail: &fail}
	})
	h.auditor = auditor
	return h, &fail
}

func waitForResolvedNotice(t *testing.T, h *approverHarness, user string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if resolvedNotices(h.inbox(t, user)) > 0 {
			return
		}
	}
	t.Fatalf("%s was never told the request was answered", user)
}

func TestPromptOutcome_CardsStayUntilTheirSettlementIsPublished(t *testing.T) {
	h, fail := startHarnessWhereDeliverPublishCanFail(t)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-settle-retry"
	requesterSink(t, h.brk, "settle-retry-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	aliceRow := h.promptRow(t, aliceLogin, requestID)
	h.promptRow(t, bobLogin, requestID)
	h.reportCard(t, requestID, cardAt("C0OPS", "1.1"), 1)

	fail.Store(true)
	if rr := h.respond(aliceLogin, aliceRow.ID, `{"status":"answered","data":{"approved":true}}`); rr.Code != http.StatusNoContent {
		t.Fatalf("alice respond = %d, body=%s", rr.Code, rr.Body.String())
	}
	waitForResolvedNotice(t, h, bobLogin)

	if cards, err := h.core.cards.List(t.Context(), requestID); err != nil || len(cards) != 1 {
		t.Fatalf("cards after a failed settlement = %v (err %v), want the row kept for a retry", cards, err)
	}

	fail.Store(false)
	h.core.settleLeftoverCards(t.Context(), time.Now().Add(time.Minute).UnixMilli())

	if got := outcomes(t, deliveries, 1); got[0].PromptOutcome.Outcome != a2a.NotifyOutcomeApproved {
		t.Errorf("retried outcome = %+v, want approved", got[0].PromptOutcome)
	}
	if cards, err := h.core.cards.List(t.Context(), requestID); err != nil || len(cards) != 0 {
		t.Errorf("cards after the retry = %v (err %v), want them forgotten", cards, err)
	}
}

func TestPromptOutcome_ALateReceiptWhoseSettlementFailsIsKeptForARetry(t *testing.T) {
	h, fail := startHarnessWhereDeliverPublishCanFail(t)
	const requestID = "hil-approval-late-receipt"
	requesterSink(t, h.brk, "late-receipt-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	aliceRow := h.promptRow(t, aliceLogin, requestID)
	h.promptRow(t, bobLogin, requestID)
	if rr := h.respond(aliceLogin, aliceRow.ID, `{"status":"answered","data":{"approved":true}}`); rr.Code != http.StatusNoContent {
		t.Fatalf("alice respond = %d, body=%s", rr.Code, rr.Body.String())
	}
	waitForResolvedNotice(t, h, bobLogin)

	fail.Store(true)
	h.reportCard(t, requestID, cardAt("C0OPS", "1.1"), 1)
}

func TestPromptOutcome_AReceiptThatCannotBeRecordedIsReturnedToTheQueue(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-receipt-nack"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	if _, err := h.core.db.ExecContext(t.Context(), `DROP TABLE notify_hil_cards`); err != nil {
		t.Fatalf("break the card store: %v", err)
	}

	var acked, nacked atomic.Bool
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), nil, func() { acked.Store(true) }, func() { nacked.Store(true) })
	msg.UserProperties = map[string]string{a2a.PropChannel: "slack"}
	h.core.dispatchReply(t.Context(), msg,
		a2a.UserInputResponseData{RequestID: requestID, Status: a2a.HILStatusDelivered, Data: map[string]any{"card": cardAt("C0OPS", "1.1")}},
		mustPending(t, h, requestID))

	if !nacked.Load() || acked.Load() {
		t.Errorf("nacked = %v, acked = %v, want the receipt returned to the queue", nacked.Load(), acked.Load())
	}
}

type cardRecordedAfterList struct {
	hilCardStore
	once sync.Once
}

func (c *cardRecordedAfterList) List(ctx context.Context, requestID string) ([]store.HILCard, error) {
	cards, err := c.hilCardStore.List(ctx, requestID)
	c.once.Do(func() {
		_ = c.Add(ctx, requestID, "slack", `{"channel_id":"C0LATE","ts":"2.2"}`)
	})
	return cards, err
}

func TestPromptOutcome_ACardRecordedWhileOthersSettleIsNotForgotten(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-card-race"
	requesterSink(t, h.brk, "card-race-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	aliceRow := h.promptRow(t, aliceLogin, requestID)
	h.promptRow(t, bobLogin, requestID)
	h.reportCard(t, requestID, cardAt("C0OPS", "1.1"), 1)
	h.core.cards = &cardRecordedAfterList{hilCardStore: h.core.cards}

	if rr := h.respond(aliceLogin, aliceRow.ID, `{"status":"answered","data":{"approved":true}}`); rr.Code != http.StatusNoContent {
		t.Fatalf("alice respond = %d, body=%s", rr.Code, rr.Body.String())
	}
	waitForResolvedNotice(t, h, bobLogin)

	left, err := h.core.cards.List(t.Context(), requestID)
	if err != nil || len(left) != 1 || !strings.Contains(left[0].Card, "C0LATE") {
		t.Fatalf("cards after settling = %v (err %v), want only the card recorded during the settle", left, err)
	}
	h.core.settleLeftoverCards(t.Context(), time.Now().Add(time.Minute).UnixMilli())
	var settled []string
	for _, d := range outcomes(t, deliveries, 2) {
		settled = append(settled, string(d.Address))
	}
	if !slices.ContainsFunc(settled, func(a string) bool { return strings.Contains(a, "C0LATE") }) {
		t.Errorf("settled cards = %v, want the late card settled by the retry", settled)
	}
}
