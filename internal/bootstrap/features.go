// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

func LoadFeatureRegistry(logger *slog.Logger) (*features.Registry, error) {
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		return nil, fmt.Errorf("load feature flags: %w", err)
	}
	for _, name := range reg.UnknownEnvOverrides() {
		logger.Warn("ignoring SAM_FEATURE_* env var that does not match any registered flag", "env", name)
	}
	return reg, nil
}
