// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

const deepResearchTool = "deep_research"

func withResearchConnector(logger *slog.Logger, toolsets []string, connectors []ConnectorConfig, emission ToolEmission) ToolEmission {
	connID, search := researchSearchConfig(connectors)
	if search == nil {
		return emission
	}
	configs := maps.Clone(emission.BuiltinToolConfigs)
	for _, toolset := range toolsets {
		if !slices.Contains(builtinToolsetTools[toolset], deepResearchTool) ||
			slices.Contains(emission.BuiltinExcludedTools[toolset], deepResearchTool) {
			continue
		}
		if configs == nil {
			configs = make(map[string]map[string]map[string]any)
		}
		perTool := maps.Clone(configs[toolset])
		if perTool == nil {
			perTool = make(map[string]map[string]any)
		}
		authored := perTool[deepResearchTool]
		cfg := maps.Clone(authored)
		if cfg == nil {
			cfg = make(map[string]any, len(search))
		}
		maps.DeleteFunc(cfg, func(key string, _ any) bool { return websearch.IsProviderConfigKey(key) })
		maps.Copy(cfg, search)
		unreadable := keepSafeSearchFailClosedAndOmitFalseSoTheOperatorDefaultApplies(cfg, authored)
		if logger != nil {
			logResearchOverride(logger.With("toolset", toolset, "connectorID", connID), authored, unreadable != nil)
		}
		perTool[deepResearchTool] = cfg
		configs[toolset] = perTool
	}
	emission.BuiltinToolConfigs = configs
	return emission
}

func keepSafeSearchFailClosedAndOmitFalseSoTheOperatorDefaultApplies(cfg, authored map[string]any) error {
	authoredOn, unreadable := websearch.SafeSearch(authored)
	if authoredOn || unreadable != nil {
		cfg["safe_search"] = true
	}
	if cfg["safe_search"] != true {
		delete(cfg, "safe_search")
	}
	return unreadable
}

func logResearchOverride(logger *slog.Logger, authored map[string]any, safeSearchUnreadable bool) {
	if replaced := replacedProviderKeys(authored); len(replaced) > 0 {
		logger.Warn("deep_research searches through the agent's Web Search connector instead of its own search settings",
			"replacedKeys", replaced)
	}
	if safeSearchUnreadable {
		logger.Warn("deep_research tool_config safe_search is neither true nor false; keeping safe search on until it is set to true or false",
			"valueType", fmt.Sprintf("%T", authored["safe_search"]))
	}
}

func replacedProviderKeys(authored map[string]any) []string {
	var keys []string
	for key := range authored {
		if key != "safe_search" && websearch.IsProviderConfigKey(key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func uuidv7CreatedBefore(id, other string) bool { return id < other }

func researchSearchConfig(connectors []ConnectorConfig) (string, map[string]any) {
	var oldestID string
	var search map[string]any
	for _, conn := range connectors {
		if conn.Type != webSearchConnectorType || (oldestID != "" && !uuidv7CreatedBefore(conn.ID, oldestID)) {
			continue
		}
		if cfg := webSearchProviderConfig(conn, connectorEnvID(conn.ID)); cfg != nil {
			oldestID, search = conn.ID, cfg
		}
	}
	return oldestID, search
}
