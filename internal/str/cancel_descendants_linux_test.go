// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build linux

package str

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func readPIDFile(t *testing.T, path string) (shell, child int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			if f := strings.Fields(string(b)); len(f) == 2 {
				shell, _ = strconv.Atoi(f[0])
				child, _ = strconv.Atoi(f[1])
				return shell, child
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tool never wrote %s", path)
	return 0, 0
}

func waitForOwnProcessGroup(t *testing.T, childPID, toolGroup int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		group, err := syscall.Getpgid(childPID)
		if err != nil {
			t.Fatalf("getpgid(%d): %v", childPID, err)
		}
		if group != toolGroup {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("test setup: child %d still shares the tool's process group %d", childPID, toolGroup)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processStopped(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	s := string(b)
	return strings.HasPrefix(strings.TrimSpace(s[strings.LastIndexByte(s, ')')+1:]), "Z")
}

func TestDirectCancelKillsChildInOwnProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pids")
	script := writeScript(t, dir, "own_group_child.sh",
		"setsid sleep 300 &\necho \"$$ $!\" > "+pidFile+".tmp\nmv "+pidFile+".tmp "+pidFile+"\nwait")

	runner := newDirectRunner(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runner.Execute(ctx, minimalParams(), minimalEntry(script), nil, func(string) {})
	}()

	toolPID, childPID := readPIDFile(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })

	waitForOwnProcessGroup(t, childPID, toolPID)

	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Execute did not return after cancel")
	}

	deadline := time.Now().Add(3 * time.Second)
	for !processStopped(childPID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !processStopped(childPID) {
		t.Errorf("child %d in its own process group survived cancel of the tool", childPID)
	}
}
