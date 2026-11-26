// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestRemoveExternalQueues_DeletesTheSAMHandlerQueuesOnTheEventBroker(t *testing.T) {
	dataBroker := &deprovisionTrackingBroker{DevBroker: broker.NewDevBroker()}
	var connectedTo string
	prev := removeHandlerBrokerFor
	removeHandlerBrokerFor = func(_ map[string]any, cfg *Config, _ string, _ *slog.Logger) (broker.Broker, error) {
		connectedTo = cfg.BrokerConfig.BrokerURL
		return dataBroker, nil
	}
	t.Cleanup(func() { removeHandlerBrokerFor = prev })

	appConfig := map[string]any{
		"namespace":    "ns/",
		"gateway_id":   "em-gw",
		"gateway_type": "event_mesh",
		"event_mesh_broker_config": map[string]any{
			"broker_url": "tcp://customer:55555",
		},
		"event_handlers": []any{
			map[string]any{"name": "orders", "subscriptions": []any{map[string]any{"topic": "orders/>"}}, "target_agent_name": "a"},
			map[string]any{"name": "own", "existing_queue_name": "customer-queue", "target_agent_name": "a"},
		},
	}
	if err := entrypoint.RemoveExternalQueues(t.Context(), appConfig); err != nil {
		t.Fatalf("RemoveExternalQueues: %v", err)
	}

	if connectedTo != "tcp://customer:55555" {
		t.Fatalf("connected to %q, want the customer's broker from the config", connectedTo)
	}
	got := dataBroker.names()
	if !slices.Contains(got, "ns/q/eventmesh/em-gw/orders") {
		t.Fatalf("deleted %v, want the SAM queue of rule orders", got)
	}
	if slices.Contains(got, "customer-queue") {
		t.Fatalf("deleted %v, which includes the customer's own queue", got)
	}
}

func TestRemoveExternalQueues_NothingForAnotherEntrypointType(t *testing.T) {
	if err := entrypoint.RemoveExternalQueues(t.Context(), map[string]any{"gateway_type": "slack"}); err != nil {
		t.Fatalf("RemoveExternalQueues: %v", err)
	}
}

func TestRemoveHandlerQueues_ReportsWhatStopsIt(t *testing.T) {
	handlers := []any{map[string]any{"name": "orders", "subscriptions": []any{map[string]any{"topic": "orders/>"}}, "target_agent_name": "a"}}
	t.Run("a config that does not parse", func(t *testing.T) {
		if err := removeHandlerQueues(t.Context(), map[string]any{"event_handlers": "not a list"}); err == nil {
			t.Error("no error for an unparsable config")
		}
	})
	t.Run("a dev broker with no address", func(t *testing.T) {
		t.Setenv("SAM_DEV_BROKER_HOST", "")
		t.Setenv("SAM_DEV_BROKER_PORT", "")
		err := removeHandlerQueues(t.Context(), map[string]any{
			"event_mesh_broker_config": map[string]any{"dev_mode": true},
			"event_handlers":           handlers,
		})
		if err == nil {
			t.Error("no error without a dev broker address")
		}
	})
	t.Run("an unreachable broker", func(t *testing.T) {
		prev := removeHandlerBrokerFor
		removeHandlerBrokerFor = func(map[string]any, *Config, string, *slog.Logger) (broker.Broker, error) {
			return &unreachableBroker{DevBroker: broker.NewDevBroker()}, nil
		}
		t.Cleanup(func() { removeHandlerBrokerFor = prev })
		err := removeHandlerQueues(t.Context(), map[string]any{
			"event_mesh_broker_config": map[string]any{"broker_url": "tcp://customer:55555"},
			"event_handlers":           handlers,
		})
		if err == nil {
			t.Error("no error for a broker that cannot be reached")
		}
	})
}

type unreachableBroker struct{ *broker.DevBroker }

func (*unreachableBroker) Connect(context.Context) error { return context.DeadlineExceeded }

func TestNewDataPlaneBroker_DevModeUsesTheTCPDevBroker(t *testing.T) {
	t.Setenv("SAM_DEV_BROKER_HOST", "127.0.0.1")
	t.Setenv("SAM_DEV_BROKER_PORT", "1")
	brk, err := newDataPlaneBroker(map[string]any{"event_mesh_broker_config": map[string]any{"dev_mode": true}}, &Config{}, "em-gw", slog.Default())
	if err != nil || brk == nil {
		t.Fatalf("newDataPlaneBroker = (%v, %v), want a dev broker client", brk, err)
	}
}
