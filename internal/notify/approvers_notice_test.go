// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	sysauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

type deliveredReply struct {
	acked, nacked atomic.Bool
}

func (h *approverHarness) dispatchAs(t *testing.T, requestID string, pr *store.PendingRequest, resp a2a.UserInputResponseData, redelivered bool) *deliveredReply {
	t.Helper()
	d := &deliveredReply{}
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), nil, func() { d.acked.Store(true) }, func() { d.nacked.Store(true) })
	msg.Redelivered = redelivered
	msg.UserProperties = map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-alice")}
	h.core.dispatchReply(t.Context(), msg, resp, pr)
	return d
}

var approveAsAlice = func(requestID string) a2a.UserInputResponseData {
	return a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}
}

func TestApproverSet_AClaimThatKeepsFailingTellsTheAnswererAndReturnsTheAnswer(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-claim-fails"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	pr := mustPending(t, h, requestID)
	renameTableAway(t, h, "notify_pending_requests")

	d := h.dispatchAs(t, requestID, pr, approveAsAlice(requestID), false)

	if !d.nacked.Load() || d.acked.Load() {
		t.Errorf("nacked = %v, acked = %v, want the answer returned to the queue", d.nacked.Load(), d.acked.Load())
	}
	told := outcomes(t, deliveries, 1)[0]
	if told.PromptOutcome.Refusal != msgNotRecorded || !told.PromptOutcome.Retryable {
		t.Errorf("the answerer was told %+v, want the retryable %q", told.PromptOutcome, msgNotRecorded)
	}
}

func TestApproverSet_ARouteFailureOnAFirstAnswerReopensTheRequestAndTellsTheAnswerer(t *testing.T) {
	var fail atomic.Bool
	auditor := &recordingAuditor{}
	h := startApproverHarness(t, true, auditor, func(b broker.Broker) broker.Broker {
		return requesterPublishFailsBroker{Broker: b, fail: &fail}
	})
	h.auditor = auditor
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-first-route-fails"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	pr := mustPending(t, h, requestID)
	fail.Store(true)

	d := h.dispatchAs(t, requestID, pr, approveAsAlice(requestID), false)

	if !d.nacked.Load() || d.acked.Load() {
		t.Errorf("nacked = %v, acked = %v, want the answer returned to the queue", d.nacked.Load(), d.acked.Load())
	}
	if got := mustPending(t, h, requestID); got.Status != "pending" || got.AnsweredBy != nil {
		t.Errorf("the row after the failed route = %+v, want it open again with no answerer", got)
	}
	for _, e := range auditor.snapshot() {
		if e.Outcome == sysauth.HILApprovalAccepted {
			t.Errorf("an answer that was not passed on was audited as accepted: %+v", e)
		}
	}
	if told := outcomes(t, deliveries, 1)[0]; told.PromptOutcome.Refusal != msgNotRecorded {
		t.Errorf("the answerer was told %q, want %q", told.PromptOutcome.Refusal, msgNotRecorded)
	}
}

func TestApproverSet_ARouteFailureOnACountedAnswerSaysItWasCounted(t *testing.T) {
	var fail atomic.Bool
	auditor := &recordingAuditor{}
	h := startApproverHarness(t, true, auditor, func(b broker.Broker) broker.Broker {
		return requesterPublishFailsBroker{Broker: b, fail: &fail}
	})
	h.auditor = auditor
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-counted-route-fails"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	pr := mustPending(t, h, requestID)
	if _, claimed, err := h.core.pending.ClaimResolution(t.Context(), requestID, store.ApproverSet(*pr.Approvers), store.Resolution{
		Status: "answered", Decision: a2a.NotifyOutcomeApproved, AnsweredBy: aliceLogin, AnsweredByName: aliceLogin, AnsweredVia: "slack",
	}); err != nil || !claimed {
		t.Fatalf("claim the row as the pod that then died: claimed %v, err %v", claimed, err)
	}
	fail.Store(true)

	d := h.dispatchAs(t, requestID, mustPending(t, h, requestID), approveAsAlice(requestID), false)

	if !d.nacked.Load() {
		t.Error("the answer was not returned to the queue")
	}
	if told := outcomes(t, deliveries, 1)[0]; told.PromptOutcome.Refusal != msgCountedPassOnRetrying {
		t.Errorf("the answerer was told %q, want %q", told.PromptOutcome.Refusal, msgCountedPassOnRetrying)
	}
}

func TestApproverSet_AnUnreadableStoredSetTellsTheAnswerer(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-unreadable"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	broken := *mustPending(t, h, requestID)
	garbage := "{not json"
	broken.Approvers = &garbage

	d := h.dispatchAs(t, requestID, &broken, approveAsAlice(requestID), false)

	if !d.acked.Load() {
		t.Error("the unreadable request's answer was not acknowledged")
	}
	told := outcomes(t, deliveries, 1)[0]
	if told.PromptOutcome.Refusal != msgApproversUnreadable || told.PromptOutcome.Retryable {
		t.Errorf("the answerer was told %+v, want the final %q", told.PromptOutcome, msgApproversUnreadable)
	}
}

func TestApproverSet_ARedeliveredAnswerThatFailsAgainDoesNotNoticeTheAnswererAgain(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-notice-once"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	pr := mustPending(t, h, requestID)
	renameTableAway(t, h, "notify_pending_requests")

	d := h.dispatchAs(t, requestID, pr, approveAsAlice(requestID), true)

	if !d.nacked.Load() {
		t.Error("the answer was not returned to the queue")
	}
	if told := deliveries(300 * time.Millisecond); told != nil {
		t.Errorf("a redelivery told the answerer again: %+v", told)
	}
}

func TestApproverSet_EachRequestIsAuditedAndAnnouncedOnItsOwn(t *testing.T) {
	h := newApproverHarness(t, true)
	h.advertiseSlack(true)
	deliveries := h.slackDeliveries(t)
	place := a2a.NotifyPlace{ChannelType: "slack", Address: json.RawMessage(`{"channel_id":"C0OPS"}`)}
	requests := []string{"hil-approval-own-one", "hil-approval-own-two"}
	for _, requestID := range requests {
		sink := requesterSink(t, h.brk, requestID+"-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
		publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}, Places: []a2a.NotifyPlace{place}}))
		waitPending(t, h.core, requestID)
		h.publishReply(t, approveAsAlice(requestID), map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"})
		if got := sink(3 * time.Second); got == nil {
			t.Fatalf("%s: alice's answer was not routed", requestID)
		}
		h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted)
	}

	noticed := map[string]int{}
	for d := deliveries(time.Second); d != nil; d = deliveries(500 * time.Millisecond) {
		if d.Mode == a2a.NotifyModeNotify {
			noticed[d.RequestID]++
		}
	}
	for _, requestID := range requests {
		if noticed[requestID] != 1 {
			t.Errorf("%s: the place was told %d times, want once", requestID, noticed[requestID])
		}
	}
	for deadline := time.Now().Add(3 * time.Second); resolvedNotices(h.inbox(t, bobLogin)) < len(requests) && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}
	if got := resolvedNotices(h.inbox(t, bobLogin)); got != len(requests) {
		t.Errorf("bob holds %d notices that a request was answered, want one per request", got)
	}
}

func TestApproverSet_ACardOrChannelTheBrokerSuppliedIsCheckedBeforeItIsUsed(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-hostile-props"
	q, err := h.brk.CreateQueue("hostile-deliver-sink", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.brk.Subscribe(q, "test-ns/sam/v1/notify/deliver/>"); err != nil {
		t.Fatal(err)
	}
	delivered := func() int {
		count := 0
		for {
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			msg, rerr := h.brk.Receive(ctx, q)
			cancel()
			if rerr != nil {
				return count
			}
			msg.Ack()
			count++
		}
	}
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	pr := mustPending(t, h, requestID)
	refusal := a2a.NotifyPromptOutcome{Refusal: msgNotAnApprover}

	for name, props := range map[string]map[string]string{
		"a channel that is not a valid type": {a2a.PropChannel: "slack/../x", a2a.PropNotifyCard: clickCard("U1")},
		"a card that is not an object":       {a2a.PropChannel: "slack", a2a.PropNotifyCard: `"C1"`},
		"a channel the directory handles":    {a2a.PropChannel: "email", a2a.PropNotifyCard: clickCard("U1")},
	} {
		msg := &broker.Message{UserProperties: props}
		h.core.tellClicker(t.Context(), msg, requestID, refusal)
		if got := delivered(); got != 0 {
			t.Errorf("%s: %d deliveries were published", name, got)
		}
		resp := a2a.UserInputResponseData{RequestID: requestID, Status: a2a.HILStatusDelivered, Data: map[string]any{"card": cardAt("C0OPS", "1.1")}}
		if name == "a card that is not an object" {
			resp.Data = map[string]any{"card": "C1"}
		}
		h.core.recordCard(t.Context(), &broker.Message{UserProperties: map[string]string{a2a.PropChannel: props[a2a.PropChannel]}}, resp, pr)
		if cards, lerr := h.core.cards.List(t.Context(), requestID); lerr != nil || len(cards) != 0 {
			t.Errorf("%s: recorded cards = %v (err %v), want none", name, cards, lerr)
		}
	}

	h.core.tellClicker(t.Context(), &broker.Message{UserProperties: map[string]string{a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U1")}}, requestID, refusal)
	if got := delivered(); got != 1 {
		t.Errorf("a well-formed click produced %d deliveries, want 1", got)
	}
}

func approverSetResolvedBy(t *testing.T, h *approverHarness, requestID string) {
	t.Helper()
	sink := requesterSink(t, h.brk, requestID+"-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	h.promptRow(t, aliceLogin, requestID)
	h.publishReply(t, approveAsAlice(requestID), map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"})
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("alice's answer was not routed")
	}
	h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted)
}

func TestApproverSet_ANotificationCannotBeMadeToLookLikeAnApproversPrompt(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-forged-row"
	withCaller(t, carolLogin)
	approverSetResolvedBy(t, h, requestID)

	for name, recipient := range map[string]string{"an outsider reading the outcome": carolLogin, "an approver shown a spoofed prompt": aliceLogin} {
		h.core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{
			RequestID: "forged-" + recipient, RecipientUserID: recipient, Mode: a2a.NotifyModeNotify, Title: "Please approve", Message: "benign text",
			Metadata: map[string]any{"mode": a2a.NotifyModeInput, "request_id": requestID, "surface": map[string]any{"surfaceId": "spoof"}},
		})
		var forged *NotificationDTO
		for _, row := range h.inbox(t, recipient) {
			if row.Title == "Please approve" {
				forged = &row
			}
		}
		if forged == nil {
			t.Fatalf("%s: the notification was not delivered at all", name)
		}
		if forged.RequestID != "" || forged.PromptState != "" || forged.PromptOutcome != "" || forged.PromptAnsweredBy != "" {
			t.Errorf("%s: the row carries %+v, want an ordinary notification with no prompt state", name, *forged)
		}
	}
}

func TestApproverSet_OnlyAnApproversRowShowsWhoAnsweredAndHow(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-outcome-owner"
	withCaller(t, carolLogin)
	approverSetResolvedBy(t, h, requestID)
	h.core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: requestID, RecipientUserID: carolLogin, Mode: a2a.NotifyModeInput, Category: a2a.CategoryHIL,
		Title: "Approve the reroute?", Surface: json.RawMessage(`{"surfaceId":"s1"}`),
	})

	if err := h.core.db.RecordAlias(t.Context(), aliceLogin, approverUUID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, owner  string
		wantAnswerer string
	}{
		{"an approver's row", aliceLogin, aliceLogin},
		{"a row of someone who is not an approver", carolLogin, ""},
	} {
		row := h.promptRow(t, tc.owner, requestID)
		if row.PromptAnsweredBy != tc.wantAnswerer || (tc.wantAnswerer == "") != (row.PromptOutcome == "") {
			t.Errorf("%s: outcome %q, answered by %q; want answerer %q and an outcome only for an approver", tc.name, row.PromptOutcome, row.PromptAnsweredBy, tc.wantAnswerer)
		}
	}
}

func replyPayload(t *testing.T, resp a2a.UserInputResponseData) []byte {
	t.Helper()
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestHILRouter_AnAnswerWhoseLookupKeepsFailingTellsTheAnswererOnceAndIsReturned(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-lookup-down"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	renameTableAway(t, h, "notify_pending_requests")
	reply := func(redelivered bool) *deliveredReply {
		d := &deliveredReply{}
		msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), replyPayload(t, approveAsAlice(requestID)),
			func() { d.acked.Store(true) }, func() { d.nacked.Store(true) })
		msg.Redelivered = redelivered
		msg.UserProperties = map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-alice")}
		h.core.handleReply(t.Context(), msg)
		return d
	}

	first := reply(false)

	if !first.nacked.Load() || first.acked.Load() {
		t.Errorf("first delivery: nacked = %v, acked = %v, want the answer returned to the queue", first.nacked.Load(), first.acked.Load())
	}
	if told := outcomes(t, deliveries, 1)[0]; told.PromptOutcome.Refusal != msgNotRecorded || !told.PromptOutcome.Retryable {
		t.Errorf("the answerer was told %+v, want the retryable %q", told.PromptOutcome, msgNotRecorded)
	}
	if dup := deliveries(300 * time.Millisecond); dup != nil {
		t.Errorf("one failed click produced a second notice: %+v", dup)
	}
	receipt := &deliveredReply{}
	msg := broker.NewDeliveredMessage("m2", a2a.NotifyReplyTopic(h.ns, requestID),
		replyPayload(t, a2a.UserInputResponseData{RequestID: requestID, Status: a2a.HILStatusDelivered, Data: map[string]any{"card": cardAt("C0OPS", "1.1")}}),
		func() { receipt.acked.Store(true) }, func() { receipt.nacked.Store(true) })
	msg.UserProperties = map[string]string{a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-alice")}
	h.core.handleReply(t.Context(), msg)
	if !receipt.nacked.Load() {
		t.Error("a delivery receipt whose lookup failed was not returned to the queue")
	}
	if told := deliveries(300 * time.Millisecond); told != nil {
		t.Errorf("a delivery receipt produced an answerer notice: %+v", told)
	}
	if again := reply(true); !again.nacked.Load() {
		t.Error("the redelivery was not returned to the queue")
	}
	if told := deliveries(300 * time.Millisecond); told != nil {
		t.Errorf("the redelivery told the answerer again: %+v", told)
	}
}

func TestHILRouter_AReadAfterAnUnclaimedReplyIsRetriedAndTellsTheAnswererWhenItFails(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-single-unclaimed"
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}})
	req.Approvers = nil
	publishApproverRequest(t, h, req)
	waitPending(t, h.core, requestID)
	restore := renameTableAway(t, h, "notify_pending_requests")
	time.AfterFunc(250*time.Millisecond, restore)
	recovered := func() *deliveredReply {
		d := &deliveredReply{}
		msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), nil, func() { d.acked.Store(true) }, func() { d.nacked.Store(true) })
		h.core.unclaimedReply(t.Context(), msg, approveAsAlice(requestID))
		return d
	}

	if d := recovered(); d.nacked.Load() || !d.acked.Load() {
		t.Errorf("a read that recovered within the retries: nacked = %v, acked = %v, want it handled", d.nacked.Load(), d.acked.Load())
	}

	renameTableAway(t, h, "notify_pending_requests")
	d := &deliveredReply{}
	msg := broker.NewDeliveredMessage("m2", a2a.NotifyReplyTopic(h.ns, requestID), nil, func() { d.acked.Store(true) }, func() { d.nacked.Store(true) })
	msg.UserProperties = map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-alice")}
	h.core.unclaimedReply(t.Context(), msg, approveAsAlice(requestID))
	if !d.nacked.Load() {
		t.Error("a read that kept failing was not returned to the queue")
	}
	if told := outcomes(t, deliveries, 1)[0]; told.PromptOutcome.Refusal != msgNotRecorded {
		t.Errorf("the answerer was told %q, want %q", told.PromptOutcome.Refusal, msgNotRecorded)
	}
}

func TestApproverSet_AReadOfASettledRequestThatKeepsFailingTellsTheAnswererOnceAndIsReturned(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-settled-read-fails"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	waitPending(t, h.core, requestID)
	pr := mustPending(t, h, requestID)
	settled := *pr
	settled.Status = "answered"
	renameTableAway(t, h, "notify_pending_requests")

	first := h.dispatchAs(t, requestID, &settled, approveAsAlice(requestID), false)

	if !first.nacked.Load() || first.acked.Load() {
		t.Errorf("first delivery: nacked = %v, acked = %v, want the answer returned to the queue", first.nacked.Load(), first.acked.Load())
	}
	if told := outcomes(t, deliveries, 1)[0]; told.PromptOutcome.Refusal != msgNotRecorded || !told.PromptOutcome.Retryable {
		t.Errorf("the answerer was told %+v, want the retryable %q", told.PromptOutcome, msgNotRecorded)
	}
	again := h.dispatchAs(t, requestID, &settled, approveAsAlice(requestID), true)
	if !again.nacked.Load() {
		t.Error("the redelivery was not returned to the queue")
	}
	if told := deliveries(300 * time.Millisecond); told != nil {
		t.Errorf("the redelivery told the answerer again: %+v", told)
	}
}

func TestApproverSet_TheApproverWhoAnswersIsTheOneRecordedAndTheOthersAreToldTheirName(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-second-listed-answers"
	sink := requesterSink(t, h.brk, "second-listed-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{
		UserIDs: []string{aliceLogin, bobLogin},
		Names:   map[string]string{aliceLogin: "Alice Kim", bobLogin: "Bob Ortiz"},
	})
	publishApproverRequest(t, h, req)
	h.promptRow(t, aliceLogin, requestID)
	h.promptRow(t, bobLogin, requestID)

	h.publishReply(t, approveAsAlice(requestID), map[string]string{a2a.PropUserID: bobLogin, a2a.PropChannel: "slack"})

	if got := sink(3 * time.Second); got == nil || got.Data["approved"] != true {
		t.Fatalf("bob's answer was not routed: %+v", got)
	}
	waitForResolvedNotice(t, h, aliceLogin)
	if resolvedNotices(h.inbox(t, bobLogin)) != 0 {
		t.Error("the approver who answered was told that someone answered")
	}
	var notice string
	for _, row := range h.inbox(t, aliceLogin) {
		if row.Title == msgResolvedTitle {
			notice = row.Body
		}
	}
	if notice != "Approved by Bob Ortiz." {
		t.Errorf("alice's notice = %q, want it to name the approver who answered", notice)
	}
	if pr := mustPending(t, h, requestID); deref(pr.AnsweredBy) != bobLogin || deref(pr.AnsweredByName) != "Bob Ortiz" {
		t.Errorf("recorded answerer = %q (%q), want bob", deref(pr.AnsweredBy), deref(pr.AnsweredByName))
	}
}

func TestApproverSet_AnApproverMatchedByPlatformIdIsTheOneRecorded(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-platform-match"
	sink := requesterSink(t, h.brk, "platform-match-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{
		UserIDs: []string{aliceLogin, approverUUID},
		Names:   map[string]string{aliceLogin: "Alice Kim", approverUUID: "Bob Ortiz"},
	}))
	waitPending(t, h.core, requestID)

	h.publishReply(t, approveAsAlice(requestID), map[string]string{a2a.PropUserUUID: approverUUID, a2a.PropChannel: "web"})

	if got := sink(3 * time.Second); got == nil {
		t.Fatal("the answer was not routed")
	}
	for deadline := time.Now().Add(3 * time.Second); deref(mustPending(t, h, requestID).AnsweredByName) == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}
	if pr := mustPending(t, h, requestID); deref(pr.AnsweredByName) != "Bob Ortiz" {
		t.Errorf("recorded answerer name = %q, want the approver matched by platform id", deref(pr.AnsweredByName))
	}
}

func TestApproverSet_ASubmittedNotificationCannotSpellItsWayIntoAPrompt(t *testing.T) {
	h := newApproverHarness(t, true)
	withCaller(t, carolLogin)
	const requestID = "hil-approval-spelled-row"
	approverSetResolvedBy(t, h, requestID)

	spellings := map[string][2]string{
		"capitalised":      {"Mode", "Surface"},
		"upper case":       {"MODE", "SURFACE"},
		"long s":           {"mode", "\u017furface"},
		"underscore cased": {"mode", "Surface"},
	}
	for name, keys := range spellings {
		idKey := "Request_ID"
		if name == "upper case" {
			idKey = "REQUEST_ID"
		}
		title := "spelled " + name
		body, err := json.Marshal(map[string]any{
			"title": title, "message": "benign text",
			"metadata": map[string]any{keys[0]: a2a.NotifyModeInput, idKey: requestID, keys[1]: map[string]any{"surfaceId": "spoof"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if rr := h.serve(carolLogin, http.MethodPost, "/api/v1/notify", string(body)); rr.Code >= http.StatusBadRequest {
			t.Fatalf("%s: POST /api/v1/notify = %d, body=%s", name, rr.Code, rr.Body.String())
		}
		var row *NotificationDTO
		for deadline := time.Now().Add(3 * time.Second); row == nil && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			for _, candidate := range h.inbox(t, carolLogin) {
				if candidate.Title == title {
					row = &candidate
				}
			}
		}
		if row == nil {
			t.Fatalf("%s: the notification never reached the inbox", name)
		}
		if row.RequestID != "" || row.PromptState != "" || len(row.Surface) != 0 {
			t.Errorf("%s: the row reads as a prompt: requestId %q, state %q, surface %s", name, row.RequestID, row.PromptState, row.Surface)
		}
	}
}

func TestInboxMetadata_DropsEverySpellingOfAReservedKey(t *testing.T) {
	for _, key := range []string{"mode", "Mode", "MODE", "request_id", "Request_ID", "surface", "Surface", "\u017furface"} {
		md := inboxMetadata(a2a.NotifyRequest{Mode: a2a.NotifyModeNotify, Metadata: map[string]any{key: "x", "source": "agent"}})

		if _, kept := md[key]; kept || md["source"] != "agent" {
			t.Errorf("metadata %q = %v, want the reserved key dropped and the rest kept", key, md)
		}
	}
}

func TestParseHILMeta_ReadsOnlyTheExactKeys(t *testing.T) {
	cases := map[string]string{
		"exact keys":   `{"mode":"input","request_id":"r1","surface":{"s":1}}`,
		"mode spelled": `{"Mode":"input","request_id":"r1","surface":{"s":1}}`,
		"id spelled":   `{"mode":"input","Request_ID":"r1","surface":{"s":1}}`,
	}
	for name, payload := range cases {
		id, surface := parseHILMeta(payload)
		if (name == "exact keys") != (id == "r1") {
			t.Errorf("%s: request id = %q, want it read only from the exact key", name, id)
		}
		if name != "exact keys" && len(surface) != 0 {
			t.Errorf("%s: surface = %s, want none", name, surface)
		}
	}
	if _, surface := parseHILMeta(`{"mode":"input","request_id":"r1","Surface":{"s":1}}`); len(surface) != 0 {
		t.Errorf("a spelled surface key was read: %s", surface)
	}
}

func TestApproverSet_AnApproverListedByLoginSeesTheOutcomeOnTheirPlatformKeyedRow(t *testing.T) {
	h := newApproverHarness(t, true)
	const (
		requestID = "hil-approval-login-listed"
		caller    = "alice-by-platform-id"
	)
	approverCallers[caller] = auth.NewTestSubject(auth.SubjectFields{UserID: aliceLogin, PlatformUserID: approverUUID})
	t.Cleanup(func() { delete(approverCallers, caller) })
	if err := h.core.db.RecordAlias(t.Context(), aliceLogin, approverUUID); err != nil {
		t.Fatal(err)
	}
	sink := requesterSink(t, h.brk, "login-listed-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	h.promptRow(t, caller, requestID)

	h.publishReply(t, approveAsAlice(requestID), map[string]string{a2a.PropUserID: bobLogin, a2a.PropChannel: "slack"})
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("bob's answer was not routed")
	}

	var row NotificationDTO
	for deadline := time.Now().Add(3 * time.Second); row.PromptOutcome == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		row = h.promptRow(t, caller, requestID)
	}
	if row.PromptOutcome != a2a.NotifyOutcomeApproved || row.PromptAnsweredBy != bobLogin {
		t.Errorf("the row of an approver listed by login = outcome %q, answered by %q, want approved by bob", row.PromptOutcome, row.PromptAnsweredBy)
	}
}

func TestHILRouter_AResolvedSingleRecipientPromptCarriesNoOutcomeLabel(t *testing.T) {
	h := newApproverHarness(t, false)
	const requestID = "hil-single-outcome-label"
	requesterSink(t, h.brk, "single-label-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}})
	req.Approvers = nil
	publishApproverRequest(t, h, req)
	row := h.promptRow(t, aliceLogin, requestID)
	if row.PromptState != promptStatePending || row.PromptOutcome != "" {
		t.Fatalf("an open prompt reads %+v, want pending with no outcome", row)
	}

	if rr := h.respond(aliceLogin, row.ID, `{"status":"cancelled"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("respond = %d, body=%s", rr.Code, rr.Body.String())
	}

	for deadline := time.Now().Add(3 * time.Second); row.PromptState != promptStateResolved && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		row = h.promptRow(t, aliceLogin, requestID)
	}
	if row.PromptState != promptStateResolved || row.PromptOutcome != "" || row.PromptAnsweredBy != "" {
		t.Errorf("a resolved single-recipient prompt = state %q, outcome %q, answered by %q, want it resolved with no outcome so the badge reads as on main",
			row.PromptState, row.PromptOutcome, row.PromptAnsweredBy)
	}
}

func TestHILRouter_AWithdrawnReplyOnASingleRecipientRowIsRoutedAsMainDid(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-single-withdrawn"
	requesterAddr := a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID)
	sink := requesterSink(t, h.brk, "single-withdrawn-sink", requesterAddr)
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}})
	req.Approvers = nil
	publishApproverRequest(t, h, req)
	waitPending(t, h.core, requestID)

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: a2a.HILStatusWithdrawn},
		map[string]string{a2a.PropReplyTo: requesterAddr})

	got := sink(3 * time.Second)
	if got == nil || got.Status != a2a.HILStatusWithdrawn {
		t.Fatalf("the requester got %+v, want the withdrawn reply routed to it as before", got)
	}
	if pr := mustPending(t, h, requestID); pr.Status != a2a.HILStatusWithdrawn || pr.Decision != nil {
		t.Errorf("the row = status %q, decision %v, want it claimed with the reply's status and no approver-set decision", pr.Status, pr.Decision)
	}
}

func TestApproverSet_AnApproverWhoseAliasWasRecordedAfterTheRequestStillSeesTheOutcome(t *testing.T) {
	h := newApproverHarness(t, true)
	const (
		requestID = "hil-approval-alias-after-request"
		caller    = "alice-first-login"
	)
	approverCallers[caller] = auth.NewTestSubject(auth.SubjectFields{UserID: aliceLogin, PlatformUserID: approverUUID})
	t.Cleanup(func() { delete(approverCallers, caller) })
	sink := requesterSink(t, h.brk, "alias-after-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	waitPending(t, h.core, requestID)

	row := h.promptRow(t, caller, requestID)
	if rr := h.respond(caller, row.ID, `{"status":"answered","data":{"approved":true}}`); rr.Code != http.StatusNoContent {
		t.Fatalf("respond = %d, body=%s", rr.Code, rr.Body.String())
	}
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("the answer was not routed")
	}

	for deadline := time.Now().Add(3 * time.Second); row.PromptOutcome == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		row = h.promptRow(t, caller, requestID)
	}
	if row.PromptOutcome != a2a.NotifyOutcomeApproved || row.PromptAnsweredBy != aliceLogin {
		t.Errorf("the row after first sign-in = outcome %q, answered by %q, want approved by alice", row.PromptOutcome, row.PromptAnsweredBy)
	}
}

func TestApproverSet_AFailedAliasLookupWithholdsTheOutcomeLabelFromEveryone(t *testing.T) {
	h := newApproverHarness(t, true)
	const (
		requestID = "hil-approval-label-alias-down"
		caller    = "alice-label-alias-down"
	)
	withCaller(t, carolLogin)
	approverCallers[caller] = auth.NewTestSubject(auth.SubjectFields{UserID: aliceLogin, PlatformUserID: approverUUID})
	t.Cleanup(func() { delete(approverCallers, caller) })
	sink := requesterSink(t, h.brk, "label-alias-down-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	waitPending(t, h.core, requestID)
	row := h.promptRow(t, caller, requestID)
	h.core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: requestID, RecipientUserID: carolLogin, Mode: a2a.NotifyModeInput, Category: a2a.CategoryHIL,
		Title: "Approve the reroute?", Surface: json.RawMessage(`{"surfaceId":"s1"}`),
	})
	if rr := h.respond(caller, row.ID, `{"status":"answered","data":{"approved":true}}`); rr.Code != http.StatusNoContent {
		t.Fatalf("respond = %d, body=%s", rr.Code, rr.Body.String())
	}
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("the answer was not routed")
	}
	h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted)
	breakAliasStore(t, h)

	for _, owner := range []string{caller, carolLogin} {
		got := h.promptRow(t, owner, requestID)
		if got.PromptState != promptStateResolved || got.PromptOutcome != "" || got.PromptAnsweredBy != "" {
			t.Errorf("%s with the alias store down: state %q, outcome %q, answered by %q, want a resolved prompt with the label withheld",
				owner, got.PromptState, got.PromptOutcome, got.PromptAnsweredBy)
		}
	}
}

func TestApproverSet_EachRefusedRequestIsAuditedOnItsOwn(t *testing.T) {
	h := newApproverHarness(t, false)
	requests := []string{"hil-approval-refused-one", "hil-approval-refused-two"}
	for _, requestID := range requests {
		sink := requesterSink(t, h.brk, requestID+"-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
		publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
		if got := sink(3 * time.Second); got == nil || got.Status != "cancelled" {
			t.Fatalf("%s: requester got %+v, want a refusal", requestID, got)
		}
		h.auditor.outcome(t, requestID, sysauth.HILApprovalRequestDenied)
	}
}
