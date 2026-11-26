// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const reservedRequiredScopesConfigKey = "required_scopes"

func validateToolsetRequiredScopes(val any, toolNames map[string]struct{}) map[string][]string {
	details := map[string][]string{}
	if val == nil {
		return details
	}
	m, ok := val.(map[string]any)
	if !ok {
		details[reservedRequiredScopesConfigKey] = append(details[reservedRequiredScopesConfigKey],
			fmt.Sprintf("expected object keyed by tool name, got %T", val))
		return details
	}
	for toolName, entryRaw := range m {
		path := reservedRequiredScopesConfigKey + "." + toolName
		if _, known := toolNames[toolName]; !known {
			details[path] = append(details[path], "unknown tool name for this toolset (or the package has no discovered tools yet)")
			continue
		}
		scopes, ok := coerceScopeList(entryRaw)
		if !ok {
			details[path] = append(details[path], fmt.Sprintf("expected a list of scope strings, got %T", entryRaw))
			continue
		}
		if len(scopes) == 0 {
			details[path] = append(details[path], "scope list is empty; omit the tool to leave it ungated")
			continue
		}
		if d, ok := rbac.ValidateScopes(scopes); !ok {
			details[path] = append(details[path], d["scopes"]...)
		}
		if d, ok := rbac.ValidateScopeShape(scopes); !ok {
			details[path] = append(details[path], d["scopes"]...)
		}
		for _, s := range scopes {
			if msg := requiredScopeRuleViolation(s); msg != "" {
				details[path] = append(details[path], msg)
			}
		}
	}
	return details
}

const (
	toolExecutionScopeCategory = "tool"
	collectionScopeSentinel    = "_"
)

func requiredScopeRuleViolation(s string) string {
	segs := strings.Split(s, ":")
	if segs[0] != toolExecutionScopeCategory {
		return fmt.Sprintf("scope %q must be a tool-execution scope (tool:<toolset>__<tool>:<verb>)", s)
	}
	for _, seg := range segs {
		if strings.Contains(seg, "*") || seg == collectionScopeSentinel {
			return fmt.Sprintf("scope %q must not use a wildcard or the `_` sentinel in a required scope; author the concrete tool:<toolset>__<tool>:<verb>", s)
		}
	}
	return ""
}

func coerceScopeList(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, true
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func scopesForTool(agentScopes, tsScopes map[string]any, toolName string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(vals map[string]any) {
		if vals == nil {
			return
		}
		s, ok := coerceScopeList(vals[toolName])
		if !ok {
			return
		}
		for _, sc := range s {
			if _, dup := seen[sc]; dup {
				continue
			}
			seen[sc] = struct{}{}
			out = append(out, sc)
		}
	}
	add(tsScopes)
	add(agentScopes)
	return out
}

func requiredScopeToolKeys(configValuesJSON *string) []string {
	if configValuesJSON == nil || *configValuesJSON == "" {
		return nil
	}
	var vals map[string]any
	if err := json.Unmarshal([]byte(*configValuesJSON), &vals); err != nil {
		return nil
	}
	block, _ := vals[reservedRequiredScopesConfigKey].(map[string]any)
	if len(block) == 0 {
		return nil
	}
	keys := make([]string, 0, len(block))
	for k := range block {
		keys = append(keys, k)
	}
	return keys
}

func requiredScopesFromConfig(configValues map[string]any) []string {
	if configValues == nil {
		return nil
	}
	s, _ := coerceScopeList(configValues[reservedRequiredScopesConfigKey])
	return s
}
