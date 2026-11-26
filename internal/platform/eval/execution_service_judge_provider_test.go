// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type recordingBoundJudge struct{ called atomic.Bool }

func (r *recordingBoundJudge) JudgeScore(_ context.Context, _, _, _ string, _ *llm.SpendAttribution) (string, error) {
	r.called.Store(true)
	return `{"choice":"Yes","reasoning":"bound"}`, nil
}

func TestExecutionServiceJudgeRoutesToModelConfigProvider(t *testing.T) {
	runJudgeProviderScenario(t, true)
}

func TestExecutionServiceJudgeWorksWithoutBoundClient(t *testing.T) {
	runJudgeProviderScenario(t, false)
}

func TestExecutionServiceBuiltinJudgeGradesWithItsOwnModel(t *testing.T) {
	runJudgeProviderScenario(t, true, func(j *judgedExample) {
		j.builtin = true
		j.defaultModelAlias = "general"
	})
}

func runJudgeProviderScenario(t *testing.T, withBound bool, adjust ...func(*judgedExample)) {
	provider := newJudgeProbeServer(t, `{"choice":"Yes","reasoning":"PROVIDER"}`)

	var bound *recordingBoundJudge
	var llmClient LLMJudgeClient
	if withBound {
		bound = &recordingBoundJudge{}
		llmClient = bound
	}
	db := store.TestDB(t)
	model := "judge-cfg"
	example := judgedExample{
		evaluatorModel: &model,
		llmClient:      llmClient,
		judgeResolver: &fakeJudgeResolver{
			want: "judge-cfg", configID: "cfg1", dedicated: true,
			raw: providerRaw(provider.srv.URL),
		},
		modelResolver: func(ref string) (map[string]any, map[string]any) {
			if ref == "judge-cfg" {
				return map[string]any{"model": "openai/qwen3"}, map[string]any{"id": "cfg1", "alias": "judge-cfg"}
			}
			return nil, nil
		},
	}
	for _, f := range adjust {
		f(&example)
	}
	runID := runJudgedExample(t, db, newConnectedBroker(t), example)

	if provider.hits.Load() == 0 {
		t.Fatal("judge did not contact the model-config provider — it routed elsewhere")
	}
	if bound != nil && bound.called.Load() {
		t.Fatal("judge fell back to the bound platform client instead of the model-config provider")
	}
	results, err := store.NewEvalRunResultRepo(db).ListByRun(t.Context(), runID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 1 || len(results[0].Scores) != 1 || results[0].Scores[0].Score != 1.0 {
		t.Fatalf("expected one score of 1.0 from the provider verdict, got %+v", results)
	}
}

func newConnectedBroker(t *testing.T) *broker.DevBroker {
	t.Helper()
	br := broker.NewDevBroker()
	if err := br.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(context.Background()) })
	return br
}

type judgedExample struct {
	evaluatorModel    *string
	builtin           bool
	runAs             *string
	llmClient         LLMJudgeClient
	judgeResolver     JudgeModelResolver
	modelResolver     func(ref string) (model, config map[string]any)
	defaultModelAlias string
	spendPublisher    *spend.Publisher
	onAgentRequest    func(*broker.Message)
}

func runJudgedExample(t *testing.T, db *store.DB, br *broker.DevBroker, j judgedExample) string {
	t.Helper()
	ctx := t.Context()
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)

	ds := &store.EvalDataset{Name: "fixture", CreatedBy: "a", UpdatedBy: "a"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	const prompt = "ping"
	if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: prompt}); err != nil {
		t.Fatalf("create example: %v", err)
	}
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	cs := `{"Yes":1.0,"No":0.0}`
	ev := &store.EvalEvaluator{
		Name: "judge", Type: "llm_judge", Model: j.evaluatorModel, IsDefault: j.builtin,
		PromptTemplate: &tmpl, ChoiceScores: &cs, CreatedBy: "a", UpdatedBy: "a",
	}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "exp", TargetAgent: "Orchestrator", Models: "[]", RunAs: j.runAs,
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID, CreatedBy: "a", UpdatedBy: "a",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	namespace := "test-ns"
	storageSvc := NewStorageService(nil, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		Trigger: NewTriggerPublisher(br, namespace), Storage: storageSvc, ModelResolver: j.modelResolver,
	})
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil), Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: expRepo, Examples: exampleRepo,
		LLMClient: j.llmClient, JudgeResolver: j.judgeResolver,
		DefaultModelAlias: j.defaultModelAlias, SpendPublisher: j.spendPublisher, BudgetGate: budgettest.NewGate(),
		Config: ExecutionConfig{
			ExampleBudget: 10 * time.Second, WatchdogInterval: 500 * time.Millisecond,
			ScoringConcurrency: 2,
		},
	})
	agent := newObservedFakeAgent(t, br, namespace, "Orchestrator", map[string]string{prompt: "pong"}, j.onAgentRequest)
	t.Cleanup(agent.Stop)
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "a", "judged-example", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		row, err := runRepo.GetByID(ctx, runs[0].ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if !IsTerminalStatus(row.Status) {
			continue
		}
		if row.Status != StatusCompleted {
			t.Fatalf("final status = %q, want completed (err=%v)", row.Status, derefOrEmpty(row.ErrorMessage))
		}
		return runs[0].ID
	}
	t.Fatal("run did not reach a terminal state within deadline")
	return ""
}

type mixedJudgeResolver struct {
	goodRef string
	goodID  string
	goodRaw map[string]any
	badRef  string
}

func (m *mixedJudgeResolver) Resolve(_ context.Context, ref string) (map[string]any, string, bool, error) {
	switch ref {
	case m.goodRef:
		return m.goodRaw, m.goodID, true, nil
	case m.badRef:
		return nil, "", false, errors.New("model config store unavailable")
	}
	return nil, "", false, nil
}

func TestExecutionServiceJudgeProviderErrorMarksWarnings(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "fixture", CreatedBy: "a", UpdatedBy: "a"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	const prompt = "ping"
	if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: prompt}); err != nil {
		t.Fatalf("create example: %v", err)
	}

	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	cs := `{"Yes":1.0,"No":0.0}`
	goodModel, badModel := "good-cfg", "bad-cfg"
	goodEv := &store.EvalEvaluator{Name: "good", Type: "llm_judge", Model: &goodModel,
		PromptTemplate: &tmpl, ChoiceScores: &cs, CreatedBy: "a", UpdatedBy: "a"}
	badEv := &store.EvalEvaluator{Name: "bad", Type: "llm_judge", Model: &badModel,
		PromptTemplate: &tmpl, ChoiceScores: &cs, CreatedBy: "a", UpdatedBy: "a"}
	if err := evRepo.Create(ctx, goodEv); err != nil {
		t.Fatalf("create good evaluator: %v", err)
	}
	if err := evRepo.Create(ctx, badEv); err != nil {
		t.Fatalf("create bad evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID, CreatedBy: "a", UpdatedBy: "a",
	}
	if err := expRepo.Create(ctx, exp, []string{goodEv.ID, badEv.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })

	provider := newJudgeProbeServer(t, `{"choice":"Yes","reasoning":"PROVIDER"}`)

	namespace := "test-ns"
	trigger := NewTriggerPublisher(br, namespace)
	storageSvc := NewStorageService(nil, namespace)
	modelResolver := func(ref string) (map[string]any, map[string]any) {
		switch ref {
		case "good-cfg":
			return map[string]any{"model": "openai/qwen3"}, map[string]any{"id": "good", "alias": "good-cfg"}
		case "bad-cfg":
			return map[string]any{"model": "openai/qwen3"}, map[string]any{"id": "bad", "alias": "bad-cfg"}
		}
		return nil, nil
	}
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		Trigger: trigger, Storage: storageSvc, ModelResolver: modelResolver,
	})

	judgeResolver := &mixedJudgeResolver{
		goodRef: "good-cfg", goodID: "good", goodRaw: providerRaw(provider.srv.URL),
		badRef: "bad-cfg",
	}

	brokerCli := NewBrokerClient(br, namespace, nil, nil)
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: brokerCli, Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo,
		JudgeResolver: judgeResolver,
		Config: ExecutionConfig{
			ExampleBudget: 10 * time.Second, WatchdogInterval: 500 * time.Millisecond,
			ScoringConcurrency: 2,
		},
	})

	agent := newFakeAgent(t, br, namespace, "Orchestrator", map[string]string{prompt: "pong"})
	t.Cleanup(agent.Stop)

	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "a", "warnings", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		row, err := runRepo.GetByID(ctx, runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if !IsTerminalStatus(row.Status) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if row.Status != StatusCompletedWithWarnings {
			t.Fatalf("final status = %q, want completed_with_warnings (err=%v)", row.Status, derefOrEmpty(row.ErrorMessage))
		}
		results, err := resultRepo.ListByRun(ctx, runID)
		if err != nil {
			t.Fatalf("list results: %v", err)
		}
		if len(results) != 1 || len(results[0].Scores) != 2 {
			t.Fatalf("expected one result with two scores, got %+v", results)
		}
		var sawGood, sawBad bool
		for _, sc := range results[0].Scores {
			switch sc.EvaluatorID {
			case goodEv.ID:
				sawGood = true
				if sc.Score != 1.0 || sc.Passed == nil || !*sc.Passed {
					t.Errorf("good evaluator score = %v passed = %v, want 1.0 / true", sc.Score, sc.Passed)
				}
			case badEv.ID:
				sawBad = true
				if sc.Passed == nil || *sc.Passed {
					t.Errorf("bad evaluator should be passed=false, got %v", sc.Passed)
				}
				if sc.Reasoning == nil || *sc.Reasoning == "" {
					t.Error("bad evaluator should carry a failure reason")
				}
			}
		}
		if !sawGood || !sawBad {
			t.Fatalf("missing a score row: sawGood=%v sawBad=%v", sawGood, sawBad)
		}
		if row.SummaryScores == nil {
			t.Fatalf("run has no summary_scores")
		}
		var summary map[string]float64
		if err := json.Unmarshal([]byte(*row.SummaryScores), &summary); err != nil {
			t.Fatalf("unmarshal summary_scores %q: %v", *row.SummaryScores, err)
		}
		if mean, ok := summary[goodEv.ID]; !ok || mean != 1.0 {
			t.Errorf("summary_scores[good] = %v (present=%v), want 1.0 — the errored evaluator must not drag the good evaluator's mean", mean, ok)
		}
		if mean, ok := summary[badEv.ID]; ok {
			t.Errorf("summary_scores[bad] = %v present, want absent — the errored evaluator's synthetic 0 must be excluded from the mean", mean)
		}
		return
	}
	t.Fatal("run did not reach a terminal state within deadline")
}
