// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type fakeAgentModels struct {
	providers map[string]string
}

func (f fakeAgentModels) ModelProviderForAgent(_ context.Context, agentName string) (string, bool, error) {
	p, ok := f.providers[agentName]
	return p, ok, nil
}

type fakeModelConfig struct {
	byKey map[string][]string
}

func (f fakeModelConfig) AcceptableModelIdentifiers(_ context.Context, aliasOrID string) ([]string, error) {
	return f.byKey[aliasOrID], nil
}

func TestWatchlistService_Set_DedupThenCap(t *testing.T) {
	db := store.TestDB(t)
	svc := NewWatchlistService(WatchlistServiceParams{
		Watchlist:   store.NewEvalWatchlistRepo(db),
		Runs:        store.NewEvalRunRepo(db),
		Evaluators:  store.NewEvalEvaluatorRepo(db),
		AgentModels: fakeAgentModels{},
		ModelConfig: fakeModelConfig{},
	})
	ctx := t.Context()

	t.Run("six_names_one_dup_accepted", func(t *testing.T) {
		entries, err := svc.Set(ctx, "u1", []string{"a", "b", "c", "d", "e", "a"})
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if len(entries) != 5 {
			t.Fatalf("want 5 entries, got %d", len(entries))
		}
	})

	t.Run("six_unique_rejected", func(t *testing.T) {
		_, err := svc.Set(ctx, "u2", []string{"a", "b", "c", "d", "e", "f"})
		if err == nil {
			t.Fatal("expected ValidationError for 6 unique agents")
		}
		if _, ok := errors.AsType[*platpkg.ValidationError](err); !ok {
			t.Errorf("want ValidationError, got %T", err)
		}
	})
}

func TestWatchlistService_List_ResolvesEntries(t *testing.T) {
	db := store.TestDB(t)
	svc := NewWatchlistService(WatchlistServiceParams{
		Watchlist:  store.NewEvalWatchlistRepo(db),
		Runs:       store.NewEvalRunRepo(db),
		Evaluators: store.NewEvalEvaluatorRepo(db),
		AgentModels: fakeAgentModels{providers: map[string]string{
			"DBAgent":    "planning",
			"EmptyAgent": "",
		}},
		ModelConfig: fakeModelConfig{},
	})
	ctx := t.Context()

	if _, err := svc.Set(ctx, "u1", []string{"DBAgent", "EmptyAgent", "BuiltIn"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	entries, err := svc.List(ctx, "u1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := map[string]WatchlistEntryResponse{}
	for _, e := range entries {
		byName[e.AgentName] = e
	}

	for name, e := range byName {
		if e.TargetType != store.EvalTargetTypeAgent {
			t.Errorf("%s targetType = %q, want agent", name, e.TargetType)
		}
	}
	if e := byName["DBAgent"]; e.ResolvedModelProvider != "planning" || e.IsBuiltIn {
		t.Errorf("DBAgent = %+v, want provider=planning isBuiltIn=false", e)
	}
	if e := byName["EmptyAgent"]; e.ResolvedModelProvider != defaultAgentModelProvider || e.IsBuiltIn {
		t.Errorf("EmptyAgent = %+v, want provider=general isBuiltIn=false", e)
	}
	if e := byName["BuiltIn"]; e.ResolvedModelProvider != defaultAgentModelProvider || !e.IsBuiltIn {
		t.Errorf("BuiltIn = %+v, want provider=general isBuiltIn=true", e)
	}
}

func TestWatchlistService_ChartData(t *testing.T) {
	db := store.TestDB(t)
	watchRepo := store.NewEvalWatchlistRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	ctx := t.Context()

	judge := &store.EvalEvaluator{Name: "Judge", Type: "llm_judge", CreatedBy: "u", UpdatedBy: "u"}
	tmpl := "tpl"
	cs := `{"Yes":1.0}`
	judge.PromptTemplate = &tmpl
	judge.ChoiceScores = &cs
	if err := evRepo.Create(ctx, judge); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}

	mkExp := func(name, modelsJSON string) string {
		exp := &store.EvalExperiment{Name: name, TargetAgent: "AgentA", Models: modelsJSON,
			PrimaryEvaluatorID: &judge.ID, CreatedBy: "u", UpdatedBy: "u"}
		if err := expRepo.Create(ctx, exp, nil); err != nil {
			t.Fatalf("create exp %q: %v", name, err)
		}
		return exp.ID
	}
	e1 := mkExp("E1", `[{"modelConfigId":"general"}]`)
	e2 := mkExp("E2", `[{"modelConfigId":"other"}]`)

	scored := fmt.Sprintf(`{%q: 0.8}`, judge.ID)
	mkRun := func(expID, group string, started int64, summary *string) {
		run := &store.EvalRun{ExperimentID: expID, RunGroupID: group, Status: "completed",
			Model: "general", StartedTime: started, SummaryScores: summary, StartedBy: "u"}
		if err := runRepo.Create(ctx, run); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	g1, g2, g3 := idgen.New(), idgen.New(), idgen.New()
	mkRun(e1, g1, 100, &scored)
	mkRun(e1, g2, 200, nil)
	mkRun(e2, g3, 300, &scored)

	if _, err := watchRepo.SetForUser(ctx, "u1", []string{"AgentA"}); err != nil {
		t.Fatalf("set watchlist: %v", err)
	}

	svc := NewWatchlistService(WatchlistServiceParams{
		Watchlist:   watchRepo,
		Runs:        runRepo,
		Evaluators:  evRepo,
		AgentModels: fakeAgentModels{providers: map[string]string{"AgentA": "general"}},
		ModelConfig: fakeModelConfig{byKey: map[string][]string{"general": {"general-id", "general"}}},
	})

	charts, err := svc.ChartData(ctx, "u1")
	if err != nil {
		t.Fatalf("chart data: %v", err)
	}
	if len(charts) != 1 {
		t.Fatalf("want 1 agent chart, got %d", len(charts))
	}
	chart := charts[0]
	if chart.AgentName != "AgentA" || chart.ResolvedModelProvider != "general" {
		t.Errorf("chart header = %+v", chart)
	}
	if len(chart.Experiments) != 1 || chart.Experiments[0].ExperimentID != e1 {
		t.Fatalf("want only E1, got %+v", chart.Experiments)
	}
	series := chart.Experiments[0]
	if series.PrimaryEvaluatorName == nil || *series.PrimaryEvaluatorName != "Judge" {
		t.Errorf("primaryEvaluatorName = %v, want Judge", series.PrimaryEvaluatorName)
	}
	if len(series.Points) != 2 {
		t.Fatalf("want 2 points, got %d", len(series.Points))
	}
	if series.Points[0].RunGroupID != g1 || series.Points[0].Score == nil || *series.Points[0].Score != 0.8 {
		t.Errorf("point[0] = %+v, want g1 score 0.8", series.Points[0])
	}
	if series.Points[1].RunGroupID != g2 || series.Points[1].Score != nil {
		t.Errorf("point[1] = %+v, want g2 score nil", series.Points[1])
	}
	_ = e2
}

func TestExperimentTargetsModel(t *testing.T) {
	acc := []string{"general", "general-id"}
	cases := []struct {
		name   string
		models string
		want   bool
	}{
		{"modelConfigId match", `[{"modelConfigId":"general"}]`, true},
		{"legacy model key match", `[{"model":"general-id"}]`, true},
		{"no match", `[{"modelConfigId":"other"}]`, false},
		{"empty json", "", false},
		{"malformed json", `not json`, false},
		{"empty array", `[]`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := experimentTargetsModel(c.models, acc, "exp-1", nil); got != c.want {
				t.Errorf("experimentTargetsModel(%q) = %v, want %v", c.models, got, c.want)
			}
		})
	}
	t.Run("empty acceptable", func(t *testing.T) {
		if experimentTargetsModel(`[{"modelConfigId":"general"}]`, nil, "exp-1", nil) {
			t.Error("want false when acceptable is empty")
		}
	})
}

func TestExtractScore(t *testing.T) {
	js := func(s string) *string { return &s }
	id := "eval-1"
	cases := []struct {
		name    string
		summary *string
		want    *float64
	}{
		{"present", js(`{"eval-1": 0.75}`), ptrFloat(0.75)},
		{"missing key", js(`{"other": 1.0}`), nil},
		{"nil summary", nil, nil},
		{"empty string", js(""), nil},
		{"malformed", js(`nope`), nil},
		{"non-numeric", js(`{"eval-1": "high"}`), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractScore(c.summary, id, "rg-1", nil)
			switch {
			case got == nil && c.want == nil:
			case got != nil && c.want != nil && *got == *c.want:
			default:
				t.Errorf("extractScore = %v, want %v", got, c.want)
			}
		})
	}
}

func ptrFloat(f float64) *float64 { return &f }

type fakeWatchlistWorkflows []string

func (f fakeWatchlistWorkflows) ResolveWorkflow(_ context.Context, name string) (*WorkflowTarget, error) {
	fold := func(n string) string { return strings.ToLower(strings.ReplaceAll(n, " ", "")) }
	for _, wf := range f {
		if fold(wf) == fold(name) {
			return &WorkflowTarget{Name: wf, InstanceName: wf}, nil
		}
	}
	return nil, nil
}

type failingWatchlistWorkflows struct{}

func (failingWatchlistWorkflows) ResolveWorkflow(context.Context, string) (*WorkflowTarget, error) {
	return nil, errors.New("registry unavailable")
}

func TestWatchlistService_WhatAWatchedNameIs(t *testing.T) {
	db := store.TestDB(t)
	watchRepo := store.NewEvalWatchlistRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	ctx := t.Context()

	judge := &store.EvalEvaluator{Name: "Workflow Path", Type: "heuristic", CreatedBy: "u", UpdatedBy: "u"}
	if err := evRepo.Create(ctx, judge); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	flow := &store.EvalExperiment{Name: "order-desk-flow", TargetAgent: "OrderDesk", TargetType: store.EvalTargetTypeWorkflow,
		Models: `[{"modelConfigId":"general"}]`, PrimaryEvaluatorID: &judge.ID, CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, flow, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	scored := fmt.Sprintf(`{%q: 0.5}`, judge.ID)
	if err := runRepo.Create(ctx, &store.EvalRun{ExperimentID: flow.ID, RunGroupID: idgen.New(), Status: "completed",
		Model: "general", StartedTime: 100, SummaryScores: &scored, StartedBy: "u"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := watchRepo.SetForUser(ctx, "u1", []string{"OrderDesk"}); err != nil {
		t.Fatalf("set watchlist: %v", err)
	}
	if _, err := watchRepo.SetForUser(ctx, "u2", []string{"NewFlow"}); err != nil {
		t.Fatalf("set watchlist: %v", err)
	}
	agentOnly := &store.EvalExperiment{Name: "agent-only", TargetAgent: "Helper", TargetType: store.EvalTargetTypeAgent,
		Models: "[]", CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, agentOnly, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	if _, err := watchRepo.SetForUser(ctx, "u3", []string{"Helper"}); err != nil {
		t.Fatalf("set watchlist: %v", err)
	}
	service := func(workflows WorkflowTargetResolver) *WatchlistService {
		return NewWatchlistService(WatchlistServiceParams{
			Watchlist: watchRepo, Runs: runRepo, Evaluators: evRepo, Workflows: workflows,
			AgentModels: fakeAgentModels{providers: map[string]string{"OrderDesk": "general", "Helper": "general"}},
			ModelConfig: fakeModelConfig{},
		})
	}
	chartOf := func(t *testing.T, svc *WatchlistService, user string) WatchlistAgentChartResponse {
		t.Helper()
		charts, err := svc.ChartData(ctx, user)
		if err != nil || len(charts) != 1 {
			t.Fatalf("charts = %+v, %v", charts, err)
		}
		return charts[0]
	}

	t.Run("a deleted workflow with experiments, also named as an agent", func(t *testing.T) {
		chart := chartOf(t, service(fakeWatchlistWorkflows{}), "u1")
		if chart.TargetType != store.EvalTargetTypeWorkflow || len(chart.Experiments) != 1 || chart.Experiments[0].ExperimentID != flow.ID {
			t.Errorf("chart = %+v, want the workflow with its experiment", chart)
		}
	})
	t.Run("a resolvable workflow with no experiments yet", func(t *testing.T) {
		chart := chartOf(t, service(fakeWatchlistWorkflows{"NewFlow"}), "u2")
		if chart.TargetType != store.EvalTargetTypeWorkflow || len(chart.Experiments) != 0 || chart.ResolvedModelProvider != "" {
			t.Errorf("chart = %+v, want an empty workflow chart", chart)
		}
	})
	t.Run("an agent's experiments keep it an agent", func(t *testing.T) {
		if chart := chartOf(t, service(fakeWatchlistWorkflows{}), "u3"); chart.TargetType != store.EvalTargetTypeAgent {
			t.Errorf("chart = %+v, want an agent", chart)
		}
	})
	t.Run("a workflow named the same but for case and spacing leaves the agent an agent", func(t *testing.T) {
		chart := chartOf(t, service(fakeWatchlistWorkflows{"hel per"}), "u3")
		if chart.TargetType != store.EvalTargetTypeAgent || chart.ResolvedModelProvider != "general" {
			t.Errorf("chart = %+v, want the agent Helper under general", chart)
		}
	})
	t.Run("a known agent that is also a workflow of exactly its name is the workflow", func(t *testing.T) {
		chart := chartOf(t, service(fakeWatchlistWorkflows{"Helper"}), "u3")
		if chart.TargetType != store.EvalTargetTypeWorkflow || chart.ResolvedModelProvider != "" {
			t.Errorf("chart = %+v, want the workflow Helper", chart)
		}
	})
	t.Run("a workflow watched through Set reads as one on every page", func(t *testing.T) {
		svc := service(fakeWatchlistWorkflows{"NewFlow"})
		set, err := svc.Set(ctx, "u4", []string{"NewFlow"})
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		listed, _, err := svc.ListPaginated(ctx, "u4", 1, 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		charts, _, err := svc.ChartDataPaginated(ctx, "u4", 1, 10)
		if err != nil || len(charts) != 1 {
			t.Fatalf("charts = %+v, %v", charts, err)
		}
		for name, e := range map[string]WatchlistEntryResponse{"set": set[0], "list": listed[0]} {
			if e.TargetType != store.EvalTargetTypeWorkflow || e.ResolvedModelProvider != "" || e.IsBuiltIn {
				t.Errorf("%s entry = %+v, want the workflow NewFlow with no provider", name, e)
			}
		}
		if c := charts[0]; c.TargetType != store.EvalTargetTypeWorkflow || c.ResolvedModelProvider != "" || c.IsBuiltIn {
			t.Errorf("chart = %+v, want the workflow NewFlow with no provider", c)
		}
	})
	t.Run("a resolver failure fails the read", func(t *testing.T) {
		if _, err := service(failingWatchlistWorkflows{}).List(ctx, "u2"); err == nil || !strings.Contains(err.Error(), "resolve workflow") {
			t.Errorf("list error = %v, want the resolver's error", err)
		}
	})
	t.Run("a failure to find workflow experiments fails the read", func(t *testing.T) {
		closed, err := store.Open(store.TestDBURL(t), nil)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		if err := closed.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
		svc := NewWatchlistService(WatchlistServiceParams{
			Watchlist: watchRepo, Runs: store.NewEvalRunRepo(closed), Evaluators: evRepo,
			Workflows:   fakeWatchlistWorkflows{"OrderDesk"},
			AgentModels: fakeAgentModels{}, ModelConfig: fakeModelConfig{},
		})
		const want = "find workflow experiments for"
		if _, err := svc.List(ctx, "u1"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("list error = %v, want %q", err, want)
		}
		if _, err := svc.ChartData(ctx, "u1"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("chart error = %v, want %q", err, want)
		}
	})
	t.Run("with workflow evaluations off the name is an agent", func(t *testing.T) {
		chart := chartOf(t, service(nil), "u1")
		if chart.TargetType != store.EvalTargetTypeAgent || len(chart.Experiments) != 0 {
			t.Errorf("chart = %+v, want an agent chart without the workflow's experiment", chart)
		}
	})
}

func TestWatchlistService_ChartData_Workflow(t *testing.T) {
	db := store.TestDB(t)
	watchRepo := store.NewEvalWatchlistRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	ctx := t.Context()

	path := &store.EvalEvaluator{Name: "Workflow Path", Type: "heuristic", CreatedBy: "u", UpdatedBy: "u"}
	if err := evRepo.Create(ctx, path); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{Name: "order-desk-paths", TargetAgent: "OrderDesk", TargetType: store.EvalTargetTypeWorkflow,
		Models: "[]", PrimaryEvaluatorID: &path.ID, CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	sameName := &store.EvalExperiment{Name: "order-desk-agent", TargetAgent: "OrderDesk", TargetType: store.EvalTargetTypeAgent,
		Models: "[]", PrimaryEvaluatorID: &path.ID, CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, sameName, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	scored := fmt.Sprintf(`{%q: 0.75}`, path.ID)
	for i, run := range []*store.EvalRun{
		{ExperimentID: exp.ID, Model: ""}, {ExperimentID: exp.ID, Model: "fast"},
		{ExperimentID: sameName.ID, Model: ""}, {ExperimentID: sameName.ID, Model: ""}, {ExperimentID: sameName.ID, Model: ""},
	} {
		run.RunGroupID, run.Status, run.StartedTime, run.SummaryScores, run.StartedBy = idgen.New(), "completed", int64(100*(i+1)), &scored, "u"
		if err := runRepo.Create(ctx, run); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	if _, err := watchRepo.SetForUser(ctx, "u1", []string{"OrderDesk"}); err != nil {
		t.Fatalf("set watchlist: %v", err)
	}
	svc := NewWatchlistService(WatchlistServiceParams{
		Watchlist: watchRepo, Runs: runRepo, Evaluators: evRepo,
		Workflows:   fakeWatchlistWorkflows{"OrderDesk"},
		AgentModels: fakeAgentModels{},
		ModelConfig: fakeModelConfig{},
	})

	entries, err := svc.List(ctx, "u1")
	if err != nil || len(entries) != 1 || entries[0].TargetType != store.EvalTargetTypeWorkflow || entries[0].ResolvedModelProvider != "" {
		t.Fatalf("entries = %+v, %v; want the workflow with no model provider", entries, err)
	}
	charts, err := svc.ChartData(ctx, "u1")
	if err != nil || len(charts) != 1 {
		t.Fatalf("charts = %+v, %v", charts, err)
	}
	if charts[0].TargetType != store.EvalTargetTypeWorkflow || len(charts[0].Experiments) != 1 ||
		charts[0].Experiments[0].ExperimentID != exp.ID || len(charts[0].Experiments[0].Points) != 2 {
		t.Fatalf("chart = %+v, want the workflow's experiment with both runs whatever their model", charts[0])
	}
}

func TestWatchlistService_ChartData_MultipleAgentsByPosition(t *testing.T) {
	db := store.TestDB(t)
	watchRepo := store.NewEvalWatchlistRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	ctx := t.Context()

	judge := &store.EvalEvaluator{Name: "Judge", Type: "llm_judge", CreatedBy: "u", UpdatedBy: "u"}
	tmpl, cs := "tpl", `{"Yes":1.0}`
	judge.PromptTemplate, judge.ChoiceScores = &tmpl, &cs
	if err := evRepo.Create(ctx, judge); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}

	mkExpRun := func(agent string) {
		exp := &store.EvalExperiment{Name: "E-" + agent, TargetAgent: agent,
			Models:             `[{"modelConfigId":"general"}]`,
			PrimaryEvaluatorID: &judge.ID, CreatedBy: "u", UpdatedBy: "u"}
		if err := expRepo.Create(ctx, exp, nil); err != nil {
			t.Fatalf("create exp for %s: %v", agent, err)
		}
		run := &store.EvalRun{ExperimentID: exp.ID, RunGroupID: idgen.New(), Status: "completed",
			Model: "general", StartedTime: 100, StartedBy: "u"}
		if err := runRepo.Create(ctx, run); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	mkExpRun("AgentZ")
	mkExpRun("AgentA")
	if _, err := watchRepo.SetForUser(ctx, "u1", []string{"AgentZ", "AgentA"}); err != nil {
		t.Fatalf("set watchlist: %v", err)
	}

	svc := NewWatchlistService(WatchlistServiceParams{
		Watchlist:   watchRepo,
		Runs:        runRepo,
		Evaluators:  evRepo,
		AgentModels: fakeAgentModels{providers: map[string]string{"AgentA": "general", "AgentZ": "general"}},
		ModelConfig: fakeModelConfig{byKey: map[string][]string{"general": {"general-id", "general"}}},
	})
	charts, err := svc.ChartData(ctx, "u1")
	if err != nil {
		t.Fatalf("chart data: %v", err)
	}
	if len(charts) != 2 {
		t.Fatalf("want 2 charts, got %d", len(charts))
	}
	if charts[0].AgentName != "AgentZ" || charts[0].Position != 0 {
		t.Errorf("position 0 = (%q, %d), want (AgentZ, 0)", charts[0].AgentName, charts[0].Position)
	}
	if charts[1].AgentName != "AgentA" || charts[1].Position != 1 {
		t.Errorf("position 1 = (%q, %d), want (AgentA, 1)", charts[1].AgentName, charts[1].Position)
	}
}

func TestWatchlistService_ChartData_MatchesAcrossAliasAndUUID(t *testing.T) {
	db := store.TestDB(t)
	watchRepo := store.NewEvalWatchlistRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	ctx := t.Context()

	judge := &store.EvalEvaluator{Name: "Judge", Type: "llm_judge", CreatedBy: "u", UpdatedBy: "u"}
	tmpl, cs := "tpl", `{"Yes":1.0}`
	judge.PromptTemplate, judge.ChoiceScores = &tmpl, &cs
	if err := evRepo.Create(ctx, judge); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}

	exp := &store.EvalExperiment{Name: "ByUUID", TargetAgent: "AgentA",
		Models:             `[{"modelConfigId":"general-uuid"}]`,
		PrimaryEvaluatorID: &judge.ID, CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, RunGroupID: idgen.New(), Status: "completed",
		Model: "general-uuid", StartedTime: 100, StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := watchRepo.SetForUser(ctx, "u1", []string{"AgentA"}); err != nil {
		t.Fatalf("set watchlist: %v", err)
	}

	svc := NewWatchlistService(WatchlistServiceParams{
		Watchlist: watchRepo, Runs: runRepo, Evaluators: evRepo,
		AgentModels: fakeAgentModels{providers: map[string]string{"AgentA": "general"}},
		ModelConfig: fakeModelConfig{byKey: map[string][]string{"general": {"general-uuid", "general"}}},
	})
	charts, err := svc.ChartData(ctx, "u1")
	if err != nil {
		t.Fatalf("chart data: %v", err)
	}
	if len(charts) != 1 || len(charts[0].Experiments) != 1 {
		t.Fatalf("want 1 chart with 1 experiment, got %+v", charts)
	}
	if charts[0].Experiments[0].ExperimentID != exp.ID {
		t.Errorf("experiment id = %q, want %q (alias→UUID cross-match failed)",
			charts[0].Experiments[0].ExperimentID, exp.ID)
	}
}

func TestWatchlistService_ChartData_FallsBackToLiteralWhenModelConfigUnknown(t *testing.T) {
	db := store.TestDB(t)
	watchRepo := store.NewEvalWatchlistRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	ctx := t.Context()

	judge := &store.EvalEvaluator{Name: "Judge", Type: "llm_judge", CreatedBy: "u", UpdatedBy: "u"}
	tmpl, cs := "tpl", `{"Yes":1.0}`
	judge.PromptTemplate, judge.ChoiceScores = &tmpl, &cs
	if err := evRepo.Create(ctx, judge); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}

	exp := &store.EvalExperiment{Name: "Literal", TargetAgent: "AgentA",
		Models:             `[{"modelConfigId":"orphan-alias"}]`,
		PrimaryEvaluatorID: &judge.ID, CreatedBy: "u", UpdatedBy: "u"}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, RunGroupID: idgen.New(), Status: "completed",
		Model: "orphan-alias", StartedTime: 100, StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := watchRepo.SetForUser(ctx, "u1", []string{"AgentA"}); err != nil {
		t.Fatalf("set watchlist: %v", err)
	}

	svc := NewWatchlistService(WatchlistServiceParams{
		Watchlist: watchRepo, Runs: runRepo, Evaluators: evRepo,
		AgentModels: fakeAgentModels{providers: map[string]string{"AgentA": "orphan-alias"}},
		ModelConfig: fakeModelConfig{},
	})
	charts, err := svc.ChartData(ctx, "u1")
	if err != nil {
		t.Fatalf("chart data: %v", err)
	}
	if len(charts) != 1 || len(charts[0].Experiments) != 1 {
		t.Fatalf("want 1 chart with 1 experiment (literal fallback), got %+v", charts)
	}
}

func TestResolveAcceptableModels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		provider    string
		identifiers []string
		err         error
		want        []string
		wantErr     bool
	}{
		{
			name:        "happy_path_alias_with_uuid_pair",
			provider:    "general",
			identifiers: []string{"general-uuid", "general"},
			want:        []string{"general", "general-uuid"},
		},
		{
			name:        "unknown_alias_falls_back_to_literal_only",
			provider:    "orphan",
			identifiers: nil,
			want:        []string{"orphan"},
		},
		{
			name:        "blank_identifiers_are_skipped",
			provider:    "general",
			identifiers: []string{"", "general-uuid"},
			want:        []string{"general", "general-uuid"},
		},
		{
			name:        "blank_provider_yields_only_resolver_output",
			provider:    "",
			identifiers: []string{"some-uuid"},
			want:        []string{"some-uuid"},
		},
		{
			name:    "resolver_error_propagates",
			err:     errors.New("config lookup boom"),
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			svc := &WatchlistService{
				modelConfig: erroringModelConfig{ids: c.identifiers, err: c.err},
			}
			got, err := svc.resolveAcceptableModels(t.Context(), c.provider)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (out=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !eqStrSlice(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

type erroringModelConfig struct {
	ids []string
	err error
}

func (e erroringModelConfig) AcceptableModelIdentifiers(_ context.Context, _ string) ([]string, error) {
	return e.ids, e.err
}

func eqStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
