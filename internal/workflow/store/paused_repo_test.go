// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func samplePaused(requestID string) PausedWorkflow {
	execID := idgen.New()
	return PausedWorkflow{
		RequestID:     requestID,
		ExecutionID:   execID,
		WorkflowName:  "wf",
		NodeID:        "approve",
		RequesterAddr: "ns/sam/v1/notify/response/wf/" + requestID,
		StateSnapshot: `{"execution_id":"` + execID + `"}`,
		ExecContext:   `{"task_id":"t-1"}`,
		Routing:       `{"node_id":"approve","sub_task_id":"st-1"}`,
	}
}

func TestPausedRepo_InsertGetClaimFirstWins(t *testing.T) {
	ctx := t.Context()
	repo := NewPausedRepo(openTestDB(t))

	if err := repo.Insert(ctx, samplePaused("req-1")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, "req-1")
	if err != nil || got == nil {
		t.Fatalf("Get: row=%v err=%v", got, err)
	}
	if got.Status != "paused" || got.NodeID != "approve" || got.StateSnapshot == "" {
		t.Fatalf("unexpected row: %+v", got)
	}

	claimed, ok, err := repo.Claim(ctx, "req-1", "resumed")
	if err != nil || !ok || claimed == nil {
		t.Fatalf("first claim should win: ok=%v row=%v err=%v", ok, claimed, err)
	}
	if claimed.RequesterAddr != got.RequesterAddr {
		t.Errorf("claimed row mismatch: %+v", claimed)
	}

	if _, ok, err := repo.Claim(ctx, "req-1", "timed_out"); err != nil || ok {
		t.Fatalf("second claim must lose: ok=%v err=%v", ok, err)
	}

	if _, ok, err := repo.Claim(ctx, "nope", "resumed"); err != nil || ok {
		t.Fatalf("claim of unknown: ok=%v err=%v", ok, err)
	}
}

func TestPausedRepo_ExpiredAndCleanup(t *testing.T) {
	ctx := t.Context()
	repo := NewPausedRepo(openTestDB(t))

	mustInsert := func(id string, exp *int64) {
		p := samplePaused(id)
		p.ExpiresAt = exp
		if err := repo.Insert(ctx, p); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	past, future := int64(1000), int64(1<<62)
	mustInsert("past", &past)
	mustInsert("future", &future)
	mustInsert("none", nil)
	otherWorkflow := samplePaused("past-other-workflow")
	otherWorkflow.WorkflowName, otherWorkflow.ExpiresAt = "wf-other", &past
	if err := repo.Insert(ctx, otherWorkflow); err != nil {
		t.Fatalf("insert other workflow: %v", err)
	}

	exp, err := repo.Expired(ctx, "wf", 5000, 10)
	if err != nil {
		t.Fatalf("Expired: %v", err)
	}
	if len(exp) != 1 || exp[0].RequestID != "past" {
		t.Fatalf("only the past-deadline paused row should be expired, got %+v", exp)
	}

	if _, ok, cerr := repo.Claim(ctx, "past", "timed_out"); cerr != nil || !ok {
		t.Fatalf("claim past: ok=%v err=%v", ok, cerr)
	}
	if exp, _ := repo.Expired(ctx, "wf", 5000, 10); len(exp) != 0 {
		t.Fatalf("claimed row must not reappear as expired, got %+v", exp)
	}
	deleted, err := repo.DeleteResolvedOlderThan(ctx, 1<<62, 100)
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteResolvedOlderThan should reclaim the 1 resolved row, got %d (err=%v)", deleted, err)
	}
}

func TestPausedRepo_PausedForExecution(t *testing.T) {
	ctx := t.Context()
	repo := NewPausedRepo(openTestDB(t))

	execA, execB := idgen.New(), idgen.New()

	mine := samplePaused("req-mine")
	mine.ExecutionID = execA
	if err := repo.Insert(ctx, mine); err != nil {
		t.Fatalf("insert mine: %v", err)
	}
	answered := samplePaused("req-answered")
	answered.ExecutionID = execA
	if err := repo.Insert(ctx, answered); err != nil {
		t.Fatalf("insert answered: %v", err)
	}
	if _, _, err := repo.Claim(ctx, "req-answered", "resumed"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	other := samplePaused("req-other")
	other.ExecutionID = execB
	if err := repo.Insert(ctx, other); err != nil {
		t.Fatalf("insert other: %v", err)
	}

	rows, err := repo.PausedForExecution(ctx, execA)
	if err != nil {
		t.Fatalf("PausedForExecution: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestID != "req-mine" {
		got := make([]string, len(rows))
		for i := range rows {
			got[i] = rows[i].RequestID + "/" + rows[i].Status
		}
		t.Fatalf("rows = %v, want just the still-paused row of the first execution", got)
	}
	if rows[0].Routing == "" {
		t.Error("routing came back empty, so the record could not name the tool")
	}
}

func TestPausedRepo_ClaimAllForExecution(t *testing.T) {
	ctx := t.Context()
	repo := NewPausedRepo(openTestDB(t))

	mine, other := idgen.New(), idgen.New()

	stillPaused := samplePaused("req-paused")
	stillPaused.ExecutionID = mine
	if err := repo.Insert(ctx, stillPaused); err != nil {
		t.Fatalf("insert paused: %v", err)
	}
	alreadyAnswered := samplePaused("req-answered")
	alreadyAnswered.ExecutionID = mine
	if err := repo.Insert(ctx, alreadyAnswered); err != nil {
		t.Fatalf("insert answered: %v", err)
	}
	if _, _, err := repo.Claim(ctx, "req-answered", "resumed"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	elsewhere := samplePaused("req-other-run")
	elsewhere.ExecutionID = other
	if err := repo.Insert(ctx, elsewhere); err != nil {
		t.Fatalf("insert other: %v", err)
	}

	n, err := repo.ClaimAllForExecution(ctx, mine, "cancelled")
	if err != nil {
		t.Fatalf("ClaimAllForExecution: %v", err)
	}
	if n != 1 {
		t.Errorf("claimed %d rows, want 1 — only the still-paused row of this execution", n)
	}

	for _, tc := range []struct{ requestID, want string }{
		{"req-paused", "cancelled"},
		{"req-answered", "resumed"},
		{"req-other-run", "paused"},
	} {
		row, gerr := repo.Get(ctx, tc.requestID)
		if gerr != nil || row == nil {
			t.Fatalf("get %s: %v", tc.requestID, gerr)
		}
		if row.Status != tc.want {
			t.Errorf("%s status = %q, want %q", tc.requestID, row.Status, tc.want)
		}
	}
}

func TestPausedRepo_StatusesFor(t *testing.T) {
	ctx := t.Context()
	repo := NewPausedRepo(openTestDB(t))

	first := samplePaused("req-1")
	if err := repo.Insert(ctx, first); err != nil {
		t.Fatalf("insert: %v", err)
	}
	second := samplePaused("req-2")
	second.ExecutionID = first.ExecutionID
	if err := repo.Insert(ctx, second); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, _, err := repo.Claim(ctx, "req-2", "resumed"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	unrelated := samplePaused("req-not-asked-about")
	unrelated.ExecutionID = first.ExecutionID
	if err := repo.Insert(ctx, unrelated); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := repo.StatusesFor(ctx, []string{"req-1", "req-2", "req-never-existed"})
	if err != nil {
		t.Fatalf("StatusesFor: %v", err)
	}
	if _, present := got["req-not-asked-about"]; present {
		t.Error("the query answered for an id it was not asked about; the request filter is not doing anything")
	}
	if got["req-1"] != "paused" {
		t.Errorf("req-1 = %q, want paused", got["req-1"])
	}
	if got["req-2"] != "resumed" {
		t.Errorf("req-2 = %q, want resumed", got["req-2"])
	}
	if _, present := got["req-never-existed"]; present {
		t.Error("an id with no row came back present; the caller cannot then tell a vanished row from one it cannot read")
	}
	if len(got) != 2 {
		t.Errorf("got %d statuses for 3 ids, want 2", len(got))
	}
}

func TestPausedRepo_StatusesForChunksPastTheInClauseCap(t *testing.T) {
	ctx := t.Context()
	repo := NewPausedRepo(openTestDB(t))

	execID := idgen.New()
	const rowCount = dbutil.InClauseCap + 5
	ids := make([]string, 0, rowCount)
	for i := range rowCount {
		p := samplePaused(fmt.Sprintf("req-%04d", i))
		p.ExecutionID = execID
		if err := repo.Insert(ctx, p); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		ids = append(ids, p.RequestID)
	}

	got, err := repo.StatusesFor(ctx, ids)
	if err != nil {
		t.Fatalf("StatusesFor over %d ids: %v", len(ids), err)
	}
	if len(got) != rowCount {
		t.Errorf("got %d statuses for %d ids; a refused batch loses every record for the run, not just the tail", len(got), rowCount)
	}
}

func TestPausedRepo_StatusesForEmpty(t *testing.T) {
	repo := NewPausedRepo(openTestDB(t))
	got, err := repo.StatusesFor(t.Context(), nil)
	if err != nil {
		t.Fatalf("StatusesFor(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d statuses for no ids", len(got))
	}
}
