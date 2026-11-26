// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func DeployedEntrypoints(ctx context.Context, db *store.DB, yamlSvc *YAMLService, logger *slog.Logger) ([]control.EntrypointApply, error) {
	if db == nil {
		return nil, nil
	}
	gateways, err := allEntrypoints(ctx, store.NewGatewayRepo(db))
	if err != nil {
		return nil, err
	}
	desired, err := store.NewGatewayDeploymentRepo(db).Desired(ctx)
	if err != nil {
		return nil, fmt.Errorf("load entrypoint deployments: %w", err)
	}
	inFlight, err := store.NewDeploymentIntentRepo(db).Newest(ctx, store.IntentKindEntrypoint)
	if err != nil {
		return nil, fmt.Errorf("load entrypoint deployments in flight: %w", err)
	}
	var out []control.EntrypointApply
	var linkAgents []deployedLinkAgent
	linkAgentsRead := false
	for _, gw := range gateways {
		apply := desiredEntrypoint(gw, desired[gw.ID], inFlight[gw.ID], yamlSvc, logger)
		if apply == nil {
			continue
		}
		if hostsLinkAgents(gw, apply) {
			if !linkAgentsRead {
				if linkAgents, err = linkAgentsDeployed(ctx, db, logger); err != nil {
					return nil, fmt.Errorf("load deployed link agents: %w", err)
				}
				linkAgentsRead = true
			}
			mergeLinkAgents(apply, onEntrypoint(linkAgents, gw.ID), logger)
		}
		out = append(out, *apply)
	}
	return out, nil
}

func EntrypointReaders(db func() *store.DB, namespace string, logger *slog.Logger) (func(context.Context) ([]control.EntrypointApply, error), func(context.Context, string) (*control.EntrypointApply, error)) {
	return func(ctx context.Context) ([]control.EntrypointApply, error) {
			return DeployedEntrypoints(ctx, db(), NewYAMLService(namespace, logger), logger)
		}, func(ctx context.Context, name string) (*control.EntrypointApply, error) {
			return DeployedEntrypoint(ctx, db(), NewYAMLService(namespace, logger), logger, name)
		}
}

const entrypointPageSize = 500

func allEntrypoints(ctx context.Context, repo *store.GatewayRepo) ([]*store.Gateway, error) {
	var all []*store.Gateway
	for page := 1; ; page++ {
		gateways, total, err := repo.ListPaginated(ctx, page, entrypointPageSize, "")
		if err != nil {
			return nil, fmt.Errorf("list entrypoints: %w", err)
		}
		all = append(all, gateways...)
		if len(gateways) < entrypointPageSize || len(all) >= total {
			return all, nil
		}
	}
}

func DeployedEntrypoint(ctx context.Context, db *store.DB, yamlSvc *YAMLService, logger *slog.Logger, id string) (*control.EntrypointApply, error) {
	if db == nil {
		return nil, nil
	}
	gw, err := store.NewGatewayRepo(db).GetByID(ctx, id)
	if err != nil || gw == nil {
		return nil, err
	}
	dep, err := store.NewGatewayDeploymentRepo(db).DesiredFor(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load entrypoint deployment: %w", err)
	}
	intent, err := store.NewDeploymentIntentRepo(db).For(ctx, store.IntentKindEntrypoint, id)
	if err != nil {
		return nil, fmt.Errorf("load entrypoint deployment in flight: %w", err)
	}
	apply := desiredEntrypoint(gw, dep, intent, yamlSvc, logger)
	if !hostsLinkAgents(gw, apply) {
		return apply, nil
	}
	deployed, err := linkAgentsDeployedOn(ctx, db, id, logger)
	if err != nil {
		return nil, fmt.Errorf("load deployed link agents: %w", err)
	}
	mergeLinkAgents(apply, deployed, logger)
	return apply, nil
}

func desiredEntrypoint(gw *store.Gateway, dep *store.GatewayDeployment, intent *store.DeploymentIntent, yamlSvc *YAMLService, logger *slog.Logger) *control.EntrypointApply {
	if intent != nil {
		var pending entrypointIntent
		if json.Unmarshal([]byte(intent.Payload), &pending) == nil {
			return entrypointApply(gw.ID, pending.Deployment.ID, pending.Deployment.Action, pending.Payloads)
		}
	}
	if dep == nil {
		return nil
	}
	if dep.Action == DeployActionUndeploy {
		return entrypointApply(gw.ID, dep.ID, dep.Action, nil)
	}
	unreadable := &control.EntrypointApply{Name: gw.ID, DeploymentID: dep.ID}
	snapshot := derefStr(dep.GatewayConfigSnapshot)
	if snapshot == "" {
		if yamlSvc == nil {
			logger.Warn("deployed entrypoint has no config snapshot; it starts at its next deploy", "entrypointID", gw.ID)
			return unreadable
		}
		var err error
		if snapshot, err = yamlSvc.GenerateGatewayYAML(gw); err != nil {
			logger.Error("deployed entrypoint has no config snapshot and its config cannot be generated; it starts at its next deploy", "entrypointID", gw.ID, "error", err)
			return unreadable
		}
	}
	apps, err := schema.ParseAppsConfig([]byte(snapshot))
	if err != nil {
		logger.Warn("deployed entrypoint snapshot does not parse; it starts at its next deploy", "entrypointID", gw.ID, "error", err)
		return unreadable
	}
	var payloads []ControlPayload
	for _, app := range apps {
		if appConfig, ok := app["app_config"].(map[string]any); ok {
			name, _ := app["name"].(string)
			payloads = append(payloads, ControlPayload{"name": name, "app_config": appConfig})
		}
	}
	if apply := entrypointApply(gw.ID, dep.ID, dep.Action, payloads); apply != nil {
		return apply
	}
	return unreadable
}

func entrypointApply(entrypointID, deploymentID, action string, payloads []ControlPayload) *control.EntrypointApply {
	if action == DeployActionUndeploy {
		return &control.EntrypointApply{Name: entrypointID, DeploymentID: deploymentID, Undeploy: true}
	}
	for _, payload := range payloads {
		name, _ := payload["name"].(string)
		appConfig, _ := payload["app_config"].(map[string]any)
		if name != "" && appConfig != nil {
			return &control.EntrypointApply{Name: name, DeploymentID: deploymentID, AppConfig: appConfig}
		}
	}
	return nil
}
