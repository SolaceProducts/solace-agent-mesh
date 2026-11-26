// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package execbit

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func minimalPE() []byte {
	b := make([]byte, 0x44)
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:0x40], 0x40)
	copy(b[0x40:], []byte{'P', 'E', 0, 0})
	return b
}

func TestLooksRunnable(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		file    string
		content []byte
		want    bool
	}{
		{"linux elf", "tool", []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01}, true},
		{"windows pe", "tool.bin", minimalPE(), true},
		{"text file that happens to start MZ", "regions.csv", []byte("MZ,Country\nMozambique,MZ\n"), false},
		{"MZ with no PE signature", "notpe.bin", append([]byte("MZ"), make([]byte, 0x50)...), false},
		{"macos mach-o", "tool.macho", []byte{0xCF, 0xFA, 0xED, 0xFE}, true},
		{"macos mach-o 32-bit", "tool.macho32", []byte{0xCE, 0xFA, 0xED, 0xFE}, true},
		{"macos universal", "tool.fat", []byte{0xCA, 0xFE, 0xBA, 0xBE}, true},
		{"macos universal 64-bit", "tool.fat64", []byte{0xCA, 0xFE, 0xBA, 0xBF}, true},
		{"shebang script", "run", []byte("#!/bin/sh\necho hi\n"), true},
		{"python entry point with shebang", "main.py", []byte("#!/usr/bin/env python3\nprint('hi')\n"), true},
		{"python module without shebang", "weather.py", []byte("VERSION = 1\n"), false},
		{"shell script without shebang", "build.sh", []byte("echo hi\n"), false},
		{"plain text", "README.md", []byte("# docs\n"), false},
		{"yaml manifest", "manifest.yaml", []byte("name: tool\n"), false},
		{"empty file", "empty", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.file)
			if err := os.WriteFile(path, tc.content, 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := LooksRunnable(path); got != tc.want {
				t.Errorf("LooksRunnable(%s) = %v, want %v", tc.file, got, tc.want)
			}
		})
	}
}

func TestLooksRunnableRejectsDirectoriesAndMissingPaths(t *testing.T) {
	dir := t.TempDir()
	if LooksRunnable(dir) {
		t.Error("a directory must not be runnable")
	}
	if LooksRunnable(filepath.Join(dir, "absent")) {
		t.Error("a missing path must not be runnable")
	}
}

func TestLooksRunnableIgnoresTheExecutableBit(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "tool")
	if err := os.WriteFile(binary, []byte{0x7f, 'E', 'L', 'F', 0x02}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(binary, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if !LooksRunnable(binary) {
		t.Error("a native binary must be runnable regardless of its mode")
	}
}

func TestLooksRunnableIsNameIndependent(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name     string
		body     []byte
		runnable bool
	}{
		{"shebang", []byte("#!/usr/bin/env python3\nprint('hi')\n"), true},
		{"no shebang", []byte("print('hi')\n"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			staged := filepath.Join(dir, "."+tc.name+".py.tmp")
			final := filepath.Join(dir, tc.name+".py")
			for _, path := range []string{staged, final} {
				if err := os.WriteFile(path, tc.body, 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
			}
			if LooksRunnable(staged) != LooksRunnable(final) {
				t.Error("the staging name changed the answer")
			}
			if got := LooksRunnable(final); got != tc.runnable {
				t.Errorf("LooksRunnable = %v, want %v", got, tc.runnable)
			}
		})
	}
}

func TestLooksRunnableBytes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		content  []byte
		runnable bool
	}{
		{"elf", []byte{0x7f, 'E', 'L', 'F'}, true},
		{"macho 32-bit", []byte{0xCE, 0xFA, 0xED, 0xFE}, true},
		{"shebang", []byte("#!/bin/sh\n"), true},
		{"dos header", []byte("MZ\x90\x00"), true},
		{"yaml", []byte("name: tool\n"), false},
		{"empty", nil, false},
		{"one byte", []byte("#"), false},
	} {
		if got := LooksRunnableBytes(tc.content); got != tc.runnable {
			t.Errorf("%s: LooksRunnableBytes = %v, want %v", tc.name, got, tc.runnable)
		}
	}
}
