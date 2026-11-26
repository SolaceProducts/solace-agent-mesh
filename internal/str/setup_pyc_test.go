// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type fakeCompile struct {
	calls int
	dirs  []string
	err   error
}

func (f *fakeCompile) run(_ context.Context, _ string, dir string, _ *slog.Logger) error {
	f.calls++
	f.dirs = append(f.dirs, dir)
	return f.err
}

func fixedInterpreter(path, version string) func() (string, string) {
	return func() (string, string) { return path, version }
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrecompilePython_SkipsDirsWithoutPython(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifest.yaml"), "version: 1\n")
	writeFile(t, filepath.Join(dir, "tool"), "binary")
	fc := &fakeCompile{}
	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	if fc.calls != 0 {
		t.Fatalf("compileall ran %d times on a directory with no Python sources", fc.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, pycMarkerFile)); err == nil {
		t.Fatal("marker written for a directory that was never compiled")
	}
}

func TestPrecompilePython_RetriesWhenCompileNeverRan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "handler.py"), "x = 1\n")
	fc := &fakeCompile{err: errors.New("fork/exec /usr/bin/python3: no space left on device")}

	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	if _, err := os.Stat(filepath.Join(dir, pycMarkerFile)); err == nil {
		t.Fatal("marker written although compileall failed")
	}
	fc.err = nil
	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	if fc.calls != 2 {
		t.Fatalf("compileall ran %d times, want a retry after the failure", fc.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, pycMarkerFile)); err != nil {
		t.Fatalf("marker not written after the successful retry: %v", err)
	}
}

func TestPrecompilePython_CompilesOncePerInterpreter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "handler.py"), "x = 1\n")
	writeFile(t, filepath.Join(dir, "python", "dep", "__init__.py"), "y = 2\n")
	fc := &fakeCompile{}

	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	if fc.calls != 1 {
		t.Fatalf("compileall ran %d times for the same interpreter, want 1", fc.calls)
	}
	if fc.dirs[0] != dir {
		t.Fatalf("compileall ran on %q, want the tool dir %q", fc.dirs[0], dir)
	}
	marker, err := os.ReadFile(filepath.Join(dir, pycMarkerFile))
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if got := strings.TrimSpace(string(marker)); got != "/usr/bin/python3 3.11.2" {
		t.Fatalf("marker = %q, want interpreter path and version", got)
	}

	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.12.1"), fc.run, slog.Default())
	if fc.calls != 2 {
		t.Fatalf("compileall ran %d times after an interpreter change, want 2", fc.calls)
	}
}

func TestPrecompilePython_NoInterpreterIsANoop(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "handler.py"), "x = 1\n")
	fc := &fakeCompile{}
	precompilePythonWith(t.Context(), dir, fixedInterpreter("", ""), fc.run, slog.Default())
	if fc.calls != 0 {
		t.Fatal("compileall ran without an interpreter")
	}
	if _, err := os.Stat(filepath.Join(dir, pycMarkerFile)); err == nil {
		t.Fatal("marker written without a compile")
	}
}

func TestPrecompilePython_ReadOnlyDirIsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "handler.py"), "x = 1\n")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	fc := &fakeCompile{}
	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	if fc.calls != 0 {
		t.Fatal("compileall ran on a read-only tool dir")
	}
}

func TestPrecompilePython_RejectedModuleStillWritesMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "handler.py"), "x = 1\n")
	fc := &fakeCompile{err: fmt.Errorf("compileall %s: %w", dir, &exec.ExitError{})}
	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	precompilePythonWith(t.Context(), dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	if fc.calls != 1 {
		t.Fatalf("compileall ran %d times after a failure, want 1", fc.calls)
	}
}

func TestRunCompileAll_ProducesBytecode(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "handler.py"), "import dep\n")
	writeFile(t, filepath.Join(dir, "python", "dep", "__init__.py"), "y = 2\n")
	writeFile(t, filepath.Join(dir, "python", "broken.py"), "def (:\n")

	err = runCompileAll(t.Context(), nil, py, dir, slog.Default())
	if err == nil {
		t.Fatal("compileall should report the syntax error in broken.py")
	}
	for _, src := range []string{filepath.Join(dir, "handler.py"), filepath.Join(dir, "python", "dep", "__init__.py")} {
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(src), "__pycache__", strings.TrimSuffix(filepath.Base(src), ".py")+".*.pyc"))
		if len(matches) == 0 {
			t.Errorf("no bytecode produced for %s", src)
		}
	}
}

func TestProcessPackage_FlatPythonPrecompilesBytecode(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "python-test-tool", "tool.py"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool.py"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "python", "dep.py"), "y = 2\n")

	p := NewPackageProcessor(dir, slog.Default())
	tools, errs := p.processPackage(t.Context(), dir, "flatpkg")
	if len(errs) > 0 || len(tools) == 0 {
		t.Fatalf("processPackage: tools=%d errs=%v", len(tools), errs)
	}
	if _, err := os.Stat(filepath.Join(dir, pycMarkerFile)); err != nil {
		t.Fatalf("marker missing after flat package processing: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "python", "__pycache__", "dep.*.pyc"))
	if len(matches) == 0 {
		t.Fatal("no bytecode produced for the python/ layer of a flat package")
	}
}

func TestCompileAllArgs_PinsWorkersToGOMAXPROCS(t *testing.T) {
	args := compileAllArgs("/tools/x")
	want := []string{"-I", "-m", "compileall", "-q", "-j", strconv.Itoa(runtime.GOMAXPROCS(0)), "/tools/x"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %q, want %q", args, want)
	}
}

func TestPrecompilePython_CancelledLeavesNoMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "handler.py"), "x = 1\n")
	fc := &fakeCompile{err: errors.New("signal: killed")}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	precompilePythonWith(ctx, dir, fixedInterpreter("/usr/bin/python3", "3.11.2"), fc.run, slog.Default())
	if fc.calls != 1 {
		t.Fatalf("compileall ran %d times, want 1", fc.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, pycMarkerFile)); err == nil {
		t.Fatal("marker written although the compile was cancelled")
	}
}
