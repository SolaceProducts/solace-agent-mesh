// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type blockingBroker struct {
	broker.Broker
	entered chan struct{}
	release chan error
	once    sync.Once
}

func (b *blockingBroker) PublishGuaranteed(_ context.Context, _ string, _ *broker.Message) error {
	b.once.Do(func() { close(b.entered) })
	return <-b.release
}

func TestPausedStore_ConcurrentReleaseIsRaceFree(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance

	var readers sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if w.workflowHILEnabled() {
					_ = w.pausedStore()
				}
			}
		}()
	}

	w.closePausedStore()
	close(stop)
	readers.Wait()

	if w.pausedStore() != nil {
		t.Error("the store was not released")
	}
}

func TestResumePausedNode_AfterReleaseWarnsInsteadOfCrashing(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})

	var logged strings.Builder
	w.logger = slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	w.closePausedStore()

	w.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})

	if got := logged.String(); !strings.Contains(got, "released its store") {
		t.Errorf("a resume arriving after the release said nothing; logs were:\n%s", got)
	}
}

func TestPauseWorkflowNode_CleanupUsesTheSnapshotItStoredWith(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	w := e.instance

	blocking := &blockingBroker{
		Broker:  w.svc.Broker(),
		entered: make(chan struct{}),
		release: make(chan error, 1),
	}
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyWorkflowToolApprovalEnforcement, Default: true})
	w.svc = runtime.NewRuntime(blocking, nil,
		runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services()

	var logged strings.Builder
	var logMu sync.Mutex
	e.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &logMu}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	w.logger = e.logger

	go e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "call_gated", SubTaskID: "sub-1"})

	select {
	case <-blocking.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the pause never reached its notify publish")
	}

	w.closePausedStore()
	blocking.release <- context.DeadlineExceeded
	w.wg.Wait()
	e.instance.wg.Wait()

	logMu.Lock()
	got := logged.String()
	logMu.Unlock()
	if strings.Contains(got, "panicked") {
		t.Errorf("the cleanup dereferenced the released store instead of using its snapshot; logs were:\n%s", got)
	}
	if !strings.Contains(got, "failed to clean up paused row") {
		t.Errorf("the publish-failure cleanup never ran, so this proves nothing; logs were:\n%s", got)
	}
}

type lockedWriter struct {
	w  *strings.Builder
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
