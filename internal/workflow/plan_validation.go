// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func FatalMessages(problems []StaticProblem) []string {
	var messages []string
	for _, p := range problems {
		if p.Fatal {
			messages = append(messages, p.Message)
		}
	}
	return messages
}

type StaticProblem struct {
	NodeID string

	ToolName string

	Message string

	Fatal bool

	NotFatalBecause string
}

func (p StaticProblem) Downgraded() bool {
	return p.NotFatalBecause != ""
}

type schemaTrust func(t tool.Tool) bool

type StaticCheckOptions struct {
	Permissive bool
}

func planSchemaTrust(cfg *WorkflowConfig, opts StaticCheckOptions) schemaTrust {
	explicit := make(map[string]bool, len(cfg.Tools))
	opaque := len(cfg.Connectors) > 0
	for _, entry := range cfg.Tools {
		if name, ok := builtinToolName(entry); ok {
			explicit[name] = true
			continue
		}
		m, _ := entry.(map[string]any)
		if toolType, _ := m["tool_type"].(string); toolType != "builtin-group" {
			opaque = true
		}
	}
	return func(t tool.Tool) bool {
		if !tool.HasAuthoredSchemas(t) {
			return false
		}
		return explicit[t.Name()] || !opaque
	}
}

func StaticToolNodeProblems(cfg *WorkflowConfig, tools *tool.Set, opts StaticCheckOptions) []StaticProblem {
	if cfg == nil || tools == nil {
		return nil
	}
	byID := make(map[string]*NodeConfig, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		if n == nil || n.ID == "" {
			continue
		}
		if _, dup := byID[n.ID]; !dup {
			byID[n.ID] = n
		}
	}
	for id, n := range byID {
		if n.Connector != "" && n.ToolName != "" {
			withoutToolName := *n
			withoutToolName.ToolName = ""
			byID[id] = &withoutToolName
		}
	}
	trust := planSchemaTrust(cfg, opts)
	stops := analyzeParsedRunStops(cfg.OnExit, cfg.Nodes)
	var problems []StaticProblem
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		problems = append(problems, toolArgProblems(byID[id], tools, trust, opts.Permissive, stops)...)
	}
	for _, p := range unproducibleNodeReferences(byID, cfg.OutputMapping, tools) {
		if !trustedResultSchema(p, byID, tools, trust) {
			continue
		}
		problems = append(problems, StaticProblem{
			Message: p.detail,
		})
	}
	return problems
}

func trustedResultSchema(p referenceProblem, nodes map[string]*NodeConfig, tools *tool.Set, trust schemaTrust) bool {
	if p.kind != problemKindUnproducibleField && p.kind != problemKindUnverifiedField {
		return true
	}
	upstream, ok := nodes[p.upstreamNodeID]
	if !ok {
		return true
	}
	t := tools.GetTool(upstream.ToolName)
	return t != nil && trust(t)
}

func toolArgProblems(node *NodeConfig, tools *tool.Set, trust schemaTrust, permissive bool, stops *RunStopAnalysis) []StaticProblem {
	if node.Type != "tool" || node.Input == nil || isOperatorMap(node.Input) {
		return nil
	}
	if node.Connector != "" {
		return nil
	}
	t := tools.GetTool(node.ToolName)
	if t == nil {
		return nil
	}
	undeclared := undeclaredToolArgs(t, node.Input)
	if len(undeclared) == 0 {
		return nil
	}
	message := undeclaredArgsMessage(node.ID, node.ToolName, undeclared, declaredToolArgs(t))
	if !trust(t) || permissive {
		return []StaticProblem{{NodeID: node.ID, Message: message}}
	}
	return []StaticProblem{stops.FailingNodeProblem(node.ID, node.ToolName, message)}
}
