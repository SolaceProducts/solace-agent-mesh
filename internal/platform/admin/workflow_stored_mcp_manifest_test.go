// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func TestToolNodeNames_ReadAStoredMCPConnectorsManifestWithoutACall(t *testing.T) {
	t.Parallel()
	emitted := buildConnectorTools(ConnectorConfig{
		ID:   "0f6c1d7e-3a52-4e9b-8f10-7a2b9c4d5e66",
		Type: "mcp",
		Values: map[string]any{
			"server_url":     "http://127.0.0.1:1/mcp",
			"selected_tools": "get_forecast,search_locations",
			"manifest": []any{
				map[string]any{"name": "get_forecast"},
				map[string]any{"name": "search_locations"},
				map[string]any{"name": "get_weather_alerts"},
			},
		},
	})
	if len(emitted) != 1 {
		t.Fatalf("connector emitted %d entries, want 1", len(emitted))
	}
	hydrated := func(nodeTool string) map[string]any {
		return map[string]any{
			"workflow": map[string]any{
				"nodes": []any{map[string]any{"id": "forecast", "type": "tool", "tool_name": nodeTool}},
			},
			"tools": []any{emitted[0]},
		}
	}

	messages := unknownToolNodeMessages("weather-flow", hydrated("get_weather_alerts"))
	if len(messages) != 1 {
		t.Fatalf("messages = %v; a tool the operator deselected is not registered, so the node must be refused", messages)
	}
	for _, want := range []string{`"get_weather_alerts"`, "references unknown tool", "get_forecast, search_locations"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("message %q is missing %q", messages[0], want)
		}
	}
	for _, registered := range []string{"get_forecast", "search_locations"} {
		if messages := unknownToolNodeMessages("weather-flow", hydrated(registered)); len(messages) != 0 {
			t.Errorf("%s is registered by the manifest, got %v", registered, messages)
		}
	}
}
