// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package discovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestCardRequestLoadsReportedCardsWithTheirLastSeen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	brk := broker.NewDevBroker(broker.WithDevBrokerLogger(discardLogger()))
	if err := brk.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	seen := time.Now().Add(-90 * time.Second).Truncate(time.Millisecond)
	running := NewRegistry()
	running.ObserveSeen(a2a.AgentCard{Name: "sales"}, "run-1", seen)
	if err := AnswerCardRequests(ctx, brk, "ns", running, discardLogger()); err != nil {
		t.Fatal(err)
	}

	starting := NewRegistry()
	var mu sync.Mutex
	loaded := make(chan struct{}, 1)
	err := RequestCards(ctx, brk, "ns", time.Second, time.Hour, func(rc ReportedCard) {
		mu.Lock()
		defer mu.Unlock()
		starting.ObserveSeen(rc.Card, rc.InstanceID, rc.LastSeen)
		loaded <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-loaded:
	case <-time.After(2 * time.Second):
		t.Fatal("no reported card loaded")
	}

	got := starting.Instances("sales")
	if len(got) != 1 || !got[0].LastSeen.Equal(seen) {
		t.Fatalf("loaded %+v, want one instance last seen %s", got, seen)
	}

	starting.ObserveSeen(a2a.AgentCard{Name: "sales"}, "run-1", seen.Add(-time.Minute))
	if got := starting.Instances("sales"); !got[0].LastSeen.Equal(seen) {
		t.Fatalf("an older report moved last-seen back to %s", got[0].LastSeen)
	}

	starting.ObserveSeen(a2a.AgentCard{Name: "sales", Description: "older"}, "run-1", seen.Add(-time.Minute))
	if got := starting.Get("sales"); got == nil || got.Description == "older" {
		t.Fatalf("an older report of another card replaced the instance's newer card: %+v", got)
	}

	starting.ObserveSeen(a2a.AgentCard{Name: "sales", Description: "reported"}, "run-1", time.Now().Add(time.Minute))
	starting.Observe(a2a.AgentCard{Name: "sales", Description: "live"}, "run-1")
	if got := starting.Get("sales"); got == nil || got.Description != "live" {
		t.Fatalf("a live card was ignored after a report stamped by a clock ahead of this one: %+v", got)
	}
}

func TestRequestCardsDropsOldCardsAndCapsFutureOnes(t *testing.T) {
	brk := broker.NewDevBroker(broker.WithDevBrokerLogger(discardLogger()))
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	running := NewRegistry()
	running.ObserveSeen(a2a.AgentCard{Name: "gone"}, "run-1", time.Now().Add(-2*time.Hour))
	running.ObserveSeen(a2a.AgentCard{Name: "ahead"}, "run-2", time.Now().Add(time.Hour))
	if err := AnswerCardRequests(t.Context(), brk, "ns", running, discardLogger()); err != nil {
		t.Fatal(err)
	}
	loaded := make(chan ReportedCard, 2)
	if err := RequestCards(t.Context(), brk, "ns", time.Second, time.Hour, func(rc ReportedCard) { loaded <- rc }); err != nil {
		t.Fatal(err)
	}
	select {
	case rc := <-loaded:
		if rc.Card.Name != "ahead" || rc.LastSeen.After(time.Now()) {
			t.Fatalf("loaded %s last seen %s, want only ahead, last seen no later than now", rc.Card.Name, rc.LastSeen)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reported card loaded")
	}
	select {
	case rc := <-loaded:
		t.Fatalf("also loaded %s, seen longer ago than maxAge", rc.Card.Name)
	case <-time.After(1500 * time.Millisecond):
	}
}

type refusingBroker struct{ *broker.DevBroker }

func (refusingBroker) CreateDirectSubscription() (broker.Queue, error) {
	return nil, errors.New("subscriptions refused")
}

func TestCardRequestReportsARefusedSubscription(t *testing.T) {
	brk := refusingBroker{broker.NewDevBroker(broker.WithDevBrokerLogger(discardLogger()))}
	if err := AnswerCardRequests(t.Context(), brk, "ns", NewRegistry(), discardLogger()); err == nil {
		t.Error("answering with no subscription reported success")
	}
	if err := RequestCards(t.Context(), brk, "ns", time.Second, time.Hour, func(ReportedCard) {}); err == nil {
		t.Error("requesting with no subscription reported success")
	}
}

func TestLoadBatchSkipsUnusableCards(t *testing.T) {
	var loaded []string
	loadBatch([]byte(`{"cards":[{"card":{"name":"ok"},"last_seen_ms":1},{"card":{},"last_seen_ms":1},{"card":"not a card"}]}`), func(rc ReportedCard) {
		loaded = append(loaded, rc.Card.Name)
	})
	loadBatch([]byte(`not json`), func(ReportedCard) { t.Error("loaded a card from an unreadable batch") })
	if len(loaded) != 1 || loaded[0] != "ok" {
		t.Fatalf("loaded %v, want only the usable card", loaded)
	}
}
