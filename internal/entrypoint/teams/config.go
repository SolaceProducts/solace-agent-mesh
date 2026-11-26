// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const legacyChannelUserRunAs = "channel-user"

type TeamsConfig struct {
	AppID       string
	AppPassword string
	TenantID    string

	DefaultAgentName      string
	InitialStatusMessage  string
	EnableTypingIndicator bool
	BufferUpdateInterval  time.Duration
	MinAPIInterval        time.Duration
	MaxDownloadFileSizeMB int

	HTTPHost string
	HTTPPort int

	EmailCacheTTLSeconds int
	APIBaseURL           string

	LargeFileThresholdMB int

	FeedbackEnabled bool

	UseUserIdentityInChannels bool
	RunAs                     string
}

func ParseTeamsConfig(cfg map[string]any) (*TeamsConfig, error) {
	return parseTeamsConfigWithLogger(cfg, slog.Default())
}

func applyTeamsRunAsConfig(c *TeamsConfig, cfg map[string]any, logger *slog.Logger) {
	if v, ok := cfg["use_user_identity_in_channels"].(bool); ok {
		c.UseUserIdentityInChannels = v
	}
	if v, ok := cfg["run_as"].(string); ok {
		c.RunAs = v
	}
	if c.RunAs == legacyChannelUserRunAs {
		logger.Warn("teams adapter: run_as \"channel-user\" is deprecated; mapped to the channel service account, which keeps its Orchestrator-only reach. Set an explicit run_as service account, or use_user_identity_in_channels, to control channel identity.")
		c.RunAs = rbac.ChannelSystemUserRunAs
	}
}

func parseTeamsConfigWithLogger(cfg map[string]any, logger *slog.Logger) (*TeamsConfig, error) {
	c := &TeamsConfig{
		InitialStatusMessage:  "Processing your request...",
		EnableTypingIndicator: true,
		BufferUpdateInterval:  500 * time.Millisecond,
		MinAPIInterval:        200 * time.Millisecond,
		MaxDownloadFileSizeMB: 100,
		HTTPHost:              "0.0.0.0",
		HTTPPort:              8080,
		EmailCacheTTLSeconds:  300,
		LargeFileThresholdMB:  10,
	}

	if v, ok := cfg["app_id"].(string); ok && v != "" {
		c.AppID = v
	}
	if v, ok := cfg["microsoft_app_id"].(string); ok && v != "" {
		c.AppID = v
	}
	if c.AppID == "" {
		return nil, fmt.Errorf("teams adapter: app_id (or microsoft_app_id) is required")
	}

	if v, ok := cfg["app_password"].(string); ok && v != "" {
		c.AppPassword = v
	}
	if v, ok := cfg["microsoft_app_password"].(string); ok && v != "" {
		c.AppPassword = v
	}
	if c.AppPassword == "" {
		return nil, fmt.Errorf("teams adapter: app_password (or microsoft_app_password) is required")
	}

	if v, ok := cfg["tenant_id"].(string); ok && strings.TrimSpace(v) != "" {
		c.TenantID = strings.TrimSpace(v)
	}
	if v, ok := cfg["microsoft_app_tenant_id"].(string); ok && strings.TrimSpace(v) != "" {
		c.TenantID = strings.TrimSpace(v)
	}
	if v, ok := cfg["default_agent_name"].(string); ok {
		c.DefaultAgentName = v
	}
	if v, ok := cfg["initial_status_message"].(string); ok {
		c.InitialStatusMessage = v
	}
	if v, ok := cfg["enable_typing_indicator"].(bool); ok {
		c.EnableTypingIndicator = v
	}
	if v, ok := cfg["feedback_enabled"].(bool); ok {
		c.FeedbackEnabled = v
	}
	applyTeamsRunAsConfig(c, cfg, logger)

	hasBufferSec := false
	if v, ok, present := getIntKey(cfg, "buffer_update_interval_seconds"); present {
		if !ok {
			logger.Warn("teams adapter: ignoring non-numeric buffer_update_interval_seconds; keeping default", "default", c.BufferUpdateInterval)
		} else if v > 0 {
			c.BufferUpdateInterval = time.Duration(v) * time.Second
			hasBufferSec = true
		}
	}
	if v, ok, present := getIntKey(cfg, "buffer_update_interval_ms"); present {
		if !ok {
			logger.Warn("teams adapter: ignoring non-numeric buffer_update_interval_ms; keeping previous value", "value", c.BufferUpdateInterval)
		} else if v > 0 {
			if hasBufferSec {
				logger.Warn("teams adapter: both buffer_update_interval_seconds and buffer_update_interval_ms set; _ms wins", "ms", v)
			}
			c.BufferUpdateInterval = time.Duration(v) * time.Millisecond
		}
	}

	if v, ok, present := getIntKey(cfg, "min_api_interval_ms"); present {
		if !ok {
			logger.Warn("teams adapter: ignoring non-numeric min_api_interval_ms; keeping default", "default", c.MinAPIInterval)
		} else if v >= 0 {
			c.MinAPIInterval = time.Duration(v) * time.Millisecond
		}
	}

	if v, ok, _ := getIntKey(cfg, "max_download_file_size_mb"); ok && v > 0 {
		c.MaxDownloadFileSizeMB = v
	}
	if v, ok := cfg["http_host"].(string); ok && v != "" {
		c.HTTPHost = v
	}
	if v, ok, present := getIntKey(cfg, "http_port"); present {
		if ok {
			c.HTTPPort = v
		}
	}
	tlsKeysTerminatedAtGatewayProxy := []string{"ssl_certfile", "ssl_keyfile", "ssl_keyfile_password"}
	for _, k := range tlsKeysTerminatedAtGatewayProxy {
		if _, present := cfg[k]; present {
			logger.Warn("teams adapter: ignoring TLS config; terminate TLS at the gateway proxy / ingress", "key", k)
		}
	}
	if v, ok, present := getIntKey(cfg, "teams_email_cache_ttl_seconds"); present && ok {
		c.EmailCacheTTLSeconds = v
	}
	if v, ok := cfg["api_base_url"].(string); ok {
		c.APIBaseURL = v
	}
	if v, ok, _ := getIntKey(cfg, "large_file_threshold_mb"); ok && v > 0 {
		c.LargeFileThresholdMB = v
	}

	return c, nil
}

func getIntKey(m map[string]any, key string) (value int, numeric bool, present bool) {
	raw, present := m[key]
	if !present {
		return 0, false, false
	}
	switch v := raw.(type) {
	case int:
		return v, true, true
	case int64:
		return int(v), true, true
	case float64:
		return int(v), true, true
	}
	return 0, false, true
}
