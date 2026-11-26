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

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/agentlink"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const deployerKindLink = "link"

type LinkAgentDeployer struct {
	linkAgents *store.LinkAgentRepo
	yamlSvc    *YAMLService
	logger     *slog.Logger
}

func NewLinkAgentDeployer(linkAgents *store.LinkAgentRepo, yamlSvc *YAMLService, logger *slog.Logger) *LinkAgentDeployer {
	if logger == nil {
		logger = slog.Default()
	}
	return &LinkAgentDeployer{linkAgents: linkAgents, yamlSvc: yamlSvc, logger: logger}
}

func (*LinkAgentDeployer) Kind() string { return deployerKindLink }

func isEntrypointHosted(d Deployer) bool {
	return d != nil && d.Kind() == deployerKindLink
}

func (d *LinkAgentDeployer) Validate(ctx context.Context, input DeployInput) error {
	if input.Agent == nil {
		return &ValidationError{Message: "agent is required"}
	}
	link, err := d.linkAgents.GetByID(ctx, input.Agent.ID)
	if err != nil {
		return fmt.Errorf("load link agent: %w", err)
	}
	if link == nil {
		if input.Action == DeployActionUndeploy {
			return nil
		}
		return &ValidationError{Message: fmt.Sprintf("link_agents row missing for agent %s", input.Agent.ID)}
	}
	hosting, err := DeployedEntrypoint(ctx, d.linkAgents.DB(), d.yamlSvc, d.logger, link.EntrypointID)
	if err != nil {
		return fmt.Errorf("read the link agent's entrypoint: %w", err)
	}
	if entrypointConfigUnreadable(hosting) {
		return errLinkEntrypointUnreadable(link.EntrypointID)
	}
	if input.Action == DeployActionUndeploy {
		return nil
	}
	if !entrypointIsServing(hosting) {
		return errLinkEntrypointNotDeployed(ctx, d.linkAgents.DB(), link.EntrypointID)
	}
	if _, err := agentlink.ParseConfig(map[string]any{
		agentLinkPlatformManagedKey: true,
		agentLinkAgentsKey:          []any{linkAgentRuntimeConfig(link)},
	}); err != nil {
		d.logger.Warn("link agent deploy refused: the agent link does not accept its settings", "linkAgentID", link.ID, "error", err)
		return &ValidationError{Message: "the agent link does not accept this link agent's settings; save the link agent again to see what to change"}
	}
	return nil
}

func errLinkEntrypointUnreadable(entrypointID string) error {
	return &ValidationError{Message: fmt.Sprintf("the link agent's entrypoint %s is deployed but its saved config cannot be read; deploy the entrypoint again, then retry", entrypointID)}
}

func errLinkEntrypointNotDeployed(ctx context.Context, db *store.DB, entrypointID string) error {
	name := entrypointID
	if gw, err := store.NewGatewayRepo(db).GetByID(ctx, entrypointID); err == nil && gw != nil {
		name = gw.Name
	}
	return &ValidationError{Message: fmt.Sprintf("the link agent's entrypoint %q is not deployed; deploy the entrypoint first", name)}
}

func (d *LinkAgentDeployer) InstanceName(agent *store.Agent) string {
	if agent == nil {
		return ""
	}
	return AgentInstanceName(agent)
}

func (d *LinkAgentDeployer) BuildPayloads(ctx context.Context, input DeployInput) (BuildResult, error) {
	if input.Agent == nil {
		return BuildResult{}, fmt.Errorf("agent is nil")
	}
	link, err := d.linkAgents.GetByID(ctx, input.Agent.ID)
	if err != nil {
		return BuildResult{}, fmt.Errorf("load link agent: %w", err)
	}
	if link == nil {
		return BuildResult{}, &NotFoundError{Resource: resourceLinkAgent, ID: input.Agent.ID}
	}
	snapshot, err := json.Marshal(linkAgentSnapshotOf(link))
	if err != nil {
		return BuildResult{}, fmt.Errorf("marshal snapshot: %w", err)
	}
	surfaceHashes, err := computeAgentSurfaceHashes(ctx, nil, &agentCardSurface{Name: link.Name, Description: link.Description}, nil)
	if err != nil {
		return BuildResult{}, fmt.Errorf("compute agent surface hashes: %w", err)
	}
	return BuildResult{
		SnapshotYAML:  string(snapshot),
		DataSnapshot:  deployedLinkAgentInfo(link),
		ConfigHash:    computeLinkAgentConfigHash(link),
		SurfaceHashes: surfaceHashes,
	}, nil
}

func (*LinkAgentDeployer) ReplayFromSnapshot(_ string) ([]ControlPayload, error) {
	return nil, ErrNoReplay
}
