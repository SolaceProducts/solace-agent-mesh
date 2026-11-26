// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	runningTaskLease         = 2 * time.Minute
	runningTaskRenewInterval = 30 * time.Second
	runningTaskStoreTimeout  = 5 * time.Second
)

type runningTasks struct {
	ownerID string

	mu    sync.Mutex
	tasks map[string]session.RunningTask
}

func newRunningTasks() *runningTasks {
	return &runningTasks{
		ownerID: idgen.New(),
		tasks:   make(map[string]session.RunningTask),
	}
}

func (r *runningTasks) releasedSince(earlier []session.RunningTask) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var released []string
	for _, t := range earlier {
		if _, held := r.tasks[t.TaskID]; !held {
			released = append(released, t.TaskID)
		}
	}
	return released
}

func (r *runningTasks) add(task session.RunningTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[task.TaskID] = task
}

func (r *runningTasks) forget(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, held := r.tasks[taskID]
	delete(r.tasks, taskID)
	return held
}

func (r *runningTasks) snapshot() []session.RunningTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Collect(maps.Values(r.tasks))
}

func (a *Agent) claimRunningTask(ctx context.Context, task session.RunningTask, logger *slog.Logger) {
	store := a.svc.Sessions()
	if store == nil {
		return
	}
	a.runningTasks.add(task)
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runningTaskStoreTimeout)
	defer cancel()
	if err := store.RegisterRunningTask(storeCtx, task, a.runningTasks.ownerID, time.Now().Add(runningTaskLease)); err != nil {
		logger.Warn("failed to record running task; concurrent tasks in this session see it as interrupted until the next renewal",
			"error", err)
	}
}

func (a *Agent) releaseRunningTask(taskID string) {
	if !a.runningTasks.forget(taskID) {
		return
	}
	a.removeRunningTaskRow(taskID)
}

func (a *Agent) removeRunningTaskRow(taskID string) {
	store := a.svc.Sessions()
	if store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), runningTaskStoreTimeout)
	defer cancel()
	if err := store.RemoveRunningTask(ctx, taskID, a.runningTasks.ownerID); err != nil {
		a.logger.Warn("failed to remove running task; it stays listed until its lease lapses",
			"taskID", taskID, "error", err)
	}
}

func (a *Agent) forgetPausedTask(taskID string) {
	if state := a.pausedTasks.Load(taskID); state != nil {
		state.releaseRequestHold()
	}
	a.pausedTasks.Delete(taskID)
	a.forgetEarlyAnswers(taskID)
	a.releaseRunningTask(taskID)
}

func (a *Agent) maintainRunningTasks(ctx context.Context) {
	ticker := time.NewTicker(runningTaskRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			a.renewRunningTasks(ctx, now)
			a.sweepLapsedRunningTasks(ctx, now)
		}
	}
}

func (a *Agent) renewRunningTasks(ctx context.Context, now time.Time) {
	store := a.svc.Sessions()
	if store == nil {
		return
	}
	tasks := a.runningTasks.snapshot()
	if len(tasks) == 0 {
		return
	}
	renewCtx, cancel := context.WithTimeout(ctx, runningTaskStoreTimeout)
	defer cancel()
	renewed, err := store.RenewRunningTasks(renewCtx, a.runningTasks.ownerID, tasks, now.Add(runningTaskLease))
	a.removeRowsReleasedDuringRenewal(tasks)
	if err != nil {
		a.logger.Warn("failed to renew running-task leases; concurrent tasks may treat this replica's work as interrupted",
			"runningTasks", len(tasks), "error", err)
		return
	}
	a.logger.Debug("renewed running-task leases", "runningTasks", len(tasks), "renewed", renewed, "notRenewed", len(tasks)-renewed)
}

func (a *Agent) removeRowsReleasedDuringRenewal(renewed []session.RunningTask) {
	for _, taskID := range a.runningTasks.releasedSince(renewed) {
		a.removeRunningTaskRow(taskID)
	}
}

func (a *Agent) sweepLapsedRunningTasks(ctx context.Context, now time.Time) {
	store := a.svc.Sessions()
	if store == nil {
		return
	}
	sweepCtx, cancel := context.WithTimeout(ctx, runningTaskStoreTimeout)
	defer cancel()
	removed, err := store.DeleteLapsedRunningTasks(sweepCtx, a.cfg.Name, adoptionCutoff(now))
	if err != nil {
		a.logger.Warn("failed to clear lapsed running-task claims", "error", err)
		return
	}
	if removed > 0 {
		a.logger.Debug("cleared lapsed running-task claims", "removed", removed)
	}
}

type siblingClaims struct {
	running    map[string]bool
	readFailed bool
}

func (a *Agent) readSiblingClaims(ctx context.Context, appName, userID, sessionID string, logger *slog.Logger) siblingClaims {
	store := a.svc.Sessions()
	if store == nil {
		return siblingClaims{}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, runningTaskStoreTimeout)
	defer cancel()
	ids, err := store.ListRunningTasks(lookupCtx, appName, userID, sessionID, adoptionCutoff(time.Now()))
	if err != nil {
		logger.Warn("failed to list running tasks; concurrent tasks in this session will look interrupted", "error", err)
		return siblingClaims{readFailed: true}
	}
	running := make(map[string]bool, len(ids))
	for _, id := range ids {
		running[id] = true
	}
	return siblingClaims{running: running}
}

func (a *Agent) historyTurnsFor(ctx context.Context, sess *session.Session, taskID string, stubBatchBytes int, claims siblingClaims, logger *slog.Logger) historyTurns {
	return historyTurns{
		currentTaskID:  taskID,
		liveSiblings:   a.liveSiblingTasks(ctx, sess, openSiblingTasks(sess.Messages, taskID), claims, logger),
		stubBatchBytes: stubBatchBytes,
	}
}

func (a *Agent) liveSiblingTasks(ctx context.Context, sess *session.Session, candidates map[string]bool, claims siblingClaims, logger *slog.Logger) map[string]bool {
	store := a.svc.Sessions()
	if len(candidates) == 0 || store == nil || claims.readFailed {
		return nil
	}
	live := make(map[string]bool, len(candidates))
	var unclaimed []string
	for id := range candidates {
		if claims.running[id] {
			live[id] = true
		} else {
			unclaimed = append(unclaimed, id)
		}
	}
	if a.checkpoints == nil || len(unclaimed) == 0 {
		return live
	}
	lookupCtx, cancel := context.WithTimeout(ctx, runningTaskStoreTimeout)
	defer cancel()
	checkpointed, err := store.CheckpointedTasks(lookupCtx, sess.SessionID, unclaimed)
	if err != nil {
		logger.Warn("failed to read concurrent tasks' checkpoints; paused ones will look interrupted", "error", err)
		return live
	}
	for _, id := range checkpointed {
		live[id] = true
	}
	return live
}

func (a *Agent) reloadSessionForRebuild(ctx context.Context, state *TaskExecutionState, rc *taskRunContext) {
	if rc.unstoredMessages > 0 {
		return
	}
	fresh, err := a.svc.Sessions().Get(ctx, rc.sess.AppName, rc.sess.UserID, rc.sess.SessionID)
	if err != nil || fresh == nil {
		rc.logger.Warn("could not reload the session before rebuilding history; concurrent tasks that finished since may look interrupted",
			"error", err)
		return
	}
	*rc.sess = *fresh
	rc.sessMetaFP = metadataFingerprint(fresh.Metadata)
	if state.unconfirmedAppend != nil {
		state.unconfirmedAppend.sess = nil
	}
}
