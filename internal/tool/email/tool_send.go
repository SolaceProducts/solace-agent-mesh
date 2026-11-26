// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type SendEmailTool struct {
	tool.SideEffectingTool
	connector *Connector
}

func NewSendEmailTool(connector *Connector) *SendEmailTool {
	return &SendEmailTool{connector: connector}
}

func (t *SendEmailTool) Name() string { return "send_email" }

func (t *SendEmailTool) Description() string {
	return "Sends an email to specified recipients. " +
		"Supports plain text and HTML bodies, attachments from artifacts, and email threading. " +
		"Recipients must be in the configured domain allowlist. " +
		"For threading (replies), provide in_reply_to with the original Message-ID."
}

func (t *SendEmailTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"to": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Recipient email addresses (must be in connector's domain allowlist).",
			},
			"cc": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "CC recipients (optional).",
			},
			"bcc": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "BCC recipients (optional).",
			},
			"subject": map[string]any{
				"type":        "string",
				"description": "Email subject line.",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "Email body (plain text).",
			},
			"body_html": map[string]any{
				"type":        "string",
				"description": "HTML body (optional, for rich formatting).",
			},
			"attachments": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Artifact filenames to attach (optional).",
			},
			"in_reply_to": map[string]any{
				"type":        "string",
				"description": "Message-ID to reply to (from Email Gateway, for threading).",
			},
			"references": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Message-ID reference chain (from Email Gateway, for threading).",
			},
		},
		Required: []string{"to", "subject"},
	}
}

func (t *SendEmailTool) RequiredScopes() []string {
	return []string{rbac.ScopeToolEmailSend}
}

func (t *SendEmailTool) Instructions() string { return "" }

func (t *SendEmailTool) IsLongRunning() bool { return false }

func (t *SendEmailTool) RawStringArgs() []string { return nil }

func (t *SendEmailTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return map[string]tool.ArtifactParamInfo{
		"attachments": {IsList: true, IsOptional: true},
	}
}

func (t *SendEmailTool) ContextParam() string { return "" }

func (t *SendEmailTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	start := time.Now()

	to, err := parseStringSlice(args, "to")
	if err != nil {
		return tool.Error(err.Error()), nil
	}
	if len(to) == 0 {
		return tool.Error("at least one recipient is required in 'to'"), nil
	}

	cc, err := parseStringSlice(args, "cc")
	if err != nil {
		return tool.Error(err.Error()), nil
	}
	bcc, err := parseStringSlice(args, "bcc")
	if err != nil {
		return tool.Error(err.Error()), nil
	}

	subject, _ := args["subject"].(string)
	if subject == "" {
		return tool.Error("subject is required"), nil
	}

	body, _ := args["body"].(string)
	bodyHTML, _ := args["body_html"].(string)
	if body == "" && bodyHTML == "" {
		return tool.Error("either 'body' or 'body_html' is required"), nil
	}
	inReplyTo, _ := args["in_reply_to"].(string)
	references, err := parseStringSlice(args, "references")
	if err != nil {
		return tool.Error(err.Error()), nil
	}

	msg := &Message{
		To:         to,
		CC:         cc,
		BCC:        bcc,
		Subject:    subject,
		BodyPlain:  body,
		BodyHTML:   bodyHTML,
		InReplyTo:  inReplyTo,
		References: references,
	}

	attachments, attachErr := t.attachments(ctx, args, toolCtx)
	if attachErr != nil {
		return mapError(attachErr), nil
	}
	msg.Attachments = attachments

	messageID, err := t.connector.Send(ctx, msg, toolCtx)

	entry := &AuditEntry{
		Timestamp:   start,
		AgentName:   toolCtx.AgentName,
		UserID:      toolCtx.UserID,
		SessionID:   toolCtx.SessionID,
		Recipients:  msg.AllRecipients(),
		Subject:     subject,
		HasHTML:     bodyHTML != "",
		Attachments: len(msg.Attachments),
		InReplyTo:   inReplyTo,
		DurationMs:  time.Since(start).Milliseconds(),
	}

	if err != nil {
		entry.Result = "error"
		entry.ErrorType = errorType(err)
		entry.ErrorMessage = err.Error()
		t.connector.LogAudit(ctx, entry)
		return mapError(err), nil
	}

	entry.Result = "sent"
	entry.MessageID = messageID
	t.connector.LogAudit(ctx, entry)

	return tool.OK(
		fmt.Sprintf("Email sent successfully to %d recipients", len(msg.AllRecipients())),
		tool.WithData(map[string]any{
			"message_id":      messageID,
			"recipients":      len(msg.AllRecipients()),
			"has_attachments": len(msg.Attachments) > 0,
		}),
	), nil
}

func (t *SendEmailTool) attachments(ctx context.Context, args map[string]any, toolCtx *tool.Context) ([]Attachment, error) {
	if preloaded, ok := args["attachments"].([]*tool.Artifact); ok {
		return t.sizeCheckedAttachments(preloaded)
	}
	filenames, err := parseStringSlice(args, "attachments")
	if err != nil {
		return nil, err
	}
	if len(filenames) == 0 {
		return nil, nil
	}
	loaded, err := t.loadAttachments(ctx, filenames, toolCtx)
	if err != nil {
		return nil, err
	}
	return t.sizeCheckedAttachments(loaded)
}

func (t *SendEmailTool) loadAttachments(ctx context.Context, filenames []string, toolCtx *tool.Context) ([]*tool.Artifact, error) {
	if toolCtx.ArtifactService == nil {
		return nil, errors.New("artifact service not available for attachments")
	}

	facade := toolCtx.Facade()
	loaded := make([]*tool.Artifact, 0, len(filenames))
	for _, filename := range filenames {
		name, version := artifact.SplitVersionRef(filename)
		content, mimeType, err := facade.LoadArtifact(ctx, name, version)
		if err != nil {
			return nil, fmt.Errorf("failed to load attachment %q: %w", filename, err)
		}
		if content == nil {
			return nil, &AttachmentError{Filename: filename, Reason: "not_found"}
		}
		loaded = append(loaded, &tool.Artifact{Filename: name, Content: content, MIMEType: mimeType})
	}
	return loaded, nil
}

func (t *SendEmailTool) sizeCheckedAttachments(loaded []*tool.Artifact) ([]Attachment, error) {
	cfg := t.connector.Config()
	var totalSize int64
	attachments := make([]Attachment, 0, len(loaded))
	for _, art := range loaded {
		size := int64(len(art.Content))
		if size > cfg.MaxAttachmentSizeBytes {
			return nil, &AttachmentError{
				Filename: art.Filename,
				Size:     size,
				MaxSize:  cfg.MaxAttachmentSizeBytes,
				Reason:   "size_exceeded",
			}
		}
		totalSize += size
		if totalSize > cfg.MaxTotalAttachmentBytes {
			return nil, &AttachmentError{
				Filename: art.Filename,
				Size:     totalSize,
				MaxSize:  cfg.MaxTotalAttachmentBytes,
				Reason:   "total_exceeded",
			}
		}
		attachments = append(attachments, Attachment{
			Filename: art.Filename,
			Content:  art.Content,
			MIMEType: art.MIMEType,
		})
	}
	return attachments, nil
}

func parseStringSlice(args map[string]any, key string) ([]string, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return nil, nil
	}

	switch val := v.(type) {
	case []string:
		return val, nil
	case []any:
		result := make([]string, 0, len(val))
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] is not a string", key, i)
			}
			result = append(result, s)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}

func mapError(err error) *tool.Result {
	var allowErr *AllowlistError
	var throttleErr *ThrottleError
	var smtpErr *SMTPError
	var attachErr *AttachmentError
	var authErr *AuthError
	var connErr *ConnectionError

	switch {
	case errors.As(err, &allowErr):
		return tool.Error(allowErr.Error(), tool.WithData(map[string]any{
			"error_type":      "allowlist_error",
			"domain":          allowErr.Domain,
			"allowed_domains": allowErr.AllowedDomains,
		}))
	case errors.As(err, &throttleErr):
		return tool.Error(throttleErr.Error(), tool.WithData(map[string]any{
			"error_type":          "throttle_error",
			"retry_after_seconds": int(throttleErr.RetryAfter.Seconds()),
		}))
	case errors.As(err, &smtpErr):
		return tool.Error(smtpErr.Error(), tool.WithData(map[string]any{
			"error_type": "smtp_reject",
			"smtp_code":  smtpErr.Code,
		}))
	case errors.As(err, &attachErr):
		data := map[string]any{
			"error_type": "attachment_error",
			"reason":     attachErr.Reason,
			"filename":   attachErr.Filename,
		}
		if attachErr.Reason != "not_found" {
			data["max_size_bytes"] = attachErr.MaxSize
		}
		return tool.Error(attachErr.Error(), tool.WithData(data))
	case errors.As(err, &authErr):
		return tool.Error(authErr.Error(), tool.WithData(map[string]any{
			"error_type": "auth_error",
		}))
	case errors.As(err, &connErr):
		return tool.Error(connErr.Error(), tool.WithData(map[string]any{
			"error_type": "connection_error",
		}))
	default:
		return tool.Error(fmt.Sprintf("email send failed: %v", err))
	}
}

func errorType(err error) string {
	var allowErr *AllowlistError
	var throttleErr *ThrottleError
	var smtpErr *SMTPError
	var attachErr *AttachmentError
	var authErr *AuthError
	var connErr *ConnectionError

	switch {
	case errors.As(err, &allowErr):
		return "allowlist_error"
	case errors.As(err, &throttleErr):
		return "throttle_error"
	case errors.As(err, &smtpErr):
		return "smtp_reject"
	case errors.As(err, &attachErr):
		return "attachment_error"
	case errors.As(err, &authErr):
		return "auth_error"
	case errors.As(err, &connErr):
		return "connection_error"
	default:
		return "unknown_error"
	}
}
