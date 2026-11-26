// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	DefaultMessageChannelSize = 5000

	MaxMessageSize = 10 * 1024 * 1024
)

var DirectSendTimeout = 250 * time.Millisecond

type MetricsRecorder interface {
	Counter(name string, delta int64, labels ...string)
	Gauge(name string, value float64, labels ...string)
}

type NoopMetricsRecorder struct{}

func (NoopMetricsRecorder) Counter(string, int64, ...string) {
}

func (NoopMetricsRecorder) Gauge(string, float64, ...string) {
}

func MetricsRecorderOrNoop(m MetricsRecorder) MetricsRecorder {
	if m == nil {
		return NoopMetricsRecorder{}
	}
	return m
}

var ErrQueueNotFound = errors.New("queue not found")

var ErrProvisionCooldown = errors.New("queue provision cooldown active")

var ErrPermissionDenied = errors.New("permission denied")

var ErrConsumeQueueUnavailable = errors.New("pre-provisioned queue missing or inaccessible")

type Broker interface {
	Connect(ctx context.Context) error

	Disconnect(ctx context.Context) error

	IsConnected() bool

	CreateQueue(name string, opts QueueOptions) (Queue, error)

	CreateDirectSubscription() (Queue, error)

	DeleteQueue(name string) error

	DeprovisionQueue(name string) error

	Subscribe(queue Queue, topicPattern string) error

	Unsubscribe(queue Queue, topicPattern string) error

	Publish(ctx context.Context, topic string, msg *Message) error

	PublishGuaranteed(ctx context.Context, topic string, msg *Message) error

	Receive(ctx context.Context, queue Queue) (*Message, error)

	Request(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error)

	CreateTemporaryQueue() (Queue, error)

	RequestReplyInbox(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error)
}

type Queue interface {
	Name() string

	IsDirectSubscription() bool
}

type SimpleQueue struct {
	QName              string
	DirectSubscription bool
}

func (q *SimpleQueue) Name() string { return q.QName }

func (q *SimpleQueue) IsDirectSubscription() bool { return q.DirectSubscription }

type BindState int

const (
	BindActive BindState = iota
	BindStandby
)

func (s BindState) String() string {
	if s == BindStandby {
		return "standby"
	}
	return "active"
}

type QueueOptions struct {
	Named bool

	Exclusive bool

	Backpressure bool

	MaxRedelivery uint

	RespectTTL bool

	QuotaMB uint

	OnBindState func(BindState)
}

type QueueProvisioner interface {
	ProvisionQueue(ctx context.Context, name string, opts QueueOptions, topics ...string) error
}

func ProvisionQueue(ctx context.Context, b Broker, name string, opts QueueOptions, topics ...string) error {
	if p, ok := b.(QueueProvisioner); ok {
		return p.ProvisionQueue(ctx, name, opts, topics...)
	}
	return nil
}

var ErrRedeliveryExhausted = errors.New("redelivery limit reached")

type Requeuer interface {
	Requeue(queueName string, msg *Message) error
}

func Requeue(b Broker, queueName string, msg *Message) error {
	if r, ok := b.(Requeuer); ok {
		return r.Requeue(queueName, msg)
	}
	return errors.ErrUnsupported
}

type Binder interface {
	Bound(q Queue) bool
}

func Bound(b Broker, q Queue) bool {
	if binder, ok := b.(Binder); ok {
		return binder.Bound(q)
	}
	return true
}

type Message struct {
	ID string

	Topic string

	Payload []byte

	UserProperties map[string]string

	ReplyTo string

	CorrelationID string

	TTL time.Duration

	Headers *PublishHeaders

	Redelivered bool

	ReplicationGroupMessageID string

	redeliveries int

	ackFunc  func()
	nackFunc func()

	nackRedelivers bool
}

func (m *Message) NackRedelivers() bool { return m.nackRedelivers }

type PublishHeaders struct {
	DMQEligible *bool `json:"dmq_eligible,omitempty"`

	Expiration time.Time `json:"expiration,omitzero"`

	Priority *int `json:"priority,omitempty"`

	SequenceNumber *uint64 `json:"sequence_number,omitempty"`

	ApplicationMessageID string `json:"application_message_id,omitempty"`

	ApplicationMessageType string `json:"application_message_type,omitempty"`

	SenderID string `json:"sender_id,omitempty"`

	HTTPContentEncoding string `json:"http_content_encoding,omitempty"`
}

func (h *PublishHeaders) clone() *PublishHeaders {
	if h == nil {
		return nil
	}
	c := *h
	if h.DMQEligible != nil {
		v := *h.DMQEligible
		c.DMQEligible = &v
	}
	if h.Priority != nil {
		v := *h.Priority
		c.Priority = &v
	}
	if h.SequenceNumber != nil {
		v := *h.SequenceNumber
		c.SequenceNumber = &v
	}
	return &c
}

func (m *Message) Ack() {
	if m.ackFunc != nil {
		m.ackFunc()
	}
}

func (m *Message) Nack() {
	if m.nackFunc != nil {
		m.nackFunc()
	}
}

func NewRedeliverableMessage(id, topic string, payload []byte, ack, nack func()) *Message {
	m := NewDeliveredMessage(id, topic, payload, ack, nack)
	m.nackRedelivers = true
	return m
}

func NewDeliveredMessage(id, topic string, payload []byte, ack, nack func()) *Message {
	return &Message{
		ID:       id,
		Topic:    topic,
		Payload:  payload,
		ackFunc:  ack,
		nackFunc: nack,
	}
}

func isReplyForConcurrentRequest(msg *Message, want string) bool {
	return want != "" && msg.CorrelationID != "" && msg.CorrelationID != want
}

func receiveMatching(ctx context.Context, recv func(context.Context, Queue) (*Message, error), q Queue, want string) (*Message, error) {
	var discarded int
	for {
		msg, err := recv(ctx, q)
		if err != nil {
			if discarded > 0 {
				return nil, fmt.Errorf("no reply matched correlationID %q after discarding %d mismatched: %w", want, discarded, err)
			}
			return nil, err
		}
		if msg == nil {
			continue
		}
		if isReplyForConcurrentRequest(msg, want) {
			discarded++
			continue
		}
		return msg, nil
	}
}

type ReconnectNotifier interface {
	OnReconnect(fn func())
}

type ConnectionLossNotifier interface {
	OnConnectionLost(fn func())
}

type ExistingQueueBinder interface {
	BindExisting(name string) (BoundQueue, error)

	ConsumeExisting(name string) (Queue, error)
}

type BoundQueue interface {
	RemoveSubscription(topic string) error
	Receive(ctx context.Context) (*Message, error)
	Close()
}
