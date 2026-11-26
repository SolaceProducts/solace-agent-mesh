// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestLoadTrustConfig_DefaultsWhenSectionMissing(t *testing.T) {
	t.Parallel()
	cfg := config.FromMap(map[string]any{})
	tc := LoadTrustConfig(cfg)
	if tc.Enabled {
		t.Error("Enabled should default to false")
	}
	if tc.KeyRotationInterval != 0 {
		t.Errorf("KeyRotationInterval should default to 0 (disabled), got %v", tc.KeyRotationInterval)
	}
	if tc.KeyOverlapRetention != 0 {
		t.Errorf("KeyOverlapRetention should default to 0, got %v", tc.KeyOverlapRetention)
	}
}

func TestLoadTrustConfig_KeyRotationFields(t *testing.T) {
	t.Parallel()
	cfg := config.FromMap(map[string]any{
		"trust_manager": map[string]any{
			"enabled":                       true,
			"key_rotation_interval_seconds": 3600,
			"key_overlap_retention_seconds": 1800,
		},
	})
	tc := LoadTrustConfig(cfg)
	if tc.KeyRotationInterval != time.Hour {
		t.Errorf("KeyRotationInterval = %v, want 1h", tc.KeyRotationInterval)
	}
	if tc.KeyOverlapRetention != 30*time.Minute {
		t.Errorf("KeyOverlapRetention = %v, want 30m", tc.KeyOverlapRetention)
	}
}

func TestLoadTrustConfig_OverlapDefaultsToJWTTTLWhenRotationEnabled(t *testing.T) {
	t.Parallel()
	cfg := config.FromMap(map[string]any{
		"trust_manager": map[string]any{
			"enabled":                       true,
			"key_rotation_interval_seconds": 3600,
			"jwt_default_ttl_seconds":       1800,
		},
	})
	tc := LoadTrustConfig(cfg)
	if tc.KeyOverlapRetention != 30*time.Minute {
		t.Errorf("KeyOverlapRetention should default to JWTDefaultTTL when omitted with rotation enabled, got %v", tc.KeyOverlapRetention)
	}
}

func TestLoadTrustConfig_ExplicitZeroOverlapPreserved(t *testing.T) {
	t.Parallel()
	cfg := config.FromMap(map[string]any{
		"trust_manager": map[string]any{
			"enabled":                       true,
			"key_rotation_interval_seconds": 3600,
			"key_overlap_retention_seconds": 0,
		},
	})
	tc := LoadTrustConfig(cfg)
	if tc.KeyOverlapRetention != 0 {
		t.Errorf("explicit overlap=0 should be preserved, got %v", tc.KeyOverlapRetention)
	}
}

func TestLoadTrustConfig_RotationDisabledDoesNotInheritJWTTTL(t *testing.T) {
	t.Parallel()
	cfg := config.FromMap(map[string]any{
		"trust_manager": map[string]any{
			"enabled":                 true,
			"jwt_default_ttl_seconds": 1800,
		},
	})
	tc := LoadTrustConfig(cfg)
	if tc.KeyRotationInterval != 0 {
		t.Errorf("KeyRotationInterval should remain 0, got %v", tc.KeyRotationInterval)
	}
	if tc.KeyOverlapRetention != 0 {
		t.Errorf("KeyOverlapRetention should remain 0 when rotation disabled, got %v", tc.KeyOverlapRetention)
	}
}
