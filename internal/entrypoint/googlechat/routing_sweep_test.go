// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func trackIdleTask(a *Adapter, taskID string) *taskState {
	st := &taskState{meta: dmMeta()}
	a.trackTask(taskID, st)
	st.touch(time.Now().Add(-2 * taskIdleRelease))
	return st
}

func isTracked(a *Adapter, taskID string) bool {
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	_, ok := a.tasks[taskID]
	return ok
}

func TestStart_RunsTheIdleTaskSweep(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	a.sub = &fakeSub{}
	a.cfg.PubSubSubscription = "projects/p/subscriptions/s"
	a.sweepInterval = 10 * time.Millisecond
	trackIdleTask(a, tid("task-idle"))

	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for isTracked(a, tid("task-idle")) {
		if time.Now().After(deadline) {
			t.Fatal("the idle task was never released: the sweep is not running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTrackTask_FreshTaskSurvivesTheNextSweep(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	a.trackTask(tid("task-1"), &taskState{meta: dmMeta()})

	a.releaseIdleTasks(time.Now())

	if !isTracked(a, tid("task-1")) {
		t.Error("a task tracked a moment ago was released as idle")
	}
}

func TestStatusEventKeepsATaskFromTheIdleSweep(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	trackIdleTask(a, tid("task-1"))

	a.OnStatusUpdate(t.Context(), tid("task-1"), progressEvent("Looking it up"), nil)
	a.releaseIdleTasks(time.Now())

	if !isTracked(a, tid("task-1")) {
		t.Error("a task that just had an event was released as idle")
	}
}

func TestAdoptStatus_StopsWaitingWhenTheQueueCloses(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)
	if !replicaA.recordTask(t.Context(), tid("task-r"), requesterEmail, &taskState{meta: dmMeta()}) {
		t.Fatal("recordTask did not record the task")
	}
	if err := replicaB.LoadTask(t.Context(), tid("task-r")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	replicaB.OnStatusUpdate(t.Context(), tid("task-r"), progressEvent("Looking it up"), nil)
	time.Sleep(2 * statusAdoptPoll)

	start := time.Now()
	replicaB.forgetTask(tid("task-r"))
	if elapsed := time.Since(start); elapsed > statusAdoptWait/2 {
		t.Errorf("closing the queue took %v while it waited to adopt a status message, want it to stop at once", elapsed)
	}
}

func TestAdoptStatus_LogsALookupFailureOnce(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	logs := recordLogs(a)
	a.items = &flakyItems{taskItemStore: a.items, listFailures: 1000}

	ctx, cancel := context.WithTimeout(t.Context(), 3*statusAdoptPoll)
	defer cancel()
	if _, ok := a.adoptStatus(tid("task-1"))(ctx); ok {
		t.Fatal("adopted a status message the task items could not have returned")
	}

	if n := logs.count(slog.LevelWarn, "google chat status message lookup failed"); n != 1 {
		t.Errorf("lookup-failure warnings = %d, want exactly 1 across the retries", n)
	}
}
