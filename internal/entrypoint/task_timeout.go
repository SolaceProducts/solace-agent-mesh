// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const defaultTaskTimeoutSeconds = 300

const defaultTaskHardTimeoutSeconds = 3600

const TaskTimeoutErrorCode = -32001

const TaskAgentNotAvailableErrorCode = -32031

const TaskTimeoutWarningType = "gateway_timeout_warning"

func IsTaskTimeoutError(err *a2a.Error) bool {
	if err == nil {
		return false
	}
	if err.Code == TaskTimeoutErrorCode {
		return true
	}
	if data, ok := err.Data.(map[string]any); ok {
		if status, _ := data["taskStatus"].(string); status == "timeout" {
			return true
		}
	}
	return false
}

const (
	minSweepInterval = time.Millisecond
	maxSweepInterval = time.Second
)

const (
	timerCallbackWorkers  = 8
	timerCallbackDeadline = 10 * time.Second
)

const unreadRowRetry = time.Minute

type taskTimerEntry struct {
	softDeadline time.Time
	fireSoft     func(context.Context)
	softFired    bool
	hardDeadline time.Time
	fireHard     func(context.Context)
	interval     time.Duration
}

type taskTimerRegistry struct {
	mu      sync.Mutex
	timers  map[string]*taskTimerEntry
	running bool
	stopped bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func newTaskTimerRegistry() *taskTimerRegistry {
	return &taskTimerRegistry{
		timers: make(map[string]*taskTimerEntry),
		stopCh: make(chan struct{}),
	}
}

func clampSweepInterval(timeout time.Duration) time.Duration {
	iv := timeout / 10
	if iv < minSweepInterval {
		iv = minSweepInterval
	}
	if iv > maxSweepInterval {
		iv = maxSweepInterval
	}
	return iv
}

func (r *taskTimerRegistry) startOrReset(taskID string, soft time.Duration, fireSoft func(context.Context), hard time.Duration, fireHard func(context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}

	now := time.Now()
	e := &taskTimerEntry{}
	shortest := time.Duration(0)
	if soft > 0 && fireSoft != nil {
		e.softDeadline = now.Add(soft)
		e.fireSoft = fireSoft
		shortest = soft
	}
	if hard > 0 && fireHard != nil {
		e.hardDeadline = now.Add(hard)
		e.fireHard = fireHard
		if shortest == 0 || hard < shortest {
			shortest = hard
		}
	}
	if shortest == 0 {
		delete(r.timers, taskID)
		return
	}
	e.interval = clampSweepInterval(shortest)
	r.timers[taskID] = e
	r.ensureSweeperLocked()
}

func (r *taskTimerRegistry) ensureSweeperLocked() {
	if r.running || r.stopped {
		return
	}
	r.running = true
	r.wg.Add(1)
	go r.sweepLoop()
}

func (r *taskTimerRegistry) sweepLoop() {
	defer r.wg.Done()
	for {
		r.mu.Lock()
		interval := r.nextIntervalLocked()
		r.mu.Unlock()

		select {
		case <-r.stopCh:
			return
		case <-time.After(interval):
		}

		now := time.Now()
		r.mu.Lock()
		var expired []func(context.Context)
		for id, e := range r.timers {
			if e.fireSoft != nil && !e.softFired && !e.softDeadline.After(now) {
				expired = append(expired, e.fireSoft)
				e.softFired = true
			}
			if e.fireHard != nil && !e.hardDeadline.After(now) {
				expired = append(expired, e.fireHard)
				delete(r.timers, id)
				continue
			}
			if e.fireHard == nil && e.softFired {
				delete(r.timers, id)
			}
		}
		idle := len(r.timers) == 0
		if idle {
			r.running = false
		}
		r.mu.Unlock()

		r.runCallbacks(expired)
		if idle {
			return
		}
	}
}

func (r *taskTimerRegistry) runCallbacks(expired []func(context.Context)) {
	if len(expired) == 0 {
		return
	}
	slots := make(chan struct{}, timerCallbackWorkers)
	var wg sync.WaitGroup
	for _, fire := range expired {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			ctx, cancel := context.WithTimeout(context.Background(), timerCallbackDeadline)
			defer cancel()
			fire(ctx)
		})
	}
	wg.Wait()
}

func (r *taskTimerRegistry) nextIntervalLocked() time.Duration {
	iv := maxSweepInterval
	for _, e := range r.timers {
		if e.interval < iv {
			iv = e.interval
		}
	}
	return iv
}

func (r *taskTimerRegistry) armHardIfAbsent(taskID string, hard time.Duration, fireHard func(context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || hard <= 0 || fireHard == nil {
		return
	}
	if _, armed := r.timers[taskID]; armed {
		return
	}
	r.timers[taskID] = &taskTimerEntry{hardDeadline: time.Now().Add(hard), fireHard: fireHard, interval: clampSweepInterval(hard)}
	r.ensureSweeperLocked()
}

func (r *taskTimerRegistry) cancel(taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.timers, taskID)
}

func (r *taskTimerRegistry) clearAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.timers)
}

const stopDrainTimeout = 5 * time.Second

func (r *taskTimerRegistry) stopAll() {
	r.mu.Lock()
	clear(r.timers)
	if !r.stopped {
		r.stopped = true
		close(r.stopCh)
	}
	r.mu.Unlock()

	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopDrainTimeout):
	}
}

func (g *GatewayCore) startTaskTimeout(taskID string) {
	if g.cfg == nil || g.cfg.TaskTimeoutSeconds <= 0 || g.taskTimers == nil || g.tasks == nil {
		return
	}
	if !g.eventBindActive.Load() {
		g.logger.Debug("idle timer not armed: event queue bind not active on this instance", "taskID", taskID)
		return
	}
	if !g.tasks.Holds(taskID) {
		g.logger.Debug("idle timer not armed: task not held by this instance", "taskID", taskID)
		return
	}
	info := g.tasks.Get(taskID)
	waiting := info != nil && info.WaitingRequestID != ""
	idleTimeout := time.Duration(g.cfg.TaskTimeoutSeconds) * time.Second

	idleSec := g.cfg.TaskTimeoutSeconds
	hardSec := g.cfg.TaskHardTimeoutSeconds
	hardTimeout := time.Duration(hardSec) * time.Second
	if waiting {
		g.taskTimers.startOrReset(taskID,
			0, nil,
			hardTimeout, func(ctx context.Context) { g.onTaskHardTimeout(ctx, taskID, hardSec) })
		return
	}
	g.taskTimers.startOrReset(taskID,
		idleTimeout, func(ctx context.Context) { g.onTaskIdleWarning(ctx, taskID, idleSec) },
		hardTimeout, func(ctx context.Context) { g.onTaskHardTimeout(ctx, taskID, hardSec) })
}

func (g *GatewayCore) armHardTimeoutIfAbsent(taskID string) {
	if g.cfg == nil || g.cfg.TaskTimeoutSeconds <= 0 || g.taskTimers == nil {
		return
	}
	hardSec := g.cfg.TaskHardTimeoutSeconds
	g.taskTimers.armHardIfAbsent(taskID, time.Duration(hardSec)*time.Second,
		func(ctx context.Context) { g.onTaskHardTimeout(ctx, taskID, hardSec) })
}

func (g *GatewayCore) taskTimerState(taskID string) (running, mine, known bool) {
	reader, ok := g.tasks.(interface {
		taskRecord(string) (*store.TaskRecord, error)
	})
	if !ok {
		running, known = g.tasks.RunState(taskID)
		return running, true, known
	}
	row, err := reader.taskRecord(taskID)
	if err != nil {
		return false, false, false
	}
	if row == nil {
		return false, false, true
	}
	return true, row.GatewayInstanceID == "" || row.GatewayInstanceID == g.instanceID, true
}

func (g *GatewayCore) startTaskTimeoutFromEvent(taskID string) {
	if taker, ok := g.tasks.(taskRecordTaker); ok && g.eventBindActive.Load() {
		if !g.tasks.Holds(taskID) {
			taker.take(taskID, g.instanceID)
		} else if info := g.tasks.Get(taskID); info != nil && info.WaitingRequestID != "" {
			taker.refreshWaiting(taskID)
		}
	}
	g.startTaskTimeout(taskID)
}

func (g *GatewayCore) cancelTaskTimeout(taskID string) {
	if g.taskTimers == nil {
		return
	}
	g.taskTimers.cancel(taskID)
}

func (g *GatewayCore) pauseTaskTimeoutForUserInput(taskID, requestID string) {
	if requestID == "" || g.tasks == nil {
		g.cancelTaskTimeout(taskID)
		return
	}
	g.tasks.SetWaiting(taskID, requestID)
	g.startTaskTimeout(taskID)
}

func (g *GatewayCore) onTaskIdleWarning(ctx context.Context, taskID string, idleSec int) {
	if g.cfg == nil || !g.eventBindActive.Load() {
		return
	}
	if g.skipEndingTask(taskID) {
		return
	}

	running, mine, known := g.taskTimerState(taskID)
	if !known {
		g.logger.Debug("still-working notice skipped: task state could not be read", "taskID", taskID)
		return
	}
	if !running || !mine {
		return
	}
	info := g.tasks.Get(taskID)
	if info == nil {
		return
	}

	g.taskScopedLogger(taskID).Info("task idle past soft timeout — surfacing recoverable warning, not cancelling",
		"timeout_seconds", idleSec,
	)

	warn := map[string]any{
		"type":    TaskTimeoutWarningType,
		"message": "The agent is taking longer than expected. Still working…",
	}
	event := a2a.NewStatusUpdateEvent(taskID, info.SessionID, []a2a.Part{a2a.NewDataPart(warn)}, false, nil)
	raw, err := json.Marshal(a2a.NewResponse(taskID, event))
	if err != nil {
		g.taskScopedLogger(taskID).Warn("failed to marshal idle-timeout warning", "error", err)
		return
	}
	g.relayTaskEvent(ctx, taskID, relayedStatus, raw)
	g.adapter.OnStatusUpdate(ctx, taskID, event, raw)
}

func (g *GatewayCore) onTaskHardTimeout(ctx context.Context, taskID string, timeoutSec int) {
	if g.cfg == nil || !g.eventBindActive.Load() {
		return
	}
	if g.skipEndingTask(taskID) {
		return
	}
	running, mine, known := g.taskTimerState(taskID)
	if !known {
		g.taskScopedLogger(taskID).Debug("hard timeout deferred: task state could not be read", "retryIn", unreadRowRetry)
		g.taskTimers.armHardIfAbsent(taskID, unreadRowRetry, func(ctx context.Context) { g.onTaskHardTimeout(ctx, taskID, timeoutSec) })
		return
	}
	if !running {
		g.logger.Debug("task ended before its timeout fired", "taskID", taskID)
		g.ForgetTask(taskID)
		return
	}
	if !mine {
		g.logger.Debug("task timer dropped: another instance holds the task", "taskID", taskID)
		g.cancelTaskTimeout(taskID)
		return
	}

	g.taskScopedLogger(taskID).Warn("task timed out — no agent activity",
		"timeout_seconds", timeoutSec,
	)

	g.endTaskWithError(ctx, taskID, &a2a.Error{
		Code:    TaskTimeoutErrorCode,
		Message: "The request timed out. Please try again.",
		Data: map[string]any{
			"taskStatus":      "timeout",
			"timeout_seconds": timeoutSec,
		},
	})
}

func (g *GatewayCore) onTaskAgentNotAvailable(ctx context.Context, taskID string) {
	if g.cfg == nil || !g.eventBindActive.Load() || g.skipEndingTask(taskID) {
		return
	}
	if running, mine, known := g.taskTimerState(taskID); !known || !running || !mine {
		return
	}
	g.taskScopedLogger(taskID).Warn("task ended: its agent is not deployed", "agentName", g.tasks.AgentName(taskID))
	g.endTaskWithError(ctx, taskID, &a2a.Error{
		Code:    TaskAgentNotAvailableErrorCode,
		Message: AgentNotAvailableText,
		Data:    map[string]any{"taskStatus": "agent_not_available"},
	})
}

func (g *GatewayCore) endTaskWithError(ctx context.Context, taskID string, a2aErr *a2a.Error) {
	agentName := g.tasks.AgentName(taskID)
	var traceID string
	if info := g.tasks.Get(taskID); info != nil {
		traceID = info.TraceID
	}

	errResp := a2a.NewErrorResponse(taskID, a2aErr.Code, a2aErr.Message)
	raw, marshalErr := json.Marshal(errResp)
	if marshalErr != nil {
		raw = nil
	}

	g.relayTaskEvent(ctx, taskID, relayedResponse, raw)
	g.adapter.OnError(ctx, taskID, a2aErr, raw)
	g.fireTaskCompleteHooks(taskID, nil, a2aErr)
	g.endScheduledRun(ctx, taskID, nil, a2aErr)

	if tl := g.taskLogger.Load(); tl != nil {
		tl.LogEvent(TaskLogEvent{
			TaskID:      taskID,
			TraceID:     traceID,
			Topic:       "",
			Direction:   "error",
			Payload:     raw,
			EventKind:   "status-update",
			IsFinal:     true,
			FinalStatus: "failed",
		})
	}

	if agentName != "" {
		if cancelErr := g.CancelTask(context.WithoutCancel(ctx), taskID, agentName); cancelErr != nil {
			g.taskScopedLogger(taskID).Warn("failed to cancel ended task",
				"error", cancelErr,
			)
		}
	} else {
		g.taskScopedLogger(taskID).Warn("ended task not cancelled at the agent: agent name unknown")
	}

	g.ForgetTask(taskID)
	g.cancelTaskTimeout(taskID)
}
