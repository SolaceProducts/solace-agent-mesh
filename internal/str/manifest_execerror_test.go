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

func TestResolveExecutable_DistinguishesNotFoundFromNotExecutable(t *testing.T) {
	t.Run("present but not executable", func(t *testing.T) {
		dir := t.TempDir()
		tool := filepath.Join(dir, "mytool")
		if err := os.WriteFile(tool, []byte{0x7f, 'E', 'L', 'F'}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chmod(tool, 0o644); err != nil {
			t.Fatalf("chmod: %v", err)
		}

		_, err := resolveExecutable(dir, "mytool")
		if err == nil {
			t.Fatal("expected an error for a non-executable file")
		}
		msg := err.Error()
		if strings.Contains(msg, "not found") {
			t.Errorf("message claims the file is missing: %q", msg)
		}
		for _, want := range []string{"not executable", tool} {
			if !strings.Contains(msg, want) {
				t.Errorf("message %q does not mention %q", msg, want)
			}
		}
	})

	t.Run("genuinely absent", func(t *testing.T) {
		_, err := resolveExecutable(t.TempDir(), "mytool")
		if err == nil {
			t.Fatal("expected an error for a missing file")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("a missing executable should still report not found: %q", err.Error())
		}
	})

	t.Run("a directory is not a candidate", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "mytool"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		_, err := resolveExecutable(dir, "mytool")
		if err == nil {
			t.Fatal("expected an error when the name is a directory")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("a directory should report not found, got %q", err.Error())
		}
	})
}

func TestUnresolvedExecutableError_WindowsExtension(t *testing.T) {
	fakeWindowsGOOS(t)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mytool"), []byte{'M', 'Z'}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := unresolvedExecutableError(dir, "mytool").Error()
	for _, want := range []string{"Windows cannot spawn it", ".exe"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "not found") {
		t.Errorf("message claims the file is missing: %q", msg)
	}
}

func TestUnresolvedExecutableError_ShebangHint(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "entrypoint")
	if err := os.WriteFile(script, []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	msg := unresolvedExecutableError(dir, "entrypoint").Error()
	if !strings.Contains(msg, "#!") {
		t.Errorf("message does not name the interpreter line: %q", msg)
	}
	if !strings.Contains(msg, "not executable") {
		t.Errorf("message does not state the cause: %q", msg)
	}
}

func TestUnresolvedExecutableError_ScriptsDirOnUnix(t *testing.T) {
	if runtimeGOOS() == "windows" {
		t.Skip("asserts the non-Windows search path")
	}
	dir := t.TempDir()
	scripts := filepath.Join(dir, "python", "Scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tool := filepath.Join(scripts, "weather-tools")
	if err := os.WriteFile(tool, []byte("#!/usr/bin/env python3\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := unresolvedExecutableError(dir, "weather-tools").Error()
	for _, want := range []string{"only searched on a Windows runtime", "python/bin/"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
	if strings.Contains(msg, "not executable") {
		t.Errorf("message blames the exec bit for a search-path problem: %q", msg)
	}
}
