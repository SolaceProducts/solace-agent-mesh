// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package str

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSetupScript_RunsInThePackageDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte("#!/bin/bash\npwd > cwd.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := NewSandboxRunner(SandboxConfig{Mode: "direct", WorkBaseDir: t.TempDir()}, slog.Default())
	if err := runSetupScript(t.Context(), runner, dir, true, slog.Default()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "cwd.txt"))
	if err != nil {
		t.Fatalf("relative write did not land in the package dir: %v", err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if gotDir, _ := filepath.EvalSymlinks(strings.TrimSpace(string(got))); gotDir != want {
		t.Fatalf("cwd = %q, want %q", gotDir, want)
	}
}

func TestRunSetupScript_MarkerDoesNotFollowALinkOutOfThePackage(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/bash\nln -s " + victim + " .sam-setup-done\n"
	if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runSetupScript(t.Context(), nil, dir, true, slog.Default()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "original" {
		t.Fatalf("marker write followed the link: victim = %q", got)
	}
}
