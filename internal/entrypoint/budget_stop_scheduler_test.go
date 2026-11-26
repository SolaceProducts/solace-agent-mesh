// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const workflowBudgetStopMessage = `Workflow "Payroll" was stopped because this deployment's usage budget is used up until 1 November 2099 00:00 UTC. Contact your administrator if you need it sooner.`

const agentBudgetStopMessage = "This request was stopped because this deployment's usage budget is used up until 1 November 2099 00:00 UTC. Contact your administrator if you need it sooner."

func budgetStopError(category string) *a2a.Error {
	return &a2a.Error{Code: -32603, Message: agentBudgetStopMessage,
		Data: map[string]any{a2a.ErrorDataLLMErrorCategory: category}}
}

func failedWorkflowTask(metadata map[string]any) *a2a.Task {
	task := finalTask(a2a.TaskStateFailed, workflowBudgetStopMessage)
	task.Metadata = metadata
	return task
}

func answerEveryAttempt(t *testing.T, svc *ScheduledTaskService, taskID string, now time.Time, task *a2a.Task, a2aErr *a2a.Error) int {
	t.Helper()
	sent := map[string]bool{}
	for i := range 4 {
		mustRunPasses(t, svc, now.Add(time.Duration(2*i)*time.Second))
		for _, r := range taskRuns(t, svc, taskID) {
			if r.Status == "running" && !sent[r.ID] {
				sent[r.ID] = true
				svc.handleAnswer(t.Context(), *r.A2ATaskID, task, a2aErr)
			}
		}
	}
	return len(sent)
}

func budgetStopSchedule(t *testing.T, autoPauseAfter, maxRetries int) (*ScheduledTaskService, string) {
	t.Helper()
	svc := passesSvc(t)
	svc.cfg.AutoPauseAfterConsecutiveFailures = autoPauseAfter
	task := dueTask(t, svc, time.Now().Add(-time.Second), func(st *store.ScheduledTask) {
		st.MaxRetries = maxRetries
		st.RetryDelaySeconds = 1
	})
	return svc, task.ID
}

func TestScheduledTask_AnAgentStoppedForBudgetEndsTheFireWithoutRetryOrAutoPause(t *testing.T) {
	svc, taskID := budgetStopSchedule(t, 1, 2)

	attempts := answerEveryAttempt(t, svc, taskID, time.Now(), nil, budgetStopError(taxonomy.ErrorCategoryBudgetExhausted))

	if attempts != 1 {
		t.Errorf("attempts = %d, want 1: a budget stop is not retried", attempts)
	}
	execs := taskRuns(t, svc, taskID)
	if len(execs) != 1 {
		t.Fatalf("executions = %d, want 1", len(execs))
	}
	if execs[0].Status != "failed" {
		t.Errorf("execution status = %q, want failed: the agent stopped partway", execs[0].Status)
	}
	if execs[0].ErrorMessage == nil || !strings.Contains(*execs[0].ErrorMessage, "1 November 2099 00:00 UTC") {
		t.Errorf("error message = %v, want the agent's message naming the reset time", execs[0].ErrorMessage)
	}
	got := reloadTask(t, svc, taskID)
	if got.ConsecutiveFailureCount != 0 {
		t.Errorf("consecutive_failure_count = %d, want 0", got.ConsecutiveFailureCount)
	}
	if !got.Enabled {
		t.Error("a schedule stopped for budget was auto-paused at a threshold of 1")
	}
}

func TestScheduledTask_AWorkflowStoppedForBudgetEndsTheFireWithoutRetryOrAutoPause(t *testing.T) {
	svc, taskID := budgetStopSchedule(t, 1, 2)
	stopped := failedWorkflowTask(map[string]any{a2a.ErrorDataLLMErrorCategory: taxonomy.ErrorCategoryBudgetExhausted})

	attempts := answerEveryAttempt(t, svc, taskID, time.Now(), stopped, nil)

	if attempts != 1 {
		t.Errorf("attempts = %d, want 1: a budget stop is not retried", attempts)
	}
	got := reloadTask(t, svc, taskID)
	if got.ConsecutiveFailureCount != 0 {
		t.Errorf("consecutive_failure_count = %d, want 0", got.ConsecutiveFailureCount)
	}
	if !got.Enabled {
		t.Error("a schedule whose workflow stopped for budget was auto-paused at a threshold of 1")
	}
}

func TestScheduledTask_AFailedWorkflowWithoutTheBudgetCategoryIsStillRetried(t *testing.T) {
	svc, taskID := budgetStopSchedule(t, 0, 1)

	attempts := answerEveryAttempt(t, svc, taskID, time.Now(), failedWorkflowTask(nil), nil)

	if attempts != 2 {
		t.Errorf("attempts = %d, want 2: one attempt and one retry", attempts)
	}
}

func TestScheduledTask_AnAgentErrorWithoutTheBudgetCategoryIsStillRetried(t *testing.T) {
	svc, taskID := budgetStopSchedule(t, 0, 1)

	attempts := answerEveryAttempt(t, svc, taskID, time.Now(), nil, budgetStopError(llm.ErrorCategoryRateLimit))

	if attempts != 2 {
		t.Errorf("attempts = %d, want 2: one attempt and one retry", attempts)
	}
	if got := reloadTask(t, svc, taskID); got.ConsecutiveFailureCount == 0 {
		t.Error("an ordinary agent failure did not count toward auto-pause")
	}
}

func TestIsBudgetStop(t *testing.T) {
	tests := []struct {
		name string
		err  *a2a.Error
		want bool
	}{
		{name: "no error", err: nil, want: false},
		{name: "error without data", err: &a2a.Error{Code: -32603, Message: "boom"}, want: false},
		{name: "data that is not a map", err: &a2a.Error{Code: -32603, Data: "budget_exhausted"}, want: false},
		{
			name: "another failure category",
			err:  &a2a.Error{Code: -32603, Data: map[string]any{a2a.ErrorDataLLMErrorCategory: llm.ErrorCategoryRateLimit}},
			want: false,
		},
		{
			name: "budget exhausted category",
			err:  &a2a.Error{Code: -32603, Data: map[string]any{a2a.ErrorDataLLMErrorCategory: taxonomy.ErrorCategoryBudgetExhausted}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBudgetStop(tt.err); got != tt.want {
				t.Errorf("IsBudgetStop() = %v, want %v", got, tt.want)
			}
		})
	}
}
