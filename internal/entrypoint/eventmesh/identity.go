// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

func resolveUserID(
	exprCtx *sacexpr.Context,
	handler *EventHandlerConfig,
	gatewayDefault string,
	forceUserIdentity string,
	logger *slog.Logger,
) (userID string, forced bool) {
	userID = handler.DefaultUserIdentity
	if userID == "" {
		userID = gatewayDefault
	}
	if handler.UserIdentityExpression != "" {
		resolved, err := exprCtx.GetData(handler.UserIdentityExpression)
		if err != nil {
			logger.Warn("failed to evaluate user_identity_expression",
				"error", err,
				"handler", handler.Name,
			)
		} else if s, ok := resolved.(string); ok && s != "" {
			userID = s
		}
	}

	if forceUserIdentity != "" {
		logger.Warn("DEVELOPMENT MODE: forcing user_identity",
			"original", userID,
			"forced", forceUserIdentity,
			"handler", handler.Name,
		)
		return forceUserIdentity, true
	}

	return userID, false
}
