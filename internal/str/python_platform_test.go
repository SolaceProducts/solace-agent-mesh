// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParsePyListPaths(t *testing.T) {
	out := "Installed Pythons found by py Launcher for Windows\n" +
		" -V:3.12 *        C:\\Users\\me\\AppData\\Local\\Programs\\Python\\Python312\\python.exe\n" +
		" -V:3.11          C:\\Program Files\\Python311\\python.exe\n" +
		" -3.9-64          C:\\Python39\\python.exe\n" +
		" -V:3.13          \\\\nas\\python\\Python313\\python.exe\n" +
		" -V:3.10          C:/Python310/python.exe\n" +
		" -V:3.8           C:\\a\\python.exe  D:\\b\\pythonw.exe\n"

	got := parsePyListPaths(out)
	want := []string{
		`C:\Users\me\AppData\Local\Programs\Python\Python312\python.exe`,
		`C:\Program Files\Python311\python.exe`,
		`C:\Python39\python.exe`,
		`\\nas\python\Python313\python.exe`,
		`C:/Python310/python.exe`,
		`C:\a\python.exe`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("parsePyListPaths =\n%#v\nwant\n%#v", got, want)
	}
}

func TestWindowsPythonGlobs_CoverStoreAndPythonOrgLayouts(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\me\AppData\Local`)
	t.Setenv("ProgramFiles", `C:\Program Files`)

	joined := strings.Join(windowsPythonGlobs(), "\n")
	for _, marker := range []string{"Programs", "WindowsApps", "PythonSoftwareFoundation.Python.3.", "Python3*"} {
		if !strings.Contains(joined, marker) {
			t.Errorf("windowsPythonGlobs missing %q; globs = %v", marker, windowsPythonGlobs())
		}
	}
}

func TestLinuxPythonDirs_IncludesStandardBinDirs(t *testing.T) {
	dirs := linuxPythonDirs()
	for _, want := range []string{"/usr/local/bin", "/usr/bin"} {
		if !slices.Contains(dirs, want) {
			t.Errorf("linuxPythonDirs missing %q; got %v", want, dirs)
		}
	}
}

func TestPyLauncherPath_ResolvesAbsoluteLauncher(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "py.exe")
	if err := os.WriteFile(launcher, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SystemRoot", root)
	t.Setenv("LOCALAPPDATA", "")

	if got := pyLauncherPath(); got != launcher {
		t.Fatalf("pyLauncherPath() = %q; want the absolute %q (a bare \"py\" on PATH is a security hotspot)", got, launcher)
	}
}

func TestPyLauncherPath_EmptyWhenAbsent(t *testing.T) {
	t.Setenv("SystemRoot", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())

	if got := pyLauncherPath(); got != "" {
		t.Fatalf("pyLauncherPath() = %q; want \"\" when no launcher is installed", got)
	}
}

func TestProbePythonVersionContext_TimesOutOnHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sleeper script")
	}
	sleeper := filepath.Join(t.TempDir(), "python3")
	if err := os.WriteFile(sleeper, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := probePythonVersionContext(ctx, sleeper)

	if got != "" {
		t.Fatalf("probePythonVersionContext on a hanging interpreter = %q; want \"\"", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("probe ignored the context deadline; took %s (a hung interpreter would wedge STR startup)", elapsed)
	}
}
