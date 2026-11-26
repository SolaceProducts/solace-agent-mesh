// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/scheduler"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const scheduleTaskToolName = "schedule_task"

type ScheduleTaskTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *ScheduleTaskTool) Name() string { return scheduleTaskToolName }

func (t *ScheduleTaskTool) Description() string {
	return "Schedule a workflow or agent to be invoked later — once at a specific " +
		"time, or repeatedly on a cron or interval schedule. Use this to set a " +
		"timer/reminder, run a follow-up after a delay, escalate if something " +
		"hasn't happened, or kick off recurring work. The scheduled invocation " +
		"runs as a fresh task under your identity and delivers the given message " +
		"to the target; it does NOT block the current task. Returns once the " +
		"schedule request has been accepted by the broker (the gateway then " +
		"creates the durable task), not when it fires."
}

func (t *ScheduleTaskTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"target_name": map[string]any{
				"type":        "string",
				"description": "Name of the workflow or agent to invoke when the schedule fires. Required.",
			},
			"target_type": map[string]any{
				"type":        "string",
				"enum":        []any{"agent", "workflow"},
				"description": "Whether target_name is an 'agent' or a 'workflow'. Defaults to 'agent'.",
			},
			"schedule_type": map[string]any{
				"type":        "string",
				"enum":        []any{"one_time", "cron", "interval"},
				"description": "How the schedule is expressed. Required.",
			},
			"schedule": map[string]any{
				"type": "string",
				"description": "The schedule expression matching schedule_type: an ISO 8601 " +
					"timestamp for 'one_time' (e.g. 2026-12-31T15:30:00Z), a cron " +
					"expression for 'cron' (e.g. '0 9 * * *'), or a duration for " +
					"'interval' (e.g. '15m', '24h'). Required.",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "The instruction/prompt delivered to the target when it fires. Required.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Optional human-readable label for the scheduled task. A unique name is generated if omitted.",
			},
			"timezone": map[string]any{
				"type":        "string",
				"description": "Optional IANA timezone for cron/one_time schedules (e.g. 'America/New_York'). Defaults to UTC.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Optional max seconds the fired invocation may run before it is marked timed out. Defaults to 3600; at most 604800 (7 days).",
			},
		},
		Required: []string{"target_name", "schedule_type", "schedule", "message"},
	}
}

func (t *ScheduleTaskTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "status", typ: "string", description: "Always the literal \"scheduled\" — the schedule was durably accepted, not that the task has fired."},
		resultField{name: "scheduled_task_id", typ: "string", description: "Id of the created scheduled task; pass it to the list, update, delete and enable tools."},
		resultField{name: "name", typ: "string", description: "Task name — the supplied name, or a generated scheduled-<id> when it was omitted."},
		resultField{name: "target_name", typ: "string", description: "Agent or workflow that will be invoked when the schedule fires."},
		resultField{name: "schedule_type", typ: "string", description: "The schedule kind requested: one_time, cron or interval."},
	)
}

func (t *ScheduleTaskTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	targetName, _ := args["target_name"].(string)
	scheduleType, _ := args["schedule_type"].(string)
	schedule, _ := args["schedule"].(string)
	message, _ := args["message"].(string)

	switch {
	case targetName == "":
		return tool.Error("target_name is required (the workflow or agent to invoke)."), nil
	case scheduleType == "":
		return tool.Error("schedule_type is required (one_time, cron, or interval)."), nil
	case schedule == "":
		return tool.Error("schedule is required (the timestamp, cron expression, or interval)."), nil
	case message == "":
		return tool.Error("message is required (the instruction delivered to the target)."), nil
	}

	if toolCtx.ScheduleTask == nil {
		return tool.Error("Scheduling is not available in this environment " +
			"(no scheduled-task service is wired)."), nil
	}

	timeoutSeconds, ok := optionalIntArg(args, "timeout_seconds")
	if !ok {
		return tool.Error("timeout_seconds must be a whole number of seconds."), nil
	}
	if rejection := scheduleRequestRejection(args, scheduleType, schedule, message, timeoutSeconds); rejection != "" {
		return tool.Error(rejection), nil
	}

	requestID := idgen.New()
	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "scheduled-" + requestID
	}
	targetType, _ := args["target_type"].(string)
	timezone, _ := toolTimezone(args)

	metadata := map[string]any{}
	if toolCtx.ParentTaskID != "" {
		metadata["task_id"] = toolCtx.ParentTaskID
	}
	if toolCtx.TraceID != "" {
		metadata["trace_id"] = toolCtx.TraceID
	}

	req := a2a.ScheduleRequest{
		RequestID:          requestID,
		Name:               name,
		ScheduleType:       scheduleType,
		ScheduleExpression: schedule,
		Timezone:           timezone,
		TargetType:         targetType,
		TargetName:         targetName,
		TaskMessage:        message,
		Metadata:           metadata,
		TimeoutSeconds:     timeoutSeconds,
		SenderUserID:       toolCtx.UserID,
		SenderAgent:        toolCtx.AgentName,
		Source:             "chat",
	}

	if err := toolCtx.ScheduleTask(ctx, req); err != nil {
		return tool.Error("Failed to schedule the task: " + err.Error()), nil
	}

	resultData := map[string]any{
		"status":            "scheduled",
		"scheduled_task_id": requestID,
		"name":              name,
		"target_name":       targetName,
		"schedule_type":     scheduleType,
	}
	resultJSON, _ := json.Marshal(resultData)
	return tool.OK(string(resultJSON), tool.WithData(resultData)), nil
}

func scheduleRequestRejection(args map[string]any, scheduleType, schedule, message string, timeoutSeconds int) string {
	if strings.TrimSpace(message) == "" {
		return "message must not be blank (the instruction delivered to the target)."
	}
	if timeoutSeconds < 0 || timeoutSeconds > scheduler.MaxTimeoutSeconds {
		return fmt.Sprintf("timeout_seconds must be between %d and %d; omit it for the default.", scheduler.MinTimeoutSeconds, scheduler.MaxTimeoutSeconds)
	}
	timezone, ok := toolTimezone(args)
	if !ok {
		return "timezone must be an IANA timezone name such as America/New_York."
	}
	if strings.EqualFold(timezone, "Local") {
		return "timezone must be an IANA timezone name such as America/New_York, not Local."
	}
	if err := scheduler.ValidateExpression(scheduler.ScheduleType(scheduleType), schedule, timezone); err != nil {
		return "schedule is invalid: " + err.Error()
	}
	return ""
}

func toolTimezone(args map[string]any) (string, bool) {
	v, present := args["timezone"]
	if !present || v == nil {
		return "", true
	}
	timezone, ok := v.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(timezone), true
}

func optionalIntArg(args map[string]any, key string) (value int, ok bool) {
	v, present := args[key]
	if !present || v == nil {
		return 0, true
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
