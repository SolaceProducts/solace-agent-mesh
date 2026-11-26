// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	samruntime "github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	billingDesk     = "billing-desk"
	collectionsDesk = "collections-desk"
	dispatchDesk    = "dispatch-desk"
	escalationsDesk = "escalations-desk"
	rotatedToken    = "link-token-rotated"
)

func tokenOf(name string) string { return "link-token-of-" + name }

func linkAgent(name string, settings ...map[string]any) map[string]any {
	entry := map[string]any{"name": name, "token_sha256": digest(tokenOf(name))}
	for _, more := range settings {
		maps.Copy(entry, more)
	}
	return entry
}

func withToken(tok string) map[string]any { return map[string]any{"token_sha256": digest(tok)} }

func anyList(agents []map[string]any) []any {
	list := make([]any, 0, len(agents))
	for _, agent := range agents {
		list = append(list, agent)
	}
	return list
}

type rosterBroker struct {
	*trackingBroker
	cards        atomic.Int32
	removalCards atomic.Int32
	requeued     atomic.Int32

	mu            sync.Mutex
	bound         map[string]int
	released      map[string]int
	deprovisioned map[string]int

	provisionFailsOnce sync.Map
	provisionHolds     sync.Map
	subscribeFailsOnce sync.Map
	onRelease          sync.Map
}

type provisionHold struct {
	entered chan struct{}
	release chan struct{}
}

func (b *rosterBroker) holdProvisioning(queue string) *provisionHold {
	hold := &provisionHold{entered: make(chan struct{}), release: make(chan struct{})}
	b.provisionHolds.Store(queue, hold)
	return hold
}

func (b *rosterBroker) failSubscribingOnce(topic string) { b.subscribeFailsOnce.Store(topic, true) }

func (b *rosterBroker) Subscribe(q broker.Queue, topic string) error {
	if _, failing := b.subscribeFailsOnce.LoadAndDelete(topic); failing {
		return errSubscribing
	}
	return b.trackingBroker.Subscribe(q, topic)
}

var errSubscribing = errors.New("the broker refused the subscription")

var errQueueProvisioning = errors.New("the broker refused the queue")

func (b *rosterBroker) failProvisioningOnce(queue string) { b.provisionFailsOnce.Store(queue, true) }

func (b *rosterBroker) ProvisionQueue(ctx context.Context, name string, opts broker.QueueOptions, topics ...string) error {
	if held, holding := b.provisionHolds.LoadAndDelete(name); holding {
		hold := held.(*provisionHold)
		close(hold.entered)
		<-hold.release
	}
	if _, failing := b.provisionFailsOnce.LoadAndDelete(name); failing {
		return errQueueProvisioning
	}
	return b.trackingBroker.ProvisionQueue(ctx, name, opts, topics...)
}

func (b *rosterBroker) count(counts map[string]int, name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	counts[name]++
}

func (b *rosterBroker) counted(counts map[string]int, name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return counts[name]
}

func (b *rosterBroker) binds(name string) int              { return b.counted(b.bound, name) }
func (b *rosterBroker) releases(name string) int           { return b.counted(b.released, name) }
func (b *rosterBroker) deprovisions(name string) int       { return b.counted(b.deprovisioned, name) }
func (b *rosterBroker) isDirectSubscription(n string) bool { return strings.HasPrefix(n, "direct-") }

func (b *rosterBroker) releasedDirectSubscriptions() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	released := 0
	for name, times := range b.released {
		if b.isDirectSubscription(name) {
			released += times
		}
	}
	return released
}

func (b *rosterBroker) releasedQueues() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	released := 0
	for name, times := range b.released {
		if !b.isDirectSubscription(name) {
			released += times
		}
	}
	return released
}

func (b *rosterBroker) CreateQueue(name string, opts broker.QueueOptions) (broker.Queue, error) {
	b.count(b.bound, name)
	return b.trackingBroker.CreateQueue(name, opts)
}

func (b *rosterBroker) DeleteQueue(name string) error {
	b.count(b.released, name)
	if hook, ok := b.onRelease.LoadAndDelete(name); ok {
		hook.(func())()
	}
	return b.trackingBroker.DeleteQueue(name)
}

func (b *rosterBroker) Requeue(queueName string, m *broker.Message) error {
	b.requeued.Add(1)
	return b.trackingBroker.Requeue(queueName, m)
}

func (b *rosterBroker) DeprovisionQueue(name string) error {
	b.count(b.deprovisioned, name)
	return b.trackingBroker.DeprovisionQueue(name)
}

func (b *rosterBroker) Publish(ctx context.Context, topic string, m *broker.Message) error {
	if topic == a2a.DiscoveryAgentCardsTopic(testNS) {
		if removal(m) {
			b.removalCards.Add(1)
		} else {
			b.cards.Add(1)
		}
	}
	return b.trackingBroker.Publish(ctx, topic, m)
}

type flowReleasingBroker struct{ *rosterBroker }

func (*flowReleasingBroker) Bound(broker.Queue) bool { return true }

func (b *flowReleasingBroker) DeleteQueue(name string) error {
	b.count(b.released, name)
	if b.isDirectSubscription(name) {
		return b.trackingBroker.DeleteQueue(name)
	}
	return nil
}

type linkRig struct {
	*rig
	flows  *rosterBroker
	limits map[string]any
}

type linkPlan struct {
	dev           *broker.DevBroker
	agents        []map[string]any
	releasesBinds bool
	leaderOnly    bool
	limits        map[string]any
	failsToStart  []string
	tune          func(*Adapter)
	opts          []samruntime.Option
}

func newLinkRig(t *testing.T, plan linkPlan) *linkRig {
	t.Helper()
	l := &linkRig{limits: plan.limits}
	dev := plan.dev
	if dev == nil {
		dev = connectedDevBroker(t)
	}
	l.rig = startRig(t, rigPlan{
		dev:    dev,
		agents: anyList(plan.agents),
		authn:  fakeAuth{},
		limits: plan.limits,
		opts:   plan.opts,
		wrap: func(b *trackingBroker) broker.Broker {
			b.requeuesGiveBacks.Store(true)
			l.flows = &rosterBroker{trackingBroker: b, bound: map[string]int{}, released: map[string]int{}, deprovisioned: map[string]int{}}
			for _, queue := range plan.failsToStart {
				l.flows.failProvisioningOnce(queue)
			}
			if plan.releasesBinds {
				return &flowReleasingBroker{l.flows}
			}
			return l.flows
		},
		prepare: func(a *Adapter, core *entrypoint.GatewayCore) {
			a.cardRepublish = time.Hour
			if plan.leaderOnly {
				core.UseLeaderIngress()
			}
			if plan.tune != nil {
				plan.tune(a)
			}
		},
	})
	return l
}

func onBothFlowKinds(t *testing.T, test func(t *testing.T, releasesBinds bool)) {
	t.Helper()
	onBothBrokerKinds(t, test)
}

func (l *linkRig) settings(agents ...map[string]any) map[string]any {
	return linkSettings(anyList(agents), l.limits)
}

func (l *linkRig) reconfigure(agents ...map[string]any) {
	l.t.Helper()
	if err := l.adapter.Reconfigure(l.t.Context(), l.settings(agents...)); err != nil {
		l.t.Fatalf("reconfigure: %v", err)
	}
}

func (l *linkRig) serving(name string, tok ...string) *client {
	l.t.Helper()
	presented := tokenOf(name)
	if len(tok) > 0 {
		presented = tok[0]
	}
	c := l.connectAs(name, presented)
	l.consumeAs(c, name)
	return c
}

func (l *linkRig) deliver(c *client, name, taskID string) received {
	l.t.Helper()
	l.submitTo(name, taskID, "tok:alice:"+taskID, nil)
	f := c.next(typeTask)
	if f.TaskID != taskID {
		l.t.Fatalf("task %s, want %s", f.TaskID, taskID)
	}
	return f
}

func acknowledge(c *client, f received) {
	c.t.Helper()
	c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
}

func complete(c *client, taskID string) {
	c.t.Helper()
	c.send(map[string]any{"type": typeResult, "taskId": taskID, "state": stateCompleted, "text": "done"})
}

func answered(t *testing.T, answers <-chan *broker.Message, taskID string) {
	t.Helper()
	if m := recv(t, answers); m.Topic != responseTopic(taskID) || !strings.Contains(string(m.Payload), `"completed"`) {
		t.Fatalf("an answer on %s, want task %s completed", m.Topic, taskID)
	}
}

func (l *linkRig) connectionIDs(name string) []string {
	var ids []string
	for _, c := range l.adapter.agent(name).connections() {
		ids = append(ids, c.id)
	}
	slices.Sort(ids)
	return ids
}

type connectRefusal struct {
	status     int
	retryAfter string
	body       string
}

func (l *linkRig) refusal(name, tok string) connectRefusal {
	l.t.Helper()
	_, resp, err := l.dial(name, tok)
	if err == nil || resp == nil {
		l.t.Fatalf("the connection of %s was not refused over HTTP (err %v)", name, err)
	}
	body, _ := io.ReadAll(resp.Body)
	return connectRefusal{status: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), body: strings.TrimSpace(string(body))}
}

func closedWith(t *testing.T, c *client, code int) {
	t.Helper()
	if closed := c.closed(); closed.Code != code {
		t.Fatalf("the connection closed with %d (%s), want %d", closed.Code, closed.Text, code)
	}
}

func (l *linkRig) lead(number uint64) *ledTerm {
	l.t.Helper()
	return (&replica{rig: l.rig}).lead(number)
}

func TestReconfigureAddsRemovesAndRestartsAgentsAndLeavesTheOthersAsTheyWere(t *testing.T) {
	onBothFlowKinds(t, func(t *testing.T, releasesBinds bool) {
		l := newLinkRig(t, linkPlan{releasesBinds: releasesBinds, agents: []map[string]any{
			linkAgent(agentName), linkAgent(billingDesk), linkAgent(dispatchDesk), linkAgent(escalationsDesk),
		}})
		claims, billing := l.serving(agentName), l.serving(billingDesk)
		dispatch, escalations := l.serving(dispatchDesk), l.serving(escalationsDesk)
		acknowledge(claims, l.deliver(claims, agentName, "claims-open"))
		waitFor(t, func() bool { return l.brk.acks.Load() == 1 })
		inFlight := l.deliver(claims, agentName, "claims-in-flight")
		l.deliver(escalations, escalationsDesk, "escalations-queued")
		claimsState, billingState := l.adapter.agent(agentName), l.adapter.agent(billingDesk)
		claimsConns, billingConns := l.connectionIDs(agentName), l.connectionIDs(billingDesk)
		answers := l.listen(a2a.GatewayResponseTopic(testNS, "gw1", ">"))

		l.reconfigure(linkAgent(agentName), linkAgent(billingDesk), linkAgent(collectionsDesk),
			linkAgent(escalationsDesk, withToken(rotatedToken)))

		closedWith(t, dispatch, closeCredentialsRevoked)
		unknown := l.refusal("no-such-agent", tokenOf(dispatchDesk))
		if removed := l.refusal(dispatchDesk, tokenOf(dispatchDesk)); removed.status != http.StatusUnauthorized || removed != unknown {
			t.Fatalf("the removed agent's reconnect was answered %+v, want the 401 an unknown agent gets (%+v)", removed, unknown)
		}
		for _, queue := range a2a.LinkAgentQueueNames(testNS, dispatchDesk) {
			if l.flows.releases(queue) == 0 {
				t.Fatalf("the removed agent's queue %s was never let go of", queue)
			}
			if !releasesBinds && l.brk.HasQueue(queue) {
				t.Fatalf("the removed agent's queue %s is still on a broker that keeps no bind per client", queue)
			}
		}

		closedWith(t, escalations, closeCredentialsRevoked)
		if replaced := l.refusal(escalationsDesk, tokenOf(escalationsDesk)); replaced.status != http.StatusUnauthorized {
			t.Fatalf("the replaced token was answered %d, want 401", replaced.status)
		}
		rotated := l.serving(escalationsDesk, rotatedToken)
		requireRedelivered(t, rotated.next(typeTask), "escalations-queued")

		collections := l.serving(collectionsDesk)
		l.deliver(collections, collectionsDesk, "collections-first")

		if l.adapter.agent(agentName) != claimsState || l.adapter.agent(billingDesk) != billingState {
			t.Fatal("an agent whose config did not change was replaced")
		}
		if now := l.connectionIDs(agentName); !slices.Equal(now, claimsConns) {
			t.Fatalf("the unchanged agent's connections are %v, want %v as before", now, claimsConns)
		}
		if now := l.connectionIDs(billingDesk); !slices.Equal(now, billingConns) {
			t.Fatalf("the other unchanged agent's connections are %v, want %v as before", now, billingConns)
		}
		complete(claims, "claims-open")
		answered(t, answers, "claims-open")
		acknowledge(claims, inFlight)
		complete(claims, "claims-in-flight")
		answered(t, answers, "claims-in-flight")
		acknowledge(billing, l.deliver(billing, billingDesk, "billing-after"))
		claims.quiet()
		billing.quiet()
	})
}

func TestReconfigureRefusesAnInvalidConfigOrAChangedEntrypointSettingAndChangesNothing(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName), linkAgent(billingDesk)}})
	claims := l.serving(agentName)
	acknowledge(claims, l.deliver(claims, agentName, "T1"))
	running, conns := l.adapter.roster.all(), l.connectionIDs(agentName)
	answers := l.listen(responseTopic("T1"))

	withoutADigest := map[string]any{"name": collectionsDesk, "token_sha256": "not-a-digest"}
	for name, refused := range map[string]struct {
		config        map[string]any
		says          []string
		needsARestart bool
	}{
		"an agent without a token digest": {
			config: linkSettings([]any{linkAgent(agentName), withoutADigest}, nil),
			says:   []string{"token_sha256"},
		},
		"a changed entrypoint-level setting": {
			config:        linkSettings([]any{linkAgent(agentName), linkAgent(collectionsDesk)}, map[string]any{"max_inflight": rigMaxInflight + 1}),
			says:          []string{"max_inflight changed", "deployed again, which restarts it"},
			needsARestart: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := l.adapter.Reconfigure(t.Context(), refused.config)
			if err == nil {
				t.Fatal("the config was applied, want it refused")
			}
			for _, said := range refused.says {
				if !strings.Contains(err.Error(), said) {
					t.Fatalf("refused with %q, want it to say %q", err, said)
				}
			}
			if got := errors.Is(err, entrypoint.ErrRestartRequired); got != refused.needsARestart {
				t.Fatalf("errors.Is(%q, ErrRestartRequired) = %t, want %t", err, got, refused.needsARestart)
			}
			if now := l.adapter.roster.all(); !slices.Equal(now, running) {
				t.Fatalf("%d agents are served after a refused config, want the %d served before, each as it was", len(now), len(running))
			}
			if now := l.connectionIDs(agentName); !slices.Equal(now, conns) {
				t.Fatalf("the agent's connections are %v after a refused config, want %v", now, conns)
			}
			if added := l.refusal(collectionsDesk, tokenOf(collectionsDesk)); added.status != http.StatusUnauthorized {
				t.Fatalf("the agent of a refused config was answered %d, want 401", added.status)
			}
		})
	}
	l.serving(billingDesk)
	complete(claims, "T1")
	answered(t, answers, "T1")
	claims.quiet()
}

func TestReconfigureOfAStoppedLinkAsksForARestart(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName)}})
	if err := l.adapter.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	err := l.adapter.Reconfigure(t.Context(), l.settings(linkAgent(agentName), linkAgent(billingDesk)))

	if !errors.Is(err, entrypoint.ErrRestartRequired) {
		t.Fatalf("Reconfigure after Stop = %v, want an error that asks for a restart", err)
	}
	if l.adapter.agent(billingDesk) != nil {
		t.Fatal("a stopped link took on an added agent")
	}
}

func TestReconfigureThatCannotStartAnAddedAgentFailsWithoutARestartAndTheNextOneStartsIt(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName)}})
	claims := l.serving(agentName)
	acknowledge(claims, l.deliver(claims, agentName, "T1"))
	claimsState, conns := l.adapter.agent(agentName), l.connectionIDs(agentName)
	answers := l.listen(responseTopic("T1"))
	l.flows.failProvisioningOnce(a2a.LinkAgentRequestQueueName(testNS, escalationsDesk))
	agents := []map[string]any{linkAgent(agentName), linkAgent(collectionsDesk), linkAgent(escalationsDesk)}

	err := l.adapter.Reconfigure(t.Context(), l.settings(agents...))

	if !errors.Is(err, errQueueProvisioning) {
		t.Fatalf("Reconfigure = %v, want the queue provisioning failure", err)
	}
	if !strings.Contains(err.Error(), escalationsDesk) {
		t.Fatalf("Reconfigure = %q, want it to name %s", err, escalationsDesk)
	}
	if errors.Is(err, entrypoint.ErrRestartRequired) {
		t.Fatalf("Reconfigure = %q, a failure to start one agent must not ask for a restart of the whole link", err)
	}
	if l.adapter.agent(agentName) != claimsState {
		t.Fatal("the connected agent was replaced by a reconfigure that failed to start another agent")
	}
	if now := l.connectionIDs(agentName); !slices.Equal(now, conns) {
		t.Fatalf("the connected agent's connections are %v, want %v as before", now, conns)
	}
	if l.adapter.agent(escalationsDesk) != nil {
		t.Fatal("the agent whose queue could not be provisioned is served")
	}
	if refused := l.refusal(escalationsDesk, tokenOf(escalationsDesk)); refused.status != http.StatusServiceUnavailable || refused.retryAfter != followerRetryAfterSeconds {
		t.Fatalf("the agent that failed to start was answered %+v, want 503 with a retry hint", refused)
	}
	if refused := l.refusal(escalationsDesk, "not-its-token"); refused.status != http.StatusUnauthorized {
		t.Fatalf("a wrong token for the agent that failed to start was answered %d, want 401", refused.status)
	}
	l.serving(collectionsDesk)
	complete(claims, "T1")
	answered(t, answers, "T1")

	if err := l.adapter.Reconfigure(t.Context(), l.settings(agents...)); err != nil {
		t.Fatalf("the second Reconfigure with the same config: %v", err)
	}
	escalations := l.serving(escalationsDesk)
	l.deliver(escalations, escalationsDesk, "escalations-first")
	if l.adapter.agent(agentName) != claimsState {
		t.Fatal("the connected agent was replaced by the second reconfigure")
	}
	claims.quiet()
}

func TestAnAgentWhoseRestartFailedIsToldToRetryNotThatItsTokenIsInvalidUntilItStartsOrIsRemoved(t *testing.T) {
	for name, restart := range map[string]struct {
		next          map[string]any
		closeCode     int
		failingToken  string
		replacedToken string
	}{
		"deployed again": {next: renamed(billingDesk), closeCode: websocket.CloseServiceRestart, failingToken: tokenOf(billingDesk), replacedToken: tokenOf(billingDesk)},
		"token replaced": {next: linkAgent(billingDesk, withToken(rotatedToken)), closeCode: closeCredentialsRevoked, failingToken: rotatedToken, replacedToken: tokenOf(billingDesk)},
	} {
		t.Run(name, func(t *testing.T) {
			audit := &authEvents{}
			l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName), linkAgent(billingDesk)}, opts: []samruntime.Option{samruntime.WithAuditLogger(audit)}})
			billing := l.serving(billingDesk)
			l.flows.failProvisioningOnce(a2a.LinkAgentRequestQueueName(testNS, billingDesk))

			err := l.adapter.Reconfigure(t.Context(), l.settings(linkAgent(agentName), restart.next))

			if !errors.Is(err, errQueueProvisioning) {
				t.Fatalf("Reconfigure = %v, want the queue provisioning failure", err)
			}
			closedPromptlyWith(t, billing, restart.closeCode)
			if l.adapter.agent(billingDesk) != nil {
				t.Fatal("the agent whose restart failed is served")
			}
			refused := l.refusal(billingDesk, restart.failingToken)
			if refused.status != http.StatusServiceUnavailable || refused.retryAfter != followerRetryAfterSeconds || refused.body != msgAgentRestarting {
				t.Fatalf("the reconnect of the agent whose restart failed was answered %+v, want 503 with a retry hint and %q", refused, msgAgentRestarting)
			}
			if audited := audit.failures("invalid agent credentials"); audited != 0 {
				t.Fatalf("%d invalid-credentials audit events for an agent that presented its configured token", audited)
			}
			if refused := l.refusal(billingDesk, "not-its-token"); refused.status != http.StatusUnauthorized {
				t.Fatalf("a wrong token for the agent whose restart failed was answered %d, want 401", refused.status)
			}
			if restart.replacedToken != restart.failingToken {
				if refused := l.refusal(billingDesk, restart.replacedToken); refused.status != http.StatusUnauthorized {
					t.Fatalf("the replaced token was answered %d, want 401", refused.status)
				}
			}
			if audited := audit.failures("invalid agent credentials"); audited != 1+btoi(restart.replacedToken != restart.failingToken) {
				t.Fatalf("%d invalid-credentials audit events, want one per wrong or replaced token", audited)
			}

			l.reconfigure(linkAgent(agentName), restart.next)

			l.serving(billingDesk, restart.failingToken)
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestAnAgentThatFailedToStartAndIsThenRemovedFromTheConfigIsAnUnknownAgentAgain(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName)}})
	l.flows.failProvisioningOnce(a2a.LinkAgentRequestQueueName(testNS, escalationsDesk))
	if err := l.adapter.Reconfigure(t.Context(), l.settings(linkAgent(agentName), linkAgent(escalationsDesk))); !errors.Is(err, errQueueProvisioning) {
		t.Fatalf("Reconfigure = %v, want the queue provisioning failure", err)
	}
	if refused := l.refusal(escalationsDesk, tokenOf(escalationsDesk)); refused.status != http.StatusServiceUnavailable {
		t.Fatalf("the agent that failed to start was answered %d, want 503", refused.status)
	}

	l.reconfigure(linkAgent(agentName))

	if refused := l.refusal(escalationsDesk, tokenOf(escalationsDesk)); refused.status != http.StatusUnauthorized {
		t.Fatalf("the agent removed after it failed to start was answered %d, want 401", refused.status)
	}
}

func TestAnAgentAddedOnAFollowerIsServedOnlyOnceThatReplicaLeads(t *testing.T) {
	url, wakes := countingRuntime(t)
	plan := linkPlan{dev: connectedDevBroker(t), leaderOnly: true, agents: []map[string]any{linkAgent(agentName)}}
	leader, follower := newLinkRig(t, plan), newLinkRig(t, plan)
	term := leader.lead(1)
	added := linkAgent(billingDesk, requestDriven(url, true))

	follower.reconfigure(linkAgent(agentName), added)
	if follower.adapter.agent(billingDesk) == nil {
		t.Fatal("the follower does not hold the added agent")
	}
	wokenTimes(t, wakes, 0)
	if refused := follower.refusal(billingDesk, tokenOf(billingDesk)); refused.status != http.StatusServiceUnavailable || refused.retryAfter != followerRetryAfterSeconds {
		t.Fatalf("the follower answered the added agent %+v, want 503 with Retry-After %s", refused, followerRetryAfterSeconds)
	}
	for _, queue := range a2a.LinkAgentQueueNames(testNS, billingDesk) {
		if binds := follower.flows.binds(queue); binds != 0 {
			t.Fatalf("the follower bound %s %d times, want 0", queue, binds)
		}
	}
	if cards, removals := follower.flows.cards.Load(), follower.flows.removalCards.Load(); cards != 0 || removals != 0 {
		t.Fatalf("the follower published %d cards and %d removal cards, want none", cards, removals)
	}

	leader.reconfigure(linkAgent(agentName), added)
	wokenTimes(t, wakes, 1)
	onLeader := leader.serving(billingDesk)
	acknowledge(onLeader, leader.deliver(onLeader, billingDesk, "billing-on-leader"))
	waitFor(t, func() bool { return leader.brk.acks.Load() == 1 && leader.flows.cards.Load() >= 1 })

	term.end()
	closedForServiceRestart(t, onLeader)
	follower.lead(2)
	select {
	case <-follower.adapter.agent(billingDesk).wakeWatching:
	case <-time.After(arrivalWait):
		t.Fatal("the replica that now leads never watched the agent it was given as a follower")
	}
	onNewLeader := follower.serving(billingDesk)
	follower.deliver(onNewLeader, billingDesk, "billing-on-new-leader")
}

func TestARemovedRequestDrivenAgentIsNoLongerWatchedOrWokenAndItsFlowsAreReleased(t *testing.T) {
	onBothFlowKinds(t, func(t *testing.T, releasesBinds bool) {
		url, wakes := countingRuntime(t)
		var sinceStart atomic.Int64
		start := time.Now()
		l := newLinkRig(t, linkPlan{
			releasesBinds: releasesBinds,
			agents:        []map[string]any{linkAgent(agentName, requestDriven(url, false)), linkAgent(billingDesk)},
			tune: func(a *Adapter) {
				a.now = func() time.Time { return start.Add(time.Duration(sinceStart.Load())) }
			},
		})
		l.awaitWakeWatch()
		l.submitTo(agentName, "T1", "tok:alice:T1", nil)
		wokenTimes(t, wakes, 1)
		removed, watched := l.adapter.agent(agentName), l.brk.watched.Load()

		l.reconfigure(linkAgent(billingDesk))

		sinceStart.Store(int64(2 * wakeInterval))
		l.submitTo(agentName, "T2", "tok:alice:T2", nil)
		wokenTimes(t, wakes, 1)
		if now := l.brk.watched.Load(); now != watched {
			t.Fatalf("the removed agent's wake watcher read %d more requests, want 0", now-watched)
		}
		if removed.goRun(func(context.Context) {}) {
			t.Fatal("the removed agent still starts work")
		}
		for _, queue := range a2a.LinkAgentQueueNames(testNS, agentName) {
			if l.flows.releases(queue) == 0 {
				t.Fatalf("the removed agent's queue %s was never let go of", queue)
			}
			if !releasesBinds && l.brk.HasQueue(queue) {
				t.Fatalf("the removed agent's queue %s is still on a broker that keeps no bind per client", queue)
			}
		}
		const directSubscriptionsPerAgent = 3
		if released := l.flows.releasedDirectSubscriptions(); released != directSubscriptionsPerAgent {
			t.Fatalf("%d direct subscriptions were released, want the removed agent's %d", released, directSubscriptionsPerAgent)
		}
	})
}

func TestReconfigureWithTheSameAgentsTouchesNothing(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{
		linkAgent(agentName, map[string]any{"peers": []any{peerName, "payments"}}), linkAgent(billingDesk),
	}})
	claims := l.serving(agentName)
	acknowledge(claims, l.deliver(claims, agentName, "T1"))
	waitFor(t, func() bool { return l.flows.cards.Load() >= 1 && l.brk.acks.Load() == 1 })
	running, conns := l.adapter.roster.all(), l.connectionIDs(agentName)
	cards, removals := l.flows.cards.Load(), l.flows.removalCards.Load()
	releasedQueues, releasedSubscriptions := l.flows.releasedQueues(), l.flows.releasedDirectSubscriptions()
	answers := l.listen(responseTopic("T1"))

	l.reconfigure(linkAgent(agentName, map[string]any{"peers": []any{peerName, "payments"}}), linkAgent(billingDesk))
	l.reconfigure(linkAgent(billingDesk), linkAgent(agentName, map[string]any{"peers": []any{"payments", peerName}}))

	if now := l.adapter.roster.all(); !slices.Equal(now, running) {
		t.Fatal("an agent was replaced by a config that changes nothing")
	}
	if now := l.connectionIDs(agentName); !slices.Equal(now, conns) {
		t.Fatalf("the agent's connections are %v, want %v as before", now, conns)
	}
	complete(claims, "T1")
	answered(t, answers, "T1")
	claims.quiet()
	if now, nowRemoved := l.flows.cards.Load(), l.flows.removalCards.Load(); now != cards || nowRemoved != removals {
		t.Fatalf("%d cards and %d removal cards were published by a config that changes nothing, want none", now-cards, nowRemoved-removals)
	}
	if queues, subscriptions := l.flows.releasedQueues(), l.flows.releasedDirectSubscriptions(); queues != releasedQueues || subscriptions != releasedSubscriptions {
		t.Fatalf("%d queues and %d direct subscriptions were released by a config that changes nothing, want none",
			queues-releasedQueues, subscriptions-releasedSubscriptions)
	}
}

func TestAnAgentWhoseSettingsChangedReconnectsWithItsTokenAndKeepsItsCard(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName), linkAgent(billingDesk)}})
	claims := l.serving(agentName)
	waitFor(t, func() bool { return l.flows.cards.Load() >= 1 })

	l.reconfigure(linkAgent(agentName, map[string]any{"display_name": "Claims Desk"}), linkAgent(billingDesk))

	closedWith(t, claims, websocket.CloseServiceRestart)
	if removals := l.flows.removalCards.Load(); removals != 0 {
		t.Fatalf("%d removal cards were published for an agent that only restarted, want 0", removals)
	}
	again := l.serving(agentName)
	if shown := l.adapter.agent(agentName).displayName(); shown != "Claims Desk" {
		t.Fatalf("the restarted agent is shown as %q, want its new display name", shown)
	}
	l.deliver(again, agentName, "T1")
}

func TestAnAgentThatWasDisabledIsDelistedAndRefused(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName), linkAgent(billingDesk)}})
	claims := l.serving(agentName)
	waitFor(t, func() bool { return l.flows.cards.Load() >= 1 })

	l.reconfigure(linkAgent(agentName, map[string]any{"enabled": false}), linkAgent(billingDesk))

	closedWith(t, claims, websocket.CloseServiceRestart)
	if removals := l.flows.removalCards.Load(); removals != 1 {
		t.Fatalf("%d removal cards were published for the disabled agent, want 1", removals)
	}
	if refused := l.refusal(agentName, tokenOf(agentName)); refused.status != http.StatusForbidden {
		t.Fatalf("the disabled agent was answered %d, want 403", refused.status)
	}
}

func TestAnAgentThatIsNoLongerRequestDrivenLosesItsWakeQueueAndKeepsItsRequests(t *testing.T) {
	url, _ := countingRuntime(t)
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName, requestDriven(url, false)), linkAgent(billingDesk)}})
	l.awaitWakeWatch()
	l.submitTo(agentName, "T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return l.brk.watched.Load() == 1 })

	l.reconfigure(linkAgent(agentName), linkAgent(billingDesk))

	if removed := l.flows.deprovisions(a2a.LinkAgentWakeQueueName(testNS, agentName)); removed != 1 {
		t.Fatalf("the wake queue was deprovisioned %d times, want 1", removed)
	}
	if removed := l.flows.deprovisions(a2a.LinkAgentRequestQueueName(testNS, agentName)); removed != 0 {
		t.Fatalf("the request queue was deprovisioned %d times, want 0", removed)
	}
	connected := l.serving(agentName)
	if f := connected.next(typeTask); f.TaskID != "T1" {
		t.Fatalf("got task %s, want T1, queued before the agent restarted", f.TaskID)
	}
}

func (l *linkRig) answerTasksUntil(stop <-chan struct{}, name, tok string) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		c, _, err := l.dial(name, tok)
		if err != nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		_ = c.ws.WriteJSON(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
		_ = c.ws.WriteJSON(map[string]any{"type": typeConsume})
		for open := true; open; {
			select {
			case <-stop:
				_ = c.ws.Close()
				return
			case f, isOpen := <-c.in:
				open = isOpen
				if f.Type == typeTask {
					_ = c.ws.WriteJSON(map[string]any{"type": typeAck, "delivery": f.Delivery})
					_ = c.ws.WriteJSON(map[string]any{"type": typeResult, "taskId": f.TaskID, "state": stateCompleted, "text": "done"})
				}
			}
		}
	}
}

func (l *linkRig) submitUntil(stop <-chan struct{}, names ...string) {
	for i := 0; ; i++ {
		for _, name := range names {
			taskID := fmt.Sprintf("%s-%d", name, i)
			_ = l.brk.PublishGuaranteed(context.Background(), a2a.AgentRequestTopic(testNS, name), &broker.Message{
				Payload: request(taskID),
				UserProperties: map[string]string{
					a2a.PropReplyTo:   responseTopic(taskID),
					a2a.PropUserID:    "alice@example.com",
					a2a.PropAuthToken: "tok:alice:" + taskID,
				},
			})
		}
		select {
		case <-stop:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (l *linkRig) leadShortTermsUntil(stop <-chan struct{}) {
	for number := uint64(1); ; number++ {
		ctx, cancel := context.WithCancel(context.Background())
		l.adapter.RunIngress(ctx, number)
		select {
		case <-stop:
			cancel()
			return
		case <-time.After(120 * time.Millisecond):
		}
		cancel()
	}
}

var linkWork = regexp.MustCompile(`agentlink\.\(\*(Adapter|agentState|conn|leaderTerm|workGate)\)`)

func linkGoroutines() map[string]string {
	stacks := make([]byte, 16<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	running := map[string]string{}
	for _, stack := range strings.Split(string(stacks), "\n\n") {
		if linkWork.MatchString(stack) {
			goroutine, _, _ := strings.Cut(stack, " [")
			running[goroutine] = stack
		}
	}
	return running
}

func requireNoLinkGoroutinesBeyond(t *testing.T, alreadyRunning map[string]string) {
	t.Helper()
	deadline := time.Now().Add(arrivalWait)
	for {
		var leaked []string
		for goroutine, stack := range linkGoroutines() {
			if _, earlier := alreadyRunning[goroutine]; !earlier {
				leaked = append(leaked, stack)
			}
		}
		if len(leaked) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines of the agent link outlived its stop, the first:\n%s", len(leaked), leaked[0])
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReconfigureIsSafeWhileAgentsConnectTasksFlowAndTheTermChanges(t *testing.T) {
	alreadyRunning := linkGoroutines()
	l := newLinkRig(t, linkPlan{
		leaderOnly: true,
		limits:     map[string]any{"max_connects_per_minute": 100000},
		agents:     []map[string]any{linkAgent(agentName), linkAgent(billingDesk)},
	})
	untouched := l.adapter.agent(agentName)
	shapes := [][]map[string]any{
		{linkAgent(agentName), linkAgent(billingDesk)},
		{linkAgent(agentName), linkAgent(billingDesk, withToken(rotatedToken)), linkAgent(collectionsDesk)},
		{linkAgent(agentName), linkAgent(collectionsDesk, map[string]any{"display_name": "Collections"})},
	}

	stop := make(chan struct{})
	var busy sync.WaitGroup
	busy.Go(func() { l.leadShortTermsUntil(stop) })
	busy.Go(func() { l.answerTasksUntil(stop, agentName, tokenOf(agentName)) })
	busy.Go(func() { l.answerTasksUntil(stop, billingDesk, tokenOf(billingDesk)) })
	busy.Go(func() { l.answerTasksUntil(stop, billingDesk, rotatedToken) })
	busy.Go(func() { l.answerTasksUntil(stop, collectionsDesk, tokenOf(collectionsDesk)) })
	busy.Go(func() { l.submitUntil(stop, agentName, billingDesk, collectionsDesk) })
	busy.Go(func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(7 * time.Millisecond):
				l.adapter.sweepOnce(time.Now())
			}
		}
	})
	busy.Go(func() {
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := l.adapter.Reconfigure(context.Background(), l.settings(shapes[i%len(shapes)]...)); err != nil {
				t.Errorf("reconfigure %d: %v", i, err)
				return
			}
		}
	})
	time.Sleep(3 * time.Second)
	close(stop)
	busy.Wait()

	if l.adapter.agent(agentName) != untouched {
		t.Fatal("the agent whose config never changed was replaced")
	}
	l.reconfigure(shapes[0]...)
	l.lead(1 << 40)
	claims := l.serving(agentName)
	l.submitTo(agentName, "after-the-rush", "tok:alice:after-the-rush", nil)
	for f := claims.nextOf(typeTask, typeError); f.TaskID != "after-the-rush"; f = claims.nextOf(typeTask, typeError) {
		acknowledge(claims, f)
	}

	l.server.Close()
	if err := l.core.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireNoLinkGoroutinesBeyond(t, alreadyRunning)
}

func TestAnAgentThatIsRestartingAsksAConnectionWithItsTokenToTryAgain(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName), linkAgent(billingDesk)}})
	l.adapter.agent(agentName).retire()

	if refused := l.refusal(agentName, tokenOf(agentName)); refused.status != http.StatusServiceUnavailable || refused.retryAfter != followerRetryAfterSeconds {
		t.Fatalf("the restarting agent was answered %+v, want 503 with Retry-After %s", refused, followerRetryAfterSeconds)
	}
	if refused := l.refusal(agentName, "not-the-agents-token"); refused.status != http.StatusUnauthorized {
		t.Fatalf("another token was answered %d while the agent restarts, want 401", refused.status)
	}
	l.serving(billingDesk)
}

func TestReconfigureThatFailsToStartAnAddedAgentAfterProvisioningItsQueueLetsGoOfTheQueues(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName)}})
	queue := a2a.LinkAgentRequestQueueName(testNS, billingDesk)
	l.flows.failSubscribingOnce(a2a.PeerResponseTopic(testNS, billingDesk, ">"))
	before := l.flows.releases(queue)

	err := l.adapter.Reconfigure(t.Context(), l.settings(linkAgent(agentName), linkAgent(billingDesk)))

	if !errors.Is(err, errSubscribing) {
		t.Fatalf("Reconfigure = %v, want the subscription failure", err)
	}
	if after := l.flows.releases(queue); after <= before {
		t.Fatalf("%s was released %d times after a start that provisioned it failed, want it released", queue, after-before)
	}
	if wake := a2a.LinkAgentWakeQueueName(testNS, billingDesk); l.flows.releases(wake) == 0 {
		t.Fatalf("the wake queue %s was not released", wake)
	}
	l.reconfigure(linkAgent(agentName), linkAgent(billingDesk))
	l.serving(billingDesk)
}

func TestReconfigureThatIsInsideTheStartOfAnAddedAgentWhenTheLinkStopsDoesNotKeepTheAgent(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName)}})
	queue := a2a.LinkAgentRequestQueueName(testNS, billingDesk)
	hold := l.flows.holdProvisioning(queue)
	reconfigured := make(chan error, 1)
	go func() {
		reconfigured <- l.adapter.Reconfigure(t.Context(), l.settings(linkAgent(agentName), linkAgent(billingDesk)))
	}()
	select {
	case <-hold.entered:
	case <-time.After(arrivalWait):
		t.Fatal("the reconfigure never reached the start of the added agent")
	}
	stopping, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := l.adapter.Stop(stopping); err != nil {
		t.Fatal(err)
	}
	before := l.flows.releases(queue)

	close(hold.release)

	select {
	case err := <-reconfigured:
		if !errors.Is(err, entrypoint.ErrRestartRequired) {
			t.Fatalf("Reconfigure = %v, want an error that asks for a restart", err)
		}
	case <-time.After(arrivalWait):
		t.Fatal("the reconfigure never returned")
	}
	if l.adapter.agent(billingDesk) != nil {
		t.Fatal("an agent that finished starting after the link stopped is in the roster")
	}
	if after := l.flows.releases(queue); after <= before {
		t.Fatalf("%s was released %d times for an agent started after the link stopped, want it released", queue, after-before)
	}
}

func TestStartOfAPlatformManagedLinkServesTheAgentsThatStartAndRetriesTheOneThatDoesNot(t *testing.T) {
	var sinceStart atomic.Int64
	start := time.Now()
	l := newLinkRig(t, linkPlan{
		limits:       map[string]any{"platform_managed": true},
		agents:       []map[string]any{linkAgent(agentName), linkAgent(billingDesk)},
		failsToStart: []string{a2a.LinkAgentRequestQueueName(testNS, billingDesk)},
		tune: func(a *Adapter) {
			a.now = func() time.Time { return start.Add(time.Duration(sinceStart.Load())) }
		},
	})

	if l.adapter.agent(billingDesk) != nil {
		t.Fatal("the agent whose queue could not be provisioned is served")
	}
	if refused := l.refusal(billingDesk, tokenOf(billingDesk)); refused.status != http.StatusServiceUnavailable || refused.retryAfter != followerRetryAfterSeconds {
		t.Fatalf("the agent that failed to start was answered %+v, want 503 with a retry hint", refused)
	}
	if refused := l.refusal(billingDesk, "not-its-token"); refused.status != http.StatusUnauthorized {
		t.Fatalf("a wrong token for the agent that failed to start was answered %d, want 401", refused.status)
	}
	claims := l.serving(agentName)
	acknowledge(claims, l.deliver(claims, agentName, "claims-first"))

	l.adapter.startAgentsDue(l.adapter.now())
	if l.adapter.agent(billingDesk) != nil {
		t.Fatal("the agent that failed to start was tried again before its retry was due")
	}

	sinceStart.Store(int64(startRetryFirst))
	waitFor(t, func() bool { return l.adapter.agent(billingDesk) != nil })

	billing := l.serving(billingDesk)
	acknowledge(billing, l.deliver(billing, billingDesk, "billing-first"))
}

func TestAnAgentThatKeepsFailingToStartIsTriedLessOften(t *testing.T) {
	for failures, want := range map[int]time.Duration{1: startRetryFirst, 2: 2 * startRetryFirst, 4: 8 * startRetryFirst, 19: startRetryMax, 37: startRetryMax, 40: startRetryMax, 64: startRetryMax} {
		if got := startRetryDelay(failures); got != want {
			t.Errorf("after %d failures the next start is tried in %s, want %s", failures, got, want)
		}
	}
}

func TestAStoppedLinkTakesNoNewBackgroundWork(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName)}})
	if err := l.adapter.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	var ran atomic.Bool
	started := l.adapter.goRun(func(context.Context) { ran.Store(true) })

	if started {
		t.Fatal("a stopped link started background work after its stop waited for the work it had")
	}
	if ran.Load() {
		t.Fatal("the work a stopped link refused ran")
	}
}

func TestEachFailedStartOfTheSameAgentPutsItsNextTryFurtherOff(t *testing.T) {
	roster := newAgentRoster()
	cfg := &AgentConfig{Name: billingDesk, TokenSHA256: []byte("billing-desk-digest")}
	failedAt := time.Now()

	for failures, wait := range []time.Duration{startRetryFirst, 2 * startRetryFirst, 4 * startRetryFirst} {
		roster.failedToStart(cfg, failedAt)
		next, failed := roster.nextStartAttempt(billingDesk)
		if !failed || !next.Equal(failedAt.Add(wait)) {
			t.Fatalf("after %d failed starts the next try is at +%s, want +%s", failures+1, next.Sub(failedAt), wait)
		}
	}
	if due := roster.startsDue(failedAt.Add(2 * startRetryFirst)); len(due) != 0 {
		t.Fatalf("an agent that failed three times is tried again after %s, want after %s", 2*startRetryFirst, 4*startRetryFirst)
	}
	due := roster.startsDue(failedAt.Add(4 * startRetryFirst))
	if len(due) != 1 || due[0].failures != 3 {
		t.Fatalf("starts due once the third wait passed = %+v, want the agent with 3 failures", due)
	}
}

func TestAnAgentThatFailedToStartIsAnswered503WhileItIsTakenDown(t *testing.T) {
	l := newLinkRig(t, linkPlan{limits: map[string]any{"platform_managed": true}, agents: []map[string]any{linkAgent(agentName)}})
	requestQueue := a2a.LinkAgentRequestQueueName(testNS, billingDesk)
	l.flows.failProvisioningOnce(requestQueue)
	var duringTakeDown connectRefusal
	l.flows.onRelease.Store(requestQueue, func() { duringTakeDown = l.refusal(billingDesk, tokenOf(billingDesk)) })

	if err := l.adapter.Reconfigure(t.Context(), l.settings(linkAgent(agentName), linkAgent(billingDesk))); err == nil {
		t.Fatal("a reconfigure whose added agent could not start reported no error")
	}

	if duringTakeDown.status != http.StatusServiceUnavailable {
		t.Fatalf("the agent's own token was answered %d while its failed start was taken down, want 503", duringTakeDown.status)
	}
}
