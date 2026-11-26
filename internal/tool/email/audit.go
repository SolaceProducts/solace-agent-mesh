// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"log/slog"
	"time"
)

type AuditEntry struct {
	Timestamp    time.Time
	ConnectorID  string
	AgentName    string
	UserID       string
	SessionID    string
	Recipients   []string
	Subject      string
	HasHTML      bool
	Attachments  int
	InReplyTo    string
	Result       string
	ErrorType    string
	ErrorMessage string
	MessageID    string
	DurationMs   int64
}

type Auditor struct {
	logger      *slog.Logger
	connectorID string
}

func NewAuditor(logger *slog.Logger, connectorID string) *Auditor {
	return &Auditor{
		logger:      logger.With("logger_name", "email_connector"),
		connectorID: connectorID,
	}
}

func (a *Auditor) LogSend(_ context.Context, entry *AuditEntry) {
	if entry.ConnectorID == "" {
		entry.ConnectorID = a.connectorID
	}

	attrs := []any{
		"connectorID", entry.ConnectorID,
		"agentName", entry.AgentName,
		"userID", entry.UserID,
		"sessionID", entry.SessionID,
		"recipientCount", len(entry.Recipients),
		"hasHTML", entry.HasHTML,
		"attachments", entry.Attachments,
		"result", entry.Result,
		"duration_ms", entry.DurationMs,
	}

	if entry.InReplyTo != "" {
		attrs = append(attrs, "inReplyTo", entry.InReplyTo)
	}

	if entry.MessageID != "" {
		attrs = append(attrs, "messageID", entry.MessageID)
	}

	if entry.ErrorType != "" {
		attrs = append(attrs, "errorType", entry.ErrorType)
	}

	if entry.ErrorMessage != "" {
		attrs = append(attrs, "errorMessage", entry.ErrorMessage)
	}

	switch entry.Result {
	case "sent":
		a.logger.Info("email_sent", attrs...)
	case "rejected", "throttled":
		a.logger.Warn("email_blocked", attrs...)
	case "error":
		a.logger.Warn("email_failed", attrs...)
	default:
		a.logger.Info("email_connector_send", attrs...)
	}
}
