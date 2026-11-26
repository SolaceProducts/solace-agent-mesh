// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const (
	PauseKindSignal = "signal"

	wakeHolderPrefix = "wake:"

	signalTimeoutRetryDelay = 30 * time.Second
	dueWakeScanMargin       = 5 * time.Second
	dueWakeSweepPages       = 16
)

type EventWake struct {
	NodeID string
}

func (*EventWake) isWorkflowEvent() {
}

type EventSignal struct {
	RequestID string
	NodeID    string
	Status    string
	Data      map[string]any
}

func (*EventSignal) isWorkflowEvent() {
}

func wakeHolder(executionID, messageID string) string {
	return wakeHolderPrefix + executionID + ":" + messageID
}

func (s *WorkflowExecutionState) hasWakeTimers() bool {
	return slices.ContainsFunc(s.Timers, func(t Timer) bool { return t.Kind == TimerKindWake })
}

func (s *WorkflowExecutionState) hasWakeTimer(nodeID string) bool {
	return slices.ContainsFunc(s.Timers, func(t Timer) bool { return t.Kind == TimerKindWake && t.NodeID == nodeID })
}

func (s *WorkflowExecutionState) nextWakeAt() *int64 {
	var next *int64
	for _, t := range s.Timers {
		if t.Kind != TimerKindWake {
			continue
		}
		at := t.FiresAt.UnixMilli()
		if next == nil || at < *next {
			next = &at
		}
	}
	return next
}

func (e *DAGExecutor) outcomeDecided() bool {
	return e.state.PendingOutcome != "" || e.state.Finalized
}

func (e *DAGExecutor) dropWakeTimers() {
	e.state.Timers = slices.DeleteFunc(e.state.Timers, func(t Timer) bool { return t.Kind == TimerKindWake })
}

func (e *DAGExecutor) skipDurableWaitAfterOutcome(node *NodeConfig) bool {
	if !e.outcomeDecided() {
		return false
	}
	e.logger.Debug("workflow: not starting a timer or wait node; the run's outcome is already decided",
		"node", node.ID, "executionID", e.state.ExecutionID, "outcome", e.state.PendingOutcome)
	return true
}

func (e *DAGExecutor) dispatchTimerNode(node *NodeConfig) {
	if e.skipDurableWaitAfterOutcome(node) {
		return
	}
	e.state.AddPending(node.ID)
	e.state.HasPausedRows = true
	e.state.RemoveTimer(TimerKindWake, node.ID)
	e.state.AddTimer(Timer{
		Kind:     TimerKindWake,
		NodeID:   node.ID,
		FiresAt:  time.Now().Add(node.WaitDuration),
		Duration: node.WaitDuration,
	})
	e.logger.Info("workflow timer node started",
		"node", node.ID, "executionID", e.state.ExecutionID, "duration", node.WaitDuration)
}

func (e *DAGExecutor) dispatchWaitNode(ctx context.Context, node *NodeConfig) {
	if e.skipDurableWaitAfterOutcome(node) {
		return
	}
	e.state.AddPending(node.ID)
	e.state.RemoveTimer(TimerKindWake, node.ID)
	if e.evalRun() {
		e.handleNodeError(node.ID, fmt.Errorf("wait node %q waits for signal %q: %w", node.ID, node.Signal, errNoHumanInEvalRun))
		return
	}
	if node.Timeout > 0 {
		e.state.AddTimer(Timer{
			Kind:     TimerKindWake,
			NodeID:   node.ID,
			FiresAt:  time.Now().Add(node.Timeout),
			Duration: node.Timeout,
		})
	}
	alreadyAnswered, err := e.pauseForSignal(ctx, node)
	if errors.Is(err, errRunLost) {
		return
	}
	if err != nil {
		e.state.RemoveTimer(TimerKindWake, node.ID)
		e.handleNodeError(node.ID, fmt.Errorf("wait node %q could not start waiting for signal %q: %w", node.ID, node.Signal, err))
		return
	}
	if alreadyAnswered != nil {
		e.applySignalAcceptedBeforeRestore(node, alreadyAnswered)
		return
	}
	e.logger.Info("workflow wait node waiting for a signal",
		"node", node.ID, "executionID", e.state.ExecutionID, "signal", node.Signal)
}

func (e *DAGExecutor) signalRequestID(node *NodeConfig) string {
	return a2a.WorkflowSignalRequestID(e.execCtx.TaskID, node.Signal)
}

func (e *DAGExecutor) pauseForSignal(ctx context.Context, node *NodeConfig) (*wfstore.PausedWorkflow, error) {
	w := e.instance
	if w == nil || e.execCtx == nil {
		return nil, fmt.Errorf("the run has no workflow store")
	}
	runs := w.runStore()
	if runs == nil {
		return nil, fmt.Errorf("the workflow store is not available; the instance is stopping")
	}
	ctx, cancel := context.WithTimeout(ctx, workflowHILPauseTimeout)
	defer cancel()
	e.state.HasPausedRows = true
	ecJSON, err := w.runExecContext(e.execCtx)
	if err != nil {
		return nil, fmt.Errorf("marshal exec context: %w", err)
	}
	routingJSON, err := json.Marshal(persistedRouting{NodeID: node.ID, Kind: PauseKindSignal})
	if err != nil {
		return nil, fmt.Errorf("marshal routing: %w", err)
	}
	requestID := e.signalRequestID(node)
	holder, alreadyAnswered, err := runs.InsertSignalPause(ctx, wfstore.PausedWorkflow{
		RequestID:       requestID,
		ExecutionID:     e.state.ExecutionID,
		TaskID:          e.execCtx.TaskID,
		WorkflowName:    w.cfg.Name,
		NodeID:          node.ID,
		RequesterAddr:   a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID),
		RecipientUserID: e.execCtx.UserProps.UserID,
		ExecContext:     ecJSON,
		Routing:         string(routingJSON),
	}, e.execCtx.holderOf())
	if errors.Is(err, wfstore.ErrNotHolder) {
		e.loseRunLocked("store a signal wait")
		return nil, errRunLost
	}
	if err != nil {
		return nil, fmt.Errorf("persist signal wait: %w", err)
	}
	e.execCtx.setHolder(holder)
	if alreadyAnswered != nil {
		return alreadyAnswered, nil
	}
	e.recordParkedPause(requestID, parkedStepOf(node.ID, "", "", ""))
	return nil, nil
}

func (e *DAGExecutor) applySignalAcceptedBeforeRestore(node *NodeConfig, pause *wfstore.PausedWorkflow) {
	var answer a2a.UserInputResponseData
	if err := json.Unmarshal([]byte(*pause.Answer), &answer); err != nil {
		e.state.RemoveTimer(TimerKindWake, node.ID)
		e.handleNodeError(node.ID, fmt.Errorf("wait node %q: the signal it accepted before the run was restored is unreadable: %w", node.ID, err))
		return
	}
	e.logger.Info("workflow wait node: applying the signal it accepted before the run was restored",
		"node", node.ID, "executionID", e.state.ExecutionID, "requestID", pause.RequestID)
	e.handleSignal(&EventSignal{RequestID: pause.RequestID, NodeID: node.ID, Status: answer.Status, Data: answer.Data})
}

func (e *DAGExecutor) handleWake(ctx context.Context) {
	now := time.Now()
	for _, t := range slices.Clone(e.state.Timers) {
		if e.outcomeDecided() {
			e.dropWakeTimers()
			return
		}
		if t.Kind != TimerKindWake || t.FiresAt.After(now) {
			continue
		}
		node := e.dag.Nodes[t.NodeID]
		switch {
		case !e.state.IsNodePending(t.NodeID):
			e.state.RemoveTimer(TimerKindWake, t.NodeID)
		case !isDurableWaitNode(node):
			e.failForRemovedWaitNode(t.NodeID, "a timer or wait node")
			return
		case node.Type == NodeTypeTimer:
			e.state.RemoveTimer(TimerKindWake, t.NodeID)
			e.logger.Info("workflow timer node fired", "node", t.NodeID, "executionID", e.state.ExecutionID,
				"lateByMs", now.Sub(t.FiresAt).Milliseconds())
			e.handleCompletion(NodeCompletion{
				NodeID: t.NodeID,
				Output: map[string]any{
					"fired_at":      now.UTC().Format(time.RFC3339Nano),
					"scheduled_for": t.FiresAt.UTC().Format(time.RFC3339Nano),
				},
			})
		default:
			e.timeOutSignalWait(ctx, node, t)
		}
	}
}

func (e *DAGExecutor) failForRemovedWaitNode(nodeID, wantType string) {
	e.state.RemoveTimer(TimerKindWake, nodeID)
	e.logger.Warn("workflow: a parked run waits on a node its redeployed definition no longer has as the type it parked on; failing the run",
		"node", nodeID, "executionID", e.state.ExecutionID, "wantType", wantType)
	e.finalizeFailure(fmt.Sprintf("node %q, which the run was waiting on, is no longer %s in the workflow definition", nodeID, wantType))
}

func (e *DAGExecutor) timeOutSignalWait(ctx context.Context, node *NodeConfig, t Timer) {
	e.state.RemoveTimer(TimerKindWake, node.ID)
	requestID, open := e.openSignalRequestID(node)
	if !open {
		e.failWaitWithoutOpenPause(node)
		return
	}
	t.FiresAt = time.Now().Add(signalTimeoutRetryDelay)
	e.state.AddTimer(t)
	if err := e.instance.publishSignalAnswer(ctx, requestID, pauseTimedOut); err != nil {
		e.logger.Warn("workflow wait node: could not publish its timeout; trying again",
			"node", node.ID, "executionID", e.state.ExecutionID, "retryIn", signalTimeoutRetryDelay, "error", err)
		return
	}
	e.logger.Info("workflow wait node timed out; the timeout is applied unless a signal arrived first",
		"node", node.ID, "executionID", e.state.ExecutionID, "signal", node.Signal)
}

func (e *DAGExecutor) openSignalRequestID(node *NodeConfig) (string, bool) {
	signalPrefix := a2a.WorkflowSignalRequestID(e.execCtx.TaskID, "")
	for requestID, step := range e.parkedPauses {
		if step.nodeID == node.ID && strings.HasPrefix(requestID, signalPrefix) {
			return requestID, true
		}
	}
	return "", false
}

func (e *DAGExecutor) failWaitWithoutOpenPause(node *NodeConfig) {
	e.handleNodeError(node.ID, fmt.Errorf("wait node %q was due with no open pause for signal %q", node.ID, node.Signal))
}

func (e *DAGExecutor) handleSignal(evt *EventSignal) {
	delete(e.parkedPauses, evt.RequestID)
	if !e.state.IsNodePending(evt.NodeID) {
		e.logger.Warn("workflow wait node: a signal was accepted for a node that is no longer waiting; dropping it",
			"node", evt.NodeID, "executionID", e.state.ExecutionID)
		return
	}
	node := e.dag.Nodes[evt.NodeID]
	if node == nil || node.Type != NodeTypeWait {
		e.failForRemovedWaitNode(evt.NodeID, "a wait node")
		return
	}
	e.state.RemoveTimer(TimerKindWake, evt.NodeID)
	if evt.Status == pauseTimedOut {
		e.handleCompletion(NodeCompletion{
			NodeID:    evt.NodeID,
			Error:     fmt.Errorf("wait node %q timed out waiting for signal %q after %s", node.ID, node.Signal, node.Timeout),
			ErrorKind: ErrorKindError,
		})
		return
	}
	output := maps.Clone(evt.Data)
	if output == nil {
		output = map[string]any{}
	}
	output["status"] = evt.Status
	e.handleCompletion(NodeCompletion{NodeID: evt.NodeID, Output: output})
}

func (w *WorkflowInstance) publishSignalAnswer(ctx context.Context, requestID, status string) error {
	if w == nil || w.svc == nil || w.svc.Broker() == nil {
		return fmt.Errorf("no broker")
	}
	payload, err := json.Marshal(a2a.UserInputResponseData{
		Type:      string(a2a.SignalUserInputResponse),
		RequestID: requestID,
		Status:    status,
	})
	if err != nil {
		return err
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runWriteTimeout)
	defer cancel()
	return w.svc.Broker().PublishGuaranteed(pubCtx, a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID), &broker.Message{Payload: payload})
}

func (w *WorkflowInstance) resumeSignal(ctx context.Context, requestID, executionID string, exec *WorkflowExecution, routing persistedRouting, nodeStatus string, data map[string]any) {
	logger := exec.Executor.logger
	logger.Info("workflow wait node: signal received", "requestID", requestID,
		"executionID", executionID, "node", routing.NodeID, "signalStatus", nodeStatus)
	if w.signalApplyHook != nil {
		w.signalApplyHook(exec)
	}
	if err := exec.Executor.Advance(ctx, &EventSignal{RequestID: requestID, NodeID: routing.NodeID, Status: nodeStatus, Data: data}); err != nil {
		logger.Error("workflow wait node: resume Advance failed",
			"requestID", requestID, "executionID", executionID, "error", err)
	}
	w.cleanupIfFinalized(exec)
}

func (w *WorkflowInstance) publishWake(ctx context.Context, executionID string) error {
	if w.svc == nil || w.svc.Broker() == nil {
		return errors.New("no broker")
	}
	pubCtx, cancel := context.WithTimeout(ctx, runWriteTimeout)
	defer cancel()
	topic := a2a.WorkflowWakeTopic(w.cfg.Namespace, w.cfg.Name, executionID)
	return w.svc.Broker().PublishGuaranteed(pubCtx, topic, &broker.Message{Payload: []byte(idgen.New())})
}

func (w *WorkflowInstance) wakeParkedRun(executionID string) {
	if w.ctx == nil || w.ctx.Err() != nil {
		return
	}
	if err := w.publishWake(w.ctx, executionID); err != nil {
		w.logger.Warn("workflow: could not publish the wake of a parked run; the due-wake sweep sends it again",
			"executionID", executionID, "error", err)
	}
}

func (w *WorkflowInstance) parkedAndNotYetDue(ctx context.Context, executionID string) bool {
	runs := w.runStore()
	if runs == nil {
		return false
	}
	readCtx, cancel := context.WithTimeout(ctx, runWriteTimeout)
	defer cancel()
	run, err := runs.WakeState(readCtx, executionID)
	if err != nil {
		w.logger.Warn("workflow: could not read a run's wake time; taking the wake as due", "executionID", executionID, "error", err)
		return false
	}
	if run == nil || run.Status != wfstore.RunParked {
		return false
	}
	return run.WakeAt == nil || *run.WakeAt > time.Now().Add(dueWakeScanMargin).UnixMilli()
}

func (w *WorkflowInstance) handleWakeMessage(ctx context.Context, msg *broker.Message) {
	executionID := strings.TrimPrefix(msg.Topic, a2a.WorkflowWakeTopicPrefix(w.cfg.Namespace, w.cfg.Name))
	if w.parkedAndNotYetDue(ctx, executionID) {
		w.logger.Debug("workflow: a wake for a parked run whose timer is not due; dropping", "executionID", executionID)
		msg.Ack()
		return
	}
	wake := func(exec *WorkflowExecution) {
		_ = exec.Executor.Advance(w.ctx, &EventWake{})
		w.cleanupIfFinalized(exec)
	}
	w.takeOverForEvent(ctx, msg, executionID, wakeHolder(executionID, string(msg.Payload)), wake)
}

func (w *WorkflowInstance) sweepDueWakes(ctx context.Context) {
	runs := w.runStore()
	if runs == nil {
		return
	}
	cutoff := time.Now().Add(-dueWakeScanMargin).UnixMilli()
	var cursor wfstore.WakeCursor
	publishedBefore := 0
	for range dueWakeSweepPages {
		ids, next, err := runs.ParkedDueToWake(ctx, w.cfg.Name, cutoff, cursor, workflowHILSweepBatch)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			w.logger.Warn("workflow: due-wake sweep query failed", "error", err)
			return
		}
		published, failedExecutionID, err := w.publishWakes(ctx, ids)
		publishedBefore += published
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Warn("workflow: could not publish the wake of a parked run whose timer is due; the next sweep tries again",
					"executionID", failedExecutionID, "publishedThisSweep", publishedBefore, "error", err)
			}
			return
		}
		if len(ids) < workflowHILSweepBatch {
			return
		}
		cursor = next
	}
	w.logger.Warn("workflow: more parked runs may be due to wake than one sweep publishes; the rest wait for the next sweep",
		"published", dueWakeSweepPages*workflowHILSweepBatch)
}

func (w *WorkflowInstance) publishWakes(ctx context.Context, executionIDs []string) (published int, failedExecutionID string, err error) {
	for _, executionID := range executionIDs {
		if err := ctx.Err(); err != nil {
			return published, executionID, err
		}
		w.logger.Debug("workflow: waking a parked run whose timer is due", "executionID", executionID)
		if err := w.publishWake(ctx, executionID); err != nil {
			return published, executionID, err
		}
		published++
	}
	return published, "", nil
}
