// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type scriptedModel struct {
	replies []sdk.ChatResult
	errs    map[int]error
	calls   [][]sdk.ChatMessage
	opts    []sdk.ChatOptions
}

func (m *scriptedModel) chat(_ context.Context, msgs []sdk.ChatMessage, opts sdk.ChatOptions) (*sdk.ChatResult, error) {
	n := len(m.calls)
	m.calls = append(m.calls, append([]sdk.ChatMessage(nil), msgs...))
	m.opts = append(m.opts, opts)
	if err := m.errs[n]; err != nil {
		return nil, err
	}
	if n >= len(m.replies) {
		return &sdk.ChatResult{Content: "done", FinishReason: "stop"}, nil
	}
	r := m.replies[n]
	return &r, nil
}

func call(id, name string, args map[string]any) sdk.ChatToolCall {
	b, _ := json.Marshal(args)
	return sdk.ChatToolCall{ID: id, Name: name, Arguments: string(b)}
}

func toolTurn(calls ...sdk.ChatToolCall) sdk.ChatResult {
	return sdk.ChatResult{ToolCalls: calls, FinishReason: "tool_calls", Usage: sdk.ChatUsage{TotalTokens: 10}}
}

func invoke(t *testing.T, ws string, m *scriptedModel, params map[string]any) map[string]any {
	t.Helper()
	return invokeMode(t, ModeImplement, ws, m, params)
}

func invokeMode(t *testing.T, mode Mode, ws string, m *scriptedModel, params map[string]any) map[string]any {
	t.Helper()
	text := func(k string) string { v, _ := params[k].(string); return v }
	steps, _ := params["max_steps"].(int)
	reset, _ := params["reset"].(bool)
	schema, _ := params["output_schema"].(map[string]any)
	out, err := Run(context.Background(), Options{
		Mode: mode, Dir: filepath.Join(ws, text("subdir")), RepoRoot: ws,
		Task: text("task"), VerifyCommand: text("verify_command"),
		Plan: text("plan"), Reset: reset, MaxSteps: steps, OutputSchema: schema,
		Chat: m.chat,
	})
	var res *sdk.Result
	if err == nil {
		res = runResult(out, runData(out), schema != nil)
	} else {
		res = sdk.Error(err.Error())
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshaling result: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("re-parsing result: %v", err)
	}
	return wire
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSamCoderEditsRunsVerifyAndReturnsDiff(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "calc.sh"), "add() {\n  echo $(( $1 - $2 ))\n}\n")
	writeFile(t, filepath.Join(ws, ".gitignore"), "out/\n")
	writeFile(t, filepath.Join(ws, "out", "ignored.txt"), "x")

	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "grep", map[string]any{"pattern": `\$1 - \$2`})),
		toolTurn(call("2", "read", map[string]any{"path": "calc.sh"})),
		toolTurn(call("3", "edit", map[string]any{"path": "calc.sh", "old_string": "echo $(( $1 - $2 ))", "new_string": "echo $(( $1 + $2 ))"})),
		toolTurn(call("4", "write", map[string]any{"path": "test.sh", "content": ". ./calc.sh\n[ \"$(add 2 3)\" = 5 ]\n"})),
		toolTurn(call("5", "bash", map[string]any{"command": "bash test.sh && echo PASS"})),
		toolTurn(call("6", "changes", map[string]any{})),
		{Content: "Fixed add to sum its arguments and added test.sh.", FinishReason: "stop"},
	}}

	res := invoke(t, ws, m, map[string]any{"task": "add() subtracts; make it add", "verify_command": "bash test.sh"})

	data, _ := res["data"].(map[string]any)
	if data["stop_reason"] != stopCompleted {
		t.Fatalf("stop_reason = %v, data = %v", data["stop_reason"], data)
	}
	diff, _ := data["diff"].(string)
	if !strings.Contains(diff, "+  echo $(( $1 + $2 ))") || !strings.Contains(diff, "b/test.sh") {
		t.Errorf("diff missing the edit or the new file:\n%s", diff)
	}
	if strings.Contains(diff, "ignored.txt") {
		t.Errorf("diff includes a gitignored file:\n%s", diff)
	}
	verify, _ := data["verify"].(map[string]any)
	if verify["exit_code"] != float64(0) {
		t.Errorf("verify = %v", verify)
	}
	toolMsgs := toolResults(m.calls[len(m.calls)-1])
	if !strings.Contains(toolMsgs["1"], "calc.sh:2:") || !strings.Contains(toolMsgs["2"], "2:   echo") || !strings.Contains(toolMsgs["5"], "PASS") {
		t.Errorf("tool results the model saw: %v", toolMsgs)
	}
	if c := toolMsgs["6"]; !strings.Contains(c, "M calc.sh") || !strings.Contains(c, "A test.sh") || !strings.Contains(c, "+  echo $(( $1 + $2 ))") {
		t.Errorf("changes tool output:\n%s", c)
	}
	if !strings.Contains(m.calls[0][1].Content, "bash test.sh") {
		t.Errorf("task prompt does not carry the verify command: %q", m.calls[0][1].Content)
	}
	if _, err := os.Stat(filepath.Join(ws, ".git")); err == nil {
		t.Errorf("baseline leaked a .git into the workspace")
	}
}

func toolResults(msgs []sdk.ChatMessage) map[string]string {
	out := map[string]string{}
	for _, m := range msgs {
		if m.Role == "tool" {
			out[m.ToolCallID] = m.Content
		}
	}
	return out
}

func TestSamCoderForcesReportAtStepLimit(t *testing.T) {
	ws := t.TempDir()
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "glob", map[string]any{"pattern": "**/*"})),
		toolTurn(call("2", "glob", map[string]any{"pattern": "**/*"})),
		{Content: "Ran out of steps before finding anything.", FinishReason: "stop"},
	}}

	res := invoke(t, ws, m, map[string]any{"task": "look around", "max_steps": 2})

	data, _ := res["data"].(map[string]any)
	if data["stop_reason"] != stopMaxSteps {
		t.Fatalf("stop_reason = %v", data["stop_reason"])
	}
	last := m.opts[len(m.opts)-1]
	if last.ToolChoice != "none" || m.calls[len(m.calls)-1][len(m.calls[len(m.calls)-1])-1].Content != maxStepsPrompt {
		t.Errorf("final call did not withhold tools: %+v", last)
	}
}

func TestSamCoderStopsOnBudgetAndStillReturnsDiff(t *testing.T) {
	ws := t.TempDir()
	m := &scriptedModel{
		replies: []sdk.ChatResult{toolTurn(call("1", "write", map[string]any{"path": "a.txt", "content": "hi\n"}))},
		errs:    map[int]error{1: &sdk.LLMError{Category: sdk.ChatErrorBudgetExhausted, Message: "budget"}},
	}

	res := invoke(t, ws, m, map[string]any{"task": "write a file"})

	data, _ := res["data"].(map[string]any)
	if data["stop_reason"] != stopBudgetExhausted || res["status"] != "partial" {
		t.Fatalf("status=%v stop_reason=%v", res["status"], data["stop_reason"])
	}
	if !strings.Contains(data["diff"].(string), "+hi") {
		t.Errorf("diff = %q", data["diff"])
	}
}

func TestSamCoderRetriesContextOverflowWithElidedHistory(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "big.txt"), strings.Repeat("line\n", 1000))
	m := &scriptedModel{
		replies: []sdk.ChatResult{
			toolTurn(call("1", "read", map[string]any{"path": "big.txt"})),
			toolTurn(call("2", "read", map[string]any{"path": "big.txt"})),
		},
		errs: map[int]error{2: &sdk.LLMError{Category: sdk.ChatErrorContextOverflow}},
	}

	invoke(t, ws, m, map[string]any{"task": "read twice"})

	retried := toolResults(m.calls[3])
	if retried["1"] != elidedToolResult || retried["2"] == elidedToolResult {
		t.Errorf("retry history = %v", retried)
	}
}

func TestWorkspaceRefusesEscapes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	ws := &workspace{root: root}
	for _, p := range []string{"../x", "/etc/passwd", "link/new.txt"} {
		if _, err := ws.resolve(p); err == nil {
			t.Errorf("resolve(%q) allowed an escape", p)
		}
	}
	if _, err := ws.resolve("sub/new.txt"); err != nil {
		t.Errorf("resolve of a new nested path: %v", err)
	}
}

func TestReplaceInContentTiers(t *testing.T) {
	cases := []struct {
		name, content, old, new, want, wantErr string
		all                                    bool
	}{
		{name: "exact", content: "a := 1\n", old: "a := 1", new: "a := 2", want: "a := 2\n"},
		{name: "smart quotes in file", content: "msg := “hello”\n", old: `msg := "hello"`, new: `msg := "bye"`, want: "msg := \"bye\"\n"},
		{name: "trailing whitespace in file", content: "if x {   \n\treturn\n}\n", old: "if x {\n\treturn\n}", new: "if y {\n\treturn\n}", want: "if y {\n\treturn\n}\n"},
		{name: "ambiguous", content: "x\nx\n", old: "x", new: "y", wantErr: "matches 2 places"},
		{name: "replace all", content: "x\nx\n", old: "x", new: "y", all: true, want: "y\ny\n"},
		{name: "miss hint", content: "func a() {\n\treturn 1\n}\n", old: "func a() {\n\treturn 2\n}", new: "z", wantErr: "first line appears at line 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := replaceInContent(tc.content, tc.old, tc.new, tc.all)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestGlobToRegexp(t *testing.T) {
	cases := map[string]map[string]bool{
		"**/*_test.go":  {"a_test.go": true, "x/y/a_test.go": true, "a.go": false},
		"cmd/*/main.go": {"cmd/sam/main.go": true, "cmd/a/b/main.go": false},
		"*.{ts,tsx}":    {"a.ts": true, "a.tsx": true, "d/a.ts": false},
	}
	for pattern, paths := range cases {
		re, err := globToRegexp(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for p, want := range paths {
			if re.MatchString(p) != want {
				t.Errorf("%s ~ %s = %v, want %v", pattern, p, !want, want)
			}
		}
	}
}

func TestRunShellTimesOutAndKillsChildren(t *testing.T) {
	res := runShell(context.Background(), t.TempDir(), "sleep 30 & sleep 30; echo never", 1, nil)
	if !res.TimedOut || strings.Contains(res.Output, "never") {
		t.Errorf("res = %+v", res)
	}
}

func TestPlannerReadsOnlyAndReturnsThePlan(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.go"), "package a\n")
	plan := "## Goal\nx\n## Files\n- `a.go`: change it\n## Steps\n- [ ] edit a.go"
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "write", map[string]any{"path": "a.go", "content": "package b\n"})),
		{Content: plan, FinishReason: "stop"},
	}}

	res := invokeMode(t, ModePlan, ws, m, map[string]any{"task": "rename the package", "verify_command": "go test ./..."})

	for _, tool := range m.opts[0].Tools {
		if planWithheld[tool.Name] {
			t.Errorf("planner offered %s", tool.Name)
		}
	}
	if got := toolResults(m.calls[1])["1"]; !strings.Contains(got, "not available while planning") {
		t.Errorf("write while planning returned %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "a.go")); string(got) != "package a\n" {
		t.Errorf("planner changed a.go: %q", got)
	}
	data, _ := res["data"].(map[string]any)
	if data["plan"] != plan || data["verify"] != nil || data["diff"] != nil {
		t.Errorf("data = %v", data)
	}
	if _, err := os.Stat(stateDir(ws)); err == nil {
		t.Errorf("planner wrote state into a workspace it must treat as read-only")
	}
	if !strings.Contains(m.calls[0][1].Content, "The caller will check the change with:\ngo test ./...") {
		t.Errorf("planner prompt = %q", m.calls[0][1].Content)
	}
}

func TestImplementFollowsThePlanAndChecksTheDiffAgainstIt(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.go"), "package a\n")
	writeFile(t, filepath.Join(ws, "tools", "x", "manifest.yaml"), "v: 1\n")
	plan := "## Files\n- `a.go`: rename\n- `b.go`: new\n## Steps\n- [ ] rename"
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "edit", map[string]any{"path": "a.go", "old_string": "package a", "new_string": "package b"})),
		toolTurn(call("2", "edit", map[string]any{"path": "tools/x/manifest.yaml", "old_string": "v: 1", "new_string": "v: 2"})),
		toolTurn(call("3", "edit", map[string]any{"path": planRelPath, "old_string": "- [ ] rename", "new_string": "- [x] rename"})),
		{Content: "done", FinishReason: "stop"},
	}}

	res := invoke(t, ws, m, map[string]any{"task": "rename the package", "plan": plan})

	if got, _ := os.ReadFile(filepath.Join(stateDir(ws), ".gitignore")); string(got) != "*\n" {
		t.Errorf("state dir .gitignore = %q", got)
	}
	if !strings.Contains(m.calls[0][1].Content, "- [ ] rename") {
		t.Errorf("task prompt does not carry the plan: %q", m.calls[0][1].Content)
	}
	data, _ := res["data"].(map[string]any)
	if strings.Contains(data["diff"].(string), stateDirName) {
		t.Errorf("diff includes the state dir:\n%s", data["diff"])
	}
	pc, _ := data["plan_check"].(map[string]any)
	if fmt.Sprint(pc["unplanned_files"]) != "[tools/x/manifest.yaml]" ||
		fmt.Sprint(pc["planned_untouched"]) != "[b.go]" ||
		fmt.Sprint(pc["manifest_changed"]) != "[tools/x/manifest.yaml]" {
		t.Errorf("plan_check = %v", pc)
	}
	if !strings.Contains(readPlan(ws), "- [x] rename") {
		t.Errorf("tick did not land in the plan file: %q", readPlan(ws))
	}
}

func TestDiffIsCumulativeAcrossCallsUntilReset(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.txt"), "one\n")
	first := &scriptedModel{replies: []sdk.ChatResult{toolTurn(call("1", "write", map[string]any{"path": "a.txt", "content": "two\n"}))}}
	invoke(t, ws, first, map[string]any{"task": "first"})

	second := &scriptedModel{replies: []sdk.ChatResult{toolTurn(call("1", "write", map[string]any{"path": "b.txt", "content": "new\n"}))}}
	data, _ := invoke(t, ws, second, map[string]any{"task": "continue"})["data"].(map[string]any)
	if diff := data["diff"].(string); !strings.Contains(diff, "+two") || !strings.Contains(diff, "b/b.txt") {
		t.Errorf("second call's diff is not cumulative:\n%s", diff)
	}

	third := &scriptedModel{}
	data, _ = invoke(t, ws, third, map[string]any{"task": "fresh", "reset": true})["data"].(map[string]any)
	if diff := data["diff"].(string); diff != "" {
		t.Errorf("diff after reset = %q", diff)
	}
}

func TestPlanIsRepinnedAfterElision(t *testing.T) {
	ws := t.TempDir()
	if err := writePlan(ws, "## Steps\n- [ ] keep me in view"); err != nil {
		t.Fatal(err)
	}
	msgs := []sdk.ChatMessage{
		{Role: "user", Content: "task"},
		{Role: "tool", Content: strings.Repeat("x", 100)},
		{Role: "tool", Content: strings.Repeat("y", 100)},
	}
	if !elideOldToolResults(msgs, 50, 50, 1) {
		t.Fatal("expected an elision")
	}
	msgs = repinPlan(msgs, ws)
	msgs = repinPlan(msgs, ws)
	last := msgs[len(msgs)-1].Content
	if !strings.HasPrefix(last, pinnedPlanHeader) || !strings.Contains(last, "keep me in view") {
		t.Errorf("last message = %q", last)
	}
	if strings.Contains(msgs[len(msgs)-2].Content, "keep me in view") {
		t.Errorf("earlier pin was not superseded: %q", msgs[len(msgs)-2].Content)
	}
}

func TestElisionCutsWellBelowTheBudgetSoLaterTurnsKeepTheCache(t *testing.T) {
	msgs := []sdk.ChatMessage{{Role: "user", Content: "task"}}
	for range 10 {
		msgs = append(msgs, sdk.ChatMessage{Role: "tool", Content: strings.Repeat("x", 400)})
	}

	if !elideOldToolResults(msgs, 3600, 2000, 2) {
		t.Fatal("expected an elision once the history passed the budget")
	}
	elided := 0
	for _, m := range msgs {
		if m.Content == elidedToolResult {
			elided++
		}
	}
	if elided < 6 {
		t.Errorf("elided %d results; cutting to the target needs at least 6", elided)
	}
	if msgs[9].Content == elidedToolResult || msgs[10].Content == elidedToolResult {
		t.Error("the newest results were elided")
	}

	msgs = append(msgs, sdk.ChatMessage{Role: "tool", Content: strings.Repeat("z", 400)})
	if elideOldToolResults(msgs, 3600, 2000, 2) {
		t.Error("one more result after a cut elided again; the next turn should reuse the cached prefix")
	}
}

func TestSearchFollowsGitignoreInACheckout(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".gitignore"), "out/\n")
	writeFile(t, filepath.Join(root, "build", "flags.go"), "package build // needle\n")
	writeFile(t, filepath.Join(root, "out", "gen.go"), "package out // needle\n")
	if out, gitErr := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); gitErr != nil {
		t.Fatalf("git init: %v: %s", gitErr, out)
	}
	w := &workspace{root: root}

	grep, err := w.grep(context.Background(), "needle", "", "")
	if err != nil {
		t.Fatal(err)
	}
	glob, err := w.glob(context.Background(), "**/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{"grep": grep, "glob": glob} {
		if !strings.Contains(got, "build/flags.go") {
			t.Errorf("%s missed source under build/: %q", name, got)
		}
		if strings.Contains(got, "out/gen.go") {
			t.Errorf("%s searched a gitignored directory: %q", name, got)
		}
	}
}

func TestWriteWarnsWhenTheFileNoLongerParses(t *testing.T) {
	ws := t.TempDir()
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "write", map[string]any{"path": "ok.go", "content": "package a\n\nfunc A() {}\n"})),
		toolTurn(call("2", "write", map[string]any{"path": "bad.go", "content": "package a\n\nfunc B() {\n"})),
		toolTurn(call("3", "write", map[string]any{"path": "bad.json", "content": `{"a": }`})),
		toolTurn(call("4", "write", map[string]any{"path": "notes.txt", "content": "func {"})),
	}}

	invoke(t, ws, m, map[string]any{"task": "write some files"})

	results := toolResults(m.calls[len(m.calls)-1])
	for id, wantWarning := range map[string]bool{"1": false, "2": true, "3": true, "4": false} {
		if got := strings.Contains(results[id], "no longer parses"); got != wantWarning {
			t.Errorf("result %s = %q, want a parse warning: %v", id, results[id], wantWarning)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(ws, "bad.go")); string(got) != "package a\n\nfunc B() {\n" {
		t.Errorf("a write that does not parse should still land, got %q", got)
	}
}

func TestFailingVerifyGoesBackToTheModel(t *testing.T) {
	ws := t.TempDir()
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "write", map[string]any{"path": "a.txt", "content": "a\n"})),
		{Content: "done, I think", FinishReason: "stop"},
		toolTurn(call("2", "write", map[string]any{"path": "ok.txt", "content": "ok\n"})),
		{Content: "now it passes", FinishReason: "stop"},
	}}

	res := invoke(t, ws, m, map[string]any{"task": "make the check pass", "verify_command": "test -f ok.txt"})

	data, _ := res["data"].(map[string]any)
	verify, _ := data["verify"].(map[string]any)
	if verify["exit_code"] != float64(0) || data["verify_rounds"] != float64(1) || data["steps"] != float64(4) {
		t.Fatalf("verify=%v verify_rounds=%v steps=%v", verify, data["verify_rounds"], data["steps"])
	}
	if data["report"] != "now it passes" {
		t.Errorf("report = %v, want the last round's", data["report"])
	}
	followUp := m.calls[2][len(m.calls[2])-1]
	if followUp.Role != "user" || !strings.Contains(followUp.Content, "verify command fails") || !strings.Contains(followUp.Content, "test -f ok.txt") {
		t.Errorf("follow-up = %+v", followUp)
	}
}

func TestFailingVerifyIsRetriedAtMostTwice(t *testing.T) {
	ws := t.TempDir()
	m := &scriptedModel{}

	res := invoke(t, ws, m, map[string]any{"task": "cannot pass", "verify_command": "false"})

	data, _ := res["data"].(map[string]any)
	if data["verify_rounds"] != float64(verifyRetries) || len(m.calls) != verifyRetries+1 {
		t.Errorf("verify_rounds=%v calls=%d", data["verify_rounds"], len(m.calls))
	}
}

func TestThirdIdenticalFailingCallGetsANote(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.txt"), "hello\n")
	stuck := map[string]any{"path": "a.txt", "old_string": "goodbye", "new_string": "bye"}
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "edit", stuck)),
		toolTurn(call("2", "edit", stuck)),
		toolTurn(call("3", "edit", stuck)),
	}}

	invoke(t, ws, m, map[string]any{"task": "edit a.txt"})

	results := toolResults(m.calls[len(m.calls)-1])
	if strings.Contains(results["2"], "Change your approach") || !strings.Contains(results["3"], "Change your approach") {
		t.Errorf("results = %v", results)
	}
}

func TestStepsLeftWarningBeforeTheLimit(t *testing.T) {
	ws := t.TempDir()
	var replies []sdk.ChatResult
	for i := range 20 {
		replies = append(replies, toolTurn(call(fmt.Sprint(i+1), "glob", map[string]any{"pattern": fmt.Sprintf("*.%d", i)})))
	}
	m := &scriptedModel{replies: replies}

	invoke(t, ws, m, map[string]any{"task": "look around", "max_steps": 20})

	results := toolResults(m.calls[len(m.calls)-1])
	if !strings.Contains(results["15"], "5 steps left") || strings.Contains(results["14"], "5 steps left") {
		t.Errorf("result 14 = %q, result 15 = %q", results["14"], results["15"])
	}
}

func TestMultiEditAppliesAllOrNothing(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "f.go")
	writeFile(t, path, "a\nb\nc\n")
	w := &workspace{root: root}

	if _, err := w.edit("f.go", []editOp{{OldString: "a", NewString: "A"}, {OldString: "zzz", NewString: "Z"}}); err == nil || !strings.Contains(err.Error(), "edits[1]") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "a\nb\nc\n" {
		t.Fatalf("partial edit was written: %q", got)
	}
	if _, err := w.edit("f.go", []editOp{{OldString: "a", NewString: "A"}, {OldString: "c", NewString: "C"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "A\nb\nC\n" {
		t.Fatalf("got %q", got)
	}
}

func TestVerifyRetriesShareTheRunsStepLimit(t *testing.T) {
	bash := func(id string) sdk.ChatToolCall { return call(id, "bash", map[string]any{"command": "true"}) }
	stop := sdk.ChatResult{Content: "done", FinishReason: "stop"}
	m := &scriptedModel{replies: []sdk.ChatResult{toolTurn(bash("1")), toolTurn(bash("2")), stop, toolTurn(bash("3")), stop}}

	out, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "cannot pass", VerifyCommand: "false", MaxSteps: 6, Chat: m.chat})
	if err != nil {
		t.Fatal(err)
	}
	if out.VerifyRounds != verifyRetries || out.Steps != 6 {
		t.Errorf("verify_rounds=%d steps=%d, want %d rounds within 6 steps", out.VerifyRounds, out.Steps, verifyRetries)
	}
}

func TestOutcomeSplitsModelTimeFromToolTime(t *testing.T) {
	m := &scriptedModel{replies: []sdk.ChatResult{toolTurn(call("1", "bash", map[string]any{"command": "sleep 0.05"}))}}
	slowModel := func(ctx context.Context, msgs []sdk.ChatMessage, opts sdk.ChatOptions) (*sdk.ChatResult, error) {
		time.Sleep(20 * time.Millisecond)
		return m.chat(ctx, msgs, opts)
	}

	out, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "look", VerifyCommand: "true", Chat: slowModel})
	if err != nil {
		t.Fatal(err)
	}
	if out.ModelTime < 40*time.Millisecond {
		t.Errorf("model time %v, want at least two 20ms turns", out.ModelTime)
	}
	if out.ToolTime["bash"] < 50*time.Millisecond || out.ToolTime[verifyToolTime] <= 0 {
		t.Errorf("tool time %v", out.ToolTime)
	}
	timing := timingData(out)
	if _, ok := timing["tool_seconds"].(map[string]float64)["bash"]; !ok {
		t.Errorf("timing data %v", timing)
	}
}
