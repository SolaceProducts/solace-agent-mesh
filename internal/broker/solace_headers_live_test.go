// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/message"
	"solace.dev/go/messaging/pkg/solace/resource"
)

const deadMessageQueue = "#DEAD_MSG_QUEUE"

func connectedSolaceBroker(t *testing.T) *SolaceBroker {
	t.Helper()
	b := NewSolaceBroker(solaceBrokerConfig(t), testLogger())
	must(t, b.Connect(context.Background()))
	t.Cleanup(func() { _ = b.Disconnect(context.Background()) })
	return b
}

func queueReceiver(t *testing.T, b *SolaceBroker, queue string) solace.PersistentMessageReceiver {
	t.Helper()
	b.mu.RLock()
	svc := b.svc
	b.mu.RUnlock()
	rx, err := svc.CreatePersistentMessageReceiverBuilder().Build(resource.QueueDurableNonExclusive(queue))
	must(t, err)
	must(t, rx.Start())
	t.Cleanup(func() { _ = rx.Terminate(time.Second) })
	return rx
}

func receiveByAppID(t *testing.T, rx solace.PersistentMessageReceiver, appID string, within time.Duration) message.InboundMessage {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		in, err := rx.ReceiveMessage(500 * time.Millisecond)
		if err != nil {
			continue
		}
		_ = rx.Ack(in)
		if id, _ := in.GetApplicationMessageID(); id == appID {
			return in
		}
	}
	return nil
}

func TestSolacePublishGuaranteed_HeadersAndUserPropertiesArrive(t *testing.T) {
	b := connectedSolaceBroker(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	queue := fmt.Sprintf("sam-go/test/headers/%d", suffix)
	topic := fmt.Sprintf("sam-go/test/headers/%d/reply", suffix)
	t.Cleanup(func() { _ = b.DeprovisionQueue(queue) })
	must(t, b.ProvisionQueue(ctx, queue, QueueOptions{Named: true}, topic))

	appID := fmt.Sprintf("hdr-%d", suffix)
	prio := 7
	seq := uint64(42)
	dmq := true
	must(t, b.PublishGuaranteed(ctx, topic, &Message{
		Payload:        []byte(`{"ok":true}`),
		UserProperties: map[string]string{"source": "sam", "orderId": "42"},
		CorrelationID:  "corr-1",
		TTL:            time.Minute,
		Headers: &PublishHeaders{
			DMQEligible:            &dmq,
			Priority:               &prio,
			SequenceNumber:         &seq,
			ApplicationMessageID:   appID,
			ApplicationMessageType: "order.reply",
			SenderID:               "sam-entrypoint",
			HTTPContentEncoding:    "gzip",
		},
	}))

	in := receiveByAppID(t, queueReceiver(t, b, queue), appID, 10*time.Second)
	if in == nil {
		t.Fatal("published message never arrived on the queue")
	}
	if got, _ := in.GetPriority(); got != prio {
		t.Errorf("priority = %d, want %d", got, prio)
	}
	if got, _ := in.GetSequenceNumber(); got != int64(seq) {
		t.Errorf("sequence number = %d, want %d", got, seq)
	}
	if got, _ := in.GetCorrelationID(); got != "corr-1" {
		t.Errorf("correlation id = %q", got)
	}
	if got, _ := in.GetApplicationMessageType(); got != "order.reply" {
		t.Errorf("application message type = %q", got)
	}
	if got, _ := in.GetSenderID(); got != "sam-entrypoint" {
		t.Errorf("sender id = %q", got)
	}
	if got, _ := in.GetHTTPContentEncoding(); got != "gzip" {
		t.Errorf("http content encoding = %q", got)
	}
	for k, want := range map[string]string{"source": "sam", "orderId": "42"} {
		if v, ok := in.GetProperty(k); !ok || fmt.Sprint(v) != want {
			t.Errorf("user property %s = %v (present %v), want %s", k, v, ok, want)
		}
	}
}

func TestSolacePublish_DirectHeadersAndUserPropertiesArrive(t *testing.T) {
	b := connectedSolaceBroker(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	queue := fmt.Sprintf("sam-go/test/direct-headers/%d", suffix)
	topic := fmt.Sprintf("sam-go/test/direct-headers/%d/reply", suffix)
	t.Cleanup(func() { _ = b.DeprovisionQueue(queue) })
	must(t, b.ProvisionQueue(ctx, queue, QueueOptions{Named: true}, topic))
	rx := queueReceiver(t, b, queue)

	appID := fmt.Sprintf("direct-hdr-%d", suffix)
	prio := 5
	seq := uint64(9)
	must(t, b.Publish(ctx, topic, &Message{
		Payload:        []byte(`{"ok":true}`),
		UserProperties: map[string]string{"source": "sam", "orderId": "7"},
		CorrelationID:  "corr-direct",
		Headers: &PublishHeaders{
			Priority:               &prio,
			SequenceNumber:         &seq,
			ApplicationMessageID:   appID,
			ApplicationMessageType: "order.reply",
			SenderID:               "sam-entrypoint",
			HTTPContentEncoding:    "gzip",
		},
	}))

	in := receiveByAppID(t, rx, appID, 10*time.Second)
	if in == nil {
		t.Fatal("directly published message never arrived on the queue")
	}
	if got, _ := in.GetPriority(); got != prio {
		t.Errorf("priority = %d, want %d", got, prio)
	}
	if got, _ := in.GetSequenceNumber(); got != int64(seq) {
		t.Errorf("sequence number = %d, want %d", got, seq)
	}
	if got, _ := in.GetCorrelationID(); got != "corr-direct" {
		t.Errorf("correlation id = %q", got)
	}
	if got, _ := in.GetApplicationMessageType(); got != "order.reply" {
		t.Errorf("application message type = %q", got)
	}
	if got, _ := in.GetSenderID(); got != "sam-entrypoint" {
		t.Errorf("sender id = %q", got)
	}
	if got, _ := in.GetHTTPContentEncoding(); got != "gzip" {
		t.Errorf("http content encoding = %q", got)
	}
	for k, want := range map[string]string{"source": "sam", "orderId": "7"} {
		if v, ok := in.GetProperty(k); !ok || fmt.Sprint(v) != want {
			t.Errorf("user property %s = %v (present %v), want %s", k, v, ok, want)
		}
	}
}

func TestSolacePublishGuaranteed_ExpiredDMQEligibleMessageLandsOnDMQ(t *testing.T) {
	b := connectedSolaceBroker(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	queue := fmt.Sprintf("sam-go/test/dmq/%d", suffix)
	topic := fmt.Sprintf("sam-go/test/dmq/%d/reply", suffix)
	must(t, b.ProvisionQueue(ctx, deadMessageQueue, QueueOptions{Named: true}))
	t.Cleanup(func() { _ = b.DeprovisionQueue(queue) })
	must(t, b.ProvisionQueue(ctx, queue, QueueOptions{Named: true, RespectTTL: true}, topic))

	eligible, notEligible := true, false
	eligibleID := fmt.Sprintf("dmq-yes-%d", suffix)
	discardedID := fmt.Sprintf("dmq-no-%d", suffix)
	for id, dmq := range map[string]*bool{eligibleID: &eligible, discardedID: &notEligible} {
		must(t, b.PublishGuaranteed(ctx, topic, &Message{
			Payload: []byte(id),
			TTL:     time.Second,
			Headers: &PublishHeaders{DMQEligible: dmq, ApplicationMessageID: id},
		}))
	}

	dmqRx := queueReceiver(t, b, deadMessageQueue)
	if receiveByAppID(t, dmqRx, eligibleID, 15*time.Second) == nil {
		t.Fatalf("the expired DMQ-eligible message never reached %s", deadMessageQueue)
	}
	if receiveByAppID(t, dmqRx, discardedID, 2*time.Second) != nil {
		t.Fatalf("the expired message that was not DMQ eligible reached %s; it must be discarded", deadMessageQueue)
	}
}
