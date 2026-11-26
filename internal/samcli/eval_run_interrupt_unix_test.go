// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package samcli

import (
	"os"
	"syscall"
)

const canInterruptSelf = true

func interruptSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGINT) }
