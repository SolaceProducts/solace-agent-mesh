// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func ExpectedPathFromEval(path *evalplatform.ExpectedPath) *apiv2.ExpectedPath {
	if path == nil {
		return nil
	}
	out := &apiv2.ExpectedPath{Nodes: make([]apiv2.ExpectedNode, 0, len(path.Nodes)), Tools: make([]apiv2.ExpectedTool, 0, len(path.Tools))}
	for _, id := range slices.Sorted(maps.Keys(path.Nodes)) {
		out.Nodes = append(out.Nodes, apiv2.ExpectedNode{NodeID: id, Expectation: path.Nodes[id]})
	}
	for _, name := range slices.Sorted(maps.Keys(path.Tools)) {
		out.Tools = append(out.Tools, apiv2.ExpectedTool{ToolName: name, Expectation: path.Tools[name]})
	}
	return out
}

func EvalExpectedPath(path *apiv2.ExpectedPath) *evalplatform.ExpectedPath {
	if path == nil {
		return nil
	}
	out := &evalplatform.ExpectedPath{}
	if len(path.Nodes) > 0 {
		out.Nodes = make(map[string]string, len(path.Nodes))
		for _, n := range path.Nodes {
			out.Nodes[n.NodeID] = n.Expectation
		}
	}
	if len(path.Tools) > 0 {
		out.Tools = make(map[string]string, len(path.Tools))
		for _, t := range path.Tools {
			out.Tools[t.ToolName] = t.Expectation
		}
	}
	return out
}

func ExpectedFactsFromEval(facts *evalplatform.ExpectedFacts) *apiv2.ExpectedFacts {
	if facts == nil {
		return nil
	}
	return &apiv2.ExpectedFacts{Required: nonNil(facts.Required), Forbidden: nonNil(facts.Forbidden)}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func EvalExpectedFacts(facts *apiv2.ExpectedFacts) *evalplatform.ExpectedFacts {
	if facts == nil {
		return nil
	}
	return &evalplatform.ExpectedFacts{Required: facts.Required, Forbidden: facts.Forbidden}
}
