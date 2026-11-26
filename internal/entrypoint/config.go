// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type GatewayConfig struct {
	Namespace string

	SessionSecretKey string

	GatewayID string

	GatewayUUID string

	RunAsUserUUIDs map[string]string

	FastAPIHost string

	FastAPIPort int

	CORSAllowedOrigins []string

	CORSAllowedOriginRegex string

	SSEMaxQueueSize int

	DefaultUserIdentity string

	DefaultUserTimezone string

	ForceUserIdentity string

	FrontendUseAuthorization bool

	ExternalAuthServiceURL string

	ExternalAuthProvider string

	ExternalAuthClaimKey string

	ExternalAuthCallbackURI string

	FrontendAuthLoginURL string

	FrontendRedirectURL string

	FrontendServerURL string

	AllowHostPathVolumes bool

	RequireContainerMountForLinked bool

	SystemPurpose string

	ResponseFormat string

	EventWorkers int

	AsyncEventLog bool

	EventLogFlushInterval time.Duration

	EnableEmbedResolution bool

	GatewayArtifactContentLimitBytes int

	CardPublishIntervalSeconds int

	DatabaseURL string

	PlatformServiceURL string

	SessionServiceRaw       map[string]any
	ArtifactServiceRaw      map[string]any
	TaskLoggingRaw          map[string]any
	BackgroundTasksRaw      map[string]any
	FeedbackPublishingRaw   map[string]any
	FrontendConfigRaw       map[string]any
	SpeechRaw               map[string]any
	AuthorizationServiceRaw map[string]any
	SAMAccessTokenRaw       map[string]any

	ModelRaw any

	TaskLoggerQueueSize int

	GatewayMaxUploadSizeBytes int

	GatewayMaxProjectSizeBytes int

	GatewayMaxBatchUploadSizeBytes int

	GatewayMaxProjectDescriptionChars int

	GatewayMaxProjectInstructionsChars int

	TaskTimeoutSeconds int

	TaskHardTimeoutSeconds int

	TaskRequestTTLSeconds int

	FastAPIHTTPSPort int

	SSLKeyFile string

	SSLCertFile string

	SSLKeyFilePassword string

	DataRetentionRaw map[string]any

	SchedulerServiceRaw map[string]any

	SentinelMonitorRaw map[string]any

	Capabilities map[string]bool
}

func authzEnforcing(rawType string, frontendUseAuth bool) bool {
	if rawType != "" {
		return rawType == "default_rbac" || rawType == "deny_all"
	}
	return frontendUseAuth
}

func (c *GatewayConfig) AuthzEnforcing() bool {
	t, _ := c.AuthorizationServiceRaw["type"].(string)
	return authzEnforcing(t, c.FrontendUseAuthorization)
}

func AppConfigAuthzEnforcing(appConfig map[string]any) bool {
	t, _ := getMap(appConfig, "authorization_service")["type"].(string)
	return authzEnforcing(t, getBool(appConfig, "frontend_use_authorization"))
}

const maxFrontendDisclaimerTextLen = 800

const maxEventLogFlushIntervalMs = 60_000

const (
	pythonParityTaskLoggerQueueSize = 600
	pythonParityMaxUploadBytes      = 50_000_000
)

const defaultMaxProjectDescriptionChars = 1000

const defaultMaxProjectInstructionsChars = 32_000

func parseDefaultUserTimezone(v, fallback string) string {
	if v == "" {
		return fallback
	}
	if tz, ok := NormalizeUserTimezone(v); ok {
		return tz
	}
	slog.Default().Warn("ignoring invalid default_user_timezone; keeping default", "value", v, "default", fallback)
	return fallback
}

func taskRequestTTLSeconds(m map[string]any, cfg *GatewayConfig) int {
	if _, hasTTL := m["task_request_ttl_seconds"]; hasTTL {
		return getInt(m, "task_request_ttl_seconds")
	}
	if cfg.TaskTimeoutSeconds > 0 && cfg.TaskHardTimeoutSeconds > 0 {
		return cfg.TaskHardTimeoutSeconds
	}
	return cfg.TaskTimeoutSeconds
}

func ParseGatewayConfig(m map[string]any) (*GatewayConfig, error) {
	cfg := &GatewayConfig{
		FastAPIHost:                        "127.0.0.1",
		FastAPIPort:                        8800,
		SSEMaxQueueSize:                    200,
		DefaultUserIdentity:                "",
		DefaultUserTimezone:                "UTC",
		ExternalAuthProvider:               "generic",
		AsyncEventLog:                      true,
		EnableEmbedResolution:              true,
		GatewayArtifactContentLimitBytes:   10_000_000,
		CardPublishIntervalSeconds:         config.DefaultCardPublishIntervalSecs,
		TaskLoggerQueueSize:                pythonParityTaskLoggerQueueSize,
		GatewayMaxUploadSizeBytes:          pythonParityMaxUploadBytes,
		GatewayMaxProjectSizeBytes:         pythonParityMaxUploadBytes,
		GatewayMaxBatchUploadSizeBytes:     pythonParityMaxUploadBytes,
		GatewayMaxProjectDescriptionChars:  defaultMaxProjectDescriptionChars,
		GatewayMaxProjectInstructionsChars: defaultMaxProjectInstructionsChars,
		TaskTimeoutSeconds:                 defaultTaskTimeoutSeconds,
		TaskHardTimeoutSeconds:             defaultTaskHardTimeoutSeconds,
	}

	cfg.Namespace = strings.TrimRight(getString(m, "namespace"), "/")
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("gateway config: 'namespace' is required")
	}
	if err := a2a.ValidateNamespace(cfg.Namespace); err != nil {
		return nil, fmt.Errorf("gateway config: %w", err)
	}

	cfg.SessionSecretKey = getString(m, "session_secret_key")

	cfg.GatewayID = getString(m, "gateway_id")
	if cfg.GatewayID == "" {
		return nil, fmt.Errorf("gateway config: 'gateway_id' is required")
	}
	if cfg.GatewayID == a2a.EvalGatewayID {
		return nil, fmt.Errorf("gateway config: 'gateway_id' %q is reserved for eval run replies", cfg.GatewayID)
	}
	cfg.parseSpendRows(m)

	if v := getString(m, "fastapi_host"); v != "" {
		cfg.FastAPIHost = v
	}
	if _, hasPort := m["fastapi_port"]; hasPort {
		cfg.FastAPIPort = getInt(m, "fastapi_port")
	}
	cfg.CORSAllowedOrigins = getStringSlice(m, "cors_allowed_origins")
	if v := getString(m, "cors_allowed_origin_regex"); v != "" {
		cfg.CORSAllowedOriginRegex = v
	}

	if v := getInt(m, "sse_max_queue_size"); v != 0 {
		cfg.SSEMaxQueueSize = v
	}

	if v := getString(m, "default_user_identity"); v != "" {
		cfg.DefaultUserIdentity = v
	}

	cfg.DefaultUserTimezone = parseDefaultUserTimezone(getString(m, "default_user_timezone"), cfg.DefaultUserTimezone)
	cfg.ForceUserIdentity = getString(m, "force_user_identity")

	cfg.FrontendUseAuthorization = getBool(m, "frontend_use_authorization")
	cfg.ExternalAuthServiceURL = getString(m, "external_auth_service_url")
	if v := getString(m, "external_auth_provider"); v != "" {
		cfg.ExternalAuthProvider = v
	}
	cfg.ExternalAuthClaimKey = getString(m, "external_auth_claim_key")
	cfg.ExternalAuthCallbackURI = getString(m, "external_auth_callback_uri")
	cfg.FrontendAuthLoginURL = getString(m, "frontend_auth_login_url")
	cfg.FrontendRedirectURL = getString(m, "frontend_redirect_url")
	cfg.FrontendServerURL = getString(m, "frontend_server_url")
	cfg.AllowHostPathVolumes = getBool(m, "volumes_allow_host_paths")
	cfg.RequireContainerMountForLinked = getBool(m, "volumes_require_container_mount_for_linked")
	if _, ok := m["async_event_log"]; ok {
		cfg.AsyncEventLog = getBool(m, "async_event_log")
	}
	if v := getInt(m, "event_log_flush_interval_ms"); v > maxEventLogFlushIntervalMs {
		return nil, fmt.Errorf("gateway config: 'event_log_flush_interval_ms' (%d) must be at most %d", v, maxEventLogFlushIntervalMs)
	} else if v > 0 {
		cfg.EventLogFlushInterval = time.Duration(v) * time.Millisecond
	}
	if _, ok := m["event_workers"]; ok {
		cfg.EventWorkers = getInt(m, "event_workers")
	}

	cfg.SystemPurpose = getString(m, "system_purpose")
	cfg.ResponseFormat = getString(m, "response_format")

	if _, ok := m["enable_embed_resolution"]; ok {
		cfg.EnableEmbedResolution = getBool(m, "enable_embed_resolution")
	} else if _, ok := m["resolve_artifact_uris_in_gateway"]; ok {
		cfg.EnableEmbedResolution = getBool(m, "resolve_artifact_uris_in_gateway")
	}
	if v := getInt(m, "gateway_artifact_content_limit_bytes"); v != 0 {
		cfg.GatewayArtifactContentLimitBytes = v
	}

	if ps := getMap(m, "platform_service"); ps != nil {
		cfg.PlatformServiceURL = getString(ps, "url")
	}

	cfg.SessionServiceRaw = getMap(m, "session_service")
	cfg.ArtifactServiceRaw = getMap(m, "artifact_service")
	cfg.TaskLoggingRaw = getMap(m, "task_logging")
	cfg.BackgroundTasksRaw = getMap(m, "background_tasks")
	cfg.FeedbackPublishingRaw = getMap(m, "feedback_publishing")
	if _, ok := cfg.FeedbackPublishingRaw["topic"]; ok {
		slog.Default().Warn(
			"feedback_publishing.topic is no longer supported; feedback events publish to the fixed namespaced topic and the configured value is ignored",
			"topic", a2a.FeedbackTopic(cfg.Namespace),
		)
	}
	if ffe := getMap(m, "frontend_feature_enablement"); len(ffe) > 0 {
		keys := make([]string, 0, len(ffe))
		for k := range ffe {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		slog.Default().Warn(
			"frontend_feature_enablement YAML block is no longer supported; "+
				"feature flag state is controlled by SAM_FEATURE_<KEY> env vars only — listed keys ignored",
			"keys", keys,
		)
	}
	cfg.SpeechRaw = getMap(m, "speech")
	cfg.AuthorizationServiceRaw = getMap(m, "authorization_service")
	cfg.SAMAccessTokenRaw = getMap(m, "sam_access_token")
	cfg.ModelRaw = m["model"]

	if v := getInt(m, "task_logger_queue_size"); v != 0 {
		cfg.TaskLoggerQueueSize = v
	}

	if v := getInt(m, "gateway_max_upload_size_bytes"); v != 0 {
		cfg.GatewayMaxUploadSizeBytes = v
	}
	if v := getInt(m, "gateway_max_project_size_bytes"); v != 0 {
		cfg.GatewayMaxProjectSizeBytes = v
	}
	if v := getInt(m, "gateway_max_batch_upload_size_bytes"); v != 0 {
		cfg.GatewayMaxBatchUploadSizeBytes = v
	}
	if raw, present := m["gateway_max_project_description_chars"]; present {
		if v, ok := asInt(raw); ok && v > 0 {
			cfg.GatewayMaxProjectDescriptionChars = v
		} else {
			slog.Default().Warn("ignoring invalid gateway_max_project_description_chars; keeping default",
				"value", raw, "default", cfg.GatewayMaxProjectDescriptionChars)
		}
	}
	if raw, present := m["gateway_max_project_instructions_chars"]; present {
		if v, ok := asInt(raw); ok && v > 0 {
			cfg.GatewayMaxProjectInstructionsChars = v
		} else {
			slog.Default().Warn("ignoring invalid gateway_max_project_instructions_chars; keeping default",
				"value", raw, "default", cfg.GatewayMaxProjectInstructionsChars)
		}
	}

	cfg.CardPublishIntervalSeconds = resolveCardPublishInterval(m, cfg.CardPublishIntervalSeconds)

	if rawCaps := getMap(m, "gateway_capabilities"); len(rawCaps) > 0 {
		cfg.Capabilities = make(map[string]bool, len(rawCaps))
		for k, v := range rawCaps {
			if b, ok := v.(bool); ok {
				cfg.Capabilities[k] = b
			}
		}
	}

	if _, hasTimeout := m["task_timeout_seconds"]; hasTimeout {
		cfg.TaskTimeoutSeconds = getInt(m, "task_timeout_seconds")
	}
	hardExplicit := false
	if _, ok := m["task_hard_timeout_seconds"]; ok {
		cfg.TaskHardTimeoutSeconds = getInt(m, "task_hard_timeout_seconds")
		hardExplicit = true
	}
	if cfg.TaskTimeoutSeconds > 0 && cfg.TaskHardTimeoutSeconds > 0 && cfg.TaskHardTimeoutSeconds <= cfg.TaskTimeoutSeconds {
		if hardExplicit {
			return nil, fmt.Errorf("gateway config: 'task_hard_timeout_seconds' (%d) must be greater than 'task_timeout_seconds' (%d)", cfg.TaskHardTimeoutSeconds, cfg.TaskTimeoutSeconds)
		}
		cfg.TaskHardTimeoutSeconds = cfg.TaskTimeoutSeconds + defaultTaskHardTimeoutSeconds
	}
	cfg.TaskRequestTTLSeconds = taskRequestTTLSeconds(m, cfg)

	cfg.FastAPIHTTPSPort = getInt(m, "fastapi_https_port")
	cfg.SSLKeyFile = getString(m, "ssl_keyfile")
	cfg.SSLCertFile = getString(m, "ssl_certfile")
	cfg.SSLKeyFilePassword = getString(m, "ssl_keyfile_password")

	cfg.DataRetentionRaw = getMap(m, "data_retention")

	cfg.SchedulerServiceRaw = getMap(m, "scheduler_service")
	cfg.SentinelMonitorRaw = getMap(m, "observability_monitor")

	cfg.DatabaseURL = sessionServiceDatabaseURL(cfg.SessionServiceRaw)

	cfg.FrontendConfigRaw = parseFrontendConfigRaw(m)

	return cfg, nil
}

func parseFrontendConfigRaw(m map[string]any) map[string]any {
	raw := make(map[string]any)
	for _, key := range []string{
		"frontend_welcome_message",
		"frontend_app_name",
		"frontend_collect_feedback",
		"frontend_logo_url",
		"frontend_small_logo_url",
		"frontend_disclaimer_text",
	} {
		if v, ok := m[key]; ok {
			raw[key] = v
		}
	}
	if v, ok := m["frontend_bot_name"]; ok {
		if _, hasNew := raw["frontend_app_name"]; !hasNew {
			raw["frontend_app_name"] = v
		}
		slog.Default().Warn("'frontend_bot_name' is deprecated; rename it to 'frontend_app_name'")
	}
	if disclaimer, ok := raw["frontend_disclaimer_text"].(string); ok {
		if runes := []rune(disclaimer); len(runes) > maxFrontendDisclaimerTextLen {
			slog.Default().Warn(
				"frontend_disclaimer_text exceeds the maximum length; truncating",
				"max", maxFrontendDisclaimerTextLen,
				"length", len(runes),
			)
			raw["frontend_disclaimer_text"] = string(runes[:maxFrontendDisclaimerTextLen])
		}
	}
	return raw
}

func resolveCardPublishInterval(m map[string]any, def int) int {
	if pub := getMap(m, "gateway_card_publishing"); pub != nil {
		if raw, present := pub["interval_seconds"]; present {
			if n, ok := asInt(raw); ok {
				return n
			}
			slog.Default().Warn("ignoring non-numeric gateway_card_publishing.interval_seconds; keeping default",
				"value", raw, "default", def)
		}
	}
	if raw, present := m["card_publish_interval_seconds"]; present {
		if n, ok := asInt(raw); ok {
			return n
		}
		slog.Default().Warn("ignoring non-numeric card_publish_interval_seconds; keeping default",
			"value", raw, "default", def)
	}
	return def
}

func sessionServiceDatabaseURL(svc map[string]any) string {
	if svc == nil {
		return ""
	}
	if t, _ := svc["type"].(string); t != "sql" {
		return ""
	}
	url, _ := svc["database_url"].(string)
	return url
}

type ScheduledTaskServiceConfig struct {
	Enabled                           bool
	DefaultTimeoutSeconds             int
	RetryDelaySeconds                 int
	ExecutionHistoryKeepCount         int
	MisfireGraceTimeSeconds           int
	AutoPauseAfterConsecutiveFailures int
}

func ParseScheduledTaskServiceConfig(m map[string]any) *ScheduledTaskServiceConfig {
	cfg := &ScheduledTaskServiceConfig{
		Enabled:                           true,
		DefaultTimeoutSeconds:             3600,
		RetryDelaySeconds:                 60,
		ExecutionHistoryKeepCount:         100,
		MisfireGraceTimeSeconds:           600,
		AutoPauseAfterConsecutiveFailures: 10,
	}
	if m == nil {
		return cfg
	}
	if v, ok := m["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	if v := getInt(m, "default_timeout_seconds"); v > 0 {
		cfg.DefaultTimeoutSeconds = v
	}
	if v := getInt(m, "retry_delay_seconds"); v > 0 {
		cfg.RetryDelaySeconds = v
	}
	if v := getInt(m, "execution_history_keep_count"); v > 0 {
		cfg.ExecutionHistoryKeepCount = v
	}
	if v := getInt(m, "misfire_grace_time_seconds"); v > 0 {
		cfg.MisfireGraceTimeSeconds = v
	}
	if _, ok := m["auto_pause_after_consecutive_failures"]; ok {
		cfg.AutoPauseAfterConsecutiveFailures = getInt(m, "auto_pause_after_consecutive_failures")
	}
	return cfg
}

func (c *GatewayConfig) parseSpendRows(m map[string]any) {
	c.GatewayUUID = getString(m, gatewayUUIDKey)
	if c.GatewayUUID != "" && !idgen.IsCanonical(c.GatewayUUID) {
		slog.Default().Warn("ignoring gateway_uuid that is not a canonical UUID; spend books the gateway name",
			"gatewayID", c.GatewayID)
		c.GatewayUUID = ""
	}
	var ignoredRunAsUsers int
	c.RunAsUserUUIDs, ignoredRunAsUsers = parseRunAsUserUUIDs(getMap(m, runAsUserUUIDsKey))
	if ignoredRunAsUsers > 0 {
		slog.Default().Warn("ignoring run_as_user_uuids entries that do not map a system principal to a canonical UUID; spend books those run_as users by name",
			"gatewayID", c.GatewayID, "ignored", ignoredRunAsUsers)
	}
}

const runAsUserUUIDsKey = "run_as_user_uuids"

func parseRunAsUserUUIDs(raw map[string]any) (uuids map[string]string, ignored int) {
	uuids = make(map[string]string, len(raw))
	for principal, v := range raw {
		if id, ok := v.(string); ok && idgen.IsCanonical(id) && rbac.IsSystemPrincipal(principal) {
			uuids[principal] = id
			continue
		}
		ignored++
	}
	return uuids, ignored
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func getInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	}
	return 0
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil
	}
	return 0, false
}

func getBool(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func getMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func getStringSlice(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
