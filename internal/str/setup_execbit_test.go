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
	"testing"
)

type zipEntry struct {
	content []byte
	mode    os.FileMode
}

func writeZipWithModes(t *testing.T, dir string, entries map[string]zipEntry) string {
	t.Helper()
	path := filepath.Join(dir, "package.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	for name, e := range entries {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(e.mode)
		w, createErr := zw.CreateHeader(header)
		if createErr != nil {
			t.Fatalf("create entry %s: %v", name, createErr)
		}
		if _, writeErr := w.Write(e.content); writeErr != nil {
			t.Fatalf("write entry %s: %v", name, writeErr)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return path
}

func TestExtractZipFile_GrantsExecToRunnableEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
	}{
		{"windows packaged bundle", 0o644},
		{"windows explorer zip", 0o666},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			zipPath := writeZipWithModes(t, dir, map[string]zipEntry{
				"tool":          {[]byte{0x7f, 'E', 'L', 'F', 0x02, 0x01}, tc.mode},
				"entrypoint.py": {[]byte("#!/usr/bin/env python3\nprint('hi')\n"), tc.mode},
				"manifest.yaml": {[]byte("name: tool\n"), tc.mode},
			})

			dest := t.TempDir()
			if err := extractZipFile(zipPath, dest); err != nil {
				t.Fatalf("extractZipFile: %v", err)
			}

			for _, name := range []string{"tool", "entrypoint.py"} {
				info, err := os.Stat(filepath.Join(dest, name))
				if err != nil {
					t.Fatalf("stat %s: %v", name, err)
				}
				perm := info.Mode().Perm()
				if perm&0o111 == 0 {
					t.Errorf("%s extracted as %v; the STR cannot execute it", name, perm)
				}
				if perm&0o022 != 0 {
					t.Errorf("%s extracted as %v; an executable must not be group- or world-writable", name, perm)
				}
			}

			info, err := os.Stat(filepath.Join(dest, "manifest.yaml"))
			if err != nil {
				t.Fatalf("stat manifest.yaml: %v", err)
			}
			if info.Mode().Perm()&0o111 != 0 {
				t.Errorf("manifest.yaml extracted as %v; it is not something the STR runs", info.Mode().Perm())
			}
		})
	}
}

func TestExtractZipFile_KeepsSuppliedExecutableMode(t *testing.T) {
	dir := t.TempDir()
	zipPath := writeZipWithModes(t, dir, map[string]zipEntry{
		"tool":      {[]byte{0x7f, 'E', 'L', 'F', 0x02}, 0o755},
		"README.md": {[]byte("# docs\n"), 0o644},
	})

	dest := t.TempDir()
	if err := extractZipFile(zipPath, dest); err != nil {
		t.Fatalf("extractZipFile: %v", err)
	}

	for name, want := range map[string]os.FileMode{"tool": 0o755, "README.md": 0o644} {
		info, err := os.Stat(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s extracted as %v, want %v", name, got, want)
		}
	}
}

func writeZipRawEntry(t *testing.T, dir, name string, creatorHost uint16, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, "raw.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.CreatorVersion = creatorHost << 8
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return path
}

func TestExtractZipFile_EntryWithNoUsableMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		host uint16
	}{
		{"unmapped creator host", 10},
		{"another unmapped host", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			zipPath := writeZipRawEntry(t, dir, "tool", tc.host, []byte{0x7f, 'E', 'L', 'F', 0x02})

			dest := t.TempDir()
			if err := extractZipFile(zipPath, dest); err != nil {
				t.Fatalf("extractZipFile: %v", err)
			}
			info, err := os.Stat(filepath.Join(dest, "tool"))
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			perm := info.Mode().Perm()
			if perm&0o111 == 0 {
				t.Errorf("tool extracted %v; the STR cannot execute it", perm)
			}
			if perm&0o400 == 0 {
				t.Errorf("tool extracted %v; it is not even readable", perm)
			}
		})
	}
}

func TestExtractZipFile_NormalisesHostileModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    os.FileMode
		content []byte
		want    os.FileMode
	}{
		{"setuid executable", 0o4755, []byte{0x7f, 'E', 'L', 'F'}, 0o755},
		{"world-writable data", 0o666, []byte("name: tool\n"), 0o644},
		{"exec without read", 0o111, []byte{0x7f, 'E', 'L', 'F'}, 0o755},
		{"owner-only runnable", 0o600, []byte("#!/bin/sh\ntrue\n"), 0o755},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			zipPath := writeZipWithModes(t, dir, map[string]zipEntry{"entry": {tc.content, tc.mode}})

			dest := t.TempDir()
			if err := extractZipFile(zipPath, dest); err != nil {
				t.Fatalf("extractZipFile: %v", err)
			}
			info, err := os.Stat(filepath.Join(dest, "entry"))
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if got := info.Mode(); got.Perm() != tc.want || got&os.ModeSetuid != 0 || got&os.ModeSetgid != 0 {
				t.Errorf("extracted %v, want %v with no setuid/setgid", got, tc.want)
			}
		})
	}
}
