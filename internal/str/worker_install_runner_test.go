// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package str

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerInit_RunsTheFirstPackageScanThroughItsRunner(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("STR_BUCKET_NAME", "")
	toolsets := t.TempDir()
	workBase := t.TempDir()
	pkgDir := filepath.Join(toolsets, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	zf, err := os.Create(filepath.Join(pkgDir, "package.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	fw, err := zw.Create("setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fw.Write([]byte("#!/bin/bash\necho \"$TMPDIR\" > tmpdir.txt\n")); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = zf.Close(); err != nil {
		t.Fatal(err)
	}

	w := newTestWorker(t, &testServices{brk: newTestBroker(t)}, writeTestManifest(t))
	w.cfg.ToolsetsDir = toolsets
	w.cfg.SkillsDir = t.TempDir()
	w.cfg.Sandbox.WorkBaseDir = workBase
	if err = w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(pkgDir, "tmpdir.txt"))
	if err != nil {
		t.Fatalf("setup.sh did not run in the package dir: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(got)), workBase) {
		t.Fatalf("first scan ran setup.sh outside the runner: TMPDIR=%q", got)
	}
	if w.manifest.runner != w.runner || w.packageProcessor.runner != w.runner || w.skillRegistry.runner != w.runner {
		t.Fatal("every install path must hold the worker's runner")
	}
}
