// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type TriggerPublisher struct {
	broker    broker.Broker
	namespace string
}

func NewTriggerPublisher(connectedBroker broker.Broker, namespace string) *TriggerPublisher {
	return &TriggerPublisher{broker: connectedBroker, namespace: strings.TrimRight(namespace, "/")}
}

func (p *TriggerPublisher) TriggerTopic() string {
	return p.namespace + "/eval/v1/run/trigger"
}

func (p *TriggerPublisher) PublishRunTrigger(ctx context.Context, runID string) error {
	return p.publishRunIDEvent(ctx, p.TriggerTopic(), runID)
}

func (p *TriggerPublisher) PublishGenerationTrigger(ctx context.Context, operationID string) error {
	body, err := json.Marshal(map[string]string{"generation_id": operationID})
	if err != nil {
		return fmt.Errorf("marshal generation trigger: %w", err)
	}
	return p.publish(ctx, p.TriggerTopic(), body)
}

func (p *TriggerPublisher) CancelTopic() string {
	return p.namespace + "/eval/v1/run/cancel"
}

func (p *TriggerPublisher) PublishCancel(ctx context.Context, runID string) error {
	return p.publishRunIDEvent(ctx, p.CancelTopic(), runID)
}

func (p *TriggerPublisher) publishRunIDEvent(ctx context.Context, topic, runID string) error {
	body, err := json.Marshal(map[string]string{"run_id": runID})
	if err != nil {
		return fmt.Errorf("trigger publisher: marshal: %w", err)
	}
	return p.publish(ctx, topic, body)
}

func (p *TriggerPublisher) publish(ctx context.Context, topic string, body []byte) error {
	if p.broker == nil {
		return fmt.Errorf("trigger publisher: broker not configured")
	}
	msg := &broker.Message{Topic: topic, Payload: body}
	if err := p.broker.Publish(ctx, topic, msg); err != nil {
		return fmt.Errorf("trigger publisher: publish: %w", err)
	}
	return nil
}
