// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	stateRunning  int32 = 0
	stateNatural  int32 = 1
	stateStopping int32 = 2
)

type ManagedProcess struct {
	Cmd    *exec.Cmd
	Label  string
	logger *slog.Logger

	output io.Writer

	state atomic.Int32

	doneCh chan struct{}

	waitErr error

	drainWg sync.WaitGroup
}

type Option func(*ManagedProcess)

func WithOutputWriter(w io.Writer) Option {
	return func(mp *ManagedProcess) { mp.output = w }
}

func SpawnProcess(label string, args []string, env []string, logger *slog.Logger, opts ...Option) (*ManagedProcess, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("spawn %s: no command specified", label)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env

	setProcessGroup(cmd)
	hideConsoleWindow(cmd)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("spawn %s: stderr pipe: %w", label, err)
	}
	cmd.Stdout = cmd.Stderr

	mp := &ManagedProcess{
		Cmd:    cmd,
		Label:  label,
		logger: logger,
		output: os.Stderr,
		doneCh: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(mp)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", label, err)
	}

	mp.drainWg.Go(func() { mp.drainPipe(stderrPipe) })
	go mp.monitor()

	logger.Info("started subprocess", "label", label, "pid", cmd.Process.Pid)
	return mp, nil
}

func (mp *ManagedProcess) Done() <-chan struct{} {
	return mp.doneCh
}

func (mp *ManagedProcess) WasStopped() bool {
	return mp.state.Load() == stateStopping
}

func (mp *ManagedProcess) ExitError() error {
	return mp.waitErr
}

func (mp *ManagedProcess) Pid() int {
	if mp.Cmd == nil || mp.Cmd.Process == nil {
		return -1
	}
	return mp.Cmd.Process.Pid
}

func (mp *ManagedProcess) Stop(timeout time.Duration) {
	if mp.Cmd.Process == nil {
		return
	}

	if !mp.state.CompareAndSwap(stateRunning, stateStopping) {
		<-mp.doneCh
		return
	}

	mp.logger.Debug("sending SIGTERM", "label", mp.Label, "pid", mp.Cmd.Process.Pid)
	if err := signalProcessGroup(mp.Cmd.Process.Pid, syscall.SIGTERM); err != nil {
		mp.logger.Debug("SIGTERM failed", "label", mp.Label, "error", err)
		<-mp.doneCh
		return
	}

	select {
	case <-mp.doneCh:
		mp.logger.Debug("process exited", "label", mp.Label)
	case <-time.After(timeout):
		mp.logger.Warn("force-killing process", "label", mp.Label, "pid", mp.Cmd.Process.Pid)
		_ = signalProcessGroup(mp.Cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-mp.doneCh:
		case <-time.After(2 * time.Second):
			mp.logger.Warn("process did not exit after SIGKILL", "label", mp.Label)
		}
	}
}

func (mp *ManagedProcess) Running() bool {
	select {
	case <-mp.doneCh:
		return false
	default:
		return true
	}
}

func (mp *ManagedProcess) monitor() {
	mp.drainWg.Wait()
	err := mp.Cmd.Wait()
	mp.state.CompareAndSwap(stateRunning, stateNatural)
	mp.waitErr = err
	close(mp.doneCh)
}

func (mp *ManagedProcess) drainPipe(r io.ReadCloser) {
	if _, err := io.Copy(mp.output, r); err != nil {
		mp.logger.Debug("drain forwarding stopped", "label", mp.Label, "error", err)
	}
}

func BuildChildEnv(brokerHost string, brokerPort int) []string {
	strip := map[string]bool{
		"HEALTH_ADDR": true,
	}

	stripKey := func(key string) bool {
		if strip[key] {
			return true
		}
		return strings.HasPrefix(key, "SAM_") && strings.HasSuffix(key, "_HEALTH_ADDR")
	}

	var env []string
	for _, e := range os.Environ() {
		key := e
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			key = e[:idx]
		}
		if stripKey(key) {
			continue
		}
		env = append(env, e)
	}

	env = append(env,
		fmt.Sprintf("SAM_DEV_BROKER_HOST=%s", brokerHost),
		fmt.Sprintf("SAM_DEV_BROKER_PORT=%d", brokerPort),
		fmt.Sprintf("DEV_BROKER_HOST=%s", brokerHost),
		fmt.Sprintf("DEV_BROKER_PORT=%d", brokerPort),
	)
	return env
}

func WriteTempConfig(apps []map[string]any, logConfig map[string]any, extraSections ...map[string]any) (string, error) {
	payload := map[string]any{"apps": apps}
	if logConfig != nil {
		payload["log"] = logConfig
	}
	for _, extra := range extraSections {
		for k, v := range extra {
			payload[k] = v
		}
	}

	data, err := yaml.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal temp config: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "sam-exec-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create temp config: %w", err)
	}

	if _, writeErr := tmpFile.Write(data); writeErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("write temp config: %w", writeErr)
	}
	_ = tmpFile.Close()

	return tmpFile.Name(), nil
}

func TempFileBasename(path string) string {
	return filepath.Base(path)
}
