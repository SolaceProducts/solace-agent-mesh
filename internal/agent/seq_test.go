// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestEventSeqGen_PerTaskMonotonic(t *testing.T) {
	g := NewEventSeqGen()
	for i := uint64(1); i <= 5; i++ {
		got := g.Next("task-A")
		if got != i {
			t.Fatalf("task-A call %d: got %d, want %d", i, got, i)
		}
	}
}

func TestEventSeqGen_TasksAreIndependent(t *testing.T) {
	g := NewEventSeqGen()
	if g.Next("task-A") != 1 {
		t.Fatal("task-A first call should be 1")
	}
	if g.Next("task-B") != 1 {
		t.Fatal("task-B first call should be 1 — counters must be independent per taskID")
	}
	if g.Next("task-A") != 2 {
		t.Fatal("task-A second call should be 2")
	}
}

func TestEventSeqGen_DropResetsCounter(t *testing.T) {
	g := NewEventSeqGen()
	_ = g.Next("task-A")
	_ = g.Next("task-A")
	g.Drop("task-A")
	if got := g.Next("task-A"); got != 1 {
		t.Fatalf("post-Drop first call: got %d, want 1", got)
	}
}

func TestEventSeqGen_StampWritesBothProps(t *testing.T) {
	g := NewEventSeqGen()
	m := map[string]string{}
	g.Stamp(m, "agent-foo", "task-A")
	if m[a2a.PropPublisherID] != "agent-foo" {
		t.Fatalf("publisherId = %q, want %q", m[a2a.PropPublisherID], "agent-foo")
	}
	if m[a2a.PropEventSeq] != "1" {
		t.Fatalf("eventSeq = %q, want %q", m[a2a.PropEventSeq], "1")
	}
	g.Stamp(m, "agent-foo", "task-A")
	if m[a2a.PropEventSeq] != "2" {
		t.Fatalf("eventSeq second call = %q, want %q", m[a2a.PropEventSeq], "2")
	}
}

func TestEventSeqGen_StampNoOpOnEmptyPublisher(t *testing.T) {
	g := NewEventSeqGen()
	m := map[string]string{}
	g.Stamp(m, "", "task-A")
	if _, ok := m[a2a.PropPublisherID]; ok {
		t.Fatalf("empty publisherID must skip stamp, got %v", m)
	}
	if _, ok := m[a2a.PropEventSeq]; ok {
		t.Fatalf("empty publisherID must skip stamp, got %v", m)
	}
}

func TestEventSeqGen_NilReceiverIsSafe(t *testing.T) {
	var g *EventSeqGen
	if got := g.Next("task-A"); got != 0 {
		t.Fatalf("nil Next: got %d, want 0", got)
	}
	g.Drop("task-A")
	m := map[string]string{}
	g.Stamp(m, "agent", "task-A")
	if len(m) != 0 {
		t.Fatalf("nil Stamp must be no-op, got %v", m)
	}
}

func TestEventSeqGen_ConcurrentTasksIndependent(t *testing.T) {
	g := NewEventSeqGen()
	const tasks = 8
	const callsPerTask = 200

	var wg sync.WaitGroup
	for i := range tasks {
		taskID := "task-" + string(rune('A'+i))
		wg.Go(func() {
			seen := make(map[uint64]struct{}, callsPerTask)
			var maxSeq uint64
			for range callsPerTask {
				n := g.Next(taskID)
				if _, dup := seen[n]; dup {
					t.Errorf("duplicate seq %d for %s", n, taskID)
				}
				seen[n] = struct{}{}
				if n > maxSeq {
					maxSeq = n
				}
			}
			if maxSeq != uint64(callsPerTask) {
				t.Errorf("%s max seq = %d, want %d", taskID, maxSeq, callsPerTask)
			}
		})
	}
	wg.Wait()
}

func TestEventSeqGen_DropPreservesActiveTask(t *testing.T) {
	g := NewEventSeqGen()
	g.Next("task-pause")
	g.Next("task-pause")
	if got := g.Next("task-pause"); got != 3 {
		t.Fatalf("pre-pause: got %d, want 3", got)
	}
	if next := g.Next("task-pause"); next != 4 {
		t.Fatalf("post-intercept resume: got %d, want 4 (intercept must not drop)", next)
	}
	g.Drop("task-pause")
	if restart := g.Next("task-pause"); restart != 1 {
		t.Fatalf("post-Drop restart: got %d, want 1", restart)
	}
}

func TestEventSeqGen_SameTaskConcurrentNoDup(t *testing.T) {
	g := NewEventSeqGen()
	const callers = 8
	const callsPer = 50

	var counts sync.Map
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			for range callsPer {
				n := g.Next("task-shared")
				v, _ := counts.LoadOrStore(n, new(int64))
				atomic.AddInt64(v.(*int64), 1)
			}
		})
	}
	wg.Wait()

	expected := uint64(callers * callsPer)
	for s := uint64(1); s <= expected; s++ {
		v, ok := counts.Load(s)
		if !ok {
			t.Fatalf("missing seq %d", s)
		}
		if c := atomic.LoadInt64(v.(*int64)); c != 1 {
			t.Fatalf("seq %d emitted %d times, want 1", s, c)
		}
	}
}
