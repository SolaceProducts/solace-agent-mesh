// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParsePlatformConfig_Defaults(t *testing.T) {
	m := map[string]any{
		"namespace": "test-ns",
	}

	cfg, err := ParsePlatformConfig(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Namespace != "test-ns" {
		t.Errorf("Namespace = %q, want %q", cfg.Namespace, "test-ns")
	}
	if cfg.FastAPIHost != "127.0.0.1" {
		t.Errorf("FastAPIHost = %q, want %q", cfg.FastAPIHost, "127.0.0.1")
	}
	if cfg.FastAPIPort != 8001 {
		t.Errorf("FastAPIPort = %d, want %d", cfg.FastAPIPort, 8001)
	}
	if cfg.HealthCheckIntervalSeconds != 30 {
		t.Errorf("HealthCheckIntervalSeconds = %d, want %d", cfg.HealthCheckIntervalSeconds, 30)
	}
	if cfg.HealthCheckTTLSeconds != 90 {
		t.Errorf("HealthCheckTTLSeconds = %d, want %d", cfg.HealthCheckTTLSeconds, 90)
	}
	if cfg.ExternalAuthProvider != "generic" {
		t.Errorf("ExternalAuthProvider = %q, want %q", cfg.ExternalAuthProvider, "generic")
	}
	if cfg.FastAPIHTTPSPort != 8444 {
		t.Errorf("FastAPIHTTPSPort = %d, want %d", cfg.FastAPIHTTPSPort, 8444)
	}
	if cfg.SSLCertFile != "" || cfg.SSLKeyFile != "" || cfg.SSLKeyFilePassword != "" {
		t.Errorf("SSL fields should default to empty, got cert=%q key=%q password=%q",
			cfg.SSLCertFile, cfg.SSLKeyFile, cfg.SSLKeyFilePassword)
	}
	if !cfg.SeedBuiltinAgents {
		t.Error("SeedBuiltinAgents should default to true")
	}
}

func TestParsePlatformConfig_SeedBuiltinAgentsDisabled(t *testing.T) {
	cfg, err := ParsePlatformConfig(map[string]any{
		"namespace":           "test-ns",
		"seed_builtin_agents": false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SeedBuiltinAgents {
		t.Error("SeedBuiltinAgents should be false when explicitly disabled")
	}
}

func TestParsePlatformConfig_SSL(t *testing.T) {
	m := map[string]any{
		"namespace":            "my-ns",
		"fastapi_https_port":   9443,
		"ssl_certfile":         "/etc/tls/cert.pem",
		"ssl_keyfile":          "/etc/tls/key.pem",
		"ssl_keyfile_password": "secret",
	}

	cfg, err := ParsePlatformConfig(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.FastAPIHTTPSPort != 9443 {
		t.Errorf("FastAPIHTTPSPort = %d, want %d", cfg.FastAPIHTTPSPort, 9443)
	}
	if cfg.SSLCertFile != "/etc/tls/cert.pem" {
		t.Errorf("SSLCertFile = %q", cfg.SSLCertFile)
	}
	if cfg.SSLKeyFile != "/etc/tls/key.pem" {
		t.Errorf("SSLKeyFile = %q", cfg.SSLKeyFile)
	}
	if cfg.SSLKeyFilePassword != "secret" {
		t.Errorf("SSLKeyFilePassword = %q", cfg.SSLKeyFilePassword)
	}
}

func TestParsePlatformConfig_MissingNamespace(t *testing.T) {
	_, err := ParsePlatformConfig(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
}

func TestParsePlatformConfig_Overrides(t *testing.T) {
	m := map[string]any{
		"namespace":                     "my-ns",
		"fastapi_host":                  "0.0.0.0",
		"fastapi_port":                  9001,
		"database_url":                  "sqlite:///test.db",
		"health_check_interval_seconds": 15,
		"health_check_ttl_seconds":      45,
		"frontend_use_authorization":    true,
		"external_auth_provider":        "enterprise",
		"cors_allowed_origins":          []any{"http://localhost:3000", "http://localhost:8000"},
	}

	cfg, err := ParsePlatformConfig(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.FastAPIHost != "0.0.0.0" {
		t.Errorf("FastAPIHost = %q, want %q", cfg.FastAPIHost, "0.0.0.0")
	}
	if cfg.FastAPIPort != 9001 {
		t.Errorf("FastAPIPort = %d, want %d", cfg.FastAPIPort, 9001)
	}
	if cfg.DatabaseURL != "sqlite:///test.db" {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "sqlite:///test.db")
	}
	if cfg.HealthCheckIntervalSeconds != 15 {
		t.Errorf("HealthCheckIntervalSeconds = %d, want %d", cfg.HealthCheckIntervalSeconds, 15)
	}
	if !cfg.FrontendUseAuthorization {
		t.Error("FrontendUseAuthorization should be true")
	}
	if cfg.ExternalAuthProvider != "enterprise" {
		t.Errorf("ExternalAuthProvider = %q, want %q", cfg.ExternalAuthProvider, "enterprise")
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Errorf("CORSAllowedOrigins len = %d, want 2", len(cfg.CORSAllowedOrigins))
	}
}

func TestParsePlatformConfig_AgentInstructionBoundDefaults(t *testing.T) {
	cfg, err := ParsePlatformConfig(map[string]any{"namespace": "test-ns"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentInstructionMinChars != 1 {
		t.Errorf("AgentInstructionMinChars = %d, want 1", cfg.AgentInstructionMinChars)
	}
	if cfg.AgentInstructionMaxChars != 32_000 {
		t.Errorf("AgentInstructionMaxChars = %d, want %d", cfg.AgentInstructionMaxChars, 32_000)
	}
}

func TestParsePlatformConfig_AgentInstructionBoundOverrides(t *testing.T) {
	cfg, err := ParsePlatformConfig(map[string]any{
		"namespace":                   "test-ns",
		"agent_instruction_min_chars": 50,
		"agent_instruction_max_chars": 100_000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentInstructionMinChars != 50 {
		t.Errorf("AgentInstructionMinChars = %d, want 50", cfg.AgentInstructionMinChars)
	}
	if cfg.AgentInstructionMaxChars != 100_000 {
		t.Errorf("AgentInstructionMaxChars = %d, want %d", cfg.AgentInstructionMaxChars, 100_000)
	}
}

func TestParsePlatformConfig_AgentInstructionBoundInvalidKeepsDefault(t *testing.T) {
	for _, bad := range []any{0, -5, "lots", true} {
		cfg, err := ParsePlatformConfig(map[string]any{
			"namespace":                   "test-ns",
			"agent_instruction_max_chars": bad,
		})
		if err != nil {
			t.Fatalf("value %v: unexpected error: %v", bad, err)
		}
		if cfg.AgentInstructionMaxChars != 32_000 {
			t.Errorf("value %v (%T): AgentInstructionMaxChars = %d, want default %d",
				bad, bad, cfg.AgentInstructionMaxChars, 32_000)
		}
	}
}

func TestParsePlatformConfig_AgentInstructionInvalidWarns(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if _, err := ParsePlatformConfig(map[string]any{
		"namespace":                   "test-ns",
		"agent_instruction_max_chars": -5,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"ignoring invalid platform config value", "agent_instruction_max_chars"} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q; got: %s", want, got)
		}
	}
}

func TestParsePlatformConfig_AgentInstructionAbsentDoesNotWarn(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	if _, err := ParsePlatformConfig(map[string]any{"namespace": "test-ns"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), "agent_instruction") {
		t.Errorf("absent key must not warn; got: %s", buf.String())
	}
}

func TestParsePlatformConfig_AgentInstructionInvertedRangeIsRejected(t *testing.T) {
	_, err := ParsePlatformConfig(map[string]any{
		"namespace":                   "test-ns",
		"agent_instruction_min_chars": 40000,
		"agent_instruction_max_chars": 32000,
	})
	if err == nil {
		t.Fatal("expected an error for min > max")
	}
	if !strings.Contains(err.Error(), "agent_instruction_min_chars") {
		t.Errorf("error must name the offending key; got: %v", err)
	}
}

func TestParsePlatformConfig_AgentInstructionAcceptsQuotedNumber(t *testing.T) {
	cfg, err := ParsePlatformConfig(map[string]any{
		"namespace":                   "test-ns",
		"agent_instruction_max_chars": "50000",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentInstructionMaxChars != 50000 {
		t.Errorf("AgentInstructionMaxChars = %d, want 50000", cfg.AgentInstructionMaxChars)
	}
}

func TestParsePlatformConfig_MaxUploadBytes(t *testing.T) {
	cases := map[string]struct {
		value any
		want  int64
	}{
		"absent keeps default":      {want: DefaultMaxUploadBytes},
		"int override":              {value: 300 << 20, want: 300 << 20},
		"quoted override":           {value: "314572800", want: 300 << 20},
		"zero keeps default":        {value: 0, want: DefaultMaxUploadBytes},
		"negative keeps default":    {value: -1, want: DefaultMaxUploadBytes},
		"non-numeric keeps default": {value: "lots", want: DefaultMaxUploadBytes},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := map[string]any{"namespace": "test-ns"}
			if tc.value != nil {
				m["max_upload_bytes"] = tc.value
			}
			cfg, err := ParsePlatformConfig(m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.MaxUploadBytes != tc.want {
				t.Errorf("MaxUploadBytes = %d, want %d", cfg.MaxUploadBytes, tc.want)
			}
		})
	}
}
