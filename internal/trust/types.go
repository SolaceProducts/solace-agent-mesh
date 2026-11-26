// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"fmt"
	"time"
)

type TrustCard struct {
	ComponentType string         `json:"component_type"`
	ComponentID   string         `json:"component_id"`
	Namespace     string         `json:"namespace"`
	JWKS          map[string]any `json:"jwks"`
	IssuedAt      int64          `json:"issued_at"`
	ExpiresAt     int64          `json:"expires_at"`
	Version       string         `json:"version"`

	PublicURL string `json:"public_url,omitempty"`
}

func (c *TrustCard) IsExpired() bool {
	return time.Now().UnixMilli() > c.ExpiresAt
}

type Config struct {
	Enabled                    bool   `yaml:"enabled"`
	CardPublishIntervalSeconds int    `yaml:"card_publish_interval_seconds"`
	CardExpirationDays         int    `yaml:"card_expiration_days"`
	VerificationMode           string `yaml:"verification_mode"`
	ClockSkewToleranceSeconds  int    `yaml:"clock_skew_tolerance_seconds"`
	JWTDefaultTTLSeconds       int    `yaml:"jwt_default_ttl_seconds"`
	JWTMaxTTLSeconds           int    `yaml:"jwt_max_ttl_seconds"`
	StartupWaitTimeoutSeconds  int    `yaml:"startup_wait_timeout_seconds"`
	MinRequiredGateways        int    `yaml:"min_required_gateways"`
	FailOnTimeout              bool   `yaml:"fail_on_timeout"`

	KeyRotationInterval time.Duration `yaml:"-"`

	KeyOverlapRetention time.Duration `yaml:"-"`

	SigningKey SigningKeyConfig `yaml:"-"`
}

type SigningKeyConfig struct {
	Seed           []byte
	PersistenceDir string
}

func DefaultConfig() Config {
	return Config{
		Enabled:                    false,
		CardPublishIntervalSeconds: 60,
		CardExpirationDays:         30,
		VerificationMode:           "strict",
		ClockSkewToleranceSeconds:  300,
		JWTDefaultTTLSeconds:       3600,
		JWTMaxTTLSeconds:           86400,
		StartupWaitTimeoutSeconds:  120,
		MinRequiredGateways:        1,
		FailOnTimeout:              true,
	}
}

func (c *Config) Validate() error {
	if c.CardPublishIntervalSeconds <= 0 {
		return fmt.Errorf("card_publish_interval_seconds must be positive, got %d", c.CardPublishIntervalSeconds)
	}
	if c.CardExpirationDays <= 0 {
		return fmt.Errorf("card_expiration_days must be positive, got %d", c.CardExpirationDays)
	}
	if c.VerificationMode != "strict" && c.VerificationMode != "permissive" {
		return fmt.Errorf("verification_mode must be 'strict' or 'permissive', got %q", c.VerificationMode)
	}
	if c.ClockSkewToleranceSeconds < 0 {
		return fmt.Errorf("clock_skew_tolerance_seconds must be non-negative, got %d", c.ClockSkewToleranceSeconds)
	}
	if c.JWTDefaultTTLSeconds <= 0 {
		return fmt.Errorf("jwt_default_ttl_seconds must be positive, got %d", c.JWTDefaultTTLSeconds)
	}
	if c.JWTMaxTTLSeconds <= 0 {
		return fmt.Errorf("jwt_max_ttl_seconds must be positive, got %d", c.JWTMaxTTLSeconds)
	}
	if c.JWTDefaultTTLSeconds > c.JWTMaxTTLSeconds {
		return fmt.Errorf("jwt_default_ttl_seconds (%d) must be <= jwt_max_ttl_seconds (%d)", c.JWTDefaultTTLSeconds, c.JWTMaxTTLSeconds)
	}
	if c.StartupWaitTimeoutSeconds <= 0 {
		return fmt.Errorf("startup_wait_timeout_seconds must be positive, got %d", c.StartupWaitTimeoutSeconds)
	}
	if c.MinRequiredGateways < 0 {
		return fmt.Errorf("min_required_gateways must be non-negative, got %d", c.MinRequiredGateways)
	}
	if c.KeyRotationInterval < 0 {
		return fmt.Errorf("key_rotation_interval must be non-negative, got %v", c.KeyRotationInterval)
	}
	if c.KeyOverlapRetention < 0 {
		return fmt.Errorf("key_overlap_retention must be non-negative, got %v", c.KeyOverlapRetention)
	}
	return nil
}

func (c *Config) ClockSkew() time.Duration {
	return time.Duration(c.ClockSkewToleranceSeconds) * time.Second
}

func (c *Config) DefaultTTL() time.Duration {
	return time.Duration(c.JWTDefaultTTLSeconds) * time.Second
}

func (c *Config) MaxTTL() time.Duration {
	return time.Duration(c.JWTMaxTTLSeconds) * time.Second
}
