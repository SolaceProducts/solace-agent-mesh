// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func lookupTool(rows string) HostTool {
	return HostTool{
		Spec: sdk.ChatTool{Name: "lookup", Description: "look things up", Parameters: obj(map[string]any{"q": str("query")}, "q")},
		Handle: func(_ context.Context, args string) (string, error) {
			if strings.Contains(args, "big") {
				return strings.Repeat(rows, hostOutputMaxBytes/len(rows)+1), nil
			}
			return "answer for " + args, nil
		},
	}
}

func TestHostToolsAreOfferedAndLongOutputGoesToAFileUnderTheStateDir(t *testing.T) {
	dir := t.TempDir()
	saved := hostOutputDirName + "/lookup-1.txt"
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "lookup", map[string]any{"q": "small"}), call("2", "lookup", map[string]any{"q": "big"})),
		toolTurn(
			call("3", "glob", map[string]any{"pattern": "**/*.txt"}),
			call("4", "grep", map[string]any{"pattern": "row 1234567890", "path": saved}),
			call("5", "edit", map[string]any{"path": saved, "old_string": "row", "new_string": "forged"}),
			call("6", "write", map[string]any{"path": saved, "content": "forged"}),
			call("7", "changes", map[string]any{}),
		),
		{Content: "done", FinishReason: "stop"},
	}}

	out, err := Run(context.Background(), Options{Dir: dir, Task: "x", NoExec: true, HostTools: []HostTool{lookupTool("row 1234567890\n")}, Chat: m.chat})
	if err != nil {
		t.Fatal(err)
	}

	offered := false
	for _, tool := range m.opts[0].Tools {
		offered = offered || tool.Name == "lookup"
	}
	if !offered {
		t.Errorf("lookup was not offered: %+v", m.opts[0].Tools)
	}
	results := toolResults(m.calls[1])
	if results["1"] != `answer for {"q":"small"}` {
		t.Errorf("small result = %q", results["1"])
	}
	if len(results["2"]) > hostPreviewBytes+200 || !strings.Contains(results["2"], saved) {
		t.Errorf("big result was not saved to a file: %d bytes, tail %q", len(results["2"]), results["2"][max(len(results["2"])-120, 0):])
	}
	content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(saved)))
	if err != nil || len(content) <= hostOutputMaxBytes || strings.Contains(string(content), "forged") {
		t.Errorf("saved output: %d bytes, %v", len(content), err)
	}
	later := toolResults(m.calls[2])
	if strings.Contains(later["3"], "lookup-1.txt") {
		t.Errorf("glob listed the saved output: %q", later["3"])
	}
	if !strings.Contains(later["4"], "row 1234567890") {
		t.Errorf("grep of the saved output = %q", later["4"][:min(len(later["4"]), 200)])
	}
	for _, id := range []string{"5", "6"} {
		if !strings.Contains(later[id], "saved host tool output") {
			t.Errorf("call %s changed the saved output: %q", id, later[id])
		}
	}
	if later["7"] != "no changes yet" || len(out.Changes.Files) != 0 {
		t.Errorf("the saved output shows as a change: %q, %+v", later["7"], out.Changes)
	}
}

func TestRecordGetsEveryTurnAndTheFullToolResults(t *testing.T) {
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "lookup", map[string]any{"q": "big"})),
		{Content: "done", FinishReason: "stop"},
	}}
	var events []Event
	if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", Mode: ModePlan, NoShell: true,
		HostTools: []HostTool{lookupTool("row\n")}, Chat: m.chat, Record: func(e Event) { events = append(events, e) }}); err != nil {
		t.Fatal(err)
	}

	if len(events) != 3 || events[0].Kind != EventModel || events[1].Kind != EventTool || events[2].Kind != EventModel {
		t.Fatalf("events = %+v", events)
	}
	if tool := events[1]; tool.Tool != "lookup" || tool.Step != 1 || !strings.Contains(tool.Result, hostOutputDirName+"/lookup-1.txt") {
		t.Errorf("tool event = %+v", tool)
	}
	if events[0].Usage == nil || len(events[0].ToolCalls) != 1 || events[2].Content != "done" {
		t.Errorf("model events = %+v, %+v", events[0], events[2])
	}
}

const hostOutputDirName = stateDirName + "/" + hostOutputDir

func TestHostToolsMayNotShadowOtherTools(t *testing.T) {
	shadow := lookupTool("x")
	shadow.Spec.Name = "bash"
	for name, tools := range map[string][]HostTool{
		"built-in": {shadow},
		"twice":    {lookupTool("x"), lookupTool("y")},
	} {
		if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", HostTools: tools, Chat: (&scriptedModel{}).chat}); err == nil {
			t.Errorf("%s: Run accepted the host tools", name)
		}
	}
}

func TestSkillsAreListedAndReadableButNotEditable(t *testing.T) {
	dir := t.TempDir()
	skills, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skills, "triage", skillFile)
	writeFile(t, skillPath, "---\nname: triage\ndescription: How to triage a failing agent.\n---\n# Triage\nCount first.\n")
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(
			call("1", "read", map[string]any{"path": skillPath}),
			call("2", "edit", map[string]any{"path": skillPath, "old_string": "Count first.", "new_string": "Guess."}),
		),
		{Content: "done", FinishReason: "stop"},
	}}

	if _, err := Run(context.Background(), Options{Dir: dir, Task: "x", NoShell: true, Skills: []string{filepath.Join(skills, "triage")}, Chat: m.chat}); err != nil {
		t.Fatal(err)
	}

	if prompt := m.calls[0][1].Content; !strings.Contains(prompt, "- triage: How to triage a failing agent. ("+skillPath+")") {
		t.Errorf("prompt does not list the skill: %q", prompt)
	}
	results := toolResults(m.calls[1])
	if !strings.Contains(results["1"], "Count first.") || !strings.Contains(results["2"], "outside the workspace") {
		t.Errorf("read = %q, edit = %q", results["1"], results["2"])
	}
}

func TestASkillWithoutADescriptionIsRefused(t *testing.T) {
	skills := t.TempDir()
	writeFile(t, filepath.Join(skills, "bare", skillFile), "# Bare\n")
	if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", Skills: []string{filepath.Join(skills, "bare")}, Chat: (&scriptedModel{}).chat}); err == nil {
		t.Error("Run accepted a skill with no description")
	}
}
