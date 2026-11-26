// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func gatewayCard(gatewayID string, caps *a2a.NotifyCapabilities) a2a.AgentCard {
	exts := []a2a.AgentExtension{{URI: a2a.ExtensionURIGatewayRole}}
	if caps != nil {
		exts = append(exts, a2a.NotifyCapabilitiesExtension(*caps))
	}
	return a2a.AgentCard{Name: gatewayID, Capabilities: a2a.AgentCapabilities{Extensions: exts}}
}

func notifiable(channelType string, interactable bool) *a2a.NotifyCapabilities {
	return &a2a.NotifyCapabilities{ChannelType: channelType, Notifiable: true, Interactable: interactable}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestRegistry() (*GatewayCapabilityRegistry, *fakeClock) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r := NewGatewayCapabilityRegistry(time.Minute)
	r.now = clock.now
	return r, clock
}

func cardMessage(t *testing.T, card a2a.AgentCard, instanceID string, removed bool) *broker.Message {
	t.Helper()
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	props := map[string]string{cardPropInstanceID: instanceID}
	if removed {
		props[cardPropRemoved] = "true"
	}
	return &broker.Message{Payload: data, UserProperties: props}
}

func TestChannelCapability_DeclinesOnlyOnPositiveKnowledge(t *testing.T) {
	cases := []struct {
		name                     string
		c                        ChannelCapability
		declineDelivery, prompts bool
	}{
		{"never advertised", ChannelCapability{}, false, false},
		{"notifiable and interactable", ChannelCapability{Advertised: true, Notifiable: true, Interactable: true}, false, false},
		{"notifiable only", ChannelCapability{Advertised: true, Notifiable: true}, false, true},
		{"advertised, not notifiable", ChannelCapability{Advertised: true}, true, true},
	}
	for _, tc := range cases {
		if got := tc.c.DeclinesDelivery(); got != tc.declineDelivery {
			t.Errorf("%s: DeclinesDelivery = %v, want %v", tc.name, got, tc.declineDelivery)
		}
		if got := tc.c.DeclinesPrompts(); got != tc.prompts {
			t.Errorf("%s: DeclinesPrompts = %v, want %v", tc.name, got, tc.prompts)
		}
	}
}

func TestCapabilityRegistry_MergesLiveInstancesPerType(t *testing.T) {
	r, _ := newTestRegistry()
	r.Observe(gatewayCard("teams-gw", notifiable("teams", true)), "t1")
	r.Observe(gatewayCard("slack-gw", notifiable("slack", false)), "s1")
	r.Observe(gatewayCard("relay-gw", &a2a.NotifyCapabilities{ChannelType: "relay", Interactable: true}), "r1")

	want := map[string]ChannelCapability{
		"teams":   {Advertised: true, Notifiable: true, Interactable: true},
		"slack":   {Advertised: true, Notifiable: true},
		"relay":   {Advertised: true},
		"unknown": {},
	}
	for ct, w := range want {
		if got := r.Capability(ct); got != w {
			t.Errorf("Capability(%q) = %+v, want %+v", ct, got, w)
		}
	}
}

func TestCapabilityRegistry_IgnoresNonGatewayAndCardsWithoutExtension(t *testing.T) {
	r, _ := newTestRegistry()
	agentCard := a2a.AgentCard{Name: "agent", Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
		a2a.NotifyCapabilitiesExtension(*notifiable("slack", true)),
	}}}
	if c := r.Observe(agentCard, "a1"); c.Kind != CapabilityUnchanged {
		t.Errorf("agent card change = %+v, want unchanged", c)
	}
	if c := r.Observe(gatewayCard("webui", nil), "g1"); c.Kind != CapabilityUnchanged {
		t.Errorf("gateway card without the extension change = %+v, want unchanged", c)
	}
	if got := r.Capability("slack"); got.Advertised {
		t.Errorf("Capability(slack) = %+v, want nothing advertised", got)
	}
}

func TestCapabilityRegistry_ReportsChanges(t *testing.T) {
	r, clock := newTestRegistry()
	slackCaps := notifiable("slack", false)

	if c := r.Observe(gatewayCard("slack-gw", slackCaps), "s1"); c.Kind != CapabilityAdvertised || c.Capabilities != *slackCaps || c.InstanceID != "s1" {
		t.Fatalf("first card = %+v, want advertised with its capabilities", c)
	}
	clock.t = clock.t.Add(50 * time.Second)
	if c := r.Observe(gatewayCard("slack-gw", slackCaps), "s1"); c.Kind != CapabilityUnchanged {
		t.Errorf("heartbeat repeating the same capabilities = %+v, want unchanged", c)
	}
	clock.t = clock.t.Add(50 * time.Second)
	if c := r.Observe(gatewayCard("slack-gw", notifiable("slack", true)), "s1"); c.Kind != CapabilityAdvertised {
		t.Errorf("changed capabilities = %+v, want advertised", c)
	}
	if !r.Capability("slack").Interactable {
		t.Fatal("a refresh must replace capabilities and extend the instance's lifetime")
	}

	if c := r.Observe(gatewayCard("slack-gw", nil), "s1"); c.Kind != CapabilityWithdrawn || c.Capabilities.ChannelType != "slack" {
		t.Errorf("card without the extension = %+v, want withdrawn slack", c)
	}
	if got := r.Capability("slack"); got.Advertised {
		t.Errorf("withdrawn instance still counted: %+v", got)
	}
}

func TestCapabilityRegistry_Expiry(t *testing.T) {
	r, clock := newTestRegistry()
	r.Observe(gatewayCard("slack-gw", notifiable("slack", true)), "s1")
	clock.t = clock.t.Add(30 * time.Second)
	r.Observe(gatewayCard("teams-gw", notifiable("teams", true)), "t1")

	clock.t = clock.t.Add(31 * time.Second)
	if got := r.Capability("slack"); got.Advertised {
		t.Errorf("before sweep: Capability(slack) = %+v, want reads to ignore expired instances", got)
	}
	if !r.Capability("teams").Advertised {
		t.Error("teams is still live")
	}
	expired := r.ExpireStale()
	if len(expired) != 1 || expired[0].GatewayID != "slack-gw" || expired[0].InstanceID != "s1" || expired[0].Capabilities.ChannelType != "slack" {
		t.Errorf("ExpireStale = %+v, want the slack instance", expired)
	}
	if again := r.ExpireStale(); len(again) != 0 {
		t.Errorf("second ExpireStale = %+v, want none", again)
	}
}

func TestGatewayCapabilityTTL_OutlastsSeveralHeartbeats(t *testing.T) {
	heartbeat := time.Duration(config.DefaultCardPublishIntervalSecs) * time.Second
	if gatewayCapabilityTTL < 3*heartbeat {
		t.Errorf("gatewayCapabilityTTL = %v, want at least three default heartbeats (%v)", gatewayCapabilityTTL, 3*heartbeat)
	}
}

func TestCapabilityRegistry_InstanceAwareRemoval(t *testing.T) {
	r, _ := newTestRegistry()
	r.Observe(gatewayCard("slack-gw", notifiable("slack", true)), "old-pod")
	r.Observe(gatewayCard("slack-gw", notifiable("slack", true)), "new-pod")

	if c := r.Remove("slack-gw", "old-pod"); c.Kind != CapabilityWithdrawn {
		t.Errorf("removing a known instance = %+v, want withdrawn", c)
	}
	if !r.Capability("slack").Advertised {
		t.Fatal("the terminating pod's removal card must not evict the live replacement")
	}
	r.Remove("slack-gw", "new-pod")
	if got := r.Capability("slack"); got.Advertised {
		t.Errorf("all instances removed, got %+v", got)
	}
	if c := r.Remove("slack-gw", "new-pod"); c.Kind != CapabilityUnchanged {
		t.Errorf("removing an unknown instance = %+v, want unchanged", c)
	}
}

func TestCapabilityRegistry_AnyInstanceAggregation(t *testing.T) {
	r, _ := newTestRegistry()
	r.Observe(gatewayCard("slack-a", notifiable("slack", false)), "a1")
	r.Observe(gatewayCard("slack-b", notifiable("slack", true)), "b1")

	if !r.Capability("slack").Interactable {
		t.Error("slack must be interactable when any live instance advertises it")
	}
	r.Remove("slack-b", "b1")
	if got := r.Capability("slack"); !got.Advertised || got.Interactable {
		t.Errorf("only the non-interactable instance remains, got %+v", got)
	}
}

func TestCapabilityRegistry_NotifiableTypes(t *testing.T) {
	r, clock := newTestRegistry()
	r.Observe(gatewayCard("teams-gw", notifiable("teams", true)), "t1")
	r.Observe(gatewayCard("slack-a", notifiable("slack", false)), "a1")
	r.Observe(gatewayCard("slack-b", notifiable("slack", true)), "b1")
	r.Observe(gatewayCard("relay-gw", &a2a.NotifyCapabilities{ChannelType: "relay"}), "r1")
	clock.t = clock.t.Add(30 * time.Second)
	r.Observe(gatewayCard("discord-gw", notifiable("discord", false)), "d1")

	if got := r.NotifiableTypes(); !slices.Equal(got, []string{"discord", "slack", "teams"}) {
		t.Fatalf("NotifiableTypes = %v, want each live notifiable type once, sorted, without relay", got)
	}

	clock.t = clock.t.Add(31 * time.Second)
	r.Observe(gatewayCard("discord-gw", notifiable("discord", false)), "d1")
	if got := r.NotifiableTypes(); !slices.Equal(got, []string{"discord"}) {
		t.Fatalf("NotifiableTypes = %v, want only the instance that is still live", got)
	}
}

func TestCapabilityRegistry_ResolvesAddressNeedsANotifiableInstance(t *testing.T) {
	r, _ := newTestRegistry()
	r.Observe(gatewayCard("slack-a", &a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: false, ResolvesAddress: true}), "a1")
	if got := r.Capability("slack"); got.ResolvesAddress {
		t.Fatalf("Capability(slack) = %+v, want no resolving from a gateway that is not notifiable", got)
	}
	r.Observe(gatewayCard("slack-b", &a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true}), "b1")
	if got := r.Capability("slack"); got.ResolvesAddress {
		t.Fatalf("Capability(slack) = %+v, want no resolving while no instance is both", got)
	}
	r.Observe(gatewayCard("slack-c", &a2a.NotifyCapabilities{ChannelType: "slack", Notifiable: true, ResolvesAddress: true}), "c1")
	if got := r.Capability("slack"); !got.ResolvesAddress {
		t.Fatalf("Capability(slack) = %+v, want resolving once one instance is notifiable and resolves", got)
	}
}

func TestCapabilityRegistry_ApplyUsesTheRemovedProperty(t *testing.T) {
	r, _ := newTestRegistry()
	card := gatewayCard("slack-gw", notifiable("slack", true))

	if c, err := r.Apply(cardMessage(t, card, "pod-1", false)); err != nil || c.Kind != CapabilityAdvertised || c.InstanceID != "pod-1" {
		t.Fatalf("heartbeat = %+v, %v; want advertised for pod-1", c, err)
	}
	if c, err := r.Apply(cardMessage(t, card, "pod-1", true)); err != nil || c.Kind != CapabilityWithdrawn {
		t.Fatalf("removal card still carrying the extension = %+v, %v; want withdrawn", c, err)
	}
	if got := r.Capability("slack"); got.Advertised {
		t.Errorf("removed instance still counted: %+v", got)
	}
}

func TestCapabilityRegistry_ApplyRejectsMalformedAndIgnoresAgents(t *testing.T) {
	r, _ := newTestRegistry()
	if _, err := r.Apply(&broker.Message{Payload: []byte("{not json")}); err == nil {
		t.Error("a malformed card must be reported")
	}
	agent := a2a.AgentCard{Name: "agent", Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
		a2a.NotifyCapabilitiesExtension(*notifiable("slack", true)),
	}}}
	if c, err := r.Apply(cardMessage(t, agent, "a1", false)); err != nil || c.Kind != CapabilityUnchanged {
		t.Errorf("agent card = %+v, %v; want ignored", c, err)
	}
}

func publishGatewayCard(t *testing.T, brk broker.Broker, ns string, card a2a.AgentCard, instanceID string, removed bool) {
	t.Helper()
	if err := brk.Publish(context.Background(), a2a.DiscoveryGatewayCardsTopic(ns), cardMessage(t, card, instanceID, removed)); err != nil {
		t.Fatalf("publish gateway card: %v", err)
	}
}

func waitForCapability(t *testing.T, core *NotifyCore, channelType string, want ChannelCapability) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := core.capabilities.Capability(channelType)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Capability(%q) = %+v, want %+v", channelType, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var advertisedInteractive = ChannelCapability{Advertised: true, Notifiable: true, Interactable: true}

func TestCapabilityDiscovery_LearnsAndForgetsFromGatewayCards(t *testing.T) {
	core, brk, ns := startRouterCore(t)

	publishGatewayCard(t, brk, ns, gatewayCard("slack-gw", notifiable("slack", true)), "old-pod", false)
	publishGatewayCard(t, brk, ns, gatewayCard("slack-gw", notifiable("slack", true)), "new-pod", false)
	waitForCapability(t, core, "slack", advertisedInteractive)

	publishGatewayCard(t, brk, ns, gatewayCard("slack-gw", nil), "old-pod", true)
	publishGatewayCard(t, brk, ns, gatewayCard("teams-gw", notifiable("teams", true)), "t1", false)
	waitForCapability(t, core, "teams", advertisedInteractive)
	waitForCapability(t, core, "slack", advertisedInteractive)

	publishGatewayCard(t, brk, ns, gatewayCard("slack-gw", nil), "new-pod", true)
	waitForCapability(t, core, "slack", ChannelCapability{})
}

func TestCapabilityDiscovery_NotStartedWithoutProactiveDelivery(t *testing.T) {
	core, _, _ := startCore(t, "")
	if core.capabilities != nil {
		t.Fatal("capability discovery must only run where proactive delivery is wired")
	}
}

func TestCapabilityDiscovery_StopsOnStop(t *testing.T) {
	core, _, _ := startRouterCore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := core.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("Stop hit its deadline: a capability discovery goroutine did not exit")
	}
}
