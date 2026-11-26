// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewAuditor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	auditor := NewAuditor(logger, "test-connector")

	assert.NotNil(t, auditor)
	assert.Equal(t, "test-connector", auditor.connectorID)
}

func TestAuditor_LogSend(t *testing.T) {
	tests := []struct {
		name       string
		entry      *AuditEntry
		wantLevel  string
		wantFields []string
	}{
		{
			name: "sent email",
			entry: &AuditEntry{
				Timestamp:   time.Now(),
				AgentName:   "test-agent",
				UserID:      "test-user",
				SessionID:   "test-session",
				Recipients:  []string{"user@example.com"},
				Subject:     "Test Subject",
				HasHTML:     false,
				Attachments: 0,
				Result:      "sent",
				MessageID:   "<msg-123@example.com>",
				DurationMs:  50,
			},
			wantLevel:  "INFO",
			wantFields: []string{"email_sent", "test-agent", "test-user", "msg-123"},
		},
		{
			name: "rejected email",
			entry: &AuditEntry{
				Timestamp:    time.Now(),
				AgentName:    "test-agent",
				UserID:       "test-user",
				SessionID:    "test-session",
				Recipients:   []string{"user@notallowed.com"},
				Subject:      "Test",
				Result:       "rejected",
				ErrorType:    "allowlist_error",
				ErrorMessage: "domain not allowed",
				DurationMs:   10,
			},
			wantLevel:  "WARN",
			wantFields: []string{"email_blocked", "allowlist_error", "domain not allowed"},
		},
		{
			name: "throttled email",
			entry: &AuditEntry{
				Timestamp:    time.Now(),
				AgentName:    "test-agent",
				UserID:       "test-user",
				SessionID:    "test-session",
				Recipients:   []string{"user@example.com"},
				Subject:      "Test",
				Result:       "throttled",
				ErrorType:    "throttle_error",
				ErrorMessage: "rate limit exceeded",
				DurationMs:   5,
			},
			wantLevel:  "WARN",
			wantFields: []string{"email_blocked", "throttle_error"},
		},
		{
			name: "error email",
			entry: &AuditEntry{
				Timestamp:    time.Now(),
				AgentName:    "test-agent",
				UserID:       "test-user",
				SessionID:    "test-session",
				Recipients:   []string{"user@example.com"},
				Subject:      "Test",
				Result:       "error",
				ErrorType:    "smtp_reject",
				ErrorMessage: "550 User not found",
				DurationMs:   100,
			},
			wantLevel:  "WARN",
			wantFields: []string{"email_failed", "smtp_reject", "550 User not found"},
		},
		{
			name: "unknown result",
			entry: &AuditEntry{
				Timestamp:  time.Now(),
				AgentName:  "test-agent",
				UserID:     "test-user",
				SessionID:  "test-session",
				Recipients: []string{"user@example.com"},
				Subject:    "Test",
				Result:     "unknown",
				DurationMs: 10,
			},
			wantLevel:  "INFO",
			wantFields: []string{"email_connector_send"},
		},
		{
			name: "with in_reply_to",
			entry: &AuditEntry{
				Timestamp:  time.Now(),
				AgentName:  "test-agent",
				UserID:     "test-user",
				SessionID:  "test-session",
				Recipients: []string{"user@example.com"},
				Subject:    "Re: Test",
				InReplyTo:  "<original@example.com>",
				Result:     "sent",
				MessageID:  "<reply@example.com>",
				DurationMs: 50,
			},
			wantLevel:  "INFO",
			wantFields: []string{"inReplyTo", "original@example.com"},
		},
		{
			name: "with attachments and html",
			entry: &AuditEntry{
				Timestamp:   time.Now(),
				AgentName:   "test-agent",
				UserID:      "test-user",
				SessionID:   "test-session",
				Recipients:  []string{"user@example.com"},
				Subject:     "Test with attachments",
				HasHTML:     true,
				Attachments: 2,
				Result:      "sent",
				MessageID:   "<msg@example.com>",
				DurationMs:  150,
			},
			wantLevel:  "INFO",
			wantFields: []string{"hasHTML", "true", "attachments", "2"},
		},
		{
			name: "fills in connector_id",
			entry: &AuditEntry{
				Timestamp:   time.Now(),
				ConnectorID: "",
				AgentName:   "test-agent",
				UserID:      "test-user",
				SessionID:   "test-session",
				Recipients:  []string{"user@example.com"},
				Subject:     "Test",
				Result:      "sent",
				MessageID:   "<msg@example.com>",
				DurationMs:  50,
			},
			wantLevel:  "INFO",
			wantFields: []string{"connectorID", "test-connector"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			auditor := NewAuditor(logger, "test-connector")

			auditor.LogSend(context.Background(), tt.entry)

			output := buf.String()
			assert.Contains(t, output, tt.wantLevel)
			for _, field := range tt.wantFields {
				assert.Contains(t, output, field, "expected field %q in log output", field)
			}
		})
	}
}

func TestAuditor_LogSend_PreservesConnectorID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	auditor := NewAuditor(logger, "default-connector")

	entry := &AuditEntry{
		Timestamp:   time.Now(),
		ConnectorID: "custom-connector",
		AgentName:   "test-agent",
		UserID:      "test-user",
		SessionID:   "test-session",
		Recipients:  []string{"user@example.com"},
		Subject:     "Test",
		Result:      "sent",
		MessageID:   "<msg@example.com>",
		DurationMs:  50,
	}

	auditor.LogSend(context.Background(), entry)

	output := buf.String()
	assert.Contains(t, output, "custom-connector")
	assert.True(t, strings.Count(output, "custom-connector") >= 1)
}
