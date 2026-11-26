// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const scheduledTasksResource = "scheduledTasks"

func callScheduledTaskControl(ctx context.Context, toolCtx *tool.Context, method, path string, body map[string]any) (map[string]any, *tool.Result) {
	if toolCtx.ControlQuerier == nil {
		return nil, tool.Error("Scheduled-task management is not available (no gateway with the scheduler is reachable).")
	}
	if body == nil {
		body = map[string]any{}
	}
	if _, ok := body["userId"]; !ok && toolCtx.UserID != "" {
		body["userId"] = toolCtx.UserID
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, tool.Error(fmt.Sprintf("Failed to encode request: %s", err))
	}
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, method, path, encoded)
	if err != nil {
		return nil, tool.Error(fmt.Sprintf("Scheduled-task request failed: %s", err))
	}
	var parsed map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, tool.Error(fmt.Sprintf("Failed to parse scheduled-task response: %s", err))
		}
	}
	return parsed, nil
}

type ListScheduledTasksTool struct {
	artifactToolBase
	tool.ReadOnlyTool
}

func (t *ListScheduledTasksTool) Name() string { return "list_scheduled_tasks" }

func (t *ListScheduledTasksTool) Description() string {
	return "List the scheduled tasks you own (recurring or one-time jobs that fire an agent/workflow " +
		"later). Returns each task's id, name, schedule, target, whether it's enabled, and next/last run " +
		"times. Use the returned ids with update_scheduled_task, delete_scheduled_task, or " +
		"set_scheduled_task_enabled."
}

func (t *ListScheduledTasksTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"enabled_only": map[string]any{
				"type":        "boolean",
				"description": "When true, return only enabled tasks. Defaults to false (all tasks).",
			},
		},
	}
}

func (t *ListScheduledTasksTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "tasks", typ: "array", description: "The page of scheduled tasks; each element has the fields listed on the update tool. Empty when none matched."},
		resultField{name: "total", typ: "integer", description: "Total tasks matching the query across every page, not just this one."},
		resultField{name: "pageNumber", typ: "integer", description: "One-based page number that was served."},
		resultField{name: "pageSize", typ: "integer", description: "Page size that was served."},
	)
}

func (t *ListScheduledTasksTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	body := map[string]any{}
	if v, ok := args["enabled_only"].(bool); ok {
		body["enabledOnly"] = v
	}
	parsed, errResult := callScheduledTaskControl(ctx, toolCtx, "get", scheduledTasksResource, body)
	if errResult != nil {
		return errResult, nil
	}
	resultJSON, _ := json.Marshal(parsed)
	return tool.OK(string(resultJSON), tool.WithData(parsed)), nil
}

type UpdateScheduledTaskTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *UpdateScheduledTaskTool) Name() string { return "update_scheduled_task" }

func (t *UpdateScheduledTaskTool) Description() string {
	return "Change an existing scheduled task's schedule, message, name, description, or timezone. " +
		"Only the fields you supply are changed. Identify the task by task_id (from list_scheduled_tasks). " +
		"To turn a task on or off use set_scheduled_task_enabled instead; to remove it use " +
		"delete_scheduled_task."
}

func (t *UpdateScheduledTaskTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID of the scheduled task to update (from list_scheduled_tasks). Required.",
			},
			"name":        map[string]any{"type": "string", "description": "New human-readable name."},
			"description": map[string]any{"type": "string", "description": "New description."},
			"schedule_type": map[string]any{
				"type":        "string",
				"enum":        []any{"one_time", "cron", "interval"},
				"description": "New schedule kind. Provide schedule_expression to match when you change this.",
			},
			"schedule_expression": map[string]any{
				"type": "string",
				"description": "New schedule expression matching schedule_type: an ISO 8601 timestamp for " +
					"one_time, a cron expression for cron (e.g. '0 9 * * *'), or a duration for interval (e.g. '15m').",
			},
			"timezone": map[string]any{
				"type":        "string",
				"description": "New IANA timezone for cron/one_time schedules (e.g. 'America/New_York').",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "New instruction/prompt delivered to the target when the task fires.",
			},
		},
		Required: []string{"task_id"},
	}
}

func (t *UpdateScheduledTaskTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(scheduledTaskFields(
		"id", "name", "scheduleType", "scheduleExpression", "timezone",
		"targetAgentName", "targetType", "taskMessage", "enabled", "source",
		"runCount", "createdBy",
	)...)
}

func (t *UpdateScheduledTaskTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return tool.Error("task_id is required (the id of the scheduled task to update)."), nil
	}
	body := map[string]any{}
	for _, k := range []string{"name", "description", "timezone", "message"} {
		if v, ok := args[k].(string); ok && v != "" {
			body[k] = v
		}
	}
	if v, ok := args["schedule_type"].(string); ok && v != "" {
		body["scheduleType"] = v
	}
	if v, ok := args["schedule_expression"].(string); ok && v != "" {
		body["scheduleExpression"] = v
	}
	if len(body) == 0 {
		return tool.Error("Nothing to update: supply at least one of name, description, schedule_type, schedule_expression, timezone, or message."), nil
	}
	parsed, errResult := callScheduledTaskControl(ctx, toolCtx, "patch", scheduledTasksResource+"/"+taskID, body)
	if errResult != nil {
		return errResult, nil
	}
	resultJSON, _ := json.Marshal(parsed)
	return tool.OK(string(resultJSON), tool.WithData(parsed)), nil
}

type DeleteScheduledTaskTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *DeleteScheduledTaskTool) Name() string { return "delete_scheduled_task" }

func (t *DeleteScheduledTaskTool) Description() string {
	return "Permanently remove a scheduled task so it will no longer fire. Identify it by task_id " +
		"(from list_scheduled_tasks). This cannot be undone — confirm with the user before calling. " +
		"To pause a task without deleting it, use set_scheduled_task_enabled with enabled=false."
}

func (t *DeleteScheduledTaskTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID of the scheduled task to delete (from list_scheduled_tasks). Required.",
			},
		},
		Required: []string{"task_id"},
	}
}

func (t *DeleteScheduledTaskTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "success", typ: "boolean", description: "Always true on a completing call — the delete was accepted."},
		resultField{name: "id", typ: "string", description: "Id of the scheduled task that was deleted."},
		resultField{name: "deleted", typ: "boolean", description: "Always true; the task will no longer fire."},
	)
}

func (t *DeleteScheduledTaskTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return tool.Error("task_id is required (the id of the scheduled task to delete)."), nil
	}
	parsed, errResult := callScheduledTaskControl(ctx, toolCtx, "delete", scheduledTasksResource+"/"+taskID, nil)
	if errResult != nil {
		return errResult, nil
	}
	resultJSON, _ := json.Marshal(parsed)
	return tool.OK(string(resultJSON), tool.WithData(parsed)), nil
}

type SetScheduledTaskEnabledTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *SetScheduledTaskEnabledTool) Name() string { return "set_scheduled_task_enabled" }

func (t *SetScheduledTaskEnabledTool) Description() string {
	return "Enable or disable (pause) a scheduled task without deleting it. Set enabled=false to pause " +
		"a task so it stops firing, or enabled=true to resume it. Identify the task by task_id (from " +
		"list_scheduled_tasks)."
}

func (t *SetScheduledTaskEnabledTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "ID of the scheduled task (from list_scheduled_tasks). Required.",
			},
			"enabled": map[string]any{
				"type":        "boolean",
				"description": "true to enable/resume the task, false to disable/pause it. Required.",
			},
		},
		Required: []string{"task_id", "enabled"},
	}
}

var scheduledTaskDTOFields = []resultField{
	{name: "id", typ: "string", description: "The scheduled task's id."},
	{name: "name", typ: "string", description: "Human-readable task name."},
	{name: "description", typ: "string", description: "Free-text description. Omitted when the task has none.", optional: true},
	{name: "scheduleType", typ: "string", description: "Schedule kind: one_time, cron or interval."},
	{name: "scheduleExpression", typ: "string", description: "The schedule itself — an ISO 8601 timestamp, a cron expression or a duration, matching scheduleType."},
	{name: "timezone", typ: "string", description: "IANA timezone the schedule is evaluated in."},
	{name: "targetAgentName", typ: "string", description: "Agent or workflow invoked when the task fires."},
	{name: "targetType", typ: "string", description: "Whether the target is an agent or a workflow."},
	{name: "taskMessage", typ: "string", description: "Plain-text instruction delivered to the target when the task fires."},
	{name: "enabled", typ: "boolean", description: "Whether the task is currently armed."},
	{name: "source", typ: "string", description: "How the task was created, e.g. chat, config or web."},
	{name: "runCount", typ: "integer", description: "How many times the task has fired so far."},
	{name: "nextRunTime", typ: "string", description: "Next scheduled fire time, ISO 8601 UTC. Omitted when nothing is scheduled.", optional: true},
	{name: "lastRunTime", typ: "string", description: "Last fire time, ISO 8601 UTC. Omitted when the task has never run.", optional: true},
	{name: "createdBy", typ: "string", description: "User that created the task."},
}

func scheduledTaskFields(alwaysWritten ...string) []resultField {
	required := make(map[string]bool, len(alwaysWritten))
	for _, name := range alwaysWritten {
		required[name] = true
	}
	out := make([]resultField, 0, len(scheduledTaskDTOFields))
	for _, field := range scheduledTaskDTOFields {
		field.optional = !required[field.name]
		out = append(out, field)
	}
	return out
}

func (t *SetScheduledTaskEnabledTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(append(
		scheduledTaskFields("id", "enabled"),
		resultField{name: "success", typ: "boolean", optional: true,
			description: "Always true when present. Written only on the disable path that could not reload the task afterwards, which reports no other field."},
	)...)
}

func (t *SetScheduledTaskEnabledTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return tool.Error("task_id is required (the id of the scheduled task)."), nil
	}
	enabled, ok := args["enabled"].(bool)
	if !ok {
		return tool.Error("enabled is required (true to enable/resume, false to disable/pause)."), nil
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	parsed, errResult := callScheduledTaskControl(ctx, toolCtx, "post", scheduledTasksResource+"/"+taskID+"/"+action, nil)
	if errResult != nil {
		return errResult, nil
	}
	resultJSON, _ := json.Marshal(parsed)
	return tool.OK(string(resultJSON), tool.WithData(parsed)), nil
}
