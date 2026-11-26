// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyUnavailableExecutable_MissingBinary(t *testing.T) {
	path := "/some/dir/count_chars"
	msg := classifyUnavailableExecutable(path)

	if !strings.Contains(msg, "count_chars") {
		t.Errorf("expected tool name in message, got: %q", msg)
	}
	if !strings.Contains(msg, "runtime: go") {
		t.Errorf("expected runtime:go hint in message, got: %q", msg)
	}
	if !strings.Contains(msg, "sam skill package") {
		t.Errorf("expected sam skill package hint in message, got: %q", msg)
	}
}

func TestClassifyUnavailableExecutable_SourceFileExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "count_chars")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := classifyUnavailableExecutable(src)

	if !strings.Contains(msg, "count_chars") {
		t.Errorf("expected tool name in message, got: %q", msg)
	}
	if !strings.Contains(msg, "runtime: go") {
		t.Errorf("expected runtime:go hint for non-executable file, got: %q", msg)
	}
}

func TestClassifyUnavailableExecutable_Empty(t *testing.T) {
	msg := classifyUnavailableExecutable("")
	if msg == "" {
		t.Error("expected non-empty message for empty executable path")
	}
}

func TestClassifyUnavailableExecutable_GoSourceDir(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "count_chars")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(toolDir, "count_chars")

	msg := classifyUnavailableExecutable(execPath)

	if !strings.Contains(msg, "count_chars") {
		t.Errorf("expected tool name in message, got: %q", msg)
	}
	if !strings.Contains(msg, "sam skill package") {
		t.Errorf("expected rebuild hint in message, got: %q", msg)
	}
}

func TestClassifyUnavailableExecutable_PythonMissing(t *testing.T) {
	msg := classifyUnavailableExecutable("/some/dir/report_gen.py")

	if !strings.Contains(msg, "report_gen.py") {
		t.Errorf("expected tool name in message, got: %q", msg)
	}
	if strings.Contains(msg, "runtime: go") {
		t.Errorf("Go-specific advice for a Python tool, got: %q", msg)
	}
}

func TestClassifyUnavailableExecutable_PythonExecBitLost(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "report_gen.py")
	if err := os.WriteFile(src, []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := classifyUnavailableExecutable(src)

	if !strings.Contains(msg, "report_gen.py") {
		t.Errorf("expected tool name in message, got: %q", msg)
	}
	if !strings.Contains(msg, "chmod +x") {
		t.Errorf("expected exec-bit restore hint, got: %q", msg)
	}
	if strings.Contains(msg, "runtime: go") {
		t.Errorf("Go-specific advice for a Python tool, got: %q", msg)
	}
}
