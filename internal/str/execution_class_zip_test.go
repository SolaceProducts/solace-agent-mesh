// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package str

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func writeClassZip(t *testing.T, toolsetsDir, marker string) string {
	t.Helper()
	pkgDir := filepath.Join(toolsetsDir, "renderpkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\n" +
		"echo run >> '" + marker + "'\n" +
		`case "$1" in
  --schema) printf '%s' '{"tools":{"render":{"description":"test","parameters":{"type":"object","properties":{"text":{"type":"string"}}}}}}' ;;
  *) exit 0 ;;
esac
`
	manifest := `version: 1
execution_class: isolated
tools:
  render:
    runtime: native
    executable: ./render
    description: "Render"
    parameters:
      - name: text
        type: string
        required: true
`
	writeZipWithModes(t, pkgDir, map[string]zipEntry{
		manifestFileName: {content: []byte(manifest), mode: 0o644},
		"render":         {content: []byte(stub), mode: 0o755},
	})
	if err := os.WriteFile(filepath.Join(pkgDir, "stale_tool"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return pkgDir
}

func TestExecutionClasses_ZipOfAnotherClassIsNeverExtracted(t *testing.T) {
	const toolName = "renderpkg" + PackageToolSep + "render"
	brk := newFlakyBroker(t)
	marker := filepath.Join(t.TempDir(), "ran")
	toolsets := t.TempDir()
	pkgDir := writeClassZip(t, toolsets, marker)

	def := newClassWorker(t, brk, WorkerConfig{WorkerID: "default", ToolsetsDir: toolsets})
	bindClassWorker(t, def)

	for _, name := range []string{"render", "stale_tool"} {
		if _, err := os.Stat(filepath.Join(pkgDir, name)); err == nil {
			t.Errorf("%s is on the default STR's disk", name)
		}
	}
	if _, err := os.Stat(filepath.Join(pkgDir, manifestFileName)); err != nil {
		t.Errorf("the manifest was not kept, so the STR cannot list the tools it does not serve: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pkgDir, "package.zip")); err != nil {
		t.Errorf("the zip was marked extracted, so an STR that later serves the class would not extract it: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the default STR ran the package's executable")
	}
	if brk.bound(def.queue, remotetool.InvokeTopic("test-ns", toolName)) {
		t.Errorf("%s is on the default STR's queue", toolName)
	}
	if _, delegated := def.manifest.DelegatedToolNames()[toolName]; !delegated {
		t.Errorf("the default STR does not list %s as served elsewhere", toolName)
	}
}

func TestExecutionClasses_ZipOfItsOwnClassIsExtracted(t *testing.T) {
	const toolName = "renderpkg" + PackageToolSep + "render"
	brk := newFlakyBroker(t)
	toolsets := t.TempDir()
	pkgDir := writeClassZip(t, toolsets, filepath.Join(t.TempDir(), "ran"))

	runner := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "runner",
		ToolsetsDir:         toolsets,
		ExecutionClassSplit: true,
		ExecutionClass:      isolatedClass,
	})
	bindClassWorker(t, runner)

	if _, err := os.Stat(filepath.Join(pkgDir, "render")); err != nil {
		t.Errorf("the runner STR did not extract the package it serves: %v", err)
	}
	if !brk.bound(runner.queue, remotetool.InvokeTopic("test-ns", toolName)) {
		t.Errorf("%s is not on the runner STR's queue", toolName)
	}
}

func TestFreshZipClass(t *testing.T) {
	t.Run("no zip", func(t *testing.T) {
		if _, found, err := freshZipClass(t.TempDir()); found || err != nil {
			t.Errorf("found = %v, err = %v, want neither", found, err)
		}
	})
	t.Run("zip without a manifest", func(t *testing.T) {
		dir := t.TempDir()
		writeZipWithModes(t, dir, map[string]zipEntry{"tool": {content: []byte("x"), mode: 0o755}})
		if _, found, err := freshZipClass(dir); found || err != nil {
			t.Errorf("found = %v, err = %v, want neither", found, err)
		}
	})
	t.Run("zip with no class", func(t *testing.T) {
		dir := t.TempDir()
		writeZipWithModes(t, dir, map[string]zipEntry{manifestFileName: {content: []byte("version: 1\n"), mode: 0o644}})
		class, found, err := freshZipClass(dir)
		if err != nil || !found || class != "" {
			t.Errorf("class = %q, found = %v, err = %v, want an empty class found", class, found, err)
		}
	})
	t.Run("unreadable manifest", func(t *testing.T) {
		dir := t.TempDir()
		writeZipWithModes(t, dir, map[string]zipEntry{manifestFileName: {content: []byte("tools: [\n"), mode: 0o644}})
		if _, _, err := freshZipClass(dir); err == nil {
			t.Error("a manifest that does not parse gave no error")
		}
	})
}
