// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	pf "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestBuildMCPGatewayConfig_FileInput(t *testing.T) {
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{ID: "gw-abc-123", Name: "test", Description: strPtr("test gateway fixture for MCP file input")}
	runtimeKey := mcpFieldRuntimeKey(t, "enableFileInput")
	if runtimeKey != "enable_file_input" {
		t.Fatalf("enableFileInput runtime key = %q; the MCP adapter reads enable_file_input and ignores any other key", runtimeKey)
	}

	cases := map[string]struct {
		value any
		want  bool
	}{
		"string true":  {value: "true", want: true},
		"bool true":    {value: true, want: true},
		"string false": {value: "false"},
		"absent":       {},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			values := map[string]any{"enableAuth": "false"}
			if tc.value != nil {
				values["enableFileInput"] = tc.value
			}
			apps, _ := svc.buildMCPGatewayConfig(gw, values)["apps"].([]map[string]any)
			appConfig, _ := apps[0]["app_config"].(map[string]any)
			adapter, _ := appConfig["gateway_adapter"].(map[string]any)
			adapterConfig, _ := adapter["adapter_config"].(map[string]any)

			got, present := adapterConfig[runtimeKey]
			if tc.want && got != true {
				t.Errorf("adapter_config[%q] = %v, want true", runtimeKey, got)
			}
			if !tc.want && present {
				t.Errorf("adapter_config[%q] = %v, want it left out so the runtime default (off) applies", runtimeKey, got)
			}
		})
	}
}

func mcpFieldRuntimeKey(t *testing.T, name string) string {
	t.Helper()
	def, ok := pf.LookupGatewayType("mcp")
	if !ok {
		t.Fatal("no mcp gateway type registered")
	}
	for _, f := range def.Fields {
		if f.Name == name {
			return f.RuntimeKey
		}
	}
	t.Fatalf("mcp gateway type has no %s field", name)
	return ""
}
