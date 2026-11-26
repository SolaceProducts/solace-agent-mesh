// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package seccomp

import (
	"encoding/binary"
	"fmt"
	"runtime"

	"golang.org/x/net/bpf"
)

const (
	retKillProcess = 0x80000000
	retErrno       = 0x00050000
	retAllow       = 0x7fff0000
	retDataMask    = 0x0000ffff
	eperm          = 0x1
)

const (
	offNr   = 0
	offArch = 4
)

type Arch struct {
	Name            string
	AuditArch       uint32
	SysBind         uint32
	SysIoUringSetup uint32
	SysIoUringEnter uint32
	SysIoUringReg   uint32
}

var (
	archAMD64 = Arch{
		Name: "amd64", AuditArch: 0xC000003E,
		SysBind: 49, SysIoUringSetup: 425, SysIoUringEnter: 426, SysIoUringReg: 427,
	}
	archARM64 = Arch{
		Name: "arm64", AuditArch: 0xC00000B7,
		SysBind: 200, SysIoUringSetup: 425, SysIoUringEnter: 426, SysIoUringReg: 427,
	}
)

func archFor(goarch string) (Arch, error) {
	switch goarch {
	case "amd64":
		return archAMD64, nil
	case "arm64":
		return archARM64, nil
	default:
		return Arch{}, fmt.Errorf("seccomp: unsupported architecture %q", goarch)
	}
}

func instructions(a Arch) []bpf.Instruction {
	return []bpf.Instruction{
		bpf.LoadAbsolute{Off: offArch, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.AuditArch, SkipTrue: 1},
		bpf.RetConstant{Val: retKillProcess},

		bpf.LoadAbsolute{Off: offNr, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.SysBind, SkipTrue: 4},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.SysIoUringSetup, SkipTrue: 3},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.SysIoUringEnter, SkipTrue: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: a.SysIoUringReg, SkipTrue: 1},
		bpf.RetConstant{Val: retAllow},
		bpf.RetConstant{Val: retErrno | (eperm & retDataMask)},
	}
}

func Program(a Arch) ([]byte, error) {
	raw, err := bpf.Assemble(instructions(a))
	if err != nil {
		return nil, fmt.Errorf("seccomp: assemble %s: %w", a.Name, err)
	}
	buf := make([]byte, 0, len(raw)*8)
	var w [8]byte
	for _, ins := range raw {
		binary.LittleEndian.PutUint16(w[0:2], ins.Op)
		w[2] = ins.Jt
		w[3] = ins.Jf
		binary.LittleEndian.PutUint32(w[4:8], ins.K)
		buf = append(buf, w[:]...)
	}
	return buf, nil
}

func HostProgram() ([]byte, error) {
	a, err := archFor(runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	return Program(a)
}
