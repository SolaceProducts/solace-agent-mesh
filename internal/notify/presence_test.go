// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

func waitChannel(t *testing.T, core *NotifyCore, userID, channelType string) *store.UserChannel {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		uc, err := core.channels.GetByType(t.Context(), userID, channelType)
		if err != nil {
			t.Fatalf("GetByType: %v", err)
		}
		if uc != nil {
			return uc
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("presence for %s/%s never recorded", userID, channelType)
	return nil
}

func TestPresence_LearnsUserChannelEndToEnd(t *testing.T) {
	core, brk, ns := startRouterCore(t)

	addr, _ := json.Marshal(map[string]string{"channel_id": "C9", "user_id": "U9"})
	payload, _ := json.Marshal(a2a.NotifyPresence{
		UserID: "trent", ChannelType: "slack", Address: addr, Email: "trent@x.com", GatewayID: "gw-7",
	})
	if err := brk.PublishGuaranteed(t.Context(), a2a.NotifyPresenceTopic(ns, "trent"), &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish presence: %v", err)
	}

	uc := waitChannel(t, core, "trent", "slack")
	if uc.Address != `{"channel_id":"C9","user_id":"U9"}` || uc.Email != "trent@x.com" || uc.GatewayID != "gw-7" {
		t.Fatalf("learned channel = %+v, want the published address", uc)
	}
}

func TestPresence_MalformedAndMissingDropped(t *testing.T) {
	core, _, _ := startRouterCore(t)

	core.handlePresence(t.Context(), &broker.Message{Payload: []byte(`{not json`)})
	missing, _ := json.Marshal(a2a.NotifyPresence{UserID: "trent", ChannelType: "slack"})
	core.handlePresence(t.Context(), &broker.Message{Payload: missing})

	all, err := core.channels.ListByUser(t.Context(), "trent")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("malformed/missing presence must not upsert; got %d rows", len(all))
	}
}

func TestPresence_LoopBacksOffOnReceiveError(t *testing.T) {
	core, brk, _ := startRouterCore(t)
	q, err := brk.CreateQueue("pl-test", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	_ = brk.Disconnect(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { core.presenceLoop(ctx, q); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("presenceLoop did not exit after context cancellation")
	}
}

func TestPresence_StoreErrorSelfHeals(t *testing.T) {
	core, _, _ := startRouterCore(t)
	if err := core.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	addr, _ := json.Marshal(map[string]string{"channel_id": "C1"})
	payload, _ := json.Marshal(a2a.NotifyPresence{UserID: "trent", ChannelType: "slack", Address: addr})
	var acked, nacked bool
	msg := broker.NewDeliveredMessage("m1", "t", payload, func() { acked = true }, func() { nacked = true })

	core.handlePresence(t.Context(), msg)
	if !acked || nacked {
		t.Errorf("store error should ACK (self-heal), not NACK: acked=%v nacked=%v", acked, nacked)
	}
}
