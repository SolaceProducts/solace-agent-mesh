// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"errors"
	"os/exec"
)

func runChild(cmd *exec.Cmd) error {
	beginChildStart()
	if err := cmd.Start(); err != nil {
		abortChildStart()
		return err
	}
	pid := cmd.Process.Pid
	trackChild(pid)
	err := cmd.Wait()
	untrackChild(pid)
	return err
}

func childOutput(cmd *exec.Cmd) ([]byte, error) {
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr *bytes.Buffer
	if cmd.Stderr == nil {
		stderr = &bytes.Buffer{}
		cmd.Stderr = stderr
	}
	err := runChild(cmd)
	var exitErr *exec.ExitError
	if stderr != nil && errors.As(err, &exitErr) {
		exitErr.Stderr = stderr.Bytes()
	}
	return stdout.Bytes(), err
}

func childCombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := runChild(cmd)
	return out.Bytes(), err
}
