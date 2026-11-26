// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func createScheduledTask(t *testing.T, repo *ScheduledTaskRepo, slug, namespace string, edit func(*ScheduledTask)) *ScheduledTask {
	t.Helper()
	task := newTestScheduledTask(tid(slug), namespace, slug)
	if edit != nil {
		edit(task)
	}
	if err := repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	return task
}

func newRun(slug, taskID, status string, scheduledFor int64) *ScheduledTaskExecution {
	a2a := tid(slug + "-a2a")
	return &ScheduledTaskExecution{
		ID:              tid(slug),
		ScheduledTaskID: taskID,
		Status:          status,
		A2ATaskID:       &a2a,
		ScheduledFor:    scheduledFor,
	}
}

func createRun(t *testing.T, repo *ScheduledTaskRepo, e *ScheduledTaskExecution) {
	t.Helper()
	if err := repo.CreateExecution(t.Context(), e); err != nil {
		t.Fatal(err)
	}
}

func insertOlderVersionRun(t *testing.T, db *DB, id, taskID, status string, scheduledFor int64, startedAt *int64) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO scheduled_task_executions (id, scheduled_task_id, status, scheduled_for, started_at, retry_count, trigger_type)
		 VALUES (?, ?, ?, ?, ?, 0, 'scheduled')`,
		id, taskID, status, scheduledFor, startedAt); err != nil {
		t.Fatal(err)
	}
}

func mustGetRun(t *testing.T, repo *ScheduledTaskRepo, id string) *ScheduledTaskExecution {
	t.Helper()
	e, err := repo.GetExecutionByID(t.Context(), id)
	if err != nil || e == nil {
		t.Fatalf("get run %s: %v, %v", id, e, err)
	}
	return e
}

func mustGetTask(t *testing.T, repo *ScheduledTaskRepo, id string) *ScheduledTask {
	t.Helper()
	task, err := repo.GetByID(t.Context(), id)
	if err != nil || task == nil {
		t.Fatalf("get task %s: %v, %v", id, task, err)
	}
	return task
}

func countRuns(t *testing.T, repo *ScheduledTaskRepo, taskID string) int {
	t.Helper()
	n, err := repo.CountExecutionsByTask(t.Context(), taskID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func runIDs(runs []*ScheduledTaskExecution) []string {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func race(t *testing.T, n int, fn func() (bool, error)) int64 {
	t.Helper()
	var wins atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			<-start
			won, err := fn()
			if err != nil {
				t.Error(err)
				return
			}
			if won {
				wins.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	return wins.Load()
}

func TestScheduledTaskRepo_OlderVersionRunsGetZero(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "old-rows", "ns1", nil)
	insertOlderVersionRun(t, db, tid("old-run"), task.ID, "pending", 1000, nil)

	e := mustGetRun(t, repo, tid("old-run"))
	if e.SubmittedAt == nil || *e.SubmittedAt != 0 || e.FinalizedAt == nil || *e.FinalizedAt != 0 {
		t.Fatalf("older-version row: submitted_at=%v finalized_at=%v, want 0 and 0", e.SubmittedAt, e.FinalizedAt)
	}

	createRun(t, repo, newRun("new-run", task.ID, "pending", 1000))
	e = mustGetRun(t, repo, tid("new-run"))
	if e.SubmittedAt != nil || e.FinalizedAt != nil {
		t.Fatalf("new row: submitted_at=%v finalized_at=%v, want NULL and NULL", e.SubmittedAt, e.FinalizedAt)
	}
}

func TestScheduledTaskRepo_Migration64DownUp(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	if err := db.MigrateDownTo(ctx, 63); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "down-up", "ns1", nil)
	createRun(t, repo, newRun("down-up-run", task.ID, "pending", 1000))
}

func TestScheduledTaskRepo_FindDueSchedules(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	now := int64(1_000_000)
	at := func(ms int64) func(*ScheduledTask) {
		return func(st *ScheduledTask) { st.NextRunAt = &ms }
	}
	createScheduledTask(t, repo, "due-early", "ns1", at(now-5000))
	createScheduledTask(t, repo, "due-now", "ns1", at(now))
	createScheduledTask(t, repo, "due-later", "ns1", at(now+1))
	createScheduledTask(t, repo, "due-unarmed", "ns1", nil)
	createScheduledTask(t, repo, "due-other-ns", "ns2", at(now))
	createScheduledTask(t, repo, "due-disabled", "ns1", func(st *ScheduledTask) {
		at(now)(st)
		st.Enabled = false
	})
	deleted := createScheduledTask(t, repo, "due-deleted", "ns1", at(now))
	if err := repo.SoftDelete(t.Context(), deleted.ID, "user1"); err != nil {
		t.Fatal(err)
	}

	due, err := repo.FindDueSchedules(t.Context(), "ns1", now)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(due))
	for _, st := range due {
		got = append(got, st.ID)
	}
	assertIDs(t, got, []string{tid("due-early"), tid("due-now")})
}

func TestScheduledTaskRepo_SetFirstRun(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	once := createScheduledTask(t, repo, "first-once", "ns1", func(st *ScheduledTask) { st.ScheduleType = "one_time" })
	fired := int64(500)
	createScheduledTask(t, repo, "first-fired", "ns1", func(st *ScheduledTask) {
		st.ScheduleType = "one_time"
		st.LastRunAt = &fired
	})

	never, err := repo.FindNeverFiredSchedules(ctx, "ns1")
	if err != nil {
		t.Fatal(err)
	}
	if len(never) != 1 || never[0].ID != once.ID {
		t.Fatalf("never fired = %v, want only %s", never, once.ID)
	}

	if won := race(t, 10, func() (bool, error) { return repo.SetFirstRun(ctx, once.ID, 9000) }); won != 1 {
		t.Fatalf("SetFirstRun winners = %d, want 1", won)
	}
	if got := mustGetTask(t, repo, once.ID).NextRunAt; got == nil || *got != 9000 {
		t.Fatalf("next_run_at = %v, want 9000", got)
	}
	if won, err := repo.SetFirstRun(ctx, tid("first-fired"), 9000); err != nil || won {
		t.Fatalf("SetFirstRun on a fired schedule = %v, %v; want false", won, err)
	}
}

func TestScheduledTaskRepo_FireDue(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	due := int64(1_000_000)
	next := due + 60_000
	task := createScheduledTask(t, repo, "fire", "ns1", func(st *ScheduledTask) { st.NextRunAt = &due })

	_, won, err := repo.FireDue(ctx, task.ID, due, &next, due+10, newRun("fire-run-1", task.ID, "pending", due), nil)
	if err != nil || !won {
		t.Fatalf("first FireDue = %v, %v; want true", won, err)
	}
	got := mustGetTask(t, repo, task.ID)
	if got.NextRunAt == nil || *got.NextRunAt != next || got.LastRunAt == nil || *got.LastRunAt != due+10 {
		t.Fatalf("after fire next_run_at=%v last_run_at=%v", got.NextRunAt, got.LastRunAt)
	}
	run := mustGetRun(t, repo, tid("fire-run-1"))
	if run.Status != "pending" || run.SubmittedAt != nil || run.FinalizedAt != nil {
		t.Fatalf("fired run = %+v", run)
	}

	_, won, err = repo.FireDue(ctx, task.ID, due, &next, due+20, newRun("fire-run-2", task.ID, "pending", due), nil)
	if err != nil || won {
		t.Fatalf("FireDue with a moved tick = %v, %v; want false", won, err)
	}
	if n := countRuns(t, repo, task.ID); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}

	if _, won, err := repo.FireDue(ctx, task.ID, next, nil, next, nil, nil); err != nil || !won {
		t.Fatalf("FireDue without a run = %v, %v; want true", won, err)
	}
	if got := mustGetTask(t, repo, task.ID); got.NextRunAt != nil || got.LastRunAt == nil || *got.LastRunAt != due+10 {
		t.Fatalf("after a fire without a run next_run_at=%v last_run_at=%v, want NULL and unchanged", got.NextRunAt, got.LastRunAt)
	}
	if n := countRuns(t, repo, task.ID); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
}

func TestScheduledTaskRepo_FireDue_SkipsWhileARunIsActive(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	due := int64(1_000_000)
	next := due + 60_000
	task := createScheduledTask(t, repo, "fire-busy", "ns1", func(st *ScheduledTask) { st.NextRunAt = &due })
	createRun(t, repo, newRun("fire-busy-manual", task.ID, "pending", due-1))

	skipped := newRun("fire-busy-skip", task.ID, "skipped", due)
	inserted, won, err := repo.FireDue(ctx, task.ID, due, &next, due+10, newRun("fire-busy-run", task.ID, "pending", due), skipped)
	if err != nil || !won || inserted != skipped {
		t.Fatalf("FireDue with a run active = %v, %v, %v; want the skipped run inserted", inserted, won, err)
	}
	if got := mustGetTask(t, repo, task.ID); got.LastRunAt != nil || got.NextRunAt == nil || *got.NextRunAt != next {
		t.Fatalf("after a skipped fire last_run_at=%v next_run_at=%v, want NULL and moved", got.LastRunAt, got.NextRunAt)
	}
	if n := countRuns(t, repo, task.ID); n != 2 {
		t.Fatalf("runs = %d, want the manual run and the skipped one", n)
	}
}

func TestScheduledTaskRepo_FireDue_SkippedRunLeavesLastRun(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	due := int64(1_000_000)
	task := createScheduledTask(t, repo, "fire-late", "ns1", func(st *ScheduledTask) {
		st.ScheduleType = "one_time"
		st.NextRunAt = &due
	})
	if _, won, err := repo.FireDue(t.Context(), task.ID, due, nil, due+700_000, newRun("fire-late-skip", task.ID, "skipped", due), nil); err != nil || !won {
		t.Fatalf("FireDue of a late one-time schedule = %v, %v", won, err)
	}
	if got := mustGetTask(t, repo, task.ID); got.LastRunAt != nil || got.NextRunAt != nil {
		t.Fatalf("after the skip last_run_at=%v next_run_at=%v, want both NULL", got.LastRunAt, got.NextRunAt)
	}
	never, err := repo.FindNeverFiredSchedules(t.Context(), "ns1")
	if err != nil || len(never) != 0 {
		t.Fatalf("never fired = %v, %v; a one-time schedule with a skipped run must not be armed again", never, err)
	}
}

func TestScheduledTaskRepo_FireDue_DisabledLoses(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	due := int64(1_000_000)
	task := createScheduledTask(t, repo, "fire-off", "ns1", func(st *ScheduledTask) { st.NextRunAt = &due })
	if err := repo.SetEnabled(t.Context(), task.ID, false); err != nil {
		t.Fatal(err)
	}
	_, won, err := repo.FireDue(t.Context(), task.ID, due, nil, due, newRun("fire-off-run", task.ID, "pending", due), nil)
	if err != nil || won {
		t.Fatalf("FireDue on a disabled schedule = %v, %v; want false", won, err)
	}
	if n := countRuns(t, repo, task.ID); n != 0 {
		t.Fatalf("runs = %d, want 0", n)
	}
}

func TestScheduledTaskRepo_FireDue_ConcurrentOneRun(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	due := int64(1_000_000)
	next := due + 60_000
	task := createScheduledTask(t, repo, "fire-race", "ns1", func(st *ScheduledTask) { st.NextRunAt = &due })

	var caller atomic.Int64
	won := race(t, 2, func() (bool, error) {
		run := newRun("fire-race-"+string(rune('a'+caller.Add(1))), task.ID, "pending", due)
		_, won, err := repo.FireDue(ctx, task.ID, due, &next, due, run, nil)
		return won, err
	})
	if won != 1 {
		t.Fatalf("FireDue winners = %d, want 1", won)
	}
	if n := countRuns(t, repo, task.ID); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
}

func TestScheduledTaskRepo_ActiveExecutions(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	a := createScheduledTask(t, repo, "active-a", "ns1", nil)
	b := createScheduledTask(t, repo, "active-b", "ns1", nil)
	c := createScheduledTask(t, repo, "active-c", "ns2", nil)
	createRun(t, repo, newRun("active-a-1", a.ID, "pending", 1))
	createRun(t, repo, newRun("active-a-2", a.ID, "running", 1))
	createRun(t, repo, newRun("active-b-1", b.ID, "completed", 1))
	createRun(t, repo, newRun("active-c-1", c.ID, "running", 1))
	started := int64(1)
	insertOlderVersionRun(t, db, tid("active-b-old"), b.ID, "running", 1, &started)

	if busy, err := repo.HasActiveExecution(ctx, a.ID); err != nil || !busy {
		t.Fatalf("HasActiveExecution(a) = %v, %v; want true", busy, err)
	}
	if busy, err := repo.HasActiveExecution(ctx, b.ID); err != nil || busy {
		t.Fatalf("HasActiveExecution(b) = %v, %v; want false", busy, err)
	}
}

func TestScheduledTaskRepo_HasUnsentExecutionNoted(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	waiting := createScheduledTask(t, repo, "noted-waiting", "ns1", nil)
	other := createScheduledTask(t, repo, "noted-other", "ns1", nil)
	sent := createScheduledTask(t, repo, "noted-sent", "ns1", nil)
	plain := createScheduledTask(t, repo, "noted-plain", "ns1", nil)
	note, otherNote := "waiting on the identity", "waiting on the budget"
	pending := func(slug, taskID string, errorMessage *string) {
		run := newRun(slug, taskID, "pending", 1)
		run.ErrorMessage = errorMessage
		createRun(t, repo, run)
	}
	pending("noted-waiting-1", waiting.ID, &note)
	pending("noted-other-1", other.ID, &otherNote)
	pending("noted-plain-1", plain.ID, nil)
	submittedAt := int64(5)
	running := newRun("noted-sent-1", sent.ID, "running", 1)
	running.ErrorMessage, running.SubmittedAt = &note, &submittedAt
	createRun(t, repo, running)
	cases := []struct {
		name   string
		taskID string
		notes  []string
		want   bool
	}{
		{"a pending run carries the note", waiting.ID, []string{note}, true},
		{"a pending run carries one of the notes", other.ID, []string{note, otherNote}, true},
		{"a pending run carries another note", other.ID, []string{note}, false},
		{"a pending run carries no note", plain.ID, []string{note}, false},
		{"a run that was sent carries the note", sent.ID, []string{note}, false},
		{"no note is asked about", waiting.ID, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := repo.HasUnsentExecutionNoted(t.Context(), c.taskID, c.notes...)

			if err != nil {
				t.Fatalf("HasUnsentExecutionNoted(%s, %q): %v", c.taskID, c.notes, err)
			}
			if got != c.want {
				t.Errorf("HasUnsentExecutionNoted(%s, %q) = %v, want %v", c.taskID, c.notes, got, c.want)
			}
		})
	}
}

func TestScheduledTaskRepo_FindUnsubmittedExecutions(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	now := int64(1_000_000)
	task := createScheduledTask(t, repo, "unsub", "ns1", nil)
	other := createScheduledTask(t, repo, "unsub-other", "ns2", nil)
	createRun(t, repo, newRun("unsub-due", task.ID, "pending", now-1))
	createRun(t, repo, newRun("unsub-now", task.ID, "pending", now))
	createRun(t, repo, newRun("unsub-retry-later", task.ID, "pending", now+1))
	createRun(t, repo, newRun("unsub-other-ns", other.ID, "pending", now))
	claimed := newRun("unsub-claimed", task.ID, "pending", now)
	claimed.SubmittedAt = &now
	createRun(t, repo, claimed)
	insertOlderVersionRun(t, db, tid("unsub-old"), task.ID, "pending", now, nil)

	runs, err := repo.FindUnsubmittedExecutions(ctx, "ns1", now)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, runIDs(runs), []string{tid("unsub-due"), tid("unsub-now")})
}

func TestScheduledTaskRepo_MarkSubmitted_OneWinner(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	task := createScheduledTask(t, repo, "submit", "ns1", nil)
	createRun(t, repo, newRun("submit-run", task.ID, "pending", 1000))

	if won := race(t, 2, func() (bool, error) { return repo.MarkSubmitted(ctx, tid("submit-run"), 2000) }); won != 1 {
		t.Fatalf("MarkSubmitted winners = %d, want 1", won)
	}
	run := mustGetRun(t, repo, tid("submit-run"))
	if run.Status != "running" || run.SubmittedAt == nil || *run.SubmittedAt != 2000 || run.StartedAt == nil || *run.StartedAt != 2000 {
		t.Fatalf("claimed run = %+v", run)
	}
}

func TestScheduledTaskRepo_RunAsRoundTrips(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	runAs := "system:nightly"
	task := newTestScheduledTask(tid("runas-task"), "ns1", "runas")
	task.RunAs = &runAs
	if err := repo.Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetByID(ctx, task.ID)
	if err != nil || stored == nil || stored.RunAs == nil || *stored.RunAs != runAs {
		t.Fatalf("stored task = %+v, %v; want runAs %s", stored, err, runAs)
	}
	if err = repo.Update(ctx, task.ID, map[string]any{"run_as": nil}); err != nil {
		t.Fatal(err)
	}
	if cleared, _ := repo.GetByID(ctx, task.ID); cleared.RunAs != nil {
		t.Errorf("runAs = %q after clearing it", *cleared.RunAs)
	}

	pending := newRun("runas-run", task.ID, "pending", 1000)
	pending.RunAs = &runAs
	createRun(t, repo, pending)
	if run := mustGetRun(t, repo, tid("runas-run")); run.RunAs == nil || *run.RunAs != runAs {
		t.Errorf("pending run runAs = %v, want %s", run.RunAs, runAs)
	}
	won, err := repo.MarkSubmitted(ctx, tid("runas-run"), 2000)
	if err != nil || !won {
		t.Fatalf("MarkSubmitted = %v, %v", won, err)
	}
	if run := mustGetRun(t, repo, tid("runas-run")); run.RunAs == nil || *run.RunAs != runAs {
		t.Errorf("submitted run runAs = %v, want %s kept", run.RunAs, runAs)
	}
	runs, err := repo.FindRecentExecutions(ctx, "ns1", nil, 10)
	if err != nil || len(runs) != 1 || runs[0].RunAs == nil || *runs[0].RunAs != runAs {
		t.Errorf("recent runs = %+v, %v; want the run with runAs %s", runs, err, runAs)
	}
}

func TestScheduledTaskRepo_MarkSubmitted_OlderVersionRowLoses(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "submit-old", "ns1", nil)
	insertOlderVersionRun(t, db, tid("submit-old-run"), task.ID, "pending", 1000, nil)

	if won, err := repo.MarkSubmitted(t.Context(), tid("submit-old-run"), 2000); err != nil || won {
		t.Fatalf("MarkSubmitted on an older-version row = %v, %v; want false", won, err)
	}
}

func TestScheduledTaskRepo_FindOverdueExecutions(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	now := int64(10_000_000)
	hour := createScheduledTask(t, repo, "overdue-hour", "ns1", func(st *ScheduledTask) { st.TimeoutSeconds = 3600 })
	dflt := createScheduledTask(t, repo, "overdue-default", "ns1", func(st *ScheduledTask) { st.TimeoutSeconds = 0 })
	other := createScheduledTask(t, repo, "overdue-other", "ns2", func(st *ScheduledTask) { st.TimeoutSeconds = 1 })

	started := func(slug, taskID string, at int64) {
		run := newRun(slug, taskID, "running", at)
		run.StartedAt = &at
		createRun(t, repo, run)
	}
	started("overdue-running", hour.ID, now-3_600_001)
	started("overdue-running-in-time", hour.ID, now-3_600_000)
	createRun(t, repo, newRun("overdue-pending", hour.ID, "pending", now-3_600_001))
	createRun(t, repo, newRun("overdue-pending-in-time", hour.ID, "pending", now-1000))
	started("overdue-default-run", dflt.ID, now-60_001)
	started("overdue-other-ns", other.ID, 0)
	oldStart := now - 3_600_001
	insertOlderVersionRun(t, db, tid("overdue-old"), hour.ID, "running", oldStart, &oldStart)
	done := newRun("overdue-done", hour.ID, "completed", 0)
	createRun(t, repo, done)

	runs, err := repo.FindOverdueExecutions(ctx, "ns1", now, 60_000)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, id := range runIDs(runs) {
		got[id] = true
	}
	want := []string{tid("overdue-running"), tid("overdue-pending"), tid("overdue-default-run"), tid("overdue-old")}
	if len(got) != len(want) {
		t.Fatalf("overdue = %v, want %v", runIDs(runs), want)
	}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("overdue = %v, missing %s", runIDs(runs), id)
		}
	}
}

func TestScheduledTaskRepo_FindUnfinalizedExecutions(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "unfin", "ns1", nil)
	for i, status := range []string{"completed", "failed", "timeout", "cancelled", "skipped", "running", "pending"} {
		createRun(t, repo, newRun("unfin-"+status, task.ID, status, int64(i)))
	}
	finalized := newRun("unfin-finalized", task.ID, "completed", 10)
	stamp := int64(5)
	finalized.FinalizedAt = &stamp
	createRun(t, repo, finalized)
	insertOlderVersionRun(t, db, tid("unfin-old"), task.ID, "completed", 11, nil)

	runs, err := repo.FindUnfinalizedExecutions(t.Context(), "ns1")
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, runIDs(runs), []string{
		tid("unfin-completed"), tid("unfin-failed"), tid("unfin-timeout"), tid("unfin-cancelled"), tid("unfin-skipped"),
	})
}

func TestScheduledTaskRepo_FindUnfinalizedExecutions_RunsHeldForANoticeResendComeLast(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "unfin-held", "ns1", nil)
	createRun(t, repo, newRun("unfin-held-run", task.ID, "failed", 1))
	createRun(t, repo, newRun("unfin-first-unsent", task.ID, "failed", 2))
	createRun(t, repo, newRun("unfin-later", task.ID, "completed", 3))
	if _, err := db.ExecContext(t.Context(),
		`UPDATE scheduled_task_executions SET budget_stop = ?, notifications_sent = ? WHERE id = ?`,
		BudgetStopFirst, `[{"type":"webhook","status":"failed","sentAt":"2026-11-02T00:00:00Z"}]`, tid("unfin-held-run")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(),
		`UPDATE scheduled_task_executions SET budget_stop = ? WHERE id = ?`,
		BudgetStopFirst, tid("unfin-first-unsent")); err != nil {
		t.Fatal(err)
	}

	runs, err := repo.FindUnfinalizedExecutions(t.Context(), "ns1")
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, runIDs(runs), []string{tid("unfin-first-unsent"), tid("unfin-later"), tid("unfin-held-run")})
}

func failedEnd(runSlug, taskID string) RunEnd {
	msg := "agent error"
	return RunEnd{
		ExecutionID: tid(runSlug), TaskID: taskID, FromStatus: "running",
		End: ExecutionEnd{Status: "failed", CompletedAt: 5000, ErrorMessage: &msg},
	}
}

func TestScheduledTaskRepo_EndExecution_Completed(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	task := createScheduledTask(t, repo, "end-ok", "ns1", func(st *ScheduledTask) {
		st.RunCount = 4
		st.ConsecutiveFailureCount = 2
	})
	createRun(t, repo, newRun("end-ok-run", task.ID, "running", 1000))
	summary, artifacts := `{"text":"done"}`, `[]`

	res, err := repo.EndExecution(ctx, RunEnd{
		ExecutionID: tid("end-ok-run"), TaskID: task.ID, FromStatus: "running", AutoPauseAfter: 1,
		End: ExecutionEnd{Status: "completed", CompletedAt: 5000, ResultSummary: &summary, Artifacts: &artifacts},
	})
	if err != nil || !res.Ended || !res.Counted || res.Paused {
		t.Fatalf("EndExecution = %+v, %v; want ended and counted", res, err)
	}
	run := mustGetRun(t, repo, tid("end-ok-run"))
	if run.Status != "completed" || run.CompletedAt == nil || *run.CompletedAt != 5000 ||
		run.ResultSummary == nil || *run.ResultSummary != summary || run.ErrorMessage != nil || run.FinalizedAt != nil {
		t.Fatalf("ended run = %+v", run)
	}
	got := mustGetTask(t, repo, task.ID)
	if got.RunCount != 5 || got.ConsecutiveFailureCount != 0 || got.LastRunAt == nil || *got.LastRunAt != 5000 || !got.Enabled {
		t.Fatalf("task after success = %+v", got)
	}

	late := failedEnd("end-ok-run", task.ID)
	late.End.Status = "timeout"
	res, err = repo.EndExecution(ctx, late)
	if err != nil || res.Ended || res.Counted {
		t.Fatalf("late EndExecution = %+v, %v; want nothing written", res, err)
	}
	if got := mustGetTask(t, repo, task.ID); got.RunCount != 5 || got.ConsecutiveFailureCount != 0 {
		t.Fatalf("late end changed the counters: %+v", got)
	}
}

func TestScheduledTaskRepo_EndExecution_FailureWithRetry(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "end-retry", "ns1", func(st *ScheduledTask) { st.ConsecutiveFailureCount = 2 })
	createRun(t, repo, newRun("end-retry-run", task.ID, "running", 1000))
	end := failedEnd("end-retry-run", task.ID)
	end.AutoPauseAfter = 3
	end.Retry = newRun("end-retry-next", task.ID, "pending", 65_000)
	end.Retry.RetryCount = 1

	res, err := repo.EndExecution(t.Context(), end)
	if err != nil || !res.Ended || !res.Counted || res.Paused {
		t.Fatalf("EndExecution = %+v, %v; want ended, counted, not paused", res, err)
	}
	got := mustGetTask(t, repo, task.ID)
	if got.ConsecutiveFailureCount != 3 || got.RunCount != 0 || !got.Enabled {
		t.Fatalf("task after failure with retry = %+v", got)
	}
	next := mustGetRun(t, repo, tid("end-retry-next"))
	if next.Status != "pending" || next.RetryCount != 1 || next.ScheduledFor != 65_000 || next.SubmittedAt != nil || next.FinalizedAt != nil {
		t.Fatalf("retry row = %+v", next)
	}
}

func TestScheduledTaskRepo_EndExecution_AutoPause(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	next := int64(99_000)
	task := createScheduledTask(t, repo, "end-pause", "ns1", func(st *ScheduledTask) {
		st.ConsecutiveFailureCount = 2
		st.NextRunAt = &next
	})
	createRun(t, repo, newRun("end-pause-run", task.ID, "pending", 1000))
	end := failedEnd("end-pause-run", task.ID)
	end.FromStatus = "pending"
	end.End.Status = "timeout"
	end.AutoPauseAfter = 3

	res, err := repo.EndExecution(t.Context(), end)
	if err != nil || !res.Ended || !res.Paused || res.ConsecutiveFailures != 3 {
		t.Fatalf("EndExecution = %+v, %v; want ended, paused at 3", res, err)
	}
	got := mustGetTask(t, repo, task.ID)
	if got.Enabled || got.NextRunAt != nil || got.ConsecutiveFailureCount != 3 {
		t.Fatalf("task after auto-pause = %+v", got)
	}
}

func TestScheduledTaskRepo_EndExecution_NoPauseBelowThresholdOrWhenDisabled(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	below := createScheduledTask(t, repo, "end-below", "ns1", nil)
	createRun(t, repo, newRun("end-below-run", below.ID, "running", 1000))
	off := createScheduledTask(t, repo, "end-off", "ns1", func(st *ScheduledTask) {
		st.Enabled = false
		st.ConsecutiveFailureCount = 9
	})
	createRun(t, repo, newRun("end-off-run", off.ID, "running", 1000))

	for _, c := range []struct{ run, task string }{{"end-below-run", below.ID}, {"end-off-run", off.ID}} {
		end := failedEnd(c.run, c.task)
		end.AutoPauseAfter = 3
		res, err := repo.EndExecution(ctx, end)
		if err != nil || !res.Ended || res.Paused {
			t.Fatalf("%s: EndExecution = %+v, %v; want ended, not paused", c.run, res, err)
		}
	}
	if !mustGetTask(t, repo, below.ID).Enabled {
		t.Fatal("schedule below the threshold was paused")
	}
}

func TestScheduledTaskRepo_EndExecution_OlderVersionRunNotCounted(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "end-old", "ns1", nil)
	started := int64(1000)
	insertOlderVersionRun(t, db, tid("end-old-run"), task.ID, "running", started, &started)
	end := failedEnd("end-old-run", task.ID)
	end.AutoPauseAfter = 1
	end.Retry = newRun("end-old-retry", task.ID, "pending", 65_000)

	res, err := repo.EndExecution(t.Context(), end)
	if err != nil || !res.Ended || res.Counted || res.Paused {
		t.Fatalf("EndExecution = %+v, %v; want ended, not counted", res, err)
	}
	got := mustGetTask(t, repo, task.ID)
	if got.ConsecutiveFailureCount != 0 || !got.Enabled {
		t.Fatalf("older-version run changed the schedule: %+v", got)
	}
	if n := countRuns(t, repo, task.ID); n != 1 {
		t.Fatalf("runs = %d, want no retry row", n)
	}
}

func TestScheduledTaskRepo_EndExecution_Answered(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	task := createScheduledTask(t, repo, "end-answer", "ns1", nil)
	createRun(t, repo, newRun("end-answer-pending", task.ID, "pending", 1000))
	createRun(t, repo, newRun("end-answer-marked", task.ID, "pending", 1000))
	if _, err := repo.MarkSubmitted(ctx, tid("end-answer-marked"), 2000); err != nil {
		t.Fatal(err)
	}
	started := int64(1000)
	insertOlderVersionRun(t, db, tid("end-answer-old"), task.ID, "running", 1000, &started)

	for _, slug := range []string{"end-answer-pending", "end-answer-marked", "end-answer-old"} {
		res, err := repo.EndExecution(ctx, RunEnd{
			ExecutionID: tid(slug), TaskID: task.ID, FromStatus: "pending",
			End: ExecutionEnd{Status: "completed", CompletedAt: 5000, Answered: true},
		})
		if err != nil || !res.Ended {
			t.Fatalf("%s: EndExecution = %+v, %v; want ended", slug, res, err)
		}
	}
	want := map[string]int64{"end-answer-pending": 5000, "end-answer-marked": 2000, "end-answer-old": 0}
	for slug, sub := range want {
		run := mustGetRun(t, repo, tid(slug))
		if run.Status != "completed" || run.SubmittedAt == nil || *run.SubmittedAt != sub {
			t.Errorf("%s = {status %q, submitted_at %v}, want completed and %d", slug, run.Status, run.SubmittedAt, sub)
		}
	}
	if got := mustGetTask(t, repo, task.ID); got.RunCount != 2 {
		t.Fatalf("run_count = %d, want 2 (the older-version run is not counted)", got.RunCount)
	}

	res, err := repo.EndExecution(ctx, RunEnd{
		ExecutionID: tid("end-answer-marked"), TaskID: task.ID,
		End: ExecutionEnd{Status: "failed", CompletedAt: 6000, Answered: true},
	})
	if err != nil || res.Ended {
		t.Fatalf("answer to an ended run = %+v, %v; want nothing written", res, err)
	}
}

func TestScheduledTaskRepo_EndExecution_AnswerAndTimeoutRaceCountsOnce(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	task := createScheduledTask(t, repo, "end-race", "ns1", nil)
	createRun(t, repo, newRun("end-race-run", task.ID, "running", 1000))

	var caller atomic.Int64
	won := race(t, 2, func() (bool, error) {
		n := caller.Add(1)
		end := failedEnd("end-race-run", task.ID)
		if n == 2 {
			end.End.Status = "timeout"
		}
		end.Retry = newRun("end-race-retry-"+string(rune('a'+n)), task.ID, "pending", 65_000)
		res, err := repo.EndExecution(ctx, end)
		return res.Ended, err
	})
	if won != 1 {
		t.Fatalf("EndExecution winners = %d, want 1", won)
	}
	if got := mustGetTask(t, repo, task.ID).ConsecutiveFailureCount; got != 1 {
		t.Fatalf("consecutive_failure_count = %d, want 1", got)
	}
	if n := countRuns(t, repo, task.ID); n != 2 {
		t.Fatalf("runs = %d, want the run and one retry", n)
	}
}

func TestScheduledTaskRepo_ClaimFinalize(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	task := createScheduledTask(t, repo, "fin", "ns1", nil)
	createRun(t, repo, newRun("fin-done", task.ID, "completed", 1000))
	createRun(t, repo, newRun("fin-running", task.ID, "running", 1000))
	insertOlderVersionRun(t, db, tid("fin-old"), task.ID, "completed", 1000, nil)

	if won := race(t, 2, func() (bool, error) { return repo.ClaimFinalize(ctx, tid("fin-done"), 5000) }); won != 1 {
		t.Fatalf("ClaimFinalize winners = %d, want 1", won)
	}
	if run := mustGetRun(t, repo, tid("fin-done")); run.FinalizedAt == nil || *run.FinalizedAt != 5000 {
		t.Fatalf("finalized_at = %v, want 5000", run.FinalizedAt)
	}
	for _, slug := range []string{"fin-running", "fin-old"} {
		if won, err := repo.ClaimFinalize(ctx, tid(slug), 5000); err != nil || won {
			t.Fatalf("ClaimFinalize(%s) = %v, %v; want false", slug, won, err)
		}
	}
}

func TestScheduledTaskRepo_CreateExecutionIfIdle(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()
	task := createScheduledTask(t, repo, "idle", "ns1", nil)
	other := createScheduledTask(t, repo, "idle-other", "ns1", nil)

	var n atomic.Int64
	inserted := race(t, 4, func() (bool, error) {
		busy, err := repo.CreateExecutionIfIdle(ctx, newRun("idle-run-"+string(rune('a'+n.Add(1))), task.ID, "pending", 1000))
		return !busy, err
	})
	if inserted != 1 || countRuns(t, repo, task.ID) != 1 {
		t.Fatalf("inserted = %d, runs = %d; want one run", inserted, countRuns(t, repo, task.ID))
	}

	if busy, err := repo.CreateExecutionIfIdle(ctx, newRun("idle-other-run", other.ID, "pending", 1000)); err != nil || busy || countRuns(t, repo, other.ID) != 1 {
		t.Fatalf("another schedule while one runs = busy %v, err %v; want inserted", busy, err)
	}
}

func budgetEnd(runSlug, taskID string, completedAt int64) RunEnd {
	msg := "usage budget is used up"
	return RunEnd{
		ExecutionID: tid(runSlug), TaskID: taskID, FromStatus: "running", AutoPauseAfter: 1,
		End: ExecutionEnd{Status: "skipped", CompletedAt: completedAt, ErrorMessage: &msg, Cause: EndBudgetSpent},
	}
}

func endRun(t *testing.T, repo *ScheduledTaskRepo, slug string, end RunEnd) RunEndResult {
	t.Helper()
	createRun(t, repo, newRun(slug, end.TaskID, "running", 1000))
	res, err := repo.EndExecution(t.Context(), end)
	if err != nil || !res.Ended {
		t.Fatalf("EndExecution(%s) = %+v, %v; want ended", end.ExecutionID, res, err)
	}
	return res
}

func TestScheduledTaskRepo_EndExecution_BudgetStreak(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "streak", "ns1", nil)
	var stops []string
	for _, slug := range []string{"streak-1", "streak-2"} {
		res := endRun(t, repo, slug, budgetEnd(slug, task.ID, 5000))
		if res.Counted || res.Paused {
			t.Fatalf("budget end %s = %+v, want it neither counted nor pausing", slug, res)
		}
		stops = append(stops, *mustGetRun(t, repo, tid(slug)).BudgetStop)
	}
	answered := failedEnd("streak-answered", task.ID)
	answered.End.Answered = true
	endRun(t, repo, "streak-answered", answered)
	endRun(t, repo, "streak-3", budgetEnd("streak-3", task.ID, 7000))
	stops = append(stops, *mustGetRun(t, repo, tid("streak-3")).BudgetStop)

	if want := []string{BudgetStopFirst, BudgetStopRepeat, BudgetStopFirst}; strings.Join(stops, ",") != strings.Join(want, ",") {
		t.Fatalf("budget stops = %v, want %v: an answered run ends the streak", stops, want)
	}
	if run := mustGetRun(t, repo, tid("streak-answered")); run.BudgetStop != nil {
		t.Errorf("answered run budget stop = %q, want none", *run.BudgetStop)
	}
	if got := mustGetTask(t, repo, task.ID); got.ConsecutiveFailureCount != 1 || !got.Enabled {
		t.Errorf("task = failures %d, enabled %v; want only the answered failure counted", got.ConsecutiveFailureCount, got.Enabled)
	}
}

func TestScheduledTaskRepo_EndExecution_UnsentRunsKeepTheBudgetStreak(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "keep", "ns1", nil)
	endRun(t, repo, "keep-1", budgetEnd("keep-1", task.ID, 5000))
	endRun(t, repo, "keep-unsent", failedEnd("keep-unsent", task.ID))
	endRun(t, repo, "keep-2", budgetEnd("keep-2", task.ID, 6000))

	if stop := *mustGetRun(t, repo, tid("keep-2")).BudgetStop; stop != BudgetStopRepeat {
		t.Errorf("budget stop after an unanswered failure = %q, want %q", stop, BudgetStopRepeat)
	}
}

func TestScheduledTaskRepo_EndExecution_ASubmittedRunEndsTheBudgetStreak(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "admitted", "ns1", nil)
	endRun(t, repo, "admitted-1", budgetEnd("admitted-1", task.ID, 5000))
	createRun(t, repo, newRun("admitted-sent", task.ID, "pending", 1000))
	if won, err := repo.MarkSubmitted(t.Context(), tid("admitted-sent"), 5500); err != nil || !won {
		t.Fatalf("MarkSubmitted = %v, %v", won, err)
	}
	if res, err := repo.EndExecution(t.Context(), failedEnd("admitted-sent", task.ID)); err != nil || !res.Ended {
		t.Fatalf("EndExecution = %+v, %v", res, err)
	}
	endRun(t, repo, "admitted-2", budgetEnd("admitted-2", task.ID, 6000))

	if stop := *mustGetRun(t, repo, tid("admitted-2")).BudgetStop; stop != BudgetStopFirst {
		t.Errorf("budget stop after a submitted run failed unanswered = %q, want %q: the gate admitted it", stop, BudgetStopFirst)
	}

	createRun(t, repo, newRun("admitted-stopped", task.ID, "pending", 1000))
	if won, err := repo.MarkSubmitted(t.Context(), tid("admitted-stopped"), 6500); err != nil || !won {
		t.Fatalf("MarkSubmitted = %v, %v", won, err)
	}
	stopped := budgetEnd("admitted-stopped", task.ID, 7000)
	stopped.End.Status, stopped.End.Answered = "failed", true
	res, err := repo.EndExecution(t.Context(), stopped)
	if err != nil || !res.Ended || res.Counted {
		t.Fatalf("EndExecution = %+v, %v; want ended and not counted", res, err)
	}
	if stop := *mustGetRun(t, repo, tid("admitted-stopped")).BudgetStop; stop != BudgetStopFirst {
		t.Errorf("budget stop of a submitted run an agent stopped while a streak was open = %q, want %q", stop, BudgetStopFirst)
	}
	if at, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT budget_blocked_at FROM scheduled_tasks WHERE id = ?`, task.ID); err != nil || at != 7000 {
		t.Errorf("budget_blocked_at = %d, %v; want the new streak's start 7000", at, err)
	}
}

func TestScheduledTaskRepo_EndExecution_ConcurrentBudgetStopsHaveOneFirst(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "budget-race", "ns1", nil)
	const n = 4
	for i := range n {
		createRun(t, repo, newRun(fmt.Sprintf("budget-race-%d", i), task.ID, "running", 1000))
	}
	var next atomic.Int32
	firsts := race(t, n, func() (bool, error) {
		res, err := repo.EndExecution(t.Context(), budgetEnd(fmt.Sprintf("budget-race-%d", next.Add(1)-1), task.ID, 5000))
		return res.BudgetStop == BudgetStopFirst, err
	})
	if firsts != 1 {
		t.Errorf("first budget stops = %d, want 1", firsts)
	}
}

func TestScheduledTaskRepo_EndExecution_UncountedCausesLeaveTheTaskAlone(t *testing.T) {
	type outcome struct {
		Ended, Counted, Paused bool
		Failures, RunCount     int
		Enabled                bool
		RunStatus              string
		Runs                   int
	}
	for _, c := range []struct {
		name  string
		cause EndCause
	}{
		{"the run-as feature being turned off", EndRunAsDisabled},
		{"a platform that reports no service accounts", EndPlatformTooOld},
		{"an identity that could not be confirmed", EndIdentityUnconfirmed},
		{"a budget state that could not be confirmed", EndBudgetUnconfirmed},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := newTestDB(t)
			repo := NewScheduledTaskRepo(db)
			task := createScheduledTask(t, repo, "uncounted", "ns1", func(st *ScheduledTask) { st.ConsecutiveFailureCount = 2 })
			createRun(t, repo, newRun("uncounted-run", task.ID, "pending", 1000))
			msg := "run-as is turned off"
			end := RunEnd{
				ExecutionID: tid("uncounted-run"), TaskID: task.ID, FromStatus: "pending", AutoPauseAfter: 3,
				End:   ExecutionEnd{Status: "failed", CompletedAt: 5000, ErrorMessage: &msg, Cause: c.cause},
				Retry: newRun("uncounted-retry", task.ID, "pending", 65_000),
			}

			res, err := repo.EndExecution(t.Context(), end)

			if err != nil {
				t.Fatalf("EndExecution: %v", err)
			}
			got := mustGetTask(t, repo, task.ID)
			want := outcome{Ended: true, Failures: 2, Enabled: true, RunStatus: "failed", Runs: 1}
			gotOutcome := outcome{res.Ended, res.Counted, res.Paused, got.ConsecutiveFailureCount, got.RunCount, got.Enabled,
				mustGetRun(t, repo, tid("uncounted-run")).Status, countRuns(t, repo, task.ID)}
			if diff := cmp.Diff(want, gotOutcome); diff != "" {
				t.Errorf("outcome (-want +got):\n%s", diff)
			}
			if got.LastRunAt != nil {
				t.Errorf("last run at = %d, want it untouched", *got.LastRunAt)
			}
		})
	}
}

func TestScheduledTaskRepo_Migration67DownUp(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	if err := db.MigrateDownTo(ctx, 66); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"scheduled_tasks.budget_blocked_at", "scheduled_task_executions.budget_stop"} {
		table, col, _ := strings.Cut(column, ".")
		if _, err := db.ExecContext(ctx, `SELECT `+col+` FROM `+table); err == nil {
			t.Fatalf("%s survived the down migration", column)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "down-up-67", "ns1", nil)
	endRun(t, repo, "down-up-67-run", budgetEnd("down-up-67-run", task.ID, 5000))
}
