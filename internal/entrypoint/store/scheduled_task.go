// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"encoding/json"
	"slices"
)

type ScheduledTask struct {
	ID                      string  `db:"id"`
	Name                    string  `db:"name"`
	Description             *string `db:"description"`
	Namespace               string  `db:"namespace"`
	UserID                  *string `db:"user_id"`
	CreatedBy               string  `db:"created_by"`
	ScheduleType            string  `db:"schedule_type"`
	ScheduleExpression      string  `db:"schedule_expression"`
	Timezone                string  `db:"timezone"`
	TargetAgentName         string  `db:"target_agent_name"`
	TargetType              string  `db:"target_type"`
	TaskMessage             string  `db:"task_message"`
	TaskMetadata            *string `db:"task_metadata"`
	Enabled                 bool    `db:"enabled"`
	MaxRetries              int     `db:"max_retries"`
	RetryDelaySeconds       int     `db:"retry_delay_seconds"`
	TimeoutSeconds          int     `db:"timeout_seconds"`
	Source                  string  `db:"source"`
	ConsecutiveFailureCount int     `db:"consecutive_failure_count"`
	RunCount                int     `db:"run_count"`
	NotificationConfig      *string `db:"notification_config"`
	NextRunAt               *int64  `db:"next_run_at"`
	LastRunAt               *int64  `db:"last_run_at"`
	CreatedAt               int64   `db:"created_at"`
	UpdatedAt               int64   `db:"updated_at"`
	DeletedAt               *int64  `db:"deleted_at"`
	DeletedBy               *string `db:"deleted_by"`
	RunAs                   *string `db:"run_as"`
}

const (
	TriggerTypeScheduled = "scheduled"
	TriggerTypeManual    = "manual"
)

const ExecutionStatusQuiet = "quiet"

const (
	BudgetStopFirst  = "first"
	BudgetStopRepeat = "repeat"
)

const (
	RefusalRunAsDisabled  = "run_as_disabled"
	RefusalPlatformTooOld = "platform_too_old"
)

const (
	RefusalNoticeFirst  = "first"
	RefusalNoticeRepeat = "repeat"
)

const (
	SkippedRunningNote      = "previous execution still running"
	SkippedIdentityWaitNote = "previous execution is still waiting to confirm the identity it runs as"
)

func (t *ScheduledTask) ResolveOwnerUserID() string {
	if t.UserID != nil && *t.UserID != "" {
		return *t.UserID
	}
	return t.CreatedBy
}

type ScheduledTaskExecution struct {
	ID                string  `db:"id"`
	ScheduledTaskID   string  `db:"scheduled_task_id"`
	Status            string  `db:"status"`
	A2ATaskID         *string `db:"a2a_task_id"`
	ScheduledFor      int64   `db:"scheduled_for"`
	StartedAt         *int64  `db:"started_at"`
	CompletedAt       *int64  `db:"completed_at"`
	ResultSummary     *string `db:"result_summary"`
	ErrorMessage      *string `db:"error_message"`
	Artifacts         *string `db:"artifacts"`
	NotificationsSent *string `db:"notifications_sent"`
	RetryCount        int     `db:"retry_count"`
	TriggerType       string  `db:"trigger_type"`
	TriggeredBy       *string `db:"triggered_by"`
	TaskSnapshot      *string `db:"task_snapshot"`
	SubmittedAt       *int64  `db:"submitted_at"`
	FinalizedAt       *int64  `db:"finalized_at"`
	BudgetStop        *string `db:"budget_stop"`
	RunAs             *string `db:"run_as"`
	RefusalCause      *string `db:"refusal_cause"`
	RefusalNotice     *string `db:"refusal_notice"`
}

func (e *ScheduledTaskExecution) NoticeDelivered() bool {
	if e.NotificationsSent == nil {
		return false
	}
	var records []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(*e.NotificationsSent), &records); err != nil {
		return false
	}
	return slices.ContainsFunc(records, func(rec struct {
		Status string `json:"status"`
	}) bool {
		return rec.Status == "sent"
	})
}

func (e *ScheduledTaskExecution) ArtifactUserID(task *ScheduledTask) string {
	if e.RanAsSystemUser() {
		return *e.RunAs
	}
	return task.ResolveOwnerUserID()
}

func (e *ScheduledTaskExecution) RanAsSystemUser() bool {
	return e.RunAs != nil && *e.RunAs != ""
}
