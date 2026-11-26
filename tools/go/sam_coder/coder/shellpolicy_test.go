// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestNoShellWithholdsBashInEveryMode(t *testing.T) {
	for _, mode := range []Mode{ModeImplement, ModePlan} {
		dir := t.TempDir()
		m := &scriptedModel{replies: []sdk.ChatResult{toolTurn(call("1", "bash", map[string]any{"command": "touch pwned"}))}}
		if _, err := Run(context.Background(), Options{Mode: mode, Dir: dir, Task: "look", NoShell: true, Chat: m.chat}); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(toolNames(m.opts[0].Tools), "bash") {
			t.Errorf("%s: bash offered with NoShell", mode)
		}
		if !strings.Contains(m.calls[0][1].Content, "no shell in this run") {
			t.Errorf("%s: prompt lacks the no-shell note", mode)
		}
		if got := toolResults(m.calls[1])["1"]; !strings.Contains(got, "no shell") {
			t.Errorf("%s: bash call result = %q", mode, got)
		}
		if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
			t.Errorf("%s: bash ran with NoShell", mode)
		}
	}
}

func TestShellAllowRunsOnlyListedCommandsWithoutAShell(t *testing.T) {
	dir := t.TempDir()
	ws := &workspace{root: dir, shell: newShellPolicy(false, []string{"echo", "go version"})}
	bash := func(command string) string {
		return ws.dispatch(context.Background(), call("1", "bash", map[string]any{"command": command}))
	}

	if out := bash("echo 'a;b' \"c d\""); !strings.Contains(out, "a;b c d") {
		t.Errorf("allowed command output = %q", out)
	}
	refused := []string{
		"ls",
		"go env",
		"echo hi; touch pwned",
		"echo hi && touch pwned",
		"echo $(touch pwned)",
		"echo `touch pwned`",
		"echo \"$(touch pwned)\"",
		"echo hi > pwned",
		"echo hi | tee pwned",
		"echo 'unterminated",
	}
	for _, command := range refused {
		if out := bash(command); !strings.HasPrefix(out, "error:") {
			t.Errorf("%q ran: %q", command, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("a refused command created a file")
	}
}

func TestShellAllowFindsCommandsOnTheSessionPath(t *testing.T) {
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "sessiontool"), "#!/bin/sh\necho from-session-path\n")
	if err := os.Chmod(filepath.Join(bin, "sessiontool"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &workspace{root: t.TempDir(), env: []string{"PATH=" + bin}, shell: newShellPolicy(false, []string{"sessiontool"})}

	out := ws.dispatch(context.Background(), call("1", "bash", map[string]any{"command": "sessiontool"}))
	if !strings.Contains(out, "from-session-path") {
		t.Errorf("command on the session PATH did not run: %q", out)
	}
}

func TestCallerNotesStartOnTheirOwnLine(t *testing.T) {
	m := &scriptedModel{}
	if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "look", Instructions: "Check from memory.", NoShell: true, Chat: m.chat}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.calls[0][1].Content, "Check from memory.\nThere is no shell") {
		t.Errorf("notes run into the caller's instructions: %q", m.calls[0][1].Content)
	}
}

func TestShellAllowIsDescribedToTheModel(t *testing.T) {
	m := &scriptedModel{}
	if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "look", ShellAllow: []string{" go  test ", "gofmt"}, Chat: m.chat}); err != nil {
		t.Fatal(err)
	}
	var bashDesc string
	for _, tool := range m.opts[0].Tools {
		if tool.Name == "bash" {
			bashDesc = tool.Description
		}
	}
	if !strings.Contains(bashDesc, "go test, gofmt") || !strings.Contains(m.calls[0][1].Content, "go test, gofmt") {
		t.Errorf("allowlist not described: tool %q", bashDesc)
	}
}
