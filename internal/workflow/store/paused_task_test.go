// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestPausedRepo_PausesForTaskAndCloseUnlessCarried(t *testing.T) {
	ctx := t.Context()
	repo := NewPausedRepo(openTestDB(t))

	answered, open, other := samplePaused("req-answered"), samplePaused("req-open"), samplePaused("req-other")
	answered.TaskID, open.TaskID, other.TaskID = "task-1", "task-1", "task-2"
	for _, p := range []PausedWorkflow{answered, open, other} {
		if err := repo.Insert(ctx, p); err != nil {
			t.Fatalf("Insert %s: %v", p.RequestID, err)
		}
	}
	if _, ok, err := repo.Claim(ctx, "req-answered", "resumed"); err != nil || !ok {
		t.Fatalf("Claim: ok=%v err=%v", ok, err)
	}

	pauses, err := repo.PausesForTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("PausesForTask: %v", err)
	}
	statuses := map[string]string{}
	for _, p := range pauses {
		statuses[p.RequestID] = p.Status
	}
	if len(statuses) != 2 || statuses["req-answered"] != "resumed" || statuses["req-open"] != "paused" {
		t.Fatalf("PausesForTask(task-1) = %v, want the answered and the open row of that task only", statuses)
	}

	_, _, carried, err := repo.CloseUnlessCarried(ctx, "task-1", "closed_for_restart", func([]TaskPause) bool { return true })
	if err != nil || !carried {
		t.Fatalf("CloseUnlessCarried with a carrier: carried=%v err=%v", carried, err)
	}
	if got, _ := repo.StatusesFor(ctx, []string{"req-open"}); got["req-open"] != "paused" {
		t.Fatalf("a carried task's open row = %q, want it left paused", got["req-open"])
	}

	_, n, carried, err := repo.CloseUnlessCarried(ctx, "task-1", "closed_for_restart", func([]TaskPause) bool { return false })
	if err != nil || carried || n != 1 {
		t.Fatalf("CloseUnlessCarried: n=%d carried=%v err=%v, want only the still-paused row closed", n, carried, err)
	}
	got, err := repo.StatusesFor(ctx, []string{"req-answered", "req-open", "req-other"})
	if err != nil {
		t.Fatalf("StatusesFor: %v", err)
	}
	if got["req-answered"] != "resumed" || got["req-open"] != "closed_for_restart" || got["req-other"] != "paused" {
		t.Errorf("statuses after CloseUnlessCarried = %v", got)
	}
}

func TestPausedRepo_AnswerDuringCloseFindsTheRowClosed(t *testing.T) {
	ctx := t.Context()
	repo := NewPausedRepo(openTestDB(t))
	open := samplePaused("req-open")
	open.TaskID = "task-1"
	if err := repo.Insert(ctx, open); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	answered := make(chan bool, 1)
	_, _, _, err := repo.CloseUnlessCarried(ctx, "task-1", "closed_for_restart", func([]TaskPause) bool {
		go func() {
			_, ok, claimErr := repo.Claim(ctx, "req-open", "resumed")
			answered <- ok && claimErr == nil
		}()
		return false
	})
	if err != nil {
		t.Fatalf("CloseUnlessCarried: %v", err)
	}
	if <-answered {
		t.Error("an answer claimed the row while the redelivery was closing it; the run would start twice")
	}
}

func TestMigrations_TaskIDRoundTrip(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	const versionBeforeTaskID = 2
	if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), versionBeforeTaskID); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	repo := NewPausedRepo(db)
	p := samplePaused("req-1")
	p.TaskID = "task-1"
	if err := repo.Insert(ctx, p); err != nil {
		t.Fatalf("Insert after round trip: %v", err)
	}
	pauses, err := repo.PausesForTask(ctx, "task-1")
	if err != nil || !slices.ContainsFunc(pauses, func(tp TaskPause) bool { return tp.RequestID == "req-1" }) {
		t.Fatalf("PausesForTask after round trip: %v err=%v", pauses, err)
	}
}
