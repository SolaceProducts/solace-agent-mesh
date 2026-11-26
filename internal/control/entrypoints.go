// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import "time"

const (
	GatewayStatusInterval = 2 * time.Second
	GatewayStatusExpiry   = 3 * GatewayStatusInterval
)

type EntrypointApply struct {
	Name         string         `json:"name"`
	DeploymentID string         `json:"deployment_id"`
	Undeploy     bool           `json:"undeploy,omitempty"`
	AppConfig    map[string]any `json:"app_config,omitempty"`
	Revision     string         `json:"revision,omitempty"`
	Attempt      string         `json:"attempt,omitempty"`
}

type EntrypointStatus struct {
	Name         string `json:"name"`
	DeploymentID string `json:"deployment_id"`
	Revision     string `json:"revision,omitempty"`
	Attempt      string `json:"attempt,omitempty"`
	Error        string `json:"error,omitempty"`
}

type GatewayStatus struct {
	ProcessID   string             `json:"process_id"`
	Leaving     bool               `json:"leaving,omitempty"`
	Entrypoints []EntrypointStatus `json:"entrypoints,omitempty"`
}

type TargetState int

const (
	TargetUnknown TargetState = iota
	TargetDeployed
	TargetNotDeployed
)

type Target struct {
	State    TargetState
	CardName string
}
