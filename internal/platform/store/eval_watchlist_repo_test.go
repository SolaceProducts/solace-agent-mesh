// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func agentNames(rows []*EvalAgentWatchlist) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.AgentName
	}
	return out
}

func positions(rows []*EvalAgentWatchlist) []int {
	out := make([]int, len(rows))
	for i, r := range rows {
		out[i] = r.Position
	}
	return out
}

func eq[T comparable](got, want []T) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestEvalWatchlistRepo_SetForUser(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalWatchlistRepo(db)
	ctx := t.Context()

	t.Run("creates_entries_with_position_index", func(t *testing.T) {
		rows, err := repo.SetForUser(ctx, "user-1", []string{"alpha", "beta", "gamma"})
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if !eq(agentNames(rows), []string{"alpha", "beta", "gamma"}) {
			t.Errorf("names = %v", agentNames(rows))
		}
		if !eq(positions(rows), []int{0, 1, 2}) {
			t.Errorf("positions = %v", positions(rows))
		}
		for _, r := range rows {
			if r.UserID != "user-1" {
				t.Errorf("user_id = %q, want user-1", r.UserID)
			}
		}
	})

	t.Run("replaces_previous_selection", func(t *testing.T) {
		if _, err := repo.SetForUser(ctx, "user-2", []string{"alpha", "beta"}); err != nil {
			t.Fatalf("set: %v", err)
		}
		rows, err := repo.SetForUser(ctx, "user-2", []string{"gamma"})
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if !eq(agentNames(rows), []string{"gamma"}) || rows[0].Position != 0 {
			t.Errorf("rows = %v", agentNames(rows))
		}
		listed, _ := repo.ListForUser(ctx, "user-2")
		if !eq(agentNames(listed), []string{"gamma"}) {
			t.Errorf("listed = %v", agentNames(listed))
		}
	})

	t.Run("empty_list_clears", func(t *testing.T) {
		if _, err := repo.SetForUser(ctx, "user-3", []string{"alpha"}); err != nil {
			t.Fatalf("set: %v", err)
		}
		rows, err := repo.SetForUser(ctx, "user-3", nil)
		if err != nil {
			t.Fatalf("set empty: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("expected empty, got %v", agentNames(rows))
		}
		listed, _ := repo.ListForUser(ctx, "user-3")
		if len(listed) != 0 {
			t.Errorf("expected empty list, got %v", agentNames(listed))
		}
	})

	t.Run("isolated_per_user", func(t *testing.T) {
		_, _ = repo.SetForUser(ctx, "iso-1", []string{"alpha"})
		_, _ = repo.SetForUser(ctx, "iso-2", []string{"beta"})
		_, _ = repo.SetForUser(ctx, "iso-1", []string{"gamma"})
		two, _ := repo.ListForUser(ctx, "iso-2")
		if !eq(agentNames(two), []string{"beta"}) {
			t.Errorf("iso-2 = %v, want [beta]", agentNames(two))
		}
	})

	t.Run("dedupes_with_contiguous_positions", func(t *testing.T) {
		rows, err := repo.SetForUser(ctx, "user-dup", []string{"alpha", "beta", "alpha"})
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if !eq(agentNames(rows), []string{"alpha", "beta"}) {
			t.Errorf("names = %v", agentNames(rows))
		}
		if !eq(positions(rows), []int{0, 1}) {
			t.Errorf("positions = %v", positions(rows))
		}
	})
}

func TestEvalWatchlistRepo_ListForUser(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalWatchlistRepo(db)
	ctx := t.Context()

	t.Run("orders_by_position", func(t *testing.T) {
		if _, err := repo.SetForUser(ctx, "ord-1", []string{"c", "a", "b"}); err != nil {
			t.Fatalf("set: %v", err)
		}
		rows, err := repo.ListForUser(ctx, "ord-1")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !eq(agentNames(rows), []string{"c", "a", "b"}) {
			t.Errorf("names = %v, want [c a b]", agentNames(rows))
		}
	})

	t.Run("unknown_user_empty", func(t *testing.T) {
		rows, err := repo.ListForUser(ctx, "ghost")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("expected empty, got %v", agentNames(rows))
		}
	})
}

func seedCompletedRun(ctx context.Context, t *testing.T, repo *EvalRunRepo, expID, groupID, model string, started int64, summary *string) {
	t.Helper()
	run := &EvalRun{
		ExperimentID:  expID,
		RunGroupID:    groupID,
		Status:        "completed",
		Model:         model,
		StartedTime:   started,
		SummaryScores: summary,
		StartedBy:     "u",
	}
	if err := repo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
}

func TestEvalRunRepo_TopExperimentsForAgent(t *testing.T) {
	db := TestDB(t)
	expRepo := NewEvalExperimentRepo(db)
	evRepo := NewEvalEvaluatorRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()

	evID := newTestEvaluator(ctx, t, evRepo, "Judge", "llm_judge")

	mk := func(name, agent string, primaryEval *string) string {
		exp := &EvalExperiment{Name: name, TargetAgent: agent, Models: `[{"modelConfigId":"general"}]`,
			PrimaryEvaluatorID: primaryEval, CreatedBy: "u", UpdatedBy: "u"}
		if err := expRepo.Create(ctx, exp, nil); err != nil {
			t.Fatalf("create exp %q: %v", name, err)
		}
		return exp.ID
	}

	e1 := mk("E1", "AgentA", &evID)
	e2 := mk("E2", "AgentA", &evID)
	e3 := mk("E3", "AgentA", nil)
	eB := mk("EB", "AgentB", &evID)
	eW := &EvalExperiment{Name: "EW", TargetAgent: "AgentA", TargetType: EvalTargetTypeWorkflow, Models: `[{"modelConfigId":"general"}]`,
		PrimaryEvaluatorID: &evID, CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, eW, nil); err != nil {
		t.Fatalf("create workflow exp: %v", err)
	}
	for i := range 3 {
		seedCompletedRun(ctx, t, runRepo, eW.ID, idgen.New(), "general", int64(10+i), nil)
	}

	seedCompletedRun(ctx, t, runRepo, e1, idgen.New(), "general", 100, nil)
	seedCompletedRun(ctx, t, runRepo, e1, idgen.New(), "general", 200, nil)
	seedCompletedRun(ctx, t, runRepo, e2, idgen.New(), "general", 300, nil)
	seedCompletedRun(ctx, t, runRepo, e3, idgen.New(), "general", 400, nil)
	seedCompletedRun(ctx, t, runRepo, eB, idgen.New(), "general", 500, nil)
	seedCompletedRun(ctx, t, runRepo, e2, idgen.New(), "other-model", 600, nil)
	pending := &EvalRun{ExperimentID: e1, RunGroupID: idgen.New(), Status: "pending", Model: "general", StartedTime: 700, StartedBy: "u"}
	if err := runRepo.Create(ctx, pending); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	ranked, err := runRepo.TopExperimentsForAgent(ctx, "AgentA", []string{"general"}, 3)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("want 2 ranked (E1, E2), got %d: %+v", len(ranked), ranked)
	}
	if ranked[0].ExperimentID != e1 || ranked[0].RunGroupCount != 2 {
		t.Errorf("rank[0] = (%s, count=%d), want (E1, 2)", ranked[0].ExperimentName, ranked[0].RunGroupCount)
	}
	if ranked[1].ExperimentID != e2 || ranked[1].RunGroupCount != 1 {
		t.Errorf("rank[1] = (%s, count=%d), want (E2, 1)", ranked[1].ExperimentName, ranked[1].RunGroupCount)
	}

	t.Run("empty_acceptable_models_returns_nil", func(t *testing.T) {
		got, err := runRepo.TopExperimentsForAgent(ctx, "AgentA", nil, 3)
		if err != nil || got != nil {
			t.Errorf("want (nil,nil), got (%v,%v)", got, err)
		}
	})
}

func TestEvalRunRepo_TopExperimentsForWorkflow(t *testing.T) {
	db := TestDB(t)
	expRepo := NewEvalExperimentRepo(db)
	evRepo := NewEvalEvaluatorRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()
	evID := newTestEvaluator(ctx, t, evRepo, "Workflow Path", "heuristic")

	mk := func(name, target, targetType string, groups int, primary *string) string {
		exp := &EvalExperiment{Name: name, TargetAgent: target, TargetType: targetType, Models: "[]",
			PrimaryEvaluatorID: primary, CreatedBy: "u", UpdatedBy: "u"}
		if err := expRepo.Create(ctx, exp, nil); err != nil {
			t.Fatalf("create exp %q: %v", name, err)
		}
		for i := range groups {
			seedCompletedRun(ctx, t, runRepo, exp.ID, idgen.New(), []string{"", "fast", "general"}[i%3], int64(100*i), nil)
		}
		return exp.ID
	}
	w4 := mk("W4", "OrderDesk", EvalTargetTypeWorkflow, 4, &evID)
	w3 := mk("W3", "OrderDesk", EvalTargetTypeWorkflow, 3, &evID)
	w2 := mk("W2", "OrderDesk", EvalTargetTypeWorkflow, 2, &evID)
	mk("W1", "OrderDesk", EvalTargetTypeWorkflow, 1, &evID)
	mk("Agent", "OrderDesk", EvalTargetTypeAgent, 9, &evID)
	mk("NoPrimary", "OrderDesk", EvalTargetTypeWorkflow, 9, nil)
	mk("Other", "Billing", EvalTargetTypeWorkflow, 9, &evID)
	if err := runRepo.Create(ctx, &EvalRun{ExperimentID: w2, RunGroupID: idgen.New(), Status: "running", StartedTime: 900, StartedBy: "u"}); err != nil {
		t.Fatalf("create running: %v", err)
	}
	if err := runRepo.Create(ctx, &EvalRun{ExperimentID: w2, RunGroupID: idgen.New(), Status: "completed_with_warnings", StartedTime: 901, StartedBy: "u"}); err != nil {
		t.Fatalf("create warned: %v", err)
	}

	for _, limit := range []int{3, 0} {
		ranked, err := runRepo.TopExperimentsForWorkflow(ctx, "OrderDesk", limit)
		if err != nil {
			t.Fatalf("rank: %v", err)
		}
		var got []string
		for _, rk := range ranked {
			got = append(got, rk.ExperimentID)
		}
		if want := []string{w4, w3, w2}; !eq(got, want) || ranked[2].RunGroupCount != 3 {
			t.Errorf("limit %d: ranked %+v, want W4, W3 and W2 with 3 groups", limit, ranked)
		}
	}
}

func TestEvalRunRepo_HasWorkflowExperiment(t *testing.T) {
	db := TestDB(t)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()
	for _, exp := range []*EvalExperiment{
		{Name: "flow", TargetAgent: "OrderDesk", TargetType: EvalTargetTypeWorkflow},
		{Name: "agent", TargetAgent: "Helper", TargetType: EvalTargetTypeAgent},
	} {
		exp.Models, exp.CreatedBy, exp.UpdatedBy = "[]", "u", "u"
		if err := expRepo.Create(ctx, exp, nil); err != nil {
			t.Fatalf("create exp %q: %v", exp.Name, err)
		}
	}

	for name, want := range map[string]bool{"OrderDesk": true, "orderdesk": false, "Helper": false, "Nobody": false} {
		got, err := runRepo.HasWorkflowExperiment(ctx, name)
		if err != nil {
			t.Fatalf("has workflow experiment %q: %v", name, err)
		}
		if got != want {
			t.Errorf("HasWorkflowExperiment(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestEvalRunRepo_LastRunGroupsForWorkflowExperiment(t *testing.T) {
	db := TestDB(t)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()
	exp := &EvalExperiment{Name: "Flow", TargetAgent: "OrderDesk", TargetType: EvalTargetTypeWorkflow, Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	ga, gb, gc := idgen.New(), idgen.New(), idgen.New()
	seedCompletedRun(ctx, t, runRepo, exp.ID, ga, "fast", 150, nil)
	seedCompletedRun(ctx, t, runRepo, exp.ID, ga, "", 100, nil)
	seedCompletedRun(ctx, t, runRepo, exp.ID, gb, "general", 200, nil)
	seedCompletedRun(ctx, t, runRepo, exp.ID, gc, "", 300, nil)

	runs, err := runRepo.LastRunGroupsForWorkflowExperiment(ctx, exp.ID, 10)
	if err != nil {
		t.Fatalf("last groups: %v", err)
	}
	var starts []int64
	for _, r := range runs {
		starts = append(starts, r.StartedTime)
	}
	if !slices.Equal(starts, []int64{100, 200, 300}) {
		t.Errorf("starts = %v, want one earliest run per group, oldest first", starts)
	}
	if runs, _ := runRepo.LastRunGroupsForWorkflowExperiment(ctx, exp.ID, 2); len(runs) != 2 || runs[0].RunGroupID != gb {
		t.Errorf("limit 2 = %+v, want the two most recent groups", runs)
	}
}

func TestEvalRunRepo_LastRunGroupsForExperiment(t *testing.T) {
	db := TestDB(t)
	expRepo := NewEvalExperimentRepo(db)
	runRepo := NewEvalRunRepo(db)
	ctx := t.Context()

	exp := &EvalExperiment{Name: "LastGroups", TargetAgent: "AgentA", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}

	ga, gb, gc := idgen.New(), idgen.New(), idgen.New()
	seedCompletedRun(ctx, t, runRepo, exp.ID, ga, "general", 100, nil)
	seedCompletedRun(ctx, t, runRepo, exp.ID, ga, "general", 150, nil)
	seedCompletedRun(ctx, t, runRepo, exp.ID, gb, "general", 200, nil)
	seedCompletedRun(ctx, t, runRepo, exp.ID, gb, "general", 250, nil)
	seedCompletedRun(ctx, t, runRepo, exp.ID, gc, "general", 300, nil)

	runs, err := runRepo.LastRunGroupsForExperiment(ctx, exp.ID, []string{"general"}, 10)
	if err != nil {
		t.Fatalf("last groups: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("want 3 (one per group), got %d", len(runs))
	}
	wantGroups := []string{ga, gb, gc}
	wantStarts := []int64{100, 200, 300}
	for i, run := range runs {
		if run.RunGroupID != wantGroups[i] || run.StartedTime != wantStarts[i] {
			t.Errorf("run[%d] = (%s, %d), want (%s, %d)", i, run.RunGroupID, run.StartedTime, wantGroups[i], wantStarts[i])
		}
	}

	t.Run("limit_trims_to_most_recent_then_asc", func(t *testing.T) {
		runs, err := runRepo.LastRunGroupsForExperiment(ctx, exp.ID, []string{"general"}, 2)
		if err != nil {
			t.Fatalf("last groups: %v", err)
		}
		if !eq([]string{runs[0].RunGroupID, runs[1].RunGroupID}, []string{gb, gc}) {
			t.Errorf("groups = %s,%s want gb,gc", runs[0].RunGroupID, runs[1].RunGroupID)
		}
	})

	t.Run("started_time_collision_yields_one_row_per_group", func(t *testing.T) {
		exp2 := &EvalExperiment{Name: "Collide", TargetAgent: "AgentA", Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
		if err := expRepo.Create(ctx, exp2, nil); err != nil {
			t.Fatalf("create exp: %v", err)
		}
		grp := idgen.New()
		seedCompletedRun(ctx, t, runRepo, exp2.ID, grp, "general", 500, nil)
		seedCompletedRun(ctx, t, runRepo, exp2.ID, grp, "general", 500, nil)
		seedCompletedRun(ctx, t, runRepo, exp2.ID, grp, "general", 500, nil)
		runs, err := runRepo.LastRunGroupsForExperiment(ctx, exp2.ID, []string{"general"}, 10)
		if err != nil {
			t.Fatalf("last groups: %v", err)
		}
		if len(runs) != 1 {
			t.Fatalf("want 1 row (group deduped), got %d", len(runs))
		}
		if runs[0].RunGroupID != grp {
			t.Errorf("group = %s, want %s", runs[0].RunGroupID, grp)
		}
	})
}

func TestEvalWatchlistRepo_ListPaginatedForUser(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalWatchlistRepo(db)
	ctx := t.Context()
	if _, err := repo.SetForUser(ctx, "user-1", []string{"a", "b", "c", "d", "e"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := repo.SetForUser(ctx, "user-2", []string{"x"}); err != nil {
		t.Fatalf("set: %v", err)
	}

	for _, tc := range []struct {
		name             string
		pageNumber, size int
		want             []string
	}{
		{"first_page", 1, 2, []string{"a", "b"}},
		{"last_partial_page", 3, 2, []string{"e"}},
		{"past_the_end", 4, 2, []string{}},
		{"whole_list", 1, 100, []string{"a", "b", "c", "d", "e"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, total, err := repo.ListPaginatedForUser(ctx, "user-1", tc.pageNumber, tc.size)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if total != 5 {
				t.Errorf("total = %d, want 5 whatever the page", total)
			}
			if !eq(agentNames(rows), tc.want) {
				t.Errorf("names = %v, want %v", agentNames(rows), tc.want)
			}
		})
	}

	t.Run("unknown_user_is_empty", func(t *testing.T) {
		rows, total, err := repo.ListPaginatedForUser(ctx, "nobody", 1, 10)
		if err != nil || total != 0 || len(rows) != 0 {
			t.Errorf("rows = %d, total = %d, err = %v, want none", len(rows), total, err)
		}
	})
}
