// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/str/seccomp"
)

func argsContainSeccompFD3(args []string) bool {
	for i, a := range args {
		if a == "--seccomp" && i+1 < len(args) && args[i+1] == "3" {
			return true
		}
	}
	return false
}

func TestBuildBwrapCommand_SeccompFilter(t *testing.T) {
	entry := &ManifestEntry{ToolName: "echo", Executable: "/bin/echo"}

	t.Run("enabled wires --seccomp 3 and one ExtraFile", func(t *testing.T) {
		r := NewSandboxRunner(SandboxConfig{Mode: "bwrap", SeccompBindDeny: true}, slog.Default())
		cmd, err := r.buildBwrapCommand(t.Context(), entry, []string{"/tmp/args.json"}, t.TempDir(), nil, "", slog.Default())
		if err != nil {
			t.Fatalf("buildBwrapCommand: %v", err)
		}
		defer func() {
			for _, f := range cmd.ExtraFiles {
				_ = f.Close()
			}
		}()
		if !argsContainSeccompFD3(cmd.Args) {
			t.Errorf("expected --seccomp 3 in args, got %v", cmd.Args)
		}
		if len(cmd.ExtraFiles) != 1 || cmd.ExtraFiles[0] == nil {
			t.Errorf("expected exactly one non-nil ExtraFile, got %d", len(cmd.ExtraFiles))
		}
		scIdx := slices.Index(cmd.Args, "--seccomp")
		toolIdx := slices.Index(cmd.Args, entry.Executable)
		if scIdx == -1 || toolIdx == -1 || scIdx > toolIdx {
			t.Errorf("--seccomp must precede the tool argv (%q); seccomp@%d tool@%d args=%v", entry.Executable, scIdx, toolIdx, cmd.Args)
		}
	})

	t.Run("disabled wires neither", func(t *testing.T) {
		r := NewSandboxRunner(SandboxConfig{Mode: "bwrap", SeccompBindDeny: false}, slog.Default())
		cmd, err := r.buildBwrapCommand(t.Context(), entry, []string{"/tmp/args.json"}, t.TempDir(), nil, "", slog.Default())
		if err != nil {
			t.Fatalf("buildBwrapCommand: %v", err)
		}
		if argsContainSeccompFD3(cmd.Args) {
			t.Errorf("did not expect --seccomp in args, got %v", cmd.Args)
		}
		if len(cmd.ExtraFiles) != 0 {
			t.Errorf("expected no ExtraFiles, got %d", len(cmd.ExtraFiles))
		}
	})
}

func TestNewSeccompFilterFile_ContainsHostProgram(t *testing.T) {
	f, err := newSeccompFilterFile()
	if err != nil {
		t.Fatalf("newSeccompFilterFile: %v", err)
	}
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read seccomp pipe: %v", err)
	}
	want, err := seccomp.HostProgram()
	if err != nil {
		t.Fatalf("HostProgram: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("seccomp pipe = %d bytes, want %d bytes equal to HostProgram()", len(got), len(want))
	}
}
