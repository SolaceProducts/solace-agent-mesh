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

const (
	webuiSettingsOutboxPrefix  = "webui_settings."
	webuiSettingsOutboxChanged = webuiSettingsOutboxPrefix + "changed"

	webuiSettingsOutboxAggregateID = "00000000-0000-0000-0000-000000000000"
)

type WebuiSettingsChangePublisher struct {
	broker    broker.Broker
	namespace string
}

func NewWebuiSettingsChangePublisher(brk broker.Broker, namespace string) *WebuiSettingsChangePublisher {
	return &WebuiSettingsChangePublisher{broker: brk, namespace: namespace}
}

func (p *WebuiSettingsChangePublisher) PublishOutboxEvent(ctx context.Context, event store.OutboxEvent) error {
	if event.EventType != webuiSettingsOutboxChanged {
		return fmt.Errorf("unknown webui settings outbox event type %q", event.EventType)
	}
	topic := a2a.WebuiSettingsUpdateTopic(p.namespace)
	if err := p.broker.PublishGuaranteed(ctx, topic, &broker.Message{Topic: topic}); err != nil {
		return fmt.Errorf("publish %s to %s: %w", event.EventType, topic, err)
	}
	return nil
}

var _ platpkg.OutboxPublisher = (*WebuiSettingsChangePublisher)(nil)
