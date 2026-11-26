// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package build

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func verifyBinaryTarget(path string, target Target) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("verify binary %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var hdr [4096]byte
	n, err := io.ReadFull(f, hdr[:])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return fmt.Errorf("verify binary %s: %w", path, err)
	}
	if n < 4 {
		return nil
	}
	buf := hdr[:n]

	gotOS := detectBinaryOS(buf)
	if gotOS == "" {
		return nil
	}
	if gotOS != target.OS {
		return fmt.Errorf("binary %s was built for %s but target is %s — refusing to cache poisoned output; check your build.sh honours SAM_TOOL_TARGET_OS/ARCH", path, gotOS, target.OS)
	}

	switch target.OS {
	case "linux":
		return verifyELFArch(path, buf, target.Arch)
	case "darwin":
		return verifyMachOArch(path, buf, target.Arch)
	case "windows":
		return verifyPEArch(path, buf, target.Arch)
	}
	return nil
}

func verifyBinariesInDir(distDir string, target Target) error {
	return filepath.Walk(distDir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		switch ext {
		case ".yaml", ".yml", ".json", ".md", ".txt", ".py", ".sh", ".bat", ".ps1", ".toml":
			return nil
		}
		return verifyBinaryTarget(p, target)
	})
}

func detectBinaryOS(magic []byte) string {
	if len(magic) >= 4 && magic[0] == 0x7F && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F' {
		return "linux"
	}
	if len(magic) >= 4 {
		m := binary.LittleEndian.Uint32(magic[:4])
		switch m {
		case 0xFEEDFACE, 0xFEEDFACF, 0xCEFAEDFE, 0xCFFAEDFE, 0xCAFEBABE, 0xBEBAFECA:
			return "darwin"
		}
	}
	if len(magic) >= 2 && magic[0] == 'M' && magic[1] == 'Z' {
		return "windows"
	}
	return ""
}

func verifyELFArch(path string, buf []byte, arch string) error {
	if len(buf) < 20 {
		return nil
	}
	want := elfMachineFor(arch)
	if want == 0 {
		return nil
	}
	var got uint16
	if buf[5] == 2 {
		got = binary.BigEndian.Uint16(buf[18:20])
	} else {
		got = binary.LittleEndian.Uint16(buf[18:20])
	}
	if got != want {
		return fmt.Errorf("binary %s ELF e_machine=0x%X does not match target arch %s — refusing to cache", path, got, arch)
	}
	return nil
}

func verifyMachOArch(path string, buf []byte, arch string) error {
	if len(buf) < 8 {
		return nil
	}
	first := binary.LittleEndian.Uint32(buf[:4])
	if first == 0xCAFEBABE || first == 0xBEBAFECA {
		return nil
	}
	want := machoCPUTypeFor(arch)
	if want == 0 {
		return nil
	}
	var cpu uint32
	switch first {
	case 0xFEEDFACE, 0xFEEDFACF:
		cpu = binary.LittleEndian.Uint32(buf[4:8])
	case 0xCEFAEDFE, 0xCFFAEDFE:
		cpu = binary.BigEndian.Uint32(buf[4:8])
	default:
		return nil
	}
	const abi64 = 0x01000000
	if cpu == want || cpu == (want&^abi64) || cpu == (want|abi64) {
		return nil
	}
	return fmt.Errorf("binary %s Mach-O cputype=0x%X does not match target arch %s — refusing to cache", path, cpu, arch)
}

func verifyPEArch(path string, buf []byte, arch string) error {
	if len(buf) < 0x40 {
		return nil
	}
	off := int(binary.LittleEndian.Uint32(buf[0x3C:0x40]))
	if off < 0 || off+8 > len(buf) {
		return nil
	}
	if string(buf[off:off+4]) != "PE\x00\x00" {
		return nil
	}
	want := peMachineFor(arch)
	if want == 0 {
		return nil
	}
	got := binary.LittleEndian.Uint16(buf[off+4 : off+6])
	if got != want {
		return fmt.Errorf("binary %s PE Machine=0x%X does not match target arch %s — refusing to cache", path, got, arch)
	}
	return nil
}

func elfMachineFor(arch string) uint16 {
	switch arch {
	case "amd64":
		return 0x3E
	case "arm64":
		return 0xB7
	case "386":
		return 0x03
	case "arm":
		return 0x28
	case "ppc64le":
		return 0x15
	case "s390x":
		return 0x16
	case "riscv64":
		return 0xF3
	}
	return 0
}

func machoCPUTypeFor(arch string) uint32 {
	switch arch {
	case "amd64":
		return 0x01000007
	case "arm64":
		return 0x0100000C
	case "386":
		return 0x00000007
	case "arm":
		return 0x0000000C
	}
	return 0
}

func peMachineFor(arch string) uint16 {
	switch arch {
	case "amd64":
		return 0x8664
	case "arm64":
		return 0xAA64
	case "386":
		return 0x014C
	case "arm":
		return 0x01C4
	}
	return 0
}
