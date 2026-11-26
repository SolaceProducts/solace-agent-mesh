// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"testing"
	"time"
)

func TestApprovalPause_PersistsIterationRouting(t *testing.T) {
	e, _ := newLivePauseExecutor(t)
	e.state.PendingNodes["each"] = true

	e.invokeToolNode(gatedCallNode(), map[string]any{"url": "https://example.test"},
		NodeCompletion{NodeID: "each", SubTaskID: "sub-1", MapNodeID: "each", ItemIndex: 2})
	e.instance.wg.Wait()

	rows, err := e.instance.pausedRepo.Expired(t.Context(), e.instance.cfg.Name, time.Now().Add(24*time.Hour).UnixMilli(), 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("want one paused row, got %d (%v)", len(rows), err)
	}
	var routing persistedRouting
	if err := json.Unmarshal([]byte(rows[0].Routing), &routing); err != nil {
		t.Fatalf("routing: %v", err)
	}
	if routing.ToolNodeID != "call_gated" {
		t.Errorf("ToolNodeID = %q; an approval would dispatch the wrong node", routing.ToolNodeID)
	}
	if routing.MapNodeID != "each" || routing.ItemIndex != 2 {
		t.Errorf("iteration routing lost: %+v — the resume would complete the wrong branch", routing)
	}
}

func TestExecutionNoLongerLive_EachEndedState(t *testing.T) {
	for _, tc := range []struct {
		name string
		mark func(*WorkflowExecutionState)
		want string
	}{
		{"finalized", func(s *WorkflowExecutionState) { s.Finalized = true }, "finalized"},
		{"cancelled", func(s *WorkflowExecutionState) { s.Cancelled = true }, "cancelled"},
		{"past deadline", func(s *WorkflowExecutionState) {
			s.WorkflowDeadline = time.Now().Add(-time.Minute)
		}, "deadline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newLivePauseExecutor(t)
			exec := &WorkflowExecution{State: e.state, Context: e.execCtx, Executor: e}

			if _, dead := executionNoLongerLive(exec); dead {
				t.Fatal("precondition: a fresh run must read as live")
			}
			tc.mark(e.state)
			reason, dead := executionNoLongerLive(exec)
			if !dead {
				t.Fatalf("%s did not read as ended; a late approval would dispatch", tc.name)
			}
			if reason == "" {
				t.Error("the refusal must say why, or the operator sees an unexplained failure")
			}
		})
	}
}

func TestHandleApproved_SkipsANodeThatIsNoLongerPending(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	delete(e.state.PendingNodes, "call_gated")

	e.handleApproved(&EventNodeApproved{
		NodeID: "call_gated", ToolNodeID: "call_gated", SubTaskID: "sub-1",
		ToolName: "gated_call", Args: map[string]any{"url": "https://example.test"},
	})
	e.instance.wg.Wait()

	if rec.executed() {
		t.Error("dispatched an approved tool for a node a cancel had already resolved")
	}
}

func TestHandleApproved_ReArmsTheNodeTimer(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	e.cfg.Nodes[0].Timeout = 30 * time.Second

	release := make(chan struct{})
	rec.block = release

	e.handleApproved(&EventNodeApproved{
		NodeID: "call_gated", ToolNodeID: "call_gated", SubTaskID: "sub-1",
		ToolName: "gated_call", Args: map[string]any{"url": "https://example.test"},
	})
	rec.waitUntilRunning(t)

	armed := e.state.RemoveTimerBySubTask(TimerKindNodeTimeout, "sub-1")
	close(release)
	e.instance.wg.Wait()

	if !armed {
		t.Error("no node timer after an approved dispatch; a hung approved call would never time out")
	}
}
