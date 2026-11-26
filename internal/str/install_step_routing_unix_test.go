// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package str

import (
	"bufio"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func recordingBwrapRunner(t *testing.T) (*SandboxRunner, string) {
	t.Helper()
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv.log")
	fake := filepath.Join(dir, "fake-prlimit")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" >> '" + argvFile + "'\n" +
		"case \" $* \" in *\" --schema \"*) " +
		"printf '{\"tools\":{\"t\":{\"description\":\"d\",\"parameters\":{\"type\":\"object\",\"properties\":{}}}}}\\n' ;; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:        "bwrap",
			BwrapBin:    "/usr/bin/bwrap",
			PrlimitBin:  fake,
			WorkBaseDir: t.TempDir(),
		},
		logger:    slog.Default(),
		freshProc: true,
	}
	r.cfg.setDefaults()
	return r, argvFile
}

func recordedArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	f, err := os.Open(argvFile)
	if err != nil {
		t.Fatalf("wrapper not invoked (no argv recorded): %v", err)
	}
	defer func() { _ = f.Close() }()
	var args []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		args = append(args, sc.Text())
	}
	return args
}

func TestProbeSchema_BindsToolDirReadOnlyUnderBwrap(t *testing.T) {
	r, argvFile := recordingBwrapRunner(t)
	toolDir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(toolDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probeSchema(r, "/bin/sh", toolDir, "", nil, "--schema"); err != nil {
		t.Fatalf("probeSchema: %v", err)
	}
	args := recordedArgv(t, argvFile)
	if findFlagTriple(args, "--ro-bind", resolved, toolDir) < 0 {
		t.Fatalf("--schema must bind the tool dir read-only; argv = %v", args)
	}
	if findFlagTriple(args, "--bind", resolved, toolDir) >= 0 {
		t.Fatalf("--schema must NOT bind the tool dir writable; argv = %v", args)
	}
}

func TestProcessFlatPython_BindsToolDirReadOnlyUnderBwrap(t *testing.T) {
	r, argvFile := recordingBwrapRunner(t)
	p := NewPackageProcessor(t.TempDir(), slog.Default())
	p.SetRunner(r)
	pkgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkgDir, "probe.py"), []byte("# flat python tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, errs := p.processFlatPythonPackage(context.Background(), pkgDir, "pypkg"); len(errs) != 0 {
		t.Fatalf("processFlatPythonPackage: %v", errs)
	}
	args := recordedArgv(t, argvFile)
	if findFlagTriple(args, "--ro-bind", resolved, pkgDir) < 0 {
		t.Fatalf("flat-Python --schema must bind the tool dir read-only; argv = %v", args)
	}
}

func TestRunCompileAll_BindsToolDirWritableUnderBwrap(t *testing.T) {
	r, argvFile := recordingBwrapRunner(t)
	toolDir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(toolDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := runCompileAll(context.Background(), r, "/bin/sh", toolDir, slog.Default()); err != nil {
		t.Fatalf("runCompileAll: %v", err)
	}
	args := recordedArgv(t, argvFile)
	if findFlagTriple(args, "--bind", resolved, toolDir) < 0 {
		t.Fatalf("compileall must bind the tool dir writable; argv = %v", args)
	}
}

func TestRunSetupScript_BindsToolDirWritableUnderBwrap(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("setup.sh needs bash")
	}
	r, argvFile := recordingBwrapRunner(t)
	toolDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(toolDir, "setup.sh"), []byte("#!/bin/bash\n:\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(toolDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := runSetupScript(context.Background(), r, toolDir, true, slog.Default()); err != nil {
		t.Fatalf("runSetupScript: %v", err)
	}
	args := recordedArgv(t, argvFile)
	if findFlagTriple(args, "--bind", resolved, toolDir) < 0 {
		t.Fatalf("setup.sh must bind the tool dir writable; argv = %v", args)
	}
}

func TestResolveConfigAwareSchema_BindsConfigDirUnderBwrap(t *testing.T) {
	r, argvFile := recordingBwrapRunner(t)
	w := &Worker{logger: slog.Default()}
	w.runner = r
	entry := &ManifestEntry{ToolName: "t", Executable: "/bin/sh", ToolDir: t.TempDir()}
	if _, err := w.resolveConfigAwareToolSchema(slog.Default(), entry, map[string]any{"k": "v"}); err != nil {
		t.Fatalf("resolveConfigAwareToolSchema: %v", err)
	}
	args := recordedArgv(t, argvFile)
	if !hasRoBindContaining(args, "str-init-") {
		t.Fatalf("the config dir must be bound read-only past private /tmp; argv = %v", args)
	}
}

func hasRoBindContaining(args []string, substr string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--ro-bind" && strings.Contains(args[i+1], substr) {
			return true
		}
	}
	return false
}
