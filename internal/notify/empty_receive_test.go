// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type emptyFirstReceiveBroker struct {
	broker.Broker
	mu    sync.Mutex
	empty map[string]bool
}

func (b *emptyFirstReceiveBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	b.mu.Lock()
	first := !b.empty[q.Name()]
	b.empty[q.Name()] = true
	b.mu.Unlock()
	if first {
		return nil, nil
	}
	return b.Broker.Receive(ctx, q)
}

func (b *emptyFirstReceiveBroker) hadEmptyReceive(queueName string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.empty[queueName]
}

func TestNotifyCore_LoopsKeepConsumingAfterAnEmptyReceive(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	brk := &emptyFirstReceiveBroker{Broker: dev, empty: map[string]bool{}}
	core, _, ns := startCoreOn(t, brk, "sqlite://"+t.TempDir()+"/notify.db")

	for _, loop := range []string{"request", "reply", "presence"} {
		queueName := a2a.QueueName(ns, "notify", loop)
		deadline := time.Now().Add(2 * time.Second)
		for !brk.hadEmptyReceive(queueName) {
			if time.Now().After(deadline) {
				t.Fatalf("the %s loop never received from %s", loop, queueName)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	addr := a2a.NotifyResponseTopic(ns, "agent-1", "req-E")
	sink := requesterSink(t, dev, "sink-E", addr)
	publishReq(t, dev, ns, a2a.NotifyRequest{
		RequestID:       "req-E",
		RecipientUserID: "alice@example.com",
		Mode:            a2a.NotifyModeInput,
		RequesterAddr:   addr,
		Title:           "Approve?",
		Message:         "approve the deploy",
		ExpiresAt:       isoFromNow(time.Hour),
	})
	waitPending(t, core, "req-E")
	publishReply(t, dev, ns, a2a.UserInputResponseData{RequestID: "req-E", Status: "answered"})
	if got := sink(3 * time.Second); got == nil || got.Status != "answered" {
		t.Fatalf("routed response = %+v, want the answer", got)
	}

	address, _ := json.Marshal(map[string]string{"channel_id": "D1", "user_id": "U1"})
	payload, _ := json.Marshal(a2a.NotifyPresence{
		UserID: "alice", ChannelType: "slack", Address: address, Email: "alice@example.com", GatewayID: "gw-1",
	})
	if err := dev.PublishGuaranteed(t.Context(), a2a.NotifyPresenceTopic(ns, "alice"), &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish presence: %v", err)
	}
	if uc := waitChannel(t, core, "alice", "slack"); uc.Email != "alice@example.com" {
		t.Fatalf("learned channel = %+v, want the published presence", uc)
	}
}
