// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func (g *GatewayCore) relayedSurface() string {
	relay, ok := g.adapter.(RelayedSurfaceAdapter)
	if !ok {
		return ""
	}
	surface := relay.RelayedSurface()
	if !slices.Contains(httputil.RelayedSurfaces, surface) {
		return ""
	}
	return surface
}
