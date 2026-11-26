// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

const bootFetchHold = 300 * time.Millisecond

type heldBootMirror struct {
	packageName string
	entered     chan struct{}
	release     chan struct{}
	land        func() error
	once        sync.Once
}

func (m *heldBootMirror) Fetch(context.Context, string) error  { return nil }
func (m *heldBootMirror) Remove(context.Context, string) error { return nil }

func (m *heldBootMirror) RemoteNames(context.Context) ([]string, error) {
	return []string{m.packageName}, nil
}

func (m *heldBootMirror) FetchAll(ctx context.Context) error {
	var err error
	m.once.Do(func() {
		close(m.entered)
		select {
		case <-m.release:
			err = m.land()
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

func writeEchoPackage(toolsetsDir, packageName string) error {
	pkgDir := filepath.Join(toolsetsDir, packageName)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	stub := `#!/bin/sh
case "$1" in
  --schema) printf '%s' '{"tools":{"echo_tool":{"description":"test","parameters":{"type":"object","properties":{"text":{"type":"string"}}}}}}' ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(pkgDir, "echo_tool"), []byte(stub), 0o755); err != nil {
		return err
	}
	manifest := `version: 1
tools:
  echo_tool:
    runtime: native
    executable: ./echo_tool
    parameters:
      - name: text
        type: string
        required: true
`
	return os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte(manifest), 0o644)
}

func TestStart_ReadsQueueOnlyAfterBootLoad(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on Unix executable bit + shell stub")
	}

	const packageName = "echopkg"
	toolName := packageName + PackageToolSep + "echo_tool"

	brk := newTestBroker(t)
	toolsetsDir := t.TempDir()
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:    "test-worker",
		Namespace:   "test-ns",
		ToolsetsDir: toolsetsDir,
		Sandbox:     SandboxConfig{Mode: "direct"},
	})

	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := brk.Subscribe(w.queue, remotetool.InvokeTopic("test-ns", toolName)); err != nil {
		t.Fatalf("subscribe predecessor topic: %v", err)
	}

	mirror := &heldBootMirror{
		packageName: packageName,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
		land:        func() error { return writeEchoPackage(toolsetsDir, packageName) },
	}
	w.packageStore = mirror

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(mirror.release) }) }

	var startErr error
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		startErr = w.Start(ctx)
	}()
	t.Cleanup(func() {
		release()
		<-startDone
		_ = w.Stop(context.Background())
	})

	select {
	case <-mirror.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the boot reconcile never reached the package mirror, so this test cannot hold the " +
			"worker between starting and loading its uploaded tools")
	}

	time.AfterFunc(bootFetchHold, release)
	payload, err := invokeToolOverBroker(t, ctx, brk, toolName, nil)
	if err != nil {
		t.Fatalf("no reply to an invocation that was waiting on the queue when the worker started: %v", err)
	}
	if isErrorResponse(payload) {
		if code, message := parseErrorResponse(t, payload); code == remotetool.ErrToolNotFound {
			t.Fatalf("the worker read its queue before the boot load finished and answered a waiting "+
				"invocation as not available: %s", message)
		}
	}

	<-startDone
	if startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	if w.manifest.GetTool(toolName) == nil {
		t.Errorf("%q is not in the manifest after Start, so the boot reconcile did not load the "+
			"package the mirror landed", toolName)
	}
}
