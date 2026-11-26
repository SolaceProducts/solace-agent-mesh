// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ControlPayload = map[string]any

type DeployInput struct {
	Agent             *store.Agent
	Action            string
	Toolsets          []string
	ToolsetExclusions map[string]string
	InputModes        []string
	OutputModes       []string
	ConnectorIDs      []string
	SkillIDs          []string
	Opts              DeployOptions
}

type BuildResult struct {
	Payloads      []ControlPayload
	SnapshotYAML  string
	DataSnapshot  any
	ConfigHash    string
	SurfaceHashes AgentSurfaceHashes
}

var ErrNoReplay = errors.New("deployer does not support snapshot replay")

type Deployer interface {
	Kind() string

	Validate(ctx context.Context, input DeployInput) error

	InstanceName(agent *store.Agent) string

	BuildPayloads(ctx context.Context, input DeployInput) (BuildResult, error)

	ReplayFromSnapshot(snapshotYAML string) ([]ControlPayload, error)
}

type DeployerRegistry struct {
	byType map[string]Deployer
}

func NewDeployerRegistry() *DeployerRegistry {
	return &DeployerRegistry{byType: make(map[string]Deployer)}
}

func (r *DeployerRegistry) Register(agentType string, d Deployer) {
	r.byType[agentType] = d
}

func (r *DeployerRegistry) Lookup(agentType string) Deployer {
	if r == nil {
		return nil
	}
	return r.byType[agentType]
}

func (r *DeployerRegistry) LookupOrError(agentType string) (Deployer, error) {
	d := r.Lookup(agentType)
	if d == nil {
		return nil, &ValidationError{Message: fmt.Sprintf("agent type %q is not deployable from the platform service", agentType)}
	}
	return d, nil
}

func NewDefaultDeployerRegistry(yamlSvc *YAMLService, connectorRepo *store.ConnectorRepo, toolPackageRepo *store.ToolPackageRepo, skillRepo *store.SkillRepo, a2aRepo *store.A2AAgentRepo, namespace string, logger *slog.Logger) *DeployerRegistry {
	r := NewDeployerRegistry()
	r.Register(AgentTypeStandard, NewStandardAgentDeployer(yamlSvc, connectorRepo, toolPackageRepo, skillRepo, logger))
	if a2aRepo != nil {
		r.Register(AgentTypeA2AProxy, NewA2AProxyDeployer(a2aRepo, namespace))
	}
	return r
}
