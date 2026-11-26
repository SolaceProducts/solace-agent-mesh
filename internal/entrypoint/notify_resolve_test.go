// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func connectedBroker(t *testing.T) *broker.DevBroker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func resolveRequest(t *testing.T, brk broker.Broker, replyTo string) (*broker.Message, error) {
	t.Helper()
	payload, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return brk.Request(t.Context(), a2a.NotifyResolveTopic("test-ns", "slack"), &broker.Message{Payload: payload, ReplyTo: replyTo}, 400*time.Millisecond)
}

func TestStartNotifyResolveConsumer_AnswersAndAdvertises(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	core.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "slack", Interactable: true}, slog.Default(), func(a2a.NotifyDeliver) {})
	if got := core.AdvertisedNotifyCapabilities(); got == nil || got.ResolvesAddress {
		t.Fatalf("before the resolve consumer: advertised = %+v, want not resolving", got)
	}

	seen := make(chan a2a.NotifyResolveRequest, 1)
	core.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), func(_ context.Context, req a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		seen <- req
		return a2a.NotifyResolveResponse{Address: json.RawMessage(`{"user_id":"U1"}`)}
	})
	want := a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true, Interactable: true, ResolvesAddress: true}
	if got := core.AdvertisedNotifyCapabilities(); got == nil || *got != want {
		t.Fatalf("advertised = %+v, want %+v", got, want)
	}

	reply, err := resolveRequest(t, brk, a2a.NotifyResolveReplyTopic("test-ns", "r1"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	var resp a2a.NotifyResolveResponse
	if err := json.Unmarshal(reply.Payload, &resp); err != nil || string(resp.Address) != `{"user_id":"U1"}` || resp.Error != "" {
		t.Fatalf("reply = %s (%v), want the adapter's address", reply.Payload, err)
	}
	if req := <-seen; req.Email != "u1@example.com" || req.UserID != "u1" {
		t.Fatalf("adapter saw %+v", req)
	}
}

func TestStartNotifyResolveConsumer_RefusesAForeignReplyTopic(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	core.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), func(a2a.NotifyDeliver) {})
	called := make(chan struct{}, 1)
	core.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		called <- struct{}{}
		return a2a.NotifyResolveResponse{}
	})

	if reply, err := resolveRequest(t, brk, "test-ns/sam/v1/notify/deliver/slack/hijack"); err == nil {
		t.Fatalf("got a reply %s for a foreign reply topic, want none", reply.Payload)
	}
	select {
	case <-called:
		t.Error("the adapter ran a lookup for a request with a foreign reply topic")
	default:
	}
}

func isoAt(offset time.Duration) string {
	return time.Now().Add(offset).UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func decodeResolveReply(t *testing.T, reply *broker.Message) a2a.NotifyResolveResponse {
	t.Helper()
	var resp a2a.NotifyResolveResponse
	if err := json.Unmarshal(reply.Payload, &resp); err != nil {
		t.Fatalf("decode reply %s: %v", reply.Payload, err)
	}
	return resp
}

func resolveReplyError(t *testing.T, reply *broker.Message) string {
	t.Helper()
	return decodeResolveReply(t, reply).Error
}

func assertAnsweredWithTheAddress(t *testing.T, name string, replies []*broker.Message) {
	t.Helper()
	if len(replies) != 1 {
		t.Errorf("%s: got %d answers, want 1", name, len(replies))
		return
	}
	if resp := decodeResolveReply(t, replies[0]); resp.Error != "" || string(resp.Address) != `{"user_id":"U1"}` {
		t.Errorf("%s: answer = %+v, want the looked-up address", name, resp)
	}
}

func TestStartNotifyResolveConsumer_AnswersUnavailableWhenItCannotLookUp(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	core.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), func(a2a.NotifyDeliver) {})
	lookups := 0
	core.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		lookups++
		return a2a.NotifyResolveResponse{Address: json.RawMessage(`{"user_id":"U1"}`)}
	})

	emptyEmail, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string][]byte{"not json": []byte("{"), "no email": emptyEmail} {
		reply, err := brk.Request(t.Context(), a2a.NotifyResolveTopic("test-ns", "slack"), &broker.Message{Payload: payload, ReplyTo: a2a.NotifyResolveReplyTopic("test-ns", "r1")}, 400*time.Millisecond)
		if err != nil {
			t.Fatalf("%s: no answer: %v", name, err)
		}
		if got := resolveReplyError(t, reply); got != a2a.NotifyResolveUnavailable {
			t.Errorf("%s: answered %q, want %q", name, got, a2a.NotifyResolveUnavailable)
		}
	}
	if lookups != 0 {
		t.Errorf("the adapter ran %d lookups for requests it cannot use", lookups)
	}
}

func TestAnswerNotifyResolve_AcksEveryRequestOnce(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	good, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com", ExpiresAt: isoAt(-2 * notifyResolveClockGrace)})
	if err != nil {
		t.Fatal(err)
	}
	reply := a2a.NotifyResolveReplyTopic("test-ns", "r1")
	ok := func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		return a2a.NotifyResolveResponse{Address: json.RawMessage(`{"user_id":"U1"}`)}
	}
	panics := func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse { panic("lookup blew up") }

	cases := []struct {
		name        string
		payload     []byte
		replyTo     string
		redelivered bool
		resolve     func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse
	}{
		{"answered", good, reply, false, ok},
		{"foreign reply topic", good, "test-ns/sam/v1/notify/deliver/slack/x", false, ok},
		{"redelivered", good, reply, true, ok},
		{"expired", expired, reply, false, ok},
		{"malformed", []byte("{"), reply, false, ok},
		{"panicking lookup", good, reply, false, panics},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acks, nacks := 0, 0
			msg := deliveredResolve(tc.payload, tc.replyTo, tc.redelivered, func() { acks++ }, func() { nacks++ })
			core.answerNotifyResolve(t.Context(), brk, msg, slog.Default(), tc.resolve)
			if acks != 1 || nacks != 0 {
				t.Errorf("acked %d times and nacked %d, want acked once and never nacked", acks, nacks)
			}
		})
	}
}

func deliveredResolve(payload []byte, replyTo string, redelivered bool, ack func(), nack ...func()) *broker.Message {
	onNack := func() {}
	if len(nack) > 0 {
		onNack = nack[0]
	}
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyResolveTopic("test-ns", "slack"), payload, ack, onNack)
	msg.ReplyTo = replyTo
	msg.Redelivered = redelivered
	return msg
}

func TestAnswerNotifyResolve_PanicLogNamesTheRequestAndCarriesTheStack(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	payload, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r-panic", UserID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	msg := deliveredResolve(payload, a2a.NotifyResolveReplyTopic("test-ns", "r1"), false, func() {})
	core.answerNotifyResolve(t.Context(), brk, msg, logger, func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		panic(errors.New("lookup for u1@example.com blew up"))
	})
	for _, want := range []string{`"level":"ERROR"`, `"requestID":"r-panic"`, `"panicType":"*errors.errorString"`, `"stack":"goroutine`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("panic log = %s, want it to contain %s", logs.String(), want)
		}
	}
	for _, secret := range []string{"u1@example.com", "blew up"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("panic log carries the panic value (%q): %s", secret, logs.String())
		}
	}
}

func answerWithReplies(t *testing.T, req a2a.NotifyResolveRequest, redelivered bool, resolve func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse) []*broker.Message {
	t.Helper()
	return answerWithRepliesLogged(t, req, redelivered, slog.Default(), resolve)
}

func answerWithRepliesLogged(t *testing.T, req a2a.NotifyResolveRequest, redelivered bool, logger *slog.Logger, resolve func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse) []*broker.Message {
	t.Helper()
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	replyTopic := a2a.NotifyResolveReplyTopic("test-ns", "r1")
	sink, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(sink, replyTopic); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	core.answerNotifyResolve(t.Context(), brk, deliveredResolve(payload, replyTopic, redelivered, func() {}), logger, resolve)

	var replies []*broker.Message
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		msg, err := brk.Receive(ctx, sink)
		cancel()
		if err != nil || msg == nil {
			return replies
		}
		replies = append(replies, msg)
	}
}

func TestAnswerNotifyResolve_AnswersARedeliveredRequestStillInsideItsWindow(t *testing.T) {
	req := a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com", ExpiresAt: isoAt(3 * time.Second)}
	replies := answerWithReplies(t, req, true, func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		return a2a.NotifyResolveResponse{Address: json.RawMessage(`{"user_id":"U1"}`)}
	})
	if len(replies) != 1 {
		t.Fatalf("got %d answers for a redelivered request that is still wanted, want 1", len(replies))
	}
}

func TestAnswerNotifyResolve_DropsARequestThatWentStaleInTheQueue(t *testing.T) {
	req := a2a.NotifyResolveRequest{RequestID: "r-stale", UserID: "u1", Email: "u1@example.com", ExpiresAt: isoAt(-30 * time.Second)}
	var logs bytes.Buffer
	replies := answerWithRepliesLogged(t, req, false, slog.New(slog.NewJSONHandler(&logs, nil)), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		t.Error("ran a lookup for a request nobody is waiting for")
		return a2a.NotifyResolveResponse{}
	})
	if len(replies) != 0 {
		t.Fatalf("answered a stale request: %d replies", len(replies))
	}
	for _, want := range []string{`"level":"WARN"`, `"requestID":"r-stale"`, `"correlationID"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("drop log = %s, want it to contain %s", logs.String(), want)
		}
	}
}

func TestAnswerNotifyResolve_ToleratesClockDifferencesBetweenPods(t *testing.T) {
	india := time.FixedZone("IST", 5*3600+1800)
	for name, expiresAt := range map[string]string{
		"UTC with milliseconds": isoAt(-10 * time.Second),
		"an offset":             time.Now().Add(-10 * time.Second).In(india).Format(time.RFC3339),
		"nanoseconds":           time.Now().Add(-10 * time.Second).UTC().Format(time.RFC3339Nano),
	} {
		req := a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com", ExpiresAt: expiresAt}
		replies := answerWithReplies(t, req, false, func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
			return a2a.NotifyResolveResponse{Address: json.RawMessage(`{"user_id":"U1"}`)}
		})
		assertAnsweredWithTheAddress(t, name, replies)
	}
	stale := a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com", ExpiresAt: time.Now().Add(-2 * time.Minute).In(india).Format(time.RFC3339)}
	if replies := answerWithReplies(t, stale, false, func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		t.Error("ran a lookup for a stale request given with an offset")
		return a2a.NotifyResolveResponse{}
	}); len(replies) != 0 {
		t.Errorf("answered a stale request given with an offset: %d replies", len(replies))
	}
}

func TestAnswerNotifyResolve_TreatsAnUnreadableDeadlineAsNone(t *testing.T) {
	req := a2a.NotifyResolveRequest{RequestID: "r-bad", UserID: "u1", Email: "u1@example.com", ExpiresAt: "soon"}
	var logs bytes.Buffer
	replies := answerWithRepliesLogged(t, req, false, slog.New(slog.NewJSONHandler(&logs, nil)), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		return a2a.NotifyResolveResponse{Address: json.RawMessage(`{"user_id":"U1"}`)}
	})
	assertAnsweredWithTheAddress(t, "unreadable deadline", replies)
	for _, want := range []string{`"level":"WARN"`, `"requestID":"r-bad"`, `"length":4`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log = %s, want it to contain %s", logs.String(), want)
		}
	}
	if strings.Contains(logs.String(), "soon") {
		t.Errorf("log carries the unreadable value: %s", logs.String())
	}
}

func TestAnswerNotifyResolve_CapsEveryLookup(t *testing.T) {
	for name, expiresAt := range map[string]string{"no deadline": "", "an hour away": isoAt(time.Hour)} {
		req := a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com", ExpiresAt: expiresAt}
		var capped time.Duration
		answerWithReplies(t, req, false, func(ctx context.Context, _ a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Errorf("%s: the lookup is unbounded", name)
			}
			capped = time.Until(deadline)
			return a2a.NotifyResolveResponse{}
		})
		if capped <= 3*time.Second || capped > 4*time.Second {
			t.Errorf("%s: lookup bounded to %v, want between 3s and 4s", name, capped)
		}
	}
}

func TestAnswerNotifyResolve_HandsBackARequestReceivedWhileStopping(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	payload, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r-stop", UserID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	replyTopic := a2a.NotifyResolveReplyTopic("test-ns", "r1")
	sink, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(sink, replyTopic); err != nil {
		t.Fatal(err)
	}
	acks, nacks := 0, 0
	msg := deliveredResolve(payload, replyTopic, false, func() { acks++ }, func() { nacks++ })
	msg.CorrelationID = "c-stop"
	ctx, stop := context.WithCancel(t.Context())
	stop()
	var logs bytes.Buffer
	core.answerNotifyResolve(ctx, brk, msg, slog.New(slog.NewJSONHandler(&logs, nil)), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		t.Error("ran a lookup after the gateway's context was done")
		return a2a.NotifyResolveResponse{}
	})
	if acks != 0 || nacks != 1 {
		t.Errorf("acks = %d, nacks = %d, want the request handed back once", acks, nacks)
	}
	for _, want := range []string{`"level":"INFO"`, `"requestID":"r-stop"`, `"correlationID":"c-stop"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("hand-back log = %s, want it to contain %s", logs.String(), want)
		}
	}
	recvCtx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if reply, err := brk.Receive(recvCtx, sink); err == nil && reply != nil {
		t.Errorf("published %s for a request that was handed back before its lookup", reply.Payload)
	}
}

func TestAnswerNotifyResolve_AnswersALookupThatRunsOutOfItsOwnTime(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	replyTopic := a2a.NotifyResolveReplyTopic("test-ns", "r1")
	sink, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(sink, replyTopic); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	acks, nacks := 0, 0
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyResolveTopic("test-ns", "slack"), payload, func() { acks++ }, func() { nacks++ })
	msg.ReplyTo = replyTopic

	prev := notifyResolveDeadline
	notifyResolveDeadline = 50 * time.Millisecond
	t.Cleanup(func() { notifyResolveDeadline = prev })
	core.answerNotifyResolve(t.Context(), brk, msg, slog.Default(), func(lookupCtx context.Context, _ a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		<-lookupCtx.Done()
		return a2a.NotifyResolveResponse{Error: a2a.NotifyResolveUnavailable}
	})
	if acks != 1 || nacks != 0 {
		t.Errorf("acks = %d, nacks = %d, want one ack and no nack", acks, nacks)
	}
	recvCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	reply, err := brk.Receive(recvCtx, sink)
	if err != nil || reply == nil || resolveReplyError(t, reply) != a2a.NotifyResolveUnavailable {
		t.Fatalf("reply = %v (%v), want an unavailable answer", reply, err)
	}
}

func TestStartNotifyResolveConsumer_LookupsEndWhenTheGatewayStops(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	ctx, stop := context.WithCancel(t.Context())
	core.StartNotifyDeliverConsumer(ctx, NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), func(a2a.NotifyDeliver) {})
	cancelledWithTheGateway := make(chan bool, 1)
	core.StartNotifyResolveConsumer(ctx, "slack", slog.Default(), func(lookupCtx context.Context, _ a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		stop()
		cancelledWithTheGateway <- lookupCtx.Err() != nil
		return a2a.NotifyResolveResponse{}
	})

	if _, err := resolveRequest(t, brk, a2a.NotifyResolveReplyTopic("test-ns", "r1")); err == nil {
		t.Error("got an answer for a lookup the stopping gateway cut short")
	}
	select {
	case cancelled := <-cancelledWithTheGateway:
		if !cancelled {
			t.Error("the lookup outlived the gateway's context")
		}
	case <-time.After(time.Second):
		t.Fatal("the consumer never ran the lookup")
	}
}

func TestAnswerNotifyResolve_HandsBackARequestCutShortByShutdown(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	replyTopic := a2a.NotifyResolveReplyTopic("test-ns", "r1")
	sink, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(sink, replyTopic); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(a2a.NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	acks, nacks := 0, 0
	msg := broker.NewDeliveredMessage("m1", a2a.NotifyResolveTopic("test-ns", "slack"), payload, func() { acks++ }, func() { nacks++ })
	msg.ReplyTo = replyTopic

	ctx, stop := context.WithCancel(t.Context())
	core.answerNotifyResolve(ctx, brk, msg, slog.Default(), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		stop()
		return a2a.NotifyResolveResponse{Error: a2a.NotifyResolveUnavailable}
	})
	if acks != 0 || nacks != 1 {
		t.Errorf("acks = %d, nacks = %d, want the request handed back once", acks, nacks)
	}
	recvCtx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if reply, err := brk.Receive(recvCtx, sink); err == nil && reply != nil {
		t.Errorf("published %s for a request that was handed back", reply.Payload)
	}
}

type idleThenCancelBroker struct {
	broker.Broker
	cancel context.CancelFunc
	calls  int
}

func (b *idleThenCancelBroker) Receive(ctx context.Context, _ broker.Queue) (*broker.Message, error) {
	b.calls++
	if b.calls == 1 {
		return nil, nil
	}
	b.cancel()
	return nil, ctx.Err()
}

func TestReceiveNotifyMessages_SkipsAnIdleReceive(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	brk := &idleThenCancelBroker{cancel: cancel}
	receiveNotifyMessages(ctx, brk, nil, slog.Default(), func(*broker.Message) { t.Error("dispatched an idle receive") })
	if brk.calls != 2 {
		t.Errorf("receive called %d times, want the idle one skipped then the cancelled one", brk.calls)
	}
}

type failingProvisioner struct{ broker.Broker }

func (failingProvisioner) ProvisionQueue(context.Context, string, broker.QueueOptions, ...string) error {
	return errors.New("not permitted")
}

func TestStartNotifyResolveConsumer_AdvertisesOnlyOnceTheQueueIsProvisioned(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, failingProvisioner{Broker: brk}, true)
	core.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), func(a2a.NotifyDeliver) {})
	core.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		return a2a.NotifyResolveResponse{}
	})
	if core.NotifyResolverBound() {
		t.Errorf("advertised %+v although the queue could not be provisioned", core.AdvertisedNotifyCapabilities())
	}
}

func TestNotifyResolverBound_FollowsWhatIsAdvertised(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	if core.NotifyResolverBound() {
		t.Fatal("bound before any consumer started")
	}
	core.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), func(a2a.NotifyDeliver) {})
	if core.NotifyResolverBound() {
		t.Fatal("bound with only the deliver consumer")
	}
	core.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		return a2a.NotifyResolveResponse{}
	})
	if !core.NotifyResolverBound() {
		t.Fatal("not bound once the resolve consumer started")
	}
}

func TestStartNotifyResolveConsumer_SurvivesAPanickingLookup(t *testing.T) {
	brk := connectedBroker(t)
	core := notifyDeliverCore(t, brk, true)
	core.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), func(a2a.NotifyDeliver) {})
	calls := 0
	core.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		calls++
		if calls == 1 {
			panic("lookup blew up")
		}
		return a2a.NotifyResolveResponse{Address: json.RawMessage(`{"user_id":"U1"}`)}
	})

	first, err := resolveRequest(t, brk, a2a.NotifyResolveReplyTopic("test-ns", "r1"))
	if err != nil {
		t.Fatalf("the panicking lookup must still be answered: %v", err)
	}
	if got := resolveReplyError(t, first); got != a2a.NotifyResolveUnavailable {
		t.Fatalf("panicking lookup answered %q, want %q", got, a2a.NotifyResolveUnavailable)
	}
	if _, err := resolveRequest(t, brk, a2a.NotifyResolveReplyTopic("test-ns", "r2")); err != nil {
		t.Fatalf("the consumer stopped after a panic: %v", err)
	}
}

func TestStartNotifyResolveConsumer_Gating(t *testing.T) {
	resolve := func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		return a2a.NotifyResolveResponse{Address: json.RawMessage(`{}`)}
	}
	(*GatewayCore)(nil).StartNotifyResolveConsumer(context.Background(), "slack", slog.Default(), resolve)

	brk := connectedBroker(t)
	noDeliver := notifyDeliverCore(t, brk, true)
	noDeliver.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), resolve)
	if got := noDeliver.AdvertisedNotifyCapabilities(); got != nil {
		t.Errorf("without a deliver consumer: advertised = %+v, want nothing", got)
	}

	other := notifyDeliverCore(t, brk, true)
	other.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "teams"}, slog.Default(), func(a2a.NotifyDeliver) {})
	other.StartNotifyResolveConsumer(t.Context(), "slack", slog.Default(), resolve)
	if got := other.AdvertisedNotifyCapabilities(); got == nil || got.ResolvesAddress {
		t.Errorf("resolve consumer for another type changed what teams advertises: %+v", got)
	}

	if reply, err := resolveRequest(t, brk, a2a.NotifyResolveReplyTopic("test-ns", "r1")); err == nil {
		t.Fatalf("got a reply %s although no resolve consumer was bound for slack", reply.Payload)
	}
}
