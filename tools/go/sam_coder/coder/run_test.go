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

func TestRunUsesOnlyTheCallersEnvironment(t *testing.T) {
	t.Setenv("SAMCODER_TEST_SECRET", "do-not-leak")
	dir := t.TempDir()
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "bash", map[string]any{"command": "echo secret=$SAMCODER_TEST_SECRET goflags=$GOFLAGS"})),
		{Content: "done", FinishReason: "stop"},
	}}

	out, err := Run(context.Background(), Options{
		Dir: dir, Task: "x", VerifyCommand: "echo verify=$SAMCODER_TEST_SECRET",
		Env:  []string{"PATH=" + os.Getenv("PATH"), "GOFLAGS=-tags=integration"},
		Chat: m.chat,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := toolResults(m.calls[1])["1"]
	if strings.Contains(got, "do-not-leak") || !strings.Contains(got, "goflags=-tags=integration") {
		t.Errorf("bash saw %q", got)
	}
	if out.Verify == nil || strings.Contains(out.Verify.Output, "do-not-leak") {
		t.Errorf("verify = %+v", out.Verify)
	}
}

func TestRunContinuesAnEarlierConversation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "House rules.")
	first := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "write", map[string]any{"path": "a.txt", "content": "one\n"})),
		{Content: "wrote a.txt", FinishReason: "stop"},
	}}
	out1, err := Run(context.Background(), Options{Dir: dir, Task: "write a.txt", Chat: first.chat})
	if err != nil {
		t.Fatal(err)
	}

	second := &scriptedModel{replies: []sdk.ChatResult{{Content: "fixed", FinishReason: "stop"}}}
	out2, err := Run(context.Background(), Options{Dir: dir, Task: "your reply missed the summary; add it", History: out1.History, Chat: second.chat})
	if err != nil {
		t.Fatal(err)
	}

	sent := second.calls[0]
	if len(sent) != len(out1.History)+1 || sent[0].Role != "system" {
		t.Fatalf("continuation sent %d messages, history had %d", len(sent), len(out1.History))
	}
	last := sent[len(sent)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "your reply missed the summary") || strings.Contains(last.Content, "House rules.") {
		t.Errorf("follow-up message = %q", last.Content)
	}
	if len(out2.Changes.Files) != 1 || !strings.Contains(out2.Changes.Diff, "b/a.txt") {
		t.Errorf("diff does not carry the first run's change: %+v", out2.Changes)
	}
}

func TestRunReadsExtraDirsAndCallerInstructions(t *testing.T) {
	dir := t.TempDir()
	cache, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cache, "mod@v2", "x.go"), "package mod\n")
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "read", map[string]any{"path": filepath.Join(cache, "mod@v2", "x.go")})),
		{Content: "done", FinishReason: "stop"},
	}}

	if _, err := Run(context.Background(), Options{Mode: ModePlan, Dir: dir, Task: "x", ReadDirs: []string{cache}, Instructions: "The module cache is at " + cache + ".\n", Chat: m.chat}); err != nil {
		t.Fatal(err)
	}

	if got := toolResults(m.calls[1])["1"]; !strings.Contains(got, "package mod") {
		t.Errorf("read of a read dir = %q", got)
	}
	if !strings.Contains(m.calls[0][1].Content, "The module cache is at "+cache) {
		t.Errorf("instructions missing from the prompt: %q", m.calls[0][1].Content)
	}
}

func TestRunStartsWithAReadDirThatDoesNotExistYetAndReadsItOnceItDoes(t *testing.T) {
	dir := t.TempDir()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(parent, "go", "mod")
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "bash", map[string]any{"command": "mkdir -p " + cache + " && echo 'package mod' > " + filepath.Join(cache, "x.go")})),
		toolTurn(call("2", "read", map[string]any{"path": filepath.Join(cache, "x.go")})),
		{Content: "done", FinishReason: "stop"},
	}}

	if _, err := Run(context.Background(), Options{Dir: dir, Task: "x", ReadDirs: []string{cache}, Chat: m.chat}); err != nil {
		t.Fatal(err)
	}

	if got := toolResults(m.calls[2])["2"]; !strings.Contains(got, "package mod") {
		t.Errorf("read of a read dir made during the run = %q", got)
	}
}

func TestRunWithoutAShellIsNotToldToUseBash(t *testing.T) {
	for _, mode := range []Mode{ModeImplement, ModePlan} {
		m := &scriptedModel{replies: []sdk.ChatResult{{Content: "done", FinishReason: "stop"}}}
		if _, err := Run(context.Background(), Options{Mode: mode, Dir: t.TempDir(), Task: "x", NoShell: true, Chat: m.chat}); err != nil {
			t.Fatal(err)
		}
		system := m.calls[0][0].Content
		if strings.Contains(system, "Use bash") || !strings.Contains(system, noShellRule) {
			t.Errorf("%s: system prompt still sends the model to bash: %q", mode, system)
		}
	}
}

func TestRunRejectsOptionsItCannotStartWith(t *testing.T) {
	m := &scriptedModel{}
	for name, o := range map[string]Options{
		"no task": {Dir: t.TempDir(), Chat: m.chat},
		"no chat": {Dir: t.TempDir(), Task: "x"},
		"no dir":  {Dir: filepath.Join(t.TempDir(), "missing"), Task: "x", Chat: m.chat},
	} {
		if _, err := Run(context.Background(), o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
