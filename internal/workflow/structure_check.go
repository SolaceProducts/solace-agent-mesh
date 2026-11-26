// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"slices"
	"strings"
)

func StructureProblems(appConfig map[string]any) []string {
	body, _, err := loadableWorkflowBody(appConfig)
	if err != nil {
		return nil
	}
	ordered, duplicates := declaredNodes(body)

	problems := make([]string, 0, len(duplicates))
	for _, d := range duplicates {
		problems = append(problems, fmt.Sprintf("node id %q is defined %d times; node ids must be unique", d.id, d.count))
	}

	byID := make(map[string]*NodeConfig, len(ordered))
	for _, n := range ordered {
		byID[n.ID] = n
	}
	addEscalationRerouteWaits(byID)

	for _, n := range ordered {
		if slices.Contains(n.waitsFor(), n.ID) {
			problems = append(problems, fmt.Sprintf("node %q depends on itself", n.ID))
		}
	}
	for _, cycle := range dependencyCycles(ordered, byID) {
		problems = append(problems, fmt.Sprintf("dependency cycle among nodes %s", quoteAll(cycle)))
	}
	return problems
}

type duplicateID struct {
	id    string
	count int
}

func declaredNodes(body map[string]any) (ordered []*NodeConfig, duplicates []duplicateID) {
	rawNodes, _ := WithConnectorNodePlaceholders(body)["nodes"].([]any)
	counts := make(map[string]int, len(rawNodes))
	for _, raw := range rawNodes {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := getString(m, "id")
		if id == "" {
			continue
		}
		counts[id]++
		if counts[id] > 1 {
			continue
		}
		node, parseErr := parseNodeConfig(m, DefaultNodeTimeout)
		if parseErr != nil {
			node = &NodeConfig{ID: id, DependsOn: getStringSlice(m, "depends_on", "dependencies")}
		}
		ordered = append(ordered, node)
	}
	for _, n := range ordered {
		if counts[n.ID] > 1 {
			duplicates = append(duplicates, duplicateID{id: n.ID, count: counts[n.ID]})
		}
	}
	return ordered, duplicates
}

func dependencyCycles(ordered []*NodeConfig, byID map[string]*NodeConfig) [][]string {
	position := make(map[string]int, len(ordered))
	for i, n := range ordered {
		position[n.ID] = i
	}

	index := make(map[string]int, len(ordered))
	lowLink := make(map[string]int, len(ordered))
	onStack := make(map[string]bool, len(ordered))
	var stack []string
	next := 0
	var cycles [][]string

	var visit func(id string)
	visit = func(id string) {
		index[id], lowLink[id] = next, next
		next++
		stack = append(stack, id)
		onStack[id] = true

		for _, dep := range byID[id].waitsFor() {
			if _, defined := byID[dep]; !defined {
				continue
			}
			if seen, ok := index[dep]; !ok {
				visit(dep)
				lowLink[id] = min(lowLink[id], lowLink[dep])
			} else if onStack[dep] {
				lowLink[id] = min(lowLink[id], seen)
			}
		}

		if lowLink[id] != index[id] {
			return
		}
		var component []string
		for {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[top] = false
			component = append(component, top)
			if top == id {
				break
			}
		}
		if len(component) > 1 {
			slices.SortFunc(component, func(a, b string) int { return position[a] - position[b] })
			cycles = append(cycles, component)
		}
	}

	for _, n := range ordered {
		if _, ok := index[n.ID]; !ok {
			visit(n.ID)
		}
	}
	slices.SortFunc(cycles, func(a, b []string) int { return position[a[0]] - position[b[0]] })
	return cycles
}

func quoteAll(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
