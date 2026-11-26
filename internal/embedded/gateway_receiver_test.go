// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type receivingProvider struct {
	noopExtensionProvider
	gateway *entrypoint.GatewayExecutor
	calls   int
}

func (p *receivingProvider) SetInProcessGateway(gateway *entrypoint.GatewayExecutor) {
	p.gateway, p.calls = gateway, p.calls+1
}

func TestBuildPlatformCore_HandsTheInProcessGatewayToTheProvider(t *testing.T) {
	provider := &receivingProvider{}
	inst := newTestInstance(Config{Extensions: provider})
	setupMinimalRuntime(t, inst)
	classified := budgetWiringApps()
	gwe, err := inst.registerGatewayExecutor(classified)
	if err != nil {
		t.Fatalf("registerGatewayExecutor: %v", err)
	}

	inst.buildPlatformCore(classified, "test-ns", 0, gwe)
	if provider.calls != 1 || provider.gateway != gwe {
		t.Errorf("provider handed %p in %d calls, want the gateway executor %p once", provider.gateway, provider.calls, gwe)
	}

	provider.calls = 0
	inst.buildPlatformCore(classified, "test-ns", 0, nil)
	if provider.calls != 0 {
		t.Errorf("provider handed a gateway %d times with no gateway in the process", provider.calls)
	}
}
