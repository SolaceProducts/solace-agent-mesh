// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestParseConfigReadsSection(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"product_analytics": map[string]any{
			"buffer_size":      250,
			"flush_interval":   "3s",
			"batch_size":       25,
			"rate_cap_per_min": 120,
		},
	})
	sc := ParseConfig(cfg)
	if sc.BufferSize != 250 {
		t.Errorf("BufferSize = %d, want 250", sc.BufferSize)
	}
	if sc.FlushInterval != 3*time.Second {
		t.Errorf("FlushInterval = %v, want 3s", sc.FlushInterval)
	}
	if sc.BatchSize != 25 {
		t.Errorf("BatchSize = %d, want 25", sc.BatchSize)
	}
	if sc.RateCapPerMin != 120 {
		t.Errorf("RateCapPerMin = %d, want 120", sc.RateCapPerMin)
	}
}

func TestParseConfigToleratesNonMapSection(t *testing.T) {
	for name, val := range map[string]any{
		"nil section":    nil,
		"scalar section": true,
		"string section": "on",
	} {
		cfg := config.FromMap(map[string]any{"product_analytics": val})
		sc := ParseConfig(cfg)
		if sc != (ServiceConfig{}).withDefaults() {
			t.Errorf("%s: got %+v, want all defaults", name, sc)
		}
	}
}

func TestParseConfigSectionPartialTakesDefaults(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"product_analytics": map[string]any{"batch_size": 7},
	})
	sc := ParseConfig(cfg)
	if sc.BatchSize != 7 {
		t.Errorf("BatchSize = %d, want 7", sc.BatchSize)
	}
	if sc.BufferSize != defaultBufferSize {
		t.Errorf("BufferSize = %d, want default %d", sc.BufferSize, defaultBufferSize)
	}
	if sc.FlushInterval != defaultFlushInterval {
		t.Errorf("FlushInterval = %v, want default %v", sc.FlushInterval, defaultFlushInterval)
	}
	if sc.RateCapPerMin != defaultRateCapPerMin {
		t.Errorf("RateCapPerMin = %d, want default %d", sc.RateCapPerMin, defaultRateCapPerMin)
	}
}
