// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func newTestManager() *SSEManager {
	return NewSSEManager(100, slog.Default())
}

func (m *SSEManager) totalTasks() int {
	n := 0
	for i := range m.shards {
		m.shards[i].mu.Lock()
		n += len(m.shards[i].tasks)
		m.shards[i].mu.Unlock()
	}
	return n
}

func TestSSEManager_PublishToActiveSubscriber(t *testing.T) {
	m := newTestManager()
	m.RegisterTask("t1", "", "")

	ch, _, ok := m.Subscribe("t1")
	if !ok {
		t.Fatal("expected subscribe to succeed")
	}

	m.Publish("t1", &SSEEvent{EventType: "status_update", Data: []byte(`{"x":1}`)})

	got := drain(ch, 1, time.Second)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].EventType != "status_update" {
		t.Errorf("unexpected event type: %s", got[0].EventType)
	}
}

func TestSSEManager_FinalEventClosesChannel(t *testing.T) {
	m := newTestManager()
	m.RegisterTask("t1", "", "")

	ch, _, ok := m.Subscribe("t1")
	if !ok {
		t.Fatal("expected subscribe to succeed")
	}

	m.Publish("t1", &SSEEvent{EventType: "status_update", Data: []byte(`{"s":1}`)})
	m.Publish("t1", &SSEEvent{EventType: "final_response", Data: []byte(`{"done":true}`), Final: true})

	var events []*SSEEvent
	timeout := time.After(time.Second)
	for {
		select {
		case evt, open := <-ch:
			if !open {
				goto done
			}
			events = append(events, evt)
		case <-timeout:
			t.Fatal("timed out waiting for channel close")
		}
	}
done:
	if len(events) != 2 {
		t.Fatalf("expected 2 events before close, got %d", len(events))
	}
	if events[1].EventType != "final_response" {
		t.Errorf("expected final_response, got %s", events[1].EventType)
	}
}

func TestSSEManager_MultipleSubscribers(t *testing.T) {
	m := newTestManager()
	m.RegisterTask("t1", "", "")

	ch1, _, _ := m.Subscribe("t1")
	ch2, _, _ := m.Subscribe("t1")

	m.Publish("t1", &SSEEvent{EventType: "status_update", Data: []byte(`{"m":1}`)})

	got1 := drain(ch1, 1, time.Second)
	got2 := drain(ch2, 1, time.Second)

	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("expected both subscribers to get 1 event, got %d and %d", len(got1), len(got2))
	}
}

func TestSSEManager_SubscribeToClosedTaskReturnsClosedChannel(t *testing.T) {
	m := newTestManager()
	m.RegisterTask("t1", "", "")

	m.Publish("t1", &SSEEvent{EventType: "status_update", Data: []byte(`{"s":1}`)})
	m.Publish("t1", &SSEEvent{EventType: "final_response", Data: []byte(`{"done":true}`), Final: true})

	ch, _, ok := m.Subscribe("t1")
	if !ok {
		t.Fatal("expected subscribe to succeed for closed task")
	}

	select {
	case evt, open := <-ch:
		if open {
			t.Fatalf("expected no live events on a closed task, got %v", evt.EventType)
		}
	case <-time.After(time.Second):
		t.Fatal("expected channel to be closed immediately")
	}
}

func TestSSEManager_SubscribeNonexistent(t *testing.T) {
	m := newTestManager()
	_, _, ok := m.Subscribe("nope")
	if ok {
		t.Fatal("expected subscribe to fail for nonexistent task")
	}
}

func TestSSEManager_CleanupTask(t *testing.T) {
	m := newTestManager()
	m.RegisterTask("t1", "", "")
	m.CleanupTask("t1")
	if m.HasTask("t1") {
		t.Fatal("expected task to be cleaned up")
	}
}

func TestSSEManager_QueueFull(t *testing.T) {
	m := NewSSEManager(2, slog.Default())
	m.RegisterTask("t1", "", "")

	ch, connID, ok := m.Subscribe("t1")
	if !ok {
		t.Fatal("expected subscribe to succeed")
	}

	for range 5 {
		m.Publish("t1", &SSEEvent{EventType: "status_update", Data: []byte(`{}`)})
	}

	drained := 0
	closed := false
	deadline := time.After(time.Second)
	for !closed {
		select {
		case _, open := <-ch:
			if !open {
				closed = true
			} else {
				drained++
			}
		case <-deadline:
			t.Fatal("expected the overflowed subscriber's channel to be closed")
		}
	}
	if drained > 2 {
		t.Fatalf("expected at most queue-size (2) buffered events, got %d", drained)
	}

	m.Publish("t1", &SSEEvent{EventType: "status_update", Data: []byte(`{}`)})
	m.Unsubscribe("t1", connID)
}

func TestSSEManager_AutoRegister(t *testing.T) {
	m := newTestManager()

	m.Publish("t1", &SSEEvent{EventType: "status_update", Data: []byte(`{"auto":true}`)})
	if !m.HasTask("t1") {
		t.Fatal("expected auto-registration")
	}

	if _, _, ok := m.Subscribe("t1"); !ok {
		t.Fatal("expected subscribe after auto-register")
	}
}

func TestSSEManager_OrphanSweep(t *testing.T) {
	m := newTestManager()
	m.orphanGrace = 20 * time.Millisecond
	m.RegisterTask("t1", "", "")

	m.Publish("t1", &SSEEvent{EventType: "final_response", Data: []byte(`{"done":true}`), Final: true})

	if !m.HasTask("t1") {
		t.Fatal("finalized entry should be retained during the grace window")
	}
	deadline := time.After(time.Second)
	for m.HasTask("t1") {
		select {
		case <-deadline:
			t.Fatal("orphaned finalized entry was not swept after the grace period")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestSSEManager_OrphanSweep_SubscriberDuringGraceCancels(t *testing.T) {
	m := newTestManager()
	m.orphanGrace = 40 * time.Millisecond
	m.RegisterTask("t1", "", "")
	m.Publish("t1", &SSEEvent{EventType: "final_response", Data: []byte(`{"done":true}`), Final: true})

	_, connID, ok := m.Subscribe("t1")
	if !ok {
		t.Fatal("subscribe within grace window should succeed")
	}
	time.Sleep(120 * time.Millisecond)
	if !m.HasTask("t1") {
		t.Fatal("entry must survive: a subscriber connected during the grace window")
	}
	m.Unsubscribe("t1", connID)
	if m.HasTask("t1") {
		t.Fatal("entry should be freed once the subscriber unsubscribes from a closed task")
	}
}

func TestSSEManager_NoOrphanSweepWhenSubscriberPresent(t *testing.T) {
	m := newTestManager()
	m.orphanGrace = 30 * time.Millisecond
	m.RegisterTask("t1", "", "")
	_, connID, _ := m.Subscribe("t1")
	m.Publish("t1", &SSEEvent{EventType: "final_response", Data: []byte(`{}`), Final: true})
	time.Sleep(80 * time.Millisecond)
	if !m.HasTask("t1") {
		t.Fatal("no sweep should be scheduled when a subscriber is present at final")
	}
	m.Unsubscribe("t1", connID)
	if m.HasTask("t1") {
		t.Fatal("entry should be freed via unsubscribe")
	}
}

func TestSSEManager_OrphanSweepDisabled(t *testing.T) {
	m := newTestManager()
	m.orphanGrace = 0
	m.RegisterTask("t1", "", "")
	m.Publish("t1", &SSEEvent{EventType: "final_response", Data: []byte(`{}`), Final: true})
	time.Sleep(40 * time.Millisecond)
	if !m.HasTask("t1") {
		t.Fatal("with orphanGrace<=0 a finalized subscriber-less entry must NOT be swept")
	}
}

func TestSSEManager_ConcurrentPublishSubscribe(t *testing.T) {
	m := NewSSEManager(50, slog.Default())
	m.orphanGrace = 5 * time.Millisecond

	var wg sync.WaitGroup
	for w := range 8 {
		taskID := fmt.Sprintf("t%d", w%4)
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.RegisterTask(taskID, "", "")
			for i := range 100 {
				m.Publish(taskID, &SSEEvent{EventType: "status_update", Data: make([]byte, (i%64)+1), Final: i == 99})
			}
		}()
		go func() {
			defer wg.Done()
			for range 60 {
				if _, connID, ok := m.Subscribe(taskID); ok {
					m.Unsubscribe(taskID, connID)
				}
			}
		}()
	}
	wg.Wait()
}

func TestSSEManager_ShardIsolation(t *testing.T) {
	m := newTestManager()

	seen := make(map[uint32]struct{})
	for i := range 200 {
		seen[shardIndex(fmt.Sprintf("task-%d", i))] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("shardIndex collapsed %d keys into %d shard(s); expected a spread", 200, len(seen))
	}

	a, b := "task-0", ""
	for i := 1; i < 500; i++ {
		cand := fmt.Sprintf("task-%d", i)
		if shardIndex(cand) != shardIndex(a) {
			b = cand
			break
		}
	}
	if b == "" {
		t.Fatal("could not find two task IDs on distinct shards")
	}

	m.RegisterTask(a, "", "")
	m.RegisterTask(b, "", "")
	chA, _, _ := m.Subscribe(a)
	chB, _, _ := m.Subscribe(b)

	m.Publish(a, &SSEEvent{EventType: "status_update", Data: []byte(`{"a":1}`)})

	if got := drain(chA, 1, time.Second); len(got) != 1 {
		t.Fatalf("task A subscriber expected 1 event, got %d", len(got))
	}
	if got := drain(chB, 1, 100*time.Millisecond); len(got) != 0 {
		t.Fatalf("task B subscriber must receive nothing when only A is published to, got %d", len(got))
	}

	m.CleanupTask(a)
	if m.HasTask(a) {
		t.Fatal("task A should be cleaned up")
	}
	if !m.HasTask(b) {
		t.Fatal("cleaning up task A must not affect task B on another shard")
	}
}

func TestSSEManager_ConcurrentPublishForget(t *testing.T) {
	m := NewSSEManager(50, slog.Default())
	m.orphanGrace = 5 * time.Millisecond

	var mu sync.Mutex
	forgets := make(map[string]int)
	m.SetForgetHook(func(taskID string) {
		mu.Lock()
		forgets[taskID]++
		mu.Unlock()
	})

	const n = 200
	var wg sync.WaitGroup
	for i := range n {
		taskID := fmt.Sprintf("race-task-%d", i)
		m.RegisterTask(taskID, "", "")
		_, connID, _ := m.Subscribe(taskID)
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.Publish(taskID, &SSEEvent{EventType: "final_response", Final: true})
		}()
		go func() {
			defer wg.Done()
			m.Unsubscribe(taskID, connID)
		}()
	}
	wg.Wait()

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		done := len(forgets) == n
		mu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			got := len(forgets)
			mu.Unlock()
			t.Fatalf("only %d/%d tasks were forgotten before the deadline", got, n)
		case <-time.After(5 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for taskID, c := range forgets {
		if c != 1 {
			t.Errorf("task %s forgotten %d times, want exactly 1", taskID, c)
		}
	}
	if m.totalTasks() != 0 {
		t.Errorf("expected all task entries freed, %d remain", m.totalTasks())
	}
}

func TestAdapter_WiresForgetHook(t *testing.T) {
	a, _, _ := setupTestAdapter(t)
	if a.sseManager.onForget == nil {
		t.Fatal("adapter Init did not wire the SSE manager forget hook")
	}
}

func TestSSEManager_ForgetHookFiresOnLastSubscriber(t *testing.T) {
	m := newTestManager()
	var mu sync.Mutex
	var forgotten []string
	m.SetForgetHook(func(taskID string) {
		mu.Lock()
		forgotten = append(forgotten, taskID)
		mu.Unlock()
	})

	m.RegisterTask("t1", "", "")
	_, connID, _ := m.Subscribe("t1")

	m.Publish("t1", &SSEEvent{EventType: "final_response", Final: true})
	mu.Lock()
	n := len(forgotten)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("forget hook fired while a subscriber was still attached: %v", forgotten)
	}

	m.Unsubscribe("t1", connID)
	mu.Lock()
	defer mu.Unlock()
	if len(forgotten) != 1 || forgotten[0] != "t1" {
		t.Fatalf("expected Forget(t1) on last unsubscribe of a closed task, got %v", forgotten)
	}
}

func TestSSEManager_ForgetHookWaitsForLastSubscriber(t *testing.T) {
	m := newTestManager()
	var mu sync.Mutex
	var count int
	m.SetForgetHook(func(string) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	m.RegisterTask("t1", "", "")
	_, c1, _ := m.Subscribe("t1")
	_, c2, _ := m.Subscribe("t1")
	m.Publish("t1", &SSEEvent{EventType: "final_response", Final: true})

	m.Unsubscribe("t1", c1)
	mu.Lock()
	n := count
	mu.Unlock()
	if n != 0 {
		t.Fatalf("forget hook fired while a subscriber remained: count=%d", n)
	}

	m.Unsubscribe("t1", c2)
	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Fatalf("expected exactly one Forget after the last subscriber left, got %d", count)
	}
}

func TestSSEManager_ForgetHookFiresOnOrphanSweep(t *testing.T) {
	m := newTestManager()
	m.orphanGrace = 10 * time.Millisecond
	done := make(chan string, 1)
	m.SetForgetHook(func(taskID string) { done <- taskID })

	m.RegisterTask("t1", "", "")
	m.Publish("t1", &SSEEvent{EventType: "final_response", Final: true})

	select {
	case got := <-done:
		if got != "t1" {
			t.Fatalf("orphan sweep forgot %q, want t1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("orphan sweep did not fire the forget hook")
	}
}

func drain(ch <-chan *SSEEvent, n int, timeout time.Duration) []*SSEEvent {
	var events []*SSEEvent
	timer := time.After(timeout)
	for range n {
		select {
		case evt, open := <-ch:
			if !open {
				return events
			}
			events = append(events, evt)
		case <-timer:
			return events
		}
	}
	return events
}
