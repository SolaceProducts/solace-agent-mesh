// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const (
	parkWriteTimeout     = 15 * time.Second
	parkRetryDelay       = 2 * time.Second
	runWriteTimeout      = 5 * time.Second
	expiryBackstopMargin = 2 * time.Minute
)

func (w *WorkflowInstance) runStore() *wfstore.RunRepo {
	if p := w.pausedStore(); p != nil {
		return p.Runs()
	}
	return nil
}

const (
	deadlineHolderPrefix = "deadline:"
	cancelHolderPrefix   = "cancel:"
)

func deadlineHolder(executionID, messageID string) string {
	if messageID == "" || messageID == executionID {
		return deadlineHolderPrefix + executionID
	}
	return deadlineHolderPrefix + executionID + ":" + messageID
}

func cancelHolder(taskID, messageID string) string {
	if messageID == "" {
		return cancelHolderPrefix + taskID
	}
	return cancelHolderPrefix + taskID + ":" + messageID
}

const (
	pauseResumed  = "resumed"
	pauseTimedOut = "timed_out"
)

func claimStatusFor(answerStatus string) string {
	if answerStatus == pauseTimedOut {
		return pauseTimedOut
	}
	return pauseResumed
}

func (w *WorkflowInstance) sealer() auth.Sealer {
	if w.svc == nil {
		return nil
	}
	s, _ := w.svc.CredentialService().(auth.Sealer)
	return s
}

func (w *WorkflowInstance) runExecContext(execCtx *WorkflowExecutionContext) (string, error) {
	pec := persistedExecContext{
		UserProps:          execCtx.UserProps,
		TaskID:             execCtx.TaskID,
		ContextID:          execCtx.ContextID,
		IsStructured:       execCtx.IsStructuredInvocation,
		SpendOrigin:        agent.PersistedSpendOrigin(execCtx.SpendOrigin),
		ModelOverrideAlias: execCtx.ModelOverrideAlias,
	}
	token := pec.UserProps.AuthToken
	if execCtx.Identity != nil {
		identity := *execCtx.Identity
		if token == "" {
			token = identity.AuthToken
		}
		identity.AuthToken = ""
		identity.Claims = nil
		pec.Identity = &identity
	}
	pec.UserProps.AuthToken = ""
	if token != "" {
		if s := w.sealer(); s == nil {
			w.noSealerWarn.Do(func() {
				w.logger.Warn("workflow: no identity key to encrypt the caller's token; an AWE that continues a stored run does so with no token",
					"taskID", execCtx.TaskID)
			})
		} else if sealed, err := s.Seal(token); err != nil {
			w.logger.Warn("workflow: could not encrypt the caller's token; an AWE that continues this run does so with no token",
				"taskID", execCtx.TaskID, "error", err)
		} else {
			pec.SealedAuthToken = sealed
		}
	}
	b, err := json.Marshal(pec)
	return string(b), err
}

func (w *WorkflowInstance) unsealAuthToken(ec *persistedExecContext) {
	if ec.SealedAuthToken == "" {
		return
	}
	s := w.sealer()
	if s == nil {
		w.logger.Warn("workflow resume: the run carries the caller's token and this process has no identity key; resuming with no token",
			"taskID", ec.TaskID)
		return
	}
	token, err := s.Open(ec.SealedAuthToken)
	if err != nil {
		w.logger.Warn("workflow resume: could not decrypt the caller's token; resuming with no token",
			"taskID", ec.TaskID, "error", err)
		return
	}
	ec.UserProps.AuthToken = token
	if ec.Identity != nil {
		ec.Identity.AuthToken = token
	}
}

func (e *DAGExecutor) writeParkLocked() bool {
	w := e.instance
	runs := w.runStore()
	if runs == nil {
		return !e.state.HasPausedRows
	}
	state, err := json.Marshal(e.state)
	if err != nil {
		e.logger.Error("workflow park: could not encode the run's state; its message stays unacknowledged",
			"executionID", e.state.ExecutionID, "error", err)
		return false
	}
	execContext, err := w.runExecContext(e.execCtx)
	if err != nil {
		e.logger.Error("workflow park: could not encode the run's context; its message stays unacknowledged",
			"executionID", e.state.ExecutionID, "error", err)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), parkWriteTimeout)
	defer cancel()
	answered, err := runs.Park(ctx, e.state.ExecutionID, e.execCtx.holderOf(), e.runState(state, execContext))
	switch {
	case errors.Is(err, wfstore.ErrNotHolder):
		if e.parkUnsure {
			e.execCtx.ackRequest()
		}
		e.loseRunLocked("park")
		return false
	case errors.Is(err, wfstore.ErrPauseClaimed):
		e.logger.Debug("workflow park: an open pause was just claimed; not parking",
			"executionID", e.state.ExecutionID)
		if e.unsureClaimCount.Load() > 0 {
			e.scheduleParkRetryLocked()
		}
		return false
	case err != nil:
		e.logger.Warn("workflow park: writing the run failed; retrying, its message stays unacknowledged until it succeeds",
			"executionID", e.state.ExecutionID, "error", err)
		e.parkUnsure = true
		e.scheduleParkRetryLocked()
		return false
	case len(answered) > 0:
		e.logger.Info("workflow park: answers arrived while the run was running; applying them instead of parking",
			"executionID", e.state.ExecutionID, "answered", len(answered))
		executionID := e.state.ExecutionID
		w.wg.Go(func() { w.applyAnsweredPauses(executionID, answered) })
		return false
	}
	e.parkWritten = true
	return true
}

func (e *DAGExecutor) scheduleParkRetryLocked() {
	if e.parkRetryPending {
		return
	}
	e.parkRetryPending = true
	time.AfterFunc(parkRetryDelay, func() {
		e.mu.Lock()
		e.parkRetryPending = false
		e.mu.Unlock()
		if e.instance.ctx != nil && e.instance.ctx.Err() != nil {
			return
		}
		e.applyLandedClaims()
		_ = e.Advance(context.Background(), &eventSettle{})
	})
}

func claimLanded(p *wfstore.PausedWorkflow) bool {
	return p != nil && p.Answer != nil && (p.Status == pauseResumed || p.Status == pauseTimedOut)
}

func (e *DAGExecutor) applyLandedClaims() {
	w := e.instance
	store := w.pausedStore()
	if store == nil || e.unsureClaimCount.Load() == 0 {
		return
	}
	e.claimMu.Lock()
	var landed []*wfstore.PausedWorkflow
	var kept []string
	for _, requestID := range e.unsureClaims {
		ctx, cancel := context.WithTimeout(context.Background(), runWriteTimeout)
		p, err := store.Get(ctx, requestID)
		cancel()
		switch {
		case err != nil:
			kept = append(kept, requestID)
		case claimLanded(p):
			landed = append(landed, p)
		}
	}
	e.setUnsureClaimsLocked(kept)
	e.claimMu.Unlock()
	if len(kept) > 0 {
		e.mu.Lock()
		e.scheduleParkRetryLocked()
		e.mu.Unlock()
	}
	exec := w.residentByID(e.state.ExecutionID)
	if exec == nil {
		return
	}
	for _, p := range landed {
		var resp a2a.UserInputResponseData
		if err := json.Unmarshal([]byte(*p.Answer), &resp); err != nil {
			e.logger.Error("workflow HIL: a written answer is unreadable", "requestID", p.RequestID, "error", err)
			continue
		}
		resp.RequestID = p.RequestID
		w.applyAnswer(w.ctx, exec, p, resp)
	}
}

func (e *DAGExecutor) setUnsureClaimsLocked(ids []string) {
	e.unsureClaims = ids
	e.unsureClaimCount.Store(int32(len(ids)))
}

func (e *DAGExecutor) takeUnsureClaimLocked(requestID string) bool {
	i := slices.Index(e.unsureClaims, requestID)
	if i < 0 {
		return false
	}
	e.setUnsureClaimsLocked(slices.Delete(e.unsureClaims, i, i+1))
	return true
}

func (e *DAGExecutor) endRunLocked() {
	if !e.state.HasPausedRows || e.instance == nil {
		return
	}
	runs := e.instance.runStore()
	if runs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), runWriteTimeout)
	defer cancel()
	if err := runs.End(ctx, e.state.ExecutionID, e.execCtx.holderOf()); err != nil {
		e.logger.Warn("workflow: could not mark the finished run ended; the hourly purge keeps it",
			"executionID", e.state.ExecutionID, "error", err)
	}
}

var errRunLost = errors.New("another copy now holds this workflow run")

var errStoreReleased = errors.New("the workflow store was released")

func (e *DAGExecutor) loseRunLocked(op string) {
	if e.lost {
		return
	}
	e.lost = true
	h := e.execCtx.holderOf()
	e.logger.Warn("workflow: another copy now holds this run; stopping this copy",
		"executionID", e.state.ExecutionID, "op", op, "heldBy", h.Key, "epoch", h.Epoch)
	e.execCtx.dropRequest()
	e.execCtx.Cancel()
}

func (w *WorkflowInstance) loseRun(exec *WorkflowExecution, op string) {
	e := exec.Executor
	e.mu.Lock()
	e.loseRunLocked(op)
	e.mu.Unlock()
	w.evictParked(e)
}

func (e *DAGExecutor) stillHoldsLocked(op string) bool {
	if e.lost {
		return false
	}
	if e.instance == nil || e.execCtx == nil {
		return true
	}
	h := e.execCtx.holderOf()
	runs := e.instance.runRepo()
	if runs == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), runWriteTimeout)
	defer cancel()
	holds, err := runs.Fence(ctx, e.state.ExecutionID, h)
	if err != nil {
		e.logger.Warn("workflow: could not check that this copy still holds the run; going on",
			"executionID", e.state.ExecutionID, "op", op, "error", err)
		return true
	}
	if !holds {
		e.loseRunLocked(op)
	}
	return holds
}

func (w *WorkflowInstance) evictParked(e *DAGExecutor) {
	w.executionsMu.Lock()
	if cur := w.executionsByID[e.state.ExecutionID]; cur != nil && cur.Executor == e {
		delete(w.executionsByID, e.state.ExecutionID)
		delete(w.executions, e.execCtx.TaskID)
		for _, id := range e.state.SubTaskIDs() {
			delete(w.subTaskIndex, id)
		}
	}
	w.executionsMu.Unlock()
	e.execCtx.Cancel()
}

func (w *WorkflowInstance) residentByID(executionID string) *WorkflowExecution {
	w.executionsMu.Lock()
	exec := w.executionsByID[executionID]
	w.executionsMu.Unlock()
	if exec == nil || exec.Executor.isParked() {
		return nil
	}
	return exec
}

func (w *WorkflowInstance) residentByTask(taskID string) *WorkflowExecution {
	w.executionsMu.Lock()
	exec := w.executions[taskID]
	w.executionsMu.Unlock()
	if exec == nil || exec.Executor.isParked() {
		return nil
	}
	return exec
}

func (e *DAGExecutor) isParked() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.parkWritten
}

func (w *WorkflowInstance) residentHolderOf(run *wfstore.WorkflowRun) *WorkflowExecution {
	exec := w.residentByID(run.ExecutionID)
	if exec == nil || exec.Context.holderKey() != run.HeldBy {
		return nil
	}
	return exec
}

func (w *WorkflowInstance) handleWorkflowHILResponse(ctx context.Context, msg *broker.Message) {
	var resp a2a.UserInputResponseData
	if err := json.Unmarshal(msg.Payload, &resp); err != nil || resp.RequestID == "" {
		w.logger.Warn("workflow HIL: malformed answer; dropping", "topic", msg.Topic, "error", err)
		msg.Ack()
		return
	}
	runs := w.runStore()
	if runs == nil {
		w.logger.Warn("workflow HIL: answer arrived after the instance released its store; leaving it for redelivery",
			"requestID", resp.RequestID)
		return
	}
	claim := claimStatusFor(resp.Status)
	acceptCtx, cancel := context.WithTimeout(ctx, runWriteTimeout)
	res, err := runs.AcceptAnswer(acceptCtx, w.cfg.Name, resp.RequestID, claim, string(msg.Payload))
	cancel()
	if err != nil {
		w.logger.Error("workflow HIL: could not record the answer; returning it to the queue",
			"requestID", resp.RequestID, "error", err)
		msg.Nack()
		return
	}
	switch res.Outcome {
	case wfstore.AnswerNoRun:
		msg.Ack()
		w.resumePausedNode(ctx, resp.RequestID, claim, resp.Status, resp.Data)
	case wfstore.AnswerOtherWorkflow:
		w.logger.Warn("workflow HIL: answer names a pause of another workflow; dropping", "requestID", resp.RequestID)
		msg.Ack()
	case wfstore.AnswerSettled:
		w.logger.Info("workflow HIL: answer for a pause no longer open; dropping", "requestID", resp.RequestID)
		msg.Ack()
	case wfstore.AnswerRecorded:
		msg.Ack()
		if exec := w.residentHolderOf(res.Run); exec != nil {
			w.applyHeldAnswer(ctx, exec, resp)
			return
		}
		w.forwardAnswer(ctx, msg.Payload, resp.RequestID)
	case wfstore.AnswerTookOver:
		w.continueRun(ctx, res.Run, msg, resp.RequestID, func(exec *WorkflowExecution) {
			w.applyAnswer(ctx, exec, res.Pause, resp)
		})
	case wfstore.AnswerHoldsRun:
		w.retakeHeldRun(ctx, res.Run.ExecutionID, resp.RequestID, msg, func(exec *WorkflowExecution) {
			w.applyAnswer(ctx, exec, res.Pause, resp)
		})
	}
}

func (w *WorkflowInstance) continueRun(ctx context.Context, run *wfstore.WorkflowRun, msg *broker.Message, heldBy string, apply func(*WorkflowExecution)) {
	w.executionsMu.Lock()
	stale := w.executionsByID[run.ExecutionID]
	w.executionsMu.Unlock()
	if stale != nil {
		w.evictParked(stale.Executor)
	}
	if w.resumeClaimHook != nil {
		w.resumeClaimHook()
	}
	exec, err := w.rebuildFromRun(run, msg, heldBy)
	if err != nil {
		w.logger.Error("workflow: could not rebuild the run from workflow_checkpoint; returning its message to the queue",
			"executionID", run.ExecutionID, "heldBy", heldBy, "error", err)
		msg.Nack()
		return
	}
	if err := w.recordOpenPauses(ctx, exec, run, heldBy); err != nil {
		w.logger.Error("workflow: could not read the open pauses of a run to continue; returning its message to the queue",
			"executionID", run.ExecutionID, "heldBy", heldBy, "error", err)
		exec.Context.Cancel()
		msg.Nack()
		return
	}
	w.executionsMu.Lock()
	stopping := w.stopping || w.processStopping.Load()
	if !stopping {
		exec = w.registerExecutionLocked(run.ExecutionID, exec)
	}
	w.executionsMu.Unlock()
	if stopping {
		w.logger.Info("workflow: this instance is stopping; leaving the run's message for another AWE",
			"executionID", run.ExecutionID, "heldBy", heldBy)
		exec.Context.Cancel()
		return
	}
	w.logger.Info("workflow: continuing a run from workflow_checkpoint",
		"executionID", run.ExecutionID, "taskID", run.TaskID, "heldBy", heldBy)
	apply(exec)
}

const recordOpenPausesAttempts = 3

func (w *WorkflowInstance) recordOpenPauses(ctx context.Context, exec *WorkflowExecution, run *wfstore.WorkflowRun, heldBy string) error {
	backoff := 50 * time.Millisecond
	var err error
	for attempt := range recordOpenPausesAttempts {
		if err = w.recordOpenPausesOnce(ctx, exec, run, heldBy); err == nil {
			return nil
		}
		if attempt == recordOpenPausesAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return err
}

func (w *WorkflowInstance) recordOpenPausesOnce(ctx context.Context, exec *WorkflowExecution, run *wfstore.WorkflowRun, heldBy string) error {
	if err := w.recordPausesParkedBeforeRebuild(ctx, exec); err != nil {
		return err
	}
	if heldBy == wfstore.HeldByRequest || isEventHolder(heldBy) {
		return nil
	}
	open, err := run.OpenPauseIDs()
	if err != nil {
		return err
	}
	if !slices.Contains(open, heldBy) {
		return nil
	}
	store := w.pausedStore()
	if store == nil {
		return errStoreReleased
	}
	pause, err := store.Get(ctx, heldBy)
	if err != nil || pause == nil {
		return err
	}
	routing, ok := decodeRouting(*pause)
	if !ok {
		return nil
	}
	step := parkedStepOf(routing.NodeID, routing.SubTaskID, routing.MapNodeID, routing.LoopNodeID)
	step.beforeRebuild = true
	e := exec.Executor
	e.mu.Lock()
	e.recordParkedPause(heldBy, step)
	e.mu.Unlock()
	return nil
}

func (w *WorkflowInstance) retakeHeldRun(ctx context.Context, executionID, heldBy string, msg *broker.Message, apply func(*WorkflowExecution)) {
	if exec := w.residentByID(executionID); exec != nil && exec.Context.holderKey() == heldBy && msg.Redelivered {
		exec.Context.adoptRedelivery(msg)
		return
	}
	if !msg.Redelivered {
		w.logger.Info("workflow: duplicate of the message holding a run; dropping",
			"executionID", executionID, "heldBy", heldBy)
		msg.Ack()
		return
	}
	runs := w.runStore()
	if runs == nil {
		w.logger.Warn("workflow: a run to retake arrived after the instance released its store; leaving its message for redelivery",
			"executionID", executionID)
		return
	}
	retakeCtx, cancel := context.WithTimeout(ctx, runWriteTimeout)
	run, closed, err := runs.Retake(retakeCtx, executionID, heldBy, statusClosedForRestart)
	cancel()
	if errors.Is(err, wfstore.ErrNotHolder) {
		msg.Ack()
		return
	}
	if err != nil {
		w.logger.Error("workflow: could not retake a run whose AWE died; returning its message to the queue",
			"executionID", executionID, "error", err)
		msg.Nack()
		return
	}
	w.logger.Warn("workflow: the AWE running this run died; continuing it from its last stored state",
		"executionID", executionID, "heldBy", heldBy, "closedPauses", closed)
	w.continueRun(ctx, run, msg, heldBy, w.afterRestore(run, heldBy, apply))
}

func (w *WorkflowInstance) afterRestore(run *wfstore.WorkflowRun, heldBy string, apply func(*WorkflowExecution)) func(*WorkflowExecution) {
	if w.checkpointStore() == nil {
		return apply
	}
	if isEventHolder(heldBy) {
		return func(exec *WorkflowExecution) {
			if !runOutcomeDecided(exec) {
				apply(exec)
				return
			}
			if err := exec.Executor.Advance(w.ctx, &EventRestore{}); err != nil {
				exec.Executor.logger.Error("restore Advance failed", "executionID", run.ExecutionID, "error", err)
			}
			w.cleanupIfFinalized(exec)
		}
	}
	open, err := run.OpenPauseIDs()
	applied := err == nil && !slices.Contains(open, heldBy)
	return func(exec *WorkflowExecution) {
		if err := exec.Executor.Advance(w.ctx, &EventRestore{}); err != nil {
			exec.Executor.logger.Error("restore Advance failed", "executionID", run.ExecutionID, "error", err)
		}
		if applied {
			w.cleanupIfFinalized(exec)
			return
		}
		apply(exec)
	}
}

func runOutcomeDecided(exec *WorkflowExecution) bool {
	e := exec.Executor
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state.Cancelled || e.state.InExitHandler || e.state.PendingOutcome != ""
}

func isEventHolder(heldBy string) bool {
	return strings.HasPrefix(heldBy, deadlineHolderPrefix) || strings.HasPrefix(heldBy, cancelHolderPrefix)
}

func (w *WorkflowInstance) rebuildFromRun(run *wfstore.WorkflowRun, msg *broker.Message, heldBy string) (*WorkflowExecution, error) {
	exec, err := w.reconstructExecution(&wfstore.PausedWorkflow{
		ExecutionID:   run.ExecutionID,
		StateSnapshot: run.StateSnapshot,
		ExecContext:   run.ExecContext,
	})
	if err != nil {
		return nil, err
	}
	exec.Executor.reconstructed = exec.State.HasPausedRows
	exec.Context.holdWith(msg, wfstore.Holder{Key: heldBy, Epoch: run.HolderEpoch})
	return exec, nil
}

func (w *WorkflowInstance) applyAnswer(ctx context.Context, exec *WorkflowExecution, pause *wfstore.PausedWorkflow, resp a2a.UserInputResponseData) {
	routing, ok := decodeRouting(*pause)
	if !ok {
		w.logger.Error("workflow HIL: the paused row's routing is unreadable; the answer cannot be applied",
			"requestID", pause.RequestID, "executionID", pause.ExecutionID)
		return
	}
	if !routing.forgottenWhenApplied() {
		exec.Executor.forgetParkedPause(pause.RequestID)
	}
	w.resumeRouted(ctx, pause.RequestID, pause.ExecutionID, exec, routing, resp.Status, resp.Data)
}

func (w *WorkflowInstance) applyHeldAnswer(ctx context.Context, exec *WorkflowExecution, resp a2a.UserInputResponseData) {
	if w.controlStopping() {
		w.logger.Info("workflow HIL: the instance is stopping; the answer stays on the pause for the run's next holder",
			"requestID", resp.RequestID)
		return
	}
	if w.resumeClaimHook != nil {
		w.resumeClaimHook()
	}
	runs := w.runStore()
	if runs == nil {
		return
	}
	holder := exec.Context.holderOf()
	e := exec.Executor
	e.claimMu.Lock()
	ownLanded := e.takeUnsureClaimLocked(resp.RequestID)
	claimCtx, cancel := context.WithTimeout(ctx, runWriteTimeout)
	pause, claimed, err := runs.ClaimHeld(claimCtx, resp.RequestID, holder, claimStatusFor(resp.Status))
	cancel()
	unsure := err != nil && !errors.Is(err, wfstore.ErrNotHolder)
	if unsure {
		e.setUnsureClaimsLocked(append(e.unsureClaims, resp.RequestID))
	}
	e.claimMu.Unlock()
	switch {
	case errors.Is(err, wfstore.ErrNotHolder):
		w.loseRun(exec, "claim an answer")
		return
	case unsure:
		w.logger.Warn("workflow HIL: could not claim an answer for a run held here; it is applied when the run next parks",
			"requestID", resp.RequestID, "error", err)
		e.mu.Lock()
		e.scheduleParkRetryLocked()
		e.mu.Unlock()
		return
	case !claimed && (!ownLanded || !claimLanded(pause)):
		return
	}
	if w.controlStopping() {
		w.releaseHeldClaim(ctx, runs, resp.RequestID, holder)
		return
	}
	w.applyAnswer(ctx, exec, pause, resp)
}

func (w *WorkflowInstance) releaseHeldClaim(ctx context.Context, runs *wfstore.RunRepo, requestID string, holder wfstore.Holder) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runWriteTimeout)
	defer cancel()
	if err := runs.ReleaseClaim(releaseCtx, requestID, holder); err != nil {
		w.logger.Warn("workflow HIL: the instance stopped during a claim and the answer could not be put back; whoever answered is asked again",
			"requestID", requestID, "error", err)
		return
	}
	w.logger.Info("workflow HIL: the instance stopped during a claim; the answer stays on the pause for the run's next holder",
		"requestID", requestID)
}

func (w *WorkflowInstance) controlStopping() bool {
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()
	return w.stopping
}

func (w *WorkflowInstance) applyAnsweredPauses(executionID string, answered []wfstore.PausedWorkflow) {
	exec := w.residentByID(executionID)
	if exec == nil {
		return
	}
	for i := range answered {
		if answered[i].Answer == nil {
			continue
		}
		var resp a2a.UserInputResponseData
		if err := json.Unmarshal([]byte(*answered[i].Answer), &resp); err != nil {
			w.logger.Error("workflow HIL: a written answer is unreadable", "requestID", answered[i].RequestID, "error", err)
			continue
		}
		w.applyHeldAnswer(w.ctx, exec, resp)
	}
}

func (w *WorkflowInstance) forwardAnswer(ctx context.Context, payload []byte, requestID string) {
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runWriteTimeout)
	defer cancel()
	if err := w.svc.Broker().Publish(pubCtx, a2a.WorkflowForwardTopic(w.cfg.Namespace, w.cfg.Name), &broker.Message{Payload: payload}); err != nil {
		w.logger.Warn("workflow HIL: could not pass the answer on; the AWE running the run applies it when the run next parks",
			"requestID", requestID, "error", err)
	}
}

func (w *WorkflowInstance) handleForwardedAnswer(ctx context.Context, msg *broker.Message) {
	var resp a2a.UserInputResponseData
	if err := json.Unmarshal(msg.Payload, &resp); err != nil || resp.RequestID == "" {
		return
	}
	store := w.pausedStore()
	if store == nil {
		return
	}
	pause, err := store.Get(ctx, resp.RequestID)
	if err != nil || pause == nil || pause.Answer == nil {
		return
	}
	var stored a2a.UserInputResponseData
	if err := json.Unmarshal([]byte(*pause.Answer), &stored); err != nil {
		return
	}
	stored.RequestID = pause.RequestID
	exec := w.residentByID(pause.ExecutionID)
	if exec == nil {
		return
	}
	w.applyHeldAnswer(ctx, exec, stored)
}

func (w *WorkflowInstance) publishDeadline(executionID string) {
	if w.svc == nil || w.svc.Broker() == nil || w.ctx == nil || w.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(w.ctx, runWriteTimeout)
	defer cancel()
	topic := a2a.WorkflowDeadlineTopic(w.cfg.Namespace, w.cfg.Name, executionID)
	if err := w.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{Payload: []byte(idgen.New())}); err != nil {
		w.logger.Warn("workflow: could not publish the deadline of a parked run; it ends when it next resumes",
			"executionID", executionID, "error", err)
	}
}

func (w *WorkflowInstance) handleDeadline(ctx context.Context, msg *broker.Message) {
	executionID := strings.TrimPrefix(msg.Topic, a2a.WorkflowDeadlineTopicPrefix(w.cfg.Namespace, w.cfg.Name))
	timeout := func(exec *WorkflowExecution) {
		_ = exec.Executor.Advance(w.ctx, &EventWorkflowTimeout{})
		w.cleanupIfFinalized(exec)
	}
	w.takeOverForEvent(ctx, msg, executionID, deadlineHolder(executionID, string(msg.Payload)), timeout)
}

func (w *WorkflowInstance) takeOverForEvent(ctx context.Context, msg *broker.Message, executionID, heldBy string, apply func(*WorkflowExecution)) {
	runs := w.runStore()
	if runs == nil {
		return
	}
	takeCtx, cancel := context.WithTimeout(ctx, runWriteTimeout)
	run, took, err := runs.TakeOverParked(takeCtx, w.cfg.Name, executionID, heldBy)
	cancel()
	switch {
	case err != nil:
		w.logger.Error("workflow: could not take over a parked run; returning the message to the queue",
			"executionID", executionID, "heldBy", heldBy, "error", err)
		msg.Nack()
	case took:
		w.continueRun(ctx, run, msg, heldBy, apply)
	case run != nil && run.Status == wfstore.RunHeld && run.HeldBy == heldBy:
		w.retakeHeldRun(ctx, executionID, heldBy, msg, apply)
	default:
		msg.Ack()
	}
}

func (w *WorkflowInstance) cancelParkedRun(ctx context.Context, taskID string, msg *broker.Message) {
	runs := w.runStore()
	if runs == nil {
		msg.Ack()
		return
	}
	readCtx, cancelRead := context.WithTimeout(ctx, runWriteTimeout)
	run, err := runs.LiveRunForTask(readCtx, w.cfg.Name, taskID)
	cancelRead()
	if err != nil || run == nil {
		if err != nil {
			w.logger.Warn("workflow: could not read the run of a cancelled task", "taskID", taskID, "error", err)
		}
		msg.Ack()
		return
	}
	cancel := func(exec *WorkflowExecution) {
		w.logger.Info("cancelling workflow execution", "taskID", taskID)
		_ = exec.Executor.Advance(w.ctx, &EventCancel{Reason: "client-requested"})
		w.cleanupIfFinalized(exec)
	}
	w.takeOverForEvent(ctx, msg, run.ExecutionID, cancelHolder(taskID, msg.ReplicationGroupMessageID), cancel)
}

func (w *WorkflowInstance) sweepHILTimeouts(ctx context.Context) {
	store := w.pausedStore()
	if store == nil || w.svc == nil || w.svc.Broker() == nil {
		return
	}
	rows, err := store.Expired(ctx, w.cfg.Name, time.Now().Add(-expiryBackstopMargin).UnixMilli(), workflowHILSweepBatch)
	if err != nil {
		w.logger.Warn("workflow HIL: timeout sweep query failed", "error", err)
		return
	}
	for i := range rows {
		payload, err := json.Marshal(a2a.UserInputResponseData{
			Type:      string(a2a.SignalUserInputResponse),
			RequestID: rows[i].RequestID,
			Status:    "timed_out",
		})
		if err != nil {
			continue
		}
		if err := w.svc.Broker().PublishGuaranteed(ctx, rows[i].RequesterAddr, &broker.Message{Payload: payload}); err != nil {
			w.logger.Warn("workflow HIL: could not publish a backstop timeout; retrying next sweep",
				"requestID", rows[i].RequestID, "error", err)
			continue
		}
		w.logger.Warn("workflow HIL: a pause outlived its deadline without a timeout from the notification service; sent one",
			"requestID", rows[i].RequestID, "executionID", rows[i].ExecutionID)
	}
}

func (w *WorkflowInstance) sweepParkedDeadlines(ctx context.Context) {
	runs := w.runStore()
	if runs == nil {
		return
	}
	ids, err := runs.ParkedPastDeadline(ctx, w.cfg.Name, time.Now().Add(-expiryBackstopMargin).UnixMilli(), workflowHILSweepBatch)
	if err != nil {
		w.logger.Warn("workflow: parked-run deadline sweep query failed", "error", err)
		return
	}
	for _, executionID := range ids {
		w.logger.Warn("workflow: a parked run outlived its deadline without its deadline message; sending one",
			"executionID", executionID)
		w.publishDeadline(executionID)
	}
}

type eventSettle struct{}

func (*eventSettle) isWorkflowEvent() {
}
