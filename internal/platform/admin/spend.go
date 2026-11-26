// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func directActionSpend(ctx context.Context, logger *slog.Logger, clientSurface string, surface spend.Surface, modelKey, modelConfigID string) *llm.SpendAttribution {
	var userID, userSub string
	if identity, ok := auth.SubjectFromContext(ctx); ok && identity != nil {
		userID, userSub = spend.StampedRowOr(identity.PlatformUserID(), identity.UserID())
	}
	if userID == "" && userSub == "" {
		logger.Debug("platform LLM call spend not recorded; the request carries no identity", "surface", surface)
		return nil
	}
	return &llm.SpendAttribution{
		UserID:        userID,
		UserSub:       userSub,
		Channel:       clientSurface,
		TriggerKind:   string(spend.TriggerRequest),
		Surface:       string(surface),
		ModelKey:      modelKey,
		ModelConfigID: modelConfigID,
	}
}
