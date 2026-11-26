// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

const (
	describeMaxEditDistance = 3
	describeMinSharedPrefix = 4
	describeCandidateRows   = 100
)

type suggestionPool struct {
	loaded bool
	names  []string
}

func closeNames(query string, candidates []string) []string {
	type scored struct {
		name     string
		distance int
	}
	lowered := strings.ToLower(query)
	var nearby []scored
	for _, name := range slices.Compact(slices.Sorted(slices.Values(candidates))) {
		candidate := strings.ToLower(name)
		distance := describeMaxEditDistance + 1
		if abs(utf8.RuneCountInString(lowered)-utf8.RuneCountInString(candidate)) <= describeMaxEditDistance {
			distance = levenshtein(lowered, candidate)
		}
		if distance <= describeMaxEditDistance || commonPrefixLen(lowered, candidate) >= describeMinSharedPrefix {
			nearby = append(nearby, scored{name: name, distance: distance})
		}
	}
	slices.SortFunc(nearby, func(a, b scored) int {
		return cmp.Or(cmp.Compare(a.distance, b.distance), strings.Compare(a.name, b.name))
	})
	names := make([]string, 0, describeMaxSuggestions)
	for _, c := range nearby[:min(len(nearby), describeMaxSuggestions)] {
		names = append(names, c.name)
	}
	return names
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func levenshtein(a, b string) int {
	left, right := []rune(a), []rune(b)
	previous := make([]int, len(right)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, l := range left {
		current := make([]int, len(right)+1)
		current[0] = i + 1
		for j, r := range right {
			substitution := previous[j]
			if l != r {
				substitution++
			}
			current[j+1] = min(previous[j+1]+1, current[j]+1, substitution)
		}
		previous = current
	}
	return previous[len(right)]
}

func commonPrefixLen(a, b string) int {
	left, right := []rune(a), []rune(b)
	n := 0
	for n < len(left) && n < len(right) && left[n] == right[n] {
		n++
	}
	return n
}

func (d *ToolDescriber) toolNameSuggestions(ctx context.Context, query string) ([]string, error) {
	tools, err := d.toolCandidates(ctx)
	if err != nil {
		return nil, err
	}
	connectors, err := d.connectorCandidates(ctx)
	if err != nil {
		return nil, err
	}
	return closeNames(query, slices.Concat(tools, connectors)), nil
}

func (d *ToolDescriber) connectorNameSuggestions(ctx context.Context, query string) ([]string, error) {
	connectors, err := d.connectorCandidates(ctx)
	if err != nil {
		return nil, err
	}
	return closeNames(query, connectors), nil
}

func (d *ToolDescriber) toolCandidates(ctx context.Context) ([]string, error) {
	if !d.access.tools {
		return nil, nil
	}
	if d.toolPool.loaded {
		return d.toolPool.names, nil
	}
	names := slices.DeleteFunc(toolinit.BuiltinToolNames(), func(name string) bool { return !toolinit.AgentBindableBuiltin(name) })
	names = slices.Concat(names, toolinit.STRBuiltinNames())
	packageTools, err := d.readyPackageToolNames(ctx)
	if err != nil {
		return nil, err
	}
	d.toolPool = suggestionPool{loaded: true, names: slices.Concat(names, packageTools)}
	return d.toolPool.names, nil
}

func (d *ToolDescriber) readyPackageToolNames(ctx context.Context) ([]string, error) {
	if d.packages == nil || !d.access.toolsets {
		return nil, nil
	}
	ready := "ready"
	packages, _, err := d.packages.ListWithOffset(ctx, 0, describeCandidateRows, &ready)
	if err != nil {
		return nil, fmt.Errorf("describe tools: list packages: %w", err)
	}
	ids := make([]string, len(packages))
	byID := make(map[string]string, len(packages))
	for i, pkg := range packages {
		ids[i], byID[pkg.ID] = pkg.ID, pkg.Name
	}
	filesByPackage, err := d.packages.GetFilesForPackages(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("describe tools: list package tools: %w", err)
	}
	var names []string
	for id, files := range filesByPackage {
		for _, file := range files {
			names = append(names, byID[id]+store.PackageToolSeparator+file.ToolName)
		}
	}
	return names, nil
}

func (d *ToolDescriber) connectorCandidates(ctx context.Context) ([]string, error) {
	if !d.access.connectors || d.connectors == nil {
		return nil, nil
	}
	if d.connectorPool.loaded {
		return d.connectorPool.names, nil
	}
	rows, _, err := d.connectors.ListPaginated(ctx, 1, describeCandidateRows, store.ConnectorFilter{ExcludeTypes: hiddenConnectorTypes(d.flags)}, store.ConnectorOrder{})
	if err != nil {
		return nil, fmt.Errorf("describe tools: list connectors: %w", err)
	}
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}
	d.connectorPool = suggestionPool{loaded: true, names: names}
	return names, nil
}

func (d *ToolDescriber) connectorNamesOfType(ctx context.Context, connType string) ([]string, error) {
	if !d.access.connectors || d.connectors == nil {
		return nil, nil
	}
	rows, _, err := d.connectors.ListPaginated(ctx, 1, describeMaxSuggestions, store.ConnectorFilter{Type: connType}, store.ConnectorOrder{})
	if err != nil {
		return nil, fmt.Errorf("describe tools: list connectors: %w", err)
	}
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}
	return names, nil
}
