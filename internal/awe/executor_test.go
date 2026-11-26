// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type mockInstance struct {
	name        string
	displayName string
	kind        string
	initErr     error
	startErr    error
	stopErr     error
	healthErr   error

	initCalled  atomic.Int32
	startCalled atomic.Int32
	stopCalled  atomic.Int32
}

func newMockInstance(name, kind string) *mockInstance {
	return &mockInstance{name: name, kind: kind}
}

func (m *mockInstance) Name() string        { return m.name }
func (m *mockInstance) DisplayName() string { return m.displayName }
func (m *mockInstance) Kind() string        { return m.kind }

func (m *mockInstance) Init(_ context.Context, _ config.Config, _ runtime.Services) error {
	m.initCalled.Add(1)
	return m.initErr
}

func (m *mockInstance) Start(_ context.Context) error {
	m.startCalled.Add(1)
	return m.startErr
}

func (m *mockInstance) Stop(_ context.Context) error {
	m.stopCalled.Add(1)
	return m.stopErr
}

func (m *mockInstance) Health() error {
	return m.healthErr
}

type removerInstance struct {
	*mockInstance
	removeCalled atomic.Int32
	removeErr    error
}

func newRemoverInstance(name, kind string) *removerInstance {
	return &removerInstance{mockInstance: newMockInstance(name, kind)}
}

func (r *removerInstance) Remove(ctx context.Context) error {
	r.removeCalled.Add(1)
	_ = r.Stop(ctx)
	return r.removeErr
}

func newTestServices() runtime.Services {
	return runtime.NewRuntime(broker.NewDevBroker(), nil).Services()
}

func TestExecutorAddAndList(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("agent-a", "agent")
	b := newMockInstance("agent-b", "agent")
	exe.AddInstance(a)
	exe.AddInstance(b)

	if len(exe.Instances()) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(exe.Instances()))
	}
}

func TestExecutorInit(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	b := newMockInstance("b", "agent")
	exe.AddInstance(a)
	exe.AddInstance(b)

	if err := exe.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	if a.initCalled.Load() != 1 {
		t.Error("instance a Init not called")
	}
	if b.initCalled.Load() != 1 {
		t.Error("instance b Init not called")
	}
}

func TestExecutorInitFailure(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	b := newMockInstance("b", "agent")
	b.initErr = fmt.Errorf("init failed")
	exe.AddInstance(a)
	exe.AddInstance(b)

	if err := exe.Init(context.Background(), nil); err != nil {
		t.Fatalf("one failed instance must not fail the executor: %v", err)
	}
	if a.initCalled.Load() != 1 {
		t.Error("instance a Init should have been called")
	}
	if got := statusOf(t, exe, "b"); got != StatusError {
		t.Errorf("failed instance status = %q, want %q", got, StatusError)
	}
}

func TestExecutorStart(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	b := newMockInstance("b", "agent")
	exe.AddInstance(a)
	exe.AddInstance(b)

	if err := exe.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if a.startCalled.Load() != 1 || b.startCalled.Load() != 1 {
		t.Error("not all instances started")
	}
}

func TestExecutorStop(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	b := newMockInstance("b", "agent")
	exe.AddInstance(a)
	exe.AddInstance(b)

	if err := exe.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if a.stopCalled.Load() != 1 || b.stopCalled.Load() != 1 {
		t.Error("not all instances stopped")
	}
}

func TestExecutorStop_RunsStopHooksAfterEveryInstanceHasStopped(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	exe.AddInstance(a)
	var stoppedWhenHookRan int32
	exe.OnStop(func(context.Context) error {
		stoppedWhenHookRan = a.stopCalled.Load()
		return errors.New("hook failed")
	})

	err := exe.Stop(context.Background())

	if stoppedWhenHookRan != 1 {
		t.Errorf("instances stopped when the hook ran = %d, want 1", stoppedWhenHookRan)
	}
	if err == nil || err.Error() != "hook failed" {
		t.Errorf("Stop err = %v, want the hook's error", err)
	}
}

func TestExecutorStopConcurrent(t *testing.T) {
	exe := NewExecutor(newTestServices())
	for i := range 10 {
		exe.AddInstance(newMockInstance(fmt.Sprintf("inst-%d", i), "agent"))
	}

	if err := exe.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, inst := range exe.Instances() {
		m := inst.(*mockInstance)
		if m.stopCalled.Load() != 1 {
			t.Errorf("instance %q not stopped", m.name)
		}
	}
}

func TestExecutorStopError(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	a.stopErr = fmt.Errorf("stop failed")
	exe.AddInstance(a)

	err := exe.Stop(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecutorHealth(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	b := newMockInstance("b", "agent")
	exe.AddInstance(a)
	exe.AddInstance(b)

	if err := exe.Health(); err != nil {
		t.Errorf("healthy instances should pass: %v", err)
	}

	b.healthErr = fmt.Errorf("LLM timeout")
	err := exe.Health()
	if err == nil {
		t.Error("one dead instance among live ones was reported as fully healthy")
	}
	if !errors.Is(err, runtime.ErrDegraded) {
		t.Errorf("one dead instance among live ones must stay non-fatal: %v", err)
	}

	a.healthErr = fmt.Errorf("LLM timeout")
	if err := exe.Health(); err == nil {
		t.Error("expected health failure when all instances are dead")
	}
}

func TestExecutorHealthDegradedIsolation(t *testing.T) {
	degradedErr := fmt.Errorf("agent card not available: %w", runtime.ErrDegraded)

	exe := NewExecutor(newTestServices())
	healthy := newMockInstance("healthy", "agent")
	proxy := newMockInstance("proxy", "proxy")
	proxy.healthErr = degradedErr
	exe.AddInstance(healthy)
	exe.AddInstance(proxy)
	if err := exe.Health(); err != nil {
		t.Errorf("degraded instance alongside a healthy one must not fail health: %v", err)
	}

	solo := NewExecutor(newTestServices())
	soloProxy := newMockInstance("proxy", "proxy")
	soloProxy.healthErr = degradedErr
	solo.AddInstance(soloProxy)
	if err := solo.Health(); err != nil {
		t.Errorf("all-degraded (recoverable) must not fail health: %v", err)
	}
}

func TestExecutorHealthAllDead(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	b := newMockInstance("b", "agent")
	a.healthErr = fmt.Errorf("ctx canceled")
	b.healthErr = fmt.Errorf("ctx canceled")
	exe.AddInstance(a)
	exe.AddInstance(b)
	if err := exe.Health(); err == nil {
		t.Error("all-dead must fail health so k8s restarts the pod")
	}

	b.healthErr = fmt.Errorf("agent card not available: %w", runtime.ErrDegraded)
	if err := exe.Health(); !errors.Is(err, runtime.ErrDegraded) {
		t.Errorf("dead + degraded (one recoverable) must stay non-fatal: %v", err)
	}
}

func TestExecutorHealthRestartRequiredFailsAmongLiveInstances(t *testing.T) {
	exe := NewExecutor(newTestServices())
	live := newMockInstance("live", "agent")
	lost := newMockInstance("lost", "agent")
	exe.AddInstance(live)
	exe.AddInstance(lost)

	lost.healthErr = fmt.Errorf("agent %q request receive loop exited: %w", "lost", runtime.ErrRestartRequired)
	err := exe.Health()
	if err == nil || errors.Is(err, runtime.ErrDegraded) {
		t.Fatalf("Health = %v, want a non-degraded error so the pod restarts", err)
	}
	if !errors.Is(err, runtime.ErrRestartRequired) {
		t.Errorf("Health = %v, want it to wrap the instance's ErrRestartRequired", err)
	}

	lost.healthErr = nil
	setInstanceStatus(exe, "lost", StatusError)
	if err := exe.Health(); !errors.Is(err, runtime.ErrDegraded) {
		t.Errorf("an instance in StatusError beside a live one must stay degraded: %v", err)
	}
}

func TestExecutorHealthStoppedSkippedAndEmpty(t *testing.T) {
	empty := NewExecutor(newTestServices())
	if err := empty.Health(); err != nil {
		t.Errorf("empty executor should be healthy: %v", err)
	}

	exe := NewExecutor(newTestServices())
	live := newMockInstance("live", "agent")
	dead := newMockInstance("dead", "agent")
	dead.healthErr = fmt.Errorf("ctx canceled")
	exe.AddInstance(live)
	exe.AddInstance(dead)

	setInstanceStatus(exe, "dead", StatusStopped)
	if err := exe.Health(); err != nil {
		t.Errorf("stopped dead instance must be skipped: %v", err)
	}

	setInstanceStatus(exe, "dead", StatusRunning)
	setInstanceStatus(exe, "live", StatusStopped)
	err := exe.Health()
	if err == nil {
		t.Fatal("expected all-dead error when the only live instance is dead")
	}
	if !errors.Is(err, dead.healthErr) {
		t.Errorf("all-dead error must wrap the instance error, got %v", err)
	}
}

func setInstanceStatus(e *Executor, name, status string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, mi := range e.instances {
		if mi.inst.Name() == name {
			mi.setStatus(status)
		}
	}
}

func TestExecutorInitAndStart(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	b := newMockInstance("b", "agent")
	exe.AddInstance(a)
	exe.AddInstance(b)

	if err := exe.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := exe.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if a.initCalled.Load() != 1 || b.initCalled.Load() != 1 {
		t.Error("not all instances initialized")
	}
	if a.startCalled.Load() != 1 || b.startCalled.Load() != 1 {
		t.Error("not all instances started")
	}
}

func TestExecutorInitNoInstances(t *testing.T) {
	exe := NewExecutor(newTestServices())
	if err := exe.Init(context.Background(), nil); err != nil {
		t.Errorf("Init with no instances should succeed (await control-plane deploys), got %v", err)
	}
}

func TestExecutorInitFailureNoStart(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	a.initErr = fmt.Errorf("init failed")
	healthy := newMockInstance("healthy", "agent")
	exe.AddInstance(a)
	exe.AddInstance(healthy)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if a.startCalled.Load() != 0 {
		t.Error("an instance that failed Init was started anyway")
	}
	if healthy.startCalled.Load() != 1 {
		t.Error("the healthy sibling was not started")
	}
}

func TestExecutorLoadConfig(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		name := cfg.GetString("name")
		return newMockInstance(name, "agent"), nil
	})

	cfg := config.FromMap(map[string]any{
		"kind": "agent",
		"name": "test-agent",
	})

	if err := exe.LoadConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if len(exe.Instances()) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(exe.Instances()))
	}
	if exe.Instances()[0].Name() != "test-agent" {
		t.Errorf("name = %q", exe.Instances()[0].Name())
	}
}

func TestExecutorLoadConfigUnknownKind(t *testing.T) {
	exe := NewExecutor(newTestServices())
	cfg := config.FromMap(map[string]any{
		"kind": "unknown",
		"name": "test",
	})

	if err := exe.LoadConfig(cfg); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestExecutorLoadConfigMissingKind(t *testing.T) {
	exe := NewExecutor(newTestServices())
	cfg := config.FromMap(map[string]any{
		"name": "test",
	})

	if err := exe.LoadConfig(cfg); err == nil {
		t.Error("expected error for missing kind")
	}
}

func TestExecutorImplementsComponent(t *testing.T) {
	var _ runtime.Component = (*Executor)(nil)
}

type mockVolumeService struct {
	cleanupCalls atomic.Int32
}

func (m *mockVolumeService) Create(_ context.Context, _ volume.CreateOptions) (string, error) {
	return "", nil
}
func (m *mockVolumeService) Get(_ context.Context, _ string) (*volume.Volume, error) {
	return nil, nil
}
func (m *mockVolumeService) List(_ context.Context, _ string, _ volume.ListFilter) ([]*volume.Volume, error) {
	return nil, nil
}
func (m *mockVolumeService) Delete(_ context.Context, _ string, _ volume.CallerType) error {
	return nil
}
func (m *mockVolumeService) RemoveIfExpired(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockVolumeService) AcquireWriteLock(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}
func (m *mockVolumeService) ReleaseWriteLock(_ context.Context, _ string, _ string) error {
	return nil
}
func (m *mockVolumeService) RecordAccess(_ context.Context, _ string) error { return nil }
func (m *mockVolumeService) Resolve(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (m *mockVolumeService) CleanupExpired(_ context.Context) (int, error) {
	m.cleanupCalls.Add(1)
	return 0, nil
}

func (m *mockVolumeService) AssociateSession(_ context.Context, _ string, _ string) error {
	return nil
}

func (m *mockVolumeService) ListSessionVolumes(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (m *mockVolumeService) UserUsage(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (m *mockVolumeService) RefreshSize(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (m *mockVolumeService) RefreshAllSizes(_ context.Context) (int, error) {
	return 0, nil
}

func (m *mockVolumeService) PerUserQuotaBytes() int64 { return 0 }

var _ volume.Service = (*mockVolumeService)(nil)

func TestExecutorStartLaunchesVolumeCleanup(t *testing.T) {
	volSvc := &mockVolumeService{}
	svc := runtime.NewRuntime(broker.NewDevBroker(), nil, runtime.WithVolumes(volSvc)).Services()
	exe := NewExecutor(svc)

	exe.AddInstance(newMockInstance("a", "agent"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if err := exe.Start(ctx); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	if volSvc.cleanupCalls.Load() < 1 {
		t.Error("expected CleanupExpired to be called at least once on startup")
	}

	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestExecutorStartNoVolumeService(t *testing.T) {
	svc := runtime.NewRuntime(broker.NewDevBroker(), nil).Services()
	exe := NewExecutor(svc)
	exe.AddInstance(newMockInstance("a", "agent"))

	if err := exe.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type slowInstance struct {
	mockInstance
	initStarted chan struct{}
	initProceed chan struct{}
	stopStarted chan struct{}
	stopProceed chan struct{}
}

func (s *slowInstance) Init(ctx context.Context, _ config.Config, _ runtime.Services) error {
	s.initCalled.Add(1)
	if s.initStarted != nil {
		close(s.initStarted)
	}
	if s.initProceed != nil {
		select {
		case <-s.initProceed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.initErr
}

func (s *slowInstance) Stop(ctx context.Context) error {
	s.stopCalled.Add(1)
	if s.stopStarted != nil {
		close(s.stopStarted)
	}
	if s.stopProceed != nil {
		select {
		case <-s.stopProceed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.stopErr
}

func TestExecutorPerNameLock_AddAndRemove_SameName(t *testing.T) {
	exe := NewExecutor(newTestServices())

	si := &slowInstance{
		mockInstance: mockInstance{name: "racy", kind: "agent"},
		initStarted:  make(chan struct{}),
		initProceed:  make(chan struct{}),
	}
	exe.RegisterKind("agent", func(_ config.Config) (Instance, error) {
		return si, nil
	})

	cfg := config.FromMap(map[string]any{
		"kind": "agent",
		"name": "racy",
	})

	addDone := make(chan error, 1)
	go func() {
		_, err := exe.AddAndStart(context.Background(), cfg)
		addDone <- err
	}()

	select {
	case <-si.initStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("AddAndStart never reached Init")
	}

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- exe.RemoveInstance(context.Background(), "racy")
	}()

	select {
	case err := <-removeDone:
		t.Fatalf("RemoveInstance returned (%v) before AddAndStart finished — per-name lock not honoured", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(si.initProceed)

	if err := <-addDone; err != nil {
		t.Fatalf("AddAndStart failed: %v", err)
	}

	select {
	case err := <-removeDone:
		if err != nil {
			t.Fatalf("RemoveInstance failed after lock release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveInstance did not run after AddAndStart finished")
	}

	if got := si.stopCalled.Load(); got != 1 {
		t.Errorf("stopCalled = %d, want exactly 1 (no double-Stop)", got)
	}
	for _, in := range exe.Instances() {
		if in.Name() == "racy" {
			t.Errorf("instance %q still present after RemoveInstance", in.Name())
		}
	}
}

func TestExecutorPerNameLock_DoubleRemove_NoDoubleStop(t *testing.T) {
	exe := NewExecutor(newTestServices())

	si := &slowInstance{
		mockInstance: mockInstance{name: "racy", kind: "agent"},
		stopStarted:  make(chan struct{}),
		stopProceed:  make(chan struct{}),
	}
	exe.AddInstance(si)

	for _, mi := range exe.snapshotForTest() {
		mi.setStatus(StatusRunning)
	}

	var firstErr, secondErr error
	first := make(chan struct{})
	go func() {
		firstErr = exe.RemoveInstance(context.Background(), "racy")
		close(first)
	}()

	select {
	case <-si.stopStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first RemoveInstance never reached Stop")
	}

	second := make(chan struct{})
	go func() {
		secondErr = exe.RemoveInstance(context.Background(), "racy")
		close(second)
	}()

	select {
	case <-second:
		t.Fatal("second RemoveInstance completed before first released the per-name lock")
	case <-time.After(100 * time.Millisecond):
	}

	close(si.stopProceed)

	<-first
	<-second

	if firstErr != nil {
		t.Errorf("first RemoveInstance failed: %v", firstErr)
	}
	if secondErr != nil {
		t.Errorf("second RemoveInstance should succeed via cold-cache deprovision, got: %v", secondErr)
	}

	if got := si.stopCalled.Load(); got != 1 {
		t.Errorf("stopCalled = %d, want exactly 1 (no double-Stop)", got)
	}
}

func (e *Executor) snapshotForTest() []*managedInstance {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*managedInstance, len(e.instances))
	copy(out, e.instances)
	return out
}
