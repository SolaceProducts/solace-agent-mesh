// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type fakeTaskRows struct {
	row   *store.Task
	err   error
	reads int
}

func (f *fakeTaskRows) GetByID(context.Context, string) (*store.Task, error) {
	f.reads++
	return f.row, f.err
}

func TestTaskRunningCheck(t *testing.T) {
	ended := int64(1)
	parentID := "root-task"
	cases := []struct {
		name                 string
		running, known       bool
		rows                 *fakeTaskRows
		wantRunning, wantKnw bool
		wantReads            int
	}{
		{name: "tracked here and running", running: true, known: true, rows: &fakeTaskRows{}, wantRunning: true, wantKnw: true},
		{name: "state unreadable here", rows: &fakeTaskRows{}, wantRunning: false, wantKnw: false},
		{name: "ended here, no task logger row", known: true, rows: &fakeTaskRows{}, wantKnw: true, wantReads: 1},
		{name: "not tracked here, task logger row open", known: true, rows: &fakeTaskRows{row: &store.Task{}}, wantRunning: true, wantKnw: true, wantReads: 1},
		{name: "not tracked here, task logger row ended", known: true, rows: &fakeTaskRows{row: &store.Task{EndTime: &ended}}, wantKnw: true, wantReads: 1},
		{name: "not tracked here, open row of a sub-task", known: true, rows: &fakeTaskRows{row: &store.Task{ParentTaskID: &parentID}}, wantKnw: true, wantReads: 1},
		{name: "task logger row unreadable", known: true, rows: &fakeTaskRows{err: errors.New("db down")}, wantReads: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := taskRunningCheck(func(string) (bool, bool) { return tc.running, tc.known }, tc.rows, slog.New(slog.DiscardHandler))
			running, known := check("task-1")
			if running != tc.wantRunning || known != tc.wantKnw {
				t.Fatalf("running, known = %v, %v; want %v, %v", running, known, tc.wantRunning, tc.wantKnw)
			}
			if tc.rows.reads != tc.wantReads {
				t.Fatalf("task logger reads = %d, want %d", tc.rows.reads, tc.wantReads)
			}
		})
	}
}

func TestTaskRunningCheck_LogsAnUnreadableRowOnce(t *testing.T) {
	var logs bytes.Buffer
	check := taskRunningCheck(func(string) (bool, bool) { return false, true }, &fakeTaskRows{err: errors.New("db down")}, slog.New(slog.NewTextHandler(&logs, nil)))

	for range 3 {
		check("task-1")
	}
	if got := strings.Count(logs.String(), "task logger row read failed"); got != 1 {
		t.Fatalf("logged the failure %d times over 3 reads, want 1:\n%s", got, logs.String())
	}

	for range 97 {
		check("task-1")
	}
	if got := strings.Count(logs.String(), "task logger row read failed"); got != 2 {
		t.Fatalf("logged the failure %d times over 100 reads, want 2 (the first and the 100th)", got)
	}
}

func TestTaskRunningCheck_LogsALaterOutageAfterARecovery(t *testing.T) {
	var logs bytes.Buffer
	rows := &fakeTaskRows{err: errors.New("db down")}
	check := taskRunningCheck(func(string) (bool, bool) { return false, true }, rows, slog.New(slog.NewTextHandler(&logs, nil)))

	check("task-1")
	rows.err = nil
	check("task-1")
	rows.err = errors.New("db down again")
	check("task-1")

	if got := strings.Count(logs.String(), "task logger row read failed"); got != 2 {
		t.Fatalf("logged %d failures across two outages, want 2:\n%s", got, logs.String())
	}
}

func TestSubscribe_TaskAnotherEntrypointStarted_StreamsActivityUntilItEnds(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	ctx := t.Context()
	taskID := idgen.New()
	tasks := store.NewTaskRepo(core.Store())
	now := time.Now().UnixMilli()
	if err := tasks.Create(ctx, &store.Task{ID: taskID, UserID: "system", StartTime: now, LastActivityTime: &now, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog})
	frames, ended := liveFeed(ctx, t, ts.URL+"/api/v1/sse/subscribe/"+taskID)
	waitFor(t, "the stream did not hold an entry for the task", func() bool { return adapter.sseManager.HasTask(taskID) })

	adapter.OnActivityEvent(ctx, taskID, "test-ns/a2a/v1/gateway/status/event-mesh-gw/"+taskID, statusUpdateFixture(taskID, "SomeWorkflow"))
	if fr := nextFrame(t, frames, 3*time.Second); fr.eventType != "activity_event" {
		t.Fatalf("frame = %+v, want the forwarded activity frame", fr)
	}

	if err := tasks.UpdateEndTime(ctx, taskID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end once the task logger recorded the task's end")
	}
}

func TestSubscribe_TaskUnknownEverywhere_NotFound(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/v1/sse/subscribe/"+idgen.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
