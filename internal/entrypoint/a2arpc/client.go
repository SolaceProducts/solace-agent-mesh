// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2arpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const DefaultTimeout = 60 * time.Second

type RPCError struct {
	Code    int
	Message string
	Data    any
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("a2arpc: code=%d %s", e.Code, e.Message)
}

type Client struct {
	broker    broker.Broker
	namespace string
	gatewayID string
	timeout   time.Duration
}

type response struct {
	result json.RawMessage
	err    error
}

type Config struct {
	Broker    broker.Broker
	Namespace string
	GatewayID string
	Timeout   time.Duration
}

func New(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		broker:    cfg.Broker,
		namespace: cfg.Namespace,
		gatewayID: cfg.GatewayID,
		timeout:   timeout,
	}
}

func (c *Client) Call(ctx context.Context, agentName, userID, method, extensionURI string, params any) (json.RawMessage, error) {
	if c.broker == nil {
		return nil, fmt.Errorf("a2arpc: broker not configured")
	}
	if agentName == "" {
		return nil, fmt.Errorf("a2arpc: agentName is required")
	}
	if method == "" {
		return nil, fmt.Errorf("a2arpc: method is required")
	}
	if extensionURI == "" {
		return nil, fmt.Errorf("a2arpc: extensionURI is required")
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	rpcID := idgen.New()
	req := &a2a.Request{
		JSONRPC: "2.0",
		ID:      rpcID,
		Method:  method,
		Params:  params,
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("a2arpc: marshal request: %w", err)
	}

	replyTo := a2a.GatewayRPCReplyTopic(c.namespace, rpcID)
	queue, err := c.broker.CreateDirectSubscription()
	if err != nil {
		return nil, fmt.Errorf("a2arpc: create reply subscription: %w", err)
	}
	defer func() { _ = c.broker.DeleteQueue(queue.Name()) }()
	if err := c.broker.Subscribe(queue, replyTo); err != nil {
		return nil, fmt.Errorf("a2arpc: subscribe reply: %w", err)
	}
	userProps := a2a.EncodeUserProperties(&a2a.UserProperties{
		Timestamp:     time.Now().UnixMilli(),
		ClientID:      c.gatewayID,
		ReplyTo:       replyTo,
		A2AExtensions: extensionURI,
		UserID:        userID,
	})

	topic := a2a.AgentRequestTopic(c.namespace, agentName)
	if pubErr := c.broker.PublishGuaranteed(ctx, topic, &broker.Message{
		Payload:        payload,
		UserProperties: userProps,
	}); pubErr != nil {
		return nil, fmt.Errorf("a2arpc: publish: %w", pubErr)
	}

	for {
		msg, err := c.broker.Receive(ctx, queue)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("a2arpc: %w", ctx.Err())
		}
		if errors.Is(err, broker.ErrQueueNotFound) {
			return nil, fmt.Errorf("a2arpc: %w", err)
		}
		if err != nil || msg == nil {
			continue
		}
		r := parseResponse(msg.Payload)
		if r.err != nil {
			return nil, r.err
		}
		return r.result, nil
	}
}

func parseResponse(payload []byte) *response {
	var resp a2a.Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		return &response{err: fmt.Errorf("a2arpc: unmarshal response: %w", err)}
	}
	if resp.Error != nil {
		return &response{err: &RPCError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
			Data:    resp.Error.Data,
		}}
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		return &response{err: fmt.Errorf("a2arpc: re-marshal result: %w", err)}
	}
	return &response{result: raw}
}
