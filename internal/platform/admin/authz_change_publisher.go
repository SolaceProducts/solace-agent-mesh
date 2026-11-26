// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type AuthzChangePublisher struct {
	broker    broker.Broker
	namespace string
}

func NewAuthzChangePublisher(brk broker.Broker, namespace string) *AuthzChangePublisher {
	return &AuthzChangePublisher{broker: brk, namespace: namespace}
}

func (p *AuthzChangePublisher) PublishOutboxEvent(ctx context.Context, event store.OutboxEvent) error {
	var topic string
	switch event.EventType {
	case authzOutboxRolesChanged:
		topic = a2a.AuthzRolesChangedTopic(p.namespace)
	case authzOutboxUsersChanged:
		topic = a2a.AuthzUsersChangedTopic(p.namespace)
	default:
		return fmt.Errorf("unknown authz outbox event type %q", event.EventType)
	}
	if err := p.broker.PublishGuaranteed(ctx, topic, &broker.Message{Topic: topic, Payload: []byte("{}")}); err != nil {
		return fmt.Errorf("publish %s to %s: %w", event.EventType, topic, err)
	}
	return nil
}

var _ platpkg.OutboxPublisher = (*AuthzChangePublisher)(nil)
