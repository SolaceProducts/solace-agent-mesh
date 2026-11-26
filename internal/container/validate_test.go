// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeMinimalELF(t *testing.T, dir, name string, machine elf.Machine) string {
	t.Helper()

	path := filepath.Join(dir, name)

	var hdr [64]byte

	hdr[0] = 0x7f
	hdr[1] = 'E'
	hdr[2] = 'L'
	hdr[3] = 'F'

	hdr[4] = 2

	hdr[5] = 1

	hdr[6] = 1

	hdr[7] = 0

	binary.LittleEndian.PutUint16(hdr[16:18], 2)

	binary.LittleEndian.PutUint16(hdr[18:20], uint16(machine))

	binary.LittleEndian.PutUint32(hdr[20:24], 1)

	binary.LittleEndian.PutUint16(hdr[52:54], 64)

	if err := os.WriteFile(path, hdr[:], 0o755); err != nil {
		t.Fatalf("write test ELF: %v", err)
	}
	return path
}

func TestValidateBinary_LinuxAMD64(t *testing.T) {
	dir := t.TempDir()
	path := writeMinimalELF(t, dir, "test-amd64", elf.EM_X86_64)

	if err := ValidateBinary(path, "amd64"); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateBinary_LinuxARM64(t *testing.T) {
	dir := t.TempDir()
	path := writeMinimalELF(t, dir, "test-arm64", elf.EM_AARCH64)

	if err := ValidateBinary(path, "arm64"); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateBinary_ArchMismatch(t *testing.T) {
	dir := t.TempDir()
	path := writeMinimalELF(t, dir, "test-amd64", elf.EM_X86_64)

	err := ValidateBinary(path, "arm64")
	if err == nil {
		t.Fatal("expected error for arch mismatch")
	}
	if got := err.Error(); !contains(got, "architecture mismatch") {
		t.Errorf("error = %q, want to contain 'architecture mismatch'", got)
	}
}

func TestValidateBinary_MachO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-macho")

	data := []byte{0xcf, 0xfa, 0xed, 0xfe, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}

	err := ValidateBinary(path, "arm64")
	if err == nil {
		t.Fatal("expected error for Mach-O binary")
	}
	if got := err.Error(); !contains(got, "Mach-O") {
		t.Errorf("error = %q, want to contain 'Mach-O'", got)
	}
}

func TestValidateBinary_WindowsPE(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.exe")

	data := []byte{'M', 'Z', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}

	err := ValidateBinary(path, "amd64")
	if err == nil {
		t.Fatal("expected error for Windows PE binary")
	}
	if got := err.Error(); !contains(got, "Windows PE") {
		t.Errorf("error = %q, want to contain 'Windows PE'", got)
	}
}

func TestValidateBinary_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty")
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	err := ValidateBinary(path, "amd64")
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestValidateBinary_Directory(t *testing.T) {
	dir := t.TempDir()
	err := ValidateBinary(dir, "amd64")
	if err == nil {
		t.Fatal("expected error for directory")
	}
	if got := err.Error(); !contains(got, "directory") {
		t.Errorf("error = %q, want to contain 'directory'", got)
	}
}

func TestValidateBinary_NotFound(t *testing.T) {
	err := ValidateBinary("/nonexistent/binary", "amd64")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidateBinary_ScriptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\necho hello\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := ValidateBinary(path, "amd64")
	if err == nil {
		t.Fatal("expected error for script file")
	}
	if got := err.Error(); !contains(got, "script") {
		t.Errorf("error = %q, want to contain 'script'", got)
	}
}

func TestHostContainerArch(t *testing.T) {
	arch := HostContainerArch()
	if arch != "amd64" && arch != "arm64" {
		t.Logf("HostContainerArch = %q (unexpected but not necessarily wrong)", arch)
	}
}

func TestElfMachineToArch(t *testing.T) {
	tests := []struct {
		machine elf.Machine
		want    string
	}{
		{elf.EM_X86_64, "amd64"},
		{elf.EM_AARCH64, "arm64"},
		{elf.EM_386, "386"},
		{elf.EM_MIPS, ""},
	}
	for _, tt := range tests {
		got := elfMachineToArch(tt.machine)
		if got != tt.want {
			t.Errorf("elfMachineToArch(%v) = %q, want %q", tt.machine, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsStr(s, substr)))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
