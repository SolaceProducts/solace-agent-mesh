// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

func deliverSink(t *testing.T, brk broker.Broker, name, ns, channelType string) func(time.Duration) *a2a.NotifyDeliver {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create deliver queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.NotifyDeliverSubscription(ns, channelType)); err != nil {
		t.Fatalf("subscribe deliver: %v", err)
	}
	return func(timeout time.Duration) *a2a.NotifyDeliver {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		msg, rerr := brk.Receive(ctx, q)
		if rerr != nil {
			return nil
		}
		var d a2a.NotifyDeliver
		if uerr := json.Unmarshal(msg.Payload, &d); uerr != nil {
			t.Fatalf("unmarshal deliver: %v", uerr)
		}
		msg.Ack()
		return &d
	}
}

func TestProactive_PublishesDeliverForConfirmedChannel(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	sink := deliverSink(t, brk, "slack-deliver", ns, "slack")
	publishGatewayCard(t, brk, ns, gatewayCard("slack-gw", notifiable("slack", true)), "slack-pod", false)
	waitForCapability(t, core, "slack", advertisedInteractive)

	confirmChannel(t, core, "trent", "slack", `{"channel_id":"C1","user_id":"U1"}`)

	surface := json.RawMessage(`{"surfaceId":"s","components":[]}`)
	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID: "req-1", RecipientUserID: "trent", Mode: a2a.NotifyModeInput,
		RequesterAddr: a2a.NotifyResponseTopic(ns, "agent-1", "req-1"),
		Title:         "Approve?", Surface: surface,
	})

	d := sink(2 * time.Second)
	if d == nil {
		t.Fatal("expected a proactive NotifyDeliver on the slack deliver topic")
	}
	if d.RequestID != "req-1" || d.Mode != a2a.NotifyModeInput || d.ChannelType != "slack" {
		t.Fatalf("deliver = %+v, want req-1/input/slack", d)
	}
	if string(d.Address) != `{"channel_id":"C1","user_id":"U1"}` {
		t.Fatalf("address = %s, want the learned conversationReference", d.Address)
	}
	if string(d.Surface) != string(surface) {
		t.Fatalf("surface not forwarded: %s", d.Surface)
	}
}

func TestProactive_DeliversToConfirmedAddressNotLatestPresence(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	sink := deliverSink(t, brk, "slack-deliver", ns, "slack")
	confirmed := `{"channel_id":"CONFIRMED"}`
	confirmChannel(t, core, "trent", "slack", confirmed)
	if err := core.channels.Upsert(t.Context(), store.UserChannel{UserID: "trent", ChannelType: "slack", Address: `{"channel_id":"OTHER-WORKSPACE"}`}); err != nil {
		t.Fatalf("later presence: %v", err)
	}

	publishReq(t, brk, ns, a2a.NotifyRequest{RequestID: "req-x", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Title: "hi"})

	d := sink(2 * time.Second)
	if d == nil || string(d.Address) != confirmed {
		t.Fatalf("deliver = %+v, want it addressed to the confirmed %s", d, confirmed)
	}
}

func TestProactive_DeliversBeforeAnyGatewayAdvertises(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	sink := deliverSink(t, brk, "slack-deliver", ns, "slack")
	confirmChannel(t, core, "trent", "slack", `{"channel_id":"C1"}`)
	if got := core.capabilities.Capability("slack"); got.Advertised {
		t.Fatalf("precondition: no gateway card yet, got %+v", got)
	}

	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID: "req-cold", RecipientUserID: "trent", Mode: a2a.NotifyModeInput,
		RequesterAddr: a2a.NotifyResponseTopic(ns, "agent-1", "req-cold"),
		Title:         "Approve?", Surface: json.RawMessage(`{"surfaceId":"s","components":[]}`),
	})

	if d := sink(2 * time.Second); d == nil || d.RequestID != "req-cold" || d.Mode != a2a.NotifyModeInput {
		t.Fatalf("deliver = %+v, want the HIL prompt published before any gateway advertised", d)
	}
}

func confirmChannel(t *testing.T, core *NotifyCore, userID, channelType, address string) {
	t.Helper()
	if err := core.channels.Upsert(t.Context(), store.UserChannel{UserID: userID, ChannelType: channelType, Address: address}); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	const code = "ABCD-EFGH"
	hash := hashVerificationCode(userID, channelType, code)
	if err := core.channels.StartConfirmation(t.Context(), userID, channelType, address, hash, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("start confirmation: %v", err)
	}
	if outcome, err := core.channels.ConfirmByCode(t.Context(), userID, channelType, hash, maxVerificationAttempts); err != nil || outcome != store.ConfirmOK {
		t.Fatalf("confirm by code: outcome=%v err=%v", outcome, err)
	}
}

func TestProactive_UnconfirmedChannelFailsClosed(t *testing.T) {
	core, brk, ns := startRouterCore(t)
	sink := deliverSink(t, brk, "slack-deliver", ns, "slack")

	if err := core.channels.Upsert(t.Context(), store.UserChannel{
		UserID: "trent", ChannelType: "slack", Address: `{"channel_id":"C1","user_id":"U1"}`,
	}); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}

	publishReq(t, brk, ns, a2a.NotifyRequest{
		RequestID: "req-unconf", RecipientUserID: "trent", Mode: a2a.NotifyModeInput,
		RequesterAddr: a2a.NotifyResponseTopic(ns, "agent-1", "req-unconf"),
		Title:         "Approve?", Surface: json.RawMessage(`{"surfaceId":"s","components":[]}`),
	})

	if d := sink(time.Second); d != nil {
		t.Fatalf("expected no proactive deliver for an unconfirmed channel, got %+v", d)
	}
}

type fakeProactivePub struct {
	calls []a2a.NotifyDeliver
	err   error
}

func (f *fakeProactivePub) PublishDeliver(_ context.Context, _ string, d a2a.NotifyDeliver) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, d)
	return nil
}

type fakeLearned struct {
	addrs       map[string]string
	unconfirmed map[string]bool
	err         error
	calls       int
}

func (f *fakeLearned) ChannelsFor(_ context.Context, _ string) ([]LearnedChannel, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var learned []LearnedChannel
	for ct, addr := range f.addrs {
		learned = append(learned, LearnedChannel{ChannelType: ct, Address: addr, Status: AddressConfirmed})
	}
	for ct := range f.unconfirmed {
		learned = append(learned, LearnedChannel{ChannelType: ct, Status: AddressUnconfirmed})
	}
	slices.SortFunc(learned, func(a, b LearnedChannel) int { return strings.Compare(a.ChannelType, b.ChannelType) })
	return learned, nil
}

func TestDeliverProactive_UnconfirmedChannelIsSkippedVisibly(t *testing.T) {
	pub := &fakeProactivePub{}
	d := proactiveDispatcher(pub, &fakeLearned{unconfirmed: map[string]bool{"slack": true}}, nil)

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Title: "hi"})

	if len(pub.calls) != 0 {
		t.Fatalf("an unconfirmed channel must not be delivered to, got %+v", pub.calls)
	}
	if got := countRecs(recs, "slack", "skipped"); got != 1 {
		t.Fatalf("want 1 skipped slack record, recs=%+v", recs)
	}
	for _, rec := range recs {
		if rec.ChannelType == "slack" && rec.Error != "channel not confirmed by the user" {
			t.Fatalf("skipped slack record reason = %q", rec.Error)
		}
		if rec.ChannelType == "teams" {
			t.Fatalf("teams was never seen; want no record, got %+v", rec)
		}
	}
}

type staticCapabilities map[string]ChannelCapability

func (s staticCapabilities) Capability(channelType string) ChannelCapability { return s[channelType] }

var nothingAdvertised = staticCapabilities{}

func proactiveDispatcher(pub ProactivePublisher, learned LearnedChannels, prefs Preferences) *Dispatcher {
	return proactiveDispatcherWith(pub, learned, prefs, nothingAdvertised)
}

func proactiveDispatcherWith(pub ProactivePublisher, learned LearnedChannels, prefs Preferences, caps ChannelCapabilities) *Dispatcher {
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), nil, slog.Default())
	if prefs != nil {
		d.SetPreferences(prefs)
	}
	d.SetProactive(pub, learned, caps)
	return d
}

func TestDeliverProactive_ConfirmedChannelsDriveDelivery(t *testing.T) {
	pub := &fakeProactivePub{}
	learned := &fakeLearned{addrs: map[string]string{"slack": "{}", "teams": "{}", "relay": "{}"}}
	caps := staticCapabilities{
		"teams": {Advertised: true, Notifiable: true, Interactable: true},
		"relay": {Advertised: true},
	}
	d := proactiveDispatcherWith(pub, learned, nil, caps)

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify})

	var published []string
	for _, c := range pub.calls {
		published = append(published, c.ChannelType)
	}
	if want := []string{"slack", "teams"}; !slices.Equal(published, want) {
		t.Fatalf("published to %v, want %v (relay declines; unadvertised slack still delivered)", published, want)
	}
	if countRecs(recs, "relay", "skipped") != 1 {
		t.Fatalf("a channel its gateways decline must be recorded skipped, recs=%+v", recs)
	}
}

func TestDeliverProactive_ARedeliveredRequestPublishesEachDeliverOnce(t *testing.T) {
	pub := &fakeProactivePub{}
	d, _, _ := newWebhookDispatcher(t, "trent")
	d.SetProactive(pub, &fakeLearned{addrs: map[string]string{"slack": "{}"}}, nothingAdvertised)
	req := a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify}

	d.Deliver(t.Context(), req)
	again := d.Deliver(t.Context(), req)

	if len(pub.calls) != 1 {
		t.Errorf("deliver commands published = %d, want 1", len(pub.calls))
	}
	if countRecs(again, "slack", "skipped") != 1 {
		t.Errorf("redelivery records = %+v, want slack skipped", again)
	}
}

func TestDeliverProactive_HILSkipsNonInteractableType(t *testing.T) {
	learned := &fakeLearned{addrs: map[string]string{"slack": "{}", "webex": "{}"}}
	caps := staticCapabilities{
		"slack": {Advertised: true, Notifiable: true, Interactable: true},
		"webex": {Advertised: true, Notifiable: true},
	}

	pub := &fakeProactivePub{}
	recs := proactiveDispatcherWith(pub, learned, nil, caps).Deliver(t.Context(),
		a2a.NotifyRequest{RequestID: "hil", RecipientUserID: "trent", Mode: a2a.NotifyModeInput})
	if len(pub.calls) != 1 || pub.calls[0].ChannelType != "slack" {
		t.Fatalf("HIL must publish only to the interactable slack, got %+v", pub.calls)
	}
	if countRecs(recs, "webex", "skipped") != 1 {
		t.Fatalf("HIL to non-interactable webex must be recorded skipped, recs=%+v", recs)
	}

	pub2 := &fakeProactivePub{}
	recs2 := proactiveDispatcherWith(pub2, learned, nil, caps).Deliver(t.Context(),
		a2a.NotifyRequest{RequestID: "fyi", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify})
	if len(pub2.calls) != 2 || countRecs(recs2, "webex", "sent") != 1 {
		t.Fatalf("notify mode must use non-interactable webex too, calls=%+v recs=%+v", pub2.calls, recs2)
	}
}

func TestDeliverProactive_NilCapabilitiesFailsClosedInboxStillWritten(t *testing.T) {
	pub := &fakeProactivePub{}
	inbox := &fakeInbox{}
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), inbox, slog.Default())
	d.SetProactive(pub, &fakeLearned{addrs: map[string]string{"slack": "{}"}}, nil)

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Title: "hi"})
	if len(pub.calls) != 0 {
		t.Fatalf("no capabilities source must mean no proactive delivery, got %+v", pub.calls)
	}
	if len(inbox.calls) != 1 || countRecs(recs, "web", "sent") != 1 {
		t.Fatalf("the inbox row must still be written, inbox=%+v recs=%+v", inbox.calls, recs)
	}
}

func TestDeliverProactive_ReachableAndUnreachable(t *testing.T) {
	pub := &fakeProactivePub{}
	learned := &fakeLearned{addrs: map[string]string{"slack": `{"channel_id":"C1"}`}}
	d := proactiveDispatcher(pub, learned, nil)

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Title: "hi",
	})
	if len(pub.calls) != 1 || pub.calls[0].ChannelType != "slack" {
		t.Fatalf("want 1 slack deliver published, got %+v", pub.calls)
	}
	if got := countRecs(recs, "slack", "sent"); got != 1 {
		t.Fatalf("want 1 slack sent record, recs=%+v", recs)
	}
	if got := countRecs(recs, "teams", "sent"); got != 0 {
		t.Fatalf("teams is unreachable; want no teams record, recs=%+v", recs)
	}
}

func TestDeliverProactive_ForwardsArtifacts(t *testing.T) {
	pub := &fakeProactivePub{}
	d := proactiveDispatcher(pub, &fakeLearned{addrs: map[string]string{"slack": `{"channel_id":"C1"}`}}, nil)

	arts := []a2a.NotifyArtifact{{UserID: "trent", SessionID: "s1", Filename: "chart.png"}}
	d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Title: "hi",
		Artifacts: arts,
	})
	if len(pub.calls) != 1 {
		t.Fatalf("want 1 deliver published, got %d", len(pub.calls))
	}
	if got := pub.calls[0].Artifacts; len(got) != 1 || got[0].Filename != "chart.png" {
		t.Fatalf("artifacts not forwarded onto the deliver: %+v", got)
	}
}

func TestDeliverProactive_OptOutAndError(t *testing.T) {
	pub := &fakeProactivePub{}
	d := proactiveDispatcher(pub, &fakeLearned{addrs: map[string]string{"slack": "{}"}}, fakePrefs{disabled: map[string]bool{store.PrefKey("slack", ""): true}})
	recs := d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify})
	if len(pub.calls) != 0 {
		t.Fatalf("opted-out slack must not publish, got %+v", pub.calls)
	}
	if countRecs(recs, "slack", "skipped") != 1 {
		t.Fatalf("want slack skipped, recs=%+v", recs)
	}

	pub2 := &fakeProactivePub{}
	d2 := proactiveDispatcher(pub2, &fakeLearned{err: errors.New("db down")}, nil)
	recs2 := d2.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r2", RecipientUserID: "trent", Mode: a2a.NotifyModeInput, Channels: []string{"slack"}})
	if len(pub2.calls) != 0 || countRecs(recs2, "slack", "failed") != 1 {
		t.Fatalf("lookup error → 1 slack failed, no publish; recs=%+v calls=%+v", recs2, pub2.calls)
	}
}

func TestDeliverProactive_LookupFailureIsVisible(t *testing.T) {
	lookupErr := errors.New("db down")

	learned := &fakeLearned{err: lookupErr}
	recs := proactiveDispatcher(&fakeProactivePub{}, learned, nil).Deliver(t.Context(),
		a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Channels: []string{"web", "slack"}})
	if countRecs(recs, "slack", "failed") != 1 || countRecs(recs, "web", "failed") != 0 {
		t.Errorf("channels [web slack]: recs = %+v, want only slack failed", recs)
	}

	inbox := &fakeInbox{}
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), inbox, slog.Default())
	d.SetProactive(&fakeProactivePub{}, &fakeLearned{err: lookupErr}, nothingAdvertised)
	recs = d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Title: "hi"})
	if countRecs(recs, "web", "sent") != 1 || countRecs(recs, "", "failed") != 1 {
		t.Fatalf("default channels: recs = %+v, want the inbox sent plus one untyped failure", recs)
	}
	if got := summarizeDeliveries(recs); got != "partial" {
		t.Errorf("default channels summarize as %q, want partial", got)
	}

	for _, channels := range [][]string{{"web"}, {"webhook"}, {"web", "email", "connector"}} {
		learned := &fakeLearned{err: lookupErr}
		recs := proactiveDispatcher(&fakeProactivePub{}, learned, nil).Deliver(t.Context(),
			a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Channels: channels})
		if learned.calls != 0 {
			t.Errorf("channels %v: looked up the user's gateway channels %d times, want none", channels, learned.calls)
		}
		for _, r := range recs {
			if r.Status == "failed" {
				t.Errorf("channels %v: unexpected failed record %+v", channels, r)
			}
		}
	}
}

func TestDeliverProactive_PublishErrorRecordedFailed(t *testing.T) {
	pub := &fakeProactivePub{err: errors.New("broker down")}
	d := proactiveDispatcher(pub, &fakeLearned{addrs: map[string]string{"slack": "{}"}}, nil)
	recs := d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeInput, Channels: []string{"slack"}})
	if countRecs(recs, "slack", "failed") != 1 {
		t.Fatalf("a publish error should record slack failed, recs=%+v", recs)
	}
}

func TestDeliverProactive_RespectsExplicitChannels(t *testing.T) {
	pub := &fakeProactivePub{}
	learned := &fakeLearned{addrs: map[string]string{"slack": "{}", "teams": "{}"}}
	d := proactiveDispatcher(pub, learned, nil)
	d.Deliver(t.Context(), a2a.NotifyRequest{RequestID: "r", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify, Channels: []string{"teams"}})
	if len(pub.calls) != 1 || pub.calls[0].ChannelType != "teams" {
		t.Fatalf("explicit channels=[teams] should publish only teams, got %+v", pub.calls)
	}
}

func countRecs(recs []DeliveryRecord, channelType, status string) int {
	n := 0
	for _, r := range recs {
		if r.ChannelType == channelType && r.Status == status {
			n++
		}
	}
	return n
}
