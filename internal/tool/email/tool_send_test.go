// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendEmailTool_Metadata(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)

	assert.Equal(t, "send_email", sendTool.Name())
	assert.Contains(t, sendTool.Description(), "email")
	assert.Equal(t, []string{"tool:email:send"}, sendTool.RequiredScopes())
	assert.False(t, sendTool.IsLongRunning())
	assert.Equal(t, "", sendTool.Instructions())
	assert.Nil(t, sendTool.RawStringArgs())
	assert.Equal(t, "", sendTool.ContextParam())

	artParams := sendTool.ArtifactParams()
	require.Contains(t, artParams, "attachments")
	assert.True(t, artParams["attachments"].IsList)
	assert.True(t, artParams["attachments"].IsOptional)
}

func TestSendEmailTool_Parameters(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	params := sendTool.Parameters()

	assert.Equal(t, "object", params.Type)
	assert.Contains(t, params.Properties, "to")
	assert.Contains(t, params.Properties, "cc")
	assert.Contains(t, params.Properties, "bcc")
	assert.Contains(t, params.Properties, "subject")
	assert.Contains(t, params.Properties, "body")
	assert.Contains(t, params.Properties, "body_html")
	assert.Contains(t, params.Properties, "attachments")
	assert.Contains(t, params.Properties, "in_reply_to")
	assert.Contains(t, params.Properties, "references")
	assert.Equal(t, []string{"to", "subject"}, params.Required)
}

func TestSendEmailTool_Execute_Validation(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	tests := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{
			name:    "missing to",
			args:    map[string]any{"subject": "Test", "body": "Hello"},
			wantErr: "at least one recipient",
		},
		{
			name:    "empty to",
			args:    map[string]any{"to": []string{}, "subject": "Test", "body": "Hello"},
			wantErr: "at least one recipient",
		},
		{
			name:    "missing subject",
			args:    map[string]any{"to": []string{"user@example.com"}, "body": "Hello"},
			wantErr: "subject is required",
		},
		{
			name:    "missing body and body_html",
			args:    map[string]any{"to": []string{"user@example.com"}, "subject": "Test"},
			wantErr: "either 'body' or 'body_html' is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sendTool.Execute(ctx, tt.args, toolCtx)
			require.NoError(t, err)
			assert.Equal(t, tool.StatusError, result.Status)
			assert.Contains(t, result.Message, tt.wantErr)
		})
	}
}

func TestSendEmailTool_Execute_AllowlistError(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	args := map[string]any{
		"to":      []any{"user@notallowed.com"},
		"subject": "Test",
		"body":    "Hello",
	}

	result, err := sendTool.Execute(ctx, args, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Contains(t, result.Message, "not in allowlist")
	assert.Equal(t, "allowlist_error", result.Data["error_type"])
}

func TestParseStringSlice(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		key     string
		want    []string
		wantErr bool
	}{
		{
			name: "nil value",
			args: map[string]any{},
			key:  "to",
			want: nil,
		},
		{
			name: "string slice",
			args: map[string]any{"to": []string{"a", "b"}},
			key:  "to",
			want: []string{"a", "b"},
		},
		{
			name: "any slice with strings",
			args: map[string]any{"to": []any{"a", "b"}},
			key:  "to",
			want: []string{"a", "b"},
		},
		{
			name:    "any slice with non-string",
			args:    map[string]any{"to": []any{"a", 123}},
			key:     "to",
			wantErr: true,
		},
		{
			name:    "wrong type",
			args:    map[string]any{"to": "not a slice"},
			key:     "to",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStringSlice(tt.args, tt.key)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType string
		wantData map[string]any
	}{
		{
			name:     "allowlist error",
			err:      &AllowlistError{Recipient: "user@other.com", Domain: "other.com", AllowedDomains: []string{"example.com"}},
			wantType: "allowlist_error",
			wantData: map[string]any{"error_type": "allowlist_error", "domain": "other.com"},
		},
		{
			name:     "throttle error",
			err:      &ThrottleError{Limit: 60, Window: "minute"},
			wantType: "throttle_error",
			wantData: map[string]any{"error_type": "throttle_error"},
		},
		{
			name:     "smtp error",
			err:      &SMTPError{Code: 550, Message: "User not found"},
			wantType: "smtp_reject",
			wantData: map[string]any{"error_type": "smtp_reject", "smtp_code": 550},
		},
		{
			name:     "attachment error",
			err:      &AttachmentError{Filename: "big.pdf", Size: 20000000, MaxSize: 10000000, Reason: "size_exceeded"},
			wantType: "attachment_error",
			wantData: map[string]any{"error_type": "attachment_error", "max_size_bytes": int64(10000000)},
		},
		{
			name:     "auth error",
			err:      &AuthError{Message: "invalid credentials"},
			wantType: "auth_error",
			wantData: map[string]any{"error_type": "auth_error"},
		},
		{
			name:     "connection error",
			err:      &ConnectionError{Host: "smtp.example.com", Port: 587, Message: "timeout"},
			wantType: "connection_error",
			wantData: map[string]any{"error_type": "connection_error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapError(tt.err)
			assert.Equal(t, tool.StatusError, result.Status)
			for k, v := range tt.wantData {
				assert.Equal(t, v, result.Data[k], "data[%s] mismatch", k)
			}
		})
	}
}

func TestErrorType(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{&AllowlistError{}, "allowlist_error"},
		{&ThrottleError{}, "throttle_error"},
		{&SMTPError{}, "smtp_reject"},
		{&AttachmentError{}, "attachment_error"},
		{&AuthError{}, "auth_error"},
		{&ConnectionError{}, "connection_error"},
		{context.Canceled, "unknown_error"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, errorType(tt.err))
		})
	}
}

func TestSendEmailTool_Execute_WithBodyHTML(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	args := map[string]any{
		"to":        []any{"user@example.com"},
		"subject":   "Test",
		"body_html": "<html><body>Hello</body></html>",
	}

	result, err := sendTool.Execute(ctx, args, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Contains(t, result.Message, "connection")
}

func TestSendEmailTool_Execute_WithAllFields(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	args := map[string]any{
		"to":          []any{"to@example.com"},
		"cc":          []any{"cc@example.com"},
		"bcc":         []any{"bcc@example.com"},
		"subject":     "Test Subject",
		"body":        "Plain text body",
		"body_html":   "<html><body>HTML body</body></html>",
		"in_reply_to": "<original@example.com>",
		"references":  []any{"<ref1@example.com>", "<ref2@example.com>"},
	}

	result, err := sendTool.Execute(ctx, args, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
}

func TestSendEmailTool_Execute_InvalidCC(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	args := map[string]any{
		"to":      []any{"user@example.com"},
		"cc":      "not-a-list",
		"subject": "Test",
		"body":    "Hello",
	}

	result, err := sendTool.Execute(ctx, args, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Contains(t, result.Message, "must be an array")
}

func TestSendEmailTool_Execute_InvalidBCC(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	args := map[string]any{
		"to":      []any{"user@example.com"},
		"bcc":     123,
		"subject": "Test",
		"body":    "Hello",
	}

	result, err := sendTool.Execute(ctx, args, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Contains(t, result.Message, "must be an array")
}

func TestSendEmailTool_Execute_InvalidReferences(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	args := map[string]any{
		"to":         []any{"user@example.com"},
		"subject":    "Test",
		"body":       "Hello",
		"references": []any{123},
	}

	result, err := sendTool.Execute(ctx, args, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Contains(t, result.Message, "not a string")
}

func TestSendEmailTool_Execute_InvalidTo(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	args := map[string]any{
		"to":      []any{123},
		"subject": "Test",
		"body":    "Hello",
	}

	result, err := sendTool.Execute(ctx, args, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Contains(t, result.Message, "not a string")
}

func TestSendEmailTool_Execute_AttachmentsNoArtifactService(t *testing.T) {
	connector := mustTestConnector(t)
	sendTool := NewSendEmailTool(connector)
	ctx := t.Context()
	toolCtx := &tool.Context{
		AgentName:       "test-agent",
		UserID:          "test-user",
		SessionID:       "test-session",
		ArtifactService: nil,
	}

	args := map[string]any{
		"to":          []any{"user@example.com"},
		"subject":     "Test",
		"body":        "Hello",
		"attachments": []any{"file.txt"},
	}

	result, err := sendTool.Execute(ctx, args, toolCtx)
	require.NoError(t, err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Contains(t, result.Message, "artifact service not available")
}

func TestMapError_GenericError(t *testing.T) {
	err := context.DeadlineExceeded
	result := mapError(err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Contains(t, result.Message, "email send failed")
}

func TestMapError_ThrottleErrorRetryAfter(t *testing.T) {
	err := &ThrottleError{
		Limit:      60,
		Window:     "minute",
		RetryAfter: 30 * time.Second,
	}
	result := mapError(err)
	assert.Equal(t, tool.StatusError, result.Status)
	assert.Equal(t, 30, result.Data["retry_after_seconds"])
}

func mustTestConnector(t *testing.T) *Connector {
	t.Helper()
	cfg := ConnectorConfig{
		Name:               "test-connector",
		Provider:           "smtp",
		SMTP:               &SMTPConfig{Host: "localhost", Port: 2525},
		EnvelopeFrom:       "noreply@example.com",
		RecipientAllowlist: []string{"example.com"},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	conn, err := NewConnector(cfg, logger)
	require.NoError(t, err)
	return conn
}
