// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"cmp"
	"fmt"
	"slices"
)

const (
	whyExitHandler   = "it is an on_exit handler, and a handler that fails is logged without changing how the run ends"
	whyWhenFalse     = "its when: is the constant false, so it never runs"
	whyUpstreamDead  = "everything it waits for either never runs or cannot select it, so it never runs"
	whyParentDead    = "the node that runs it never runs"
	whyEmptyMapItems = "the map that runs it iterates a literal empty list, so it never runs"
)

type RunStopAnalysis struct {
	harmless map[string]string
}

func AnalyzeRunStops(body map[string]any) *RunStopAnalysis {
	nodes, duplicates := declaredNodes(body)
	var onExit *ExitHandler
	if raw := getAny(body, "on_exit", "onExit"); raw != nil {
		if handler, err := parseExitHandler(raw); err == nil {
			onExit = handler
		}
	}
	analysis := analyzeParsedRunStops(onExit, nodes)
	for _, d := range duplicates {
		delete(analysis.harmless, d.id)
	}
	return analysis
}

func (a *RunStopAnalysis) cannotStopBecause(nodeID string) (why string, ok bool) {
	if a == nil {
		return "", false
	}
	why = a.harmless[nodeID]
	return why, why != ""
}

func (a *RunStopAnalysis) FailingNodeProblem(nodeID, toolName, message string) StaticProblem {
	why, harmless := a.cannotStopBecause(nodeID)
	if !harmless {
		return StaticProblem{NodeID: nodeID, ToolName: toolName, Message: message, Fatal: true}
	}
	return StaticProblem{
		NodeID:          nodeID,
		ToolName:        toolName,
		Message:         fmt.Sprintf("%s; this does not refuse the workflow, because %s", message, why),
		NotFatalBecause: why,
	}
}

func analyzeParsedRunStops(onExit *ExitHandler, nodes []*NodeConfig) *RunStopAnalysis {
	g := newRunStopGraph(onExit, nodes)
	analysis := &RunStopAnalysis{harmless: make(map[string]string, len(g.byID))}
	for id := range g.byID {
		if why, harmless := g.cannotStop(id); harmless {
			analysis.harmless[id] = why
		}
	}
	return analysis
}

type runStopGraph struct {
	byID      map[string]*NodeConfig
	exit      map[string]bool
	dispatch  map[string][]*NodeConfig
	selection map[string]switchSelection
	neverRuns map[string]string
}

type switchSelection struct {
	neverSelected map[string]bool
}

func newRunStopGraph(onExit *ExitHandler, nodes []*NodeConfig) *runStopGraph {
	g := &runStopGraph{
		byID:      make(map[string]*NodeConfig, len(nodes)),
		exit:      make(map[string]bool),
		dispatch:  make(map[string][]*NodeConfig),
		selection: make(map[string]switchSelection),
		neverRuns: make(map[string]string),
	}
	var order []*NodeConfig
	for _, n := range nodes {
		if n == nil || n.ID == "" {
			continue
		}
		if _, dup := g.byID[n.ID]; dup {
			continue
		}
		own := *n
		own.implicitWaits = slices.Clone(n.implicitWaits)
		g.byID[n.ID] = &own
		order = append(order, &own)
	}
	addEscalationRerouteWaits(g.byID)
	for _, id := range exitHandlerNodeIDs(onExit) {
		g.exit[id] = true
	}
	for _, n := range order {
		switch n.Type {
		case "loop":
			g.dispatch[n.BodyNode] = append(g.dispatch[n.BodyNode], n)
		case "map":
			g.dispatch[n.TemplateNode] = append(g.dispatch[n.TemplateNode], n)
		case "switch":
			g.selection[n.ID] = newSwitchSelection(n)
		}
	}
	g.findNodesThatNeverRun(order)
	return g
}

func newSwitchSelection(sw *NodeConfig) switchSelection {
	targets, possible := map[string]bool{}, map[string]bool{}
	constantTrueSeen := false
	for _, c := range sw.Cases {
		targets[c.Node] = true
		if constantTrueSeen || c.Condition == constantFalse {
			continue
		}
		possible[c.Node] = true
		constantTrueSeen = c.Condition == constantTrue
	}
	if sw.DefaultCase != "" {
		targets[sw.DefaultCase] = true
		if !constantTrueSeen {
			possible[sw.DefaultCase] = true
		}
	}
	sel := switchSelection{neverSelected: map[string]bool{}}
	for target := range targets {
		if !possible[target] {
			sel.neverSelected[target] = true
		}
	}
	return sel
}

const (
	constantTrue  = "true"
	constantFalse = "false"
)

func (g *runStopGraph) findNodesThatNeverRun(nodes []*NodeConfig) {
	waiting := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	var ready []string
	for _, n := range nodes {
		for _, dep := range n.waitsFor() {
			if _, defined := g.byID[dep]; defined {
				waiting[n.ID]++
				dependents[dep] = append(dependents[dep], n.ID)
			}
		}
		if waiting[n.ID] == 0 {
			ready = append(ready, n.ID)
		}
	}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		if why, dead := g.neverRunsInMainFlow(g.byID[id]); dead {
			g.neverRuns[id] = why
		}
		for _, next := range dependents[id] {
			if waiting[next]--; waiting[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
}

func (g *runStopGraph) neverRunsInMainFlow(n *NodeConfig) (string, bool) {
	if g.dispatchedOutsideMainFlow(n.ID) {
		return "", false
	}
	if n.When == constantFalse {
		return whyWhenFalse, true
	}
	waits := n.waitsFor()
	if len(waits) == 0 {
		return "", false
	}
	for _, dep := range waits {
		if _, dead := g.neverRuns[dep]; dead || g.switchRoutesAwayFrom(dep, n.ID) {
			continue
		}
		return "", false
	}
	return whyUpstreamDead, true
}

func (g *runStopGraph) switchRoutesAwayFrom(switchID, target string) bool {
	sw := g.byID[switchID]
	if sw == nil || sw.Type != "switch" || g.dispatchedOutsideMainFlow(switchID) {
		return false
	}
	return g.selection[switchID].neverSelected[target]
}

func (g *runStopGraph) dispatchedOutsideMainFlow(id string) bool {
	return g.exit[id] || len(g.dispatch[id]) > 0
}

func (g *runStopGraph) cannotStop(id string) (string, bool) {
	if !g.dispatchedOutsideMainFlow(id) {
		why, dead := g.neverRuns[id]
		return why, dead
	}
	firstReason := ""
	for _, parent := range g.dispatch[id] {
		why, dispatches := g.whyParentDoesNotDispatch(parent)
		if dispatches {
			return "", false
		}
		firstReason = cmp.Or(firstReason, why)
	}
	if g.exit[id] {
		return whyExitHandler, true
	}
	return firstReason, true
}

func (g *runStopGraph) whyParentDoesNotDispatch(parent *NodeConfig) (why string, dispatches bool) {
	if g.dispatchedOutsideMainFlow(parent.ID) {
		return "", true
	}
	if _, dead := g.neverRuns[parent.ID]; dead {
		return whyParentDead, false
	}
	if parent.Type == "map" && hasLiteralEmptyItems(parent) {
		return whyEmptyMapItems, false
	}
	return "", true
}

func hasLiteralEmptyItems(mapNode *NodeConfig) bool {
	switch items := mapNode.ItemsExpression.(type) {
	case []any:
		return len(items) == 0
	case []string:
		return len(items) == 0
	}
	return false
}
