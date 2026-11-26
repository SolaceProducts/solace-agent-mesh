// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func (a *Agent) resumeOrphanPeerResponse(subTaskID string, msg *broker.Message) bool {
	return a.queueOrphanAdoption(peerAdoption(subTaskID, msg))
}

func peerAdoption(subTaskID string, msg *broker.Message) orphanAdoption {
	return orphanAdoption{
		corrID: subTaskID,
		source: "peer response",
		kinds:  []string{session.PendingKindPeer, session.PendingKindWorkflowPeer},
		result: func(*session.PendingCall) (*tool.Result, error) {
			result, _ := parsePeerResponse(peerNameFromMessage(msg), msg)
			return result, nil
		},
	}
}

func (a *Agent) queueOrphanAdoption(work orphanAdoption) bool {
	if a.checkpoints == nil {
		return false
	}
	if a.tryQueueOrphanAdoption(work) {
		return true
	}
	a.noteOrphanAdoptionRefused(work)
	return false
}

func (a *Agent) orphanQueueFor(work orphanAdoption) chan orphanAdoption {
	if work.userAnswer {
		return a.orphanAnswers
	}
	return a.orphanAdoptions
}

func (a *Agent) tryQueueOrphanAdoption(work orphanAdoption) bool {
	select {
	case a.orphanQueueFor(work) <- work:
		return true
	default:
		return false
	}
}

func (a *Agent) noteOrphanAdoptionRefused(work orphanAdoption) {
	queue := a.orphanQueueFor(work)
	a.logger.Debug("the orphan-adoption queue is full; this message was not queued",
		"corrID", work.corrID, "source", work.source, "queueDepth", len(queue), "queueCapacity", cap(queue))
	if work.userAnswer {
		return
	}
	if warn, refused := a.orphanRefusals.note(time.Now()); warn {
		a.logger.Warn("the orphan-adoption queue is full; replies and status messages for calls held by another replica were not queued. A call whose reply no replica adopts is resolved as timed out by the expiry reaper",
			"refused", refused, "latestSource", work.source, "queueDepth", len(queue), "queueCapacity", cap(queue))
	}
}

type orphanRefusalLog struct {
	mu sync.Mutex

	lastWarned time.Time
	refused    int
}

const orphanRefusalWarnInterval = time.Minute

func (l *orphanRefusalLog) note(now time.Time) (warn bool, refused int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refused++
	if !l.lastWarned.IsZero() && now.Sub(l.lastWarned) < orphanRefusalWarnInterval {
		return false, 0
	}
	refused = l.refused
	l.refused = 0
	l.lastWarned = now
	return true, refused
}

type orphanAdoption struct {
	corrID     string
	source     string
	kinds      []string
	result     func(pending *session.PendingCall) (*tool.Result, error)
	statusOnly bool
	userAnswer bool
	attempts   int
	onSettled  func(recorded bool)
}

func (w orphanAdoption) settle(recorded bool) {
	if w.onSettled != nil {
		w.onSettled(recorded)
	}
}

var errOrphanNotForThisReplica = errors.New("this replica cannot build a result for the reply")

const (
	orphanAdoptionMaxAttempts = 4
	orphanAdoptionRetryDelay  = 250 * time.Millisecond
)

type pendingCallExtender interface {
	ExtendPendingCall(ctx context.Context, agentName, corrID string, until time.Time) error
}

const peerStatusExtendInterval = 30 * time.Second

func (a *Agent) noteOrphanPeerStatus(subTaskID string) {
	if a.checkpoints == nil || !a.checkpoints.claimStatusExtension(subTaskID, time.Now()) {
		return
	}
	work := orphanAdoption{corrID: subTaskID, source: "peer status", statusOnly: true}
	if !a.tryQueueOrphanAdoption(work) {
		a.checkpoints.releaseStatusExtension(subTaskID)
		a.noteOrphanAdoptionRefused(work)
	}
}

func (a *Agent) extendOrphanPeerCall(subTaskID string) {
	ctx, cancel := detach(context.Background())
	defer cancel()

	pending, err := a.checkpoints.lookup(ctx, subTaskID)
	if err != nil {
		a.logger.Warn("failed to look up pending call for a peer status", "subTaskID", subTaskID, "error", err)
		return
	}
	extension := time.Duration(0)
	if pending != nil {
		extension = a.checkpoints.delegationExpiry(pending.Kind)
	}
	if extension == 0 || pending.ExpiresAt == nil {
		return
	}
	extender, ok := a.checkpoints.store.(pendingCallExtender)
	if !ok {
		return
	}
	if err := extender.ExtendPendingCall(ctx, a.cfg.Name, subTaskID, time.Now().Add(extension)); err != nil {
		a.logger.Warn("failed to extend the expiry of a pending call whose peer is still reporting status",
			"traceID", pending.TraceID, "taskID", pending.TaskID, "subTaskID", subTaskID, "error", err)
	}
}

const (
	orphanAdoptionQueueDepth = 192
	orphanAnswerQueueDepth   = 64
	orphanAdoptionWorkers    = 4
)

func (a *Agent) runOrphanAdoptions(ctx context.Context) {
	for {
		work, ok := a.nextOrphanWork(ctx)
		if !ok {
			if queued := len(a.orphanAnswers) + len(a.orphanAdoptions); queued > 0 {
				a.logger.Warn("shutting down with replies still queued for adoption; they will be resolved by the expiry reaper instead of resuming their tasks",
					"queued", queued)
			}
			return
		}
		if work.statusOnly {
			a.extendOrphanPeerCall(work.corrID)
			continue
		}
		a.adoptOrphanReply(work)
	}
}

func (a *Agent) nextOrphanWork(ctx context.Context) (orphanAdoption, bool) {
	select {
	case work := <-a.orphanAnswers:
		return work, true
	default:
	}
	select {
	case <-ctx.Done():
		return orphanAdoption{}, false
	case work := <-a.orphanAnswers:
		return work, true
	case work := <-a.orphanAdoptions:
		return work, true
	}
}

func (a *Agent) adoptOrphanReply(work orphanAdoption) {
	switch a.tryAdoptOrphanReply(work) {
	case orphanHandled:
		work.settle(true)
	case orphanNotForThisReplica:
		work.settle(false)
	case orphanRetry:
		a.retryOrphanAdoption(work)
	}
}

type orphanOutcome int

const (
	orphanHandled orphanOutcome = iota
	orphanRetry
	orphanNotForThisReplica
)

func (a *Agent) tryAdoptOrphanReply(work orphanAdoption) orphanOutcome {
	ctx, cancel := detach(context.Background())
	defer cancel()
	subTaskID := work.corrID

	pending, err := a.checkpoints.lookup(ctx, subTaskID)
	if err != nil {
		a.logger.Warn("failed to look up pending call for an orphaned reply",
			"subTaskID", subTaskID, "source", work.source, "error", err)
		return orphanRetry
	}
	if pending == nil {
		a.logger.Debug("no checkpoint awaits this reply", "subTaskID", subTaskID, "source", work.source)
		return orphanHandled
	}

	if pending.AgentName != "" && pending.AgentName != a.cfg.Name {
		a.logger.Warn("refusing a reply whose pending call belongs to another agent",
			"traceID", pending.TraceID, "subTaskID", subTaskID, "taskID", pending.TaskID,
			"pendingAgentName", pending.AgentName)
		return orphanHandled
	}

	if !slices.Contains(work.kinds, pending.Kind) {
		a.logger.Warn("refusing a reply whose pending call waits for something else",
			"traceID", pending.TraceID, "corrID", subTaskID, "source", work.source, "taskID", pending.TaskID, "pendingKind", pending.Kind)
		return orphanHandled
	}

	if a.pausedTasks.Load(pending.TaskID) != nil {
		return orphanHandled
	}

	result, err := work.result(pending)
	if err != nil {
		if errors.Is(err, errOrphanNotForThisReplica) {
			a.logger.Warn("this replica cannot build the result for an orphaned reply; leaving it for another replica or the expiry reaper",
				"traceID", pending.TraceID, "corrID", subTaskID, "source", work.source, "taskID", pending.TaskID, "error", err)
			return orphanNotForThisReplica
		}
		a.logger.Warn("could not build the result for an orphaned reply; will retry",
			"traceID", pending.TraceID, "corrID", subTaskID, "source", work.source, "taskID", pending.TaskID, "error", err)
		return orphanRetry
	}
	if result == nil {
		a.logger.Debug("an orphaned reply has no result to record here", "corrID", subTaskID, "source", work.source, "taskID", pending.TaskID)
		return orphanHandled
	}
	logger := a.logger.With("traceID", pending.TraceID, "taskID", pending.TaskID, "subTaskID", subTaskID)
	logger.Debug("adopting a reply for a task paused on another replica", "source", work.source)

	if a.adoptCheckpointedTask(ctx, pending, subTaskID, result, logger) {
		return orphanHandled
	}
	return orphanRetry
}

func (a *Agent) retryOrphanAdoption(work orphanAdoption) {
	work.attempts++
	if work.attempts >= orphanAdoptionMaxAttempts {
		a.logGaveUpOnOrphanReply(work)
		work.settle(false)
		return
	}
	time.AfterFunc(orphanAdoptionRetryDelay, func() { a.requeueOrphanAdoption(work) })
}

func (a *Agent) requeueOrphanAdoption(work orphanAdoption) {
	if a.stopping.Load() {
		work.settle(false)
		return
	}
	if a.tryQueueOrphanAdoption(work) {
		return
	}
	work.settle(false)
	a.noteOrphanAdoptionRefused(work)
	if work.userAnswer {
		a.logger.Warn("could not queue a user's answer for another recording attempt; the task's holder may still apply it, otherwise the task waits out its input timeout",
			"corrID", work.corrID, "source", work.source, "attempts", work.attempts)
	}
}

func (a *Agent) logGaveUpOnOrphanReply(work orphanAdoption) {
	a.logger.Error("giving up on recording an orphaned reply; the expiry reaper will resolve the call",
		"corrID", work.corrID, "source", work.source, "attempts", work.attempts)
}

func (a *Agent) adoptCheckpointedTask(
	ctx context.Context,
	pending *session.PendingCall,
	corrID string,
	result *tool.Result,
	logger *slog.Logger,
) bool {
	remaining, err := a.checkpoints.store.RecordToolResult(ctx, pending.TaskID, corrID, a.checkpoints.toolCallResult(pending.TaskID, pending.ToolCallID, pending.ToolName, result))
	if errors.Is(err, session.ErrCallNotOutstanding) {
		if banked, readErr := a.checkpoints.store.GetCheckpoint(ctx, pending.TaskID); readErr == nil && banked != nil &&
			resultSupersedesBanked(banked.ToolResults, pending.ToolCallID, result) {
			logger.Warn("a reply arrived after this call had already been resolved as timed out; the turn will report the timeout instead",
				"toolCallID", pending.ToolCallID)
			return true
		}
		logger.Debug("orphaned result arrived for a call already resolved elsewhere; ignoring it")
		return true
	}
	if err != nil {
		logger.Warn("failed to record an orphaned result against its checkpoint", "error", err)
		return false
	}
	if pending.Kind == session.PendingKindRemoteTool {
		a.reportAdoptedRemoteToolCall(ctx, pending, result)
	}

	if remaining > 0 {
		logger.Info("recorded orphaned result; task still waiting", "remainingCalls", remaining)
		return true
	}
	a.adoptIfResolved(ctx, pending.TaskID, logger)
	return true
}

func (c *taskCheckpointer) toolCallResult(taskID, toolCallID, toolName string, result *tool.Result) *session.ToolCallResult {
	return &session.ToolCallResult{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    boundedResultContent(resultMessage(result)),
		IsError:    resultIsError(result),
		Payload:    c.marshalResult(taskID, result),
	}
}

func recordWithRetries(ctx context.Context, record func() error) error {
	var err error
	for attempt := range orphanAdoptionMaxAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return err
			case <-time.After(orphanAdoptionRetryDelay):
			}
		}
		if err = record(); err == nil {
			return nil
		}
	}
	return err
}

func (a *Agent) adoptIfResolved(ctx context.Context, taskID string, logger *slog.Logger) {
	if a.stopping.Load() {
		return
	}
	now := time.Now()
	cp, err := a.checkpoints.store.AdoptCheckpoint(ctx, taskID, a.checkpoints.ownerID, adoptionCutoff(now), checkpointLease, a.checkpoints.takeoverWindow(), !a.mayTakeOver(now))
	if err != nil {
		logger.Warn("failed to adopt checkpoint for resume", "error", err)
		return
	}
	if cp == nil {
		logger.Debug("checkpoint not adoptable; its owner is still renewing it")
		return
	}
	if cp.Final != nil {
		a.publishAdoptedFinal(ctx, cp, logger)
		return
	}

	state, err := unmarshalTaskState(cp.State)
	if err != nil {
		a.dropUndecodableCheckpoint(ctx, taskID, err, logger)
		return
	}

	logger.Info("resuming checkpointed task on this replica")
	a.startAdoptedTask(ctx, state, cp.ToolResults, logger)
}

func (a *Agent) startAdoptedTask(
	ctx context.Context,
	state *TaskExecutionState,
	results []session.ToolCallResult,
	logger *slog.Logger,
) {
	a.applyCheckpointResults(state, results)
	a.checkpoints.restoreAuthToken(state, logger)
	a.rehydrateModelOverride(ctx, state, logger)
	state.checkpointed.Store(true)
	if a.runCtx != nil && a.runCtx.Err() != nil {
		logger.Info("not adopting a checkpoint during shutdown; leaving it for a live replica")
		a.checkpoints.releaseOwnership(context.WithoutCancel(ctx), state.TaskID)
		return
	}
	var stored map[string]bool
	if state.rebuildFromRows {
		own, err := a.adoptedTaskRows(ctx, state)
		if err != nil {
			logger.Warn("could not read an adopted task's stored turns; leaving it for a later adoption", "error", err)
			a.checkpoints.releaseOwnership(context.WithoutCancel(ctx), state.TaskID)
			return
		}
		state.restoreAdoptedRunCounters(own)
		stored = storedToolCallIDs(own)
	}

	release, claimed := markResuming(state)
	if !claimed {
		logger.Warn("not starting an adopted task that is already claimed", "taskID", state.TaskID)
		return
	}
	state.resumedFromPause.Store(true)
	a.claimRunningTask(ctx, session.RunningTask{
		TaskID: state.TaskID, AppName: a.cfg.Name, UserID: sessionUserID(state.UserProps.UserID), SessionID: state.EffectiveSessionID,
	}, logger)
	a.expectAdoptedPersonAnswers(state)
	a.pausedTasks.Store(state)
	go func() {
		defer a.endResumeClaim(state, release)
		if hasLoginToRetry(state) && a.retryLoginsOnAdoption(a.runCtx, state, stored, logger) {
			return
		}
		a.resumeTaskExecution(state)
	}()
}

func hasLoginToRetry(state *TaskExecutionState) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, pc := range state.PendingCalls {
		if pc.Kind == PendingCallAuth {
			return true
		}
	}
	return false
}

func isAuthAnsweredPayload(recorded session.ToolCallResult) bool {
	return isAuthAnswered(resultFromPayload(recorded.Payload))
}

func (a *Agent) applyCheckpointResults(state *TaskExecutionState, results []session.ToolCallResult) {
	byToolCallID := make(map[string]session.ToolCallResult, len(results))
	for _, r := range results {
		byToolCallID[r.ToolCallID] = r
	}

	for _, pc := range state.PendingCalls {
		recorded, found := byToolCallID[pc.ToolCallID]
		if !found {
			continue
		}
		if pc.Kind == PendingCallAuth && isAuthAnsweredPayload(recorded) {
			continue
		}
		if pc.Result == nil {
			full, decodeErr := decodeResultPayload(recorded.Payload)
			if decodeErr != nil {
				a.logger.Warn("a recorded tool result could not be decoded; using its message only",
					"taskID", state.TaskID, "toolCallID", recorded.ToolCallID, "error", decodeErr)
			}
			if full != nil {
				pc.Result = full
			} else if recorded.IsError {
				pc.Result = tool.Error(recorded.Content)
			} else {
				pc.Result = tool.OK(recorded.Content)
			}
		}
		state.SyncResults[pc.Index] = pc.Result
		delete(state.PendingCalls, pc.ToolCallID)
	}

	state.TotalPending = int32(len(state.PendingCalls))
}

func (a *Agent) rehydrateModelOverride(ctx context.Context, state *TaskExecutionState, logger *slog.Logger) {
	if state.ModelOverrideAlias == "" {
		return
	}
	override, errReason := a.resolveTaskModelOverride(ctx,
		map[string]any{"model_override": map[string]any{"model_id": state.ModelOverrideAlias}}, logger)
	if override == nil {
		logger.Warn("could not re-resolve the task's model override on resume; continuing on the agent default",
			"modelAlias", state.ModelOverrideAlias, "reason", errReason)
		return
	}

	priorEffort, priorBudget, priorCache := state.OverrideReasoningEffort, state.OverrideReasoningBudgetTokens, state.OverrideCacheStrategy
	state.setModelOverride(override)
	if state.OverrideReasoningEffort != priorEffort ||
		state.OverrideReasoningBudgetTokens != priorBudget ||
		state.OverrideCacheStrategy != priorCache {
		logger.Warn("the model binding changed while this task was paused; the resumed turns use the new settings",
			"modelAlias", state.ModelOverrideAlias,
			"reasoningEffortBefore", priorEffort, "reasoningEffortAfter", state.OverrideReasoningEffort,
			"reasoningBudgetBefore", priorBudget, "reasoningBudgetAfter", state.OverrideReasoningBudgetTokens,
			"cacheStrategyBefore", priorCache, "cacheStrategyAfter", state.OverrideCacheStrategy)
	}
}

const (
	checkpointReapInterval       = 30 * time.Second
	checkpointTakeoverAfter      = 4 * checkpointReapInterval
	checkpointReapBatch          = 128
	checkpointTickBudget         = 20 * time.Second
	checkpointLease              = 5 * time.Minute
	checkpointClockSkewAllowance = time.Minute
)

func adoptionCutoff(now time.Time) time.Time {
	return now.Add(-checkpointClockSkewAllowance)
}

func (a *Agent) maintainCheckpoints(ctx context.Context) {
	if a.checkpoints == nil {
		return
	}
	interval := a.checkpoints.interval
	if interval <= 0 {
		interval = checkpointReapInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			tickCtx, cancelTick := context.WithTimeout(ctx, checkpointTickBudget)
			a.renewOwnedLeases(tickCtx, now)
			a.reapExpiredPendingCalls(tickCtx, now)
			a.resumeAbandonedCheckpoints(tickCtx, now)
			a.checkpoints.retryFailedDiscards(tickCtx)
			a.collectRecordedAnswers(tickCtx)
			cancelTick()
		}
	}
}

func (a *Agent) renewOwnedLeases(ctx context.Context, now time.Time) {
	states := a.pausedTasks.Snapshot()
	taskIDs := make([]string, 0, len(states))
	for _, state := range states {
		taskIDs = append(taskIDs, state.TaskID)
	}
	renewed, err := a.checkpoints.store.RenewLeases(ctx, a.checkpoints.ownerID, taskIDs, now.Add(checkpointLease))
	a.checkpoints.recordRenewalTick(err == nil, now)
	if len(taskIDs) == 0 {
		if err != nil {
			a.logger.Warn("the checkpoint store did not answer the renewal tick; not taking over other processes' tasks until it does", "error", err)
		}
		return
	}
	if err != nil {
		a.logger.Error("failed to renew checkpoint leases; tasks paused here may be adopted elsewhere",
			"pausedTasks", len(taskIDs), "error", err)
		return
	}

	if renewed < len(taskIDs) {
		a.logger.Warn("lost ownership of paused tasks; another replica has adopted them",
			"pausedTasks", len(taskIDs), "renewed", renewed, "lost", len(taskIDs)-renewed)
		a.dropFencedTasks(ctx, taskIDs)
		return
	}
	a.logger.Debug("renewed checkpoint leases", "renewed", renewed, "pausedTasks", len(taskIDs))
}

func (a *Agent) dropFencedTasks(ctx context.Context, taskIDs []string) {
	for _, taskID := range taskIDs {
		state := a.pausedTasks.Load(taskID)
		if state == nil {
			continue
		}
		logger := a.logger.With("traceID", state.UserProps.TraceID, "taskID", taskID)

		owner, exists, err := a.checkpoints.store.CheckpointOwner(ctx, taskID)
		if err != nil {
			logger.Warn("could not determine checkpoint ownership; keeping the task local", "error", err)
			continue
		}

		switch {
		case !exists && !state.checkpointed.Load():
			logger.Warn("paused task has no checkpoint; continuing without durable backup")
			continue
		case !exists:
			logger.Warn("checkpoint was completed or cancelled elsewhere; standing down")
		case owner == "":
			if a.stopping.Load() {
				continue
			}
			reclaimed, reclaimErr := a.checkpoints.store.ReclaimUnowned(ctx, taskID,
				a.checkpoints.ownerID, time.Now().Add(checkpointLease))
			if reclaimErr != nil {
				logger.Warn("checkpoint is unowned and could not be reclaimed; keeping the task local",
					"error", reclaimErr)
				continue
			}
			if reclaimed {
				logger.Info("reclaimed ownership of an unowned checkpoint")
			} else {
				logger.Warn("could not reclaim an unowned checkpoint; another replica took it first")
			}
			continue
		case owner == a.checkpoints.ownerID:
			continue
		default:
			logger.Warn("another replica owns this task; standing down here", "newOwnerID", owner)
		}

		a.standDownLostTask(state)
	}
}

func (a *Agent) mayTakeOver(now time.Time) bool {
	return !a.stopping.Load() && a.checkpoints.mayTakeOver(now)
}

func (a *Agent) mayTakeOverRow(cp *session.TaskCheckpoint, now time.Time) bool {
	if cp.OwnerID == "" {
		return !a.stopping.Load()
	}
	return a.mayTakeOver(now)
}

func (a *Agent) standDownLostTask(state *TaskExecutionState) {
	a.fenceThenCancelActiveTask(state)
	state.mu.Lock()
	for _, pc := range state.PendingCalls {
		if pc.TimeoutTimer != nil {
			pc.TimeoutTimer.Stop()
		}
	}
	state.mu.Unlock()
	a.peerRouter.ReleaseTask(state.TaskID)
	a.seqGen.Drop(state.TaskID)
	a.forgetPausedTask(state.TaskID)
}

func (a *Agent) fenceThenCancelActiveTask(state *TaskExecutionState) {
	state.Fenced.Store(true)
	a.cancelActiveTask(state.TaskID)
}

func (a *Agent) cancelActiveTask(taskID string) {
	a.activeTasksMu.Lock()
	entry, ok := a.activeTasks[taskID]
	controls := a.controlEntriesForLocked(taskID)
	a.activeTasksMu.Unlock()
	if ok {
		entry.publishOnce.Do(func() {
			a.logger.Debug("stopping a running turn of a task another process now holds; withholding its response", "taskID", taskID, "traceID", entry.userProps.TraceID)
		})
		entry.cancel()
	}
	for _, control := range controls {
		control.cancel()
	}
}

func (a *Agent) reapExpiredPendingCalls(ctx context.Context, now time.Time) {
	if a.stopping.Load() {
		return
	}
	judgeOwners := a.mayTakeOver(now)
	expired, err := a.checkpoints.store.ListExpiredPendingCalls(ctx, a.cfg.Name, now, checkpointReapBatch)
	if err != nil {
		a.logger.Warn("failed to list expired pending calls", "error", err)
		return
	}
	for _, pending := range expired {
		if a.pausedTasks.Load(pending.TaskID) != nil {
			continue
		}
		logger := a.logger.With("traceID", pending.TraceID, "taskID", pending.TaskID,
			"corrID", pending.CorrID, "kind", pending.Kind)

		predatesToolCallIDColumn := pending.ToolCallID == ""
		if predatesToolCallIDColumn {
			if !judgeOwners {
				continue
			}
			logger.Error("discarding a task whose pending call predates tool_call_id; it cannot be resumed, and retrying it forever would only hide that")
			removed, delErr := a.checkpoints.store.DeleteAbandonedCheckpoint(ctx, pending.TaskID, adoptionCutoff(now))
			if delErr != nil {
				logger.Warn("failed to discard the unresumable task; leaving it for the next tick", "error", delErr)
				continue
			}
			if !removed {
				logger.Warn("a live replica holds the unresumable task; leaving it to decide")
			}
			continue
		}

		cp, readErr := a.checkpoints.store.GetCheckpoint(ctx, pending.TaskID)
		if readErr != nil {
			logger.Warn("could not read the checkpoint for an expired call; leaving it for the next tick", "error", readErr)
			continue
		}
		if cp == nil {
			logger.Warn("discarding an expired call whose checkpoint is already gone; nothing can record against it")
			if delErr := a.checkpoints.store.DeletePendingCall(ctx, pending.CorrID); delErr != nil {
				logger.Warn("failed to discard the orphaned correlation", "error", delErr)
			}
			continue
		}

		if !a.mayTakeOverRow(cp, now) || !a.checkpoints.ownerGone(cp, now) {
			logger.Debug("expired pending call still has a live owner; leaving it to their timer",
				"ownerId", cp.OwnerID)
			continue
		}

		logger.Warn("pending call expired with no response and its owner has stopped renewing; resolving as timed out")
		a.adoptCheckpointedTask(ctx, &pending, pending.CorrID, expiredCallResult(&pending, cp, logger), logger)
	}
}

func expiredCallResult(pending *session.PendingCall, cp *session.TaskCheckpoint, logger *slog.Logger) *tool.Result {
	if pending.Kind == session.PendingKindToolApproval {
		return pendingApprovalExpiry(pending)
	}
	if result := expiredQuestionResult(pending, cp, logger); result != nil {
		return result
	}
	return tool.Error("The call timed out waiting for a response, and the process that issued it is no longer running.")
}

func (a *Agent) resumeAbandonedCheckpoints(ctx context.Context, now time.Time) {
	if a.stopping.Load() {
		return
	}
	releasedOnly := !a.mayTakeOver(now)
	silentFor := a.checkpoints.takeoverWindow()
	resumable, err := a.checkpoints.store.ListResumableCheckpoints(ctx, a.cfg.Name, adoptionCutoff(now), silentFor, checkpointReapBatch, releasedOnly)
	if err != nil {
		a.logger.Warn("failed to list resumable checkpoints", "error", err)
		return
	}
	for i := range resumable {
		taskID := resumable[i].TaskID
		if a.pausedTasks.Load(taskID) != nil {
			continue
		}
		cp, adoptErr := a.checkpoints.store.AdoptCheckpoint(ctx, taskID, a.checkpoints.ownerID, adoptionCutoff(now), checkpointLease, silentFor, releasedOnly)
		if adoptErr != nil {
			a.logger.Warn("failed to adopt an abandoned checkpoint",
				"traceID", resumable[i].TraceID, "taskID", taskID, "error", adoptErr)
			continue
		}
		if cp == nil {
			continue
		}
		if cp.Final != nil {
			a.publishAdoptedFinal(ctx, cp, a.logger.With("traceID", cp.TraceID, "taskID", taskID))
			continue
		}
		state, stateErr := unmarshalTaskState(cp.State)
		if stateErr != nil {
			a.dropUndecodableCheckpoint(ctx, taskID, stateErr, a.logger.With("traceID", cp.TraceID, "taskID", taskID))
			continue
		}
		logger := a.logger.With("traceID", state.UserProps.TraceID, "taskID", taskID)
		logger.Info("resuming a checkpoint abandoned by a dead owner")
		a.startAdoptedTask(ctx, state, cp.ToolResults, logger)
	}
}

func (a *Agent) publishAdoptedFinal(ctx context.Context, cp *session.TaskCheckpoint, logger *slog.Logger) {
	logger.Info("publishing the final answer of a task whose owner stopped while answering it")
	if state, err := unmarshalTaskState(cp.State); err == nil && state.IsRunBased {
		if delErr := a.svc.Sessions().Delete(ctx, a.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID); delErr != nil {
			logger.Warn("failed to delete run-based session of an adopted answer", "sessionID", state.EffectiveSessionID, "error", delErr)
		}
	}
	msg := &broker.Message{Payload: cp.Final.Payload, UserProperties: cp.Final.UserProperties}
	if err := a.publishFinalMessage(ctx, cp.Final.Topic, msg, logger); err != nil {
		return
	}
	if _, err := a.checkpoints.store.DeleteCheckpointIfOwned(ctx, cp.TaskID, a.checkpoints.ownerID); err != nil {
		logger.Warn("published a stored final answer and could not delete its checkpoint; queueing a retry", "error", err)
		a.checkpoints.rememberFailedDiscard(cp.TaskID)
	}
}

func resultSupersedesBanked(banked []session.ToolCallResult, toolCallID string, arriving *tool.Result) bool {
	if resultIsError(arriving) {
		return false
	}
	for i := range banked {
		if banked[i].ToolCallID == toolCallID {
			return banked[i].IsError
		}
	}
	return false
}

func resultMessage(r *tool.Result) string {
	if r == nil {
		return ""
	}
	return r.Message
}

func resultIsError(r *tool.Result) bool {
	return r != nil && (r.Status == tool.StatusError || r.Status == tool.StatusFailed)
}

func peerNameFromMessage(msg *broker.Message) string {
	if msg == nil || msg.UserProperties == nil {
		return "peer"
	}
	if name := msg.UserProperties["agent_name"]; name != "" {
		return name
	}
	return "peer"
}

func (a *Agent) dropUndecodableCheckpoint(ctx context.Context, taskID string, decodeErr error, logger *slog.Logger) {
	if errors.Is(decodeErr, errCheckpointFromNewerBuild) {
		logger.Warn("checkpoint written by a newer build; releasing it for a process that can read it", "error", decodeErr)
		if err := a.checkpoints.store.ReleaseOwnership(ctx, taskID, a.checkpoints.ownerID); err != nil {
			logger.Warn("failed to release ownership", "error", err)
		}
		return
	}
	logger.Error("removing a checkpoint no build can read; its task cannot be resumed", "error", decodeErr)
	if _, err := a.checkpoints.store.DeleteCheckpointIfOwned(ctx, taskID, a.checkpoints.ownerID); err != nil {
		logger.Warn("failed to remove the unreadable checkpoint", "error", err)
	}
}

func (a *Agent) adoptedTaskRows(ctx context.Context, state *TaskExecutionState) ([]session.Message, error) {
	ctx, cancel := detach(ctx)
	defer cancel()
	sess, err := a.svc.Sessions().Get(ctx, a.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID)
	if err != nil || sess == nil {
		return nil, err
	}
	return ownRows(sess.Messages, state.TaskID), nil
}

func storedToolCallIDs(own []session.Message) map[string]bool {
	stored := map[string]bool{}
	for _, m := range own {
		if m.Role == "tool" && m.ToolCallID != "" {
			stored[m.ToolCallID] = true
		}
	}
	return stored
}
