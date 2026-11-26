// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package build

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectBinaryOS(t *testing.T) {
	tests := []struct {
		name  string
		bytes []byte
		want  string
	}{
		{"ELF", []byte{0x7F, 'E', 'L', 'F', 0, 0}, "linux"},
		{"MachO-LE-64", elfNot([]byte{0xCF, 0xFA, 0xED, 0xFE}), "darwin"},
		{"MachO-LE-32", elfNot([]byte{0xCE, 0xFA, 0xED, 0xFE}), "darwin"},
		{"MachO-BE-64", elfNot([]byte{0xFE, 0xED, 0xFA, 0xCF}), "darwin"},
		{"MachO-BE-32", elfNot([]byte{0xFE, 0xED, 0xFA, 0xCE}), "darwin"},
		{"MachO-fat-BE", elfNot([]byte{0xCA, 0xFE, 0xBA, 0xBE}), "darwin"},
		{"MachO-fat-LE", elfNot([]byte{0xBE, 0xBA, 0xFE, 0xCA}), "darwin"},
		{"PE-MZ", []byte{'M', 'Z', 0, 0}, "windows"},
		{"shell-script", []byte("#!/bin/sh\n"), ""},
		{"text", []byte("hello\n"), ""},
		{"too-short", []byte{0, 1, 2}, ""},
		{"empty", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectBinaryOS(tc.bytes); got != tc.want {
				t.Errorf("detectBinaryOS(%x) = %q; want %q", tc.bytes, got, tc.want)
			}
		})
	}
}

func elfNot(b []byte) []byte { return b }

func TestVerifyBinaryTarget_MachOArch(t *testing.T) {
	tests := []struct {
		name       string
		magic      uint32
		cpu        uint32
		target     Target
		wantErrSub string
	}{
		{
			name:   "amd64 binary against amd64 target",
			magic:  0xFEEDFACF,
			cpu:    0x01000007,
			target: Target{OS: "darwin", Arch: "amd64"},
		},
		{
			name:   "arm64 binary against arm64 target",
			magic:  0xFEEDFACF,
			cpu:    0x0100000C,
			target: Target{OS: "darwin", Arch: "arm64"},
		},
		{
			name:       "amd64 binary against arm64 target — the poisoning case",
			magic:      0xFEEDFACF,
			cpu:        0x01000007,
			target:     Target{OS: "darwin", Arch: "arm64"},
			wantErrSub: "Mach-O cputype=0x1000007 does not match target arch arm64",
		},
		{
			name:       "arm64 binary against amd64 target",
			magic:      0xFEEDFACF,
			cpu:        0x0100000C,
			target:     Target{OS: "darwin", Arch: "amd64"},
			wantErrSub: "Mach-O cputype=0x100000C does not match target arch amd64",
		},
		{
			name:   "swapped-endian Mach-O still parses cputype",
			magic:  0xCFFAEDFE,
			cpu:    0x01000007,
			target: Target{OS: "darwin", Arch: "amd64"},
		},
		{
			name:   "fat binary tolerated — multi-arch by design",
			magic:  0xCAFEBABE,
			cpu:    0,
			target: Target{OS: "darwin", Arch: "arm64"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMachO(t, tc.magic, tc.cpu)
			err := verifyBinaryTarget(path, tc.target)
			checkErrSub(t, err, tc.wantErrSub)
		})
	}
}

func TestVerifyBinaryTarget_PEArch(t *testing.T) {
	tests := []struct {
		name       string
		machine    uint16
		target     Target
		wantErrSub string
	}{
		{
			name:    "amd64 PE against amd64 target",
			machine: 0x8664,
			target:  Target{OS: "windows", Arch: "amd64"},
		},
		{
			name:    "arm64 PE against arm64 target",
			machine: 0xAA64,
			target:  Target{OS: "windows", Arch: "arm64"},
		},
		{
			name:       "amd64 PE against arm64 target",
			machine:    0x8664,
			target:     Target{OS: "windows", Arch: "arm64"},
			wantErrSub: "PE Machine=0x8664 does not match target arch arm64",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writePE(t, tc.machine)
			err := verifyBinaryTarget(path, tc.target)
			checkErrSub(t, err, tc.wantErrSub)
		})
	}
}

func TestVerifyBinaryTarget_ELFArch(t *testing.T) {
	tests := []struct {
		name       string
		machine    uint16
		bigEndian  bool
		target     Target
		wantErrSub string
	}{
		{
			name:    "amd64 ELF against amd64 target",
			machine: 0x3E,
			target:  Target{OS: "linux", Arch: "amd64"},
		},
		{
			name:    "arm64 ELF against arm64 target",
			machine: 0xB7,
			target:  Target{OS: "linux", Arch: "arm64"},
		},
		{
			name:       "amd64 ELF against arm64 target",
			machine:    0x3E,
			target:     Target{OS: "linux", Arch: "arm64"},
			wantErrSub: "ELF e_machine=0x3E does not match target arch arm64",
		},
		{
			name:      "s390x BE ELF parses via e_ident[5]=2",
			machine:   0x16,
			bigEndian: true,
			target:    Target{OS: "linux", Arch: "s390x"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeELF(t, tc.machine, tc.bigEndian)
			err := verifyBinaryTarget(path, tc.target)
			checkErrSub(t, err, tc.wantErrSub)
		})
	}
}

func TestVerifyBinaryTarget_WrongOS(t *testing.T) {
	path := writeELF(t, 0x3E, false)
	err := verifyBinaryTarget(path, Target{OS: "darwin", Arch: "amd64"})
	checkErrSub(t, err, "was built for linux but target is darwin")
}

func TestVerifyBinaryTarget_Tolerance(t *testing.T) {
	dir := t.TempDir()
	target := Target{OS: "linux", Arch: "arm64"}

	if err := verifyBinaryTarget(filepath.Join(dir, "missing"), target); err != nil {
		t.Errorf("missing file should pass: %v", err)
	}

	tiny := filepath.Join(dir, "tiny")
	if err := os.WriteFile(tiny, []byte{1, 2}, 0o644); err != nil {
		t.Fatalf("write tiny: %v", err)
	}
	if err := verifyBinaryTarget(tiny, target); err != nil {
		t.Errorf("3-byte file should pass: %v", err)
	}

	script := filepath.Join(dir, "build.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := verifyBinaryTarget(script, target); err != nil {
		t.Errorf("shell script should pass: %v", err)
	}
}

func TestVerifyBinariesInDir(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatalf("write py: %v", err)
	}

	good := filepath.Join(dir, "tool")
	writeELFAt(t, good, 0xB7, false)
	target := Target{OS: "linux", Arch: "arm64"}
	if err := verifyBinariesInDir(dir, target); err != nil {
		t.Errorf("all-matching dir should pass: %v", err)
	}

	subBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(subBin, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	machoPath := filepath.Join(subBin, "run-darwin-arm64")
	writeMachOAt(t, machoPath, 0xFEEDFACF, 0x01000007)
	err := verifyBinariesInDir(dir, target)
	if err == nil {
		t.Fatal("walker should catch a Mach-O at a non-canonical name")
	}
	if !strings.Contains(err.Error(), "was built for darwin") {
		t.Errorf("error should call out the OS mismatch: %v", err)
	}
}

func writeELF(t *testing.T, machine uint16, bigEndian bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "elf")
	writeELFAt(t, path, machine, bigEndian)
	return path
}

func writeELFAt(t *testing.T, path string, machine uint16, bigEndian bool) {
	t.Helper()
	var hdr [64]byte
	hdr[0], hdr[1], hdr[2], hdr[3] = 0x7F, 'E', 'L', 'F'
	hdr[4] = 2
	if bigEndian {
		hdr[5] = 2
		binary.BigEndian.PutUint16(hdr[18:20], machine)
	} else {
		hdr[5] = 1
		binary.LittleEndian.PutUint16(hdr[18:20], machine)
	}
	if err := os.WriteFile(path, hdr[:], 0o644); err != nil {
		t.Fatalf("write ELF: %v", err)
	}
}

func writeMachO(t *testing.T, magic, cpu uint32) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "macho")
	writeMachOAt(t, path, magic, cpu)
	return path
}

func writeMachOAt(t *testing.T, path string, magic, cpu uint32) {
	t.Helper()
	var hdr [16]byte
	binary.LittleEndian.PutUint32(hdr[:4], magic)
	switch magic {
	case 0xCEFAEDFE, 0xCFFAEDFE:
		binary.BigEndian.PutUint32(hdr[4:8], cpu)
	default:
		binary.LittleEndian.PutUint32(hdr[4:8], cpu)
	}
	if err := os.WriteFile(path, hdr[:], 0o644); err != nil {
		t.Fatalf("write Mach-O: %v", err)
	}
}

func writePE(t *testing.T, machine uint16) string {
	t.Helper()
	const peOff = 0x80
	buf := make([]byte, peOff+24)
	buf[0], buf[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(buf[0x3C:0x40], peOff)
	copy(buf[peOff:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(buf[peOff+4:peOff+6], machine)
	path := filepath.Join(t.TempDir(), "pe.exe")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write PE: %v", err)
	}
	return path
}

func checkErrSub(t *testing.T, err error, wantSub string) {
	t.Helper()
	switch {
	case wantSub == "" && err != nil:
		t.Errorf("expected nil error, got: %v", err)
	case wantSub != "" && err == nil:
		t.Errorf("expected error containing %q, got nil", wantSub)
	case wantSub != "" && !strings.Contains(err.Error(), wantSub):
		t.Errorf("error %q does not contain %q", err.Error(), wantSub)
	}
}
