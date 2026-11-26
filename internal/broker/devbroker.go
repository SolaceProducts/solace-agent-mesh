// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type DevBroker struct {
	mu                sync.RWMutex
	connected         bool
	subscriptions     map[string][]string
	regexCache        map[string]*regexp.Regexp
	queues            map[string]chan *Message
	maxRedelivery     map[string]uint
	queueSize         int
	directSubSeq      uint64
	consumeOnlyQueues map[string]bool
	exclusive         map[string]*devExclusiveQueue
	root              *devOwner
	logger            *slog.Logger
	metrics           atomic.Pointer[MetricsRecorder]
}

func NewDevBroker(opts ...DevBrokerOption) *DevBroker {
	b := &DevBroker{
		subscriptions:     make(map[string][]string),
		regexCache:        make(map[string]*regexp.Regexp),
		queues:            make(map[string]chan *Message),
		maxRedelivery:     make(map[string]uint),
		consumeOnlyQueues: make(map[string]bool),
		exclusive:         make(map[string]*devExclusiveQueue),
		root:              &devOwner{},
		logger:            slog.Default().With("logger_name", "devbroker"),
		queueSize:         DefaultMessageChannelSize,
	}
	var noop MetricsRecorder = NoopMetricsRecorder{}
	b.metrics.Store(&noop)
	for _, opt := range opts {
		opt(b)
	}
	return b
}

type DevBrokerOption func(*DevBroker)

func WithDevBrokerLogger(logger *slog.Logger) DevBrokerOption {
	return func(b *DevBroker) {
		if logger != nil {
			b.logger = logger.With("logger_name", "devbroker")
		}
	}
}

func (b *DevBroker) SetMetrics(m MetricsRecorder) {
	resolved := MetricsRecorderOrNoop(m)
	b.metrics.Store(&resolved)
}

func (b *DevBroker) Connect(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected = true
	return nil
}

func (b *DevBroker) Disconnect(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connected = false
	return nil
}

func (b *DevBroker) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connected
}

func (b *DevBroker) ConnectionState() int {
	if b.IsConnected() {
		return 1
	}
	return 0
}

func (b *DevBroker) CreateQueue(name string, opts QueueOptions) (Queue, error) {
	return b.createQueue(b.root, name, opts)
}

func (b *DevBroker) createQueue(owner *devOwner, name string, opts QueueOptions) (Queue, error) {
	b.mu.Lock()
	if b.consumeOnlyQueues[name] {
		b.mu.Unlock()
		return nil, fmt.Errorf("devbroker: queue %q is consume-only; cannot be registered as a SAM-managed queue", name)
	}
	_, exists := b.queues[name]
	if !exists {
		b.queues[name] = make(chan *Message, b.queueSize)
		b.maxRedelivery[name] = opts.MaxRedelivery
	}
	if bind, ok := b.bindLocked(owner, name, opts); ok {
		b.mu.Unlock()
		if bind != nil {
			b.notify(bind)
		}
		return &devQueue{name: name}, nil
	}
	b.mu.Unlock()

	if !exists && opts.Named && opts.OnBindState != nil {
		opts.OnBindState(BindActive)
	}

	return &devQueue{name: name}, nil
}

func (b *DevBroker) ProvisionQueue(_ context.Context, name string, opts QueueOptions, topics ...string) error {
	q, err := b.CreateQueue(name, opts)
	if err != nil {
		return err
	}
	for _, topic := range topics {
		if err := b.Subscribe(q, topic); err != nil {
			return err
		}
	}
	return nil
}

func (b *DevBroker) CreateDirectSubscription() (Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.directSubSeq++
	name := fmt.Sprintf("direct-%d-%d", time.Now().UnixNano(), b.directSubSeq)
	b.queues[name] = make(chan *Message, b.queueSize)
	return &devQueue{name: name, directSubscription: true}, nil
}

func (b *DevBroker) DeprovisionQueue(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.consumeOnlyQueues[name] {
		b.logger.Debug("devbroker: DeprovisionQueue skipped: consume-only queue", "queue", name)
		return nil
	}
	b.dropExclusiveLocked(name)
	b.closeQueueLocked(name)
	return nil
}

func (b *DevBroker) DeleteQueue(name string) error {
	return b.deleteQueue(b.root, name)
}

func (b *DevBroker) deleteQueue(owner *devOwner, name string) error {
	b.mu.Lock()
	if b.consumeOnlyQueues[name] {
		b.mu.Unlock()
		b.logger.Debug("devbroker: DeleteQueue skipped: consume-only queue", "queue", name)
		return nil
	}
	xq, exclusive := b.exclusive[name]
	if !exclusive {
		b.closeQueueLocked(name)
		b.mu.Unlock()
		return nil
	}
	if xq.indexOf(owner) < 0 {
		if owner == b.root {
			b.dropExclusiveLocked(name)
			b.closeQueueLocked(name)
		}
		b.mu.Unlock()
		return nil
	}
	changed := b.releaseLocked(owner, name, false)
	b.mu.Unlock()
	b.notify(changed...)
	return nil
}

func (b *DevBroker) closeQueueLocked(name string) {
	for pattern, queueNames := range b.subscriptions {
		filtered := queueNames[:0]
		for _, qn := range queueNames {
			if qn != name {
				filtered = append(filtered, qn)
			}
		}
		if len(filtered) == 0 {
			delete(b.subscriptions, pattern)
			delete(b.regexCache, pattern)
		} else {
			b.subscriptions[pattern] = filtered
		}
	}

	if ch, exists := b.queues[name]; exists {
		close(ch)
		delete(b.queues, name)
		delete(b.maxRedelivery, name)
	}
}

func (b *DevBroker) Subscribe(q Queue, topicPattern string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	queueName := q.Name()

	if b.consumeOnlyQueues[queueName] {
		return fmt.Errorf("devbroker: subscribe: queue %q is consume-only; subscriptions are managed by the queue owner", queueName)
	}

	if _, exists := b.queues[queueName]; !exists {
		b.queues[queueName] = make(chan *Message, b.queueSize)
	}

	re := subscriptionToRegex(topicPattern)
	pattern := re.String()

	b.regexCache[pattern] = re

	for _, qn := range b.subscriptions[pattern] {
		if qn == queueName {
			return nil
		}
	}
	b.subscriptions[pattern] = append(b.subscriptions[pattern], queueName)
	return nil
}

func (b *DevBroker) Unsubscribe(q Queue, topicPattern string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	queueName := q.Name()
	if b.consumeOnlyQueues[queueName] {
		return fmt.Errorf("devbroker: unsubscribe: queue %q is consume-only; subscriptions are managed by the queue owner", queueName)
	}
	re := subscriptionToRegex(topicPattern)
	pattern := re.String()

	queueNames := b.subscriptions[pattern]
	filtered := queueNames[:0]
	for _, qn := range queueNames {
		if qn != queueName {
			filtered = append(filtered, qn)
		}
	}
	if len(filtered) == 0 {
		delete(b.subscriptions, pattern)
		delete(b.regexCache, pattern)
	} else {
		b.subscriptions[pattern] = filtered
	}
	return nil
}

func (b *DevBroker) Publish(_ context.Context, topic string, msg *Message) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.connected {
		return fmt.Errorf("devbroker: not connected")
	}

	b.deliverToMatchingLocked(topic, msg)
	return nil
}

func (b *DevBroker) PublishGuaranteed(ctx context.Context, topic string, msg *Message) error {
	return b.Publish(ctx, topic, msg)
}

func (b *DevBroker) deliverToMatchingLocked(topic string, msg *Message) {
	m := *b.metrics.Load()
	for _, queueName := range b.matchingQueuesLocked(topic) {
		ch, exists := b.queues[queueName]
		if !exists {
			continue
		}
		clone := cloneMessage(msg, b.logger)
		clone.Topic = topic
		timer := time.NewTimer(DirectSendTimeout)
		select {
		case ch <- clone:
			timer.Stop()
			m.Gauge("broker.direct.channel_depth", float64(len(ch)), "queue", queueName)
		case <-timer.C:
			m.Counter("broker.direct.dropped", 1, "queue", queueName)
			b.logger.Warn("devbroker: dropping message after backpressure timeout",
				"queue", queueName, "topic", topic, "timeout", DirectSendTimeout)
		}
	}
}

func (b *DevBroker) Requeue(queueName string, msg *Message) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ch, exists := b.queues[queueName]
	if !exists {
		return errDevQueueNotFound(queueName)
	}
	limit := b.maxRedelivery[queueName]
	if limit > 0 && msg.redeliveries >= int(limit) {
		return fmt.Errorf("devbroker: requeue to %q: %w", queueName, ErrRedeliveryExhausted)
	}
	clone := cloneMessage(msg, b.logger)
	clone.Redelivered = true
	clone.redeliveries = msg.redeliveries + 1
	timer := time.NewTimer(DirectSendTimeout)
	defer timer.Stop()
	select {
	case ch <- clone:
		return nil
	case <-timer.C:
		return fmt.Errorf("devbroker: queue %q full: requeue dropped after %s", queueName, DirectSendTimeout)
	}
}

var _ Requeuer = (*DevBroker)(nil)

func (b *DevBroker) Receive(ctx context.Context, q Queue) (*Message, error) {
	return b.receive(ctx, b.root, q)
}

func (b *DevBroker) receive(ctx context.Context, owner *devOwner, q Queue) (*Message, error) {
	b.mu.RLock()
	ch, exists := b.queues[q.Name()]
	var bind *devBind
	xq, exclusive := b.exclusive[q.Name()]
	if exclusive {
		bind = xq.bindOf(owner)
	}
	b.mu.RUnlock()

	if !exists || (exclusive && bind == nil) {
		return nil, errDevQueueNotFound(q.Name())
	}
	var released <-chan struct{}
	if bind != nil {
		released = bind.released
		select {
		case <-bind.promoted:
		case <-released:
			return nil, fmt.Errorf("devbroker: queue %q released: %w", q.Name(), ErrQueueNotFound)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("devbroker: queue %q closed: %w", q.Name(), ErrQueueNotFound)
		}
		return msg, nil
	case <-released:
		return nil, fmt.Errorf("devbroker: queue %q released: %w", q.Name(), ErrQueueNotFound)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *DevBroker) Request(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) {
	replyQueue, err := b.CreateDirectSubscription()
	if err != nil {
		return nil, fmt.Errorf("devbroker: creating reply queue: %w", err)
	}
	defer func() { _ = b.DeleteQueue(replyQueue.Name()) }()

	if msg.ReplyTo == "" {
		return nil, fmt.Errorf("devbroker: message has no ReplyTo topic")
	}
	if msg.CorrelationID == "" {
		msg.CorrelationID = idgen.New()
	}
	if err := b.Subscribe(replyQueue, msg.ReplyTo); err != nil {
		return nil, fmt.Errorf("devbroker: subscribing reply queue: %w", err)
	}

	if err := b.Publish(ctx, topic, msg); err != nil {
		return nil, fmt.Errorf("devbroker: publishing request: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return receiveMatching(timeoutCtx, b.Receive, replyQueue, msg.CorrelationID)
}

func (b *DevBroker) CreateTemporaryQueue() (Queue, error) { //nolint:revive
	return b.CreateDirectSubscription()
}

func (b *DevBroker) RequestReplyInbox(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) { //nolint:revive
	if msg.ReplyTo == "" {
		msg.ReplyTo = fmt.Sprintf("_devbroker/inbox/%d", devInboxSeq.Add(1))
	}
	return b.Request(ctx, topic, msg, timeout)
}

var devInboxSeq atomic.Uint64

func (b *DevBroker) MatchingQueues(topic string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.matchingQueuesLocked(topic)
}

func (b *DevBroker) HasQueue(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.queues[name]
	return ok
}

func errDevQueueNotFound(name string) error {
	return fmt.Errorf("devbroker: queue %q: %w", name, ErrQueueNotFound)
}

func (b *DevBroker) BindExisting(name string) (BoundQueue, error) {
	if !b.HasQueue(name) {
		return nil, errDevQueueNotFound(name)
	}
	return &devBoundQueue{broker: b, name: name}, nil
}

func (b *DevBroker) ConsumeExisting(name string) (Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.connected {
		return nil, fmt.Errorf("devbroker: not connected")
	}
	if _, ok := b.queues[name]; !ok {
		return nil, fmt.Errorf("devbroker: queue %q: %w", name, ErrQueueNotFound)
	}
	if _, exclusive := b.exclusive[name]; exclusive {
		return nil, fmt.Errorf("devbroker: queue %q is exclusive; consume-only on an exclusive queue would never receive", name)
	}
	b.consumeOnlyQueues[name] = true
	return &devQueue{name: name}, nil
}

type devBoundQueue struct {
	broker *DevBroker
	name   string
}

func (q *devBoundQueue) RemoveSubscription(topic string) error {
	return q.broker.Unsubscribe(&devQueue{name: q.name}, topic)
}

func (q *devBoundQueue) Receive(ctx context.Context) (*Message, error) {
	q.broker.mu.RLock()
	ch, exists := q.broker.queues[q.name]
	q.broker.mu.RUnlock()
	if !exists {
		return nil, errDevQueueNotFound(q.name)
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("devbroker: queue %q closed: %w", q.name, ErrQueueNotFound)
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (q *devBoundQueue) Close() {
}

var _ ExistingQueueBinder = (*DevBroker)(nil)

func (b *DevBroker) matchingQueuesLocked(topic string) []string {
	seen := make(map[string]bool)
	var result []string
	for pattern, queueNames := range b.subscriptions {
		re := b.regexCache[pattern]
		if re != nil && re.MatchString(topic) {
			for _, qn := range queueNames {
				if !seen[qn] {
					seen[qn] = true
					result = append(result, qn)
				}
			}
		}
	}
	return result
}

func cloneMessage(msg *Message, logger *slog.Logger) *Message {
	clone := NewDeliveredMessage(
		msg.ID,
		msg.Topic,
		nil,
		func() {},
		func() {
			logger.Warn("devbroker: NACK called — converted to no-op (dev broker does not support redelivery)")
		},
	)
	clone.ReplyTo = msg.ReplyTo
	clone.CorrelationID = msg.CorrelationID
	clone.TTL = msg.TTL
	clone.Headers = msg.Headers.clone()
	if msg.Payload != nil {
		clone.Payload = make([]byte, len(msg.Payload))
		copy(clone.Payload, msg.Payload)
	}
	if msg.UserProperties != nil {
		clone.UserProperties = make(map[string]string, len(msg.UserProperties))
		for k, v := range msg.UserProperties {
			clone.UserProperties[k] = v
		}
	}
	return clone
}

type devQueue struct {
	name               string
	directSubscription bool
}

func (q *devQueue) Name() string { return q.name }

func (q *devQueue) IsDirectSubscription() bool { return q.directSubscription }

var (
	_ Broker = (*DevBroker)(nil)
	_ Queue  = (*devQueue)(nil)
)
