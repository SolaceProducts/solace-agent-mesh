// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func newComputeQueueRunner(t *testing.T) *SandboxRunner {
	t.Helper()
	return newComputeQueueRunnerWith(t, 1, 2)
}

func newComputeQueueRunnerWith(t *testing.T, compute, inFlight int) *SandboxRunner {
	t.Helper()
	return NewSandboxRunner(SandboxConfig{
		Mode:                    "direct",
		WorkBaseDir:             t.TempDir(),
		DefaultTimeoutSeconds:   60,
		MaxConcurrentExecutions: compute,
		MaxInFlight:             inFlight,
	}, slog.Default())
}

func waitForComputeHeld(t *testing.T, r *SandboxRunner, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, held, _ := r.compute.snapshot(); held >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no execution took a compute slot within the deadline")
}

func startExecute(ctx context.Context, r *SandboxRunner, params *remotetool.InvokeParams, script string) <-chan *remotetool.InvokeError {
	done := make(chan *remotetool.InvokeError, 1)
	go func() {
		_, ierr := r.Execute(ctx, params, minimalEntry(script), nil, func(string) {})
		done <- ierr
	}()
	return done
}

func TestExecute_QueueWaitNotChargedToTimeout(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	slow := writeScript(t, dir, "slow.sh", `sleep 7; echo '{"result":"slow"}' > result.json`)
	fast := writeScript(t, dir, "fast.sh", `sleep 0.1; echo '{"result":"fast"}' > result.json`)

	r := newComputeQueueRunner(t)

	aParams := minimalParams()
	aParams.TaskID = "task-a"
	aDone := startExecute(t.Context(), r, aParams, slow)
	waitForComputeHeld(t, r, 1)

	bParams := minimalParams()
	bParams.TaskID = "task-b"
	bParams.TimeoutSeconds = 1
	bStart := time.Now()
	bDone := startExecute(t.Context(), r, bParams, fast)
	waitForComputeWaiters(t, r, 1)

	bErr := <-bDone
	bWall := time.Since(bStart)
	if bErr != nil {
		t.Fatalf("queued call failed: code=%s msg=%q", bErr.Code, bErr.Message)
	}
	if bWall < 6*time.Second {
		t.Fatalf("queued call returned after %v; expected it to wait behind the slow tool", bWall)
	}

	if aErr := <-aDone; aErr != nil {
		t.Fatalf("slow call failed: code=%s msg=%q", aErr.Code, aErr.Message)
	}
}

func TestExecute_CancelWhileQueuedForCompute(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	slow := writeScript(t, dir, "slow.sh", `sleep 1; echo '{"result":"slow"}' > result.json`)
	fast := writeScript(t, dir, "fast.sh", `echo '{"result":"fast"}' > result.json`)

	r := newComputeQueueRunner(t)

	aParams := minimalParams()
	aParams.TaskID = "task-a"
	aDone := startExecute(t.Context(), r, aParams, slow)
	waitForComputeHeld(t, r, 1)

	bCtx, cancelB := context.WithCancel(t.Context())
	defer cancelB()
	bParams := minimalParams()
	bParams.TaskID = "task-b"
	bDone := startExecute(bCtx, r, bParams, fast)
	waitForComputeWaiters(t, r, 1)

	cancelB()
	var bErr *remotetool.InvokeError
	select {
	case bErr = <-bDone:
	case <-time.After(time.Second):
		t.Fatal("queued call did not return within 1s of cancellation")
	}
	if bErr == nil {
		t.Fatal("queued call succeeded; want INTERNAL_ERROR")
	}
	if bErr.Code != remotetool.ErrInternalError {
		t.Fatalf("queued call code = %s, want %s (msg=%q)", bErr.Code, remotetool.ErrInternalError, bErr.Message)
	}

	if aErr := <-aDone; aErr != nil {
		t.Fatalf("slow call failed: code=%s msg=%q", aErr.Code, aErr.Message)
	}

	if _, held, waiting := r.compute.snapshot(); held != 0 || waiting != 0 {
		t.Fatalf("compute held=%d waiting=%d after both calls returned, want 0/0", held, waiting)
	}
	if len(r.inflightSem) != 0 {
		t.Fatalf("inflight held = %d, want 0", len(r.inflightSem))
	}

	drainCtx, cancelDrain := context.WithCancel(t.Context())
	cancelDrain()
	r.runWorkDirCleanup(drainCtx)
	instanceDir, err := r.instanceWorkDir()
	if err != nil {
		t.Fatalf("instance dir: %v", err)
	}
	entries, err := os.ReadDir(instanceDir)
	if err != nil {
		t.Fatalf("read instance dir: %v", err)
	}
	var leftover []string
	for _, e := range entries {
		if e.Name() != instanceLockName {
			leftover = append(leftover, e.Name())
		}
	}
	if len(leftover) != 0 {
		t.Fatalf("work dirs left behind after cleanup drain: %v", leftover)
	}
}

func runIdleSleepers(t *testing.T, r *SandboxRunner, n int, sleepSeconds int) time.Duration {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		r.runCPUIdleWatcher(ctx)
	}()

	script := writeScript(t, t.TempDir(), "sleeper.sh",
		"sleep "+strconv.Itoa(sleepSeconds)+`; echo '{"result":"ok"}' > result.json`)
	start := time.Now()
	dones := make([]<-chan *remotetool.InvokeError, 0, n)
	for i := range n {
		params := minimalParams()
		params.TaskID = "sleeper-" + string(rune('a'+i))
		params.TimeoutSeconds = 30
		dones = append(dones, startExecute(ctx, r, params, script))
	}
	for i, done := range dones {
		if ierr := <-done; ierr != nil {
			t.Fatalf("sleeper %d failed: code=%s msg=%q", i, ierr.Code, ierr.Message)
		}
	}
	wall := time.Since(start)
	cancel()
	<-watcherDone

	if _, held, waiting := r.compute.snapshot(); held != 0 || waiting != 0 {
		t.Fatalf("compute budget not drained: held=%d waiting=%d", held, waiting)
	}
	return wall
}

func TestExecute_IdleSleepersYieldCompute_FakeSampler(t *testing.T) {
	skipOnWindows(t)
	r := newComputeQueueRunnerWith(t, 2, 8)
	var yields atomic.Int32
	r.cpuWatch = newCPUIdleWatcher(func(roots []int) (map[int]treeSample, bool) {
		out := make(map[int]treeSample, len(roots))
		for _, pid := range roots {
			out[pid] = treeSample{ticks: 1}
		}
		return out, true
	})
	r.cpuWatch.onYield = func() { yields.Add(1) }

	wall := runIdleSleepers(t, r, 8, 3)
	if wall < 3*time.Second {
		t.Fatalf("batch finished in %v, faster than one 3 s sleep", wall)
	}
	if wall >= 5*time.Second {
		t.Fatalf("batch took %v; eight sleepers on a compute budget of two must yield, not queue (serialised would be 12 s)", wall)
	}
	if got := yields.Load(); got < 8 {
		t.Fatalf("yields = %d, want at least one per sleeper", got)
	}
}

func TestExecute_IdleSleepersYieldCompute_Linux(t *testing.T) {
	skipOnWindows(t)
	if _, ok := procTreeCPU(nil); !ok {
		t.Skip("needs readable /proc process accounting")
	}
	r := newComputeQueueRunnerWith(t, 2, 8)
	wall := runIdleSleepers(t, r, 8, 5)
	if wall < 5*time.Second {
		t.Fatalf("batch finished in %v, faster than one 5 s sleep", wall)
	}
	if wall >= 10*time.Second {
		t.Fatalf("batch took %v; sleepers must yield their compute slot to the idle watcher (serialised would be 20 s)", wall)
	}
}
