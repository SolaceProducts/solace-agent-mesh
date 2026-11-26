// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestWebuiSettingsChangePublisher_PublishesAnEmptyPayloadOnTheUpdateTopic(t *testing.T) {
	t.Parallel()
	brk := connectedDevBroker(t)
	topic := a2a.WebuiSettingsUpdateTopic("default")
	q := modelTopicCollector(t, brk, topic)

	relay := NewWebuiSettingsChangePublisher(brk, "default")
	if err := relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{EventType: webuiSettingsOutboxChanged, AggregateID: webuiSettingsOutboxAggregateID, Payload: "{}"}); err != nil {
		t.Fatalf("PublishOutboxEvent: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("Receive on %s: %v", topic, err)
	}
	if len(msg.Payload) != 0 {
		t.Errorf("payload = %q, want empty: a GWE built before the outbox decodes \"{}\" as a row with no overrides", msg.Payload)
	}
}

func TestWebuiSettingsChangePublisher_RejectsAnUnknownEventType(t *testing.T) {
	t.Parallel()
	relay := NewWebuiSettingsChangePublisher(connectedDevBroker(t), "default")
	if err := relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{EventType: webuiSettingsOutboxPrefix + "other"}); err == nil {
		t.Fatal("an unknown webui settings event type was reported published")
	}
}

func TestWebuiSettingsChangePublisher_ReturnsAFailedPublishSoTheOutboxRetries(t *testing.T) {
	t.Parallel()
	relay := NewWebuiSettingsChangePublisher(broker.NewDevBroker(), "default")
	if err := relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{EventType: webuiSettingsOutboxChanged}); err == nil {
		t.Fatal("a publish to a disconnected broker was reported published")
	}
}

func TestBuildOutboxRoutes_RoutesWebuiSettingsEvents(t *testing.T) {
	t.Parallel()
	relay := NewWebuiSettingsChangePublisher(broker.NewDevBroker(), "default")
	routes := buildOutboxRoutes(nil, nil, nil, nil, relay)
	if len(routes) != 1 || routes[0].Prefix != webuiSettingsOutboxPrefix || routes[0].Publisher != relay {
		t.Errorf("routes = %+v, want one %s route to the webui settings relay", routes, webuiSettingsOutboxPrefix)
	}
}
