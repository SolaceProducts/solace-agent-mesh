// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "strings"

const (
	SenderIdentityModeKey = "sender_identity_mode"

	SenderIdentityIDPLookup = "idp_lookup"

	SenderIdentitySystemUser = "system_user"
)

func whatsAppSenderIdentityMode(values map[string]any) string {
	if mode, _ := values[SenderIdentityModeKey].(string); strings.TrimSpace(mode) != "" {
		return strings.TrimSpace(mode)
	}
	if runAs, _ := values["run_as"].(string); strings.TrimSpace(runAs) != "" {
		return SenderIdentitySystemUser
	}
	return SenderIdentityIDPLookup
}

func StripDerivedGatewayValues(gatewayType string, values map[string]any) {
	switch gatewayType {
	case "whatsapp":
		delete(values, SenderIdentityModeKey)
	case "mcp":
		stripStampedMCPStateRedirection(values)
	}
}
