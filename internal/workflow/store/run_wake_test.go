// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const wakeNow int64 = 1_800_000_000_000

func parkedWithWake(t *testing.T, runs *RunRepo, workflowName string, wakeAt *int64) string {
	t.Helper()
	execID := idgen.New()
	if _, err := runs.Park(t.Context(), execID, Holder{Key: HeldByRequest}, RunState{
		TaskID: "task-" + execID, WorkflowName: workflowName,
		StateSnapshot: testState, ExecContext: testContext, WakeAt: wakeAt,
	}); err != nil {
		t.Fatalf("Park: %v", err)
	}
	return execID
}

func TestRunRepo_ParkedDueToWakeListsOnlyThisWorkflowsDueParkedRuns(t *testing.T) {
	db := openTestDB(t)
	runs := NewRunRepo(db)
	past, now, future := wakeNow-1, wakeNow, wakeNow+1

	duePast := parkedWithWake(t, runs, "wf", &past)
	dueNow := parkedWithWake(t, runs, "wf", &now)
	parkedWithWake(t, runs, "wf", &future)
	parkedWithWake(t, runs, "wf", nil)
	parkedWithWake(t, runs, "other-wf", &past)
	held := idgen.New()
	if _, err := runs.WriteStep(t.Context(), held, Holder{Key: HeldByRequest}, RunState{
		TaskID: "task-" + held, WorkflowName: "wf", StateSnapshot: testState, ExecContext: testContext, WakeAt: &past,
	}); err != nil {
		t.Fatalf("WriteStep: %v", err)
	}

	got, _, err := runs.ParkedDueToWake(t.Context(), "wf", wakeNow, WakeCursor{}, 10)
	if err != nil {
		t.Fatalf("ParkedDueToWake: %v", err)
	}
	want := []string{duePast, dueNow}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParkedDueToWake(wf, now) mismatch (-want +got):\n%s", diff)
	}
}

func TestRunRepo_WakeStateReturnsTheStatusAndWakeTimeOfARunOrNothingForAnUnknownRun(t *testing.T) {
	past := wakeNow - 1
	for _, tc := range []struct {
		name string
		run  string
		want *RunWake
	}{
		{name: "a parked run with a wake time", run: "parkedWithWake", want: &RunWake{Status: RunParked, WakeAt: &past}},
		{name: "a parked run without a wake time", run: "parkedWithoutWake", want: &RunWake{Status: RunParked}},
		{name: "a held run keeps the wake time it was stepped with", run: "held", want: &RunWake{Status: RunHeld, WakeAt: &past}},
		{name: "an execution id no run has", run: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := NewRunRepo(openTestDB(t))
			held := idgen.New()
			if _, err := runs.WriteStep(t.Context(), held, Holder{Key: HeldByRequest}, RunState{
				TaskID: "task-" + held, WorkflowName: "wf", StateSnapshot: testState, ExecContext: testContext, WakeAt: &past,
			}); err != nil {
				t.Fatalf("WriteStep: %v", err)
			}
			executions := map[string]string{
				"parkedWithWake":    parkedWithWake(t, runs, "wf", &past),
				"parkedWithoutWake": parkedWithWake(t, runs, "wf", nil),
				"held":              held,
				"unknown":           idgen.New(),
			}

			got, err := runs.WakeState(t.Context(), executions[tc.run])

			if err != nil {
				t.Fatalf("WakeState(%s): %v", tc.run, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("WakeState(%s) mismatch (-want +got):\n%s", tc.run, diff)
			}
		})
	}
}

func TestRunRepo_ParkStoresTheWakeTimeAndATakeoverKeepsItOffTheDueList(t *testing.T) {
	db := openTestDB(t)
	runs := NewRunRepo(db)
	past := wakeNow - 1
	execID := parkedWithWake(t, runs, "wf", &past)

	if _, took, err := runs.TakeOverParked(t.Context(), "wf", execID, "wake:"+execID); err != nil || !took {
		t.Fatalf("TakeOverParked = %v, %v; want it taken", took, err)
	}
	got, _, err := runs.ParkedDueToWake(t.Context(), "wf", wakeNow, WakeCursor{}, 10)
	if err != nil {
		t.Fatalf("ParkedDueToWake: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParkedDueToWake after the takeover = %v, want none", got)
	}
	run, err := runs.Get(t.Context(), execID)
	if err != nil || run == nil || run.WakeAt == nil || *run.WakeAt != past {
		t.Errorf("stored wake_at = %v (err %v), want %d", run, err, past)
	}
}

func TestRunRepo_ParkedDueToWakePagesByWakeTimeThenExecutionIdWithoutRepeatsOrGaps(t *testing.T) {
	const (
		runA = "00000000-0000-7000-8000-00000000000a"
		runB = "00000000-0000-7000-8000-00000000000b"
		runC = "00000000-0000-7000-8000-00000000000c"
		runD = "00000000-0000-7000-8000-00000000000d"
		runE = "00000000-0000-7000-8000-00000000000e"
		runY = "00000000-0000-7000-8000-0000000000f0"
		runZ = "00000000-0000-7000-8000-0000000000ff"
	)
	db := openTestDB(t)
	runs := NewRunRepo(db)
	for _, seed := range []struct {
		executionID  string
		workflowName string
		wakeAt       int64
	}{
		{runD, "wf", 200},
		{runA, "wf", 300},
		{runC, "wf", 100},
		{runE, "wf", 200},
		{runB, "wf", 200},
		{runZ, "wf", 400},
		{runY, "other-wf", 100},
	} {
		wakeAt := seed.wakeAt
		if _, err := runs.Park(t.Context(), seed.executionID, Holder{Key: HeldByRequest}, RunState{
			TaskID: "task-" + seed.executionID, WorkflowName: seed.workflowName,
			StateSnapshot: testState, ExecContext: testContext, WakeAt: &wakeAt,
		}); err != nil {
			t.Fatalf("Park(%s): %v", seed.executionID, err)
		}
	}

	var pages [][]string
	var cursor WakeCursor
	for range 10 {
		ids, next, err := runs.ParkedDueToWake(t.Context(), "wf", 300, cursor, 2)
		if err != nil {
			t.Fatalf("ParkedDueToWake: %v", err)
		}
		pages = append(pages, ids)
		cursor = next
		if len(ids) < 2 {
			break
		}
	}

	want := [][]string{{runC, runB}, {runD, runE}, {runA}}
	if diff := cmp.Diff(want, pages); diff != "" {
		t.Errorf("pages of ParkedDueToWake(wf, 300) mismatch (-want +got):\n%s", diff)
	}
}

func takenOverWithAWake(t *testing.T, runs *RunRepo) (string, Holder) {
	t.Helper()
	past := wakeNow - 1
	execID := parkedWithWake(t, runs, "wf", &past)
	holderKey := "wake:" + execID
	run, took, err := runs.TakeOverParked(t.Context(), "wf", execID, holderKey)
	if err != nil || !took {
		t.Fatalf("TakeOverParked = %v, %v; want it taken", took, err)
	}
	return execID, Holder{Key: holderKey, Epoch: run.HolderEpoch}
}

func TestRunRepo_ParkWithoutAWakeTimeClearsTheEarlierOneSoTheRunLeavesTheDueList(t *testing.T) {
	db := openTestDB(t)
	runs := NewRunRepo(db)
	execID, holder := takenOverWithAWake(t, runs)

	if _, err := runs.Park(t.Context(), execID, holder, RunState{
		TaskID: "task-" + execID, WorkflowName: "wf", StateSnapshot: testState, ExecContext: testContext,
	}); err != nil {
		t.Fatalf("Park: %v", err)
	}

	due, _, err := runs.ParkedDueToWake(t.Context(), "wf", wakeNow, WakeCursor{}, 10)
	if err != nil {
		t.Fatalf("ParkedDueToWake: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("ParkedDueToWake after parking without a wake time = %v, want none", due)
	}
	if run, err := runs.Get(t.Context(), execID); err != nil || run == nil || run.WakeAt != nil {
		t.Errorf("stored run after parking without a wake time = %+v (err %v), want wake_at NULL", run, err)
	}
}

func TestRunRepo_WriteStepWithoutAWakeTimeClearsTheEarlierOne(t *testing.T) {
	db := openTestDB(t)
	runs := NewRunRepo(db)
	execID, holder := takenOverWithAWake(t, runs)

	if _, err := runs.WriteStep(t.Context(), execID, holder, RunState{
		TaskID: "task-" + execID, WorkflowName: "wf", StateSnapshot: testState, ExecContext: testContext,
	}); err != nil {
		t.Fatalf("WriteStep: %v", err)
	}

	if run, err := runs.Get(t.Context(), execID); err != nil || run == nil || run.WakeAt != nil {
		t.Errorf("stored run after a step without a wake time = %+v (err %v), want wake_at NULL", run, err)
	}
}

type signalPauseRow struct {
	Execution string
	Status    string
	Answer    *string
}

func TestRunRepo_InsertSignalPauseKeepsAnAnsweredClosedRowOfItsOwnRunAndReplacesOtherClosedRows(t *testing.T) {
	const requestID = "signal:task-1:payment_received"
	answer := `{"status":"received","data":{"payer":"bob"}}`
	for _, tc := range []struct {
		name           string
		earlierStatus  string
		earlierAnswer  *string
		laterExecution string
		wantErr        bool
		wantKept       bool
		want           signalPauseRow
	}{
		{
			name:          "a closed row of the same run with an answer is kept and returned instead of a new row",
			earlierStatus: "resumed", earlierAnswer: &answer, laterExecution: "earlier",
			wantKept: true, want: signalPauseRow{Execution: "earlier", Status: "resumed", Answer: &answer},
		},
		{
			name:          "a closed row of another run with an answer is replaced",
			earlierStatus: "resumed", earlierAnswer: &answer, laterExecution: "other",
			want: signalPauseRow{Execution: "later", Status: StatusPaused},
		},
		{
			name:          "a closed row of the same run without an answer is replaced",
			earlierStatus: "closed_for_restart", laterExecution: "earlier",
			want: signalPauseRow{Execution: "later", Status: StatusPaused},
		},
		{
			name:          "an open row of another run is kept and the insert fails",
			earlierStatus: StatusPaused, laterExecution: "other",
			wantErr: true, want: signalPauseRow{Execution: "earlier", Status: StatusPaused},
		},
		{
			name:          "an open row of the same run holding an answer is kept and the insert fails",
			earlierStatus: StatusAnswered, earlierAnswer: &answer, laterExecution: "earlier",
			wantErr: true, want: signalPauseRow{Execution: "earlier", Status: StatusAnswered, Answer: &answer},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			runs, paused := NewRunRepo(db), NewPausedRepo(db)
			earlier := samplePaused(requestID)
			if _, _, err := runs.InsertSignalPause(t.Context(), earlier, Holder{Key: HeldByRequest}); err != nil {
				t.Fatalf("InsertSignalPause(earlier): %v", err)
			}
			if _, err := db.ExecContext(t.Context(),
				`UPDATE workflow_paused SET status = ?, answer = ? WHERE request_id = ?`, tc.earlierStatus, tc.earlierAnswer, requestID); err != nil {
				t.Fatalf("seed the earlier row: %v", err)
			}
			later := samplePaused(requestID)
			executions := map[string]string{"earlier": earlier.ExecutionID, "other": later.ExecutionID}
			later.ExecutionID = executions[tc.laterExecution]
			executions["later"] = later.ExecutionID

			_, kept, err := runs.InsertSignalPause(t.Context(), later, Holder{Key: HeldByRequest})

			if (err != nil) != tc.wantErr {
				t.Fatalf("InsertSignalPause(later) error = %v, want error %v", err, tc.wantErr)
			}
			row, getErr := paused.Get(t.Context(), requestID)
			if getErr != nil || row == nil {
				t.Fatalf("Get(%s) = %v, %v", requestID, row, getErr)
			}
			if (kept != nil) != tc.wantKept {
				t.Errorf("InsertSignalPause(later) returned the existing row = %v, want %v", kept != nil, tc.wantKept)
			}
			if kept != nil {
				if diff := cmp.Diff(row, kept); diff != "" {
					t.Errorf("returned row differs from the stored row (-stored +returned):\n%s", diff)
				}
			}
			got := signalPauseRow{Execution: row.ExecutionID, Status: row.Status, Answer: row.Answer}
			want := signalPauseRow{Execution: executions[tc.want.Execution], Status: tc.want.Status, Answer: tc.want.Answer}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("stored row after InsertSignalPause(later) mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunRepo_InsertPauseKeepsAClosedRowOfTheSameRequest(t *testing.T) {
	const requestID = "hil-request-1"
	db := openTestDB(t)
	runs, paused := NewRunRepo(db), NewPausedRepo(db)
	if _, err := runs.InsertPause(t.Context(), samplePaused(requestID), Holder{Key: HeldByRequest}); err != nil {
		t.Fatalf("InsertPause(earlier): %v", err)
	}
	if _, claimed, err := paused.Claim(t.Context(), requestID, "closed_for_restart"); err != nil || !claimed {
		t.Fatalf("Claim(earlier) = %v, %v", claimed, err)
	}
	if _, err := runs.InsertPause(t.Context(), samplePaused(requestID), Holder{Key: HeldByRequest}); err == nil {
		t.Error("InsertPause over a closed row of the same request succeeded, want the request id to stay taken")
	}
	if status, _ := pauseRow(t, paused, requestID); status != "closed_for_restart" {
		t.Errorf("earlier row = %s, want closed_for_restart", status)
	}
}
