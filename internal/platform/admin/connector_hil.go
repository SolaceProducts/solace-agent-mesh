// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

const defaultEventMeshRole = "event_mesh_request"

const hilField = "values.hil"

var connectorHILRoles = map[string]map[string][]string{
	"email":          {"": {"send_email"}},
	"slack":          {"": {"slack_send_message", "slack_update"}},
	"sql":            {"": {"sql_query"}},
	"knowledge_base": {"": {"query_bedrock_kb"}},
	"document_db":    {"mongodb": {"mongo_query"}, "dynamodb": {"dynamodb_query"}},
	"graph_db":       {"neo4j": {"neo4j_query"}, "neptune": {"neptune_query"}},
	"search":         {"elasticsearch": {"elasticsearch_query"}, "opensearch": {"opensearch_query"}},
	"web_search":     {"tavily": {"web_search"}, "exa": {"web_search"}},
}

func connectorHILRoleNames(connType, subtype string, values map[string]any) []string {
	if connType == "event_mesh" {
		return eventMeshHILRoles(values)
	}
	bySubtype := connectorHILRoles[connType]
	if roles, ok := bySubtype[strings.ToLower(subtype)]; ok {
		return roles
	}
	return bySubtype[""]
}

func eventMeshHILRoles(values map[string]any) []string {
	operations, err := eventMeshOperations(values)
	if err != nil || len(operations) == 0 {
		return []string{eventMeshRole(values)}
	}
	roles := make([]string, 0, len(operations))
	for _, operation := range operations {
		roles = append(roles, eventMeshRole(operation))
	}
	return roles
}

func eventMeshRole(values map[string]any) string {
	name, _ := values["tool_name"].(string)
	return cmp.Or(name, defaultEventMeshRole)
}

func validateConnectorHIL(connType, subtype string, values map[string]any) error {
	if err := validateConnectorHILShape(values["hil"]); err != nil {
		return err
	}
	switch connType {
	case "api":
		return validateAPIHILToolNames(values["hil"])
	case "mcp":
		return nil
	}
	roles := connectorHILRoleNames(connType, subtype, values)
	if roles == nil {
		return refuseGatesOnUnlistedType(connType, subtype, values["hil"])
	}
	return validateHILToolKeys(values["hil"], roles)
}

func refuseGatesOnUnlistedType(connType, subtype string, rawHIL any) error {
	if ConnectorHILIsEmpty(rawHIL) {
		return nil
	}
	return &ValidationError{
		Field:   hilField,
		Message: fmt.Sprintf("approval gates are not supported on a %s connector", strings.Trim(connType+"/"+subtype, "/")),
	}
}

func ConnectorHILToolKeys(raw any) []string {
	parsed, _ := parseConnectorHIL(raw)
	tools, _ := parsed["tools"].(map[string]any)
	return slices.Sorted(maps.Keys(tools))
}

func ValidateAuthoredConnectorHIL(ctx context.Context, connType, subtype string, values any, spec []byte) error {
	authored, ok := values.(map[string]any)
	if !ok {
		return nil
	}
	if connType == "api" {
		if spec == nil {
			return nil
		}
		known, err := openapi.EnumerateToolNames(ctx, spec, nil, nil)
		if err != nil {
			return nil
		}
		return validateHILToolKeys(authored["hil"], known)
	}
	roles := connectorHILRoleNames(connType, subtype, authored)
	if roles == nil {
		return nil
	}
	return validateHILToolKeys(authored["hil"], roles)
}

func validateAPIHILToolNames(raw any) error {
	for _, key := range ConnectorHILToolKeys(raw) {
		if message := apiHILToolNameProblem(key); message != "" {
			return &ValidationError{Field: hilField, Message: message}
		}
	}
	return nil
}

func apiHILToolNameProblem(key string) string {
	registered := openapi.OperationToolName(key)
	switch {
	case key == "":
		return "hil.tools keys must be the snake_case operation tool name; an empty key names no operation"
	case registered == key:
		return ""
	case registered == "":
		return fmt.Sprintf("hil.tools.%s must be the snake_case operation tool name", key)
	default:
		return fmt.Sprintf("hil.tools.%s must be the snake_case operation tool name; use %s", key, registered)
	}
}

const maxListedHILToolNames = 20

func validateHILToolKeys(raw any, known []string) error {
	parsed, _ := parseConnectorHIL(raw)
	tools, _ := parsed["tools"].(map[string]any)
	for _, name := range slices.Sorted(maps.Keys(tools)) {
		if !slices.Contains(known, name) {
			return &ValidationError{
				Field:   hilField,
				Message: fmt.Sprintf("hil.tools.%s is not a tool this connector provides; use one of: %s", name, listToolNames(known)),
			}
		}
	}
	return nil
}

func listToolNames(names []string) string {
	if len(names) <= maxListedHILToolNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, ...and %d more", strings.Join(names[:maxListedHILToolNames], ", "), len(names)-maxListedHILToolNames)
}

func withConnectorHIL(entry, values map[string]any, role string) map[string]any {
	if hil := connectorRoleHIL(values["hil"], role); entry != nil && hil != nil {
		entry["hil"] = hil
	}
	return entry
}

func connectorRoleHIL(raw any, role string) map[string]any {
	parsed := normalizeConnectorHIL(raw)
	hil := make(map[string]any, len(parsed))
	for key, value := range parsed {
		if key != "tools" {
			hil[key] = value
		}
	}
	if tools, ok := parsed["tools"].(map[string]any); ok {
		if override, ok := tools[role].(map[string]any); ok {
			maps.Copy(hil, override)
		}
	}
	if len(hil) == 0 {
		return nil
	}
	return hil
}

func hilAuthored(requested, stored any) bool {
	if requested == nil {
		return false
	}
	return !reflect.DeepEqual(normalizeConnectorHIL(requested), normalizeConnectorHIL(stored))
}

func pruneUnprovidedRoleGates(connType, subtype string, values map[string]any) []string {
	if connType == "api" {
		return nil
	}
	known := connectorHILRoleNames(connType, subtype, values)
	if known == nil {
		return nil
	}
	return pruneHILToolKeys(values, known)
}

func pruneHILToolKeys(values map[string]any, known []string) []string {
	parsed, _ := parseConnectorHIL(values["hil"])
	tools, _ := parsed["tools"].(map[string]any)
	var removed []string
	for _, name := range slices.Sorted(maps.Keys(tools)) {
		if !slices.Contains(known, name) {
			removed = append(removed, name)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	removeHILToolKeys(values, removed)
	return removed
}

func removeHILToolKeys(values map[string]any, names []string) {
	parsed, _ := parseConnectorHIL(values["hil"])
	tools, _ := parsed["tools"].(map[string]any)
	kept := maps.Clone(tools)
	for _, name := range names {
		delete(kept, name)
	}
	pruned := maps.Clone(parsed)
	if len(kept) == 0 {
		delete(pruned, "tools")
	} else {
		pruned["tools"] = kept
	}
	values["hil"] = pruned
	dropEmptyHIL(values)
}

func dropEmptyHIL(values map[string]any) {
	if raw, present := values["hil"]; present && ConnectorHILIsEmpty(raw) {
		delete(values, "hil")
	}
}

func ConnectorHILIsEmpty(raw any) bool {
	parsed, err := parseConnectorHIL(raw)
	if err != nil {
		return false
	}
	if tools, ok := parsed["tools"].(map[string]any); ok && len(tools) == 0 {
		return len(parsed) == 1
	}
	return len(parsed) == 0
}

const (
	removedGateWarningPrefix = "approval gate on "
	removedGateWarningSuffix = "the connector no longer provides that tool"
)

func removedGateWarnings(removed []string) []string {
	if len(removed) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(removed))
	for _, name := range removed {
		warnings = append(warnings, fmt.Sprintf("%s%q was removed: %s", removedGateWarningPrefix, name, removedGateWarningSuffix))
	}
	return warnings
}

func ConnectorHILSurvivesRemovedGates(rawHIL any, warnings []string) bool {
	values := map[string]any{"hil": rawHIL}
	var names []string
	for _, warning := range warnings {
		if name, ok := removedGateName(warning); ok {
			names = append(names, name)
		}
	}
	removeHILToolKeys(values, names)
	_, survives := values["hil"]
	return survives
}

func removedGateName(warning string) (string, bool) {
	if !IsRemovedGateWarning(warning) {
		return "", false
	}
	quoted, err := strconv.QuotedPrefix(strings.TrimPrefix(warning, removedGateWarningPrefix))
	if err != nil {
		return "", false
	}
	name, err := strconv.Unquote(quoted)
	return name, err == nil
}

func IsRemovedGateWarning(warning string) bool {
	return strings.HasPrefix(warning, removedGateWarningPrefix) && strings.HasSuffix(warning, removedGateWarningSuffix)
}

const (
	uncheckedGateConsequence = "so a gate on an operation the specification lacks stops the agents bound to this connector from starting, " +
		"or is not applied when an agent selects operations with an allow_list"
	uncheckedAPIHILReason    = "the connector has no uploaded specification and none could be read from its specification URL, " + uncheckedGateConsequence
	unparseableSpecHILReason = "the uploaded specification could not be parsed, " + uncheckedGateConsequence
	unreadableSpecHILReason  = "the uploaded specification could not be read, " + uncheckedGateConsequence
	missingFromURLSpecEnding = "name no operation in the specification at the connector's specification URL, " +
		"so agents bound to this connector that do not narrow operations with an allow_list fail to start until the key is removed or corrected"
)

var uncheckedGatesWarningReasons = []string{uncheckedAPIHILReason, unparseableSpecHILReason, unreadableSpecHILReason, missingFromURLSpecEnding}

func IsUncheckedGatesWarning(warning string) bool {
	return slices.ContainsFunc(uncheckedGatesWarningReasons, func(reason string) bool { return strings.HasSuffix(warning, reason) })
}

func uncheckedHILWarnings(values map[string]any, reason string) []string {
	keys := ConnectorHILToolKeys(values["hil"])
	if len(keys) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("approval gates on %s could not be checked: %s", strings.Join(quoteAll(keys), ", "), reason)}
}

func missingFromURLSpecWarnings(values map[string]any, spec URLSpec) []string {
	var missing []string
	for _, key := range ConnectorHILToolKeys(values["hil"]) {
		if !slices.Contains(spec.Tools, key) {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("approval gates on %s %s", strings.Join(quoteAll(missing), ", "), missingFromURLSpecEnding)}
}

func quoteAll(names []string) []string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return quoted
}
