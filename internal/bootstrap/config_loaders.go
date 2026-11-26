// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"time"

	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/audit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func LoadTrustConfig(cfg config.Config) trust.Config {
	tc := trust.DefaultConfig()
	sec := cfg.GetSection("trust_manager")
	if sec == nil {
		return tc
	}

	tc.Enabled = sec.GetBool("enabled")
	if v := sec.GetInt("card_publish_interval_seconds"); v > 0 {
		tc.CardPublishIntervalSeconds = v
	}
	if v := sec.GetInt("card_expiration_days"); v > 0 {
		tc.CardExpirationDays = v
	}
	if v := sec.GetString("verification_mode"); v != "" {
		tc.VerificationMode = v
	}
	if v := sec.GetInt("clock_skew_tolerance_seconds"); v > 0 {
		tc.ClockSkewToleranceSeconds = v
	}
	if v := sec.GetInt("jwt_default_ttl_seconds"); v > 0 {
		tc.JWTDefaultTTLSeconds = v
	}
	if v := sec.GetInt("jwt_max_ttl_seconds"); v > 0 {
		tc.JWTMaxTTLSeconds = v
	}
	if v := sec.GetInt("startup_wait_timeout_seconds"); v > 0 {
		tc.StartupWaitTimeoutSeconds = v
	}
	if sec.GetRaw("min_required_gateways") != nil {
		tc.MinRequiredGateways = sec.GetInt("min_required_gateways")
	}
	if sec.GetRaw("fail_on_timeout") != nil {
		tc.FailOnTimeout = sec.GetBool("fail_on_timeout")
	}

	if v := sec.GetInt("key_rotation_interval_seconds"); v > 0 {
		tc.KeyRotationInterval = time.Duration(v) * time.Second
	}
	if sec.GetRaw("key_overlap_retention_seconds") != nil {
		v := sec.GetInt("key_overlap_retention_seconds")
		tc.KeyOverlapRetention = time.Duration(v) * time.Second
	} else if tc.KeyRotationInterval > 0 {
		tc.KeyOverlapRetention = tc.DefaultTTL()
	}

	return tc
}

func LoadIdentityConfig(cfg config.Config) identity.Config {
	ic := identity.DefaultConfig()
	sec := cfg.GetSection("agent_identity")
	if sec == nil {
		return ic
	}

	if v := sec.GetString("key_mode"); v != "" {
		ic.Mode = v
	}
	if v := sec.GetString("key_identity"); v != "" {
		ic.Key = v
	}
	if v := sec.GetString("key_persistence"); v != "" {
		ic.FilePath = v
	}

	return ic
}

func LoadAuditConfig(cfg config.Config) audit.Config {
	sec := cfg.GetSection("audit_log")
	if sec == nil {
		return audit.Config{}
	}

	if sec.GetRaw("enabled") != nil {
		enabled := sec.GetBool("enabled")
		return audit.Config{Enabled: &enabled}
	}

	return audit.Config{}
}
