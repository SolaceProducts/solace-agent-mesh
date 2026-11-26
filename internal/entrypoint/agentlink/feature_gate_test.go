// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestInit_RefusesWhileFeatureFlagOff(t *testing.T) {
	cases := []struct {
		name string
		env  string
		reg  bool
	}{
		{name: "no registry"},
		{name: "flag turned off", env: "false", reg: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(features.EnvVarName(features.KeyAgentLinkEntrypoint), tc.env)
			var opts []runtime.Option
			if tc.reg {
				catalog := features.NewRegistry()
				if err := features.LoadFlags(catalog); err != nil {
					t.Fatal(err)
				}
				opts = append(opts, runtime.WithFeatureRegistry(catalog))
			}
			brk := broker.NewDevBroker()
			if err := brk.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			adapter, err := New(map[string]any{"agents": []any{map[string]any{"name": "claims-assistant", "token_sha256": digest(token)}}})
			if err != nil {
				t.Fatal(err)
			}
			mounted := false
			adapter.SetOnHandlerReady(func(http.Handler) { mounted = true })
			core := entrypoint.NewGatewayCore(adapter, map[string]any{"namespace": testNS, "gateway_id": testGW})
			core.SetServices(runtime.NewRuntime(brk, nil, opts...).Services())

			if err := core.Init(context.Background(), nil); !errors.Is(err, errFeatureDisabled) {
				t.Fatalf("Init = %v, want errFeatureDisabled", err)
			}
			if err := core.Start(context.Background()); !errors.Is(err, errFeatureDisabled) {
				t.Fatalf("Start = %v, want errFeatureDisabled", err)
			}
			if mounted {
				t.Fatal("the connect route was mounted while the flag is off")
			}
		})
	}
}

func TestFeatureFlagIsEarlyAccessAndOnByDefault(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyAgentLinkEntrypoint), "")
	catalog := features.NewRegistry()
	if err := features.LoadFlags(catalog); err != nil {
		t.Fatal(err)
	}
	def, ok := catalog.Get(features.KeyAgentLinkEntrypoint)
	if !ok {
		t.Fatal("the agent link flag is not in the catalog")
	}
	if def.ReleasePhase != features.PhaseEarlyAccess || !catalog.IsEnabled(features.KeyAgentLinkEntrypoint) {
		t.Fatalf("flag = %+v enabled=%v, want early_access and enabled", def, catalog.IsEnabled(features.KeyAgentLinkEntrypoint))
	}
}
