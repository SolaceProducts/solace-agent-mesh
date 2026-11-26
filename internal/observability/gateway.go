// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "github.com/SolaceProducts/solace-agent-mesh/internal/runtime"

func NewGatewayOutboundMonitor(gatewayName, op string) *Monitor {
	return newMonitor(
		runtime.MetricProvider(),
		metricOutboundRequestDuration,
		classifyRemoteError,
		"service.peer.name", gatewayName,
		"operation.name", op,
	)
}
