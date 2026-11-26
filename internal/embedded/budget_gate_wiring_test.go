// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const budgetWatchComponent = "budget-watch"

func budgetWiringApps() EmbeddedApps {
	return EmbeddedApps{Gateway: []samapp.AppEntry{{Name: "gw", AppConfig: map[string]any{"gateway_id": "gw-1"}}}}
}

func TestEmbedded_TheGatewayAndPlatformRefuseOnABroadcastBudgetState(t *testing.T) {
	for name, ext := range map[string]ExtensionProvider{
		"without extensions":                         nil,
		"with extensions that add no budget options": noopExtensionProvider{},
	} {
		t.Run(name, func(t *testing.T) { assertRefusesOnBroadcastBudgetState(t, Config{Extensions: ext}) })
	}
}

func assertRefusesOnBroadcastBudgetState(t *testing.T, cfg Config) {
	inst := newTestInstance(cfg)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) }) //nolint:contextcheck
	inst.sharedBroker = brk
	classified := budgetWiringApps()

	if err := inst.buildRuntime(t.Context(), map[string]any{}, classified, "test-ns"); err != nil {
		t.Fatalf("buildRuntime: %v", err)
	}
	t.Cleanup(inst.bindings.Close)
	startBudgetWatch(t, inst)
	gwe, err := inst.registerGatewayExecutor(classified)
	if err != nil {
		t.Fatalf("registerGatewayExecutor: %v", err)
	}
	core, _, _ := inst.buildPlatformCore(classified, "test-ns", 0, gwe)

	budgettest.BroadcastMeshBlocked(t, brk, "test-ns", time.Now().Add(time.Hour))

	budgettest.WaitForMeshRefusal(t, "the runtime", inst.rt.Services().BudgetGate())
	if n := len(gwe.Gateways()); n != 1 {
		t.Fatalf("gateways = %d, want 1", n)
	}
	for _, gw := range gwe.Gateways() {
		budgettest.WaitForMeshRefusal(t, "gateway "+gw.GatewayID(), gw.BudgetGate())
	}
	budgettest.WaitForMeshRefusal(t, "the platform", core.BudgetGate())
}

func startBudgetWatch(t *testing.T, inst *Instance) {
	t.Helper()
	for _, c := range inst.rt.Components() {
		if c.Name != budgetWatchComponent {
			continue
		}
		if err := c.Component.Init(t.Context(), nil); err != nil {
			t.Fatalf("init %s: %v", budgetWatchComponent, err)
		}
		if err := c.Component.Start(t.Context()); err != nil {
			t.Fatalf("start %s: %v", budgetWatchComponent, err)
		}
		t.Cleanup(func() { _ = c.Component.Stop(context.Background()) }) //nolint:contextcheck
		return
	}
	t.Fatalf("the runtime registers no %s", budgetWatchComponent)
}
