// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/scheduler"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const schedulerTick = 5 * time.Second

const sendTimeout = 30 * time.Second

const markTimeout = 5 * time.Second

const (
	sendPassBudget   = 10 * time.Second
	finishPassBudget = 10 * time.Second
)

const (
	budgetNoticeRetryInterval = time.Minute
	budgetNoticeRetryWindow   = 30 * time.Minute
)

const completionMarkWait = 30 * time.Second

const (
	skippedRunningMessage      = store.SkippedRunningNote
	skippedIdentityWaitMessage = store.SkippedIdentityWaitNote
	schedulePausedMessage      = "scheduled task is paused"
	timedOutMessage            = "execution timed out"
	neverSentMessage           = "execution was not sent to the agent before its timeout"
	ownerUnauthorizedMessage   = "owner no longer authorized"
	ownerDeactivatedMessage    = "owner's account is deactivated"
	ownerUnresolvedMessage     = "owner does not resolve to an active user account"
	ownerDeniedTargetMessage   = "owner not authorized to run the target agent"
	scheduleDeletedMessage     = "scheduled task was deleted"
)

func (s *ScheduledTaskService) RunPasses(ctx context.Context) error {
	return s.runPasses(ctx, time.Now())
}

func (s *ScheduledTaskService) runPasses(ctx context.Context, now time.Time) error {
	return errors.Join(
		s.firePass(ctx, now),
		s.timeoutPass(ctx, now),
		s.sendPass(ctx, now),
		s.finishPass(ctx, now),
	)
}

func (s *ScheduledTaskService) namespace() string {
	return s.core.Config().Namespace
}

func (s *ScheduledTaskService) timeoutFor(task *store.ScheduledTask) time.Duration {
	if task != nil && task.TimeoutSeconds > 0 {
		return time.Duration(task.TimeoutSeconds) * time.Second
	}
	return time.Duration(s.cfg.DefaultTimeoutSeconds) * time.Second
}

func (s *ScheduledTaskService) retryDelayFor(task *store.ScheduledTask) time.Duration {
	if task.RetryDelaySeconds > 0 {
		return time.Duration(task.RetryDelaySeconds) * time.Second
	}
	return time.Duration(s.cfg.RetryDelaySeconds) * time.Second
}

func (s *ScheduledTaskService) firePass(ctx context.Context, now time.Time) error {
	ns := s.namespace()
	never, err := s.repo.FindNeverFiredSchedules(ctx, ns)
	if err != nil {
		return fmt.Errorf("find never-fired schedules: %w", err)
	}
	for _, task := range never {
		next, nextErr := FirstRunAt(task, now, false)
		if nextErr != nil {
			s.logger.Error("scheduled task has an invalid schedule; it will not fire", "taskID", task.ID, "error", nextErr)
			if createErr := s.repo.CreateExecution(ctx, s.endedRun(task, now, "skipped", invalidScheduleMessage(nextErr), now)); createErr != nil {
				s.logger.Warn("failed to record a skipped fire", "taskID", task.ID, "error", createErr)
			}
			continue
		}
		if next == nil {
			continue
		}
		if _, setErr := s.repo.SetFirstRun(ctx, task.ID, *next); setErr != nil {
			s.logger.Warn("failed to write first fire time", "taskID", task.ID, "error", setErr)
		}
	}

	due, err := s.repo.FindDueSchedules(ctx, ns, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("find due schedules: %w", err)
	}
	var errs []error
	for _, task := range due {
		if err := s.fire(ctx, task, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *ScheduledTaskService) fire(ctx context.Context, task *store.ScheduledTask, now time.Time) error {
	dueMs := *task.NextRunAt
	due := time.UnixMilli(dueMs)
	grace := time.Duration(s.cfg.MisfireGraceTimeSeconds) * time.Second
	late := now.Sub(due)
	next, nextErr := s.nextRunAfter(task, due, now)

	var run, ifBusy *store.ScheduledTaskExecution
	switch {
	case nextErr != nil:
		s.logger.Error("scheduled task has an invalid schedule; it will not fire again", "taskID", task.ID, "error", nextErr)
		run = s.endedRun(task, due, "skipped", invalidScheduleMessage(nextErr), now)
	case late > grace:
		s.logger.Warn("scheduled fire skipped, later than its misfire grace",
			"taskID", task.ID, "dueAt", due, "late", late, "grace", s.cfg.MisfireGraceTimeSeconds)
		run = s.endedRun(task, due, "skipped", fmt.Sprintf("missed by %s, more than the misfire grace of %s", late.Round(time.Second), grace), now)
	default:
		run = s.pendingRun(task, due, 0, store.TriggerTypeScheduled, nil)
		ifBusy = s.endedRun(task, due, "skipped", s.skippedBusyMessage(ctx, task), now)
	}

	inserted, _, err := s.repo.FireDue(ctx, task.ID, dueMs, next, now.UnixMilli(), run, ifBusy)
	if err != nil {
		return fmt.Errorf("fire scheduled task %s: %w", task.ID, err)
	}
	switch {
	case inserted == nil:
	case inserted == ifBusy && *inserted.ErrorMessage == skippedIdentityWaitMessage:
		s.logger.Debug("skipping scheduled fire — an earlier run waits to confirm its identity", "taskID", task.ID)
	case inserted == ifBusy:
		s.logger.Warn("skipping scheduled fire — task already executing", "taskID", task.ID)
	case inserted.Status == "pending":
		s.emitUserNotification("execution_queued", map[string]any{
			"execution_id": inserted.ID,
			"task_id":      task.ID,
			"task_name":    task.Name,
			"trigger_type": inserted.TriggerType,
		}, s.resolveUserID(task), task.CreatedBy)
	}
	return nil
}

func invalidScheduleMessage(err error) string {
	return "invalid schedule: " + err.Error()
}

func (s *ScheduledTaskService) nextRunAfter(task *store.ScheduledTask, due, now time.Time) (*int64, error) {
	at, ok, err := scheduler.NextFireTime(scheduler.ScheduleType(task.ScheduleType),
		task.ScheduleExpression, task.Timezone, due, now)
	if err != nil || !ok {
		return nil, err
	}
	ms := at.UnixMilli()
	return &ms, nil
}

func unrunnableMessage(task *store.ScheduledTask, run *store.ScheduledTaskExecution) (string, bool) {
	switch {
	case task == nil || task.DeletedAt != nil:
		return scheduleDeletedMessage, true
	case !task.Enabled && run.TriggerType == store.TriggerTypeScheduled:
		return schedulePausedMessage, true
	}
	return "", false
}

func (s *ScheduledTaskService) skippedBusyMessage(ctx context.Context, task *store.ScheduledTask) string {
	if storedRunAs(task) == "" {
		return skippedRunningMessage
	}
	waiting, err := s.repo.HasUnsentExecutionNoted(ctx, task.ID, identityUnconfirmedText, platformUnreportedText)
	if err != nil {
		s.logger.Debug("failed to check whether an earlier run waits on its identity", "taskID", task.ID, "error", err)
		return skippedRunningMessage
	}
	if waiting {
		return skippedIdentityWaitMessage
	}
	return skippedRunningMessage
}

func (s *ScheduledTaskService) pendingRun(task *store.ScheduledTask, scheduledFor time.Time, retryCount int, triggerType string, triggeredBy *string) *store.ScheduledTaskExecution {
	a2aTaskID := idgen.New()
	return &store.ScheduledTaskExecution{
		ID:              idgen.New(),
		ScheduledTaskID: task.ID,
		Status:          "pending",
		A2ATaskID:       &a2aTaskID,
		ScheduledFor:    scheduledFor.UnixMilli(),
		RetryCount:      retryCount,
		TriggerType:     triggerType,
		TriggeredBy:     triggeredBy,
		TaskSnapshot:    buildTaskSnapshot(task, s.logger),
		RunAs:           task.RunAs,
	}
}

func (s *ScheduledTaskService) endedRun(task *store.ScheduledTask, scheduledFor time.Time, status, message string, now time.Time) *store.ScheduledTaskExecution {
	nowMs := now.UnixMilli()
	return &store.ScheduledTaskExecution{
		ID:              idgen.New(),
		ScheduledTaskID: task.ID,
		Status:          status,
		ScheduledFor:    scheduledFor.UnixMilli(),
		CompletedAt:     &nowMs,
		ErrorMessage:    &message,
		TriggerType:     store.TriggerTypeScheduled,
		TaskSnapshot:    buildTaskSnapshot(task, s.logger),
		FinalizedAt:     &nowMs,
	}
}

func (s *ScheduledTaskService) sendPass(ctx context.Context, now time.Time) error {
	runs, err := s.repo.FindUnsubmittedExecutions(ctx, s.namespace(), now.UnixMilli())
	if err != nil {
		return fmt.Errorf("find unsubmitted executions: %w", err)
	}
	deadline := time.Now().Add(cmp.Or(s.sendBudget, sendPassBudget))
	var identityDown error
	for _, run := range runs {
		if ctx.Err() != nil {
			return nil
		}
		awaitsOutage := identityDown != nil && stampedRunAs(run) != ""
		if time.Now().After(deadline) && !awaitsOutage {
			continue
		}
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
		if outage := s.send(sendCtx, run, now, identityDown); outage != nil {
			identityDown = outage
		}
		cancel()
	}
	return nil
}

func (s *ScheduledTaskService) send(ctx context.Context, run *store.ScheduledTaskExecution, now time.Time, lookupDown error) (outage error) {
	task, err := s.repo.GetByID(ctx, run.ScheduledTaskID)
	if err != nil {
		s.logger.Error("failed to load scheduled task for its run", "execID", run.ID, "taskID", run.ScheduledTaskID, "error", err)
		return
	}
	if message, unrunnable := unrunnableMessage(task, run); unrunnable {
		s.endRun(ctx, now, run, task, "skipped", message, false)
		return
	}
	if IsBudgetUnconfirmed(s.core.BudgetGate().Check("")) {
		s.waitForBudgetState(ctx, run)
		return
	}
	if waitedOutBudgetState(run) {
		s.noteUnsent(ctx, run, nil)
	}

	userID := s.resolveUserID(task)
	if userID == "" {
		s.logger.Warn("scheduled task has no owner — aborting", "taskID", task.ID, "execID", run.ID)
		s.endRun(ctx, now, run, task, "failed", noOwnerMessage, false)
		return
	}

	targetAgent := task.TargetAgentName
	if task.TargetType != "workflow" {
		var card *a2a.AgentCard
		if reg := s.core.AgentRegistry(); reg != nil {
			card = reg.FindByConfiguredName(task.TargetAgentName)
		}
		if card == nil && s.core.discoveryWarmingUp() {
			s.logger.Debug("scheduled task target not discovered yet; sending on a later pass",
				"taskID", task.ID, "execID", run.ID, "agentName", task.TargetAgentName)
			return
		}
		if card == nil {
			s.logger.Warn("scheduled task target agent unavailable — skipping fire",
				"taskID", task.ID, "execID", run.ID, "agentName", task.TargetAgentName)
			s.endRun(ctx, now, run, task, "skipped", fmt.Sprintf("agent unavailable: %q is not currently discoverable", task.TargetAgentName), false)
			return
		}
		targetAgent = card.Name
	}

	runAs := stampedRunAs(run)
	caller, idErr := s.runCaller(ctx, task, runAs, lookupDown)
	switch {
	case errors.Is(idErr, ErrRunAsDisabled):
		s.logger.Warn("scheduled task is set to run as a service account but run-as is turned off — aborting",
			"taskID", task.ID, "execID", run.ID, "runAs", runAs)
		s.endUncounted(ctx, now, run, task, store.EndRunAsDisabled, runAsDisabledMessage)
		return
	case waitsOnIdentity(idErr) && runAs != "":
		s.waitForIdentity(ctx, run, runAs, idErr, lookupDown != nil)
		if errors.Is(idErr, auth.ErrIdentityPlatformUnreachable) {
			return idErr
		}
		return nil
	case errors.Is(idErr, ErrIdentityUnauthorized) && runAs != "":
		s.logger.Warn("scheduled task run-as caller not authorized — aborting",
			"taskID", task.ID, "execID", run.ID, "createdByFP", strutil.PIIFingerprint(task.CreatedBy), "runAs", runAs, "error", idErr)
		s.endRun(ctx, now, run, task, "failed", runRefusalMessage(idErr), false)
		return
	case errors.Is(idErr, ErrIdentityUnauthorized):
		s.logger.Warn("scheduled task owner no longer authorized — aborting",
			append([]any{"taskID", task.ID, "execID", run.ID, "createdByFP", strutil.PIIFingerprint(task.CreatedBy), "error", idErr}, ownerRefusalAttrs(idErr)...)...)
		s.endRun(ctx, now, run, task, "failed", ownerRefusalMessage(idErr), false)
		return
	case idErr != nil:
		s.logger.Warn("identity resolution failed — will retry if attempts remain",
			"taskID", task.ID, "execID", run.ID, "error", idErr)
		s.endRun(ctx, now, run, task, "failed", identityResolutionFailedMessage, true)
		return
	}
	s.noteUnsent(ctx, run, nil)
	sessionID := s.runSession(ctx, task, run.ID, userID)

	tz, ok := NormalizeUserTimezone(task.Timezone)
	if !ok && task.Timezone != "" {
		s.logger.Warn("scheduled task timezone unusable at run time; using gateway default",
			"taskID", task.ID, "execID", run.ID, "timezone", task.Timezone)
	}
	a2aTaskID := *run.A2ATaskID
	msg := s.buildMessage(task, run.ID, sessionID)
	if err = s.core.SubmitTaskAttributedTo(ctx, targetAgent, a2aTaskID, msg, caller, runWireUserID(task, runAs), withNormalizedUserTimezone(tz)); err != nil {
		if IsAuthorizationDenial(err) {
			s.logger.Warn("scheduled task owner may not run its target — aborting",
				"taskID", task.ID, "execID", run.ID, "agentName", task.TargetAgentName, "runAs", runAs, "error", err)
			s.endRun(ctx, now, run, task, "failed", deniedTargetMessage(runAs), false)
			return
		}
		if IsAgentNotAvailable(err) {
			s.logger.Warn("scheduled task target is not deployed — aborting",
				"taskID", task.ID, "execID", run.ID, "agentName", task.TargetAgentName)
			s.endRun(ctx, now, run, task, "failed", AgentNotAvailableText, false)
			return
		}
		if IsBudgetUnconfirmed(err) {
			s.waitForBudgetState(ctx, run)
			return
		}
		if spent, ok := BudgetRefusal(err); ok {
			s.skipForSpentBudget(ctx, now, run, task, spent)
			return
		}
		s.logger.Warn("failed to submit scheduled task; the next pass sends it again",
			"taskID", task.ID, "execID", run.ID, "a2aTaskID", a2aTaskID, "error", err)
		return
	}
	markCtx, cancelMark := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer cancelMark()
	marked, err := s.repo.MarkSubmitted(markCtx, run.ID, now.UnixMilli())
	if err != nil {
		s.logger.Warn("failed to mark a sent scheduled run; the next pass sends it again",
			"execID", run.ID, "error", err)
		return
	}
	if !marked {
		return
	}
	s.emitUserNotification("execution_started", map[string]any{
		"execution_id": run.ID,
		"task_id":      task.ID,
		"task_name":    task.Name,
	}, userID, task.CreatedBy)
	s.logger.Info("scheduled task submitted",
		"taskID", task.ID, "userID", task.UserID, "execID", run.ID, "a2aTaskID", a2aTaskID,
		"agentName", task.TargetAgentName)
	return nil
}

func (s *ScheduledTaskService) runSession(ctx context.Context, task *store.ScheduledTask, execID, userID string) string {
	sessName := task.Name
	switch {
	case s.sessKeyRepo != nil:
		id, err := s.sessKeyRepo.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
			GatewayType: store.GatewayTypeScheduled,
			ExternalKey: execID,
			UserID:      userID,
			AgentID:     &task.TargetAgentName,
			Name:        &sessName,
			Source:      strPtr("scheduler"),
		})
		if err == nil {
			return id
		}
		s.logger.Warn("failed to create scheduler session + mapping", "execID", execID, "error", err)
	case s.sessRepo != nil:
		sess := &store.Session{
			ID:      idgen.New(),
			Name:    &sessName,
			UserID:  userID,
			AgentID: &task.TargetAgentName,
			Source:  strPtr("scheduler"),
		}
		if err := s.sessRepo.Create(ctx, sess); err != nil {
			s.logger.Warn("failed to create scheduler session", "sessionID", sess.ID, "error", err)
		}
		return sess.ID
	}
	return idgen.New()
}

func (s *ScheduledTaskService) endRun(ctx context.Context, now time.Time, run *store.ScheduledTaskExecution, task *store.ScheduledTask, status, message string, mayRetry bool) {
	errMsg := sanitizeError(message)
	s.endRunLogged(ctx, now, run, task, store.ExecutionEnd{Status: status, CompletedAt: now.UnixMilli(), ErrorMessage: &errMsg}, mayRetry)
}

func (s *ScheduledTaskService) endUncounted(ctx context.Context, now time.Time, run *store.ScheduledTaskExecution, task *store.ScheduledTask, cause store.EndCause, message string) {
	errMsg := sanitizeError(message)
	s.endRunLogged(ctx, now, run, task, store.ExecutionEnd{Status: "failed", CompletedAt: now.UnixMilli(), ErrorMessage: &errMsg, Cause: cause}, false)
}

func (s *ScheduledTaskService) endRunLogged(ctx context.Context, now time.Time, run *store.ScheduledTaskExecution, task *store.ScheduledTask, end store.ExecutionEnd, mayRetry bool) {
	if _, err := s.endRunWith(ctx, now, run, task, end, mayRetry); err != nil {
		s.logger.Error("failed to end scheduled run", "execID", run.ID, "runStatus", end.Status, "error", err)
	}
}

func (s *ScheduledTaskService) endRunWith(ctx context.Context, now time.Time, run *store.ScheduledTaskExecution, task *store.ScheduledTask, executionEnd store.ExecutionEnd, mayRetry bool) (store.RunEndResult, error) {
	end := store.RunEnd{
		ExecutionID:    run.ID,
		TaskID:         run.ScheduledTaskID,
		FromStatus:     run.Status,
		End:            executionEnd,
		AutoPauseAfter: s.cfg.AutoPauseAfterConsecutiveFailures,
	}
	retrying := task != nil && mayRetry && run.TriggerType == store.TriggerTypeScheduled
	if retrying && run.RetryCount < task.MaxRetries {
		end.Retry = s.pendingRun(task, now.Add(s.retryDelayFor(task)), run.RetryCount+1, store.TriggerTypeScheduled, nil)
	}
	res, err := s.repo.EndExecution(ctx, end)
	if err != nil || !res.Counted || task == nil {
		return res, err
	}
	switch {
	case end.Retry != nil:
		s.logger.Warn("retrying scheduled task",
			"taskID", task.ID, "attempt", end.Retry.RetryCount, "maxRetries", task.MaxRetries)
	case retrying && task.MaxRetries > 0:
		s.logger.Error("scheduled task exhausted all retries",
			"taskID", task.ID, "attempts", run.RetryCount+1, "maxRetries", task.MaxRetries)
	}
	if res.Paused {
		s.logger.Warn("auto-paused scheduled task after consecutive failures",
			"taskID", task.ID, "agentName", task.TargetAgentName,
			"consecutiveFailures", res.ConsecutiveFailures, "threshold", s.cfg.AutoPauseAfterConsecutiveFailures)
		s.emitUserNotification("task_auto_paused", map[string]any{
			"task_id":              task.ID,
			"task_name":            task.Name,
			"consecutive_failures": res.ConsecutiveFailures,
		}, s.resolveUserID(task), task.CreatedBy)
	}
	return res, nil
}

func (s *ScheduledTaskService) waitForBudgetState(ctx context.Context, run *store.ScheduledTaskExecution) {
	s.logger.Debug("scheduled task waits for the budget state; the next pass sends it again",
		"taskID", run.ScheduledTaskID, "execID", run.ID)
	note := BudgetUnconfirmedText
	s.noteUnsent(ctx, run, &note)
}

func (s *ScheduledTaskService) noteUnsent(ctx context.Context, run *store.ScheduledTaskExecution, note *string) {
	if (note == nil) == (run.ErrorMessage == nil) && (note == nil || *note == *run.ErrorMessage) {
		return
	}
	if err := s.repo.NoteUnsent(ctx, run.ID, note); err != nil {
		s.logger.Warn("failed to note why a scheduled run is unsent", "execID", run.ID, "error", err)
	}
}

func waitsOnIdentity(err error) bool {
	return errors.Is(err, auth.ErrIdentityLookupUnavailable) || errors.Is(err, auth.ErrSystemUserRowNotReported)
}

func identityWaitNote(cause error) string {
	if errors.Is(cause, auth.ErrSystemUserRowNotReported) {
		return platformUnreportedText
	}
	return identityUnconfirmedText
}

func (s *ScheduledTaskService) waitForIdentity(ctx context.Context, run *store.ScheduledTaskExecution, runAs string, cause error, inheritedOutage bool) {
	note := identityWaitNote(cause)
	if inheritedOutage {
		s.logger.Debug("scheduled run waits to confirm the identity it runs as; the platform was unreachable earlier in this pass",
			"taskID", run.ScheduledTaskID, "execID", run.ID, "runAs", runAs, "lookup", inheritedOutageLookup)
		s.noteUnsent(ctx, run, &note)
		return
	}
	fields := []any{"taskID", run.ScheduledTaskID, "execID", run.ID, "runAs", runAs, "lookup", identityLookupFailure(cause), "error", cause}
	if unsentWithNote(run, note) {
		s.logger.Debug("scheduled run still waits to confirm the identity it runs as; the next pass sends it again", fields...)
		return
	}
	s.logger.Warn("scheduled run waits to confirm the identity it runs as; the next pass sends it again", fields...)
	s.noteUnsent(ctx, run, &note)
}

const inheritedOutageLookup = "inherited_outage"

func identityLookupFailure(err error) string {
	switch {
	case errors.Is(err, auth.ErrSystemUserRowNotReported):
		return "system_user_row_not_reported"
	case errors.Is(err, auth.ErrSystemUserLookupUnavailable):
		return "system_user_row_unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	return "platform_lookup_failed"
}

func unsentWithNote(run *store.ScheduledTaskExecution, note string) bool {
	return run.Status == "pending" && run.SubmittedAt == nil && run.ErrorMessage != nil && *run.ErrorMessage == note
}

func waitedOutBudgetState(run *store.ScheduledTaskExecution) bool {
	return unsentWithNote(run, BudgetUnconfirmedText)
}

func waitedOutIdentity(run *store.ScheduledTaskExecution) bool {
	return unsentWithNote(run, identityUnconfirmedText)
}

func waitedOutPlatform(run *store.ScheduledTaskExecution) bool {
	return unsentWithNote(run, platformUnreportedText)
}

func (s *ScheduledTaskService) skipForSpentBudget(ctx context.Context, now time.Time, run *store.ScheduledTaskExecution, task *store.ScheduledTask, refusal *budget.ExhaustedError) {
	text := ScheduledRunSkippedText(refusal)
	end := store.ExecutionEnd{Status: "skipped", CompletedAt: now.UnixMilli(), ErrorMessage: &text, Cause: store.EndBudgetSpent}
	s.skipForBudget(ctx, now, run, task, end, SubmitFailureReason(refusal), BudgetRefusalTier(refusal))
}

func (s *ScheduledTaskService) skipUnconfirmedBudget(ctx context.Context, now time.Time, run *store.ScheduledTaskExecution, task *store.ScheduledTask) {
	text := BudgetUnconfirmedText
	end := store.ExecutionEnd{Status: "skipped", CompletedAt: now.UnixMilli(), ErrorMessage: &text, Cause: store.EndBudgetUnconfirmed}
	s.skipForBudget(ctx, now, run, task, end, taxonomy.RejectBudgetUnconfirmed, "")
}

func (s *ScheduledTaskService) skipForBudget(ctx context.Context, now time.Time, run *store.ScheduledTaskExecution, task *store.ScheduledTask, end store.ExecutionEnd, reason, tier string) {
	s.logger.Debug("scheduled task skipped by the budget gate",
		"taskID", run.ScheduledTaskID, "execID", run.ID, "reason", reason, "budgetTier", tier)
	if _, err := s.endRunWith(ctx, now, run, task, end, false); err != nil {
		s.logger.Error("failed to end scheduled run", "execID", run.ID, "runStatus", end.Status, "error", err)
	}
}

func (s *ScheduledTaskService) timeoutPass(ctx context.Context, now time.Time) error {
	runs, err := s.repo.FindOverdueExecutions(ctx, s.namespace(), now.UnixMilli(), s.timeoutFor(nil).Milliseconds())
	if err != nil {
		return fmt.Errorf("find overdue executions: %w", err)
	}
	for _, run := range runs {
		task, err := s.repo.GetByID(ctx, run.ScheduledTaskID)
		if err != nil {
			s.logger.Error("failed to load scheduled task for an overdue run", "execID", run.ID, "error", err)
			continue
		}
		if waitedOutBudgetState(run) {
			s.skipUnconfirmedBudget(ctx, now, run, task)
			continue
		}
		if waitedOutIdentity(run) || waitedOutPlatform(run) {
			if message, unrunnable := unrunnableMessage(task, run); unrunnable {
				s.endRun(ctx, now, run, task, "skipped", message, false)
				continue
			}
		}
		if waitedOutIdentity(run) {
			s.logger.Error("scheduled run's identity could not be confirmed before its timeout; giving up",
				"taskID", run.ScheduledTaskID, "execID", run.ID, "runAs", stampedRunAs(run), "counted", false)
			s.endUncounted(ctx, now, run, task, store.EndIdentityUnconfirmed, identityUnconfirmedTimeoutMessage)
			continue
		}
		if waitedOutPlatform(run) {
			s.logger.Error("scheduled run's platform did not report service accounts before its timeout; giving up",
				"taskID", run.ScheduledTaskID, "execID", run.ID, "runAs", stampedRunAs(run), "counted", false)
			s.endUncounted(ctx, now, run, task, store.EndPlatformTooOld, runAsPlatformTooOldMessage)
			continue
		}
		if run.Status == "pending" {
			s.logger.Error("scheduled run was not sent before its timeout; giving up", "taskID", run.ScheduledTaskID, "execID", run.ID)
			s.endRun(ctx, now, run, task, "timeout", neverSentMessage, false)
			continue
		}
		s.logger.Warn("scheduled task timed out", "taskID", run.ScheduledTaskID, "execID", run.ID, "runStatus", run.Status)
		s.endRun(ctx, now, run, task, "timeout", timedOutMessage, false)
	}
	return nil
}

func (s *ScheduledTaskService) finishPass(ctx context.Context, now time.Time) error {
	runs, err := s.repo.FindUnfinalizedExecutions(ctx, s.namespace())
	if err != nil {
		return fmt.Errorf("find unfinalized executions: %w", err)
	}
	deadline := time.Now().Add(finishPassBudget)
	for _, run := range runs {
		if time.Now().After(deadline) {
			return nil
		}
		s.finish(ctx, run, now)
	}
	return nil
}

func (s *ScheduledTaskService) finish(ctx context.Context, run *store.ScheduledTaskExecution, now time.Time) {
	task, err := s.repo.GetByID(ctx, run.ScheduledTaskID)
	if err != nil || task == nil {
		s.logger.Error("failed to load scheduled task to finish its run", "execID", run.ID, "error", err)
		return
	}
	lastAttempt, resending := unsentBudgetNotice(run)
	givingUp := resending && budgetNoticeWindowPassed(run, now)
	if resending && !givingUp && now.Sub(lastAttempt) < budgetNoticeRetryInterval {
		return
	}
	sessionID, lookupErr := s.lookUpRunSession(ctx, run.ID)
	if lookupErr != nil && run.Status == store.ExecutionStatusQuiet {
		return
	}
	hadSession := sessionID != ""
	if !resending && sessionID != "" && run.A2ATaskID != nil && submitted(run) {
		kept, ok := s.saveRunChat(ctx, run, task, sessionID, now)
		if !ok {
			return
		}
		sessionID = kept
	}
	switch {
	case givingUp:
		s.logger.Error("scheduled task's budget notice failed on every channel; finishing the run without it",
			"taskID", task.ID, "execID", run.ID)
	case !s.dispatchNotifications(ctx, run.ID, sessionID, task, now):
		return
	}
	if hadSession || run.Status == store.ExecutionStatusQuiet {
		s.emitSessionCreated(run.ID, sessionID, task, run.Status)
	}
	if deleted, err := s.repo.DeleteOldestExecutions(ctx, task.ID, s.cfg.ExecutionHistoryKeepCount); err != nil {
		s.logger.Debug("failed to prune execution history", "taskID", task.ID, "error", err)
	} else if deleted > 0 {
		s.logger.Debug("pruned execution history", "taskID", task.ID, "deleted", deleted)
	}
	if _, err := s.repo.ClaimFinalize(ctx, run.ID, now.UnixMilli()); err != nil {
		s.logger.Error("failed to mark scheduled run finished", "execID", run.ID, "error", err)
	}
}

func submitted(run *store.ScheduledTaskExecution) bool {
	return run.SubmittedAt != nil && *run.SubmittedAt > 0
}

func (s *ScheduledTaskService) runSessionID(ctx context.Context, execID string) string {
	sessionID, _ := s.lookUpRunSession(ctx, execID)
	return sessionID
}

func (s *ScheduledTaskService) lookUpRunSession(ctx context.Context, execID string) (string, error) {
	if s.sessKeyRepo == nil {
		return "", nil
	}
	mapping, err := s.sessKeyRepo.GetByGatewayAndExternalKeys(ctx, store.GatewayTypeScheduled, []string{execID})
	if err != nil {
		s.logger.Warn("failed to look up the session of a scheduled run", "execID", execID, "error", err)
		return "", err
	}
	return mapping[execID], nil
}

func (s *ScheduledTaskService) saveRunChat(ctx context.Context, run *store.ScheduledTaskExecution, task *store.ScheduledTask, sessionID string, now time.Time) (string, bool) {
	switch run.Status {
	case "completed":
		return sessionID, s.saveAnsweredChat(ctx, run, task, sessionID, now)
	case "failed":
		final, _, err := s.storedRunEvents(ctx, run.ID, *run.A2ATaskID)
		if err != nil {
			return sessionID, false
		}
		if text := failureChatText(final, run); text != "" {
			s.saveChatTask(ctx, task, sessionID, *run.A2ATaskID, nil, text, nil)
		}
	case store.ExecutionStatusQuiet:
		return s.settleQuietRunSession(ctx, run, task, sessionID, now)
	}
	return sessionID, true
}

func (s *ScheduledTaskService) saveAnsweredChat(ctx context.Context, run *store.ScheduledTaskExecution, task *store.ScheduledTask, sessionID string, now time.Time) bool {
	final, rag, ok := s.loadAnsweredRun(ctx, run, task, now)
	if !ok {
		return false
	}
	s.saveAnsweredRun(ctx, run, task, sessionID, final, rag)
	return true
}

func (s *ScheduledTaskService) loadAnsweredRun(ctx context.Context, run *store.ScheduledTaskExecution, task *store.ScheduledTask, now time.Time) (storedFinal, []map[string]any, bool) {
	a2aTaskID := *run.A2ATaskID
	if s.sseRepo != nil {
		_, done, err := s.sseRepo.SequenceState(ctx, a2aTaskID)
		if err != nil {
			s.logger.Warn("failed to read the stored events of a scheduled run", "execID", run.ID, "error", err)
			return storedFinal{}, nil, false
		}
		if !done && now.Before(s.completionWaitEnd(run, task)) {
			return storedFinal{}, nil, false
		}
	}
	final, rag, err := s.storedRunEvents(ctx, run.ID, a2aTaskID)
	if err != nil {
		return storedFinal{}, nil, false
	}
	return final, rag, true
}

func (s *ScheduledTaskService) saveAnsweredRun(ctx context.Context, run *store.ScheduledTaskExecution, task *store.ScheduledTask, sessionID string, final storedFinal, rag []map[string]any) {
	merged := mergeRAGData(extractRAGFromTask(final.task), rag)
	if final.task != nil && len(rag) > 0 {
		if summary := buildResultSummary(final.task, merged); summary != "" {
			if err := s.repo.UpdateExecution(ctx, run.ID, map[string]any{"result_summary": summary}); err != nil {
				s.logger.Warn("failed to add citations to a scheduled run's result", "execID", run.ID, "error", err)
			}
		}
	}
	s.saveChatTask(ctx, task, sessionID, *run.A2ATaskID, final.task, "", merged)
}

func (s *ScheduledTaskService) settleQuietRunSession(ctx context.Context, run *store.ScheduledTaskExecution, task *store.ScheduledTask, sessionID string, now time.Time) (string, bool) {
	if s.sessRepo == nil {
		return sessionID, true
	}
	final, rag, ok := s.loadAnsweredRun(ctx, run, task, now)
	if !ok {
		return sessionID, false
	}
	final = final.withoutBlankTask()
	if final.task == nil {
		s.logger.Warn("the stored events of a quiet scheduled run hold no readable final answer, so its session is kept",
			"taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", *run.A2ATaskID, "finalUnreadable", final.unreadable, "finalIsError", final.rpcErr != nil)
		s.saveAnsweredRun(ctx, run, task, sessionID, final, rag)
		return sessionID, true
	}
	holdsFile, err := s.sessionHoldsServableFile(ctx, run, task, sessionID, producedFileNames(final.task))
	if err != nil {
		s.logger.Warn("the files of a quiet scheduled run's session could not be listed, so its session is kept",
			"taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", *run.A2ATaskID, "sessionID", sessionID, "errorClass", artifactCheckFailure(err))
		s.saveAnsweredRun(ctx, run, task, sessionID, final, rag)
		return sessionID, true
	}
	if holdsFile {
		s.logger.Debug("a quiet scheduled run saved a servable file, so its session is kept",
			"taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", *run.A2ATaskID)
		s.saveAnsweredRun(ctx, run, task, sessionID, final, rag)
		return sessionID, true
	}
	hidden, err := s.sessRepo.HideEmptySession(ctx, sessionID, s.resolveUserID(task))
	if err != nil {
		s.logger.Warn("failed to hide the session of a quiet scheduled run",
			"taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", *run.A2ATaskID, "sessionID", sessionID, "error", err)
		return sessionID, false
	}
	if hidden {
		s.logger.Info("a quiet scheduled run produced no file, so its session was hidden",
			"taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", *run.A2ATaskID, "sessionID", sessionID)
		return "", true
	}
	s.logger.Debug("the session of a quiet scheduled run was left as it is: it is gone, not the owner's or already has a chat",
		"taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", *run.A2ATaskID, "sessionID", sessionID)
	return sessionID, true
}

func producedFileNames(final *a2a.Task) []string {
	if final == nil {
		return nil
	}
	var files chatArtifactParts
	addManifestArtifacts(final.Metadata["produced_artifacts"], "", files.add)
	names := make([]string, 0, len(files.list))
	for _, file := range files.list {
		name, _ := file["name"].(string)
		names = append(names, name)
	}
	return names
}

func (s *ScheduledTaskService) completionWaitEnd(run *store.ScheduledTaskExecution, task *store.ScheduledTask) time.Time {
	start := run.ScheduledFor
	if run.StartedAt != nil {
		start = *run.StartedAt
	}
	end := time.UnixMilli(start).Add(s.timeoutFor(task))
	if run.CompletedAt != nil {
		if markWait := time.UnixMilli(*run.CompletedAt).Add(completionMarkWait); markWait.Before(end) {
			end = markWait
		}
	}
	return end
}

type storedFinal struct {
	task       *a2a.Task
	rpcErr     *a2a.Error
	unreadable bool
}

func (f storedFinal) withoutBlankTask() storedFinal {
	if f.task != nil && f.task.Status.State == "" {
		return storedFinal{unreadable: true}
	}
	return f
}

func (f storedFinal) orUnreadable() storedFinal {
	if f.task != nil || f.rpcErr != nil {
		return f
	}
	return storedFinal{unreadable: true}
}

func (s *ScheduledTaskService) storedRunEvents(ctx context.Context, execID, a2aTaskID string) (storedFinal, []map[string]any, error) {
	var final storedFinal
	if s.sseRepo == nil {
		return final, nil, nil
	}
	rows, err := s.sseRepo.GetByTask(ctx, a2aTaskID, 0)
	if err != nil {
		s.logger.Warn("failed to read the stored events of a scheduled run", "execID", execID, "error", err)
		return final, nil, err
	}
	var rag []ragEntry
	for _, row := range rows {
		var env rpcResultEnvelope
		if err := json.Unmarshal([]byte(row.EventData), &env); err != nil {
			if row.EventType == "final_response" {
				final = final.orUnreadable()
			}
			continue
		}
		switch row.EventType {
		case "status_update":
			var event a2a.TaskStatusUpdateEvent
			if json.Unmarshal(env.resultOrNull(), &event) == nil {
				rag, _, _, _ = mergeRAGEntries(rag, extractRAGEntries(event.Status.Message))
			}
		case "final_response":
			if env.Error != nil {
				final = storedFinal{rpcErr: env.Error}
				continue
			}
			var task a2a.Task
			if json.Unmarshal(env.resultOrNull(), &task) == nil {
				final = storedFinal{task: &task}
			} else {
				final = final.orUnreadable()
			}
		}
	}
	return final, ragEnvelopes(rag), nil
}

func failureChatText(final storedFinal, run *store.ScheduledTaskExecution) string {
	if run.ErrorMessage != nil && *run.ErrorMessage == sanitizeError(interactiveStateErrorMessage) {
		return *run.ErrorMessage
	}
	if final.rpcErr != nil {
		return sanitizeError(final.rpcErr.Message)
	}
	if message, unsuccessful := unsuccessfulEnd(final.task); unsuccessful {
		return truncate(message, maxChatErrorLen)
	}
	return ""
}
