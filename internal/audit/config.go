// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package audit

import (
	"os"
	"strings"
)

type Config struct {
	Enabled *bool `yaml:"enabled"`
}

func DefaultConfig() Config {
	enabled := true
	return Config{Enabled: &enabled}
}

func (c Config) IsEnabled() bool {
	if envVal := os.Getenv("SAM_AUDIT_LOG"); envVal != "" {
		return !strings.EqualFold(envVal, "false") && envVal != "0"
	}
	if c.Enabled != nil {
		return *c.Enabled
	}
	return true
}
