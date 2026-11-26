// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

const ExtensionURIGatewayRole = "https://solace.com/a2a/extensions/sam/gateway-role"

func IsGatewayCard(card AgentCard) bool {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIGatewayRole {
			return true
		}
	}
	return false
}

const ExtensionURINotifyCapabilities = "https://solace.com/a2a/extensions/sam/notify-capabilities"

type NotifyCapabilities struct {
	ChannelType     string
	Notifiable      bool
	Interactable    bool
	ResolvesAddress bool
	ApproverSets    bool
}

const maxNotifyChannelTypeBytes = 64

func NotifyCapabilitiesExtension(c NotifyCapabilities) AgentExtension {
	return AgentExtension{
		URI:         ExtensionURINotifyCapabilities,
		Description: "Notification-service capabilities of this gateway",
		Params: map[string]any{
			"channel_type":     c.ChannelType,
			"notifiable":       c.Notifiable,
			"interactable":     c.Interactable,
			"resolves_address": c.ResolvesAddress,
			"approver_sets":    c.ApproverSets,
		},
	}
}

func NotifyCapabilitiesFromCard(card AgentCard) (NotifyCapabilities, bool) {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURINotifyCapabilities {
			continue
		}
		channelType, _ := ext.Params["channel_type"].(string)
		if !ValidNotifyChannelType(channelType) {
			return NotifyCapabilities{}, false
		}
		notifiable, _ := ext.Params["notifiable"].(bool)
		interactable, _ := ext.Params["interactable"].(bool)
		resolvesAddress, _ := ext.Params["resolves_address"].(bool)
		approverSets, _ := ext.Params["approver_sets"].(bool)
		return NotifyCapabilities{
			ChannelType: channelType, Notifiable: notifiable, Interactable: interactable,
			ResolvesAddress: resolvesAddress, ApproverSets: approverSets,
		}, true
	}
	return NotifyCapabilities{}, false
}

func ValidNotifyChannelType(s string) bool {
	if s == "" || len(s) > maxNotifyChannelTypeBytes {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
