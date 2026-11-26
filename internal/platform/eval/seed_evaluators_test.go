// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestSeedWorkflowEvaluators(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewEvalEvaluatorRepo(db)
	ctx := t.Context()
	if err := SeedBuiltinEvaluators(ctx, repo, nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := SeedWorkflowEvaluators(ctx, repo); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	rows, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(builtinEvaluators)+2 {
		t.Errorf("%d evaluators, want the built-ins plus two", len(rows))
	}
	for heuristic, name := range map[string]string{heuristicWorkflowPath: "Workflow Path", heuristicOutputFacts: "Output Facts"} {
		var seeded []*store.EvalEvaluator
		for _, e := range rows {
			if e.HeuristicType != nil && *e.HeuristicType == heuristic {
				seeded = append(seeded, e)
			}
		}
		if len(seeded) != 1 || !seeded[0].IsDefault || seeded[0].Name != name {
			t.Errorf("%d %s rows (%+v); want one default %s", len(seeded), heuristic, seeded, name)
		}
	}
}

func TestSeedBuiltinEvaluatorsIdempotent(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewEvalEvaluatorRepo(db)
	ctx := context.Background()

	if serr := SeedBuiltinEvaluators(ctx, repo, nil); serr != nil {
		t.Fatalf("seed first: %v", serr)
	}
	first, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list 1: %v", err)
	}
	if len(first) != len(builtinEvaluators) {
		t.Fatalf("first seed count = %d, want %d", len(first), len(builtinEvaluators))
	}

	if serr := SeedBuiltinEvaluators(ctx, repo, nil); serr != nil {
		t.Fatalf("seed second: %v", serr)
	}
	second, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list 2: %v", err)
	}
	if len(second) != len(first) {
		t.Fatalf("second seed count = %d, want %d", len(second), len(first))
	}

	byName := make(map[string]*store.EvalEvaluator, len(second))
	for _, e := range second {
		byName[e.Name] = e
	}
	for _, name := range []string{
		"Factuality", "Closed QA", "Valid JSON", "JSON Diff",
		"Response Match", "Levenshtein", "LLM Judge",
	} {
		ev, ok := byName[name]
		if !ok {
			t.Fatalf("missing seed evaluator %q", name)
		}
		if !ev.IsDefault {
			t.Fatalf("seed %q: IsDefault must be true", name)
		}
		if ev.CreatedBy != "system" {
			t.Fatalf("seed %q: CreatedBy = %q", name, ev.CreatedBy)
		}
	}
}
