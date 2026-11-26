// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestCrashSinkWritesIdentifyingHeader(t *testing.T) {
	paths := testPaths(t)
	id := newRunIdentity()

	sink, err := installCrashSink(paths, id, time.Now())
	if err != nil {
		t.Fatalf("installCrashSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.release() })

	data, err := os.ReadFile(sink.path)
	if err != nil {
		t.Fatalf("read crash file: %v", err)
	}
	header := string(data)
	if int64(len(data)) != sink.headerBytes {
		t.Errorf("crash file is %d bytes, want headerBytes %d", len(data), sink.headerBytes)
	}
	if !strings.Contains(header, id.RunID) {
		t.Error("crash header omits the run id, so a traceback cannot be tied to its log lines")
	}
	if !strings.Contains(header, id.OS) || !strings.Contains(header, id.Arch) {
		t.Errorf("crash header omits the platform:\n%s", header)
	}
}

func TestCrashSinkReleaseDeletesHeaderOnlyFile(t *testing.T) {
	paths := testPaths(t)

	sink, err := installCrashSink(paths, newRunIdentity(), time.Now())
	if err != nil {
		t.Fatalf("installCrashSink: %v", err)
	}
	if err := sink.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(sink.path); !os.IsNotExist(err) {
		t.Error("a clean exit must not leave a header-only crash file behind")
	}
}

func TestInstallCrashSinkLeavesNoFileWhenRegistrationFails(t *testing.T) {
	paths := testPaths(t)

	original := setCrashOutput
	setCrashOutput = func(*os.File, debug.CrashOptions) error {
		return errors.New("registration refused")
	}
	t.Cleanup(func() { setCrashOutput = original })

	if _, err := installCrashSink(paths, newRunIdentity(), time.Now()); err == nil {
		t.Fatal("installCrashSink returned no error when registration failed")
	}

	entries, err := os.ReadDir(paths.crash)
	if err != nil {
		t.Fatalf("read crash dir: %v", err)
	}
	for _, e := range entries {
		t.Errorf("failed install left %q behind", e.Name())
	}
}

func TestCrashSinkReleaseIsSafeWhenUninstalled(t *testing.T) {
	var empty crashSink
	if err := empty.release(); err != nil {
		t.Errorf("release on an uninstalled sink: %v", err)
	}
}

func TestCrashSinkRetainsAppendedTraceback(t *testing.T) {
	paths := testPaths(t)

	sink, err := installCrashSink(paths, newRunIdentity(), time.Now())
	if err != nil {
		t.Fatalf("installCrashSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.release() })

	f, err := os.OpenFile(sink.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open crash file for append: %v", err)
	}
	if _, werr := f.WriteString("panic: boom\n\ngoroutine 1 [running]:\n"); werr != nil {
		t.Fatalf("append traceback: %v", werr)
	}
	if cerr := f.Close(); cerr != nil {
		t.Fatalf("close crash file: %v", cerr)
	}

	data, err := os.ReadFile(sink.path)
	if err != nil {
		t.Fatalf("read crash file: %v", err)
	}
	if !strings.Contains(string(data), "=== SAM Desktop crash report ===") {
		t.Error("traceback overwrote the identifying header")
	}
	if !strings.Contains(string(data), "panic: boom") {
		t.Error("traceback did not survive alongside the header")
	}
}

func TestPruneCrashFilesKeepsNewest(t *testing.T) {
	paths := testPaths(t)
	stamps := []string{
		"20260101-000000", "20260102-000000", "20260103-000000",
		"20260104-000000", "20260105-000000", "20260106-000000", "20260107-000000",
	}
	for _, s := range stamps {
		name := filepath.Join(paths.crash, "sam-crash-"+s+".log")
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed crash file: %v", err)
		}
	}
	unrelated := filepath.Join(paths.crash, "notes.txt")
	if err := os.WriteFile(unrelated, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("seed unrelated file: %v", err)
	}

	if err := pruneCrashFiles(paths.crash, crashFilesRetained); err != nil {
		t.Fatalf("pruneCrashFiles: %v", err)
	}

	for _, s := range stamps[:2] {
		if _, err := os.Stat(filepath.Join(paths.crash, "sam-crash-"+s+".log")); !os.IsNotExist(err) {
			t.Errorf("crash file %s should have been pruned", s)
		}
	}
	for _, s := range stamps[2:] {
		if _, err := os.Stat(filepath.Join(paths.crash, "sam-crash-"+s+".log")); err != nil {
			t.Errorf("crash file %s should have been retained: %v", s, err)
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Error("pruning removed a file it does not own")
	}
}

func TestPruneCrashFilesUnderLimitIsNoop(t *testing.T) {
	paths := testPaths(t)
	name := filepath.Join(paths.crash, "sam-crash-20260101-000000.log")
	if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed crash file: %v", err)
	}
	if err := pruneCrashFiles(paths.crash, crashFilesRetained); err != nil {
		t.Fatalf("pruneCrashFiles: %v", err)
	}
	if _, err := os.Stat(name); err != nil {
		t.Errorf("sole crash file was pruned: %v", err)
	}
}

func TestPrepareDiagnosticsPathsCreatesLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), diagnosticsDirName)
	paths, err := prepareDiagnosticsPaths(root)
	if err != nil {
		t.Fatalf("prepareDiagnosticsPaths: %v", err)
	}
	for _, dir := range []string{paths.logs, paths.crash} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
	if got, want := paths.desktopLog(), filepath.Join(root, "logs", "desktop.log"); got != want {
		t.Errorf("desktopLog() = %q, want %q", got, want)
	}
}

func TestResolveDiagnosticsPathsIgnoresDataDir(t *testing.T) {
	home := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("SAM_HOME", home)
	t.Setenv("SAM_DATA_DIR", dataDir)

	paths, err := resolveDiagnosticsPaths()
	if err != nil {
		t.Fatalf("resolveDiagnosticsPaths: %v", err)
	}

	if want := filepath.Join(home, diagnosticsDirName); paths.root != want {
		t.Errorf("diagnostics root = %q, want %q", paths.root, want)
	}
	if strings.HasPrefix(paths.root, dataDir) {
		t.Errorf("diagnostics folder followed SAM_DATA_DIR into %q", paths.root)
	}
}

func TestRunIdentityIsUnique(t *testing.T) {
	first, second := newRunIdentity(), newRunIdentity()
	if first.RunID == "" {
		t.Fatal("run id is empty")
	}
	if first.RunID == second.RunID {
		t.Error("two launches produced the same run id")
	}
}

func testPaths(t *testing.T) diagnosticsPaths {
	t.Helper()
	paths, err := prepareDiagnosticsPaths(filepath.Join(t.TempDir(), diagnosticsDirName))
	if err != nil {
		t.Fatalf("prepareDiagnosticsPaths: %v", err)
	}
	return paths
}

func TestDiagnosticsPathsOrResolveReusesCachedPaths(t *testing.T) {
	cached, err := prepareDiagnosticsPaths(filepath.Join(t.TempDir(), "diagnostics"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := diagnosticsPathsOrResolve(cached)
	if err != nil {
		t.Fatal(err)
	}
	if got != cached {
		t.Errorf("resolved %+v, want the cached paths %+v", got, cached)
	}
}

func TestDiagnosticsPathsOrResolveRecoversFromEmptyCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SAM_HOME", home)

	got, err := diagnosticsPathsOrResolve(diagnosticsPaths{})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, diagnosticsDirName); got.root != want {
		t.Errorf("root = %q, want %q", got.root, want)
	}
	if info, err := os.Stat(got.logs); err != nil || !info.IsDir() {
		t.Errorf("logs dir %s not created: %v", got.logs, err)
	}
}

func TestDesktopLogHandlerMasksSecrets(t *testing.T) {
	var buf strings.Builder
	log := slog.New(newDesktopLogHandler(&buf))

	log.Info("connecting",
		"api_key", "sk-live-must-not-appear",
		"client_secret", "shhh-must-not-appear",
		"password", "hunter2-must-not-appear",
		"agentName", "orchestrator",
	)

	out := buf.String()
	if strings.Contains(out, "must-not-appear") {
		t.Errorf("secret value survived into the shareable desktop log:\n%s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected redaction marker in output:\n%s", out)
	}
	if !strings.Contains(out, "orchestrator") {
		t.Errorf("masking must not swallow non-secret attributes:\n%s", out)
	}
}
