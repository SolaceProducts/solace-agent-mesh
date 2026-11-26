// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectorConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ConnectorConfig
		wantErr string
	}{
		{
			name:    "empty config",
			config:  ConnectorConfig{},
			wantErr: "connector name is required",
		},
		{
			name: "missing provider",
			config: ConnectorConfig{
				Name: "test",
			},
			wantErr: "provider is required",
		},
		{
			name: "unsupported provider",
			config: ConnectorConfig{
				Name:     "test",
				Provider: "graph",
			},
			wantErr: "unsupported provider",
		},
		{
			name: "missing smtp config",
			config: ConnectorConfig{
				Name:     "test",
				Provider: "smtp",
			},
			wantErr: "smtp configuration is required",
		},
		{
			name: "missing smtp host",
			config: ConnectorConfig{
				Name:     "test",
				Provider: "smtp",
				SMTP:     &SMTPConfig{Port: 587},
			},
			wantErr: "host is required",
		},
		{
			name: "invalid smtp port",
			config: ConnectorConfig{
				Name:     "test",
				Provider: "smtp",
				SMTP:     &SMTPConfig{Host: "smtp.example.com", Port: 0},
			},
			wantErr: "port 0 is invalid",
		},
		{
			name: "invalid tls mode",
			config: ConnectorConfig{
				Name:     "test",
				Provider: "smtp",
				SMTP: &SMTPConfig{
					Host: "smtp.example.com",
					Port: 587,
					TLS:  "invalid",
				},
			},
			wantErr: "tls mode",
		},
		{
			name: "missing envelope_from",
			config: ConnectorConfig{
				Name:     "test",
				Provider: "smtp",
				SMTP: &SMTPConfig{
					Host: "smtp.example.com",
					Port: 587,
				},
			},
			wantErr: "envelope_from is required",
		},
		{
			name: "invalid envelope_from",
			config: ConnectorConfig{
				Name:         "test",
				Provider:     "smtp",
				SMTP:         &SMTPConfig{Host: "smtp.example.com", Port: 587},
				EnvelopeFrom: "invalid",
			},
			wantErr: "not a valid email",
		},
		{
			name: "empty allowlist",
			config: ConnectorConfig{
				Name:         "test",
				Provider:     "smtp",
				SMTP:         &SMTPConfig{Host: "smtp.example.com", Port: 587},
				EnvelopeFrom: "noreply@example.com",
			},
			wantErr: "recipient_allowlist is required",
		},
		{
			name: "empty domain in allowlist",
			config: ConnectorConfig{
				Name:               "test",
				Provider:           "smtp",
				SMTP:               &SMTPConfig{Host: "smtp.example.com", Port: 587},
				EnvelopeFrom:       "noreply@example.com",
				RecipientAllowlist: []string{"example.com", ""},
			},
			wantErr: "recipient_allowlist[1] is empty",
		},
		{
			name: "valid minimal config",
			config: ConnectorConfig{
				Name:               "test",
				Provider:           "smtp",
				SMTP:               &SMTPConfig{Host: "smtp.example.com", Port: 587},
				EnvelopeFrom:       "noreply@example.com",
				RecipientAllowlist: []string{"example.com"},
			},
			wantErr: "",
		},
		{
			name: "valid full config",
			config: ConnectorConfig{
				Name:     "corporate-smtp",
				Provider: "smtp",
				SMTP: &SMTPConfig{
					Host: "smtp.example.com",
					Port: 587,
					TLS:  TLSModeSTARTTLS,
					Auth: &AuthConfig{
						Type:     AuthTypePlain,
						Username: "user",
						Password: "pass",
					},
				},
				EnvelopeFrom:            "noreply@example.com",
				RecipientAllowlist:      []string{"example.com", "partner.example.com"},
				RateLimits:              RateLimitConfig{PerMinute: 60, PerHour: 500},
				MaxAttachmentSizeBytes:  10 * 1024 * 1024,
				MaxTotalAttachmentBytes: 25 * 1024 * 1024,
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestConnectorConfig_WithDefaults(t *testing.T) {
	cfg := ConnectorConfig{
		Name:               "test",
		Provider:           "smtp",
		SMTP:               &SMTPConfig{Host: "smtp.example.com"},
		EnvelopeFrom:       "noreply@example.com",
		RecipientAllowlist: []string{"example.com"},
	}

	withDefaults := cfg.WithDefaults()

	assert.Equal(t, DefaultSMTPPort, withDefaults.SMTP.Port)
	assert.Equal(t, TLSModeSTARTTLS, withDefaults.SMTP.TLS)
	assert.Equal(t, int64(DefaultMaxAttachmentSize), withDefaults.MaxAttachmentSizeBytes)
	assert.Equal(t, int64(DefaultMaxTotalAttachmentSize), withDefaults.MaxTotalAttachmentBytes)
	assert.Equal(t, DefaultPerMinuteLimit, withDefaults.RateLimits.PerMinute)
	assert.Equal(t, DefaultPerHourLimit, withDefaults.RateLimits.PerHour)
}

func TestAuthConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		auth    AuthConfig
		wantErr string
	}{
		{
			name:    "none auth type",
			auth:    AuthConfig{Type: AuthTypeNone},
			wantErr: "",
		},
		{
			name:    "empty auth type",
			auth:    AuthConfig{Type: ""},
			wantErr: "",
		},
		{
			name:    "plain without username",
			auth:    AuthConfig{Type: AuthTypePlain, Password: "pass"},
			wantErr: "username is required",
		},
		{
			name:    "plain without password",
			auth:    AuthConfig{Type: AuthTypePlain, Username: "user"},
			wantErr: "password is required",
		},
		{
			name:    "login without credentials",
			auth:    AuthConfig{Type: AuthTypeLogin},
			wantErr: "username is required",
		},
		{
			name:    "valid plain auth",
			auth:    AuthConfig{Type: AuthTypePlain, Username: "user", Password: "pass"},
			wantErr: "",
		},
		{
			name:    "valid login auth",
			auth:    AuthConfig{Type: AuthTypeLogin, Username: "user", Password: "pass"},
			wantErr: "",
		},
		{
			name:    "invalid auth type",
			auth:    AuthConfig{Type: "oauth"},
			wantErr: "auth type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.auth.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	tests := []struct {
		email string
		valid bool
	}{
		{"user@example.com", true},
		{"user.name@example.com", true},
		{"user@sub.example.com", true},
		{"user+tag@example.com", true},
		{"", false},
		{"@example.com", false},
		{"user@", false},
		{"user", false},
		{"user@.com", false},
		{"user@example.", false},
		{"user@exam..ple.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			assert.Equal(t, tt.valid, isValidEmail(tt.email))
		})
	}
}
