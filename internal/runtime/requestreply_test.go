// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestRequestReplyBasic(t *testing.T) {
	b := broker.NewDevBroker()
	if err := b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	rr := NewRequestReply(b, "test/reply")

	reqQueue, createErr := b.CreateQueue("responder-q", broker.QueueOptions{})
	if createErr != nil {
		t.Fatal(createErr)
	}
	if subErr := b.Subscribe(reqQueue, "test/request"); subErr != nil {
		t.Fatal(subErr)
	}

	ctx := context.Background()
	go func() {
		receiveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, recvErr := b.Receive(receiveCtx, reqQueue)
		if recvErr != nil || req == nil {
			return
		}

		reply := &broker.Message{
			Payload:       []byte("REPLY:" + string(req.Payload)),
			CorrelationID: req.CorrelationID,
		}
		_ = b.Publish(ctx, req.ReplyTo, reply)
	}()

	request := &Message{
		Payload: []byte("PING"),
	}
	reply, err := rr.Request(ctx, "test/request", request, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reply == nil {
		t.Fatal("expected reply")
	}
	if string(reply.Payload) != "REPLY:PING" {
		t.Errorf("reply = %q, want %q", reply.Payload, "REPLY:PING")
	}

	if request.CorrelationID == "" {
		t.Error("correlation ID should be auto-generated")
	}
	if request.ReplyTo == "" {
		t.Error("reply-to should be auto-generated")
	}
}

func TestRequestReplyCustomCorrelation(t *testing.T) {
	b := broker.NewDevBroker()
	if err := b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	rr := NewRequestReply(b, "custom/reply")

	reqQueue, _ := b.CreateQueue("custom-q", broker.QueueOptions{})
	_ = b.Subscribe(reqQueue, "custom/request")

	ctx := context.Background()
	go func() {
		receiveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, _ := b.Receive(receiveCtx, reqQueue)
		if req == nil {
			return
		}
		_ = b.Publish(ctx, req.ReplyTo, &broker.Message{
			Payload:       []byte("OK"),
			CorrelationID: req.CorrelationID,
		})
	}()

	request := &Message{
		Payload:       []byte("test"),
		CorrelationID: "my-corr-123",
		ReplyTo:       "custom/reply/my-corr-123",
	}
	reply, err := rr.Request(ctx, "custom/request", request, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reply == nil {
		t.Fatal("expected reply")
	}

	if request.CorrelationID != "my-corr-123" {
		t.Errorf("correlation ID should be preserved, got %q", request.CorrelationID)
	}
	if request.ReplyTo != "custom/reply/my-corr-123" {
		t.Errorf("reply-to should be preserved, got %q", request.ReplyTo)
	}
}

func TestReplyHelper(t *testing.T) {
	b := broker.NewDevBroker()
	if err := b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	rr := NewRequestReply(b, "test/reply")
	ctx := context.Background()

	replyQueue, _ := b.CreateQueue("reply-q", broker.QueueOptions{})
	_ = b.Subscribe(replyQueue, "test/reply/>")

	incomingRequest := &Message{
		Payload:       []byte("request-data"),
		ReplyTo:       "test/reply/corr-456",
		CorrelationID: "corr-456",
	}

	response := &Message{
		Payload: []byte("response-data"),
	}
	if err := rr.Reply(ctx, incomingRequest, response); err != nil {
		t.Fatal(err)
	}

	receiveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := b.Receive(receiveCtx, replyQueue)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected reply message")
	}
	if string(msg.Payload) != "response-data" {
		t.Errorf("payload = %q", msg.Payload)
	}
	if msg.CorrelationID != "corr-456" {
		t.Errorf("correlation ID = %q, want %q", msg.CorrelationID, "corr-456")
	}
}

func TestMessageConversion(t *testing.T) {
	rm := &Message{
		ID:             "msg-1",
		Topic:          "test/topic",
		Payload:        []byte(`{"key":"value"}`),
		UserProperties: map[string]string{"sender": "test"},
		ReplyTo:        "reply/topic",
		CorrelationID:  "corr-001",
	}

	bm := runtimeToBrokerMessage(rm)
	if bm.ID != rm.ID || bm.Topic != rm.Topic || bm.ReplyTo != rm.ReplyTo {
		t.Error("broker message fields mismatch")
	}
	if bm.UserProperties["sender"] != "test" {
		t.Error("user properties not copied")
	}

	rm2 := brokerToRuntimeMessage(bm)
	if rm2.ID != rm.ID || rm2.Topic != rm.Topic || rm2.CorrelationID != rm.CorrelationID {
		t.Error("round-trip mismatch")
	}

	var parsed map[string]string
	if err := json.Unmarshal(rm2.Payload, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["key"] != "value" {
		t.Error("payload corrupted")
	}
}
