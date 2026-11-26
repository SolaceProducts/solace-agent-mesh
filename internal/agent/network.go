// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sandbox/hostmatch"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func (a *Agent) ValidateNetworkConfig() error {
	if err := a.validateNetworkPolicies(); err != nil {
		return err
	}
	return a.validateNetworkBindings()
}

func (a *Agent) validateNetworkPolicies() error {
	if len(a.cfg.NetworkPolicies) == 0 && len(a.cfg.ToolNetworkBindings) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(a.cfg.NetworkPolicies))
	for i := range a.cfg.NetworkPolicies {
		policy := &a.cfg.NetworkPolicies[i]
		if policy.Name == "" {
			return fmt.Errorf("network policy at index %d has empty name", i)
		}
		if _, dup := seen[policy.Name]; dup {
			return fmt.Errorf("network policy %q declared more than once", policy.Name)
		}
		seen[policy.Name] = struct{}{}

		if len(policy.Allowlist) == 0 {
			return fmt.Errorf(
				"network policy %q has empty allowlist; "+
					"either populate it or remove the tool's network_binding so the tool runs without network access",
				policy.Name)
		}
		for j, entry := range policy.Allowlist {
			if err := validateAllowlistEntry(entry); err != nil {
				return fmt.Errorf("network policy %q allowlist entry %d (%q): %w", policy.Name, j, entry, err)
			}
		}
	}
	return nil
}

func (a *Agent) validateNetworkBindings() error {
	if len(a.cfg.ToolNetworkBindings) == 0 {
		return nil
	}
	policiesByName := make(map[string]struct{}, len(a.cfg.NetworkPolicies))
	for i := range a.cfg.NetworkPolicies {
		policiesByName[a.cfg.NetworkPolicies[i].Name] = struct{}{}
	}
	for toolName, bindings := range a.cfg.ToolNetworkBindings {
		for paramName, policyName := range bindings {
			if _, ok := policiesByName[policyName]; !ok {
				return fmt.Errorf(
					"tool %q network_binding %q references unknown network policy %q "+
						"(declare it under the agent's networks: section)",
					toolName, paramName, policyName)
			}
		}
	}
	return nil
}

func validateAllowlistEntry(entry string) error {
	trimmed := strings.TrimSpace(entry)
	if trimmed == "" {
		return fmt.Errorf("empty entry")
	}
	if trimmed != entry {
		return fmt.Errorf("leading or trailing whitespace; use %q", trimmed)
	}
	if strings.Contains(entry, "/") {
		return fmt.Errorf("contains '/'; allowlist entries are host globs, not URLs")
	}
	if strings.Contains(entry, "://") {
		return fmt.Errorf("contains scheme (://); allowlist entries are host globs only")
	}
	if strings.Contains(entry, ":") {
		return fmt.Errorf("contains ':' (port?); ports are not part of v1 allowlists")
	}
	if idx := strings.Index(entry, "*"); idx >= 0 {
		if idx != 0 || !strings.HasPrefix(entry, "*.") {
			return fmt.Errorf("wildcards are only allowed as a leading '*.' segment (e.g. '*.example.com')")
		}
		if strings.Count(entry, "*") > 1 {
			return fmt.Errorf("more than one wildcard")
		}
	}
	return nil
}

func (a *Agent) resolveNetworkBindings(toolName string) ([]tool.NetworkBinding, error) {
	bindings, ok := a.cfg.ToolNetworkBindings[toolName]
	if !ok || len(bindings) == 0 {
		return nil, nil
	}
	policiesByName := make(map[string]*NetworkPolicy, len(a.cfg.NetworkPolicies))
	for i := range a.cfg.NetworkPolicies {
		policiesByName[a.cfg.NetworkPolicies[i].Name] = &a.cfg.NetworkPolicies[i]
	}

	resolved := make([]tool.NetworkBinding, 0, len(bindings))
	for paramName, policyName := range bindings {
		policy, ok := policiesByName[policyName]
		if !ok {
			return nil, fmt.Errorf(
				"resolveNetworkBindings: tool %q binding %q references policy %q which is not declared "+
					"(ValidateNetworkConfig should have caught this at startup)",
				toolName, paramName, policyName)
		}
		allowlist := make([]string, len(policy.Allowlist))
		copy(allowlist, policy.Allowlist)
		resolved = append(resolved, tool.NetworkBinding{
			ParamName:  paramName,
			PolicyName: policyName,
			Allowlist:  allowlist,
		})
	}
	slices.SortFunc(resolved, func(a, b tool.NetworkBinding) int {
		return cmp.Compare(a.ParamName, b.ParamName)
	})
	return resolved, nil
}

func MatchAllowlist(allowlist []string, host string) bool {
	return hostmatch.Match(host, allowlist)
}
