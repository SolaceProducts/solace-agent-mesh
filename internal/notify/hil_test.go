// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func startCore(t *testing.T, dbURL string) (*NotifyCore, broker.Broker, string) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return startCoreOn(t, brk, dbURL)
}

func startCoreOn(t *testing.T, brk broker.Broker, dbURL string) (*NotifyCore, broker.Broker, string) {
	t.Helper()
	ctx := t.Context()
	const namespace = "test-ns"
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	appConfig := map[string]any{"namespace": namespace}
	if dbURL != "" {
		appConfig["database_url"] = dbURL
	}
	core := NewNotifyCore(appConfig, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return core, brk, namespace
}

func startRouterCore(t *testing.T) (*NotifyCore, broker.Broker, string) {
	t.Helper()
	return startCore(t, "sqlite://"+t.TempDir()+"/notify.db")
}

func requesterSink(t *testing.T, brk broker.Broker, name, addr string) func(timeout time.Duration) *a2a.UserInputResponseData {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create requester queue: %v", err)
	}
	if err := brk.Subscribe(q, addr); err != nil {
		t.Fatalf("subscribe requester: %v", err)
	}
	return func(timeout time.Duration) *a2a.UserInputResponseData {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			return nil
		}
		var resp a2a.UserInputResponseData
		if uerr := json.Unmarshal(msg.Payload, &resp); uerr != nil {
			t.Fatalf("unmarshal routed response: %v", uerr)
		}
		msg.Ack()
		return &resp
	}
}

func publishReq(t *testing.T, brk broker.Broker, ns string, req a2a.NotifyRequest) {
	t.Helper()
	data, _ := json.Marshal(req)
	if err := brk.PublishGuaranteed(context.Background(),
		a2a.NotifyRequestTopic(ns, req.RecipientUserID), &broker.Message{Payload: data}); err != nil {
		t.Fatalf("publish request: %v", err)
	}
}

func publishReply(t *testing.T, brk broker.Broker, ns string, resp a2a.UserInputResponseData) {
	t.Helper()
	data, _ := json.Marshal(resp)
	if err := brk.PublishGuaranteed(context.Background(),
		a2a.NotifyReplyTopic(ns, resp.RequestID), &broker.Message{Payload: data}); err != nil {
		t.Fatalf("publish reply: %v", err)
	}
}

func waitPending(t *testing.T, core *NotifyCore, requestID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pr, err := core.pending.Get(context.Background(), requestID)
		if err != nil {
			t.Fatalf("Get pending: %v", err)
		}
		if pr != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pending request %s never persisted", requestID)
}

func isoFromNow(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

func TestHILRouter_RoutesReplyToRequester(t *testing.T) {
	core, brk, ns := startRouterCore(t)

	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-A")
	sink := requesterSink(t, brk, "sink-A", addr)

	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID:       "req-A",
		RecipientUserID: "trent@example.org",
		Mode:            a2a.NotifyModeInput,
		RequesterAddr:   addr,
		Title:           "Approve?",
		Message:         "approve the deploy",
		ExpiresAt:       isoFromNow(time.Hour),
	})
	waitPending(t, core, "req-A")

	publishReply(t, brk, ns, a2a.UserInputResponseData{
		RequestID: "req-A", Status: "answered", Data: map[string]any{"choice": "approve"},
	})

	got := sink(3 * time.Second)
	if got == nil {
		t.Fatal("no response routed to the requester")
	}
	if got.Status != "answered" || got.Data["choice"] != "approve" {
		t.Fatalf("routed response mismatch: %+v", got)
	}
	if got.Type != string(a2a.SignalUserInputResponse) {
		t.Errorf("response Type should default to %q, got %q", a2a.SignalUserInputResponse, got.Type)
	}

	publishReply(t, brk, ns, a2a.UserInputResponseData{RequestID: "req-A", Status: "answered"})
	if dup := sink(400 * time.Millisecond); dup != nil {
		t.Errorf("duplicate reply must be dropped (first-wins), got %+v", dup)
	}
}

func TestHILRouter_TimeoutRoutesTimedOut(t *testing.T) {
	core, brk, ns := startRouterCore(t)

	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-T")
	sink := requesterSink(t, brk, "sink-T", addr)

	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID:       "req-T",
		RecipientUserID: "trent@example.org",
		Mode:            a2a.NotifyModeInput,
		RequesterAddr:   addr,
		Message:         "expired prompt",
		ExpiresAt:       isoFromNow(-time.Minute),
	})
	waitPending(t, core, "req-T")

	core.sweepTimeouts(t.Context())

	got := sink(3 * time.Second)
	if got == nil || got.Status != "timed_out" {
		t.Fatalf("expected timed_out routed to the requester, got %+v", got)
	}

	publishReply(t, brk, ns, a2a.UserInputResponseData{RequestID: "req-T", Status: "answered"})
	if late := sink(400 * time.Millisecond); late != nil {
		t.Errorf("a reply after timeout must be dropped, got %+v", late)
	}
}

func TestHILRouter_InputWithoutRequesterAddrRejected(t *testing.T) {
	core, brk, ns := startRouterCore(t)

	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID: "req-X", RecipientUserID: "u", Mode: a2a.NotifyModeInput, Message: "x",
	})
	time.Sleep(200 * time.Millisecond)

	pr, err := core.pending.Get(t.Context(), "req-X")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if pr != nil {
		t.Errorf("input without requester_addr must not persist a pending row, got %+v", pr)
	}
}

func TestHILRouter_DeliveredReceiptIsNotAnAnswer(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-D")
	sink := requesterSink(t, brk, "sink-D", addr)

	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID: "req-D", RecipientUserID: "trent@example.org", Mode: a2a.NotifyModeInput,
		RequesterAddr: addr, Message: "approve?", ExpiresAt: isoFromNow(time.Hour),
	})
	waitPending(t, core, "req-D")

	publishReply(t, brk, ns, a2a.UserInputResponseData{RequestID: "req-D", Status: "delivered"})
	if got := sink(400 * time.Millisecond); got != nil {
		t.Fatalf("a delivery receipt must not route a response, got %+v", got)
	}
	if pr, _ := core.pending.Get(t.Context(), "req-D"); pr == nil || pr.Status != "pending" {
		t.Fatalf("request must remain pending after a delivery receipt, got %+v", pr)
	}

	publishReply(t, brk, ns, a2a.UserInputResponseData{RequestID: "req-D", Status: "answered", Data: map[string]any{"ok": true}})
	if got := sink(3 * time.Second); got == nil || got.Status != "answered" {
		t.Fatalf("the real answer must route after the receipt, got %+v", got)
	}
}

func TestHILRouter_UndeliverablePromptFailsFast(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-U")
	sink := requesterSink(t, brk, "sink-U", addr)

	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID: "req-U", RecipientUserID: "trent@example.org", Mode: a2a.NotifyModeInput,
		RequesterAddr: addr, Message: "approve?", Channels: []string{"email"}, ExpiresAt: isoFromNow(time.Hour),
	})

	got := sink(3 * time.Second)
	if got == nil || got.Status != "cancelled" {
		t.Fatalf("undeliverable prompt should route a cancelled response, got %+v", got)
	}
	if got.Data["reason"] != "delivery_failed" {
		t.Errorf("cancelled response should carry reason=delivery_failed, got %v", got.Data["reason"])
	}
	if pr, _ := core.pending.Get(t.Context(), "req-U"); pr == nil || pr.Status != "failed" {
		t.Errorf("undeliverable request should be marked failed, got %+v", pr)
	}
}

func TestHILRouter_DedupDropsDuplicatePrompt(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-DD")

	for _, id := range []string{"req-DD-1", "req-DD-2"} {
		publishReq(t, brk, ns, a2a.NotifyRequest{
			RequestID: id, RecipientUserID: "dedup@example.org", Mode: a2a.NotifyModeInput,
			RequesterAddr: addr, Message: "approve?", Dedup: "same-key", ExpiresAt: isoFromNow(time.Hour),
		})
	}

	deadline := time.Now().Add(3 * time.Second)
	delivered := false
	for time.Now().Before(deadline) {
		rows, err := core.inbox.ListByUser(t.Context(), "dedup@example.org", store.InboxFilter{}, 10, 0)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(rows) > 1 {
			t.Fatalf("dedup'd re-publish must deliver exactly one prompt, got %d inbox rows", len(rows))
		}
		if len(rows) == 1 {
			delivered = true
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !delivered {
		t.Fatal("no prompt delivered for the dedup'd requests in time")
	}
}

func TestHILRouter_DBlessInputModeDropped(t *testing.T) {
	core, brk, ns := startCore(t, "")
	if core.pending != nil {
		t.Fatal("precondition: DB-less core must have no pending store")
	}
	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-NoDB")
	sink := requesterSink(t, brk, "sink-nodb", addr)

	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID: "req-NoDB", RecipientUserID: "u", Mode: a2a.NotifyModeInput,
		RequesterAddr: addr, Message: "x",
	})
	if got := sink(400 * time.Millisecond); got != nil {
		t.Errorf("DB-less input-mode request must not route anything, got %+v", got)
	}
}

func TestHILRouter_ReplyRouteFailureReopensForRetry(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-RF")
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: "req-RF", RequesterAddr: addr, RecipientUserID: "u",
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	_ = brk.Disconnect(context.Background())

	payload, _ := json.Marshal(a2a.UserInputResponseData{RequestID: "req-RF", Status: "answered"})
	core.handleReply(t.Context(), &broker.Message{Payload: payload})

	pr, err := core.pending.Get(t.Context(), "req-RF")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if pr == nil || pr.Status != "pending" {
		t.Fatalf("a route failure must re-open the row for retry, got %+v", pr)
	}
}

func TestHILRouter_TimeoutRouteFailureReopens(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-TF")
	past := time.Now().Add(-time.Minute).UnixMilli()
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: "req-TF", RequesterAddr: addr, ExpiresAt: &past,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	_ = brk.Disconnect(context.Background())

	core.sweepTimeouts(t.Context())

	pr, err := core.pending.Get(t.Context(), "req-TF")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if pr == nil || pr.Status != "pending" {
		t.Fatalf("a timeout route failure must re-open the row, got %+v", pr)
	}
}

func TestHILRouter_MalformedAndMissingRepliesAreDropped(t *testing.T) {
	core, _, _ := startRouterCore(t)
	core.handleReply(t.Context(), &broker.Message{Payload: []byte(`{not json`)})
	noID, _ := json.Marshal(a2a.UserInputResponseData{Status: "answered"})
	core.handleReply(t.Context(), &broker.Message{Payload: noID})
	unknown, _ := json.Marshal(a2a.UserInputResponseData{RequestID: "ghost", Status: "answered"})
	core.handleReply(t.Context(), &broker.Message{Payload: unknown})
}

func TestHILRouter_UnparseableExpiresAtIsAccepted(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-BE")
	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID: "req-BE", RecipientUserID: "trent@example.org", Mode: a2a.NotifyModeInput,
		RequesterAddr: addr, Message: "approve?", ExpiresAt: "not-a-timestamp",
	})
	waitPending(t, core, "req-BE")
	pr, _ := core.pending.Get(t.Context(), "req-BE")
	if pr == nil || pr.ExpiresAt != nil {
		t.Fatalf("a bad expires_at should persist with no deadline, got %+v", pr)
	}
}

func TestHILRouter_ClaimErrorNacksForRedelivery(t *testing.T) {
	core, _, ns := startRouterCore(t)
	if err := core.pending.Insert(t.Context(), store.PendingRequest{
		RequestID: "req-CE", RequesterAddr: a2a.NotifyResponseTopic(ns, "agent-1", "req-CE"),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := core.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	nacked := false
	msg := broker.NewDeliveredMessage("m1", "t", mustJSONReply(t, "req-CE", "answered"),
		func() {}, func() { nacked = true })
	core.handleReply(t.Context(), msg)
	if !nacked {
		t.Error("a persistent claim error must Nack the reply for redelivery, not drop it")
	}
}

func TestHILRouter_ReplyLoopBacksOffOnReceiveError(t *testing.T) {
	core, brk, _ := startRouterCore(t)
	q, err := brk.CreateQueue("rl-test", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	_ = brk.Disconnect(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { core.replyLoop(ctx, q); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("replyLoop did not exit after context cancellation")
	}
}

func mustJSONReply(t *testing.T, requestID, status string) []byte {
	t.Helper()
	b, err := json.Marshal(a2a.UserInputResponseData{RequestID: requestID, Status: status})
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}
	return b
}

func TestParseExpiresAtMillis(t *testing.T) {
	if got := parseExpiresAtMillis(""); got != nil {
		t.Errorf("empty string → nil (no deadline), got %v", *got)
	}
	if got := parseExpiresAtMillis("not-a-timestamp"); got != nil {
		t.Errorf("unparseable → nil (no deadline), got %v", *got)
	}
	got := parseExpiresAtMillis("2026-06-08T12:00:00.500+00:00")
	if got == nil {
		t.Fatal("valid ISO 8601 with millis must parse")
	}
	want := time.Date(2026, 6, 8, 12, 0, 0, 500*int(time.Millisecond), time.UTC).UnixMilli()
	if *got != want {
		t.Errorf("parsed epoch ms = %d, want %d", *got, want)
	}
}
