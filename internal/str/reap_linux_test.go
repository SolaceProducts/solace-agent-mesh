// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build linux

package str

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func startTracked(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	beginChildStart()
	if err := cmd.Start(); err != nil {
		abortChildStart()
		t.Fatalf("start: %v", err)
	}
	trackChild(cmd.Process.Pid)
}

func waitTracked(cmd *exec.Cmd) error {
	err := cmd.Wait()
	untrackChild(cmd.Process.Pid)
	return err
}

func isZombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	_, ppid, _ := strings.Cut(string(b), ") ")
	return strings.HasPrefix(ppid, "Z")
}

func TestGlobalReaper_CollectsOrphanWhileDirectChildRuns(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Skipf("PR_SET_CHILD_SUBREAPER unavailable: %v", err)
	}
	t.Cleanup(func() { _ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0) })
	StartGlobalReaper(t.Context(), nil)

	long := exec.Command("sleep", "30")
	startTracked(t, long)
	t.Cleanup(func() { _ = long.Process.Kill(); _ = waitTracked(long) })

	forker := exec.Command("sh", "-c", "(sleep 0.2; exit 3) & echo $!")
	out, err := childOutput(forker)
	if err != nil {
		t.Fatalf("forker: %v", err)
	}
	gpid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("grandchild pid: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/proc/" + strconv.Itoa(gpid)); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if isZombie(gpid) {
		t.Fatalf("grandchild %d is still a zombie: the reaper did not collect it while a direct child was running", gpid)
	}
}

func TestGlobalReaper_NeverStealsDirectChildStatus(t *testing.T) {
	StartGlobalReaper(t.Context(), nil)

	const children = 200
	var wg sync.WaitGroup
	errs := make(chan error, children)
	for i := 0; i < children; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command("sh", "-c", "exit 7")
			beginChildStart()
			if err := cmd.Start(); err != nil {
				abortChildStart()
				errs <- err
				return
			}
			trackChild(cmd.Process.Pid)
			err := waitTracked(cmd)
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("direct child status lost: %v", err)
	}
}

func TestChildOutput_SurvivesReaperDrain(t *testing.T) {
	StartGlobalReaper(t.Context(), nil)

	const children = 100
	var wg sync.WaitGroup
	errs := make(chan error, children)
	for i := 0; i < children; i++ {
		wg.Go(func() {
			out, err := childOutput(exec.Command("sh", "-c", "echo ok; exit 5"))
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 5 {
				errs <- fmt.Errorf("want exit 5, got %v", err)
				return
			}
			if strings.TrimSpace(string(out)) != "ok" {
				errs <- fmt.Errorf("stdout = %q", out)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestDrainOrphans_HoldsWhileAStartIsInFlight(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Skipf("PR_SET_CHILD_SUBREAPER unavailable: %v", err)
	}
	t.Cleanup(func() { _ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0) })

	forker := exec.Command("sh", "-c", "(exit 0) & echo $!")
	out, err := childOutput(forker)
	if err != nil {
		t.Fatalf("forker: %v", err)
	}
	gpid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("grandchild pid: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !isZombie(gpid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !isZombie(gpid) {
		t.Skip("could not produce a reparented zombie in time")
	}

	beginChildStart()
	drainOrphans(nil)
	if !isZombie(gpid) {
		abortChildStart()
		t.Fatal("drainOrphans reaped while a start was in flight")
	}
	abortChildStart()
	drainOrphans(nil)
	if isZombie(gpid) {
		t.Fatal("drainOrphans left the orphan once no start was in flight")
	}
}
