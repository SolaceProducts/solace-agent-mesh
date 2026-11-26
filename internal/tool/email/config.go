// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"errors"
	"fmt"
	"strings"
)

type TLSMode string

const (
	TLSModeNone     TLSMode = "none"
	TLSModeSTARTTLS TLSMode = "starttls"
	TLSModeTLS      TLSMode = "tls"
)

type AuthType string

const (
	AuthTypeNone           AuthType = "none"
	AuthTypePlain          AuthType = "plain"
	AuthTypeLogin          AuthType = "login"
	AuthTypeOAuthMicrosoft AuthType = "oauth_microsoft"
	AuthTypeOAuthGoogle    AuthType = "oauth_google"
)

type ConnectorConfig struct {
	Name string `yaml:"name" json:"name"`

	Provider string `yaml:"provider" json:"provider"`

	SMTP *SMTPConfig `yaml:"smtp,omitempty" json:"smtp,omitempty"`

	EnvelopeFrom string `yaml:"envelope_from" json:"envelope_from"`

	RecipientAllowlist []string `yaml:"recipient_allowlist" json:"recipient_allowlist"`

	RateLimits RateLimitConfig `yaml:"rate_limits" json:"rate_limits"`

	MaxAttachmentSizeBytes int64 `yaml:"max_attachment_size_bytes" json:"max_attachment_size_bytes"`

	MaxTotalAttachmentBytes int64 `yaml:"max_total_attachment_bytes" json:"max_total_attachment_bytes"`
}

type SMTPConfig struct {
	Host string      `yaml:"host" json:"host"`
	Port int         `yaml:"port" json:"port"`
	TLS  TLSMode     `yaml:"tls" json:"tls"`
	Auth *AuthConfig `yaml:"auth,omitempty" json:"auth,omitempty"`
}

type AuthConfig struct {
	Type     AuthType `yaml:"type" json:"type"`
	Username string   `yaml:"username" json:"username"`
	Password string   `yaml:"password" json:"password"`

	OAuth *OAuthConfig `yaml:"oauth,omitempty" json:"oauth,omitempty"`
}

type OAuthConfig struct {
	Provider string `yaml:"provider" json:"provider"`

	TenantID     string `yaml:"tenant_id,omitempty" json:"tenant_id,omitempty"`
	ClientID     string `yaml:"client_id" json:"client_id"`
	ClientSecret string `yaml:"client_secret" json:"client_secret"`

	RefreshToken string `yaml:"refresh_token,omitempty" json:"refresh_token,omitempty"`

	Email string `yaml:"email" json:"email"`
}

type RateLimitConfig struct {
	PerMinute int `yaml:"per_minute" json:"per_minute"`

	PerHour int `yaml:"per_hour" json:"per_hour"`
}

const (
	DefaultMaxAttachmentSize      = 10 * 1024 * 1024
	DefaultMaxTotalAttachmentSize = 25 * 1024 * 1024
	DefaultSMTPPort               = 587
	DefaultPerMinuteLimit         = 60
	DefaultPerHourLimit           = 500
)

func (c *ConnectorConfig) Validate() error {
	if c.Name == "" {
		return errors.New("connector name is required")
	}

	if c.Provider == "" {
		return errors.New("provider is required")
	}

	if c.Provider != "smtp" {
		return fmt.Errorf("unsupported provider %q: only 'smtp' is currently supported", c.Provider)
	}

	if c.SMTP == nil {
		return errors.New("smtp configuration is required for smtp provider")
	}

	if err := c.SMTP.Validate(); err != nil {
		return fmt.Errorf("smtp config: %w", err)
	}

	if c.EnvelopeFrom == "" {
		return errors.New("envelope_from is required")
	}

	if !isValidEmail(c.EnvelopeFrom) {
		return fmt.Errorf("envelope_from %q is not a valid email address", c.EnvelopeFrom)
	}

	if len(c.RecipientAllowlist) == 0 {
		return errors.New("recipient_allowlist is required and must contain at least one domain")
	}

	for i, domain := range c.RecipientAllowlist {
		if domain == "" {
			return fmt.Errorf("recipient_allowlist[%d] is empty", i)
		}
		if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
			return fmt.Errorf("recipient_allowlist[%d] %q has invalid format", i, domain)
		}
	}

	return nil
}

func (c *SMTPConfig) Validate() error {
	if c.Host == "" {
		return errors.New("host is required")
	}

	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port %d is invalid: must be 1-65535", c.Port)
	}

	switch c.TLS {
	case TLSModeNone, TLSModeSTARTTLS, TLSModeTLS, "":
	default:
		return fmt.Errorf("tls mode %q is invalid: must be 'none', 'starttls', or 'tls'", c.TLS)
	}

	if c.Auth != nil {
		if err := c.Auth.Validate(); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	return nil
}

func (c *AuthConfig) Validate() error {
	switch c.Type {
	case AuthTypeNone, "":
		return nil
	case AuthTypePlain, AuthTypeLogin:
		if c.Username == "" {
			return errors.New("username is required for plain/login auth")
		}
		if c.Password == "" {
			return errors.New("password is required for plain/login auth")
		}
	case AuthTypeOAuthMicrosoft:
		if c.OAuth == nil {
			return errors.New("oauth configuration is required for oauth_microsoft auth")
		}
		if c.OAuth.TenantID == "" {
			return errors.New("tenant_id is required for Microsoft OAuth")
		}
		if c.OAuth.ClientID == "" {
			return errors.New("client_id is required for Microsoft OAuth")
		}
		if c.OAuth.ClientSecret == "" {
			return errors.New("client_secret is required for Microsoft OAuth")
		}
		if c.OAuth.Email == "" {
			return errors.New("email is required for Microsoft OAuth")
		}
	case AuthTypeOAuthGoogle:
		if c.OAuth == nil {
			return errors.New("oauth configuration is required for oauth_google auth")
		}
		if c.OAuth.ClientID == "" {
			return errors.New("client_id is required for Google OAuth")
		}
		if c.OAuth.ClientSecret == "" {
			return errors.New("client_secret is required for Google OAuth")
		}
		if c.OAuth.RefreshToken == "" {
			return errors.New("refresh_token is required for Google OAuth")
		}
		if c.OAuth.Email == "" {
			return errors.New("email is required for Google OAuth")
		}
	default:
		return fmt.Errorf("auth type %q is invalid: must be 'none', 'plain', 'login', 'oauth_microsoft', or 'oauth_google'", c.Type)
	}
	return nil
}

func (c *ConnectorConfig) WithDefaults() ConnectorConfig {
	cfg := *c

	if cfg.MaxAttachmentSizeBytes == 0 {
		cfg.MaxAttachmentSizeBytes = DefaultMaxAttachmentSize
	}
	if cfg.MaxTotalAttachmentBytes == 0 {
		cfg.MaxTotalAttachmentBytes = DefaultMaxTotalAttachmentSize
	}
	if cfg.RateLimits.PerMinute == 0 {
		cfg.RateLimits.PerMinute = DefaultPerMinuteLimit
	}
	if cfg.RateLimits.PerHour == 0 {
		cfg.RateLimits.PerHour = DefaultPerHourLimit
	}

	if cfg.SMTP != nil {
		smtp := *cfg.SMTP
		if smtp.Port == 0 {
			smtp.Port = DefaultSMTPPort
		}
		if smtp.TLS == "" {
			smtp.TLS = TLSModeSTARTTLS
		}
		cfg.SMTP = &smtp
	}

	return cfg
}

func isValidEmail(email string) bool {
	if email == "" {
		return false
	}
	atIdx := strings.LastIndex(email, "@")
	if atIdx <= 0 || atIdx >= len(email)-1 {
		return false
	}
	local := email[:atIdx]
	domain := email[atIdx+1:]
	if local == "" || domain == "" {
		return false
	}
	if strings.Contains(domain, "..") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	return true
}
