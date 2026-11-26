// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const AuthTypeOAuth2Delegated = "oauth2_delegated"

const GrantIDInModelParamsMessage = llm.GrantIDKey + " cannot be set in modelParams; use a " + AuthTypeOAuth2Delegated + " auth config"

var delegatedCredentialKeys = []string{"api_key", "api_key_file", llm.AzureWorkloadIdentityKey}

func validateDelegatedModelConfig(raw map[string]any) error {
	value, ok := raw[llm.GrantIDKey]
	if !ok {
		return nil
	}
	details := map[string][]string{}
	if id, _ := value.(string); !isGUID(id) {
		details["authConfig."+llm.GrantIDKey] = []string{llm.GrantIDKey + " must be the UUID of an OAuth grant"}
	}
	for key, v := range raw {
		if v == nil || v == "" {
			continue
		}
		if strings.HasPrefix(key, "oauth_") || slices.Contains(delegatedCredentialKeys, key) {
			details[key] = []string{key + " is not allowed with " + AuthTypeOAuth2Delegated + " auth"}
		}
	}
	if len(details) == 0 {
		return nil
	}
	return &ValidationError{Message: "invalid " + AuthTypeOAuth2Delegated + " config: " + joinDetailMessages(details), Details: details}
}
