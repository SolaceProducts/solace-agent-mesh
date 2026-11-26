// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type blockingJudge struct{ started chan struct{} }

func (b *blockingJudge) JudgeScore(ctx context.Context, _ string, _, _ string, _ *llm.SpendAttribution) (string, error) {
	close(b.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func TestEvaluateAndSave_AStopLeavesTheTrialForTheNextGWE(t *testing.T) {
	for name, tc := range map[string]struct {
		cause      terminationCause
		wantScores int
	}{
		"stop":        {causeShutdown, 0},
		"user cancel": {causeUserCancel, 1},
	} {
		t.Run(name, func(t *testing.T) {
			db := store.TestDB(t)
			ctx := t.Context()
			ds := &store.EvalDataset{Name: "stop-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
			if err := store.NewEvalDatasetRepo(db).Create(ctx, ds); err != nil {
				t.Fatal(err)
			}
			ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}
			if err := store.NewEvalExampleRepo(db).Create(ctx, ex); err != nil {
				t.Fatal(err)
			}
			exp := &store.EvalExperiment{
				Name: "stop-exp", TargetAgent: "Orchestrator", Models: "[]", RunsPerExample: 1, MaxWorkers: 1,
				DatasetID: &ds.ID, CreatedBy: "alice", UpdatedBy: "alice",
			}
			if err := store.NewEvalExperimentRepo(db).Create(ctx, exp, nil); err != nil {
				t.Fatal(err)
			}
			run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, ProgressTotal: 1, Source: "manual", StartedBy: "alice"}
			runs := store.NewEvalRunRepo(db)
			if err := runs.Create(ctx, run); err != nil {
				t.Fatal(err)
			}
			results := store.NewEvalRunResultRepo(db)
			judge := &blockingJudge{started: make(chan struct{})}
			svc := NewExecutionService(ExecutionServiceParams{
				Namespace: "ns", Runs: runs, Results: results, Scores: store.NewEvalRunScoreRepo(db),
				Experiments: store.NewEvalExperimentRepo(db), Examples: store.NewEvalExampleRepo(db),
			})
			model, tmpl, choices := "stub-model", "Judge {{Prompt}} → {{Response}}", `{"Yes": 1.0, "No": 0.0}`
			evaluator := &store.EvalEvaluator{ID: idgen.New(), Name: "judge", Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &choices}

			runCtx, cancel := context.WithCancel(ctx)
			svc.mu.Lock()
			svc.activeRuns[run.ID] = cancel
			svc.mu.Unlock()
			go func() {
				<-judge.started
				svc.cancelWithCause(run.ID, tc.cause)
			}()
			response := "the answer"
			svc.evaluateAndSave(runCtx, evaluateAndSaveArgs{
				RunID: run.ID, Experiment: exp, EvaluatorSnapshot: []*store.EvalEvaluator{evaluator},
				Task:         &pendingTask{exampleID: ex.ID, example: ex},
				ResponseText: &response, State: &runState{pending: map[string]*pendingTask{}, judge: judge},
			})

			saved, err := results.ListByRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(saved) != 1 || len(saved[0].Scores) != tc.wantScores {
				t.Fatalf("saved %d results, want 1 result with %d scores", len(saved), tc.wantScores)
			}
			if tc.wantScores == 0 {
				if _, err := runs.DeleteHalfSavedResults(ctx, run.ID, []string{evaluator.ID}); err != nil {
					t.Fatal(err)
				}
				if left, _ := results.ListByRun(ctx, run.ID); len(left) != 0 {
					t.Fatalf("takeover cleanup left %d results, want the trial deleted for regrading", len(left))
				}
			} else if saved[0].Scores[0].Errored == nil || !*saved[0].Scores[0].Errored {
				t.Fatalf("score = %+v, want errored", saved[0].Scores[0])
			}
		})
	}
}

func TestEvaluateAndSave_FileSavesWaitForASlot(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	ds := &store.EvalDataset{Name: "slots-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := store.NewEvalDatasetRepo(db).Create(ctx, ds); err != nil {
		t.Fatal(err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}
	if err := store.NewEvalExampleRepo(db).Create(ctx, ex); err != nil {
		t.Fatal(err)
	}
	exp := &store.EvalExperiment{
		Name: "slots-exp", TargetAgent: "Orchestrator", Models: "[]", RunsPerExample: 2, MaxWorkers: 1,
		DatasetID: &ds.ID, CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := store.NewEvalExperimentRepo(db).Create(ctx, exp, nil); err != nil {
		t.Fatal(err)
	}
	runs := store.NewEvalRunRepo(db)
	run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, ProgressTotal: 2, Source: "manual", StartedBy: "alice"}
	if err := runs.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	results := store.NewEvalRunResultRepo(db)
	cfg := DefaultExecutionConfig()
	cfg.DBOpTimeout = 100 * time.Millisecond
	svc := NewExecutionService(ExecutionServiceParams{
		Namespace: "ns", Runs: runs, Results: results, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: store.NewEvalExperimentRepo(db), Examples: store.NewEvalExampleRepo(db), Config: cfg,
	})
	response := "the answer"
	save := func(trial int) {
		svc.evaluateAndSave(ctx, evaluateAndSaveArgs{
			RunID: run.ID, Experiment: exp, Task: &pendingTask{exampleID: ex.ID, trialIndex: trial, example: ex},
			ResponseText: &response, State: &runState{pending: map[string]*pendingTask{}},
		})
	}

	for range maxConcurrentFileSaves {
		svc.fileSaves <- struct{}{}
	}
	save(0)
	if saved, _ := results.ListByRun(ctx, run.ID); len(saved) != 0 {
		t.Fatalf("saved %d results with every slot taken, want 0", len(saved))
	}

	<-svc.fileSaves
	save(1)
	if saved, _ := results.ListByRun(ctx, run.ID); len(saved) != 1 {
		t.Fatalf("saved %d results after a slot freed, want 1", len(saved))
	}
}

func TestExecuteRun_AStopDuringTheLastGradingLeavesTheRunForTheNextGWE(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	ds := &store.EvalDataset{Name: "last-grading", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := store.NewEvalDatasetRepo(db).Create(ctx, ds); err != nil {
		t.Fatal(err)
	}
	examples := store.NewEvalExampleRepo(db)
	if err := examples.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	model, tmpl, choices := "stub-model", "Judge {{Prompt}} → {{Response}}", `{"Yes": 1.0, "No": 0.0}`
	evaluator := &store.EvalEvaluator{Name: "judge", Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &choices, CreatedBy: "alice", UpdatedBy: "alice"}
	evaluators := store.NewEvalEvaluatorRepo(db)
	if err := evaluators.Create(ctx, evaluator); err != nil {
		t.Fatal(err)
	}
	experiments := store.NewEvalExperimentRepo(db)
	exp := &store.EvalExperiment{
		Name: "last-grading", TargetAgent: "Orchestrator", Models: "[]", RunsPerExample: 1, MaxWorkers: 1,
		DatasetID: &ds.ID, CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := experiments.Create(ctx, exp, []string{evaluator.ID}); err != nil {
		t.Fatal(err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = br.Disconnect(context.Background()) })
	namespace := "test-ns"
	agent := newFakeAgent(t, br, namespace, "Orchestrator", map[string]string{"p": "the answer"})
	t.Cleanup(agent.Stop)

	runs, results := store.NewEvalRunRepo(db), store.NewEvalRunResultRepo(db)
	judge := &blockingJudge{started: make(chan struct{})}
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil),
		Runs: runs, Results: results, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: experiments, Examples: examples, LLMClient: judge,
		Config: ExecutionConfig{ExampleBudget: time.Hour, WatchdogInterval: time.Hour},
	})
	if err := exec.Start(ctx); err != nil {
		t.Fatal(err)
	}
	runSvc := NewRunService(RunServiceParams{
		Runs: runs, Results: results, Experiments: experiments, Examples: examples, Evaluators: evaluators,
		Trigger: NewTriggerPublisher(br, namespace),
	})
	triggered, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	runID := triggered[0].ID

	select {
	case <-judge.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the judge was never called")
	}
	exec.Stop()

	got, err := runs.GetByID(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRunning || got.ProgressCurrent != 0 {
		t.Fatalf("run = %s with progress %d, want running with progress 0", got.Status, got.ProgressCurrent)
	}
	saved, err := results.ListByRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || len(saved[0].Scores) != 0 {
		t.Fatalf("saved %d results, want the one trial saved without scores", len(saved))
	}
}
