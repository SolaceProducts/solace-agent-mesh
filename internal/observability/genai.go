// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "github.com/SolaceProducts/solace-agent-mesh/internal/runtime"

const (
	metricGenAIOperationDuration = "gen_ai.client.operation.duration"
	metricGenAITTFTDuration      = "gen_ai.client.operation.ttft.duration"
)

func NewGenAIMonitor(metrics runtime.Metrics, model string) *Monitor {
	return newMonitor(
		metrics,
		metricGenAIOperationDuration,
		classifyError,
		"gen_ai.request.model", model,
	)
}

func NewGenAITTFTMonitor(metrics runtime.Metrics, model string) *Monitor {
	return newMonitor(
		metrics,
		metricGenAITTFTDuration,
		classifyError,
		"gen_ai.request.model", model,
	)
}
