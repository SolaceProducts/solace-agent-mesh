// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

type bindRecorder struct {
	mu     sync.Mutex
	states []BindState
}

func (r *bindRecorder) record(s BindState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, s)
}

func (r *bindRecorder) reports() []BindState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.states)
}

func (r *bindRecorder) last() (BindState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.states) == 0 {
		return BindStandby, false
	}
	return r.states[len(r.states)-1], true
}

func exclusiveOn(r *bindRecorder) QueueOptions {
	return QueueOptions{Named: true, Exclusive: true, OnBindState: r.record}
}

func connectedDevBroker(t *testing.T) *DevBroker {
	t.Helper()
	b := NewDevBroker()
	if err := b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

func bindExclusive(t *testing.T, brk Broker, queue, topic string, r *bindRecorder) Queue {
	t.Helper()
	q, err := brk.CreateQueue(queue, exclusiveOn(r))
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err := brk.Subscribe(q, topic); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return q
}

func publish(t *testing.T, brk Broker, topic, payload string) {
	t.Helper()
	if err := brk.Publish(context.Background(), topic, &Message{Payload: []byte(payload)}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func receiveWithin(brk Broker, q Queue, d time.Duration) (*Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return brk.Receive(ctx, q)
}

func requireReports(t *testing.T, who string, r *bindRecorder, want ...BindState) {
	t.Helper()
	if got := r.reports(); !slices.Equal(got, want) {
		t.Errorf("%s reports = %v, want %v", who, got, want)
	}
}

func TestTheDevBrokerItselfLeadsAnExclusiveQueueAtOnceAndOnlyOnce(t *testing.T) {
	b := connectedDevBroker(t)
	var first, repeat bindRecorder
	if _, err := b.CreateQueue("ex", exclusiveOn(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CreateQueue("ex", exclusiveOn(&repeat)); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "first bind", &first, BindActive)
	requireReports(t, "repeat bind of the same client", &repeat)
}

func TestOnlyTheActiveClientReceivesAnExclusiveQueue(t *testing.T) {
	b := connectedDevBroker(t)
	a, c := b.NewClient(), b.NewClient()
	var aState, cState bindRecorder
	qa := bindExclusive(t, a, "ex", "ex/topic", &aState)
	qc := bindExclusive(t, c, "ex", "ex/topic", &cState)
	requireReports(t, "first client", &aState, BindActive)
	requireReports(t, "second client", &cState)

	publish(t, b, "ex/topic", `"one"`)
	if msg, err := receiveWithin(a, qa, time.Second); err != nil || msg == nil {
		t.Fatalf("active client receive = (%v, %v), want the message", msg, err)
	}
	if msg, err := receiveWithin(c, qc, 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("standby client receive = (%v, %v), want nothing until its deadline", msg, err)
	}
}

func TestReleasingTheActiveBindHandsTheNextClientItsSpooledMessages(t *testing.T) {
	b := connectedDevBroker(t)
	a, c := b.NewClient(), b.NewClient()
	var aState, cState bindRecorder
	bindExclusive(t, a, "ex", "ex/topic", &aState)
	qc := bindExclusive(t, c, "ex", "ex/topic", &cState)
	for i := range 3 {
		publish(t, b, "ex/topic", fmt.Sprintf(`"m%d"`, i))
	}

	if err := a.DeleteQueue("ex"); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "client that released", &aState, BindActive)
	requireReports(t, "promoted client", &cState, BindActive)
	for i := range 3 {
		if msg, err := receiveWithin(c, qc, time.Second); err != nil || msg == nil {
			t.Fatalf("promoted client receive %d = (%v, %v), want the spooled message", i, msg, err)
		}
	}
	if got := b.MatchingQueues("ex/topic"); !slices.Equal(got, []string{"ex"}) {
		t.Errorf("queues matching after the handover = %v, want the subscription kept", got)
	}
}

func TestADisconnectedClientIsToldItStandsByAndTheNextLeads(t *testing.T) {
	b := connectedDevBroker(t)
	a, c := b.NewClient(), b.NewClient()
	var aState, cState bindRecorder
	qa := bindExclusive(t, a, "ex", "ex/topic", &aState)
	bindExclusive(t, c, "ex", "ex/topic", &cState)

	if err := a.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "disconnected client", &aState, BindActive, BindStandby)
	requireReports(t, "promoted client", &cState, BindActive)
	if _, err := receiveWithin(a, qa, time.Second); !errors.Is(err, ErrQueueNotFound) {
		t.Errorf("disconnected client receive err = %v, want ErrQueueNotFound", err)
	}
}

func TestAStandbyThatLeavesChangesNothingAndTheLastBindClosesTheQueue(t *testing.T) {
	b := connectedDevBroker(t)
	a, c := b.NewClient(), b.NewClient()
	var aState, cState bindRecorder
	qa := bindExclusive(t, a, "ex", "ex/topic", &aState)
	bindExclusive(t, c, "ex", "ex/topic", &cState)

	if err := c.DeleteQueue("ex"); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "active client", &aState, BindActive)
	requireReports(t, "standby that left", &cState)
	publish(t, b, "ex/topic", `"still"`)
	if msg, err := receiveWithin(a, qa, time.Second); err != nil || msg == nil {
		t.Fatalf("active client receive = (%v, %v), want the message", msg, err)
	}

	if err := a.DeleteQueue("ex"); err != nil {
		t.Fatal(err)
	}
	if b.HasQueue("ex") || len(b.MatchingQueues("ex/topic")) != 0 {
		t.Error("the queue outlived its last bind")
	}
}

func TestDeprovisioningAnExclusiveQueueEndsEveryBindAndPromotesNone(t *testing.T) {
	b := connectedDevBroker(t)
	a, c := b.NewClient(), b.NewClient()
	var aState, cState bindRecorder
	qa := bindExclusive(t, a, "ex", "ex/topic", &aState)
	qc := bindExclusive(t, c, "ex", "ex/topic", &cState)

	if err := a.DeprovisionQueue("ex"); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "active client", &aState, BindActive)
	requireReports(t, "standby client", &cState)
	for name, pair := range map[string]struct {
		brk Broker
		q   Queue
	}{"active": {a, qa}, "standby": {c, qc}} {
		if _, err := receiveWithin(pair.brk, pair.q, time.Second); !errors.Is(err, ErrQueueNotFound) {
			t.Errorf("%s client receive err = %v, want ErrQueueNotFound", name, err)
		}
	}
}

func TestANonExclusiveQueueIsSharedByEveryClient(t *testing.T) {
	b := connectedDevBroker(t)
	a, c := b.NewClient(), b.NewClient()
	var aState, cState bindRecorder
	opts := func(r *bindRecorder) QueueOptions { return QueueOptions{Named: true, OnBindState: r.record} }
	qa, err := a.CreateQueue("shared", opts(&aState))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Subscribe(qa, "shared/topic"); err != nil {
		t.Fatal(err)
	}
	qc, err := c.CreateQueue("shared", opts(&cState))
	if err != nil {
		t.Fatal(err)
	}
	requireReports(t, "creating client", &aState, BindActive)
	requireReports(t, "second client", &cState)

	publish(t, b, "shared/topic", `"work"`)
	if msg, err := receiveWithin(c, qc, time.Second); err != nil || msg == nil {
		t.Fatalf("second client receive = (%v, %v), want the competing consumer's message", msg, err)
	}
}

func TestTheFirstBindFixesTheAccessType(t *testing.T) {
	b := connectedDevBroker(t)
	a, c := b.NewClient(), b.NewClient()
	var aState, cState bindRecorder
	bindExclusive(t, a, "ex", "ex/topic", &aState)
	if _, err := c.CreateQueue("ex", QueueOptions{Named: true, OnBindState: cState.record}); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "client asking for a shared bind", &cState)

	if err := a.DeleteQueue("ex"); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "client asking for a shared bind", &cState, BindActive)
}

func TestADevBrokerClientStandsByBehindTheDevBrokerItself(t *testing.T) {
	b := connectedDevBroker(t)
	view := b.NewClient()
	var rootState, viewState bindRecorder
	bindExclusive(t, b, "ex", "ex/topic", &rootState)
	qv := bindExclusive(t, view, "ex", "ex/topic", &viewState)
	requireReports(t, "client", &viewState)

	if err := b.DeleteQueue("ex"); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "client", &viewState, BindActive)
	publish(t, b, "ex/topic", `"now"`)
	if msg, err := receiveWithin(view, qv, time.Second); err != nil || msg == nil {
		t.Fatalf("promoted client receive = (%v, %v), want the message", msg, err)
	}
}

func TestAStandbyReceiveWakesWhenItsBindIsPromoted(t *testing.T) {
	b := connectedDevBroker(t)
	a, c := b.NewClient(), b.NewClient()
	var aState, cState bindRecorder
	bindExclusive(t, a, "ex", "ex/topic", &aState)
	qc := bindExclusive(t, c, "ex", "ex/topic", &cState)

	got := make(chan error, 1)
	go func() {
		msg, err := receiveWithin(c, qc, 5*time.Second)
		if err == nil && msg == nil {
			err = errors.New("no message")
		}
		got <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := a.DeleteQueue("ex"); err != nil {
		t.Fatal(err)
	}
	publish(t, b, "ex/topic", `"after"`)
	if err := <-got; err != nil {
		t.Fatalf("standby receive after promotion: %v", err)
	}
}

func TestConcurrentBindsAndReleasesLeaveEveryReportTrue(t *testing.T) {
	b := connectedDevBroker(t)
	const clients = 16
	views := make([]*DevBrokerClient, clients)
	states := make([]*bindRecorder, clients)
	for i := range views {
		views[i], states[i] = b.NewClient(), &bindRecorder{}
	}

	var wg sync.WaitGroup
	for i := range views {
		wg.Go(func() {
			if _, err := views[i].CreateQueue("ex", exclusiveOn(states[i])); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for i := range clients - 1 {
		wg.Go(func() {
			if i%2 == 0 {
				_ = views[i].DeleteQueue("ex")
			} else {
				_ = views[i].Disconnect(context.Background())
			}
		})
	}
	wg.Wait()

	if state, ok := states[clients-1].last(); !ok || state != BindActive {
		t.Errorf("the one client left last reported %v (reported: %v), want active", state, ok)
	}
	for i := range clients - 1 {
		if i%2 == 1 {
			if state, ok := states[i].last(); ok && state != BindStandby {
				t.Errorf("disconnected client %d last reported %v, want standby", i, state)
			}
		}
	}
}
