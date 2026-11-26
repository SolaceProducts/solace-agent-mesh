// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"regexp"
	"strings"
	"testing"
)

var validBedrockToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func TestSanitizeToolName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"valid name unchanged", "summer-promos__build_report", "summer-promos__build_report"},
		{"artifact embed mistaken for a tool", "artifact_return:report.md", "artifact_return_report_md"},
		{"placeholder name", "$FUNCTION_NAME", "_FUNCTION_NAME"},
		{"empty", "", invalidToolNamePlaceholder},
		{"non-ascii", "café_tool", "caf__tool"},
		{"only unsafe characters", ":::", "___"},
		{"exactly the limit", strings.Repeat("a", 64), strings.Repeat("a", 64)},
		{"too long", strings.Repeat("a", 80), strings.Repeat("a", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeToolName(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeToolName(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !validBedrockToolName.MatchString(got) {
				t.Errorf("result %q does not match the Bedrock toolUse.name pattern", got)
			}
		})
	}
}

func TestRequestReplaysRejectedToolNameProviderSafe(t *testing.T) {
	sent := captureAnthropicRequestWith(t, "claude-sonnet-5-5", func(req *ChatRequest) {
		req.Messages = []ChatMessage{
			{Role: "user", Content: "Build the report"},
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "toolu_bad", Name: "artifact_return:report.md", Arguments: `{}`},
				{ID: "toolu_ok", Name: "build_report", Arguments: `{}`},
			}},
			{Role: "tool", ToolCallID: "toolu_bad", Content: "Invalid tool name"},
			{Role: "tool", ToolCallID: "toolu_ok", Content: "done"},
		}
	})

	names := map[string]string{}
	messages, _ := sent["messages"].([]any)
	for _, m := range messages {
		blocks, _ := m.(map[string]any)["content"].([]any)
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			if block["type"] != "tool_use" {
				continue
			}
			id, _ := block["id"].(string)
			names[id], _ = block["name"].(string)
		}
	}

	if got := names["toolu_bad"]; !validBedrockToolName.MatchString(got) {
		t.Errorf("rejected call sent with name %q, want a provider-safe name", got)
	}
	if got := names["toolu_ok"]; got != "build_report" {
		t.Errorf("valid call sent with name %q, want it unchanged", got)
	}
}
