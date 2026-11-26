// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package runtime

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyDiagSignal(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGUSR1)
}
