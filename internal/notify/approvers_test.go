// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	sysauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	aliceLogin   = "alice@example.com"
	bobLogin     = "bob@example.com"
	malloryLogin = "mallory@example.com"
	callerHeader = "X-Test-Caller"
)

var approverCallers = map[string]*auth.Subject{
	aliceLogin:   auth.NewTestSubject(auth.SubjectFields{UserID: aliceLogin}),
	bobLogin:     auth.NewTestSubject(auth.SubjectFields{UserID: bobLogin}),
	malloryLogin: auth.NewTestSubject(auth.SubjectFields{UserID: malloryLogin, Scopes: []string{"*"}}),
}

type recordingAuditor struct {
	sysauth.NoopAuditLogger
	mu     sync.Mutex
	events []sysauth.HILApprovalEvent
}

func (r *recordingAuditor) LogHILApproval(_ context.Context, e sysauth.HILApprovalEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingAuditor) outcome(t *testing.T, requestID, outcome string) sysauth.HILApprovalEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, e := range r.events {
			if e.RequestID == requestID && e.Outcome == outcome {
				r.mu.Unlock()
				return e
			}
		}
		r.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %q audit event for %s; have %+v", outcome, requestID, r.snapshot())
	return sysauth.HILApprovalEvent{}
}

func (r *recordingAuditor) refusal(t *testing.T, requestID, reason string) sysauth.HILApprovalEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range r.snapshot() {
			if e.RequestID == requestID && e.Outcome == sysauth.HILApprovalRefused && e.Reason == reason {
				return e
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no refusal with reason %q for %s; have %+v", reason, requestID, r.snapshot())
	return sysauth.HILApprovalEvent{}
}

func (r *recordingAuditor) snapshot() []sysauth.HILApprovalEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

type approverHarness struct {
	core    *NotifyCore
	brk     broker.Broker
	ns      string
	auditor *recordingAuditor
}

func newApproverHarness(t *testing.T, flagOn bool) *approverHarness {
	t.Helper()
	auditor := &recordingAuditor{}
	h := startApproverHarness(t, flagOn, auditor, nil)
	h.auditor = auditor
	return h
}

func startApproverHarness(t *testing.T, flagOn bool, audit sysauth.AuditLogger, serviceBroker func(broker.Broker) broker.Broker) *approverHarness {
	t.Helper()
	if flagOn {
		t.Setenv("SAM_FEATURE_HIL_APPROVER_SETS", "true")
	} else {
		t.Setenv("SAM_FEATURE_HIL_APPROVER_SETS", "false")
	}
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	var svcBroker broker.Broker = brk
	if serviceBroker != nil {
		svcBroker = serviceBroker(brk)
	}
	rt := runtime.NewRuntime(svcBroker, nil, runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(reg), runtime.WithAuditLogger(audit))
	appConfig := map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}
	core := NewNotifyCore(appConfig, NotifyExtensions{AuthMiddleware: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, ok := approverCallers[r.Header.Get(callerHeader)]
			if !ok {
				http.Error(w, "unknown test caller", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), caller)))
		})
	}})
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return &approverHarness{core: core, brk: brk, ns: "test-ns"}
}

func (h *approverHarness) serve(caller, method, path, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(callerHeader, caller)
	h.core.handler.ServeHTTP(rr, req)
	return rr
}

func (h *approverHarness) inbox(t *testing.T, caller string) []NotificationDTO {
	t.Helper()
	rr := h.serve(caller, http.MethodGet, "/api/v1/notify/inbox", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET inbox as %s = %d, body=%s", caller, rr.Code, rr.Body.String())
	}
	var page PaginatedResponse[NotificationDTO]
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	return page.Data
}

func (h *approverHarness) promptRow(t *testing.T, caller, requestID string) NotificationDTO {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range h.inbox(t, caller) {
			if n.RequestID == requestID {
				return n
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never received prompt %s", caller, requestID)
	return NotificationDTO{}
}

func (h *approverHarness) respond(caller, rowID, body string) *httptest.ResponseRecorder {
	return h.serve(caller, http.MethodPost, "/api/v1/notify/inbox/"+rowID+"/respond", body)
}

func (h *approverHarness) publishReply(t *testing.T, resp a2a.UserInputResponseData, props map[string]string) {
	t.Helper()
	data, _ := json.Marshal(resp)
	if err := h.brk.PublishGuaranteed(context.Background(), a2a.NotifyReplyTopic(h.ns, resp.RequestID),
		&broker.Message{Payload: data, UserProperties: props}); err != nil {
		t.Fatalf("publish reply: %v", err)
	}
}

func (h *approverHarness) slackDeliveries(t *testing.T) func(time.Duration) *a2a.NotifyDeliver {
	t.Helper()
	q, err := h.brk.CreateQueue("slack-deliver-sink", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create deliver queue: %v", err)
	}
	if err := h.brk.Subscribe(q, a2a.NotifyDeliverSubscription(h.ns, "slack")); err != nil {
		t.Fatalf("subscribe deliver: %v", err)
	}
	return func(timeout time.Duration) *a2a.NotifyDeliver {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		msg, err := h.brk.Receive(ctx, q)
		if err != nil {
			return nil
		}
		msg.Ack()
		var d a2a.NotifyDeliver
		if err := json.Unmarshal(msg.Payload, &d); err != nil {
			t.Fatalf("decode deliver: %v", err)
		}
		return &d
	}
}

func (h *approverHarness) advertiseSlack(approverSets bool) {
	h.core.capabilities.Observe(gatewayCard("slack-gw", &a2a.NotifyCapabilities{
		ChannelType: "slack", Notifiable: true, Interactable: true, ApproverSets: approverSets,
	}), "slack-1")
}

func newApproverSetRequest(ns, requestID string, set *a2a.NotifyApprovers) a2a.NotifyRequest {
	first := ""
	if len(set.UserIDs) > 0 {
		first = set.UserIDs[0]
	}
	return a2a.NotifyRequest{
		RequestID:       requestID,
		RecipientUserID: first,
		Mode:            a2a.NotifyModeInput,
		SenderAgent:     "ops-agent",
		Category:        a2a.CategoryHIL,
		RequesterAddr:   a2a.NotifyResponseTopic(ns, "ops-agent", requestID),
		Title:           "Approve the reroute?",
		Surface:         json.RawMessage(`{"surfaceId":"s1"}`),
		ExpiresAt:       isoFromNow(time.Hour),
		Approvers:       set,
	}
}

func publishApproverRequest(t *testing.T, h *approverHarness, req a2a.NotifyRequest) {
	t.Helper()
	data, _ := json.Marshal(req)
	if err := h.brk.PublishGuaranteed(context.Background(),
		a2a.NotifyRequestTopic(h.ns, cmpOr(req.RecipientUserID, "approvers")), &broker.Message{Payload: data}); err != nil {
		t.Fatalf("publish request: %v", err)
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestApproverSet_FirstAnswerWinsAndOthersSeeWhoAnswered(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-two-users"
	sink := requesterSink(t, h.brk, "two-users-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))

	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))

	aliceRow := h.promptRow(t, aliceLogin, requestID)
	bobRow := h.promptRow(t, bobLogin, requestID)
	asked := h.auditor.outcome(t, requestID, sysauth.HILApprovalAsked)
	if !slices.Equal(asked.Approvers, []string{aliceLogin, bobLogin}) {
		t.Errorf("audited approvers = %v, want alice and bob", asked.Approvers)
	}

	if rr := h.respond(aliceLogin, aliceRow.ID, `{"status":"answered","data":{"approved":true}}`); rr.Code != http.StatusNoContent {
		t.Fatalf("alice respond = %d, body=%s", rr.Code, rr.Body.String())
	}
	got := sink(3 * time.Second)
	if got == nil || got.Status != "answered" || got.Data["approved"] != true {
		t.Fatalf("alice's answer was not routed to the requester: %+v", got)
	}
	accepted := h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted)
	if accepted.Answerer != aliceLogin || accepted.Via != hilViaWeb || accepted.Decision != "approved" {
		t.Errorf("accepted audit = %+v, want alice via web approving", accepted)
	}

	if rr := h.respond(bobLogin, bobRow.ID, `{"status":"answered","data":{"approved":false}}`); rr.Code != http.StatusConflict {
		t.Errorf("bob's late answer = %d, want 409 already answered", rr.Code)
	}
	if extra := sink(300 * time.Millisecond); extra != nil {
		t.Errorf("a second answer reached the requester: %+v", extra)
	}

	resolved := h.promptRow(t, bobLogin, requestID)
	if resolved.PromptState != promptStateResolved || resolved.PromptAnsweredBy != aliceLogin {
		t.Errorf("bob's copy = state %q answeredBy %q, want resolved by alice", resolved.PromptState, resolved.PromptAnsweredBy)
	}
	if !slices.ContainsFunc(h.inbox(t, bobLogin), func(n NotificationDTO) bool {
		return n.Title == "Approval request answered" && strings.Contains(n.Body, aliceLogin)
	}) {
		t.Error("bob was not told that alice answered")
	}
	if slices.ContainsFunc(h.inbox(t, aliceLogin), func(n NotificationDTO) bool { return n.Title == "Approval request answered" }) {
		t.Error("the answerer was told about her own answer")
	}
}

func TestApproverSet_LateReplyOnTheBrokerIsAuditedAsLate(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-late"
	sink := requesterSink(t, h.brk, "late-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, bobLogin}}))
	waitPending(t, h.core, requestID)

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}},
		map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"})
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("alice's answer was not routed")
	}
	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": false}},
		map[string]string{a2a.PropUserID: bobLogin, a2a.PropChannel: "slack"})
	late := h.auditor.outcome(t, requestID, sysauth.HILApprovalLate)
	if late.Answerer != bobLogin || late.Via != "slack" {
		t.Errorf("late audit = %+v, want bob via slack", late)
	}
	if extra := sink(300 * time.Millisecond); extra != nil {
		t.Errorf("a late answer reached the requester: %+v", extra)
	}
}

func TestApproverSet_OutsiderAnswerRefusedAndAudited(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-outsider"
	sink := requesterSink(t, h.brk, "outsider-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}))
	aliceRow := h.promptRow(t, aliceLogin, requestID)

	t.Run("a chat answer from someone not listed", func(t *testing.T) {
		h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}},
			map[string]string{a2a.PropUserID: malloryLogin, a2a.PropChannel: "slack"})
		refused := h.auditor.outcome(t, requestID, sysauth.HILApprovalRefused)
		if refused.Answerer != malloryLogin || refused.Reason != refusalNotAnApprover {
			t.Errorf("refused audit = %+v, want mallory as not an approver", refused)
		}
		if got := sink(300 * time.Millisecond); got != nil {
			t.Fatalf("an outsider's answer reached the requester: %+v", got)
		}
	})

	t.Run("an inbox answer on an approver's row with the send-others scope", func(t *testing.T) {
		rr := h.serve(malloryLogin, http.MethodPost, "/api/v1/notify/inbox/"+aliceRow.ID+"/respond?userId="+aliceLogin,
			`{"status":"answered","data":{"approved":true}}`)
		if rr.Code != http.StatusForbidden {
			t.Errorf("delegated answer = %d, want 403", rr.Code)
		}
	})

	t.Run("a system principal named as the answerer", func(t *testing.T) {
		h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}},
			map[string]string{a2a.PropUserID: "system:ops-agent", a2a.PropChannel: "slack"})
		h.auditor.refusal(t, requestID, refusalSystemAnswerer)
		if got := sink(300 * time.Millisecond); got != nil {
			t.Fatalf("a system principal's answer reached the requester: %+v", got)
		}
	})

	if rr := h.respond(aliceLogin, aliceRow.ID, `{"status":"answered","data":{"approved":true}}`); rr.Code != http.StatusNoContent {
		t.Fatalf("alice could not answer after the refusals: %d", rr.Code)
	}
	if got := sink(3 * time.Second); got == nil || got.Status != "answered" {
		t.Fatalf("alice's answer was not routed: %+v", got)
	}
}

func TestApproverSet_AnAnswerLookedUpBeforeTheRequestWasStoredIsStillChecked(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-approval-stale-lookup"
	sink := requesterSink(t, h.brk, "stale-lookup-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{bobLogin}}))
	waitPending(t, h.core, requestID)
	approve := a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}

	h.core.dispatchReply(t.Context(), &broker.Message{UserProperties: map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"}}, approve, nil)

	refused := h.auditor.outcome(t, requestID, sysauth.HILApprovalRefused)
	if refused.Answerer != aliceLogin || refused.Reason != refusalNotAnApprover {
		t.Errorf("refused audit = %+v, want alice refused as not an approver", refused)
	}
	if got := sink(300 * time.Millisecond); got != nil {
		t.Fatalf("a non-approver's answer reached the requester: %+v", got)
	}
	if pr, err := h.core.pending.Get(t.Context(), requestID); err != nil || pr == nil || pr.Status != "pending" {
		t.Fatalf("the request after the refused answer = %+v (err %v), want it still pending", pr, err)
	}

	h.publishReply(t, approve, map[string]string{a2a.PropUserID: bobLogin, a2a.PropChannel: "slack"})
	if got := sink(3 * time.Second); got == nil || got.Status != "answered" {
		t.Fatalf("the approver's answer was not routed: %+v", got)
	}
	if accepted := h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted); accepted.Answerer != bobLogin {
		t.Errorf("accepted audit = %+v, want bob", accepted)
	}
}

func TestApproverSet_RequestRefusedBeforeAnyoneIsAsked(t *testing.T) {
	cases := map[string]struct {
		flagOn bool
		set    *a2a.NotifyApprovers
		reason string
	}{
		"system principal approver": {true, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin, "system:ops-agent"}}, refusalApproverSystem},
		"groups":                    {true, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}, Groups: []string{"ops"}}, refusalApproverGroups},
		"places only":               {true, &a2a.NotifyApprovers{Places: []a2a.NotifyPlace{{ChannelType: "slack", Address: json.RawMessage(`{"channel_id":"C1"}`)}}}, refusalApproverNoUser},
		"malformed place":           {true, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}, Places: []a2a.NotifyPlace{{ChannelType: "slack", Address: json.RawMessage(`"C1"`)}}}, refusalApproverPlaceInvalid},
		"flag off":                  {false, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}}, refusalApproversDisabled},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newApproverHarness(t, tc.flagOn)
			requestID := "hil-approval-refused-" + strings.ReplaceAll(name, " ", "-")
			sink := requesterSink(t, h.brk, requestID+"-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
			publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, tc.set))

			got := sink(3 * time.Second)
			if got == nil || got.Status != "cancelled" || got.Data["reason"] != tc.reason {
				t.Fatalf("requester got %+v, want cancelled with reason %q", got, tc.reason)
			}
			if pr, err := h.core.pending.Get(t.Context(), requestID); err != nil || pr != nil {
				t.Errorf("a refused request left a pending row: %+v (err %v)", pr, err)
			}
			if inbox := h.inbox(t, aliceLogin); len(inbox) != 0 {
				t.Errorf("a refused request was delivered: %+v", inbox)
			}
			h.auditor.outcome(t, requestID, sysauth.HILApprovalRequestDenied)
		})
	}
}

func TestApproverSet_PlacePostAndChatAnswers(t *testing.T) {
	h := newApproverHarness(t, true)
	h.advertiseSlack(true)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-place"
	sink := requesterSink(t, h.brk, "place-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	place := a2a.NotifyPlace{ChannelType: "slack", Address: json.RawMessage(`{"channel_id":"C0OPS"}`)}
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}, Places: []a2a.NotifyPlace{place}}))

	post := deliveries(3 * time.Second)
	if post == nil || !post.ApproverSet || post.UserID != "" || post.Mode != a2a.NotifyModeInput || string(post.Address) != string(place.Address) {
		t.Fatalf("place post = %+v, want one approver-set input prompt addressed to the place", post)
	}

	t.Run("an answer the gateway could not map to a user", func(t *testing.T) {
		h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}, nil)
		refused := h.auditor.outcome(t, requestID, sysauth.HILApprovalRefused)
		if refused.Reason != refusalUnverifiedAnswerer || refused.Answerer != "" {
			t.Errorf("refused audit = %+v, want an unverified answerer", refused)
		}
		if got := sink(300 * time.Millisecond); got != nil {
			t.Fatalf("an unmapped answer reached the requester: %+v", got)
		}
	})

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": false}},
		map[string]string{a2a.PropUserID: aliceLogin, a2a.PropChannel: "slack"})
	if got := sink(3 * time.Second); got == nil || got.Data["approved"] != false {
		t.Fatalf("alice's answer from the place was not routed: %+v", got)
	}
	notice := deliveries(3 * time.Second)
	if notice == nil || notice.Mode != a2a.NotifyModeNotify || notice.Message != "Declined by "+aliceLogin+"." {
		t.Errorf("place resolution notice = %+v, want who answered and the decision", notice)
	}
	pr, err := h.core.pending.Get(t.Context(), requestID)
	if err != nil || pr == nil || pr.AnsweredBy == nil || *pr.AnsweredBy != aliceLogin || pr.AnsweredVia == nil || *pr.AnsweredVia != "slack" {
		t.Errorf("pending row = %+v (err %v), want answered by alice via slack", pr, err)
	}
}

func TestApproverSet_PlaceSkippedWhenItsGatewayCannotVerifyAnswerers(t *testing.T) {
	h := newApproverHarness(t, true)
	h.advertiseSlack(false)
	deliveries := h.slackDeliveries(t)
	const requestID = "hil-approval-old-gateway"
	sink := requesterSink(t, h.brk, "old-gw-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	place := a2a.NotifyPlace{ChannelType: "slack", Address: json.RawMessage(`{"channel_id":"C0OPS"}`)}
	publishApproverRequest(t, h, newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}, Places: []a2a.NotifyPlace{place}}))

	h.promptRow(t, aliceLogin, requestID)
	if d := deliveries(300 * time.Millisecond); d != nil {
		t.Fatalf("posted to a place whose gateway does not handle approver sets: %+v", d)
	}
	if got := sink(300 * time.Millisecond); got != nil {
		t.Fatalf("the request failed although alice's inbox was reached: %+v", got)
	}
}

func TestApproverSet_ReplyWithoutAnswererStillRoutesForASingleRecipient(t *testing.T) {
	h := newApproverHarness(t, true)
	const requestID = "hil-single-recipient"
	sink := requesterSink(t, h.brk, "single-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{UserIDs: []string{aliceLogin}})
	req.Approvers = nil
	publishApproverRequest(t, h, req)
	waitPending(t, h.core, requestID)

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"approved": true}}, nil)
	if got := sink(3 * time.Second); got == nil || got.Status != "answered" {
		t.Fatalf("a single-recipient answer from a gateway that names nobody was not routed: %+v", got)
	}
	if events := h.auditor.snapshot(); len(events) != 0 {
		t.Errorf("a single-recipient request was audited as an approver set: %+v", events)
	}
}

func TestApproverSet_RequestWireShapeIsAdditive(t *testing.T) {
	plain, err := json.Marshal(a2a.NotifyRequest{RequestID: "r", RecipientUserID: aliceLogin, Mode: a2a.NotifyModeInput})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "approvers") {
		t.Errorf("a request without approvers carries the key: %s", plain)
	}
	deliver, err := json.Marshal(a2a.NotifyDeliver{RequestID: "r", Mode: a2a.NotifyModeInput})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(deliver), "approver_set") {
		t.Errorf("a single-recipient deliver carries the marker: %s", deliver)
	}
}

func TestApproverSet_LogsCarryNoQuestionOrAnswer(t *testing.T) {
	const (
		questionCanary = "CANARY-approval-question-must-not-reach-logs"
		titleCanary    = "CANARY-approval-title-must-not-reach-logs"
		answerCanary   = "CANARY-approval-answer-must-not-reach-logs"
	)
	logs := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: dropTimestamp})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := newApproverHarness(t, true)
	h.advertiseSlack(true)
	const requestID = "hil-approval-canary"
	sink := requesterSink(t, h.brk, "canary-sink", a2a.NotifyResponseTopic(h.ns, "ops-agent", requestID))
	req := newApproverSetRequest(h.ns, requestID, &a2a.NotifyApprovers{
		UserIDs: []string{aliceLogin, bobLogin},
		Places:  []a2a.NotifyPlace{{ChannelType: "slack", Address: json.RawMessage(`{"channel_id":"C0OPS"}`)}},
	})
	req.Title = titleCanary
	req.Message = questionCanary
	req.Surface = json.RawMessage(`{"surfaceId":"s1","title":"` + questionCanary + `"}`)
	publishApproverRequest(t, h, req)
	waitPending(t, h.core, requestID)

	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"note": answerCanary}},
		map[string]string{a2a.PropUserID: malloryLogin, a2a.PropChannel: "slack"})
	h.auditor.outcome(t, requestID, sysauth.HILApprovalRefused)
	h.publishReply(t, a2a.UserInputResponseData{RequestID: requestID, Status: "answered", Data: map[string]any{"note": answerCanary}},
		map[string]string{a2a.PropUserID: bobLogin, a2a.PropChannel: "slack"})
	if got := sink(3 * time.Second); got == nil {
		t.Fatal("bob's answer was not routed")
	}
	h.auditor.outcome(t, requestID, sysauth.HILApprovalAccepted)

	captured := logs.String()
	if !strings.Contains(captured, "notify: approver-set prompt delivered") {
		t.Fatal("the capture holds no approver-set record; the canary would pass vacuously")
	}
	for _, marker := range []string{questionCanary, titleCanary, answerCanary} {
		if strings.Contains(captured, marker) {
			t.Errorf("%s reached the logs", marker)
		}
	}
}
