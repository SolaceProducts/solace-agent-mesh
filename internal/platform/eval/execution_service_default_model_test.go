// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type capturingBoundJudge struct {
	verdict string

	mu     sync.Mutex
	models []string
}

func (c *capturingBoundJudge) JudgeScore(_ context.Context, model, _, _ string, _ *llm.SpendAttribution) (string, error) {
	c.mu.Lock()
	c.models = append(c.models, model)
	c.mu.Unlock()
	return c.verdict, nil
}

func (c *capturingBoundJudge) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.models...)
}

func TestExecutionServiceDefaultModelFallback(t *testing.T) {
	t.Run("configured general grades the no-model evaluator", func(t *testing.T) {
		runDefaultModelScenario(t, true)
	})
	t.Run("absent general leaves a clean no-model error", func(t *testing.T) {
		runDefaultModelScenario(t, false)
	})
}

func runDefaultModelScenario(t *testing.T, withDefault bool) {
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
	ev := &store.EvalEvaluator{
		Name: "judge", Type: "llm_judge",
		PromptTemplate: &tmpl, ChoiceScores: &cs, CreatedBy: "a", UpdatedBy: "a",
	}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID, CreatedBy: "a", UpdatedBy: "a",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })

	namespace := "test-ns"
	trigger := NewTriggerPublisher(br, namespace)
	storageSvc := NewStorageService(nil, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		Trigger: trigger, Storage: storageSvc,
	})

	const generalModel = "openai/general-from-store"
	resolver := &fakeJudgeResolver{want: "general"}
	if withDefault {
		resolver.raw = map[string]any{"model": generalModel}
	}

	bound := &capturingBoundJudge{verdict: `{"choice":"Yes","reasoning":"bound"}`}

	brokerCli := NewBrokerClient(br, namespace, nil, nil)
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: brokerCli, Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo,
		LLMClient: bound, JudgeResolver: resolver, DefaultModelAlias: "general",
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

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "a", "default-model", "manual")
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
		results, err := resultRepo.ListByRun(ctx, runID)
		if err != nil {
			t.Fatalf("list results: %v", err)
		}
		if len(results) != 1 || len(results[0].Scores) != 1 {
			t.Fatalf("expected one result with one score, got %+v", results)
		}
		score := results[0].Scores[0]

		if withDefault {
			if row.Status != StatusCompleted {
				t.Fatalf("final status = %q, want completed (err=%v)", row.Status, derefOrEmpty(row.ErrorMessage))
			}
			if score.Score != 1.0 || score.Passed == nil || !*score.Passed {
				t.Errorf("score = %v passed = %v, want 1.0 / true", score.Score, score.Passed)
			}
			if seen := bound.seen(); len(seen) != 1 || seen[0] != generalModel {
				t.Errorf("bound judge models = %v, want exactly [%q] (resolved default)", seen, generalModel)
			}
			return
		}

		if row.Status != StatusCompletedWithWarnings {
			t.Fatalf("final status = %q, want completed_with_warnings", row.Status)
		}
		if score.Passed == nil || *score.Passed {
			t.Errorf("score should be passed=false (errored), got %v", score.Passed)
		}
		if seen := bound.seen(); len(seen) != 0 {
			t.Errorf("bound judge should not be called when no model resolves, got %v", seen)
		}
		return
	}
	t.Fatal("run did not reach a terminal state within deadline")
}

type countingJudgeResolver struct{ calls int }

func (c *countingJudgeResolver) Resolve(context.Context, string) (map[string]any, string, bool, error) {
	c.calls++
	return nil, "", false, nil
}

func TestResolveDefaultJudgeModelWarnsOnlyWhenAJudgeNeedsIt(t *testing.T) {
	model := "own-model"
	cases := map[string]struct {
		evaluators []*store.EvalEvaluator
		wantWarn   bool
	}{
		"every judge has its own model": {
			evaluators: []*store.EvalEvaluator{{Type: evaluatorTypeLLMJudge, Model: &model}, {Type: "heuristic"}},
		},
		"a judge has no model": {
			evaluators: []*store.EvalEvaluator{{Type: evaluatorTypeLLMJudge, Model: &model}, {Type: evaluatorTypeLLMJudge}},
			wantWarn:   true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			resolver := &countingJudgeResolver{}
			s := &ExecutionService{
				judgeResolver:     resolver,
				defaultModelAlias: "general",
				logger:            slog.New(slog.NewTextHandler(&logs, nil)),
			}
			s.resolveDefaultJudgeModel(t.Context(), "run-1", &runState{evaluators: tc.evaluators})
			if warned := bytes.Contains(logs.Bytes(), []byte("default judge model not configured")); warned != tc.wantWarn {
				t.Errorf("warned = %v, want %v (logs: %s)", warned, tc.wantWarn, logs.String())
			}
			if (resolver.calls > 0) != tc.wantWarn {
				t.Errorf("resolver calls = %d, want a lookup only when a judge needs the default", resolver.calls)
			}
		})
	}
}
