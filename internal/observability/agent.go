// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "github.com/SolaceProducts/solace-agent-mesh/internal/runtime"

func NewAgentMonitor(metrics runtime.Metrics, agentName string) *Monitor {
	return newMonitor(
		metrics,
		metricOperationDuration,
		classifyError,
		"type", "agent",
		"component.name", agentName,
		"operation.name", "execute",
	)
}
