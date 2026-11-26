// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newAgentLossCore(t *testing.T) (*GatewayCore, *capturingAdapter) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := &capturingAdapter{LoggingAdapter: *NewLoggingAdapter(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	core := &GatewayCore{
		adapter:    adapter,
		agents:     NewAgentRegistry(),
		tasks:      NewMemoryTaskRegistry(),
		taskTimers: newTaskTimerRegistry(),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg: &GatewayConfig{
			Namespace:          "test-ns",
			GatewayID:          "test-gw",
			TaskTimeoutSeconds: 300,
		},
	}
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())
	core.eventBindActive.Store(true)
	return core, adapter
}

func waitForTaskRemoved(t *testing.T, core *GatewayCore, taskID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !core.IsTaskRunning(taskID) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Errorf("task %q should be removed from registry after its terminal error", taskID)
}

func TestUndeployedAgentEndsItsTask(t *testing.T) {
	tests := []struct {
		name    string
		state   control.TargetState
		wantEnd bool
	}{
		{name: "not deployed", state: control.TargetNotDeployed, wantEnd: true},
		{name: "deployed", state: control.TargetDeployed},
		{name: "unknown", state: control.TargetUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, adapter := newAgentLossCore(t)
			const taskID = "task-on-gone-agent"
			_ = core.tasks.Register(taskID, "agent-gone", "user-1", TaskIdentity{})
			core.SetTargetLookup(func(context.Context, string) control.Target {
				return control.Target{State: tc.state, CardName: "agent-gone"}
			})

			core.endTasksOfUndeployedAgents(t.Context(), []heldTask{{id: taskID}})

			if !tc.wantEnd {
				if len(adapter.Errors()) != 0 || !core.IsTaskRunning(taskID) {
					t.Fatalf("task ended for an agent that is %s", tc.name)
				}
				return
			}
			errs := adapter.Errors()
			if len(errs) != 1 || errs[0].err == nil {
				t.Fatalf("errors = %+v, want one terminal error", errs)
			}
			if errs[0].err.Code != TaskAgentNotAvailableErrorCode || errs[0].err.Message != AgentNotAvailableText {
				t.Errorf("error = %+v, want code %d with the not-available text", errs[0].err, TaskAgentNotAvailableErrorCode)
			}
			waitForTaskRemoved(t, core, taskID)
		})
	}
}

func TestAllByAgent(t *testing.T) {
	r := NewMemoryTaskRegistry()

	_ = r.Register("task-a1", "agent-alpha", "user-1", TaskIdentity{})
	_ = r.Register("task-a2", "agent-alpha", "user-1", TaskIdentity{})
	_ = r.Register("task-b1", "agent-beta", "user-2", TaskIdentity{})

	got := r.AllByAgent("agent-alpha")
	if len(got) != 2 {
		t.Fatalf("AllByAgent(agent-alpha) = %v, want 2 entries", got)
	}
	seen := make(map[string]bool)
	for _, id := range got {
		seen[id] = true
	}
	if !seen["task-a1"] || !seen["task-a2"] {
		t.Errorf("AllByAgent missing expected tasks; got %v", got)
	}

	betaTasks := r.AllByAgent("agent-beta")
	if len(betaTasks) != 1 || betaTasks[0] != "task-b1" {
		t.Errorf("AllByAgent(agent-beta) = %v, want [task-b1]", betaTasks)
	}

	none := r.AllByAgent("agent-unknown")
	if len(none) != 0 {
		t.Errorf("AllByAgent(unknown) = %v, want empty", none)
	}
}

var _ TaskRegistry = (*memoryTaskRegistry)(nil)
