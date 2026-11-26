// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
)

func validateAdditionalConfigurationsTier2(raw, stored json.RawMessage, strict bool) (warnings []string, err error) {
	return tier2WithOptionalBounds(raw, stored, strict, true)
}

func tier2WithOptionalBounds(raw, stored json.RawMessage, strict, checkBounds bool) (warnings []string, err error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, nil
	}

	var found extraConfigFindings
	walkExtraConfigSchema(obj, agentExtraConfigSchema, "", &found)
	unknown := found.unknown
	if !checkBounds {
		found.rules = nil
	}
	blockingRules, carriedRules := splitByStoredValue(found.rules, obj, stored)
	typeErrors := slices.Concat(found.typeErrors, blockingRules)

	if len(typeErrors) > 0 {
		sort.Strings(typeErrors)
		return nil, &ValidationError{
			Message: fmt.Sprintf("additionalConfigurations contains %d value(s) that do not match the agent config schema", len(typeErrors)),
			Field:   "additionalConfigurations",
			Details: map[string][]string{
				"additionalConfigurations": typeErrors,
			},
		}
	}

	sort.Strings(carriedRules)
	for _, msg := range carriedRules {
		warnings = append(warnings, fmt.Sprintf("additionalConfigurations: %s; kept because this save does not change the stored value, and it will be refused once changed", msg))
	}
	if len(unknown) == 0 {
		return warnings, nil
	}
	sort.Strings(unknown)
	if strict {
		details := make([]string, 0, len(unknown))
		for _, k := range unknown {
			details = append(details, fmt.Sprintf("unknown key %q (strict mode)", k))
		}
		return nil, &ValidationError{
			Message: fmt.Sprintf("additionalConfigurations contains %d unknown key(s); strict mode requires every key to be recognised by the agent runtime", len(unknown)),
			Field:   "additionalConfigurations",
			Details: map[string][]string{
				"additionalConfigurations": details,
			},
		}
	}
	for _, k := range unknown {
		if _, dropped := strippedUnsupportedKeys[k]; dropped {
			warnings = append(warnings, fmt.Sprintf("additionalConfigurations: %q is not implemented at agent level and has been removed; human-in-the-loop is configured per tool, on the tool's own entry under tools[]", k))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("additionalConfigurations: unknown key %q is not consumed by the agent runtime; check for typos", k))
	}
	return warnings, nil
}

type extraConfigFindings struct {
	unknown    []string
	typeErrors []string
	rules      []extraConfigRuleViolation
}

func walkExtraConfigSchema(obj map[string]any, schema map[string]agentExtraConfigKey, path string, found *extraConfigFindings) {
	for key, val := range obj {
		fullPath := key
		if path != "" {
			fullPath = path + "." + key
		}
		entry, recognised := schema[key]
		if !recognised {
			found.unknown = append(found.unknown, fullPath)
			continue
		}
		if msg := checkExtraConfigType(entry, val, fullPath); msg != "" {
			found.typeErrors = append(found.typeErrors, msg)
			continue
		}
		if msg := checkExtraConfigRule(entry, val, fullPath); msg != "" {
			found.rules = append(found.rules, extraConfigRuleViolation{topKey: topLevelKey(fullPath), message: msg})
		}
		if entry.Type == extraTypeObject && entry.Nested != nil {
			child, ok := val.(map[string]any)
			if !ok {
				continue
			}
			walkExtraConfigSchema(child, entry.Nested, fullPath, found)
		}
		if entry.Type == extraTypeList && entry.Nested != nil {
			arr, ok := val.([]any)
			if !ok {
				continue
			}
			for i, item := range arr {
				itemPath := fullPath + "[" + strconv.Itoa(i) + "]"
				m, ok := item.(map[string]any)
				if !ok {
					found.typeErrors = append(found.typeErrors, fmt.Sprintf("%s: expected object, got %s", itemPath, jsonTypeName(item)))
					continue
				}
				walkExtraConfigSchema(m, entry.Nested, itemPath, found)
			}
		}
	}
}

func checkExtraConfigType(entry agentExtraConfigKey, val any, path string) string {
	switch entry.Type {
	case extraTypeAny:
		return ""
	case extraTypeBool:
		if _, ok := val.(bool); !ok {
			return fmt.Sprintf("%s: expected boolean, got %s", path, jsonTypeName(val))
		}
	case extraTypeInt:
		f, ok := val.(float64)
		if !ok {
			return fmt.Sprintf("%s: expected integer, got %s", path, jsonTypeName(val))
		}
		if _, frac := math.Modf(f); frac != 0 {
			return fmt.Sprintf("%s: expected integer, got fractional number", path)
		}
	case extraTypeNumber:
		if _, ok := val.(float64); !ok {
			return fmt.Sprintf("%s: expected number, got %s", path, jsonTypeName(val))
		}
	case extraTypeString:
		if _, ok := val.(string); !ok {
			return fmt.Sprintf("%s: expected string, got %s", path, jsonTypeName(val))
		}
	case extraTypeStringList:
		arr, ok := val.([]any)
		if !ok {
			return fmt.Sprintf("%s: expected array of strings, got %s", path, jsonTypeName(val))
		}
		for i, el := range arr {
			if _, ok := el.(string); !ok {
				return fmt.Sprintf("%s[%d]: expected string, got %s", path, i, jsonTypeName(el))
			}
		}
	case extraTypeObject:
		if _, ok := val.(map[string]any); !ok {
			return fmt.Sprintf("%s: expected object, got %s", path, jsonTypeName(val))
		}
	case extraTypeList:
		if _, ok := val.([]any); !ok {
			return fmt.Sprintf("%s: expected array, got %s", path, jsonTypeName(val))
		}
	case extraTypeStringOrObject:
		switch val.(type) {
		case string, map[string]any:
			return ""
		default:
			return fmt.Sprintf("%s: expected string or object, got %s", path, jsonTypeName(val))
		}
	}
	return ""
}

func checkExtraConfigRule(entry agentExtraConfigKey, val any, path string) string {
	if f, isNumber := val.(float64); isNumber && entry.Type == extraTypeInt && entry.IntMin != nil && belowMinimum(f, *entry.IntMin) {
		return fmt.Sprintf("%s: value %v is below minimum %d", path, f, *entry.IntMin)
	}
	return ""
}

func belowMinimum(value float64, minimum int) bool {
	return value < float64(minimum)
}

func jsonTypeName(v any) string {
	if v == nil {
		return "null"
	}
	switch v.(type) {
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}
