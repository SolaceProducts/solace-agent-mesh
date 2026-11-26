// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
)

func TestInstallEntry_TakesNoPackageProfileAndBindsTheToolDir(t *testing.T) {
	entry := installEntry(installStep{name: "setup.sh", toolDir: "/opt/sam/tools/p", executable: "/bin/bash", writableToolDir: true, readOnlyMounts: []string{"/tmp/str-init-1"}})
	if entry.SandboxProfile != "" {
		t.Fatalf("install step must use the STR default profile, got %q", entry.SandboxProfile)
	}
	want := []ExtraMount{
		{HostPath: "/opt/sam/tools/p", MountPath: "/opt/sam/tools/p", ReadOnly: false},
		{HostPath: "/tmp/str-init-1", MountPath: "/tmp/str-init-1", ReadOnly: true},
	}
	if !slices.Equal(entry.ExtraMounts, want) {
		t.Fatalf("mounts = %+v, want %+v", entry.ExtraMounts, want)
	}
	if ro := installEntry(installStep{toolDir: "/opt/sam/tools/p"}).ExtraMounts[0].ReadOnly; !ro {
		t.Fatal("a step without writableToolDir must get its tool dir read-only")
	}
}

func TestBuildBwrapCommand_InstallStepRunsItsOwnArgsWithClearedEnv(t *testing.T) {
	runner := hardeningTestRunner(t)
	toolDir := t.TempDir()
	resolvedToolDir, err := filepath.EvalSymlinks(toolDir)
	if err != nil {
		t.Fatal(err)
	}
	step := installStep{name: "setup.sh", toolDir: toolDir, executable: "/bin/bash", args: []string{"-c", `cd "$1" && exec bash ./setup.sh`, "setup.sh", toolDir}, writableToolDir: true}

	cmd, err := runner.buildBwrapCommand(context.Background(), installEntry(step), step.args, t.TempDir(), nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args
	if !slices.Contains(args, "--clearenv") {
		t.Fatal("install step must run with a cleared env under bwrap")
	}
	if findFlagTriple(args, "--bind", resolvedToolDir, toolDir) < 0 {
		t.Fatalf("tool dir must be bound writable with --bind: %q", args)
	}
	if findFlagTriple(args, "--ro-bind", resolvedToolDir, toolDir) >= 0 {
		t.Fatalf("tool dir must not be bound read-only: %q", args)
	}
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
		}
	}
	tail := args[sep+1:]
	if want := append([]string{"/bin/bash"}, step.args...); !slices.Equal(tail, want) {
		t.Fatalf("command = %q, want %q", tail, want)
	}
}
