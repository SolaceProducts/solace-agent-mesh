// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

func UnknownToolNodeProblems(appConfig map[string]any) []StaticProblem {
	appConfig = asRendered(appConfig)
	if appConfig == nil {
		return nil
	}
	body, nested := appConfig["workflow"].(map[string]any)
	if !nested {
		body = appConfig
	}
	levels := []map[string]any{body}
	if nested {
		levels = append(levels, appConfig)
	}
	catalog := newToolCatalog(levels, body)
	nodes, _ := body["nodes"].([]any)
	if !catalog.complete() {
		return catalog.unlistedMCPAdvisory(nodes)
	}
	stops := AnalyzeRunStops(body)
	var problems []StaticProblem
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok || node["type"] != "tool" {
			continue
		}
		toolName := toolNodeName(node)
		if toolName == "" || hasVarRef(toolName) || catalog.registers(toolName) {
			continue
		}
		id, _ := node["id"].(string)
		problems = append(problems, stops.FailingNodeProblem(id, toolName,
			fmt.Sprintf("tool node %q calls %q, but none of the workflow's tool sources registers that name, so the node fails whenever it runs with \"references unknown tool\"; the tools it registers are: %s",
				id, toolName, catalog.describe())))
	}
	return problems
}

func toolNodeName(node map[string]any) string {
	for _, key := range []string{"tool_name", "toolName"} {
		if v, present := node[key]; present {
			if s, isString := v.(string); isString {
				return s
			}
			return fmt.Sprint(v)
		}
	}
	return ""
}

type toolCatalog struct {
	names   map[string]bool
	entries int

	unlistedMCP int
	otherOpen   bool
}

func asRendered(appConfig map[string]any) map[string]any {
	raw, err := json.Marshal(appConfig)
	if err != nil {
		return nil
	}
	var rendered map[string]any
	if err := json.Unmarshal(raw, &rendered); err != nil {
		return nil
	}
	return rendered
}

func newToolCatalog(levels []map[string]any, body map[string]any) *toolCatalog {
	c := &toolCatalog{names: map[string]bool{}}
	for _, level := range levels {
		if declaresAny(level["toolsets"]) || declaresAny(level["connectors"]) {
			c.markOpen()
		}
		entries, _ := level["tools"].([]any)
		for _, entry := range entries {
			c.addEntry(entry)
		}
	}
	nodes, _ := body["nodes"].([]any)
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		if connector, _ := node["connector"].(string); connector != "" && node["type"] == "tool" {
			c.markOpen()
		}
	}
	return c
}

func (c *toolCatalog) markOpen() {
	c.otherOpen = true
}

func declaresAny(v any) bool {
	switch list := v.(type) {
	case nil:
		return false
	case []any:
		return len(list) > 0
	case []string:
		return len(list) > 0
	default:
		return true
	}
}

func (c *toolCatalog) addEntry(entry any) {
	m, ok := entry.(map[string]any)
	if !ok {
		return
	}
	c.entries++
	if name, isBuiltin := builtinToolName(entry); isBuiltin {
		if hasVarRef(name) {
			c.markOpen()
			return
		}
		c.names[name] = true
		return
	}
	switch toolType, _ := m["tool_type"].(string); toolType {
	case toolinit.ToolTypeBuiltin:
	case toolinit.ToolTypeBuiltinGroup:
		c.markOpen()
	case "mcp":
		names, pinning := pinnedMCPToolNames(m)
		switch pinning {
		case mcpUnlisted:
			c.unlistedMCP++
		case mcpOpaque:
			c.markOpen()
		case mcpPinned:
			for _, name := range names {
				c.names[name] = true
			}
		}
	default:
		c.markOpen()
	}
}

func (c *toolCatalog) complete() bool {
	return !c.otherOpen && c.unlistedMCP == 0 && c.entries > 0
}

func (c *toolCatalog) registers(name string) bool {
	return c.names[name]
}

func (c *toolCatalog) describe() string {
	if len(c.names) == 0 {
		return "none"
	}
	return strings.Join(slices.Sorted(maps.Keys(c.names)), ", ")
}

func (c *toolCatalog) unlistedMCPAdvisory(nodes []any) []StaticProblem {
	if c.otherOpen || c.unlistedMCP == 0 {
		return nil
	}
	var unjudged []string
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok || node["type"] != "tool" {
			continue
		}
		if name := toolNodeName(node); name != "" && !hasVarRef(name) && !c.registers(name) {
			id, _ := node["id"].(string)
			unjudged = append(unjudged, fmt.Sprintf("%q (calls %q)", id, name))
		}
	}
	if len(unjudged) == 0 {
		return nil
	}
	return []StaticProblem{{
		Message: fmt.Sprintf("tool node(s) %s are not checked: an mcp entry names its tools only once its server is connected, so a name that server does not register would fail at run time with \"references unknown tool\". Pin the entry's tools with tool_name, allow_list or an inline manifest to have the names checked",
			strings.Join(unjudged, ", ")),
	}}
}

func mcpEntryFilter(entry map[string]any) (*mcp.ToolsetConfig, string) {
	filter := &mcp.ToolsetConfig{}
	filter.ToolName, _ = entry["tool_name"].(string)
	filter.AllowList = stringList(entry["allow_list"])
	filter.DenyList = stringList(entry["deny_list"])
	prefix, _ := entry["tool_name_prefix"].(string)
	return filter, prefix
}

type mcpPinning int

const (
	mcpUnlisted mcpPinning = iota
	mcpOpaque
	mcpPinned
)

func pinnedMCPToolNames(entry map[string]any) (names []string, pinning mcpPinning) {
	if file, _ := entry["manifest_file"].(string); file != "" {
		return nil, mcpOpaque
	}
	filter, prefix := mcpEntryFilter(entry)
	if hasVarRef(prefix) || hasVarRef(filter.ToolName) || slices.ContainsFunc(filter.AllowList, hasVarRef) || slices.ContainsFunc(filter.DenyList, hasVarRef) {
		return nil, mcpOpaque
	}

	if manifest, _ := entry["manifest"].([]any); len(manifest) > 0 {
		for _, item := range manifest {
			name, ok := manifestToolName(item)
			if !ok || hasVarRef(name) {
				return nil, mcpOpaque
			}
			if filter.ShouldIncludeTool(name) {
				names = append(names, mcp.SAMToolName(name, prefix))
			}
		}
		return names, mcpPinned
	}

	serverNames := filter.AllowList
	if filter.ToolName != "" {
		serverNames = []string{filter.ToolName}
	}
	if len(serverNames) == 0 {
		return nil, mcpUnlisted
	}
	for _, name := range serverNames {
		names = append(names, mcp.SAMToolName(name, prefix))
	}
	return names, mcpPinned
}

func manifestToolName(item any) (string, bool) {
	m, ok := item.(map[string]any)
	if !ok {
		return "", false
	}
	if name, _ := m["name"].(string); name != "" {
		return name, true
	}
	if id, _ := m["id"].(string); id != "" {
		return id, true
	}
	return "", false
}

func stringList(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hasVarRef(s string) bool {
	return strings.Contains(s, "${") || strings.Contains(s, "{{")
}
