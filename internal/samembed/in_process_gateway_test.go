// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samembed

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestPlatformRoutesGetTheGatewayHandedToTheProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		gateway *entrypoint.GatewayExecutor
	}{
		{name: "a handed gateway reaches the routes", gateway: &entrypoint.GatewayExecutor{}},
		{name: "no gateway leaves the routes without one", gateway: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &provider{}
			p.SetInProcessGateway(tc.gateway)
			p.PlatformExtensions(nil, discardLogger())
			if p.routeDep.InProcessGateway != tc.gateway {
				t.Errorf("routes got gateway %p, want %p", p.routeDep.InProcessGateway, tc.gateway)
			}
		})
	}
}
