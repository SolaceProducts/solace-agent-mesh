// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newBrokerPublishMonitor() *Monitor {
	return newMonitor(
		runtime.MetricProvider(),
		metricOutboundRequestDuration,
		classifyRemoteError,
		"service.peer.name", "solace_broker",
		"operation.name", "publish",
	)
}

type InstrumentedBroker struct {
	broker.Broker
}

func NewInstrumentedBroker(inner broker.Broker) broker.Broker {
	wrapped := &InstrumentedBroker{Broker: inner}
	if binder, ok := inner.(broker.Binder); ok {
		return &instrumentedBinder{InstrumentedBroker: wrapped, binder: binder}
	}
	return wrapped
}

type instrumentedBinder struct {
	*InstrumentedBroker
	binder broker.Binder
}

func (b *instrumentedBinder) Bound(q broker.Queue) bool { return b.binder.Bound(q) }

var _ broker.Binder = (*instrumentedBinder)(nil)

func (b *InstrumentedBroker) Publish(ctx context.Context, topic string, msg *broker.Message) (err error) {
	defer newBrokerPublishMonitor().Stop(&err)
	return b.Broker.Publish(ctx, topic, msg)
}

func (b *InstrumentedBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) (err error) {
	defer newBrokerPublishMonitor().Stop(&err)
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

func (b *InstrumentedBroker) ConnectionState() int {
	if bs, ok := b.Broker.(BrokerState); ok {
		return bs.ConnectionState()
	}
	if b.IsConnected() {
		return 1
	}
	return 0
}

func (b *InstrumentedBroker) BindExisting(name string) (broker.BoundQueue, error) {
	if eb, ok := b.Broker.(broker.ExistingQueueBinder); ok {
		return eb.BindExisting(name)
	}
	return nil, errors.ErrUnsupported
}

func (b *InstrumentedBroker) Requeue(queueName string, msg *broker.Message) error {
	return broker.Requeue(b.Broker, queueName, msg)
}

func (b *InstrumentedBroker) ConsumeExisting(name string) (broker.Queue, error) {
	if eb, ok := b.Broker.(broker.ExistingQueueBinder); ok {
		return eb.ConsumeExisting(name)
	}
	return nil, errors.ErrUnsupported
}

var _ broker.ExistingQueueBinder = (*InstrumentedBroker)(nil)

type BrokerState interface {
	ConnectionState() int
}

func RegisterBrokerConnectionState(reg *Registry, componentType string, brk BrokerState) {
	if reg == nil || brk == nil {
		return
	}
	reg.ObservableValue("broker.connection.state", func() float64 {
		return float64(brk.ConnectionState())
	}, "component_type", componentType)
}

func (b *InstrumentedBroker) ProvisionQueue(ctx context.Context, name string, opts broker.QueueOptions, topics ...string) error {
	return broker.ProvisionQueue(ctx, b.Broker, name, opts, topics...)
}

func (b *InstrumentedBroker) OnReconnect(fn func()) {
	if rn, ok := b.Broker.(broker.ReconnectNotifier); ok {
		rn.OnReconnect(fn)
	}
}

func (b *InstrumentedBroker) OnConnectionLost(fn func()) {
	if n, ok := b.Broker.(broker.ConnectionLossNotifier); ok {
		n.OnConnectionLost(fn)
	}
}

var (
	_ broker.ReconnectNotifier      = (*InstrumentedBroker)(nil)
	_ broker.ConnectionLossNotifier = (*InstrumentedBroker)(nil)
)
