// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

type installStep struct {
	name            string
	toolDir         string
	executable      string
	args            []string
	writableToolDir bool
	readOnlyMounts  []string
	pythonPath      string
	stdout, stderr  io.Writer
}

func (r *SandboxRunner) runInstallStep(ctx context.Context, step installStep) error {
	if r == nil {
		argv := toolInvocationArgv(step.executable, step.args...)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		hideConsoleWindow(cmd)
		if step.pythonPath != "" {
			cmd.Env = envWithPythonPath(step.pythonPath)
		}
		cmd.Stdout, cmd.Stderr = step.stdout, step.stderr
		return runChild(cmd)
	}

	workDir, err := r.setupWorkDir()
	if err != nil {
		return fmt.Errorf("%s: work dir: %w", step.name, err)
	}
	defer r.removeWorkDir(workDir)

	cmd, err := r.buildCommand(ctx, installEntry(step), step.args, workDir, nil, "", r.log())
	if err != nil {
		return fmt.Errorf("%s: %w", step.name, err)
	}
	defer func() {
		for _, f := range cmd.ExtraFiles {
			_ = f.Close()
		}
	}()
	applyGroupCancel(cmd)
	cmd.Stdout, cmd.Stderr = step.stdout, step.stderr
	runErr := runChild(cmd)
	killAndReapGroup(cmd, r.log())
	return runErr
}

func installEntry(step installStep) *ManifestEntry {
	entry := &ManifestEntry{ToolName: step.name, Executable: step.executable, ToolDir: step.toolDir}
	if step.toolDir != "" {
		entry.ExtraMounts = append(entry.ExtraMounts, ExtraMount{HostPath: step.toolDir, MountPath: step.toolDir, ReadOnly: !step.writableToolDir})
	}
	for _, p := range step.readOnlyMounts {
		entry.ExtraMounts = append(entry.ExtraMounts, ExtraMount{HostPath: p, MountPath: p, ReadOnly: true})
	}
	return entry
}
