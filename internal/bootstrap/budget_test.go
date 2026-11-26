// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestBootstrapOptions_GiveTheRuntimeAGateThePlatformsBroadcastsReach(t *testing.T) {
	cfg, brk := testBootstrapConfig(t)
	_, opts, err := Bootstrap(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil, opts...)
	if err = rt.Init(t.Context()); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err = rt.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) }) //nolint:contextcheck

	budgettest.BroadcastMeshBlocked(t, brk, cfg.Namespace, time.Now().Add(time.Hour))
	budgettest.WaitForMeshRefusal(t, "the runtime's gate", rt.Services().BudgetGate())
}
