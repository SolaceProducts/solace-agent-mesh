// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var testZipTime = time.Date(2026, 8, 10, 14, 30, 22, 0, time.UTC)

func testDiagnosticsTree(t *testing.T) diagnosticsPaths {
	t.Helper()
	paths, err := prepareDiagnosticsPaths(filepath.Join(t.TempDir(), "diagnostics"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		paths.desktopLog(): "live log line\n",
		filepath.Join(paths.logs, "desktop-2026-08-01T00-00-00.000.log"): "rotated log line\n",
		filepath.Join(paths.crash, "sam-crash-20260801-000000.log"):      "crash traceback\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func readZipEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("archive unreadable: %v", err)
	}
	entries := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		entries[f.Name] = string(content)
	}
	return entries
}

func TestWriteDiagnosticsZipPacksDiagnosticsTree(t *testing.T) {
	paths := testDiagnosticsTree(t)

	var buf bytes.Buffer
	if err := writeDiagnosticsZip(&buf, paths, testZipTime); err != nil {
		t.Fatal(err)
	}
	entries := readZipEntries(t, buf.Bytes())

	want := map[string]string{
		"diagnostics/logs/desktop.log":                         "live log line\n",
		"diagnostics/logs/desktop-2026-08-01T00-00-00.000.log": "rotated log line\n",
		"diagnostics/crash/sam-crash-20260801-000000.log":      "crash traceback\n",
	}
	for name, content := range want {
		if entries[name] != content {
			t.Errorf("entry %s = %q, want %q", name, entries[name], content)
		}
	}

	info := entries["info.txt"]
	if !strings.Contains(info, "samHome:   "+filepath.Dir(paths.root)) {
		t.Errorf("info.txt missing SAM home:\n%s", info)
	}
	if !strings.Contains(info, "createdAt: 2026-08-10T14:30:22Z") {
		t.Errorf("info.txt missing creation time:\n%s", info)
	}
	if _, ok := entries["manifest.txt"]; !ok {
		t.Error("manifest.txt missing from archive")
	}
}

func TestWriteDiagnosticsZipNeverPacksOutsideDiagnostics(t *testing.T) {
	paths := testDiagnosticsTree(t)
	samHome := filepath.Dir(paths.root)
	if err := os.WriteFile(filepath.Join(samHome, "settings.yaml"), []byte("api_key: sk-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := writeDiagnosticsZip(&buf, paths, testZipTime); err != nil {
		t.Fatal(err)
	}
	for name, content := range readZipEntries(t, buf.Bytes()) {
		if name != "info.txt" && name != "manifest.txt" && !strings.HasPrefix(name, "diagnostics/") {
			t.Errorf("archive contains entry outside the diagnostics folder: %s", name)
		}
		if strings.Contains(content, "sk-secret") {
			t.Errorf("archive entry %s leaked settings.yaml content", name)
		}
	}
}

func TestDiagnosticsZipManifestListsEveryEntry(t *testing.T) {
	paths := testDiagnosticsTree(t)

	var buf bytes.Buffer
	if err := writeDiagnosticsZip(&buf, paths, testZipTime); err != nil {
		t.Fatal(err)
	}
	entries := readZipEntries(t, buf.Bytes())

	manifest := entries["manifest.txt"]
	for name, content := range entries {
		if name == "manifest.txt" {
			continue
		}
		wantLine := fmt.Sprintf("%12d  %s", len(content), name)
		if !strings.Contains(manifest, wantLine) {
			t.Errorf("manifest missing %q:\n%s", wantLine, manifest)
		}
	}
}

func TestWriteDiagnosticsZipSkipsDanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	paths := testDiagnosticsTree(t)
	if err := os.Symlink(filepath.Join(paths.logs, "gone.log"), filepath.Join(paths.logs, "dangling.log")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := writeDiagnosticsZip(&buf, paths, testZipTime); err != nil {
		t.Fatalf("dangling symlink must not fail the archive: %v", err)
	}
	if _, ok := readZipEntries(t, buf.Bytes())["diagnostics/logs/dangling.log"]; ok {
		t.Error("dangling symlink packed into archive")
	}
}

func TestDiagnosticsZipFilename(t *testing.T) {
	got := diagnosticsZipFilename("1.4.2", testZipTime)
	want := "solace-agent-mesh-diagnostics-1.4.2-20260810-143022.zip"
	if got != want {
		t.Errorf("diagnosticsZipFilename = %q, want %q", got, want)
	}
}

func TestWriteDiagnosticsZipFileWritesReadableArchive(t *testing.T) {
	paths := testDiagnosticsTree(t)
	dest := filepath.Join(t.TempDir(), "diagnostics.zip")

	if err := writeDiagnosticsZipFile(dest, paths, testZipTime); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("written archive unreadable: %v", err)
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) < 3 {
		t.Errorf("archive has %d entries, want at least info.txt + logs + manifest", len(zr.File))
	}
}

func TestWriteDiagnosticsZipFileRemovesPartialOnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-bit test is POSIX-only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	paths := testDiagnosticsTree(t)
	unreadable := filepath.Join(paths.logs, "unreadable.log")
	if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "diagnostics.zip")
	if err := writeDiagnosticsZipFile(dest, paths, testZipTime); err == nil {
		t.Fatal("want error for unreadable diagnostics file")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("partial archive left behind at %s", dest)
	}
}

func TestResolveDiagnosticsZipDestinationCancelWritesNothing(t *testing.T) {
	downloads := filepath.Join(t.TempDir(), "Downloads")

	dest, err := resolveDiagnosticsZipDestination("", nil, downloads, "diagnostics.zip")
	if err != nil {
		t.Fatal(err)
	}
	if dest != "" {
		t.Errorf("cancel resolved to %q, want no destination", dest)
	}
	if _, err := os.Stat(downloads); !os.IsNotExist(err) {
		t.Errorf("cancel created the fallback dir %s", downloads)
	}
}

func TestResolveDiagnosticsZipDestinationUsesChosenPath(t *testing.T) {
	chosen := filepath.Join(t.TempDir(), "somewhere", "picked.zip")

	dest, err := resolveDiagnosticsZipDestination(chosen, nil, filepath.Join(t.TempDir(), "Downloads"), "diagnostics.zip")
	if err != nil {
		t.Fatal(err)
	}
	if dest != chosen {
		t.Errorf("destination = %q, want the chosen path %q", dest, chosen)
	}
}

func TestResolveDiagnosticsZipDestinationFallsBackWhenDialogFails(t *testing.T) {
	downloads := filepath.Join(t.TempDir(), "Downloads")

	dest, err := resolveDiagnosticsZipDestination("", errors.New("no dialog"), downloads, "diagnostics.zip")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(downloads, "diagnostics.zip"); dest != want {
		t.Errorf("destination = %q, want %q", dest, want)
	}
	if info, err := os.Stat(downloads); err != nil || !info.IsDir() {
		t.Errorf("fallback dir %s was not created: %v", downloads, err)
	}
}

func TestResolveDiagnosticsZipDestinationReportsUncreatableFallback(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "Downloads")
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	dest, err := resolveDiagnosticsZipDestination("", errors.New("no dialog"), blocked, "diagnostics.zip")
	if err == nil {
		t.Fatal("want an error when the fallback dir cannot be created")
	}
	if dest != "" {
		t.Errorf("destination = %q, want none on error", dest)
	}
}
