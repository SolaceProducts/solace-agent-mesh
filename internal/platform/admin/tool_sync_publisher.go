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
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type syncNotification struct {
	MessageID string `json:"messageId"`
	Category  string `json:"category"`
	Reason    string `json:"reason"`
	Name      string `json:"name"`
}

type ToolSyncPublisher struct {
	broker    broker.Broker
	namespace string
	logger    *slog.Logger
}

func NewToolSyncPublisher(brk broker.Broker, namespace string, logger *slog.Logger) *ToolSyncPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &ToolSyncPublisher{broker: brk, namespace: namespace, logger: logger}
}

func (p *ToolSyncPublisher) PublishOutboxEvent(ctx context.Context, event store.OutboxEvent) error {
	if p.broker == nil {
		return fmt.Errorf("tool sync publisher: broker not configured")
	}

	category, ok := categoryForEventType(event.EventType)
	if !ok {
		p.logger.Warn("tool sync publisher: ignoring event with unknown prefix",
			"eventID", event.ID, "eventType", event.EventType)
		return nil
	}

	name := nameFromOutboxPayload(event.Payload)
	if name == "" {
		name = event.AggregateID
	}

	payload, err := json.Marshal(syncNotification{
		MessageID: event.ID,
		Category:  category,
		Reason:    event.EventType,
		Name:      name,
	})
	if err != nil {
		return fmt.Errorf("tool sync publisher: marshal notification for event %s: %w", event.ID, err)
	}

	topic := a2a.SyncNotifyTopic(p.namespace, category)
	msg := &broker.Message{Payload: payload}
	if err := p.broker.PublishGuaranteed(ctx, topic, msg); err != nil {
		return fmt.Errorf("tool sync publisher: publish to %s: %w", topic, err)
	}

	p.logger.Debug("tool sync publisher: notification sent",
		"topic", topic, "eventType", event.EventType, "aggregateID", event.AggregateID, "name", name)
	return nil
}

func nameFromOutboxPayload(raw string) string {
	if raw == "" {
		return ""
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return ""
	}
	return p.Name
}

func categoryForEventType(eventType string) (string, bool) {
	switch {
	case strings.HasPrefix(eventType, "tool."):
		return "tools", true
	case strings.HasPrefix(eventType, "skill."):
		return "skills", true
	}
	return "", false
}

var _ platpkg.OutboxPublisher = (*ToolSyncPublisher)(nil)
