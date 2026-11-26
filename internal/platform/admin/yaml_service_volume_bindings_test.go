// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"reflect"
	"testing"
)

func TestBuildAgentConfigFull_VolumeBindings_ExpandsToBuiltinEntries(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	wantTools := builtinToolsetTools["builtin_volume_tools"]
	if len(wantTools) == 0 {
		t.Fatal("builtin_volume_tools resolved no tool names; registration regressed")
	}

	emission := ToolEmission{
		BuiltinVolumeBindings: map[string]map[string]string{
			"builtin_volume_tools": {"workspace": "cc_workspace"},
		},
	}
	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_volume_tools"}, nil, nil, nil, emission)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)

	if len(tools) != len(wantTools) {
		t.Fatalf("want %d builtin entries, got %d: %+v", len(wantTools), len(tools), tools)
	}
	gotNames := make(map[string]bool, len(tools))
	for _, entry := range tools {
		if entry["tool_type"] != "builtin" {
			t.Errorf("entry tool_type = %v, want builtin: %+v", entry["tool_type"], entry)
		}
		name, _ := entry["tool_name"].(string)
		gotNames[name] = true
		vb, ok := entry["volume_bindings"].(map[string]any)
		if !ok {
			t.Errorf("entry %q missing volume_bindings: %+v", name, entry)
			continue
		}
		if vb["workspace"] != "cc_workspace" {
			t.Errorf("entry %q volume_bindings = %v, want workspace=cc_workspace", name, vb)
		}
	}
	for _, want := range wantTools {
		if !gotNames[want] {
			t.Errorf("missing expected volume tool entry %q", want)
		}
	}
}

func TestBuildAgentConfigFull_VolumeBindings_WithPerToolConfig(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	wantTools := builtinToolsetTools["builtin_volume_tools"]
	if len(wantTools) == 0 {
		t.Skip("builtin_volume_tools not registered")
	}
	firstTool := wantTools[0]

	emission := ToolEmission{
		BuiltinToolConfigs: map[string]map[string]map[string]any{
			"builtin_volume_tools": {firstTool: {"max_bytes": 1024}},
		},
		BuiltinVolumeBindings: map[string]map[string]string{
			"builtin_volume_tools": {"workspace": "ws"},
		},
	}
	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_volume_tools"}, nil, nil, nil, emission)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)

	var checked bool
	for _, entry := range tools {
		if entry["tool_name"] != firstTool {
			continue
		}
		checked = true
		if vb, _ := entry["volume_bindings"].(map[string]any); vb["workspace"] != "ws" {
			t.Errorf("%q volume_bindings = %v, want workspace=ws", firstTool, entry["volume_bindings"])
		}
		cfg, ok := entry["tool_config"].(map[string]any)
		if !ok || cfg["max_bytes"] != 1024 {
			t.Errorf("%q tool_config = %v, want max_bytes=1024", firstTool, entry["tool_config"])
		}
	}
	if !checked {
		t.Fatalf("expected an entry for %q", firstTool)
	}
}

func TestVolumeBindingsYAML(t *testing.T) {
	t.Parallel()
	if got := volumeBindingsYAML(nil); got != nil {
		t.Errorf("nil input = %v, want nil", got)
	}
	if got := volumeBindingsYAML(map[string]string{}); got != nil {
		t.Errorf("empty input = %v, want nil", got)
	}
	got := volumeBindingsYAML(map[string]string{"workspace": "ws"})
	if !reflect.DeepEqual(got, map[string]any{"workspace": "ws"}) {
		t.Errorf("got %v, want {workspace: ws}", got)
	}
	a := volumeBindingsYAML(map[string]string{"workspace": "ws"})
	b := volumeBindingsYAML(map[string]string{"workspace": "ws"})
	a["workspace"] = "mutated"
	if b["workspace"] != "ws" {
		t.Errorf("maps share backing storage: b = %v", b)
	}
}

func TestBuildAgentConfigFull_SchedulingTools_EmitsUnscopedGroup(t *testing.T) {
	t.Parallel()

	svc := NewYAMLService("test-ns", slog.Default())
	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"scheduling_tools"}, nil, nil, nil, ToolEmission{})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	tools := parseToolsFromYAML(t, yamlStr)
	if len(tools) != 1 {
		t.Fatalf("want 1 builtin-group entry, got %d: %+v", len(tools), tools)
	}
	entry := tools[0]
	if entry["tool_type"] != "builtin-group" || entry["group_name"] != "scheduling_tools" {
		t.Errorf("entry = %+v, want builtin-group scheduling_tools", entry)
	}
	if _, ok := entry["required_scopes"]; ok {
		t.Errorf("entry carries required_scopes, want none: %+v", entry)
	}
}
