// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
)

const reservedAuthConfigKey = "auth"

var allowedAuthCredentialKeys = map[string]struct{}{
	"client_id":     {},
	"client_secret": {},
	"username":      {},
	"password":      {},
	"token":         {},
}

var authCredentialSecretKeys = map[string]struct{}{
	"client_secret": {},
	"password":      {},
	"token":         {},
}

var allowedDeployerAuthTypes = map[string]struct{}{
	"oauth2": {},
	"basic":  {},
	"bearer": {},
}

var allowedAuthSchemeKeys = map[string]struct{}{
	"audience":                   {},
	"resource":                   {},
	"refresh_url":                {},
	"token_endpoint_auth_method": {},
	"disable_pkce":               {},
	"disable_consent_prompt":     {},
}

var authSchemeBoolKeys = map[string]struct{}{
	"disable_pkce":           {},
	"disable_consent_prompt": {},
}

func validateDeployerAuthBlock(val any) map[string][]string {
	details := map[string][]string{}
	if val == nil {
		return details
	}
	m, ok := val.(map[string]any)
	if !ok {
		recordExpectedObject(details, reservedAuthConfigKey, val)
		return details
	}
	for k, v := range m {
		switch k {
		case "credential":
			validateAuthLeafMap(details, "credential", v, allowedAuthCredentialKeys, nil,
				"field is not allowed in deployer-supplied auth credential (allowed: client_id, client_secret, username, password, token)")
		case "scheme":
			validateAuthLeafMap(details, "scheme", v, allowedAuthSchemeKeys, authSchemeBoolKeys,
				"field is not allowed in deployer-supplied auth scheme (authorization_url/token_url/scopes come from the SDK declaration)")
		case "type":
			validateDeployerAuthType(details, v)
		default:
			details[reservedAuthConfigKey+"."+k] = append(details[reservedAuthConfigKey+"."+k],
				"field is not allowed in deployer-supplied auth block")
		}
	}
	return details
}

func validateDeployerAuthType(details map[string][]string, v any) {
	path := reservedAuthConfigKey + ".type"
	if v == nil {
		return
	}
	ts, ok := v.(string)
	if !ok {
		details[path] = append(details[path], fmt.Sprintf("expected string, got %T", v))
		return
	}
	if _, ok := allowedDeployerAuthTypes[ts]; !ok {
		details[path] = append(details[path],
			fmt.Sprintf("unsupported auth type %q (allowed: basic, bearer, oauth2)", ts))
	}
}

func validateAuthLeafMap(details map[string][]string, parentKey string, v any, allowed, boolKeys map[string]struct{}, disallowedMsg string) {
	if v == nil {
		return
	}
	parentPath := reservedAuthConfigKey + "." + parentKey
	m, ok := v.(map[string]any)
	if !ok {
		recordExpectedObject(details, parentPath, v)
		return
	}
	for leaf, lv := range m {
		path := parentPath + "." + leaf
		if _, ok := allowed[leaf]; !ok {
			details[path] = append(details[path], disallowedMsg)
			continue
		}
		if lv == nil {
			continue
		}
		if _, isBool := boolKeys[leaf]; isBool {
			if _, ok := lv.(bool); !ok {
				details[path] = append(details[path], fmt.Sprintf("expected bool, got %T", lv))
			}
			continue
		}
		if _, ok := lv.(string); !ok {
			details[path] = append(details[path], fmt.Sprintf("expected string, got %T", lv))
		}
	}
}

func recordExpectedObject(details map[string][]string, path string, got any) {
	details[path] = append(details[path], fmt.Sprintf("expected object, got %T", got))
}

func rejectReservedConfigKey(configSchema *string) string {
	if configSchema == nil || *configSchema == "" {
		return ""
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(*configSchema), &entries); err != nil {
		return ""
	}
	for _, e := range entries {
		switch k, _ := e["key"].(string); k {
		case reservedAuthConfigKey:
			return fmt.Sprintf("declared config field %q is reserved for deployer-supplied OAuth credentials; remove it from the tool's config schema declaration", reservedAuthConfigKey)
		case reservedHILConfigKey:
			return fmt.Sprintf("declared config field %q is reserved for per-tool human-in-the-loop approval config; remove it from the tool's config schema declaration", reservedHILConfigKey)
		case reservedRequiredScopesConfigKey:
			return fmt.Sprintf("declared config field %q is reserved for per-tool RBAC required scopes; remove it from the tool's config schema declaration", reservedRequiredScopesConfigKey)
		}
	}
	return ""
}

func sanitizeDeployerAuthValue(val any) map[string]any {
	m, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	if ts, ok := m["type"].(string); ok {
		if _, allowed := allowedDeployerAuthTypes[ts]; allowed {
			out["type"] = ts
		}
	}
	if cred, ok := m["credential"].(map[string]any); ok {
		safeCred := filterAllowedFields(cred, allowedAuthCredentialKeys, nil, authCredentialSecretKeys)
		if len(safeCred) > 0 {
			out["credential"] = safeCred
		}
	}
	if scheme, ok := m["scheme"].(map[string]any); ok {
		safeScheme := filterAllowedFields(scheme, allowedAuthSchemeKeys, authSchemeBoolKeys, nil)
		if len(safeScheme) > 0 {
			out["scheme"] = safeScheme
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterAllowedFields(src map[string]any, allowed, boolKeys, redactKeys map[string]struct{}) map[string]any {
	out := map[string]any{}
	for k, v := range src {
		if _, ok := allowed[k]; !ok {
			continue
		}
		if _, isBool := boolKeys[k]; isBool {
			b, ok := v.(bool)
			if !ok {
				continue
			}
			out[k] = b
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if _, isSecret := redactKeys[k]; isSecret {
			out[k] = redactedSentinel
		} else {
			out[k] = s
		}
	}
	return out
}

func mergeDeployerAuthBlock(newAuth, existingAuth any) any {
	newMap, ok := newAuth.(map[string]any)
	if !ok {
		return newAuth
	}
	existingMap, _ := existingAuth.(map[string]any)
	newCred, _ := newMap["credential"].(map[string]any)
	if newCred == nil {
		return newAuth
	}
	existingCred, _ := existingMap["credential"].(map[string]any)
	for leaf := range authCredentialSecretKeys {
		newVal, present := newCred[leaf]
		if !present {
			continue
		}
		if newVal == nil {
			if existingVal, ok := existingCred[leaf]; ok {
				newCred[leaf] = existingVal
			} else {
				delete(newCred, leaf)
			}
			continue
		}
		s, isStr := newVal.(string)
		if !isStr {
			continue
		}
		switch s {
		case redactedSentinel:
			if existingVal, ok := existingCred[leaf]; ok {
				newCred[leaf] = existingVal
			} else {
				delete(newCred, leaf)
			}
		case "":
			delete(newCred, leaf)
		}
	}
	if len(newCred) == 0 {
		delete(newMap, "credential")
	} else {
		newMap["credential"] = newCred
	}
	return newMap
}
