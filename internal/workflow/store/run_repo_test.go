// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	testState   = `{"execution_id":"x"}`
	testContext = `{"task_id":"t"}`
	testAnswer  = `{"approved":true}`
)

type runOf int

const (
	runHeldByRequest runOf = iota
	runParked
	runHeldByAnswerB
	runLegacy
)

func newRun(t *testing.T, runs *RunRepo, paused *PausedRepo, state runOf) (execID, a, b string) {
	t.Helper()
	ctx := t.Context()
	execID = idgen.New()
	a, b = execID+"-a", execID+"-b"
	h := Holder{Key: HeldByRequest}
	for _, id := range []string{a, b} {
		p := samplePaused(id)
		p.ExecutionID, p.TaskID = execID, "task-"+execID
		if state == runLegacy {
			if err := paused.Insert(ctx, p); err != nil {
				t.Fatalf("Insert %s: %v", id, err)
			}
			continue
		}
		var err error
		if h, err = runs.InsertPause(ctx, p, h); err != nil {
			t.Fatalf("InsertPause %s: %v", id, err)
		}
	}
	if state == runParked || state == runHeldByAnswerB {
		if _, err := runs.Park(ctx, execID, h, RunState{StateSnapshot: testState, ExecContext: testContext, OpenPauses: []string{a, b}}); err != nil {
			t.Fatalf("Park: %v", err)
		}
	}
	if state == runHeldByAnswerB {
		if res, err := runs.AcceptAnswer(ctx, "wf", b, "resumed", testAnswer); err != nil || res.Outcome != AnswerTookOver {
			t.Fatalf("AcceptAnswer(b) = %v, %v; want it to take the run over", res.Outcome, err)
		}
	}
	return execID, a, b
}

func heldAs(t *testing.T, runs *RunRepo, execID, key string) Holder {
	t.Helper()
	run, err := runs.Get(t.Context(), execID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if run == nil {
		return Holder{Key: key}
	}
	return Holder{Key: key, Epoch: run.HolderEpoch}
}

func runRow(t *testing.T, runs *RunRepo, execID string) (status, heldBy string) {
	t.Helper()
	run, err := runs.Get(t.Context(), execID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if run == nil {
		return "", ""
	}
	return run.Status, run.HeldBy
}

func pauseRow(t *testing.T, paused *PausedRepo, requestID string) (status, answer string) {
	t.Helper()
	p, err := paused.Get(t.Context(), requestID)
	if err != nil || p == nil {
		t.Fatalf("Get pause %s: %v, %v", requestID, p, err)
	}
	if p.Answer != nil {
		answer = *p.Answer
	}
	return p.Status, answer
}

func TestRunRepo_AcceptAnswer(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)

	tests := []struct {
		name          string
		state         runOf
		answerB       bool
		unknown       bool
		otherWorkflow bool
		want          AnswerOutcome
		wantRun       string
		wantHeldBy    string
		wantPause     string
		wantAnswer    string
		wantNoPause   bool
	}{
		{name: "a parked run is taken over by the answer", state: runParked,
			want: AnswerTookOver, wantRun: RunHeld, wantHeldBy: "<a>", wantPause: "resumed"},
		{name: "a run its request holds gets the answer written on the pause", state: runHeldByRequest,
			want: AnswerRecorded, wantRun: RunHeld, wantHeldBy: "", wantPause: "answered", wantAnswer: testAnswer},
		{name: "a run another answer holds gets the answer written on the pause", state: runHeldByAnswerB,
			want: AnswerRecorded, wantRun: RunHeld, wantHeldBy: "<b>", wantPause: "answered", wantAnswer: testAnswer},
		{name: "the answer that holds the run is reported as holding it", state: runHeldByAnswerB, answerB: true,
			want: AnswerHoldsRun, wantRun: RunHeld, wantHeldBy: "<b>", wantPause: "resumed"},
		{name: "a pause with no run row was written by the previous release", state: runLegacy,
			want: AnswerNoRun, wantRun: "", wantHeldBy: "", wantPause: "paused"},
		{name: "an unknown pause is settled", state: runParked, unknown: true,
			want: AnswerSettled, wantRun: RunParked, wantHeldBy: "", wantNoPause: true},
		{name: "an answer received by another workflow leaves the pause and the run alone", state: runParked, otherWorkflow: true,
			want: AnswerOtherWorkflow, wantRun: RunParked, wantHeldBy: "", wantPause: "paused"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			execID, a, b := newRun(t, runs, paused, tc.state)
			target := a
			if tc.answerB {
				target = b
			}
			if tc.unknown {
				target = execID + "-unknown"
			}
			receiver := "wf"
			if tc.otherWorkflow {
				receiver = "other-wf"
			}
			res, err := runs.AcceptAnswer(t.Context(), receiver, target, "resumed", testAnswer)
			if err != nil {
				t.Fatalf("AcceptAnswer: %v", err)
			}
			if res.Outcome != tc.want {
				t.Errorf("outcome = %v, want %v", res.Outcome, tc.want)
			}
			wantHeldBy := map[string]string{"<a>": a, "<b>": b, "": ""}[tc.wantHeldBy]
			if status, heldBy := runRow(t, runs, execID); status != tc.wantRun || heldBy != wantHeldBy {
				t.Errorf("run = %s held by %q, want %s held by %q", status, heldBy, tc.wantRun, wantHeldBy)
			}
			if tc.wantNoPause {
				if res.Pause != nil {
					t.Errorf("pause = %+v, want none", res.Pause)
				}
				return
			}
			if status, answer := pauseRow(t, paused, target); status != tc.wantPause || answer != tc.wantAnswer {
				t.Errorf("pause = %s with answer %q, want %s with %q", status, answer, tc.wantPause, tc.wantAnswer)
			}
		})
	}

	t.Run("a resolved pause is a duplicate", func(t *testing.T) {
		execID, a, _ := newRun(t, runs, paused, runParked)
		if _, err := runs.AcceptAnswer(t.Context(), "wf", a, "resumed", testAnswer); err != nil {
			t.Fatalf("first AcceptAnswer: %v", err)
		}
		if _, err := runs.Park(t.Context(), execID, heldAs(t, runs, execID, a), RunState{StateSnapshot: testState, ExecContext: testContext, OpenPauses: nil}); err != nil {
			t.Fatalf("Park after resume: %v", err)
		}
		res, err := runs.AcceptAnswer(t.Context(), "wf", a, "resumed", testAnswer)
		if err != nil || res.Outcome != AnswerSettled {
			t.Fatalf("second AcceptAnswer = %v, %v; want settled", res.Outcome, err)
		}
		if status, heldBy := runRow(t, runs, execID); status != RunParked || heldBy != "" {
			t.Errorf("run = %s held by %q, want it left parked", status, heldBy)
		}
	})
}

func TestRunRepo_Park(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)

	tests := []struct {
		name         string
		state        runOf
		answerFirst  bool
		heldBy       string
		wantErr      error
		wantAnswered int
		wantRun      string
		wantState    string
		parkState    string
	}{
		{name: "the holder parks the run with its state", state: runHeldByRequest,
			wantRun: RunParked, wantState: testState},
		{name: "a written answer stops the park and is returned", state: runHeldByRequest, answerFirst: true,
			wantAnswered: 1, wantRun: RunHeld},
		{name: "a caller not holding the run cannot park it", state: runHeldByRequest, heldBy: "someone-else",
			wantErr: ErrNotHolder, wantRun: RunHeld},
		{name: "a parked run cannot be parked with another state", state: runParked, parkState: `{"other":true}`,
			wantErr: ErrNotHolder, wantRun: RunParked, wantState: testState},
		{name: "a park retried after its commit landed succeeds", state: runParked,
			wantRun: RunParked, wantState: testState},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			execID, a, b := newRun(t, runs, paused, tc.state)
			if tc.answerFirst {
				if res, err := runs.AcceptAnswer(ctx, "wf", a, "resumed", testAnswer); err != nil || res.Outcome != AnswerRecorded {
					t.Fatalf("AcceptAnswer = %v, %v", res.Outcome, err)
				}
			}
			parkState := testState
			if tc.parkState != "" {
				parkState = tc.parkState
			}
			answered, err := runs.Park(ctx, execID, heldAs(t, runs, execID, tc.heldBy), RunState{StateSnapshot: parkState, ExecContext: testContext, OpenPauses: []string{a, b}})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Park error = %v, want %v", err, tc.wantErr)
			}
			if len(answered) != tc.wantAnswered || (tc.wantAnswered == 1 && answered[0].RequestID != a) {
				t.Errorf("answered = %+v, want %d (pause a)", answered, tc.wantAnswered)
			}
			run, err := runs.Get(ctx, execID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if run.Status != tc.wantRun || run.StateSnapshot != tc.wantState {
				t.Errorf("run = %s with state %q, want %s with %q", run.Status, run.StateSnapshot, tc.wantRun, tc.wantState)
			}
			if tc.wantRun == RunParked {
				ids, err := run.OpenPauseIDs()
				if err != nil || len(ids) != 2 || ids[0] != a || ids[1] != b {
					t.Errorf("open pauses = %v (%v), want [%s %s]", ids, err, a, b)
				}
			}
		})
	}
}

func TestRunRepo_ClaimHeld(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)

	tests := []struct {
		name        string
		recordFirst bool
		claimTwice  bool
		heldBy      string
		wantErr     error
		wantClaimed bool
		wantAnswer  string
		wantPause   string
	}{
		{name: "the holder claims an unanswered pause", wantClaimed: true, wantPause: "resumed"},
		{name: "the holder claims a pause with a written answer and gets it", recordFirst: true,
			wantClaimed: true, wantAnswer: testAnswer, wantPause: "resumed"},
		{name: "a resolved pause is not claimed again", claimTwice: true, wantPause: "resumed"},
		{name: "a caller not holding the run cannot claim", heldBy: "someone-else",
			wantErr: ErrNotHolder, wantPause: "paused"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			execID, a, _ := newRun(t, runs, paused, runHeldByRequest)
			if tc.recordFirst {
				if _, err := runs.AcceptAnswer(ctx, "wf", a, "resumed", testAnswer); err != nil {
					t.Fatalf("AcceptAnswer: %v", err)
				}
			}
			if tc.claimTwice {
				if _, _, err := runs.ClaimHeld(ctx, a, heldAs(t, runs, execID, HeldByRequest), "resumed"); err != nil {
					t.Fatalf("first ClaimHeld: %v", err)
				}
			}
			p, claimed, err := runs.ClaimHeld(ctx, a, heldAs(t, runs, execID, tc.heldBy), "resumed")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ClaimHeld error = %v, want %v", err, tc.wantErr)
			}
			if claimed != tc.wantClaimed {
				t.Errorf("claimed = %v, want %v", claimed, tc.wantClaimed)
			}
			var answer string
			if p != nil && p.Answer != nil {
				answer = *p.Answer
			}
			if answer != tc.wantAnswer {
				t.Errorf("answer = %q, want %q", answer, tc.wantAnswer)
			}
			if status, _ := pauseRow(t, paused, a); status != tc.wantPause {
				t.Errorf("pause = %s, want %s", status, tc.wantPause)
			}
		})
	}
}

func TestRunRepo_Retake(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)

	execID, a, b := newRun(t, runs, paused, runHeldByAnswerB)
	later := samplePaused(execID + "-later")
	later.ExecutionID, later.TaskID = execID, "task-"+execID
	if _, err := runs.InsertPause(ctx, later, heldAs(t, runs, execID, b)); err != nil {
		t.Fatalf("InsertPause later: %v", err)
	}

	if _, _, err := runs.Retake(ctx, execID, "someone-else", "closed_for_restart"); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("Retake by a non-holder error = %v, want ErrNotHolder", err)
	}
	run, closed, err := runs.Retake(ctx, execID, b, "closed_for_restart")
	if err != nil {
		t.Fatalf("Retake: %v", err)
	}
	if closed != 1 || run.StateSnapshot != testState {
		t.Errorf("Retake closed %d with state %q, want 1 with %q", closed, run.StateSnapshot, testState)
	}
	for id, want := range map[string]string{a: "paused", b: "resumed", later.RequestID: "closed_for_restart"} {
		if status, _ := pauseRow(t, paused, id); status != want {
			t.Errorf("pause %s = %s, want %s", id, status, want)
		}
	}
}

func TestRunRepo_AcceptAnswerRetiresThePauseOfAnEndedRun(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	ctx := t.Context()
	execID, a, _ := newRun(t, runs, paused, runHeldByRequest)
	if err := runs.MarkAnswered(ctx, execID, heldAs(t, runs, execID, HeldByRequest)); err != nil {
		t.Fatalf("MarkAnswered: %v", err)
	}
	res, err := runs.AcceptAnswer(ctx, "wf", a, "timed_out", testAnswer)
	if err != nil || res.Outcome != AnswerSettled {
		t.Fatalf("AcceptAnswer = %v, %v; want settled", res.Outcome, err)
	}
	if status, _ := pauseRow(t, paused, a); status != "cancelled" {
		t.Errorf("pause of the ended run = %q, want cancelled", status)
	}
}

func TestRunRepo_SettleTaskRequest(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)

	tests := []struct {
		name       string
		state      runOf
		recordA    bool
		restore    bool
		stepWrite  bool
		answered   bool
		want       RequestOutcome
		wantClosed int64
		wantRun    string
		wantPauseA string
	}{
		{name: "a task with no run row has nothing to settle", state: runLegacy,
			want: RequestNoRun, wantRun: "", wantPauseA: "paused"},
		{name: "a parked run makes the request a leftover", state: runParked,
			want: RequestCarried, wantRun: RunParked, wantPauseA: "paused"},
		{name: "a run held by an answer makes the request a leftover", state: runHeldByAnswerB,
			want: RequestCarried, wantRun: RunHeld, wantPauseA: "paused"},
		{name: "a run its request held restarts and its open pauses close", state: runHeldByRequest, recordA: true,
			want: RequestRestart, wantClosed: 2, wantRun: RunEnded, wantPauseA: "closed_for_restart"},
		{name: "with restore off, a run its request held with a stored state restarts", state: runHeldByRequest, stepWrite: true,
			want: RequestRestart, wantClosed: 2, wantRun: RunEnded, wantPauseA: "closed_for_restart"},
		{name: "with restore on, a run its request held with no stored state restarts", state: runHeldByRequest, restore: true,
			want: RequestRestart, wantClosed: 2, wantRun: RunEnded, wantPauseA: "closed_for_restart"},
		{name: "with restore on, a run its request held with a stored state is returned untouched", state: runHeldByRequest, restore: true, stepWrite: true,
			want: RequestRestore, wantRun: RunHeld, wantPauseA: "paused"},
		{name: "a run that answered its caller makes the request a leftover", state: runHeldByRequest, answered: true,
			want: RequestAnswered, wantRun: RunEnded, wantPauseA: "paused"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			execID, a, _ := newRun(t, runs, paused, tc.state)
			if tc.recordA {
				if _, err := runs.AcceptAnswer(ctx, "wf", a, "resumed", testAnswer); err != nil {
					t.Fatalf("AcceptAnswer: %v", err)
				}
			}
			if tc.stepWrite {
				if _, err := runs.WriteStep(ctx, execID, heldAs(t, runs, execID, HeldByRequest), RunState{StateSnapshot: testState, ExecContext: testContext}); err != nil {
					t.Fatalf("WriteStep: %v", err)
				}
			}
			if tc.answered {
				if err := runs.MarkAnswered(ctx, execID, heldAs(t, runs, execID, HeldByRequest)); err != nil {
					t.Fatalf("MarkAnswered: %v", err)
				}
			}
			got, err := runs.SettleTaskRequest(ctx, "task-"+execID, "closed_for_restart", tc.restore)
			if err != nil {
				t.Fatalf("SettleTaskRequest: %v", err)
			}
			if got.Outcome != tc.want || got.Closed != tc.wantClosed {
				t.Errorf("SettleTaskRequest = %v closing %d, want %v closing %d", got.Outcome, got.Closed, tc.want, tc.wantClosed)
			}
			if (tc.want == RequestRestore || tc.want == RequestAnswered) && (got.Run == nil || got.Run.ExecutionID != execID) {
				t.Errorf("SettleTaskRequest run = %+v, want execution %s", got.Run, execID)
			}
			if status, _ := runRow(t, runs, execID); status != tc.wantRun {
				t.Errorf("run = %q, want %q", status, tc.wantRun)
			}
			if status, _ := pauseRow(t, paused, a); status != tc.wantPauseA {
				t.Errorf("pause a = %s, want %s", status, tc.wantPauseA)
			}
		})
	}
}

func TestRunRepo_TakeOverParked(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)

	for _, tc := range []struct {
		name     string
		state    runOf
		workflow string
		wantTook bool
		wantRun  string
		wantBy   string
	}{
		{name: "a parked run is taken over", state: runParked, workflow: "wf", wantTook: true, wantRun: RunHeld, wantBy: "deadline"},
		{name: "a held run is left alone", state: runHeldByRequest, workflow: "wf", wantRun: RunHeld, wantBy: ""},
		{name: "a parked run of another workflow is left alone", state: runParked, workflow: "other-wf", wantRun: RunParked, wantBy: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execID, _, _ := newRun(t, runs, paused, tc.state)
			parked := heldAs(t, runs, execID, "")
			_, took, err := runs.TakeOverParked(t.Context(), tc.workflow, execID, "deadline")
			if err != nil || took != tc.wantTook {
				t.Fatalf("TakeOverParked = %v, %v; want %v", took, err, tc.wantTook)
			}
			if status, by := runRow(t, runs, execID); status != tc.wantRun || by != tc.wantBy {
				t.Errorf("run = %s held by %q, want %s held by %q", status, by, tc.wantRun, tc.wantBy)
			}
			if tc.wantTook {
				if got := heldAs(t, runs, execID, "deadline").Epoch; got != parked.Epoch+1 {
					t.Errorf("epoch after the takeover = %d, want %d", got, parked.Epoch+1)
				}
			}
		})
	}
}

func TestRunRepo_EndAndDelete(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)

	ended, _, _ := newRun(t, runs, paused, runHeldByRequest)
	live, _, _ := newRun(t, runs, paused, runParked)
	if err := runs.End(ctx, ended, heldAs(t, runs, ended, HeldByRequest)); err != nil {
		t.Fatalf("End: %v", err)
	}
	n, err := runs.DeleteEndedOlderThan(ctx, 1<<62, 100)
	if err != nil || n != 1 {
		t.Fatalf("DeleteEndedOlderThan = %d, %v; want the one ended run", n, err)
	}
	if status, _ := runRow(t, runs, ended); status != "" {
		t.Errorf("ended run still stored as %q", status)
	}
	if status, _ := runRow(t, runs, live); status != RunParked {
		t.Errorf("live run = %q, want it kept parked", status)
	}
}

func TestPausedRepo_AnsweredRowsAreOpen(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)

	execID, a, _ := newRun(t, runs, paused, runHeldByRequest)
	if _, err := runs.AcceptAnswer(ctx, "wf", a, "resumed", testAnswer); err != nil {
		t.Fatalf("AcceptAnswer: %v", err)
	}
	open, err := paused.PausedForExecution(ctx, execID)
	if err != nil || len(open) != 2 {
		t.Fatalf("PausedForExecution = %d rows (%v), want both pauses", len(open), err)
	}
	n, err := paused.ClaimAllForExecution(ctx, execID, "cancelled")
	if err != nil || n != 2 {
		t.Fatalf("ClaimAllForExecution = %d (%v), want both pauses retired", n, err)
	}
}

func TestRunRepo_ConcurrentClaimsOfOneAnswerClaimItOnce(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	for round := range 20 {
		execID, a, _ := newRun(t, runs, paused, runHeldByRequest)
		if res, err := runs.AcceptAnswer(t.Context(), "wf", a, "resumed", testAnswer); err != nil || res.Outcome != AnswerRecorded {
			t.Fatalf("round %d: record: %v %v", round, res.Outcome, err)
		}
		holder := heldAs(t, runs, execID, HeldByRequest)
		const claimers = 8
		claimed := make(chan bool, claimers)
		start := make(chan struct{})
		for range claimers {
			go func() {
				<-start
				_, ok, err := runs.ClaimHeld(t.Context(), a, holder, "resumed")
				claimed <- err == nil && ok
			}()
		}
		close(start)
		n := 0
		for range claimers {
			if <-claimed {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("round %d: %d claims of one answer succeeded, want 1", round, n)
		}
	}
}

func TestRunRepo_ConcurrentCopiesOfOneAnswerTakeTheRunOverOnce(t *testing.T) {
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	for round := range 20 {
		_, a, _ := newRun(t, runs, paused, runParked)
		const copies = 8
		outcomes := make(chan AnswerOutcome, copies)
		start := make(chan struct{})
		for range copies {
			go func() {
				<-start
				res, err := runs.AcceptAnswer(t.Context(), "wf", a, "resumed", testAnswer)
				if err != nil {
					outcomes <- -1
					return
				}
				outcomes <- res.Outcome
			}()
		}
		close(start)
		took, other := 0, 0
		for range copies {
			switch <-outcomes {
			case AnswerTookOver:
				took++
			case AnswerHoldsRun, AnswerSettled:
			default:
				other++
			}
		}
		if took != 1 || other != 0 {
			t.Fatalf("round %d: %d took the run over and %d had another outcome, want 1 and 0", round, took, other)
		}
		if status, answer := pauseRow(t, paused, a); status != "resumed" || answer != "" {
			t.Fatalf("round %d: pause = %s with answer %q, want resumed and no written answer", round, status, answer)
		}
	}
}

func TestRunRepo_RetakeReopensAnAnswerTheDeadHolderClaimed(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	execID, a, b := newRun(t, runs, paused, runHeldByAnswerB)
	if res, err := runs.AcceptAnswer(ctx, "wf", a, "resumed", testAnswer); err != nil || res.Outcome != AnswerRecorded {
		t.Fatalf("AcceptAnswer(a) = %v, %v; want it recorded for the holder", res.Outcome, err)
	}
	if _, claimed, err := runs.ClaimHeld(ctx, a, heldAs(t, runs, execID, b), "resumed"); err != nil || !claimed {
		t.Fatalf("ClaimHeld(a) = %v, %v", claimed, err)
	}

	if _, _, err := runs.Retake(ctx, execID, b, "closed_for_restart"); err != nil {
		t.Fatalf("Retake: %v", err)
	}
	if status, answer := pauseRow(t, paused, a); status != StatusAnswered || answer != testAnswer {
		t.Errorf("pause a = %s with answer %q, want answered with its answer kept", status, answer)
	}
	if status, _ := pauseRow(t, paused, b); status != "resumed" {
		t.Errorf("pause b, the retaken answer's own, = %s, want resumed", status)
	}
}

func TestRunRepo_RetakeClosesMorePausesThanTheInClauseCap(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	execID, _, b := newRun(t, runs, paused, runHeldByAnswerB)
	const later = 1001
	for i := range later {
		p := samplePaused(fmt.Sprintf("%s-item-%d", execID, i))
		p.ExecutionID, p.TaskID = execID, "task-"+execID
		if _, err := runs.InsertPause(ctx, p, heldAs(t, runs, execID, b)); err != nil {
			t.Fatalf("InsertPause %d: %v", i, err)
		}
	}
	_, closed, err := runs.Retake(ctx, execID, b, "closed_for_restart")
	if err != nil || closed != later {
		t.Fatalf("Retake closed %d, %v; want the %d pauses opened after the park", closed, err, later)
	}
}

func TestRunRepo_ReleaseClaim(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	execID, a, _ := newRun(t, runs, paused, runHeldByRequest)
	if res, err := runs.AcceptAnswer(ctx, "wf", a, "resumed", testAnswer); err != nil || res.Outcome != AnswerRecorded {
		t.Fatalf("AcceptAnswer = %v, %v", res.Outcome, err)
	}
	if _, claimed, err := runs.ClaimHeld(ctx, a, heldAs(t, runs, execID, HeldByRequest), "resumed"); err != nil || !claimed {
		t.Fatalf("ClaimHeld = %v, %v", claimed, err)
	}
	if err := runs.ReleaseClaim(ctx, a, heldAs(t, runs, execID, "someone-else")); !errors.Is(err, ErrNotHolder) {
		t.Errorf("ReleaseClaim by a non-holder = %v, want ErrNotHolder", err)
	}
	if err := runs.ReleaseClaim(ctx, a, heldAs(t, runs, execID, HeldByRequest)); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if status, answer := pauseRow(t, paused, a); status != StatusAnswered || answer != testAnswer {
		t.Errorf("pause = %s with answer %q, want answered with its answer", status, answer)
	}
}

func TestRunRepo_DeleteHeldIdleOlderThan(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	idle, _, _ := newRun(t, runs, paused, runHeldByRequest)
	if _, err := db.ExecContext(ctx, `UPDATE workflow_paused SET status = 'cancelled' WHERE execution_id = ?`, idle); err != nil {
		t.Fatalf("close pauses: %v", err)
	}
	waiting, _, _ := newRun(t, runs, paused, runHeldByRequest)
	parked, _, _ := newRun(t, runs, paused, runParked)
	withDeadline, _, _ := newRun(t, runs, paused, runHeldByRequest)
	if _, err := db.ExecContext(ctx, `UPDATE workflow_checkpoint SET deadline_at = ? WHERE execution_id = ?`, int64(1<<62), withDeadline); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	n, err := runs.DeleteHeldIdleOlderThan(ctx, 1<<62, 100)
	if err != nil || n != 1 {
		t.Fatalf("DeleteHeldIdleOlderThan = %d, %v; want the idle held run with no open pause", n, err)
	}
	for execID, want := range map[string]string{idle: "", waiting: RunHeld, parked: RunParked, withDeadline: RunHeld} {
		if status, _ := runRow(t, runs, execID); status != want {
			t.Errorf("run %s = %q, want %q", execID, status, want)
		}
	}
}

func TestRunRepo_AnswerToAPauseWhoseRunWasPurgedClosesIt(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	purged, a, _ := newRun(t, runs, paused, runHeldByRequest)
	if err := runs.End(ctx, purged, heldAs(t, runs, purged, HeldByRequest)); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, err := runs.DeleteEndedOlderThan(ctx, 1<<62, 100); err != nil {
		t.Fatalf("DeleteEndedOlderThan: %v", err)
	}
	if res, err := runs.AcceptAnswer(ctx, "wf", a, "timed_out", testAnswer); err != nil || res.Outcome != AnswerSettled {
		t.Fatalf("AcceptAnswer = %v, %v; want settled", res.Outcome, err)
	}
	if status, _ := pauseRow(t, paused, a); status != "cancelled" {
		t.Errorf("pause of the purged run = %s, want cancelled", status)
	}

	_, legacy, _ := newRun(t, runs, paused, runLegacy)
	if res, err := runs.AcceptAnswer(ctx, "wf", legacy, "resumed", testAnswer); err != nil || res.Outcome != AnswerNoRun {
		t.Errorf("AcceptAnswer on an earlier release's pause = %v, %v; want AnswerNoRun", res.Outcome, err)
	}
}

func TestRunRepo_ParkRefusesAClaimedOpenPause(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	execID, a, b := newRun(t, runs, paused, runHeldByRequest)
	if _, claimed, err := runs.ClaimHeld(ctx, a, heldAs(t, runs, execID, HeldByRequest), "resumed"); err != nil || !claimed {
		t.Fatalf("ClaimHeld = %v, %v", claimed, err)
	}
	_, err := runs.Park(ctx, execID, heldAs(t, runs, execID, HeldByRequest), RunState{StateSnapshot: testState, ExecContext: testContext, OpenPauses: []string{a, b}})
	if !errors.Is(err, ErrPauseClaimed) {
		t.Fatalf("Park error = %v, want ErrPauseClaimed", err)
	}
	if status, _ := runRow(t, runs, execID); status != RunHeld {
		t.Errorf("run = %s, want still held", status)
	}
}

func TestRunRepo_EpochFencesTheCopyThatLostTheRun(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	execID, _, b := newRun(t, runs, paused, runParked)
	parkedEpoch := heldAs(t, runs, execID, "").Epoch
	if _, err := runs.AcceptAnswer(ctx, "wf", b, "resumed", testAnswer); err != nil {
		t.Fatalf("AcceptAnswer: %v", err)
	}
	stale := heldAs(t, runs, execID, b)
	if stale.Epoch != parkedEpoch+1 {
		t.Fatalf("epoch after the takeover = %d, want %d", stale.Epoch, parkedEpoch+1)
	}
	if _, _, err := runs.Retake(ctx, execID, b, "closed_for_restart"); err != nil {
		t.Fatalf("Retake: %v", err)
	}
	current := heldAs(t, runs, execID, b)
	if current.Epoch != stale.Epoch+1 {
		t.Fatalf("epoch after the retake = %d, want %d", current.Epoch, stale.Epoch+1)
	}

	if holds, err := runs.Fence(ctx, execID, stale); err != nil || holds {
		t.Errorf("Fence of the stale copy = %v, %v; want false", holds, err)
	}
	if err := runs.MarkAnswered(ctx, execID, stale); !errors.Is(err, ErrNotHolder) {
		t.Errorf("MarkAnswered by the stale copy = %v, want ErrNotHolder", err)
	}
	if status, _ := runRow(t, runs, execID); status != RunHeld {
		t.Fatalf("run = %s after the stale copy's mark, want still held", status)
	}
	if holds, err := runs.Fence(ctx, execID, current); err != nil || !holds {
		t.Errorf("Fence of the current holder = %v, %v; want true", holds, err)
	}
	if err := runs.MarkAnswered(ctx, execID, current); err != nil {
		t.Errorf("MarkAnswered by the current holder: %v", err)
	}
	if err := runs.MarkAnswered(ctx, execID, current); err != nil {
		t.Errorf("a retried mark that landed: %v", err)
	}
}

func TestRunRepo_EveryWriteFencesTheCopyAtAnEarlierEpoch(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	st := RunState{StateSnapshot: testState, ExecContext: testContext}
	for _, tc := range []struct {
		name  string
		write func(execID, a string, stale Holder) error
	}{
		{"InsertPause", func(execID, _ string, stale Holder) error {
			p := samplePaused(execID + "-c")
			p.ExecutionID = execID
			_, err := runs.InsertPause(ctx, p, stale)
			return err
		}},
		{"WriteStep", func(execID, _ string, stale Holder) error {
			_, err := runs.WriteStep(ctx, execID, stale, st)
			return err
		}},
		{"Park", func(execID, _ string, stale Holder) error {
			_, err := runs.Park(ctx, execID, stale, st)
			return err
		}},
		{"ClaimHeld", func(_, a string, stale Holder) error {
			_, _, err := runs.ClaimHeld(ctx, a, stale, "resumed")
			return err
		}},
		{"ReleaseClaim", func(_, a string, stale Holder) error {
			return runs.ReleaseClaim(ctx, a, stale)
		}},
		{"MarkAnswered", func(execID, _ string, stale Holder) error {
			return runs.MarkAnswered(ctx, execID, stale)
		}},
		{"End", func(execID, _ string, stale Holder) error {
			if err := runs.End(ctx, execID, stale); err != nil {
				return err
			}
			return ErrNotHolder
		}},
		{"Fence", func(execID, _ string, stale Holder) error {
			if holds, err := runs.Fence(ctx, execID, stale); err != nil || holds {
				return fmt.Errorf("fence = %v, %w", holds, err)
			}
			return ErrNotHolder
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execID, a, b := newRun(t, runs, paused, runHeldByAnswerB)
			stale := heldAs(t, runs, execID, b)
			if _, _, err := runs.Retake(ctx, execID, b, "closed_for_restart"); err != nil {
				t.Fatalf("Retake: %v", err)
			}
			if err := tc.write(execID, a, stale); !errors.Is(err, ErrNotHolder) {
				t.Errorf("%s by the copy at epoch %d = %v, want ErrNotHolder", tc.name, stale.Epoch, err)
			}
			if status, by := runRow(t, runs, execID); status != RunHeld || by != b {
				t.Errorf("run = %s held by %q, want still held by the retaking copy", status, by)
			}
			if status, _ := pauseRow(t, paused, a); status != "paused" {
				t.Errorf("pause a = %s, want still paused", status)
			}
		})
	}
}

func TestRunRepo_APauseKeepsItsHeldRunFresh(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	execID, _, _ := newRun(t, runs, paused, runHeldByRequest)
	if _, err := db.ExecContext(ctx, `UPDATE workflow_checkpoint SET updated_at = 1 WHERE execution_id = ?`, execID); err != nil {
		t.Fatalf("age the run: %v", err)
	}
	p := samplePaused(execID + "-c")
	p.ExecutionID, p.TaskID = execID, "task-"+execID
	if _, err := runs.InsertPause(ctx, p, heldAs(t, runs, execID, HeldByRequest)); err != nil {
		t.Fatalf("InsertPause: %v", err)
	}
	if run, err := runs.Get(ctx, execID); err != nil || run.UpdatedAt <= 1 {
		t.Fatalf("run updated_at = %d (err %v), want it moved by the pause", run.UpdatedAt, err)
	}
}

func TestRunRepo_FenceOfARunWithNoRowHolds(t *testing.T) {
	runs := NewRunRepo(openTestDB(t))
	if holds, err := runs.Fence(t.Context(), idgen.New(), Holder{Key: HeldByRequest}); err != nil || !holds {
		t.Fatalf("Fence of a run with no row = %v, %v; want held", holds, err)
	}
}
