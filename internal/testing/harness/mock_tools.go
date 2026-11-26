// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func workflowMockBuiltin(name string, _ map[string]any) tool.BuiltinTool {
	switch name {
	case "get_weather_tool":
		return &GetWeatherTool{}
	case "failing_tool":
		return &FailingTool{}
	default:
		return nil
	}
}

type GetWeatherTool struct{}

func (t *GetWeatherTool) Name() string                                      { return "get_weather_tool" }
func (t *GetWeatherTool) Description() string                               { return "A mock weather tool for testing." }
func (t *GetWeatherTool) Instructions() string                              { return "" }
func (t *GetWeatherTool) IsLongRunning() bool                               { return false }
func (t *GetWeatherTool) RawStringArgs() []string                           { return nil }
func (t *GetWeatherTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (t *GetWeatherTool) ContextParam() string                              { return "" }

func (t *GetWeatherTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "The city to get weather for.",
			},
			"unit": map[string]any{
				"type":        "string",
				"description": "Temperature unit (celsius or fahrenheit).",
			},
		},
		Required: []string{"location"},
	}
}

func (t *GetWeatherTool) Execute(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
	location, _ := args["location"].(string)
	unit, _ := args["unit"].(string)
	if unit == "" {
		unit = "celsius"
	}

	var data map[string]any
	switch strings.ToLower(location) {
	case "london":
		data = map[string]any{"temperature": "22", "unit": unit, "condition": "sunny"}
	case "paris":
		data = map[string]any{"temperature": "25", "unit": unit, "condition": "lovely"}
	default:
		data = map[string]any{"temperature": "unknown", "unit": unit, "condition": "unknown"}
	}

	return tool.OK(fmt.Sprintf("Weather data for %s", location), tool.WithData(map[string]any{"data": data})), nil
}

type VolumeInspectTool struct{}

func (t *VolumeInspectTool) Name() string                                      { return "volume_inspect" }
func (t *VolumeInspectTool) Description() string                               { return "Inspect volume bindings for testing." }
func (t *VolumeInspectTool) Instructions() string                              { return "" }
func (t *VolumeInspectTool) IsLongRunning() bool                               { return false }
func (t *VolumeInspectTool) RawStringArgs() []string                           { return nil }
func (t *VolumeInspectTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (t *VolumeInspectTool) ContextParam() string                              { return "" }

func (t *VolumeInspectTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"label": map[string]any{
				"type":        "string",
				"description": "An optional label for this inspection call.",
			},
		},
	}
}

func (t *VolumeInspectTool) VolumeParams() []tool.VolumeParamInfo {
	return []tool.VolumeParamInfo{
		{Name: "workspace", Mode: "readwrite", MountPath: "/workspace"},
		{Name: "reference", Mode: "readonly", MountPath: "/reference"},
	}
}

func (t *VolumeInspectTool) Execute(_ context.Context, args map[string]any, tc *tool.Context) (*tool.Result, error) {
	report := map[string]any{
		"volume_bindings_count": len(tc.VolumeBindings),
	}

	if len(tc.CallerVolumeIDs) > 0 {
		report["caller_volume_ids"] = tc.CallerVolumeIDs
	}

	if len(tc.VolumeBindings) > 0 {
		bindings := make([]map[string]any, len(tc.VolumeBindings))
		for i, vb := range tc.VolumeBindings {
			bindings[i] = map[string]any{
				"volume_id":  vb.VolumeID,
				"mount_path": vb.MountPath,
				"read_only":  vb.ReadOnly,
			}
		}
		report["volume_bindings"] = bindings
	}

	reportJSON, _ := json.Marshal(report)
	return tool.OK(string(reportJSON)), nil
}

type FailingTool struct{}

func (t *FailingTool) Name() string                                      { return "failing_tool" }
func (t *FailingTool) Description() string                               { return "A mock tool that always fails." }
func (t *FailingTool) Instructions() string                              { return "" }
func (t *FailingTool) IsLongRunning() bool                               { return false }
func (t *FailingTool) RawStringArgs() []string                           { return nil }
func (t *FailingTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (t *FailingTool) ContextParam() string                              { return "" }

func (t *FailingTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object"}
}

func (t *FailingTool) Execute(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	return tool.Failed("process exited with code 1", &tool.ToolFailedDetail{
		Code:       "TOOL_EXIT_NONZERO",
		Message:    "process exited with code 1",
		StderrTail: "Error: something went wrong\n  at line 42",
		StackTrace: "RuntimeError: tool failed\n  at run() line 42",
	}), nil
}

type ScopedMockTool struct{}

func (t *ScopedMockTool) Name() string                                      { return "mock_scoped_tool" }
func (t *ScopedMockTool) Description() string                               { return "A mock tool gated on a required scope." }
func (t *ScopedMockTool) Instructions() string                              { return "" }
func (t *ScopedMockTool) IsLongRunning() bool                               { return false }
func (t *ScopedMockTool) RawStringArgs() []string                           { return nil }
func (t *ScopedMockTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (t *ScopedMockTool) ContextParam() string                              { return "" }

func (t *ScopedMockTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object"}
}

func (t *ScopedMockTool) RequiredScopes() []string {
	return []string{fmt.Sprintf("tool:%s:invoke", "mock_scoped")}
}

func (t *ScopedMockTool) Execute(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	return tool.OK("scoped mock tool ran"), nil
}
