// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

var (
	_ ProcessStopper = (*agent.Agent)(nil)
	_ ProcessStopper = (*workflow.WorkflowInstance)(nil)
)

type processStopperInstance struct {
	*mockInstance
	processExitStopCalled atomic.Int32
}

func (p *processStopperInstance) StopForProcessExit(_ context.Context) error {
	p.processExitStopCalled.Add(1)
	return nil
}

func TestExecutorStopUsesProcessExitStop(t *testing.T) {
	exe := NewExecutor(newTestServices())
	inst := &processStopperInstance{mockInstance: newMockInstance("a", "agent")}
	exe.AddInstance(inst)

	if err := exe.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	if inst.processExitStopCalled.Load() != 1 {
		t.Error("process shutdown must stop a ProcessStopper through StopForProcessExit")
	}
	if inst.stopCalled.Load() != 0 {
		t.Error("process shutdown must not use the control-plane Stop of a ProcessStopper")
	}
}

func TestRemoveInstanceKeepsControlPlaneStop(t *testing.T) {
	exe := NewExecutor(newTestServices())
	inst := &processStopperInstance{mockInstance: newMockInstance("a", "agent")}
	exe.AddInstance(inst)
	if err := exe.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := exe.RemoveInstance(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}

	if inst.stopCalled.Load() != 1 {
		t.Error("a delete RPC must stop through the bounded control-plane Stop")
	}
	if inst.processExitStopCalled.Load() != 0 {
		t.Error("a delete RPC must not use the process-exit stop")
	}
}
