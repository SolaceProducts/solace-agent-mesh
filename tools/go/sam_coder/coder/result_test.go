// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

var verdictSchema = map[string]any{
	"type":     "object",
	"required": []any{"verdict", "reasons"},
	"properties": map[string]any{
		"verdict": map[string]any{"type": "string", "enum": []any{"safe", "unsafe"}},
		"reasons": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"additionalProperties": false,
}

func TestStructuredResultIsValidatedAndRetried(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.go"), "package a\n")
	m := &scriptedModel{replies: []sdk.ChatResult{
		{Content: "It looks safe.", FinishReason: "stop"},
		toolTurn(call("1", submitResultTool, map[string]any{"verdict": "maybe"})),
		toolTurn(call("2", submitResultTool, map[string]any{"verdict": "safe", "reasons": []any{"no API change"}})),
	}}

	res := invokeMode(t, ModePlan, ws, m, map[string]any{"task": "is the bump safe?", "output_schema": verdictSchema})
	if len(m.calls) != 3 {
		t.Fatalf("model called %d times, result = %v", len(m.calls), res)
	}

	offered := false
	for _, tool := range m.opts[0].Tools {
		offered = offered || tool.Name == submitResultTool
	}
	if !offered {
		t.Fatalf("submit_result was not offered")
	}
	if last := m.calls[1][len(m.calls[1])-1]; last.Role != "user" || last.Content != resultNudgePrompt {
		t.Errorf("a text reply was not nudged toward submit_result: %+v", last)
	}
	if got := toolResults(m.calls[2])["1"]; !strings.Contains(got, "does not match the schema") {
		t.Errorf("invalid result returned %q", got)
	}
	data, _ := res["data"].(map[string]any)
	want := map[string]any{"verdict": "safe", "reasons": []any{"no API change"}}
	if fmt.Sprint(data["result"]) != fmt.Sprint(want) || res["status"] == "partial" || data["plan"] != nil {
		t.Errorf("status=%v data=%v", res["status"], data)
	}
}

func TestStructuredResultIsForcedAtStepLimitAndMissingOneIsPartial(t *testing.T) {
	ws := t.TempDir()
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "glob", map[string]any{"pattern": "*"})),
		{Content: "no idea", FinishReason: "stop"},
	}}

	res := invoke(t, ws, m, map[string]any{"task": "assess", "output_schema": verdictSchema, "max_steps": 1})

	final := m.opts[1]
	if final.ToolChoice != "required" || len(final.Tools) != 1 || final.Tools[0].Name != submitResultTool {
		t.Errorf("final step opts = %+v", final)
	}
	data, _ := res["data"].(map[string]any)
	if res["status"] != "partial" || data["result"] != nil {
		t.Errorf("status=%v data=%v", res["status"], data)
	}
}

func TestOutputSchemaMustBeAnObject(t *testing.T) {
	res := invoke(t, t.TempDir(), &scriptedModel{}, map[string]any{"task": "x", "output_schema": map[string]any{"type": "array"}})
	if res["status"] != "error" {
		t.Errorf("non-object schema accepted: %v", res)
	}
}

func TestRepoInstructionsAreReadFromRootDownToSubdir(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "AGENTS.md"), "Use tabs.")
	writeFile(t, filepath.Join(ws, "CLAUDE.md"), "ignored when AGENTS.md exists")
	writeFile(t, filepath.Join(ws, "svc", "CLAUDE.md"), "Run make test in svc.")
	writeFile(t, filepath.Join(ws, "svc", "api", "x.go"), "package api\n")
	m := &scriptedModel{}

	invoke(t, ws, m, map[string]any{"task": "x", "subdir": "svc/api"})

	prompt := m.calls[0][1].Content
	root, svc := strings.Index(prompt, "Use tabs."), strings.Index(prompt, "Run make test in svc.")
	if root < 0 || svc < root || strings.Contains(prompt, "ignored when") {
		t.Errorf("instructions in prompt = %q", prompt)
	}
	if !strings.Contains(prompt, "--- svc/CLAUDE.md ---") || strings.Index(prompt, "Task:") < svc {
		t.Errorf("instructions not labelled or not before the task: %q", prompt)
	}
}

func TestRepoInstructionsAreCapped(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "AGENTS.md"), strings.Repeat("x", repoInstructionsMaxBytes*2))
	got := repoInstructions(ws, ws)
	if len(got) > repoInstructionsMaxBytes+len(repoInstructionsHeader)+64 || !strings.HasSuffix(got, "(instructions truncated)\n") {
		t.Errorf("len=%d", len(got))
	}
}
