// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"

const (
	DeployStatusSuccess = "success"
	DeployStatusFailed  = "failed"
)

const (
	DeployActionDeploy   = "deploy"
	DeployActionUpdate   = "update"
	DeployActionUndeploy = "undeploy"
)

const (
	DeployFailureRejected      = "rejected"
	DeployFailureInvalidConfig = "invalid_config"
	DeployFailureRuntimeError  = "runtime_error"
)

const (
	DisplayStatusDeployed     = "deployed"
	DisplayStatusNotDeployed  = "not_deployed"
	DisplayStatusDeployFailed = "deploy_failed"
)

type RuntimeStatus = platpkg.RuntimeStatus

type Problem = platpkg.Problem

const (
	RuntimeStatusRunning      = platpkg.RuntimeStatusRunning
	RuntimeStatusDegraded     = platpkg.RuntimeStatusDegraded
	RuntimeStatusStarting     = platpkg.RuntimeStatusStarting
	RuntimeStatusDisconnected = platpkg.RuntimeStatusDisconnected
	RuntimeStatusStopped      = platpkg.RuntimeStatusStopped
)

func RunningStatus(problems []Problem) RuntimeStatus { return platpkg.RunningStatus(problems) }

func StartingStatus() RuntimeStatus { return platpkg.StartingStatus() }

func DisconnectedStatus() RuntimeStatus { return platpkg.DisconnectedStatus() }

func StoppedStatus() RuntimeStatus { return platpkg.StoppedStatus() }

type CreateAgentDeploymentRequest struct {
	AgentID       string `json:"agentId" validate:"required,uuid"`
	Action        string `json:"action" validate:"required,oneof=deploy update undeploy"`
	SkipIfCurrent bool   `json:"skipIfCurrent,omitempty"`
}

type AgentDeploymentResponse struct {
	ID            string    `json:"id"`
	AgentID       string    `json:"agentId"`
	Status        string    `json:"status"`
	Action        string    `json:"action"`
	ErrorMessage  string    `json:"errorMessage,omitempty"`
	CreatedBy     string    `json:"createdBy"`
	CreatedTime   Timestamp `json:"createdAt"`
	UpdatedTime   Timestamp `json:"updatedAt"`
	FailureReason string    `json:"-"`
	ErrorID       string    `json:"-"`
}

type CreateGatewayDeploymentRequest struct {
	GatewayID string `json:"gatewayId" validate:"required,uuid"`
	Action    string `json:"action" validate:"required,oneof=deploy update undeploy"`
}

type GatewayDeploymentResponse struct {
	ID            string    `json:"id"`
	GatewayID     string    `json:"gatewayId"`
	Status        string    `json:"status"`
	Action        string    `json:"action"`
	ErrorMessage  string    `json:"errorMessage,omitempty"`
	CreatedBy     string    `json:"createdBy"`
	UpdatedBy     string    `json:"updatedBy"`
	CreatedTime   Timestamp `json:"createdAt"`
	UpdatedTime   Timestamp `json:"updatedAt"`
	FailureReason string    `json:"-"`
	ErrorID       string    `json:"-"`
}
