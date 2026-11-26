// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package seccomp

import (
	"encoding/binary"
	"testing"

	"golang.org/x/net/bpf"
)

func seccompData(nr, arch uint32) []byte {
	d := make([]byte, 64)
	binary.BigEndian.PutUint32(d[offNr:], nr)
	binary.BigEndian.PutUint32(d[offArch:], arch)
	return d
}

func runFilter(t *testing.T, a Arch, nr, arch uint32) int {
	t.Helper()
	vm, err := bpf.NewVM(instructions(a))
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	out, err := vm.Run(seccompData(nr, arch))
	if err != nil {
		t.Fatalf("vm.Run: %v", err)
	}
	return out
}

func TestFilterDeniesBindAndIoUring(t *testing.T) {
	wantErrno := int(retErrno | (eperm & retDataMask))
	for _, a := range []Arch{archAMD64, archARM64} {
		denied := map[string]uint32{
			"bind":              a.SysBind,
			"io_uring_setup":    a.SysIoUringSetup,
			"io_uring_enter":    a.SysIoUringEnter,
			"io_uring_register": a.SysIoUringReg,
		}
		for name, nr := range denied {
			if got := runFilter(t, a, nr, a.AuditArch); got != wantErrno {
				t.Errorf("%s/%s: action = %#x, want ERRNO(EPERM) %#x", a.Name, name, got, wantErrno)
			}
		}
	}
}

func TestFilterAllowsOtherSyscalls(t *testing.T) {
	for _, a := range []Arch{archAMD64, archARM64} {
		for _, nr := range []uint32{0, 1, 2} {
			if got := runFilter(t, a, nr, a.AuditArch); got != int(uint32(retAllow)) {
				t.Errorf("%s: nr=%d action = %#x, want ALLOW", a.Name, nr, got)
			}
		}
	}
}

func TestFilterKillsForeignArch(t *testing.T) {
	if got := runFilter(t, archAMD64, archAMD64.SysBind, archARM64.AuditArch); got != int(uint32(retKillProcess)) {
		t.Errorf("foreign arch: action = %#x, want KILL_PROCESS", got)
	}
}

func TestHostProgramSerialized(t *testing.T) {
	prog, err := HostProgram()
	if err != nil {
		t.Fatalf("HostProgram: %v", err)
	}
	if len(prog) == 0 || len(prog)%8 != 0 {
		t.Errorf("program length = %d, want non-zero multiple of 8 (struct sock_filter)", len(prog))
	}
}

func TestArchForRejectsUnsupported(t *testing.T) {
	if _, err := archFor("riscv64"); err == nil {
		t.Fatal("archFor(riscv64): expected error for unsupported architecture, got nil")
	}
}

func TestProgramSerializationRoundTrip(t *testing.T) {
	for _, a := range []Arch{archAMD64, archARM64} {
		prog, err := Program(a)
		if err != nil {
			t.Fatalf("%s: Program: %v", a.Name, err)
		}
		want, err := bpf.Assemble(instructions(a))
		if err != nil {
			t.Fatalf("%s: Assemble: %v", a.Name, err)
		}
		if len(prog) != len(want)*8 {
			t.Fatalf("%s: program = %d bytes, want %d (%d instructions)", a.Name, len(prog), len(want)*8, len(want))
		}
		for i, w := range want {
			off := i * 8
			gotOp := binary.LittleEndian.Uint16(prog[off : off+2])
			gotK := binary.LittleEndian.Uint32(prog[off+4 : off+8])
			if gotOp != w.Op || prog[off+2] != w.Jt || prog[off+3] != w.Jf || gotK != w.K {
				t.Errorf("%s insn[%d] = {op:%#x jt:%d jf:%d k:%#x}, want {op:%#x jt:%d jf:%d k:%#x}",
					a.Name, i, gotOp, prog[off+2], prog[off+3], gotK, w.Op, w.Jt, w.Jf, w.K)
			}
		}
	}
}
