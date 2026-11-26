// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type RequestReply struct {
	broker      broker.Broker
	replyPrefix string
}

func NewRequestReply(b broker.Broker, replyPrefix string) *RequestReply {
	if replyPrefix == "" {
		replyPrefix = "sam/reply"
	}
	return &RequestReply{
		broker:      b,
		replyPrefix: replyPrefix,
	}
}

func (rr *RequestReply) Request(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) {
	if msg.CorrelationID == "" {
		msg.CorrelationID = idgen.New()
	}

	if msg.ReplyTo == "" {
		msg.ReplyTo = fmt.Sprintf("%s/%s", rr.replyPrefix, msg.CorrelationID)
	}

	brokerMsg := runtimeToBrokerMessage(msg)
	reply, err := rr.broker.Request(ctx, topic, brokerMsg, timeout)
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return nil, nil
	}
	return brokerToRuntimeMessage(reply), nil
}

func (rr *RequestReply) Publish(ctx context.Context, topic string, msg *Message) error {
	return rr.broker.Publish(ctx, topic, runtimeToBrokerMessage(msg))
}

func (rr *RequestReply) Reply(ctx context.Context, request, response *Message) error {
	if request.ReplyTo == "" {
		return fmt.Errorf("request has no ReplyTo topic")
	}
	response.CorrelationID = request.CorrelationID
	return rr.broker.Publish(ctx, request.ReplyTo, runtimeToBrokerMessage(response))
}

func runtimeToBrokerMessage(m *Message) *broker.Message {
	return &broker.Message{
		ID:             m.ID,
		Topic:          m.Topic,
		Payload:        m.Payload,
		UserProperties: m.UserProperties,
		ReplyTo:        m.ReplyTo,
		CorrelationID:  m.CorrelationID,
	}
}

func brokerToRuntimeMessage(m *broker.Message) *Message {
	return &Message{
		ID:             m.ID,
		Topic:          m.Topic,
		Payload:        m.Payload,
		UserProperties: m.UserProperties,
		ReplyTo:        m.ReplyTo,
		CorrelationID:  m.CorrelationID,
		ReceivedAt:     time.Now(),
		ackFunc:        m.Ack,
		nackFunc:       m.Nack,
	}
}
