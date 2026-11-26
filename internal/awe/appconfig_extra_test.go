// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestLookupBuiltinTool(t *testing.T) {
	simpleTools := []struct {
		name     string
		wantName string
	}{
		{"list_artifacts", "list_artifacts"},
		{"load_artifact", "load_artifact"},
		{"delete_artifact", "delete_artifact"},
		{"append_to_artifact", "append_to_artifact"},
		{"artifact_search_and_replace_regex", "artifact_search_and_replace_regex"},
		{"get_current_time", "get_current_time"},
		{"web_request", "web_request"},
		{"ask_user_question", "ask_user_question"},
		{"validate_component_config", "validate_component_config"},
		{"query_data_with_sql", "query_data_with_sql"},
		{"transform_data_with_jmespath", "transform_data_with_jmespath"},
		{"merge_structured_data", "merge_structured_data"},
		{"create_sqlite_db", "create_sqlite_db"},
		{"artifact_grep", "artifact_grep"},
		{"index_search", "index_search"},
		{"platform_list_components", "platform_list_components"},
		{"platform_get_component_config", "platform_get_component_config"},
	}
	for _, tt := range simpleTools {
		t.Run(tt.name, func(t *testing.T) {
			tool := lookupBuiltinTool(tt.name, nil)
			if tool == nil {
				t.Fatalf("lookupBuiltinTool(%q) returned nil", tt.name)
			}
			if tool.Name() != tt.wantName {
				t.Errorf("lookupBuiltinTool(%q).Name() = %q, want %q", tt.name, tool.Name(), tt.wantName)
			}
		})
	}

	configTools := []string{
		"create_image_from_description", "describe_image", "describe_audio",
		"edit_image_with_gemini", "generate_image_with_gemini",
		"web_search", "web_search_google", "deep_research",
	}
	for _, name := range configTools {
		t.Run(name, func(t *testing.T) {
			tool := lookupBuiltinTool(name, nil)
			if tool == nil {
				t.Fatalf("lookupBuiltinTool(%q) returned nil", name)
			}
		})
	}
}

func TestLookupBuiltinToolUnknown(t *testing.T) {
	tool := lookupBuiltinTool("totally_unknown", nil)
	if tool != nil {
		t.Errorf("lookupBuiltinTool(\"totally_unknown\") = %v, want nil", tool)
	}
}

func TestLookupBuiltinToolSubTask(t *testing.T) {
	tool := lookupBuiltinTool("sub_task", nil)
	if tool != nil {
		t.Errorf("lookupBuiltinTool(\"sub_task\") should return nil")
	}
}

func TestLookupBuiltinToolWithConfig(t *testing.T) {
	toolMap := map[string]any{
		"tool_config": map[string]any{
			"api_key": "test-key",
		},
	}
	tool := lookupBuiltinTool("web_search_google", toolMap)
	if tool == nil {
		t.Fatal("lookupBuiltinTool(\"web_search_google\") with config returned nil")
	}
}

func TestLookupBuiltinGroup(t *testing.T) {
	groups := []struct {
		name     string
		minTools int
	}{
		{"artifact_management", 5},
		{"general_agent_tools", 3},
		{"image_tools", 5},
		{"web_tools", 2},
		{"research", 2},
		{"builtin_artifact_tools", 5},
		{"builtin_web_request_tools", 2},
		{"builtin_research_tools", 2},
		{"builtin_image_tools", 5},
		{"builtin_file_tools", 1},
		{"builtin_time_tools", 1},
		{"hil_tools", 1},
		{"builder_tools", 7},
		{"data_analysis", 3},
	}
	for _, tt := range groups {
		t.Run(tt.name, func(t *testing.T) {
			names := lookupBuiltinGroup(tt.name)
			if names == nil {
				t.Fatalf("lookupBuiltinGroup(%q) returned nil", tt.name)
			}
			if len(names) < tt.minTools {
				t.Errorf("lookupBuiltinGroup(%q) returned %d tools, want >= %d", tt.name, len(names), tt.minTools)
			}
		})
	}
}

func TestLookupBuiltinGroupUnknown(t *testing.T) {
	names := lookupBuiltinGroup("nonexistent_group")
	if names != nil {
		t.Errorf("lookupBuiltinGroup(\"nonexistent_group\") should return nil")
	}
}

func TestLookupBuiltinGroupResearchAlias(t *testing.T) {
	got := lookupBuiltinGroup("builtin_research_tools")
	want := []string{"web_search_google", "deep_research"}
	if !slices.Equal(got, want) {
		t.Errorf("lookupBuiltinGroup(\"builtin_research_tools\") = %v, want %v", got, want)
	}
}

func TestParseHILConfigsFromEntries_Basic(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": true,
					"approval_message": "Are you sure?",
					"show_args":        false,
				},
			},
			toolNames: []string{"my_tool"},
		},
	}
	result, perr := parseHILConfigsFromEntries(entries)
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 HIL config, got %d", len(result))
	}
	cfg, ok := result["my_tool"]
	if !ok {
		t.Fatal("expected config for my_tool")
	}
	if !cfg.RequireApproval {
		t.Error("RequireApproval should be true")
	}
	if cfg.ApprovalMessage != "Are you sure?" {
		t.Errorf("ApprovalMessage = %q, want %q", cfg.ApprovalMessage, "Are you sure?")
	}
	if cfg.ShowArgs {
		t.Error("ShowArgs should be false")
	}
}

func TestParseHILConfigsFromEntries_NoHIL(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolMap:   map[string]any{"tool_name": "foo"},
			toolNames: []string{"foo"},
		},
	}
	result, perr := parseHILConfigsFromEntries(entries)
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 HIL configs, got %d", len(result))
	}
}

func TestParseHILConfigsFromEntries_RequireApprovalFalse(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": false,
					"approval_message": "unused",
				},
			},
			toolNames: []string{"tool_a"},
		},
	}
	result, perr := parseHILConfigsFromEntries(entries)
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 HIL configs (require_approval=false), got %d", len(result))
	}
}

func TestParseHILConfigsFromEntries_MultipleTools(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": true,
					"approval_message": "Approve all",
				},
			},
			toolNames: []string{"tool_a", "tool_b", "tool_c"},
		},
	}
	result, perr := parseHILConfigsFromEntries(entries)
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 HIL configs, got %d", len(result))
	}
	for _, name := range []string{"tool_a", "tool_b", "tool_c"} {
		cfg, ok := result[name]
		if !ok {
			t.Errorf("missing config for %s", name)
			continue
		}
		if cfg.ApprovalMessage != "Approve all" {
			t.Errorf("%s ApprovalMessage = %q, want %q", name, cfg.ApprovalMessage, "Approve all")
		}
	}
}

func TestParseHILConfigsFromEntries_PerToolOverrides(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": true,
					"approval_message": "Default message",
					"show_args":        true,
					"tools": map[string]any{
						"tool_b": map[string]any{
							"approval_message": "Custom for B",
							"show_args":        false,
						},
					},
				},
			},
			toolNames: []string{"tool_a", "tool_b"},
		},
	}
	result, perr := parseHILConfigsFromEntries(entries)
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 HIL configs, got %d", len(result))
	}

	cfgA := result["tool_a"]
	if cfgA.ApprovalMessage != "Default message" {
		t.Errorf("tool_a ApprovalMessage = %q, want %q", cfgA.ApprovalMessage, "Default message")
	}
	if !cfgA.ShowArgs {
		t.Error("tool_a ShowArgs should be true (default)")
	}

	cfgB := result["tool_b"]
	if cfgB.ApprovalMessage != "Custom for B" {
		t.Errorf("tool_b ApprovalMessage = %q, want %q", cfgB.ApprovalMessage, "Custom for B")
	}
	if cfgB.ShowArgs {
		t.Error("tool_b ShowArgs should be false (override)")
	}
}

func TestParseHILConfigsFromEntries_Timeout(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": true,
					"timeout":          "5m",
				},
			},
			toolNames: []string{"my_tool"},
		},
	}
	result, perr := parseHILConfigsFromEntries(entries)
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	cfg := result["my_tool"]
	if cfg.Timeout != 5*time.Minute {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, 5*time.Minute)
	}
}

func TestParseHILConfigsFromEntries_InvalidTimeoutFailsClosed(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": true,
					"timeout":          "not-a-duration",
				},
			},
			toolNames: []string{"my_tool"},
		},
	}
	_, perr := parseHILConfigsFromEntries(entries)
	if perr == nil {
		t.Fatal("an unparseable timeout was accepted; the gate would silently use a different bound")
	}
	if !strings.Contains(perr.Error(), "not a valid duration") {
		t.Errorf("error does not name the problem: %v", perr)
	}
	if !strings.Contains(perr.Error(), "my_tool") {
		t.Errorf("error does not name the tool an operator has to fix: %v", perr)
	}
}

func TestParseHILConfigsFromEntries_DegradedEntryDoesNotFailTheBoot(t *testing.T) {
	entries := toHILEntries([]toolinit.EntryRecord{{
		ToolMap: map[string]any{
			"tool_type": "mcp",
			"hil":       map[string]any{"tools": map[string]any{"srv__search": map[string]any{"require_approval": true}}},
		},
		ToolNames: nil,
		Status:    toolinit.EntryStatusFailed,
		Error:     "connect failed",
	}})
	got, err := parseHILConfigsFromEntries(entries)
	if err != nil {
		t.Fatalf("a dead MCP backend must not stop the agent starting: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an entry that registered nothing cannot gate anything, got %v", got)
	}
}

func TestParseHILConfigsFromEntries_HealthyEntryRejectsUnknownOverride(t *testing.T) {
	entries := toHILEntries([]toolinit.EntryRecord{{
		ToolMap: map[string]any{
			"tool_type": "mcp",
			"hil":       map[string]any{"tools": map[string]any{"srv__searhc": map[string]any{"require_approval": true}}},
		},
		ToolNames: []string{"srv__search"},
		Status:    toolinit.EntryStatusReady,
	}})
	if _, err := parseHILConfigsFromEntries(entries); err == nil {
		t.Fatal("a typo on a healthy entry was accepted; the tool it meant to gate stays ungated")
	}
}

func TestApplyHILConfigs_InstallsEvenWhenEmpty(t *testing.T) {
	ag := agent.New(agent.AgentConfig{Name: "a", Namespace: "ns"})
	if err := applyHILConfigs(ag, []hilEntryRecord{
		{toolMap: map[string]any{"hil": map[string]any{"require_approval": true}}, toolNames: []string{"danger"}},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, gated := ag.HILToolConfigForTool("danger"); !gated {
		t.Fatal("precondition: the gate should be installed")
	}

	if err := applyHILConfigs(ag, nil); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if _, gated := ag.HILToolConfigForTool("danger"); gated {
		t.Error("a config that no longer declares a gate left the previous one enforcing")
	}
}

func TestApplyHILConfigs_PropagatesParseErrors(t *testing.T) {
	ag := agent.New(agent.AgentConfig{Name: "a", Namespace: "ns"})
	err := applyHILConfigs(ag, []hilEntryRecord{
		{toolMap: map[string]any{"hil": map[string]any{"require_approval": "yes"}}, toolNames: []string{"danger"}},
	})
	if err == nil {
		t.Fatal("a malformed gate was accepted at the install seam")
	}
}

func TestParseHILConfigsFromEntries_MisshapenFieldsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		hil  any
	}{
		{"hil is not a map", true},
		{"require_approval quoted by a Helm value", map[string]any{"require_approval": "true"}},
		{"show_args quoted", map[string]any{"require_approval": true, "show_args": "false"}},
		{"timeout is a number", map[string]any{"require_approval": true, "timeout": 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, perr := parseHILConfigsFromEntries([]hilEntryRecord{
				{toolMap: map[string]any{"hil": tc.hil}, toolNames: []string{"my_tool"}},
			})
			if perr == nil {
				t.Fatal("a misshapen gate was accepted; the tool would run ungated")
			}
		})
	}
}

func TestParseHILConfigsFromEntries_Empty(t *testing.T) {
	result, perr := parseHILConfigsFromEntries(nil)
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestParseHILConfigsFromEntries_MultipleEntries(t *testing.T) {
	entries := []hilEntryRecord{
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": true,
					"approval_message": "Entry 1",
				},
			},
			toolNames: []string{"tool_from_entry1"},
		},
		{
			toolMap: map[string]any{
				"hil": map[string]any{
					"require_approval": true,
					"approval_message": "Entry 2",
					"timeout":          "1h",
				},
			},
			toolNames: []string{"tool_from_entry2"},
		},
	}
	result, perr := parseHILConfigsFromEntries(entries)
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 HIL configs, got %d", len(result))
	}
	if result["tool_from_entry1"].ApprovalMessage != "Entry 1" {
		t.Errorf("entry1 message = %q", result["tool_from_entry1"].ApprovalMessage)
	}
	if result["tool_from_entry2"].Timeout != time.Hour {
		t.Errorf("entry2 timeout = %v, want 1h", result["tool_from_entry2"].Timeout)
	}
}

func TestParseHILToolConfig_Defaults(t *testing.T) {
	cfg, perr := parseHILToolConfig(map[string]any{}, "test_tool")
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	if cfg.RequireApproval {
		t.Error("RequireApproval should default to false")
	}
	if !cfg.ShowArgs {
		t.Error("ShowArgs should default to true")
	}
	if cfg.ApprovalMessage != "" {
		t.Errorf("ApprovalMessage should be empty, got %q", cfg.ApprovalMessage)
	}
	if cfg.Timeout != 0 {
		t.Errorf("Timeout should be zero, got %v", cfg.Timeout)
	}
}

func TestParseHILToolConfig_AllFields(t *testing.T) {
	cfg, perr := parseHILToolConfig(map[string]any{
		"require_approval": true,
		"approval_message": "Please approve",
		"timeout":          "30s",
		"show_args":        false,
	}, "test_tool")
	if perr != nil {
		t.Fatalf("unexpected parse error: %v", perr)
	}
	if !cfg.RequireApproval {
		t.Error("RequireApproval should be true")
	}
	if cfg.ApprovalMessage != "Please approve" {
		t.Errorf("ApprovalMessage = %q", cfg.ApprovalMessage)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
	if cfg.ShowArgs {
		t.Error("ShowArgs should be false")
	}
}

func TestMergeHILToolConfig(t *testing.T) {
	base := agent.HILToolConfig{
		RequireApproval: true,
		ApprovalMessage: "base msg",
		Timeout:         10 * time.Second,
		ShowArgs:        true,
	}
	merged, mErr := mergeHILToolConfig(base, map[string]any{
		"approval_message": "overridden",
		"show_args":        false,
	}, "test_tool")
	if mErr != nil {
		t.Fatalf("unexpected merge error: %v", mErr)
	}

	if !merged.RequireApproval {
		t.Error("RequireApproval should remain true (not overridden)")
	}
	if merged.ApprovalMessage != "overridden" {
		t.Errorf("ApprovalMessage = %q, want %q", merged.ApprovalMessage, "overridden")
	}
	if merged.Timeout != 10*time.Second {
		t.Errorf("Timeout should remain 10s, got %v", merged.Timeout)
	}
	if merged.ShowArgs {
		t.Error("ShowArgs should be false (overridden)")
	}
}

func TestMergeHILToolConfig_TimeoutOverride(t *testing.T) {
	base := agent.HILToolConfig{Timeout: 10 * time.Second}
	merged, mErr := mergeHILToolConfig(base, map[string]any{
		"timeout": "2m",
	}, "test_tool")
	if mErr != nil {
		t.Fatalf("unexpected merge error: %v", mErr)
	}
	if merged.Timeout != 2*time.Minute {
		t.Errorf("Timeout = %v, want 2m", merged.Timeout)
	}
}

func TestMergeHILToolConfig_EmptyOverride(t *testing.T) {
	base := agent.HILToolConfig{
		RequireApproval: true,
		ApprovalMessage: "keep",
		ShowArgs:        true,
	}
	merged, mErr := mergeHILToolConfig(base, map[string]any{}, "test_tool")
	if mErr != nil {
		t.Fatalf("unexpected merge error: %v", mErr)
	}
	if !reflect.DeepEqual(merged, base) {
		t.Errorf("empty override should not change base: got %+v, want %+v", merged, base)
	}
}

func TestParseSkillRefs(t *testing.T) {
	raw := []any{
		map[string]any{"name": "manim", "path": "/skills/manim"},
		map[string]any{"name": "code_runner"},
		map[string]any{"name": ""},
		"not a map",
		map[string]any{"other": "no_name"},
	}

	refs := parseSkillRefs(raw)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %+v", len(refs), refs)
	}
	if refs[0].Name != "manim" || refs[0].Path != "/skills/manim" {
		t.Errorf("refs[0] = %+v, want {Name:manim Path:/skills/manim}", refs[0])
	}
	if refs[1].Name != "code_runner" || refs[1].Path != "" {
		t.Errorf("refs[1] = %+v, want {Name:code_runner Path:}", refs[1])
	}
}

func TestParseSkillRefsEmpty(t *testing.T) {
	refs := parseSkillRefs(nil)
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
	refs = parseSkillRefs([]any{})
	if len(refs) != 0 {
		t.Errorf("expected 0 refs for empty slice, got %d", len(refs))
	}
}

func TestParseSkillRefs_ToolConfig(t *testing.T) {
	raw := []any{
		map[string]any{
			"name": "manim",
			"tool_config": map[string]any{
				"render": map[string]any{
					"api_key": "sk-secret",
					"region":  "us-east-1",
				},
				"cleanup": map[string]any{
					"ttl_seconds": 3600,
				},
			},
		},
		map[string]any{"name": "no_config"},
	}
	refs := parseSkillRefs(raw)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	if refs[0].Name != "manim" {
		t.Fatalf("refs[0].Name = %q, want manim", refs[0].Name)
	}
	if got, want := len(refs[0].ToolConfig), 2; got != want {
		t.Fatalf("refs[0].ToolConfig entries = %d, want %d", got, want)
	}
	if refs[0].ToolConfig["render"]["api_key"] != "sk-secret" {
		t.Errorf("render.api_key = %v", refs[0].ToolConfig["render"]["api_key"])
	}
	if refs[0].ToolConfig["render"]["region"] != "us-east-1" {
		t.Errorf("render.region = %v", refs[0].ToolConfig["render"]["region"])
	}
	if refs[0].ToolConfig["cleanup"]["ttl_seconds"] != 3600 {
		t.Errorf("cleanup.ttl_seconds = %v", refs[0].ToolConfig["cleanup"]["ttl_seconds"])
	}
	if refs[1].ToolConfig != nil {
		t.Errorf("refs[1].ToolConfig = %v, want nil for skill with no tool_config block", refs[1].ToolConfig)
	}
}

func TestSplitSkillToolName(t *testing.T) {
	cases := []struct {
		in    string
		skill string
		bare  string
		ok    bool
	}{
		{"manim__render", "manim", "render", true},
		{"my_skill__some_tool", "my_skill", "some_tool", true},
		{"no_separator", "", "", false},
		{"__missing_skill", "", "", false},
		{"missing_tool__", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		skill, bare, ok := splitSkillToolName(tc.in)
		if skill != tc.skill || bare != tc.bare || ok != tc.ok {
			t.Errorf("splitSkillToolName(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, skill, bare, ok, tc.skill, tc.bare, tc.ok)
		}
	}
}

func TestExtractModelName(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{"string simple", "gpt-4", "gpt-4"},
		{"string with provider prefix", "openai/gpt-4", "gpt-4"},
		{"map with model", map[string]any{"model": "anthropic/claude-4-5-sonnet"}, "claude-4-5-sonnet"},
		{"map without model", map[string]any{"other": "val"}, ""},
		{"nil", nil, ""},
		{"int type", 42, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractModelName(tt.raw)
			if got != tt.want {
				t.Errorf("extractModelName(%v) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestExtractOriginalModelName(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{"string preserves prefix", "openai/gpt-4", "openai/gpt-4"},
		{"map preserves prefix", map[string]any{"model": "openai/vertex-claude"}, "openai/vertex-claude"},
		{"map no model key", map[string]any{}, ""},
		{"nil", nil, ""},
		{"int type", 123, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOriginalModelName(tt.raw)
			if got != tt.want {
				t.Errorf("extractOriginalModelName(%v) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestStringVal(t *testing.T) {
	m := map[string]any{
		"key1": "value1",
		"key2": 42,
		"key3": nil,
	}
	if got := stringVal(m, "key1"); got != "value1" {
		t.Errorf("stringVal(key1) = %q, want %q", got, "value1")
	}
	if got := stringVal(m, "key2"); got != "" {
		t.Errorf("stringVal(key2) = %q, want empty (not a string)", got)
	}
	if got := stringVal(m, "key3"); got != "" {
		t.Errorf("stringVal(key3) = %q, want empty (nil)", got)
	}
	if got := stringVal(m, "missing"); got != "" {
		t.Errorf("stringVal(missing) = %q, want empty", got)
	}
}

func TestIntDefault(t *testing.T) {
	cfg := config.FromMap(map[string]any{"count": 42})
	if got := intDefault(cfg, "count", 10); got != 42 {
		t.Errorf("intDefault(count) = %d, want 42", got)
	}
	if got := intDefault(cfg, "missing", 10); got != 10 {
		t.Errorf("intDefault(missing) = %d, want 10 (default)", got)
	}
}

func TestBoolPtr(t *testing.T) {
	cfg := config.FromMap(map[string]any{"flag": true})
	got := boolPtr(cfg, "flag")
	if got == nil {
		t.Fatal("boolPtr(flag) returned nil")
	}
	if !*got {
		t.Error("boolPtr(flag) should be true")
	}
	got = boolPtr(cfg, "missing")
	if got != nil {
		t.Errorf("boolPtr(missing) should be nil, got %v", *got)
	}
}

func TestStringDefault(t *testing.T) {
	cfg := config.FromMap(map[string]any{"name": "hello"})
	if got := stringDefault(cfg, "name", "fallback"); got != "hello" {
		t.Errorf("stringDefault(name) = %q, want %q", got, "hello")
	}
	if got := stringDefault(cfg, "missing", "fallback"); got != "fallback" {
		t.Errorf("stringDefault(missing) = %q, want %q", got, "fallback")
	}
	cfg2 := config.FromMap(map[string]any{"name": ""})
	if got := stringDefault(cfg2, "name", "fallback"); got != "fallback" {
		t.Errorf("stringDefault(empty) = %q, want %q", got, "fallback")
	}
}

func TestParseSkillsFromCard(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"skills": []any{
			map[string]any{
				"id":          "skill-1",
				"name":        "My Skill",
				"description": "Does things",
				"tags":        []any{"tag1", "tag2"},
				"examples":    []any{"example 1", "example 2"},
				"inputModes":  []any{"text", "image/png"},
				"outputModes": []any{"text", "application/json"},
			},
			map[string]any{
				"id":   "skill-2",
				"name": "Another",
			},
			"not-a-map",
		},
	})

	skills := parseSkills(cfg)
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	s1 := skills[0]
	if s1.ID != "skill-1" || s1.Name != "My Skill" || s1.Description != "Does things" {
		t.Errorf("skill[0] = %+v", s1)
	}
	if len(s1.Tags) != 2 || s1.Tags[0] != "tag1" || s1.Tags[1] != "tag2" {
		t.Errorf("skill[0].Tags = %v", s1.Tags)
	}
	if len(s1.Examples) != 2 {
		t.Errorf("skill[0].Examples = %v", s1.Examples)
	}
	if len(s1.InputModes) != 2 || s1.InputModes[0] != "text" || s1.InputModes[1] != "image/png" {
		t.Errorf("skill[0].InputModes = %v", s1.InputModes)
	}
	if len(s1.OutputModes) != 2 || s1.OutputModes[0] != "text" || s1.OutputModes[1] != "application/json" {
		t.Errorf("skill[0].OutputModes = %v", s1.OutputModes)
	}

	s2 := skills[1]
	if s2.ID != "skill-2" {
		t.Errorf("skill[1].ID = %q", s2.ID)
	}
	if len(s2.Tags) != 0 {
		t.Errorf("skill[1].Tags should be empty (initialized to []), got %v", s2.Tags)
	}
	if len(s2.InputModes) != 0 {
		t.Errorf("skill[1].InputModes should be empty, got %v", s2.InputModes)
	}
	if len(s2.OutputModes) != 0 {
		t.Errorf("skill[1].OutputModes should be empty, got %v", s2.OutputModes)
	}
}

func TestParseSkillsFromCardEmpty(t *testing.T) {
	cfg := config.FromMap(map[string]any{})
	skills := parseSkills(cfg)
	if skills != nil {
		t.Errorf("expected nil for no skills config, got %v", skills)
	}
}

func TestRegisterToolsAutoRegistersIndexSearch(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	if _, err := RegisterTools(ag, nil, svc); err != nil {
		t.Fatal(err)
	}

	if ag.Tools().GetTool("index_search") == nil {
		t.Error("index_search should be auto-registered even when not in config")
	}
}

func TestToolsRegisteredWithoutAToolsEntryAreTheAutoRegisteredBuiltins(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterTools(ag, nil, svc); err != nil {
		t.Fatal(err)
	}

	for name := range schema.ValidBuiltinToolNames {
		registered := ag.Tools().GetTool(name) != nil
		if registered != schema.AutoRegisteredBuiltinToolNames[name] {
			t.Errorf("%q: registered with no tools: entry = %v, listed as auto-registered = %v", name, registered, schema.AutoRegisteredBuiltinToolNames[name])
		}
	}
}

func TestRegisterToolsDoesNotDuplicateIndexSearch(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "index_search"},
	}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatal(err)
	}

	if ag.Tools().GetTool("index_search") == nil {
		t.Error("index_search should be present when explicitly configured")
	}
}

func TestRegisterToolsIndexSearchWithTypicalConfig(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management"},
		map[string]any{"tool_type": "builtin-group", "group_name": "general_agent_tools"},
		map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
	}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatal(err)
	}

	if ag.Tools().GetTool("index_search") == nil {
		t.Error("index_search should be auto-registered alongside typical builtin groups")
	}
	for _, name := range []string{"list_artifacts", "load_artifact", "artifact_search_and_replace_regex", "get_current_time", "web_request"} {
		if ag.Tools().GetTool(name) == nil {
			t.Errorf("expected configured tool %q to be registered", name)
		}
	}
}

func TestRegisterTools_OpenAPIToolType_RegistersOperations(t *testing.T) {
	t.Parallel()
	specJSON := `{
	  "openapi": "3.0.0",
	  "info": {"title": "t", "version": "1"},
	  "servers": [{"url": "http://example.com"}],
	  "paths": {
	    "/users": {
	      "get": {"operationId": "listUsers", "responses": {"200": {"description": "ok"}}}
	    },
	    "/users/{id}": {
	      "get": {
	        "operationId": "getUser",
	        "parameters": [
	          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}
	        ],
	        "responses": {"200": {"description": "ok"}}
	      }
	    }
	  }
	}`

	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":     "openapi",
			"specification": specJSON,
		},
	}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	for _, want := range []string{"list_users", "get_user"} {
		if ag.Tools().GetTool(want) == nil {
			got := make([]string, 0, len(ag.Tools().ListTools()))
			for _, tl := range ag.Tools().ListTools() {
				got = append(got, tl.Name())
			}
			t.Errorf("expected openapi tool %q to be registered; got %v", want, got)
		}
	}
}

func TestRegisterTools_OpenAPIToolType_InvalidSpec(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":     "openapi",
			"specification": "not a spec",
		},
	}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools should not hard-fail on invalid spec: %v", err)
	}
}

func TestRegisterTools_PythonToolType_LogsMigrationGuidance(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":   "python",
			"tool_name":   "my_python_tool",
			"module_path": "agents.my_agent.tools",
		},
	}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools should not fail on python type: %v", err)
	}
	if ag.Tools().GetTool("my_python_tool") != nil {
		t.Error("tool_type: python should NOT silently register a tool in Go SAM")
	}
}

func TestRegisterToolsBuiltinInProcess(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if ag.Tools().GetTool("get_current_time") == nil {
		t.Error("in-process builtin should be registered")
	}
}

func TestRegisterToolsBuiltinFallthroughToRemote(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent", Namespace: "ns"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":        "builtin",
			"tool_name":        "mermaid_diagram_generator",
			"tool_description": "Render Mermaid syntax to PNG",
			"timeout_seconds":  120,
		},
	}
	remoteExec, err := RegisterTools(ag, toolsCfg, svc)
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if remoteExec == nil {
		t.Fatal("expected remote executor to be created for STR-routed builtin")
	}
	if ag.Tools().GetTool("mermaid_diagram_generator") == nil {
		t.Error("STR-routed builtin should be registered on the agent")
	}
}

func TestRegisterToolsSamRemoteReturnsMigrationError(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent", Namespace: "ns"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":        "sam_remote",
			"tool_name":        "legacy_tool",
			"tool_description": "legacy",
		},
	}
	_, err := RegisterTools(ag, toolsCfg, svc)
	if err == nil {
		t.Fatal("expected migration error for tool_type: sam_remote, got nil")
	}
	if !strings.Contains(err.Error(), "sam_remote") || !strings.Contains(err.Error(), "builtin") {
		t.Errorf("error should reference both sam_remote and builtin: %v", err)
	}
	if !strings.Contains(err.Error(), "legacy_tool") {
		t.Errorf("error should include the offending tool_name: %v", err)
	}
}

func TestRegisterToolsBuiltinGroupMixesInProcessAndSTR(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent", Namespace: "ns"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "data_analysis"},
	}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	for _, name := range []string{"query_data_with_sql", "transform_data_with_jmespath", "merge_structured_data", "create_chart_from_plotly_config"} {
		if ag.Tools().GetTool(name) == nil {
			t.Errorf("expected group member %q to be registered", name)
		}
	}
}

func TestRegisterTools_MCP_ConcurrentDiscovery_OrderingPreserved(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	mkManifest := func(toolName string) []any {
		return []any{
			map[string]any{
				"name":         toolName,
				"description":  "manifest tool",
				"input_schema": map[string]any{"type": "object"},
			},
		}
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"type": "streamable-http", "url": "https://srv-alpha.example.com/mcp"},
			"tool_name_prefix":  "alpha_",
			"manifest":          mkManifest("ping"),
		},
		map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"type": "streamable-http", "url": "https://srv-beta.example.com/mcp"},
			"tool_name_prefix":  "beta_",
			"manifest":          mkManifest("ping"),
		},
		map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"type": "streamable-http", "url": "https://srv-gamma.example.com/mcp"},
			"tool_name_prefix":  "gamma_",
			"manifest":          mkManifest("ping"),
		},
	}

	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	for _, want := range []string{"alpha_ping", "beta_ping", "gamma_ping"} {
		if ag.Tools().GetTool(want) == nil {
			t.Errorf("expected MCP tool %q to be registered", want)
		}
	}
}

func TestRegisterTools_MCP_ConcurrentDiscovery_PartialFailure(t *testing.T) {
	t.Parallel()
	svc := newTestServices()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"type": "streamable-http", "url": "https://valid.example.com/mcp"},
			"tool_name_prefix":  "valid_",
			"manifest": []any{
				map[string]any{"name": "ping", "input_schema": map[string]any{"type": "object"}},
			},
		},
		map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"url": "https://no-type.example.com/mcp"},
		},
	}

	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if ag.Tools().GetTool("valid_ping") == nil {
		t.Error("valid MCP toolset should still register when a sibling is invalid")
	}
}

func firstArtifactGroupIndex(tools []any) int {
	for i, item := range tools {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["tool_type"].(string); t != "builtin-group" {
			continue
		}
		if g, _ := m["group_name"].(string); g == "artifact_management" {
			return i
		}
	}
	return -1
}

func TestEnsureArtifactManagementGroup_InjectsWhenMissing(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}

	got := EnsureArtifactManagementGroup(tools, nil, "test-agent", slog.Default())

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (injected entry + existing)", len(got))
	}
	if firstArtifactGroupIndex(got) != 0 {
		t.Errorf("artifact_management group should be prepended at index 0, got tools=%v", got)
	}
	if t0, _ := got[1].(map[string]any)["tool_name"].(string); t0 != "get_current_time" {
		t.Errorf("existing tool should remain at index 1, got %v", got[1])
	}
}

func TestEnsureArtifactManagementGroup_NoDuplicateWhenPresent(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management"},
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}

	got := EnsureArtifactManagementGroup(tools, nil, "test-agent", slog.Default())

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (no duplicate)", len(got))
	}
	count := 0
	for _, item := range got {
		m, _ := item.(map[string]any)
		if t, _ := m["tool_type"].(string); t == "builtin-group" {
			if g, _ := m["group_name"].(string); g == "artifact_management" {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("artifact_management group should appear exactly once, got %d occurrences", count)
	}
}

func TestEnsureArtifactManagementGroup_OptOutSkipsInjection(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}
	optOut := false

	got := EnsureArtifactManagementGroup(tools, &optOut, "test-agent", slog.Default())

	if firstArtifactGroupIndex(got) != -1 {
		t.Errorf("auto_inject_artifact_tools=false must skip injection, got %v", got)
	}
	if len(got) != 1 {
		t.Errorf("len(got) = %d, want 1 (unchanged)", len(got))
	}
}

func TestParseAgentConfigAutoInjectArtifactToolsFlag(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want *bool
	}{
		{
			name: "unset",
			cfg:  map[string]any{"agent_name": "a"},
			want: nil,
		},
		{
			name: "explicit-true",
			cfg:  map[string]any{"agent_name": "a", "auto_inject_artifact_tools": true},
			want: func() *bool { v := true; return &v }(),
		},
		{
			name: "explicit-false",
			cfg:  map[string]any{"agent_name": "a", "auto_inject_artifact_tools": false},
			want: func() *bool { v := false; return &v }(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac, err := ParseAgentConfig(config.FromMap(tt.cfg))
			if err != nil {
				t.Fatalf("ParseAgentConfig: %v", err)
			}
			switch {
			case tt.want == nil && ac.AutoInjectArtifactTools != nil:
				t.Errorf("AutoInjectArtifactTools = %v, want nil", *ac.AutoInjectArtifactTools)
			case tt.want != nil && ac.AutoInjectArtifactTools == nil:
				t.Errorf("AutoInjectArtifactTools = nil, want %v", *tt.want)
			case tt.want != nil && *tt.want != *ac.AutoInjectArtifactTools:
				t.Errorf("AutoInjectArtifactTools = %v, want %v", *ac.AutoInjectArtifactTools, *tt.want)
			}
		})
	}
}

func firstDataAnalysisGroupIndex(tools []any) int {
	for i, item := range tools {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["tool_type"].(string); t != "builtin-group" {
			continue
		}
		if g, _ := m["group_name"].(string); g == "data_analysis" {
			return i
		}
	}
	return -1
}

func TestEnsureDataAnalysisGroup_InjectsWhenMissing(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}

	got := EnsureDataAnalysisGroup(tools, nil, "test-agent", slog.Default())

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (injected entry + existing)", len(got))
	}
	if firstDataAnalysisGroupIndex(got) != 0 {
		t.Errorf("data_analysis group should be prepended at index 0, got tools=%v", got)
	}
}

func TestEnsureDataAnalysisGroup_NoDuplicateWhenPresent(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "data_analysis"},
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}

	got := EnsureDataAnalysisGroup(tools, nil, "test-agent", slog.Default())

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (no duplicate)", len(got))
	}
	count := 0
	for _, item := range got {
		m, _ := item.(map[string]any)
		if t, _ := m["tool_type"].(string); t == "builtin-group" {
			if g, _ := m["group_name"].(string); g == "data_analysis" {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("data_analysis group should appear exactly once, got %d occurrences", count)
	}
}

func TestEnsureDataAnalysisGroup_OptOutSkipsInjection(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}
	optOut := false

	got := EnsureDataAnalysisGroup(tools, &optOut, "test-agent", slog.Default())

	if firstDataAnalysisGroupIndex(got) != -1 {
		t.Errorf("auto_inject_data_analysis_tools=false must skip injection, got %v", got)
	}
	if len(got) != 1 {
		t.Errorf("len(got) = %d, want 1 (unchanged)", len(got))
	}
}

func TestParseAgentConfigAutoInjectDataAnalysisToolsFlag(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want *bool
	}{
		{
			name: "unset",
			cfg:  map[string]any{"agent_name": "a"},
			want: nil,
		},
		{
			name: "explicit-true",
			cfg:  map[string]any{"agent_name": "a", "auto_inject_data_analysis_tools": true},
			want: func() *bool { v := true; return &v }(),
		},
		{
			name: "explicit-false",
			cfg:  map[string]any{"agent_name": "a", "auto_inject_data_analysis_tools": false},
			want: func() *bool { v := false; return &v }(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac, err := ParseAgentConfig(config.FromMap(tt.cfg))
			if err != nil {
				t.Fatalf("ParseAgentConfig: %v", err)
			}
			switch {
			case tt.want == nil && ac.AutoInjectDataAnalysisTools != nil:
				t.Errorf("AutoInjectDataAnalysisTools = %v, want nil", *ac.AutoInjectDataAnalysisTools)
			case tt.want != nil && ac.AutoInjectDataAnalysisTools == nil:
				t.Errorf("AutoInjectDataAnalysisTools = nil, want %v", *tt.want)
			case tt.want != nil && *tt.want != *ac.AutoInjectDataAnalysisTools:
				t.Errorf("AutoInjectDataAnalysisTools = %v, want %v", *ac.AutoInjectDataAnalysisTools, *tt.want)
			}
		})
	}
}

func TestRegisterTools_OpenAPIToolType_ObjStoreSpec(t *testing.T) {
	t.Parallel()
	specYAML := `openapi: 3.0.0
info: {title: ObjStore API, version: "1.0"}
servers: [{url: "https://api.example.com"}]
paths:
  /ping:
    get:
      operationId: ping
      responses: {"200": {description: ok}}
`
	store, err := objectstore.NewResourceStore(map[string]any{"type": "memory"}, objectstore.ResourceStoreOptions{
		YAMLKey: "resource_store", Purpose: "test", Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewResourceStore: %v", err)
	}
	key := "ns/connectors/c1/openapi.yaml"
	if err := store.Put(context.Background(), key, strings.NewReader(specYAML)); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	svc := runtime.NewRuntime(broker.NewDevBroker(), nil, runtime.WithConnectorSpecStore(store)).Services()
	ag := agent.New(agent.AgentConfig{Name: "test-agent"})
	if err := ag.Init(t.Context(), nil, svc); err != nil {
		t.Fatal(err)
	}

	toolsCfg := []any{map[string]any{
		"tool_type":         "openapi",
		"specification_url": "objstore://" + key,
		"base_url":          "https://api.example.com",
	}}
	if _, err := RegisterTools(ag, toolsCfg, svc); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if ag.Tools().GetTool("ping") == nil {
		got := make([]string, 0, len(ag.Tools().ListTools()))
		for _, tl := range ag.Tools().ListTools() {
			got = append(got, tl.Name())
		}
		t.Fatalf("expected 'ping' tool registered from objstore:// spec; got %v", got)
	}
}

func hasBuiltinGroup(tools []any, groupName string) bool {
	for _, item := range tools {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["tool_type"].(string); t != "builtin-group" {
			continue
		}
		if g, _ := m["group_name"].(string); g == groupName {
			return true
		}
	}
	return false
}

func TestEnsureArtifactManagementGroup_RecognizesPlatformToolsetID(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "builtin_artifact_tools"},
	}

	got := EnsureArtifactManagementGroup(tools, nil, "test-agent", slog.Default())

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 — builtin_artifact_tools is the same group", len(got))
	}
	if hasBuiltinGroup(got, "artifact_management") {
		t.Error("must not inject artifact_management alongside its own platform toolset ID")
	}
}

func TestEnsureArtifactManagementGroup_SkipsWhenToolsMaterializedIndividually(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "list_artifacts"},
		map[string]any{"tool_type": "builtin", "tool_name": "load_artifact"},
	}

	got := EnsureArtifactManagementGroup(tools, nil, "test-agent", slog.Default())

	if hasBuiltinGroup(got, "artifact_management") {
		t.Errorf("injected artifact_management over individually-emitted members, re-granting excluded tools: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d, want 2 (unchanged)", len(got))
	}
}

func TestEnsureDataAnalysisGroup_SkipsWhenToolsMaterializedIndividually(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "query_data_with_sql"},
	}

	got := EnsureDataAnalysisGroup(tools, nil, "test-agent", slog.Default())

	if hasBuiltinGroup(got, "data_analysis") {
		t.Errorf("injected data_analysis over an individually-emitted member: %v", got)
	}
}

func TestEnsureBuiltinGroup_StillInjectsForUnrelatedTools(t *testing.T) {
	tools := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
		map[string]any{"tool_type": "builtin-group", "group_name": "builtin_web_request_tools"},
	}

	got := EnsureArtifactManagementGroup(tools, nil, "test-agent", slog.Default())

	if !hasBuiltinGroup(got, "artifact_management") {
		t.Errorf("an unrelated toolset must not suppress artifact auto-injection: %v", got)
	}
}

func TestEnsureBuiltinGroup_OptOutSuppressesEmptyToolList(t *testing.T) {
	optOut := false

	if got := EnsureArtifactManagementGroup([]any{}, &optOut, "test-agent", slog.Default()); len(got) != 0 {
		t.Errorf("artifact injection must be suppressed by the opt-out, got %v", got)
	}
	if got := EnsureDataAnalysisGroup([]any{}, &optOut, "test-agent", slog.Default()); len(got) != 0 {
		t.Errorf("data_analysis injection must be suppressed by the opt-out, got %v", got)
	}

	if got := EnsureArtifactManagementGroup([]any{}, nil, "test-agent", slog.Default()); len(got) != 1 {
		t.Errorf("default auto-injection should still apply for an agent that declares nothing, got %v", got)
	}
}
