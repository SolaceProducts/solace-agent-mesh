// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	methodSend   = "message/send"
	methodStream = "message/stream"
	methodCancel = "tasks/cancel"

	codeInvalidRequest = -32600
	msgAuthFailed      = "Authentication failed"
	msgAgentStopped    = "Agent stopped: SAM has stopped this agent"
	msgAccessDenied    = "Access denied: insufficient scopes"
	msgRequestTooLarge = "Request too large: the agent link sends an agent messages of at most %d bytes"
	msgTaskExpired     = "the agent did not answer before the task's time limit"
	msgLinkMoved       = "the agent link moved to another gateway replica before the agent answered; send the task again"
	msgRequestLost     = "the agent link moved to another gateway replica and could not return the task to its queue; send the task again"
	msgRequestNotBack  = "the agent link could not return the task to its queue for another delivery; send the task again"
	msgAgentRemoved    = "the agent was removed from its entrypoint before it answered"
	msgAgentRedeployed = "the agent was deployed again with new settings before it answered; send the task again"
	msgRedeployLostIt  = "the agent was deployed again with new settings and the agent link could not return the task to its queue; send the task again"

	maxCallDepth             = 10
	maxCardBytes             = 256 << 10
	instanceSuffix           = "@agent-link"
	controlState             = "agent-control:"
	defaultPublishWait       = 10 * time.Second
	maxOpenSubtasks          = 1024
	maxOpenPrompts           = 1024
	maxHeldBytes             = 16 << 20
	maxShrunkHeldBytes       = 64 << 10
	maxQueuedControlRefusals = 16
	maxCancelBytes           = 16 << 10
)

type taskBinding struct {
	replyTo              string
	statusTopic          string
	props                map[string]string
	identity             *auth.Identity
	connID               string
	deliveredAt          time.Time
	deadline             time.Time
	acknowledged         bool
	requestLost          bool
	unanswered           *taskFailure
	answerFailingSince   time.Time
	final                bool
	passedOverAsAnswered bool
	closedAt             time.Time
	cancelled            bool
	cancelCopyApplied    bool
	finalStatusSent      bool
	answerSeq            uint64
	request              requestFacts
	run                  *taskRun
	redelivered          bool
}

type subtaskBinding struct {
	parent     string
	connID     string
	peer       string
	peerTaskID string
	cancelled  bool
}

type agentState struct {
	a             *Adapter
	cfg           *AgentConfig
	ctx           context.Context
	retire        context.CancelFunc
	departing     atomic.Bool
	work          workGate
	capacity      chan struct{}
	bound         chan struct{}
	boundOnce     sync.Once
	stateReady    chan struct{}
	wakeWatching  chan struct{}
	wakeWatchOnce sync.Once
	keepBound     bool

	cardMu          sync.Mutex
	cardUp          bool
	lastCardPublish time.Time

	seq             *agent.EventSeqGen
	router          *agent.PeerRouter
	cardSyncDue     chan struct{}
	quiet           chan struct{}
	refusedControls chan refusedControl
	afterTaskBound  func()
	afterHeldClaim  func(*conn)
	nameSharedWith  map[string]bool

	leaseCheck      atomic.Bool
	listing         atomic.Bool
	listAgain       atomic.Bool
	listingGaveUpAt atomic.Int64
	listingGiveUps  atomic.Int32

	mu                  sync.Mutex
	conns               map[string]*conn
	next                int
	tasks               map[string]*taskBinding
	subtasks            map[string]*subtaskBinding
	peersWarnedOffMesh  map[string]bool
	asks                map[string]*openPrompt
	described           *describedCard
	toolScopesNamed     []string
	held                map[string]heldAnswer
	heldSeq             uint64
	heldBytes           int
	publishedBody       []byte
	controlGen          uint64
	lastStopCommand     time.Time
	connectWindow       time.Time
	connectsInWindow    int
	lastDescribePublish time.Time
	describePending     bool
	withdrawn           bool
	republishing        bool
	stopped             bool
	settling            bool
	requestsEndHere     bool
	framesInFlight      int
	giveBacksInFlight   int
	peerResultsInFlight int
	resultsDropped      int
	usageEventsDropped  int
	dispatching         bool
	stopDispatch        context.CancelFunc
	dispatchDone        chan struct{}
	lastWake            time.Time
	awaitingConnect     bool
	wakeFailed          bool
	wakeClaims          uint64
	copies              *requestCopies
	wakeToken           string
	wakeTokenExp        time.Time
}

func newAgentState(a *Adapter, cfg *AgentConfig) *agentState {
	done := make(chan struct{})
	close(done)
	seq := agent.NewEventSeqGen()
	ctx, retire := context.WithCancel(a.ctx)
	return &agentState{
		a:                  a,
		cfg:                cfg,
		ctx:                ctx,
		retire:             retire,
		capacity:           make(chan struct{}, 1),
		bound:              make(chan struct{}),
		stateReady:         make(chan struct{}),
		wakeWatching:       make(chan struct{}),
		conns:              map[string]*conn{},
		tasks:              map[string]*taskBinding{},
		subtasks:           map[string]*subtaskBinding{},
		peersWarnedOffMesh: map[string]bool{},
		asks:               map[string]*openPrompt{},
		held:               map[string]heldAnswer{},
		copies:             newRequestCopies(maxTrackedCopies),
		cardSyncDue:        make(chan struct{}, 1),
		quiet:              make(chan struct{}, 1),
		refusedControls:    make(chan refusedControl, maxQueuedControlRefusals),
		dispatchDone:       done,
		seq:                seq,
		router:             agent.NewPeerRouter(a.core.Services().Broker(), a.core.Namespace(), a.logger).WithSeqGen(seq),
	}
}

func (s *agentState) ns() string         { return s.a.core.Namespace() }
func (s *agentState) brk() broker.Broker { return s.a.core.Services().Broker() }
func (s *agentState) requestTopic() string {
	return a2a.AgentRequestTopic(s.ns(), s.cfg.Name)
}
func (s *agentState) queueName() string { return a2a.LinkAgentRequestQueueName(s.ns(), s.cfg.Name) }
func (s *agentState) retired() bool     { return s.departing.Load() || s.ctx.Err() != nil }

func (s *agentState) goRun(fn func(context.Context)) bool {
	if !s.work.enter() {
		return false
	}
	if !s.a.goRun(func(context.Context) {
		defer s.work.leave()
		fn(s.ctx)
	}) {
		s.work.leave()
		return false
	}
	return true
}
func (s *agentState) queueOptions() broker.QueueOptions {
	return broker.QueueOptions{Named: true, MaxRedelivery: 3, RespectTTL: true}
}
func (s *agentState) controlTopic() string {
	return a2a.ControlAppCustomTopic(s.ns(), s.cfg.Name, "patch", "instances/"+s.cfg.Name)
}

func (s *agentState) start(ctx context.Context) error {
	brk := s.brk()
	_, releasesOnly := brk.(broker.Binder)
	s.keepBound = !releasesOnly
	if err := broker.ProvisionQueue(ctx, brk, s.queueName(), s.queueOptions(), s.requestTopic()); err != nil {
		return fmt.Errorf("provision queue %s: %w", s.queueName(), err)
	}
	if err := s.receiveDirect(brk, "agent-link-direct", s.onDirect,
		a2a.PeerResponseTopic(s.ns(), s.cfg.Name, ">"), a2a.PeerStatusTopic(s.ns(), s.cfg.Name, ">")); err != nil {
		return err
	}
	if err := s.receiveDirect(brk, "agent-link-control", s.onControl, s.controlTopic()); err != nil {
		return err
	}
	if err := s.receiveDirect(brk, "agent-link-request-copies", func(_ context.Context, m *broker.Message) { s.onRequestCopy(m) },
		s.requestTopic()); err != nil {
		return err
	}
	s.goRun(s.runControlFollowUps)
	s.goRun(s.runControlRefusals)
	s.goRun(s.loadStopState)
	if s.keepBound {
		s.ensureDispatch()
	}
	if s.wakeable() && !s.a.leadsOnly() {
		s.goRun(s.watchWakes)
	}
	return nil
}

func (s *agentState) wakeable() bool { return s.cfg.Wake != nil && s.cfg.Enabled }

func (s *agentState) receiveDirect(brk broker.Broker, loopName string, handle func(context.Context, *broker.Message), topics ...string) error {
	sub, err := brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("direct subscription: %w", err)
	}
	unsubscribe := func() { _ = brk.DeleteQueue(sub.Name()) }
	for _, topic := range topics {
		if err := brk.Subscribe(sub, topic); err != nil {
			unsubscribe()
			return fmt.Errorf("subscribe %s: %w", topic, err)
		}
	}
	listening := s.goRun(func(ctx context.Context) {
		defer unsubscribe()
		broker.RunReceiveLoop(ctx, brk, sub, s.a.logger, broker.ReceiveLoopOptions{LoopName: loopName},
			func(m *broker.Message) { handle(ctx, m) })
	})
	if !listening {
		unsubscribe()
	}
	return nil
}

func (s *agentState) runControlFollowUps(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.cardSyncDue:
			_ = s.syncCard(ctx, s.isStopped())
		}
	}
}

func (s *agentState) runControlRefusals(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case refused := <-s.refusedControls:
			s.replyControl(ctx, refused.msg, refused.resp)
		}
	}
}

func (s *agentState) connections() []*conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*conn, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, c)
	}
	return out
}

func (s *agentState) admitConnect() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.a.now()
	if now.Sub(s.connectWindow) >= time.Minute {
		s.connectWindow, s.connectsInWindow = now, 0
	}
	if s.connectsInWindow >= s.a.cfg.MaxConnectsPerMinute {
		return false
	}
	s.connectsInWindow++
	return true
}

type joinOutcome int

const (
	joined joinOutcome = iota
	joinRefusedFull
	joinRefusedNotLeading
	joinRefusedRetired
)

func (s *agentState) join(c *conn) joinOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired() {
		return joinRefusedRetired
	}
	if !s.a.serves() {
		return joinRefusedNotLeading
	}
	if len(s.conns) >= s.a.cfg.MaxConnectionsPerAgent {
		return joinRefusedFull
	}
	s.conns[c.id] = c
	s.awaitingConnect, s.wakeFailed = false, false
	enabled := !s.stopped
	c.send(outFrame{Type: typeWelcome, Protocol: protocolVersion, Agent: s.cfg.Name, Connection: c.id,
		MaxInflight: s.a.cfg.MaxInflight, Limits: s.a.limits(), Enabled: &enabled})
	if s.stopped {
		sendControl(c, true)
	}
	s.startDeliveringLocked(c)
	return joined
}

type heldAnswer struct {
	taskID    string
	kind      string
	encoded   []byte
	seq       uint64
	claimedBy string
}

func heldKey(f outFrame) string { return f.Type + "/" + f.TaskID + "/" + f.RequestID + f.SubtaskID }

func (s *agentState) sendOrHold(target *conn, taskID string, f outFrame) {
	f = fitted(f, s.a.cfg.MaxFrameBytes)
	encoded, err := json.Marshal(f)
	if err != nil {
		return
	}
	if target != nil && target.sendEncoded(encoded) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.tasks[taskID]; b == nil || b.final {
		s.a.logger.Debug("agent link dropped an answer for an ended task", "agentName", s.cfg.Name, "taskID", taskID, "frameType", f.Type)
		return
	}
	key := heldKey(f)
	s.dropHeldLocked(key)
	if s.heldBytes+len(encoded) > maxHeldBytes {
		encoded, err = json.Marshal(fitted(f, maxShrunkHeldBytes))
		if err != nil || s.heldBytes+len(encoded) > maxHeldBytes {
			s.a.logger.Warn("agent link dropped an answer it had no room to hold", "agentName", s.cfg.Name, "taskID", taskID,
				"frameType", f.Type, "heldBytes", s.heldBytes)
			return
		}
	}
	s.heldSeq++
	s.held[key] = heldAnswer{taskID: taskID, kind: f.Type, encoded: encoded, seq: s.heldSeq}
	s.heldBytes += len(encoded)
	s.handOffHeldLocked(connIDOf(target))
}

func connIDOf(c *conn) string {
	if c == nil {
		return ""
	}
	return c.id
}

func (s *agentState) dropHeldLocked(key string) {
	if h, held := s.held[key]; held {
		delete(s.held, key)
		s.heldBytes -= len(h.encoded)
	}
}

func fitted(f outFrame, limit int64) outFrame {
	if encodedSize(f) <= limit {
		return f
	}
	original := f
	f.Data, f.Truncated = nil, true
	for budget := int(limit / 2); ; budget /= 2 {
		if original.Text != nil {
			text, _ := tool.CapText(*original.Text, budget)
			f.Text = &text
		}
		if original.Error != nil {
			cut := *original.Error
			cut.Message, _ = tool.CapText(cut.Message, budget)
			f.Error = &cut
		}
		if len(original.Answers) > 0 {
			f.Answers = make([]string, len(original.Answers))
			for i, answer := range original.Answers {
				f.Answers[i], _ = tool.CapText(answer, max(1, budget/len(original.Answers)))
			}
		}
		if encodedSize(f) <= limit || budget <= 1 {
			return f
		}
	}
}

func encodedSize(f outFrame) int64 {
	b, err := json.Marshal(f)
	if err != nil {
		return 0
	}
	return int64(len(b))
}

func (s *agentState) deliverHeld(c *conn) {
	defer c.deliveries.Done()
	for {
		s.mu.Lock()
		if c.isClosed() {
			s.mu.Unlock()
			return
		}
		key, h, found := s.nextHeldLocked()
		if found {
			h.claimedBy = c.id
			s.held[key] = h
		}
		s.mu.Unlock()
		if !found {
			return
		}
		if s.afterHeldClaim != nil {
			s.afterHeldClaim(c)
		}
		if !c.sendHeld(h.encoded, func() { s.heldWritten(key, c.id) }) {
			s.releaseHeld(c.id)
			return
		}
	}
}

func (s *agentState) heldWritten(key, connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, held := s.held[key]; held && h.claimedBy == connID {
		s.dropHeldLocked(key)
	}
}

func (s *agentState) releaseHeld(connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, h := range s.held {
		if h.claimedBy == connID {
			h.claimedBy = ""
			s.held[key] = h
		}
	}
	s.handOffHeldLocked(connID)
}

func (s *agentState) handOffHeldLocked(exceptID string) {
	if _, _, waiting := s.nextHeldLocked(); !waiting {
		return
	}
	for id, c := range s.conns {
		if id != exceptID && !c.isClosed() {
			s.startDeliveringLocked(c)
			return
		}
	}
}

func (s *agentState) startDeliveringLocked(c *conn) {
	c.deliveries.Add(1)
	if !s.goRun(func(context.Context) { s.deliverHeld(c) }) {
		c.deliveries.Done()
	}
}

func (s *agentState) nextHeldLocked() (string, heldAnswer, bool) {
	var key string
	var next heldAnswer
	found := false
	for k, h := range s.held {
		if h.claimedBy == "" && (!found || h.seq < next.seq) {
			key, next, found = k, h, true
		}
	}
	return key, next, found
}

func (s *agentState) heldLocked(frameType string) int {
	n := 0
	for _, h := range s.held {
		if h.kind == frameType {
			n++
		}
	}
	return n
}

func (s *agentState) removeConn(c *conn) {
	s.mu.Lock()
	delete(s.conns, c.id)
	pending := c.inflight
	c.inflight = map[uint64]handedTask{}
	for _, handed := range pending {
		s.giveBackLocked(handed.request)
	}
	s.giveBacksInFlight += len(pending)
	claim, rewake := s.workLeftBehindLocked()
	s.mu.Unlock()
	for _, handed := range pending {
		s.giveBack(handed.request)
		s.gaveBack()
	}
	c.deliveries.Wait()
	s.releaseHeld(c.id)
	s.stopDispatchIfIdle()
	s.signalCapacity()
	if rewake {
		s.a.logger.Debug("agent link waking agent again for work left in its queue", "agentName", s.cfg.Name)
		s.startWake(claim)
	}
}

func (s *agentState) workLeftBehindLocked() (uint64, bool) {
	if !s.wakeable() || s.retired() || !s.a.serves() || s.stopped || s.hasConsumerLocked() {
		return 0, false
	}
	left := s.copies.leftWaiting(s.cancelSettledLocked) || s.wakeFailed
	s.copies.forgetWaiting()
	if !left {
		return 0, false
	}
	return s.claimLocked(s.a.now()), true
}

func (s *agentState) signalCapacity() {
	select {
	case s.capacity <- struct{}{}:
	default:
	}
}

func (s *agentState) settle(c *conn, f inFrame) {
	s.mu.Lock()
	handed := c.inflight[f.Delivery]
	m := handed.request
	delete(c.inflight, f.Delivery)
	switch b := s.tasks[handed.taskID]; {
	case m != nil && f.Type != typeAck:
		s.giveBackLocked(m)
		s.giveBacksInFlight++
	case m != nil && b != nil:
		b.acknowledged = true
	}
	s.mu.Unlock()
	if m == nil {
		c.sendError(errCodeUnknownDeliver, "no such delivery on this connection", strconv.FormatUint(f.Delivery, 10))
		return
	}
	if f.Type == typeAck {
		m.Ack()
	} else {
		s.giveBack(m)
		s.gaveBack()
	}
	s.stopDispatchIfIdle()
	s.signalCapacity()
}

func (s *agentState) ensureDispatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dispatching || s.retired() || !s.a.serves() {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	previous, done := s.dispatchDone, make(chan struct{})
	started := s.goRun(func(context.Context) {
		defer close(done)
		select {
		case <-previous:
		case <-ctx.Done():
			return
		}
		s.dispatch(ctx)
	})
	if !started {
		cancel()
		return
	}
	s.dispatching, s.stopDispatch, s.dispatchDone = true, cancel, done
}

func (s *agentState) stopDispatchIfIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dispatching || s.keepBound || s.stopped || s.hasConsumerLocked() || s.hasInflightLocked() {
		return
	}
	s.stopDispatch()
	s.dispatching = false
}

func (s *agentState) hasConsumerLocked() bool {
	for _, c := range s.conns {
		if c.consuming && !c.isClosed() {
			return true
		}
	}
	return false
}

func (s *agentState) hasInflightLocked() bool {
	for _, c := range s.conns {
		if len(c.inflight) > 0 {
			return true
		}
	}
	return false
}

func (s *agentState) hasConsumer() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasConsumerLocked()
}

func (s *agentState) bind(ctx context.Context) broker.Queue {
	brk := s.brk()
	for {
		q, err := brk.CreateQueue(s.queueName(), s.queueOptions())
		if err == nil {
			err = brk.Subscribe(q, s.requestTopic())
		}
		if err == nil {
			s.boundOnce.Do(func() { close(s.bound) })
			return q
		}
		s.a.logger.Warn("agent link queue bind failed; retrying", "agentName", s.cfg.Name, "error", err)
		if !sleepCtx(ctx, 2*time.Second) {
			return nil
		}
	}
}

func (s *agentState) dispatch(ctx context.Context) {
	select {
	case <-s.stateReady:
	case <-ctx.Done():
		return
	}
	brk := s.brk()
	q := s.bind(ctx)
	if q == nil {
		return
	}
	defer s.releaseRequestQueue()
	defer s.acknowledgeSettledRequestsInHandWhenStandingDown()
	for {
		if !s.waitCapacity(ctx) {
			return
		}
		m, err := brk.Receive(ctx, q)
		if ctx.Err() != nil {
			if m != nil {
				s.givingBack()
				s.giveBack(m)
				s.gaveBack()
			}
			return
		}
		if errors.Is(err, broker.ErrQueueNotFound) {
			s.a.logger.Warn("agent link lost its queue; binding again", "agentName", s.cfg.Name)
			if q = s.bind(ctx); q == nil {
				return
			}
			continue
		}
		if err != nil {
			s.a.logger.Warn("agent link receive failed", "agentName", s.cfg.Name, "error", err)
			if !sleepCtx(ctx, time.Second) {
				return
			}
			continue
		}
		if m != nil {
			s.deliver(ctx, m)
		}
	}
}

func (s *agentState) waitCapacity(ctx context.Context) bool {
	for {
		if s.isStopped() || s.pickConsumer() != nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-s.capacity:
		case <-time.After(time.Second):
		}
	}
}

func (s *agentState) pickConsumer() *conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pickConsumerLocked()
}

func (s *agentState) awaitDelivery(ctx context.Context, req jsonrpcEnvelope, props a2a.UserProperties, identity *auth.Identity,
	facts requestFacts, m *broker.Message) (*conn, *encodedTask) {
	for {
		target := s.awaitDeliverableLocked(ctx, req.ID)
		b := s.tasks[req.ID]
		switch {
		case b != nil && b.final:
			s.mu.Unlock()
			s.a.logger.Debug("agent link acknowledged a task it already answered", "agentName", s.cfg.Name, "taskID", req.ID)
			m.Ack()
			return nil, nil
		case s.stopped:
			s.mu.Unlock()
			s.refuseAndSettle(ctx, req.ID, props, msgAgentStopped, m)
			return nil, nil
		case b != nil && b.cancelled:
			s.mu.Unlock()
			s.settleCancelled(ctx, req.ID, props, m)
			return nil, nil
		case target == nil:
			s.mu.Unlock()
			s.returnToQueue(m)
			return nil, nil
		}
		requiredScopes, toolScopes := s.describedScopesLocked()
		s.mu.Unlock()

		if !s.mayRun(ctx, identity, requiredScopes) {
			s.a.logger.Warn("agent link refused a task", "agentName", s.cfg.Name, "taskID", req.ID, "traceID", props.TraceID, "reason", "insufficient scopes")
			s.refuseAndSettle(ctx, req.ID, props, msgAccessDenied, m)
			return nil, nil
		}
		task, err := encodeTask(req.ID, m.Redelivered, taskFor(facts, props, agent.RequestContextOf(facts.message, props), s.cfg.ShareUserProfile,
			s.grantedToolScopes(ctx, identity, toolScopes)))
		if err != nil || task.size() > s.a.cfg.MaxFrameBytes {
			s.a.logger.Warn("agent link refused a task too large to send", "agentName", s.cfg.Name, "taskID", req.ID, "traceID", props.TraceID, "bytes", task.size())
			s.refuseAndSettle(ctx, req.ID, props, fmt.Sprintf(msgRequestTooLarge, s.a.cfg.MaxFrameBytes), m)
			return nil, nil
		}

		s.mu.Lock()
		b = s.tasks[req.ID]
		nowRequired, nowTools := s.describedScopesLocked()
		unchanged := !s.stopped && slices.Equal(nowRequired, requiredScopes) && slices.Equal(nowTools, toolScopes) && target.deliverableLocked() &&
			len(target.inflight) < s.a.cfg.MaxInflight && (b == nil || (!b.final && !b.cancelled))
		if unchanged {
			return target, task
		}
		s.mu.Unlock()
	}
}

func (s *agentState) describedScopesLocked() ([]string, []string) {
	if s.described == nil {
		return nil, nil
	}
	return s.described.requiredScopes, s.described.toolScopes
}

func (s *agentState) refusedAsStopped(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.tasks[taskID]
	return s.stopped && (b == nil || !b.final)
}

func (s *agentState) settleCancelled(ctx context.Context, taskID string, props a2a.UserProperties, m *broker.Message) {
	if _, err := s.answerIfCancelled(ctx, taskID); err != nil {
		s.a.logger.Warn("agent link could not answer a cancelled task", "agentName", s.cfg.Name, "taskID", taskID, "traceID", props.TraceID, "error", err)
		s.returnToQueue(m)
		return
	}
	m.Ack()
}

func (s *agentState) rewakeLocked() (uint64, bool) {
	if !s.wakeable() || s.retired() || !s.a.serves() || s.stopped || s.hasConsumerLocked() {
		return 0, false
	}
	now := s.a.now()
	if s.awaitingConnect && now.Sub(s.lastWake) < wakeInterval {
		return 0, false
	}
	s.copies.forgetWaiting()
	return s.claimLocked(now), true
}

func (s *agentState) awaitDeliverableLocked(ctx context.Context, taskID string) *conn {
	for {
		s.mu.Lock()
		if b := s.tasks[taskID]; s.stopped || !s.hasConsumerLocked() || (b != nil && (b.final || b.cancelled)) {
			return nil
		}
		if target := s.pickConsumerLocked(); target != nil {
			return target
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			return nil
		case <-s.capacity:
		case <-time.After(time.Second):
		}
	}
}

func (s *agentState) pickConsumerLocked() *conn {
	ids := make([]string, 0, len(s.conns))
	for id, c := range s.conns {
		if c.deliverableLocked() && len(c.inflight) < s.a.cfg.MaxInflight {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sortStrings(ids)
	s.next++
	return s.conns[ids[s.next%len(ids)]]
}

func (s *agentState) deliver(ctx context.Context, m *broker.Message) {
	s.noteTaken(m)
	var req jsonrpcEnvelope
	if err := json.Unmarshal(m.Payload, &req); err != nil || req.ID == "" {
		s.a.logger.Warn("agent link dropped an unreadable request", "agentName", s.cfg.Name)
		m.Ack()
		return
	}
	if req.Method != methodSend && req.Method != methodStream && req.Method != methodCancel {
		s.a.logger.Warn("agent link dropped a request with an unknown method", "agentName", s.cfg.Name, "taskID", req.ID)
		m.Ack()
		return
	}
	if req.Method == methodCancel {
		taskID := cancelledTaskID(req.Params)
		if taskID == "" {
			s.a.logger.Debug("agent link dropped a cancel that names no task", "agentName", s.cfg.Name, "requestID", req.ID)
			m.Ack()
			return
		}
		s.deliverCancel(m, taskID)
		return
	}
	props := a2a.DecodeUserProperties(m.UserProperties)
	answerTo := findSignalRequestID(req.Params, a2a.SignalUserInputResponse)
	if answerTo == "" && s.answeredAlready(req.ID) {
		s.a.logger.Debug("agent link acknowledged a task it already answered", "agentName", s.cfg.Name, "taskID", req.ID)
		m.Ack()
		return
	}
	if s.refusedAsStopped(req.ID) {
		s.refuseAndSettle(ctx, req.ID, props, msgAgentStopped, m)
		return
	}
	identity, err := s.a.authenticate(ctx, props, req.ID)
	if err != nil {
		s.a.logger.Warn("agent link refused a task", "agentName", s.cfg.Name, "taskID", req.ID, "traceID", props.TraceID, "reason", "authentication failed")
		s.refuseAndSettle(ctx, req.ID, props, msgAuthFailed, m)
		return
	}
	if answerTo != "" {
		if !s.takeAnswer(ctx, req, props, answerTo) {
			s.dropUntakeable(ctx, req, props, answerTo, m)
			return
		}
		m.Ack()
		return
	}
	facts, readable := s.a.readRequest(req.Params)
	if !readable {
		s.dropUntakeable(ctx, req, props, "", m)
		return
	}
	target, task := s.awaitDelivery(ctx, req, props, identity, facts, m)
	if target == nil {
		return
	}
	now := s.a.now()
	if b := s.tasks[req.ID]; b != nil {
		b.boundAgainLocked(target.id, now.Add(s.a.cfg.TaskTTL))
	} else {
		s.tasks[req.ID] = &taskBinding{
			replyTo:     props.ReplyTo,
			statusTopic: props.A2AStatusTopic,
			props:       copyProps(m.UserProperties),
			identity:    identity,
			connID:      target.id,
			deliveredAt: now,
			deadline:    now.Add(s.a.cfg.TaskTTL),
			request:     facts,
			redelivered: m.Redelivered,
		}
	}
	target.lastDelivery++
	delivery := target.lastDelivery
	target.inflight[delivery] = handedTask{request: m, taskID: req.ID}
	sent := target.sendEncoded(task.frame(delivery))
	if !sent {
		delete(target.inflight, delivery)
		s.giveBackLocked(m)
		s.giveBacksInFlight++
	}
	s.mu.Unlock()
	if s.afterTaskBound != nil {
		s.afterTaskBound()
	}
	if !sent {
		s.giveBack(m)
		s.gaveBack()
	}
}

func (b *taskBinding) boundAgainLocked(connID string, deadlineOfAFreshRun time.Time) {
	b.connID, b.acknowledged, b.requestLost = connID, false, false
	b.answerFailingSince = time.Time{}
	if b.unanswered != nil {
		b.unanswered, b.deadline = nil, deadlineOfAFreshRun
	}
}

type encodedTask struct {
	head []byte
	tail []byte
}

func encodeTask(taskID string, redelivered bool, task *taskObject) (*encodedTask, error) {
	id, err := json.Marshal(taskID)
	if err != nil {
		return &encodedTask{}, err
	}
	body, err := json.Marshal(task)
	if err != nil {
		return &encodedTask{}, err
	}
	tail := make([]byte, 0, len(id)+len(body)+64)
	tail = append(append(tail, `,"taskId":`...), id...)
	if redelivered {
		tail = append(tail, `,"redelivered":true`...)
	}
	tail = append(append(append(tail, `,"task":`...), body...), '}')
	return &encodedTask{head: []byte(`{"type":"task","delivery":`), tail: tail}, nil
}

func (t *encodedTask) size() int64 { return int64(len(t.head)+len(t.tail)) + 20 }

func (t *encodedTask) frame(delivery uint64) []byte {
	frame := make([]byte, 0, t.size())
	frame = strconv.AppendUint(append(frame, t.head...), delivery, 10)
	return append(frame, t.tail...)
}

func (s *agentState) deliverCancel(m *broker.Message, taskID string) {
	if s.absorbCopyApplied(taskID) || s.applyCancel(taskID, false) || s.absorbCopyApplied(taskID) {
		m.Ack()
		return
	}
	s.mu.Lock()
	b := s.tasks[taskID]
	target := s.cancelTargetLocked(b)
	stopped := s.stopped
	s.mu.Unlock()
	switch {
	case target != nil && b != nil:
		s.sendOrHold(target, taskID, outFrame{Type: typeCancel, TaskID: taskID})
		m.Ack()
	case target != nil:
		if !target.send(outFrame{Type: typeCancel, TaskID: taskID}) {
			s.returnToQueue(m)
			return
		}
		m.Ack()
	case b != nil, stopped:
		m.Ack()
	default:
		s.returnToQueue(m)
	}
}

func (s *agentState) absorbCopyApplied(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.tasks[taskID]
	if b == nil || !b.cancelCopyApplied {
		return false
	}
	b.cancelCopyApplied = false
	return true
}

func (s *agentState) applyCancel(taskID string, fromCopy bool) bool {
	s.mu.Lock()
	b := s.tasks[taskID]
	if b == nil || b.final || b.cancelled || (fromCopy && s.settling) {
		s.mu.Unlock()
		return false
	}
	b.cancelled, b.cancelCopyApplied = true, fromCopy
	if grace := s.a.now().Add(cancelGrace); grace.Before(b.deadline) {
		b.deadline = grace
	}
	target := s.cancelTargetLocked(b)
	s.mu.Unlock()
	s.signalCapacity()
	s.sendOrHold(target, taskID, outFrame{Type: typeCancel, TaskID: taskID})
	s.releaseTask(taskID)
	return true
}

func (s *agentState) onRequestCopy(m *broker.Message) {
	taskID := cancelTarget(m.Payload)
	if taskID != "" && s.applyCancel(taskID, true) {
		s.a.logger.Debug("agent link applied a cancel from the request topic", "agentName", s.cfg.Name, "taskID", taskID)
	}
}

func cancelTarget(payload []byte) string {
	if len(payload) > maxCancelBytes {
		return ""
	}
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(payload, &req) != nil || req.Method != methodCancel {
		return ""
	}
	return cancelledTaskID(req.Params)
}

func cancelledTaskID(params json.RawMessage) string {
	var p struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(params, &p) != nil {
		return ""
	}
	return p.ID
}

func (s *agentState) answerIfCancelled(ctx context.Context, taskID string) (bool, error) {
	s.mu.Lock()
	b := s.tasks[taskID]
	if b == nil || b.final || !b.cancelled {
		s.mu.Unlock()
		return false, nil
	}
	run := s.runLocked(taskID, b)
	resp := s.answerLocked(inFrame{TaskID: taskID, State: stateCanceled}, b, run, nil)
	b.final, b.closedAt = true, s.a.now()
	up, replyTo := a2a.DecodeUserProperties(b.props), b.replyTo
	s.mu.Unlock()
	if replyTo != "" {
		if err := s.publishAnswer(ctx, replyTo, resp, up, s.answerSeq(b, taskID)); err != nil {
			s.reopenUnanswered(b, failedByStandDown)
			return true, err
		}
	}
	s.endTask(taskID)
	return true, nil
}

func (s *agentState) cancelTargetLocked(b *taskBinding) *conn {
	if b != nil {
		if c := s.conns[b.connID]; c != nil && !c.isClosed() {
			return c
		}
	}
	for _, c := range s.conns {
		if c.consuming && !c.isClosed() {
			return c
		}
	}
	return nil
}

func (s *agentState) refuse(ctx context.Context, taskID string, props a2a.UserProperties, message string) error {
	if props.ReplyTo == "" {
		return nil
	}
	body, err := json.Marshal(a2a.NewErrorResponse(taskID, codeInvalidRequest, message))
	if err != nil {
		return err
	}
	if err := s.publish(ctx, props.ReplyTo, body, s.responseProps(copyPropsOf(props)), true); err != nil {
		s.a.logger.Warn("agent link refusal publish failed", "agentName", s.cfg.Name, "taskID", taskID, "error", err)
		return err
	}
	return nil
}

func (s *agentState) refuseAndSettle(ctx context.Context, taskID string, props a2a.UserProperties, message string, m *broker.Message) {
	held := s.claimRedelivered(taskID, props, m)
	if s.refuse(ctx, taskID, props, message) != nil {
		leftByAStandDown := held != nil && s.reopenWithItsRequestGoingBack(held)
		s.returnToQueue(m)
		if leftByAStandDown {
			s.forgetUnlessItsRequestWasLost(ctx, taskID)
		}
		return
	}
	if held != nil {
		s.a.logger.Warn("agent link closed a task whose request, delivered again, was refused", "agentName", s.cfg.Name,
			"taskID", taskID, "traceID", props.TraceID)
		s.endTask(taskID)
	}
	m.Ack()
}

func (s *agentState) claimRedelivered(taskID string, props a2a.UserProperties, m *broker.Message) *taskBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.tasks[taskID]
	if b == nil || b.final || !m.Redelivered || props.ReplyTo == "" || props.ReplyTo != b.replyTo {
		return nil
	}
	b.final, b.closedAt = true, s.a.now()
	return b
}

func (s *agentState) answeredAlready(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.tasks[taskID]
	return b != nil && b.final
}

func (s *agentState) responseProps(request map[string]string) map[string]string {
	props := map[string]string{
		a2a.PropDelegatingAgentName: s.cfg.Name,
		a2a.PropTimestamp:           strconv.FormatInt(s.a.now().UnixMilli(), 10),
	}
	for _, k := range []string{a2a.PropClientID, a2a.PropUserID, a2a.PropA2AUserConfig, a2a.PropAuthToken, a2a.PropTraceID} {
		if v := request[k]; v != "" {
			props[k] = v
		}
	}
	return props
}

func (s *agentState) boundedPublish(ctx context.Context, publish func(context.Context)) {
	pctx, cancel := context.WithTimeout(ctx, s.a.publishWait)
	defer cancel()
	publish(pctx)
}

func (s *agentState) publish(ctx context.Context, topic string, payload []byte, props map[string]string, guaranteed bool) error {
	pctx, cancel := context.WithTimeout(ctx, s.a.publishWait)
	defer cancel()
	msg := &broker.Message{Payload: payload, UserProperties: props}
	if guaranteed {
		return s.brk().PublishGuaranteed(pctx, topic, msg)
	}
	return s.brk().Publish(pctx, topic, msg)
}

func (s *agentState) onDirect(ctx context.Context, m *broker.Message) {
	sub := m.Topic[strings.LastIndex(m.Topic, "/")+1:]
	if strings.Contains(m.Topic, "/agent/response/") {
		s.router.HandleResponse(sub, m)
	} else {
		s.router.HandleStatus(sub, m)
	}
}

func (s *agentState) instanceID() string { return s.cfg.Name + instanceSuffix }

func linkExtensions(cfg *AgentConfig) []a2a.AgentExtension {
	var exts []a2a.AgentExtension
	if cfg.Wake != nil {
		exts = append(exts, a2a.AgentExtension{URI: a2a.ExtensionURIOnDemand})
	}
	if registration := registrationParams(cfg); len(registration) > 0 {
		exts = append(exts, a2a.AgentExtension{URI: a2a.ExtensionURIRegistration, Params: registration})
	}
	return exts
}

func registrationParams(cfg *AgentConfig) map[string]any {
	params := map[string]any{}
	if cfg.Owner != "" {
		params["owner"] = cfg.Owner
	}
	if cfg.Team != "" {
		params["team"] = cfg.Team
	}
	return params
}

func (s *agentState) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

func (s *agentState) setStopped(stopped bool) {
	s.applyStopState(stopped, nil)
}

func (s *agentState) applyStopState(stopped bool, readAt *uint64) bool {
	s.mu.Lock()
	if readAt != nil && *readAt != s.controlGen {
		s.mu.Unlock()
		return false
	}
	s.controlGen++
	if readAt == nil && stopped {
		s.lastStopCommand = s.a.now()
	}
	changed := s.stopped != stopped
	s.stopped = stopped
	if changed {
		for _, c := range s.conns {
			sendControl(c, stopped)
		}
	}
	s.mu.Unlock()
	if !changed {
		return true
	}
	s.signalCapacity()
	s.a.logger.Warn("agent link agent control changed", "agentName", s.cfg.Name, "enabled", !stopped)
	if stopped {
		s.ensureDispatch()
	} else {
		s.stopDispatchIfIdle()
		if s.wakeable() && s.cfg.Wake.OnStart {
			s.startListing(true)
		}
	}
	s.requestCardSync()
	return true
}

func sendControl(c *conn, stopped bool) {
	enabled := !stopped
	reason := "released_by_sam"
	if stopped {
		reason = "stopped_by_sam"
	}
	c.send(outFrame{Type: typeControl, Enabled: &enabled, Reason: reason})
}

func (s *agentState) sweep(now time.Time) {
	s.mu.Lock()
	s.copies.expire(now)
	var expired []string
	for id, b := range s.tasks {
		switch {
		case b.final && now.Sub(b.closedAt) > closedTaskGrace:
			delete(s.tasks, id)
		case !b.final && now.After(b.deadline):
			expired = append(expired, id)
		}
	}
	s.mu.Unlock()
	for _, id := range expired {
		s.expire(s.ctx, id, now)
	}
	if !s.wakeable() || !s.cfg.Wake.OnStart || s.listing.Load() {
		return
	}
	if s.listed() {
		s.listingGaveUpAt.Store(0)
		s.listingGiveUps.Store(0)
		return
	}
	gaveUpAt := s.listingGaveUpAt.Load()
	gaveUpLately := gaveUpAt != 0 && now.Sub(time.Unix(0, gaveUpAt)) < relistWait(s.listingGiveUps.Load())
	if !gaveUpLately && !s.isStopped() {
		s.startListing(false)
	}
}

type taskFailure struct {
	kind             string
	answer           string
	logged           string
	loggedAt         slog.Level
	publishFailedLog string
}

const publishOfATaskFailureFailed = "agent link could not publish the failure of a task"

var (
	failedAtItsTimeLimit = taskFailure{
		kind:             "expired",
		answer:           msgTaskExpired,
		logged:           "agent link task expired unanswered",
		loggedAt:         slog.LevelWarn,
		publishFailedLog: "agent link expired task answer publish failed",
	}
	failedByStandDown = taskFailure{
		kind:             "link moved",
		answer:           msgLinkMoved,
		logged:           "agent link failed a task its agent had not answered: this replica stopped serving the agent",
		loggedAt:         slog.LevelInfo,
		publishFailedLog: publishOfATaskFailureFailed,
	}
	failedWithItsRequestLost = taskFailure{
		kind:             "request lost at a stand-down",
		answer:           msgRequestLost,
		logged:           "agent link failed a task whose request it could not return to the queue as this replica stopped serving the agent",
		loggedAt:         slog.LevelInfo,
		publishFailedLog: publishOfATaskFailureFailed,
	}
	failedWithItsRequestNotBack = taskFailure{
		kind:             "request not returned",
		answer:           msgRequestNotBack,
		logged:           "agent link failed a task whose request it could not return to the queue",
		loggedAt:         slog.LevelWarn,
		publishFailedLog: publishOfATaskFailureFailed,
	}
	failedByRemoval = taskFailure{
		kind:             "agent removed",
		answer:           msgAgentRemoved,
		logged:           "agent link failed a task its agent had not answered: the agent was removed from this entrypoint",
		loggedAt:         slog.LevelInfo,
		publishFailedLog: publishOfATaskFailureFailed,
	}
	failedByRedeploy = taskFailure{
		kind:             "agent redeployed",
		answer:           msgAgentRedeployed,
		logged:           "agent link failed a task its agent had not answered: the agent was deployed again with new settings",
		loggedAt:         slog.LevelInfo,
		publishFailedLog: publishOfATaskFailureFailed,
	}
	failedByRedeployWithItsRequestLost = taskFailure{
		kind:             "request lost at a redeploy",
		answer:           msgRedeployLostIt,
		logged:           "agent link failed a task whose request it could not return to the queue as its agent was deployed again",
		loggedAt:         slog.LevelInfo,
		publishFailedLog: publishOfATaskFailureFailed,
	}
	canceledInItsAgentsStead = taskFailure{
		kind:             "canceled",
		publishFailedLog: "agent link cancelled task answer publish failed",
	}
)

const answeredACancelledTask = "agent link answered a cancelled task its agent left unanswered"

func (s *agentState) expire(ctx context.Context, taskID string, now time.Time) {
	s.failOpenTask(ctx, taskID, failedAtItsTimeLimit, func(b *taskBinding) bool { return !s.settling && now.After(b.deadline) })
}

func (s *agentState) failOpenTask(ctx context.Context, taskID string, failure taskFailure, due func(*taskBinding) bool) {
	s.mu.Lock()
	b := s.tasks[taskID]
	if b == nil || b.final || !due(b) {
		s.mu.Unlock()
		return
	}
	if b.unanswered != nil {
		failure = *b.unanswered
	}
	up := a2a.DecodeUserProperties(b.props)
	ids := []any{"agentName", s.cfg.Name, "taskID", taskID, "traceID", up.TraceID}
	if b.cancelled {
		s.mu.Unlock()
		switch answered, err := s.answerIfCancelled(ctx, taskID); {
		case err != nil:
			s.answerNotPublished(ctx, taskID, b, canceledInItsAgentsStead, up.TraceID, err)
		case answered:
			s.a.logger.Log(ctx, failure.loggedAt, answeredACancelledTask, ids...)
		}
		return
	}
	b.final, b.closedAt = true, s.a.now()
	replyTo := b.replyTo
	var resp *a2a.Response
	if replyTo != "" {
		resp = s.runLocked(taskID, b).errorResponseLocked(taskID, failure.answer)
	}
	s.mu.Unlock()
	if replyTo != "" {
		if err := s.publishAnswer(ctx, replyTo, resp, up, s.answerSeq(b, taskID)); err != nil {
			s.reopenUnanswered(b, failure)
			s.answerNotPublished(ctx, taskID, b, failure, up.TraceID, err)
			return
		}
	}
	s.a.logger.Log(ctx, failure.loggedAt, failure.logged, ids...)
	s.endTaskWithin(ctx, taskID)
}

func (s *agentState) warnOfSharedName() {
	for _, other := range s.a.core.AgentRegistry().InstanceIDs(s.cfg.Name) {
		if other == s.instanceID() || s.nameSharedWith[other] {
			continue
		}
		if s.nameSharedWith == nil {
			s.nameSharedWith = map[string]bool{}
		}
		s.nameSharedWith[other] = true
		s.a.logger.Warn("agent link agent shares its name with another agent on the mesh", "agentName", s.cfg.Name,
			"otherInstance", other)
	}
}

func copyProps(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyPropsOf(up a2a.UserProperties) map[string]string {
	return a2a.EncodeUserProperties(&up)
}

func validTopicLevel(s string) bool {
	return s != "" && len(s) <= maxIDBytes && !strings.ContainsAny(s, "/*> \t\n")
}

func findSignalRequestID(raw json.RawMessage, signal a2a.SignalType) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return searchSignal(v, string(signal), 0)
}

func searchSignal(v any, signal string, depth int) string {
	if depth > 12 {
		return ""
	}
	switch x := v.(type) {
	case map[string]any:
		if x["type"] == signal {
			if id, ok := x["request_id"].(string); ok && id != "" {
				return id
			}
		}
		for _, child := range x {
			if id := searchSignal(child, signal, depth+1); id != "" {
				return id
			}
		}
	case []any:
		for _, child := range x {
			if id := searchSignal(child, signal, depth+1); id != "" {
				return id
			}
		}
	}
	return ""
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

var errMissingToken = errors.New("missing auth token")
