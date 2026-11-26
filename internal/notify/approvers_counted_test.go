// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	sysauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

func TestApproverSet_AfterOneApproversAnswerCountsNoOtherAnswerIsRouted(t *testing.T) {
	cases := map[string]struct {
		approverLogin string
		approved      bool
	}{
		"another approver gives the same decision":      {bobLogin, true},
		"the same approver gives the opposite decision": {aliceLogin, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newApproverHarness(t, true)
			deliveries := h.slackDeliveries(t)
			const requestID = "hil-approval-second-answer"
			sink := requesterSink(t, h.brk, "second-answer-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
			publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
			waitPending(t, h.core, requestID)
			h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}},
				map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"})
			if got := sink(3 * time.Second); got == nil || got.Data["approved"] != true {
				t.Fatalf("alice's answer was not routed: %+v", got)
			}
			h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted)

			h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": tc.approved}},
				map[string]string{a2a.PropUserID: tc.approverLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-second")})

			late := h.auditor.outcome(t, requestID, sysauth.HILApprovalLate)
			if late.Answerer != tc.approverLogin {
				t.Errorf("late audit = %+v, want %s", late, tc.approverLogin)
			}
			if extra := sink(300 * time.Millisecond); extra != nil {
				t.Errorf("a second answer reached the requester: %+v", extra)
			}
			told := outcomes(t, deliveries, 1)[0]
			if !strings.Contains(told.PromptOutcome.Refusal, "already settled") {
				t.Errorf("the second answerer was told %q, want that the request was already settled", told.PromptOutcome.Refusal)
			}
			accepted := 0
			for _, e := range h.auditor.snapshot() {
				if e.Outcome == sysauth.HILApprovalAccepted {
					accepted++
				}
			}
			if accepted != 1 {
				t.Errorf("accepted audit events = %d, want 1", accepted)
			}
		})
	}
}

type requesterPublishFailsBroker struct {
	broker.Broker
	fail *atomic.Bool
}

func (b requesterPublishFailsBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if b.fail.Load() && strings.Contains(topic, "/notify/response/") {
		return errors.New("broker unavailable")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

func TestApproverSet_AFailedRouteWhileFinishingACountedAnswerIsReturnedToTheQueue(t *testing.T) {
	var fail atomic.Bool
	auditor := &recordingAuditor{}
	h := startApproverHarness(t, true, auditor, func(b broker.Broker) broker.Broker {
		return requesterPublishFailsBroker{Broker: b, fail: &fail}
	})
	h.auditor = auditor
	const requestID = "hil-approval-finish-route-fails"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	waitPending(t, h.core, requestID)
	pr := mustPending(t, h, requestID)
	if _, claimed, err := h.core.pending.ClaimResolution(t.Context(), requestID, store.ApproverSet(*pr.Approvers), store.Resolution{
		Status: "answered", Decision: a2a.NotifyOutcomeApproved, AnsweredBy: aliceLogin, AnsweredByName: aliceLogin, AnsweredVia: "slack",
	}); err != nil || !claimed {
		t.Fatalf("claim the row as the pod that then died: claimed %v, err %v", claimed, err)
	}
	fail.Store(true)

	var acked, nacked atomic.Bool
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), nil, func() { acked.Store(true) }, func() { nacked.Store(true) })
	msg.UserProperties = map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"}
	h.core.dispatchReply(t.Context(), msg,
		a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}, mustPending(t, h, requestID))

	if !nacked.Load() || acked.Load() {
		t.Errorf("nacked = %v, acked = %v, want the answer returned to the queue so the requester still gets it", nacked.Load(), acked.Load())
	}
}

func TestApproverSet_TheSweepLoopSettlesCardsLeftUnsettled(t *testing.T) {
	h, fail := startHarnessWhereDeliverPublishCanFail(t)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-loop-settles"
	requesterSink(t, h.brk, "loop-settles-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	aliceRow := h.promptRow(t, aliceLogin, requestID)
	h.promptRow(t, bobLogin, requestID)
	h.reportCard(t, requestID, cardAt("C0OPS", "1.1"), 1)
	fail.Store(true)
	if rr := h.respond(aliceLogin, aliceRow.ID, `{"status":"answered","data":{"approved":true}}`); rr.Code != http.StatusNoContent {
		t.Fatalf("alice respond = %d, body=%s", rr.Code, rr.Body.String())
	}
	waitForResolvedNotice(t, h, bobLogin)
	fail.Store(false)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go h.core.timeoutSweepLoop(ctx, 20*time.Millisecond, 0)

	if got := outcomes(t, deliveries, 1); got[0].PromptOutcome.Outcome != a2a.NotifyOutcomeApproved {
		t.Errorf("settled outcome = %+v, want approved", got[0].PromptOutcome)
	}
}

func TestApproverSet_ARepeatOfTheCountedAnswerIsAuditedAndAnnouncedOnce(t *testing.T) {
	h := newApproverHarness(t, true)
	h.advertiseSlack(true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-repeat-once"
	sink := requesterSink(t, h.brk, "repeat-once-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	place := a2a.NotifyPlace{ChannelType: "slack", Address: json.RawMessage(`{"channel_id":"C0OPS"}`)}
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}, Places: []a2a.NotifyPlace{place}}))
	if post := deliveries(3 * time.Second); post == nil || post.Mode != a2a.NotifyModeInput {
		t.Fatalf("the place was not asked: %+v", post)
	}
	approve := a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}
	alice := map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"}
	h.publishReply(t, approve, alice)
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("alice's answer was not routed")
	}
	if notice := deliveries(3 * time.Second); notice == nil || notice.Mode != a2a.NotifyModeNotify {
		t.Fatalf("the place was not told: %+v", notice)
	}

	h.publishReply(t, approve, alice)
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("the repeated answer was not re-routed")
	}

	if again := deliveries(500 * time.Millisecond); again != nil {
		t.Errorf("the place was told a second time: %+v", again)
	}
	accepted := 0
	for _, e := range h.auditor.snapshot() {
		if e.Outcome == sysauth.HILApprovalAccepted {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("accepted audit events = %d, want 1 for one decision", accepted)
	}
}

func renameTableAway(t *testing.T, h *approverHarness, table string) (restore func()) {
	t.Helper()
	if _, err := h.core.db.ExecContext(t.Context(), `ALTER TABLE `+table+` RENAME TO `+table+`_away`); err != nil {
		t.Fatalf("take %s away: %v", table, err)
	}
	var once sync.Once
	restore = func() {
		once.Do(func() {
			_, _ = h.core.db.ExecContext(context.Background(), `ALTER TABLE `+table+`_away RENAME TO `+table)
		})
	}
	t.Cleanup(restore)
	return restore
}

func TestApproverSet_AnAliasLookupThatRecoversQuicklyIsRetriedInProcess(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-alias-recovers"
	sink := requesterSink(t, h.brk, "alias-recovers-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{approverUUID}}))
	waitPending(t, h.core, requestID)
	if err := h.core.db.RecordAlias(t.Context(), aliceLogin, approverUUID); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	restore := renameTableAway(t, h, "notify_user_aliases")
	time.AfterFunc(60*time.Millisecond, restore)

	var acked, nacked atomic.Bool
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), nil, func() { acked.Store(true) }, func() { nacked.Store(true) })
	msg.UserProperties = map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"}
	h.core.dispatchReply(t.Context(), msg,
		a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}, mustPending(t, h, requestID))

	if nacked.Load() || !acked.Load() {
		t.Errorf("nacked = %v, acked = %v, want the answer handled without a redelivery", nacked.Load(), acked.Load())
	}
	if got := sink(time.Second); got == nil || got.Data["approved"] != true {
		t.Errorf("the answer was not routed after the lookup recovered: %+v", got)
	}
}

func TestPromptOutcome_AReceiptWhoseCardStoreRecoversQuicklyIsRecordedInProcess(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-receipt-recovers"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	restore := renameTableAway(t, h, "notify_hil_cards")
	time.AfterFunc(60*time.Millisecond, restore)

	var acked, nacked atomic.Bool
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), nil, func() { acked.Store(true) }, func() { nacked.Store(true) })
	msg.UserProperties = map[string]string{a2a.PropChannel: "slack"}
	h.core.dispatchReply(t.Context(), msg,
		a2a.UserInputResponseData{RequestID: requestID, Status: a2a.HILStatusDelivered, Data: map[string]any{"card": cardAt("C0OPS", "1.1")}},
		mustPending(t, h, requestID))

	if nacked.Load() || !acked.Load() {
		t.Errorf("nacked = %v, acked = %v, want the receipt handled without a redelivery", nacked.Load(), acked.Load())
	}
	if cards, err := h.core.cards.List(t.Context(), requestID); err != nil || len(cards) != 1 {
		t.Errorf("recorded cards = %v (err %v), want the card recorded once the store recovered", cards, err)
	}
}

type queueOptionsRecorder struct {
	broker.Broker
	mu   sync.Mutex
	opts map[string]broker.QueueOptions
}

func (r *queueOptionsRecorder) CreateQueue(name string, opts broker.QueueOptions) (broker.Queue, error) {
	r.mu.Lock()
	r.opts[name] = opts
	r.mu.Unlock()
	return r.Broker.CreateQueue(name, opts)
}

func TestReplyQueue_RedeliveryIsBoundedOnlyWithApproverSetsOn(t *testing.T) {
	tests := []struct {
		name   string
		flagOn bool
		want   broker.QueueOptions
	}{
		{name: "flag off provisions the queue as main does", flagOn: false, want: broker.QueueOptions{Named: true}},
		{name: "flag on bounds redelivery at three", flagOn: true, want: broker.QueueOptions{Named: true, MaxRedelivery: 3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &queueOptionsRecorder{opts: map[string]broker.QueueOptions{}}
			h := startApproverHarness(t, tc.flagOn, &recordingAuditor{}, func(b broker.Broker) broker.Broker {
				recorder.Broker = b
				return recorder
			})

			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			got, ok := recorder.opts[a2a.QueueName(h.ns, "notify", "reply")]
			if !ok {
				t.Fatalf("reply queue was never created")
			}
			if diff := cmp.Diff(tc.want, got, cmpopts.IgnoreFields(broker.QueueOptions{}, "OnBindState")); diff != "" {
				t.Errorf("reply queue options mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestApproverSet_AnApproverAliasLookedUpAfterABriefFailureIsStored(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-request-alias-recovers"
	if err := h.core.db.RecordAlias(t.Context(), bobLogin, approverUUID); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	restore := renameTableAway(t, h, "notify_user_aliases")
	time.AfterFunc(60*time.Millisecond, restore)

	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{bobLogin}}))
	waitPending(t, h.core, requestID)

	var rec approverRecord
	if err := json.Unmarshal([]byte(*mustPending(t, h, requestID).Approvers), &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Users) != 1 || rec.Users[0].PlatformUserID != approverUUID {
		t.Errorf("stored approvers = %+v, want bob stored with his platform id", rec.Users)
	}
}

func aliasDownRequest(t *testing.T, h *approverHarness, requestID string) a2a.NotifyRequest {
	t.Helper()
	breakAliasStore(t, h)
	return newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{bobLogin}})
}

func TestApproverSet_AnAliasLookupThatKeepsFailingReturnsTheRequestToTheQueueFirst(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-request-alias-down-first"
	sink := requesterSink(t, h.brk, "alias-down-first-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := aliasDownRequest(t, h, requestID)

	if retry := h.core.handleApproverSetRequest(t.Context(), req, false); !retry {
		t.Error("a first delivery was not returned to the queue")
	}

	if got := sink(300 * time.Millisecond); got != nil {
		t.Errorf("the requester was refused on the first delivery: %+v", got)
	}
	if pr, err := h.core.pending.Get(t.Context(), requestID); err != nil || pr != nil {
		t.Errorf("the request left a pending row: %+v (err %v)", pr, err)
	}
}

func TestApproverSet_AnAliasLookupThatKeepsFailingRefusesTheRequestOnTheRedelivery(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-request-alias-down"
	sink := requesterSink(t, h.brk, "request-alias-down-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := aliasDownRequest(t, h, requestID)

	if retry := h.core.handleApproverSetRequest(t.Context(), req, true); retry {
		t.Error("a request whose refusal was published was returned to the queue")
	}

	got := sink(3 * time.Second)
	if got == nil || got.Status != "cancelled" || got.Data["reason"] != refusalApproverLookupUnavailable {
		t.Fatalf("requester got %+v, want cancelled with reason %q", got, refusalApproverLookupUnavailable)
	}
	if pr, err := h.core.pending.Get(t.Context(), requestID); err != nil || pr != nil {
		t.Errorf("the failed request left a pending row: %+v (err %v)", pr, err)
	}
	h.auditor.outcome(t, requestID, sysauth.HILApprovalRequestDenied)
}

func TestApproverSet_ARequestInterruptedByShutdownIsReturnedNotRefused(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-request-shutdown"
	sink := requesterSink(t, h.brk, "request-shutdown-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := aliasDownRequest(t, h, requestID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if retry := h.core.handleApproverSetRequest(ctx, req, true); !retry {
		t.Error("a request interrupted by shutdown was acknowledged")
	}

	if got := sink(300 * time.Millisecond); got != nil {
		t.Errorf("the requester was refused during shutdown: %+v", got)
	}
}

func TestApproverSet_ARefusalThatCannotBePublishedIsReturnedToTheQueue(t *testing.T) {
	var fail atomic.Bool
	auditor := &recordingAuditor{}
	h := startApproverHarness(t, true, auditor, func(b broker.Broker) broker.Broker {
		return requesterPublishFailsBroker{Broker: b, fail: &fail}
	})
	h.auditor = auditor
	req := aliasDownRequest(t, h, "hil-approval-refusal-unpublished")
	fail.Store(true)

	if retry := h.core.handleApproverSetRequest(t.Context(), req, true); !retry {
		t.Error("a refusal the requester never received was acknowledged")
	}
}

func TestHILRouter_AnOrdinaryApprovalSurvivesABriefDatabaseOutage(t *testing.T) {
	h := newApproverHarness(t, false)
	const requestID = "hil-single-db-blip"
	sink := requesterSink(t, h.brk, "db-blip-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}})
	req.Approvers = nil
	publishApproverRequest(t, h, req)
	waitPending(t, h.core, requestID)
	restore := renameTableAway(t, h, "notify_pending_requests")
	time.AfterFunc(250*time.Millisecond, restore)

	var acked, nacked atomic.Bool
	payload, err := json.Marshal(a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}})
	if err != nil {
		t.Fatal(err)
	}
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), payload, func() { acked.Store(true) }, func() { nacked.Store(true) })
	h.core.handleReply(t.Context(), msg)

	if nacked.Load() || !acked.Load() {
		t.Errorf("nacked = %v, acked = %v, want the reply handled without a redelivery", nacked.Load(), acked.Load())
	}
	if got := sink(time.Second); got == nil || got.Status != "answered" {
		t.Errorf("the approval was not routed once the database recovered: %+v", got)
	}
}

func requestMessage(t *testing.T, req a2a.NotifyRequest, redelivered bool) (*broker.Message, *deliveredReply) {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	d := &deliveredReply{}
	msg := broker.NewRedeliverableMessage("m1", a2a.NotifyRequestTopic("test-ns", ""), payload, func() { d.acked.Store(true) }, func() { d.nacked.Store(true) })
	msg.Redelivered = redelivered
	return msg, d
}

func TestApproverSet_ARequestMessageWhoseAliasLookupFailsIsReturnedFirstAndRefusedOnRedelivery(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-message-level"
	sink := requesterSink(t, h.brk, "message-level-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := aliasDownRequest(t, h, requestID)

	first, firstResult := requestMessage(t, req, false)
	h.core.handleMessage(t.Context(), first)
	if !firstResult.nacked.Load() || firstResult.acked.Load() {
		t.Errorf("first delivery: nacked = %v, acked = %v, want it returned to the queue", firstResult.nacked.Load(), firstResult.acked.Load())
	}
	if got := sink(300 * time.Millisecond); got != nil {
		t.Fatalf("the requester was refused on the first delivery: %+v", got)
	}

	again, againResult := requestMessage(t, req, true)
	h.core.handleMessage(t.Context(), again)
	if !againResult.acked.Load() || againResult.nacked.Load() {
		t.Errorf("redelivery: nacked = %v, acked = %v, want it acknowledged once refused", againResult.nacked.Load(), againResult.acked.Load())
	}
	if got := sink(3 * time.Second); got == nil || got.Data["reason"] != refusalApproverLookupUnavailable {
		t.Errorf("requester got %+v on the redelivery, want the %q refusal", got, refusalApproverLookupUnavailable)
	}
}

func TestApproverSet_OnABrokerThatCannotRedeliverAFailingAliasLookupRefusesAtOnce(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-dev-broker"
	sink := requesterSink(t, h.brk, "dev-broker-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := aliasDownRequest(t, h, requestID)

	publishApproverRequest(t, h, req)

	got := sink(5 * time.Second)
	if got == nil || got.Status != "cancelled" || got.Data["reason"] != refusalApproverLookupUnavailable {
		t.Fatalf("requester got %+v, want the %q refusal on the only delivery", got, refusalApproverLookupUnavailable)
	}
}

func harnessWhereRefusalsCanFail(t *testing.T, flagOn bool) (*approverHarness, *atomic.Bool) {
	t.Helper()
	var fail atomic.Bool
	auditor := &recordingAuditor{}
	h := startApproverHarness(t, flagOn, auditor, func(b broker.Broker) broker.Broker {
		return requesterPublishFailsBroker{Broker: b, fail: &fail}
	})
	h.auditor = auditor
	return h, &fail
}

func deliverRequest(t *testing.T, h *approverHarness, req a2a.NotifyRequest, redeliverable, redelivered bool) *deliveredReply {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	d := &deliveredReply{}
	ack, nack := func() { d.acked.Store(true) }, func() { d.nacked.Store(true) }
	topic := a2a.NotifyRequestTopic("test-ns", "")
	msg := broker.NewDeliveredMessage("m1", topic, payload, ack, nack)
	if redeliverable {
		msg = broker.NewRedeliverableMessage("m1", topic, payload, ack, nack)
	}
	msg.Redelivered = redelivered
	h.core.handleMessage(t.Context(), msg)
	return d
}

func TestApproverSet_AnUnpublishedRefusalIsRetriedOnABrokerThatRedelivers(t *testing.T) {
	cases := map[string]struct {
		flagOn bool
		set    *a2a.NotifyApprovers
		reason string
	}{
		"invalid set": {true, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, "system:ops-agent"}}, refusalApproverSystem},
		"flag off":    {false, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}, refusalApproversDisabled},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, fail := harnessWhereRefusalsCanFail(t, tc.flagOn)
			requestID := "hil-approval-unpublished-" + strings.ReplaceAll(name, " ", "-")
			sink := requesterSink(t, h.brk, requestID+"-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
			req := newApproverSetRequest(h.ns, requestID, tc.set)
			fail.Store(true)

			first := deliverRequest(t, h, req, true, false)
			if !first.nacked.Load() || first.acked.Load() {
				t.Errorf("first delivery: nacked = %v, acked = %v, want it returned so the refusal can be retried", first.nacked.Load(), first.acked.Load())
			}
			fail.Store(false)
			again := deliverRequest(t, h, req, true, true)

			if !again.acked.Load() || again.nacked.Load() {
				t.Errorf("redelivery: nacked = %v, acked = %v, want it acknowledged once the refusal is published", again.nacked.Load(), again.acked.Load())
			}
			if got := sink(3 * time.Second); got == nil || got.Data["reason"] != tc.reason {
				t.Errorf("requester got %+v, want the %q refusal", got, tc.reason)
			}
			denied := 0
			for _, e := range h.auditor.snapshot() {
				if e.Outcome == sysauth.HILApprovalRequestDenied {
					denied++
				}
			}
			if denied != 1 {
				t.Errorf("request_refused audit events = %d, want one although the refusal took two deliveries", denied)
			}
		})
	}
}

func TestApproverSet_AStoreThatCannotTakeTheRequestFailsItWhereRedeliveryIsUnavailable(t *testing.T) {
	for _, kind := range []string{"redeliverable broker", "broker that cannot redeliver"} {
		t.Run(kind, func(t *testing.T) {
			h := newApproverHarness(t, true)
			requestID := "hil-approval-store-down-" + strings.ReplaceAll(kind, " ", "-")
			sink := requesterSink(t, h.brk, requestID+"-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
			req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}})
			renameTableAway(t, h, "notify_pending_requests")
			redeliverable := kind == "redeliverable broker"

			first := deliverRequest(t, h, req, redeliverable, false)

			if redeliverable {
				if !first.nacked.Load() {
					t.Error("first delivery was not returned to the queue")
				}
				if got := sink(300 * time.Millisecond); got != nil {
					t.Fatalf("the requester was refused before a retry was possible: %+v", got)
				}
				first = deliverRequest(t, h, req, true, true)
			}
			if !first.acked.Load() {
				t.Errorf("the last attempt was not acknowledged after the refusal: nacked = %v", first.nacked.Load())
			}
			if got := sink(3 * time.Second); got == nil || got.Data["reason"] != refusalApproverStoreUnavailable {
				t.Errorf("requester got %+v, want the %q refusal", got, refusalApproverStoreUnavailable)
			}
		})
	}
}

func TestApproverSet_ARequestInterruptedByShutdownIsReturnedOnEveryBroker(t *testing.T) {
	for _, redeliverable := range []bool{true, false} {
		h := newApproverHarness(t, true)
		requestID := "hil-approval-shutdown-" + map[bool]string{true: "redeliverable", false: "dev"}[redeliverable]
		sink := requesterSink(t, h.brk, requestID+"-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
		req := aliasDownRequest(t, h, requestID)
		payload, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		d := &deliveredReply{}
		ack, nack := func() { d.acked.Store(true) }, func() { d.nacked.Store(true) }
		msg := broker.NewDeliveredMessage("m1", a2a.NotifyRequestTopic("test-ns", ""), payload, ack, nack)
		if redeliverable {
			msg = broker.NewRedeliverableMessage("m1", a2a.NotifyRequestTopic("test-ns", ""), payload, ack, nack)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		h.core.handleMessage(ctx, msg)

		if !d.nacked.Load() || d.acked.Load() {
			t.Errorf("redeliverable %v: nacked = %v, acked = %v, want it returned", redeliverable, d.nacked.Load(), d.acked.Load())
		}
		if got := sink(300 * time.Millisecond); got != nil {
			t.Errorf("redeliverable %v: the requester was refused during shutdown: %+v", redeliverable, got)
		}
		for _, e := range h.auditor.snapshot() {
			if e.Outcome == sysauth.HILApprovalRequestDenied {
				t.Errorf("redeliverable %v: a request interrupted by shutdown was audited as refused: %+v", redeliverable, e)
			}
		}
	}
}
