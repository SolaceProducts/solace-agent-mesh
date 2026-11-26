// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var errUserEventIncomplete = errors.New("user event names no origin, event or recipient")

type userEventPublisher struct {
	brk    broker.Broker
	topic  string
	origin string
	sse    *SSEManager
	logger *slog.Logger
}

func newUserEventPublisher(brk broker.Broker, namespace, processOrigin string, sse *SSEManager, logger *slog.Logger) *userEventPublisher {
	return &userEventPublisher{brk: brk, topic: a2a.UserEventTopic(namespace), origin: processOrigin, sse: sse, logger: logger}
}

func (p *userEventPublisher) SendUserNotification(userID, eventType string, data any) error {
	serialized, err := json.Marshal(data)
	if err != nil {
		return err
	}
	keys := []string{userID}
	p.sse.sendUserNotificationJSON(keys, eventType, string(serialized))
	payload, err := json.Marshal(a2a.UserEvent{Origin: p.origin, Keys: keys, Event: eventType, Data: serialized})
	if err != nil {
		return err
	}
	if err := p.brk.Publish(context.Background(), p.topic, &broker.Message{Payload: payload}); err != nil {
		p.logger.Warn("user event publish failed; streams on other gateways miss it until their next refresh",
			"eventType", eventType, "error", err)
	}
	return nil
}
