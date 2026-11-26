// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type replicaBroker struct {
	*trackingBroker
	requestQueueBinds    atomic.Int32
	requestQueueReleases atomic.Int32
	wakeQueueReleases    atomic.Int32
	cards                atomic.Int32
	removalCards         atomic.Int32
	requestQueueHeld     atomic.Value
	heldPublishes        sync.Map
	repliesStall         atomic.Bool
	heldRequeues         atomic.Pointer[gate]
	heldReceives         atomic.Pointer[gate]
}

func (b *replicaBroker) holdRequestQueueReleases(t *testing.T) (letGo func()) {
	held := make(chan struct{})
	b.requestQueueHeld.Store(held)
	var once sync.Once
	letGo = func() { once.Do(func() { close(held) }) }
	t.Cleanup(letGo)
	return letGo
}

func (b *replicaBroker) holdPublishesTo(t *testing.T, topic string) *gate {
	held := shutGate(t)
	b.heldPublishes.Store(topic, held)
	return held
}

func (b *replicaBroker) holdRequeues(t *testing.T) *gate {
	held := shutGate(t)
	b.heldRequeues.Store(held)
	return held
}

func (b *replicaBroker) holdReceivedRequests(t *testing.T) *gate {
	held := shutGate(t)
	b.heldReceives.Store(held)
	return held
}

func (b *replicaBroker) holdReceived() {
	if held := b.heldReceives.Load(); held != nil {
		_ = held.pass()
	}
}

func (b *replicaBroker) CreateQueue(name string, opts broker.QueueOptions) (broker.Queue, error) {
	if name == a2a.QueueName(testNS, "a2a", agentName) {
		b.requestQueueBinds.Add(1)
	}
	return b.trackingBroker.CreateQueue(name, opts)
}

func (b *replicaBroker) Publish(ctx context.Context, topic string, m *broker.Message) error {
	if topic == a2a.DiscoveryAgentCardsTopic(testNS) {
		if removal(m) {
			b.removalCards.Add(1)
		} else {
			b.cards.Add(1)
		}
	}
	if held, ok := b.heldPublishes.Load(topic); ok {
		if err := held.(*gate).pass(); err != nil {
			return err
		}
	}
	return b.trackingBroker.Publish(ctx, topic, m)
}

func isReply(topic string) bool {
	return strings.HasPrefix(topic, strings.TrimSuffix(responseTopic("T"), "T"))
}

func (b *replicaBroker) PublishGuaranteed(ctx context.Context, topic string, m *broker.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.repliesStall.Load() && isReply(topic) {
		<-ctx.Done()
		return ctx.Err()
	}
	if held, ok := b.heldPublishes.Load(topic); ok {
		if err := held.(*gate).pass(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return b.trackingBroker.PublishGuaranteed(ctx, topic, m)
}

func (b *replicaBroker) Requeue(queueName string, m *broker.Message) error {
	if held := b.heldRequeues.Load(); held != nil {
		if err := held.pass(); err != nil {
			return err
		}
	}
	return b.trackingBroker.Requeue(queueName, m)
}

func (b *replicaBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	m, err := b.trackingBroker.Receive(ctx, q)
	if m != nil && err == nil && isAgentQueue(q) {
		b.holdReceived()
	}
	return m, err
}

type flowDelivery struct {
	original *broker.Message
}

type flowBroker struct {
	*replicaBroker
	settledAfterRelease atomic.Int32
	discarded           atomic.Int32

	mu        sync.Mutex
	unsettled map[*broker.Message]flowDelivery
}

func (*flowBroker) Bound(broker.Queue) bool { return true }

func (b *flowBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	if !isAgentQueue(q) {
		return b.trackingBroker.Receive(ctx, q)
	}
	b.receiving.Add(1)
	m, err := b.DevBroker.Receive(ctx, q)
	b.receiving.Add(-1)
	if m == nil || err != nil || !strings.Contains(m.Topic, "/agent/request/") {
		return m, err
	}
	b.taken.Add(1)
	var out *broker.Message
	out = broker.NewDeliveredMessage(m.ID, m.Topic, m.Payload, func() { b.settle(out, true) }, func() { b.settle(out, false) })
	out.UserProperties, out.ReplyTo, out.CorrelationID, out.Redelivered = m.UserProperties, m.ReplyTo, m.CorrelationID, m.Redelivered
	b.mu.Lock()
	b.unsettled[out] = flowDelivery{original: m}
	b.mu.Unlock()
	b.holdReceived()
	return out, nil
}

func (b *flowBroker) settle(delivered *broker.Message, acknowledged bool) {
	b.mu.Lock()
	delivery, bound := b.unsettled[delivered]
	delete(b.unsettled, delivered)
	b.mu.Unlock()
	switch {
	case !bound:
		b.settledAfterRelease.Add(1)
	case acknowledged:
		b.acks.Add(1)
	default:
		b.nacks.Add(1)
		b.redeliver(delivery)
	}
}

func (b *flowBroker) redeliver(delivery flowDelivery) {
	if err := b.DevBroker.Requeue(a2a.QueueName(testNS, "a2a", agentName), delivery.original); err != nil {
		b.discarded.Add(1)
	}
}

func (b *flowBroker) DeleteQueue(name string) error {
	switch {
	case name == a2a.QueueName(testNS, "a2a", agentName):
		b.requestQueueReleases.Add(1)
		if held, _ := b.requestQueueHeld.Load().(chan struct{}); held != nil {
			<-held
		}
		b.mu.Lock()
		unsettled := b.unsettled
		b.unsettled = map[*broker.Message]flowDelivery{}
		b.mu.Unlock()
		for _, delivery := range unsettled {
			b.redeliver(delivery)
		}
	case strings.Contains(name, "/q/agentlink/wake/"):
		b.wakeQueueReleases.Add(1)
	}
	return nil
}

type replica struct {
	*rig
	link *replicaBroker
	flow *flowBroker
}

type replicasPlan struct {
	agentCfg        map[string]any
	releasesBinds   bool
	neverRedelivers bool
	authn           auth.Authenticator
	tune            func(*Adapter)
	opts            []runtime.Option
}

func newReplicas(t *testing.T, plan replicasPlan) (*replica, *replica) {
	t.Helper()
	dev := connectedDevBroker(t)
	return newReplica(t, dev, plan), newReplica(t, dev, plan)
}

func newReplica(t *testing.T, dev *broker.DevBroker, plan replicasPlan) *replica {
	t.Helper()
	rep := &replica{}
	var authn auth.Authenticator = fakeAuth{}
	if plan.authn != nil {
		authn = plan.authn
	}
	rep.rig = startRig(t, rigPlan{
		dev:    dev,
		agents: []any{claimsAgent(plan.agentCfg)},
		authn:  authn,
		opts:   plan.opts,
		wrap: func(b *trackingBroker) broker.Broker {
			b.requeuesGiveBacks.Store(!plan.releasesBinds && !plan.neverRedelivers)
			rep.link = &replicaBroker{trackingBroker: b}
			if plan.releasesBinds {
				rep.flow = &flowBroker{replicaBroker: rep.link, unsettled: map[*broker.Message]flowDelivery{}}
				return rep.flow
			}
			return rep.link
		},
		prepare: func(a *Adapter, core *entrypoint.GatewayCore) {
			core.UseLeaderIngress()
			if plan.tune != nil {
				plan.tune(a)
			}
		},
	})
	return rep
}

func onBothBrokerKinds(t *testing.T, test func(t *testing.T, releasesBinds bool)) {
	t.Helper()
	for name, releasesBinds := range map[string]bool{
		"a dev broker that requeues and whose nack does nothing":      false,
		"a broker with flows that redelivers on unbind and on a nack": true,
	} {
		t.Run(name, func(t *testing.T) { test(t, releasesBinds) })
	}
}

type ledTerm struct {
	t       *testing.T
	replica *replica
	number  uint64
	ctx     context.Context
	cancel  context.CancelFunc
	started *leaderTerm
}

func (r *replica) lead(number uint64) *ledTerm {
	r.t.Helper()
	term := r.startTerm(number)
	waitFor(r.t, func() bool {
		leading := r.adapter.leading.Load()
		return leading != nil && leading.number == number
	})
	return term
}

func (r *replica) startTerm(number uint64) *ledTerm {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r.t.Cleanup(cancel)
	r.adapter.RunIngress(ctx, number)
	r.adapter.termMu.Lock()
	started := r.adapter.term
	r.adapter.termMu.Unlock()
	if started == nil || started.number != number {
		r.t.Fatalf("term %d was not taken up", number)
	}
	return &ledTerm{t: r.t, replica: r, number: number, ctx: ctx, cancel: cancel, started: started}
}

func (term *ledTerm) ended() {
	term.t.Helper()
	select {
	case <-term.started.ended:
	case <-time.After(arrivalWait):
		term.t.Fatal("the term never ended")
	}
}

const closeWait = 10 * time.Second

func closeOf(t *testing.T, c *client) (code, framesBefore int) {
	t.Helper()
	deadline := time.After(closeWait)
	for {
		select {
		case _, open := <-c.in:
			if open {
				framesBefore++
				continue
			}
			var closed *websocket.CloseError
			if !errors.As(c.readErr, &closed) {
				t.Fatalf("connection ended with %v", c.readErr)
			}
			return closed.Code, framesBefore
		case <-deadline:
			t.Fatal("the connection was never closed")
		}
	}
}

func (term *ledTerm) tick() {
	term.replica.adapter.RunIngress(term.ctx, term.number)
}

const standDownWithNothingToWaitFor = defaultSettleWait / 2

func (term *ledTerm) end() {
	term.t.Helper()
	term.cancel()
	term.endsWithin(standDownWithNothingToWaitFor)
}

func (term *ledTerm) endsWithin(limit time.Duration) {
	term.t.Helper()
	began := time.Now()
	select {
	case <-term.started.ended:
	case <-time.After(limit):
		term.t.Fatalf("the stand-down had not ended after %v, want it done well inside its %v deadline", time.Since(began), defaultSettleWait)
	}
}

func (r *replica) refusesAsFollower(tok string) {
	r.t.Helper()
	_, resp, err := r.dial(agentName, tok)
	if err == nil || resp == nil {
		r.t.Fatalf("a follower accepted a connection (err %v)", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "1" {
		r.t.Fatalf("a follower answered %d with Retry-After %q, want 503 with Retry-After 1", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

func closedForServiceRestart(t *testing.T, c *client) {
	t.Helper()
	if code, _ := closeOf(t, c); code != websocket.CloseServiceRestart {
		t.Fatalf("the connection closed with %d, want %d (service restart)", code, websocket.CloseServiceRestart)
	}
}

func (r *replica) connectAttempts() int {
	attempts := 0
	r.locked(func(s *agentState) { attempts = s.connectsInWindow })
	return attempts
}

func (r *replica) watchesWakes() bool {
	select {
	case <-r.adapter.agent(agentName).wakeWatching:
		return true
	default:
		return false
	}
}

func requestDriven(url string, onStart bool) map[string]any {
	return map[string]any{"wake": map[string]any{"url": url, "on_start": onStart}}
}

func TestAFollowerAnswers503BeforeCheckingCredentialsAndTheLeaderAccepts(t *testing.T) {
	audit := &authEvents{}
	a, b := newReplicas(t, replicasPlan{opts: []runtime.Option{runtime.WithAuditLogger(audit)}})
	a.refusesAsFollower(token)
	a.lead(1)

	for _, tok := range []string{token, "not-the-agents-token", ""} {
		b.refusesAsFollower(tok)
	}
	if attempts := b.connectAttempts(); attempts != 0 {
		t.Fatalf("the follower counted %d connects against the agent's rate, want 0", attempts)
	}
	audit.mu.Lock()
	audited := len(audit.events)
	audit.mu.Unlock()
	if audited != 0 {
		t.Fatalf("refusals by a follower wrote %d audit events, want 0", audited)
	}

	c := a.connect()
	a.consume(c)
	a.taskOn(c, "T1", nil)
}

func TestAConnectionWhoseHandshakeEndsAfterTheTermDoesNotJoin(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	s := a.adapter.agent(agentName)
	term.end()

	if outcome := s.join(newConn(a.adapter, s, nil)); outcome != joinRefusedNotLeading {
		t.Fatalf("join outcome %d, want %d (refused: not leading)", outcome, joinRefusedNotLeading)
	}
	if open := len(s.connections()); open != 0 {
		t.Fatalf("%d connections joined a replica that does not lead", open)
	}
}

func TestTasksAreDeliveredOnTheLeaderAndAFollowerNeverTakesFromTheRequestQueue(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		a.lead(1)
		c := a.connect()
		a.consume(c)

		const tasks = 6
		for i := range tasks {
			taskID := fmt.Sprintf("T%d", i)
			b.submit(taskID, "tok:alice:"+taskID, nil)
			f := c.next(typeTask)
			if f.TaskID != taskID || f.Redelivered {
				t.Fatalf("got task %s redelivered=%t, want %s delivered once", f.TaskID, f.Redelivered, taskID)
			}
			c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
		}
		waitFor(t, func() bool { return a.brk.acks.Load() == tasks })
		c.quiet()
		if taken, binds := b.brk.taken.Load(), b.link.requestQueueBinds.Load(); taken != 0 || binds != 0 {
			t.Fatalf("the follower bound the request queue %d times and took %d requests, want neither", binds, taken)
		}
		if taken := a.brk.taken.Load(); taken != tasks {
			t.Fatalf("the leader took %d requests, want %d", taken, tasks)
		}
	})
}

func TestARequestDrivenAgentIsWokenOncePerRequestAndNeverWhileConnectedToTheLeader(t *testing.T) {
	url, wakes := countingRuntime(t)
	a, b := newReplicas(t, replicasPlan{agentCfg: requestDriven(url, false)})
	a.lead(1)
	a.awaitWakeWatch()

	b.submit("T0", "tok:alice:T0", nil)
	wokenTimes(t, wakes, 1)

	c := a.connect()
	a.consume(c)
	woken := c.next(typeTask)
	c.send(map[string]any{"type": typeAck, "delivery": woken.Delivery})
	const whileConnected = 10
	for i := 1; i <= whileConnected; i++ {
		taskID := fmt.Sprintf("T%d", i)
		b.submit(taskID, "tok:alice:"+taskID, nil)
		f := c.next(typeTask)
		c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
	}
	waitFor(t, func() bool { return a.brk.watched.Load() == 1+whileConnected })
	wokenTimes(t, wakes, 1)
	if b.watchesWakes() || b.brk.watched.Load() != 0 {
		t.Fatal("the follower watched the agent's requests to wake it")
	}
}

func TestOnStartWakesAnAgentOnceWithTwoReplicasAndRepeatedLeaderTicks(t *testing.T) {
	url, wakes := countingRuntime(t)
	a, _ := newReplicas(t, replicasPlan{agentCfg: requestDriven(url, true)})
	wokenTimes(t, wakes, 0)

	term := a.lead(1)
	a.awaitWakeWatch()
	for range 3 {
		term.tick()
	}
	wokenTimes(t, wakes, 1)
	if leading := a.adapter.leading.Load(); leading != term.started {
		t.Fatal("a repeated leader tick of the same term started the term again")
	}
}

func TestAHandoverClosesTheOldLeadersConnectionsAndTheNewLeaderRedeliversUnacknowledgedTasks(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		const republishEvery = 20 * time.Millisecond
		url, wakes := countingRuntime(t)
		a, b := newReplicas(t, replicasPlan{
			agentCfg:      requestDriven(url, false),
			releasesBinds: releasesBinds,
			tune:          func(adapter *Adapter) { adapter.cardRepublish = republishEvery },
		})
		term := a.lead(1)
		a.awaitWakeWatch()
		c := a.connect()
		a.consume(c)
		a.submit("T1", "tok:alice:T1", nil)
		if first := c.next(typeTask); first.TaskID != "T1" || first.Redelivered {
			t.Fatalf("got task %s redelivered=%t, want T1 delivered for the first time", first.TaskID, first.Redelivered)
		}
		waitFor(t, func() bool { return a.link.cards.Load() >= 3 && a.brk.watched.Load() == 1 })

		term.end()
		closedForServiceRestart(t, c)
		cardsWhileLeading := a.link.cards.Load()
		a.refusesAsFollower(token)

		b.lead(2)
		next := b.connect()
		b.consume(next)
		requireRedelivered(t, next.next(typeTask), "T1")
		waitFor(t, func() bool { return b.link.cards.Load() >= 3 })

		time.Sleep(10 * republishEvery)
		if cards, removals := a.link.cards.Load()-cardsWhileLeading, a.link.removalCards.Load(); cards != 0 || removals != 0 {
			t.Fatalf("the old leader published %d cards and %d removal cards after its term, want none", cards, removals)
		}
		if removals := b.link.removalCards.Load(); removals != 0 {
			t.Fatalf("the new leader published %d removal cards, want 0", removals)
		}
		if got := wakes.Load(); got != 0 {
			t.Fatalf("the handover woke the agent %d times, want 0 because it reconnects by itself", got)
		}
		if releasesBinds && (a.link.requestQueueReleases.Load() == 0 || a.link.wakeQueueReleases.Load() == 0) {
			t.Fatalf("the old leader released the request queue %d times and the wake queue %d times, want both released",
				a.link.requestQueueReleases.Load(), a.link.wakeQueueReleases.Load())
		}
		if !releasesBinds && !a.brk.HasQueue(a2a.QueueName(testNS, "a2a", agentName)) {
			t.Fatal("the old leader deleted the request queue of a broker that keeps no bind per replica")
		}
	})
}

func TestTwoReplicasLeadingAtOnceDeliverEachTaskOnce(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		termA := a.lead(1)
		b.lead(2)
		onA, onB := a.connect(), b.connect()
		a.consume(onA)
		b.consume(onB)

		const tasks = 20
		for i := range tasks {
			taskID := fmt.Sprintf("T%d", i)
			a.submit(taskID, "tok:alice:"+taskID, nil)
		}
		delivered := map[string]int{}
		for range tasks {
			c, f := nextOf(t, typeTask, onA, onB)
			delivered[f.TaskID]++
			c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
		}
		waitFor(t, func() bool { return a.brk.acks.Load()+b.brk.acks.Load() == tasks })
		onA.quiet()
		onB.quiet()
		if len(delivered) != tasks {
			t.Fatalf("%d distinct tasks were delivered, want %d: %v", len(delivered), tasks, delivered)
		}
		if taken := a.brk.taken.Load() + b.brk.taken.Load(); taken != tasks {
			t.Fatalf("the two leaders took %d requests between them, want %d", taken, tasks)
		}

		termA.end()
		closedForServiceRestart(t, onA)
		b.taskOn(onB, "T-after", nil)
	})
}

func TestAReplicaThatLeadsAgainServesAgain(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		url, _ := countingRuntime(t)
		a, _ := newReplicas(t, replicasPlan{agentCfg: requestDriven(url, false), releasesBinds: releasesBinds})
		first := a.lead(1)
		a.awaitWakeWatch()
		c := a.connect()
		a.consume(c)
		a.taskOn(c, "T1", nil)
		waitFor(t, func() bool { return a.brk.acks.Load() == 1 })

		first.end()
		closedForServiceRestart(t, c)
		a.refusesAsFollower(token)

		a.lead(2)
		again := a.connect()
		again.send(map[string]any{"type": typeConsume})
		again.refused(errCodeBadMessage, "")
		a.consume(again)
		a.taskOn(again, "T2", nil)
	})
}

func TestOnlyTheLeaderActsWhenSAMStopsAnAgentAndTheNextLeaderKeepsItStopped(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		waitFor(t, func() bool { return a.link.cards.Load() >= 1 })

		a.stop()
		waitFor(t, func() bool {
			return a.link.removalCards.Load() == 1 && a.brk.receiving.Load() == 1 && b.adapter.agent(agentName).isStopped()
		})
		time.Sleep(300 * time.Millisecond)
		if removals, binds := b.link.removalCards.Load(), b.link.requestQueueBinds.Load(); removals != 0 || binds != 0 {
			t.Fatalf("the follower published %d removal cards and bound the request queue %d times for a stopped agent, want neither", removals, binds)
		}

		term.end()
		closedForServiceRestart(t, c)
		if receiving := a.brk.receiving.Load(); receiving != 0 {
			t.Fatal("the old leader still receives the stopped agent's requests after its term")
		}
		b.lead(2)
		waitFor(t, func() bool { return b.link.removalCards.Load() == 1 })
		answers := b.listen(responseTopic("T1"))
		b.submit("T1", "tok:alice:T1", nil)
		if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAgentStopped) {
			t.Fatalf("the new leader answered %s, want the stopped agent's refusal", m.Payload)
		}
		reconnected, _, err := b.dial(agentName, token)
		if err != nil {
			t.Fatal(err)
		}
		if welcome := reconnected.next(typeWelcome); welcome.Enabled == nil || *welcome.Enabled {
			t.Fatalf("the new leader welcomed the stopped agent as running: %s", welcome.Raw)
		}
	})
}

func TestOnlyTheLeaderWakesAnAgentToListItWhenSAMReleasesIt(t *testing.T) {
	url, wakes := countingRuntime(t)
	var sinceStart atomic.Int64
	start := time.Now()
	a, b := newReplicas(t, replicasPlan{
		agentCfg: requestDriven(url, true),
		tune: func(adapter *Adapter) {
			adapter.now = func() time.Time { return start.Add(time.Duration(sinceStart.Load())) }
		},
	})
	a.lead(1)
	a.awaitWakeWatch()
	wokenTimes(t, wakes, 1)

	bothStopped := func(stopped bool) func() bool {
		return func() bool {
			return a.adapter.agent(agentName).isStopped() == stopped && b.adapter.agent(agentName).isStopped() == stopped
		}
	}
	a.stop()
	waitFor(t, bothStopped(true))
	sinceStart.Store(int64(2 * wakeInterval))
	a.release()
	waitFor(t, bothStopped(false))
	wokenTimes(t, wakes, 2)
}
