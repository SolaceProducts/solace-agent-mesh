// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestRemoteWatches_AnnouncementLapses(t *testing.T) {
	now := time.Unix(1_000, 0)
	w := remoteWatches{now: func() time.Time { return now }, ttl: 30 * time.Second}

	if w.watched("t-1") {
		t.Fatal("a task never announced is watched")
	}
	w.touch("t-1")
	now = now.Add(29 * time.Second)
	if !w.watched("t-1") {
		t.Fatal("an announced task is not watched before its announcement lapses")
	}
	w.touch("t-1")
	now = now.Add(29 * time.Second)
	if !w.watched("t-1") {
		t.Fatal("a repeated announcement did not extend the watch")
	}
	now = now.Add(2 * time.Second)
	if w.watched("t-1") {
		t.Fatal("a task is still watched after its announcement lapsed")
	}

	w.touch("t-2")
	now = now.Add(31 * time.Second)
	w.prune()
	if w.watched("t-2") {
		t.Fatal("prune kept a lapsed announcement")
	}
}

func TestOnTaskStreamRecord_WatchAnnouncementMarksTheTaskWatched(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	forwarded := adapter.ForwardedActivity().(entrypoint.ActivityWatchers)
	if forwarded.WatchingTask(taskID) {
		t.Fatal("a task nobody watches is reported watched")
	}

	adapter.OnTaskStreamRecord(t.Context(), taskID, entrypoint.StreamRecord{EventType: entrypoint.ActivityWatchRecord})

	if !forwarded.WatchingTask(taskID) {
		t.Fatal("a task another instance announced is not reported watched")
	}
	if adapter.sseManager.HasTask(taskID) {
		t.Fatal("the announcement created an entry")
	}
}

func TestOnTaskStreamRecord_WatchAnnouncementForANonTaskIDIsIgnored(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	adapter.OnTaskStreamRecord(t.Context(), "not-a-task-id", entrypoint.StreamRecord{EventType: entrypoint.ActivityWatchRecord})

	if adapter.watchingTask("not-a-task-id") {
		t.Fatal("an announcement whose topic names no task id was recorded")
	}
}

func TestHTTPSSEAdapter_DoesNotGateItsOwnTasksOnWatchers(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	if _, ok := any(adapter).(entrypoint.ActivityWatchers); ok {
		t.Fatal("the web UI adapter implements ActivityWatchers, so its own tasks would stop streaming while unwatched")
	}
}

func createOpenTaskRow(t *testing.T, db *store.TaskRepo, taskID, owner string) {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := db.Create(t.Context(), &store.Task{ID: taskID, UserID: owner, StartTime: now, LastActivityTime: &now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
}

func publishedWatches(pub *recordingPublisher, taskID string) int {
	n := 0
	for _, p := range pub.published() {
		if p.taskID == taskID && p.rec.EventType == entrypoint.ActivityWatchRecord {
			n++
		}
	}
	return n
}

func TestSubscribeRoute_HeldStreamIsAnnounced(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	pub := &recordingPublisher{}
	adapter.streamPub = pub
	taskID := idgen.New()
	createOpenTaskRow(t, store.NewTaskRepo(core.Store()), taskID, "sam_dev_user")

	ts := httptest.NewServer(adapter.Handler())
	t.Cleanup(ts.Close)
	liveFeed(t.Context(), t, ts.URL+"/api/v1/sse/subscribe/"+taskID)

	waitFor(t, "the held stream was not announced", func() bool { return publishedWatches(pub, taskID) > 0 })
	if got := adapter.sseManager.HeldTasksWithSubscribers(); len(got) != 1 || got[0] != taskID {
		t.Fatalf("held tasks with subscribers = %v, want [%s]", got, taskID)
	}
}

func TestActivityWatchLoop_ReannouncesUntilStopped(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	pub := &recordingPublisher{}
	adapter.streamPub = pub
	adapter.activityWatchEvery = 20 * time.Millisecond
	taskID := idgen.New()
	_, connID, ok := adapter.sseManager.SubscribeHeld(taskID)
	if !ok {
		t.Fatal("held subscription refused")
	}
	t.Cleanup(func() { adapter.sseManager.Unsubscribe(taskID, connID) })

	adapter.startActivityWatch(context.Background())
	adapter.startActivityWatch(context.Background())
	waitFor(t, "the held stream was not announced again", func() bool { return publishedWatches(pub, taskID) >= 3 })
	adapter.stopActivityWatch()

	stopped := publishedWatches(pub, taskID)
	time.Sleep(100 * time.Millisecond)
	if got := publishedWatches(pub, taskID); got != stopped {
		t.Fatalf("announcements went on after stop: %d then %d", stopped, got)
	}
	adapter.stopActivityWatch()
}

func TestAdapterStartStop_RunsTheAnnounceLoop(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	pub := &recordingPublisher{}
	adapter.streamPub = pub
	adapter.activityWatchEvery = 20 * time.Millisecond
	taskID := idgen.New()
	_, connID, ok := adapter.sseManager.SubscribeHeld(taskID)
	if !ok {
		t.Fatal("held subscription refused")
	}
	t.Cleanup(func() { adapter.sseManager.Unsubscribe(taskID, connID) })

	startCtx, cancelStart := context.WithCancel(context.Background())
	if err := adapter.Start(startCtx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Start did not run the announce loop", func() bool { return publishedWatches(pub, taskID) >= 2 })
	if err := adapter.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancelStart()
	stopped := publishedWatches(pub, taskID)
	time.Sleep(10 * adapter.activityWatchEvery)
	if got := publishedWatches(pub, taskID); got != stopped {
		t.Fatalf("announcements went on after Stop: %d then %d", stopped, got)
	}

	restartCtx, cancelRestart := context.WithCancel(context.Background())
	t.Cleanup(cancelRestart)
	if err := adapter.Start(restartCtx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a restarted adapter did not announce again", func() bool { return publishedWatches(pub, taskID) >= stopped+2 })
	if err := adapter.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStreamRecordKinds_WireValues(t *testing.T) {
	if entrypoint.ActivityEventRecord != "activity_event" || entrypoint.ActivityWatchRecord != "activity_watch" {
		t.Fatalf("record kinds = %q, %q; want activity_event, activity_watch", entrypoint.ActivityEventRecord, entrypoint.ActivityWatchRecord)
	}
}

func TestTwoInstances_ForwardOnceTheViewersInstanceAnnounces(t *testing.T) {
	inst := newWebUIInstances(t, 2)
	receiver, viewer := inst[0], inst[1]
	ctx := t.Context()
	taskID := idgen.New()
	createOpenTaskRow(t, store.NewTaskRepo(viewer.core.Store()), taskID, "system")
	forwarded := receiver.ForwardedActivity()
	if forwarded.(entrypoint.ActivityWatchers).WatchingTask(taskID) {
		t.Fatal("the task is reported watched before anyone opened it")
	}

	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: viewer.sseManager, eventLog: viewer.eventLog, onHeld: viewer.announceActivityWatch})
	frames, _ := liveFeed(ctx, t, ts.URL+"/api/v1/sse/subscribe/"+taskID)
	waitFor(t, "the receiving instance never heard the viewer's announcement", func() bool {
		return forwarded.(entrypoint.ActivityWatchers).WatchingTask(taskID)
	})

	forwarded.OnActivityEvent(ctx, taskID, "test-ns/a2a/v1/gateway/status/event-mesh-gw/"+taskID, statusUpdateFixture(taskID, "SomeWorkflow"))
	if fr := nextFrame(t, frames, 3*time.Second); fr.eventType != "activity_event" {
		t.Fatalf("frame = %+v, want the forwarded activity frame", fr)
	}
}
