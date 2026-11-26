// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/scheduler"
)

const (
	defaultScheduledTaskPageSize = 50
	maxScheduledTaskPageSize     = 200

	scheduledTasksPath    = "scheduledTasks"
	scheduledTaskByIDPath = scheduledTasksPath + "/{id}"

	errInvalidScheduledTaskBody = "scheduledTasks: invalid request body: %w"
)

func scheduledTaskControlEndpoints() []control.Endpoint {
	return []control.Endpoint{
		{Method: "get", Path: scheduledTasksPath, Description: "List the caller's scheduled tasks (paginated)"},
		{Method: "get", Path: scheduledTaskByIDPath, Description: "Get one scheduled task"},
		{Method: "patch", Path: scheduledTaskByIDPath, Description: "Update a scheduled task's schedule/message/name"},
		{Method: "delete", Path: scheduledTaskByIDPath, Description: "Delete a scheduled task"},
		{Method: "post", Path: scheduledTaskByIDPath + "/enable", Description: "Enable a scheduled task"},
		{Method: "post", Path: scheduledTaskByIDPath + "/disable", Description: "Disable a scheduled task"},
	}
}

type scheduledTaskDTO struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Description        *string `json:"description,omitempty"`
	ScheduleType       string  `json:"scheduleType"`
	ScheduleExpression string  `json:"scheduleExpression"`
	Timezone           string  `json:"timezone"`
	TargetAgentName    string  `json:"targetAgentName"`
	TargetType         string  `json:"targetType"`
	TaskMessage        string  `json:"taskMessage"`
	Enabled            bool    `json:"enabled"`
	Source             string  `json:"source"`
	RunCount           int     `json:"runCount"`
	NextRunTime        *string `json:"nextRunTime,omitempty"`
	LastRunTime        *string `json:"lastRunTime,omitempty"`
	CreatedBy          string  `json:"createdBy"`
}

func epochMsToISO(ms *int64) *string {
	if ms == nil {
		return nil
	}
	s := time.UnixMilli(*ms).UTC().Format(time.RFC3339)
	return &s
}

func taskMessageText(raw string) string {
	if raw == "" {
		return ""
	}
	parts, text := decodeTaskMessage(raw)
	if parts == nil {
		return text
	}
	var b strings.Builder
	for _, p := range parts {
		t, _ := p["text"].(string)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(t)
	}
	return b.String()
}

func (e *GatewayExecutor) schedInternalErr(op string, err error) error {
	id := idgen.New()
	e.logger.Error("scheduledTasks operation failed", "errorId", id, "op", op, "error", err)
	return fmt.Errorf("scheduledTasks: %s failed (errorId=%s): %w", op, id, err)
}

func toScheduledTaskDTO(t *store.ScheduledTask) scheduledTaskDTO {
	return scheduledTaskDTO{
		ID:                 t.ID,
		Name:               t.Name,
		Description:        t.Description,
		ScheduleType:       t.ScheduleType,
		ScheduleExpression: t.ScheduleExpression,
		Timezone:           t.Timezone,
		TargetAgentName:    t.TargetAgentName,
		TargetType:         t.TargetType,
		TaskMessage:        taskMessageText(t.TaskMessage),
		Enabled:            t.Enabled,
		Source:             t.Source,
		RunCount:           t.RunCount,
		NextRunTime:        epochMsToISO(t.NextRunAt),
		LastRunTime:        epochMsToISO(t.LastRunAt),
		CreatedBy:          t.CreatedBy,
	}
}

type scheduledTaskListRequest struct {
	Ownership   string `json:"ownership"`
	EnabledOnly bool   `json:"enabledOnly"`
	PageNumber  int    `json:"pageNumber"`
	PageSize    int    `json:"pageSize"`
	UserID      string `json:"userId"`
}

type scheduledTaskUpdateRequest struct {
	Name               *string `json:"name"`
	Description        *string `json:"description"`
	ScheduleType       *string `json:"scheduleType"`
	ScheduleExpression *string `json:"scheduleExpression"`
	Timezone           *string `json:"timezone"`
	Message            *string `json:"message"`
	UserID             string  `json:"userId"`
}

type scheduledTaskUserRequest struct {
	UserID string `json:"userId"`
}

func (e *GatewayExecutor) scheduledTaskService() (*ScheduledTaskService, string, error) {
	for _, core := range e.Gateways() {
		if core != nil && core.ScheduledTasks() != nil {
			return core.ScheduledTasks(), core.Namespace(), nil
		}
	}
	return nil, "", fmt.Errorf("scheduled tasks unavailable: no gateway instance has the scheduler enabled")
}

func (e *GatewayExecutor) resolveManageUserID(identity *auth.Identity, claimed string) (string, error) {
	if e.controlAuthzEnabled() {
		if identity == nil || identity.UserID == "" {
			return "", fmt.Errorf("scheduledTasks: an authenticated identity is required to manage scheduled tasks")
		}
		return identity.UserID, nil
	}
	if identity != nil && identity.UserID != "" {
		return identity.UserID, nil
	}
	return claimed, nil
}

func ownsTask(t *store.ScheduledTask, userID string) bool {
	return t.UserID == nil || *t.UserID == userID
}

func (e *GatewayExecutor) handleScheduledTasks(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	svc, _, err := e.scheduledTaskService()
	if err != nil {
		return nil, err
	}

	switch {
	case method == "get" && len(path) == 0:
		return e.listScheduledTasks(ctx, svc, body, identity)
	case method == "get" && len(path) == 1:
		return e.getScheduledTask(ctx, svc, path[0], body, identity)
	case method == "patch" && len(path) == 1:
		return e.updateScheduledTask(ctx, svc, path[0], body, identity)
	case method == "delete" && len(path) == 1:
		return e.deleteScheduledTask(ctx, svc, path[0], body, identity)
	case method == "post" && len(path) == 2 && path[1] == "enable":
		return e.setScheduledTaskEnabled(ctx, svc, path[0], body, identity, true)
	case method == "post" && len(path) == 2 && path[1] == "disable":
		return e.setScheduledTaskEnabled(ctx, svc, path[0], body, identity, false)
	default:
		return nil, fmt.Errorf("scheduledTasks: unsupported: %s %v", method, path)
	}
}

func (e *GatewayExecutor) loadOwned(ctx context.Context, svc *ScheduledTaskService, taskID, userID string) (*store.ScheduledTask, error) {
	if taskID == "" {
		return nil, fmt.Errorf("scheduledTasks: task id is required")
	}
	task, err := svc.repo.GetByID(ctx, taskID)
	if err != nil {
		return nil, e.schedInternalErr("load", err)
	}
	if task == nil || task.DeletedAt != nil || !ownsTask(task, userID) {
		return nil, fmt.Errorf("scheduledTasks: task %q not found", taskID)
	}
	return task, nil
}

func (e *GatewayExecutor) listScheduledTasks(ctx context.Context, svc *ScheduledTaskService, body json.RawMessage, identity *auth.Identity) (any, error) {
	var req scheduledTaskListRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf(errInvalidScheduledTaskBody, err)
		}
	}
	userID, err := e.resolveManageUserID(identity, req.UserID)
	if err != nil {
		return nil, err
	}

	ownership := req.Ownership
	switch ownership {
	case "", "namespace", "all":
		ownership = "namespace"
	case "mine":
	default:
		return nil, fmt.Errorf("scheduledTasks: ownership must be one of: mine, namespace, all")
	}

	page := req.PageNumber
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	switch {
	case pageSize == 0:
		pageSize = defaultScheduledTaskPageSize
	case pageSize < 1 || pageSize > maxScheduledTaskPageSize:
		return nil, fmt.Errorf("scheduledTasks: pageSize must be between 1 and %d", maxScheduledTaskPageSize)
	}
	offset := (page - 1) * pageSize

	tasks, total, err := svc.ListTasks(ctx, userID, ownership, req.EnabledOnly, pageSize, offset)
	if err != nil {
		return nil, e.schedInternalErr("list", err)
	}
	dtos := make([]scheduledTaskDTO, 0, len(tasks))
	for _, t := range tasks {
		dtos = append(dtos, toScheduledTaskDTO(t))
	}
	return map[string]any{"tasks": dtos, "total": total, "pageNumber": page, "pageSize": pageSize}, nil
}

func (e *GatewayExecutor) getScheduledTask(ctx context.Context, svc *ScheduledTaskService, taskID string, body json.RawMessage, identity *auth.Identity) (any, error) {
	var req scheduledTaskUserRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf(errInvalidScheduledTaskBody, err)
		}
	}
	userID, err := e.resolveManageUserID(identity, req.UserID)
	if err != nil {
		return nil, err
	}
	task, err := e.loadOwned(ctx, svc, taskID, userID)
	if err != nil {
		return nil, err
	}
	return toScheduledTaskDTO(task), nil
}

func (e *GatewayExecutor) updateScheduledTask(ctx context.Context, svc *ScheduledTaskService, taskID string, body json.RawMessage, identity *auth.Identity) (any, error) {
	var req scheduledTaskUpdateRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf(errInvalidScheduledTaskBody, err)
		}
	}
	userID, err := e.resolveManageUserID(identity, req.UserID)
	if err != nil {
		return nil, err
	}
	task, err := e.loadOwned(ctx, svc, taskID, userID)
	if err != nil {
		return nil, err
	}
	if task.Source == "config" {
		return nil, fmt.Errorf("scheduledTasks: task %q is config-managed and cannot be edited", taskID)
	}
	if task.RunAs != nil && *task.RunAs != "" {
		return nil, fmt.Errorf("scheduledTasks: task %q runs as a service account and can only be edited by a super administrator through the scheduled tasks API", taskID)
	}

	fields, scheduleChanged, err := buildControlUpdateFields(task, req)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return toScheduledTaskDTO(task), nil
	}

	updated, err := svc.ApplyUpdate(ctx, taskID, fields, scheduleChanged)
	if err != nil {
		if errors.Is(err, ErrTaskNameConflict) {
			return nil, fmt.Errorf("scheduledTasks: a scheduled task with that name already exists")
		}
		return nil, e.schedInternalErr("update", err)
	}
	return toScheduledTaskDTO(updated), nil
}

func errBlankControlField(field string) error {
	return fmt.Errorf("scheduledTasks: %s must not be blank; omit it to leave it unchanged", field)
}

func buildControlUpdateFields(task *store.ScheduledTask, req scheduledTaskUpdateRequest) (map[string]any, bool, error) {
	fields := map[string]any{}
	scheduleChanged := false

	if req.Name != nil {
		if trimmed := strings.TrimSpace(*req.Name); trimmed != "" {
			fields["name"] = trimmed
		}
	}
	if req.Description != nil {
		fields["description"] = req.Description
	}
	if req.ScheduleType != nil {
		switch *req.ScheduleType {
		case "cron", "interval", "one_time":
			fields["schedule_type"] = *req.ScheduleType
			scheduleChanged = true
		default:
			return nil, false, fmt.Errorf("scheduledTasks: scheduleType must be one of: cron, interval, one_time")
		}
	}
	if req.ScheduleExpression != nil && *req.ScheduleExpression != "" {
		expr := strings.TrimSpace(*req.ScheduleExpression)
		if expr == "" {
			return nil, false, errBlankControlField("scheduleExpression")
		}
		fields["schedule_expression"] = expr
		scheduleChanged = true
	}
	if req.Timezone != nil && *req.Timezone != "" {
		tz, ok := NormalizeUserTimezone(*req.Timezone)
		if !ok {
			return nil, false, fmt.Errorf("scheduledTasks: invalid timezone: %s", *req.Timezone)
		}
		fields["timezone"] = tz
		scheduleChanged = true
	}
	if req.Message != nil && *req.Message != "" && strings.TrimSpace(*req.Message) == "" {
		return nil, false, errBlankControlField("message")
	}
	if req.Message != nil && *req.Message != "" {
		b, err := json.Marshal([]map[string]any{{"kind": "text", "text": *req.Message}})
		if err != nil {
			return nil, false, fmt.Errorf("scheduledTasks: encode message: %w", err)
		}
		fields["task_message"] = string(b)
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
		if err := validate(scheduler.ScheduleType(st), expr, tz); err != nil {
			return nil, false, fmt.Errorf("scheduledTasks: invalid schedule expression: %s", err.Error())
		}
		if expr != task.ScheduleExpression {
			fields["schedule_expression"] = expr
		}
	}
	return fields, scheduleChanged, nil
}

func (e *GatewayExecutor) deleteScheduledTask(ctx context.Context, svc *ScheduledTaskService, taskID string, body json.RawMessage, identity *auth.Identity) (any, error) {
	var req scheduledTaskUserRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf(errInvalidScheduledTaskBody, err)
		}
	}
	userID, err := e.resolveManageUserID(identity, req.UserID)
	if err != nil {
		return nil, err
	}
	task, err := e.loadOwned(ctx, svc, taskID, userID)
	if err != nil {
		return nil, err
	}
	if task.Source == "config" {
		return nil, fmt.Errorf("scheduledTasks: task %q is config-managed and cannot be deleted", taskID)
	}
	if err := svc.DeleteTask(ctx, taskID, userID); err != nil {
		return nil, e.schedInternalErr("delete", err)
	}
	return map[string]any{"success": true, "id": taskID, "deleted": true}, nil
}

func (e *GatewayExecutor) setScheduledTaskEnabled(ctx context.Context, svc *ScheduledTaskService, taskID string, body json.RawMessage, identity *auth.Identity, enabled bool) (any, error) {
	var req scheduledTaskUserRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf(errInvalidScheduledTaskBody, err)
		}
	}
	userID, err := e.resolveManageUserID(identity, req.UserID)
	if err != nil {
		return nil, err
	}
	if _, loadErr := e.loadOwned(ctx, svc, taskID, userID); loadErr != nil {
		return nil, loadErr
	}

	if enabled {
		updated, enErr := svc.EnableTask(ctx, taskID)
		if enErr != nil {
			return nil, e.schedInternalErr("enable", enErr)
		}
		return toScheduledTaskDTO(updated), nil
	}
	if disErr := svc.DisableTask(ctx, taskID); disErr != nil {
		return nil, e.schedInternalErr("disable", disErr)
	}
	reloaded, err := svc.repo.GetByID(ctx, taskID)
	if err != nil || reloaded == nil {
		if err != nil {
			e.logger.Warn("reload after disable failed; returning minimal result", "taskID", taskID, "error", err)
		}
		return map[string]any{"success": true, "id": taskID, "enabled": false}, nil
	}
	return toScheduledTaskDTO(reloaded), nil
}
