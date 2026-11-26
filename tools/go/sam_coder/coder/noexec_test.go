// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func recordExec(t *testing.T) *[]string {
	t.Helper()
	var started []string
	original := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		started = append(started, filepath.Base(name))
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { execCommand = original })
	return &started
}

func TestANoExecRunStartsNoProcess(t *testing.T) {
	started := recordExec(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.py"), "x = 1\ny = 2\n")
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(
			call("1", "edit", map[string]any{"path": "app.py", "old_string": "x = 1", "new_string": "x = ("}),
			call("2", "grep", map[string]any{"pattern": "y ="}),
			call("3", "glob", map[string]any{"pattern": "*.py"}),
			call("4", "bash", map[string]any{"command": "echo hi"}),
			call("5", "fetch", map[string]any{"url": "https://example.com/"}),
			call("6", "changes", map[string]any{}),
		),
		{Content: "done", FinishReason: "stop"},
	}}

	out, err := Run(context.Background(), Options{Dir: dir, Task: "x", NoExec: true, VerifyCommand: "echo verify",
		ShellAllow: []string{"echo"}, FetchDomains: []string{"example.com"}, Chat: m.chat})
	if err != nil {
		t.Fatal(err)
	}

	if len(*started) != 0 {
		t.Errorf("a NoExec run started %v", *started)
	}
	if offered := toolNames(m.opts[0].Tools); slices.Contains(offered, "bash") || slices.Contains(offered, "fetch") {
		t.Errorf("offered %v", offered)
	}
	results := toolResults(m.calls[1])
	if strings.Contains(results["1"], "warning") || !strings.Contains(results["2"], "app.py:2: y = 2") || !strings.Contains(results["3"], "app.py") {
		t.Errorf("edit = %q, grep = %q, glob = %q", results["1"], results["2"], results["3"])
	}
	if !strings.HasPrefix(results["4"], "error:") || !strings.HasPrefix(results["5"], "error:") {
		t.Errorf("bash = %q, fetch = %q", results["4"], results["5"])
	}
	if !strings.Contains(results["6"], "-x = 1\n+x = (\n") {
		t.Errorf("changes = %q", results["6"])
	}
	if out.Verify != nil || out.DiffErr != "" {
		t.Errorf("verify = %+v, diff error = %q", out.Verify, out.DiffErr)
	}
	if !slices.Equal(out.Changes.Files, []string{"M app.py"}) || out.Changes.Stat != "1 file changed, 1 insertion(+), 1 deletion(-)" {
		t.Errorf("changes = %+v", out.Changes)
	}
}

func TestTheExecSeamSeesTheProcessesARunStarts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	started := recordExec(t)
	m := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "glob", map[string]any{"pattern": "*"})),
		{Content: "done", FinishReason: "stop"},
	}}

	if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", VerifyCommand: "true", Chat: m.chat}); err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(*started, "git") || !slices.ContainsFunc(*started, func(p string) bool { return p == "bash" || p == "sh" }) {
		t.Errorf("started %v, want git and the verify shell", *started)
	}
}

func TestANoExecContinuationDiffsAgainstTheFirstRunsBaseline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gone.txt"), "bye\n")
	first := &scriptedModel{replies: []sdk.ChatResult{
		toolTurn(call("1", "write", map[string]any{"path": "a.txt", "content": "one\n"})),
		{Content: "wrote a.txt", FinishReason: "stop"},
	}}
	out1, err := Run(context.Background(), Options{Dir: dir, Task: "write a.txt", NoExec: true, Chat: first.chat})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")
	if err = os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	second := &scriptedModel{replies: []sdk.ChatResult{{Content: "ok", FinishReason: "stop"}}}
	out2, err := Run(context.Background(), Options{Dir: dir, Task: "go on", History: out1.History, NoExec: true, Chat: second.chat})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(out2.Changes.Files, []string{"A a.txt", "D gone.txt"}) || !strings.Contains(out2.Changes.Diff, "+one\n+two\n") || !strings.Contains(out2.Changes.Diff, "--- a/gone.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n") {
		t.Errorf("changes = %+v", out2.Changes)
	}
}

func TestMaxTokensEndsTheRunBeforeTheNextTurn(t *testing.T) {
	turn := toolTurn(call("1", "glob", map[string]any{"pattern": "*"}))
	turn.Usage = sdk.ChatUsage{PromptTokens: 600, CompletionTokens: 100, TotalTokens: 700}
	m := &scriptedModel{replies: []sdk.ChatResult{turn, turn, turn, {Content: "done", FinishReason: "stop"}}}

	out, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", NoExec: true, MaxTokens: 1000, Chat: m.chat})
	if err != nil {
		t.Fatal(err)
	}

	if out.StopReason != StopMaxTokens || len(m.calls) != 2 || out.Steps != 2 || out.Usage.PromptTokens != 1200 {
		t.Errorf("stop = %q after %d calls, %d steps, usage %+v", out.StopReason, len(m.calls), out.Steps, out.Usage)
	}
	if last := out.History[len(out.History)-1]; last.Role != "tool" {
		t.Errorf("history ends with an unanswered %s message", last.Role)
	}
}

func TestADeadlineEndsTheRunWithWhatItHas(t *testing.T) {
	for _, noExec := range []bool{true, false} {
		if !noExec {
			if _, err := exec.LookPath("git"); err != nil {
				continue
			}
		}
		t.Run(map[bool]string{true: "no exec", false: "git"}[noExec], func(t *testing.T) {
			defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			turns := 0
			chat := func(ctx context.Context, _ []sdk.ChatMessage, _ sdk.ChatOptions) (*sdk.ChatResult, error) {
				if turns++; turns == 1 {
					r := toolTurn(call("1", "write", map[string]any{"path": "a.txt", "content": "one\n"}))
					return &r, nil
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}

			out, err := Run(ctx, Options{Dir: t.TempDir(), Task: "x", NoExec: noExec, Chat: chat})
			if err != nil {
				t.Fatal(err)
			}

			if out.StopReason != StopCancelled || !errors.Is(ctx.Err(), context.DeadlineExceeded) || out.Steps != 2 {
				t.Errorf("stop = %q after %d steps", out.StopReason, out.Steps)
			}
			if out.Changes == nil || !slices.Equal(out.Changes.Files, []string{"A a.txt"}) {
				t.Errorf("changes = %+v, diff error %q", out.Changes, out.DiffErr)
			}
		})
	}
}

func TestNearTheTokenCapTheRunReportsInsteadOfEndingEmpty(t *testing.T) {
	turn := toolTurn(call("1", "glob", map[string]any{"pattern": "*"}))
	turn.Usage = sdk.ChatUsage{PromptTokens: 900, CompletionTokens: 50, TotalTokens: 950}
	m := &scriptedModel{replies: []sdk.ChatResult{turn, {Content: "found two problems", FinishReason: "stop"}}}

	out, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", NoExec: true, MaxTokens: 1000, Chat: m.chat})
	if err != nil {
		t.Fatal(err)
	}

	if out.StopReason != StopMaxTokens || len(m.calls) != 2 || out.Report != "found two problems" {
		t.Errorf("stop = %q after %d calls, report %q", out.StopReason, len(m.calls), out.Report)
	}
}

func TestAThrottledTurnNearTheDeadlineGoesStraightToTheFinalReport(t *testing.T) {
	turns := 0
	chat := func(_ context.Context, _ []sdk.ChatMessage, opts sdk.ChatOptions) (*sdk.ChatResult, error) {
		if turns++; turns == 1 {
			return nil, &sdk.LLMError{Category: sdk.ChatErrorRateLimit, Message: "slow down"}
		}
		if opts.ToolChoice != "none" {
			t.Errorf("turn %d was not the final turn: tool choice %q", turns, opts.ToolChoice)
		}
		return &sdk.ChatResult{Content: "what I found"}, nil
	}
	started := time.Now()

	out, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "x", NoExec: true, Chat: chat,
		Deadline: time.Now().Add(transientChatBackoff / 2)})
	if err != nil {
		t.Fatal(err)
	}

	if waited := time.Since(started); waited >= transientChatBackoff {
		t.Errorf("the run waited %s on a retry that would have ended after its deadline", waited)
	}
	if out.Report != "what I found" || out.StopReason != StopMaxSteps || out.Steps != 1 {
		t.Errorf("report %q, stop %q after %d steps; want the final turn's report", out.Report, out.StopReason, out.Steps)
	}
}
