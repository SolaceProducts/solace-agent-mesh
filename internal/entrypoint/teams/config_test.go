// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestParseTeamsConfig_RequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		cfg    map[string]any
		errSub string
	}{
		{
			name:   "missing app_id",
			cfg:    map[string]any{"app_password": "secret"},
			errSub: "app_id",
		},
		{
			name:   "missing app_password",
			cfg:    map[string]any{"app_id": "test-id"},
			errSub: "app_password",
		},
		{
			name:   "empty app_id",
			cfg:    map[string]any{"app_id": "", "app_password": "secret"},
			errSub: "app_id",
		},
		{
			name:   "empty microsoft_app_id",
			cfg:    map[string]any{"microsoft_app_id": "", "app_password": "secret"},
			errSub: "app_id",
		},
		{
			name:   "empty app_password",
			cfg:    map[string]any{"app_id": "test-id", "app_password": ""},
			errSub: "app_password",
		},
		{
			name:   "empty microsoft_app_password",
			cfg:    map[string]any{"app_id": "test-id", "microsoft_app_password": ""},
			errSub: "app_password",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseTeamsConfig(tt.cfg)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errSub)
			}
			if !strings.Contains(err.Error(), tt.errSub) {
				t.Errorf("error %q does not mention %q", err.Error(), tt.errSub)
			}
		})
	}
}

func TestParseTeamsConfig_FeedbackEnabled(t *testing.T) {
	on, err := ParseTeamsConfig(map[string]any{"app_id": "x", "app_password": "y", "feedback_enabled": true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !on.FeedbackEnabled {
		t.Error("feedback_enabled=true not parsed")
	}
	off, err := ParseTeamsConfig(map[string]any{"app_id": "x", "app_password": "y"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if off.FeedbackEnabled {
		t.Error("FeedbackEnabled should default to false")
	}
}

func TestParseTeamsConfig_Defaults(t *testing.T) {
	cfg, err := ParseTeamsConfig(map[string]any{
		"app_id":       "test-id",
		"app_password": "test-secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AppID != "test-id" {
		t.Errorf("AppID = %q, want %q", cfg.AppID, "test-id")
	}
	if cfg.AppPassword != "test-secret" {
		t.Errorf("AppPassword = %q, want %q", cfg.AppPassword, "test-secret")
	}
	if cfg.InitialStatusMessage != "Processing your request..." {
		t.Errorf("InitialStatusMessage = %q, want default", cfg.InitialStatusMessage)
	}
	if !cfg.EnableTypingIndicator {
		t.Error("EnableTypingIndicator should default to true")
	}
	if cfg.BufferUpdateInterval != 500*time.Millisecond {
		t.Errorf("BufferUpdateInterval = %v, want 500ms", cfg.BufferUpdateInterval)
	}
	if cfg.MinAPIInterval != 200*time.Millisecond {
		t.Errorf("MinAPIInterval = %v, want 200ms", cfg.MinAPIInterval)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("HTTPPort = %d, want 8080", cfg.HTTPPort)
	}
	if cfg.EmailCacheTTLSeconds != 300 {
		t.Errorf("EmailCacheTTLSeconds = %d, want 300", cfg.EmailCacheTTLSeconds)
	}
	if cfg.MaxDownloadFileSizeMB != 100 {
		t.Errorf("MaxDownloadFileSizeMB = %d, want 100", cfg.MaxDownloadFileSizeMB)
	}
	if cfg.UseUserIdentityInChannels {
		t.Error("UseUserIdentityInChannels default = true, want false (secure-by-default: swap on in channels)")
	}
	if cfg.RunAs != "" {
		t.Errorf("RunAs default = %q, want empty (resolves to the baked-in default system user)", cfg.RunAs)
	}
}

func TestParseTeamsConfig_RunAs(t *testing.T) {
	t.Run("run_as keeps a custom system-user name", func(t *testing.T) {
		cfg, err := ParseTeamsConfig(map[string]any{"app_id": "id", "app_password": "pw", "run_as": "system:helpdesk"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RunAs != "system:helpdesk" {
			t.Errorf("RunAs = %q, want system:helpdesk (custom value honored)", cfg.RunAs)
		}
	})
	t.Run("use_user_identity_in_channels opts out of the swap", func(t *testing.T) {
		cfg, err := ParseTeamsConfig(map[string]any{"app_id": "id", "app_password": "pw", "use_user_identity_in_channels": true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.UseUserIdentityInChannels {
			t.Error("UseUserIdentityInChannels = false, want true")
		}
	})
	t.Run("legacy run_as=channel-user maps to the default system user", func(t *testing.T) {
		cfg, err := ParseTeamsConfig(map[string]any{"app_id": "id", "app_password": "pw", "run_as": "channel-user"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RunAs != rbac.ChannelSystemUserRunAs {
			t.Errorf("legacy channel-user run_as: RunAs = %q, want the channel system user", cfg.RunAs)
		}
	})
}

func TestParseTeamsConfig_AlternateKeys(t *testing.T) {
	cfg, err := ParseTeamsConfig(map[string]any{
		"microsoft_app_id":       "ms-id",
		"microsoft_app_password": "ms-secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AppID != "ms-id" {
		t.Errorf("AppID = %q, want %q", cfg.AppID, "ms-id")
	}
	if cfg.AppPassword != "ms-secret" {
		t.Errorf("AppPassword = %q, want %q", cfg.AppPassword, "ms-secret")
	}
}

func TestParseTeamsConfig_AllOptions(t *testing.T) {
	cfg, err := ParseTeamsConfig(map[string]any{
		"app_id":                         "id",
		"app_password":                   "pw",
		"tenant_id":                      "tenant-1",
		"default_agent_name":             "MyAgent",
		"initial_status_message":         "Working...",
		"enable_typing_indicator":        false,
		"buffer_update_interval_seconds": float64(3),
		"max_download_file_size_mb":      float64(50),
		"http_host":                      "127.0.0.1",
		"http_port":                      float64(9090),
		"teams_email_cache_ttl_seconds":  float64(600),
		"api_base_url":                   "http://localhost:3000",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q", cfg.TenantID)
	}
	if cfg.DefaultAgentName != "MyAgent" {
		t.Errorf("DefaultAgentName = %q", cfg.DefaultAgentName)
	}
	if cfg.InitialStatusMessage != "Working..." {
		t.Errorf("InitialStatusMessage = %q", cfg.InitialStatusMessage)
	}
	if cfg.EnableTypingIndicator {
		t.Error("EnableTypingIndicator should be false")
	}
	if cfg.BufferUpdateInterval != 3*time.Second {
		t.Errorf("BufferUpdateInterval = %v, want 3s", cfg.BufferUpdateInterval)
	}
	if cfg.MaxDownloadFileSizeMB != 50 {
		t.Errorf("MaxDownloadFileSizeMB = %d", cfg.MaxDownloadFileSizeMB)
	}
	if cfg.HTTPHost != "127.0.0.1" {
		t.Errorf("HTTPHost = %q", cfg.HTTPHost)
	}
	if cfg.HTTPPort != 9090 {
		t.Errorf("HTTPPort = %d", cfg.HTTPPort)
	}
	if cfg.EmailCacheTTLSeconds != 600 {
		t.Errorf("EmailCacheTTLSeconds = %d", cfg.EmailCacheTTLSeconds)
	}
	if cfg.APIBaseURL != "http://localhost:3000" {
		t.Errorf("APIBaseURL = %q", cfg.APIBaseURL)
	}
}

func captureLogger() (*slog.Logger, func() string) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf.String
}

func TestParseTeamsConfig_BothBufferKeys_WarnsAndMsWins(t *testing.T) {
	logger, captured := captureLogger()
	cfg, err := parseTeamsConfigWithLogger(map[string]any{
		"app_id":                         "id",
		"app_password":                   "pw",
		"buffer_update_interval_seconds": float64(3),
		"buffer_update_interval_ms":      float64(250),
	}, logger)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if cfg.BufferUpdateInterval != 250*time.Millisecond {
		t.Errorf("BufferUpdateInterval = %v, want 250ms (_ms must win)", cfg.BufferUpdateInterval)
	}
	if !strings.Contains(captured(), "_ms wins") {
		t.Errorf("expected warn about _ms winning over _seconds; logs:\n%s", captured())
	}
}

func TestParseTeamsConfig_LegacySSLKeysWarnAndAreIgnored(t *testing.T) {
	logger, captured := captureLogger()
	_, err := parseTeamsConfigWithLogger(map[string]any{
		"app_id":               "id",
		"app_password":         "pw",
		"ssl_certfile":         "/etc/certs/teams.crt",
		"ssl_keyfile":          "/etc/certs/teams.key",
		"ssl_keyfile_password": "secret",
	}, logger)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	logs := captured()
	for _, key := range []string{"ssl_certfile", "ssl_keyfile", "ssl_keyfile_password"} {
		if !strings.Contains(logs, key) {
			t.Errorf("expected warn naming %q; logs:\n%s", key, logs)
		}
	}
}

func TestParseTeamsConfig_NonNumericMinAPI_KeepsDefault_AndWarns(t *testing.T) {
	logger, captured := captureLogger()
	cfg, err := parseTeamsConfigWithLogger(map[string]any{
		"app_id":              "id",
		"app_password":        "pw",
		"min_api_interval_ms": "fast",
	}, logger)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if cfg.MinAPIInterval != 200*time.Millisecond {
		t.Errorf("MinAPIInterval = %v, want default 200ms (non-numeric should be ignored)", cfg.MinAPIInterval)
	}
	if !strings.Contains(captured(), "min_api_interval_ms") {
		t.Errorf("expected warn about non-numeric min_api_interval_ms; logs:\n%s", captured())
	}
}

func TestParseTeamsConfig_MinAPI_ZeroDisablesThrottle(t *testing.T) {
	cfg, err := ParseTeamsConfig(map[string]any{
		"app_id":              "id",
		"app_password":        "pw",
		"min_api_interval_ms": float64(0),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.MinAPIInterval != 0 {
		t.Errorf("MinAPIInterval = %v, want 0 for explicit opt-out", cfg.MinAPIInterval)
	}
}

func TestParseTeamsConfig_MicrosoftAppTenantID(t *testing.T) {
	cfg, err := ParseTeamsConfig(map[string]any{
		"app_id":                  "id",
		"app_password":            "pw",
		"microsoft_app_tenant_id": "tenant-alt",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.TenantID != "tenant-alt" {
		t.Errorf("TenantID = %q, want %q", cfg.TenantID, "tenant-alt")
	}
}

func TestParseTeamsConfig_MicrosoftAppTenantID_OverridesTenantID(t *testing.T) {
	cfg, err := ParseTeamsConfig(map[string]any{
		"app_id":                  "id",
		"app_password":            "pw",
		"tenant_id":               "original",
		"microsoft_app_tenant_id": "override",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.TenantID != "override" {
		t.Errorf("TenantID = %q, want %q (microsoft_app_tenant_id should win)", cfg.TenantID, "override")
	}
}

func TestParseTeamsConfig_TenantID_EmptyAndWhitespace(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
	}{
		{"empty tenant_id", map[string]any{"app_id": "id", "app_password": "pw", "tenant_id": ""}},
		{"whitespace tenant_id", map[string]any{"app_id": "id", "app_password": "pw", "tenant_id": "  "}},
		{"empty microsoft_app_tenant_id", map[string]any{"app_id": "id", "app_password": "pw", "microsoft_app_tenant_id": ""}},
		{"whitespace microsoft_app_tenant_id", map[string]any{"app_id": "id", "app_password": "pw", "microsoft_app_tenant_id": "  "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseTeamsConfig(tt.cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.TenantID != "" {
				t.Errorf("TenantID = %q, want empty (empty/whitespace should be rejected)", cfg.TenantID)
			}
		})
	}
}

func TestParseTeamsConfig_TenantID_Trimmed(t *testing.T) {
	cfg, err := ParseTeamsConfig(map[string]any{
		"app_id": "id", "app_password": "pw", "tenant_id": "  my-tenant  ",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.TenantID != "my-tenant" {
		t.Errorf("TenantID = %q, want %q (should be trimmed)", cfg.TenantID, "my-tenant")
	}
}

func TestParseTeamsConfig_BufferMsKey(t *testing.T) {
	cfg, err := ParseTeamsConfig(map[string]any{
		"app_id":                    "id",
		"app_password":              "pw",
		"buffer_update_interval_ms": float64(750),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.BufferUpdateInterval != 750*time.Millisecond {
		t.Errorf("BufferUpdateInterval = %v, want 750ms", cfg.BufferUpdateInterval)
	}
}
