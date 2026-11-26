// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func presenceCore(t *testing.T, brk broker.Broker, notifyEnabled bool) *GatewayCore {
	t.Helper()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyNotifyService, Default: notifyEnabled})
	core := NewGatewayCore(&LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw-1"})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithFeatureRegistry(reg)).Services())
	core.logger = slog.Default()
	return core
}

func receivePresence(t *testing.T, brk broker.Broker, q broker.Queue, d time.Duration) *broker.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		return nil
	}
	return msg
}

func TestPublishNotifyPresence_GatedByFlag(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	q, err := brk.CreateQueue("presence-sink", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create sink queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.NotifyPresenceSubscription("test-ns")); err != nil {
		t.Fatalf("subscribe sink: %v", err)
	}

	addr := map[string]string{"channel_id": "C1", "user_id": "U1"}

	presenceCore(t, brk, false).PublishNotifyPresence(ctx, "slack", "trent", "trent@x.com", addr)
	presenceCore(t, brk, true).PublishNotifyPresence(ctx, "slack", "trent", "trent@x.com", addr)

	msg := receivePresence(t, brk, q, time.Second)
	if msg == nil {
		t.Fatal("notify_service on: expected a presence event to be published")
	}
	var p a2a.NotifyPresence
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		t.Fatalf("unmarshal presence: %v", err)
	}
	if p.UserID != "trent" || p.ChannelType != "slack" || p.Email != "trent@x.com" || p.GatewayID != "gw-1" {
		t.Fatalf("presence event = %+v, want trent/slack/gw-1", p)
	}
	if string(p.Address) != `{"channel_id":"C1","user_id":"U1"}` {
		t.Fatalf("address = %s, want the marshaled conversationReference", p.Address)
	}

	if extra := receivePresence(t, brk, q, 200*time.Millisecond); extra != nil {
		t.Error("notify_service off must not publish a presence event")
	}
}

func TestPublishNotifyPresence_ErrorBranchesAreBestEffort(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	core := presenceCore(t, brk, true)

	core.PublishNotifyPresence(ctx, "slack", "trent", "trent@x.com", make(chan int))

	_ = brk.Disconnect(context.Background())
	core.PublishNotifyPresence(ctx, "slack", "trent", "trent@x.com", map[string]string{"channel_id": "C1"})
}

func TestPublishNotifyPresence_SkipsIncompleteInput(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	q, _ := brk.CreateQueue("presence-sink2", broker.QueueOptions{Named: true})
	_ = brk.Subscribe(q, a2a.NotifyPresenceSubscription("test-ns"))

	core := presenceCore(t, brk, true)
	core.PublishNotifyPresence(ctx, "slack", "", "e", map[string]string{"x": "y"})
	core.PublishNotifyPresence(ctx, "", "trent", "e", map[string]string{"x": "y"})
	core.PublishNotifyPresence(ctx, "slack", "trent", "e", nil)

	if msg := receivePresence(t, brk, q, 200*time.Millisecond); msg != nil {
		t.Error("incomplete presence inputs must not publish")
	}
}
