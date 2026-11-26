// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

type deprovisionTrackingBroker struct {
	*broker.DevBroker
	mu            sync.Mutex
	deprovisioned []string
}

func (b *deprovisionTrackingBroker) DeprovisionQueue(name string) error {
	b.mu.Lock()
	b.deprovisioned = append(b.deprovisioned, name)
	b.mu.Unlock()
	return b.DevBroker.DeprovisionQueue(name)
}

func (b *deprovisionTrackingBroker) names() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.deprovisioned))
	copy(out, b.deprovisioned)
	return out
}

func TestEventMeshAdapter_DeprovisionAdapterQueues(t *testing.T) {
	dataBroker := &deprovisionTrackingBroker{DevBroker: broker.NewDevBroker()}
	if err := dataBroker.Connect(context.Background()); err != nil {
		t.Fatalf("connect data broker: %v", err)
	}

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "em-gw-1",
	})

	a := &EventMeshAdapter{
		core:       core,
		dataBroker: dataBroker,
		logger:     slog.Default(),
		cfg: &Config{
			EventHandlers: []*EventHandlerConfig{
				{Name: "orders"},
				{Name: "events"},
			},
		},
	}

	if err := a.DeprovisionAdapterQueues(context.Background()); err != nil {
		t.Fatalf("DeprovisionAdapterQueues: %v", err)
	}

	got := dataBroker.names()
	want := []string{
		"test-ns/q/eventmesh/em-gw-1/orders",
		"test-ns/q/eventmesh/em-gw-1/events",
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("DeprovisionQueue calls = %v, want to include %q", got, w)
		}
	}
}

func TestEventMeshAdapter_ReceiveLoop_TeardownDoesNotLogError(t *testing.T) {
	var buf safeLogBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dataBroker := broker.NewDevBroker()
	if err := dataBroker.Connect(context.Background()); err != nil {
		t.Fatalf("connect data broker: %v", err)
	}
	t.Cleanup(func() { _ = dataBroker.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "em-gw-1",
	})

	queueName := "test-ns/q/eventmesh/em-gw-1/orders"
	queue, err := dataBroker.CreateQueue(queueName, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	a := &EventMeshAdapter{
		core:       core,
		dataBroker: dataBroker,
		logger:     logger,
		cfg: &Config{
			EventHandlers: []*EventHandlerConfig{{Name: "orders"}},
		},
	}
	a.handlerQueues = append(a.handlerQueues, queue)

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.receiveLoop(context.Background(), a.cfg.EventHandlers[0], queue)
	}()

	time.Sleep(20 * time.Millisecond)

	if err := a.DeprovisionAdapterQueues(context.Background()); err != nil {
		t.Fatalf("DeprovisionAdapterQueues: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("receive loop did not exit after its queue was deprovisioned")
	}

	out := buf.String()
	if strings.Contains(out, "level=ERROR") {
		t.Errorf("expected no ERROR log for a planned teardown, got: %s", out)
	}
}

func TestEventMeshAdapter_ReceiveLoop_HealthProblems(t *testing.T) {
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "em-gw-1",
	})

	var callCount int
	queue := &broker.SimpleQueue{QName: "test-ns/q/eventmesh/em-gw-1/orders"}
	dataBroker := &receiveFuncBroker{
		receiveFn: func(ctx context.Context, _ broker.Queue) (*broker.Message, error) {
			callCount++
			if callCount == 1 {
				return nil, broker.ErrProvisionCooldown
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	a := &EventMeshAdapter{
		core:       core,
		dataBroker: dataBroker,
		logger:     slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelError + 1})),
		cfg: &Config{
			EventHandlers: []*EventHandlerConfig{{Name: "orders"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.receiveLoop(ctx, a.cfg.EventHandlers[0], queue)
	}()

	waitForCondition(t, func() bool { return len(a.HealthProblems()) == 1 })
	if got := a.HealthProblems(); got[0].Severity != health.Degraded {
		t.Errorf("HealthProblems() = %v, want one Degraded problem", got)
	}

	cancel()
	<-done
	if got := a.HealthProblems(); got != nil {
		t.Errorf("HealthProblems() after shutdown = %v, want nil (cleared on ctx cancellation)", got)
	}
}

type receiveFuncBroker struct {
	*broker.DevBroker
	receiveFn func(context.Context, broker.Queue) (*broker.Message, error)
}

func (b *receiveFuncBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	return b.receiveFn(ctx, q)
}

func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

type safeLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestEventMeshAdapter_DeprovisionAdapterQueues_NoBrokerIsNoOpEvenBeforeInit(t *testing.T) {
	a := &EventMeshAdapter{logger: slog.Default()}
	if err := a.DeprovisionAdapterQueues(context.Background()); err != nil {
		t.Errorf("expected nil for no-broker adapter, got %v", err)
	}
}

type errorOnFirstDeprovisionBroker struct {
	*broker.DevBroker
	failOn     string
	failErr    error
	mu         sync.Mutex
	called     []string
	failedOnce bool
}

func (b *errorOnFirstDeprovisionBroker) DeprovisionQueue(name string) error {
	b.mu.Lock()
	b.called = append(b.called, name)
	shouldFail := !b.failedOnce && name == b.failOn
	if shouldFail {
		b.failedOnce = true
	}
	b.mu.Unlock()
	if shouldFail {
		return b.failErr
	}
	return b.DevBroker.DeprovisionQueue(name)
}

func (b *errorOnFirstDeprovisionBroker) calledQueues() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.called))
	copy(out, b.called)
	return out
}

func TestEventMeshAdapter_DeprovisionAdapterQueues_PartialFailure_DoesNotShortCircuit(t *testing.T) {
	failQueue := "test-ns/q/eventmesh/em-gw-1/orders"
	sentinel := errors.New("simulated broker outage on orders queue")

	dataBroker := &errorOnFirstDeprovisionBroker{
		DevBroker: broker.NewDevBroker(),
		failOn:    failQueue,
		failErr:   sentinel,
	}
	if err := dataBroker.Connect(context.Background()); err != nil {
		t.Fatalf("connect data broker: %v", err)
	}

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "em-gw-1",
	})

	a := &EventMeshAdapter{
		core:       core,
		dataBroker: dataBroker,
		logger:     slog.Default(),
		cfg: &Config{
			EventHandlers: []*EventHandlerConfig{
				{Name: "orders"},
				{Name: "events"},
				{Name: "alerts"},
			},
		},
	}

	err := a.DeprovisionAdapterQueues(context.Background())
	if err == nil {
		t.Fatal("expected joined error from DeprovisionAdapterQueues; got nil — short-circuit regression?")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("returned error does not wrap sentinel: %v", err)
	}

	called := dataBroker.calledQueues()
	wantAll := []string{
		"test-ns/q/eventmesh/em-gw-1/orders",
		"test-ns/q/eventmesh/em-gw-1/events",
		"test-ns/q/eventmesh/em-gw-1/alerts",
	}
	for _, w := range wantAll {
		if !slices.Contains(called, w) {
			t.Errorf("DeprovisionAdapterQueues short-circuited after the failure: %q was never attempted (called=%v)", w, called)
		}
	}
}

func TestEventMeshAdapter_Start_ClearsTeardown(t *testing.T) {
	dataBroker := broker.NewDevBroker()

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "em-gw-1",
	})

	a := &EventMeshAdapter{
		core:       core,
		dataBroker: dataBroker,
		logger:     slog.Default(),
		cfg:        &Config{},
	}
	a.teardown.Store(true)
	a.health.Set("queue:leftover", health.Fatal, "queue no longer exists")

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if a.cancel != nil {
			a.cancel()
		}
		a.wg.Wait()
	})

	if a.teardown.Load() {
		t.Error("teardown still true after Start — a reused adapter would silently downgrade its next genuine queue loss to Info")
	}
	if problems := a.health.Snapshot(); problems != nil {
		t.Errorf("health.Snapshot() after Start = %v, want nil — a restarted adapter must not carry a permanently orphaned problem from its previous run", problems)
	}
}
