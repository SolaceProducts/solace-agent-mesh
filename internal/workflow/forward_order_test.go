// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	forwardOrderStatusTopic = "test/status"
	responseWindow          = 100 * time.Millisecond
	forwardOrderDeadline    = 5 * time.Second
)

func TestNodeResponseWaitsForTheStatusForwardsBeforeIt(t *testing.T) {
	e, brk := newUsageFactsExecutor(t, agentNodes(1))
	e.execCtx.UserProps.A2AStatusTopic = forwardOrderStatusTopic

	forwarding, release := make(chan struct{}), make(chan struct{})
	var armed, held, released, finishedEarly atomic.Bool
	brk.publishHook = func(topic string) error {
		switch {
		case topic == forwardOrderStatusTopic && armed.Load() && held.CompareAndSwap(false, true):
			close(forwarding)
			<-release
		case topic == usageFactsReplyTopic && !released.Load():
			finishedEarly.Store(true)
		}
		return nil
	}
	subTaskID := dispatchedSubTasks(t, e)[0]
	armed.Store(true)

	w, ctx := e.instance, t.Context()
	w.routeMessage(ctx, forwardOrderStatus(t, subTaskID))
	select {
	case <-forwarding:
	case <-time.After(forwardOrderDeadline):
		t.Fatal("the status event never reached its forward")
	}
	w.routeMessage(ctx, forwardOrderResponse(t, subTaskID))

	for deadline := time.Now().Add(responseWindow); !finishedEarly.Load() && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	released.Store(true)
	close(release)
	if finishedEarly.Load() {
		t.Fatal("the workflow finished before forwarding a status event its node sent first")
	}

	waitForTerminal(brk)
	terminal, forwarded := terminalIndex(brk), forwardedLLMResponseIndex(brk)
	if terminal < 0 {
		t.Fatal("the workflow never finished once the forward was released")
	}
	if forwarded < 0 || forwarded > terminal {
		t.Errorf("forwarded llm_response at %d, terminal task at %d; want the forward first", forwarded, terminal)
	}
}

func TestStatusForwardIsRegisteredBeforeRouteMessageReturns(t *testing.T) {
	e, brk := newUsageFactsExecutor(t, agentNodes(1))
	e.execCtx.UserProps.A2AStatusTopic = forwardOrderStatusTopic

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var armed atomic.Bool
	brk.publishHook = func(topic string) error {
		if topic == forwardOrderStatusTopic && armed.Load() {
			<-release
		}
		return nil
	}
	subTaskID := dispatchedSubTasks(t, e)[0]
	armed.Store(true)

	const forwards = 64
	w := e.instance
	for i := 1; i <= forwards; i++ {
		w.routeMessage(t.Context(), forwardOrderStatus(t, subTaskID))
		if got := pendingCount(&w.statusForwards, subTaskID); got != i {
			t.Fatalf("after routing status event %d, %d forwards were registered", i, got)
		}
	}
}

func TestNodeResponseIsNotHeldPastTheBoundByAStuckForward(t *testing.T) {
	e, brk := newUsageFactsExecutor(t, agentNodes(1))
	e.execCtx.UserProps.A2AStatusTopic = forwardOrderStatusTopic
	w := e.instance
	var logged strings.Builder
	var logMu sync.Mutex
	w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &logMu}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	w.statusForwardWait = 50 * time.Millisecond

	forwarding, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	var armed, held atomic.Bool
	brk.publishHook = func(topic string) error {
		if topic == forwardOrderStatusTopic && armed.Load() && held.CompareAndSwap(false, true) {
			close(forwarding)
			<-release
		}
		return nil
	}
	subTaskID := dispatchedSubTasks(t, e)[0]
	armed.Store(true)

	w.routeMessage(t.Context(), forwardOrderStatus(t, subTaskID))
	select {
	case <-forwarding:
	case <-time.After(forwardOrderDeadline):
		t.Fatal("the status event never reached its forward")
	}
	w.routeMessage(t.Context(), forwardOrderResponse(t, subTaskID))

	waitForTerminal(brk)
	if terminalIndex(brk) < 0 {
		t.Fatal("a forward that never returned held the workflow past the bound")
	}
	logMu.Lock()
	defer logMu.Unlock()
	if !strings.Contains(logged.String(), "status forwards still in flight") {
		t.Errorf("giving up on the forward was not logged; log:\n%s", logged.String())
	}
}

func TestPendingForwards(t *testing.T) {
	short := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(t.Context(), 20*time.Millisecond)
	}

	t.Run("an unknown sub-task has nothing to wait for", func(t *testing.T) {
		var p pendingForwards
		if !p.await(t.Context(), "a") {
			t.Error("await reported undrained for a sub-task with no forwards")
		}
	})

	t.Run("await gives up when its context ends", func(t *testing.T) {
		var p pendingForwards
		done := p.begin("a")
		defer done()

		ctx, cancel := short()
		defer cancel()
		if p.await(ctx, "a") {
			t.Error("await reported drained while a forward was still in flight")
		}
		cancelled, stop := context.WithCancel(t.Context())
		stop()
		if p.await(cancelled, "a") {
			t.Error("await on a cancelled context reported drained")
		}
	})

	t.Run("every forward must finish", func(t *testing.T) {
		var p pendingForwards
		first, second := p.begin("a"), p.begin("a")
		first()

		ctx, cancel := short()
		defer cancel()
		if p.await(ctx, "a") {
			t.Error("await reported drained with one of two forwards still in flight")
		}
		second()
		if !p.await(t.Context(), "a") {
			t.Error("await did not drain once both forwards finished")
		}
		if got := pendingCount(&p, "a"); got != 0 {
			t.Errorf("%d forwards still tracked after all finished", got)
		}
	})

	t.Run("a sub-task is tracked on its own", func(t *testing.T) {
		var p pendingForwards
		done := p.begin("a")
		defer done()
		if !p.await(t.Context(), "b") {
			t.Error("await on b waited for a forward of a")
		}
	})

	t.Run("a sub-task can be tracked again once drained", func(t *testing.T) {
		var p pendingForwards
		p.begin("a")()
		again := p.begin("a")
		defer again()

		ctx, cancel := short()
		defer cancel()
		if p.await(ctx, "a") {
			t.Error("await reported drained for a forward begun after the first drained")
		}
	})

	t.Run("forwards from many goroutines are all accounted for", func(t *testing.T) {
		var p pendingForwards
		var wg sync.WaitGroup
		for range 32 {
			done := p.begin("a")
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer done()
			}()
		}
		wg.Wait()
		if !p.await(t.Context(), "a") || pendingCount(&p, "a") != 0 {
			t.Errorf("forwards left tracked after every goroutine finished")
		}
	})
}

func pendingCount(p *pendingForwards, subTaskID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f := p.bySubTask[subTaskID]; f != nil {
		return f.count
	}
	return 0
}

func waitForTerminal(brk *capturingBroker) {
	for deadline := time.Now().Add(forwardOrderDeadline); terminalIndex(brk) < 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
}

func forwardOrderStatus(t *testing.T, subTaskID string) *broker.Message {
	t.Helper()
	return &broker.Message{
		Topic:   "test-ns/a2a/v1/agent/status/PayrollWorkflow/" + subTaskID,
		Payload: mkForwardEvent(t, string(a2a.SignalLLMResponse), "Worker0", subTaskID),
	}
}

func forwardOrderResponse(t *testing.T, subTaskID string) *broker.Message {
	t.Helper()
	return &broker.Message{
		Topic:   "test-ns/a2a/v1/agent/response/PayrollWorkflow/" + subTaskID,
		Payload: completedNodeResponse(t, subTaskID),
	}
}

func completedNodeResponse(t *testing.T, subTaskID string) []byte {
	t.Helper()
	task := a2a.Task{
		ID:   subTaskID,
		Kind: "task",
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateCompleted,
			Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("done")}},
		},
	}
	payload, err := json.Marshal(a2a.NewResponse(subTaskID, task))
	if err != nil {
		t.Fatalf("marshal node response: %v", err)
	}
	return payload
}

func terminalIndex(brk *capturingBroker) int {
	for i, m := range brk.snapshot() {
		if m.Topic == usageFactsReplyTopic {
			return i
		}
	}
	return -1
}

func forwardedLLMResponseIndex(brk *capturingBroker) int {
	for i, m := range brk.snapshot() {
		if m.Topic == forwardOrderStatusTopic && strings.Contains(string(m.Payload), string(a2a.SignalLLMResponse)) {
			return i
		}
	}
	return -1
}
