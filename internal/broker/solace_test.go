// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"os"
	"testing"
	"time"
)

func solaceBrokerConfig(t *testing.T) SolaceBrokerConfig {
	t.Helper()
	url := os.Getenv("SOLACE_BROKER_URL")
	if url == "" {
		t.Skip("SOLACE_BROKER_URL not set; skipping Solace integration test")
	}
	return SolaceBrokerConfig{
		BrokerURL: url,
		VPN:       envOr("SOLACE_BROKER_VPN", "default"),
		Username:  envOr("SOLACE_BROKER_USERNAME", "default"),
		Password:  envOr("SOLACE_BROKER_PASSWORD", "default"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestSolaceBrokerConnectDisconnect(t *testing.T) {
	cfg := solaceBrokerConfig(t)
	b := NewSolaceBroker(cfg, testLogger())

	must(t, b.Connect(context.Background()))
	if !b.IsConnected() {
		t.Fatal("should be connected")
	}

	must(t, b.Disconnect(context.Background()))
	if b.IsConnected() {
		t.Fatal("should not be connected after disconnect")
	}
}

func TestSolaceBrokerPubSub(t *testing.T) {
	cfg := solaceBrokerConfig(t)
	b := NewSolaceBroker(cfg, testLogger())
	ctx := context.Background()

	must(t, b.Connect(ctx))
	defer func() { _ = b.Disconnect(ctx) }()

	q, err := b.CreateQueue("solace-test-q", QueueOptions{})
	must(t, err)
	must(t, b.Subscribe(q, "sam-go/test/>"))

	time.Sleep(200 * time.Millisecond)

	must(t, b.Publish(ctx, "sam-go/test/hello", &Message{
		Payload:        []byte(`{"msg":"hello from solace test"}`),
		UserProperties: map[string]string{"sender": "test"},
	}))

	receiveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	msg, err := b.Receive(receiveCtx, q)
	must(t, err)
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Topic != "sam-go/test/hello" {
		t.Errorf("topic = %q, want %q", msg.Topic, "sam-go/test/hello")
	}
	if msg.UserProperties["sender"] != "test" {
		t.Errorf("sender = %q", msg.UserProperties["sender"])
	}
}

func TestSolaceBrokerRequestReply(t *testing.T) {
	cfg := solaceBrokerConfig(t)
	b := NewSolaceBroker(cfg, testLogger())
	ctx := context.Background()

	must(t, b.Connect(ctx))
	defer func() { _ = b.Disconnect(ctx) }()

	reqQueue, err := b.CreateQueue("solace-responder-q", QueueOptions{})
	must(t, err)
	must(t, b.Subscribe(reqQueue, "sam-go/test/request"))

	time.Sleep(200 * time.Millisecond)

	go func() {
		receiveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, recvErr := b.Receive(receiveCtx, reqQueue)
		if recvErr != nil || req == nil {
			return
		}
		reply := &Message{
			Payload:       []byte("REPLY:" + string(req.Payload)),
			CorrelationID: req.CorrelationID,
		}
		_ = b.Publish(ctx, req.ReplyTo, reply)
	}()

	request := &Message{
		Payload: []byte("PING"),
		ReplyTo: "sam-go/test/reply/corr-001",
	}
	reply, err := b.Request(ctx, "sam-go/test/request", request, 5*time.Second)
	must(t, err)
	if reply == nil {
		t.Fatal("expected reply")
	}
	if string(reply.Payload) != "REPLY:PING" {
		t.Errorf("reply = %q, want %q", reply.Payload, "REPLY:PING")
	}
}
