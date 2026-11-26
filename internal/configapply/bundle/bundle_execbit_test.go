// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package bundle

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func forceWindowsHost(t *testing.T) {
	t.Helper()
	original := hostGOOS
	hostGOOS = "windows"
	t.Cleanup(func() { hostGOOS = original })
}

func TestBundleDir_WindowsHostInfersExecutableEntries(t *testing.T) {
	forceWindowsHost(t)

	dir := t.TempDir()
	files := map[string][]byte{
		"tool":          {0x7f, 'E', 'L', 'F', 0x02, 0x01},
		"entrypoint.py": []byte("#!/usr/bin/env python3\nprint('hi')\n"),
		"launch":        []byte("#!/bin/sh\nexec ./tool\n"),
		"manifest.yaml": []byte("name: tool\n"),
		"README.md":     []byte("# docs\n"),
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chmod(path, 0o666); err != nil {
			t.Fatalf("chmod %s: %v", name, err)
		}
	}

	data, _, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir: %v", err)
	}
	modes := zipFileModes(t, data)

	for _, name := range []string{"tool", "entrypoint.py", "launch"} {
		if got := modes[name].Perm(); got != 0o755 {
			t.Errorf("expected %s to be 0o755 in zip, got %o", name, got)
		}
	}
	for _, name := range []string{"manifest.yaml", "README.md"} {
		if got := modes[name].Perm(); got != 0o644 {
			t.Errorf("expected %s to be 0o644 in zip, got %o", name, got)
		}
	}
}

func TestBundleDir_WindowsHostMatchesUnixForTheSameTree(t *testing.T) {
	build := func(t *testing.T, execMode os.FileMode) map[string]os.FileMode {
		t.Helper()
		dir := t.TempDir()
		binary := filepath.Join(dir, "tool")
		if err := os.WriteFile(binary, []byte{0x7f, 'E', 'L', 'F', 0x02}, 0o644); err != nil {
			t.Fatalf("write binary: %v", err)
		}
		if err := os.Chmod(binary, execMode); err != nil {
			t.Fatalf("chmod binary: %v", err)
		}
		doc := filepath.Join(dir, "README.md")
		if err := os.WriteFile(doc, []byte("# docs\n"), 0o644); err != nil {
			t.Fatalf("write doc: %v", err)
		}
		if err := os.Chmod(doc, 0o644); err != nil {
			t.Fatalf("chmod doc: %v", err)
		}
		data, _, err := BundleDir(dir)
		if err != nil {
			t.Fatalf("BundleDir: %v", err)
		}
		return zipFileModes(t, data)
	}

	unixModes := build(t, 0o755)
	forceWindowsHost(t)
	windowsModes := build(t, 0o666)

	for _, name := range []string{"tool", "README.md"} {
		if unixModes[name].Perm() != windowsModes[name].Perm() {
			t.Errorf("%s: unix host produced %o, windows host produced %o",
				name, unixModes[name].Perm(), windowsModes[name].Perm())
		}
	}
}

func TestBundleDir_RunnableContentChangeYieldsDifferentHash(t *testing.T) {
	forceWindowsHost(t)

	dir := t.TempDir()
	script := filepath.Join(dir, "tool")
	if err := os.WriteFile(script, []byte("echo hi\n"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}
	_, plainHash, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir plain: %v", err)
	}

	if writeErr := os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o644); writeErr != nil {
		t.Fatalf("write shebang: %v", writeErr)
	}
	_, shebangHash, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir shebang: %v", err)
	}

	if plainHash == shebangHash {
		t.Errorf("hash unchanged after the file became runnable: %s", plainHash)
	}
}

func TestBundleDirWithOverlay_WindowsHostInfersSkillEntries(t *testing.T) {
	forceWindowsHost(t)

	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatalf("mkdir tools: %v", err)
	}
	files := map[string]struct {
		path    string
		content []byte
	}{
		"prebuilt binary": {filepath.Join(toolsDir, "lookup"), []byte{0x7f, 'E', 'L', 'F', 0x02}},
		"skill manifest":  {filepath.Join(dir, "manifest.yaml"), []byte("name: lookup\n")},
		"skill prose":     {filepath.Join(dir, "SKILL.md"), []byte("# lookup\n")},
	}
	for label, f := range files {
		if err := os.WriteFile(f.path, f.content, 0o644); err != nil {
			t.Fatalf("write %s: %v", label, err)
		}
		if err := os.Chmod(f.path, 0o666); err != nil {
			t.Fatalf("chmod %s: %v", label, err)
		}
	}

	overlay := map[string][]byte{"tools/generated": {0x7f, 'E', 'L', 'F', 0x02}}
	data, _, err := BundleDirWithOverlay(dir, overlay)
	if err != nil {
		t.Fatalf("BundleDirWithOverlay: %v", err)
	}
	modes := zipFileModes(t, data)

	for _, name := range []string{"tools/lookup", "tools/generated"} {
		if got := modes[name].Perm(); got != 0o755 {
			t.Errorf("expected %s to be 0o755 in zip, got %o", name, got)
		}
	}
	for _, name := range []string{"manifest.yaml", "SKILL.md"} {
		if got := modes[name].Perm(); got != 0o644 {
			t.Errorf("expected %s to be 0o644 in zip, got %o", name, got)
		}
	}
}

type zipOrderedEntry struct {
	name string
	mode os.FileMode
}

func zipOrderedEntries(t *testing.T, data []byte) []zipOrderedEntry {
	t.Helper()
	rdr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := make([]zipOrderedEntry, 0, len(rdr.File))
	for _, f := range rdr.File {
		out = append(out, zipOrderedEntry{name: f.Name, mode: f.Mode()})
	}
	return out
}

func TestBundleDirWithOverlay_ReplacementUsesOverlayBytes(t *testing.T) {
	forceWindowsHost(t)

	dir := t.TempDir()
	promoted := filepath.Join(dir, "promoted")
	if err := os.WriteFile(promoted, []byte("placeholder\n"), 0o644); err != nil {
		t.Fatalf("write promoted: %v", err)
	}
	demoted := filepath.Join(dir, "demoted")
	if err := os.WriteFile(demoted, []byte{0x7f, 'E', 'L', 'F', 0x02}, 0o644); err != nil {
		t.Fatalf("write demoted: %v", err)
	}
	for _, p := range []string{promoted, demoted} {
		if err := os.Chmod(p, 0o666); err != nil {
			t.Fatalf("chmod %s: %v", p, err)
		}
	}

	data, _, err := BundleDirWithOverlay(dir, map[string][]byte{
		"promoted": {0x7f, 'E', 'L', 'F', 0x02},
		"demoted":  []byte("name: tool\n"),
	})
	if err != nil {
		t.Fatalf("BundleDirWithOverlay: %v", err)
	}

	modes := zipFileModes(t, data)
	if got := modes["promoted"].Perm(); got != 0o755 {
		t.Errorf("promoted: overlay ships a binary, want 0o755, got %o", got)
	}
	if got := modes["demoted"].Perm(); got != 0o644 {
		t.Errorf("demoted: overlay ships data, want 0o644, got %o", got)
	}
}
