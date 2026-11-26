// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestRunRepo_WriteStep(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	ctx := t.Context()
	st := func(snapshot string) RunState {
		return RunState{TaskID: "task-step", WorkflowName: "wf", StateSnapshot: snapshot, ExecContext: testContext, OpenPauses: []string{"p1"}}
	}

	t.Run("the first write creates the row held by the writer at epoch 1", func(t *testing.T) {
		execID := idgen.New()
		h, err := runs.WriteStep(ctx, execID, Holder{Key: HeldByRequest}, st(`{"n":1}`))
		if err != nil || h.Epoch != 1 {
			t.Fatalf("WriteStep = %+v, %v; want written at epoch 1", h, err)
		}
		run, err := runs.Get(ctx, execID)
		if err != nil || run == nil || run.Status != RunHeld || run.HeldBy != HeldByRequest || run.HolderEpoch != 1 || run.StateSnapshot != `{"n":1}` || run.OpenPauses != `["p1"]` {
			t.Fatalf("row = %+v (err %v)", run, err)
		}
		if _, err := runs.WriteStep(ctx, execID, h, st(`{"n":2}`)); err != nil {
			t.Fatalf("second WriteStep: %v", err)
		}
		if run, _ := runs.Get(ctx, execID); run.StateSnapshot != `{"n":2}` || run.CreatedAt == 0 {
			t.Fatalf("row after the second write = %+v", run)
		}
	})

	t.Run("a first write that landed without its reply is the writer's own", func(t *testing.T) {
		execID := idgen.New()
		if _, err := runs.WriteStep(ctx, execID, Holder{Key: HeldByRequest}, st(`{"n":1}`)); err != nil {
			t.Fatalf("WriteStep: %v", err)
		}
		h, err := runs.WriteStep(ctx, execID, Holder{Key: HeldByRequest}, st(`{"n":2}`))
		if err != nil || h.Epoch != 1 {
			t.Fatalf("second write of the copy still at epoch 0 = %+v, %v; want written at epoch 1", h, err)
		}
		if _, _, err := runs.Retake(ctx, execID, HeldByRequest, "closed_for_restart"); err != nil {
			t.Fatalf("Retake: %v", err)
		}
		if _, err := runs.WriteStep(ctx, execID, Holder{Key: HeldByRequest}, st(`{"n":3}`)); !errors.Is(err, ErrNotHolder) {
			t.Errorf("an epoch 0 write after a retake = %v, want ErrNotHolder", err)
		}
	})

	t.Run("a copy at an earlier epoch is not written", func(t *testing.T) {
		execID, _, b := newRun(t, runs, paused, runHeldByAnswerB)
		stale := heldAs(t, runs, execID, b)
		if _, _, err := runs.Retake(ctx, execID, b, "closed_for_restart"); err != nil {
			t.Fatalf("Retake: %v", err)
		}
		if _, err := runs.WriteStep(ctx, execID, stale, st(`{"n":9}`)); !errors.Is(err, ErrNotHolder) {
			t.Fatalf("WriteStep by the copy the retake replaced = %v, want ErrNotHolder", err)
		}
		if _, err := runs.WriteStep(ctx, execID, heldAs(t, runs, execID, b), st(`{"n":9}`)); err != nil {
			t.Fatalf("WriteStep by the retaking copy: %v", err)
		}
	})

	for _, tc := range []struct {
		name   string
		state  runOf
		heldBy func(a, b string) string
	}{
		{name: "a parked run is not written", state: runParked, heldBy: func(_, _ string) string { return HeldByRequest }},
		{name: "a run held by another message is not written", state: runHeldByAnswerB, heldBy: func(a, _ string) string { return a }},
		{name: "a run held by an answer is written by that answer's holder", state: runHeldByAnswerB, heldBy: func(_, b string) string { return b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execID, a, b := newRun(t, runs, paused, tc.state)
			_, err := runs.WriteStep(ctx, execID, heldAs(t, runs, execID, tc.heldBy(a, b)), st(`{"n":9}`))
			wantWritten := tc.state == runHeldByAnswerB && tc.heldBy(a, b) == b
			if (err == nil) != wantWritten || (err != nil && !errors.Is(err, ErrNotHolder)) {
				t.Fatalf("WriteStep = %v; want written %v", err, wantWritten)
			}
			run, _ := runs.Get(ctx, execID)
			if (run.StateSnapshot == `{"n":9}`) != wantWritten {
				t.Fatalf("row state %q; want written %v", run.StateSnapshot, wantWritten)
			}
		})
	}

	t.Run("an ended run is not written", func(t *testing.T) {
		execID := idgen.New()
		h, err := runs.WriteStep(ctx, execID, Holder{Key: HeldByRequest}, st(`{"n":1}`))
		if err != nil {
			t.Fatalf("WriteStep: %v", err)
		}
		if err := runs.MarkAnswered(ctx, execID, h); err != nil {
			t.Fatalf("MarkAnswered: %v", err)
		}
		if _, err := runs.WriteStep(ctx, execID, h, st(`{"n":2}`)); !errors.Is(err, ErrNotHolder) {
			t.Fatalf("WriteStep after the answer = %v; want ErrNotHolder", err)
		}
		run, _ := runs.Get(ctx, execID)
		if run.Status != RunEnded || run.AnsweredAt == nil || run.EndedAt == nil {
			t.Fatalf("row = %+v, want ended and answered", run)
		}
	})
}

func TestRunRepo_DeleteHeldPastDeadline(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	ctx := t.Context()
	past, future := int64(1_000), int64(9_000_000_000_000)

	write := func(deadline *int64) string {
		execID := idgen.New()
		if _, err := runs.WriteStep(ctx, execID, Holder{Key: HeldByRequest}, RunState{TaskID: "t-" + execID, WorkflowName: "wf", StateSnapshot: testState, DeadlineAt: deadline}); err != nil {
			t.Fatalf("WriteStep: %v", err)
		}
		return execID
	}
	heldPast := write(&past)
	heldFuture := write(&future)
	heldNoDeadline := write(nil)
	parkedPast, _, _ := newRun(t, runs, paused, runParked)
	if _, err := db.ExecContext(ctx, `UPDATE workflow_checkpoint SET deadline_at = ? WHERE execution_id = ?`, past, parkedPast); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	n, err := runs.DeleteHeldPastDeadline(ctx, 2_000, 100)
	if err != nil || n != 1 {
		t.Fatalf("DeleteHeldPastDeadline = %d, %v; want 1", n, err)
	}
	for id, want := range map[string]bool{heldPast: false, heldFuture: true, heldNoDeadline: true, parkedPast: true} {
		if run, _ := runs.Get(ctx, id); (run != nil) != want {
			t.Errorf("run %s kept = %v, want %v", id, run != nil, want)
		}
	}
}

func TestRunRepo_Restart(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	ctx := t.Context()

	execID, a, _ := newRun(t, runs, paused, runHeldByRequest)
	closed, err := runs.Restart(ctx, execID, "closed_for_restart")
	if err != nil || closed != 2 {
		t.Fatalf("Restart = %d, %v; want 2 closed", closed, err)
	}
	if status, _ := runRow(t, runs, execID); status != RunEnded {
		t.Errorf("run = %q, want ended", status)
	}
	if status, _ := pauseRow(t, paused, a); status != "closed_for_restart" {
		t.Errorf("pause a = %q, want closed_for_restart", status)
	}

	carried, b2, _ := newRun(t, runs, paused, runHeldByAnswerB)
	if closed, err := runs.Restart(ctx, carried, "closed_for_restart"); err != nil || closed != 0 {
		t.Fatalf("Restart of a run held by an answer = %d, %v; want untouched", closed, err)
	}
	if status, _ := pauseRow(t, paused, b2); status != "paused" {
		t.Errorf("pause of the untouched run = %q, want paused", status)
	}
}
