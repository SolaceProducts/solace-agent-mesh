// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScaffoldedGoToolCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes `go build`; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	repo := t.TempDir()
	res, err := Init(Options{
		Name:       "scaffold-check",
		Path:       repo,
		Lang:       LangGo,
		CLIVersion: "test",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(res.SrcDir, "scaffold-check"), ".")
	cmd.Dir = res.SrcDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build failed: %v\n--- stderr ---\n%s", err, stderr.String())
	}
}
