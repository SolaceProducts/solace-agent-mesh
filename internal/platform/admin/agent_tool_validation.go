// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

type toolNameEntry struct {
	Name   string
	Source string
}

func validateConnectorToolNames(ctx context.Context, connectorIDs []string, connectorRepo *store.ConnectorRepo, agentName string, toolsets []string) error {
	seen := make(map[string]string)

	for _, entry := range getBuiltinToolNames(toolsets) {
		seen[entry.Name] = entry.Source
	}

	for _, connID := range connectorIDs {
		conn, err := connectorRepo.GetByID(ctx, connID)
		if err != nil || conn == nil {
			continue
		}

		for _, entry := range extractToolNamesFromConnector(conn) {
			if firstSource, exists := seen[entry.Name]; exists {
				return &ValidationError{
					Message: fmt.Sprintf(
						"Duplicate tool name detected: '%s'. First occurrence: %s, Second occurrence: %s. "+
							"Please modify the tool configuration to ensure unique tool names.",
						entry.Name, firstSource, entry.Source,
					),
					Field: fieldConnectorIDs,
				}
			}
			seen[entry.Name] = entry.Source
		}
	}

	return nil
}

func extractToolNamesFromConnector(conn *store.Connector) []toolNameEntry {
	if conn.Values == nil || *conn.Values == "" {
		return nil
	}

	var values map[string]any
	if err := json.Unmarshal([]byte(*conn.Values), &values); err != nil {
		return nil
	}

	source := fmt.Sprintf("%s connector '%s'", conn.Type, conn.Name)

	switch conn.Type {
	case "sql":
		return extractSQLToolNames(conn, values, source)
	case "knowledge_base":
		return extractKBToolNames(conn, source)
	case "mcp":
		return extractMCPToolNames(values, source)
	case "api":
		return extractAPIToolNames(values, source)
	case "document_db", "graph_db", "search", webSearchConnectorType:
		return extractBuiltinConnectorToolNames(conn, values, source)
	case "slack":
		return extractSlackToolNames(conn, values, source)
	case "event_mesh":
		operations, err := eventMeshOperations(values)
		if err != nil {
			return nil
		}
		entries := make([]toolNameEntry, 0, len(operations))
		for _, operation := range operations {
			if name, _ := operation["tool_name"].(string); name != "" {
				entries = append(entries, toolNameEntry{Name: name + "_" + connectorSuffix(conn.ID), Source: source})
			}
		}
		return entries
	default:
		return nil
	}
}

type builtinConnectorSubtypeNaming struct {
	verb       string
	literalKey string
}

var builtinConnectorToolMappings = map[string]map[string]builtinConnectorSubtypeNaming{
	"document_db": {
		"mongodb":  {verb: "mongo_query", literalKey: "collection"},
		"dynamodb": {verb: "dynamodb_query", literalKey: "table_name"},
	},
	"graph_db": {
		"neo4j":   {verb: "neo4j_query", literalKey: "database"},
		"neptune": {verb: "neptune_query", literalKey: "database"},
	},
	"search": {
		"elasticsearch": {verb: "elasticsearch_query"},
		"opensearch":    {verb: "opensearch_query"},
	},
	webSearchConnectorType: {
		"tavily": {verb: tool.WebSearchTurnKey},
		"exa":    {verb: tool.WebSearchTurnKey},
	},
}

func extractBuiltinConnectorToolNames(conn *store.Connector, values map[string]any, source string) []toolNameEntry {
	mapping, ok := builtinConnectorToolMappings[conn.Type]
	if !ok {
		slog.Default().Warn("connector type has no tool-name mapping; duplicate-name check skipped",
			"connectorID", conn.ID, "type", conn.Type, "source", source)
		return nil
	}
	if name, _ := values["tool_name"].(string); name != "" {
		return []toolNameEntry{{Name: name, Source: source}}
	}
	m, ok := mapping[connectorSubtype(conn)]
	if !ok {
		slog.Default().Warn("connector subtype has no tool-name mapping; duplicate-name check skipped",
			"connectorID", conn.ID, "type", conn.Type, "subtype", connectorSubtype(conn), "source", source)
		return nil
	}
	suffix := toolNameSuffix(conn.Type, conn.ID)
	name := fmt.Sprintf("%s_%s", m.verb, suffix)
	if m.literalKey != "" {
		if literal, _ := values[m.literalKey].(string); literal != "" {
			name = fmt.Sprintf("%s_%s_%s", sanitizeForBrokerTopic(literal), m.verb, suffix)
		}
	}
	return []toolNameEntry{{Name: name, Source: source}}
}

func connectorSubtype(conn *store.Connector) string {
	if conn == nil || conn.Subtype == nil {
		return ""
	}
	return strings.ToLower(*conn.Subtype)
}

func extractSlackToolNames(conn *store.Connector, values map[string]any, source string) []toolNameEntry {
	suffix := connectorSuffix(conn.ID)
	sendName, _ := values["tool_name"].(string)
	if sendName == "" {
		sendName = fmt.Sprintf("slack_send_message_%s", suffix)
	}
	updateName := fmt.Sprintf("slack_update_%s", suffix)
	return []toolNameEntry{
		{Name: sendName, Source: source},
		{Name: updateName, Source: source},
	}
}

func extractSQLToolNames(conn *store.Connector, values map[string]any, source string) []toolNameEntry {
	toolName, _ := values["tool_name"].(string)
	if toolName == "" {
		suffix := connectorSuffix(conn.ID)
		dbLiteral, _ := values["database"].(string)
		if dbLiteral != "" {
			toolName = fmt.Sprintf("%s_sql_query_%s", dbLiteral, suffix)
		} else {
			toolName = fmt.Sprintf("sql_query_%s", suffix)
		}
	}
	return []toolNameEntry{{Name: toolName, Source: source}}
}

func extractKBToolNames(conn *store.Connector, source string) []toolNameEntry {
	prefix := conn.ID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	prefix = strings.ReplaceAll(prefix, "-", "_")
	return []toolNameEntry{{Name: "query_bedrock_kb_" + prefix, Source: source}}
}

func extractMCPToolNames(values map[string]any, source string) []toolNameEntry {
	manifest := parseMCPManifest(values)
	if len(manifest) == 0 {
		return nil
	}
	prefix, _ := values["tool_name_prefix"].(string)

	selectedTools := parseMCPSelectedTools(values)
	selectedSet := make(map[string]struct{}, len(selectedTools))
	for _, t := range selectedTools {
		selectedSet[t] = struct{}{}
	}

	var entries []toolNameEntry
	for _, tool := range manifest {
		name, _ := tool["name"].(string)
		if name == "" {
			continue
		}
		if len(selectedTools) > 0 {
			if _, ok := selectedSet[name]; !ok {
				continue
			}
		}
		entries = append(entries, toolNameEntry{Name: mcp.SAMToolName(name, prefix), Source: source})
	}
	return entries
}

func extractAPIToolNames(values map[string]any, source string) []toolNameEntry {
	allowList, _ := values["allow_list"].(string)
	if allowList == "" {
		return nil
	}
	ops := parseOperationList(allowList)
	entries := make([]toolNameEntry, 0, len(ops))
	for _, op := range ops {
		entries = append(entries, toolNameEntry{Name: openapi.OperationToolName(op), Source: source})
	}
	return entries
}

func getBuiltinToolNames(toolsets []string) []toolNameEntry {
	var entries []toolNameEntry
	for _, ts := range toolsets {
		names, ok := builtinToolsetTools[ts]
		if !ok {
			continue
		}
		source := fmt.Sprintf("builtin toolset '%s'", ts)
		for _, name := range names {
			entries = append(entries, toolNameEntry{Name: name, Source: source})
		}
	}
	return entries
}
