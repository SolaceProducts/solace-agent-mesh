// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestPersistent_ForgetReleasesInMemoryState(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()
	task := tid("forget-maps")

	appendStatus(t, el, task, 3)
	if err := el.Complete(ctx, task); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	el.Forget(task)

	el.mu.Lock()
	_, hasCompleted := el.completed[task]
	_, hasSequence := el.sequences[task]
	_, hasBuffered := el.buffered[task]
	el.mu.Unlock()
	if hasCompleted || hasSequence || hasBuffered {
		t.Fatalf("Forget left in-memory state: completed=%v sequences=%v buffered=%v",
			hasCompleted, hasSequence, hasBuffered)
	}

	ch, err := el.Subscribe(ctx, task, 1)
	if err != nil {
		t.Fatalf("Subscribe after Forget: %v", err)
	}
	if got := drainSubscribe(t, ch); !equalSeqs(got, 1, 2, 3) {
		t.Fatalf("after Forget replayed %v, want [1 2 3] then close", got)
	}
}

func TestPersistent_FindSequenceAfterTimestampAfterForgetFallsBackToDBMax(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()
	task := tid("fsat-forget")

	appendStatus(t, el, task, 3)
	if err := el.Complete(ctx, task); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	el.Forget(task)

	future := time.Now().Add(time.Hour).UnixMilli()
	seq, err := el.FindSequenceAfterTimestamp(ctx, task, future)
	if err != nil {
		t.Fatalf("FindSequenceAfterTimestamp: %v", err)
	}
	if seq != 4 {
		t.Fatalf("after Forget = %d, want 4 (dbMax+1, i.e. replay nothing)", seq)
	}
}

func TestPersistent_DurableCompletionSurvivesRestart(t *testing.T) {
	sdb := store.TestDB(t)
	repo := store.NewSSEEventRepo(sdb)
	taskRepo := store.NewTaskRepo(sdb)
	ctx := context.Background()
	task := tid("restart")

	before := NewPersistentEventLog(repo, taskRepo, slog.Default())
	before.pollInterval = 10 * time.Millisecond
	appendStatus(t, before, task, 3)
	if err := before.Complete(ctx, task); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	after := NewPersistentEventLog(repo, taskRepo, slog.Default())
	after.pollInterval = 10 * time.Millisecond

	ch, err := after.Subscribe(ctx, task, 1)
	if err != nil {
		t.Fatalf("Subscribe on fresh instance: %v", err)
	}
	if got := drainSubscribe(t, ch); !equalSeqs(got, 1, 2, 3) {
		t.Fatalf("fresh instance replayed %v, want [1 2 3] then close", got)
	}
}
