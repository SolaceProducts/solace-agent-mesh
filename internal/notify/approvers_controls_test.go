// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	sysauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const (
	carolLogin = "carol@example.com"
	erinLogin  = "erin@example.com"
)

func withCaller(t *testing.T, login string, scopes ...string) {
	t.Helper()
	approverCallers[login] = auth.NewTestSubject(auth.SubjectFields{UserID: login, Scopes: scopes})
	t.Cleanup(func() { delete(approverCallers, login) })
}

func TestApproverSet_TheInboxRefusesAnAnswerFromSomeoneNotListed(t *testing.T) {
	h := newApproverHarness(t, true)
	withCaller(t, carolLogin)
	const requestID = "hil-approval-inbox-outsider"
	published := requesterSink(t, h.brk, "inbox-outsider-sink", a2a.NotifyReplyTopic(h.ns, requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	waitPending(t, h.core, requestID)
	h.core.dispatcher.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: requestID, RecipientUserID: carolLogin, Mode: a2a.NotifyModeInput, Category: a2a.CategoryHIL,
		Title: "Approve the reroute?", Surface: json.RawMessage(`{"surfaceId":"s1"}`),
	})
	carolRow := h.promptRow(t, carolLogin, requestID)

	rr := h.respond(carolLogin, carolRow.ID, `{"status":"answered","data":{"approved":true}}`)

	if rr.Code != http.StatusForbidden {
		t.Errorf("an unlisted user's answer from her own inbox row = %d, want 403", rr.Code)
	}
	if got := published(300 * time.Millisecond); got != nil {
		t.Errorf("the refused answer was published to the reply topic: %+v", got)
	}
	refused := h.auditor.outcome(t, requestID, sysauth.HILApprovalRefused)
	if refused.Answerer != carolLogin || refused.Reason != refusalNotAnApprover || refused.Via != hilViaWeb {
		t.Errorf("refused audit = %+v, want carol refused as not an approver via the web", refused)
	}
}

func TestApproverSet_TheInboxRefusesAnApproverAnsweringAnotherApproversRow(t *testing.T) {
	h := newApproverHarness(t, true)
	withCaller(t, erinLogin, "*")
	const requestID = "hil-approval-inbox-delegate"
	published := requesterSink(t, h.brk, "inbox-delegate-sink", a2a.NotifyReplyTopic(h.ns, requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, erinLogin}}))
	aliceRow := h.promptRow(t, aliceLogin, requestID)
	h.promptRow(t, erinLogin, requestID)

	rr := h.serve(erinLogin, http.MethodPost, "/api/v1/notify/inbox/"+aliceRow.ID+"/respond?userId="+aliceLogin,
		`{"status":"answered","data":{"approved":true}}`)

	if rr.Code != http.StatusForbidden {
		t.Errorf("an approver answering on alice's row = %d, want 403", rr.Code)
	}
	if got := published(300 * time.Millisecond); got != nil {
		t.Errorf("an answer recorded under alice's row was published: %+v", got)
	}
}

func TestDispatcher_AnApproverSetPromptSkipsAChannelWhoseGatewaysCannotVerifyAnswerers(t *testing.T) {
	cases := map[string]struct {
		approverSets  bool
		wantDelivered int
	}{
		"gateway cannot verify answerers": {false, 0},
		"gateway verifies answerers":      {true, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			pub := &fakeProactivePub{}
			caps := staticCapabilities{"slack": {Advertised: true, Notifiable: true, Interactable: true, ApproverSets: tc.approverSets}}
			d := proactiveDispatcherWith(pub, &fakeLearned{addrs: map[string]string{"slack": `{"user_id":"U1"}`}}, nil, caps)

			recs := d.Deliver(t.Context(), a2a.NotifyRequest{
				RequestID: "hil-approval-dm", RecipientUserID: aliceLogin, Mode: a2a.NotifyModeInput, Title: "Approve?",
				Approvers: &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}},
			})

			if len(pub.calls) != tc.wantDelivered {
				t.Fatalf("delivers published = %d (%+v), want %d", len(pub.calls), pub.calls, tc.wantDelivered)
			}
			if tc.wantDelivered == 0 && (len(recs) != 1 || recs[0].Status != "skipped" || recs[0].Error != msgNoVerifiedAnswerer) {
				t.Errorf("records = %+v, want one skipped record saying the channel cannot verify who answers", recs)
			}
			if tc.wantDelivered == 1 && !pub.calls[0].ApproverSet {
				t.Errorf("deliver = %+v, want it marked as an approver-set prompt", pub.calls[0])
			}
		})
	}
}
