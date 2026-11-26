// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestNetworkDevBrokerPubSub(t *testing.T) {
	logger := testLogger()

	server := NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	client1 := NewNetworkDevBroker(addr, "client-1", "q1", logger)
	client1.SetInitialSubscriptions([]string{"topic/from-2"})
	must(t, client1.Connect(context.Background()))
	defer func() { _ = client1.Disconnect(context.Background()) }()

	client2 := NewNetworkDevBroker(addr, "client-2", "q2", logger)
	client2.SetInitialSubscriptions([]string{"topic/from-1"})
	must(t, client2.Connect(context.Background()))
	defer func() { _ = client2.Disconnect(context.Background()) }()

	must(t, client1.Publish(context.Background(), "topic/from-1", &Message{
		Payload:        []byte(`{"msg":"hello from 1"}`),
		UserProperties: map[string]string{"sender": "client-1"},
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := client2.Receive(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Topic != "topic/from-1" {
		t.Errorf("topic = %q, want %q", msg.Topic, "topic/from-1")
	}
	if msg.UserProperties["sender"] != "client-1" {
		t.Errorf("sender = %q", msg.UserProperties["sender"])
	}
}

func TestNetworkDevBrokerWildcard(t *testing.T) {
	logger := testLogger()

	server := NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	client := NewNetworkDevBroker(addr, "wildcard-client", "wq", logger)
	client.SetInitialSubscriptions([]string{"events/>"})
	must(t, client.Connect(context.Background()))
	defer func() { _ = client.Disconnect(context.Background()) }()

	publisher := NewNetworkDevBroker(addr, "publisher", "pq", logger)
	must(t, publisher.Connect(context.Background()))
	defer func() { _ = publisher.Disconnect(context.Background()) }()

	must(t, publisher.Publish(context.Background(), "events/user/login", &Message{Payload: []byte(`"login"`)}))
	must(t, publisher.Publish(context.Background(), "events/user/logout", &Message{Payload: []byte(`"logout"`)}))
	must(t, publisher.Publish(context.Background(), "other/topic", &Message{Payload: []byte(`"other"`)}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msg1, err := client.Receive(ctx, nil)
	must(t, err)
	if msg1 == nil {
		t.Fatal("expected first message")
	}

	msg2, err := client.Receive(ctx, nil)
	must(t, err)
	if msg2 == nil {
		t.Fatal("expected second message")
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel()
	msg3, _ := client.Receive(shortCtx, nil)
	if msg3 != nil {
		t.Error("should not receive non-matching message")
	}
}

func TestNetworkDevBrokerDynamicSubscription(t *testing.T) {
	logger := testLogger()

	server := NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	client := NewNetworkDevBroker(addr, "dyn-client", "dq", logger)
	must(t, client.Connect(context.Background()))
	defer func() { _ = client.Disconnect(context.Background()) }()

	publisher := NewNetworkDevBroker(addr, "dyn-pub", "dpq", logger)
	must(t, publisher.Connect(context.Background()))
	defer func() { _ = publisher.Disconnect(context.Background()) }()

	must(t, publisher.Publish(context.Background(), "dynamic/topic", &Message{Payload: []byte(`"test"`)}))
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	msg, _ := client.Receive(shortCtx, nil)
	shortCancel()
	if msg != nil {
		t.Error("should not receive without subscription")
	}

	must(t, client.Subscribe(nil, "dynamic/>"))

	must(t, publisher.Publish(context.Background(), "dynamic/topic", &Message{Payload: []byte(`"test2"`)}))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err = client.Receive(ctx, nil)
	must(t, err)
	if msg == nil {
		t.Fatal("expected message after dynamic subscribe")
	}
}

func TestNetworkDevBrokerLocalBrokerIntegration(t *testing.T) {
	logger := testLogger()

	local := NewDevBroker()
	must(t, local.Connect(context.Background()))

	server := NewDevBrokerServer(local, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	localQueue, _ := local.CreateQueue("local-q", QueueOptions{})
	must(t, local.Subscribe(localQueue, "from-network/>"))

	netClient := NewNetworkDevBroker(addr, "net-client", "nq", logger)
	must(t, netClient.Connect(context.Background()))
	defer func() { _ = netClient.Disconnect(context.Background()) }()

	must(t, netClient.Publish(context.Background(), "from-network/test", &Message{
		Payload: []byte(`{"source":"network"}`),
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := local.Receive(ctx, localQueue)
	must(t, err)
	if msg == nil {
		t.Fatal("local broker should receive message from network client")
	}
	if msg.Topic != "from-network/test" {
		t.Errorf("topic = %q", msg.Topic)
	}
}

func TestNetworkDevBroker_UnsubscribeClearsLocalRegex(t *testing.T) {
	logger := testLogger()

	server := NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	client := NewNetworkDevBroker(addr, "client", "default-q", logger)
	must(t, client.Connect(context.Background()))
	defer func() { _ = client.Disconnect(context.Background()) }()

	qBroad, err := client.CreateQueue("broad", QueueOptions{})
	must(t, err)
	qSpecific, err := client.CreateQueue("specific", QueueOptions{})
	must(t, err)

	must(t, client.Subscribe(qBroad, "events/>"))
	must(t, client.Subscribe(qSpecific, "events/login"))

	must(t, client.Unsubscribe(qBroad, "events/>"))

	publisher := NewNetworkDevBroker(addr, "publisher", "pq", logger)
	must(t, publisher.Connect(context.Background()))
	defer func() { _ = publisher.Disconnect(context.Background()) }()

	must(t, publisher.Publish(context.Background(), "events/login", &Message{
		Payload: []byte(`"login"`),
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := client.Receive(ctx, qSpecific)
	must(t, err)
	if msg == nil {
		t.Fatal("qSpecific should receive its still-subscribed topic")
	}

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer shortCancel()
	leaked, _ := client.Receive(shortCtx, qBroad)
	if leaked != nil {
		t.Errorf("qBroad received a leaked message via stale local regex: topic=%s", leaked.Topic)
	}
}

func TestNetworkDevBrokerBidirectional(t *testing.T) {
	logger := testLogger()

	local := NewDevBroker()
	must(t, local.Connect(context.Background()))

	server := NewDevBrokerServer(local, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	netClient := NewNetworkDevBroker(addr, "net-client", "nq", logger)
	netClient.SetInitialSubscriptions([]string{"from-local/>"})
	must(t, netClient.Connect(context.Background()))
	defer func() { _ = netClient.Disconnect(context.Background()) }()

	time.Sleep(50 * time.Millisecond)

	must(t, local.Publish(context.Background(), "from-local/test", &Message{
		Payload: []byte(`{"source":"local"}`),
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := netClient.Receive(ctx, nil)
	must(t, err)
	if msg == nil {
		t.Fatal("network client should receive message from local publisher")
	}
	if msg.Topic != "from-local/test" {
		t.Errorf("topic = %q, want %q", msg.Topic, "from-local/test")
	}
}

func TestNetworkDevBrokerCreateTemporaryQueue(t *testing.T) {
	t.Parallel()
	logger := testLogger()
	server := NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	client := NewNetworkDevBroker(addr, "client-tmp", "qtmp", logger)
	must(t, client.Connect(context.Background()))
	defer func() { _ = client.Disconnect(context.Background()) }()

	q, err := client.CreateTemporaryQueue()
	if err != nil {
		t.Fatalf("CreateTemporaryQueue: %v", err)
	}
	if q.Name() == "" {
		t.Error("expected non-empty queue name")
	}
}

func TestNetworkDevBrokerRequestReplyInbox(t *testing.T) {
	t.Parallel()
	logger := testLogger()
	server := NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Stop() }()

	requester := NewNetworkDevBroker(addr, "requester", "qreq", logger)
	must(t, requester.Connect(context.Background()))
	defer func() { _ = requester.Disconnect(context.Background()) }()

	responder := NewNetworkDevBroker(addr, "responder", "qresp", logger)
	must(t, responder.Connect(context.Background()))
	defer func() { _ = responder.Disconnect(context.Background()) }()

	respQ, err := responder.CreateQueue("inbox-resp-q", QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	must(t, responder.Subscribe(respQ, "inbox/req"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			msg, recvErr := responder.Receive(ctx, respQ)
			if recvErr != nil || msg == nil {
				return
			}
			_ = responder.Publish(ctx, msg.ReplyTo, &Message{
				Payload:       msg.Payload,
				CorrelationID: msg.CorrelationID,
			})
		}
	}()

	reply, err := requester.RequestReplyInbox(context.Background(), "inbox/req", &Message{Payload: []byte("ping")}, 2*time.Second)
	if err != nil {
		t.Fatalf("RequestReplyInbox: %v", err)
	}
	if reply == nil {
		t.Fatal("expected reply, got nil")
	}
}
