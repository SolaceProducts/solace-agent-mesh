// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func elfHeader(machine uint16) string {
	h := make([]byte, 20)
	copy(h, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	h[18] = byte(machine)
	h[19] = byte(machine >> 8)
	return string(h)
}

func TestHostExecCompatError(t *testing.T) {
	dir := t.TempDir()
	origOS, origArch := runtimeGOOS, runtimeGOARCH
	t.Cleanup(func() { runtimeGOOS, runtimeGOARCH = origOS, origArch })
	runtimeGOOS = func() string { return "linux" }
	runtimeGOARCH = func() string { return "arm64" }

	t.Run("arch mismatch is reported without exec", func(t *testing.T) {
		bin := writeRawFile(t, dir, "amd64-on-arm64", elfHeader(0x3e))
		err := hostExecCompatError(bin)
		if err == nil {
			t.Fatal("expected an incompatibility error for an amd64 binary on an arm64 host")
		}
		msg := err.Error()
		if !strings.Contains(msg, "cannot run on this host") || !strings.Contains(msg, "exec format error") {
			t.Errorf("message should carry both self-diagnosis phrases; got %q", msg)
		}
	})

	t.Run("matching arch passes through", func(t *testing.T) {
		bin := writeRawFile(t, dir, "arm64-on-arm64", elfHeader(0xb7))
		if err := hostExecCompatError(bin); err != nil {
			t.Errorf("a matching binary must fall through to --schema; got %q", err)
		}
	})

	t.Run("OS mismatch is reported", func(t *testing.T) {
		macho := writeRawFile(t, dir, "macho-on-linux", "\xcf\xfa\xed\xfe\x0c\x00\x00\x01")
		if err := hostExecCompatError(macho); err == nil {
			t.Error("a Mach-O binary on a linux host must be reported as incompatible")
		}
	})

	t.Run("unknown machine on matching OS passes through", func(t *testing.T) {
		bin := writeRawFile(t, dir, "elf-unknown-arch", elfHeader(0x9999))
		if err := hostExecCompatError(bin); err != nil {
			t.Errorf("an unreadable arch must not be rejected; got %q", err)
		}
	})

	t.Run("non-native file passes through", func(t *testing.T) {
		script := writeRawFile(t, dir, "script", "#!/bin/sh\necho hi\n")
		if err := hostExecCompatError(script); err != nil {
			t.Errorf("a script is not a native binary; got %q", err)
		}
	})
}

func buildProbeBinary(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	main := `package main
import ("fmt";"os")
func main(){ if len(os.Args)>1 && os.Args[1]=="--schema" { fmt.Println(` +
		"`" + `{"tools":{"probe":{"description":"real"}}}` + "`" + `) } }
`
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module probe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(src, "probe")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build probe binary: %v\n%s", err, out)
	}
	return bin
}

func rawBytes(bs ...byte) string { return string(bs) }

func peImage(machineLo, machineHi byte) string {
	h := make([]byte, 0x40+6)
	h[0], h[1] = 'M', 'Z'
	h[0x3c] = 0x40
	h[0x40], h[0x41], h[0x42], h[0x43] = 'P', 'E', 0, 0
	h[0x44], h[0x45] = machineLo, machineHi
	return string(h)
}

func TestDetectBinaryTarget(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name             string
		body             string
		wantOS, wantArch string
	}{
		{"elf amd64", elfHeader(0x3e), "linux", "amd64"},
		{"elf arm64", elfHeader(0xb7), "linux", "arm64"},
		{"elf 386", elfHeader(0x03), "linux", "386"},
		{"elf arm", elfHeader(0x28), "linux", "arm"},
		{"elf riscv64", elfHeader(0xf3), "linux", "riscv64"},
		{"elf unknown machine", elfHeader(0x9999), "linux", ""},
		{"elf too short for machine", "\x7fELF", "linux", ""},
		{"macho le amd64", rawBytes(0xcf, 0xfa, 0xed, 0xfe, 0x07, 0, 0, 0x01), "darwin", "amd64"},
		{"macho le arm64", rawBytes(0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0, 0, 0x01), "darwin", "arm64"},
		{"macho be amd64", rawBytes(0xfe, 0xed, 0xfa, 0xcf, 0x01, 0, 0, 0x07), "darwin", "amd64"},
		{"macho unknown cputype", rawBytes(0xcf, 0xfa, 0xed, 0xfe, 0x99, 0x99, 0x99, 0x99), "darwin", ""},
		{"pe amd64", peImage(0x64, 0x86), "windows", "amd64"},
		{"pe arm64", peImage(0x64, 0xaa), "windows", "arm64"},
		{"pe 386", peImage(0x4c, 0x01), "windows", "386"},
		{"pe arm", peImage(0xc4, 0x01), "windows", "arm"},
		{"pe unknown machine", peImage(0x00, 0x00), "windows", ""},
		{"pe bad signature", func() string { s := []byte(peImage(0x64, 0x86)); s[0x40] = 'X'; return string(s) }(), "windows", ""},
		{"pe too short for header", rawBytes(0x4d, 0x5a, 0, 0, 0, 0, 0, 0), "windows", ""},
		{"pe lfanew past eof", func() string {
			s := make([]byte, 0x40)
			s[0], s[1] = 'M', 'Z'
			s[0x3c], s[0x3d] = 0xff, 0xff
			return string(s)
		}(), "windows", ""},
		{"non-native content", "hello world", "", ""},
		{"too short to classify", rawBytes(0x01, 0x02), "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeRawFile(t, dir, tc.name, tc.body)
			goos, goarch := detectBinaryTarget(path)
			if goos != tc.wantOS || goarch != tc.wantArch {
				t.Errorf("detectBinaryTarget = (%q, %q), want (%q, %q)", goos, goarch, tc.wantOS, tc.wantArch)
			}
		})
	}

	t.Run("unreadable path", func(t *testing.T) {
		goos, goarch := detectBinaryTarget(filepath.Join(dir, "does-not-exist"))
		if goos != "" || goarch != "" {
			t.Errorf("a path that can't be opened yields no target; got (%q, %q)", goos, goarch)
		}
	})

	t.Run("machOGOARCH guards a short slice", func(t *testing.T) {
		if got := machOGOARCH([]byte{0xcf, 0xfa}); got != "" {
			t.Errorf("a header too short for the cputype must yield no arch; got %q", got)
		}
	})
}

func TestProcessPackage_WrongArchBinary_ReportedWithoutExec(t *testing.T) {
	probe := buildProbeBinary(t)

	pkgDir := t.TempDir()
	if err := os.Rename(probe, filepath.Join(pkgDir, "probe")); err != nil {
		t.Fatal(err)
	}
	writePackageManifestYAML(t, pkgDir, `version: 1
tools:
  probe_tool:
    runtime: go
    executable: ./probe
`)

	origArch := runtimeGOARCH
	t.Cleanup(func() { runtimeGOARCH = origArch })
	runtimeGOARCH = func() string {
		if runtime.GOARCH == "amd64" {
			return "arm64"
		}
		return "amd64"
	}

	var failures []string
	p := NewPackageProcessor(pkgDir, captureLogger(new(bytes.Buffer)))
	p.SetDiscoveryFailedHook(func(_, binaryName, errMsg string) {
		failures = append(failures, binaryName+": "+errMsg)
	})

	schemaTools, _ := p.discoverExecutables(pkgDir, "wrongarchpkg")

	if len(schemaTools) != 0 {
		t.Fatalf("the wrong-arch binary must not be exec'd for --schema; got %d discovered tools: %+v", len(schemaTools), schemaTools)
	}
	if len(failures) != 1 {
		t.Fatalf("expected exactly one discovery failure for the wrong-arch binary; got %v", failures)
	}
	if !strings.Contains(failures[0], "cannot run on this host") || !strings.Contains(failures[0], "exec format error") {
		t.Errorf("failure should carry the arch-mismatch self-diagnosis; got %q", failures[0])
	}
}

func TestDiscoverToolsInto_ExecFormatError_CarriesHint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ENOEXEC from a junk file is a Unix behaviour")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "foreign")
	if err := os.WriteFile(exe, []byte{0xde, 0xad, 0xbe, 0xef, 0, 0, 0, 0}, 0o755); err != nil {
		t.Fatal(err)
	}
	entries := map[string]*ManifestEntry{"foreign": {ToolName: "foreign", Executable: exe, ToolDir: dir}}

	discoverToolsInto(entries, nil, testLogger())

	reason := entries["foreign"].UnservableReason
	if !strings.Contains(reason, "cannot run on this host") || !strings.Contains(reason, "exec format error") {
		t.Errorf("unservable reason should carry the arch-mismatch hint; got %q", reason)
	}
}

func TestDiscoverToolsInto_ForeignArchHeader_SkipsProbe(t *testing.T) {
	exe := buildProbeBinary(t)
	origArch := runtimeGOARCH
	t.Cleanup(func() { runtimeGOARCH = origArch })
	runtimeGOARCH = func() string {
		if runtime.GOARCH == "amd64" {
			return "arm64"
		}
		return "amd64"
	}
	entry := &ManifestEntry{ToolName: "probe", Executable: exe, ToolDir: filepath.Dir(exe)}
	entries := map[string]*ManifestEntry{"probe": entry}

	discoverToolsInto(entries, nil, testLogger())

	if !entry.Unservable {
		t.Fatalf("the wrong-arch binary must not be exec'd for --schema; entry is servable: %+v", entry)
	}
	if !strings.Contains(entry.UnservableReason, "cannot run on this host") || !strings.Contains(entry.UnservableReason, "exec format error") {
		t.Errorf("unservable reason should carry the arch-mismatch hint; got %q", entry.UnservableReason)
	}
}
