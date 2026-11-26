// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type watchlistAgentModelAdapter struct {
	repo *store.AgentRepo
}

func (a watchlistAgentModelAdapter) ModelProviderForAgent(ctx context.Context, agentName string) (string, bool, error) {
	if id, ok := agentIDFromInstanceName(agentName); ok {
		agent, _, _, _, _, err := a.repo.GetByID(ctx, id)
		if err != nil {
			return "", false, err
		}
		if agent != nil {
			provider := ""
			if agent.ModelProvider != nil {
				provider = *agent.ModelProvider
			}
			return provider, true, nil
		}
	}
	agent, _, _, _, _, err := a.repo.GetByName(ctx, agentName)
	if err != nil {
		return "", false, err
	}
	if agent == nil {
		return "", false, nil
	}
	provider := ""
	if agent.ModelProvider != nil {
		provider = *agent.ModelProvider
	}
	return provider, true, nil
}

type watchlistModelConfigAdapter struct {
	repo *store.ModelConfigRepo
}

func (a watchlistModelConfigAdapter) AcceptableModelIdentifiers(ctx context.Context, aliasOrID string) ([]string, error) {
	mc, err := a.repo.GetByAliasOrID(ctx, aliasOrID)
	if err != nil {
		return nil, err
	}
	if mc == nil {
		return nil, nil
	}
	return []string{mc.ID, mc.Alias}, nil
}
