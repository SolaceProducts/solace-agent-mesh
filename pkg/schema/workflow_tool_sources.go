// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"errors"
	"fmt"
	"strings"
)

var workflowToolSourceKeys = []string{"tools", "toolsets", "connectors"}

const noToolSourceClause = "the workflow declares no tools:, toolsets: or connectors: and no node binds a connector:"

type undeclaredToolNode struct {
	path     string
	id       string
	toolName string
}

func WorkflowToolSourceError(appConfig map[string]any) error {
	nodes := undeclaredToolNodes(appConfig)
	if len(nodes) == 0 {
		return nil
	}
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = fmt.Sprintf("tool node %q calls %q, declare %s", n.id, n.toolName, toolSourceSuggestion(n.toolName))
	}
	return errors.New(noToolSourceClause + ", so these tool nodes fail whenever they run: " + strings.Join(parts, "; "))
}

func workflowToolSourceValidationErrors(appConfig map[string]any) []ValidationError {
	nodes := undeclaredToolNodes(appConfig)
	errs := make([]ValidationError, len(nodes))
	for i, n := range nodes {
		errs[i] = ValidationError{
			Path: n.path,
			Message: fmt.Sprintf("tool node %q calls %q, but %s, so this node fails whenever it runs; declare its source with %s",
				n.id, n.toolName, noToolSourceClause, toolSourceSuggestion(n.toolName)),
		}
	}
	return errs
}

func undeclaredToolNodes(appConfig map[string]any) []undeclaredToolNode {
	body, nested := appConfig["workflow"].(map[string]any)
	if !nested {
		body = appConfig
	}
	rawNodes, _ := body["nodes"].([]any)
	if declaresToolSource(appConfig) || declaresToolSource(body) || bindsNodeConnector(rawNodes) {
		return nil
	}
	pathPrefix := ""
	if nested {
		pathPrefix = "workflow/"
	}
	var found []undeclaredToolNode
	for i, raw := range rawNodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		nameKey, toolName := nodeToolName(node)
		if node["type"] != "tool" || toolName == "" {
			continue
		}
		id, _ := node["id"].(string)
		found = append(found, undeclaredToolNode{
			path:     fmt.Sprintf("%snodes[%d]/%s", pathPrefix, i, nameKey),
			id:       id,
			toolName: toolName,
		})
	}
	return found
}

func declaresToolSource(m map[string]any) bool {
	for _, key := range workflowToolSourceKeys {
		if list, ok := m[key].([]any); ok && len(list) > 0 {
			return true
		}
	}
	return false
}

func bindsNodeConnector(nodes []any) bool {
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		if connector, _ := node["connector"].(string); node["type"] == "tool" && connector != "" {
			return true
		}
	}
	return false
}

func nodeToolName(node map[string]any) (key, name string) {
	for _, key := range []string{"tool_name", "toolName"} {
		if name := loaderString(node, key); name != "" {
			return key, name
		}
	}
	return "toolName", ""
}

func loaderString(m map[string]any, key string) string {
	v, present := m[key]
	if !present {
		return ""
	}
	if s, isString := v.(string); isString {
		return s
	}
	return fmt.Sprint(v)
}

func toolSourceSuggestion(toolName string) string {
	if prefix, tool, ok := strings.Cut(toolName, "__"); ok && prefix != "" && tool != "" {
		return fmt.Sprintf("`toolsets: [%s]` if %s is an uploaded toolset, "+
			"or a `tools:` entry `{tool_type: builtin, tool_name: %s}` if %s is a skill, "+
			"though a skill's stored config and credentials do not reach workflow tool nodes",
			prefix, prefix, toolName, prefix)
	}
	return fmt.Sprintf("a `tools:` entry such as `{tool_type: builtin, tool_name: %s}`, "+
		"a `tool_type: mcp` entry for an MCP tool, or `connector: <name>` on the node for a platform connector's tool",
		toolName)
}
