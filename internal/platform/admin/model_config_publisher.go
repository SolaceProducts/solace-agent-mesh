// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ModelConfigPublisher struct {
	broker             broker.Broker
	namespace          string
	servesPythonAgents bool
	logger             *slog.Logger
}

func NewModelConfigPublisher(brk broker.Broker, namespace string, servesPythonAgents bool, logger *slog.Logger) *ModelConfigPublisher {
	return &ModelConfigPublisher{broker: brk, namespace: namespace, servesPythonAgents: servesPythonAgents, logger: logger}
}

func (p *ModelConfigPublisher) PublishUpdate(ctx context.Context, modelID, alias, previousAlias string, rawConfig map[string]any) error {
	payload := map[string]any{"model_id": modelID, "alias": alias}
	if p.servesPythonAgents {
		payload["model_config"] = samapp.ForPythonAgent(rawConfig)
	}
	if previousAlias != "" {
		payload["previous_alias"] = previousAlias
	}
	return p.publish(ctx, "update", payload, a2a.ModelConfigUpdateTopic(p.namespace, modelID), a2a.ModelConfigUpdateTopic(p.namespace, alias))
}

func (p *ModelConfigPublisher) PublishDelete(ctx context.Context, modelID, alias string) error {
	return p.publish(ctx, "delete", map[string]any{"model_config": nil, "model_id": modelID, "alias": alias},
		a2a.ModelConfigUpdateTopic(p.namespace, modelID), a2a.ModelConfigUpdateTopic(p.namespace, alias))
}

func (p *ModelConfigPublisher) publishRenamedAndDeleted(ctx context.Context, modelID, alias, previousAlias string) error {
	return p.publish(ctx, "delete", map[string]any{"model_config": nil, "model_id": modelID, "alias": alias, "previous_alias": previousAlias},
		a2a.ModelConfigUpdateTopic(p.namespace, modelID))
}

func (p *ModelConfigPublisher) publish(ctx context.Context, change string, payload map[string]any, topics ...string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	for _, topic := range topics {
		msg := &broker.Message{
			Topic:   topic,
			Payload: data,
		}
		if err := p.broker.PublishGuaranteed(ctx, topic, msg); err != nil {
			return fmt.Errorf("publish model config %s to %s: %w", change, topic, err)
		}
	}
	p.logger.Debug("published model config change", "change", change, "modelID", payload["model_id"], "alias", payload["alias"])
	return nil
}

type ModelChangePublisher struct {
	svc       *ModelConfigService
	publisher *ModelConfigPublisher
}

func NewModelChangePublisher(svc *ModelConfigService, publisher *ModelConfigPublisher) *ModelChangePublisher {
	return &ModelChangePublisher{svc: svc, publisher: publisher}
}

func (p *ModelChangePublisher) PublishOutboxEvent(ctx context.Context, event store.OutboxEvent) error {
	var payload modelOutboxPayload
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return fmt.Errorf("decode %s payload for model %s: %w", event.EventType, event.AggregateID, err)
	}
	if event.EventType == modelOutboxEventDeleted {
		return p.publisher.PublishDelete(ctx, event.AggregateID, payload.Alias)
	}
	rawConfig, err := p.svc.GetByIDRaw(ctx, event.AggregateID)
	if err != nil {
		return fmt.Errorf("read model %s for %s: %w", event.AggregateID, event.EventType, err)
	}
	if rawConfig == nil && payload.PreviousAlias != "" {
		return p.publisher.publishRenamedAndDeleted(ctx, event.AggregateID, payload.Alias, payload.PreviousAlias)
	}
	if rawConfig == nil {
		return nil
	}
	return p.publisher.PublishUpdate(ctx, event.AggregateID, payload.Alias, payload.PreviousAlias, rawConfig)
}

var _ platpkg.OutboxPublisher = (*ModelChangePublisher)(nil)
