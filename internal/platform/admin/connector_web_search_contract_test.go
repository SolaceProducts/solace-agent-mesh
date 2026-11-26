// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

func TestWebSearchConnector_EveryRenderedKeyIsOneTheSTRToolReads(t *testing.T) {
	t.Parallel()
	for _, subtype := range []string{"tavily", "exa"} {
		entry := buildWebSearchConnectorTool(ConnectorConfig{
			ID: "c56a4180-65aa-42ec-a945-5fd21dec0538", Type: webSearchConnectorType, Subtype: subtype,
			Values: map[string]any{
				"api_key": "k", "api_base": "https://search.example", "max_results": 7, "safe_search": "yes",
				"tool_name": "web_search_custom", "tool_description": "Searches the web.",
			},
		}, "c56a4180-65aa-42ec-a945-5fd21dec0538")
		toolConfig, _ := entry["tool_config"].(map[string]any)
		if len(toolConfig) < len(websearch.ConnectorConfigKeys) {
			t.Fatalf("%s: tool_config = %v, want every connector key rendered for this input", subtype, toolConfig)
		}
		for key := range toolConfig {
			if key != "_str_binary" && !slices.Contains(websearch.ConnectorConfigKeys, key) {
				t.Errorf("%s: the renderer emits tool_config.%s, which the web_search tool does not read", subtype, key)
			}
		}
	}
}
