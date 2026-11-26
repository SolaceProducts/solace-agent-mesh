// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type mockLLMClient struct {
	name string
}

func (m *mockLLMClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: m.name}, nil
}

func (m *mockLLMClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("not implemented")
}

type mockComponent struct {
	name        string
	initErr     error
	startErr    error
	stopErr     error
	healthErr   error
	initCalled  bool
	startCalled bool
	stopCalled  bool
	order       *[]string
	mu          sync.Mutex
}

func (m *mockComponent) Init(_ context.Context, _ config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initCalled = true
	if m.order != nil {
		*m.order = append(*m.order, "init:"+m.name)
	}
	return m.initErr
}

func (m *mockComponent) Start(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalled = true
	if m.order != nil {
		*m.order = append(*m.order, "start:"+m.name)
	}
	return m.startErr
}

func (m *mockComponent) Stop(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalled = true
	if m.order != nil {
		*m.order = append(*m.order, "stop:"+m.name)
	}
	return m.stopErr
}

func (m *mockComponent) Health() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.healthErr
}

func newTestRuntime() *Runtime {
	b := broker.NewDevBroker()
	_ = b.Connect(context.Background())
	return NewRuntime(b, nil)
}

func TestRuntimeInitStartStop(t *testing.T) {
	rt := newTestRuntime()
	c1 := &mockComponent{name: "c1"}
	c2 := &mockComponent{name: "c2"}
	rt.Register("c1", c1)
	rt.Register("c2", c2)

	ctx := context.Background()

	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if !c1.initCalled || !c2.initCalled {
		t.Error("init should be called on all components")
	}
	if rt.Phase() != PhaseInitialized {
		t.Errorf("phase = %d, want %d", rt.Phase(), PhaseInitialized)
	}

	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !c1.startCalled || !c2.startCalled {
		t.Error("start should be called on all components")
	}
	if !rt.Ready() {
		t.Error("should be ready after start")
	}

	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !c1.stopCalled || !c2.stopCalled {
		t.Error("stop should be called on all components")
	}
	if rt.Phase() != PhaseStopped {
		t.Errorf("phase = %d, want %d", rt.Phase(), PhaseStopped)
	}
}

type drainObserverComponent struct {
	rt             *Runtime
	drainingAtStop bool
}

func (d *drainObserverComponent) Init(context.Context, config.Config) error { return nil }
func (d *drainObserverComponent) Start(context.Context) error               { return nil }
func (d *drainObserverComponent) Stop(context.Context) error {
	d.drainingAtStop = d.rt.IsDraining()
	return nil
}
func (d *drainObserverComponent) Health() error { return nil }

func TestRuntimeIsDrainingDuringStop(t *testing.T) {
	rt := newTestRuntime()
	obs := &drainObserverComponent{rt: rt}
	rt.Register("obs", obs)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if rt.IsDraining() {
		t.Fatal("IsDraining should be false before Stop")
	}

	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !obs.drainingAtStop {
		t.Error("IsDraining should be true while components are being stopped")
	}
	if !rt.IsDraining() {
		t.Error("IsDraining should remain true after Stop")
	}
}

func TestRuntimeInitOrder(t *testing.T) {
	rt := newTestRuntime()
	var order []string

	c1 := &mockComponent{name: "a", order: &order}
	c2 := &mockComponent{name: "b", order: &order}
	c3 := &mockComponent{name: "c", order: &order}

	rt.Register("a", c1)
	rt.Register("b", c2)
	rt.Register("c", c3)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	expected := []string{
		"init:a", "init:b", "init:c",
		"start:a", "start:b", "start:c",
		"stop:c", "stop:b", "stop:a",
	}
	if len(order) != len(expected) {
		t.Fatalf("order = %v, want %v", order, expected)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %q, want %q", i, order[i], v)
		}
	}
}

func TestRuntimeInitError(t *testing.T) {
	rt := newTestRuntime()
	c1 := &mockComponent{name: "good"}
	c2 := &mockComponent{name: "bad", initErr: fmt.Errorf("init failed")}
	c3 := &mockComponent{name: "never"}

	rt.Register("good", c1)
	rt.Register("bad", c2)
	rt.Register("never", c3)

	err := rt.Init(context.Background())
	if err == nil {
		t.Fatal("should error")
	}
	if !c1.initCalled {
		t.Error("c1 should be initialized")
	}
	if !c2.initCalled {
		t.Error("c2 should be initialized (and fail)")
	}
	if c3.initCalled {
		t.Error("c3 should NOT be initialized after c2 fails")
	}
}

func TestRuntimeHealth(t *testing.T) {
	rt := newTestRuntime()
	healthy := &mockComponent{name: "healthy"}
	unhealthy := &mockComponent{name: "sick", healthErr: errors.New("disk full")}

	rt.Register("healthy", healthy)
	if err := rt.Health(); err != nil {
		t.Errorf("should be healthy: %v", err)
	}

	rt.Register("sick", unhealthy)
	if err := rt.Health(); err == nil {
		t.Error("should be unhealthy")
	}
}

func TestRuntimeHealthDegradedNotFatal(t *testing.T) {
	rt := newTestRuntime()
	rt.Register("healthy", &mockComponent{name: "healthy"})
	rt.Register("degraded", &mockComponent{name: "degraded", healthErr: fmt.Errorf("card missing: %w", ErrDegraded)})
	if err := rt.Health(); err != nil {
		t.Errorf("degraded component must not fail runtime health: %v", err)
	}

	rt.Register("sick", &mockComponent{name: "sick", healthErr: errors.New("disk full")})
	if err := rt.Health(); err == nil {
		t.Error("a non-degraded component error must still fail runtime health")
	}
}

func TestRuntimeGracePeriod(t *testing.T) {
	rt := NewRuntime(broker.NewDevBroker(), nil, WithGracePeriod(100*time.Millisecond))
	rt.Register("slow", &slowComponent{delay: 50 * time.Millisecond})

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Error("stop should have waited for component")
	}
}

type slowComponent struct {
	delay time.Duration
}

func (c *slowComponent) Init(_ context.Context, _ config.Config) error { return nil }
func (c *slowComponent) Start(_ context.Context) error                 { return nil }
func (c *slowComponent) Stop(_ context.Context) error {
	time.Sleep(c.delay)
	return nil
}
func (c *slowComponent) Health() error { return nil }

func TestRuntimeServices(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)

	svc := rt.Services()
	if svc.Broker() != b {
		t.Error("broker mismatch")
	}
	if svc.KVStore() == nil {
		t.Error("kvstore should not be nil")
	}
	if svc.Cache() == nil {
		t.Error("cache should not be nil")
	}
	if svc.LLM("") != nil {
		t.Error("default LLM should be nil when not configured")
	}
	if svc.Artifacts() != nil {
		t.Error("artifacts should be nil when not configured")
	}
	if svc.Sessions() != nil {
		t.Error("sessions should be nil when not configured")
	}
}

func TestRuntimeServicesLLMRegistry(t *testing.T) {
	b := broker.NewDevBroker()

	defaultClient := &mockLLMClient{name: "default"}
	gpt4Client := &mockLLMClient{name: "gpt4"}

	rt := NewRuntime(b, nil,
		WithDefaultLLM(defaultClient),
		WithLLM("gpt-4", gpt4Client),
	)
	svc := rt.Services()

	if svc.LLM("") != defaultClient {
		t.Error("empty model should return default LLM")
	}

	if svc.LLM("gpt-4") != gpt4Client {
		t.Error("gpt-4 should return gpt4 client")
	}

	if svc.LLM("unknown-model") != defaultClient {
		t.Error("unknown model should fall back to default")
	}
}

func TestRuntimeWithLLMSetsDefault(t *testing.T) {
	b := broker.NewDevBroker()
	client := &mockLLMClient{name: "first"}

	rt := NewRuntime(b, nil, WithLLM("model-a", client))
	svc := rt.Services()

	if svc.LLM("") != client {
		t.Error("first WithLLM should set default")
	}
	if svc.LLM("model-a") != client {
		t.Error("model-a should return the client")
	}
}

func TestRuntimeStopOnlyStartedComponents(t *testing.T) {
	rt := newTestRuntime()
	var order []string

	c1 := &mockComponent{name: "a", order: &order}
	c2 := &mockComponent{name: "b", order: &order, startErr: fmt.Errorf("start failed")}
	c3 := &mockComponent{name: "c", order: &order}

	rt.Register("a", c1)
	rt.Register("b", c2)
	rt.Register("c", c3)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}

	err := rt.Start(ctx)
	if err == nil {
		t.Fatal("expected start error")
	}

	order = nil
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	expected := []string{"stop:a"}
	if len(order) != len(expected) {
		t.Fatalf("stop order = %v, want %v", order, expected)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %q, want %q", i, order[i], v)
		}
	}

	if c3.startCalled {
		t.Error("c3 should not have been started")
	}
	if c3.stopCalled {
		t.Error("c3 should not have been stopped")
	}
}

func TestRuntimeComponents(t *testing.T) {
	rt := newTestRuntime()
	c1 := &mockComponent{name: "comp1"}
	c2 := &mockComponent{name: "comp2"}
	rt.Register("comp1", c1)
	rt.Register("comp2", c2)

	comps := rt.Components()
	if len(comps) != 2 {
		t.Fatalf("expected 2 components, got %d", len(comps))
	}
	if comps[0].Name != "comp1" || comps[1].Name != "comp2" {
		t.Errorf("component names = %q, %q", comps[0].Name, comps[1].Name)
	}
}

func TestRuntimeStopError(t *testing.T) {
	rt := newTestRuntime()
	c1 := &mockComponent{name: "a", stopErr: fmt.Errorf("stop failed")}
	c2 := &mockComponent{name: "b"}

	rt.Register("a", c1)
	rt.Register("b", c2)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	err := rt.Stop(ctx)
	if err == nil {
		t.Fatal("expected stop error")
	}
	if !c1.stopCalled || !c2.stopCalled {
		t.Error("both components should have Stop called")
	}
}

func TestPhaseString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		phase Phase
		want  string
	}{
		{PhaseCreated, "created"},
		{PhaseInitialized, "initialized"},
		{PhaseRunning, "running"},
		{PhaseStopping, "stopping"},
		{PhaseStopped, "stopped"},
		{Phase(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.phase.String(); got != tt.want {
			t.Errorf("Phase(%d).String() = %q, want %q", tt.phase, got, tt.want)
		}
	}
}

func TestRuntimeReadyRequiresBrokerConnected(t *testing.T) {
	t.Parallel()

	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)
	ctx := t.Context()

	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if rt.Ready() {
		t.Error("Ready should be false when broker is disconnected")
	}

	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if !rt.Ready() {
		t.Error("Ready should be true once broker is connected")
	}

	if err := b.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if rt.Ready() {
		t.Error("Ready should be false after broker disconnect")
	}
}

func TestRuntimeReadyAbsentBrokerIsReady(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(nil, nil)
	ctx := t.Context()

	if rt.Ready() {
		t.Error("Ready should be false before Start")
	}
	if err := rt.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !rt.Ready() {
		t.Error("Ready should be true when no broker is registered and phase is Running")
	}
}

func TestGracePeriodFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		grace    string
		preStop  string
		want     time.Duration
		wantWarn string
	}{
		{name: "unset outside Kubernetes", want: defaultGracePeriod},
		{name: "grace less margin", grace: "60", want: 55 * time.Second},
		{name: "preStop counts against grace", grace: "60", preStop: "10", want: 45 * time.Second},
		{name: "unset preStop is no sleep", grace: "60", preStop: "", want: 55 * time.Second},
		{name: "preStop counts against the default grace", preStop: "10", want: 15 * time.Second},
		{name: "invalid grace", grace: "sixty", want: defaultGracePeriod, wantWarn: "invalid termination grace period"},
		{name: "preStop counts against the default for an invalid grace", grace: "sixty", preStop: "10", want: 15 * time.Second, wantWarn: "invalid termination grace period"},
		{name: "negative grace", grace: "-60", want: defaultGracePeriod, wantWarn: "invalid termination grace period"},
		{name: "invalid preStop ignored", grace: "60", preStop: "ten", want: 55 * time.Second, wantWarn: "invalid preStop sleep"},
		{name: "margin shrinks to half of a short budget", grace: "30", preStop: "26", want: 2 * time.Second},
		{name: "no budget left", grace: "10", preStop: "10", want: 0, wantWarn: "leaves no termination grace period"},
		{name: "preStop past grace", grace: "10", preStop: "20", want: 0, wantWarn: "leaves no termination grace period"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(graceSecondsEnv, tt.grace)
			t.Setenv(preStopSleepSecondsEnv, tt.preStop)
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			if got := gracePeriodFromEnv(logger); got != tt.want {
				t.Errorf("gracePeriodFromEnv() = %s, want %s", got, tt.want)
			}
			if tt.wantWarn == "" {
				if logs.Len() != 0 {
					t.Errorf("valid inputs logged: %s", logs.String())
				}
				return
			}
			if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), tt.wantWarn) {
				t.Errorf("want a WARN containing %q, got: %s", tt.wantWarn, logs.String())
			}
		})
	}
}

func TestNewRuntimeGracePeriod(t *testing.T) {
	t.Setenv(graceSecondsEnv, "60")
	t.Setenv(preStopSleepSecondsEnv, "10")

	if got := NewRuntime(broker.NewDevBroker(), nil).gracePeriod; got != 45*time.Second {
		t.Errorf("gracePeriod from env = %s, want 45s", got)
	}
	if got := NewRuntime(broker.NewDevBroker(), nil, WithGracePeriod(time.Second)).gracePeriod; got != time.Second {
		t.Errorf("WithGracePeriod must override the env: got %s, want 1s", got)
	}
}
