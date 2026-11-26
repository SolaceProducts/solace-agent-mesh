// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func TestValidateSkillToolsBlock_AcceptsValidShapes(t *testing.T) {
	cases := []map[string]any{
		{"tool_type": "builtin", "tool_name": "convert_file_to_markdown"},
		{"tool_type": "builtin-group", "group_name": "image_tools"},
		{
			"tool_type": "mcp",
			"connection_params": map[string]any{
				"type":    "stdio",
				"command": []any{"echo"},
			},
		},
		{
			"tool_type":         "openapi",
			"specification_url": "https://example.com/openapi.json",
		},
	}
	for i, c := range cases {
		details := validateSkillToolsBlock([]map[string]any{c})
		if len(details) != 0 {
			t.Errorf("case %d: got details %v, want none", i, details)
		}
	}
}

func TestValidateSkillToolsBlock_RejectsBadEntries(t *testing.T) {
	tests := []struct {
		name     string
		entry    map[string]any
		wantSnip string
	}{
		{"missing type", map[string]any{"tool_name": "x"}, "tool_type is required"},
		{"unknown type", map[string]any{"tool_type": "unknown"}, "unsupported tool_type"},
		{"builtin missing name", map[string]any{"tool_type": "builtin"}, "tool_name is required"},
		{"builtin-group missing group_name", map[string]any{"tool_type": "builtin-group"}, "group_name is required"},
		{"mcp missing connection_params", map[string]any{"tool_type": "mcp"}, "connection_params"},
		{"openapi missing spec", map[string]any{"tool_type": "openapi"}, "specification_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := validateSkillToolsBlock([]map[string]any{tt.entry})
			if len(details) == 0 {
				t.Fatalf("expected validation failure, got none for %v", tt.entry)
			}
			msgs := details["tools[0]"]
			if len(msgs) == 0 {
				t.Fatalf("no error under tools[0]: %v", details)
			}
			joined := strings.Join(msgs, "\n")
			if !strings.Contains(joined, tt.wantSnip) {
				t.Errorf("error %q does not contain %q", joined, tt.wantSnip)
			}
		})
	}
}

func TestValidateSkillToolsBlock_KeyedByEntryIndex(t *testing.T) {
	tools := []map[string]any{
		{"tool_type": "builtin", "tool_name": "ok"},
		{"tool_type": "builtin"},
		{"tool_type": "openapi"},
	}
	details := validateSkillToolsBlock(tools)
	if _, ok := details["tools[0]"]; ok {
		t.Errorf("tools[0] is valid but reported errors: %v", details["tools[0]"])
	}
	if _, ok := details["tools[1]"]; !ok {
		t.Errorf("tools[1] should report missing tool_name")
	}
	if _, ok := details["tools[2]"]; !ok {
		t.Errorf("tools[2] should report missing spec")
	}
}

func TestValidateSkillToolsBlock_NilOrEmpty(t *testing.T) {
	if details := validateSkillToolsBlock(nil); details != nil {
		t.Errorf("nil tools returned details: %v", details)
	}
	if details := validateSkillToolsBlock([]map[string]any{}); details != nil {
		t.Errorf("empty tools returned details: %v", details)
	}
}

func TestValidateSkillZIP_RejectsBadToolsBlock(t *testing.T) {
	zipData := buildSkillZIPWithContent(t, `---
name: demo
tools:
  - tool_type: builtin
    tool_name: ok
  - tool_type: weird
---
Body.
`)
	_, err := validateSkillZIP(zipData, "demo")
	if err == nil {
		t.Fatal("expected validation error for bad tools block")
	}
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err type = %T, want *ValidationError; err=%v", err, err)
	}
	if valErr.Details == nil {
		t.Errorf("ValidationError.Details should be populated, got nil")
	}
	if _, ok := valErr.Details["tools[1]"]; !ok {
		t.Errorf("expected validationDetails to flag tools[1], got %v", valErr.Details)
	}
}
