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

type workflowNameResolver interface {
	GetByName(ctx context.Context, name string) (*store.Workflow, error)
}

func hydrateWorkflowNodeTargets(ctx context.Context, appConfig map[string]any, resolver workflowNameResolver, logger *slog.Logger) error {
	if resolver == nil {
		return nil
	}
	parentName, _ := appConfig["display_name"].(string)
	return forEachWorkflowNodeOfType(appConfig, "workflow", func(node map[string]any) error {
		name, _ := node["workflow_name"].(string)
		if name == "" {
			return nil
		}
		if _, kind, ok := a2a.IDFromInstanceName(name); ok && kind == a2a.KindWorkflow {
			return nil
		}
		target, err := resolver.GetByName(ctx, name)
		if err != nil {
			return fmt.Errorf("resolve workflow node target %q: %w", name, err)
		}
		if target == nil {
			logNodeTargetNotFound(logger, "workflow node target not found; leaving name unresolved",
				node, parentName, "workflowName", name)
			return nil
		}
		node["workflow_name"] = a2a.InstanceName(a2a.KindWorkflow, target.ID)
		return nil
	})
}

func forEachWorkflowNodeOfType(appConfig map[string]any, nodeType string, fn func(node map[string]any) error) error {
	wfCfg, ok := appConfig["workflow"].(map[string]any)
	if !ok {
		return nil
	}
	nodes, ok := wfCfg["nodes"].([]any)
	if !ok {
		return nil
	}
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := node["type"].(string); t != nodeType {
			continue
		}
		if err := fn(node); err != nil {
			return err
		}
	}
	return nil
}

func logNodeTargetNotFound(logger *slog.Logger, msg string, node map[string]any, parentName string, targetAttrs ...any) {
	if logger == nil {
		return
	}
	nodeID, _ := node["id"].(string)
	logger.Warn(msg, append(targetAttrs, "nodeID", nodeID, "parentWorkflow", parentName)...)
}
