// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestEvalRunResultRepo_ReadsARunLargerThanOneInClause(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	seed := seedEvalRun(t, db, "running")
	results := NewEvalRunResultRepo(db)
	n := dbutil.InClauseCap + 1
	rows := make([]*EvalRunResult, n)
	for i := range rows {
		rows[i] = &EvalRunResult{RunID: seed.runID, ExampleID: seed.exampleID, TrialIndex: i}
	}
	if err := results.BulkCreate(ctx, rows); err != nil {
		t.Fatal(err)
	}
	scores := make([]*EvalRunScore, n)
	for i, row := range rows {
		scores[i] = &EvalRunScore{RunResultID: row.ID, EvaluatorID: seed.evaluatorID, Score: 1}
	}
	if err := NewEvalRunScoreRepo(db).BulkCreate(ctx, scores); err != nil {
		t.Fatal(err)
	}

	byRun, err := results.ListByRun(ctx, seed.runID)
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	byRunIDs, err := results.ListByRunIDs(ctx, []string{seed.runID})
	if err != nil {
		t.Fatalf("ListByRunIDs: %v", err)
	}
	for name, got := range map[string][]*EvalRunResultWithScores{"ListByRun": byRun, "ListByRunIDs": byRunIDs} {
		if len(got) != n {
			t.Fatalf("%s returned %d results, want %d", name, len(got), n)
		}
		for _, r := range got {
			if len(r.Scores) != 1 || r.Scores[0].RunResultID != r.ID {
				t.Fatalf("%s result %s has scores %v, want its one score", name, r.ID, r.Scores)
			}
		}
	}
}
