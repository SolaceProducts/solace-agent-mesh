// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func applySoftSubsetAssert(
	identity *auth.Identity,
	a2aUserConfig string,
	taskID string,
	delegatingAgent string,
	userID string,
	logger *slog.Logger,
) {
	auth.ApplySoftSubsetAssert(
		identity,
		a2aUserConfig,
		auth.SoftSubsetAssertFields{
			TaskID:          taskID,
			UserID:          userID,
			DelegatingAgent: delegatingAgent,
			Origin:          "agent",
		},
		logger,
	)
}

func parseEnterpriseCapabilitiesScopes(a2aUserConfig string) ([]string, bool) {
	return auth.ParseEnterpriseCapabilitiesScopes(a2aUserConfig)
}

func isScopeSubset(body, jwt []string) bool {
	return auth.IsScopeSubset(body, jwt)
}
