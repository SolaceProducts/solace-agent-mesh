// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func newControlTestExecutor(t *testing.T, brk broker.Broker, authToken string) *DAGExecutor {
	t.Helper()
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns"}
	svc := &stubServices{brk: brk}
	w := &WorkflowInstance{cfg: cfg, svc: svc, logger: slog.Default()}
	execCtx := NewExecutionContext(context.Background(), "task-t", "ctx-t", nil,
		a2a.UserProperties{UserID: "u1", AuthToken: authToken}, false)
	t.Cleanup(execCtx.Cancel)
	return &DAGExecutor{cfg: cfg, state: NewExecutionState("wf", "exec-1"), execCtx: execCtx, instance: w, logger: slog.Default()}
}

func TestBuildControlQuerier_WithBroker(t *testing.T) {
	e := newControlTestExecutor(t, broker.NewDevBroker(), "jwt-abc")
	if e.buildControlQuerier() == nil {
		t.Fatal("expected a non-nil ControlQuerier when a broker is available")
	}
}

func TestBuildControlQuerier_NoBroker(t *testing.T) {
	e := newControlTestExecutor(t, nil, "")
	if e.buildControlQuerier() != nil {
		t.Fatal("expected a nil ControlQuerier when no broker is configured")
	}
}

func TestBuildToolContext_WiresControlQuerier(t *testing.T) {
	e := newControlTestExecutor(t, broker.NewDevBroker(), "jwt-abc")
	tc := e.buildToolContext(&NodeConfig{ID: "n1", Type: "tool", ToolName: "query_task_health"}, NodeCompletion{NodeID: "n1", SubTaskID: "sub-1"})
	if tc.ControlQuerier == nil {
		t.Fatal("buildToolContext must wire ControlQuerier so control-plane builtin tools work from a tool node")
	}
}
