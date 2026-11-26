// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script tests not supported on Windows")
	}
}

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+content+"\n"), 0o755); err != nil {
		t.Fatalf("writeScript: %v", err)
	}
	return p
}

func newDirectRunner(t *testing.T) *SandboxRunner {
	t.Helper()
	return NewSandboxRunner(SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   10,
		MaxConcurrentExecutions: 4,
	}, slog.Default())
}

func minimalEntry(executable string) *ManifestEntry {
	return &ManifestEntry{
		ToolName:   "test_tool",
		Executable: executable,
	}
}

func minimalParams() *remotetool.InvokeParams {
	return &remotetool.InvokeParams{
		TaskID:     "task-test",
		ToolName:   "test_tool",
		Args:       map[string]any{},
		ToolConfig: map[string]any{},
	}
}

func TestSandbox_NonZeroExit_TOOL_EXIT_NONZERO(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "exit2.sh", `echo "some error output" >&2; exit 2`)

	runner := newDirectRunner(t)
	_, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})

	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	if invokeErr.Code != remotetool.ErrToolExitNonzero {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrToolExitNonzero)
	}
	if invokeErr.StderrTail == "" {
		t.Error("StderrTail should not be empty")
	}
}

func TestSandbox_SignalKill_TOOL_SIGNALED(t *testing.T) {
	skipOnWindows(t)
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		dir := t.TempDir()
		script := writeScript(t, dir, "sigkill.sh", `kill -9 $$`)

		runner := newDirectRunner(t)
		_, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})

		if invokeErr == nil {
			t.Fatal("expected error, got nil")
		}
		if invokeErr.Code != remotetool.ErrToolSignaled {
			t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrToolSignaled)
		}
	}
}

func TestSandbox_ExecFailed_TOOL_EXEC_FAILED(t *testing.T) {
	skipOnWindows(t)
	runner := newDirectRunner(t)

	entry := minimalEntry("/nonexistent/binary/that/does/not/exist")

	_, invokeErr := runner.Execute(t.Context(), minimalParams(), entry, nil, func(string) {})

	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	if invokeErr.Code != remotetool.ErrToolExecFailed {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrToolExecFailed)
	}
}

func TestSandbox_Timeout_TOOL_TIMEOUT(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "sleep.sh", `sleep 60`)

	runner := NewSandboxRunner(SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   1,
		MaxConcurrentExecutions: 4,
	}, slog.Default())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, invokeErr := runner.Execute(ctx, minimalParams(), minimalEntry(script), nil, func(string) {})

	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	if invokeErr.Code != remotetool.ErrToolTimeout {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrToolTimeout)
	}
}

func TestSandbox_ToolStdoutStaysOutOfTheSTRStdoutAndTheError(t *testing.T) {
	skipOnWindows(t)
	const canary = "stdout-canary-7f3a91"
	script := writeScript(t, t.TempDir(), "print.sh", `echo "`+canary+`"; echo "tool ran" >&2; exit 2`)

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	strStdout := os.Stdout
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = strStdout })

	runner := newDirectRunner(t)
	_, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})

	os.Stdout = strStdout
	if err = write.Close(); err != nil {
		t.Fatal(err)
	}
	written, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}

	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(invokeErr.StderrTail, "tool ran") {
		t.Fatalf("StderrTail = %q, want the tool's stderr: the tool did not run", invokeErr.StderrTail)
	}
	if contains(string(written), canary) {
		t.Errorf("tool stdout reached the STR's stdout: %q", written)
	}
	if contains(invokeErr.StderrTail, canary) || contains(invokeErr.Message, canary) {
		t.Errorf("tool stdout reached the invoke error: tail %q, message %q", invokeErr.StderrTail, invokeErr.Message)
	}
}

func TestSandbox_StderrTail_Truncated(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "bigstderr.sh", `for i in $(seq 1 200); do echo "line $i of output" >&2; done; exit 1`)

	runner := newDirectRunner(t)
	_, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})

	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	if len(invokeErr.StderrTail) > stderrTailMaxBytes {
		t.Errorf("StderrTail length %d > max %d", len(invokeErr.StderrTail), stderrTailMaxBytes)
	}
	if invokeErr.StderrTail == "" {
		t.Error("StderrTail should not be empty")
	}
}

func TestSandbox_StackTrace_PythonTraceback(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "pytraceback.sh", `cat >&2 <<'EOF'
Traceback (most recent call last):
  File "tool.py", line 5, in run
    raise ValueError("something went wrong")
ValueError: something went wrong
EOF
exit 1`)

	runner := newDirectRunner(t)
	_, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})

	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	if invokeErr.StackTrace == "" {
		t.Error("StackTrace should be populated for Python traceback")
	}
	if invokeErr.StackTrace != "" && invokeErr.StackTrace[:len("Traceback")] != "Traceback" {
		t.Errorf("StackTrace should start with Traceback, got: %q", invokeErr.StackTrace[:20])
	}
}

func TestSandbox_StderrRedaction(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "leak.sh", `echo "OPENAI_API_KEY=sk-supersecret123" >&2; exit 1`)

	runner := newDirectRunner(t)
	_, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})

	if invokeErr == nil {
		t.Fatal("expected error, got nil")
	}
	if contains(invokeErr.StderrTail, "sk-supersecret123") {
		t.Error("secret value leaked in StderrTail")
	}
	if contains(invokeErr.Message, "sk-supersecret123") {
		t.Error("secret value leaked in Message")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
