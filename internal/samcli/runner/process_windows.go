// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build windows

package runner

import (
	"os"
	"os/exec"
	"syscall"
)

func setProcessGroup(*exec.Cmd) {
}

func signalProcessGroup(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}
