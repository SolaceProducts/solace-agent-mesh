// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type ServiceConfig struct {
	BufferSize    int
	FlushInterval time.Duration
	BatchSize     int
	RateCapPerMin int
}

const (
	defaultBufferSize    = 1000
	defaultFlushInterval = 5 * time.Second
	defaultBatchSize     = 50
	defaultRateCapPerMin = 600
)

func (c ServiceConfig) withDefaults() ServiceConfig {
	if c.BufferSize <= 0 {
		c.BufferSize = defaultBufferSize
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = defaultFlushInterval
	}
	if c.BatchSize <= 0 {
		c.BatchSize = defaultBatchSize
	}
	if c.RateCapPerMin <= 0 {
		c.RateCapPerMin = defaultRateCapPerMin
	}
	return c
}

func ParseConfig(cfg config.Config) ServiceConfig {
	var sc ServiceConfig
	if cfg == nil {
		return sc.withDefaults()
	}
	section := cfg.GetSection("product_analytics")
	if section == nil {
		return sc.withDefaults()
	}
	sc.BufferSize = section.GetInt("buffer_size")
	sc.FlushInterval = section.GetDuration("flush_interval")
	sc.BatchSize = section.GetInt("batch_size")
	sc.RateCapPerMin = section.GetInt("rate_cap_per_min")
	return sc.withDefaults()
}
