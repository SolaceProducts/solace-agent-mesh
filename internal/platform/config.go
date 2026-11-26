// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

type PlatformConfig struct {
	Namespace string

	FastAPIHost string

	FastAPIPort int

	FastAPIHTTPSPort int

	SSLCertFile string

	SSLKeyFile string

	SSLKeyFilePassword string

	DatabaseURL string

	CORSAllowedOrigins []string

	CORSAllowedOriginRegex string

	HealthCheckIntervalSeconds int

	HealthCheckTTLSeconds int

	AgentInstructionMinChars int

	AgentInstructionMaxChars int

	FrontendUseAuthorization bool

	ExternalAuthServiceURL string

	ExternalAuthProvider string

	MaxMessageSizeBytes int

	MaxUploadBytes int64

	ResourceStoreRaw map[string]any

	STRStoreRaw map[string]any

	ArtifactServiceRaw map[string]any

	IngressDomain string

	AgentMeshID string

	SeedBuiltinAgents bool
}

const (
	defaultAgentInstructionMinChars = 1

	defaultAgentInstructionMaxChars = 32_000

	DefaultMaxUploadBytes = 100 << 20
)

func resolvePositiveInt(m map[string]any, key string, def int) int {
	raw, present := m[key]
	if !present {
		return def
	}
	if v, ok := coerceInt(raw); ok && v > 0 {
		return v
	}
	slog.Default().Warn("ignoring invalid platform config value; keeping default",
		"key", key, "value", raw, "default", def)
	return def
}

func coerceInt(v any) (int, bool) {
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

func ParsePlatformConfig(m map[string]any) (*PlatformConfig, error) {
	cfg := &PlatformConfig{
		FastAPIHost:                "127.0.0.1",
		FastAPIPort:                8001,
		FastAPIHTTPSPort:           8444,
		HealthCheckIntervalSeconds: 30,
		HealthCheckTTLSeconds:      90,
		AgentInstructionMinChars:   defaultAgentInstructionMinChars,
		AgentInstructionMaxChars:   defaultAgentInstructionMaxChars,
		ExternalAuthProvider:       "generic",
		MaxMessageSizeBytes:        10_000_000,
		MaxUploadBytes:             DefaultMaxUploadBytes,
	}

	cfg.Namespace = getString(m, "namespace")
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("platform config: 'namespace' is required")
	}

	if v := getString(m, "fastapi_host"); v != "" {
		cfg.FastAPIHost = v
	}
	if _, hasPort := m["fastapi_port"]; hasPort {
		cfg.FastAPIPort = getInt(m, "fastapi_port")
	}
	if _, hasHTTPSPort := m["fastapi_https_port"]; hasHTTPSPort {
		cfg.FastAPIHTTPSPort = getInt(m, "fastapi_https_port")
	}

	cfg.SSLCertFile = getString(m, "ssl_certfile")
	cfg.SSLKeyFile = getString(m, "ssl_keyfile")
	cfg.SSLKeyFilePassword = getString(m, "ssl_keyfile_password")

	cfg.DatabaseURL = getString(m, "database_url")
	cfg.CORSAllowedOrigins = getStringSlice(m, "cors_allowed_origins")
	cfg.CORSAllowedOriginRegex = getString(m, "cors_allowed_origin_regex")

	if v := getInt(m, "health_check_interval_seconds"); v != 0 {
		cfg.HealthCheckIntervalSeconds = v
	}
	if v := getInt(m, "health_check_ttl_seconds"); v != 0 {
		cfg.HealthCheckTTLSeconds = v
	}
	cfg.AgentInstructionMinChars = resolvePositiveInt(m, "agent_instruction_min_chars", cfg.AgentInstructionMinChars)
	cfg.AgentInstructionMaxChars = resolvePositiveInt(m, "agent_instruction_max_chars", cfg.AgentInstructionMaxChars)
	if cfg.AgentInstructionMinChars > cfg.AgentInstructionMaxChars {
		return nil, fmt.Errorf("platform config: 'agent_instruction_min_chars' (%d) must not exceed 'agent_instruction_max_chars' (%d)",
			cfg.AgentInstructionMinChars, cfg.AgentInstructionMaxChars)
	}

	cfg.FrontendUseAuthorization = getBool(m, "frontend_use_authorization")
	cfg.ExternalAuthServiceURL = getString(m, "external_auth_service_url")
	if v := getString(m, "external_auth_provider"); v != "" {
		cfg.ExternalAuthProvider = v
	}

	if v := getInt(m, "max_message_size_bytes"); v != 0 {
		cfg.MaxMessageSizeBytes = v
	}
	cfg.MaxUploadBytes = int64(resolvePositiveInt(m, "max_upload_bytes", int(cfg.MaxUploadBytes)))
	cfg.ResourceStoreRaw = getMap(m, "resource_store")
	cfg.STRStoreRaw = getMap(m, "str_store")
	cfg.ArtifactServiceRaw = getMap(m, "artifact_service")

	cfg.IngressDomain = getString(m, "ingress_domain")
	cfg.AgentMeshID = getString(m, "agent_mesh_id")

	cfg.SeedBuiltinAgents = true
	if v, ok := m["seed_builtin_agents"].(bool); ok {
		cfg.SeedBuiltinAgents = v
	}

	return cfg, nil
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
