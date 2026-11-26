// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import "testing"

func TestNotifyRegisterThenSignalWakes(t *testing.T) {
	r := newNotifyRegistry()
	ch := r.register("task-1")
	t.Cleanup(func() { r.unregister("task-1", ch) })

	r.signal("task-1")

	select {
	case <-ch:
	default:
		t.Fatal("expected a wake on the registered channel after signal")
	}
}

func TestNotifySignalNoWaiters(t *testing.T) {
	r := newNotifyRegistry()
	r.signal("nobody-home")
}

func TestNotifySignalCoalesces(t *testing.T) {
	r := newNotifyRegistry()
	ch := r.register("task-1")
	t.Cleanup(func() { r.unregister("task-1", ch) })

	r.signal("task-1")
	r.signal("task-1")

	select {
	case <-ch:
	default:
		t.Fatal("expected first wake to be buffered")
	}
	select {
	case <-ch:
		t.Fatal("second signal should have coalesced, not produced a second wake")
	default:
	}
}

func TestNotifyUnregisterDropsTaskEntryWithLastSubscriber(t *testing.T) {
	r := newNotifyRegistry()
	ch1 := r.register("task-1")
	ch2 := r.register("task-1")

	sh := r.shardFor("task-1")

	sh.mu.Lock()
	haveTwo := len(sh.waiters["task-1"]) == 2
	sh.mu.Unlock()
	if !haveTwo {
		t.Fatal("expected 2 waiters after two registrations")
	}

	r.unregister("task-1", ch1)
	sh.mu.Lock()
	_, stillPresent := sh.waiters["task-1"]
	sh.mu.Unlock()
	if !stillPresent {
		t.Fatal("task entry dropped while a subscriber still remains")
	}

	r.unregister("task-1", ch2)
	sh.mu.Lock()
	_, gone := sh.waiters["task-1"]
	sh.mu.Unlock()
	if gone {
		t.Fatal("task entry should be removed once the last subscriber leaves")
	}

	r.signal("task-1")
}
