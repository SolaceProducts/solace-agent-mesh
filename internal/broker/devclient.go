// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/sethvargo/go-retry"
)

type NetworkDevBroker struct {
	addr string

	connectMu sync.Mutex

	clientID         string
	queueName        string
	conn             net.Conn
	reader           *bufio.Reader
	connected        atomic.Bool
	connectedAtNanos atomic.Int64

	writeMu sync.Mutex

	pending   map[string]chan *Response
	pendingMu sync.Mutex
	nextID    atomic.Uint64

	initialSubscriptions []string
	dynamicSubscriptions map[string]bool
	subsMu               sync.Mutex

	queues       map[string]*netQueue
	defaultQueue *netQueue
	queuesMu     sync.RWMutex

	bgDone     chan struct{}
	reconnCh   chan struct{}
	closeCh    chan struct{}
	closeOnce  sync.Once
	reconnOnce sync.Once

	logger *slog.Logger

	metrics atomic.Pointer[MetricsRecorder]

	reconnectFnsMu sync.Mutex
	reconnectFns   []func()

	legacyServer atomic.Bool
}

type netQueue struct {
	name    string
	subs    []string
	regexes []*regexp.Regexp
	msgCh   chan *Message

	exclusive   bool
	onBindState func(BindState)
	active      atomic.Bool
	reportMu    sync.Mutex
	reported    BindState
}

func NewNetworkDevBroker(addr, clientID, queueName string, logger *slog.Logger) *NetworkDevBroker {
	if logger == nil {
		logger = slog.Default()
	}
	c := &NetworkDevBroker{
		addr:                 addr,
		clientID:             clientID,
		queueName:            queueName,
		dynamicSubscriptions: make(map[string]bool),
		pending:              make(map[string]chan *Response),
		queues:               make(map[string]*netQueue),
		logger:               logger.With("logger_name", "network-devbroker"),
	}
	var noop MetricsRecorder = NoopMetricsRecorder{}
	c.metrics.Store(&noop)
	return c
}

func (c *NetworkDevBroker) SetMetrics(m MetricsRecorder) {
	resolved := MetricsRecorderOrNoop(m)
	c.metrics.Store(&resolved)
}

func (c *NetworkDevBroker) Connect(_ context.Context) error {
	c.reconnOnce.Do(func() {
		c.reconnCh = make(chan struct{}, 1)
		c.closeCh = make(chan struct{})
		go c.reconnectLoop()
	})
	return c.connect()
}

func (c *NetworkDevBroker) connect() error {
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	return c.connectLocked()
}

func (c *NetworkDevBroker) reconnect() error {
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	select {
	case <-c.closeCh:
		return errClientClosed
	default:
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	return c.connectLocked()
}

var errClientClosed = errors.New("network devbroker: client closed")

func (c *NetworkDevBroker) connectLocked() error {
	c.logger.Debug("network devbroker: dialing", "addr", c.addr)
	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("network devbroker: connect to %s: %w", c.addr, err)
	}
	c.logger.Debug("network devbroker: TCP connected", "local", conn.LocalAddr(), "remote", conn.RemoteAddr())
	c.conn = conn
	c.reader = bufio.NewReaderSize(conn, MaxMessageSize)
	c.connectedAtNanos.Store(time.Now().UnixNano())
	c.connected.Store(true)

	c.bgDone = make(chan struct{})
	go c.backgroundReader()

	c.subsMu.Lock()
	allSubs := make([]string, 0, len(c.initialSubscriptions)+len(c.dynamicSubscriptions))
	allSubs = append(allSubs, c.initialSubscriptions...)
	for sub := range c.dynamicSubscriptions {
		allSubs = append(allSubs, sub)
	}
	c.subsMu.Unlock()

	c.logger.Debug("network devbroker: sending CONNECT", "clientID", c.clientID, "queue", c.queueName, "subs", len(allSubs))
	resp, err := c.sendCommand(&Command{
		Cmd:           CmdConnect,
		ClientID:      c.clientID,
		QueueName:     c.queueName,
		Subscriptions: allSubs,
		PushMode:      true,
	})
	if err != nil {
		c.connected.Store(false)
		_ = conn.Close()
		return fmt.Errorf("network devbroker: CONNECT: %w", err)
	}
	c.logger.Debug("network devbroker: CONNECT response received", "responseStatus", resp.Status, "clientID", resp.ClientID)
	if resp.Status != StatusOK {
		c.connected.Store(false)
		_ = conn.Close()
		return fmt.Errorf("network devbroker: CONNECT rejected: %s %s", resp.ErrorCode, resp.ErrorMessage)
	}

	if resp.ClientID != "" {
		c.clientID = resp.ClientID
	}

	c.queuesMu.Lock()
	if c.defaultQueue == nil {
		c.defaultQueue = &netQueue{
			name:  "__default__",
			msgCh: make(chan *Message, DefaultMessageChannelSize),
		}
		for _, sub := range c.initialSubscriptions {
			c.defaultQueue.subs = append(c.defaultQueue.subs, sub)
			c.defaultQueue.regexes = append(c.defaultQueue.regexes, subscriptionToRegex(sub))
		}
	}
	c.queuesMu.Unlock()

	c.logger.Info("network devbroker connected", "addr", c.addr, "clientID", c.clientID)
	c.bindExclusiveQueues()
	return nil
}

func (c *NetworkDevBroker) bindExclusiveQueues() {
	for _, q := range c.exclusiveQueues() {
		c.bindRemote(q)
	}
}

func (c *NetworkDevBroker) exclusiveQueues() []*netQueue {
	c.queuesMu.RLock()
	defer c.queuesMu.RUnlock()
	var exclusive []*netQueue
	for _, q := range c.queues {
		if q.exclusive {
			exclusive = append(exclusive, q)
		}
	}
	return exclusive
}

func (c *NetworkDevBroker) bindRemote(q *netQueue) {
	if c.legacyServer.Load() {
		c.setBindState(q, BindActive)
		return
	}
	resp, err := c.sendCommand(&Command{Cmd: CmdBind, QueueName: q.name, Exclusive: true})
	switch {
	case err != nil:
		c.logger.Warn("network devbroker: bind failed; the queue stays unbound until the client reconnects", "queue", q.name, "error", err)
	case resp.Status == StatusError && resp.ErrorCode == ErrInvalidCommand:
		if c.legacyServer.CompareAndSwap(false, true) {
			c.logger.Warn("network devbroker: server has no exclusive queues; this client takes every exclusive queue as active",
				"addr", c.addr)
		}
		c.setBindState(q, BindActive)
	case resp.Status != StatusOK:
		c.logger.Warn("network devbroker: bind rejected", "queue", q.name, "errorCode", resp.ErrorCode)
	}
}

func (c *NetworkDevBroker) applyBindState(resp *Response) {
	state, ok := parseBindState(resp.BindState)
	if !ok {
		c.logger.Warn("network devbroker: invalid bind state frame", "queue", resp.QueueName)
		return
	}
	c.queuesMu.RLock()
	q := c.queues[resp.QueueName]
	c.queuesMu.RUnlock()
	if q != nil && q.exclusive {
		c.setBindState(q, state)
	}
}

func (c *NetworkDevBroker) setBindState(q *netQueue, state BindState) {
	q.reportMu.Lock()
	defer q.reportMu.Unlock()
	q.active.Store(state == BindActive)
	if state == q.reported || q.onBindState == nil {
		return
	}
	q.reported = state
	q.onBindState(state)
}

func (c *NetworkDevBroker) reportExclusiveBindsLost() {
	for _, q := range c.exclusiveQueues() {
		c.setBindState(q, BindStandby)
	}
}

func (c *NetworkDevBroker) backgroundReader() {
	defer close(c.bgDone)

	for {
		if !c.connected.Load() {
			return
		}

		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			if !c.connected.Load() {
				return
			}
			c.logger.Warn("network devbroker: background reader error", "error", err)
			c.failAllPending(fmt.Errorf("connection lost: %w", err))
			c.connected.Store(false)
			c.reportExclusiveBindsLost()
			select {
			case c.reconnCh <- struct{}{}:
			default:
			}
			return
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			c.logger.Warn("network devbroker: invalid response frame", "error", err)
			continue
		}

		if resp.ID != "" {
			c.pendingMu.Lock()
			ch, ok := c.pending[resp.ID]
			if ok {
				delete(c.pending, resp.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				ch <- &resp
			} else {
				c.logger.Debug("network devbroker: orphan response", "id", resp.ID, "responseStatus", resp.Status)
			}
		} else if resp.Status == StatusMessage && resp.Message != nil {
			c.dispatchMessage(resp.Message)
		} else if resp.Status == StatusBindState {
			c.applyBindState(&resp)
		} else {
			c.logger.Debug("network devbroker: unhandled frame", "responseStatus", resp.Status)
		}
	}
}

func (c *NetworkDevBroker) dispatchMessage(netMsg *NetworkMessage) {
	payload, marshalErr := json.Marshal(netMsg.Payload)
	if marshalErr != nil {
		payload = []byte(fmt.Sprint(netMsg.Payload))
	}

	nackWarn := func() {
		c.logger.Warn("network devbroker: NACK called — converted to no-op (network dev broker does not support redelivery)")
	}
	msg := NewDeliveredMessage(netMsg.MessageID, netMsg.Topic, payload, func() {}, nackWarn)
	msg.UserProperties = anyMapToStringMap(netMsg.UserProperties)
	msg.ReplyTo = netMsg.ReplyTo
	msg.CorrelationID = netMsg.CorrelationID
	msg.Headers = netMsg.Headers

	m := *c.metrics.Load()
	c.queuesMu.RLock()
	dispatched := false
	heldByStandby := false
	for _, q := range c.queues {
		if q.matchesTopic(msg.Topic) {
			if q.exclusive && !q.active.Load() {
				heldByStandby = true
				continue
			}
			clone := msg
			if dispatched {
				clone = NewDeliveredMessage(msg.ID, msg.Topic, msg.Payload, func() {}, nackWarn)
				clone.UserProperties = copyStringMap(msg.UserProperties)
				clone.ReplyTo = msg.ReplyTo
				clone.CorrelationID = msg.CorrelationID
				clone.Headers = msg.Headers.clone()
			}
			timer := time.NewTimer(DirectSendTimeout)
			select {
			case q.msgCh <- clone:
				timer.Stop()
				dispatched = true
				m.Gauge("broker.direct.channel_depth", float64(len(q.msgCh)), "queue", q.name)
				c.logger.Debug("dispatched message to queue", "queue", q.name, "topic", msg.Topic)
			case <-timer.C:
				m.Counter("broker.direct.dropped", 1, "queue", q.name)
				c.logger.Warn("network devbroker: dropping message after backpressure timeout",
					"queue", q.name, "topic", msg.Topic, "timeout", DirectSendTimeout)
			}
		}
	}
	if !dispatched && !heldByStandby && c.defaultQueue != nil && c.defaultQueue.matchesTopic(msg.Topic) {
		timer := time.NewTimer(DirectSendTimeout)
		select {
		case c.defaultQueue.msgCh <- msg:
			timer.Stop()
			dispatched = true
			m.Gauge("broker.direct.channel_depth", float64(len(c.defaultQueue.msgCh)), "queue", c.defaultQueue.name)
		case <-timer.C:
			m.Counter("broker.direct.dropped", 1, "queue", c.defaultQueue.name)
			c.logger.Warn("network devbroker: dropping default-queue message after backpressure timeout",
				"topic", msg.Topic, "timeout", DirectSendTimeout)
		}
	}
	c.queuesMu.RUnlock()

	if !dispatched {
		c.logger.Debug("no matching queue for message", "topic", msg.Topic)
	}
}

func (q *netQueue) matchesTopic(topic string) bool {
	for _, re := range q.regexes {
		if re.MatchString(topic) {
			return true
		}
	}
	return false
}

func (c *NetworkDevBroker) Disconnect(_ context.Context) error {
	if c.closeCh != nil {
		c.closeOnce.Do(func() {
			close(c.closeCh)
		})
	}
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	if !c.connected.Load() {
		return nil
	}
	_, _ = c.sendCommand(&Command{Cmd: CmdDisconnect})
	c.connected.Store(false)
	if c.conn != nil {
		_ = c.conn.Close()
	}
	if c.bgDone != nil {
		<-c.bgDone
	}
	c.reportExclusiveBindsLost()
	return nil
}

func (c *NetworkDevBroker) IsConnected() bool {
	return c.connected.Load()
}

func (c *NetworkDevBroker) ConnectionState() int {
	if c.IsConnected() {
		return 1
	}
	return 0
}

func (c *NetworkDevBroker) CreateQueue(name string, opts QueueOptions) (Queue, error) {
	c.queuesMu.Lock()
	q, exists := c.queues[name]
	if !exists {
		q = &netQueue{
			name:     name,
			msgCh:    make(chan *Message, DefaultMessageChannelSize),
			reported: BindStandby,
		}
		if opts.Named && opts.Exclusive {
			q.exclusive = true
			q.onBindState = opts.OnBindState
		}
		c.queues[name] = q
	}
	c.queuesMu.Unlock()

	switch {
	case exists:
		return &devQueue{name: name}, nil
	case q.exclusive:
		if c.connected.Load() {
			c.bindRemote(q)
		}
	case opts.Named && opts.OnBindState != nil:
		opts.OnBindState(BindActive)
	}

	return &devQueue{name: name}, nil
}

func (c *NetworkDevBroker) CreateDirectSubscription() (Queue, error) {
	name := fmt.Sprintf("direct-%d-%d", time.Now().UnixNano(), networkDirectSubSeq.Add(1))
	return c.CreateQueue(name, QueueOptions{})
}

var networkDirectSubSeq atomic.Uint64

func (c *NetworkDevBroker) DeleteQueue(name string) error {
	c.queuesMu.Lock()
	q := c.queues[name]
	delete(c.queues, name)
	c.queuesMu.Unlock()

	if q == nil || !q.exclusive || !c.connected.Load() || c.legacyServer.Load() {
		return nil
	}
	resp, err := c.sendCommand(&Command{Cmd: CmdUnbind, QueueName: name})
	if err != nil {
		return fmt.Errorf("network devbroker: UNBIND: %w", err)
	}
	if resp.Status != StatusOK {
		return fmt.Errorf("network devbroker: UNBIND rejected: %s", resp.ErrorMessage)
	}
	return nil
}

func (c *NetworkDevBroker) DeprovisionQueue(name string) error {
	return c.DeleteQueue(name)
}

func (c *NetworkDevBroker) Subscribe(q Queue, topicPattern string) error {
	c.queuesMu.Lock()
	if q == nil {
		if c.defaultQueue != nil {
			c.defaultQueue.subs = append(c.defaultQueue.subs, topicPattern)
			c.defaultQueue.regexes = append(c.defaultQueue.regexes, subscriptionToRegex(topicPattern))
		}
	} else {
		nq, exists := c.queues[q.Name()]
		if exists {
			nq.subs = append(nq.subs, topicPattern)
			nq.regexes = append(nq.regexes, subscriptionToRegex(topicPattern))
		}
	}
	c.queuesMu.Unlock()

	c.subsMu.Lock()
	c.dynamicSubscriptions[topicPattern] = true
	c.subsMu.Unlock()

	if !c.connected.Load() {
		return nil
	}

	resp, err := c.sendCommand(&Command{
		Cmd:          CmdSubscribe,
		TopicPattern: topicPattern,
	})
	if err != nil {
		return fmt.Errorf("network devbroker: SUBSCRIBE: %w", err)
	}
	if resp.Status != StatusOK {
		return fmt.Errorf("network devbroker: SUBSCRIBE rejected: %s", resp.ErrorMessage)
	}
	return nil
}

func (c *NetworkDevBroker) Unsubscribe(q Queue, topicPattern string) error {
	c.subsMu.Lock()
	delete(c.dynamicSubscriptions, topicPattern)
	c.subsMu.Unlock()

	c.queuesMu.Lock()
	var nq *netQueue
	if q == nil {
		nq = c.defaultQueue
	} else {
		nq = c.queues[q.Name()]
	}
	if nq != nil {
		for i, sub := range nq.subs {
			if sub == topicPattern {
				nq.subs = append(nq.subs[:i], nq.subs[i+1:]...)
				nq.regexes = append(nq.regexes[:i], nq.regexes[i+1:]...)
				break
			}
		}
	}
	c.queuesMu.Unlock()

	if !c.connected.Load() {
		return nil
	}

	resp, err := c.sendCommand(&Command{
		Cmd:          CmdUnsubscribe,
		TopicPattern: topicPattern,
	})
	if err != nil {
		return fmt.Errorf("network devbroker: UNSUBSCRIBE: %w", err)
	}
	if resp.Status != StatusOK {
		return fmt.Errorf("network devbroker: UNSUBSCRIBE rejected: %s", resp.ErrorMessage)
	}
	return nil
}

func (c *NetworkDevBroker) Publish(_ context.Context, topic string, msg *Message) error {
	if !c.connected.Load() {
		return fmt.Errorf("network devbroker: not connected")
	}

	var payload any
	if msg.Payload != nil {
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			payload = string(msg.Payload)
		}
	}

	resp, err := c.sendCommand(&Command{
		Cmd:            CmdPublish,
		Topic:          topic,
		Payload:        payload,
		UserProperties: stringMapToAnyMap(msg.UserProperties),
		ReplyTo:        msg.ReplyTo,
		CorrelationID:  msg.CorrelationID,
		Headers:        msg.Headers,
	})
	if err != nil {
		return fmt.Errorf("network devbroker: PUBLISH: %w", err)
	}
	if resp.Status != StatusOK {
		return fmt.Errorf("network devbroker: PUBLISH rejected: %s", resp.ErrorMessage)
	}
	return nil
}

func (c *NetworkDevBroker) PublishGuaranteed(ctx context.Context, topic string, msg *Message) error {
	return c.Publish(ctx, topic, msg)
}

func (c *NetworkDevBroker) Receive(ctx context.Context, q Queue) (*Message, error) {
	var nq *netQueue

	if q == nil {
		c.queuesMu.RLock()
		nq = c.defaultQueue
		c.queuesMu.RUnlock()
	} else {
		c.queuesMu.RLock()
		nq = c.queues[q.Name()]
		c.queuesMu.RUnlock()
	}

	if nq == nil {
		qName := "<nil>"
		if q != nil {
			qName = q.Name()
		}
		return nil, fmt.Errorf("network devbroker: unknown queue %q: %w", qName, ErrQueueNotFound)
	}

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case msg := <-nq.msgCh:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	}
}

func (c *NetworkDevBroker) Request(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) {
	if msg.ReplyTo == "" {
		return nil, fmt.Errorf("network devbroker: message has no ReplyTo topic")
	}
	if msg.CorrelationID == "" {
		msg.CorrelationID = idgen.New()
	}

	replyQueue, err := c.CreateDirectSubscription()
	if err != nil {
		return nil, fmt.Errorf("network devbroker: create temporary queue: %w", err)
	}
	if err := c.Subscribe(replyQueue, msg.ReplyTo); err != nil {
		return nil, err
	}
	defer func() {
		_ = c.Unsubscribe(replyQueue, msg.ReplyTo)
		_ = c.DeleteQueue(replyQueue.Name())
	}()

	if err := c.Publish(ctx, topic, msg); err != nil {
		return nil, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return receiveMatching(timeoutCtx, c.Receive, replyQueue, msg.CorrelationID)
}

func (c *NetworkDevBroker) CreateTemporaryQueue() (Queue, error) { //nolint:revive
	return c.CreateDirectSubscription()
}

func (c *NetworkDevBroker) RequestReplyInbox(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) { //nolint:revive
	if msg.ReplyTo == "" {
		msg.ReplyTo = fmt.Sprintf("_devbroker/inbox/%d", devInboxSeq.Add(1))
	}
	return c.Request(ctx, topic, msg, timeout)
}

func (c *NetworkDevBroker) SetInitialSubscriptions(subs []string) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	c.initialSubscriptions = subs
}

func (c *NetworkDevBroker) sendCommand(cmd *Command) (*Response, error) {
	id := fmt.Sprintf("%d", c.nextID.Add(1))
	cmd.ID = id

	ch := make(chan *Response, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	data, err := json.Marshal(cmd)
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	data = append(data, '\n')

	c.logger.Debug("sending command", "cmd", cmd.Cmd, "id", id, "bytes", len(data))
	c.writeMu.Lock()
	_, writeErr := c.conn.Write(data)
	c.writeMu.Unlock()

	if writeErr != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		c.connected.Store(false)
		return nil, fmt.Errorf("write command: %w", writeErr)
	}

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case resp := <-ch:
		return resp, nil
	case <-c.closeCh:
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("client closed while waiting for response to %s (id=%s)", cmd.Cmd, id)
	case <-timer.C:
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response to %s (id=%s)", cmd.Cmd, id)
	}
}

func (c *NetworkDevBroker) failAllPending(err error) {
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		ch <- &Response{
			ID:           id,
			Status:       StatusError,
			ErrorCode:    ErrInternalError,
			ErrorMessage: err.Error(),
		}
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

func (c *NetworkDevBroker) reconnectLoop() {
	var flapping flapBackoff
	for {
		select {
		case <-c.closeCh:
			return
		case <-c.reconnCh:
		}

		<-c.bgDone

		if !c.waitOutFlapping(&flapping) {
			return
		}

		if err := c.reconnectWithBackoff(); err != nil {
			return
		}
		c.logger.Info("network devbroker: reconnected successfully")

		c.reconnectFnsMu.Lock()
		fns := append([]func(){}, c.reconnectFns...)
		c.reconnectFnsMu.Unlock()
		for _, fn := range fns {
			fn()
		}
	}
}

func (c *NetworkDevBroker) reconnectWithBackoff() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-c.closeCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	b := retry.NewExponential(reconnectInitialBackoff)
	b = retry.WithCappedDuration(reconnectMaxBackoff, b)
	return retry.Do(ctx, b, c.attemptReconnect)
}

func (c *NetworkDevBroker) attemptReconnect(_ context.Context) error {
	c.logger.Debug("network devbroker: attempting reconnect", "addr", c.addr)
	err := c.reconnect()
	if err == nil || errors.Is(err, errClientClosed) {
		return err
	}
	c.logger.Warn("network devbroker: reconnect failed", "error", err)
	return retry.RetryableError(err)
}

const (
	reconnectInitialBackoff = 500 * time.Millisecond
	reconnectMaxBackoff     = 10 * time.Second
	stableConnectionUptime  = reconnectMaxBackoff
)

type flapBackoff struct {
	delay time.Duration
}

func (f *flapBackoff) next(uptime time.Duration) time.Duration {
	if uptime >= stableConnectionUptime {
		f.delay = 0
		return 0
	}
	f.delay = min(max(2*f.delay, reconnectInitialBackoff), reconnectMaxBackoff)
	return f.delay
}

func (c *NetworkDevBroker) waitOutFlapping(f *flapBackoff) bool {
	uptime := time.Since(time.Unix(0, c.connectedAtNanos.Load()))
	delay := f.next(uptime)
	if delay == 0 {
		return true
	}
	c.logger.Warn("network devbroker: connection dropped soon after connecting; delaying reconnect",
		"uptime", uptime, "delay", delay)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-c.closeCh:
		return false
	case <-timer.C:
		return true
	}
}

func (c *NetworkDevBroker) OnReconnect(fn func()) {
	c.reconnectFnsMu.Lock()
	c.reconnectFns = append(c.reconnectFns, fn)
	c.reconnectFnsMu.Unlock()
}

func (c *NetworkDevBroker) BindExisting(string) (BoundQueue, error) {
	return nil, fmt.Errorf("network devbroker: bind existing queue: %w", errors.ErrUnsupported)
}

func (c *NetworkDevBroker) ConsumeExisting(string) (Queue, error) {
	return nil, fmt.Errorf("network devbroker: consume existing queue: %w", errors.ErrUnsupported)
}

var _ Broker = (*NetworkDevBroker)(nil)
