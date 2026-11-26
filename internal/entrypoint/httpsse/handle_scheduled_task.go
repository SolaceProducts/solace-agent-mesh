// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/scheduler"
)

const (
	errMsgTaskNotFound   = "scheduled task not found"
	errMsgTaskLoadFailed = "failed to get scheduled task"
	errMsgExecNotFound   = "execution not found"
)

func deriveTaskStatus(enabled bool, consecutiveFailureCount int) string {
	if !enabled {
		return "paused"
	}
	if consecutiveFailureCount > 0 {
		return "error"
	}
	return "active"
}

func requireRegisteredAgent(core *entrypoint.GatewayCore, name, targetType string) *scheduledTaskFieldError {
	if targetType == "workflow" {
		return nil
	}
	reg := core.AgentRegistry()
	if reg == nil {
		return nil
	}
	if reg.FindByConfiguredName(name) != nil {
		return nil
	}
	return badRequest(fmt.Sprintf("targetAgentName %q is not a registered agent", name))
}

func (h *scheduledTaskHandler) requireRegisteredAgentForUpdate(task *store.ScheduledTask, fields map[string]any) *scheduledTaskFieldError {
	name := task.TargetAgentName
	if v, ok := fields["target_agent_name"].(string); ok {
		name = v
	}
	targetType := task.TargetType
	if v, ok := fields["target_type"].(string); ok {
		targetType = v
	}
	return requireRegisteredAgent(h.core, name, targetType)
}

type scheduledTaskHandler struct {
	core        *entrypoint.GatewayCore
	repo        *store.ScheduledTaskRepo
	service     *entrypoint.ScheduledTaskService
	sessKeyRepo *store.GatewaySessionKeyRepo
	artSvc      *artifact.ScopedService
	logger      *slog.Logger
}

func (h *scheduledTaskHandler) loadOwnedTask(w http.ResponseWriter, r *http.Request, logCtx string) (task *store.ScheduledTask, userID string, ok bool) {
	taskID := r.PathValue("id")
	if err := validatePathUUID(taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, "", false
	}
	task, err := h.repo.GetByID(r.Context(), taskID)
	if err != nil {
		h.logger.Error("load scheduled task", "op", logCtx, "error", err)
		writeError(w, http.StatusInternalServerError, errMsgTaskLoadFailed)
		return nil, "", false
	}
	if task == nil || task.DeletedAt != nil {
		writeError(w, http.StatusNotFound, errMsgTaskNotFound)
		return nil, "", false
	}
	if h.refuseForeignNamespace(w, r, task, logCtx, errMsgTaskNotFound) {
		return nil, "", false
	}
	userID = resolveUserID(r, h.core)
	if task.UserID != nil && *task.UserID != userID {
		writeError(w, http.StatusNotFound, errMsgTaskNotFound)
		return nil, "", false
	}
	return task, userID, true
}

func (h *scheduledTaskHandler) refuseForeignNamespace(w http.ResponseWriter, r *http.Request, task *store.ScheduledTask, logCtx, notFoundMsg string) bool {
	if task.Namespace == h.core.Config().Namespace {
		return false
	}
	h.logger.Warn("scheduled task access denied",
		"op", logCtx,
		"reason", "namespace mismatch",
		"callerUser", resolveUserID(r, h.core),
		"scheduledTaskID", task.ID)
	writeError(w, http.StatusNotFound, notFoundMsg)
	return true
}

type scheduledTaskResponse struct {
	ID                      string                `json:"id"`
	Name                    string                `json:"name"`
	Description             *string               `json:"description"`
	Namespace               string                `json:"namespace,omitempty"`
	ScheduleType            string                `json:"scheduleType"`
	ScheduleExpression      string                `json:"scheduleExpression"`
	Timezone                string                `json:"timezone"`
	TargetAgentName         string                `json:"targetAgentName"`
	TargetType              string                `json:"targetType"`
	TaskMessage             any                   `json:"taskMessage"`
	TaskMetadata            any                   `json:"taskMetadata,omitempty"`
	Enabled                 bool                  `json:"enabled"`
	Status                  string                `json:"status"`
	MaxRetries              int                   `json:"maxRetries"`
	RetryDelaySeconds       int                   `json:"retryDelaySeconds"`
	TimeoutSeconds          int                   `json:"timeoutSeconds"`
	Source                  string                `json:"source"`
	RunCount                int                   `json:"runCount"`
	ConsecutiveFailureCount int                   `json:"consecutiveFailureCount"`
	NotificationConfig      any                   `json:"notificationConfig,omitempty"`
	NextRunTime             *string               `json:"nextRunTime"`
	LastRunTime             *string               `json:"lastRunTime"`
	CreatedTime             string                `json:"createdAt"`
	UpdatedTime             string                `json:"updatedAt"`
	CreatedBy               string                `json:"createdBy"`
	UserID                  *string               `json:"userId,omitempty"`
	LastExecution           *lastExecutionSummary `json:"lastExecution,omitempty"`
	LastCompletedExecution  *lastExecutionSummary `json:"lastCompletedExecution,omitempty"`
	RunAs                   *string               `json:"runAs,omitempty"`
}

type lastExecutionSummary struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	ScheduledFor string  `json:"scheduledFor"`
	StartedAt    *string `json:"startedAt,omitempty"`
	CompletedAt  *string `json:"completedAt,omitempty"`
	DurationMs   *int64  `json:"durationMs,omitempty"`
	ErrorMessage *string `json:"errorMessage,omitempty"`
	TriggerType  string  `json:"triggerType"`
}

type executionResponse struct {
	ID                string  `json:"id"`
	ScheduledTaskID   string  `json:"scheduledTaskId"`
	Status            string  `json:"status"`
	A2ATaskID         *string `json:"a2aTaskId,omitempty"`
	ScheduledFor      string  `json:"scheduledFor"`
	StartedAt         *string `json:"startedAt,omitempty"`
	CompletedAt       *string `json:"completedAt,omitempty"`
	DurationMs        *int64  `json:"durationMs,omitempty"`
	ResultSummary     any     `json:"resultSummary,omitempty"`
	ErrorMessage      *string `json:"errorMessage,omitempty"`
	Artifacts         any     `json:"artifacts,omitempty"`
	NotificationsSent any     `json:"notificationsSent,omitempty"`
	RetryCount        int     `json:"retryCount"`
	TriggerType       string  `json:"triggerType"`
	SessionID         *string `json:"sessionId,omitempty"`
	TriggeredBy       *string `json:"triggeredBy,omitempty"`
	TaskSnapshot      any     `json:"taskSnapshot,omitempty"`
	RunAs             *string `json:"runAs,omitempty"`
}

type createScheduledTaskRequest struct {
	Name               string  `json:"name"`
	Description        *string `json:"description"`
	ScheduleType       string  `json:"scheduleType"`
	ScheduleExpression string  `json:"scheduleExpression"`
	Timezone           string  `json:"timezone"`
	TargetAgentName    string  `json:"targetAgentName"`
	TargetType         string  `json:"targetType"`
	TaskMessage        any     `json:"taskMessage"`
	TaskMetadata       any     `json:"taskMetadata"`
	MaxRetries         *int    `json:"maxRetries"`
	RetryDelaySeconds  *int    `json:"retryDelaySeconds"`
	TimeoutSeconds     *int    `json:"timeoutSeconds"`
	NotificationConfig any     `json:"notificationConfig"`
	Source             string  `json:"source"`
	Enabled            *bool   `json:"enabled"`
	UserLevel          *bool   `json:"userLevel"`
	RunAs              *string `json:"runAs"`
}

type previewRequest struct {
	ScheduleType       string `json:"scheduleType"`
	ScheduleExpression string `json:"scheduleExpression"`
	Timezone           string `json:"timezone"`
	Count              int    `json:"count"`
}

func executionToLastSummary(e *store.ScheduledTaskExecution) *lastExecutionSummary {
	if e == nil {
		return nil
	}
	triggerType := e.TriggerType
	if triggerType == "" {
		triggerType = store.TriggerTypeScheduled
	}
	s := &lastExecutionSummary{
		ID:           e.ID,
		Status:       e.Status,
		ScheduledFor: epochMsToISO8601(e.ScheduledFor),
		StartedAt:    optionalISO8601(e.StartedAt),
		CompletedAt:  optionalISO8601(e.CompletedAt),
		ErrorMessage: e.ErrorMessage,
		TriggerType:  triggerType,
	}
	if e.StartedAt != nil && e.CompletedAt != nil {
		d := *e.CompletedAt - *e.StartedAt
		s.DurationMs = &d
	}
	return s
}

func taskToDTO(t *store.ScheduledTask) scheduledTaskResponse {
	resp := scheduledTaskResponse{
		ID:                      t.ID,
		Name:                    t.Name,
		Description:             t.Description,
		ScheduleType:            t.ScheduleType,
		ScheduleExpression:      t.ScheduleExpression,
		Timezone:                t.Timezone,
		TargetAgentName:         t.TargetAgentName,
		TargetType:              t.TargetType,
		Enabled:                 t.Enabled,
		Status:                  deriveTaskStatus(t.Enabled, t.ConsecutiveFailureCount),
		MaxRetries:              t.MaxRetries,
		RetryDelaySeconds:       t.RetryDelaySeconds,
		TimeoutSeconds:          t.TimeoutSeconds,
		Source:                  t.Source,
		RunCount:                t.RunCount,
		ConsecutiveFailureCount: t.ConsecutiveFailureCount,
		NextRunTime:             optionalISO8601(t.NextRunAt),
		LastRunTime:             optionalISO8601(t.LastRunAt),
		CreatedTime:             epochMsToISO8601(t.CreatedAt),
		UpdatedTime:             epochMsToISO8601(t.UpdatedAt),
		CreatedBy:               t.CreatedBy,
		UserID:                  t.UserID,
		RunAs:                   t.RunAs,
	}

	var msgParsed any
	if err := json.Unmarshal([]byte(t.TaskMessage), &msgParsed); err == nil {
		resp.TaskMessage = msgParsed
	} else {
		resp.TaskMessage = t.TaskMessage
	}

	if t.TaskMetadata != nil {
		var metaParsed any
		if err := json.Unmarshal([]byte(*t.TaskMetadata), &metaParsed); err == nil {
			resp.TaskMetadata = metaParsed
		}
	}

	if t.NotificationConfig != nil {
		var ncParsed any
		if err := json.Unmarshal([]byte(*t.NotificationConfig), &ncParsed); err == nil {
			resp.NotificationConfig = ncParsed
		}
	}

	return resp
}

func legacyTriggerTypeOrScheduled(stored string) string {
	if stored == "" {
		return store.TriggerTypeScheduled
	}
	return stored
}

func executionToDTO(e *store.ScheduledTaskExecution) executionResponse {
	triggerType := legacyTriggerTypeOrScheduled(e.TriggerType)
	resp := executionResponse{
		ID:              e.ID,
		ScheduledTaskID: e.ScheduledTaskID,
		Status:          e.Status,
		A2ATaskID:       e.A2ATaskID,
		ScheduledFor:    epochMsToISO8601(e.ScheduledFor),
		StartedAt:       optionalISO8601(e.StartedAt),
		CompletedAt:     optionalISO8601(e.CompletedAt),
		ErrorMessage:    e.ErrorMessage,
		RetryCount:      e.RetryCount,
		TriggerType:     triggerType,
		TriggeredBy:     e.TriggeredBy,
		RunAs:           e.RunAs,
	}

	if e.StartedAt != nil && e.CompletedAt != nil {
		d := *e.CompletedAt - *e.StartedAt
		resp.DurationMs = &d
	}

	if e.ResultSummary != nil {
		var parsed any
		if err := json.Unmarshal([]byte(*e.ResultSummary), &parsed); err == nil {
			resp.ResultSummary = backfillLegacyResultSummary(parsed)
		}
	}
	if e.Artifacts != nil {
		var parsed any
		if err := json.Unmarshal([]byte(*e.Artifacts), &parsed); err == nil {
			resp.Artifacts = parsed
		}
	}
	if e.NotificationsSent != nil {
		var parsed any
		if err := json.Unmarshal([]byte(*e.NotificationsSent), &parsed); err == nil {
			resp.NotificationsSent = parsed
		}
	}
	if e.TaskSnapshot != nil {
		var parsed any
		if err := json.Unmarshal([]byte(*e.TaskSnapshot), &parsed); err == nil {
			resp.TaskSnapshot = parsed
		}
	}

	return resp
}

func (h *scheduledTaskHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createScheduledTaskRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Description != nil {
		trimmed := strings.TrimSpace(*req.Description)
		req.Description = &trimmed
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.ScheduleType == "" {
		writeError(w, http.StatusBadRequest, "scheduleType is required")
		return
	}
	if req.ScheduleExpression == "" {
		writeError(w, http.StatusBadRequest, "scheduleExpression is required")
		return
	}
	if req.TargetAgentName == "" {
		writeError(w, http.StatusBadRequest, "targetAgentName is required")
		return
	}
	if req.TaskMessage == nil {
		writeError(w, http.StatusBadRequest, "taskMessage is required")
		return
	}

	switch req.ScheduleType {
	case "cron", "interval", "one_time":
	default:
		writeError(w, http.StatusBadRequest, "scheduleType must be one of: cron, interval, one_time")
		return
	}

	targetType := req.TargetType
	if targetType == "" {
		targetType = "agent"
	}
	if targetType != "agent" && targetType != "workflow" {
		writeError(w, http.StatusBadRequest, "targetType must be one of: agent, workflow")
		return
	}

	if vErr := requireRegisteredAgent(h.core, req.TargetAgentName, targetType); vErr != nil {
		writeError(w, vErr.code, vErr.message)
		return
	}

	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	source := req.Source
	if source == "" {
		source = entrypoint.TaskSourceUI
	}
	if !entrypoint.IsAPISettableTaskSource(source) {
		writeError(w, http.StatusBadRequest, "source must be one of: ui, chat")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	maxRetries := 0
	if req.MaxRetries != nil {
		maxRetries = *req.MaxRetries
	}
	retryDelay := 60
	if req.RetryDelaySeconds != nil {
		retryDelay = *req.RetryDelaySeconds
	}
	timeout := 3600
	if req.TimeoutSeconds != nil {
		timeout = *req.TimeoutSeconds
	}
	for _, check := range []*scheduledTaskFieldError{
		limitError(entrypoint.MaxRetriesLimit, maxRetries),
		limitError(entrypoint.RetryDelaySecondsLimit, retryDelay),
		limitError(entrypoint.TimeoutSecondsLimit, timeout),
	} {
		if check != nil {
			writeError(w, check.code, check.message)
			return
		}
	}

	normalizedTZ, ok := entrypoint.NormalizeUserTimezone(tz)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid timezone: "+tz)
		return
	}
	tz = normalizedTZ

	if err := scheduler.ValidateExpression(
		scheduler.ScheduleType(req.ScheduleType), req.ScheduleExpression, tz,
	); err != nil {
		writeError(w, http.StatusBadRequest, "invalid schedule expression: "+err.Error())
		return
	}

	taskMsgBytes, err := json.Marshal(req.TaskMessage)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid taskMessage")
		return
	}
	if msgErr := validateTaskMessage(taskMsgBytes); msgErr != nil {
		writeError(w, msgErr.code, msgErr.message)
		return
	}

	var taskMetadata *string
	if req.TaskMetadata != nil {
		metaBytes, err := json.Marshal(req.TaskMetadata)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid taskMetadata")
			return
		}
		if metaErr := validateTaskMetadata(metaBytes); metaErr != nil {
			writeError(w, metaErr.code, metaErr.message)
			return
		}
		s := string(metaBytes)
		taskMetadata = &s
	}

	var notificationConfig *string
	if req.NotificationConfig != nil {
		ncBytes, err := json.Marshal(req.NotificationConfig)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid notificationConfig")
			return
		}
		ncStr := string(ncBytes)
		if err := entrypoint.ValidateNotificationConfig(ncStr); err != nil {
			writeError(w, http.StatusBadRequest, "invalid notificationConfig: "+err.Error())
			return
		}
		notificationConfig = &ncStr
	}

	runAs, ok := h.createRunAs(w, r, req.RunAs)
	if !ok {
		return
	}

	userID := resolveUserID(r, h.core)

	task := &store.ScheduledTask{
		ID:                 idgen.New(),
		Name:               req.Name,
		Description:        req.Description,
		Namespace:          h.core.Config().Namespace,
		UserID:             &userID,
		CreatedBy:          userID,
		ScheduleType:       req.ScheduleType,
		ScheduleExpression: req.ScheduleExpression,
		Timezone:           tz,
		TargetAgentName:    req.TargetAgentName,
		TargetType:         targetType,
		TaskMessage:        string(taskMsgBytes),
		TaskMetadata:       taskMetadata,
		NotificationConfig: notificationConfig,
		Enabled:            enabled,
		MaxRetries:         maxRetries,
		RetryDelaySeconds:  retryDelay,
		TimeoutSeconds:     timeout,
		Source:             source,
		RunAs:              runAs,
	}

	if enabled {
		next, err := entrypoint.FirstRunAt(task, time.Now(), req.ScheduleType == "interval")
		if err != nil {
			h.logger.Error("first run time of scheduled task", "userID", userID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to create scheduled task")
			return
		}
		task.NextRunAt = next
	}

	if err := h.repo.Create(r.Context(), task); err != nil {
		if dbutil.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, fmt.Sprintf("a scheduled task named %q already exists", task.Name))
			return
		}
		h.logger.Error("create scheduled task", "userID", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create scheduled task")
		return
	}

	writeJSON(w, http.StatusCreated, taskToDTO(task))
}

func (h *scheduledTaskHandler) handleList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	enabledOnly := r.URL.Query().Get("enabledOnly") == "true"

	ownership := cmp.Or(r.URL.Query().Get("ownership"), "namespace")
	switch ownership {
	case "mine", "namespace", "all":
	default:
		writeError(w, http.StatusBadRequest, "ownership must be one of: mine, namespace, all")
		return
	}
	includeNamespace := ownership != "mine"

	userID := resolveUserID(r, h.core)
	offset := (page - 1) * pageSize

	listOwnership := "namespace"
	if !includeNamespace {
		listOwnership = "mine"
	}
	tasks, total, err := h.service.ListTasks(r.Context(), userID, listOwnership, enabledOnly, pageSize, offset)
	if err != nil {
		h.logger.Error("list scheduled tasks", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list scheduled tasks")
		return
	}

	taskIDs := make([]string, len(tasks))
	for i, t := range tasks {
		taskIDs[i] = t.ID
	}
	lastByTask, lastErr := h.repo.FindLastExecutionsByTasks(r.Context(), taskIDs)
	if lastErr != nil {
		h.logger.Warn("fetch last executions for task list", "error", lastErr)
		lastByTask = nil
	}
	lastCompletedByTask, completedErr := h.repo.FindLastCompletedExecutionsByTasks(r.Context(), taskIDs)
	if completedErr != nil {
		h.logger.Warn("fetch last completed executions for task list", "error", completedErr)
		lastCompletedByTask = nil
	}

	dtos := make([]scheduledTaskResponse, 0, len(tasks))
	for _, t := range tasks {
		dto := taskToDTO(t)
		if last, ok := lastByTask[t.ID]; ok {
			dto.LastExecution = executionToLastSummary(last)
		}
		if last, ok := lastCompletedByTask[t.ID]; ok {
			dto.LastCompletedExecution = executionToLastSummary(last)
		}
		dtos = append(dtos, dto)
	}

	writeJSON(w, http.StatusOK, PaginatedResponse[scheduledTaskResponse]{
		Data: dtos,
		Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
	})
}

func (h *scheduledTaskHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	task, _, ok := h.loadOwnedTask(w, r, "get")
	if !ok {
		return
	}

	dto := taskToDTO(task)
	if lastByTask, err := h.repo.FindLastExecutionsByTasks(r.Context(), []string{task.ID}); err == nil {
		if last, ok := lastByTask[task.ID]; ok {
			dto.LastExecution = executionToLastSummary(last)
		}
	} else {
		h.logger.Warn("fetch last execution for task", "taskID", task.ID, "error", err)
	}
	if lastCompleted, err := h.repo.FindLastCompletedExecutionByTask(r.Context(), task.ID); err == nil {
		dto.LastCompletedExecution = executionToLastSummary(lastCompleted)
	} else {
		h.logger.Warn("fetch last completed execution for task", "taskID", task.ID, "error", err)
	}

	writeJSON(w, http.StatusOK, dto)
}

func (h *scheduledTaskHandler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	task, _, ok := h.loadOwnedTask(w, r, "update")
	if !ok {
		return
	}
	taskID := task.ID

	var rawFields map[string]json.RawMessage
	if decErr := decodeJSON(r, &rawFields); decErr != nil {
		badRequestFromDecode(w, decErr)
		return
	}

	if len(rawFields) == 0 {
		writeError(w, http.StatusBadRequest, "no fields to update were supplied")
		return
	}
	dropUnchangedLimits(rawFields, task)
	if len(rawFields) == 0 {
		writeJSON(w, http.StatusOK, taskToDTO(task))
		return
	}

	if task.Source == "config" {
		for key := range rawFields {
			if key != "enabled" {
				writeError(w, http.StatusForbidden, "config-sourced tasks are read-only (enable/disable only)")
				return
			}
		}
	}

	fields, scheduleChanged, vErr := buildScheduledTaskUpdateFields(rawFields)
	if vErr != nil {
		writeError(w, vErr.code, vErr.message)
		return
	}
	if !h.applyRunAsUpdate(w, r, task, rawFields, fields) {
		return
	}

	_, nameChanged := fields["target_agent_name"]
	_, typeChanged := fields["target_type"]
	if nameChanged || typeChanged {
		if vErr := h.requireRegisteredAgentForUpdate(task, fields); vErr != nil {
			writeError(w, vErr.code, vErr.message)
			return
		}
	}

	if scheduleChanged {
		st := task.ScheduleType
		if v, ok := fields["schedule_type"].(string); ok {
			st = v
		}
		storedExpr := strings.TrimSpace(task.ScheduleExpression)
		expr := storedExpr
		if v, ok := fields["schedule_expression"].(string); ok {
			expr = v
		}
		tz := task.Timezone
		if v, ok := fields["timezone"].(string); ok {
			tz = v
		}
		validate := scheduler.ValidateExpression
		if st == task.ScheduleType && expr == storedExpr {
			validate = scheduler.ValidateStoredExpression
		}
		if valErr := validate(scheduler.ScheduleType(st), expr, tz); valErr != nil {
			writeError(w, http.StatusBadRequest, "invalid schedule expression: "+valErr.Error())
			return
		}
		if expr != task.ScheduleExpression {
			fields["schedule_expression"] = expr
		}
	}

	updated, upErr := h.service.ApplyUpdate(r.Context(), taskID, fields, scheduleChanged)
	if upErr != nil {
		if errors.Is(upErr, entrypoint.ErrTaskNameConflict) {
			writeError(w, http.StatusConflict, "a scheduled task with this name already exists")
			return
		}
		h.logger.Error("update scheduled task", "error", upErr)
		writeError(w, http.StatusInternalServerError, "failed to update scheduled task")
		return
	}

	writeJSON(w, http.StatusOK, taskToDTO(updated))
}

func (h *scheduledTaskHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	task, userID, ok := h.loadOwnedTask(w, r, "delete")
	if !ok {
		return
	}
	taskID := task.ID

	if task.Source == "config" {
		writeError(w, http.StatusForbidden, "config-sourced tasks cannot be deleted")
		return
	}

	if err := h.service.DeleteTask(r.Context(), taskID, userID); err != nil {
		h.logger.Error("soft delete scheduled task", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete scheduled task")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *scheduledTaskHandler) handleEnable(w http.ResponseWriter, r *http.Request) {
	task, _, ok := h.loadOwnedTask(w, r, "enable")
	if !ok {
		return
	}
	taskID := task.ID

	updated, err := h.service.EnableTask(r.Context(), taskID)
	if err != nil {
		h.logger.Error("enable scheduled task", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to enable scheduled task")
		return
	}

	var nextRunAt *int64
	if updated.NextRunAt != nil {
		nextRunAt = updated.NextRunAt
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"message":   "Task enabled successfully",
		"taskId":    taskID,
		"nextRunAt": nextRunAt,
	})
}

func (h *scheduledTaskHandler) handleDisable(w http.ResponseWriter, r *http.Request) {
	task, _, ok := h.loadOwnedTask(w, r, "disable")
	if !ok {
		return
	}
	taskID := task.ID

	if err := h.service.DisableTask(r.Context(), taskID); err != nil {
		h.logger.Error("disable scheduled task", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to disable scheduled task")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Task disabled successfully",
		"taskId":  taskID,
	})
}

func (h *scheduledTaskHandler) handleTrigger(w http.ResponseWriter, r *http.Request) {
	task, userID, ok := h.loadOwnedTask(w, r, "trigger")
	if !ok {
		return
	}
	taskID := task.ID

	execID, err := h.service.ExecuteNow(r.Context(), taskID)
	if refusal, spent := entrypoint.BudgetRefusal(err); spent {
		writeError(w, http.StatusTooManyRequests, entrypoint.ScheduledRunRefusedText(refusal, task.ResolveOwnerUserID() == userID))
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, entrypoint.ErrTaskNotFound):
			writeError(w, http.StatusNotFound, errMsgTaskNotFound)
		case errors.Is(err, entrypoint.ErrTaskAlreadyRunning):
			writeError(w, http.StatusConflict, msgTaskAlreadyExecuting)
		default:
			h.logger.Error("trigger scheduled task", "taskID", taskID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to trigger scheduled task")
		}
		return
	}

	resourceURL := fmt.Sprintf("/api/v1/scheduledTasks/%s/executions", taskID)
	w.Header().Set("Location", resourceURL)
	writeJSON(w, http.StatusAccepted, OperationResponse{
		ID:          execID,
		Status:      OperationPending,
		ResourceURL: resourceURL,
	})
}

func (h *scheduledTaskHandler) handleRun(w http.ResponseWriter, r *http.Request) {
	task, userID, ok := h.loadOwnedTask(w, r, "run")
	if !ok {
		return
	}
	taskID := task.ID

	_, err := h.service.TriggerTaskNow(r.Context(), taskID, userID)
	if refusal, spent := entrypoint.BudgetRefusal(err); spent {
		writeError(w, http.StatusTooManyRequests, entrypoint.ScheduledRunRefusedText(refusal, task.ResolveOwnerUserID() == userID))
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, entrypoint.ErrTaskNotFound):
			writeError(w, http.StatusNotFound, errMsgTaskNotFound)
		case errors.Is(err, entrypoint.ErrTaskAlreadyRunning):
			writeError(w, http.StatusConflict, msgTaskAlreadyExecuting)
		default:
			h.logger.Error("run scheduled task", "taskID", taskID, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to run scheduled task")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Task triggered",
		"taskId":  taskID,
	})
}

func (h *scheduledTaskHandler) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	task, _, ok := h.loadOwnedTask(w, r, "executions")
	if !ok {
		return
	}
	taskID := task.ID

	page, pageSize, pageErr := parsePagination(r)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	offset := (page - 1) * pageSize

	startDate, err := parseOptionalRFC3339(r.URL.Query().Get("startDate"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "startDate must be an RFC 3339 timestamp")
		return
	}
	endDate, err := parseOptionalRFC3339(r.URL.Query().Get("endDate"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "endDate must be an RFC 3339 timestamp")
		return
	}

	execs, err := h.repo.FindExecutionsByTask(r.Context(), taskID, pageSize, offset, startDate, endDate)
	if err != nil {
		h.logger.Error("list executions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list executions")
		return
	}

	total, err := h.repo.CountExecutionsByTask(r.Context(), taskID, startDate, endDate)
	if err != nil {
		h.logger.Error("count executions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to count executions")
		return
	}

	dtos := make([]executionResponse, len(execs))
	dtoPtrs := make([]*executionResponse, len(execs))
	for i, e := range execs {
		dtos[i] = executionToDTO(e)
		dtoPtrs[i] = &dtos[i]
	}
	h.attachSessionIDs(r.Context(), dtoPtrs)

	writeJSON(w, http.StatusOK, PaginatedResponse[executionResponse]{
		Data: dtos,
		Meta: PaginationMeta{Pagination: newPaginationInfo(page, pageSize, total)},
	})
}

func (h *scheduledTaskHandler) handleRecentExecutions(w http.ResponseWriter, r *http.Request) {
	pageSize, parseErr := func() (int, error) {
		if raw := r.URL.Query().Get("pageSize"); raw != "" {
			_, sz, err := parsePagination(r)
			return sz, err
		}
		return 50, nil
	}()
	if parseErr != nil {
		writeError(w, http.StatusBadRequest, parseErr.Error())
		return
	}

	userID := resolveUserID(r, h.core)
	namespace := h.core.Config().Namespace

	execs, err := h.repo.FindRecentExecutions(r.Context(), namespace, &userID, pageSize)
	if err != nil {
		h.logger.Error("list recent executions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list recent executions")
		return
	}

	dtos := make([]executionResponse, len(execs))
	dtoPtrs := make([]*executionResponse, len(execs))
	for i, e := range execs {
		dtos[i] = executionToDTO(e)
		dtoPtrs[i] = &dtos[i]
	}
	h.attachSessionIDs(r.Context(), dtoPtrs)

	total := len(dtos)
	writeJSON(w, http.StatusOK, PaginatedResponse[executionResponse]{
		Data: dtos,
		Meta: PaginationMeta{Pagination: newPaginationInfo(1, pageSize, total)},
	})
}

func (h *scheduledTaskHandler) attachSessionIDs(ctx context.Context, dtos []*executionResponse) {
	if h.sessKeyRepo == nil || len(dtos) == 0 {
		return
	}
	execIDs := make([]string, 0, len(dtos))
	for _, d := range dtos {
		execIDs = append(execIDs, d.ID)
	}
	mapping, err := h.sessKeyRepo.GetByGatewayAndExternalKeys(ctx, store.GatewayTypeScheduled, execIDs)
	if err != nil {
		h.logger.Warn("attach session ids: mapping lookup failed", "error", err)
		return
	}
	for _, d := range dtos {
		if sid, ok := mapping[d.ID]; ok {
			s := sid
			d.SessionID = &s
		}
	}
}

func (h *scheduledTaskHandler) handleExecutionByA2ATask(w http.ResponseWriter, r *http.Request) {
	a2aTaskID := r.PathValue("a2aTaskId")
	if err := validatePathUUID(a2aTaskID); err != nil {
		writeError(w, http.StatusNotFound, errMsgExecNotFound)
		return
	}

	exec, err := h.repo.GetExecutionByA2ATaskID(r.Context(), a2aTaskID)
	if err != nil {
		h.logger.Error("get execution by a2a task", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get execution")
		return
	}
	if exec == nil {
		writeError(w, http.StatusNotFound, errMsgExecNotFound)
		return
	}
	if !h.authorizeExecutionParent(w, r, exec, "get execution by a2a task") {
		return
	}

	dto := executionToDTO(exec)
	h.attachSessionIDs(r.Context(), []*executionResponse{&dto})
	writeJSON(w, http.StatusOK, dto)
}

func (h *scheduledTaskHandler) handleSchedulerStatus(w http.ResponseWriter, r *http.Request) {
	namespace := h.core.Config().Namespace

	activeCount, err := h.repo.CountByNamespace(r.Context(), namespace, nil, true)
	if err != nil {
		h.logger.Error("count active tasks", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get scheduler status")
		return
	}

	runningCount, pendingCount, err := h.repo.CountOpenExecutions(r.Context(), namespace)
	if err != nil {
		h.logger.Error("count open executions", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get scheduler status")
		return
	}
	instanceID, hostErr := os.Hostname()
	if hostErr != nil || instanceID == "" {
		instanceID = h.core.Config().GatewayID
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"instanceId":             instanceID,
		"namespace":              namespace,
		"activeTasksCount":       activeCount,
		"runningExecutionsCount": runningCount,
		"schedulerRunning":       true,
		"pendingResultsCount":    pendingCount,
	})
}

func (h *scheduledTaskHandler) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}

	if req.ScheduleType == "" {
		writeError(w, http.StatusBadRequest, "scheduleType is required")
		return
	}
	switch req.ScheduleType {
	case "cron", "interval", "one_time":
	default:
		writeError(w, http.StatusBadRequest, "scheduleType must be one of: cron, interval, one_time")
		return
	}
	if req.ScheduleExpression == "" {
		writeError(w, http.StatusBadRequest, "scheduleExpression is required")
		return
	}

	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	normalizedTZ, ok := entrypoint.NormalizeUserTimezone(tz)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid timezone: "+tz)
		return
	}
	tz = normalizedTZ
	count := min(req.Count, 50)
	if count <= 0 {
		count = 5
	}

	times, err := scheduler.PreviewNextTimes(
		scheduler.ScheduleType(req.ScheduleType), req.ScheduleExpression, tz, count,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid schedule: "+err.Error())
		return
	}
	if len(times) == 0 && req.ScheduleType == string(scheduler.ScheduleTypeCron) {
		writeError(w, http.StatusBadRequest, "invalid schedule: the cron expression never fires")
		return
	}

	formatted := make([]string, 0, len(times))
	for _, t := range times {
		formatted = append(formatted, t.Format(time.RFC3339))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nextTimes":          formatted,
		"scheduleType":       req.ScheduleType,
		"scheduleExpression": req.ScheduleExpression,
		"timezone":           tz,
	})
}

func backfillLegacyResultSummary(parsed any) any {
	m, ok := parsed.(map[string]any)
	if !ok {
		return parsed
	}
	if _, hasNew := m["agentResponse"]; !hasNew {
		if v, ok := m["text"].(string); ok && v != "" {
			m["agentResponse"] = v
			if _, hasFull := m["agentResponseFull"]; !hasFull {
				m["agentResponseFull"] = v
			}
		}
	}
	if _, hasNew := m["messages"]; !hasNew {
		if v, ok := m["history"]; ok {
			m["messages"] = v
		}
	}
	return m
}

func parseOptionalRFC3339(raw string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	ms := t.UnixMilli()
	return &ms, nil
}

func (h *scheduledTaskHandler) loadOwnedExecution(w http.ResponseWriter, r *http.Request, logCtx string) (*store.ScheduledTaskExecution, bool) {
	execID := r.PathValue("id")
	if err := validatePathUUID(execID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	exec, err := h.repo.GetExecutionByID(r.Context(), execID)
	if err != nil {
		h.logger.Error("load execution", "op", logCtx, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get execution")
		return nil, false
	}
	if exec == nil {
		writeError(w, http.StatusNotFound, errMsgExecNotFound)
		return nil, false
	}
	if !h.authorizeExecutionParent(w, r, exec, logCtx) {
		return nil, false
	}
	return exec, true
}

func (h *scheduledTaskHandler) authorizeExecutionParent(w http.ResponseWriter, r *http.Request, exec *store.ScheduledTaskExecution, logCtx string) bool {
	task, err := h.repo.GetByID(r.Context(), exec.ScheduledTaskID)
	if err != nil {
		h.logger.Error("load parent task", "op", logCtx, "error", err)
		writeError(w, http.StatusInternalServerError, errMsgTaskLoadFailed)
		return false
	}
	if task == nil || task.DeletedAt != nil {
		writeError(w, http.StatusNotFound, errMsgExecNotFound)
		return false
	}
	if h.refuseForeignNamespace(w, r, task, logCtx, errMsgExecNotFound) {
		return false
	}
	if callerUserID := resolveUserID(r, h.core); task.UserID != nil && *task.UserID != callerUserID {
		h.logger.Warn("scheduled execution access denied",
			"op", logCtx,
			"reason", "not the parent task owner",
			"callerUser", callerUserID,
			"scheduledTaskID", exec.ScheduledTaskID)
		writeError(w, http.StatusNotFound, errMsgExecNotFound)
		return false
	}
	return true
}

func (h *scheduledTaskHandler) handleGetExecution(w http.ResponseWriter, r *http.Request) {
	exec, ok := h.loadOwnedExecution(w, r, "get execution")
	if !ok {
		return
	}
	dto := executionToDTO(exec)
	h.attachSessionIDs(r.Context(), []*executionResponse{&dto})
	writeJSON(w, http.StatusOK, dto)
}

func (h *scheduledTaskHandler) handleDeleteExecution(w http.ResponseWriter, r *http.Request) {
	exec, ok := h.loadOwnedExecution(w, r, "delete execution")
	if !ok {
		return
	}
	if isUnfinishedExecution(exec) {
		writeError(w, http.StatusConflict, "an execution that is still running or finishing cannot be deleted")
		return
	}
	if err := h.repo.DeleteExecution(r.Context(), exec.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, errMsgExecNotFound)
			return
		}
		h.logger.Error("delete execution", "executionID", exec.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete execution")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
