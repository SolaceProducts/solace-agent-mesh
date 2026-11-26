// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/agentlink"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func agentLinkGateway(t *testing.T, agentKeys map[string]any) (*entrypoint.GatewayCore, entrypoint.GatewayAdapter) {
	t.Helper()
	agent := map[string]any{"name": "claims-assistant", "token_sha256": strings.Repeat("ab", 32)}
	for k, v := range agentKeys {
		agent[k] = v
	}
	appConfig := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "agent-link",
		"gateway_adapter": map[string]any{
			"type":           "agent_link",
			"adapter_config": map[string]any{"agents": []any{agent}},
		},
	}
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	flags, err := LoadFeatureRegistry(slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	core, err := DefaultGatewayFactory(appConfig, runtime.NewRuntime(dev, nil, runtime.WithFeatureRegistry(flags)).Services())
	if err != nil {
		t.Fatal(err)
	}
	adapter := core.Adapter()
	return core, adapter
}

func TestAnAgentLinkGatewayWithAConfigErrorRefusesToStartInsteadOfServingTheWebGateway(t *testing.T) {
	core, adapter := agentLinkGateway(t, map[string]any{"enabled": "off"})
	if _, isLink := adapter.(*agentlink.Adapter); !isLink {
		t.Fatalf("the gateway made a %T for a refused agent_link configuration", adapter)
	}

	err := core.Init(t.Context(), nil)
	if err == nil {
		t.Fatal("a gateway with a refused agent_link configuration initialized")
	}
	if !strings.Contains(err.Error(), "enabled") || !strings.Contains(err.Error(), "claims-assistant") {
		t.Fatalf("Init returned %v, want the config error naming the agent and the key", err)
	}
	if startErr := adapter.Start(t.Context()); startErr == nil {
		t.Fatal("the adapter of a refused configuration started")
	}
	if stopErr := adapter.Stop(t.Context()); stopErr != nil {
		t.Fatalf("stopping the adapter that never started: %v", stopErr)
	}
}

func TestAnAgentLinkGatewayWithAValidConfigInitializes(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyAgentLinkEntrypoint), "")
	core, adapter := agentLinkGateway(t, nil)
	if _, isLink := adapter.(*agentlink.Adapter); !isLink {
		t.Fatalf("the gateway made a %T for an agent_link configuration", adapter)
	}
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("a gateway with a valid agent_link configuration did not initialize: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
}

func TestAnAgentLinkGatewayRefusesToStartWithTheFlagOff(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyAgentLinkEntrypoint), "false")
	core, adapter := agentLinkGateway(t, nil)
	if _, isLink := adapter.(*agentlink.Adapter); !isLink {
		t.Fatalf("the gateway made a %T for an agent_link configuration", adapter)
	}
	err := core.Init(t.Context(), nil)
	if err == nil {
		t.Fatal("an agent_link gateway initialized with the flag off")
	}
	if !strings.Contains(err.Error(), features.EnvVarName(features.KeyAgentLinkEntrypoint)) {
		t.Fatalf("Init returned %v, want the refusal naming the flag's env var", err)
	}
}
