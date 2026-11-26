// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build linux

package str

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestGlobalReaper_SetsidOrphan(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	StartGlobalReaper(ctx, slog.Default())

	script := `#!/bin/sh
(
  # setsid puts this subshell in a new session — leaves the parent pgid.
  setsid sh -c 'sleep 0.2' &
)
exit 0
`
	scriptFile := t.TempDir() + "/setsid_orphan.sh"
	if err := os.WriteFile(scriptFile, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", scriptFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	startTracked(t, cmd)
	if err := waitTracked(cmd); err != nil {
		t.Fatalf("run direct child: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	n, err := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)

	if n > 0 {
		t.Errorf("orphan zombie still present after global reaper ran: pid=%d", n)
	}
	if err != nil && err != syscall.ECHILD {
		t.Errorf("unexpected error from follow-up Wait4: %v", err)
	}
}

func TestGlobalReaper_DoesNotStealDirectChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	StartGlobalReaper(ctx, slog.Default())

	dir := t.TempDir()
	script := writeScript(t, dir, "exit42.sh", `exit 42`)

	runner := NewSandboxRunner(SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   10,
		MaxConcurrentExecutions: 4,
	}, slog.Default())

	for range 5 {
		_, invokeErr := runner.Execute(t.Context(), minimalParams(), minimalEntry(script), nil, func(string) {})
		if invokeErr == nil {
			t.Fatal("expected error from exit 42 tool, got nil")
		}
		if invokeErr.Code != "TOOL_EXIT_NONZERO" {
			t.Errorf("expected TOOL_EXIT_NONZERO, got %q (message: %s)", invokeErr.Code, invokeErr.Message)
		}
	}
}

func TestGlobalReaper_MultipleOrphans(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	StartGlobalReaper(ctx, slog.Default())

	script := `#!/bin/sh
(setsid sh -c 'sleep 0.1' &)
exit 0
`
	scriptFile := t.TempDir() + "/burst_orphan.sh"
	if err := os.WriteFile(scriptFile, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	for range 6 {
		cmd := exec.CommandContext(ctx, "/bin/sh", scriptFile)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		startTracked(t, cmd)
		_ = waitTracked(cmd)
	}

	time.Sleep(800 * time.Millisecond)

	zombiesLeft := 0
	for {
		n, err := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
		if n <= 0 || err == syscall.ECHILD {
			break
		}
		zombiesLeft++
	}

	if zombiesLeft > 0 {
		t.Errorf("global reaper left %d zombie(s) after burst of setsid orphans", zombiesLeft)
	}
}
