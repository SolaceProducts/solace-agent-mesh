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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConnector(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	t.Run("valid config", func(t *testing.T) {
		cfg := ConnectorConfig{
			Name:               "test",
			Provider:           "smtp",
			SMTP:               &SMTPConfig{Host: "smtp.example.com", Port: 587},
			EnvelopeFrom:       "noreply@example.com",
			RecipientAllowlist: []string{"example.com"},
		}

		conn, err := NewConnector(cfg, logger)
		require.NoError(t, err)
		assert.NotNil(t, conn)
		assert.Equal(t, "test", conn.Name())
		assert.Equal(t, "noreply@example.com", conn.EnvelopeFrom())
		assert.Contains(t, conn.AllowedDomains(), "example.com")
	})

	t.Run("invalid config", func(t *testing.T) {
		cfg := ConnectorConfig{
			Name: "test",
		}

		conn, err := NewConnector(cfg, logger)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "invalid configuration")
	})

	t.Run("unsupported provider", func(t *testing.T) {
		cfg := ConnectorConfig{
			Name:               "test",
			Provider:           "graph",
			EnvelopeFrom:       "noreply@example.com",
			RecipientAllowlist: []string{"example.com"},
		}

		conn, err := NewConnector(cfg, logger)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Contains(t, err.Error(), "unsupported provider")
	})
}

func TestConnector_Tool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := ConnectorConfig{
		Name:               "test",
		Provider:           "smtp",
		SMTP:               &SMTPConfig{Host: "smtp.example.com", Port: 587},
		EnvelopeFrom:       "noreply@example.com",
		RecipientAllowlist: []string{"example.com"},
	}

	conn, err := NewConnector(cfg, logger)
	require.NoError(t, err)

	tool := conn.Tool()
	assert.NotNil(t, tool)
	assert.Equal(t, "send_email", tool.Name())
}

func TestConnector_Config(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := ConnectorConfig{
		Name:               "test",
		Provider:           "smtp",
		SMTP:               &SMTPConfig{Host: "smtp.example.com", Port: 587},
		EnvelopeFrom:       "noreply@example.com",
		RecipientAllowlist: []string{"example.com"},
	}

	conn, err := NewConnector(cfg, logger)
	require.NoError(t, err)

	returnedCfg := conn.Config()
	assert.Equal(t, int64(DefaultMaxAttachmentSize), returnedCfg.MaxAttachmentSizeBytes)
	assert.Equal(t, int64(DefaultMaxTotalAttachmentSize), returnedCfg.MaxTotalAttachmentBytes)
	assert.Equal(t, DefaultPerMinuteLimit, returnedCfg.RateLimits.PerMinute)
	assert.Equal(t, DefaultPerHourLimit, returnedCfg.RateLimits.PerHour)
}

func TestConnector_ValidateAttachments(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := ConnectorConfig{
		Name:                    "test",
		Provider:                "smtp",
		SMTP:                    &SMTPConfig{Host: "smtp.example.com", Port: 587},
		EnvelopeFrom:            "noreply@example.com",
		RecipientAllowlist:      []string{"example.com"},
		MaxAttachmentSizeBytes:  1000,
		MaxTotalAttachmentBytes: 2000,
	}

	conn, err := NewConnector(cfg, logger)
	require.NoError(t, err)

	t.Run("valid attachments", func(t *testing.T) {
		msg := &Message{
			Attachments: []Attachment{
				{Filename: "a.txt", Content: make([]byte, 500)},
				{Filename: "b.txt", Content: make([]byte, 500)},
			},
		}
		err := conn.validateAttachments(msg)
		assert.NoError(t, err)
	})

	t.Run("single attachment too large", func(t *testing.T) {
		msg := &Message{
			Attachments: []Attachment{
				{Filename: "big.txt", Content: make([]byte, 1500)},
			},
		}
		err := conn.validateAttachments(msg)
		require.Error(t, err)
		var attErr *AttachmentError
		require.ErrorAs(t, err, &attErr)
		assert.Equal(t, "size_exceeded", attErr.Reason)
	})

	t.Run("total too large", func(t *testing.T) {
		msg := &Message{
			Attachments: []Attachment{
				{Filename: "a.txt", Content: make([]byte, 800)},
				{Filename: "b.txt", Content: make([]byte, 800)},
				{Filename: "c.txt", Content: make([]byte, 800)},
			},
		}
		err := conn.validateAttachments(msg)
		require.Error(t, err)
		var attErr *AttachmentError
		require.ErrorAs(t, err, &attErr)
		assert.Equal(t, "total_exceeded", attErr.Reason)
	})
}

func TestConnector_Close(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := ConnectorConfig{
		Name:               "test",
		Provider:           "smtp",
		SMTP:               &SMTPConfig{Host: "smtp.example.com", Port: 587},
		EnvelopeFrom:       "noreply@example.com",
		RecipientAllowlist: []string{"example.com"},
	}

	conn, err := NewConnector(cfg, logger)
	require.NoError(t, err)

	err = conn.Close()
	assert.NoError(t, err)
}

func TestNewConnector_WithAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	t.Run("with plain auth", func(t *testing.T) {
		cfg := ConnectorConfig{
			Name:     "test",
			Provider: "smtp",
			SMTP: &SMTPConfig{
				Host: "smtp.example.com",
				Port: 587,
				Auth: &AuthConfig{
					Type:     AuthTypePlain,
					Username: "user",
					Password: "pass",
				},
			},
			EnvelopeFrom:       "noreply@example.com",
			RecipientAllowlist: []string{"example.com"},
		}

		conn, err := NewConnector(cfg, logger)
		require.NoError(t, err)
		assert.NotNil(t, conn)
	})

	t.Run("with login auth", func(t *testing.T) {
		cfg := ConnectorConfig{
			Name:     "test",
			Provider: "smtp",
			SMTP: &SMTPConfig{
				Host: "smtp.example.com",
				Port: 587,
				Auth: &AuthConfig{
					Type:     AuthTypeLogin,
					Username: "user",
					Password: "pass",
				},
			},
			EnvelopeFrom:       "noreply@example.com",
			RecipientAllowlist: []string{"example.com"},
		}

		conn, err := NewConnector(cfg, logger)
		require.NoError(t, err)
		assert.NotNil(t, conn)
	})

	t.Run("with oauth microsoft", func(t *testing.T) {
		cfg := ConnectorConfig{
			Name:     "test",
			Provider: "smtp",
			SMTP: &SMTPConfig{
				Host: "smtp.office365.com",
				Port: 587,
				Auth: &AuthConfig{
					Type: AuthTypeOAuthMicrosoft,
					OAuth: &OAuthConfig{
						Provider:     "microsoft",
						TenantID:     "tenant-123",
						ClientID:     "client-123",
						ClientSecret: "secret-123",
						Email:        "user@example.com",
					},
				},
			},
			EnvelopeFrom:       "noreply@example.com",
			RecipientAllowlist: []string{"example.com"},
		}

		conn, err := NewConnector(cfg, logger)
		require.NoError(t, err)
		assert.NotNil(t, conn)
	})

	t.Run("with oauth google", func(t *testing.T) {
		cfg := ConnectorConfig{
			Name:     "test",
			Provider: "smtp",
			SMTP: &SMTPConfig{
				Host: "smtp.gmail.com",
				Port: 587,
				Auth: &AuthConfig{
					Type: AuthTypeOAuthGoogle,
					OAuth: &OAuthConfig{
						Provider:     "google",
						ClientID:     "client-123",
						ClientSecret: "secret-123",
						RefreshToken: "refresh-123",
						Email:        "user@gmail.com",
					},
				},
			},
			EnvelopeFrom:       "noreply@example.com",
			RecipientAllowlist: []string{"example.com"},
		}

		conn, err := NewConnector(cfg, logger)
		require.NoError(t, err)
		assert.NotNil(t, conn)
	})
}

func TestConvertSMTPError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		err := convertSMTPError(nil)
		assert.Nil(t, err)
	})

	t.Run("unknown error type", func(t *testing.T) {
		origErr := context.Canceled
		err := convertSMTPError(origErr)
		assert.Equal(t, origErr, err)
	})
}

func TestConnector_LogAudit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := ConnectorConfig{
		Name:               "test-connector",
		Provider:           "smtp",
		SMTP:               &SMTPConfig{Host: "smtp.example.com", Port: 587},
		EnvelopeFrom:       "noreply@example.com",
		RecipientAllowlist: []string{"example.com"},
	}

	conn, err := NewConnector(cfg, logger)
	require.NoError(t, err)

	entry := &AuditEntry{
		AgentName:  "test-agent",
		Recipients: []string{"user@example.com"},
		Subject:    "Test",
		Result:     "sent",
	}

	conn.LogAudit(t.Context(), entry)
	assert.Equal(t, "test-connector", entry.ConnectorID)
}
