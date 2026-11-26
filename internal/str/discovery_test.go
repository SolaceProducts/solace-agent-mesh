// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func writeShellScript(t *testing.T, dir, name, stdout, stderr string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub assumes POSIX /bin/sh")
	}
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\n"
	if stdout != "" {
		body += "printf '%s' " + shellSingleQuote(stdout) + "\n"
	}
	if stderr != "" {
		body += "printf '%s' " + shellSingleQuote(stderr) + " 1>&2\n"
	}
	body += "exit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	n := i
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}

func TestDiscoverFromExecutable_StderrSurfacedInError(t *testing.T) {
	dir := t.TempDir()
	exe := writeShellScript(t, dir, "panicker",
		"",
		"panic: cannot connect to bamboohr: dial tcp 1.2.3.4:443: i/o timeout",
		2)

	_, err := DiscoverFromExecutable(exe, "")
	if err == nil {
		t.Fatal("expected an error from a non-zero-exit --schema probe; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stderr:") {
		t.Errorf("expected wrapped error to carry a 'stderr:' suffix; got %q", msg)
	}
	if !strings.Contains(msg, "dial tcp 1.2.3.4:443") {
		t.Errorf("expected the binary's stderr to be folded into the error message; got %q", msg)
	}
	if !strings.Contains(msg, "exit status 2") {
		t.Errorf("expected the underlying exit-status to remain in the chain; got %q", msg)
	}
}

func TestDiscoverFromExecutable_StderrTruncated(t *testing.T) {
	flood := strings.Repeat("A", 8192) + "real-cause: missing libfoo.so"
	dir := t.TempDir()
	exe := writeShellScript(t, dir, "flooder", "", flood, 1)

	_, err := DiscoverFromExecutable(exe, "")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "(truncated)") {
		t.Errorf("expected (truncated) marker for oversized stderr; got %q", msg)
	}
	if !strings.Contains(msg, "real-cause: missing libfoo.so") {
		t.Errorf("expected the tail of stderr to survive (actionable signal lives at the bottom of a panic); got truncated to %q", msg)
	}
}

func TestDiscoverFromExecutable_NoStderrOnSuccess(t *testing.T) {
	dir := t.TempDir()
	exe := writeShellScript(t, dir, "happy",
		`{"tools":{"my_tool":{"description":"ok"}}}`,
		"",
		0)

	tools, err := DiscoverFromExecutable(exe, "")
	if err != nil {
		t.Fatalf("unexpected error on success: %v", err)
	}
	if _, ok := tools["my_tool"]; !ok {
		t.Errorf("expected my_tool in discovered map; got %v", tools)
	}
}

func TestIsTransientProcessErr(t *testing.T) {
	transient := []error{
		syscall.ECHILD,
		syscall.EAGAIN,
		syscall.ETXTBSY,
		fmt.Errorf("running /tmp/foo --schema: %w", syscall.ECHILD),
		fmt.Errorf("running /tmp/foo --schema: %w", &os.PathError{Op: "fork/exec", Path: "/tmp/foo", Err: syscall.ETXTBSY}),
	}
	for _, err := range transient {
		if !isTransientProcessErr(err) {
			t.Errorf("isTransientProcessErr(%v) = false; want true (should retry)", err)
		}
	}

	fatal := []error{
		nil,
		fmt.Errorf("exit status 1"),
		fmt.Errorf("running /tmp/foo --schema: %w", syscall.ENOENT),
		fmt.Errorf("parsing schema output: invalid JSON"),
	}
	for _, err := range fatal {
		if isTransientProcessErr(err) {
			t.Errorf("isTransientProcessErr(%v) = true; want false (should not retry)", err)
		}
	}
}

func TestProbeWithTransientRetry_AnExecOfAScriptStillOpenForWritingIsRetried(t *testing.T) {
	t.Parallel()
	attempts := 0
	want := map[string]DiscoveredTool{"lookup": {}}
	got, err := probeWithTransientRetry("lookup --schema", func() (map[string]DiscoveredTool, error) {
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("running lookup --schema: %w", &os.PathError{Op: "fork/exec", Path: "lookup", Err: syscall.ETXTBSY})
		}
		return want, nil
	})
	if err != nil {
		t.Fatalf("probeWithTransientRetry: %v", err)
	}
	if attempts != 2 || len(got) != 1 {
		t.Errorf("attempts = %d, tools = %v; want one retry after text file busy and the tools of the second attempt", attempts, got)
	}
}

func TestDiscoverFromExecutable_NonTransientNotRetried(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub assumes POSIX /bin/sh")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "runs")
	path := filepath.Join(dir, "failer")
	body := "#!/bin/sh\nprintf x >> " + shellSingleQuote(counter) + "\nexit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	if _, err := DiscoverFromExecutable(path, ""); err == nil {
		t.Fatal("expected an error from a non-zero-exit --schema probe")
	}

	runs, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("non-transient failure was retried: ran %d times, want 1", len(runs))
	}
}

func TestFormatStderrSuffix_EmptyReturnsNothing(t *testing.T) {
	cases := [][]byte{nil, {}, []byte("   \n\n  ")}
	for _, in := range cases {
		got := formatStderrSuffix(in)
		if got != "" {
			t.Errorf("formatStderrSuffix(%q) = %q; want empty", in, got)
		}
	}
}

func TestFormatStderrSuffix_NewlinesCollapsed(t *testing.T) {
	in := []byte("line one\nline two\n")
	got := formatStderrSuffix(in)
	if strings.Contains(got, "\n") {
		t.Errorf("expected newlines collapsed to ' | '; got %q", got)
	}
	if !strings.Contains(got, "line one") || !strings.Contains(got, "line two") {
		t.Errorf("expected both lines preserved; got %q", got)
	}
}
