// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const legacyChannelUserRunAs = "channel-user"

type SlackConfig struct {
	BotToken                  string
	AppToken                  string
	InitialStatusMessage      string
	CorrectMarkdown           bool
	ShowActivityTimeline      bool
	ActivityDisplayMode       string
	UseUserIdentityInChannels bool
	RunAs                     string
	FeedbackEnabled           bool
	EmailCacheTTLSeconds      int
	DefaultAgentName          string
	APIBaseURL                string
	DisableRateLimiting       bool
	ForceSocketMode           bool

	ThreadHistoryLimit    int
	ChannelHistoryLimit   int
	HistoryMaxAttachments int
}

func ParseSlackConfig(cfg map[string]any) (*SlackConfig, error) {
	return parseSlackConfigWithLogger(cfg, slog.Default())
}

func parseSlackConfigWithLogger(cfg map[string]any, logger *slog.Logger) (*SlackConfig, error) {
	c := &SlackConfig{
		InitialStatusMessage:  "Got it, thinking...",
		CorrectMarkdown:       true,
		FeedbackEnabled:       false,
		EmailCacheTTLSeconds:  3600,
		ActivityDisplayMode:   "plan",
		ThreadHistoryLimit:    50,
		ChannelHistoryLimit:   0,
		HistoryMaxAttachments: 0,
	}

	if v, ok := cfg["slack_bot_token"].(string); ok && v != "" {
		c.BotToken = v
	} else {
		return nil, fmt.Errorf("slack adapter: slack_bot_token is required")
	}

	if v, ok := cfg["slack_app_token"].(string); ok && v != "" {
		c.AppToken = v
	} else {
		return nil, fmt.Errorf("slack adapter: slack_app_token is required")
	}

	if v, ok := cfg["slack_initial_status_message"].(string); ok {
		c.InitialStatusMessage = v
	}
	if v, ok := cfg["correct_markdown_formatting"].(bool); ok {
		c.CorrectMarkdown = v
	}
	if v, ok := cfg["show_activity_timeline"].(bool); ok {
		c.ShowActivityTimeline = v
	}
	if v, ok := cfg["activity_display_mode"].(string); ok && (v == "plan" || v == "timeline") {
		c.ActivityDisplayMode = v
	}
	if v, ok := cfg["use_user_identity_in_channels"].(bool); ok {
		c.UseUserIdentityInChannels = v
	}
	if v, ok := cfg["run_as"].(string); ok {
		c.RunAs = v
	}
	if legacy, ok := cfg["slack_user_run_as"].(bool); ok {
		if !legacy {
			c.UseUserIdentityInChannels = true
		}
		logger.Warn("slack adapter: slack_user_run_as is deprecated; use use_user_identity_in_channels (true = real user in channels) and run_as (service account for the confused-deputy swap)")
	}
	if c.RunAs == legacyChannelUserRunAs {
		logger.Warn("slack adapter: run_as \"channel-user\" is deprecated; mapped to the channel service account, which keeps its Orchestrator-only reach. Set an explicit run_as service account, or use_user_identity_in_channels, to control channel identity.")
		c.RunAs = rbac.ChannelSystemUserRunAs
	}
	if v, ok := cfg["feedback_enabled"].(bool); ok {
		c.FeedbackEnabled = v
	}
	c.EmailCacheTTLSeconds = intConfig(cfg, "slack_email_cache_ttl_seconds", c.EmailCacheTTLSeconds)
	if v, ok := cfg["default_agent_name"].(string); ok {
		c.DefaultAgentName = v
	}
	if v, ok := cfg["slack_api_url"].(string); ok {
		c.APIBaseURL = v
	}
	if v, ok := cfg["disable_rate_limiting"].(bool); ok {
		c.DisableRateLimiting = v
	}
	if v, ok := cfg["force_socket_mode"].(bool); ok {
		c.ForceSocketMode = v
	}
	c.ThreadHistoryLimit = intConfig(cfg, "slack_thread_history_limit", c.ThreadHistoryLimit)
	c.ChannelHistoryLimit = intConfig(cfg, "slack_channel_history_limit", c.ChannelHistoryLimit)
	c.HistoryMaxAttachments = intConfig(cfg, "slack_history_max_attachments", c.HistoryMaxAttachments)

	return c, nil
}

func intConfig(cfg map[string]any, key string, def int) int {
	switch v := cfg[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return def
	}
}
