// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ScheduledTaskRedirectTool struct {
	tool.ReadOnlyTool
	artifactToolBase
}

func NewScheduledTaskRedirectTool() *ScheduledTaskRedirectTool {
	return &ScheduledTaskRedirectTool{}
}

func (t *ScheduledTaskRedirectTool) Name() string { return "scheduled_task_redirect" }

func (t *ScheduledTaskRedirectTool) Description() string {
	return "Redirect the user to the Scheduled Tasks page. " +
		"Use this when the user wants to schedule a task (mode=create), see their scheduled tasks (mode=view), " +
		"modify an existing scheduled task (mode=edit, requires task_id), or delete one (mode=delete, requires task_id). " +
		"The destination page renders a preview panel so the user can review and adjust before saving."
}

func (t *ScheduledTaskRedirectTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"context": map[string]any{
				"type": "string",
				"description": "A one-to-two sentence summary of what the user wants to schedule, change, or remove. " +
					"For mode=create this becomes the prompt pre-filled into the AI builder. " +
					"For mode=edit this describes the change the user wants to make. " +
					"For mode=delete name the task being deleted. " +
					"For mode=view a short framing line (e.g. 'showing your scheduled tasks').",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"create", "view", "edit", "delete"},
				"description": "create: send user to the AI builder with the prompt pre-filled. view: open the scheduled-tasks list. edit: open the edit panel for task_id with the requested change shown as a suggestion. delete: open the scheduled-tasks list with a delete confirmation modal for task_id.",
			},
			"task_id": map[string]any{
				"type":        "string",
				"description": "Scheduled task ID. REQUIRED when mode is 'edit' or 'delete'. Omit for 'create' and 'view'.",
			},
			"artifacts": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Filenames of artifacts uploaded in this conversation that are relevant to the scheduled task. Copied into the destination session.",
			},
		},
		Required: []string{"context", "mode"},
	}
}

func (t *ScheduledTaskRedirectTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "type", typ: "string", description: "Always the literal \"redirect\"; marks the payload the web UI renders as a navigation button.", optional: true},
		resultField{name: "route", typ: "string", description: "Always the literal \"/scheduled-tasks\" — the destination page.", optional: true},
		resultField{name: "label", typ: "string", description: "Button text, derived from the requested mode.", optional: true},
		resultField{name: "context", typ: "string", description: "The caller's summary of what the user wants to schedule, change or remove.", optional: true},
		resultField{name: "url_params", typ: "object", description: "Query parameters for the destination page: always mode, plus taskId when one was supplied.", optional: true},
		resultField{name: "artifacts", typ: "array", description: "Filenames of conversation artifacts to copy into the destination session. Written only when at least one was supplied and the call has a session.", optional: true},
		resultField{name: "source_session_id", typ: "string", description: "Session the artifacts are copied from. Written together with artifacts.", optional: true},
	)
}

func (t *ScheduledTaskRedirectTool) Execute(_ context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	ctxStr, _ := args["context"].(string)
	if ctxStr == "" {
		return tool.Error("context parameter is required"), nil
	}

	mode, _ := args["mode"].(string)
	label, ok := labelForMode(mode)
	if !ok {
		return tool.Error("mode must be one of: create, view, edit, delete"), nil
	}

	taskID, _ := args["task_id"].(string)
	if (mode == "edit" || mode == "delete") && taskID == "" {
		return tool.Error(fmt.Sprintf("task_id is required when mode=%s", mode)), nil
	}

	if toolCtx == nil || !toolCtx.GatewayCapabilities["web_ui_redirect"] {
		return tool.OK(
			"Managing scheduled tasks from this gateway isn't supported yet — please open the Solace Agent Mesh web UI and visit Scheduled Tasks to " +
				modeAction(mode) + ".",
		), nil
	}

	urlParams := map[string]any{"mode": mode}
	if taskID != "" {
		urlParams["taskId"] = taskID
	}

	var artifacts []string
	if arr, ok := args["artifacts"].([]any); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				artifacts = append(artifacts, s)
			}
		}
	}

	data := map[string]any{
		"type":       "redirect",
		"route":      "/scheduled-tasks",
		"label":      label,
		"context":    ctxStr,
		"url_params": urlParams,
	}

	if len(artifacts) > 0 && toolCtx.SessionID != "" {
		data["artifacts"] = artifacts
		data["source_session_id"] = toolCtx.SessionID
	}

	return tool.OK(
		"Scheduled Tasks redirect prepared. The UI renders the button; do not narrate the link, the button, or skill mechanics in your next turn — your earlier preview message already told the user what they'll be able to do.",
		tool.WithData(data),
	), nil
}

func labelForMode(mode string) (string, bool) {
	switch mode {
	case "create":
		return "Create in Scheduled Tasks", true
	case "view":
		return "Open Scheduled Tasks", true
	case "edit":
		return "Review changes in Scheduled Tasks", true
	case "delete":
		return "Confirm in Scheduled Tasks", true
	default:
		return "", false
	}
}

func modeAction(mode string) string {
	switch mode {
	case "create":
		return "create one"
	case "edit":
		return "edit the task"
	case "delete":
		return "delete the task"
	default:
		return "manage your tasks"
	}
}
