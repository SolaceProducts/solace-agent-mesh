// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const agentInstanceNameKey = "agent_instance_name"

type agentNameResolver interface {
	GetByName(ctx context.Context, name string) (*store.Agent, error)
}

type agentRepoNameResolver struct{ repo *store.AgentRepo }

func (r agentRepoNameResolver) GetByName(ctx context.Context, name string) (*store.Agent, error) {
	agent, _, _, _, _, err := r.repo.GetByName(ctx, name)
	return agent, err
}

func agentResolverFor(repo *store.AgentRepo) agentNameResolver {
	if repo == nil {
		return nil
	}
	return agentRepoNameResolver{repo: repo}
}

func hydrateAgentNodeTargets(ctx context.Context, appConfig map[string]any, resolver agentNameResolver, logger *slog.Logger) error {
	if resolver == nil {
		return nil
	}
	parentName, _ := appConfig["display_name"].(string)
	return forEachWorkflowNodeOfType(appConfig, "agent", func(node map[string]any) error {
		delete(node, agentInstanceNameKey)
		name, _ := node["agent_name"].(string)
		if name == "" {
			return nil
		}
		if _, kind, ok := a2a.IDFromInstanceName(name); ok && kind == a2a.KindAgent {
			return nil
		}
		target, err := resolver.GetByName(ctx, name)
		if err != nil {
			return fmt.Errorf("resolve agent node target %q: %w", name, err)
		}
		if target == nil {
			logNodeTargetNotFound(logger, "agent node target not found; leaving name unresolved",
				node, parentName, "agentName", name)
			return nil
		}
		node[agentInstanceNameKey] = AgentInstanceName(target)
		return nil
	})
}
