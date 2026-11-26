// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func fixtureTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"manifest.yaml":      "name: example\n",
		"src/main.py":        "print('hi')\n",
		"src/utils/lib.py":   "x = 1\n",
		"README.md":          "# example\n",
		".git/HEAD":          "ref: refs/heads/main\n",
		".DS_Store":          "noise",
		"src/__pycache__/x":  "noise",
		"node_modules/x.txt": "noise",
		"dist/build.bin":     "noise",
		"src/cache.pyc":      "compiled",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dir
}

func TestBundleDir_IncludesExpectedAndExcludesNoise(t *testing.T) {
	dir := fixtureTree(t)

	data, hash, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir: %v", err)
	}
	if hash == "" || len(hash) != 64 {
		t.Errorf("hash should be 64-char hex, got %q", hash)
	}

	got := zipFiles(t, data)

	requiredFiles := []string{"README.md", "manifest.yaml", "src/main.py", "src/utils/lib.py"}
	for _, want := range requiredFiles {
		if _, ok := got[want]; !ok {
			t.Errorf("expected %q in zip; got %v", want, sortedKeys(got))
		}
	}

	notWanted := []string{
		".git/HEAD",
		".DS_Store",
		"src/__pycache__/x",
		"src/cache.pyc",
		"node_modules/x.txt",
		"dist/build.bin",
	}
	cacheBlob := filepath.Join(dir, ".sam-cache", "build", "darwin-arm64", "abc.bin")
	if mkErr := os.MkdirAll(filepath.Dir(cacheBlob), 0o755); mkErr != nil {
		t.Fatalf("mkdir cache: %v", mkErr)
	}
	if wErr := os.WriteFile(cacheBlob, []byte("binary-noise"), 0o644); wErr != nil {
		t.Fatalf("write cache: %v", wErr)
	}
	data2, _, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir post-cache: %v", err)
	}
	got2 := zipFiles(t, data2)
	if _, ok := got2[".sam-cache/build/darwin-arm64/abc.bin"]; ok {
		t.Error(".sam-cache contents leaked into the zip")
	}
	for _, bad := range notWanted {
		if _, ok := got[bad]; ok {
			t.Errorf("did not expect %q in zip; got %v", bad, sortedKeys(got))
		}
	}
}

func TestBundleDir_Reproducible(t *testing.T) {
	dir := fixtureTree(t)

	first, hashA, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("first BundleDir: %v", err)
	}
	second, hashB, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("second BundleDir: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("BundleDir output is not byte-identical across runs (lens: %d vs %d)", len(first), len(second))
	}
	if hashA != hashB {
		t.Errorf("hashes differ across runs: %s vs %s", hashA, hashB)
	}

	sum := sha256.Sum256(first)
	if got := hex.EncodeToString(sum[:]); got != hashA {
		t.Errorf("reported hash %s != SHA-256 of bytes %s", hashA, got)
	}
}

func TestBundleDir_ContentChangeYieldsDifferentHash(t *testing.T) {
	dir := fixtureTree(t)
	_, hashA, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir A: %v", err)
	}

	if writeErr := os.WriteFile(filepath.Join(dir, "src", "main.py"), []byte("print('hello world')\n"), 0o644); writeErr != nil {
		t.Fatalf("rewrite: %v", writeErr)
	}
	_, hashB, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir B: %v", err)
	}
	if hashA == hashB {
		t.Errorf("hash unchanged after content edit: %s", hashA)
	}
}

func TestBundleDir_PreservesExecutableBit(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}
	plainPath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(plainPath, []byte("# tool\n"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}

	if err := os.Chmod(execPath, 0o755); err != nil {
		t.Fatalf("chmod exec: %v", err)
	}
	if err := os.Chmod(plainPath, 0o644); err != nil {
		t.Fatalf("chmod plain: %v", err)
	}

	data, _, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir: %v", err)
	}

	modes := zipFileModes(t, data)

	if got := modes["bin/tool"].Perm(); got != 0o755 {
		t.Errorf("expected bin/tool to be 0o755 in zip, got %o", got)
	}
	if got := modes["README.md"].Perm(); got != 0o644 {
		t.Errorf("expected README.md to be 0o644 in zip, got %o", got)
	}
}

func TestBundleDir_ExecBitChangeYieldsDifferentHash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot store the executable bit, so chmod changes nothing about the tree; " +
			"TestBundleDir_RunnableContentChangeYieldsDifferentHash covers bundle identity there")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(bin, 0o644); err != nil {
		t.Fatalf("chmod 644: %v", err)
	}
	_, hashA, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir A: %v", err)
	}
	if chmodErr := os.Chmod(bin, 0o755); chmodErr != nil {
		t.Fatalf("chmod 755: %v", chmodErr)
	}
	_, hashB, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir B: %v", err)
	}
	if hashA == hashB {
		t.Errorf("hash unchanged after exec bit flip: %s", hashA)
	}
}

func TestBundleDir_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "not-a-dir.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := BundleDir(f); err == nil {
		t.Fatal("expected error when pointed at a file")
	}
}

func TestLoadZip_HashMatchesContents(t *testing.T) {
	dir := fixtureTree(t)
	data, hashA, err := BundleDir(dir)
	if err != nil {
		t.Fatalf("BundleDir: %v", err)
	}

	zipPath := filepath.Join(t.TempDir(), "common.zip")
	if writeErr := os.WriteFile(zipPath, data, 0o644); writeErr != nil {
		t.Fatalf("write zip: %v", writeErr)
	}

	loaded, hashB, err := LoadZip(zipPath)
	if err != nil {
		t.Fatalf("LoadZip: %v", err)
	}
	if !bytes.Equal(data, loaded) {
		t.Errorf("LoadZip returned different bytes than written")
	}
	if hashA != hashB {
		t.Errorf("hash mismatch: BundleDir=%s LoadZip=%s", hashA, hashB)
	}
}

func TestLoadZip_MissingFile(t *testing.T) {
	if _, _, err := LoadZip(filepath.Join(t.TempDir(), "nope.zip")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func zipFiles(t *testing.T, data []byte) map[string]string {
	t.Helper()
	rdr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := make(map[string]string)
	for _, f := range rdr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(body)
	}
	return out
}

func zipFileModes(t *testing.T, data []byte) map[string]os.FileMode {
	t.Helper()
	rdr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := make(map[string]os.FileMode)
	for _, f := range rdr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		out[f.Name] = f.Mode()
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
