// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "encoding/json"

const RedactedSentinel = "<REDACTED>"

var authSecretFields = map[string][]string{
	"oauth2":                  {"client_secret"},
	"bearer":                  {"token"},
	"apikey":                  {"api_key"},
	"basic":                   {"password"},
	"aws_iam":                 {"aws_secret_access_key", "aws_session_token"},
	"gcp_service_account":     {"service_account_json", "vertex_credentials"},
	"none":                    {},
	"azure_workload_identity": {},
	"oauth2_delegated":        {},
}

func RedactAuthConfig(authType string, authConfig map[string]any) map[string]any {
	if authConfig == nil {
		return map[string]any{}
	}

	redacted := make(map[string]any, len(authConfig))
	for k, v := range authConfig {
		redacted[k] = v
	}

	delete(redacted, "type")

	secrets := authSecretFields[authType]
	for _, key := range secrets {
		delete(redacted, key)
	}
	return redacted
}

func mergeAuthConfigForUpdate(storedJSON string, incoming map[string]any) (string, string, error) {
	var existing map[string]any
	if err := json.Unmarshal([]byte(storedJSON), &existing); err != nil || existing == nil {
		existing = map[string]any{}
	}

	oldType, _ := existing["type"].(string)
	authType := oldType
	if t, ok := incoming["type"].(string); ok && t != "" {
		authType = t
	}

	secrets := make(map[string]struct{}, len(authSecretFields[authType]))
	for _, k := range authSecretFields[authType] {
		secrets[k] = struct{}{}
	}

	if AuthTypeSwitchReplacesConfig(oldType, authType) {
		existing = map[string]any{}
	}
	stripPriorAuthTypeSecretsOnFlip(existing, oldType, authType)

	for k, v := range incoming {
		if isProtectedSecretOverlay(secrets, k, v) {
			continue
		}
		existing[k] = v
	}

	merged, err := json.Marshal(existing)
	if err != nil {
		return "", "", &ValidationError{Message: "invalid authConfig"}
	}
	resolvedType, _ := existing["type"].(string)
	return string(merged), resolvedType, nil
}

func stripPriorAuthTypeSecretsOnFlip(existing map[string]any, oldType, authType string) {
	if authType == oldType || oldType == "" {
		return
	}
	for _, k := range authSecretFields[oldType] {
		delete(existing, k)
	}
}

func isProtectedSecretOverlay(secrets map[string]struct{}, k string, v any) bool {
	if _, isSecret := secrets[k]; !isSecret {
		return false
	}
	s, ok := v.(string)
	if !ok {
		return true
	}
	return s == "" || s == RedactedSentinel
}
