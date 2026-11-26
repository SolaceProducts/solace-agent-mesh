// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const notifySourceTool = "notify_user"

type NotifyUserTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *NotifyUserTool) Name() string { return notifySourceTool }

func (t *NotifyUserTool) RequiredScopes() []string { return []string{rbac.ScopeNotifySend} }

func (t *NotifyUserTool) Description() string {
	return "Send a fire-and-forget notification to a user out-of-band, through " +
		"their configured channels (e.g. their web inbox, a webhook, or a " +
		"Slack/Teams/email connector). Use this to proactively inform a user of " +
		"something " +
		"that does not need a reply — for example the result of a long-running or " +
		"scheduled task, or a status update they asked to be told about. This is " +
		"NOT for asking the user a question (use ask_user_question) and NOT a " +
		"substitute for replying in the current conversation. The notification is " +
		"delivered asynchronously; this tool returns as soon as the request is " +
		"accepted, not when the user has seen it. By default the notification goes " +
		"to the current user."
}

func (t *NotifyUserTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "The notification body text. Required.",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Optional short headline shown above the message.",
			},
			"recipient_user_id": map[string]any{
				"type": "string",
				"description": "Optional. The user to notify. Defaults to the current " +
					"user. Notifying a different user may require additional permissions.",
			},
			"channels": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Optional. Restrict delivery to these channel types " +
					"(e.g. [\"web\", \"webhook\"]). When omitted, all of the " +
					"recipient's configured channels are used.",
			},
		},
		Required: []string{"message"},
	}
}

func (t *NotifyUserTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "status", typ: "string", description: "Always the literal \"accepted\" — the notification was durably queued, not that the user has seen it."},
		resultField{name: "request_id", typ: "string", description: "UUID of the queued notification request, for correlating with notification-service logs."},
		resultField{name: "recipient", typ: "string", description: "User the notification was addressed to — the recipient_user_id argument, or the calling user when it was omitted."},
	)
}

func (t *NotifyUserTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	message, _ := args["message"].(string)
	if message == "" {
		return tool.Error("A non-empty message is required to send a notification."), nil
	}

	if toolCtx.PublishNotification == nil {
		return tool.Error("Notifications are not available in this environment " +
			"(no notification service is wired). Tell the user directly instead."), nil
	}

	recipient, _ := args["recipient_user_id"].(string)
	if recipient == "" {
		recipient = toolCtx.UserID
	}
	if recipient == "" {
		return tool.Error("No recipient could be determined for the notification."), nil
	}

	title, _ := args["title"].(string)
	channels := stringSliceArg(args["channels"])

	metadata := map[string]any{}
	if toolCtx.ParentTaskID != "" {
		metadata["task_id"] = toolCtx.ParentTaskID
	}
	if toolCtx.TraceID != "" {
		metadata["trace_id"] = toolCtx.TraceID
	}
	if toolCtx.SessionID != "" {
		metadata["session_id"] = toolCtx.SessionID
	}

	req := a2a.NotifyRequest{
		RequestID:       idgen.New(),
		RecipientUserID: recipient,
		Mode:            a2a.NotifyModeNotify,
		SenderUserID:    toolCtx.UserID,
		SenderAgent:     toolCtx.AgentName,
		Category:        a2a.CategoryAgentMessage,
		Channels:        channels,
		Title:           title,
		Message:         message,
		Metadata:        metadata,
	}

	if err := toolCtx.PublishNotification(ctx, req); err != nil {
		return tool.Error("Failed to queue the notification: " + err.Error()), nil
	}

	resultData := map[string]any{
		"status":     "accepted",
		"request_id": req.RequestID,
		"recipient":  recipient,
	}
	resultJSON, _ := json.Marshal(resultData)
	return tool.OK(string(resultJSON), tool.WithData(resultData)), nil
}
