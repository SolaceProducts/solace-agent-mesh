// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWaitForShutdownSignal_SignalReturnsFalse(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	sigCh := make(chan os.Signal, 1)
	sigCh <- syscall.SIGTERM
	unexpectedCh := make(chan struct{})

	if got := waitForShutdownSignal(sigCh, unexpectedCh, logger); got {
		t.Errorf("waitForShutdownSignal returned true on signal; want false")
	}
	if !strings.Contains(buf.String(), "stopping SAM") {
		t.Errorf("missing 'stopping SAM' log: %s", buf.String())
	}
}

func TestWaitForShutdownSignal_UnexpectedExitReturnsTrue(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	sigCh := make(chan os.Signal, 1)
	unexpectedCh := make(chan struct{})
	close(unexpectedCh)

	done := make(chan bool, 1)
	go func() { done <- waitForShutdownSignal(sigCh, unexpectedCh, logger) }()

	select {
	case got := <-done:
		if !got {
			t.Errorf("waitForShutdownSignal returned false on unexpected exit; want true")
		}
	case <-time.After(time.Second):
		t.Fatal("waitForShutdownSignal did not return within 1s")
	}

	if !strings.Contains(buf.String(), "subprocess exited unexpectedly") {
		t.Errorf("missing 'subprocess exited unexpectedly' log: %s", buf.String())
	}
}

func TestRunSAM_ReturnsErrorWhenSubprocessCrashes(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not available: %v", err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	t.Setenv("DEV_BROKER_PORT", strconv.Itoa(port))

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(home)

	dir := t.TempDir()
	script := filepath.Join(dir, "crashing-stub.sh")
	body := "#!/bin/sh\nprintf 'crashing stub\\n' 1>&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cfgPath := filepath.Join(dir, "agent.yaml")
	cfg := "apps:\n  - name: crash-app\n    app_exec: " + script + "\n    app_config:\n      noop: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- runSAM([]string{cfgPath}, nil, true, "", nil, false, false)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("runSAM returned nil; want non-nil error for crashing subprocess")
		}
		if !strings.Contains(err.Error(), "subprocess exited unexpectedly") {
			t.Errorf("runSAM error %q does not mention 'subprocess exited unexpectedly'", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runSAM did not return within 15s")
	}
}
