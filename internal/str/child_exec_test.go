// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func skipIfNoShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh not available on windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found in PATH")
	}
}

func TestRunChild_StartFailureReturnsError(t *testing.T) {
	err := runChild(exec.Command("/nonexistent/binary/xyz"))
	if err == nil {
		t.Fatal("expected error for nonexistent binary, got nil")
	}
}

func TestChildOutput_PreservesCallerStderr(t *testing.T) {
	skipIfNoShell(t)

	cmd := exec.Command("sh", "-c", "echo out; echo err 1>&2; exit 3")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := childOutput(cmd)
	if string(stdout) != "out\n" {
		t.Errorf("stdout = %q, want %q", stdout, "out\n")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Errorf("ExitCode() = %d, want 3", exitErr.ExitCode())
	}
	if len(exitErr.Stderr) != 0 {
		t.Errorf("exitErr.Stderr = %q, want empty (caller owns stderr)", exitErr.Stderr)
	}
	if stderr.String() != "err\n" {
		t.Errorf("stderr.String() = %q, want %q", stderr.String(), "err\n")
	}
}

func TestChildOutput_AttachesStderrWhenUnset(t *testing.T) {
	skipIfNoShell(t)

	cmd := exec.Command("sh", "-c", "echo out; echo err 1>&2; exit 3")

	stdout, err := childOutput(cmd)
	if string(stdout) != "out\n" {
		t.Errorf("stdout = %q, want %q", stdout, "out\n")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v, want *exec.ExitError", err)
	}
	if !bytes.Equal(exitErr.Stderr, []byte("err\n")) {
		t.Errorf("exitErr.Stderr = %q, want %q", exitErr.Stderr, "err\n")
	}
}

func TestChildCombinedOutput_MergesStreams(t *testing.T) {
	skipIfNoShell(t)

	cmd := exec.Command("sh", "-c", "echo a; echo b 1>&2")
	out, err := childCombinedOutput(cmd)
	if err != nil {
		t.Fatalf("childCombinedOutput() error = %v, want nil", err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "a") || !strings.Contains(outStr, "b") {
		t.Errorf("output = %q, want it to contain both %q and %q", outStr, "a", "b")
	}
}
