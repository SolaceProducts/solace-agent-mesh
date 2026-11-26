// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func (w *WorkflowInstance) answer(ctx context.Context, requestID, _, nodeStatus string, data map[string]any) {
	payload, err := json.Marshal(a2a.UserInputResponseData{
		Type:      string(a2a.SignalUserInputResponse),
		RequestID: requestID,
		Status:    nodeStatus,
		Data:      data,
	})
	if err != nil {
		panic(err)
	}
	w.handleWorkflowHILResponse(ctx, &broker.Message{
		Topic:   a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID),
		Payload: payload,
	})
}

func runRowOf(t *testing.T, w *WorkflowInstance, executionID string) *wfstore.WorkflowRun {
	t.Helper()
	run, err := w.runStore().Get(t.Context(), executionID)
	if err != nil {
		t.Fatalf("read run row: %v", err)
	}
	if run == nil {
		t.Fatalf("no run row for execution %s", executionID)
	}
	return run
}

func rewriteParkedState(t *testing.T, w *WorkflowInstance, executionID string, edit func(*WorkflowExecutionState)) {
	t.Helper()
	var state WorkflowExecutionState
	if err := json.Unmarshal([]byte(runRowOf(t, w, executionID).StateSnapshot), &state); err != nil {
		t.Fatalf("decode parked state: %v", err)
	}
	edit(&state)
	b, err := json.Marshal(&state)
	if err != nil {
		t.Fatalf("encode parked state: %v", err)
	}
	if _, err := w.pausedDB.ExecContext(t.Context(),
		`UPDATE workflow_checkpoint SET state_snapshot = ? WHERE execution_id = ?`, string(b), executionID); err != nil {
		t.Fatalf("rewrite parked state: %v", err)
	}
}

func cancelTask(t *testing.T, w *WorkflowInstance, taskID string) {
	t.Helper()
	w.handleCancelRequest(t.Context(), a2a.Request{Method: "tasks/cancel", Params: map[string]any{"id": taskID}}, &broker.Message{})
	w.wg.Wait()
}

func parkAndRebuild(t *testing.T, w *WorkflowInstance, e *DAGExecutor, edit func(*wfstore.WorkflowRun)) *WorkflowExecution {
	t.Helper()
	e.mu.Lock()
	parked := e.writeParkLocked()
	e.mu.Unlock()
	if !parked {
		t.Fatal("the paused executor did not park")
	}
	run := runRowOf(t, w, e.state.ExecutionID)
	if edit != nil {
		edit(run)
	}
	exec, err := w.rebuildFromRun(run, nil, "")
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	return exec
}

func deliverDeadline(t *testing.T, w *WorkflowInstance, executionID string) {
	t.Helper()
	w.handleDeadline(t.Context(), &broker.Message{Topic: a2a.WorkflowDeadlineTopic(w.cfg.Namespace, w.cfg.Name, executionID)})
	w.wg.Wait()
}
