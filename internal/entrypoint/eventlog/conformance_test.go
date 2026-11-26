// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
	"testing"
	"time"
)

func drainSubscribe(t *testing.T, ch <-chan Event) []int64 {
	t.Helper()
	var got []int64
	timeout := time.After(3 * time.Second)
	for {
		select {
		case evt, open := <-ch:
			if !open {
				return got
			}
			got = append(got, evt.Sequence)
		case <-timeout:
			t.Fatalf("Subscribe channel did not close; got %v", got)
		}
	}
}

type eventLogBackend struct {
	name string
	make func(t *testing.T) TaskEventLog
}

func conformanceBackends() []eventLogBackend {
	return []eventLogBackend{
		{"memory", func(t *testing.T) TaskEventLog { return NewMemoryEventLog() }},
		{"persistent", func(t *testing.T) TaskEventLog { return newTestPersistentEventLog(t) }},
		{"persistent-async", func(t *testing.T) TaskEventLog { return newTestPersistentEventLogAsync(t) }},
	}
}

func appendN(t *testing.T, el TaskEventLog, taskID string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		if _, err := el.Append(ctx, taskID, tid("sess"), "", "status_update", []byte(`{}`)); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
}

func replaySeqs(t *testing.T, el TaskEventLog, taskID string, fromSeq int64) []int64 {
	t.Helper()
	events, err := el.Replay(context.Background(), taskID, fromSeq)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	seqs := make([]int64, len(events))
	for i, e := range events {
		seqs[i] = e.Sequence
	}
	return seqs
}

func equalSeqs(a []int64, b ...int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestConformance_Replay_FullSnapshot(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			taskID := tid("replay-full-" + b.name)
			appendN(t, el, taskID, 3)

			if got := replaySeqs(t, el, taskID, 0); !equalSeqs(got, 1, 2, 3) {
				t.Errorf("Replay(0) = %v, want [1 2 3]", got)
			}
			if got := replaySeqs(t, el, taskID, 1); !equalSeqs(got, 1, 2, 3) {
				t.Errorf("Replay(1) = %v, want [1 2 3]", got)
			}
		})
	}
}

func TestConformance_Replay_FromMiddle(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			taskID := tid("replay-mid-" + b.name)
			appendN(t, el, taskID, 5)

			if got := replaySeqs(t, el, taskID, 3); !equalSeqs(got, 3, 4, 5) {
				t.Errorf("Replay(3) = %v, want [3 4 5]", got)
			}
		})
	}
}

func TestConformance_Replay_PastEnd_Empty(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			taskID := tid("replay-past-" + b.name)
			appendN(t, el, taskID, 3)

			if got := replaySeqs(t, el, taskID, 10); len(got) != 0 {
				t.Errorf("Replay(10) past end = %v, want empty", got)
			}
		})
	}
}

func TestConformance_Replay_UnknownTaskEmptyNoError(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			events, err := el.Replay(context.Background(), tid("never-existed-"+b.name), 1)
			if err != nil {
				t.Fatalf("Replay(unknown) error = %v, want nil", err)
			}
			if len(events) != 0 {
				t.Errorf("Replay(unknown) = %v, want empty", events)
			}
		})
	}
}

func TestConformance_Replay_DoesNotTail(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			taskID := tid("replay-notail-" + b.name)
			appendN(t, el, taskID, 2)

			done := make(chan []int64, 1)
			go func() { done <- replaySeqs(t, el, taskID, 1) }()
			select {
			case got := <-done:
				if !equalSeqs(got, 1, 2) {
					t.Errorf("Replay = %v, want [1 2]", got)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Replay blocked — it must not tail an in-progress task")
			}
		})
	}
}

func TestConformance_Subscribe_ReplaysThenCloses(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			ctx := context.Background()
			taskID := tid("sub-close-" + b.name)
			appendN(t, el, taskID, 3)
			if err := el.Complete(ctx, taskID); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			ch, err := el.Subscribe(ctx, taskID, 1)
			if err != nil {
				t.Fatalf("Subscribe: %v", err)
			}

			var got []int64
			timeout := time.After(3 * time.Second)
			for {
				select {
				case evt, open := <-ch:
					if !open {
						if !equalSeqs(got, 1, 2, 3) {
							t.Fatalf("replayed %v, want [1 2 3] then close", got)
						}
						return
					}
					got = append(got, evt.Sequence)
				case <-timeout:
					t.Fatalf("channel did not close after replaying completed task; got %v", got)
				}
			}
		})
	}
}

func TestConformance_ForgetKeepsReplayAndClose(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			ctx := context.Background()
			taskID := tid("forget-" + b.name)
			appendN(t, el, taskID, 3)
			if err := el.Complete(ctx, taskID); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			el.Forget(taskID)

			ch, err := el.Subscribe(ctx, taskID, 1)
			if err != nil {
				t.Fatalf("Subscribe after Forget: %v", err)
			}
			if got := drainSubscribe(t, ch); !equalSeqs(got, 1, 2, 3) {
				t.Fatalf("after Forget replayed %v, want [1 2 3] then close", got)
			}

			if got := replaySeqs(t, el, taskID, 1); !equalSeqs(got, 1, 2, 3) {
				t.Fatalf("Replay after Forget = %v, want [1 2 3]", got)
			}
		})
	}
}

func TestConformance_ForgetThenReconnectFromMidSequence(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			ctx := context.Background()
			taskID := tid("forget-mid-" + b.name)
			appendN(t, el, taskID, 5)
			if err := el.Complete(ctx, taskID); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			el.Forget(taskID)

			ch, err := el.Subscribe(ctx, taskID, 3)
			if err != nil {
				t.Fatalf("Subscribe(fromSeq=3) after Forget: %v", err)
			}
			if got := drainSubscribe(t, ch); !equalSeqs(got, 3, 4, 5) {
				t.Fatalf("after Forget replayed %v, want [3 4 5] then close", got)
			}
		})
	}
}

func TestConformance_Subscribe_UnknownTask_ErrNotFound(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			_, err := el.Subscribe(context.Background(), tid("never-existed-sub-"+b.name), 1)
			if err == nil {
				t.Fatal("Subscribe(unknown) error = nil, want ErrTaskNotFound")
			}
			if !errors.Is(err, ErrTaskNotFound) {
				t.Errorf("Subscribe(unknown) error = %v, want errors.Is(ErrTaskNotFound)", err)
			}
		})
	}
}

func TestConformance_Subscribe_FromSeqReplaysThenCloses(t *testing.T) {
	for _, b := range conformanceBackends() {
		t.Run(b.name, func(t *testing.T) {
			el := b.make(t)
			ctx := context.Background()
			taskID := tid("sub-fromseq-" + b.name)
			appendN(t, el, taskID, 5)
			if err := el.Complete(ctx, taskID); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			ch, err := el.Subscribe(ctx, taskID, 3)
			if err != nil {
				t.Fatalf("Subscribe: %v", err)
			}
			if got := drainSubscribe(t, ch); !equalSeqs(got, 3, 4, 5) {
				t.Fatalf("Subscribe(fromSeq=3) replayed %v, want [3 4 5] then close", got)
			}
		})
	}
}
