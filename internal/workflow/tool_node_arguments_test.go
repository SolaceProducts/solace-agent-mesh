// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"strings"
	"testing"
)

func webRequestApp(input map[string]any, nested bool) map[string]any {
	body := map[string]any{
		"tools":          []any{map[string]any{"tool_type": "builtin", "tool_name": "web_request"}},
		"nodes":          []any{map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request", "input": input}},
		"output_mapping": map[string]any{"result": "{{fetch.output}}"},
	}
	if nested {
		return map[string]any{"name": "wf", "workflow": body}
	}
	return body
}

func TestToolNodeArgumentProblems_RefusesAnUndeclaredBuiltinArgument(t *testing.T) {
	for _, nested := range []bool{false, true} {
		problems, err := ToolNodeArgumentProblems("wf", webRequestApp(map[string]any{"url": "https://example.com", "output_filename": "x.jpg"}, nested), StaticCheckOptions{})
		if err != nil {
			t.Fatalf("nested=%v: parse: %v", nested, err)
		}
		fatal := fatalMessages(problems)
		if len(fatal) != 1 || !strings.Contains(fatal[0], "output_filename") {
			t.Fatalf("nested=%v: want one fatal problem naming output_filename, got %+v", nested, problems)
		}
	}
}

func TestToolNodeArgumentProblems_PassesADeclaredArgument(t *testing.T) {
	problems, err := ToolNodeArgumentProblems("wf", webRequestApp(map[string]any{"url": "https://example.com"}, true), StaticCheckOptions{})
	if err != nil || len(fatalMessages(problems)) != 0 {
		t.Fatalf("want no fatal problem, got %+v, %v", problems, err)
	}
}

func TestToolNodeArgumentProblems_ReturnsTheParseError(t *testing.T) {
	_, err := ToolNodeArgumentProblems("wf", map[string]any{"nodes": "not a list"}, StaticCheckOptions{})
	if err == nil {
		t.Fatal("want the parse error for a body the engine cannot load")
	}
}
