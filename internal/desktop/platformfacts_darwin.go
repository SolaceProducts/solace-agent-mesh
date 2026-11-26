// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"runtime"
	"strings"
	"syscall"
)

func collectPlatformFacts() platformFacts {
	version, _ := syscall.Sysctl("kern.osproductversion")
	translated, err := syscall.SysctlUint32("sysctl.proc_translated")
	return platformFacts{
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		OSVersion:  strings.TrimSpace(version),
		Translated: err == nil && translated == 1,
	}
}
