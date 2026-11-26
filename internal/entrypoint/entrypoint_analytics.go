// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func (g *GatewayCore) entrypointUsedEvent(task *a2a.Task, a2aErr *a2a.Error, occurredAt string) productanalytics.RawEvent {
	props := map[string]any{
		productanalytics.PropOutcome: runOutcome(task, a2aErr),
	}
	if entrypointType := g.entrypointType(); entrypointType != "" {
		props[productanalytics.PropEntrypointType] = entrypointType
	}
	return productanalytics.RawEvent{
		OccurredAt: occurredAt,
		Name:       productanalytics.EventEntrypointUsed,
		Properties: props,
	}
}

func (g *GatewayCore) entrypointType() string {
	ingress, ok := g.adapter.(MachineIngressAdapter)
	if !ok {
		return ""
	}
	entrypointType := ingress.EntrypointType()
	if !slices.Contains(taxonomy.GatewayTypes, entrypointType) {
		return ""
	}
	return entrypointType
}
