// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	sysauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

func TestApproverSet_AnAnswerAfterTheDeadlineIsRefusedAsExpired(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-after-deadline"
	sink := requesterSink(t, h.brk, "after-deadline-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}})
	req.ExpiresAt = isoFromNow(-time.Minute)
	publishApproverRequest(t, h, req)
	waitPending(t, h.core, requestID)

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}},
		map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: `{"channel_id":"C0OPS","ts":"1.1"}`})

	late := h.auditor.outcome(t, requestID, sysauth.HILApprovalLate)
	if late.Answerer != aliceLogin {
		t.Errorf("late audit = %+v, want alice", late)
	}
	if got := sink(300 * time.Millisecond); got != nil {
		t.Fatalf("an answer after the deadline reached the requester: %+v", got)
	}
	told := deliveries(3 * time.Second)
	if told == nil || told.PromptOutcome == nil || !strings.Contains(told.PromptOutcome.Refusal, "Expired") {
		t.Errorf("the clicker was told %+v, want a refusal naming Expired", told)
	}
	if pr, err := h.core.pending.Get(t.Context(), requestID); err != nil || pr == nil || pr.AnsweredBy != nil {
		t.Errorf("the row after the late answer = %+v (err %v), want no answerer recorded", pr, err)
	}
}

func resolvedNotices(inbox []NotificationDTO) int {
	count := 0
	for _, n := range inbox {
		if n.Title == msgResolvedTitle {
			count++
		}
	}
	return count
}

func noLateAudit(t *testing.T, h *approverHarness) {
	t.Helper()
	for _, e := range h.auditor.snapshot() {
		if e.Outcome == sysauth.HILApprovalLate {
			t.Errorf("the counted answerer's own redelivered answer was audited as late: %+v", e)
		}
	}
}

func TestApproverSet_ARedeliveredAnswerFinishesWhatTheDeadPodLeftUndone(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-redelivered"
	sink := requesterSink(t, h.brk, "redelivered-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	h.promptRow(t, bobLogin, requestID)
	pr := mustPending(t, h, requestID)
	_, claimed, err := h.core.pending.ClaimResolution(t.Context(), requestID, store.ApproverSet(*pr.Approvers), store.Resolution{
		Status: "answered", Decision: a2a.NotifyOutcomeApproved, AnsweredBy: aliceLogin, AnsweredByName: aliceLogin, AnsweredVia: "slack",
	})
	if err != nil || !claimed {
		t.Fatalf("claim the row as the pod that then died: claimed %v, err %v", claimed, err)
	}

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}},
		map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"})

	if got := sink(3 * time.Second); got == nil || got.Status != "answered" || got.Data["approved"] != true {
		t.Fatalf("the answer was not routed to the requester: %+v", got)
	}
	h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted)
	noLateAudit(t, h)
	told := func() bool {
		return slices.ContainsFunc(h.inbox(t, bobLogin), func(n NotificationDTO) bool {
			return n.Title == msgResolvedTitle && strings.Contains(n.Body, aliceLogin)
		})
	}
	for deadline := time.Now().Add(3 * time.Second); !told() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if !told() {
		t.Error("bob was not told that alice answered")
	}
}

func TestApproverSet_TheCountedAnswerersRepeatedClickNotifiesTheOthersOnce(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-double-click"
	sink := requesterSink(t, h.brk, "double-click-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	h.promptRow(t, bobLogin, requestID)
	approve := a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}
	alice := map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"}

	h.publishReply(t, approve, alice)
	h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted)
	h.publishReply(t, approve, alice)
	for range 2 {
		if got := sink(3 * time.Second); got == nil {
			t.Fatal("the answer was not routed to the requester both times")
		}
	}

	noLateAudit(t, h)
	waitForResolvedNotice(t, h, bobLogin)
	for deadline := time.Now().Add(500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got := resolvedNotices(h.inbox(t, bobLogin)); got != 1 {
			t.Fatalf("bob holds %d notices that the request was answered, want exactly one", got)
		}
	}
}

const approverUUID = "0192f3a4-0000-7000-8000-0000000000aa"

func breakAliasStore(t *testing.T, h *approverHarness) {
	t.Helper()
	if _, err := h.core.db.ExecContext(t.Context(), `DROP TABLE notify_user_aliases`); err != nil {
		t.Fatalf("break alias store: %v", err)
	}
}

func TestApproverSet_AnAliasLookupFailureIsNotAnApproverRefusal(t *testing.T) {
	h := newApproverHarness(t, true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-alias-error"
	sink := requesterSink(t, h.brk, "alias-error-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{approverUUID}}))
	waitPending(t, h.core, requestID)
	breakAliasStore(t, h)

	var acked, nacked atomic.Bool
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyReplyTopic(h.ns, requestID), nil, func() { acked.Store(true) }, func() { nacked.Store(true) })
	msg.UserProperties = map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-alice")}
	h.core.dispatchReply(t.Context(), msg,
		a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}, mustPending(t, h, requestID))

	if !nacked.Load() || acked.Load() {
		t.Errorf("nacked = %v, acked = %v, want the answer returned to the queue so it is retried", nacked.Load(), acked.Load())
	}
	if told := outcomes(t, deliveries, 1)[0]; told.PromptOutcome.Refusal != msgNotRecorded {
		t.Errorf("the answerer was told %q, want %q", told.PromptOutcome.Refusal, msgNotRecorded)
	}
	for _, e := range h.auditor.snapshot() {
		if e.Outcome == sysauth.HILApprovalRefused {
			t.Errorf("a lookup failure was audited as a refusal: %+v", e)
		}
	}
	if got := sink(300 * time.Millisecond); got != nil {
		t.Errorf("an unverified answer reached the requester: %+v", got)
	}
}

func TestApproverSet_AnAliasLookupFailureOnTheWebIsAServerError(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-alias-error-web"
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{approverUUID}}))
	waitPending(t, h.core, requestID)
	breakAliasStore(t, h)
	caller := auth.NewTestSubject(auth.SubjectFields{UserID: aliceLogin})

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/respond", nil).WithContext(auth.ContextWithSubject(t.Context(), caller))
	_, ok := h.core.authorizeInboxAnswer(rr, r, callerKey(caller), mustPending(t, h, requestID))

	if ok || rr.Code != http.StatusInternalServerError {
		t.Errorf("authorizeInboxAnswer = ok %v, status %d, want a 500 that is not a refusal", ok, rr.Code)
	}
}

func mustPending(t *testing.T, h *approverHarness, requestID string) *store.PendingRequest {
	t.Helper()
	pr, err := h.core.pending.Get(t.Context(), requestID)
	if err != nil || pr == nil {
		t.Fatalf("pending request %s = %+v (err %v)", requestID, pr, err)
	}
	return pr
}

type auditLoggerWithoutHILApprovals struct{ sysauth.AuditLogger }

func TestApproverSet_AnAuditLoggerThatCannotRecordApprovalsIsReportedAtStartup(t *testing.T) {
	const warning = "notify: the audit logger does not record approver-set events; refusals reach the operational log only"
	cases := map[string]struct {
		flagOn bool
		audit  sysauth.AuditLogger
		warned bool
	}{
		"logger without the approval method": {true, auditLoggerWithoutHILApprovals{sysauth.NoopAuditLogger{}}, true},
		"logger with the approval method":    {true, &recordingAuditor{}, false},
		"the no-op logger":                   {true, sysauth.NoopAuditLogger{}, false},
		"a logger that embeds the no-op one": {true, struct{ sysauth.NoopAuditLogger }{}, true},
		"flag off":                           {false, auditLoggerWithoutHILApprovals{sysauth.NoopAuditLogger{}}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			logs := &syncBuffer{}
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: dropTimestamp})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			startApproverHarness(t, tc.flagOn, tc.audit, nil)

			if got := strings.Contains(logs.String(), warning); got != tc.warned {
				t.Errorf("startup warned = %v, want %v", got, tc.warned)
			}
		})
	}
}

type brokenInbox struct{}

func (brokenInbox) Add(context.Context, string, string, string, map[string]any, string, string) (string, error) {
	return "", errors.New("inbox unavailable")
}

func TestApproverSet_APlaceAloneDoesNotCountAsDelivered(t *testing.T) {
	h := newApproverHarness(t, true)
	h.advertiseSlack(true)
	deliveries := h.slackDeliveries(t)
	h.core.dispatcher.inbox = brokenInbox{}
	const requestID = "hil-approval-place-only-delivered"
	sink := requesterSink(t, h.brk, "place-only-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	place := a2a.NotifyPlace{ChannelType: "slack", Address: json.RawMessage(`{"channel_id":"C0OPS"}`)}
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{bobLogin}, Places: []a2a.NotifyPlace{place}}))

	if post := deliveries(3 * time.Second); post == nil || !post.ApproverSet {
		t.Fatalf("the place was not posted: %+v", post)
	}
	got := sink(3 * time.Second)
	if got == nil || got.Status != "cancelled" || got.Data["reason"] != refusalDeliveryFailed {
		t.Fatalf("requester got %+v, want cancelled with reason %q because no approver could be reached", got, refusalDeliveryFailed)
	}
	h.auditor.outcome(t, requestID, sysauth.HILApprovalUndeliverable)
}
