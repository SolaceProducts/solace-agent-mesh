// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migration

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var movedQueue = MovedQueue{Queue: "ns/q/old", Topics: []string{"ns/a/>", "ns/b/>"}, Idle: 20 * time.Millisecond}

func ackAll(_ context.Context, msg *broker.Message) { msg.Ack() }

func TestMoveQueue(t *testing.T) {
	t.Parallel()
	head := []string{"bind ns/q/old", "unsubscribe ns/a/>", "unsubscribe ns/b/>"}
	tail := []string{"close", "delete ns/q/old"}
	full := slices.Concat(head, []string{"receive", "receive", "receive"}, tail)
	cases := []struct {
		name                                      string
		bindErr, removeErr, deleteErr, receiveErr error
		messages                                  []string
		wantCalls                                 []string
		wantSettled                               []string
		wantLevels                                []string
		wantErr                                   bool
		wantFound                                 bool
	}{
		{name: "moves the queue in order", messages: []string{"m1", "m2"}, wantCalls: full, wantSettled: []string{"ack m1", "ack m2"}, wantLevels: []string{"INFO"}, wantFound: true},
		{name: "empty queue", wantCalls: slices.Concat(head, []string{"receive"}, tail), wantLevels: []string{"INFO"}, wantFound: true},
		{name: "queue already gone", bindErr: errMissing, wantCalls: []string{"bind ns/q/old"}},
		{name: "broker without the capability", bindErr: errors.ErrUnsupported, wantCalls: []string{"bind ns/q/old"}, wantLevels: []string{"WARN"}},
		{name: "bind refused", bindErr: errRefused, wantCalls: []string{"bind ns/q/old"}, wantLevels: []string{"ERROR"}},
		{name: "bind fails for now", bindErr: errTransient, wantCalls: []string{"bind ns/q/old"}, wantErr: true},
		{name: "unsubscribe refused, delete succeeds", removeErr: errRefused, messages: []string{"m1"}, wantCalls: slices.Concat(head, []string{"receive", "receive"}, tail), wantSettled: []string{"ack m1"}, wantLevels: []string{"INFO"}, wantFound: true},
		{name: "unsubscribe fails for now", removeErr: errTransient, messages: []string{"m1"}, wantCalls: []string{"bind ns/q/old", "unsubscribe ns/a/>", "close"}, wantErr: true, wantFound: true},
		{name: "receive fails for now", receiveErr: errTransient, messages: []string{"m1"}, wantCalls: slices.Concat(head, []string{"receive", "close"}), wantErr: true, wantFound: true},
		{name: "delete refused after unsubscribe", deleteErr: errRefused, wantCalls: slices.Concat(head, []string{"receive"}, tail), wantLevels: []string{"WARN"}},
		{name: "unsubscribe and delete refused", removeErr: errRefused, deleteErr: errRefused, wantCalls: slices.Concat(head, []string{"receive"}, tail), wantLevels: []string{"ERROR"}},
		{name: "delete fails for now", deleteErr: errTransient, wantCalls: slices.Concat(head, []string{"receive"}, tail), wantErr: true, wantFound: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &fakeBroker{bindErr: tc.bindErr, removeErr: tc.removeErr, deleteErr: tc.deleteErr, receiveErr: tc.receiveErr, messages: tc.messages}
			logger, levels := captureLogs()

			found, err := MoveQueue(t.Context(), b, movedQueue, ackAll, logger)

			if (err != nil) != tc.wantErr {
				t.Fatalf("MoveQueue err = %v, want error: %v", err, tc.wantErr)
			}
			if found != tc.wantFound {
				t.Errorf("look again = %v, want %v", found, tc.wantFound)
			}
			if !slices.Equal(b.calls, tc.wantCalls) {
				t.Errorf("steps = %q\nwant    %q", b.calls, tc.wantCalls)
			}
			if !slices.Equal(b.settled, tc.wantSettled) {
				t.Errorf("settled = %q, want %q", b.settled, tc.wantSettled)
			}
			if got := levels(); !slices.Equal(got, tc.wantLevels) {
				t.Errorf("log levels = %v, want %v", got, tc.wantLevels)
			}
		})
	}
}

func TestMoveQueue_NackedMessageIsDeliveredAgain(t *testing.T) {
	t.Parallel()
	b := &fakeBroker{messages: []string{"m1", "m2"}}
	nacked := false
	handle := func(_ context.Context, msg *broker.Message) {
		if string(msg.Payload) == "m1" && !nacked {
			nacked = true
			msg.Nack()
			return
		}
		msg.Ack()
	}

	if _, err := MoveQueue(t.Context(), b, movedQueue, handle, slog.Default()); err != nil {
		t.Fatalf("MoveQueue: %v", err)
	}
	if want := []string{"nack m1", "ack m1", "ack m2"}; !slices.Equal(b.settled, want) {
		t.Errorf("settled = %q, want %q", b.settled, want)
	}
	if last := b.calls[len(b.calls)-1]; last != "delete ns/q/old" {
		t.Errorf("last step = %q, want the delete", last)
	}
}

func TestMoveQueue_ContextEndsMidDrain(t *testing.T) {
	t.Parallel()
	b := &fakeBroker{messages: []string{"m1", "m2"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	handle := func(_ context.Context, msg *broker.Message) {
		msg.Ack()
		cancel()
	}

	_, err := MoveQueue(ctx, b, movedQueue, handle, slog.Default())

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("MoveQueue err = %v, want context.Canceled", err)
	}
	if slices.Contains(b.calls, "delete ns/q/old") {
		t.Errorf("steps = %q; a queue that is not known empty must not be deleted", b.calls)
	}
	if !slices.Contains(b.calls, "close") {
		t.Errorf("steps = %q; the bind must be released", b.calls)
	}
	if want := []string{"ack m1"}; !slices.Equal(b.settled, want) {
		t.Errorf("settled = %q, want %q", b.settled, want)
	}
}

func TestMoveQueue_NetworkDevBrokerLeavesLiveSubscriptionsAlone(t *testing.T) {
	t.Parallel()
	server := broker.NewDevBrokerServer(nil, nil)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatalf("Start server: %v", err)
	}
	defer func() { _ = server.Stop() }()

	const topic = "ns/in/x"
	consumer := broker.NewNetworkDevBroker(addr, "move-consumer", "cq", nil)
	if err = consumer.Connect(t.Context()); err != nil {
		t.Fatalf("Connect consumer: %v", err)
	}
	defer func() { _ = consumer.Disconnect(t.Context()) }()

	replacement, err := consumer.CreateQueue("ns/q/new", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue replacement: %v", err)
	}
	if err = consumer.Subscribe(replacement, topic); err != nil {
		t.Fatalf("Subscribe replacement: %v", err)
	}

	publisher := broker.NewNetworkDevBroker(addr, "move-publisher", "pq", nil)
	if err = publisher.Connect(t.Context()); err != nil {
		t.Fatalf("Connect publisher: %v", err)
	}
	defer func() { _ = publisher.Disconnect(t.Context()) }()

	m := MovedQueue{Queue: "ns/q/old", Topics: []string{topic}, Idle: 20 * time.Millisecond}
	found, err := MoveQueue(t.Context(), consumer, m, ackAll, slog.Default())
	if err != nil {
		t.Fatalf("MoveQueue: %v", err)
	}
	if found {
		t.Error("MoveQueue reported the queue found; a NetworkDevBroker cannot bind an existing queue, so there is nothing to move")
	}

	if err = publisher.Publish(t.Context(), topic, &broker.Message{Payload: []byte("after-move")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := consumer.Receive(ctx, replacement)
	if err != nil {
		t.Fatalf("Receive on replacement after MoveQueue: %v (regression: the move stripped the session subscription)", err)
	}
	if msg == nil || !strings.Contains(string(msg.Payload), "after-move") {
		t.Fatalf("replacement received %v; want a payload containing \"after-move\"", msg)
	}
}

func TestDrain(t *testing.T) {
	t.Parallel()
	b := &fakeBroker{messages: []string{"m1", "m2", "m3"}}
	q, _ := b.BindExisting("ns/q/old")
	var seen []string
	handle := func(_ context.Context, msg *broker.Message) {
		seen = append(seen, string(msg.Payload))
		msg.Ack()
	}

	n, err := Drain(t.Context(), q, 20*time.Millisecond, handle)

	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if n != 3 || !slices.Equal(seen, []string{"m1", "m2", "m3"}) {
		t.Errorf("drained %d %q, want 3 in order", n, seen)
	}
}

func TestMoveQueue_DevBroker(t *testing.T) {
	t.Parallel()
	b := broker.NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	const topic = "ns/in/>"
	m := MovedQueue{Queue: "ns/q/old", Topics: []string{topic}, Idle: 20 * time.Millisecond}
	old, err := b.CreateQueue(m.Queue, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue old: %v", err)
	}
	if subErr := b.Subscribe(old, topic); subErr != nil {
		t.Fatalf("Subscribe old: %v", subErr)
	}
	for _, p := range []string{"m1", "m2", "m3"} {
		if pubErr := b.Publish(t.Context(), "ns/in/x", &broker.Message{Payload: []byte(p)}); pubErr != nil {
			t.Fatalf("Publish %s: %v", p, pubErr)
		}
	}
	replacement, err := b.CreateQueue("ns/q/new", broker.QueueOptions{Named: true, Exclusive: true})
	if err != nil {
		t.Fatalf("CreateQueue new: %v", err)
	}
	if subErr := b.Subscribe(replacement, topic); subErr != nil {
		t.Fatalf("Subscribe new: %v", subErr)
	}

	var seen []string
	handle := func(_ context.Context, msg *broker.Message) {
		seen = append(seen, string(msg.Payload))
		msg.Ack()
	}
	found, err := MoveQueue(t.Context(), b, m, handle, slog.Default())
	if err != nil || !found {
		t.Fatalf("MoveQueue = found %v, %v; want the queue found and moved", found, err)
	}

	if want := []string{"m1", "m2", "m3"}; !slices.Equal(seen, want) {
		t.Errorf("handled %q, want %q in order", seen, want)
	}
	if b.HasQueue(m.Queue) {
		t.Error("old queue still exists after the move")
	}
	if got := b.MatchingQueues("ns/in/x"); !slices.Equal(got, []string{"ns/q/new"}) {
		t.Errorf("topic reaches %v, want only the replacement", got)
	}

	seen = nil
	found, err = MoveQueue(t.Context(), b, m, handle, slog.Default())
	if err != nil || found {
		t.Fatalf("second MoveQueue = found %v, %v; want nothing found", found, err)
	}
	if len(seen) != 0 || b.HasQueue(m.Queue) {
		t.Errorf("second move handled %q and queue exists %v; want a no-op", seen, b.HasQueue(m.Queue))
	}
}
