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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

const (
	workflowHILSweepInterval = 30 * time.Second
	workflowHILSweepBatch    = 128
	resolvedPausedPurgeEvery = time.Hour
	resolvedPausedAge        = 24 * time.Hour
	resolvedPausedPurgeBatch = 500
)

type persistedExecContext struct {
	UserProps          a2a.UserProperties `json:"user_props"`
	TaskID             string             `json:"task_id"`
	ContextID          string             `json:"context_id"`
	IsStructured       bool               `json:"is_structured"`
	Identity           *auth.Identity     `json:"identity,omitempty"`
	SpendOrigin        *agent.SpendOrigin `json:"spend_origin,omitempty"`
	ModelOverrideAlias string             `json:"model_override_alias,omitempty"`
	SealedAuthToken    string             `json:"sealed_auth_token,omitempty"`
}

type persistedRouting struct {
	NodeID           string         `json:"node_id"`
	SubTaskID        string         `json:"sub_task_id"`
	MapNodeID        string         `json:"map_node_id,omitempty"`
	LoopNodeID       string         `json:"loop_node_id,omitempty"`
	ItemIndex        int            `json:"item_index,omitempty"`
	Kind             string         `json:"kind,omitempty"`
	ToolNodeID       string         `json:"tool_node_id,omitempty"`
	ToolName         string         `json:"tool_name,omitempty"`
	PendingArgs      map[string]any `json:"pending_args,omitempty"`
	RetryGroupID     string         `json:"retry_group_id,omitempty"`
	RetryAttempt     int            `json:"retry_attempt,omitempty"`
	StartUnpublished bool           `json:"start_unpublished,omitempty"`
}

func (r persistedRouting) forgottenWhenApplied() bool {
	return r.Kind == PauseKindSignal
}

var errPauseRowStillOpen = errors.New("the paused row could not be retired")

const PauseKindApproval = "approval"

func (w *WorkflowInstance) pausedStore() *wfstore.PausedRepo {
	w.pausedMu.RLock()
	defer w.pausedMu.RUnlock()
	return w.pausedRepo
}

func (w *WorkflowInstance) workflowHILEnabled() bool {
	return w.pausedStore() != nil
}

func (w *WorkflowInstance) permissiveToolValidation() bool {
	return w.svc != nil && w.svc.FeatureRegistry() != nil &&
		w.svc.FeatureRegistry().IsEnabled(features.KeyWorkflowToolValidationPermissive)
}

func (w *WorkflowInstance) toolApprovalEnforced() bool {
	if w.svc == nil || w.svc.FeatureRegistry() == nil {
		return true
	}
	return w.svc.FeatureRegistry().IsEnabled(features.KeyWorkflowToolApprovalEnforcement)
}

const workflowHILPauseTimeout = 15 * time.Second

func (w *WorkflowInstance) pauseWorkflowNode(ctx context.Context, e *DAGExecutor, evt *EventNodePaused) error {
	ctx, cancel := context.WithTimeout(ctx, workflowHILPauseTimeout)
	defer cancel()

	recipient := e.execCtx.UserProps.UserID
	if recipient == "" {
		return fmt.Errorf("workflow HIL pause: task has no recipient user id")
	}
	e.state.HasPausedRows = true
	ecJSON, err := w.runExecContext(e.execCtx)
	if err != nil {
		return fmt.Errorf("marshal exec context: %w", err)
	}
	routingJSON, err := json.Marshal(persistedRouting{
		NodeID:           evt.NodeID,
		SubTaskID:        evt.SubTaskID,
		MapNodeID:        evt.MapNodeID,
		LoopNodeID:       evt.LoopNodeID,
		ItemIndex:        evt.ItemIndex,
		Kind:             evt.Kind,
		ToolNodeID:       evt.ToolNodeID,
		ToolName:         evt.ToolName,
		PendingArgs:      evt.PendingArgs,
		RetryGroupID:     evt.retry.group,
		RetryAttempt:     evt.retry.attempt,
		StartUnpublished: evt.startUnpublished,
	})
	if err != nil {
		return fmt.Errorf("marshal routing: %w", err)
	}

	store := w.pausedStore()
	if store == nil {
		return fmt.Errorf("paused store is not available; the instance is stopping")
	}

	requesterAddr := a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, evt.RequestID)
	holder, err := store.Runs().InsertPause(ctx, wfstore.PausedWorkflow{
		RequestID:       evt.RequestID,
		ExecutionID:     e.state.ExecutionID,
		TaskID:          e.execCtx.TaskID,
		WorkflowName:    w.cfg.Name,
		NodeID:          evt.NodeID,
		RequesterAddr:   requesterAddr,
		RecipientUserID: recipient,
		ExecContext:     ecJSON,
		Routing:         string(routingJSON),
		ExpiresAt:       parseExpiresMs(evt.ExpiresAt),
	}, e.execCtx.holderOf())
	if errors.Is(err, wfstore.ErrNotHolder) {
		e.loseRunLocked("store a pause")
		return errRunLost
	}
	if err != nil {
		e.state.HasPausedRows = false
		return fmt.Errorf("persist paused workflow: %w", err)
	}
	e.execCtx.setHolder(holder)

	req := a2a.NotifyRequest{
		RequestID:       evt.RequestID,
		RecipientUserID: recipient,
		Mode:            a2a.NotifyModeInput,
		SenderUserID:    recipient,
		SenderAgent:     w.cfg.Name,
		Category:        a2a.CategoryHIL,
		RequesterAddr:   requesterAddr,
		Title:           "Your input is requested",
		Surface:         evt.Surface,
		ExpiresAt:       evt.ExpiresAt,
		Metadata:        hilNotifyMetadata(e.execCtx.UserProps.TraceID, e.execCtx.ContextID),
	}
	if perr := e.publishNotification(ctx, req); perr != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), pausedCleanupTimeout)
		defer cleanupCancel()
		if _, claimed, cerr := store.Claim(cleanupCtx, evt.RequestID, "failed"); cerr != nil || !claimed {
			if cerr != nil {
				w.logger.Warn("workflow HIL: failed to clean up paused row after publish failure",
					"requestID", evt.RequestID, "error", cerr)
			}
			return fmt.Errorf("route input request: %w: %w", perr, errPauseRowStillOpen)
		}
		return fmt.Errorf("route input request: %w", perr)
	}
	e.recordParkedPause(evt.RequestID, parkedStepOf(evt.NodeID, evt.SubTaskID, evt.MapNodeID, evt.LoopNodeID))
	return nil
}

func hilNotifyMetadata(traceID, sessionID string) map[string]any {
	md := map[string]any{"trace_id": traceID}
	if sessionID != "" {
		md["session_id"] = sessionID
	}
	return md
}

func parseExpiresMs(iso string) *int64 {
	if iso == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

func (w *WorkflowInstance) resumePausedNode(ctx context.Context, requestID, claimStatus, nodeStatus string, data map[string]any) {
	store := w.pausedStore()
	if store == nil {
		w.logger.Warn("workflow HIL: answer arrived after the instance released its store; the answer is lost and the row will time out",
			"requestID", requestID)
		return
	}
	row, err := store.Get(ctx, requestID)
	if err != nil {
		w.logger.Error("workflow HIL: load paused row failed; row stays paused for the timeout sweeper",
			"requestID", requestID, "error", err)
		return
	}
	if row == nil || row.Status != "paused" {
		w.logger.Info("workflow HIL: request not awaiting input (already resolved or gone); dropping", "requestID", requestID)
		if row != nil {
			w.settleRequestOfRunResumedElsewhere(row.ExecutionID, requestID, row.Status)
		}
		return
	}

	var routing persistedRouting
	if err = json.Unmarshal([]byte(row.Routing), &routing); err != nil {
		w.logger.Error("workflow HIL: malformed routing; leaving row paused", "requestID", requestID, "executionID", row.ExecutionID, "error", err)
		return
	}
	exec, isNew, err := w.prepareExecution(row)
	if err != nil {
		w.logger.Error("workflow HIL: reconstruct failed; leaving row paused for retry",
			"requestID", requestID, "executionID", row.ExecutionID, "error", err)
		return
	}

	if w.isStopping() {
		if claimStatus == "timed_out" {
			exec.Executor.logger.Debug("workflow HIL: timeout sweep deferred; the instance is stopping",
				"requestID", requestID, "executionID", row.ExecutionID)
		} else {
			exec.Executor.logger.Warn("workflow HIL: answer arrived while the instance is stopping; it is lost unless another replica claims it, otherwise the row times out",
				"requestID", requestID, "executionID", row.ExecutionID)
		}
		if isNew {
			exec.Context.Cancel()
		}
		return
	}

	claimed, err := w.claimResumeWithRetry(ctx, requestID, storedClaimStatus(claimStatus, !isNew && exec.Context.holdsRequest()))
	if err != nil {
		w.logger.Error("workflow HIL: claim failed after retries; row stays paused for the timeout sweeper",
			"requestID", requestID, "error", err)
		if isNew {
			exec.Context.Cancel()
		}
		return
	}
	if !claimed {
		w.logger.Info("workflow HIL: request already resolved (duplicate or timeout race); dropping", "requestID", requestID)
		if isNew {
			exec.Context.Cancel()
		} else if current, getErr := store.Get(ctx, requestID); getErr == nil && current != nil {
			w.settleRequestOfRunResumedElsewhere(row.ExecutionID, requestID, current.Status)
		}
		return
	}

	if w.resumeClaimHook != nil {
		w.resumeClaimHook()
	}

	exec, stopping := w.adoptClaimedResume(exec, isNew, requestID)
	if stopping {
		w.failClaimedResumeOnStop(ctx, requestID, claimStatus, exec, routing)
		return
	}
	if isNew {
		_ = w.recordPausesParkedBeforeRebuild(ctx, exec)
		holder, err := store.Runs().EnsureRun(ctx, row.ExecutionID, exec.Context.TaskID, w.cfg.Name, exec.Context.holderKey())
		if err != nil {
			w.logger.Warn("workflow HIL: could not store the run row of a run resumed from an earlier release's pause; it cannot park again",
				"executionID", row.ExecutionID, "error", err)
		} else {
			exec.Context.setHolder(holder)
		}
	}
	w.resumeRouted(ctx, requestID, row.ExecutionID, exec, routing, nodeStatus, data)
}

func (w *WorkflowInstance) resumeRouted(ctx context.Context, requestID, executionID string, exec *WorkflowExecution, routing persistedRouting, nodeStatus string, data map[string]any) {
	if routing.Kind == PauseKindApproval {
		w.resumeApprovalPause(ctx, requestID, executionID, exec, routing, nodeStatus, data)
		return
	}
	if routing.Kind == PauseKindSignal {
		w.resumeSignal(ctx, requestID, executionID, exec, routing, nodeStatus, data)
		return
	}
	output := map[string]any{"status": nodeStatus}
	for k, v := range data {
		output[k] = v
	}
	w.logger.Info("workflow HIL: resuming paused node", "requestID", requestID,
		"executionID", executionID, "node", routing.NodeID, "nodeStatus", nodeStatus)
	exec.Executor.settlePausedToolCall(routing.toolCall(), deferredResumeResult(nodeStatus, output))
	if aerr := exec.Executor.Advance(ctx, &EventNodeComplete{
		NodeID:     routing.NodeID,
		SubTaskID:  routing.SubTaskID,
		Output:     output,
		MapNodeID:  routing.MapNodeID,
		LoopNodeID: routing.LoopNodeID,
		ItemIndex:  routing.ItemIndex,
	}); aerr != nil {
		w.logger.Error("workflow HIL: resume Advance failed",
			"requestID", requestID,
			"traceID", exec.Context.UserProps.TraceID,
			"executionID", executionID,
			"error", aerr,
		)
	}
	w.cleanupIfFinalized(exec)
}

const (
	statusResumedByHolder  = "resumed_by_holder"
	statusTimedOutByHolder = "timed_out_by_holder"
)

const statusClosedForRestart = "closed_for_restart"

func pauseClaimedElsewhere(status string) bool {
	switch status {
	case "resumed", "timed_out", "cancelled":
		return true
	}
	return false
}

func storedClaimStatus(claimStatus string, byHolder bool) string {
	if !byHolder {
		return claimStatus
	}
	switch claimStatus {
	case "resumed":
		return statusResumedByHolder
	case "timed_out":
		return statusTimedOutByHolder
	}
	return claimStatus
}

func (w *WorkflowInstance) settleRequestOfRunResumedElsewhere(executionID, requestID, status string) {
	if status == statusResumedByHolder || status == statusTimedOutByHolder {
		return
	}
	w.executionsMu.Lock()
	exec := w.executionsByID[executionID]
	w.executionsMu.Unlock()
	if exec == nil || !exec.Executor.hasParkedPause(requestID) || !exec.Context.holdsRequest() {
		return
	}
	exec.Executor.logger.Info("workflow HIL: another process resumed a pause parked here; acknowledging the run's request",
		"requestID", requestID, "executionID", executionID)
	exec.Context.ackRequest()
}

func (w *WorkflowInstance) recordPausesParkedBeforeRebuild(ctx context.Context, exec *WorkflowExecution) error {
	store := w.pausedStore()
	if store == nil {
		return nil
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redeliveryLookupTimeout)
	defer cancel()
	rows, err := store.PausedForExecution(readCtx, exec.State.ExecutionID)
	if err != nil {
		exec.Executor.logger.Warn("workflow HIL: could not read the other paused rows of a rebuilt run",
			"executionID", exec.State.ExecutionID, "error", err)
		return err
	}
	e := exec.Executor
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range rows {
		routing, ok := decodeRouting(rows[i])
		if !ok {
			e.logger.Warn("workflow HIL: a paused row of a rebuilt run has unreadable routing; the run does not count it as open",
				"requestID", rows[i].RequestID, "executionID", exec.State.ExecutionID)
			continue
		}
		step := parkedStepOf(routing.NodeID, routing.SubTaskID, routing.MapNodeID, routing.LoopNodeID)
		step.beforeRebuild = true
		e.recordParkedPause(rows[i].RequestID, step)
	}
	return nil
}

const resumeClaimMaxAttempts = 4

func (w *WorkflowInstance) claimResumeWithRetry(ctx context.Context, requestID, status string) (bool, error) {
	store := w.pausedStore()
	if store == nil {
		return false, fmt.Errorf("paused store is not available; the instance is stopping")
	}
	backoff := 50 * time.Millisecond
	var err error
	for attempt := 0; attempt < resumeClaimMaxAttempts; attempt++ {
		var claimed bool
		if _, claimed, err = store.Claim(ctx, requestID, status); err == nil {
			return claimed, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return false, err
}

func (w *WorkflowInstance) prepareExecution(row *wfstore.PausedWorkflow) (*WorkflowExecution, bool, error) {
	w.executionsMu.Lock()
	if exec, ok := w.executionsByID[row.ExecutionID]; ok {
		w.executionsMu.Unlock()
		return exec, false, nil
	}
	w.executionsMu.Unlock()
	exec, err := w.reconstructExecution(row)
	if err != nil {
		return nil, false, err
	}
	return exec, true, nil
}

func (w *WorkflowInstance) registerExecution(executionID string, exec *WorkflowExecution) *WorkflowExecution {
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()
	return w.registerExecutionLocked(executionID, exec)
}

func (w *WorkflowInstance) adoptClaimedResume(exec *WorkflowExecution, isNew bool, requestID string) (*WorkflowExecution, bool) {
	exec.Executor.forgetParkedPause(requestID)
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()
	if isNew {
		exec = w.registerExecutionLocked(exec.State.ExecutionID, exec)
	}
	return exec, w.stopping
}

func (w *WorkflowInstance) failClaimedResumeOnStop(ctx context.Context, requestID, claimStatus string, exec *WorkflowExecution, routing persistedRouting) {
	refusal := tool.RefusalRunEnded
	if claimStatus == "timed_out" {
		refusal = tool.RefusalTimedOut
	}
	exec.Executor.logger.Warn("workflow HIL: claimed answer not applied; the instance is stopping, failing the run",
		"requestID", requestID, "executionID", exec.State.ExecutionID,
		"node", routing.NodeID, "tool", routing.ToolName, "claimStatus", claimStatus)
	if routing.Kind == PauseKindApproval {
		exec.Executor.auditApprovalRefusal(context.WithoutCancel(ctx), routing.ToolName, refusal)
	}
	exec.Executor.settlePausedToolCall(routing.toolCall(), hilRefusalResult(refusal))
	_ = exec.Executor.Advance(ctx, &EventInstanceStopping{})
	w.cleanupIfFinalized(exec)
}

func (w *WorkflowInstance) registerExecutionLocked(executionID string, exec *WorkflowExecution) *WorkflowExecution {
	if existing, ok := w.executionsByID[executionID]; ok {
		exec.Context.Cancel()
		return existing
	}
	w.executions[exec.Context.TaskID] = exec
	w.executionsByID[executionID] = exec
	for _, subTaskID := range exec.State.SubTaskIDs() {
		w.subTaskIndex[subTaskID] = exec.Context.TaskID
	}
	return exec
}

func (w *WorkflowInstance) reconstructExecution(row *wfstore.PausedWorkflow) (*WorkflowExecution, error) {
	exec, err := w.rebuildExecution(row.StateSnapshot, row.ExecContext, nil)
	if err != nil {
		return nil, err
	}
	exec.Executor.reconstructed = true
	return exec, nil
}

func (w *WorkflowInstance) rebuildExecution(stateSnapshot, execContext string, msg *broker.Message) (*WorkflowExecution, error) {
	var state WorkflowExecutionState
	if err := json.Unmarshal([]byte(stateSnapshot), &state); err != nil {
		return nil, fmt.Errorf("unmarshal state snapshot: %w", err)
	}
	restoreMetadataTypes(state.Metadata, w.dag)
	var ec persistedExecContext
	if err := json.Unmarshal([]byte(execContext), &ec); err != nil {
		return nil, fmt.Errorf("unmarshal exec context: %w", err)
	}
	w.unsealAuthToken(&ec)
	execCtx := NewExecutionContext(w.ctx, ec.TaskID, ec.ContextID, msg, ec.UserProps, ec.IsStructured)
	execCtx.Identity = ec.Identity
	execCtx.SpendOrigin = agent.RestoredSpendOrigin(ec.SpendOrigin)
	execCtx.ModelOverrideAlias = ec.ModelOverrideAlias
	resumeLogger := w.logger.With(
		"traceID", ec.UserProps.TraceID,
		"taskID", ec.TaskID,
		"agentName", w.cfg.Name,
		"contextID", ec.ContextID,
	)
	executor := &DAGExecutor{
		cfg:      w.cfg,
		dag:      w.dag,
		state:    &state,
		execCtx:  execCtx,
		instance: w,
		logger:   resumeLogger,
	}
	return &WorkflowExecution{State: &state, Context: execCtx, Executor: executor}, nil
}

func (w *WorkflowInstance) hilTimeoutSweepLoop(ctx context.Context) {
	ticker := time.NewTicker(workflowHILSweepInterval)
	defer ticker.Stop()
	purge := time.NewTicker(resolvedPausedPurgeEvery)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweepParkedRuns(ctx)
		case <-purge.C:
			w.purgeResolvedPaused(ctx)
		}
	}
}

func (w *WorkflowInstance) sweepParkedRuns(ctx context.Context) {
	w.sweepHILTimeouts(ctx)
	w.sweepParkedDeadlines(ctx)
	if w.durableWaits {
		w.sweepDueWakes(ctx)
	}
}

func (w *WorkflowInstance) purgeResolvedPaused(ctx context.Context) {
	store := w.pausedStore()
	if store == nil {
		return
	}
	cutoff := time.Now().Add(-resolvedPausedAge).UnixMilli()
	if total := purgeInBatches(ctx, w, cutoff, store.DeleteResolvedOlderThan); total > 0 {
		w.logger.Info("workflow HIL: removed resolved paused rows", "rows", total)
	}
}

func purgeInBatches(ctx context.Context, w *WorkflowInstance, cutoff int64, del func(context.Context, int64, int) (int64, error)) int64 {
	var total int64
	for ctx.Err() == nil {
		n, err := del(ctx, cutoff, resolvedPausedPurgeBatch)
		total += n
		if err != nil {
			w.logger.Warn("workflow HIL: purge failed; retrying in an hour", "error", err)
			break
		}
		if n < resolvedPausedPurgeBatch {
			break
		}
	}
	return total
}
