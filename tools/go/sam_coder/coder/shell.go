// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	shellDefaultTimeout = 120 * time.Second
	shellMaxTimeout     = 600 * time.Second
	shellOutputHead     = 10 << 10
	shellOutputTail     = 20 << 10
)

var execCommand = exec.CommandContext

type ShellResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out,omitempty"`
	Output   string `json:"output"`
	Duration string `json:"duration"`
}

func (r ShellResult) String() string {
	status := fmt.Sprintf("exit code %d", r.ExitCode)
	if r.TimedOut {
		status = "timed out"
	}
	return fmt.Sprintf("%s after %s\n%s", status, r.Duration, r.Output)
}

func runShell(ctx context.Context, dir, command string, timeoutSeconds int, env []string) ShellResult {
	shell := "bash"
	if _, err := exec.LookPath(shell); err != nil {
		shell = "sh"
	}
	return runArgv(ctx, dir, command, []string{shell, "-c", command}, timeoutSeconds, env)
}

func runArgv(ctx context.Context, dir, command string, argv []string, timeoutSeconds int, env []string) ShellResult {
	timeout := shellDefaultTimeout
	if timeoutSeconds > 0 {
		timeout = min(time.Duration(timeoutSeconds)*time.Second, shellMaxTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	program, err := lookPathIn(argv[0], env)
	if err != nil {
		return ShellResult{Command: command, ExitCode: -1, Output: err.Error(), Duration: "0s"}
	}
	cmd := execCommand(ctx, program, argv[1:]...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	configureProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	err = cmd.Run()
	killProcessGroup(cmd)
	res := ShellResult{
		Command:  command,
		Duration: time.Since(start).Round(time.Millisecond).String(),
		Output:   headTail(out.String(), shellOutputHead, shellOutputTail),
	}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		res.TimedOut = true
		res.ExitCode = -1
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	case err != nil:
		res.ExitCode = -1
		res.Output = err.Error()
	}
	return res
}

func lookPathIn(name string, env []string) (string, error) {
	if env == nil || strings.ContainsRune(name, filepath.Separator) {
		return exec.LookPath(name)
	}
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s: not found on PATH", name)
}

func headTail(s string, head, tail int) string {
	if len(s) <= head+tail {
		return s
	}
	omitted := len(s) - head - tail
	return s[:head] + fmt.Sprintf("\n… (%d bytes omitted) …\n", omitted) + s[len(s)-tail:]
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
