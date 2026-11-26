// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetwatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
)

const testNamespace = "budget-watch-test"

var (
	discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	periodEnd     = time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)
)

func blocked(at time.Time) budget.EnforcementState {
	return budgettest.MeshState(true, periodEnd, at)
}

func connectedBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) }) //nolint:contextcheck
	return brk
}

func startWatcher(t *testing.T, gate *budget.Gate, fetch Fetch, brk broker.Broker) *Watcher {
	t.Helper()
	w := New(gate, fetch, brk, testNamespace, discardLogger)
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck
	return w
}

func publish(t *testing.T, brk broker.Broker, payload []byte) {
	t.Helper()
	topic := a2a.BudgetStateTopic(testNamespace)
	if err := brk.Publish(t.Context(), topic, &broker.Message{Topic: topic, Payload: payload}); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func waitForRefusal(t *testing.T, gate *budget.Gate) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if gate.Check("") != nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestWatcher_AppliesEachBroadcastToTheGate(t *testing.T) {
	brk := connectedBroker(t)
	gate := budgettest.NewGate()
	startWatcher(t, gate, nil, brk)

	payload, err := json.Marshal(blocked(time.Now()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	publish(t, brk, payload)
	if !waitForRefusal(t, gate) {
		t.Fatal("a broadcast blocked state never reached the gate")
	}
}

func TestWatcher_IgnoresABroadcastItCannotDecode(t *testing.T) {
	brk := connectedBroker(t)
	gate := budgettest.NewGate()
	startWatcher(t, gate, nil, brk)

	publish(t, brk, []byte("not json"))
	payload, err := json.Marshal(blocked(time.Now()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	publish(t, brk, payload)
	if !waitForRefusal(t, gate) {
		t.Fatal("a broadcast after an undecodable one was not applied")
	}
}

func TestWatcher_PullsAtStartUntilAPullSucceeds(t *testing.T) {
	brk := connectedBroker(t)
	gate := budgettest.NewGate()
	var pulls atomic.Int32
	startWatcher(t, gate, func(context.Context) (budget.EnforcementState, error) {
		if pulls.Add(1) == 1 {
			return budget.EnforcementState{}, errors.New("platform not up yet")
		}
		return blocked(time.Now()), nil
	}, brk)

	if !waitForRefusal(t, gate) {
		t.Fatal("the state from a retried pull never reached the gate")
	}
	time.Sleep(50 * time.Millisecond)
	if n := pulls.Load(); n != 2 {
		t.Errorf("pulls = %d, want one failure then one success, and no more", n)
	}
}

func TestWatcher_WithoutABrokerDoesNothing(t *testing.T) {
	w := New(budgettest.NewGate(), nil, nil, testNamespace, discardLogger)
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := w.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestWatcher_WarnsOnceWhenPullsKeepFailing(t *testing.T) {
	brk := connectedBroker(t)
	var logs syncBuffer
	var pulls atomic.Int32
	w := New(budgettest.NewGate(), func(context.Context) (budget.EnforcementState, error) {
		pulls.Add(1)
		return budget.EnforcementState{}, errors.New("unknown resource: budget-state")
	}, brk, testNamespace, slog.New(slog.NewTextHandler(&logs, nil)))
	w.warnAfter = 0
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

	deadline := time.Now().Add(5 * time.Second)
	for pulls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := strings.Count(logs.String(), "budget state still unconfirmed after pulling"); n != 1 {
		t.Errorf("warnings = %d after %d failed pulls, want exactly one: %s", n, pulls.Load(), logs.String())
	}
}

func TestWatcher_PullsAgainWhenABlocksPeriodEndsWithoutABroadcast(t *testing.T) {
	brk := connectedBroker(t)
	gate := budgettest.NewGate()
	var pulls atomic.Int32
	firstPeriodEnd := time.Now().Add(300 * time.Millisecond)
	fetch := func(context.Context) (budget.EnforcementState, error) {
		if pulls.Add(1) == 1 {
			return budgettest.MeshState(true, firstPeriodEnd, time.Now()), nil
		}
		return budgettest.MeshState(false, periodEnd, time.Now()), nil
	}
	w := New(gate, fetch, brk, testNamespace, discardLogger)
	w.confirmEvery = 20 * time.Millisecond
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

	deadline := time.Now().Add(5 * time.Second)
	for gate.Check("") != nil || time.Now().Before(firstPeriodEnd) {
		if time.Now().After(deadline) {
			t.Fatalf("gate still refuses after the blocked period ended: %v (pulls=%d)", gate.Check(""), pulls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pulls.Load() < 2 {
		t.Errorf("pulls = %d, want a second pull once the block's period ended", pulls.Load())
	}
}

func TestWatcher_PullsAgainWhenAUserBlocksPeriodEndsWithoutABroadcast(t *testing.T) {
	brk := connectedBroker(t)
	gate := budgettest.NewGate()
	user := budget.UserKey("", "spent-user")
	var pulls atomic.Int32
	firstPeriodEnd := time.Now().Add(300 * time.Millisecond)
	fetch := func(context.Context) (budget.EnforcementState, error) {
		if pulls.Add(1) == 1 {
			return budgettest.UserBlockedState(firstPeriodEnd, time.Now(), user), nil
		}
		return budgettest.MeshState(false, periodEnd, time.Now()), nil
	}
	w := New(gate, fetch, brk, testNamespace, discardLogger)
	w.confirmEvery = 20 * time.Millisecond
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

	deadline := time.Now().Add(5 * time.Second)
	for gate.Check(user) != nil || time.Now().Before(firstPeriodEnd) {
		if time.Now().After(deadline) {
			t.Fatalf("the user is still refused after their blocked period ended: %v (pulls=%d)", gate.Check(user), pulls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pulls.Load() < 2 {
		t.Errorf("pulls = %d, want a second pull once the user block's period ended", pulls.Load())
	}
}

func TestWatcher_BacksOffWhenAPullDoesNotConfirmTheGate(t *testing.T) {
	brk := connectedBroker(t)
	gate := budgettest.NewGate()
	var pulls atomic.Int32
	ended := time.Now().Add(-time.Minute)
	startWatcher(t, gate, func(context.Context) (budget.EnforcementState, error) {
		pulls.Add(1)
		return budgettest.MeshState(true, ended, time.Now()), nil
	}, brk)

	time.Sleep(500 * time.Millisecond)
	if !errors.Is(gate.Check(""), budget.ErrUnconfirmed) {
		t.Fatalf("gate = %v, want unconfirmed by a block whose period has ended", gate.Check(""))
	}
	if n := pulls.Load(); n > 2 {
		t.Errorf("pulls = %d in half a second, want the watcher to back off between pulls that do not confirm", n)
	}
}

func TestWatcher_PullsAgainWhenThePlatformGoesSilentAboutABlock(t *testing.T) {
	alice := budget.UserKey("", "alice")
	for name, block := range map[string]func() budget.EnforcementState{
		"mesh": func() budget.EnforcementState { return blocked(time.Now()) },
		"user": func() budget.EnforcementState {
			return budgettest.UserBlockedState(time.Now().Add(time.Hour), time.Now(), alice)
		},
	} {
		t.Run(name, func(t *testing.T) {
			brk := connectedBroker(t)
			gate := budgettest.NewGate()
			var pulls atomic.Int32
			fetch := func(context.Context) (budget.EnforcementState, error) {
				if pulls.Add(1) == 1 {
					return block(), nil
				}
				return budget.EnforcementState{ComputedAt: time.Now()}, nil
			}
			w := New(gate, fetch, brk, testNamespace, discardLogger)
			w.confirmEvery = 20 * time.Millisecond
			w.repullAfter = 100 * time.Millisecond
			if err := w.Start(t.Context()); err != nil {
				t.Fatalf("start: %v", err)
			}
			t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

			deadline := time.Now().Add(5 * time.Second)
			for gate.Check(alice) == nil {
				if time.Now().After(deadline) {
					t.Fatal("the pulled block never reached the gate")
				}
				time.Sleep(5 * time.Millisecond)
			}
			for gate.Check(alice) != nil {
				if time.Now().After(deadline) {
					t.Fatalf("gate still refuses after the platform went silent: %v (pulls=%d)", gate.Check(alice), pulls.Load())
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestWatcher_DoesNotPullABlockThePlatformKeepsBroadcasting(t *testing.T) {
	brk := connectedBroker(t)
	gate := budgettest.NewGate()
	var pulls atomic.Int32
	w := New(gate, func(context.Context) (budget.EnforcementState, error) {
		pulls.Add(1)
		return blocked(time.Now()), nil
	}, brk, testNamespace, discardLogger)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	w.now = func() time.Time { return time.Unix(0, clock.Load()) }
	w.confirmEvery = 5 * time.Millisecond
	w.repullAfter = 200 * time.Millisecond
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

	for i := range 10 {
		heardAt := clock.Add(int64(150 * time.Millisecond))
		payload, _ := json.Marshal(blocked(time.Now().Add(time.Duration(i+1) * time.Second)))
		publish(t, brk, payload)
		deadline := time.Now().Add(5 * time.Second)
		for w.lastHeard.Load() != heardAt {
			if time.Now().After(deadline) {
				t.Fatalf("broadcast %d never reached the watcher", i)
			}
			time.Sleep(time.Millisecond)
		}
		time.Sleep(15 * time.Millisecond)
	}
	if n := pulls.Load(); n != 1 {
		t.Errorf("pulls = %d, want only the pull at start while broadcasts keep arriving", n)
	}
}
