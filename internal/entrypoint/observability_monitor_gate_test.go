// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestGatewayCore_ObservabilityMonitorGate(t *testing.T) {
	newCore := func(t *testing.T, monitorEnabled, flagOn bool) *GatewayCore {
		t.Helper()
		t.Setenv(features.EnvVarName(features.KeyObservabilityAgent), "")
		brk := broker.NewDevBroker()
		if err := brk.Connect(context.Background()); err != nil {
			t.Fatalf("connect broker: %v", err)
		}
		t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

		reg := features.NewRegistry()
		reg.Register(features.Definition{Key: features.KeyObservabilityAgent, Name: "observability", Default: flagOn})
		rt := runtime.NewRuntime(brk, nil, runtime.WithFeatureRegistry(reg))

		core := NewGatewayCore(&recordingAdapter{}, map[string]any{
			"namespace":             "test-ns",
			"gateway_id":            "gw-mon",
			"observability_monitor": map[string]any{"enabled": monitorEnabled},
		})
		core.SetServices(rt.Services())
		if err := core.Init(context.Background(), nil); err != nil {
			t.Fatalf("Init: %v", err)
		}
		t.Cleanup(func() { _ = core.Stop(context.Background()) })
		return core
	}

	if core := newCore(t, true, true); core.sentinelMonitor == nil {
		t.Error("monitor should be constructed when observability_monitor enabled and flag on")
	}
	if core := newCore(t, true, false); core.sentinelMonitor != nil {
		t.Error("monitor must not be constructed when observability_agent flag is off")
	}
	if core := newCore(t, false, true); core.sentinelMonitor != nil {
		t.Error("monitor must not be constructed when observability_monitor is disabled")
	}
}
