// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

type mcpConnectorTool struct {
	server     string
	registered string
}

func mcpConnectorTools(conn *store.Connector, values map[string]any, entry map[string]any) ([]mcpConnectorTool, error) {
	generic, err := asGenericJSON(entry)
	if err != nil {
		return nil, fmt.Errorf("connector %q (type mcp): %w", conn.Name, err)
	}
	cfg, err := mcp.ParseToolsetConfig(generic)
	if err != nil {
		return nil, fmt.Errorf("connector %q (type mcp) has stored settings the runtime rejects (%v); correct them on the connector", conn.Name, err)
	}
	if cfg.RequiresManifestForAuth() {
		return nil, needsManifestError(conn, values, "uses OAuth, so the platform cannot discover its tools")
	}

	var tools []mcpConnectorTool
	add := func(server string) {
		if !slices.ContainsFunc(tools, func(t mcpConnectorTool) bool { return t.server == server }) {
			tools = append(tools, mcpConnectorTool{server: server, registered: mcp.SAMToolName(server, cfg.ToolNamePrefix)})
		}
	}
	switch {
	case len(cfg.Manifest) > 0:
		for _, tool := range cfg.Manifest {
			if cfg.ShouldIncludeTool(tool.Name) {
				add(tool.Name)
			}
		}
		if len(tools) == 0 {
			return nil, fmt.Errorf("connector %q (type mcp): its tool_name, allow_list, deny_list or selected_tools leave none of its stored tools; change that selection on the connector", conn.Name)
		}
	case cfg.ToolName != "":
		add(cfg.ToolName)
	default:
		return nil, noStoredMCPToolListError(conn, values)
	}
	return tools, nil
}

func noStoredMCPToolListError(conn *store.Connector, values map[string]any) error {
	switch {
	case values["auth_type"] == "oauth":
		return needsManifestError(conn, values, "uses OAuth, so the platform cannot discover its tools")
	case holdsEnvReference(values):
		return needsManifestError(conn, values, "holds its server address or credentials as ${...} references, so the platform cannot discover its tools")
	default:
		return fmt.Errorf("connector %q (type mcp) has no stored tool list, so the platform cannot tell which tools it serves; re-discover its tools by re-saving the connector while its MCP server is reachable", conn.Name)
	}
}

func needsManifestError(conn *store.Connector, values map[string]any, cause string) error {
	remedy := "add a manifest listing its tools to the connector"
	for _, filter := range []string{"tool_name", "allow_list", "deny_list"} {
		if len(splitMCPNameList(values[filter])) > 0 {
			return fmt.Errorf("connector %q (type mcp) %s; %s, and remove its tool_name, allow_list and deny_list, which cannot be combined with a manifest", conn.Name, cause, remedy)
		}
	}
	return fmt.Errorf("connector %q (type mcp) %s; %s", conn.Name, cause, remedy)
}

func asGenericJSON(v map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return generic, nil
}

func holdsEnvReference(values map[string]any) bool {
	for _, v := range values {
		if s, ok := v.(string); ok && strings.Contains(s, "${") {
			return true
		}
	}
	return false
}

func duplicateRegisteredNames(tools []mcpConnectorTool) map[string]bool {
	seen := map[string]bool{}
	duplicated := map[string]bool{}
	for _, tool := range tools {
		if seen[tool.registered] {
			duplicated[tool.registered] = true
		}
		seen[tool.registered] = true
	}
	return duplicated
}

type mcpBindings struct {
	tools map[string][]mcpConnectorTool
	errs  map[string]error
	bound []boundMCPTool
}

type boundMCPTool struct {
	nodeID string
	ref    string
	tool   mcpConnectorTool
}

func newMCPBindings(rowsByRef map[string]*store.Connector, valuesByRef map[string]map[string]any, entriesByRef map[string][]map[string]any) *mcpBindings {
	b := &mcpBindings{tools: map[string][]mcpConnectorTool{}, errs: map[string]error{}}
	for ref, conn := range rowsByRef {
		if conn.Type != "mcp" {
			continue
		}
		b.tools[ref], b.errs[ref] = mcpConnectorTools(conn, valuesByRef[ref], entriesByRef[ref][0])
	}
	return b
}

func (b *mcpBindings) bind(conn *store.Connector, ref, nodeID, sel string) (string, error) {
	if err := b.errs[ref]; err != nil {
		return "", err
	}
	tools := b.tools[ref]
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.server
	}
	var picked mcpConnectorTool
	switch {
	case sel == "" && len(tools) == 1:
		picked = tools[0]
	case sel == "":
		return "", fmt.Errorf("connector %q emits %d tools; set 'tool' to one of: %s", conn.Name, len(tools), strings.Join(names, ", "))
	default:
		i := slices.Index(names, sel)
		if i < 0 {
			return "", fmt.Errorf("connector %q emits no tool named %q; available: %s", conn.Name, sel, strings.Join(names, ", "))
		}
		picked = tools[i]
	}
	b.bound = append(b.bound, boundMCPTool{nodeID: nodeID, ref: ref, tool: picked})
	return picked.registered, nil
}

func (b *mcpBindings) clashes() error {
	refs := slices.Sorted(maps.Keys(b.tools))
	for _, bound := range b.bound {
		for _, ref := range refs {
			for _, t := range b.tools[ref] {
				if t.registered != bound.tool.registered || (ref == bound.ref && t.server == bound.tool.server) {
					continue
				}
				if ref == bound.ref {
					return fmt.Errorf("node %q: connector %q's tools %q and %q both register as %q; deselect one with selected_tools or deny_list, because a tool_name_prefix applies to both",
						bound.nodeID, ref, bound.tool.server, t.server, t.registered)
				}
				return fmt.Errorf("node %q: connector %q's tool %q and connector %q's tool %q both register as %q; set tool_name_prefix on one connector so each node reaches its own tool",
					bound.nodeID, bound.ref, bound.tool.server, ref, t.server, t.registered)
			}
		}
	}
	return nil
}
