// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	maxEchoedValueKeyLen = 64

	minPrefixHintLen = 3

	maxHintEditDistance = 2
	minEditHintLen      = 5

	maxReportedKeys = 10
)

var legacyEventMeshOperationKeys = []string{
	"tool_name", "tool_description", "topic", "parameters", "input_schema",
	"payload_format", "request_expiry_ms", "wait_for_response", "qos", "reply_mode",
}

var connectorRuntimeValueKeys = map[string][]string{
	"sql":         {"tool_name", "tool_description"},
	"mcp":         {"selected_tools", "ssl_config"},
	"document_db": {"tool_name", "tool_description", "connection_string"},
	"graph_db":    {"tool_name", "tool_description", "connection_string"},
	"search":      {"tool_name", "tool_description", "connection_string"},
	"slack":       {"tool_name", "tool_description"},
	"web_search":  {"tool_name", "tool_description"},
}

func usesLegacyEventMeshValues(connType string, values map[string]any) bool {
	_, hasOperations := values["operations"]
	return connType == "event_mesh" && len(values) > 0 && !hasOperations
}

func declaredConnectorFields(connType, subtype string) (fields []connectorFieldDef, known bool) {
	subs, ok := connectorTypeIndex()[connType]
	if !ok {
		return nil, false
	}
	fields, ok = subs[subtype]
	return fields, ok
}

func UndeclaredConnectorValueKeys(connType, subtype string, values any) []string {
	fields, known := declaredConnectorFields(connType, subtype)
	valueMap, isObject := values.(map[string]any)
	if !known || !isObject {
		return nil
	}
	allowed := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		allowed[f.Name] = struct{}{}
	}
	for _, key := range connectorRuntimeValueKeys[connType] {
		allowed[key] = struct{}{}
	}
	if usesLegacyEventMeshValues(connType, valueMap) {
		for _, key := range legacyEventMeshOperationKeys {
			allowed[key] = struct{}{}
		}
	}
	var undeclared []string
	for key := range valueMap {
		if _, ok := allowed[key]; !ok {
			undeclared = append(undeclared, key)
		}
	}
	slices.Sort(undeclared)
	return undeclared
}

func ValidateConnectorValueKeys(connType, subtype string, values any) error {
	undeclared := UndeclaredConnectorValueKeys(connType, subtype, values)
	if len(undeclared) == 0 {
		return nil
	}
	fields, _ := declaredConnectorFields(connType, subtype)
	declared := make([]string, 0, len(fields))
	for _, f := range fields {
		declared = append(declared, f.Name)
	}

	details := make(map[string][]string, len(undeclared))
	described := make([]string, 0, len(undeclared))
	for _, key := range undeclared[:min(len(undeclared), maxReportedKeys)] {
		echoed := truncateKey(key)
		entry := fmt.Sprintf("%q", echoed)
		detail := fmt.Sprintf("the %s/%s connector does not declare %q", connType, subtype, echoed)
		if hint := closestDeclaredKey(key, declared); hint != "" {
			entry += fmt.Sprintf(" (did you mean %q?)", hint)
			detail += fmt.Sprintf("; did you mean %q?", hint)
		}
		described = append(described, entry)
		details["values."+echoed] = append(details["values."+echoed], detail)
	}
	if unreported := len(undeclared) - len(described); unreported > 0 {
		described = append(described, fmt.Sprintf("and %d more", unreported))
	}
	return &ValidationError{
		Message: fmt.Sprintf("values has %d key(s) the %s/%s connector does not declare: %s. Declared keys: %s",
			len(undeclared), connType, subtype, strings.Join(described, ", "), strings.Join(sortedUnique(declared), ", ")),
		Field:   "values",
		Details: details,
	}
}

var runtimeDefaultedFields = map[string][]string{
	"mcp/remote":             {"connection_type", "auth_type", "auth_apikey_location", "auth_oauth_mode"},
	"api/openapi":            {"auth_type", "auth_apikey_location"},
	"email/smtp":             {"smtp_tls", "smtp_auth_type"},
	"document_db/dynamodb":   {"auth_type"},
	"graph_db/neptune":       {"auth_type"},
	"knowledge_base/bedrock": {"auth_type"},
	"search/opensearch":      {"auth_type"},
	"web_search/tavily":      {"api_base"},
	"web_search/exa":         {"api_base"},
}

func withRuntimeDefaults(connType, subtype string, fields []connectorFieldDef, values map[string]any) map[string]any {
	listed := runtimeDefaultedFields[connType+"/"+subtype]
	effective := maps.Clone(values)
	if effective == nil {
		effective = map[string]any{}
	}
	for _, f := range fields {
		if !slices.Contains(listed, f.Name) || f.Default == nil {
			continue
		}
		if s, isString := effective[f.Name].(string); effective[f.Name] == nil || (isString && s == "") {
			effective[f.Name] = f.Default
		}
	}
	return effective
}

func MissingRequiredConnectorFields(connType, subtype string, values any, skipSecrets bool) []string {
	fields, known := declaredConnectorFields(connType, subtype)
	valueMap, isObject := values.(map[string]any)
	if values == nil {
		valueMap, isObject = map[string]any{}, true
	}
	if !known || !isObject {
		return nil
	}
	legacyEventMesh := usesLegacyEventMeshValues(connType, valueMap)
	effective := withRuntimeDefaults(connType, subtype, fields, valueMap)
	var missing []string
	for _, f := range fields {
		if requiredFieldIsMissing(f, effective, skipSecrets, legacyEventMesh) {
			missing = append(missing, f.Name)
		}
	}
	return missing
}

func requiredFieldIsMissing(f connectorFieldDef, valueMap map[string]any, skipSecrets, legacyEventMesh bool) bool {
	if !f.Required || !fieldVisible(f, valueMap) || (legacyEventMesh && f.Name == "operations") {
		return false
	}
	if skipSecrets && (f.Secret || f.Type == "password") {
		return false
	}
	raw := valueMap[f.Name]
	s, isString := raw.(string)
	return raw == nil || (isString && s == "")
}

func truncateKey(key string) string {
	runes := []rune(key)
	if len(runes) <= maxEchoedValueKeyLen {
		return key
	}
	return string(runes[:maxEchoedValueKeyLen]) + "..."
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

func closestDeclaredKey(key string, declared []string) string {
	norm := normalizeValueKey(key)
	candidates := sortedUnique(declared)
	for _, candidate := range candidates {
		if normalizeValueKey(candidate) == norm {
			return candidate
		}
	}
	if len(norm) >= minPrefixHintLen {
		for _, candidate := range candidates {
			if strings.HasPrefix(normalizeValueKey(candidate), norm) {
				return candidate
			}
		}
	}
	if len(norm) < minEditHintLen {
		return ""
	}
	closest, closestDistance := "", maxHintEditDistance+1
	for _, candidate := range candidates {
		if d := editDistance(norm, normalizeValueKey(candidate)); d < closestDistance {
			closest, closestDistance = candidate, d
		}
	}
	return closest
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	previous := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current := make([]int, len(rb)+1)
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(rb)]
}

func normalizeValueKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(key))
}
