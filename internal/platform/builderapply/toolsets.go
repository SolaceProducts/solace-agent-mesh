// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/toolbind"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type toolsetCatalog struct {
	toolNamesByToolsetID map[string][]string
	toolsetIDsByToolName map[string][]string
}

func newToolsetCatalog() toolsetCatalog {
	c := toolsetCatalog{
		toolNamesByToolsetID: map[string][]string{},
		toolsetIDsByToolName: map[string][]string{},
	}
	for group := range schema.ValidBuiltinGroups {
		id := schema.CanonicalBuiltinGroup(group)
		if _, done := c.toolNamesByToolsetID[id]; done {
			continue
		}
		tools := toolinit.LookupBuiltinGroup(group, toolinit.Options{})
		if len(tools) == 0 {
			continue
		}
		c.toolNamesByToolsetID[id] = tools
	}
	for id, tools := range c.toolNamesByToolsetID {
		for _, tool := range tools {
			c.toolsetIDsByToolName[tool] = append(c.toolsetIDsByToolName[tool], id)
		}
	}
	for tool := range c.toolsetIDsByToolName {
		slices.Sort(c.toolsetIDsByToolName[tool])
	}
	return c
}

func toolOwners(toolsets []string, excluded map[string][]string, cat toolsetCatalog) map[string]string {
	owner := map[string]string{}
	for _, id := range slices.Sorted(slices.Values(toolsets)) {
		for _, tool := range cat.toolNamesByToolsetID[id] {
			if _, taken := owner[tool]; !taken && !slices.Contains(excluded[id], tool) {
				owner[tool] = id
			}
		}
	}
	return owner
}

func resolveToolsetBindings(groupNames, toolNames []string, cat toolsetCatalog) ([]string, map[string][]string, error) {
	grantedAsGroup := map[string]struct{}{}
	toolsets := make([]string, 0, len(groupNames))
	for _, name := range groupNames {
		id := schema.CanonicalBuiltinGroup(name)
		if _, dup := grantedAsGroup[id]; dup {
			continue
		}
		grantedAsGroup[id] = struct{}{}
		toolsets = append(toolsets, id)
	}

	var unresolved []string
	stillNeeded := map[string]struct{}{}
	for _, tool := range toolNames {
		owners := cat.toolsetIDsByToolName[tool]
		if len(owners) == 0 {
			unresolved = append(unresolved, tool)
			continue
		}
		if slices.ContainsFunc(owners, func(id string) bool {
			_, ok := grantedAsGroup[id]
			return ok
		}) {
			continue
		}
		stillNeeded[tool] = struct{}{}
	}

	if len(unresolved) > 0 {
		slices.Sort(unresolved)
		quoted := `"` + strings.Join(unresolved, `", "`) + `"`
		return nil, nil, fmt.Errorf(
			"cannot bind tool(s) %s to this agent: no built-in toolset on this deployment provides them. %s",
			quoted, toolbind.Explain(unresolved))
	}

	excluded := map[string][]string{}
	for _, a := range assignToolsToToolsets(stillNeeded, cat) {
		toolsets = append(toolsets, a.toolsetID)
		var drop []string
		for _, member := range cat.toolNamesByToolsetID[a.toolsetID] {
			if !slices.Contains(a.covered, member) {
				drop = append(drop, member)
			}
		}
		if len(drop) > 0 {
			excluded[a.toolsetID] = drop
		}
	}
	if len(excluded) == 0 {
		excluded = nil
	}
	slices.Sort(toolsets)
	return toolsets, excluded, nil
}

type toolsetAssignment struct {
	toolsetID string
	covered   []string
}

func assignToolsToToolsets(needed map[string]struct{}, cat toolsetCatalog) []toolsetAssignment {
	remaining := make(map[string]struct{}, len(needed))
	for tool := range needed {
		remaining[tool] = struct{}{}
	}

	var out []toolsetAssignment
	for len(remaining) > 0 {
		best := bestCoveringToolset(remaining, cat)
		out = append(out, best)
		for _, tool := range best.covered {
			delete(remaining, tool)
		}
	}
	slices.SortFunc(out, func(a, b toolsetAssignment) int {
		return strings.Compare(a.toolsetID, b.toolsetID)
	})
	return out
}

func bestCoveringToolset(remaining map[string]struct{}, cat toolsetCatalog) toolsetAssignment {
	candidates := map[string]struct{}{}
	for tool := range remaining {
		for _, id := range cat.toolsetIDsByToolName[tool] {
			candidates[id] = struct{}{}
		}
	}

	type ranked struct {
		id      string
		members int
		covered []string
	}
	scored := make([]ranked, 0, len(candidates))
	for id := range candidates {
		members := cat.toolNamesByToolsetID[id]
		var covered []string
		for _, m := range members {
			if _, ok := remaining[m]; ok {
				covered = append(covered, m)
			}
		}
		scored = append(scored, ranked{id: id, members: len(members), covered: covered})
	}
	slices.SortFunc(scored, func(a, b ranked) int {
		return cmp.Or(
			cmp.Compare(len(b.covered), len(a.covered)),
			cmp.Compare(a.members, b.members),
			strings.Compare(a.id, b.id),
		)
	})
	return toolsetAssignment{toolsetID: scored[0].id, covered: scored[0].covered}
}
