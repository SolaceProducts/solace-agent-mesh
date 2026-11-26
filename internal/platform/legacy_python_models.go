// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

func LegacyPythonModelBootstrapEnabled(registry *features.Registry, logger *slog.Logger) bool {
	enabled := registry != nil && registry.IsEnabled(features.KeyLegacyPythonModelBootstrap)
	if enabled {
		logger.Warn("legacy Python model bootstrap enabled: model configs with provider credentials are served to any broker client",
			"flag", features.KeyLegacyPythonModelBootstrap)
	} else {
		logger.Info("legacy Python model bootstrap disabled: Python SAM agents receive no platform-managed models",
			"flag", features.KeyLegacyPythonModelBootstrap)
	}
	return enabled
}
