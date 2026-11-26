// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package str

import (
	"os/exec"
	"syscall"
)

func applyProcGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}

func reapProcessGroup(pgid int) {
	for done := false; !done; {
		pid, err := syscall.Wait4(-pgid, nil, syscall.WNOHANG, nil)
		done = pid <= 0 && err != syscall.EINTR
	}
}
