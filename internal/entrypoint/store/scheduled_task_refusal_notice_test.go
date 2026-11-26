// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	deliveredNotices = `[{"type":"inbox","status":"failed","error":"broker down","sentAt":"2026-11-24T10:00:00.000Z"},{"type":"webhook","status":"sent","sentAt":"2026-11-24T10:00:00.000Z"}]`
	failedNotices    = `[{"type":"inbox","status":"failed","error":"broker down","sentAt":"2026-11-24T10:00:00.000Z"}]`
	endingRunN       = 50
	endingRunAt      = 10_000
)

func refusalRunID(n int) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", n)
}

type earlierRun struct {
	n            int
	status       string
	scheduledFor int64
	trigger      string
	message      string
	refusal      string
	notice       string
	sent         string
	otherTask    bool
}

func deliveredHead(n int, at int64) earlierRun {
	return earlierRun{n: n, status: "failed", scheduledFor: at, refusal: RefusalRunAsDisabled, notice: RefusalNoticeFirst, sent: deliveredNotices}
}

func plainRun(n int, status string, at int64) earlierRun {
	return earlierRun{n: n, status: status, scheduledFor: at}
}

func busySkip(n int, at int64, note string) earlierRun {
	return earlierRun{n: n, status: "skipped", scheduledFor: at, message: note}
}

func TestScheduledTaskRepo_EndExecution_RecordsWhetherARefusalStartsOrContinuesAStreak(t *testing.T) {
	manualRefusal := func(n int, at int64, sent string) earlierRun {
		return earlierRun{n: n, status: "failed", scheduledFor: at, trigger: TriggerTypeManual, refusal: RefusalRunAsDisabled, notice: RefusalNoticeFirst, sent: sent}
	}
	cases := []struct {
		name       string
		earlier    []earlierRun
		trigger    string
		cause      EndCause
		wantCause  string
		wantNotice string
	}{
		{name: "no earlier run", wantNotice: RefusalNoticeFirst},
		{name: "an earlier refusal whose notice was delivered", earlier: []earlierRun{deliveredHead(1, 5000)}, wantNotice: RefusalNoticeRepeat},
		{
			name:       "an earlier refusal whose notice failed on every channel",
			earlier:    []earlierRun{{n: 1, status: "failed", scheduledFor: 5000, refusal: RefusalRunAsDisabled, notice: RefusalNoticeFirst, sent: failedNotices}},
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "an earlier refusal with no notice recorded",
			earlier:    []earlierRun{{n: 1, status: "failed", scheduledFor: 5000, refusal: RefusalRunAsDisabled, notice: RefusalNoticeFirst}},
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "an earlier refusal whose notices cannot be read",
			earlier:    []earlierRun{{n: 1, status: "failed", scheduledFor: 5000, refusal: RefusalRunAsDisabled, notice: RefusalNoticeFirst, sent: "not json"}},
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "an earlier repeat of a delivered refusal",
			earlier:    []earlierRun{deliveredHead(1, 4000), {n: 2, status: "failed", scheduledFor: 5000, refusal: RefusalRunAsDisabled, notice: RefusalNoticeRepeat}},
			wantNotice: RefusalNoticeRepeat,
		},
		{
			name:       "an earlier refusal of the other cause",
			earlier:    []earlierRun{{n: 1, status: "failed", scheduledFor: 5000, refusal: RefusalPlatformTooOld, notice: RefusalNoticeFirst, sent: deliveredNotices}},
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "the platform refusal continues a delivered platform refusal",
			earlier:    []earlierRun{{n: 1, status: "failed", scheduledFor: 5000, refusal: RefusalPlatformTooOld, notice: RefusalNoticeFirst, sent: deliveredNotices}},
			cause:      EndPlatformTooOld,
			wantCause:  RefusalPlatformTooOld,
			wantNotice: RefusalNoticeRepeat,
		},
		{
			name:       "a fire skipped because an earlier run was still running",
			earlier:    []earlierRun{deliveredHead(1, 5000), busySkip(2, 6000, SkippedRunningNote), busySkip(3, 7000, SkippedRunningNote)},
			wantNotice: RefusalNoticeRepeat,
		},
		{
			name:       "a fire skipped because an earlier run was waiting on its identity",
			earlier:    []earlierRun{deliveredHead(1, 5000), busySkip(2, 6000, SkippedIdentityWaitNote)},
			wantNotice: RefusalNoticeRepeat,
		},
		{
			name:       "a fire skipped for another reason",
			earlier:    []earlierRun{deliveredHead(1, 5000), {n: 2, status: "skipped", scheduledFor: 6000, message: "scheduled task is paused"}},
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "a fire skipped without a message",
			earlier:    []earlierRun{deliveredHead(1, 5000), plainRun(2, "skipped", 6000)},
			wantNotice: RefusalNoticeFirst,
		},
		{name: "a completed run between", earlier: []earlierRun{deliveredHead(1, 5000), plainRun(2, "completed", 6000)}, wantNotice: RefusalNoticeFirst},
		{name: "a failed run between", earlier: []earlierRun{deliveredHead(1, 5000), plainRun(2, "failed", 6000)}, wantNotice: RefusalNoticeFirst},
		{name: "a run that timed out between", earlier: []earlierRun{deliveredHead(1, 5000), plainRun(2, "timeout", 6000)}, wantNotice: RefusalNoticeFirst},
		{name: "a cancelled run between", earlier: []earlierRun{deliveredHead(1, 5000), plainRun(2, "cancelled", 6000)}, wantNotice: RefusalNoticeFirst},
		{name: "a pending run before it", earlier: []earlierRun{deliveredHead(1, 5000), plainRun(2, "pending", 6000)}, wantNotice: RefusalNoticeRepeat},
		{name: "a running run before it", earlier: []earlierRun{deliveredHead(1, 5000), plainRun(2, "running", 6000)}, wantNotice: RefusalNoticeRepeat},
		{
			name:       "a manual refusal between whose notice was not delivered",
			earlier:    []earlierRun{deliveredHead(1, 5000), manualRefusal(2, 6000, "")},
			wantNotice: RefusalNoticeRepeat,
		},
		{
			name:       "a manual refusal whose notice was delivered as the only earlier run",
			earlier:    []earlierRun{manualRefusal(1, 5000, deliveredNotices)},
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "a manual run that completed between",
			earlier:    []earlierRun{deliveredHead(1, 5000), {n: 2, status: "completed", scheduledFor: 6000, trigger: TriggerTypeManual}},
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "the refused run is manual",
			earlier:    []earlierRun{deliveredHead(1, 5000)},
			trigger:    TriggerTypeManual,
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "another schedule's delivered refusal",
			earlier:    []earlierRun{{n: 1, status: "failed", scheduledFor: 5000, refusal: RefusalRunAsDisabled, notice: RefusalNoticeFirst, sent: deliveredNotices, otherTask: true}},
			wantNotice: RefusalNoticeFirst,
		},
		{name: "a refusal scheduled after it", earlier: []earlierRun{deliveredHead(1, 15_000)}, wantNotice: RefusalNoticeFirst},
		{
			name:       "the earlier run by schedule has the larger id",
			earlier:    []earlierRun{deliveredHead(99, 5000), plainRun(1, "completed", 6000)},
			wantNotice: RefusalNoticeFirst,
		},
		{
			name:       "the nearest earlier run by schedule is the delivered refusal with the larger id",
			earlier:    []earlierRun{plainRun(99, "completed", 5000), deliveredHead(1, 6000)},
			wantNotice: RefusalNoticeRepeat,
		},
		{
			name:       "two runs at the same instant before it",
			earlier:    []earlierRun{plainRun(10, "completed", endingRunAt), deliveredHead(20, endingRunAt)},
			wantNotice: RefusalNoticeRepeat,
		},
		{
			name:       "a delivered refusal at the same instant with a larger id",
			earlier:    []earlierRun{deliveredHead(endingRunN+1, endingRunAt)},
			wantNotice: RefusalNoticeFirst,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewScheduledTaskRepo(newTestDB(t))
			task := createScheduledTask(t, repo, "refusal-notice", "ns1", nil)
			other := createScheduledTask(t, repo, "refusal-notice-other", "ns1", nil)
			for _, e := range tc.earlier {
				seedEarlierRun(t, repo, task, other, e)
			}
			trigger := tc.trigger
			if trigger == "" {
				trigger = TriggerTypeScheduled
			}
			ending := &ScheduledTaskExecution{ID: refusalRunID(endingRunN), ScheduledTaskID: task.ID, Status: "pending", ScheduledFor: endingRunAt, TriggerType: trigger}
			createRun(t, repo, ending)
			cause := tc.cause
			if cause == EndByRun {
				cause = EndRunAsDisabled
			}
			wantCause := tc.wantCause
			if wantCause == "" {
				wantCause = RefusalRunAsDisabled
			}
			msg := "refused"

			res, err := repo.EndExecution(t.Context(), RunEnd{
				ExecutionID: ending.ID, TaskID: task.ID, FromStatus: "pending", AutoPauseAfter: 1,
				End: ExecutionEnd{Status: "failed", CompletedAt: 20_000, ErrorMessage: &msg, Cause: cause},
			})

			if err != nil || !res.Ended || res.Counted || res.Paused {
				t.Fatalf("EndExecution = %+v, %v; want it ended and uncounted", res, err)
			}
			got := mustGetRun(t, repo, ending.ID)
			type marker struct{ Cause, Notice string }
			want := marker{wantCause, tc.wantNotice}
			if got.RefusalCause == nil || got.RefusalNotice == nil {
				t.Fatalf("refusal marker = %v, %v, want %+v", got.RefusalCause, got.RefusalNotice, want)
			}
			if diff := cmp.Diff(want, marker{*got.RefusalCause, *got.RefusalNotice}); diff != "" {
				t.Errorf("refusal marker (-want +got):\n%s", diff)
			}
		})
	}
}

func seedEarlierRun(t *testing.T, repo *ScheduledTaskRepo, task, other *ScheduledTask, e earlierRun) {
	t.Helper()
	owner := task
	if e.otherTask {
		owner = other
	}
	trigger := e.trigger
	if trigger == "" {
		trigger = TriggerTypeScheduled
	}
	run := &ScheduledTaskExecution{ID: refusalRunID(e.n), ScheduledTaskID: owner.ID, Status: e.status, ScheduledFor: e.scheduledFor, TriggerType: trigger}
	if e.message != "" {
		run.ErrorMessage = &e.message
	}
	createRun(t, repo, run)
	fields := map[string]any{}
	if e.refusal != "" {
		fields["refusal_cause"], fields["refusal_notice"] = e.refusal, e.notice
	}
	if e.sent != "" {
		fields["notifications_sent"] = e.sent
	}
	if err := repo.UpdateExecution(t.Context(), run.ID, fields); err != nil {
		t.Fatalf("mark run %d: %v", e.n, err)
	}
}

func TestScheduledTaskRepo_EndExecution_OtherEndsLeaveNoRefusalMarker(t *testing.T) {
	repo := NewScheduledTaskRepo(newTestDB(t))
	task := createScheduledTask(t, repo, "no-marker", "ns1", nil)

	endRun(t, repo, "no-marker-failed", failedEnd("no-marker-failed", task.ID))

	run := mustGetRun(t, repo, tid("no-marker-failed"))
	if run.RefusalCause != nil || run.RefusalNotice != nil {
		t.Errorf("a counted failure carries refusal marker %v, %v, want none", run.RefusalCause, run.RefusalNotice)
	}
}

func TestScheduledTaskRepo_EndExecution_ARefusalOfARunAnotherPodAlreadyEndedLeavesItUnstamped(t *testing.T) {
	repo := NewScheduledTaskRepo(newTestDB(t))
	task := createScheduledTask(t, repo, "lost-race", "ns1", nil)
	createRun(t, repo, newRun("lost-race-run", task.ID, "pending", 1000))
	won, err := repo.EndExecution(t.Context(), RunEnd{
		ExecutionID: tid("lost-race-run"), TaskID: task.ID, FromStatus: "pending",
		End: ExecutionEnd{Status: "completed", CompletedAt: 5000},
	})
	if err != nil || !won.Ended {
		t.Fatalf("EndExecution as completed = %+v, %v; want ended", won, err)
	}
	msg := "refused"

	lost, err := repo.EndExecution(t.Context(), RunEnd{
		ExecutionID: tid("lost-race-run"), TaskID: task.ID, FromStatus: "pending",
		End: ExecutionEnd{Status: "failed", CompletedAt: 6000, ErrorMessage: &msg, Cause: EndRunAsDisabled},
	})

	if err != nil {
		t.Fatalf("EndExecution as a refusal: %v", err)
	}
	if lost.Ended {
		t.Errorf("EndExecution as a refusal of an ended run = %+v, want Ended false", lost)
	}
	run := mustGetRun(t, repo, tid("lost-race-run"))
	type stamped struct{ Status, Cause, Notice string }
	got := stamped{Status: run.Status}
	if run.RefusalCause != nil {
		got.Cause = *run.RefusalCause
	}
	if run.RefusalNotice != nil {
		got.Notice = *run.RefusalNotice
	}
	if diff := cmp.Diff(stamped{Status: "completed"}, got); diff != "" {
		t.Errorf("run after the lost refusal mismatch (-want +got):\n%s", diff)
	}
}

func TestScheduledTaskRepo_EndExecution_ARefusalWhoseEarlierRunCannotBeReadIsNotEnded(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	task := createScheduledTask(t, repo, "unreadable", "ns1", nil)
	seedEarlierRun(t, repo, task, task, deliveredHead(1, 5000))
	createRun(t, repo, &ScheduledTaskExecution{ID: refusalRunID(endingRunN), ScheduledTaskID: task.ID, Status: "pending", ScheduledFor: endingRunAt})
	storeUnreadableStartedAt(t, db, refusalRunID(1))
	msg := "refused"

	_, err := repo.EndExecution(t.Context(), RunEnd{
		ExecutionID: refusalRunID(endingRunN), TaskID: task.ID, FromStatus: "pending",
		End: ExecutionEnd{Status: "failed", CompletedAt: 20_000, ErrorMessage: &msg, Cause: EndRunAsDisabled},
	})

	if err == nil {
		t.Fatal("EndExecution succeeded, want an error so that the refusal is not ended without a decision")
	}
	var status string
	if err := db.QueryRowContext(t.Context(), `SELECT status FROM scheduled_task_executions WHERE id = ?`, refusalRunID(endingRunN)).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "pending" {
		t.Errorf("status after the failed end = %q, want pending so that the next pass decides again", status)
	}
}

func storeUnreadableStartedAt(t *testing.T, db *DB, executionID string) {
	t.Helper()
	if db.Dialect() == "postgres" {
		if _, err := db.ExecContext(t.Context(), `ALTER TABLE scheduled_task_executions ALTER COLUMN started_at TYPE TEXT`); err != nil {
			t.Fatalf("let started_at hold text: %v", err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE scheduled_task_executions SET started_at = 'unreadable' WHERE id = ?`, executionID); err != nil {
		t.Fatalf("corrupt the earlier run: %v", err)
	}
}
