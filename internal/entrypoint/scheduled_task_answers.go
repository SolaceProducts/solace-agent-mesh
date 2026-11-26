// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const ownEndAttempts = 3

func (s *ScheduledTaskService) handleAnswer(ctx context.Context, a2aTaskID string, task *a2a.Task, a2aErr *a2a.Error) {
	end, mayRetry := answerEnd(task, a2aErr, time.Now(), s.quietRunsEnabled())
	run, res := s.endAnswered(ctx, a2aTaskID, end, mayRetry)
	if !res.Ended {
		return
	}
	switch end.Status {
	case "completed":
		s.logger.Info("scheduled task completed", "taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", a2aTaskID)
		return
	case store.ExecutionStatusQuiet:
		s.logger.Info("scheduled task ended quietly", "taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", a2aTaskID)
		return
	}
	if end.Cause == store.EndBudgetSpent {
		s.logger.Info("scheduled task stopped: a budget is spent",
			"taskID", run.ScheduledTaskID, "execID", run.ID, "budgetStop", res.BudgetStop)
		return
	}
	attrs := []any{"taskID", run.ScheduledTaskID, "execID", run.ID, "a2aTaskID", a2aTaskID}
	if task != nil {
		attrs = append(attrs, "taskState", string(task.Status.State))
	}
	if end.ErrorMessage != nil {
		attrs = append(attrs, "errorLength", utf8.RuneCountInString(*end.ErrorMessage))
	}
	s.logger.Warn("scheduled task ended unsuccessfully", attrs...)
}

func (s *ScheduledTaskService) handleInteractiveState(ctx context.Context, a2aTaskID string, event *a2a.TaskStatusUpdateEvent) {
	if event.Status.State != a2a.TaskStateInputNeeded && event.Status.State != a2a.TaskStateAuthRequired {
		return
	}
	message := sanitizeError(interactiveStateErrorMessage)
	end := store.ExecutionEnd{Status: "failed", CompletedAt: time.Now().UnixMilli(), ErrorMessage: &message, Answered: true}
	run, res := s.endAnswered(ctx, a2aTaskID, end, true)
	if !res.Ended {
		return
	}
	if agent := s.core.tasks.AgentName(a2aTaskID); agent != "" {
		if err := s.core.CancelTask(ctx, a2aTaskID, agent); err != nil {
			s.logger.Warn("failed to cancel a scheduled task waiting for input", "execID", run.ID, "error", err)
		}
	}
}

func (s *ScheduledTaskService) endAnswered(ctx context.Context, a2aTaskID string, end store.ExecutionEnd, mayRetry bool) (*store.ScheduledTaskExecution, store.RunEndResult) {
	d := delivery.From(ctx)
	wait := taskStateRetryMin
	for attempt := 1; ; attempt++ {
		run, res, err := s.endOpenRun(ctx, a2aTaskID, end, mayRetry)
		if err == nil {
			return run, res
		}
		if dbutil.IsRejected(err) || (d == nil && attempt >= ownEndAttempts) {
			s.logger.Error("failed to record a scheduled task's answer", "a2aTaskID", a2aTaskID, "attempt", attempt, "error", err)
			return nil, store.RunEndResult{}
		}
		s.logger.Warn("failed to record a scheduled task's answer; retrying",
			"a2aTaskID", a2aTaskID, "attempt", attempt, "retryIn", wait, "error", err)
		select {
		case <-ctx.Done():
			if d != nil {
				d.Keep()
			}
			return nil, store.RunEndResult{}
		case <-time.After(wait):
		}
		wait = min(2*wait, taskStateRetryMax)
	}
}

func (s *ScheduledTaskService) endOpenRun(ctx context.Context, a2aTaskID string, end store.ExecutionEnd, mayRetry bool) (*store.ScheduledTaskExecution, store.RunEndResult, error) {
	run, err := s.repo.GetExecutionByA2ATaskID(ctx, a2aTaskID)
	if err != nil || run == nil || (run.Status != "running" && run.Status != "pending") {
		return nil, store.RunEndResult{}, err
	}
	schedule, err := s.repo.GetByID(ctx, run.ScheduledTaskID)
	if err != nil {
		return nil, store.RunEndResult{}, err
	}
	res, err := s.endRunWith(ctx, time.Now(), run, schedule, end, mayRetry)
	return run, res, err
}

func answerEnd(task *a2a.Task, a2aErr *a2a.Error, now time.Time, quietAllowed bool) (store.ExecutionEnd, bool) {
	end := store.ExecutionEnd{CompletedAt: now.UnixMilli(), Answered: true}
	if a2aErr != nil {
		end.Status = "failed"
		end.ErrorMessage = strPtr(sanitizeError(a2aErr.Message))
		if IsBudgetStop(a2aErr) {
			end.Cause = store.EndBudgetSpent
		}
		return end, end.Cause != store.EndBudgetSpent
	}
	if message, unsuccessful := unsuccessfulEnd(task); unsuccessful {
		end.Status = "failed"
		end.ErrorMessage = strPtr(sanitizeError(message))
		if IsBudgetStoppedTask(task) {
			end.Cause = store.EndBudgetSpent
		}
		return end, task.Status.State != a2a.TaskStateCancelled && end.Cause != store.EndBudgetSpent
	}
	end.Status = "completed"
	if quietAllowed && endedQuietly(task) {
		end.Status = store.ExecutionStatusQuiet
	}
	if summary := buildResultSummary(task, extractRAGFromTask(task)); summary != "" {
		end.ResultSummary = &summary
	}
	if artifacts := buildArtifactsSummary(task); artifacts != "" {
		end.Artifacts = &artifacts
	}
	return end, false
}
