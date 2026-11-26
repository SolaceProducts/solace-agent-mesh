// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func statusOf(t *testing.T, exe *Executor, name string) string {
	t.Helper()
	for _, info := range exe.ListInstances() {
		if info.Name == name {
			return info.Status
		}
	}
	t.Fatalf("instance %q not listed", name)
	return ""
}

func TestExecutorInit_OneInstanceFails_OthersStillInitialize(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	bad := newMockInstance("bad", "workflow")
	bad.initErr = fmt.Errorf("bad output reference")
	c := newMockInstance("c", "agent")
	exe.AddInstance(a)
	exe.AddInstance(bad)
	exe.AddInstance(c)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init failed the whole executor for one bad instance: %v", err)
	}

	if c.initCalled.Load() != 1 {
		t.Error("an instance declared after the failing one was never initialized")
	}
	if a.stopCalled.Load() != 0 {
		t.Error("an already-initialized instance was torn down by an unrelated failure")
	}
	if got := statusOf(t, exe, "bad"); got != StatusError {
		t.Errorf("failed instance status = %q, want %q", got, StatusError)
	}
	if got := statusOf(t, exe, "a"); got == StatusError {
		t.Error("a healthy instance was marked error")
	}
}

func TestExecutorHealth_PartialInitFailure_IsDegradedNotSilent(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	bad := newMockInstance("bad", "workflow")
	bad.initErr = fmt.Errorf("bad output reference")
	exe.AddInstance(a)
	exe.AddInstance(bad)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	err := exe.Health()
	if err == nil {
		t.Fatal("a failed instance left Health reporting silently healthy")
	}
	if !errors.Is(err, runtime.ErrDegraded) {
		t.Errorf("Health error does not wrap ErrDegraded, so it would kill the process: %v", err)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("Health error does not name the failed instance: %v", err)
	}
}

func TestExecutorInit_AllInstancesFail_StaysFatal(t *testing.T) {
	exe := NewExecutor(newTestServices())
	for _, name := range []string{"bad1", "bad2"} {
		bad := newMockInstance(name, "workflow")
		bad.initErr = fmt.Errorf("bad output reference")
		exe.AddInstance(bad)
	}

	err := exe.Init(t.Context(), nil)
	if err == nil {
		t.Fatal("an AWE where every instance failed reported a successful Init, so the pod would report ready while serving nothing")
	}

	healthErr := exe.Health()
	if healthErr == nil {
		t.Fatal("an AWE serving nothing reported healthy")
	}
	if errors.Is(healthErr, runtime.ErrDegraded) {
		t.Error("an AWE serving nothing reported merely degraded, so nothing escalates")
	}
}

func TestExecutorHealth_NoInstances_StaysHealthy(t *testing.T) {
	exe := NewExecutor(newTestServices())
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Health(); err != nil {
		t.Errorf("an intentionally empty AWE reported unhealthy: %v", err)
	}
}

func TestPartiallyFailedAWEKeepsRuntimeHealthy(t *testing.T) {
	exe := NewExecutor(newTestServices())
	good := newMockInstance("good", "agent")
	bad := newMockInstance("bad", "workflow")
	bad.initErr = fmt.Errorf("bad output reference")
	exe.AddInstance(good)
	exe.AddInstance(bad)

	rt := runtime.NewRuntime(broker.NewDevBroker(), nil)
	rt.Register("awe", exe)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := rt.Health(); err != nil {
		t.Errorf("a partially failed AWE would 503 and crash-loop the pod: %v", err)
	}

	good.healthErr = fmt.Errorf("ctx canceled")
	if err := rt.Health(); err == nil {
		t.Error("an AWE serving nothing kept the runtime healthy")
	}
}

func TestExecutorStart_OneInstanceFails_OthersKeepRunning(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	bad := newMockInstance("bad", "agent")
	bad.startErr = fmt.Errorf("queue provisioning refused")
	c := newMockInstance("c", "agent")
	exe.AddInstance(a)
	exe.AddInstance(bad)
	exe.AddInstance(c)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("one failed instance must not fail the executor: %v", err)
	}

	if c.startCalled.Load() != 1 {
		t.Error("an instance declared after the failing one was never started")
	}
	if a.stopCalled.Load() != 0 {
		t.Error("an already-started instance was torn down by an unrelated failure")
	}
	if got := statusOf(t, exe, "a"); got != StatusRunning {
		t.Errorf("healthy instance status = %q, want %q", got, StatusRunning)
	}
	if got := statusOf(t, exe, "bad"); got != StatusError {
		t.Errorf("failed instance status = %q, want %q", got, StatusError)
	}
}

func TestExecutorHealth_NamesTheFailingPhase(t *testing.T) {
	exe := NewExecutor(newTestServices())
	healthy := newMockInstance("healthy", "agent")
	badStart := newMockInstance("bad_start", "agent")
	badStart.startErr = fmt.Errorf("queue provisioning refused")
	exe.AddInstance(healthy)
	exe.AddInstance(badStart)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	err := exe.Health()
	if err == nil {
		t.Fatal("a failed Start left Health silent")
	}
	if !strings.Contains(err.Error(), "queue provisioning refused") {
		t.Errorf("Health does not carry the underlying cause: %v", err)
	}
	if strings.Contains(err.Error(), "failed to initialize") {
		t.Errorf("a Start failure is reported as an Init failure: %v", err)
	}
}

func TestExecutorStop_SkipsInstancesThatNeverInitialized(t *testing.T) {
	exe := NewExecutor(newTestServices())
	bad := newMockInstance("bad", "workflow")
	bad.initErr = fmt.Errorf("bad output reference")
	healthy := newMockInstance("healthy", "agent")
	exe.AddInstance(bad)
	exe.AddInstance(healthy)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if bad.stopCalled.Load() != 0 {
		t.Error("Stop was called on an instance that never initialized")
	}
	if healthy.stopCalled.Load() != 1 {
		t.Error("the initialized instance was not stopped")
	}
}

func TestListAgentNames_ExcludesInstancesThatAreNotServing(t *testing.T) {
	exe := NewExecutor(newTestServices())
	serving := newMockInstance("serving", "agent")
	bad := newMockInstance("bad", "agent")
	bad.initErr = fmt.Errorf("bad model provider")
	exe.AddInstance(serving)
	exe.AddInstance(bad)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	names := exe.ListAgentNames()
	if len(names) != 1 || names[0] != "serving" {
		t.Errorf("ListAgentNames() = %v, want [serving]: the metric counts an agent that never came up", names)
	}
}

func TestExecutorStart_AllInstancesFail_StaysFatal(t *testing.T) {
	exe := NewExecutor(newTestServices())
	for _, name := range []string{"a", "b"} {
		bad := newMockInstance(name, "agent")
		bad.startErr = fmt.Errorf("queue provisioning refused")
		exe.AddInstance(bad)
	}

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err == nil {
		t.Fatal("every instance failed to start yet Start reported success, so the pod would report ready while serving nothing")
	}
}

func TestExecutorStart_AllInstancesDisabled_IsNotAFailure(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	exe.AddInstance(a)
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, mi := range exe.instances {
		mi.setEnabled(false)
	}

	if err := exe.Start(t.Context()); err != nil {
		t.Errorf("an all-disabled executor was treated as a startup failure: %v", err)
	}
}

func TestExecutorStop_StopsInstancesThatFailedToStart(t *testing.T) {
	exe := NewExecutor(newTestServices())
	badStart := newMockInstance("bad_start", "agent")
	badStart.startErr = fmt.Errorf("broker refused subscribe")
	healthy := newMockInstance("healthy", "agent")
	exe.AddInstance(badStart)
	exe.AddInstance(healthy)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := exe.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if badStart.stopCalled.Load() != 1 {
		t.Error("an instance that failed Start was never stopped, leaking whatever Init opened")
	}
}

func TestStartInstance_RefusesAnInstanceThatFailedInit(t *testing.T) {
	exe := NewExecutor(newTestServices())
	bad := newMockInstance("bad", "workflow")
	bad.initErr = fmt.Errorf("dependency cycle detected")
	healthy := newMockInstance("healthy", "agent")
	exe.AddInstance(bad)
	exe.AddInstance(healthy)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	err := exe.StartInstance(t.Context(), "bad")
	if err == nil {
		t.Fatal("StartInstance started an instance whose Init never completed")
	}
	if bad.startCalled.Load() != 0 {
		t.Error("Start was called on a never-initialized instance")
	}
	if !strings.Contains(err.Error(), "dependency cycle detected") {
		t.Errorf("refusal does not carry the original cause: %v", err)
	}
}

func TestExecutorHealth_NamesAStopFailurePhase(t *testing.T) {
	exe := NewExecutor(newTestServices())
	a := newMockInstance("a", "agent")
	a.stopErr = fmt.Errorf("broker unsubscribe refused")
	healthy := newMockInstance("healthy", "agent")
	exe.AddInstance(a)
	exe.AddInstance(healthy)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := exe.StopInstance(t.Context(), "a"); err == nil {
		t.Fatal("expected the stop to fail")
	}

	err := exe.Health()
	if err == nil {
		t.Fatal("an instance left in error by a failed stop is reported as fully healthy")
	}
	if strings.Contains(err.Error(), "no recorded cause") {
		t.Errorf("a failed StopInstance recorded no cause: %v", err)
	}
	if !strings.Contains(err.Error(), "broker unsubscribe refused") {
		t.Errorf("Health does not carry the stop failure cause: %v", err)
	}
}

func TestRemoveInstance_StopsAFailedStartInstanceBeforeDroppingIt(t *testing.T) {
	exe := NewExecutor(newTestServices())
	badStart := newMockInstance("bad_start", "workflow")
	badStart.startErr = fmt.Errorf("broker refused subscribe")
	healthy := newMockInstance("healthy", "agent")
	exe.AddInstance(badStart)
	exe.AddInstance(healthy)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := exe.removeInstance(t.Context(), "bad_start", removeForReplace); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if badStart.stopCalled.Load() != 1 {
		t.Error("a failed-start instance was dropped without being stopped, leaking what Init opened")
	}
}

func TestAddAndStart_StopsTheReplacedFailedInstance(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})
	badStart := newMockInstance("replaced", "agent")
	badStart.startErr = fmt.Errorf("broker refused subscribe")
	healthy := newMockInstance("healthy", "agent")
	exe.AddInstance(badStart)
	exe.AddInstance(healthy)

	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	cfg := config.FromMap(map[string]any{"kind": "agent", "name": "replaced"})
	if _, err := exe.AddAndStart(t.Context(), cfg); err != nil {
		t.Fatalf("add and start: %v", err)
	}

	if badStart.stopCalled.Load() != 1 {
		t.Error("the replaced failed instance was deleted without being stopped")
	}
}
