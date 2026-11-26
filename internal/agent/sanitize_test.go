// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestSanitizeToolCalls_ValidNames(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "1", Name: "myTool", Arguments: `{}`},
		{ID: "2", Name: "_private", Arguments: `{}`},
		{ID: "3", Name: "tool-name", Arguments: `{}`},
		{ID: "4", Name: "A123_foo-bar", Arguments: `{}`},
		{ID: "5", Name: "_", Arguments: `{}`},
	}
	valid, errs := sanitizeToolCalls(calls, testLogger())
	if len(valid) != len(calls) {
		t.Fatalf("expected all %d calls valid, got %d", len(calls), len(valid))
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d", len(errs))
	}
}

func TestSanitizeToolCalls_InvalidRegex(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "1", Name: "123tool", Arguments: `{}`},
		{ID: "2", Name: "tool name", Arguments: `{}`},
		{ID: "3", Name: "tool.name", Arguments: `{}`},
		{ID: "4", Name: "", Arguments: `{}`},
		{ID: "5", Name: "tool/path", Arguments: `{}`},
		{ID: "6", Name: "-leading", Arguments: `{}`},
	}
	valid, errs := sanitizeToolCalls(calls, testLogger())
	if len(valid) != 0 {
		t.Fatalf("expected 0 valid, got %d", len(valid))
	}
	if len(errs) != len(calls) {
		t.Fatalf("expected %d errors, got %d", len(calls), len(errs))
	}
	for i, e := range errs {
		if e.Role != "tool" {
			t.Errorf("error[%d]: expected role 'tool', got %q", i, e.Role)
		}
		if e.ToolCallID != calls[i].ID {
			t.Errorf("error[%d]: expected ToolCallID %q, got %q", i, calls[i].ID, e.ToolCallID)
		}
		if !strings.Contains(e.Content, "Invalid tool name") {
			t.Errorf("error[%d]: expected error content, got %q", i, e.Content)
		}
	}
}

func TestSanitizeToolCalls_PlaceholderPrefix(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "1", Name: "$FUNCTION_NAME", Arguments: `{}`},
		{ID: "2", Name: "$TOOL_NAME", Arguments: `{}`},
		{ID: "3", Name: "$myTool", Arguments: `{}`},
	}
	valid, errs := sanitizeToolCalls(calls, testLogger())
	if len(valid) != 0 {
		t.Fatalf("expected 0 valid, got %d", len(valid))
	}
	if len(errs) != 3 {
		t.Fatalf("expected 3 errors, got %d", len(errs))
	}
}

func TestSanitizeToolCalls_MixedValidInvalid(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "1", Name: "good_tool", Arguments: `{"x":1}`},
		{ID: "2", Name: "$BAD", Arguments: `{}`},
		{ID: "3", Name: "another_good", Arguments: `{"y":2}`},
		{ID: "4", Name: "123bad", Arguments: `{}`},
	}
	valid, errs := sanitizeToolCalls(calls, testLogger())
	if len(valid) != 2 {
		t.Fatalf("expected 2 valid, got %d", len(valid))
	}
	if valid[0].Name != "good_tool" || valid[1].Name != "another_good" {
		t.Errorf("unexpected valid names: %q, %q", valid[0].Name, valid[1].Name)
	}
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errs))
	}
	if errs[0].ToolCallID != "2" || errs[1].ToolCallID != "4" {
		t.Errorf("unexpected error IDs: %q, %q", errs[0].ToolCallID, errs[1].ToolCallID)
	}
}

func TestSanitizeToolCalls_ErrorMessageFormat(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "call-42", Name: "$FUNCTION_NAME", Arguments: `{}`},
	}
	_, errs := sanitizeToolCalls(calls, testLogger())
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	e := errs[0]
	if e.Role != "tool" {
		t.Errorf("expected role 'tool', got %q", e.Role)
	}
	if e.ToolCallID != "call-42" {
		t.Errorf("expected ToolCallID 'call-42', got %q", e.ToolCallID)
	}
	if e.ToolName != "$FUNCTION_NAME" {
		t.Errorf("expected ToolName '$FUNCTION_NAME', got %q", e.ToolName)
	}
	if !strings.Contains(e.Content, "$FUNCTION_NAME") {
		t.Error("error content should include the invalid name")
	}
	if !strings.Contains(e.Content, "Invalid tool name") {
		t.Error("error content should include 'Invalid tool name'")
	}
}

func TestSanitizeToolCalls_EmptyInput(t *testing.T) {
	valid, errs := sanitizeToolCalls(nil, testLogger())
	if len(valid) != 0 || len(errs) != 0 {
		t.Fatalf("expected empty results for nil input, got valid=%d errs=%d", len(valid), len(errs))
	}
}

func TestValidToolNameRegex(t *testing.T) {
	validNames := []string{
		"a", "Z", "_", "_a", "a1", "my_tool", "My-Tool", "tool_123", "A_b-C",
		"skill__echo", "my-skill__my_tool",
	}
	for _, name := range validNames {
		if !validToolName.MatchString(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}

	invalidNames := []string{
		"", "1a", "-a", "$a", "a b", "a.b", "a/b", "a:b", "a(b", "a)b",
	}
	for _, name := range invalidNames {
		if validToolName.MatchString(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}
