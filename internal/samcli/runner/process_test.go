// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func shPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("sh not available: %v", err)
	}
	return path
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s not created within %v", path, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSpawnProcess_ChildOutputVisibleWithInfoLevelParent(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var parentLogBuf, childOut bytes.Buffer
	parentLogger := slog.New(slog.NewTextHandler(&parentLogBuf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	proc, err := SpawnProcess(
		"child",
		[]string{sh, "-c", `printf 'time=X level=ERROR msg="config load failed"\n' 1>&2`},
		nil,
		parentLogger,
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	select {
	case <-proc.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("process did not exit within timeout")
	}

	got := childOut.String()
	if !strings.Contains(got, `level=ERROR`) {
		t.Errorf("child ERROR line missing from forwarded output:\n  parent log: %q\n  output:     %q", parentLogBuf.String(), got)
	}
	if !strings.Contains(got, "config load failed") {
		t.Errorf("child message missing from forwarded output:\n  output: %q", got)
	}

	if !strings.Contains(parentLogBuf.String(), "started subprocess") {
		t.Errorf("parent Info log missing: %s", parentLogBuf.String())
	}
}

func TestSpawnProcess_EmptyArgsReturnsError(t *testing.T) {
	t.Parallel()
	var logBuf bytes.Buffer
	proc, err := SpawnProcess("noop", nil, nil, testLogger(&logBuf))
	if err == nil {
		t.Fatalf("expected error for empty args, got proc=%v", proc)
	}
	if !strings.Contains(err.Error(), "no command specified") {
		t.Errorf("expected 'no command specified' in error, got: %v", err)
	}
}

func TestSpawnProcess_MissingBinaryReturnsError(t *testing.T) {
	t.Parallel()
	var logBuf bytes.Buffer
	proc, err := SpawnProcess(
		"missing",
		[]string{"/this/path/does/not/exist/sam-267-fake-binary"},
		nil,
		testLogger(&logBuf),
	)
	if err == nil {
		t.Fatalf("expected error for missing binary, got proc=%v", proc)
	}
	if !strings.Contains(err.Error(), "spawn missing") {
		t.Errorf("expected 'spawn missing' in error, got: %v", err)
	}
}

func TestManagedProcess_RunningReflectsProcessState(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	proc, err := SpawnProcess(
		"sleeper",
		[]string{sh, "-c", "trap 'exit 0' TERM; sleep 30"},
		nil,
		testLogger(&logBuf),
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	if !proc.Running() {
		t.Error("Running() returned false while subprocess is live")
	}

	proc.Stop(2 * time.Second)

	if proc.Running() {
		t.Error("Running() returned true after Stop()")
	}
}

func TestManagedProcess_StopForceKillsWhenSIGTERMIgnored(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	trapped := filepath.Join(t.TempDir(), "trapped")
	proc, err := SpawnProcess(
		"stubborn",
		[]string{sh, "-c", `trap '' TERM; : > "$1"; sleep 30`, "sh", trapped},
		nil,
		testLogger(&logBuf),
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	waitForFile(t, trapped, 5*time.Second)
	start := time.Now()
	proc.Stop(250 * time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("Stop() took %v — force-kill path did not fire", elapsed)
	}

	select {
	case <-proc.Done():
	default:
		t.Fatal("Done was not closed after force-kill Stop()")
	}
	if !strings.Contains(logBuf.String(), "force-killing process") {
		t.Errorf("expected 'force-killing process' log, got: %s", logBuf.String())
	}
}

func TestSAMRunner_WatchProcessReportsCleanExitAsUnexpected(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	logger := testLogger(&logBuf)
	r := &SAMRunner{
		logger:           logger,
		unexpectedExitCh: make(chan struct{}),
	}

	proc, err := SpawnProcess(
		"clean-exit",
		[]string{sh, "-c", "exit 0"},
		nil,
		logger,
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	var wg sync.WaitGroup
	wg.Go(func() { r.watchProcess(proc) })
	wg.Wait()

	select {
	case <-r.UnexpectedExit():
	case <-time.After(2 * time.Second):
		t.Fatal("unexpectedExitCh not closed for clean-but-unstopped exit")
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "exitCode=0") {
		t.Errorf("expected 'exitCode=0' in log, got: %s", logs)
	}
}

func TestSpawnProcess_ForwardsChildOutputToWriter(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	proc, err := SpawnProcess(
		"echo-child",
		[]string{sh, "-c", "echo hello-from-child >&2; echo on-stdout"},
		nil,
		testLogger(&logBuf),
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	select {
	case <-proc.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("process did not exit within timeout")
	}

	got := childOut.String()
	if !strings.Contains(got, "hello-from-child") {
		t.Errorf("missing child stderr in forwarded output: %q", got)
	}
	if !strings.Contains(got, "on-stdout") {
		t.Errorf("missing child stdout in forwarded output: %q", got)
	}
}

func TestSpawnProcess_UnexpectedExitCapturesExitCode(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	proc, err := SpawnProcess(
		"quick-exit",
		[]string{sh, "-c", "exit 2"},
		nil,
		testLogger(&logBuf),
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	select {
	case <-proc.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("process did not exit within timeout")
	}

	if proc.WasStopped() {
		t.Error("WasStopped=true; expected false because Stop() was not called")
	}

	exitErr := proc.ExitError()
	if exitErr == nil {
		t.Fatalf("expected non-nil exit error")
	}
	ee, ok := errors.AsType[*exec.ExitError](exitErr)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", exitErr, exitErr)
	}
	if code := ee.ExitCode(); code != 2 {
		t.Errorf("expected exit code 2, got %d", code)
	}
}

func TestSpawnProcess_StopSetsWasStopped(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	proc, err := SpawnProcess(
		"sleeper",
		[]string{sh, "-c", "trap 'exit 0' TERM; sleep 30"},
		nil,
		testLogger(&logBuf),
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	proc.Stop(2 * time.Second)

	select {
	case <-proc.Done():
	default:
		t.Fatal("Stop returned but Done channel not closed")
	}

	if !proc.WasStopped() {
		t.Error("WasStopped=false after Stop(); expected true")
	}
}

func TestSpawnProcess_StopAfterNaturalExitPreservesWasStopped(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	proc, err := SpawnProcess(
		"quick-exit",
		[]string{sh, "-c", "exit 4"},
		nil,
		testLogger(&logBuf),
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	select {
	case <-proc.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("process did not exit within timeout")
	}

	proc.Stop(2 * time.Second)

	if proc.WasStopped() {
		t.Error("WasStopped=true after natural exit; Stop() must not mask an unexpected exit")
	}
}

func TestManagedProcess_StopAfterDoneNeverFlipsAttribution(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	for i := range 50 {
		var logBuf, childOut bytes.Buffer
		proc, err := SpawnProcess(
			"race-child",
			[]string{sh, "-c", "exit 7"},
			nil,
			testLogger(&logBuf),
			WithOutputWriter(&childOut),
		)
		if err != nil {
			t.Fatalf("iter %d: spawn: %v", i, err)
		}

		select {
		case <-proc.Done():
		case <-time.After(3 * time.Second):
			t.Fatalf("iter %d: timed out waiting for Done", i)
		}

		proc.Stop(500 * time.Millisecond)

		if proc.WasStopped() {
			t.Errorf("iter %d: WasStopped=true after observing natural exit first", i)
			break
		}
	}
}

func TestSAMRunner_WatchProcessReportsUnexpectedExit(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	logger := testLogger(&logBuf)

	r := &SAMRunner{
		logger:           logger,
		unexpectedExitCh: make(chan struct{}),
	}

	proc, err := SpawnProcess(
		"quick-exit",
		[]string{sh, "-c", "exit 3"},
		nil,
		logger,
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	var wg sync.WaitGroup
	wg.Go(func() { r.watchProcess(proc) })
	wg.Wait()

	select {
	case <-r.UnexpectedExit():
	case <-time.After(2 * time.Second):
		t.Fatal("unexpectedExitCh was not closed")
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "subprocess exited unexpectedly") {
		t.Errorf("expected log 'subprocess exited unexpectedly', got: %s", logs)
	}
	if !strings.Contains(logs, "quick-exit") {
		t.Errorf("expected label 'quick-exit' in log, got: %s", logs)
	}
	if !strings.Contains(logs, "exitCode=3") {
		t.Errorf("expected 'exitCode=3' in log, got: %s", logs)
	}
}

func TestSAMRunner_WatchProcessSilentOnStop(t *testing.T) {
	t.Parallel()
	sh := shPath(t)

	var logBuf, childOut bytes.Buffer
	logger := testLogger(&logBuf)

	r := &SAMRunner{
		logger:           logger,
		unexpectedExitCh: make(chan struct{}),
	}

	proc, err := SpawnProcess(
		"sleeper",
		[]string{sh, "-c", "trap 'exit 0' TERM; sleep 30"},
		nil,
		logger,
		WithOutputWriter(&childOut),
	)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	var wg sync.WaitGroup
	wg.Go(func() { r.watchProcess(proc) })

	time.Sleep(100 * time.Millisecond)
	proc.Stop(2 * time.Second)
	wg.Wait()

	select {
	case <-r.UnexpectedExit():
		t.Fatal("unexpectedExitCh was closed after a clean Stop()")
	default:
	}
	if strings.Contains(logBuf.String(), "subprocess exited unexpectedly") {
		t.Errorf("unexpected-exit log fired for an intentional Stop(): %s", logBuf.String())
	}
}
