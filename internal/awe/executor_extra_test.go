// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
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

func (b *deprovisionTrackingBroker) deprovisionedNames() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.deprovisioned))
	copy(out, b.deprovisioned)
	return out
}

func newTrackingServices(t *testing.T) (runtime.Services, *deprovisionTrackingBroker) {
	t.Helper()
	brk := &deprovisionTrackingBroker{DevBroker: broker.NewDevBroker()}
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	return runtime.NewRuntime(brk, nil).Services(), brk
}

func TestAddAndStartSuccess(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})

	cfg := config.FromMap(map[string]any{
		"kind": "agent",
		"name": "dynamic-agent",
	})

	info, err := exe.AddAndStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if info.Name != "dynamic-agent" {
		t.Errorf("name = %q, want %q", info.Name, "dynamic-agent")
	}
	if info.Kind != "agent" {
		t.Errorf("kind = %q, want %q", info.Kind, "agent")
	}
	if info.Status != StatusRunning {
		t.Errorf("status = %q, want %q", info.Status, StatusRunning)
	}
	if !info.Enabled {
		t.Error("expected enabled = true")
	}

	if len(exe.Instances()) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(exe.Instances()))
	}
}

func TestAddAndStartMissingKind(t *testing.T) {
	exe := NewExecutor(newTestServices())
	cfg := config.FromMap(map[string]any{"name": "no-kind"})

	_, err := exe.AddAndStart(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for missing kind")
	}
}

func TestAddAndStartUnknownKind(t *testing.T) {
	exe := NewExecutor(newTestServices())
	cfg := config.FromMap(map[string]any{"kind": "unknown", "name": "x"})

	_, err := exe.AddAndStart(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestAddAndStartDuplicateName(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})
	exe.AddInstance(newMockInstance("dup", "agent"))

	cfg := config.FromMap(map[string]any{"kind": "agent", "name": "dup"})
	_, err := exe.AddAndStart(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestAddAndStartReplacesTerminalInstance(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})

	cfg := config.FromMap(map[string]any{"kind": "agent", "name": "orch"})

	if _, err := exe.AddAndStart(context.Background(), cfg); err != nil {
		t.Fatalf("first AddAndStart: %v", err)
	}

	if _, err := exe.AddAndStart(context.Background(), cfg); err == nil {
		t.Fatal("expected conflict while the instance is running")
	}

	if err := exe.StopInstance(context.Background(), "orch"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	info, err := exe.AddAndStart(context.Background(), cfg)
	if err != nil {
		t.Fatalf("redeploy after stop: %v", err)
	}
	if info.Status != StatusRunning {
		t.Errorf("status = %q, want %q", info.Status, StatusRunning)
	}
	if got := len(exe.Instances()); got != 1 {
		t.Fatalf("expected 1 instance after replace, got %d", got)
	}
}

func TestAddAndStartFactoryError(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return nil, fmt.Errorf("factory boom")
	})

	cfg := config.FromMap(map[string]any{"kind": "agent", "name": "fail"})
	_, err := exe.AddAndStart(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error from factory")
	}
}

func TestAddAndStartInitError(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		m := newMockInstance(cfg.GetString("name"), "agent")
		m.initErr = fmt.Errorf("init boom")
		return m, nil
	})

	cfg := config.FromMap(map[string]any{"kind": "agent", "name": "init-fail"})
	_, err := exe.AddAndStart(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error from init")
	}

	if len(exe.Instances()) != 0 {
		t.Errorf("expected 0 instances after init failure, got %d", len(exe.Instances()))
	}
}

func TestAddAndStartStartError(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		m := newMockInstance(cfg.GetString("name"), "agent")
		m.startErr = fmt.Errorf("start boom")
		return m, nil
	})

	cfg := config.FromMap(map[string]any{"kind": "agent", "name": "start-fail"})
	_, err := exe.AddAndStart(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error from start")
	}

	if len(exe.Instances()) != 0 {
		t.Errorf("expected 0 instances after start failure, got %d", len(exe.Instances()))
	}
}

func TestRemoveInstanceSuccess(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("to-remove", "agent")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	err := exe.RemoveInstance(context.Background(), "to-remove")
	if err != nil {
		t.Fatal(err)
	}

	if len(exe.Instances()) != 0 {
		t.Errorf("expected 0 instances, got %d", len(exe.Instances()))
	}
	if m.stopCalled.Load() != 1 {
		t.Error("expected stop to be called")
	}
}

func TestRemoveInstanceNotRunning(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("stopped", "agent")
	exe.AddInstance(m)

	err := exe.RemoveInstance(context.Background(), "stopped")
	if err != nil {
		t.Fatal(err)
	}

	if m.stopCalled.Load() != 0 {
		t.Error("stop should not be called for non-running instance")
	}
}

func TestRemoveInstance_ColdCache_DegradedServices_NoPanic(t *testing.T) {
	exe := NewExecutor(nil)
	if err := exe.RemoveInstance(context.Background(), "no-services"); err != nil {
		t.Errorf("RemoveInstance with nil services should succeed silently, got: %v", err)
	}
}

func TestRemoveInstance_ColdCache_DeprovisionsByName(t *testing.T) {
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)

	err := exe.RemoveInstance(context.Background(), "ghost-agent")
	if err != nil {
		t.Fatalf("RemoveInstance on cold cache should succeed (idempotent deprovision), got: %v", err)
	}

	want := "solace-agent-mesh/q/a2a/ghost-agent"
	names := brk.deprovisionedNames()
	if !slices.Contains(names, want) {
		t.Errorf("DeprovisionQueue calls = %v, want to include %q", names, want)
	}
}

type erroringDeprovisionBroker struct {
	*broker.DevBroker
	err error
}

func (b *erroringDeprovisionBroker) DeprovisionQueue(_ string) error {
	return b.err
}

func TestRemoveInstance_ColdCache_BrokerError_Propagates(t *testing.T) {
	sentinel := errors.New("simulated broker DeprovisionQueue failure")
	brk := &erroringDeprovisionBroker{
		DevBroker: broker.NewDevBroker(),
		err:       sentinel,
	}
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	svc := runtime.NewRuntime(brk, nil).Services()
	exe := NewExecutor(svc)

	err := exe.RemoveInstance(context.Background(), "doomed-agent")
	if err == nil {
		t.Fatal("expected RemoveInstance to return the broker error; got nil — orphan-queue regression?")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("returned error does not wrap sentinel: %v", err)
	}
}

func TestRemoveInstanceDispatchesToRemover(t *testing.T) {
	exe := NewExecutor(newTestServices())
	r := newRemoverInstance("with-remover", "agent")
	exe.AddInstance(r)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	if err := exe.RemoveInstance(context.Background(), "with-remover"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}

	if r.removeCalled.Load() != 1 {
		t.Errorf("Remove must be called for Instances implementing Remover. removeCalled = %d",
			r.removeCalled.Load())
	}
	if r.stopCalled.Load() != 1 {
		t.Errorf("Remove implementations also call Stop. stopCalled = %d", r.stopCalled.Load())
	}
}

func TestRemoveInstanceFallsBackToStopWithoutRemover(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("no-remover", "agent")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	if err := exe.RemoveInstance(context.Background(), "no-remover"); err != nil {
		t.Fatalf("RemoveInstance: %v", err)
	}

	if m.stopCalled.Load() != 1 {
		t.Errorf("Stop must still be called for Instances without Remover. stopCalled = %d",
			m.stopCalled.Load())
	}
}

func TestRemoveInstance_KeepQueues_ColdCache_NoDeprovision(t *testing.T) {
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)

	if err := exe.removeInstance(context.Background(), "ghost-agent", removeForReplace); err != nil {
		t.Fatalf("stop-only RemoveInstance on cold cache should be a no-op success, got: %v", err)
	}

	if names := brk.deprovisionedNames(); len(names) != 0 {
		t.Errorf("stop-only delete must not touch broker queues; DeprovisionQueue calls = %v", names)
	}
}

func TestRemoveInstance_KeepQueues_DispatchesToStopNotRemove(t *testing.T) {
	exe := NewExecutor(newTestServices())
	r := newRemoverInstance("replace-me", "agent")
	exe.AddInstance(r)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	if err := exe.removeInstance(context.Background(), "replace-me", removeForReplace); err != nil {
		t.Fatalf("removeInstance: %v", err)
	}

	if r.removeCalled.Load() != 0 {
		t.Errorf("stop-only delete must not call Remove (deprovisions queues). removeCalled = %d",
			r.removeCalled.Load())
	}
	if r.stopCalled.Load() != 1 {
		t.Errorf("stop-only delete must still Stop the instance. stopCalled = %d", r.stopCalled.Load())
	}
	if len(exe.Instances()) != 0 {
		t.Errorf("instance must still be removed from the executor, got %d instances", len(exe.Instances()))
	}
}

func TestHandleManagement_DeleteBody_RoutesDeprovisionFlag(t *testing.T) {
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)

	if _, err := exe.HandleManagement(context.Background(), "delete",
		[]string{"instances", "ghost-agent"}, json.RawMessage(`{"deprovision":false}`), nil); err != nil {
		t.Fatalf("delete with deprovision:false body: %v", err)
	}
	if names := brk.deprovisionedNames(); len(names) != 0 {
		t.Errorf("deprovision:false body must not touch broker queues; DeprovisionQueue calls = %v", names)
	}

	if _, err := exe.HandleManagement(context.Background(), "delete",
		[]string{"instances", "ghost-agent"}, nil, nil); err != nil {
		t.Fatalf("delete with no body: %v", err)
	}
	want := "solace-agent-mesh/q/a2a/ghost-agent"
	if names := brk.deprovisionedNames(); !slices.Contains(names, want) {
		t.Errorf("bodyless delete must keep undeploy semantics; DeprovisionQueue calls = %v, want %q", names, want)
	}
}

func TestRemoveInstanceStopError(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("err-stop", "agent")
	m.stopErr = fmt.Errorf("stop failed")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	err := exe.RemoveInstance(context.Background(), "err-stop")
	if err == nil {
		t.Fatal("expected error from stop")
	}
}

func TestStopInstanceSuccess(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("running", "agent")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.instances[0].enabled = true
	exe.mu.Unlock()

	err := exe.StopInstance(context.Background(), "running")
	if err != nil {
		t.Fatal(err)
	}

	if m.stopCalled.Load() != 1 {
		t.Error("expected stop to be called")
	}

	info, _ := exe.GetInstance("running")
	if info.Status != StatusStopped {
		t.Errorf("status = %q, want %q", info.Status, StatusStopped)
	}
	if info.Enabled {
		t.Error("expected enabled = false after stop")
	}
}

func TestStopInstanceNotFound(t *testing.T) {
	exe := NewExecutor(newTestServices())
	err := exe.StopInstance(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error for nonexistent instance")
	}
}

func TestStopInstanceNotRunning(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("created", "agent"))

	err := exe.StopInstance(context.Background(), "created")
	if err == nil {
		t.Fatal("expected error when instance is not running")
	}
}

func TestStopInstanceStopError(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("err", "agent")
	m.stopErr = fmt.Errorf("stop boom")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	err := exe.StopInstance(context.Background(), "err")
	if err == nil {
		t.Fatal("expected error from stop")
	}

	info, _ := exe.GetInstance("err")
	if info.Status != StatusError {
		t.Errorf("status = %q, want %q", info.Status, StatusError)
	}
}

func TestStartInstanceSuccess(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("stopped", "agent")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusStopped
	exe.instances[0].enabled = false
	exe.mu.Unlock()

	err := exe.StartInstance(context.Background(), "stopped")
	if err != nil {
		t.Fatal(err)
	}

	if m.startCalled.Load() != 1 {
		t.Error("expected start to be called")
	}

	info, _ := exe.GetInstance("stopped")
	if info.Status != StatusRunning {
		t.Errorf("status = %q, want %q", info.Status, StatusRunning)
	}
	if !info.Enabled {
		t.Error("expected enabled = true after start")
	}
}

func TestStartInstanceNotFound(t *testing.T) {
	exe := NewExecutor(newTestServices())
	err := exe.StartInstance(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error for nonexistent instance")
	}
}

func TestStartInstanceAlreadyRunning(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("running", "agent"))

	exe.mu.Lock()
	exe.instances[0].status = StatusRunning
	exe.mu.Unlock()

	err := exe.StartInstance(context.Background(), "running")
	if err == nil {
		t.Fatal("expected error when instance already running")
	}
}

func TestStartInstanceStartError(t *testing.T) {
	exe := NewExecutor(newTestServices())
	m := newMockInstance("err", "agent")
	m.startErr = fmt.Errorf("start boom")
	exe.AddInstance(m)

	exe.mu.Lock()
	exe.instances[0].status = StatusStopped
	exe.mu.Unlock()

	err := exe.StartInstance(context.Background(), "err")
	if err == nil {
		t.Fatal("expected error from start")
	}

	info, _ := exe.GetInstance("err")
	if info.Status != StatusError {
		t.Errorf("status = %q, want %q", info.Status, StatusError)
	}
}

func TestListInstancesEmpty(t *testing.T) {
	exe := NewExecutor(newTestServices())
	list := exe.ListInstances()
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestListInstancesMultiple(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("a", "agent"))
	exe.AddInstance(newMockInstance("b", "workflow"))

	list := exe.ListInstances()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}

	kinds := map[string]string{}
	for _, info := range list {
		kinds[info.Name] = info.Kind
	}
	if kinds["a"] != "agent" {
		t.Errorf("a kind = %q", kinds["a"])
	}
	if kinds["b"] != "workflow" {
		t.Errorf("b kind = %q", kinds["b"])
	}
}

func TestGetInstanceSuccess(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("target", "agent"))

	info, err := exe.GetInstance("target")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "target" {
		t.Errorf("name = %q", info.Name)
	}
	if info.Kind != "agent" {
		t.Errorf("kind = %q", info.Kind)
	}
	if info.Status != StatusCreated {
		t.Errorf("status = %q, want %q", info.Status, StatusCreated)
	}
}

func TestGetInstanceNotFound(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.GetInstance("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent instance")
	}
}
