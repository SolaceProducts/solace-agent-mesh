// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestAuthzChangePublisher_PublishesEachEventOnItsTopic(t *testing.T) {
	t.Parallel()
	brk := connectedDevBroker(t)
	relay := NewAuthzChangePublisher(brk, "default")
	for eventType, topic := range map[string]string{
		authzOutboxRolesChanged: a2a.AuthzRolesChangedTopic("default"),
		authzOutboxUsersChanged: a2a.AuthzUsersChangedTopic("default"),
	} {
		q := modelTopicCollector(t, brk, topic)
		if err := relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{EventType: eventType, AggregateID: "id", Payload: "{}"}); err != nil {
			t.Fatalf("%s: PublishOutboxEvent: %v", eventType, err)
		}
		if _, ok := receiveModelConfig(t, brk, q, 5*time.Second); !ok {
			t.Errorf("%s was not published on %s", eventType, topic)
		}
	}
}

func TestAuthzChangePublisher_RejectsAnUnknownEventType(t *testing.T) {
	t.Parallel()
	relay := NewAuthzChangePublisher(connectedDevBroker(t), "default")
	if err := relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{EventType: authzOutboxPrefix + "other"}); err == nil {
		t.Fatal("an unknown authz event type was reported published")
	}
}

func TestAuthzChangePublisher_ReturnsAFailedPublishSoTheOutboxRetries(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	relay := NewAuthzChangePublisher(brk, "default")
	if err := relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{EventType: authzOutboxRolesChanged}); err == nil {
		t.Fatal("a publish to a disconnected broker was reported published")
	}
}
