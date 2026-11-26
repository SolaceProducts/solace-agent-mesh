// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type HILEntry struct {
	ToolMap   map[string]any
	ToolNames []string
	Degraded  bool
}

func ParseHILConfigs(log *slog.Logger, entries []HILEntry) (map[string]HILToolConfig, error) {
	if log == nil {
		log = discardLogger
	}
	result := make(map[string]HILToolConfig)

	for _, entry := range entries {
		raw, present := entry.ToolMap["hil"]
		if !present {
			continue
		}

		entryToolName := entryConfigName(entry)

		hilRaw, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("hil tool %q: hil must be a map, got %T", entryToolName, raw)
		}
		entryDefault, err := ParseHILToolConfig(hilRaw, entryToolName)
		if err != nil {
			return nil, err
		}

		perToolOverrides := make(map[string]map[string]any)
		if toolsRaw, present := hilRaw["tools"]; present {
			toolsMap, ok := toolsRaw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("hil tool %q: hil.tools must be a map of tool name to config, got %T", entryToolName, toolsRaw)
			}
			for toolName, overrideRaw := range toolsMap {
				override, ok := overrideRaw.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("hil tool %q: hil.tools[%q] must be a map, got %T", entryToolName, toolName, overrideRaw)
				}
				if entry.Degraded && !slices.Contains(entry.ToolNames, toolName) {
					log.Warn("hil.tools names a tool this entry did not register; the entry did not register its full tool surface this boot, so the tool is NOT gated",
						"configName", entryToolName, "overrideName", toolName, "registeredTools", entry.ToolNames)
				}
				if !entry.Degraded && !slices.Contains(entry.ToolNames, toolName) {
					if !entryNarrowsSurface(entry.ToolMap) {
						return nil, fmt.Errorf("hil tool %q: hil.tools names %q, which this entry does not register (registered: %v)",
							entryToolName, toolName, entry.ToolNames)
					}
					log.Warn("hil.tools names a tool this entry did not register; if that name is a typo the tool is NOT gated",
						"configName", entryToolName, "overrideName", toolName, "registeredTools", entry.ToolNames)
				}
				perToolOverrides[toolName] = override
			}
		}

		for _, name := range entry.ToolNames {
			cfg := entryDefault

			if override, ok := perToolOverrides[name]; ok {
				merged, mergeErr := MergeHILToolConfig(cfg, override, name)
				if mergeErr != nil {
					return nil, mergeErr
				}
				cfg = merged
			}

			if cfg.RequireApproval || len(cfg.ApprovalWhen) > 0 {
				result[name] = cfg
			}
		}
	}

	return result, nil
}

var nameDerivingToolTypes = map[string]bool{"mcp": true, "openapi": true}

func entryNarrowsSurface(toolMap map[string]any) bool {
	if toolType, _ := toolMap["tool_type"].(string); !nameDerivingToolTypes[toolType] {
		return false
	}
	for _, key := range []string{"tool_name", "allow_list", "deny_list"} {
		switch v := toolMap[key].(type) {
		case nil:
		case string:
			if v != "" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func entryConfigName(entry HILEntry) string {
	if len(entry.ToolNames) > 0 {
		return entry.ToolNames[0]
	}
	for _, key := range []string{"tool_name", "group_name", "name"} {
		if v, ok := entry.ToolMap[key].(string); ok && v != "" {
			return v
		}
	}
	if v, ok := entry.ToolMap["tool_type"].(string); ok && v != "" {
		return "<" + v + " entry registering no tools>"
	}
	return "<unnamed tools: entry>"
}

func ParseHILToolConfig(m map[string]any, toolName string) (HILToolConfig, error) {
	cfg := HILToolConfig{
		ShowArgs: true,
	}
	var err error
	if cfg.RequireApproval, err = hilBool(m, "require_approval", false, toolName); err != nil {
		return cfg, err
	}
	if cfg.ShowArgs, err = hilBool(m, "show_args", true, toolName); err != nil {
		return cfg, err
	}
	if cfg.ApprovalMessage, err = hilString(m, "approval_message", toolName); err != nil {
		return cfg, err
	}
	if cfg.Timeout, err = hilDuration(m, "timeout", toolName); err != nil {
		return cfg, err
	}
	if raw, ok := m["require_approval_when"]; ok {
		rules, ruleErr := ParseApprovalRules(raw, toolName)
		if ruleErr != nil {
			return cfg, ruleErr
		}
		cfg.ApprovalWhen = rules
	}
	if raw, ok := m["approvers"]; ok {
		if cfg.Approvers, err = ParseHILApprovers(raw, toolName); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

const (
	maxHILApproverUsers  = 20
	maxHILApproverPlaces = 5
)

func ParseHILApprovers(raw any, toolName string) (HILApprovers, error) {
	var out HILApprovers
	m, ok := raw.(map[string]any)
	if !ok {
		return out, fmt.Errorf("hil tool %q: approvers must be a map, got %T", toolName, raw)
	}
	for key := range m {
		switch key {
		case "users", "places":
		case "groups":
			return out, fmt.Errorf("hil tool %q: approvers.groups is not supported yet; list the users instead", toolName)
		default:
			return out, fmt.Errorf("hil tool %q: approvers.%s is not a known key (users, places)", toolName, key)
		}
	}
	users, err := parseApproverUsers(m["users"], toolName)
	if err != nil {
		return out, err
	}
	places, err := parseApproverPlaces(m["places"], toolName)
	if err != nil {
		return out, err
	}
	if len(users) == 0 {
		return out, fmt.Errorf("hil tool %q: approvers must list at least one user; a place shows the question but only a listed user may answer it", toolName)
	}
	return HILApprovers{Users: users, Places: places}, nil
}

func parseApproverUsers(raw any, toolName string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("hil tool %q: approvers.users must be a list of user ids, got %T", toolName, raw)
	}
	if len(list) > maxHILApproverUsers {
		return nil, fmt.Errorf("hil tool %q: approvers.users lists %d users; at most %d", toolName, len(list), maxHILApproverUsers)
	}
	users := make([]string, 0, len(list))
	for i, item := range list {
		id, ok := item.(string)
		switch {
		case !ok || strings.TrimSpace(id) == "" || id != strings.TrimSpace(id):
			return nil, fmt.Errorf("hil tool %q: approvers.users[%d] must be a non-empty user id without surrounding spaces", toolName, i)
		case strings.HasPrefix(strings.ToLower(id), "system:"):
			return nil, fmt.Errorf("hil tool %q: approvers.users[%d] is a system principal, which cannot approve", toolName, i)
		}
		users = append(users, id)
	}
	return users, nil
}

func parseApproverPlaces(raw any, toolName string) ([]HILPlace, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("hil tool %q: approvers.places must be a list, got %T", toolName, raw)
	}
	if len(list) > maxHILApproverPlaces {
		return nil, fmt.Errorf("hil tool %q: approvers.places lists %d places; at most %d", toolName, len(list), maxHILApproverPlaces)
	}
	places := make([]HILPlace, 0, len(list))
	for i, item := range list {
		pm, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("hil tool %q: approvers.places[%d] must be a map with channel_type and address", toolName, i)
		}
		channelType, _ := pm["channel_type"].(string)
		address, _ := pm["address"].(map[string]any)
		if channelType == "" || len(address) == 0 || len(pm) != 2 {
			return nil, fmt.Errorf("hil tool %q: approvers.places[%d] must have exactly channel_type (a string) and address (a non-empty map)", toolName, i)
		}
		if !a2a.ValidNotifyChannelType(channelType) {
			return nil, fmt.Errorf("hil tool %q: approvers.places[%d].channel_type %q is not a valid channel type (lowercase letters, digits, - and _)", toolName, i, channelType)
		}
		places = append(places, HILPlace{ChannelType: channelType, Address: address})
	}
	return places, nil
}

func hilBool(m map[string]any, key string, def bool, toolName string) (bool, error) {
	raw, present := m[key]
	if !present {
		return def, nil
	}
	v, ok := raw.(bool)
	if !ok {
		return def, fmt.Errorf("hil tool %q: %s must be a bool, got %T", toolName, key, raw)
	}
	return v, nil
}

func hilString(m map[string]any, key, toolName string) (string, error) {
	raw, present := m[key]
	if !present {
		return "", nil
	}
	v, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("hil tool %q: %s must be a string, got %T", toolName, key, raw)
	}
	return v, nil
}

func hilDuration(m map[string]any, key, toolName string) (time.Duration, error) {
	raw, present := m[key]
	if !present {
		return 0, nil
	}
	v, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf("hil tool %q: %s must be a duration string, got %T", toolName, key, raw)
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("hil tool %q: %s %q is not a valid duration: %w", toolName, key, v, err)
	}
	return d, nil
}

func MergeHILToolConfig(base HILToolConfig, override map[string]any, toolName string) (HILToolConfig, error) {
	var err error
	if base.RequireApproval, err = hilBool(override, "require_approval", base.RequireApproval, toolName); err != nil {
		return base, err
	}
	if base.ShowArgs, err = hilBool(override, "show_args", base.ShowArgs, toolName); err != nil {
		return base, err
	}
	if _, ok := override["approval_message"]; ok {
		if base.ApprovalMessage, err = hilString(override, "approval_message", toolName); err != nil {
			return base, err
		}
	}
	if _, ok := override["timeout"]; ok {
		if base.Timeout, err = hilDuration(override, "timeout", toolName); err != nil {
			return base, err
		}
	}
	if raw, ok := override["require_approval_when"]; ok {
		rules, ruleErr := ParseApprovalRules(raw, toolName)
		if ruleErr != nil {
			return base, ruleErr
		}
		base.ApprovalWhen = rules
	}
	if raw, ok := override["approvers"]; ok {
		if base.Approvers, err = ParseHILApprovers(raw, toolName); err != nil {
			return base, err
		}
	}
	return base, nil
}

var approvalOperators = map[string]ApprovalOp{
	"eq":         OpEq,
	"in":         OpIn,
	"not_in":     OpNotIn,
	"exists":     OpExists,
	"not_exists": OpNotExists,
	"gt":         OpGt,
	"lt":         OpLt,
	"gte":        OpGte,
	"lte":        OpLte,
}

func ParseApprovalRules(raw any, toolName string) ([]ApprovalRule, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("hil tool %q: require_approval_when must be a list", toolName)
	}
	rules := make([]ApprovalRule, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("hil tool %q: require_approval_when[%d] must be a map", toolName, i)
		}
		argRaw, ok := m["arg"]
		if !ok {
			return nil, fmt.Errorf("hil tool %q: require_approval_when[%d] missing required field 'arg'", toolName, i)
		}
		arg, ok := argRaw.(string)
		if !ok || arg == "" {
			return nil, fmt.Errorf("hil tool %q: require_approval_when[%d] field 'arg' must be a non-empty string", toolName, i)
		}

		var foundOps []string
		for key := range m {
			if key == "arg" {
				continue
			}
			if _, isOp := approvalOperators[key]; isOp {
				foundOps = append(foundOps, key)
				continue
			}
			return nil, fmt.Errorf("hil tool %q: require_approval_when[%d] unknown operator %q (valid: eq, in, not_in, exists, not_exists, gt, lt, gte, lte)", toolName, i, key)
		}
		if len(foundOps) == 0 {
			return nil, fmt.Errorf("hil tool %q: require_approval_when[%d] missing operator (one of: eq, in, not_in, exists, not_exists, gt, lt, gte, lte)", toolName, i)
		}
		if len(foundOps) > 1 {
			slices.Sort(foundOps)
			return nil, fmt.Errorf("hil tool %q: require_approval_when[%d] specifies multiple operators %v — only one operator per rule is supported", toolName, i, foundOps)
		}

		opKey := foundOps[0]
		op := approvalOperators[opKey]
		val := m[opKey]
		if err := validateRuleValue(op, val); err != nil {
			return nil, fmt.Errorf("hil tool %q: require_approval_when[%d] %s: %w", toolName, i, opKey, err)
		}

		rules = append(rules, ApprovalRule{Arg: arg, Op: op, Value: val})
	}
	return rules, nil
}

func validateRuleValue(op ApprovalOp, val any) error {
	switch op {
	case OpExists, OpNotExists:
		return nil
	case OpIn, OpNotIn:
		list, ok := val.([]any)
		if !ok {
			return fmt.Errorf("value must be a list")
		}
		if len(list) == 0 {
			return fmt.Errorf("list must be non-empty")
		}
		for j, item := range list {
			if !isScalar(item) {
				return fmt.Errorf("list element [%d] must be a string, number, or bool", j)
			}
		}
		return nil
	case OpGt, OpLt, OpGte, OpLte:
		if !isNumeric(val) {
			return fmt.Errorf("value must be a number")
		}
		return nil
	case OpEq:
		if !isScalar(val) {
			return fmt.Errorf("value must be a string, number, or bool")
		}
		return nil
	}
	return fmt.Errorf("internal: unhandled operator %q", op)
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, bool,
		float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func isNumeric(v any) bool {
	switch v.(type) {
	case float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}
