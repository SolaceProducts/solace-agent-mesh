// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sendemail

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/email"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/email/smtpprovider"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type SendEmailParams struct {
	To          []string        `json:"to" desc:"Recipient email addresses (must be in connector's domain allowlist)"`
	CC          *[]string       `json:"cc" desc:"CC recipients (optional)"`
	BCC         *[]string       `json:"bcc" desc:"BCC recipients (optional)"`
	Subject     string          `json:"subject" desc:"Email subject line"`
	Body        string          `json:"body" desc:"Email body (plain text)"`
	BodyHTML    *string         `json:"body_html" desc:"HTML body (optional, for rich formatting)"`
	Attachments *[]sdk.Artifact `json:"attachments" desc:"Files to attach to the email (optional)"`
	InReplyTo   *string         `json:"in_reply_to" desc:"Message-ID to reply to (from Email Gateway, for threading)"`
	References  *[]string       `json:"references" desc:"Message-ID reference chain (from Email Gateway, for threading)"`
}

const baseInstructions = `This tool sends emails via SMTP.

## Usage Guidelines
- Specify recipients in the 'to' field (required)
- Only recipients with domains in the connector's allowlist are permitted
- Provide a clear subject line and body content
- Use body_html for rich formatting when needed
- The sender address is fixed by the connector configuration

## Replying to Emails (IMPORTANT)
When the user asks you to reply to or follow up on a previous email:

1. **Use the message_id from the original email** - Every email you send returns a message_id in the response. Save this!
2. **Set the in_reply_to parameter** to the original email's message_id
3. **Set the references parameter** to an array containing the original message_id
4. **Prefix the subject with "Re: "** (e.g., "Re: Meeting Tomorrow")

Example flow:
- You send email → response contains message_id: "<abc123@example.com>"
- User asks for a follow-up/reply → use in_reply_to: "<abc123@example.com>" and references: ["<abc123@example.com>"]

This ensures the reply threads correctly in the recipient's inbox (Gmail, Outlook, etc.) instead of appearing as a separate conversation.

## Threading for Email Gateway
When replying to an email received via Email Gateway:
- Set in_reply_to to the original email's Message-ID (provided by the gateway)
- Set references to the original email's thread reference chain (provided by the gateway)`

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("send_email",
			"Send emails via SMTP. Supports plain text, HTML, and attachments.",
			sendEmail,
			sdk.WithInstructions(baseInstructions),
			sdk.WithDynamicSchema(dynamicSchema),
			sdk.WithTimeout(30),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func dynamicSchema(config map[string]any) *sdk.SchemaOverride {
	override := &sdk.SchemaOverride{}

	if name := configString(config, "tool_name", ""); name != "" {
		override.Name = name
	}
	if desc := configString(config, "tool_description", ""); desc != "" {
		override.Description = desc
	}

	host := configString(config, "smtp_host", "")
	if host == "" {
		override.Instructions = baseInstructions + "\n\n❌ WARNING: SMTP is not configured. Email sending will fail."
		return override
	}

	envelopeFrom := configString(config, "envelope_from", "")
	allowlist := configStringSlice(config, "recipient_allowlist")

	status := "\n\n## Connector Status\n✅ SMTP Configured"
	status += fmt.Sprintf("\n- **Sender**: %s", envelopeFrom)
	if len(allowlist) > 0 {
		status += fmt.Sprintf("\n- **Allowed Domains**: %s", strings.Join(allowlist, ", "))
	}

	cfg := configFromMap(config)
	if cfg.SMTP != nil && cfg.SMTP.Host != "" && os.Getenv("SEND_EMAIL_TEST_CONNECTION") == "1" {
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
		provider := smtpprovider.NewProvider(smtpCfg, cfg.EnvelopeFrom)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := provider.TestConnection(ctx); err != nil {
			status += fmt.Sprintf("\n\n⚠️ Connection test failed: %v", err)
		} else {
			status += "\n- **Connection**: Verified"
		}
		_ = provider.Close()
	}

	override.Instructions = baseInstructions + status
	return override
}

func sendEmail(ctx context.Context, p SendEmailParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	cfg := configFromToolContext(tc)

	if len(p.To) == 0 {
		return sdk.Error("at least one recipient is required in 'to' field"), nil
	}
	if p.Subject == "" {
		return sdk.Error("subject is required"), nil
	}
	if p.Body == "" && (p.BodyHTML == nil || *p.BodyHTML == "") {
		return sdk.Error("body or body_html is required"), nil
	}

	logger := samlog.NewStderrLogger(slog.LevelInfo)

	connector, err := email.NewConnector(cfg, logger)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to create email connector: %v", err)), nil
	}
	defer func() { _ = connector.Close() }()

	msg := &email.Message{
		To:        p.To,
		Subject:   p.Subject,
		BodyPlain: p.Body,
	}
	if p.CC != nil {
		msg.CC = *p.CC
	}
	if p.BCC != nil {
		msg.BCC = *p.BCC
	}
	if p.BodyHTML != nil {
		msg.BodyHTML = *p.BodyHTML
	}
	if p.InReplyTo != nil {
		msg.InReplyTo = *p.InReplyTo
	}
	if p.References != nil {
		msg.References = *p.References
	}

	if p.Attachments != nil {
		for _, att := range *p.Attachments {
			if len(att.Content) == 0 {
				continue
			}
			msg.Attachments = append(msg.Attachments, email.Attachment{
				Filename: att.Filename,
				Content:  att.Content,
				MIMEType: att.MIMEType,
			})
		}
	}

	_ = tc.SendStatus(fmt.Sprintf("Sending email to %d recipient(s)...", len(msg.AllRecipients())))

	messageID, err := connector.Send(ctx, msg, nil)
	if err != nil {
		return mapError(err), nil
	}

	return sdk.OK(
		fmt.Sprintf("Email sent successfully to %d recipient(s)", len(msg.AllRecipients())),
		sdk.WithData(map[string]any{
			"message_id": messageID,
			"recipients": msg.AllRecipients(),
			"from":       cfg.EnvelopeFrom,
		}),
	), nil
}

func mapError(err error) *sdk.Result {
	if err == nil {
		return nil
	}

	switch e := err.(type) {
	case *email.AllowlistError:
		return sdk.Error(e.Error(), sdk.WithData(map[string]any{
			"error_type":      "allowlist_error",
			"domain":          e.Domain,
			"allowed_domains": e.AllowedDomains,
		}))
	case *email.ThrottleError:
		return sdk.Error(e.Error(), sdk.WithData(map[string]any{
			"error_type":          "throttle_error",
			"retry_after_seconds": e.RetryAfter.Seconds(),
		}))
	case *email.SMTPError:
		return sdk.Error(e.Error(), sdk.WithData(map[string]any{
			"error_type": "smtp_reject",
			"smtp_code":  e.Code,
		}))
	case *email.AttachmentError:
		return sdk.Error(e.Error(), sdk.WithData(map[string]any{
			"error_type":     "attachment_error",
			"filename":       e.Filename,
			"max_size_bytes": e.MaxSize,
		}))
	case *email.AuthError:
		return sdk.Error(e.Error(), sdk.WithData(map[string]any{
			"error_type": "auth_error",
		}))
	case *email.ConnectionError:
		return sdk.Error(e.Error(), sdk.WithData(map[string]any{
			"error_type": "connection_error",
			"host":       e.Host,
			"port":       e.Port,
		}))
	default:
		return sdk.Error(fmt.Sprintf("email send failed: %v", err))
	}
}

func configFromToolContext(tc *sdk.ToolContext) email.ConnectorConfig {
	cfg := email.ConnectorConfig{
		Name:                    tc.GetConfigString("tool_name", "send_email"),
		Provider:                "smtp",
		EnvelopeFrom:            tc.GetConfigString("envelope_from", ""),
		MaxAttachmentSizeBytes:  int64(getConfigInt(tc, "max_attachment_size_bytes", email.DefaultMaxAttachmentSize)),
		MaxTotalAttachmentBytes: int64(getConfigInt(tc, "max_total_attachment_bytes", email.DefaultMaxTotalAttachmentSize)),
		RateLimits: email.RateLimitConfig{
			PerMinute: getConfigInt(tc, "rate_limit_per_minute", email.DefaultPerMinuteLimit),
			PerHour:   getConfigInt(tc, "rate_limit_per_hour", email.DefaultPerHourLimit),
		},
	}

	if allowlist, ok := tc.GetConfig("recipient_allowlist"); ok {
		switch v := allowlist.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					cfg.RecipientAllowlist = append(cfg.RecipientAllowlist, s)
				}
			}
		case []string:
			cfg.RecipientAllowlist = v
		}
	}

	port := getConfigInt(tc, "smtp_port", email.DefaultSMTPPort)
	tlsMode := tc.GetConfigString("smtp_tls", "starttls")
	authType := tc.GetConfigString("smtp_auth_type", "plain")

	cfg.SMTP = &email.SMTPConfig{
		Host: tc.GetConfigString("smtp_host", ""),
		Port: port,
		TLS:  email.TLSMode(tlsMode),
	}

	if authType != "none" && authType != "" {
		cfg.SMTP.Auth = &email.AuthConfig{
			Type:     email.AuthType(authType),
			Username: tc.GetConfigString("smtp_username", ""),
			Password: tc.GetConfigString("smtp_password", ""),
		}

		if authType == "oauth_microsoft" || authType == "oauth_google" {
			cfg.SMTP.Auth.OAuth = &email.OAuthConfig{
				Provider:     authType[6:],
				TenantID:     tc.GetConfigString("oauth_tenant_id", ""),
				ClientID:     tc.GetConfigString("oauth_client_id", ""),
				ClientSecret: tc.GetConfigString("oauth_client_secret", ""),
				RefreshToken: tc.GetConfigString("oauth_refresh_token", ""),
				Email:        tc.GetConfigString("oauth_email", ""),
			}
		}
	}

	return cfg
}

func getConfigInt(tc *sdk.ToolContext, key string, defaultVal int) int {
	v, ok := tc.GetConfig(key)
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return defaultVal
}

func configFromMap(config map[string]any) email.ConnectorConfig {
	cfg := email.ConnectorConfig{
		Name:                    configString(config, "tool_name", "send_email"),
		Provider:                "smtp",
		EnvelopeFrom:            configString(config, "envelope_from", ""),
		RecipientAllowlist:      configStringSlice(config, "recipient_allowlist"),
		MaxAttachmentSizeBytes:  int64(configInt(config, "max_attachment_size_bytes", email.DefaultMaxAttachmentSize)),
		MaxTotalAttachmentBytes: int64(configInt(config, "max_total_attachment_bytes", email.DefaultMaxTotalAttachmentSize)),
		RateLimits: email.RateLimitConfig{
			PerMinute: configInt(config, "rate_limit_per_minute", email.DefaultPerMinuteLimit),
			PerHour:   configInt(config, "rate_limit_per_hour", email.DefaultPerHourLimit),
		},
	}

	port := configInt(config, "smtp_port", email.DefaultSMTPPort)
	tlsMode := configString(config, "smtp_tls", "starttls")
	authType := configString(config, "smtp_auth_type", "plain")

	cfg.SMTP = &email.SMTPConfig{
		Host: configString(config, "smtp_host", ""),
		Port: port,
		TLS:  email.TLSMode(tlsMode),
	}

	if authType != "none" && authType != "" {
		cfg.SMTP.Auth = &email.AuthConfig{
			Type:     email.AuthType(authType),
			Username: configString(config, "smtp_username", ""),
			Password: configString(config, "smtp_password", ""),
		}

		if authType == "oauth_microsoft" || authType == "oauth_google" {
			cfg.SMTP.Auth.OAuth = &email.OAuthConfig{
				Provider:     authType[6:],
				TenantID:     configString(config, "oauth_tenant_id", ""),
				ClientID:     configString(config, "oauth_client_id", ""),
				ClientSecret: configString(config, "oauth_client_secret", ""),
				RefreshToken: configString(config, "oauth_refresh_token", ""),
				Email:        configString(config, "oauth_email", ""),
			}
		}
	}

	return cfg
}

func configString(config map[string]any, key, defaultVal string) string {
	v, ok := config[key]
	if !ok {
		return defaultVal
	}
	s, ok := v.(string)
	if !ok {
		return defaultVal
	}
	return s
}

func configInt(config map[string]any, key string, defaultVal int) int {
	v, ok := config[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return defaultVal
}

func configStringSlice(config map[string]any, key string) []string {
	v, ok := config[key]
	if !ok {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		var result []string
		for _, item := range s {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	}
	return nil
}
