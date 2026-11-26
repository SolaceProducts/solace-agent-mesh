// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	opAgentDeploy      = "agent.deploy"
	opAgentRollback    = "agent.rollback"
	opAgentUndeploy    = "agent.undeploy"
	opWorkflowDeploy   = "workflow.deploy"
	opWorkflowRollback = "workflow.rollback"
	opWorkflowUndeploy = "workflow.undeploy"
	opEntrypointDeploy = "entrypoint.deploy"
)

type agentIntent struct {
	Deployment store.AgentDeployment `json:"deployment"`
	Payloads   []ControlPayload      `json:"payloads,omitempty"`
	Instance   string                `json:"instance,omitempty"`
}

type workflowIntent struct {
	Deployment store.WorkflowDeployment `json:"deployment"`
	YAML       string                   `json:"yaml,omitempty"`
}

type entrypointIntent struct {
	Deployment store.GatewayDeployment `json:"deployment"`
	Payloads   []ControlPayload        `json:"payloads,omitempty"`
}

type agentDeployArgs struct {
	Req    CreateAgentDeploymentRequest
	UserID string
	Opts   DeployOptions
}

type agentRollbackArgs struct {
	Req     CreateAgentDeploymentRequest
	PriorID string
	UserID  string
}

type agentUndeployArgs struct{ AgentID, UserID string }

type workflowDeployArgs struct {
	Req        CreateWorkflowDeploymentRequest
	UserID     string
	Background bool
}

type workflowRollbackArgs struct {
	Req     CreateWorkflowDeploymentRequest
	PriorID string
	UserID  string
}

type workflowUndeployArgs struct{ WorkflowID, UserID string }

type entrypointDeployArgs struct {
	Req    CreateGatewayDeploymentRequest
	UserID string
}

func serveDeploys(l *deployLeader, agents *DeploymentService, workflows *WorkflowDeploymentService, entrypoints *GatewayDeploymentService) {
	if entrypoints != nil && entrypoints.gwes != nil {
		l.onLeaderTerm(func(ctx context.Context) { followGWEsForTerm(ctx, entrypoints.gwes, l.logger) })
	}
	if l != nil {
		finishWith(l, agents, workflows, entrypoints)
	}
	if agents != nil {
		agents.leader = l
		handleOp(l, opAgentDeploy, func(a agentDeployArgs) objectKey { return agentObject(a.Req.AgentID) },
			func(ctx context.Context, a agentDeployArgs, deploymentID string) (string, error) {
				return idOf(agents.createHere(ctx, a.Req, a.UserID, a.Opts, deploymentID))
			})
		handleOp(l, opAgentRollback, func(a agentRollbackArgs) objectKey { return agentObject(a.Req.AgentID) },
			func(ctx context.Context, a agentRollbackArgs, deploymentID string) (string, error) {
				return idOf(agents.rollbackHere(ctx, a.Req, a.PriorID, a.UserID, deploymentID))
			})
		handleOp(l, opAgentUndeploy, func(a agentUndeployArgs) objectKey { return agentObject(a.AgentID) },
			func(ctx context.Context, a agentUndeployArgs, deploymentID string) (string, error) {
				return "", agents.undeployHere(ctx, a.AgentID, a.UserID, deploymentID)
			})
	}
	if workflows != nil {
		workflows.leader = l
		handleOp(l, opWorkflowDeploy, func(a workflowDeployArgs) objectKey { return workflowObject(a.Req.WorkflowID) },
			func(ctx context.Context, a workflowDeployArgs, deploymentID string) (string, error) {
				if a.Background {
					return idOf(workflows.redeployHere(ctx, a.Req, a.UserID, deploymentID))
				}
				return idOf(workflows.createHere(ctx, a.Req, a.UserID, deploymentID))
			})
		handleOp(l, opWorkflowRollback, func(a workflowRollbackArgs) objectKey { return workflowObject(a.Req.WorkflowID) },
			func(ctx context.Context, a workflowRollbackArgs, deploymentID string) (string, error) {
				return idOf(workflows.rollbackHere(ctx, a.Req, a.PriorID, a.UserID, deploymentID))
			})
		handleOp(l, opWorkflowUndeploy, func(a workflowUndeployArgs) objectKey { return workflowObject(a.WorkflowID) },
			func(ctx context.Context, a workflowUndeployArgs, deploymentID string) (string, error) {
				return "", workflows.undeployHere(ctx, a.WorkflowID, a.UserID, deploymentID)
			})
	}
	if entrypoints != nil {
		entrypoints.leader = l
		handleOp(l, opEntrypointDeploy, func(a entrypointDeployArgs) objectKey { return entrypointObject(a.Req.GatewayID) },
			func(ctx context.Context, a entrypointDeployArgs, deploymentID string) (string, error) {
				return idOf(entrypoints.deployHere(ctx, a.Req, a.UserID, deploymentID))
			})
	}
}

type recorded interface{ deploymentID() string }

func (r *AgentDeploymentResponse) deploymentID() string {
	if r == nil {
		return ""
	}
	return r.ID
}

func (r *WorkflowDeploymentResponse) deploymentID() string {
	if r == nil {
		return ""
	}
	return r.ID
}

func (r *GatewayDeploymentResponse) deploymentID() string {
	if r == nil {
		return ""
	}
	return r.ID
}

func idOf[R recorded](resp R, err error) (string, error) {
	return resp.deploymentID(), err
}
