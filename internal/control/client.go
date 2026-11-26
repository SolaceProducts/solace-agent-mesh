// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const DefaultTimeout = 30 * time.Second

type Client struct {
	broker         broker.Broker
	namespace      string
	timeout        time.Duration
	userProperties map[string]string
}

type ClientOption func(*Client)

func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.timeout = d }
}

func WithUserProperties(props map[string]string) ClientOption {
	return func(c *Client) { c.userProperties = props }
}

func NewClient(brk broker.Broker, namespace string, opts ...ClientOption) *Client {
	c := &Client{
		broker:    brk,
		namespace: strings.TrimRight(namespace, "/"),
		timeout:   DefaultTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

const (
	TargetProcessProperty    = "sam_target_process"
	ResponderProcessProperty = "sam_process_id"
)

func (c *Client) Custom(ctx context.Context, name, method, customPath string, body json.RawMessage) (json.RawMessage, error) {
	topic := a2a.ControlAppCustomTopic(c.namespace, name, method, customPath)
	return c.doRequest(ctx, topic, body, "")
}

func (c *Client) CustomTo(ctx context.Context, processID, name, method, customPath string, body json.RawMessage) (json.RawMessage, error) {
	topic := a2a.ControlAppCustomTopic(c.namespace, name, method, customPath)
	return c.doRequest(ctx, topic, body, processID)
}

func (c *Client) newRequest(body json.RawMessage, targetProcess string) (*broker.Message, string) {
	correlationID := idgen.New()
	replyTo := fmt.Sprintf("%s/sam/v1/control/reply/%s", c.namespace, correlationID)
	props := c.userProperties
	if targetProcess != "" {
		props = make(map[string]string, len(c.userProperties)+1)
		for k, v := range c.userProperties {
			props[k] = v
		}
		props[TargetProcessProperty] = targetProcess
	}
	return &broker.Message{
		Payload:        body,
		UserProperties: props,
		ReplyTo:        replyTo,
		CorrelationID:  correlationID,
	}, replyTo
}

func (c *Client) doRequest(ctx context.Context, topic string, body json.RawMessage, targetProcess string) (json.RawMessage, error) {
	msg, replyTo := c.newRequest(body, targetProcess)
	if targetProcess == "" {
		resp, err := c.broker.Request(ctx, topic, msg, c.timeout)
		if err != nil {
			return nil, requestFailed(err)
		}
		return decodeResponse(resp.Payload)
	}
	return c.requestFrom(ctx, topic, msg, replyTo, targetProcess)
}

func (c *Client) requestFrom(ctx context.Context, topic string, msg *broker.Message, replyTo, targetProcess string) (json.RawMessage, error) {
	queue, err := c.broker.CreateDirectSubscription()
	if err != nil {
		return nil, requestFailed(err)
	}
	defer func() { _ = c.broker.DeleteQueue(queue.Name()) }()
	if err := c.broker.Subscribe(queue, replyTo); err != nil {
		return nil, requestFailed(err)
	}
	if err := c.broker.Publish(ctx, topic, msg); err != nil {
		return nil, requestFailed(err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	for {
		resp, err := c.broker.Receive(waitCtx, queue)
		if err != nil {
			return nil, requestFailed(err)
		}
		if resp == nil || (resp.CorrelationID != "" && resp.CorrelationID != msg.CorrelationID) || resp.UserProperties[ResponderProcessProperty] != targetProcess {
			continue
		}
		return decodeResponse(resp.Payload)
	}
}

func requestFailed(err error) error {
	return fmt.Errorf("control request failed: %w", err)
}

func decodeResponse(payload []byte) (json.RawMessage, error) {
	var envelope Response
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("invalid control response: %w", err)
	}
	if envelope.Error != nil {
		return nil, &ClientError{
			Code:    envelope.Error.Code,
			Message: envelope.Error.Message,
		}
	}
	result, err := json.Marshal(envelope.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return result, nil
}

type ClientError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("control error %d: %s", e.Code, e.Message)
}

func IsAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "already exists")
}

const DeploymentIDField = "deployment_id"
