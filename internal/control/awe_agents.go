// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import "time"

const (
	AWEStatusInterval   = 2 * time.Second
	AWEStatusExpiry     = 3 * AWEStatusInterval
	AWEAgentsAnswerWait = 10 * time.Second
)

const FirstAskProperty = "sam_awe_first_ask"

type AgentApply struct {
	Name         string           `json:"name"`
	DeploymentID string           `json:"deployment_id"`
	Undeploy     bool             `json:"undeploy,omitempty"`
	Payloads     []map[string]any `json:"payloads,omitempty"`
}

type AgentsReply struct {
	Agents []AgentApply `json:"agents"`
}

type AgentsChanged struct {
	Name string `json:"name"`
}

type AgentStatus struct {
	Name         string `json:"name"`
	DeploymentID string `json:"deployment_id"`
	Error        string `json:"error,omitempty"`
}

type AWEStatus struct {
	ProcessID string        `json:"process_id"`
	Leaving   bool          `json:"leaving,omitempty"`
	Agents    []AgentStatus `json:"agents,omitempty"`
}
