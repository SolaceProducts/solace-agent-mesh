// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package str

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	return err != nil || !strings.Contains(string(stat), ") Z ")
}

func TestRunInstallStep_DirectModeKillsWhatTheStepLeftRunning(t *testing.T) {
	runner := NewSandboxRunner(SandboxConfig{Mode: "direct", WorkBaseDir: t.TempDir()}, slog.Default())
	var out bytes.Buffer
	err := runner.runInstallStep(context.Background(), installStep{
		name: "probe", toolDir: t.TempDir(), executable: "/bin/sh",
		args: []string{"-c", "sleep 60 >/dev/null 2>&1 & echo $!"}, stdout: &out, stderr: &out,
	})
	if err != nil {
		t.Fatalf("runInstallStep: %v (output %q)", err, out.String())
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(out.String()))
	if convErr != nil {
		t.Fatalf("no pid printed: %q", out.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("background child %d still running after the step returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRunInstallStep_NilRunnerRunsPlain(t *testing.T) {
	var runner *SandboxRunner
	var out bytes.Buffer
	if err := runner.runInstallStep(context.Background(), installStep{name: "probe", executable: "/bin/sh", args: []string{"-c", "echo ok"}, stdout: &out, stderr: &out}); err != nil {
		t.Fatalf("runInstallStep: %v", err)
	}
	if strings.TrimSpace(out.String()) != "ok" {
		t.Fatalf("output = %q", out.String())
	}
}
