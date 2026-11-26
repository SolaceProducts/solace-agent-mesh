// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitCond(timeout time.Duration, f func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return true
		}
		time.Sleep(1 * time.Millisecond)
	}
	return f()
}

func TestInProcessTimerManager_FiresAtDeadline(t *testing.T) {
	var fired int32
	gotEvent := make(chan Event, 1)

	m := NewInProcessTimerManager(nil, func(_ string, event Event) {
		atomic.AddInt32(&fired, 1)
		select {
		case gotEvent <- event:
		default:
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go m.Start(ctx)

	m.Schedule("exec-A", []Timer{
		{Kind: TimerKindRetry, NodeID: "n1", FiresAt: time.Now().Add(20 * time.Millisecond)},
	})

	select {
	case ev := <-gotEvent:
		retry, ok := ev.(*EventRetryDue)
		if !ok {
			t.Fatalf("fired event type = %T, want *EventRetryDue", ev)
		}
		if retry.NodeID != "n1" {
			t.Errorf("NodeID = %q, want n1", retry.NodeID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timer did not fire within 500ms")
	}
	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Errorf("fire count = %d, want 1", got)
	}
}

func TestInProcessTimerManager_CancelRemovesPending(t *testing.T) {
	var fired int32
	m := NewInProcessTimerManager(nil, func(_ string, _ Event) {
		atomic.AddInt32(&fired, 1)
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go m.Start(ctx)

	m.Schedule("exec-A", []Timer{
		{Kind: TimerKindRetry, NodeID: "n1", FiresAt: time.Now().Add(30 * time.Millisecond)},
	})
	m.Cancel("exec-A")
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt32(&fired); got != 0 {
		t.Errorf("fire count = %d after Cancel, want 0", got)
	}
}

func TestInProcessTimerManager_ReconcileRemoves(t *testing.T) {
	var fired int32
	m := NewInProcessTimerManager(nil, func(_ string, _ Event) {
		atomic.AddInt32(&fired, 1)
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go m.Start(ctx)

	m.Schedule("exec-A", []Timer{
		{Kind: TimerKindNodeTimeout, NodeID: "n1", SubTaskID: "s1", FiresAt: time.Now().Add(40 * time.Millisecond)},
	})
	m.Schedule("exec-A", nil)
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&fired); got != 0 {
		t.Errorf("fire count = %d after reconcile-remove, want 0", got)
	}
}

func TestInProcessTimerManager_OrderingAcrossExecutions(t *testing.T) {
	var mu sync.Mutex
	var order []string
	m := NewInProcessTimerManager(nil, func(execID string, _ Event) {
		mu.Lock()
		order = append(order, execID)
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go m.Start(ctx)

	now := time.Now()
	m.Schedule("exec-A", []Timer{
		{Kind: TimerKindWorkflowTimeout, FiresAt: now.Add(60 * time.Millisecond)},
	})
	m.Schedule("exec-B", []Timer{
		{Kind: TimerKindWorkflowTimeout, FiresAt: now.Add(20 * time.Millisecond)},
	})
	m.Schedule("exec-C", []Timer{
		{Kind: TimerKindWorkflowTimeout, FiresAt: now.Add(40 * time.Millisecond)},
	})

	ok := waitCond(500*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 3
	})
	if !ok {
		t.Fatalf("expected 3 fires, got %v", order)
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	want := []string{"exec-B", "exec-C", "exec-A"}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("fire order = %v, want %v", got, want)
		}
	}
}

func TestInProcessTimerManager_EventFromTimer_RoutingFields(t *testing.T) {
	gotEvent := make(chan Event, 1)
	m := NewInProcessTimerManager(nil, func(_ string, event Event) {
		select {
		case gotEvent <- event:
		default:
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go m.Start(ctx)

	m.Schedule("exec-A", []Timer{
		{
			Kind:      TimerKindNodeTimeout,
			NodeID:    "parent_map",
			SubTaskID: "s1",
			FiresAt:   time.Now().Add(15 * time.Millisecond),
			MapNodeID: "parent_map",
			ItemIndex: 7,
		},
	})
	select {
	case ev := <-gotEvent:
		nt, ok := ev.(*EventNodeTimeout)
		if !ok {
			t.Fatalf("fired event type = %T, want *EventNodeTimeout", ev)
		}
		if nt.MapNodeID != "parent_map" || nt.ItemIndex != 7 || nt.SubTaskID != "s1" {
			t.Errorf("routing fields not preserved: %+v", nt)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no fire within 500ms")
	}
}

func TestInProcessTimerManager_SingleGoroutineAcrossManyTimers(t *testing.T) {
	m := NewInProcessTimerManager(nil, func(_ string, _ Event) {})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go m.Start(ctx)
	if !waitCond(2*time.Second, func() bool {
		return countManagerGoroutines() >= 1
	}) {
		t.Fatalf("manager goroutine did not appear within 2s")
	}

	future := time.Now().Add(10 * time.Second)
	for ex := range 50 {
		execID := "exec-" + itoa(ex)
		timers := make([]Timer, 0, 10)
		for i := range 10 {
			timers = append(timers, Timer{
				Kind:    TimerKindRetry,
				NodeID:  "n" + itoa(i),
				FiresAt: future,
			})
		}
		m.Schedule(execID, timers)
	}

	if got := countManagerGoroutines(); got != 1 {
		t.Errorf("manager goroutine count = %d, want exactly 1 (§2.6 invariant)", got)
	}
}

func countManagerGoroutines() int {
	buf := make([]byte, 64*1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "(*inProcessTimerManager).Start")
		}
		buf = make([]byte, 2*len(buf))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
