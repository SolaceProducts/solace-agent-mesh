// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"strings"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

var modelAuthSecretKeys = map[string][]string{
	"apikey":                  {"api_key"},
	"oauth2":                  {"client_secret"},
	"bearer":                  {"token"},
	"basic":                   {"password"},
	"aws_iam":                 {"aws_secret_access_key", "aws_session_token"},
	"gcp_service_account":     {"service_account_json", "vertex_credentials"},
	"none":                    {},
	"azure_workload_identity": {},
	"oauth2_delegated":        {},
}

func redactModelAuthConfig(authConfig map[string]any) map[string]any {
	if len(authConfig) == 0 {
		return map[string]any{}
	}

	redacted := make(map[string]any, len(authConfig))
	maps.Copy(redacted, authConfig)

	authType, _ := redacted["type"].(string)
	for _, key := range modelAuthSecretKeys[authType] {
		if _, exists := redacted[key]; exists {
			redacted[key] = redactedSentinel
		}
	}
	return redacted
}

func switchesProvider(storedProvider, requestedProvider string) bool {
	return requestedProvider != "" && !strings.EqualFold(requestedProvider, storedProvider)
}

func onlyAuthType(authConfig map[string]any) map[string]any {
	if t, ok := authConfig["type"].(string); ok && t != "" {
		return map[string]any{"type": t}
	}
	return map[string]any{}
}

func mergeModelAuthConfig(existing, incoming map[string]any) map[string]any {
	oldType, _ := existing["type"].(string)
	authType := oldType
	if t, ok := incoming["type"].(string); ok && t != "" {
		authType = t
	}

	secrets := make(map[string]struct{}, 4)
	for _, k := range modelAuthSecretKeys[authType] {
		secrets[k] = struct{}{}
	}

	merged := make(map[string]any, len(existing)+len(incoming))
	if !platpkg.AuthTypeSwitchReplacesConfig(oldType, authType) {
		maps.Copy(merged, existing)
	}

	if authType != oldType && oldType != "" {
		for _, k := range modelAuthSecretKeys[oldType] {
			delete(merged, k)
		}
	}

	for k, v := range incoming {
		if _, isSecret := secrets[k]; isSecret {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if s == "" || s == redactedSentinel {
				continue
			}
		}
		merged[k] = v
	}
	return merged
}
