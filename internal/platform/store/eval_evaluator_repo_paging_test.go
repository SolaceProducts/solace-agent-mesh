// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func createPagedEvaluator(ctx context.Context, t *testing.T, repo *EvalEvaluatorRepo, name string, created int64, isDefault bool) string {
	t.Helper()
	template, choices := "{{Response}}", `{"yes":1,"no":0}`
	e := &EvalEvaluator{
		Name: name, Type: "llm_judge", IsDefault: isDefault, PromptTemplate: &template, ChoiceScores: &choices,
		CreatedBy: "u", UpdatedBy: "u", CreatedTime: created, UpdatedTime: created,
	}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("create evaluator %q: %v", name, err)
	}
	return e.ID
}

func evaluatorRowNames(rows []*EvalEvaluatorWithCount) []string {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	return names
}

func TestEvalEvaluatorRepo_ListWithCountsPaginated(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalEvaluatorRepo(db)
	ctx := t.Context()

	base := time.Now().Add(time.Hour).UnixMilli()
	bravo := createPagedEvaluator(ctx, t, repo, "paged-bravo", base, false)
	createPagedEvaluator(ctx, t, repo, "Paged-alpha", base+1, false)
	createPagedEvaluator(ctx, t, repo, "paged-charlie", base+2, false)
	createPagedEvaluator(ctx, t, repo, "paged-built-in", base+3, true)

	everything, err := repo.ListWithCounts(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	total := len(everything)
	builtins := 0
	for _, e := range everything {
		if e.IsDefault {
			builtins++
		}
	}
	if builtins != 1 || total != 4 {
		t.Fatalf("%d evaluators with %d built-in, want 4 with 1", total, builtins)
	}

	t.Run("default_order_is_built_ins_then_oldest_first", func(t *testing.T) {
		rows, count, err := repo.ListWithCountsPaginated(ctx, 1, 100, "", false)
		if err != nil || count != total || len(rows) != total {
			t.Fatalf("got %d rows, count %d, err %v; want all %d", len(rows), count, err, total)
		}
		for i, r := range rows {
			if r.IsDefault != (i < builtins) {
				t.Fatalf("row %d is %q (built-in %t), want the built-ins first", i, r.Name, r.IsDefault)
			}
		}
		if got := evaluatorRowNames(rows); !slices.Equal(got, []string{"paged-built-in", "paged-bravo", "Paged-alpha", "paged-charlie"}) {
			t.Errorf("default order = %v, want the built-in first although it is the newest, then oldest first", got)
		}
	})

	t.Run("pages_concatenate_to_the_whole_list", func(t *testing.T) {
		var names []string
		for page := 1; ; page++ {
			rows, count, err := repo.ListWithCountsPaginated(ctx, page, 2, "", false)
			if err != nil || count != total {
				t.Fatalf("page %d: count %d, err %v; want the total %d on every page", page, count, err, total)
			}
			if len(rows) == 0 {
				break
			}
			if len(rows) > 2 {
				t.Fatalf("page %d has %d rows, want at most 2", page, len(rows))
			}
			names = append(names, evaluatorRowNames(rows)...)
		}
		whole, _, err := repo.ListWithCountsPaginated(ctx, 1, 100, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if want := evaluatorRowNames(whole); !slices.Equal(names, want) {
			t.Errorf("pages = %v, want %v", names, want)
		}
	})

	t.Run("sorts_by_name_ignoring_case", func(t *testing.T) {
		asc, _, err := repo.ListWithCountsPaginated(ctx, 1, 100, EvalEvaluatorSortName, false)
		if err != nil {
			t.Fatal(err)
		}
		names := evaluatorRowNames(asc)
		if !slices.IsSortedFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }) {
			t.Errorf("name ascending = %v, want case-insensitive order", names)
		}
		desc, _, err := repo.ListWithCountsPaginated(ctx, 1, 100, EvalEvaluatorSortName, true)
		if err != nil {
			t.Fatal(err)
		}
		slices.Reverse(names)
		if got := evaluatorRowNames(desc); !slices.Equal(got, names) {
			t.Errorf("name descending = %v, want the reverse %v", got, names)
		}
	})

	t.Run("sorts_by_created_time_without_favouring_built_ins", func(t *testing.T) {
		rows, _, err := repo.ListWithCountsPaginated(ctx, 1, 4, EvalEvaluatorSortCreatedTime, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := evaluatorRowNames(rows); !slices.Equal(got, []string{"paged-bravo", "Paged-alpha", "paged-charlie", "paged-built-in"}) {
			t.Errorf("oldest first = %v, want the built-in last as the newest", got)
		}
	})

	t.Run("a_page_past_the_end_is_empty_with_the_total", func(t *testing.T) {
		rows, count, err := repo.ListWithCountsPaginated(ctx, 50, 20, "", false)
		if err != nil || len(rows) != 0 || count != total {
			t.Errorf("got %d rows, count %d, err %v; want no rows and the total %d", len(rows), count, err, total)
		}
	})

	t.Run("counts_experiments", func(t *testing.T) {
		datasetID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "Paged DS")
		for _, name := range []string{"paged-exp-one", "paged-exp-two"} {
			exp := &EvalExperiment{Name: name, DatasetID: &datasetID, TargetAgent: "agent", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
			if err := NewEvalExperimentRepo(db).Create(ctx, exp, []string{bravo}); err != nil {
				t.Fatalf("create experiment: %v", err)
			}
		}
		if n, err := repo.CountExperiments(ctx, bravo); err != nil || n != 2 {
			t.Errorf("CountExperiments = %d, %v; want 2", n, err)
		}
		if n, err := repo.CountExperiments(ctx, idgen.New()); err != nil || n != 0 {
			t.Errorf("CountExperiments of an unknown id = %d, %v; want 0", n, err)
		}
		rows, _, err := repo.ListWithCountsPaginated(ctx, 1, 100, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			want := 0
			if r.ID == bravo {
				want = 2
			}
			if r.ExperimentCount != want {
				t.Errorf("%q experimentCount = %d, want %d", r.Name, r.ExperimentCount, want)
			}
		}
	})
}
