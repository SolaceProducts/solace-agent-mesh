// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"strings"
	"testing"
)

func cardWith(exts ...AgentExtension) AgentCard {
	return AgentCard{Name: "gw", Capabilities: AgentCapabilities{Extensions: exts}}
}

func roundTrip(t *testing.T, card AgentCard) AgentCard {
	t.Helper()
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var out AgentCard
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNotifyCapabilitiesFromCard_InProcessAndRoundTrip(t *testing.T) {
	want := NotifyCapabilities{ChannelType: "slack", Notifiable: true, Interactable: true, ResolvesAddress: true, ApproverSets: true}
	card := cardWith(AgentExtension{URI: ExtensionURIGatewayRole}, NotifyCapabilitiesExtension(want))

	for name, c := range map[string]AgentCard{"inProcess": card, "jsonRoundTrip": roundTrip(t, card)} {
		got, ok := NotifyCapabilitiesFromCard(c)
		if !ok || got != want {
			t.Errorf("%s: got (%+v, %v), want (%+v, true)", name, got, ok, want)
		}
	}
}

func TestNotifyCapabilitiesExtension_WireKeysAreSnakeCase(t *testing.T) {
	ext := NotifyCapabilitiesExtension(NotifyCapabilities{ChannelType: "teams", Notifiable: true})
	data, err := json.Marshal(ext.Params)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"approver_sets":false,"channel_type":"teams","interactable":false,"notifiable":true,"resolves_address":false}`
	if string(data) != want {
		t.Errorf("params = %s, want %s", data, want)
	}
}

func TestNotifyCapabilitiesFromCard_Rejects(t *testing.T) {
	ext := func(params map[string]any) AgentExtension {
		return AgentExtension{URI: ExtensionURINotifyCapabilities, Params: params}
	}
	cases := map[string]AgentCard{
		"absent":               cardWith(AgentExtension{URI: ExtensionURIGatewayRole}),
		"noParams":             cardWith(ext(nil)),
		"emptyChannelType":     cardWith(ext(map[string]any{"channel_type": "", "notifiable": true})),
		"nonStringType":        cardWith(ext(map[string]any{"channel_type": 7, "notifiable": true})),
		"topicWildcard":        cardWith(ext(map[string]any{"channel_type": ">", "notifiable": true})),
		"topicSeparator":       cardWith(ext(map[string]any{"channel_type": "slack/x", "notifiable": true})),
		"upperCase":            cardWith(ext(map[string]any{"channel_type": "Slack", "notifiable": true})),
		"oversizedChannelType": cardWith(ext(map[string]any{"channel_type": strings.Repeat("a", maxNotifyChannelTypeBytes+1)})),
	}
	for name, card := range cases {
		if got, ok := NotifyCapabilitiesFromCard(card); ok {
			t.Errorf("%s: got (%+v, true), want ok=false", name, got)
		}
	}
}

func TestNotifyCapabilitiesFromCard_NonBooleanFlagsDecodeFalse(t *testing.T) {
	card := cardWith(AgentExtension{URI: ExtensionURINotifyCapabilities, Params: map[string]any{
		"channel_type": "webhook_relay", "notifiable": "true", "interactable": 1,
	}})
	got, ok := NotifyCapabilitiesFromCard(card)
	if !ok || got != (NotifyCapabilities{ChannelType: "webhook_relay"}) {
		t.Errorf("got (%+v, %v), want channel type only", got, ok)
	}
}

func TestNotifyCapabilitiesFromCard_MissingResolvesAddressIsFalse(t *testing.T) {
	card := cardWith(AgentExtension{URI: ExtensionURINotifyCapabilities, Params: map[string]any{"channel_type": "teams", "notifiable": true}})
	got, ok := NotifyCapabilitiesFromCard(card)
	if !ok || got.ResolvesAddress || !got.Notifiable {
		t.Fatalf("got (%+v, %v), want a notifiable card that does not resolve", got, ok)
	}
}

func TestValidNotifyResolveReplyTopic(t *testing.T) {
	const ns = "test-ns"
	valid := NotifyResolveReplyTopic(ns, "req-1")
	if !ValidNotifyResolveReplyTopic(ns, valid) {
		t.Errorf("%q must be valid", valid)
	}
	for name, topic := range map[string]string{
		"empty":            "",
		"other namespace":  NotifyResolveReplyTopic("other-ns", "req-1"),
		"no id":            NotifyResolveReplyTopic(ns, ""),
		"nested":           valid + "/x",
		"wildcard":         NotifyResolveReplyTopic(ns, ">"),
		"star":             NotifyResolveReplyTopic(ns, "*"),
		"another service":  ns + "/sam/v1/notify/deliver/slack/req-1",
		"the request side": NotifyResolveTopic(ns, "slack"),
	} {
		if ValidNotifyResolveReplyTopic(ns, topic) {
			t.Errorf("%s: %q must be refused", name, topic)
		}
	}
}

func TestIsGatewayCard(t *testing.T) {
	if !IsGatewayCard(cardWith(AgentExtension{URI: ExtensionURIInternal}, AgentExtension{URI: ExtensionURIGatewayRole})) {
		t.Error("card with gateway-role extension not recognised as a gateway")
	}
	if IsGatewayCard(cardWith(NotifyCapabilitiesExtension(NotifyCapabilities{ChannelType: "slack", Notifiable: true}))) {
		t.Error("card without gateway-role extension recognised as a gateway")
	}
}

func TestNotifyResolveReasons_AreFrozen(t *testing.T) {
	for got, want := range map[string]string{
		NotifyResolveNoUser:          "no_user",
		NotifyResolveMissingScope:    "missing_scope",
		NotifyResolveNoDirectMessage: "no_dm",
		NotifyResolveUnavailable:     "unavailable",
	} {
		if got != want {
			t.Errorf("reason %q, want %q", got, want)
		}
	}
}

func TestNotifyRefusalReasons_AreFrozen(t *testing.T) {
	for got, want := range map[string]string{
		NotifyRefusalApproverSetsDisabled: "approver_sets_disabled",
		NotifyRefusalApproverGroups:       "approver_groups_unsupported",
		NotifyRefusalApproverNoUser:       "approver_set_names_no_user",
		NotifyRefusalApproverTooMany:      "approver_set_too_large",
		NotifyRefusalApproverSystem:       "approver_is_system_principal",
		NotifyRefusalApproverInvalid:      "approver_invalid",
		NotifyRefusalApproverPlaceInvalid: "approver_place_invalid",
		NotifyRefusalApproverNotInput:     "approver_set_requires_input_mode",
		NotifyRefusalDeliveryFailed:       "delivery_failed",
		NotifyRefusalApproverStoreDown:    "approver_store_unavailable",
		NotifyRefusalApproverLookupDown:   "approver_lookup_unavailable",
	} {
		if got != want {
			t.Errorf("reason %q, want %q", got, want)
		}
	}
}

func TestNotifyResolveRequest_WireKeys(t *testing.T) {
	data, err := json.Marshal(NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com", ExpiresAt: "2026-10-26T20:15:30.123Z"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"request_id":"r1","user_id":"u1","email":"u1@example.com","expires_at":"2026-10-26T20:15:30.123Z"}`; string(data) != want {
		t.Errorf("request = %s, want %s", data, want)
	}
	data, err = json.Marshal(NotifyResolveRequest{RequestID: "r1", UserID: "u1", Email: "u1@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "expires_at") {
		t.Errorf("request without a deadline = %s, want no expires_at", data)
	}
}
