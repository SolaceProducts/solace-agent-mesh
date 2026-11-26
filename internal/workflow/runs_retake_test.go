// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func twoGatesNodes() []*NodeConfig {
	return []*NodeConfig{
		{ID: "g1", Type: "tool", ToolName: "gated_call"},
		{ID: "g2", Type: "tool", ToolName: "gated_call"},
		{ID: "after", Type: "agent", AgentName: "afterAgent", DependsOn: []string{"g1"}},
	}
}

func pauseOfNode(t *testing.T, w *WorkflowInstance, taskID, nodeID string) *wfstore.PausedWorkflow {
	t.Helper()
	pauses, err := w.pausedRepo.PausesForTask(t.Context(), taskID)
	if err != nil {
		t.Fatalf("pauses of the task: %v", err)
	}
	for _, p := range pauses {
		row, err := w.pausedRepo.Get(t.Context(), p.RequestID)
		if err != nil || row == nil {
			t.Fatalf("pause %s: %v", p.RequestID, err)
		}
		if row.NodeID == nodeID {
			return row
		}
	}
	t.Fatalf("no pause for node %q of task %s", nodeID, taskID)
	return nil
}

func pauseStatus(t *testing.T, w *WorkflowInstance, requestID string) string {
	t.Helper()
	row, err := w.pausedRepo.Get(t.Context(), requestID)
	if err != nil || row == nil {
		t.Fatalf("pause %s: %v", requestID, err)
	}
	return row.Status
}

func TestRun_AnswerTheDeadHolderAppliedIsAppliedAfterTheRetake(t *testing.T) {
	for name, checkpoints := range map[string]bool{"checkpoints off": false, "checkpoints on": true} {
		t.Run(name, func(t *testing.T) {
			a, _ := newHILWorkflow(t, twoGatesNodes()...)
			if checkpoints {
				a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
			}
			const taskID = "task-two-gates-retake"
			var reqAcks ackCounter
			req, msg := deliveredRequest(taskID, &reqAcks, false)
			a.handleTaskRequest(t.Context(), *req, msg)
			a.wg.Wait()
			g1, g2 := pauseOfNode(t, a, taskID, "g1"), pauseOfNode(t, a, taskID, "g2")
			executionID := g1.ExecutionID
			if run := runRowOf(t, a, executionID); run.Status != "parked" {
				t.Fatalf("run = %s, want parked on both approvals", run.Status)
			}

			b, recB, _ := newPeer(t, a)
			b.checkpointRepo = a.checkpointRepo
			var g1Acks, g2Acks ackCounter
			b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, g1.RequestID, true, &g1Acks, false))
			b.wg.Wait()
			b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, g2.RequestID, true, &g2Acks, false))
			b.wg.Wait()
			if recB.calls() != 2 || pauseStatus(t, b, g2.RequestID) != "resumed" {
				t.Fatalf("on b: %d tool calls, g2 %s; want both approvals applied", recB.calls(), pauseStatus(t, b, g2.RequestID))
			}

			c, recC, brkC := newPeer(t, a)
			c.checkpointRepo = a.checkpointRepo
			replies := subscribed(t, brkC, requestAckReplyTo)
			var cAcks ackCounter
			c.handleWorkflowHILResponse(t.Context(), answerFromQueue(c, g1.RequestID, true, &cAcks, true))
			c.wg.Wait()
			exec := c.residentByID(executionID)
			if exec == nil {
				t.Fatal("the run is not executing on the AWE the answer was redelivered to")
			}
			deliverSubTaskResult(t, c, subTaskOf(t, exec, "after"), a2a.TaskStateCompleted)
			c.wg.Wait()

			if got := finalState(t, brkC, replies); got != a2a.TaskStateCompleted {
				t.Fatalf("final state = %q, want completed", got)
			}
			wantCalls := 2
			if checkpoints {
				wantCalls = 0
			}
			if recC.calls() != wantCalls {
				t.Errorf("tool calls on c = %d, want %d", recC.calls(), wantCalls)
			}
			if got := pauseStatus(t, c, g2.RequestID); got != "resumed" {
				t.Errorf("g2 = %s, want resumed with the answer already given", got)
			}
			waitForCondition(t, 2*time.Second, func() bool { return cAcks.acks() == 1 },
				"the redelivered answer was not acknowledged once the run ended")
			if run := runRowOf(t, c, executionID); run.Status != "ended" {
				t.Errorf("run = %s, want ended", run.Status)
			}
		})
	}
}
