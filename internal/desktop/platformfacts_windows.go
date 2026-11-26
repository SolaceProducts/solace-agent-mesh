// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"debug/pe"
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

func collectPlatformFacts() platformFacts {
	info := windows.RtlGetVersion()
	return platformFacts{
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		OSVersion:  fmt.Sprintf("%d.%d (build %d)", info.MajorVersion, info.MinorVersion, info.BuildNumber),
		Translated: runningUnderArmEmulation(),
	}
}

func runningUnderArmEmulation() bool {
	if runtime.GOARCH == "arm64" {
		return false
	}
	var processMachine, nativeMachine uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &processMachine, &nativeMachine); err != nil {
		return false
	}
	return nativeMachine == pe.IMAGE_FILE_MACHINE_ARM64
}
