// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package coder

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBashLeavesNothingRunningAfterACommand(t *testing.T) {
	r := runShell(context.Background(), t.TempDir(), "sleep 300 >/dev/null 2>&1 & echo $!", 10, nil)
	pid, err := strconv.Atoi(strings.TrimSpace(r.Output))
	if err != nil {
		t.Fatalf("no pid in output %q", r.Output)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("background process outlived its bash command")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
