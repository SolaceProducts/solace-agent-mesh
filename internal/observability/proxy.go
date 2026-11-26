// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "github.com/SolaceProducts/solace-agent-mesh/internal/runtime"

const metricOperationDuration = "operation.duration"

func NewRemoteAgentProxyMonitor(agentName string) *Monitor {
	return newMonitor(
		runtime.MetricProvider(),
		metricOperationDuration,
		classifyA2AError,
		"type", "a2a_agent",
		"component.name", agentName,
		"operation.name", "forward_request",
	)
}
