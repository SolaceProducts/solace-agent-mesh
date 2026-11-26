// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const testNamespace = "test-ns"

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newDevBroker(t *testing.T) *broker.DevBroker {
	t.Helper()
	brk := broker.NewDevBroker(broker.WithDevBrokerLogger(discardLogger()))
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func publishCard(t *testing.T, brk broker.Broker, card a2a.AgentCard, props map[string]string) {
	t.Helper()
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	topic := a2a.DiscoveryAgentCardsTopic(testNamespace)
	if err := brk.Publish(context.Background(), topic, &broker.Message{Payload: data, UserProperties: props}); err != nil {
		t.Fatalf("publish card: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestDirectory_ReceivesCardsAndInstanceRemovals(t *testing.T) {
	brk := newDevBroker(t)
	dir := NewDirectory(brk, testNamespace, discardLogger())
	if err := dir.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := dir.Start(); err != nil {
		t.Fatalf("second Start from another component sharing the directory: %v", err)
	}
	t.Cleanup(func() { _ = dir.Close() })

	card := a2a.AgentCard{Name: "agent-x"}
	publishCard(t, brk, card, map[string]string{"instance_id": "A"})
	publishCard(t, brk, card, map[string]string{"instance_id": "B"})
	waitFor(t, func() bool { return len(dir.Registry().Instances("agent-x")) == 2 },
		"directory never recorded both replicas of agent-x")

	publishCard(t, brk, card, map[string]string{"instance_id": "A", "removed": "true"})
	waitFor(t, func() bool { return len(dir.Registry().Instances("agent-x")) == 1 },
		"directory never dropped replica A")
	if dir.Registry().Lookup("agent-x") == nil {
		t.Fatal("replica A stopping evicted agent-x although replica B still publishes it")
	}

	publishCard(t, brk, card, map[string]string{"instance_id": "B", "removed": "true"})
	waitFor(t, func() bool { return dir.Registry().Lookup("agent-x") == nil },
		"directory kept agent-x after its last replica's removal")
}

func TestDirectory_RestartKeepsCards(t *testing.T) {
	brk := newDevBroker(t)
	dir := NewDirectory(brk, testNamespace, discardLogger())
	if err := dir.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	publishCard(t, brk, a2a.AgentCard{Name: "before"}, nil)
	waitFor(t, func() bool { return dir.Registry().Lookup("before") != nil }, "card before Close never arrived")

	if err := dir.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if dir.Registry().Lookup("before") == nil {
		t.Fatal("Close dropped the cards already received")
	}

	if err := dir.Start(); err != nil {
		t.Fatalf("Start after Close: %v", err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	publishCard(t, brk, a2a.AgentCard{Name: "after"}, nil)
	waitFor(t, func() bool { return dir.Registry().Lookup("after") != nil }, "card after restart never arrived")
}

type flakyBroker struct {
	*broker.DevBroker
	down atomic.Bool
}

func (b *flakyBroker) CreateDirectSubscription() (broker.Queue, error) {
	if b.down.Load() {
		return nil, errors.New("broker unreachable")
	}
	return b.DevBroker.CreateDirectSubscription()
}

func hasProblem(dir *Directory, id string) bool {
	for _, p := range dir.HealthSnapshot() {
		if p.ID == id {
			return true
		}
	}
	return false
}

func TestDirectory_ResubscribesAfterQueueLoss(t *testing.T) {
	brk := newDevBroker(t)
	dir := NewDirectory(brk, testNamespace, discardLogger())
	dir.resubscribeDelay = 10 * time.Millisecond
	if err := dir.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = dir.Close() })

	dir.mu.Lock()
	lost := dir.run.queue.Name()
	dir.mu.Unlock()
	if err := brk.DeleteQueue(lost); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	waitFor(t, func() bool {
		publishCard(t, brk, a2a.AgentCard{Name: "after-loss"}, nil)
		return dir.Registry().Lookup("after-loss") != nil
	}, "directory never received cards again after its queue was dropped")
	waitFor(t, func() bool { return len(dir.HealthSnapshot()) == 0 },
		"directory kept reporting a problem after it resubscribed")
}

func TestDirectory_StartFailureRecovers(t *testing.T) {
	brk := &flakyBroker{DevBroker: newDevBroker(t)}
	brk.down.Store(true)
	dir := NewDirectory(brk, testNamespace, discardLogger())
	dir.resubscribeDelay = 10 * time.Millisecond
	if err := dir.Start(); err == nil {
		t.Fatal("want Start to report the failed subscription")
	}
	t.Cleanup(func() { _ = dir.Close() })
	if !hasProblem(dir, subscribeHealthKey) {
		t.Fatalf("health = %+v, want the failed subscription reported", dir.HealthSnapshot())
	}
	if err := dir.Start(); err != nil {
		t.Fatalf("Start of a directory already retrying = %v, want nil", err)
	}

	brk.down.Store(false)
	waitFor(t, func() bool { return !hasProblem(dir, subscribeHealthKey) },
		"directory never subscribed once the broker came back")
	waitFor(t, func() bool {
		publishCard(t, brk, a2a.AgentCard{Name: "recovered"}, nil)
		return dir.Registry().Lookup("recovered") != nil
	}, "directory never received cards after recovering")
}

func TestDirectory_StartWithoutBroker(t *testing.T) {
	dir := NewDirectory(nil, testNamespace, discardLogger())
	if err := dir.Start(); err == nil {
		t.Fatal("want an error starting a directory with no broker")
	}
}

func BenchmarkDirectoryRound(b *testing.B) {
	for _, n := range []int{1, 50, 200} {
		b.Run(fmt.Sprintf("agents=%d", n), func(b *testing.B) {
			names := make([]string, n)
			for i := range names {
				names[i] = fmt.Sprintf("agent_%04d", i)
			}
			msgs := make([]*broker.Message, n)
			for i, name := range names {
				card := a2a.AgentCard{
					Name: name,
					Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
						URI:    "https://solace.com/a2a/extensions/peer-agent-topology",
						Params: map[string]any{"peer_agent_names": names},
					}}},
				}
				data, err := json.Marshal(card)
				if err != nil {
					b.Fatal(err)
				}
				msgs[i] = &broker.Message{Payload: data, UserProperties: map[string]string{"instance_id": "A"}}
			}
			dir := NewDirectory(nil, testNamespace, discardLogger())
			b.ReportAllocs()
			for b.Loop() {
				for _, m := range msgs {
					dir.Ingest(m)
				}
			}
		})
	}
}
