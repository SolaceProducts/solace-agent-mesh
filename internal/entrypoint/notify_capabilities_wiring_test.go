// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
)

func TestGatewayCore_CardsDriveNotifyCapabilityRegistry(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	cards, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(cards, a2a.DiscoveryGatewayCardsTopic("test-ns")); err != nil {
		t.Fatal(err)
	}

	core := notifyDeliverCore(t, brk, true)
	ctx := t.Context()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.StartNotifyDeliverConsumer(ctx, NotifyDeliverOptions{GatewayType: "slack", Interactable: true}, slog.Default(), func(a2a.NotifyDeliver) {})
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	registry := notify.NewGatewayCapabilityRegistry(time.Minute)
	applyNextCard := func() notify.CapabilityChange {
		t.Helper()
		recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		msg, err := brk.Receive(recvCtx, cards)
		if err != nil {
			t.Fatalf("no gateway card published: %v", err)
		}
		change, err := registry.Apply(msg)
		if err != nil {
			t.Fatalf("notify could not apply the gateway's card: %v", err)
		}
		return change
	}

	if change := applyNextCard(); change.Kind != notify.CapabilityAdvertised || change.InstanceID == "" {
		t.Fatalf("first card = %+v, want slack advertised for a named instance", change)
	}
	want := notify.ChannelCapability{Advertised: true, Notifiable: true, Interactable: true}
	if got := registry.Capability("slack"); got != want {
		t.Fatalf("after the gateway's card: Capability(slack) = %+v, want %+v", got, want)
	}

	if err := core.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for registry.Capability("slack").Advertised {
		applyNextCard()
	}
}

func TestGatewayCore_CardsAdvertiseTheResolveConsumerToNotify(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	cards, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(cards, a2a.DiscoveryGatewayCardsTopic("test-ns")); err != nil {
		t.Fatal(err)
	}

	core := notifyDeliverCore(t, brk, true)
	ctx := t.Context()
	if err = core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.StartNotifyDeliverConsumer(ctx, NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), func(a2a.NotifyDeliver) {})
	core.StartNotifyResolveConsumer(ctx, "slack", slog.Default(), func(context.Context, a2a.NotifyResolveRequest) a2a.NotifyResolveResponse {
		return a2a.NotifyResolveResponse{}
	})
	if err = core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(recvCtx, cards)
	if err != nil {
		t.Fatalf("no gateway card published: %v", err)
	}
	registry := notify.NewGatewayCapabilityRegistry(time.Minute)
	if _, err = registry.Apply(msg); err != nil {
		t.Fatalf("notify could not apply the gateway's card: %v", err)
	}
	want := notify.ChannelCapability{Advertised: true, Notifiable: true, ResolvesAddress: true}
	if got := registry.Capability("slack"); got != want {
		t.Fatalf("Capability(slack) = %+v, want %+v", got, want)
	}
}
