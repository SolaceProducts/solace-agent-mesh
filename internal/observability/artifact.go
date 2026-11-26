// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "github.com/SolaceProducts/solace-agent-mesh/internal/runtime"

const (
	ArtifactOpSave   = "save"
	ArtifactOpLoad   = "load"
	ArtifactOpDelete = "delete"
	ArtifactOpList   = "list"
)

const metricOutboundRequestDuration = "outbound.request.duration"

func NewArtifactMonitor(op string) *Monitor {
	return newMonitor(
		runtime.MetricProvider(),
		metricOutboundRequestDuration,
		classifyRemoteError,
		"service.peer.name", "artifact_service",
		"operation.name", op,
	)
}
