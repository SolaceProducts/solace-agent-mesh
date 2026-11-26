// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func staleCopy(t *testing.T, checkpoints bool, nodes ...*NodeConfig) (b, c *WorkflowInstance, recB, recC *approvalArgTool, taskID, requestID, executionID string) {
	t.Helper()
	a, _ := newHILWorkflow(t, nodes...)
	if checkpoints {
		a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
	}
	taskID = "task-stale-copy"
	requestID, executionID = parkedTask(t, a, taskID)

	b, recB, _ = newPeer(t, a)
	b.checkpointRepo = a.checkpointRepo
	var bAcks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, requestID, true, &bAcks, false))
	b.wg.Wait()
	if !recB.executed() || executionFor(b, taskID) == nil {
		t.Fatalf("b: tool ran=%v resident=%v, want the run continued on b", recB.executed(), executionFor(b, taskID) != nil)
	}

	c, recC, _ = newPeer(t, a)
	c.checkpointRepo = a.checkpointRepo
	var cAcks ackCounter
	c.handleWorkflowHILResponse(t.Context(), answerFromQueue(c, requestID, true, &cAcks, true))
	c.wg.Wait()
	if c.residentByID(executionID) == nil {
		t.Fatal("c did not retake the run")
	}
	return b, c, recB, recC, taskID, requestID, executionID
}

func TestFence_StaleCopyDoesNotAnswerTheCaller(t *testing.T) {
	for name, checkpoints := range map[string]bool{"checkpoints off": false, "checkpoints on": true} {
		t.Run(name, func(t *testing.T) {
			b, c, _, _, taskID, _, executionID := staleCopy(t, checkpoints, gatedCallNode(), afterNode())
			bReplies := subscribed(t, b.svc.Broker(), requestAckReplyTo)
			cReplies := subscribed(t, c.svc.Broker(), requestAckReplyTo)

			deliverSubTaskResult(t, b, subTaskOf(t, executionFor(b, taskID), "after"), a2a.TaskStateCompleted)
			b.wg.Wait()
			if got := receiveOn(t, b.svc.Broker(), bReplies); got != nil {
				t.Errorf("the stale copy answered the caller: %s", got.Payload)
			}
			if executionFor(b, taskID) != nil {
				t.Error("the stale copy is still in memory")
			}
			if run := runRowOf(t, c, executionID); run.Status != "held" {
				t.Fatalf("run = %s after the stale copy finished, want still held by the retaking copy", run.Status)
			}

			deliverSubTaskResult(t, c, subTaskOf(t, executionFor(c, taskID), "after"), a2a.TaskStateCompleted)
			c.wg.Wait()
			if got := finalState(t, c.svc.Broker(), cReplies); got != a2a.TaskStateCompleted {
				t.Errorf("final state from the retaking copy = %q, want completed", got)
			}
			if run := runRowOf(t, c, executionID); run.Status != "ended" || run.AnsweredAt == nil {
				t.Errorf("run = %s answered=%v, want ended by the retaking copy", run.Status, run.AnsweredAt != nil)
			}
		})
	}
}

func TestFence_StaleCopyDoesNotAskAgain(t *testing.T) {
	second := &NodeConfig{ID: "gate2", Type: "tool", ToolName: "gated_call", DependsOn: []string{"after"}}
	b, _, _, _, taskID, _, _ := staleCopy(t, false, gatedCallNode(), afterNode(), second)
	before := len(pausesOf(t, b, taskID))

	deliverSubTaskResult(t, b, subTaskOf(t, executionFor(b, taskID), "after"), a2a.TaskStateCompleted)
	b.wg.Wait()

	if got := len(pausesOf(t, b, taskID)); got != before {
		t.Errorf("pauses of the task = %d, want %d: the stale copy stored one", got, before)
	}
	if executionFor(b, taskID) != nil {
		t.Error("the stale copy is still in memory")
	}
}

func TestFence_StaleCopyDoesNotRunAnApprovedTool(t *testing.T) {
	second := &NodeConfig{ID: "gate2", Type: "tool", ToolName: "gated_call", DependsOn: []string{"after"}}
	sibling := &NodeConfig{ID: "other", Type: "agent", AgentName: "otherAgent", DependsOn: []string{"after"}}
	a, _ := newHILWorkflow(t, gatedCallNode(), afterNode(), second, sibling)
	const taskID = "task-stale-approval"
	firstRequest, executionID := parkedTask(t, a, taskID)
	b, recB, _ := newPeer(t, a)
	var bAcks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, firstRequest, true, &bAcks, false))
	b.wg.Wait()
	deliverSubTaskResult(t, b, subTaskOf(t, executionFor(b, taskID), "after"), a2a.TaskStateCompleted)
	b.wg.Wait()
	var gate2 *wfstore.PausedWorkflow
	for _, p := range pausesOf(t, b, taskID) {
		if p.RequestID != firstRequest {
			row, err := b.pausedRepo.Get(t.Context(), p.RequestID)
			if err != nil || row == nil {
				t.Fatalf("gate2 pause: %v", err)
			}
			gate2 = row
		}
	}
	if gate2 == nil {
		t.Fatal("b did not pause on the second approval")
	}
	calls := recB.calls()
	exec := executionFor(b, taskID)
	pause, claimed, err := b.runStore().ClaimHeld(t.Context(), gate2.RequestID, exec.Context.holderOf(), "resumed")
	if err != nil || !claimed {
		t.Fatalf("ClaimHeld = %v, %v", claimed, err)
	}

	c, recC, _ := newPeer(t, a)
	var cAcks ackCounter
	c.handleWorkflowHILResponse(t.Context(), answerFromQueue(c, firstRequest, true, &cAcks, true))
	c.wg.Wait()

	b.applyAnswer(t.Context(), exec, pause, a2a.UserInputResponseData{
		RequestID: gate2.RequestID, Status: "answered", Data: map[string]any{"approved": true},
	})
	b.wg.Wait()
	if recB.calls() != calls {
		t.Errorf("the stale copy ran the approved tool: %d calls, want %d", recB.calls(), calls)
	}
	if c.residentByID(executionID) == nil || recC.calls() == 0 {
		t.Error("the retaking copy is not running the run")
	}
}

func pausesOf(t *testing.T, w *WorkflowInstance, taskID string) []wfstore.TaskPause {
	t.Helper()
	pauses, err := w.pausedRepo.PausesForTask(t.Context(), taskID)
	if err != nil {
		t.Fatalf("pauses of the task: %v", err)
	}
	return pauses
}

func stalePair(t *testing.T, checkpoints bool) (b, c *WorkflowInstance, recB *approvalArgTool, g2, taskID, executionID string) {
	t.Helper()
	a, _ := newHILWorkflow(t, twoGatesNodes()...)
	if checkpoints {
		a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
	}
	taskID = "task-stale-pair"
	var reqAcks ackCounter
	req, msg := deliveredRequest(taskID, &reqAcks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	g1 := pauseOfNode(t, a, taskID, "g1")
	g2, executionID = pauseOfNode(t, a, taskID, "g2").RequestID, g1.ExecutionID

	b, recB, _ = newPeer(t, a)
	b.checkpointRepo = a.checkpointRepo
	var bAcks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, g1.RequestID, true, &bAcks, false))
	b.wg.Wait()
	if executionFor(b, taskID) == nil || recB.calls() != 1 {
		t.Fatalf("b: resident=%v tool calls=%d, want the run continued on b", executionFor(b, taskID) != nil, recB.calls())
	}
	c, _, _ = newPeer(t, a)
	c.checkpointRepo = a.checkpointRepo
	var cAcks ackCounter
	c.handleWorkflowHILResponse(t.Context(), answerFromQueue(c, g1.RequestID, true, &cAcks, true))
	c.wg.Wait()
	if c.residentByID(executionID) == nil {
		t.Fatal("c did not retake the run")
	}
	return b, c, recB, g2, taskID, executionID
}

func assertCopyStopped(t *testing.T, stale, holder *WorkflowInstance, replies broker.Queue, taskID, executionID, holderKey string) {
	t.Helper()
	if got := receiveOn(t, stale.svc.Broker(), replies); got != nil {
		t.Errorf("the stale copy answered the caller: %s", got.Payload)
	}
	if executionFor(stale, taskID) != nil {
		t.Error("the stale copy is still in memory")
	}
	if run := runRowOf(t, holder, executionID); run.Status != "held" || run.HeldBy != holderKey {
		t.Errorf("run = %s held by %q, want still held by %q", run.Status, run.HeldBy, holderKey)
	}
}

func TestFence_StaleCopyStopsAtItsPark(t *testing.T) {
	b, c, _, _, taskID, executionID := stalePair(t, false)
	replies := subscribed(t, b.svc.Broker(), requestAckReplyTo)
	holderKey := runRowOf(t, c, executionID).HeldBy

	deliverSubTaskResult(t, b, subTaskOf(t, executionFor(b, taskID), "after"), a2a.TaskStateCompleted)
	b.wg.Wait()

	assertCopyStopped(t, b, c, replies, taskID, executionID, holderKey)
}

func TestFence_StaleCopyDoesNotApplyAnAnswerFromTheQueue(t *testing.T) {
	b, c, recB, g2, taskID, executionID := stalePair(t, false)
	replies := subscribed(t, b.svc.Broker(), requestAckReplyTo)
	holderKey := runRowOf(t, c, executionID).HeldBy
	calls := recB.calls()

	var acks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, g2, true, &acks, false))
	b.wg.Wait()

	if recB.calls() != calls {
		t.Errorf("the stale copy ran the approved tool: %d calls, want %d", recB.calls(), calls)
	}
	if got := pauseStatus(t, c, g2); got != "answered" {
		t.Errorf("g2 = %s, want answered for the copy holding the run", got)
	}
	assertCopyStopped(t, b, c, replies, taskID, executionID, holderKey)
}

func TestFence_StaleCopyStopsAtItsStepWrite(t *testing.T) {
	next := &NodeConfig{ID: "next", Type: "agent", AgentName: "nextAgent", DependsOn: []string{"after"}}
	b, c, _, _, taskID, _, executionID := staleCopy(t, true, gatedCallNode(), afterNode(), next)
	replies := subscribed(t, b.svc.Broker(), requestAckReplyTo)
	holderKey := runRowOf(t, c, executionID).HeldBy

	deliverSubTaskResult(t, b, subTaskOf(t, executionFor(b, taskID), "after"), a2a.TaskStateCompleted)
	b.wg.Wait()

	assertCopyStopped(t, b, c, replies, taskID, executionID, holderKey)
}

func TestFence_StaleRequestCopyStopsAtItsStepWrite(t *testing.T) {
	steps := []*NodeConfig{
		{ID: "s1", Type: "agent", AgentName: "s1Agent"},
		{ID: "s2", Type: "agent", AgentName: "s2Agent", DependsOn: []string{"s1"}},
		{ID: "s3", Type: "agent", AgentName: "s3Agent", DependsOn: []string{"s2"}},
	}
	a, _ := newHILWorkflow(t, steps...)
	a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
	replies := subscribed(t, a.svc.Broker(), requestAckReplyTo)
	const taskID = "task-stale-request"
	var aAcks ackCounter
	req, msg := deliveredRequest(taskID, &aAcks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	deliverSubTaskResult(t, a, subTaskOf(t, executionFor(a, taskID), "s1"), a2a.TaskStateCompleted)
	a.wg.Wait()
	executionID := executionFor(a, taskID).State.ExecutionID

	c, _, _ := newPeer(t, a)
	c.checkpointRepo = a.checkpointRepo
	var cAcks ackCounter
	req, msg = deliveredRequest(taskID, &cAcks, true)
	c.handleTaskRequest(t.Context(), *req, msg)
	c.wg.Wait()
	if c.residentByID(executionID) == nil {
		t.Fatal("c did not continue the run from its last step")
	}

	deliverSubTaskResult(t, a, subTaskOf(t, executionFor(a, taskID), "s2"), a2a.TaskStateCompleted)
	a.wg.Wait()

	assertCopyStopped(t, a, c, replies, taskID, executionID, wfstore.HeldByRequest)
	if aAcks.acks() != 0 {
		t.Errorf("the stale copy acknowledged the request %d times, want 0", aAcks.acks())
	}
}

func TestFence_RefusedFinalMarkStopsTheCopy(t *testing.T) {
	b, _, _, _, taskID, _, _ := staleCopy(t, false, gatedCallNode(), afterNode())
	e := executionFor(b, taskID).Executor
	e.mu.Lock()
	e.commitResponseLocked()
	lost := e.lost
	e.mu.Unlock()
	if !lost {
		t.Error("the copy whose mark was refused still runs")
	}
}

func TestRun_RefusedParkRetryAcknowledgesTheRequest(t *testing.T) {
	for name, unsure := range map[string]bool{"after a failed park": true, "with no failed park": false} {
		t.Run(name, func(t *testing.T) {
			a, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
			taskID := "task-park-retry-" + strings.ReplaceAll(name, " ", "-")
			var acks ackCounter
			req, msg := deliveredRequest(taskID, &acks, false)
			a.handleTaskRequest(t.Context(), *req, msg)
			a.wg.Wait()
			exec := executionFor(a, taskID)
			executionID := exec.State.ExecutionID
			if _, _, err := a.runStore().Retake(t.Context(), executionID, wfstore.HeldByRequest, statusClosedForRestart); err != nil {
				t.Fatalf("Retake: %v", err)
			}
			exec.Executor.mu.Lock()
			exec.Executor.parkUnsure = unsure
			exec.Executor.mu.Unlock()

			deliverSubTaskResult(t, a, subTaskOf(t, exec, "other"), a2a.TaskStateCompleted)
			a.wg.Wait()

			want := 0
			if unsure {
				want = 1
			}
			if acks.acks() != want {
				t.Errorf("request acks = %d, want %d", acks.acks(), want)
			}
			if executionFor(a, taskID) != nil {
				t.Error("the copy that lost the run is still in memory")
			}
		})
	}
}

func TestFence_CopyWhoseFirstWriteLostItsReplyIsFencedAfterARetake(t *testing.T) {
	a, _ := newHILWorkflow(t, &NodeConfig{ID: "s1", Type: "agent", AgentName: "s1Agent"})
	a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
	replies := subscribed(t, a.svc.Broker(), requestAckReplyTo)
	const taskID = "task-epoch-zero"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	exec := executionFor(a, taskID)
	executionID := exec.State.ExecutionID
	if run := runRowOf(t, a, executionID); run.HolderEpoch != 1 {
		t.Fatalf("run epoch = %d, want the first write at 1", run.HolderEpoch)
	}
	exec.Context.setHolder(wfstore.Holder{Key: wfstore.HeldByRequest})
	if _, _, err := a.runStore().Retake(t.Context(), executionID, wfstore.HeldByRequest, statusClosedForRestart); err != nil {
		t.Fatalf("Retake: %v", err)
	}

	deliverSubTaskResult(t, a, subTaskOf(t, exec, "s1"), a2a.TaskStateCompleted)
	a.wg.Wait()

	if got := receiveOn(t, a.svc.Broker(), replies); got != nil {
		t.Errorf("the copy at epoch 0 answered the caller after the retake: %s", got.Payload)
	}
	if run := runRowOf(t, a, executionID); run.Status != "held" || run.HolderEpoch != 2 {
		t.Errorf("run = %s at epoch %d, want still held at 2", run.Status, run.HolderEpoch)
	}
}
