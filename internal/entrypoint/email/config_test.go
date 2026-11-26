// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"strings"
	"testing"
	"time"
)

func TestParseEmailConfig_OAuth2Defaults(t *testing.T) {
	t.Parallel()
	cfg, err := ParseEmailConfig(minimalRawConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := cfg.PollIntervalSeconds, 30; got != want {
		t.Errorf("PollIntervalSeconds default = %d, want %d", got, want)
	}
	if cfg.MaxAttachmentSizeBytes == 0 {
		t.Error("MaxAttachmentSizeBytes should default to nonzero")
	}
	if !cfg.UsesOAuth2() {
		t.Error("UsesOAuth2() = false, want true (minimal config exercises OAuth2 fields)")
	}
	if cfg.UsesBasicAuth() {
		t.Error("UsesBasicAuth() = true, want false")
	}
}

func TestParseEmailConfig_RequiredFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		mutate      func(map[string]any)
		wantErrSubs string
	}{
		{"missing mailbox", func(m map[string]any) { delete(m, "mailbox") }, "mailbox"},
		{"missing imap_host", func(m map[string]any) { delete(m, "imap_host") }, "imap_host"},
		{"missing trusted_authserv_ids", func(m map[string]any) { delete(m, "trusted_authserv_ids") }, "trusted_authserv_ids"},
		{"missing all auth modes",
			func(m map[string]any) {
				delete(m, "oauth2_tenant_id")
				delete(m, "oauth2_client_id")
				delete(m, "oauth2_client_secret")
				delete(m, "imap_password")
			},
			"imap_password",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := minimalRawConfig()
			tc.mutate(raw)
			_, err := ParseEmailConfig(raw)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErrSubs) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSubs)
			}
		})
	}
}

func TestParseEmailConfig_BasicAuthMode(t *testing.T) {
	t.Parallel()
	raw := minimalRawConfig()
	delete(raw, "oauth2_tenant_id")
	delete(raw, "oauth2_client_id")
	delete(raw, "oauth2_client_secret")
	raw["imap_password"] = "app-password"

	cfg, err := ParseEmailConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.UsesBasicAuth() {
		t.Error("UsesBasicAuth() = false, want true")
	}
	if cfg.UsesOAuth2() {
		t.Error("UsesOAuth2() = true, want false")
	}
	if cfg.IMAPPassword != "app-password" {
		t.Errorf("IMAPPassword = %q, want %q", cfg.IMAPPassword, "app-password")
	}
}

func TestParseEmailConfig_RejectsBothAuthModes(t *testing.T) {
	t.Parallel()
	raw := minimalRawConfig()
	raw["imap_password"] = "app-password"

	_, err := ParseEmailConfig(raw)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q does not mention mutual exclusion", err.Error())
	}
}

func TestParseEmailConfig_PartialOAuth2(t *testing.T) {
	t.Parallel()
	for _, drop := range []string{"oauth2_tenant_id", "oauth2_client_id", "oauth2_client_secret"} {
		t.Run("missing_"+drop, func(t *testing.T) {
			t.Parallel()
			raw := minimalRawConfig()
			delete(raw, drop)
			_, err := ParseEmailConfig(raw)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "must all be set together") {
				t.Errorf("error %q missing partial-OAuth2 hint", err.Error())
			}
		})
	}
}

func TestParseEmailConfig_PolicyModeDefault(t *testing.T) {
	t.Parallel()
	raw := minimalRawConfig()
	delete(raw, "policy_mode")
	cfg, err := ParseEmailConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PolicyMode != ModeRequireDMARC {
		t.Errorf("PolicyMode default = %q, want %q", cfg.PolicyMode, ModeRequireDMARC)
	}
}

func TestParseEmailConfig_PolicyModeEnum(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"log_only", "require_dmarc", "strict"} {
		raw := minimalRawConfig()
		raw["policy_mode"] = mode
		if _, err := ParseEmailConfig(raw); err != nil {
			t.Errorf("mode %q: unexpected error %v", mode, err)
		}
	}
	raw := minimalRawConfig()
	raw["policy_mode"] = "bogus"
	if _, err := ParseEmailConfig(raw); err == nil {
		t.Error("expected error for bogus policy_mode")
	}
}

func TestParseEmailConfig_RequireDMARCForIdentity(t *testing.T) {
	t.Parallel()

	t.Run("default true when omitted", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseEmailConfig(minimalRawConfig())
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if !cfg.RequireDMARCForIdentity {
			t.Errorf("RequireDMARCForIdentity = false, want true (production default)")
		}
	})

	t.Run("explicit false honored", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["require_dmarc_for_identity"] = false
		cfg, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if cfg.RequireDMARCForIdentity {
			t.Errorf("RequireDMARCForIdentity = true, want false (explicit opt-out)")
		}
	})

	t.Run("explicit true honored", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["require_dmarc_for_identity"] = true
		cfg, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if !cfg.RequireDMARCForIdentity {
			t.Errorf("RequireDMARCForIdentity = false, want true (explicit on)")
		}
	})
}

func TestParseEmailConfig_InternalTrust(t *testing.T) {
	t.Parallel()

	t.Run("absent leaves field empty (feature disabled)", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseEmailConfig(minimalRawConfig())
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if cfg.TrustedInternalTenantID != "" {
			t.Errorf("TrustedInternalTenantID = %q, want empty (feature opt-in)", cfg.TrustedInternalTenantID)
		}
	})

	t.Run("propagates value when present", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["trusted_internal_tenant_id"] = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		cfg, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if cfg.TrustedInternalTenantID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
			t.Errorf("TrustedInternalTenantID = %q", cfg.TrustedInternalTenantID)
		}
	})
}

func TestParseEmailConfig_AllowedSenders(t *testing.T) {
	t.Parallel()

	t.Run("absent leaves slice nil (allow all)", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseEmailConfig(minimalRawConfig())
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if len(cfg.AllowedSenders) != 0 {
			t.Errorf("AllowedSenders = %v, want empty (no gate when unset)", cfg.AllowedSenders)
		}
	})

	t.Run("normalizes to lowercase", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["allowed_senders"] = []any{"Alice@Example.ORG", " bob@example.org  ", "carol@example.org"}
		cfg, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		want := []string{"alice@example.org", "bob@example.org", "carol@example.org"}
		if got := cfg.AllowedSenders; len(got) != len(want) {
			t.Fatalf("AllowedSenders len = %d (%v), want %d (%v)", len(got), got, len(want), want)
		}
		for i, w := range want {
			if cfg.AllowedSenders[i] != w {
				t.Errorf("AllowedSenders[%d] = %q, want %q", i, cfg.AllowedSenders[i], w)
			}
		}
	})

	t.Run("rejects non-string entries", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["allowed_senders"] = []any{"alice@example.org", 42}
		_, err := ParseEmailConfig(raw)
		if err == nil {
			t.Fatal("expected error for non-string allowed_senders entry; got nil")
		}
		if !strings.Contains(err.Error(), "allowed_senders") {
			t.Errorf("error %q does not mention allowed_senders", err.Error())
		}
	})
}

func minimalRawConfig() map[string]any {
	return map[string]any{
		"mailbox":              "sam-email-entrypoint@example.org",
		"imap_host":            "outlook.office365.com",
		"imap_port":            993,
		"oauth2_tenant_id":     "tenant-uuid",
		"oauth2_client_id":     "client-uuid",
		"oauth2_client_secret": "secret",
		"trusted_authserv_ids": []any{"protection.outlook.com"},
	}
}

func TestParseEmailConfig_DefaultAgentName(t *testing.T) {
	t.Parallel()

	t.Run("absent leaves field empty", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseEmailConfig(minimalRawConfig())
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if cfg.DefaultAgentName != "" {
			t.Errorf("DefaultAgentName = %q, want empty when key omitted", cfg.DefaultAgentName)
		}
	})

	t.Run("present is honored", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["default_agent_name"] = "Manager"
		cfg, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if cfg.DefaultAgentName != "Manager" {
			t.Errorf("DefaultAgentName = %q, want Manager", cfg.DefaultAgentName)
		}
	})
}

func TestParseEmailConfig_MIMELists(t *testing.T) {
	t.Parallel()

	t.Run("absent leaves both empty", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseEmailConfig(minimalRawConfig())
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		if len(cfg.MIMEAllowlist) != 0 || len(cfg.MIMEDenylist) != 0 {
			t.Errorf("expected both lists empty by default; allow=%v deny=%v",
				cfg.MIMEAllowlist, cfg.MIMEDenylist)
		}
	})

	t.Run("entries are lowercased and trimmed", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["mime_allowlist"] = []any{"  Text/Plain  ", "IMAGE/*"}
		raw["mime_denylist"] = []any{"Application/X-MSDOWNLOAD", "  ", ""}
		cfg, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("ParseEmailConfig: %v", err)
		}
		wantAllow := []string{"text/plain", "image/*"}
		if got := cfg.MIMEAllowlist; !equalStrings(got, wantAllow) {
			t.Errorf("MIMEAllowlist = %v, want %v", got, wantAllow)
		}
		wantDeny := []string{"application/x-msdownload"}
		if got := cfg.MIMEDenylist; !equalStrings(got, wantDeny) {
			t.Errorf("MIMEDenylist = %v, want %v", got, wantDeny)
		}
	})

	t.Run("non-string entry errors", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["mime_allowlist"] = []any{"text/plain", 42}
		_, err := ParseEmailConfig(raw)
		if err == nil || !strings.Contains(err.Error(), "mime_allowlist[1]") {
			t.Errorf("expected non-string error, got %v", err)
		}
	})
}

func TestParseEmailConfig_DeliveryMode(t *testing.T) {
	t.Parallel()

	t.Run("default is poll", func(t *testing.T) {
		t.Parallel()
		c, err := ParseEmailConfig(minimalRawConfig())
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.DeliveryMode != DeliveryModePoll {
			t.Errorf("DeliveryMode = %q, want %q", c.DeliveryMode, DeliveryModePoll)
		}
	})

	t.Run("explicit idle", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["delivery_mode"] = "idle"
		c, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.DeliveryMode != DeliveryModeIdle {
			t.Errorf("DeliveryMode = %q, want %q", c.DeliveryMode, DeliveryModeIdle)
		}
	})

	t.Run("explicit poll", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["delivery_mode"] = "poll"
		c, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.DeliveryMode != DeliveryModePoll {
			t.Errorf("DeliveryMode = %q, want %q", c.DeliveryMode, DeliveryModePoll)
		}
	})

	t.Run("invalid value rejected", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["delivery_mode"] = "magic"
		_, err := ParseEmailConfig(raw)
		if err == nil {
			t.Fatal("expected error for invalid delivery_mode, got nil")
		}
	})
}

func TestParseEmailConfig_IdleKeepaliveInterval(t *testing.T) {
	t.Parallel()

	t.Run("default is zero (Idler.Run substitutes 9 minutes)", func(t *testing.T) {
		t.Parallel()
		c, err := ParseEmailConfig(minimalRawConfig())
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if c.IdleKeepaliveInterval != 0 {
			t.Errorf("IdleKeepaliveInterval = %v, want 0 (Idler default)", c.IdleKeepaliveInterval)
		}
	})

	t.Run("explicit value parsed in seconds", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["idle_keepalive_interval_seconds"] = 120
		c, err := ParseEmailConfig(raw)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got, want := c.IdleKeepaliveInterval, 120*time.Second; got != want {
			t.Errorf("IdleKeepaliveInterval = %v, want %v", got, want)
		}
	})

	t.Run("non-positive rejected", func(t *testing.T) {
		t.Parallel()
		raw := minimalRawConfig()
		raw["idle_keepalive_interval_seconds"] = 0
		if _, err := ParseEmailConfig(raw); err == nil {
			t.Fatal("expected error for idle_keepalive_interval_seconds=0, got nil")
		}
		raw["idle_keepalive_interval_seconds"] = -5
		if _, err := ParseEmailConfig(raw); err == nil {
			t.Fatal("expected error for negative idle_keepalive_interval_seconds, got nil")
		}
	})
}

func TestEmailConfig_HasReplyTransport(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  *EmailConfig
		want bool
	}{
		{
			name: "nil receiver returns false",
			cfg:  nil,
			want: false,
		},
		{
			name: "zero value returns false",
			cfg:  &EmailConfig{},
			want: false,
		},
		{
			name: "smtp complete (basic auth path)",
			cfg: &EmailConfig{
				Mailbox:      "sam@gmail.com",
				SMTPHost:     "smtp.gmail.com",
				SMTPPort:     587,
				IMAPPassword: "app-password",
			},
			want: true,
		},
		{
			name: "smtp missing password",
			cfg: &EmailConfig{
				Mailbox:  "sam@gmail.com",
				SMTPHost: "smtp.gmail.com",
				SMTPPort: 587,
			},
			want: false,
		},
		{
			name: "smtp missing mailbox (no From/Username)",
			cfg: &EmailConfig{
				SMTPHost:     "smtp.gmail.com",
				SMTPPort:     587,
				IMAPPassword: "app-password",
			},
			want: false,
		},
		{
			name: "smtp missing host",
			cfg: &EmailConfig{
				Mailbox:      "sam@gmail.com",
				SMTPPort:     587,
				IMAPPassword: "app-password",
			},
			want: false,
		},
		{
			name: "smtp missing port",
			cfg: &EmailConfig{
				Mailbox:      "sam@gmail.com",
				SMTPHost:     "smtp.gmail.com",
				IMAPPassword: "app-password",
			},
			want: false,
		},
		{
			name: "oauth2 complete (graph path)",
			cfg: &EmailConfig{
				Mailbox:            "sam@example.org",
				OAuth2TenantID:     "tenant-uuid",
				OAuth2ClientID:     "client-uuid",
				OAuth2ClientSecret: "secret",
			},
			want: true,
		},
		{
			name: "oauth2 partial: missing secret",
			cfg: &EmailConfig{
				Mailbox:        "sam@example.org",
				OAuth2TenantID: "tenant-uuid",
				OAuth2ClientID: "client-uuid",
			},
			want: false,
		},
		{
			name: "oauth2 complete but mailbox missing",
			cfg: &EmailConfig{
				OAuth2TenantID:     "tenant-uuid",
				OAuth2ClientID:     "client-uuid",
				OAuth2ClientSecret: "secret",
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cfg.HasReplyTransport(); got != tc.want {
				t.Errorf("HasReplyTransport() = %v, want %v", got, tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseEmailConfig_HILSettings(t *testing.T) {
	m := minimalRawConfig()
	m["external_base_url"] = "https://gw.example.com/"
	m["hil_token_ttl_seconds"] = 120
	cfg, err := ParseEmailConfig(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ExternalBaseURL != "https://gw.example.com" {
		t.Errorf("ExternalBaseURL = %q, want trimmed https://gw.example.com", cfg.ExternalBaseURL)
	}
	if cfg.HILTokenTTL != 120*time.Second {
		t.Errorf("HILTokenTTL = %v, want 120s", cfg.HILTokenTTL)
	}

	t.Setenv("EMAIL_EXTERNAL_BASE_URL", "https://env.example.com")
	cfg2, err := ParseEmailConfig(minimalRawConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg2.ExternalBaseURL != "https://env.example.com" {
		t.Errorf("ExternalBaseURL via env = %q, want https://env.example.com", cfg2.ExternalBaseURL)
	}
	if cfg2.HILTokenTTL != 30*time.Minute {
		t.Errorf("HILTokenTTL default = %v, want 30m", cfg2.HILTokenTTL)
	}
}
