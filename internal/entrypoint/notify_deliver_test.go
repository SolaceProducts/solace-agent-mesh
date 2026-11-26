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

func notifyDeliverCore(t *testing.T, brk broker.Broker, notifyEnabled bool) *GatewayCore {
	t.Helper()
	core := NewGatewayCore(&LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw"})
	var opts []runtime.Option
	if notifyEnabled {
		reg := features.NewRegistry()
		reg.Register(features.Definition{Key: features.KeyNotifyService, Default: true})
		opts = append(opts, runtime.WithFeatureRegistry(reg))
	}
	core.SetServices(runtime.NewRuntime(brk, nil, opts...).Services())
	return core
}

func TestStartNotifyDeliverConsumer_ConsumesValidDropsMalformed(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := notifyDeliverCore(t, brk, true)

	got := make(chan a2a.NotifyDeliver, 4)
	core.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), func(d a2a.NotifyDeliver) { got <- d })

	pub := func(payload []byte, rid string) {
		if err := brk.PublishGuaranteed(context.Background(), a2a.NotifyDeliverTopic("test-ns", "slack", rid), &broker.Message{Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	pub([]byte(`{not json`), "bad")
	good, _ := json.Marshal(a2a.NotifyDeliver{RequestID: "g", Mode: a2a.NotifyModeNotify, ChannelType: "slack"})
	pub(good, "g")

	select {
	case d := <-got:
		if d.RequestID != "g" {
			t.Fatalf("handler got %q, want the valid command g", d.RequestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not deliver the valid command")
	}
}

func TestStartNotifyDeliverConsumer_Gating(t *testing.T) {
	called := false
	handle := func(a2a.NotifyDeliver) { called = true }

	(*GatewayCore)(nil).StartNotifyDeliverConsumer(context.Background(), NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), handle)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := notifyDeliverCore(t, brk, false)
	core.StartNotifyDeliverConsumer(context.Background(), NotifyDeliverOptions{GatewayType: "slack"}, slog.Default(), handle)

	if called {
		t.Error("gated-off consumer must not invoke the handler")
	}
}

func TestStartNotifyDeliverConsumer_AdvertisesCapabilitiesOnCard(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := notifyDeliverCore(t, brk, true)

	core.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "teams", Interactable: true}, slog.Default(), func(a2a.NotifyDeliver) {})

	card := publishedGatewayCard(t, cardState{healthProblems: noHealthProblems, notifyCaps: core.AdvertisedNotifyCapabilities})
	got, ok := a2a.NotifyCapabilitiesFromCard(card)
	want := a2a.NotifyCapabilities{ChannelType: "teams", Notifiable: true, Interactable: true}
	if !ok || got != want {
		t.Fatalf("NotifyCapabilitiesFromCard = (%+v, %v), want (%+v, true)", got, ok, want)
	}
}

func TestStartNotifyDeliverConsumer_NoCapabilitiesWithoutConsumer(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	neverStarted := notifyDeliverCore(t, brk, true)
	gatedOff := notifyDeliverCore(t, brk, false)
	gatedOff.StartNotifyDeliverConsumer(t.Context(), NotifyDeliverOptions{GatewayType: "slack", Interactable: true}, slog.Default(), func(a2a.NotifyDeliver) {})

	for name, core := range map[string]*GatewayCore{"neverStarted": neverStarted, "notifyServiceOff": gatedOff} {
		card := publishedGatewayCard(t, cardState{healthProblems: noHealthProblems, notifyCaps: core.AdvertisedNotifyCapabilities})
		if got, ok := a2a.NotifyCapabilitiesFromCard(card); ok {
			t.Errorf("%s: card advertises %+v, want no notify-capabilities extension", name, got)
		}
	}
}
