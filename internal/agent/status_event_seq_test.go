// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func receiveAll(t *testing.T, brk broker.Broker, q broker.Queue, want int, budget time.Duration) []*broker.Message {
	t.Helper()
	var out []*broker.Message
	deadline := time.Now().Add(budget)
	for len(out) < want && time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		msg, err := brk.Receive(ctx, q)
		cancel()
		if err == nil && msg != nil {
			out = append(out, msg)
			continue
		}
	}
	return out
}

func TestStatusPublisher_StampsEventSeqMonotonic(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brk.Disconnect(t.Context()) }()

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-seq-1"
	q, err := brk.CreateQueue("status-collector-1", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}

	gen := NewEventSeqGen()
	sp := NewStatusPublisher(brk, StatusPublisherConfig{
		StatusTopic: statusTopic,
		TaskID:      "task-seq-1",
		ContextID:   "ctx",
		AgentName:   "agent-under-test",
		SeqGen:      gen,
	}, slog.Default())

	for range 3 {
		sp.PublishText(t.Context(), "chunk")
	}

	msgs := receiveAll(t, brk, q, 3, 3*time.Second)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	for i, msg := range msgs {
		got := msg.UserProperties[a2a.PropPublisherID]
		if got != "agent-under-test" {
			t.Errorf("msg %d publisherId = %q, want agent-under-test", i, got)
		}
		gotSeq, err := strconv.ParseUint(msg.UserProperties[a2a.PropEventSeq], 10, 64)
		if err != nil {
			t.Errorf("msg %d eventSeq parse: %v (raw=%q)", i, err, msg.UserProperties[a2a.PropEventSeq])
			continue
		}
		want := uint64(i + 1)
		if gotSeq != want {
			t.Errorf("msg %d eventSeq = %d, want %d", i, gotSeq, want)
		}
	}
}

func TestStatusPublisher_OmitsEventSeqWhenNoGen(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brk.Disconnect(t.Context()) }()

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-nogen"
	q, err := brk.CreateQueue("status-collector-nogen", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}

	sp := NewStatusPublisher(brk, StatusPublisherConfig{
		StatusTopic: statusTopic,
		TaskID:      "task-nogen",
		ContextID:   "ctx",
		AgentName:   "agent-under-test",
	}, slog.Default())
	sp.PublishText(t.Context(), "chunk")

	msgs := receiveAll(t, brk, q, 1, 2*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if _, ok := msgs[0].UserProperties[a2a.PropEventSeq]; ok {
		t.Errorf("eventSeq must be absent when no SeqGen is wired in; got %q", msgs[0].UserProperties[a2a.PropEventSeq])
	}
	if _, ok := msgs[0].UserProperties[a2a.PropPublisherID]; ok {
		t.Errorf("publisherId must be absent when no SeqGen is wired in; got %q", msgs[0].UserProperties[a2a.PropPublisherID])
	}
}

func TestStatusPublisher_ConcurrentTasksIndependent(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brk.Disconnect(t.Context()) }()

	gen := NewEventSeqGen()

	makePub := func(taskID, topicSuffix string) (*StatusPublisher, broker.Queue) {
		topic := "test-ns/a2a/v1/gateway/status/test-gw/" + topicSuffix
		q, err := brk.CreateQueue("col-"+topicSuffix, broker.QueueOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := brk.Subscribe(q, topic); err != nil {
			t.Fatal(err)
		}
		sp := NewStatusPublisher(brk, StatusPublisherConfig{
			StatusTopic: topic,
			TaskID:      taskID,
			ContextID:   "ctx",
			AgentName:   "agent-multitask",
			SeqGen:      gen,
		}, slog.Default())
		return sp, q
	}

	pubA, qA := makePub("task-A", "tA")
	pubB, qB := makePub("task-B", "tB")

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			pubA.PublishText(t.Context(), "a")
		})
		wg.Go(func() {
			pubB.PublishText(t.Context(), "b")
		})
	}
	wg.Wait()

	for _, tc := range []struct {
		taskID string
		q      broker.Queue
	}{
		{"task-A", qA},
		{"task-B", qB},
	} {
		msgs := receiveAll(t, brk, tc.q, 5, 3*time.Second)
		if len(msgs) != 5 {
			t.Fatalf("%s: expected 5 msgs, got %d", tc.taskID, len(msgs))
		}
		seen := make(map[uint64]struct{}, 5)
		for _, msg := range msgs {
			seq, _ := strconv.ParseUint(msg.UserProperties[a2a.PropEventSeq], 10, 64)
			seen[seq] = struct{}{}
		}
		for n := uint64(1); n <= 5; n++ {
			if _, ok := seen[n]; !ok {
				t.Errorf("%s missing seq %d, saw %v", tc.taskID, n, seen)
			}
		}
	}
}
