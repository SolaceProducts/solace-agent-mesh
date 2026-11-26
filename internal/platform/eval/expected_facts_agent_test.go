// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func runAgentFactsExperiment(t *testing.T, storedFacts, reply string) (*store.EvalRun, *store.EvalRunResultWithScores) {
	t.Helper()
	db := store.TestDB(t)
	br := newConnectedBroker(t)
	ctx := t.Context()
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)

	ds := &store.EvalDataset{Name: "facts", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	const prompt = "Refund order O-1"
	if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: prompt, ExpectedFacts: &storedFacts}); err != nil {
		t.Fatalf("create example: %v", err)
	}
	ev := outputFactsEvaluator()
	ev.CreatedBy, ev.UpdatedBy = "system", "system"
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "facts-agent", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID, CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	const namespace = "test-ns"
	storageSvc := NewStorageService(nil, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo, Examples: exampleRepo, Evaluators: evRepo,
		Trigger: NewTriggerPublisher(br, namespace), Storage: storageSvc,
	})
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil), Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: expRepo, Examples: exampleRepo, BudgetGate: budgettest.NewGate(),
		Config: ExecutionConfig{ExampleBudget: 10 * time.Second, WatchdogInterval: 500 * time.Millisecond, ScoringConcurrency: 2},
	})
	agent := newFakeAgent(t, br, namespace, "Orchestrator", map[string]string{prompt: reply})
	t.Cleanup(agent.Stop)
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "facts-agent", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		run, err := runRepo.GetByID(ctx, runs[0].ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if !IsTerminalStatus(run.Status) {
			continue
		}
		results, err := resultRepo.ListByRun(ctx, run.ID)
		if err != nil || len(results) != 1 || len(results[0].Scores) != 1 {
			t.Fatalf("results = %+v, %v; want one with one score", results, err)
		}
		return run, results[0]
	}
	t.Fatal("run did not finish within the deadline")
	return nil, nil
}

func TestAgentExperimentGradesTheExpectedFacts(t *testing.T) {
	cases := []struct {
		name, stored, wantReason, wantStatus string
	}{
		{"every fact held", `{"required":["O-1","refund approved"],"forbidden":["escalated"]}`, "3 of 3 facts held.", StatusCompleted},
		{"unreadable stored facts", `{"required":["O-1"],"unknown":true}`, "expected facts cannot be read", StatusCompletedWithWarnings},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run, result := runAgentFactsExperiment(t, tc.stored, "Refund approved for order O-1.")

			if reason := derefString(result.Scores[0].Reasoning); !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reasoning %q, want it to contain %q", reason, tc.wantReason)
			}
			if run.Status != tc.wantStatus {
				t.Errorf("run status = %q, want %q", run.Status, tc.wantStatus)
			}
		})
	}
}
