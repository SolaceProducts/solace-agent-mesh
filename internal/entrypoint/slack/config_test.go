// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestParseSlackConfig_Valid(t *testing.T) {
	raw := map[string]any{
		"slack_bot_token":               "xoxb-test-token",
		"slack_app_token":               "xapp-test-token",
		"slack_initial_status_message":  "Working on it...",
		"correct_markdown_formatting":   true,
		"show_activity_timeline":        true,
		"feedback_enabled":              true,
		"slack_email_cache_ttl_seconds": 7200,
		"default_agent_name":            "TestAgent",
	}

	cfg, err := ParseSlackConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BotToken != "xoxb-test-token" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "xoxb-test-token")
	}
	if cfg.AppToken != "xapp-test-token" {
		t.Errorf("AppToken = %q, want %q", cfg.AppToken, "xapp-test-token")
	}
	if cfg.InitialStatusMessage != "Working on it..." {
		t.Errorf("InitialStatusMessage = %q, want %q", cfg.InitialStatusMessage, "Working on it...")
	}
	if !cfg.CorrectMarkdown {
		t.Error("CorrectMarkdown should be true")
	}
	if !cfg.ShowActivityTimeline {
		t.Error("ShowActivityTimeline should be true")
	}
	if !cfg.FeedbackEnabled {
		t.Error("FeedbackEnabled should be true")
	}
	if cfg.EmailCacheTTLSeconds != 7200 {
		t.Errorf("EmailCacheTTLSeconds = %d, want 7200", cfg.EmailCacheTTLSeconds)
	}
	if cfg.DefaultAgentName != "TestAgent" {
		t.Errorf("DefaultAgentName = %q, want %q", cfg.DefaultAgentName, "TestAgent")
	}
}

func TestParseSlackConfig_Defaults(t *testing.T) {
	raw := map[string]any{
		"slack_bot_token": "xoxb-token",
		"slack_app_token": "xapp-token",
	}

	cfg, err := ParseSlackConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.InitialStatusMessage != "Got it, thinking..." {
		t.Errorf("InitialStatusMessage default = %q, want %q", cfg.InitialStatusMessage, "Got it, thinking...")
	}
	if !cfg.CorrectMarkdown {
		t.Error("CorrectMarkdown should default to true")
	}
	if cfg.FeedbackEnabled {
		t.Error("FeedbackEnabled should default to false")
	}
	if cfg.EmailCacheTTLSeconds != 3600 {
		t.Errorf("EmailCacheTTLSeconds default = %d, want 3600", cfg.EmailCacheTTLSeconds)
	}
	if cfg.ActivityDisplayMode != "plan" {
		t.Errorf("ActivityDisplayMode default = %q, want plan", cfg.ActivityDisplayMode)
	}
	if cfg.UseUserIdentityInChannels {
		t.Error("UseUserIdentityInChannels default = true, want false (secure-by-default: swap on in channels)")
	}
	if cfg.RunAs != "" {
		t.Errorf("RunAs default = %q, want empty (the baked-in default system user)", cfg.RunAs)
	}
}

func TestParseSlackConfig_RunAs(t *testing.T) {
	t.Run("run_as keeps a custom system-user name", func(t *testing.T) {
		raw := map[string]any{"slack_bot_token": "xoxb", "slack_app_token": "xapp", "run_as": "system:helpdesk"}
		cfg, err := ParseSlackConfig(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RunAs != "system:helpdesk" {
			t.Errorf("RunAs = %q, want system:helpdesk (custom value honored)", cfg.RunAs)
		}
		if cfg.UseUserIdentityInChannels {
			t.Error("swap should still be active (UseUserIdentityInChannels=false)")
		}
	})

	t.Run("use_user_identity_in_channels opts out of the swap", func(t *testing.T) {
		raw := map[string]any{"slack_bot_token": "xoxb", "slack_app_token": "xapp", "use_user_identity_in_channels": true}
		cfg, err := ParseSlackConfig(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.UseUserIdentityInChannels {
			t.Error("UseUserIdentityInChannels = false, want true")
		}
	})

	t.Run("legacy slack_user_run_as=false maps to using the user identity", func(t *testing.T) {
		raw := map[string]any{"slack_bot_token": "xoxb", "slack_app_token": "xapp", "slack_user_run_as": false}
		cfg, err := ParseSlackConfig(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.UseUserIdentityInChannels {
			t.Error("legacy slack_user_run_as=false should map to UseUserIdentityInChannels=true")
		}
	})

	t.Run("legacy run_as=channel-user maps to the channel system user", func(t *testing.T) {
		raw := map[string]any{"slack_bot_token": "xoxb", "slack_app_token": "xapp", "run_as": "channel-user"}
		cfg, err := ParseSlackConfig(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RunAs != rbac.ChannelSystemUserRunAs {
			t.Errorf("legacy channel-user run_as: RunAs = %q, want %q (keeps the old Orchestrator-only reach)", cfg.RunAs, rbac.ChannelSystemUserRunAs)
		}
	})
}

func TestParseSlackConfig_ActivityDisplayMode(t *testing.T) {
	base := func(mode any) map[string]any {
		return map[string]any{
			"slack_bot_token":       "xoxb-token",
			"slack_app_token":       "xapp-token",
			"activity_display_mode": mode,
		}
	}
	cases := []struct {
		name string
		mode any
		want string
	}{
		{"timeline accepted", "timeline", "timeline"},
		{"plan accepted", "plan", "plan"},
		{"invalid keeps plan default", "grouped", "plan"},
		{"non-string keeps plan default", 42, "plan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseSlackConfig(base(tc.mode))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ActivityDisplayMode != tc.want {
				t.Errorf("ActivityDisplayMode = %q, want %q", cfg.ActivityDisplayMode, tc.want)
			}
		})
	}
}

func TestParseSlackConfig_MissingBotToken(t *testing.T) {
	raw := map[string]any{
		"slack_app_token": "xapp-token",
	}
	_, err := ParseSlackConfig(raw)
	if err == nil {
		t.Fatal("expected error for missing bot token")
	}
}

func TestParseSlackConfig_MissingAppToken(t *testing.T) {
	raw := map[string]any{
		"slack_bot_token": "xoxb-token",
	}
	_, err := ParseSlackConfig(raw)
	if err == nil {
		t.Fatal("expected error for missing app token")
	}
}

func TestParseSlackConfig_FloatTTL(t *testing.T) {
	raw := map[string]any{
		"slack_bot_token":               "xoxb-token",
		"slack_app_token":               "xapp-token",
		"slack_email_cache_ttl_seconds": float64(1800),
	}

	cfg, err := ParseSlackConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EmailCacheTTLSeconds != 1800 {
		t.Errorf("EmailCacheTTLSeconds = %d, want 1800", cfg.EmailCacheTTLSeconds)
	}
}
