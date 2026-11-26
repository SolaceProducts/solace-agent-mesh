// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build linux

package str

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestReapProcessGroup(t *testing.T) {
	script := "#!/bin/sh\nsleep 60 &\nexit 0\n"
	scriptFile := t.TempDir() + "/fork_grandchild.sh"
	if err := os.WriteFile(scriptFile, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command("/bin/sh", scriptFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid

	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait direct child: %v", err)
	}

	_ = killProcessGroup(pid)
	reapProcessGroup(pid)

	var ws syscall.WaitStatus
	n, err := syscall.Wait4(-pid, &ws, syscall.WNOHANG, nil)
	if n > 0 {
		t.Errorf("zombie still present after reapProcessGroup: pid=%d", n)
	}
	if err != nil && err != syscall.ECHILD {
		t.Errorf("unexpected error from follow-up wait4: %v", err)
	}
}

func TestKillThenReapCleansGroup(t *testing.T) {
	script := "#!/bin/sh\nsleep 60 &\nsleep 60 &\nexit 0\n"
	scriptFile := t.TempDir() + "/fork_two.sh"
	if err := os.WriteFile(scriptFile, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command("/bin/sh", scriptFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid

	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait direct child: %v", err)
	}

	_ = killProcessGroup(pid)
	reapProcessGroup(pid)

	var ws syscall.WaitStatus
	n, err := syscall.Wait4(-pid, &ws, syscall.WNOHANG, nil)
	if n > 0 {
		t.Errorf("zombie still present after reapProcessGroup: pid=%d", n)
	}
	if err != nil && err != syscall.ECHILD {
		t.Errorf("unexpected error from follow-up wait4: %v", err)
	}
}
