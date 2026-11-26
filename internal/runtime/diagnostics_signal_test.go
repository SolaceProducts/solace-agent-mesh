// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package runtime

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestStartDiagnosticsSignalHandler_DumpsNotKills(t *testing.T) {
	pid := os.Getpid()
	pattern := fmt.Sprintf("sam-awe-memstats-%d-*.json", pid)
	for _, f := range globTemp(t, pattern) {
		_ = os.Remove(f)
	}
	before := len(globTemp(t, pattern))

	stop := StartDiagnosticsSignalHandler(discardLogger())
	defer stop()

	if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
		t.Fatalf("send SIGUSR1: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(globTemp(t, pattern)) > before {
			for _, f := range globTemp(t, pattern) {
				t.Cleanup(func() { _ = os.Remove(f) })
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no diagnostics dump appeared after SIGUSR1")
}
