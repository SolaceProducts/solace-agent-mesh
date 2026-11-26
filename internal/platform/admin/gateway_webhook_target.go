// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
)

const (
	webhookTargetAgent    = "agent"
	webhookTargetWorkflow = "workflow"
)

func webhookTargetType(values map[string]any) string {
	if explicit := strings.TrimSpace(stringOrDefault(values, "targetType", "")); explicit != "" {
		return explicit
	}
	if strings.TrimSpace(stringOrDefault(values, "targetWorkflowName", "")) != "" {
		return webhookTargetWorkflow
	}
	return webhookTargetAgent
}

func normalizeWebhookValues(values map[string]any) map[string]any {
	normalized := make(map[string]any, len(values)+1)
	maps.Copy(normalized, values)
	normalized["targetType"] = webhookTargetType(values)
	for _, key := range []string{"targetAgentName", "targetWorkflowName"} {
		if _, present := normalized[key]; present && strings.TrimSpace(stringOrDefault(normalized, key, "")) == "" {
			delete(normalized, key)
		}
	}
	return normalized
}

func normalizeWhatsAppValues(values map[string]any) map[string]any {
	normalized := values
	copied := false
	ensureCopy := func() {
		if copied {
			return
		}
		next := make(map[string]any, len(normalized)+2)
		maps.Copy(next, normalized)
		normalized, copied = next, true
	}

	responseFormatPresent := false
	for _, alias := range []string{"responseFormat", "response_format"} {
		if _, present := values[alias]; present {
			responseFormatPresent = true
			break
		}
	}
	if !responseFormatPresent {
		ensureCopy()
		normalized["responseFormat"] = whatsapp.DefaultResponseFormat
	}
	for _, retired := range whatsapp.RetiredAllowlistKeys {
		if _, present := normalized[retired]; present {
			ensureCopy()
			delete(normalized, retired)
		}
	}
	return stampWhatsAppSenderIdentityMode(normalized)
}

func stampWhatsAppSenderIdentityMode(values map[string]any) map[string]any {
	if _, present := values[SenderIdentityModeKey]; present {
		return values
	}
	stamped := make(map[string]any, len(values)+1)
	maps.Copy(stamped, values)
	stamped[SenderIdentityModeKey] = whatsAppSenderIdentityMode(values)
	return stamped
}

func normalizeValuesForType(gatewayType string, values map[string]any) map[string]any {
	switch gatewayType {
	case "webhook":
		return normalizeWebhookValues(values)
	case "whatsapp":
		return normalizeWhatsAppValues(values)
	case "google_chat":
		return stampGoogleChatDelivery(values)
	}
	return values
}

func NormalizeGatewayValues(gatewayType string, values any) any {
	bag, ok := values.(map[string]any)
	if !ok {
		return values
	}
	return normalizeValuesForType(gatewayType, bag)
}
