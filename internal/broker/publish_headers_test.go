// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func fullHeaders() *PublishHeaders {
	dmq := false
	prio := 7
	seq := uint64(42)
	return &PublishHeaders{
		DMQEligible:            &dmq,
		Expiration:             time.UnixMilli(1_900_000_000_000).UTC(),
		Priority:               &prio,
		SequenceNumber:         &seq,
		ApplicationMessageID:   "app-1",
		ApplicationMessageType: "order.reply",
		SenderID:               "sam",
		HTTPContentEncoding:    "gzip",
	}
}

func TestPublishHeadersClone_IsDeep(t *testing.T) {
	orig := fullHeaders()
	c := orig.clone()
	if !reflect.DeepEqual(orig, c) {
		t.Fatalf("clone differs: %+v vs %+v", c, orig)
	}
	*c.Priority = 1
	*c.DMQEligible = true
	*c.SequenceNumber = 1
	if *orig.Priority != 7 || *orig.DMQEligible || *orig.SequenceNumber != 42 {
		t.Fatalf("mutating the clone changed the original: %+v", orig)
	}
	var nilHeaders *PublishHeaders
	if nilHeaders.clone() != nil {
		t.Fatal("clone of nil must be nil")
	}
}

func TestDevBroker_CarriesPublishHeaders(t *testing.T) {
	b := NewDevBroker()
	must(t, b.Connect(context.Background()))
	q, err := b.CreateQueue("hdr-q", QueueOptions{})
	must(t, err)
	must(t, b.Subscribe(q, "hdr/>"))

	sent := fullHeaders()
	must(t, b.PublishGuaranteed(context.Background(), "hdr/x", &Message{Payload: []byte(`{}`), Headers: sent}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := b.Receive(ctx, q)
	must(t, err)
	if !reflect.DeepEqual(got.Headers, sent) {
		t.Fatalf("headers = %+v, want %+v", got.Headers, sent)
	}
	if got.Headers == sent {
		t.Fatal("delivered headers share the publisher's pointer")
	}
}

func TestNetworkDevBroker_CarriesPublishHeaders(t *testing.T) {
	logger := testLogger()
	server := NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	must(t, err)
	defer func() { _ = server.Stop() }()

	consumer := NewNetworkDevBroker(addr, "hdr-consumer", "hq", logger)
	consumer.SetInitialSubscriptions([]string{"hdr/>"})
	must(t, consumer.Connect(context.Background()))
	defer func() { _ = consumer.Disconnect(context.Background()) }()

	publisher := NewNetworkDevBroker(addr, "hdr-publisher", "hp", logger)
	must(t, publisher.Connect(context.Background()))
	defer func() { _ = publisher.Disconnect(context.Background()) }()

	sent := fullHeaders()
	must(t, publisher.Publish(context.Background(), "hdr/x", &Message{Payload: []byte(`{"a":1}`), Headers: sent}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := consumer.Receive(ctx, nil)
	must(t, err)
	if !reflect.DeepEqual(got.Headers, sent) {
		t.Fatalf("headers over the TCP dev broker = %+v, want %+v", got.Headers, sent)
	}
}

func TestNetworkDevBroker_NoHeadersStaysNil(t *testing.T) {
	logger := testLogger()
	server := NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	must(t, err)
	defer func() { _ = server.Stop() }()

	consumer := NewNetworkDevBroker(addr, "plain-consumer", "pq", logger)
	consumer.SetInitialSubscriptions([]string{"plain/>"})
	must(t, consumer.Connect(context.Background()))
	defer func() { _ = consumer.Disconnect(context.Background()) }()

	must(t, consumer.Publish(context.Background(), "plain/x", &Message{Payload: []byte(`{}`)}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := consumer.Receive(ctx, nil)
	must(t, err)
	if got.Headers != nil {
		t.Fatalf("headers = %+v, want nil for a message published without headers", got.Headers)
	}
}
