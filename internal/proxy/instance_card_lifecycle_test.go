// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

func TestStartStop_ToggleWhileReading(t *testing.T) {
	p := newBrokerProxy(t)
	adapter := &switchableAdapter{}
	p.adapter = adapter
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 1
	p.cfg.RequireSignedCard = true
	p.cardRetryInterval = time.Millisecond

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = p.CachedCard()
				_ = p.Problems()
				_ = p.Health()
				_ = p.forwardsTasks()
			}
		})
	}
	for i := range 60 {
		switch i % 3 {
		case 0:
			adapter.set(nil, errors.New("fetch agent card: network connection failed"))
		case 1:
			adapter.set(verifiedRemoteCard(), nil)
		default:
			adapter.set(nil, fmt.Errorf("check card: %w", ErrAgentCardUntrusted))
		}
		if err := p.Start(context.Background()); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		<-p.started
		time.Sleep(time.Duration(i%4) * time.Millisecond)
		if err := p.Stop(context.Background()); err != nil {
			t.Fatalf("stop %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
}

func refusedRequest(t *testing.T, setup func(p *ProxyInstance)) (replies []string, acked bool, p *ProxyInstance, adapter *switchableAdapter) {
	t.Helper()
	p = newBrokerProxy(t)
	brk := p.svc.Broker()
	replyQ, err := brk.CreateQueue("reply-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, "reply/topic"); subErr != nil {
		t.Fatal(subErr)
	}
	p.cfg.TaskAuth = &AuthConfig{Type: "oauth2_authorization_code", AuthorizationURL: "https://auth.example.com/a", TokenURL: "https://auth.example.com/t", ClientID: "c"}
	adapter = &switchableAdapter{}
	p.adapter = adapter
	setup(p)

	req := a2a.Request{Method: "message/send", ID: "t1", Params: map[string]any{"message": map[string]any{
		"contextId": "s", "parts": []any{map[string]any{"kind": "text", "text": "hi"}},
	}}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var acks atomic.Int32
	msg := broker.NewDeliveredMessage("m1", "topic", data, func() { acks.Add(1) }, nil)
	msg.UserProperties = map[string]string{"replyTo": "reply/topic", "a2aStatusTopic": "status/topic", "userId": "user1", "traceID": "trace-1"}
	p.handleRequest(context.Background(), msg, &req)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	for {
		m, err := brk.Receive(ctx, replyQ)
		if err != nil || m == nil {
			break
		}
		replies = append(replies, string(m.Payload))
	}
	return replies, acks.Load() == 1, p, adapter
}

func TestHandleRequest_RefusesBeforeAuthorization(t *testing.T) {
	tests := []struct {
		name  string
		setup func(p *ProxyInstance)
		want  string
	}{
		{"refused card", func(p *ProxyInstance) { p.cardRefused.Store(true) }, "agent card failed its signature check"},
		{"no card yet under require_signed_card", func(p *ProxyInstance) { p.cfg.RequireSignedCard = true }, "has no agent card with a verified signature yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replies, acked, p, adapter := refusedRequest(t, tt.setup)
			if len(replies) != 1 || !strings.Contains(replies[0], tt.want) {
				t.Errorf("replies = %v, want one error saying %q (and no sign-in prompt)", replies, tt.want)
			}
			if !acked || p.tasks.Get("t1") != nil || adapter.forwarded.Load() != 0 {
				t.Errorf("acked = %v, task kept = %v, forwarded = %d; want acked, removed and not forwarded", acked, p.tasks.Get("t1") != nil, adapter.forwarded.Load())
			}
		})
	}
}

type cardEventBroker struct {
	*broker.DevBroker
	onRemoval func()

	mu     sync.Mutex
	events []string
}

func (b *cardEventBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	kind := ""
	if topic == a2a.DiscoveryAgentCardsTopic("test-ns") {
		kind = "card"
		if msg.UserProperties["removed"] == "true" {
			kind = "removal"
		}
	}
	if kind != "" {
		b.mu.Lock()
		b.events = append(b.events, kind)
		b.mu.Unlock()
	}
	err := b.DevBroker.Publish(ctx, topic, msg)
	if kind == "removal" && b.onRemoval != nil {
		b.onRemoval()
	}
	return err
}

func (b *cardEventBroker) lastEvent() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) == 0 {
		return ""
	}
	return b.events[len(b.events)-1]
}

type heldAdapter struct {
	switchableAdapter
	first, later func() (*a2a.AgentCard, error)
	calls        atomic.Int32
	entered      chan struct{}
	release      chan struct{}
}

func (a *heldAdapter) FetchAgentCard(context.Context) (*a2a.AgentCard, error) {
	if a.calls.Add(1) == 1 {
		return a.first()
	}
	select {
	case a.entered <- struct{}{}:
	default:
	}
	<-a.release
	return a.later()
}

func TestStop_NoCardAfterRemoval(t *testing.T) {
	unreachable := func() (*a2a.AgentCard, error) { return nil, errors.New("fetch agent card: network connection failed") }
	card := func() (*a2a.AgentCard, error) { return verifiedRemoteCard(), nil }
	tests := []struct {
		name         string
		interval     int
		first, later func() (*a2a.AgentCard, error)
	}{
		{"a retry that succeeds", 0, unreachable, card},
		{"a periodic fetch that fails", 1, card, unreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newBrokerProxy(t)
			events := &cardEventBroker{DevBroker: p.svc.Broker().(*broker.DevBroker)}
			p.svc.(*mockServices).brk = events
			adapter := &heldAdapter{first: tt.first, later: tt.later, entered: make(chan struct{}, 1), release: make(chan struct{})}
			p.adapter = adapter
			p.cfg.DiscoveryIntervalSeconds = tt.interval
			p.cfg.TaskCleanupIntervalMinutes = 0
			p.cardRetryInterval = 5 * time.Millisecond
			events.onRemoval = func() { close(adapter.release); time.Sleep(100 * time.Millisecond) }
			if err := p.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			<-p.started
			<-adapter.entered
			if err := p.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
			if got := events.lastEvent(); got != "removal" {
				t.Errorf("last discovery publish = %q, want the removal card", got)
			}
		})
	}
}

func TestStart_ForgetsTheLastRunsCard(t *testing.T) {
	p := newBrokerProxy(t)
	adapter := &switchableAdapter{}
	adapter.set(verifiedRemoteCard(), nil)
	p.adapter = adapter
	p.cfg.RequireSignedCard = true
	p.cfg.DiscoveryIntervalSeconds = 60
	p.cfg.TaskCleanupIntervalMinutes = 0
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-p.started
	if !p.forwardsTasks() {
		t.Fatal("not forwarding after the first fetch")
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	adapter.set(nil, errors.New("fetch agent card: network connection failed"))
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup
	if p.forwardsTasks() || p.CachedCard() != nil {
		t.Errorf("the previous run's card carried over: forwards = %v, card = %v", p.forwardsTasks(), p.CachedCard() != nil)
	}
}

func cardsPublishedSince(t *testing.T, brk broker.Broker, q broker.Queue) []a2a.AgentCard {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var cards []a2a.AgentCard
	for {
		m, err := brk.Receive(ctx, q)
		if err != nil || m == nil {
			return cards
		}
		var card a2a.AgentCard
		if err := json.Unmarshal(m.Payload, &card); err != nil {
			t.Fatalf("decode published card: %v", err)
		}
		cards = append(cards, card)
	}
}

func TestRefreshCard_StopsPublishingWhileRemoteStaysUnreachable(t *testing.T) {
	p := newBrokerProxy(t)
	adapter := &switchableAdapter{}
	adapter.set(verifiedRemoteCard(), nil)
	p.adapter = adapter
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 0
	brk := p.svc.Broker()
	q, err := brk.CreateQueue("card-watch", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.DiscoveryAgentCardsTopic("test-ns")); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup
	cardsPublishedSince(t, brk, q)

	adapter.set(nil, errors.New("fetch agent card: network connection failed"))
	p.refreshCard(context.Background(), "test fetch failed")
	cards := cardsPublishedSince(t, brk, q)
	if len(cards) != 1 {
		t.Fatalf("published %d cards on the first failed fetch, want 1", len(cards))
	}
	if got := a2a.HealthProblemsFromCard(cards[0]); len(got) != 1 || got[0].Severity != health.Degraded {
		t.Errorf("republished card health = %v, want one Degraded problem", got)
	}
	for range 20 {
		p.refreshCard(context.Background(), "test fetch failed")
	}
	cardsPublishedSince(t, brk, q)
	p.refreshCard(context.Background(), "test fetch failed")
	if got := len(cardsPublishedSince(t, brk, q)); got != 0 {
		t.Fatalf("published %d cards on a failed fetch after 21 in a row, want none so the card's TTL can run out", got)
	}

	adapter.set(verifiedRemoteCard(), nil)
	if !p.refreshCard(context.Background(), "test fetch failed") {
		t.Fatal("fetch failed after the remote came back")
	}
	cards = cardsPublishedSince(t, brk, q)
	if len(cards) != 1 || a2a.HealthProblemsFromCard(cards[0]) != nil {
		t.Errorf("after recovery published %d cards, want one healthy card", len(cards))
	}

	adapter.set(nil, errors.New("fetch agent card: network connection failed"))
	p.refreshCard(context.Background(), "test fetch failed")
	if got := len(cardsPublishedSince(t, brk, q)); got != 1 {
		t.Errorf("published %d cards on the first failure after recovery, want 1: the count starts over", got)
	}
}

func TestRefreshCard_KeepsRepublishingWhenRemoteAnswersWithAnUnusableCard(t *testing.T) {
	p := newBrokerProxy(t)
	adapter := &switchableAdapter{}
	adapter.set(verifiedRemoteCard(), nil)
	p.adapter = adapter
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 0
	brk := p.svc.Broker()
	q, err := brk.CreateQueue("card-watch", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.DiscoveryAgentCardsTopic("test-ns")); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup
	cardsPublishedSince(t, brk, q)

	adapter.set(nil, fmt.Errorf("check card: %w", ErrAgentCardUntrusted))
	const fetches = 10
	for range fetches {
		p.refreshCard(context.Background(), "test fetch failed")
	}
	if got := len(cardsPublishedSince(t, brk, q)); got != fetches {
		t.Errorf("published %d cards over %d refused fetches, want one each", got, fetches)
	}

	adapter.set(nil, fmt.Errorf("check card: %w", ErrAgentCardKeysUnavailable))
	for range fetches {
		p.refreshCard(context.Background(), "test fetch failed")
	}
	if got := len(cardsPublishedSince(t, brk, q)); got != fetches {
		t.Errorf("published %d cards over %d key-set failures, want one each", got, fetches)
	}
}

func TestRefreshCard_StopsPublishingWhenRefusedRemoteGoesDown(t *testing.T) {
	p := newBrokerProxy(t)
	adapter := &switchableAdapter{}
	adapter.set(verifiedRemoteCard(), nil)
	p.adapter = adapter
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 0
	brk := p.svc.Broker()
	q, err := brk.CreateQueue("card-watch", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.DiscoveryAgentCardsTopic("test-ns")); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup
	cardsPublishedSince(t, brk, q)

	adapter.set(nil, fmt.Errorf("check card: %w", ErrAgentCardUntrusted))
	p.refreshCard(context.Background(), "test fetch failed")
	cardsPublishedSince(t, brk, q)

	adapter.set(nil, errors.New("fetch agent card: network connection failed"))
	const republishedFetches = 3
	for range republishedFetches {
		p.refreshCard(context.Background(), "test fetch failed")
	}
	if got := len(cardsPublishedSince(t, brk, q)); got != republishedFetches {
		t.Fatalf("published %d cards over the first unreachable fetches, want %d", got, republishedFetches)
	}
	p.refreshCard(context.Background(), "test fetch failed")
	if got := len(cardsPublishedSince(t, brk, q)); got != 0 {
		t.Errorf("published %d cards after the cap, want none so the card's TTL can run out", got)
	}
}
