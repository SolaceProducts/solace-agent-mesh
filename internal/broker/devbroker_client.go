// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

type DevBrokerClient struct {
	core   *DevBroker
	owner  *devOwner
	closed atomic.Bool
}

var errDevClientClosed = errors.New("devbroker: client disconnected")

func (b *DevBroker) NewClient() *DevBrokerClient {
	return &DevBrokerClient{core: b, owner: &devOwner{}}
}

func (c *DevBrokerClient) Connect(context.Context) error {
	c.closed.Store(false)
	return nil
}

func (c *DevBrokerClient) Disconnect(context.Context) error {
	c.closed.Store(true)
	c.core.releaseAll(c.owner)
	return nil
}

func (c *DevBrokerClient) IsConnected() bool {
	return !c.closed.Load() && c.core.IsConnected()
}

func (c *DevBrokerClient) ConnectionState() int {
	if c.IsConnected() {
		return 1
	}
	return 0
}

func (c *DevBrokerClient) CreateQueue(name string, opts QueueOptions) (Queue, error) {
	if c.closed.Load() {
		return nil, errDevClientClosed
	}
	return c.core.createQueue(c.owner, name, opts)
}

func (c *DevBrokerClient) CreateDirectSubscription() (Queue, error) {
	return c.core.CreateDirectSubscription()
}

func (c *DevBrokerClient) DeleteQueue(name string) error {
	return c.core.deleteQueue(c.owner, name)
}

func (c *DevBrokerClient) DeprovisionQueue(name string) error {
	return c.core.DeprovisionQueue(name)
}

func (c *DevBrokerClient) Subscribe(q Queue, topicPattern string) error {
	return c.core.Subscribe(q, topicPattern)
}

func (c *DevBrokerClient) Unsubscribe(q Queue, topicPattern string) error {
	return c.core.Unsubscribe(q, topicPattern)
}

func (c *DevBrokerClient) Publish(ctx context.Context, topic string, msg *Message) error {
	if c.closed.Load() {
		return errDevClientClosed
	}
	return c.core.Publish(ctx, topic, msg)
}

func (c *DevBrokerClient) PublishGuaranteed(ctx context.Context, topic string, msg *Message) error {
	return c.Publish(ctx, topic, msg)
}

func (c *DevBrokerClient) Receive(ctx context.Context, q Queue) (*Message, error) {
	return c.core.receive(ctx, c.owner, q)
}

func (c *DevBrokerClient) Request(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) {
	if c.closed.Load() {
		return nil, errDevClientClosed
	}
	return c.core.Request(ctx, topic, msg, timeout)
}

func (c *DevBrokerClient) CreateTemporaryQueue() (Queue, error) {
	return c.core.CreateTemporaryQueue()
}

func (c *DevBrokerClient) RequestReplyInbox(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) {
	if c.closed.Load() {
		return nil, errDevClientClosed
	}
	return c.core.RequestReplyInbox(ctx, topic, msg, timeout)
}

func (c *DevBrokerClient) BindExisting(name string) (BoundQueue, error) {
	return c.core.BindExisting(name)
}

func (c *DevBrokerClient) ConsumeExisting(name string) (Queue, error) {
	if c.closed.Load() {
		return nil, errDevClientClosed
	}
	return c.core.ConsumeExisting(name)
}

var (
	_ Broker              = (*DevBrokerClient)(nil)
	_ ExistingQueueBinder = (*DevBrokerClient)(nil)
)
