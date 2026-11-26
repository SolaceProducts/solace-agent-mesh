// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/email/smtpprovider"
)

type Connector struct {
	config      ConnectorConfig
	provider    Provider
	allowlist   *AllowlistValidator
	rateLimiter *RateLimiter
	auditor     *Auditor
	logger      *slog.Logger
}

func NewConnector(cfg ConnectorConfig, logger *slog.Logger) (*Connector, error) {
	cfg = cfg.WithDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	var provider Provider
	switch cfg.Provider {
	case "smtp":
		smtpCfg := smtpprovider.Config{
			Host: cfg.SMTP.Host,
			Port: cfg.SMTP.Port,
			TLS:  smtpprovider.TLSMode(cfg.SMTP.TLS),
		}
		if cfg.SMTP.Auth != nil {
			smtpCfg.AuthType = smtpprovider.AuthType(cfg.SMTP.Auth.Type)
			smtpCfg.Username = cfg.SMTP.Auth.Username
			smtpCfg.Password = cfg.SMTP.Auth.Password

			if cfg.SMTP.Auth.OAuth != nil {
				smtpCfg.OAuth = &smtpprovider.OAuthConfig{
					Provider:     cfg.SMTP.Auth.OAuth.Provider,
					TenantID:     cfg.SMTP.Auth.OAuth.TenantID,
					ClientID:     cfg.SMTP.Auth.OAuth.ClientID,
					ClientSecret: cfg.SMTP.Auth.OAuth.ClientSecret,
					RefreshToken: cfg.SMTP.Auth.OAuth.RefreshToken,
					Email:        cfg.SMTP.Auth.OAuth.Email,
				}
			}
		}
		provider = &smtpProviderAdapter{smtpprovider.NewProvider(smtpCfg, cfg.EnvelopeFrom)}
	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.Provider)
	}

	return &Connector{
		config:      cfg,
		provider:    provider,
		allowlist:   NewAllowlistValidator(cfg.RecipientAllowlist),
		rateLimiter: NewRateLimiter(cfg.RateLimits),
		auditor:     NewAuditor(logger, cfg.Name),
		logger:      logger.With("connector", cfg.Name),
	}, nil
}

type smtpProviderAdapter struct {
	inner *smtpprovider.Provider
}

func (a *smtpProviderAdapter) Send(ctx context.Context, msg *Message) (string, error) {
	smtpMsg := &smtpprovider.Message{
		To:         msg.To,
		CC:         msg.CC,
		BCC:        msg.BCC,
		Subject:    msg.Subject,
		BodyPlain:  msg.BodyPlain,
		BodyHTML:   msg.BodyHTML,
		InReplyTo:  msg.InReplyTo,
		References: msg.References,
	}
	for _, att := range msg.Attachments {
		smtpMsg.Attachments = append(smtpMsg.Attachments, smtpprovider.Attachment{
			Filename: att.Filename,
			Content:  att.Content,
			MIMEType: att.MIMEType,
		})
	}

	messageID, err := a.inner.Send(ctx, smtpMsg)
	if err != nil {
		return "", convertSMTPError(err)
	}
	return messageID, nil
}

func (a *smtpProviderAdapter) TestConnection(ctx context.Context) error {
	err := a.inner.TestConnection(ctx)
	if err != nil {
		return convertSMTPError(err)
	}
	return nil
}

func (a *smtpProviderAdapter) Close() error {
	return a.inner.Close()
}

func convertSMTPError(err error) error {
	if err == nil {
		return nil
	}
	switch e := err.(type) {
	case *smtpprovider.AuthError:
		return &AuthError{Message: e.Message}
	case *smtpprovider.ConnectionError:
		return &ConnectionError{Host: e.Host, Port: e.Port, Message: e.Message}
	case *smtpprovider.SMTPError:
		return &SMTPError{Code: e.Code, Message: e.Message}
	default:
		return err
	}
}

func (c *Connector) Name() string {
	return c.config.Name
}

func (c *Connector) Config() ConnectorConfig {
	return c.config
}

func (c *Connector) Tool() tool.BuiltinTool {
	return NewSendEmailTool(c)
}

func (c *Connector) Send(ctx context.Context, msg *Message, _ *tool.Context) (string, error) {
	allRecipients := msg.AllRecipients()
	if err := c.allowlist.ValidateRecipients(allRecipients); err != nil {
		return "", err
	}

	if err := c.rateLimiter.Allow(); err != nil {
		return "", err
	}

	if err := c.validateAttachments(msg); err != nil {
		return "", err
	}

	return c.provider.Send(ctx, msg)
}

func (c *Connector) validateAttachments(msg *Message) error {
	var totalSize int64

	for _, att := range msg.Attachments {
		size := int64(len(att.Content))

		if size > c.config.MaxAttachmentSizeBytes {
			return &AttachmentError{
				Filename: att.Filename,
				Size:     size,
				MaxSize:  c.config.MaxAttachmentSizeBytes,
				Reason:   "size_exceeded",
			}
		}

		totalSize += size

		if totalSize > c.config.MaxTotalAttachmentBytes {
			return &AttachmentError{
				Filename: att.Filename,
				Size:     totalSize,
				MaxSize:  c.config.MaxTotalAttachmentBytes,
				Reason:   "total_exceeded",
			}
		}
	}

	return nil
}

func (c *Connector) TestConnection(ctx context.Context) error {
	return c.provider.TestConnection(ctx)
}

func (c *Connector) LogAudit(ctx context.Context, entry *AuditEntry) {
	entry.ConnectorID = c.config.Name
	c.auditor.LogSend(ctx, entry)
}

func (c *Connector) Close() error {
	return c.provider.Close()
}

func (c *Connector) AllowedDomains() []string {
	return c.allowlist.AllowedDomains()
}

func (c *Connector) EnvelopeFrom() string {
	return c.config.EnvelopeFrom
}
