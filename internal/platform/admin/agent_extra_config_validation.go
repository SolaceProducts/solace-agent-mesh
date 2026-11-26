// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const additionalConfigurationsMaxBytes = 64 * 1024

const additionalConfigurationsMaxDepth = 8

const additionalConfigurationsMaxKeyLen = 256

var forbiddenAdditionalConfigStructuredKeys = map[string]struct{}{
	"name":                 {},
	"description":          {},
	"systemPrompt":         {},
	"type":                 {},
	"skills":               {},
	"skillRefs":            {},
	"skillIds":             {},
	"toolsets":             {},
	"toolsetExcludedTools": {},
	"toolsetConfigs":       {},
	"connectors":           {},
	"inputModes":           {},
	"outputModes":          {},
	"modelProvider":        {},
	"deploy":               {},
}

var forbiddenAdditionalConfigComputedKeys = map[string]struct{}{
	"agentName":   {},
	"namespace":   {},
	"instruction": {},
	"tools":       {},
	"ephemeral":   {},
	"deployment":  {},
}

var strippedUnsupportedKeys = map[string]struct{}{
	"hil": {},
}

var forbiddenComputedRuntimeKeys = map[string]struct{}{
	"agent_name":     {},
	"display_name":   {},
	"namespace":      {},
	"model_provider": {},
	"instruction":    {},
	"tools":          {},
	"ephemeral":      {},
	"deployment":     {},
}

func validateAdditionalConfigurations(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}

	if len(raw) > additionalConfigurationsMaxBytes {
		return &ValidationError{
			Message: fmt.Sprintf("additionalConfigurations exceeds %d bytes", additionalConfigurationsMaxBytes),
			Field:   "additionalConfigurations",
			Details: map[string][]string{
				"additionalConfigurations": {fmt.Sprintf("payload is %d bytes; limit is %d", len(raw), additionalConfigurationsMaxBytes)},
			},
		}
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return &ValidationError{
			Message: "additionalConfigurations is not valid JSON",
			Field:   "additionalConfigurations",
			Details: map[string][]string{
				"additionalConfigurations": {fmt.Sprintf("invalid JSON: %v", err)},
			},
		}
	}

	obj, ok := parsed.(map[string]any)
	if !ok {
		return &ValidationError{
			Message: "additionalConfigurations must be a JSON object",
			Field:   "additionalConfigurations",
			Details: map[string][]string{
				"additionalConfigurations": {"top-level value must be a JSON object"},
			},
		}
	}

	for key := range obj {
		if _, blocked := forbiddenAdditionalConfigStructuredKeys[key]; blocked {
			return &ValidationError{
				Message: fmt.Sprintf("additionalConfigurations may not set %q; that field belongs on the structured agent request", key),
				Field:   "additionalConfigurations",
				Details: map[string][]string{
					"additionalConfigurations": {fmt.Sprintf("top-level key %q collides with a structured DTO field; remove it from additionalConfigurations and set it directly", key)},
				},
			}
		}
		if _, blocked := forbiddenAdditionalConfigComputedKeys[key]; blocked {
			return &ValidationError{
				Message: fmt.Sprintf("additionalConfigurations may not set %q; that key is computed by the platform at deploy time", key),
				Field:   "additionalConfigurations",
				Details: map[string][]string{
					"additionalConfigurations": {fmt.Sprintf("top-level key %q is owned by the platform's YAML emitter; remove it from additionalConfigurations", key)},
				},
			}
		}
	}

	if err := walkAdditionalConfigurations(obj, 1, ""); err != nil {
		return err
	}
	return nil
}

func walkAdditionalConfigurations(node any, depth int, path string) error {
	switch v := node.(type) {
	case map[string]any:
		if depth > additionalConfigurationsMaxDepth {
			return &ValidationError{
				Message: fmt.Sprintf("additionalConfigurations nesting exceeds %d levels", additionalConfigurationsMaxDepth),
				Field:   "additionalConfigurations",
				Details: map[string][]string{
					"additionalConfigurations": {fmt.Sprintf("path %q exceeds the %d-level depth limit", displayPath(path), additionalConfigurationsMaxDepth)},
				},
			}
		}
		for key, child := range v {
			if err := validateAdditionalConfigKey(key, path); err != nil {
				return err
			}
			childPath := key
			if path != "" {
				childPath = path + "/" + key
			}
			if err := walkAdditionalConfigurations(child, depth+1, childPath); err != nil {
				return err
			}
		}
	case []any:
		if depth > additionalConfigurationsMaxDepth {
			return &ValidationError{
				Message: fmt.Sprintf("additionalConfigurations nesting exceeds %d levels", additionalConfigurationsMaxDepth),
				Field:   "additionalConfigurations",
				Details: map[string][]string{
					"additionalConfigurations": {fmt.Sprintf("path %q exceeds the %d-level depth limit", displayPath(path), additionalConfigurationsMaxDepth)},
				},
			}
		}
		for i, child := range v {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if err := walkAdditionalConfigurations(child, depth+1, childPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAdditionalConfigKey(key, path string) error {
	if len(key) > additionalConfigurationsMaxKeyLen {
		fullPath := key
		if path != "" {
			fullPath = path + "/" + key
		}
		return &ValidationError{
			Message: fmt.Sprintf("additionalConfigurations key at %q exceeds %d bytes", displayPath(fullPath), additionalConfigurationsMaxKeyLen),
			Field:   "additionalConfigurations",
			Details: map[string][]string{
				"additionalConfigurations": {fmt.Sprintf("key length %d exceeds limit %d", len(key), additionalConfigurationsMaxKeyLen)},
			},
		}
	}
	if strings.ContainsRune(key, '\x00') {
		fullPath := key
		if path != "" {
			fullPath = path + "/" + key
		}
		return &ValidationError{
			Message: fmt.Sprintf("additionalConfigurations key at %q contains a NUL byte", displayPath(fullPath)),
			Field:   "additionalConfigurations",
			Details: map[string][]string{
				"additionalConfigurations": {"keys may not contain NUL bytes"},
			},
		}
	}
	return nil
}

func displayPath(p string) string {
	if p == "" {
		return "(root)"
	}
	return p
}

func normalizeAdditionalConfigurations(validatedBlob json.RawMessage) []byte {
	if len(validatedBlob) == 0 {
		return []byte("{}")
	}
	stripped, ok := stripUnsupportedKeys(validatedBlob)
	if ok {
		return stripped
	}
	return bytes.Clone(validatedBlob)
}

func stripUnsupportedKeys(src []byte) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(src, &obj); err != nil {
		return nil, false
	}
	removed := false
	for key := range strippedUnsupportedKeys {
		if _, present := obj[key]; present {
			delete(obj, key)
			removed = true
		}
	}
	if !removed {
		return nil, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
}

func StripUnsupportedAdditionalConfigKeys(src json.RawMessage) json.RawMessage {
	stripped, ok := stripUnsupportedKeys(src)
	if !ok {
		return src
	}
	return stripped
}

func canonicalizeAdditionalConfigurationsForHash(src []byte) string {
	if len(src) == 0 {
		return "{}"
	}
	var parsed any
	if err := json.Unmarshal(src, &parsed); err != nil {
		return "{}"
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return "{}"
	}
	return string(out)
}

func cloneAdditionalConfigurations(src []byte) json.RawMessage {
	if len(src) == 0 || isEmptyJSONObject(src) {
		return nil
	}
	return bytes.Clone(src)
}

func isEmptyJSONObject(b []byte) bool {
	trimmed := strings.TrimSpace(string(b))
	return trimmed == "{}"
}
