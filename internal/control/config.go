// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import "github.com/SolaceProducts/solace-agent-mesh/internal/config"

const DefaultMaxConcurrentHandlers = 32

type ServiceConfig struct {
	MaxConcurrentHandlers int
}

func (c *ServiceConfig) setDefaults() {
	if c.MaxConcurrentHandlers <= 0 {
		c.MaxConcurrentHandlers = DefaultMaxConcurrentHandlers
	}
}

func parseServiceConfig(cfg config.Config) ServiceConfig {
	out := ServiceConfig{}
	if cfg != nil {
		if section := cfg.GetSection("control"); section != nil {
			out.MaxConcurrentHandlers = section.GetInt("max_concurrent_handlers")
		}
	}
	out.setDefaults()
	return out
}
