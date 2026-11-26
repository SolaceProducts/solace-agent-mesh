// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type DeliveryMode string

const (
	DeliveryModeAuto DeliveryMode = "auto"
	DeliveryModeIdle DeliveryMode = "idle"
	DeliveryModePoll DeliveryMode = "poll"
)

const pollDefaultAvoidsIdleTeardownPanic = DeliveryModePoll

type EmailConfig struct {
	Mailbox  string
	IMAPHost string
	IMAPPort int

	IMAPPassword string

	OAuth2TenantID     string
	OAuth2ClientID     string
	OAuth2ClientSecret string

	SMTPHost string
	SMTPPort int

	PollIntervalSeconds int
	FetchLimit          int
	WorkerCount         int
	QueueCapacity       int

	TrustedAuthservIDs []string

	PolicyMode PolicyMode

	DeliveryMode DeliveryMode

	IdleKeepaliveInterval time.Duration

	RequireDMARCForIdentity bool

	MaxMessageSizeBytes    int64
	MaxAttachmentSizeBytes int64

	MaxOutboundAttachmentSizeBytes int64

	DefaultAgentName string

	ExternalBaseURL string

	HILTokenTTL time.Duration

	TrustedInternalTenantID string

	AllowedSenders []string

	MIMEAllowlist []string
	MIMEDenylist  []string
}

func ParseEmailConfig(cfg map[string]any) (*EmailConfig, error) {
	c := &EmailConfig{
		IMAPPort:                       993,
		SMTPHost:                       "smtp.gmail.com",
		SMTPPort:                       587,
		PollIntervalSeconds:            30,
		FetchLimit:                     25,
		WorkerCount:                    2,
		QueueCapacity:                  100,
		PolicyMode:                     ModeRequireDMARC,
		DeliveryMode:                   pollDefaultAvoidsIdleTeardownPanic,
		RequireDMARCForIdentity:        true,
		MaxMessageSizeBytes:            25 * 1024 * 1024,
		MaxAttachmentSizeBytes:         10 * 1024 * 1024,
		MaxOutboundAttachmentSizeBytes: 3 * 1024 * 1024,
	}

	if v, ok := cfg["mailbox"].(string); ok && v != "" {
		c.Mailbox = v
	} else {
		return nil, fmt.Errorf("email adapter: mailbox is required")
	}

	if v, ok := cfg["imap_host"].(string); ok && v != "" {
		c.IMAPHost = v
	} else {
		return nil, fmt.Errorf("email adapter: imap_host is required")
	}

	if err := parseAuthMode(cfg, c); err != nil {
		return nil, err
	}

	if v, ok := cfg["smtp_host"].(string); ok && v != "" {
		c.SMTPHost = v
	}
	if v, ok := getInt(cfg, "smtp_port"); ok {
		c.SMTPPort = v
	}

	if v, ok := getInt(cfg, "imap_port"); ok {
		c.IMAPPort = v
	}
	if v, ok := getInt(cfg, "poll_interval_seconds"); ok {
		c.PollIntervalSeconds = v
	}
	if v, ok := getInt(cfg, "fetch_limit"); ok {
		c.FetchLimit = v
	}
	if v, ok := getInt(cfg, "worker_count"); ok {
		c.WorkerCount = v
	}
	if v, ok := getInt(cfg, "queue_capacity"); ok {
		c.QueueCapacity = v
	}

	if v, ok := cfg["external_base_url"].(string); ok && v != "" {
		c.ExternalBaseURL = v
	} else if env := os.Getenv("EMAIL_EXTERNAL_BASE_URL"); env != "" {
		c.ExternalBaseURL = env
	}
	c.ExternalBaseURL = strings.TrimRight(c.ExternalBaseURL, "/")
	c.HILTokenTTL = 30 * time.Minute
	if v, ok := getInt(cfg, "hil_token_ttl_seconds"); ok && v > 0 {
		c.HILTokenTTL = time.Duration(v) * time.Second
	}

	if v, ok := getInt64(cfg, "max_message_size_bytes"); ok {
		c.MaxMessageSizeBytes = v
	}
	if v, ok := getInt64(cfg, "max_attachment_size_bytes"); ok {
		c.MaxAttachmentSizeBytes = v
	}
	if v, ok := getInt64(cfg, "max_outbound_attachment_size_bytes"); ok {
		c.MaxOutboundAttachmentSizeBytes = v
	}

	authservIDs, err := parseStringSlice(cfg, "trusted_authserv_ids", false)
	if err != nil {
		return nil, err
	}
	c.TrustedAuthservIDs = authservIDs
	if len(c.TrustedAuthservIDs) == 0 {
		return nil, fmt.Errorf("email adapter: trusted_authserv_ids is required and must be non-empty")
	}

	if v, ok := cfg["policy_mode"].(string); ok && v != "" {
		switch PolicyMode(v) {
		case ModeLogOnly, ModeRequireDMARC, ModeStrict:
			c.PolicyMode = PolicyMode(v)
		default:
			return nil, fmt.Errorf("email adapter: policy_mode %q is invalid (must be one of %q, %q, %q)",
				v, ModeLogOnly, ModeRequireDMARC, ModeStrict)
		}
	}

	if v, ok := cfg["delivery_mode"].(string); ok && v != "" {
		switch DeliveryMode(v) {
		case DeliveryModeAuto, DeliveryModeIdle, DeliveryModePoll:
			c.DeliveryMode = DeliveryMode(v)
		default:
			return nil, fmt.Errorf("email adapter: delivery_mode %q is invalid (must be one of %q, %q, %q)",
				v, DeliveryModeAuto, DeliveryModeIdle, DeliveryModePoll)
		}
	}

	if v, ok := getInt(cfg, "idle_keepalive_interval_seconds"); ok {
		if v <= 0 {
			return nil, fmt.Errorf("email adapter: idle_keepalive_interval_seconds must be positive (got %d)", v)
		}
		c.IdleKeepaliveInterval = time.Duration(v) * time.Second
	}

	if v, ok := cfg["require_dmarc_for_identity"].(bool); ok {
		c.RequireDMARCForIdentity = v
	}

	if c.PolicyMode == ModeLogOnly && !c.RequireDMARCForIdentity {
		return nil, fmt.Errorf("email adapter: policy_mode=log_only requires require_dmarc_for_identity=true (the combination would admit any From-spoofed sender as an authenticated identity)")
	}

	if v, ok := cfg["default_agent_name"].(string); ok {
		c.DefaultAgentName = v
	}

	if v, ok := cfg["trusted_internal_tenant_id"].(string); ok {
		c.TrustedInternalTenantID = v
	}

	if c.AllowedSenders, err = parseStringSlice(cfg, "allowed_senders", true); err != nil {
		return nil, err
	}
	if c.MIMEAllowlist, err = parseStringSlice(cfg, "mime_allowlist", true); err != nil {
		return nil, err
	}
	if c.MIMEDenylist, err = parseStringSlice(cfg, "mime_denylist", true); err != nil {
		return nil, err
	}

	if err := validateEmailLimits(c); err != nil {
		return nil, err
	}

	return c, nil
}

func parseAuthMode(cfg map[string]any, c *EmailConfig) error {
	if v, ok := cfg["imap_password"].(string); ok && v != "" {
		c.IMAPPassword = v
	}
	if v, ok := cfg["oauth2_tenant_id"].(string); ok && v != "" {
		c.OAuth2TenantID = v
	}
	if v, ok := cfg["oauth2_client_id"].(string); ok && v != "" {
		c.OAuth2ClientID = v
	}
	if v, ok := cfg["oauth2_client_secret"].(string); ok && v != "" {
		c.OAuth2ClientSecret = v
	}

	hasBasic := c.IMAPPassword != ""
	hasOAuth2Any := c.OAuth2TenantID != "" || c.OAuth2ClientID != "" || c.OAuth2ClientSecret != ""
	hasOAuth2All := c.OAuth2TenantID != "" && c.OAuth2ClientID != "" && c.OAuth2ClientSecret != ""

	switch {
	case hasBasic && hasOAuth2Any:
		return fmt.Errorf("email adapter: imap_password and oauth2_* are mutually exclusive; supply exactly one auth mode")
	case !hasBasic && !hasOAuth2Any:
		return fmt.Errorf("email adapter: one of imap_password or (oauth2_tenant_id + oauth2_client_id + oauth2_client_secret) is required")
	case !hasBasic && hasOAuth2Any && !hasOAuth2All:
		return fmt.Errorf("email adapter: oauth2_tenant_id, oauth2_client_id, and oauth2_client_secret must all be set together")
	}
	return nil
}

func parseStringSlice(cfg map[string]any, key string, lowercase bool) ([]string, error) {
	raw, ok := cfg[key].([]any)
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("email adapter: %s[%d] must be a string", key, i)
		}
		if lowercase {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "" {
				continue
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func validateEmailLimits(c *EmailConfig) error {
	if c.IMAPPort <= 0 {
		return fmt.Errorf("email adapter: imap_port must be positive (got %d)", c.IMAPPort)
	}
	if c.PollIntervalSeconds <= 0 {
		return fmt.Errorf("email adapter: poll_interval_seconds must be positive (got %d)", c.PollIntervalSeconds)
	}
	if c.FetchLimit <= 0 {
		return fmt.Errorf("email adapter: fetch_limit must be positive (got %d)", c.FetchLimit)
	}
	if c.WorkerCount <= 0 {
		return fmt.Errorf("email adapter: worker_count must be positive (got %d)", c.WorkerCount)
	}
	if c.QueueCapacity <= 0 {
		return fmt.Errorf("email adapter: queue_capacity must be positive (got %d)", c.QueueCapacity)
	}
	if c.MaxMessageSizeBytes <= 0 {
		return fmt.Errorf("email adapter: max_message_size_bytes must be positive (got %d)", c.MaxMessageSizeBytes)
	}
	if c.MaxAttachmentSizeBytes < 0 {
		return fmt.Errorf("email adapter: max_attachment_size_bytes must be >= 0 (got %d); use 0 to disable attachments", c.MaxAttachmentSizeBytes)
	}
	if c.MaxOutboundAttachmentSizeBytes < 0 {
		return fmt.Errorf("email adapter: max_outbound_attachment_size_bytes must be >= 0 (got %d); use 0 to disable outbound attachments", c.MaxOutboundAttachmentSizeBytes)
	}
	return nil
}

func (c *EmailConfig) UsesOAuth2() bool {
	return c.OAuth2TenantID != "" && c.OAuth2ClientID != "" && c.OAuth2ClientSecret != ""
}

func (c *EmailConfig) UsesBasicAuth() bool {
	return c.IMAPPassword != ""
}

func (c *EmailConfig) HasReplyTransport() bool {
	if c == nil || c.Mailbox == "" {
		return false
	}
	if c.UsesBasicAuth() && c.SMTPHost != "" && c.SMTPPort > 0 {
		return true
	}
	if c.UsesOAuth2() {
		return true
	}
	return false
}

func (c *EmailConfig) AuthMode() string {
	switch {
	case c.UsesOAuth2():
		return "oauth2"
	case c.UsesBasicAuth():
		return "basic"
	default:
		return "unknown"
	}
}

func getInt(m map[string]any, key string) (int, bool) {
	switch v := m[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

func getInt64(m map[string]any, key string) (int64, bool) {
	switch v := m[key].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	}
	return 0, false
}
