// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
)

func retireGrants(ctx context.Context, models *store.ModelConfigRepo, grants *tokenbroker.Service, grantIDs []string, logger *slog.Logger) {
	for _, id := range grantIDs {
		emptied, err := emptyModelsNaming(ctx, models, id)
		if err != nil {
			logger.Warn("failed to empty the models of a retired OAuth grant, keeping the grant", "grantID", id, "error", err)
			continue
		}
		if err := grants.Delete(ctx, id); err != nil {
			logger.Warn("failed to remove a retired OAuth grant", "grantID", id, "error", err)
			continue
		}
		if len(emptied) > 0 {
			logger.Info("removed a retired OAuth grant and emptied its models", "grantID", id, "aliases", emptied)
		}
	}
}

func emptyModelsNaming(ctx context.Context, repo *store.ModelConfigRepo, grantID string) ([]string, error) {
	all, err := repo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	var emptied []string
	for _, mc := range all {
		if parseJSONMap(mc.ModelAuthConfig)[llm.GrantIDKey] != grantID {
			continue
		}
		setPlaceholder(mc)
		if err := repo.Update(ctx, mc); err != nil {
			return emptied, fmt.Errorf("empty model %s: %w", mc.Alias, err)
		}
		emptied = append(emptied, mc.Alias)
	}
	return emptied, nil
}
