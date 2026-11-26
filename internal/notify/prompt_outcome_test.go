// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const (
	danaUserID = "0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e22"
	danaCaller = "dana-by-users-id"
)

func cardAt(channel, ts string) map[string]any {
	return map[string]any{"channel_id": channel, "ts": ts, "blocks": []any{map[string]any{"type": "section"}}}
}

func (h *approverHarness) reportCard(t *testing.T, requestID string, card map[string]any, want int) {
	t.Helper()
	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: a2a.HILStatusDelivered, Data: map[string]any{"card": card}},
		map[string]string{a2a.PropChannel: "slack"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cards, err := h.core.cards.List(t.Context(), requestID); err == nil && len(cards) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%d posted cards never recorded for %s", want, requestID)
}

func outcomes(t *testing.T, next func(time.Duration) *a2a.NotifyDeliver, n int) []*a2a.NotifyDeliver {
	t.Helper()
	var got []*a2a.NotifyDeliver
	for len(got) < n {
		d := next(3 * time.Second)
		if d == nil {
			t.Fatalf("got %d prompt outcomes, want %d: %+v", len(got), n, got)
		}
		if d.PromptOutcome != nil {
			got = append(got, d)
		}
	}
	return got
}

func clickCard(user string) string {
	return `{"channel_id":"D-` + user + `","ts":"1.9","user_id":"` + user + `"}`
}

func danaRequest(t *testing.T, h *approverHarness, requestID string) {
	t.Helper()
	approverCallers[danaCaller] = auth.NewTestSubject(auth.SubjectFields{UserID: aliceLogin, PlatformUserID: danaUserID})
	t.Cleanup(func() { delete(approverCallers, danaCaller) })
	if err := h.core.db.RecordAlias(t.Context(), aliceLogin, danaUserID); err != nil {
		t.Fatal(err)
	}
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{
		UserIDs: []string{danaUserID, bobLogin}, Names: map[string]string{danaUserID: "Dana Kim"},
	}))
	waitPending(t, h.core, requestID)
}

func TestPromptOutcome_AnAnswerSettlesEveryPostedCard(t *testing.T) {
	h := newApproverHarness(t, true)
	next := h.slackDeliveries(t)
	const requestID = "hil-approval-cards"
	sink := requesterSink(t, h.brk, "cards-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	danaRequest(t, h, requestID)
	h.reportCard(t, requestID, cardAt("D-dana", "1.1"), 1)
	h.reportCard(t, requestID, cardAt("D-bob", "1.2"), 2)

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}},
		map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-dana")})
	if got := sink(3 * time.Second); got == nil || got.Data["approved"] != true {
		t.Fatalf("Dana's answer was not routed: %+v", got)
	}

	var places []string
	for _, d := range outcomes(t, next, 2) {
		var card map[string]any
		_ = json.Unmarshal(d.Address, &card)
		places = append(places, card["channel_id"].(string))
		if d.RequestID != requestID || d.PromptOutcome.Outcome != a2a.NotifyOutcomeApproved || d.PromptOutcome.Text != "Approved by Dana Kim" || d.PromptOutcome.Refusal != "" {
			t.Errorf("outcome = %+v, want approved by Dana Kim", d.PromptOutcome)
		}
	}
	slices.Sort(places)
	if !slices.Equal(places, []string{"D-bob", "D-dana"}) {
		t.Errorf("settled cards = %v, want both posted cards", places)
	}
	forgotten := func() bool {
		cards, err := h.core.cards.List(t.Context(), requestID)
		return err == nil && len(cards) == 0
	}
	for deadline := time.Now().Add(3 * time.Second); !forgotten() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if !forgotten() {
		t.Error("posted cards are still recorded after settling, want them forgotten")
	}

	toldBob := func() bool {
		return slices.ContainsFunc(h.inbox(t, bobLogin), func(n NotificationDTO) bool {
			return n.Title == msgResolvedTitle && n.Body == "Approved by Dana Kim."
		})
	}
	for deadline := time.Now().Add(3 * time.Second); !toldBob() && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if !toldBob() {
		t.Errorf("bob was not told by name: %+v", h.inbox(t, bobLogin))
	}
	if slices.ContainsFunc(h.inbox(t, danaCaller), func(n NotificationDTO) bool { return n.Title == msgResolvedTitle }) {
		t.Error("Dana was told about her own answer")
	}
	for _, caller := range []string{bobLogin, danaCaller} {
		row := h.promptRow(t, caller, requestID)
		if row.PromptState != promptStateResolved || row.PromptOutcome != a2a.NotifyOutcomeApproved || row.PromptAnsweredBy != "Dana Kim" {
			t.Errorf("%s's copy = %q %q %q, want resolved, approved by Dana Kim", caller, row.PromptState, row.PromptOutcome, row.PromptAnsweredBy)
		}
	}
}

func TestPromptOutcome_ARefusedClickIsToldWhy(t *testing.T) {
	h := newApproverHarness(t, true)
	next := h.slackDeliveries(t)
	const requestID = "hil-approval-refusals"
	sink := requesterSink(t, h.brk, "refusals-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	danaRequest(t, h, requestID)
	answer := a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}

	h.publishReply(t, answer, map[string]string{a2a.PropUserID: malloryLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-mallory")})
	refused := outcomes(t, next, 1)[0]
	if refused.PromptOutcome.Refusal != msgNotAnApprover || string(refused.Address) != clickCard("U-mallory") {
		t.Errorf("outsider's verdict = %+v at %s, want not an approver at the clicked card", refused.PromptOutcome, refused.Address)
	}
	if got := sink(300 * time.Millisecond); got != nil {
		t.Fatalf("an outsider's answer reached the requester: %+v", got)
	}

	h.publishReply(t, answer, map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"})
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("Dana's answer was not routed")
	}
	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": false}},
		map[string]string{a2a.PropUserID: bobLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-bob")})
	late := outcomes(t, next, 1)[0]
	if late.PromptOutcome.Refusal != msgAnswerTooLate+"Approved by Dana Kim." || late.PromptOutcome.Text != "Approved by Dana Kim" {
		t.Errorf("late verdict = %+v, want already settled by Dana", late.PromptOutcome)
	}

	gone := a2a.UserInputResponseData{RequestID: "hil-approval-never-asked", Status: "answered", Data: map[string]any{"approved": true}}
	h.publishReply(t, gone, map[string]string{a2a.PropUserID: bobLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-bob")})
	orphan := outcomes(t, next, 1)[0]
	if orphan.RequestID != gone.RequestID || orphan.PromptOutcome.Refusal != msgNoLongerOpen {
		t.Errorf("verdict for a request not on record = %+v, want no longer open", orphan.PromptOutcome)
	}
}

func TestPromptOutcome_AnExpiredRequestSettlesItsCards(t *testing.T) {
	h := newApproverHarness(t, true)
	next := h.slackDeliveries(t)
	const requestID = "hil-approval-expired"
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{bobLogin}})
	req.ExpiresAt = isoFromNow(-time.Second)
	publishApproverRequest(t, h, req)
	waitPending(t, h.core, requestID)
	h.reportCard(t, requestID, cardAt("D-bob", "1.3"), 1)

	h.core.sweepTimeouts(context.Background())

	settled := outcomes(t, next, 1)[0]
	if settled.PromptOutcome.Outcome != a2a.NotifyOutcomeExpired || settled.PromptOutcome.Text != "Expired" {
		t.Errorf("outcome = %+v, want expired", settled.PromptOutcome)
	}
	if row := h.promptRow(t, bobLogin, requestID); row.PromptOutcome != a2a.NotifyOutcomeExpired {
		t.Errorf("inbox outcome = %q, want expired", row.PromptOutcome)
	}
}

func TestPromptOutcome_AWithdrawnRequestSaysTheRunEnded(t *testing.T) {
	h := newApproverHarness(t, true)
	next := h.slackDeliveries(t)
	const requestID = "hil-approval-withdrawn"
	requester := a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID)
	sink := requesterSink(t, h.brk, "withdrawn-sink", requester)
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{bobLogin}}))
	waitPending(t, h.core, requestID)
	h.reportCard(t, requestID, cardAt("D-bob", "1.4"), 1)
	withdrawal := a2a.UserInputResponseData{RequestID: requestID, Status: a2a.HILStatusWithdrawn}

	h.publishReply(t, withdrawal, map[string]string{a2a.PropReplyTo: "test-ns/someone/else"})
	time.Sleep(200 * time.Millisecond)
	if pr, err := h.core.pending.Get(t.Context(), requestID); err != nil || pr == nil || pr.Status != "pending" {
		t.Fatalf("a withdrawal from elsewhere changed the request: %+v (err %v)", pr, err)
	}

	h.publishReply(t, withdrawal, map[string]string{a2a.PropReplyTo: requester})
	settled := outcomes(t, next, 1)[0]
	if settled.PromptOutcome.Outcome != a2a.NotifyOutcomeWithdrawn || settled.PromptOutcome.Text != "No longer needed: the run ended" {
		t.Errorf("outcome = %+v, want withdrawn", settled.PromptOutcome)
	}
	if row := h.promptRow(t, bobLogin, requestID); row.PromptState != promptStateResolved || row.PromptOutcome != a2a.NotifyOutcomeWithdrawn {
		t.Errorf("inbox copy = %q %q, want resolved as withdrawn", row.PromptState, row.PromptOutcome)
	}
	if got := sink(300 * time.Millisecond); got != nil {
		t.Errorf("a withdrawal was routed back to the requester: %+v", got)
	}

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}},
		map[string]string{a2a.PropUserID: bobLogin, a2a.PropChannel: "slack", a2a.PropNotifyCard: clickCard("U-bob")})
	if late := outcomes(t, next, 1)[0]; !strings.Contains(late.PromptOutcome.Refusal, "No longer needed") {
		t.Errorf("an answer after the withdrawal = %+v, want it refused as late", late.PromptOutcome)
	}
}

func TestPromptOutcome_ACardPostedAfterTheAnswerIsSettledAtOnce(t *testing.T) {
	h := newApproverHarness(t, true)
	next := h.slackDeliveries(t)
	const requestID = "hil-approval-late-card"
	sink := requesterSink(t, h.brk, "late-card-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	danaRequest(t, h, requestID)
	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": false}},
		map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"})
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("Dana's answer was not routed")
	}

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: a2a.HILStatusDelivered, Data: map[string]any{"card": cardAt("D-bob", "1.5")}},
		map[string]string{a2a.PropChannel: "slack"})
	settled := outcomes(t, next, 1)[0]
	if settled.PromptOutcome.Text != "Declined by Dana Kim" {
		t.Errorf("outcome = %+v, want declined by Dana Kim", settled.PromptOutcome)
	}
}
