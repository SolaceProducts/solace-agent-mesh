// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

func TestParseGatewayConfig_RequiredFields(t *testing.T) {
	_, err := ParseGatewayConfig(map[string]any{"gateway_id": "gw"})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}

	_, err = ParseGatewayConfig(map[string]any{"namespace": "test-ns"})
	if err == nil {
		t.Fatal("expected error for missing gateway_id")
	}

	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "my-gw",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Namespace != "test-ns" {
		t.Errorf("namespace = %q, want %q", cfg.Namespace, "test-ns")
	}
	if cfg.GatewayID != "my-gw" {
		t.Errorf("gateway_id = %q, want %q", cfg.GatewayID, "my-gw")
	}
}

func TestParseGatewayConfig_RejectsEvalReplyGatewayID(t *testing.T) {
	_, err := ParseGatewayConfig(map[string]any{"namespace": "test-ns", "gateway_id": a2a.EvalGatewayID})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("gateway_id %q accepted, want reserved-id error; got %v", a2a.EvalGatewayID, err)
	}
}

func TestParseGatewayConfig_Defaults(t *testing.T) {
	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.FastAPIHost != "127.0.0.1" {
		t.Errorf("FastAPIHost = %q, want %q", cfg.FastAPIHost, "127.0.0.1")
	}
	if cfg.FastAPIPort != 8800 {
		t.Errorf("FastAPIPort = %d, want %d", cfg.FastAPIPort, 8800)
	}
	if cfg.SSEMaxQueueSize != 200 {
		t.Errorf("SSEMaxQueueSize = %d, want %d", cfg.SSEMaxQueueSize, 200)
	}
	if cfg.DefaultUserIdentity != "" {
		t.Errorf("DefaultUserIdentity = %q, want empty (Python parity)", cfg.DefaultUserIdentity)
	}
	if cfg.ExternalAuthProvider != "generic" {
		t.Errorf("ExternalAuthProvider = %q, want %q", cfg.ExternalAuthProvider, "generic")
	}
	if cfg.GatewayArtifactContentLimitBytes != 10_000_000 {
		t.Errorf("GatewayArtifactContentLimitBytes = %d, want %d", cfg.GatewayArtifactContentLimitBytes, 10_000_000)
	}
	if cfg.CardPublishIntervalSeconds != 60 {
		t.Errorf("CardPublishIntervalSeconds = %d, want %d", cfg.CardPublishIntervalSeconds, 60)
	}
	if cfg.GatewayMaxUploadSizeBytes != 50_000_000 {
		t.Errorf("GatewayMaxUploadSizeBytes = %d, want %d", cfg.GatewayMaxUploadSizeBytes, 50_000_000)
	}
	if cfg.GatewayMaxProjectInstructionsChars != 32_000 {
		t.Errorf("GatewayMaxProjectInstructionsChars = %d, want %d", cfg.GatewayMaxProjectInstructionsChars, 32_000)
	}
	if cfg.GatewayMaxProjectDescriptionChars != 1000 {
		t.Errorf("GatewayMaxProjectDescriptionChars = %d, want %d", cfg.GatewayMaxProjectDescriptionChars, 1000)
	}
	if !cfg.EnableEmbedResolution {
		t.Error("EnableEmbedResolution should default to true")
	}
	if !cfg.AsyncEventLog {
		t.Error("AsyncEventLog should default to true")
	}
	if cfg.TaskRequestTTLSeconds != 3600 {
		t.Errorf("TaskRequestTTLSeconds = %d, want %d", cfg.TaskRequestTTLSeconds, 3600)
	}
	if cfg.TaskTimeoutSeconds != 300 {
		t.Errorf("TaskTimeoutSeconds = %d, want %d", cfg.TaskTimeoutSeconds, 300)
	}
	if cfg.TaskHardTimeoutSeconds != 3600 {
		t.Errorf("TaskHardTimeoutSeconds = %d, want %d", cfg.TaskHardTimeoutSeconds, 3600)
	}
}

func TestParseGatewayConfig_HardTimeoutMustExceedSoft(t *testing.T) {
	cfgWithHard := func(hardSec int) map[string]any {
		return map[string]any{
			"namespace": "test-ns", "gateway_id": "test-gw",
			"task_timeout_seconds": 300, "task_hard_timeout_seconds": hardSec,
		}
	}

	for _, hard := range []int{120, 300} {
		if _, err := ParseGatewayConfig(cfgWithHard(hard)); err == nil {
			t.Errorf("hard=%d soft=300: expected error, got nil", hard)
		}
	}

	if _, err := ParseGatewayConfig(cfgWithHard(0)); err != nil {
		t.Fatalf("hard=0 (disabled) should be allowed, got %v", err)
	}

	cfg, err := ParseGatewayConfig(cfgWithHard(3600))
	if err != nil {
		t.Fatalf("valid ordering should parse, got %v", err)
	}
	if cfg.TaskHardTimeoutSeconds != 3600 {
		t.Errorf("TaskHardTimeoutSeconds = %d, want 3600", cfg.TaskHardTimeoutSeconds)
	}

	lifted, err := ParseGatewayConfig(map[string]any{
		"namespace": "test-ns", "gateway_id": "test-gw",
		"task_timeout_seconds": 5000,
	})
	if err != nil {
		t.Fatalf("raising only the soft timeout should not be rejected, got %v", err)
	}
	if lifted.TaskHardTimeoutSeconds != 5000+defaultTaskHardTimeoutSeconds {
		t.Errorf("lifted TaskHardTimeoutSeconds = %d, want %d", lifted.TaskHardTimeoutSeconds, 5000+defaultTaskHardTimeoutSeconds)
	}
}

func TestParseGatewayConfig_AsyncEventLog(t *testing.T) {
	off, err := ParseGatewayConfig(map[string]any{
		"namespace":       "test-ns",
		"gateway_id":      "test-gw",
		"async_event_log": false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if off.AsyncEventLog {
		t.Error("AsyncEventLog should be false when async_event_log: false")
	}

	on, err := ParseGatewayConfig(map[string]any{
		"namespace":       "test-ns",
		"gateway_id":      "test-gw",
		"async_event_log": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !on.AsyncEventLog {
		t.Error("AsyncEventLog should be true when async_event_log: true")
	}
}

func TestParseGatewayConfig_EventLogFlushInterval(t *testing.T) {
	base := map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"}
	unset, err := ParseGatewayConfig(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unset.EventLogFlushInterval != 0 {
		t.Errorf("EventLogFlushInterval = %s with the key absent, want 0 (event log default)", unset.EventLogFlushInterval)
	}

	set, err := ParseGatewayConfig(map[string]any{
		"namespace": "test-ns", "gateway_id": "test-gw", "event_log_flush_interval_ms": 1000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if set.EventLogFlushInterval != time.Second {
		t.Errorf("EventLogFlushInterval = %s, want 1s", set.EventLogFlushInterval)
	}

	for _, v := range []int{0, -5} {
		nonPositive, err := ParseGatewayConfig(map[string]any{
			"namespace": "test-ns", "gateway_id": "test-gw", "event_log_flush_interval_ms": v,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if nonPositive.EventLogFlushInterval != 0 {
			t.Errorf("EventLogFlushInterval = %s with event_log_flush_interval_ms=%d, want 0 (event log default)", nonPositive.EventLogFlushInterval, v)
		}
	}

	if _, err := ParseGatewayConfig(map[string]any{
		"namespace": "test-ns", "gateway_id": "test-gw", "event_log_flush_interval_ms": maxEventLogFlushIntervalMs + 1,
	}); err == nil || !strings.Contains(err.Error(), "event_log_flush_interval_ms") {
		t.Errorf("err = %v, want a rejection of an interval above %dms", err, maxEventLogFlushIntervalMs)
	}
}

func TestParseGatewayConfig_EmbedResolutionAlias(t *testing.T) {
	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":                        "test-ns",
		"gateway_id":                       "test-gw",
		"resolve_artifact_uris_in_gateway": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.EnableEmbedResolution {
		t.Error("resolve_artifact_uris_in_gateway should enable embed resolution")
	}

	cfg2, err := ParseGatewayConfig(map[string]any{
		"namespace":               "test-ns",
		"gateway_id":              "test-gw",
		"enable_embed_resolution": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg2.EnableEmbedResolution {
		t.Error("enable_embed_resolution should enable embed resolution")
	}
}

func TestParseGatewayConfig_OverrideDefaults(t *testing.T) {
	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":          "prod-ns",
		"session_secret_key": "secret123",
		"gateway_id":         "my-gw",
		"fastapi_host":       "0.0.0.0",
		"fastapi_port":       8080,
		"sse_max_queue_size": 500,
		"cors_allowed_origins": []any{
			"http://localhost:3000",
			"http://example.com",
		},
		"default_user_identity":                  "prod_user",
		"force_user_identity":                    "forced_user",
		"frontend_use_authorization":             true,
		"system_purpose":                         "Test purpose",
		"response_format":                        "Markdown",
		"enable_embed_resolution":                true,
		"gateway_artifact_content_limit_bytes":   5_000_000,
		"task_request_ttl_seconds":               120,
		"gateway_max_project_instructions_chars": 8_000,
		"gateway_max_project_description_chars":  2_500,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Namespace != "prod-ns" {
		t.Errorf("Namespace = %q, want %q", cfg.Namespace, "prod-ns")
	}
	if cfg.SessionSecretKey != "secret123" {
		t.Errorf("SessionSecretKey = %q, want %q", cfg.SessionSecretKey, "secret123")
	}
	if cfg.GatewayID != "my-gw" {
		t.Errorf("GatewayID = %q, want %q", cfg.GatewayID, "my-gw")
	}
	if cfg.FastAPIHost != "0.0.0.0" {
		t.Errorf("FastAPIHost = %q, want %q", cfg.FastAPIHost, "0.0.0.0")
	}
	if cfg.FastAPIPort != 8080 {
		t.Errorf("FastAPIPort = %d, want %d", cfg.FastAPIPort, 8080)
	}
	if cfg.SSEMaxQueueSize != 500 {
		t.Errorf("SSEMaxQueueSize = %d, want %d", cfg.SSEMaxQueueSize, 500)
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Errorf("CORSAllowedOrigins length = %d, want 2", len(cfg.CORSAllowedOrigins))
	}
	if cfg.DefaultUserIdentity != "prod_user" {
		t.Errorf("DefaultUserIdentity = %q, want %q", cfg.DefaultUserIdentity, "prod_user")
	}
	if cfg.ForceUserIdentity != "forced_user" {
		t.Errorf("ForceUserIdentity = %q, want %q", cfg.ForceUserIdentity, "forced_user")
	}
	if !cfg.FrontendUseAuthorization {
		t.Error("FrontendUseAuthorization should be true")
	}
	if cfg.SystemPurpose != "Test purpose" {
		t.Errorf("SystemPurpose = %q, want %q", cfg.SystemPurpose, "Test purpose")
	}
	if !cfg.EnableEmbedResolution {
		t.Error("EnableEmbedResolution should be true")
	}
	if cfg.GatewayArtifactContentLimitBytes != 5_000_000 {
		t.Errorf("GatewayArtifactContentLimitBytes = %d, want %d", cfg.GatewayArtifactContentLimitBytes, 5_000_000)
	}
	if cfg.TaskRequestTTLSeconds != 120 {
		t.Errorf("TaskRequestTTLSeconds = %d, want %d", cfg.TaskRequestTTLSeconds, 120)
	}
	if cfg.GatewayMaxProjectInstructionsChars != 8_000 {
		t.Errorf("GatewayMaxProjectInstructionsChars = %d, want %d", cfg.GatewayMaxProjectInstructionsChars, 8_000)
	}
	if cfg.GatewayMaxProjectDescriptionChars != 2_500 {
		t.Errorf("GatewayMaxProjectDescriptionChars = %d, want %d", cfg.GatewayMaxProjectDescriptionChars, 2_500)
	}
}

func TestParseGatewayConfig_ProjectInstructionsInvalidKeepsDefault(t *testing.T) {
	for _, bad := range []any{"lots", -5, true, 0, "0", float64(0), int64(-1)} {
		cfg, err := ParseGatewayConfig(map[string]any{
			"namespace":                              "test-ns",
			"gateway_id":                             "test-gw",
			"gateway_max_project_instructions_chars": bad,
		})
		if err != nil {
			t.Fatalf("value %v: unexpected error: %v", bad, err)
		}
		if cfg.GatewayMaxProjectInstructionsChars != 32_000 {
			t.Errorf("value %v (%T): GatewayMaxProjectInstructionsChars = %d, want default %d",
				bad, bad, cfg.GatewayMaxProjectInstructionsChars, 32_000)
		}
	}
}

func TestParseGatewayConfig_ProjectInstructionsCoercedShapes(t *testing.T) {
	for _, good := range []any{5000, int64(5000), float64(5000), "5000"} {
		cfg, err := ParseGatewayConfig(map[string]any{
			"namespace":                              "test-ns",
			"gateway_id":                             "test-gw",
			"gateway_max_project_instructions_chars": good,
		})
		if err != nil {
			t.Fatalf("value %v: unexpected error: %v", good, err)
		}
		if cfg.GatewayMaxProjectInstructionsChars != 5000 {
			t.Errorf("value %v (%T): GatewayMaxProjectInstructionsChars = %d, want 5000",
				good, good, cfg.GatewayMaxProjectInstructionsChars)
		}
	}
}

func TestParseGatewayConfig_TaskRequestTTLFollowsTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  map[string]any
		want int
	}{
		{name: "overridden hard timeout", cfg: map[string]any{"task_hard_timeout_seconds": 1800}, want: 1800},
		{name: "soft raised past the default hard timeout", cfg: map[string]any{"task_timeout_seconds": 4000}, want: 4000 + 3600},
		{name: "timeouts off", cfg: map[string]any{"task_timeout_seconds": 0}, want: 0},
	} {
		tc.cfg["namespace"], tc.cfg["gateway_id"] = "ns", "gw"
		cfg, err := ParseGatewayConfig(tc.cfg)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if cfg.TaskRequestTTLSeconds != tc.want {
			t.Errorf("%s: TaskRequestTTLSeconds = %d, want %d", tc.name, cfg.TaskRequestTTLSeconds, tc.want)
		}
	}
	var cfg *GatewayConfig
	var err error

	cfg, err = ParseGatewayConfig(map[string]any{
		"namespace":                "ns",
		"gateway_id":               "gw",
		"task_timeout_seconds":     600,
		"task_request_ttl_seconds": 90,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TaskRequestTTLSeconds != 90 {
		t.Errorf("TaskRequestTTLSeconds = %d, want 90 (explicit override)", cfg.TaskRequestTTLSeconds)
	}

	cfg, err = ParseGatewayConfig(map[string]any{
		"namespace":                "ns",
		"gateway_id":               "gw",
		"task_request_ttl_seconds": 0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TaskRequestTTLSeconds != 0 {
		t.Errorf("TaskRequestTTLSeconds = %d, want 0 (explicit disable)", cfg.TaskRequestTTLSeconds)
	}
}

func TestParseGatewayConfig_CardPublishInterval(t *testing.T) {
	base := map[string]any{"namespace": "ns", "gateway_id": "gw"}
	cases := []struct {
		name string
		set  map[string]any
		want int
	}{
		{"default when unset", nil, 60},
		{"nested gateway_card_publishing", map[string]any{"gateway_card_publishing": map[string]any{"interval_seconds": 15}}, 15},
		{"flat card_publish_interval_seconds fallback", map[string]any{"card_publish_interval_seconds": 45}, 45},
		{"nested wins over flat", map[string]any{"gateway_card_publishing": map[string]any{"interval_seconds": 15}, "card_publish_interval_seconds": 45}, 15},
		{"explicit 0 disables periodic", map[string]any{"gateway_card_publishing": map[string]any{"interval_seconds": 0}}, 0},
		{"quoted-string interval", map[string]any{"gateway_card_publishing": map[string]any{"interval_seconds": "15"}}, 15},
		{"quoted-string flat interval", map[string]any{"card_publish_interval_seconds": "45"}, 45},
		{"garbage interval keeps default", map[string]any{"gateway_card_publishing": map[string]any{"interval_seconds": "abc"}}, 60},
		{"nested without interval falls back to flat", map[string]any{"gateway_card_publishing": map[string]any{"enabled": true}, "card_publish_interval_seconds": 45}, 45},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]any{}
			for k, v := range base {
				m[k] = v
			}
			for k, v := range tc.set {
				m[k] = v
			}
			cfg, err := ParseGatewayConfig(m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.CardPublishIntervalSeconds != tc.want {
				t.Errorf("CardPublishIntervalSeconds = %d, want %d", cfg.CardPublishIntervalSeconds, tc.want)
			}
		})
	}
}

func TestParseGatewayConfig_NestedMaps(t *testing.T) {
	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": "sqlite:///test.db",
		},
		"task_logging": map[string]any{
			"enabled": true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SessionServiceRaw == nil {
		t.Error("SessionServiceRaw should not be nil")
	}
	if cfg.TaskLoggingRaw == nil {
		t.Error("TaskLoggingRaw should not be nil")
	}
	if cfg.SessionServiceRaw["type"] != "sql" {
		t.Errorf("SessionServiceRaw[type] = %v, want %q", cfg.SessionServiceRaw["type"], "sql")
	}
}

func TestParseGatewayConfig_FrontendPassthrough(t *testing.T) {
	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":                 "test-ns",
		"gateway_id":                "test-gw",
		"frontend_welcome_message":  "Hello!",
		"frontend_app_name":         "TestBot",
		"frontend_collect_feedback": true,
		"frontend_logo_url":         "https://cdn.example.com/acme.png",
		"frontend_small_logo_url":   "https://cdn.example.com/acme-s.png",
		"frontend_disclaimer_text":  "AI responses may be inaccurate.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.FrontendConfigRaw["frontend_disclaimer_text"] != "AI responses may be inaccurate." {
		t.Errorf("frontend_disclaimer_text = %v, want %q", cfg.FrontendConfigRaw["frontend_disclaimer_text"], "AI responses may be inaccurate.")
	}
	if cfg.FrontendConfigRaw["frontend_welcome_message"] != "Hello!" {
		t.Errorf("frontend_welcome_message = %v, want %q", cfg.FrontendConfigRaw["frontend_welcome_message"], "Hello!")
	}
	if cfg.FrontendConfigRaw["frontend_app_name"] != "TestBot" {
		t.Errorf("frontend_app_name = %v, want %q", cfg.FrontendConfigRaw["frontend_app_name"], "TestBot")
	}
	if cfg.FrontendConfigRaw["frontend_logo_url"] != "https://cdn.example.com/acme.png" {
		t.Errorf("frontend_logo_url = %v, want %q", cfg.FrontendConfigRaw["frontend_logo_url"], "https://cdn.example.com/acme.png")
	}
	if cfg.FrontendConfigRaw["frontend_small_logo_url"] != "https://cdn.example.com/acme-s.png" {
		t.Errorf("frontend_small_logo_url = %v, want %q", cfg.FrontendConfigRaw["frontend_small_logo_url"], "https://cdn.example.com/acme-s.png")
	}
}

type countingWarnHandler struct {
	slog.Handler
	substr string
	count  *int
}

func (h countingWarnHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn && strings.Contains(r.Message, h.substr) {
		*h.count++
	}
	return nil
}

func (h countingWarnHandler) Enabled(context.Context, slog.Level) bool { return true }

func TestParseGatewayConfig_BotNameDeprecatedAlias(t *testing.T) {
	warns := 0
	prev := slog.Default()
	slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "frontend_bot_name", count: &warns}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":         "test-ns",
		"gateway_id":        "test-gw",
		"frontend_bot_name": "LegacyBot",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FrontendConfigRaw["frontend_app_name"] != "LegacyBot" {
		t.Errorf("frontend_app_name = %v, want alias value %q", cfg.FrontendConfigRaw["frontend_app_name"], "LegacyBot")
	}
	if warns != 1 {
		t.Errorf("deprecation warning fired %d times at load, want exactly 1", warns)
	}
}

func TestParseGatewayConfig_AppNameWinsOverDeprecatedAlias(t *testing.T) {
	warns := 0
	prev := slog.Default()
	slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "frontend_bot_name", count: &warns}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":         "test-ns",
		"gateway_id":        "test-gw",
		"frontend_app_name": "NewName",
		"frontend_bot_name": "LegacyBot",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FrontendConfigRaw["frontend_app_name"] != "NewName" {
		t.Errorf("frontend_app_name = %v, want %q (new key must win over the alias)", cfg.FrontendConfigRaw["frontend_app_name"], "NewName")
	}
	if warns != 1 {
		t.Errorf("deprecation warning fired %d times at load, want exactly 1 (both keys present is still one load, one warning)", warns)
	}
}

func TestParseGatewayConfig_FeedbackPublishingTopicDeprecated(t *testing.T) {
	warns := 0
	prev := slog.Default()
	slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "feedback_publishing.topic", count: &warns}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"feedback_publishing": map[string]any{
			"enabled": true,
			"topic":   "custom/legacy/topic",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FeedbackPublishingRaw["enabled"] != true {
		t.Errorf("feedback_publishing.enabled = %v, want true", cfg.FeedbackPublishingRaw["enabled"])
	}
	fallback := WebuiSettingsFallbackFromConfig(cfg.FrontendConfigRaw, cfg.FeedbackPublishingRaw)
	if fallback.PublishFeedback == nil || *fallback.PublishFeedback != true {
		t.Errorf("PublishFeedback fallback = %v, want true", fallback.PublishFeedback)
	}
	if got, want := a2a.FeedbackTopic(cfg.Namespace), "custom/legacy/topic"; got == want {
		t.Fatalf("test setup invalid: fixed feedback topic %q must differ from the legacy value under test", got)
	}
	if warns != 1 {
		t.Errorf("deprecation warning fired %d times at load, want exactly 1", warns)
	}
}

func TestParseGatewayConfig_FeedbackPublishingWithoutTopicNoWarning(t *testing.T) {
	warns := 0
	prev := slog.Default()
	slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "feedback_publishing.topic", count: &warns}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"feedback_publishing": map[string]any{
			"enabled": true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FeedbackPublishingRaw["enabled"] != true {
		t.Errorf("feedback_publishing.enabled = %v, want true", cfg.FeedbackPublishingRaw["enabled"])
	}
	if warns != 0 {
		t.Errorf("deprecation warning fired %d times at load, want 0 when topic is absent", warns)
	}
}

func TestFeedbackPublishing_IncludeTaskInfoLevels(t *testing.T) {
	for _, level := range webuisettings.ValidFeedbackTaskInfoLevels() {
		t.Run(level, func(t *testing.T) {
			raw := map[string]any{"include_task_info": level}
			fallback := WebuiSettingsFallbackFromConfig(nil, raw)
			if fallback.FeedbackTaskInfoLevel == nil || *fallback.FeedbackTaskInfoLevel != level {
				t.Errorf("FeedbackTaskInfoLevel = %v, want %q", fallback.FeedbackTaskInfoLevel, level)
			}
		})
	}
}

func TestFeedbackPublishing_IncludeTaskInfoUnknownWarnsAndDrops(t *testing.T) {
	warns := 0
	prev := slog.Default()
	slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "not a valid level", count: &warns}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	raw := map[string]any{"include_task_info": "sitm"}
	fallback := WebuiSettingsFallbackFromConfig(nil, raw)
	if fallback.FeedbackTaskInfoLevel != nil {
		t.Errorf("FeedbackTaskInfoLevel = %v, want nil for unknown value", fallback.FeedbackTaskInfoLevel)
	}
	if warns != 1 {
		t.Errorf("warning fired %d times, want 1", warns)
	}
}

func TestFeedbackPublishing_IncludeTaskInfoWrongTypeWarnsAndDrops(t *testing.T) {
	warns := 0
	prev := slog.Default()
	slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "is not a string", count: &warns}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	raw := map[string]any{"include_task_info": true}
	fallback := WebuiSettingsFallbackFromConfig(nil, raw)
	if fallback.FeedbackTaskInfoLevel != nil {
		t.Errorf("FeedbackTaskInfoLevel = %v, want nil for wrong type", fallback.FeedbackTaskInfoLevel)
	}
	if warns != 1 {
		t.Errorf("wrong-type warning fired %d times, want 1", warns)
	}
}

func TestCfgRawInt64Ptr_WidensIntToInt64(t *testing.T) {
	raw := map[string]any{"max_payload_size_bytes": 1_500_000}
	got := cfgRawInt64Ptr(raw, "max_payload_size_bytes", "feedback_publishing")
	if got == nil || *got != int64(1_500_000) {
		t.Errorf("value = %v, want 1500000", got)
	}
}

func TestCfgRawInt64Ptr_AcceptsWholeFloat64(t *testing.T) {
	raw := map[string]any{"max_payload_size_bytes": float64(1_500_000)}
	got := cfgRawInt64Ptr(raw, "max_payload_size_bytes", "feedback_publishing")
	if got == nil || *got != int64(1_500_000) {
		t.Errorf("value = %v, want 1500000", got)
	}
}

func TestCfgRawInt64Ptr_NonIntegerFloat64Drops(t *testing.T) {
	warns := 0
	prev := slog.Default()
	slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "must be a whole number", count: &warns}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	raw := map[string]any{"max_payload_size_bytes": 1.5}
	got := cfgRawInt64Ptr(raw, "max_payload_size_bytes", "feedback_publishing")
	if got != nil {
		t.Errorf("value = %v, want nil for non-integer float", got)
	}
	if warns != 1 {
		t.Errorf("warning fired %d times, want 1", warns)
	}
}

func TestCfgRawInt64Ptr_NonPositiveDrops(t *testing.T) {
	for _, bad := range []int{0, -1} {
		warns := 0
		prev := slog.Default()
		slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "must be a positive integer", count: &warns}))

		got := cfgRawInt64Ptr(map[string]any{"max_payload_size_bytes": bad}, "max_payload_size_bytes", "feedback_publishing")
		if got != nil {
			t.Errorf("value %d: got = %v, want nil", bad, got)
		}
		if warns != 1 {
			t.Errorf("value %d: warning fired %d times, want 1", bad, warns)
		}

		slog.SetDefault(prev)
	}
}

func TestCfgRawInt64Ptr_WrongTypeDrops(t *testing.T) {
	warns := 0
	prev := slog.Default()
	slog.SetDefault(slog.New(countingWarnHandler{Handler: slog.DiscardHandler, substr: "is not an integer", count: &warns}))
	t.Cleanup(func() { slog.SetDefault(prev) })

	got := cfgRawInt64Ptr(map[string]any{"max_payload_size_bytes": "nine million"}, "max_payload_size_bytes", "feedback_publishing")
	if got != nil {
		t.Errorf("got = %v, want nil for wrong type", got)
	}
	if warns != 1 {
		t.Errorf("wrong-type warning fired %d times, want 1", warns)
	}
}

func TestFeedbackPublishing_AbsentBlockLeavesLevelNil(t *testing.T) {
	fallback := WebuiSettingsFallbackFromConfig(nil, nil)
	if fallback.FeedbackTaskInfoLevel != nil {
		t.Errorf("FeedbackTaskInfoLevel = %v, want nil when block absent", fallback.FeedbackTaskInfoLevel)
	}
	eff := webuisettings.Resolve(nil, fallback)
	if eff.FeedbackTaskInfoLevel != webuisettings.DefaultFeedbackTaskInfoLevel {
		t.Errorf("Effective.FeedbackTaskInfoLevel = %q, want %q", eff.FeedbackTaskInfoLevel, webuisettings.DefaultFeedbackTaskInfoLevel)
	}
}

func TestParseGatewayConfig_DisclaimerTruncation(t *testing.T) {
	long := strings.Repeat("é", maxFrontendDisclaimerTextLen+50)
	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":                "test-ns",
		"gateway_id":               "test-gw",
		"frontend_disclaimer_text": long,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := cfg.FrontendConfigRaw["frontend_disclaimer_text"].(string)
	if n := len([]rune(got)); n != maxFrontendDisclaimerTextLen {
		t.Errorf("truncated disclaimer length = %d runes, want %d", n, maxFrontendDisclaimerTextLen)
	}
	if !utf8.ValidString(got) {
		t.Error("truncated disclaimer is not valid UTF-8")
	}
}

func TestParseGatewayConfig_PlatformServiceURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		wantURL string
	}{
		{"section absent", map[string]any{"namespace": "n", "gateway_id": "g"}, ""},
		{"section present, no url", map[string]any{"namespace": "n", "gateway_id": "g", "platform_service": map[string]any{}}, ""},
		{"url set", map[string]any{"namespace": "n", "gateway_id": "g", "platform_service": map[string]any{"url": "https://platform.example"}}, "https://platform.example"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseGatewayConfig(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.PlatformServiceURL != tc.wantURL {
				t.Errorf("PlatformServiceURL = %q, want %q", cfg.PlatformServiceURL, tc.wantURL)
			}
		})
	}
}

func TestParseGatewayConfig_ProjectDescriptionInvalidKeepsDefault(t *testing.T) {
	for _, bad := range []any{"lots", -5, true, 0, "0", float64(0), int64(-1)} {
		cfg, err := ParseGatewayConfig(map[string]any{
			"namespace":                             "test-ns",
			"gateway_id":                            "test-gw",
			"gateway_max_project_description_chars": bad,
		})
		if err != nil {
			t.Fatalf("value %v: unexpected error: %v", bad, err)
		}
		if cfg.GatewayMaxProjectDescriptionChars != 1000 {
			t.Errorf("value %v (%T): GatewayMaxProjectDescriptionChars = %d, want default %d",
				bad, bad, cfg.GatewayMaxProjectDescriptionChars, 1000)
		}
	}
}

func TestParseGatewayConfig_ProjectDescriptionCoercedShapes(t *testing.T) {
	for _, good := range []any{400, int64(400), float64(400), "400"} {
		cfg, err := ParseGatewayConfig(map[string]any{
			"namespace":                             "test-ns",
			"gateway_id":                            "test-gw",
			"gateway_max_project_description_chars": good,
		})
		if err != nil {
			t.Fatalf("value %v: unexpected error: %v", good, err)
		}
		if cfg.GatewayMaxProjectDescriptionChars != 400 {
			t.Errorf("value %v (%T): GatewayMaxProjectDescriptionChars = %d, want 400",
				good, good, cfg.GatewayMaxProjectDescriptionChars)
		}
	}
}
