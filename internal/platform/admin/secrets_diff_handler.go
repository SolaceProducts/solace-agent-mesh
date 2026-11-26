// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/subtle"
	"encoding/json"
)

type secretField struct {
	Name     string
	Required bool
}

func modelSecretFields(storedAuthType string, candidate map[string]any) []secretField {
	authType := storedAuthType
	if t, ok := candidate["type"].(string); ok && t != "" {
		authType = t
	}
	keys := authSecretKeysFallingBackToStored(authType, storedAuthType)
	fields := make([]secretField, len(keys))
	for i, k := range keys {
		fields[i] = secretField{Name: k, Required: true}
	}
	return fields
}

func authSecretKeysFallingBackToStored(authType, storedAuthType string) []string {
	keys := modelAuthSecretKeys[authType]
	if len(keys) == 0 {
		keys = modelAuthSecretKeys[storedAuthType]
	}
	return keys
}

func registryToSecretFields(reg map[string]secretFieldInfo) []secretField {
	out := make([]secretField, 0, len(reg))
	for name, info := range reg {
		out = append(out, secretField{Name: name, Required: info.Required})
	}
	return out
}

func compareSecrets(fields []secretField, candidate, stored map[string]any) map[string]SecretFieldDiffResult {
	out := make(map[string]SecretFieldDiffResult, len(fields))
	for _, f := range fields {
		cand, present := candidate[f.Name]
		if !present || cand == nil {
			out[f.Name] = SecretFieldDiffResult{Matches: true}
			continue
		}
		candStr, ok := cand.(string)
		if !ok {
			out[f.Name] = SecretFieldDiffResult{Matches: false}
			continue
		}
		if candStr == redactedSentinel {
			out[f.Name] = SecretFieldDiffResult{Matches: true}
			continue
		}
		if candStr == "" && f.Required {
			out[f.Name] = SecretFieldDiffResult{Matches: true}
			continue
		}
		storedStr, ok := stored[f.Name].(string)
		if !ok {
			out[f.Name] = SecretFieldDiffResult{Matches: false}
			continue
		}
		matches := subtle.ConstantTimeCompare([]byte(candStr), []byte(storedStr)) == 1
		out[f.Name] = SecretFieldDiffResult{Matches: matches}
	}
	return out
}

func compareSetValues(candidate, stored map[string]any) map[string]SecretFieldDiffResult {
	out := make(map[string]SecretFieldDiffResult, len(candidate))
	for name, cand := range candidate {
		if cand == nil || cand == redactedSentinel {
			out[name] = SecretFieldDiffResult{Matches: true}
			continue
		}
		storedVal, present := stored[name]
		candJSON, candErr := storedJSONForm(cand)
		storedJSON, storedErr := storedJSONForm(storedVal)
		matches := present && candErr == nil && storedErr == nil &&
			subtle.ConstantTimeCompare(candJSON, storedJSON) == 1
		out[name] = SecretFieldDiffResult{Matches: matches}
	}
	return out
}

func storedJSONForm(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}
