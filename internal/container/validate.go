// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"debug/elf"
	"fmt"
	"os"
	"runtime"
)

func ValidateBinary(path string, expectedArch string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access binary: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a binary: %s", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("binary file is empty: %s", path)
	}

	f, err := elf.Open(path)
	if err != nil {
		return describeNonELF(path, err)
	}
	defer func() { _ = f.Close() }()

	actual := elfMachineToArch(f.Machine)
	if actual == "" {
		return fmt.Errorf("unrecognized ELF machine type: %v", f.Machine)
	}
	if actual != expectedArch {
		return fmt.Errorf("architecture mismatch: binary is %s but container requires %s", actual, expectedArch)
	}

	return nil
}

func HostContainerArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func elfMachineToArch(m elf.Machine) string {
	switch m {
	case elf.EM_X86_64:
		return "amd64"
	case elf.EM_AARCH64:
		return "arm64"
	case elf.EM_386:
		return "386"
	default:
		return ""
	}
}

func describeNonELF(path string, elfErr error) error {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 4 {
		return fmt.Errorf("not a Linux ELF binary: %w", elfErr)
	}

	magic := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	switch magic {
	case 0xFEEDFACE, 0xFEEDFACF:
		return fmt.Errorf("sam-str binary is not a Linux ELF executable (found macOS Mach-O).\n\nCross-compile a Linux binary with:\n  GOOS=linux GOARCH=%s make build-str\n  # or: make build-str-linux", HostContainerArch())
	case 0xCEFAEDFE, 0xCFFAEDFE:
		return fmt.Errorf("sam-str binary is not a Linux ELF executable (found macOS Mach-O).\n\nCross-compile a Linux binary with:\n  GOOS=linux GOARCH=%s make build-str\n  # or: make build-str-linux", HostContainerArch())
	case 0xCAFEBABE:
		return fmt.Errorf("sam-str binary is not a Linux ELF executable (found macOS universal binary).\n\nCross-compile a Linux binary with:\n  GOOS=linux GOARCH=%s make build-str\n  # or: make build-str-linux", HostContainerArch())
	}

	if data[0] == 'M' && data[1] == 'Z' {
		return fmt.Errorf("sam-str binary is not a Linux ELF executable (found Windows PE executable).\n\nCross-compile a Linux binary with:\n  GOOS=linux GOARCH=%s make build-str", HostContainerArch())
	}

	if data[0] == '#' && data[1] == '!' {
		return fmt.Errorf("sam-str binary is not a Linux ELF executable (found script file)")
	}

	return fmt.Errorf("sam-str binary is not a Linux ELF executable: %w", elfErr)
}
