// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const AuthTypeAzureWorkloadIdentity = "azure_workload_identity"

const azureModelPrefix = "azure/"

var azureWorkloadIdentityFields = []string{"client_id", "tenant_id"}

func azureWorkloadIdentityBlock(authConfig map[string]any) map[string]any {
	block := map[string]any{}
	for _, k := range azureWorkloadIdentityFields {
		if v, ok := authConfig[k]; ok {
			block[k] = v
		}
	}
	return block
}

func ValidateRuntimeModelConfig(raw map[string]any) error {
	if err := validateDelegatedModelConfig(raw); err != nil {
		return err
	}
	block, ok := raw[llm.AzureWorkloadIdentityKey].(map[string]any)
	if !ok {
		return nil
	}

	details := map[string][]string{}
	if !llm.AzureWorkloadIdentityAvailable() {
		details["authConfig.type"] = []string{AuthTypeAzureWorkloadIdentity + " auth is not enabled on this deployment: it needs " + llm.AzureWorkloadIdentityEnabledEnv + "=true and " + llm.AzureFederatedTokenFileEnv + " set"}
	}
	if model, _ := raw["model"].(string); !strings.HasPrefix(model, azureModelPrefix) {
		details["provider"] = []string{AuthTypeAzureWorkloadIdentity + " auth requires provider azure_openai"}
	}
	for _, k := range azureWorkloadIdentityFields {
		if id, _ := block[k].(string); !isGUID(id) {
			details["authConfig."+k] = []string{k + " must be a GUID, such as 00000000-0000-0000-0000-000000000000"}
		}
	}
	for key, value := range raw {
		if value == nil || value == "" {
			continue
		}
		if llm.IsAzureCredentialKey(key) {
			details[key] = []string{key + " is not allowed with " + AuthTypeAzureWorkloadIdentity + " auth"}
		}
	}
	if route, _ := raw["azure_chat_route"].(string); strings.EqualFold(strings.TrimSpace(route), "deployment") {
		details["azure_chat_route"] = []string{"azure_chat_route deployment cannot be used with " + AuthTypeAzureWorkloadIdentity + " auth: remove azure_chat_route or set it to v1"}
	}

	if len(details) == 0 {
		return nil
	}
	return &ValidationError{Message: "invalid " + AuthTypeAzureWorkloadIdentity + " config: " + joinDetailMessages(details), Details: details}
}

func isGUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil && len(s) == len("00000000-0000-0000-0000-000000000000")
}

func AuthTypeSwitchReplacesConfig(oldType, newType string) bool {
	if newType == "" || oldType == newType {
		return false
	}
	return oldType == AuthTypeAzureWorkloadIdentity || newType == AuthTypeAzureWorkloadIdentity ||
		oldType == AuthTypeOAuth2Delegated || newType == AuthTypeOAuth2Delegated
}

func joinDetailMessages(details map[string][]string) string {
	var msgs []string
	for _, field := range slices.Sorted(maps.Keys(details)) {
		msgs = append(msgs, details[field]...)
	}
	return strings.Join(msgs, "; ")
}
