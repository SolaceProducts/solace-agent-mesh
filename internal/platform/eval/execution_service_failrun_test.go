// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func failRunFixture(t *testing.T, withDataset bool, with ...func(*ExecutionServiceParams)) (*ExecutionService, *store.DB, *store.EvalRunRepo, string, *recordingRunReporter) {
	t.Helper()
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	ctx := t.Context()

	exp := &store.EvalExperiment{
		Name: "fail-run-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, CreatedBy: "alice", UpdatedBy: "alice",
	}
	if withDataset {
		ds := &store.EvalDataset{Name: "fail-run", CreatedBy: "alice", UpdatedBy: "alice"}
		if err := datasetRepo.Create(ctx, ds); err != nil {
			t.Fatalf("create dataset: %v", err)
		}
		if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}); err != nil {
			t.Fatalf("create example: %v", err)
		}
		exp.DatasetID = &ds.ID
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusPending, ProgressTotal: 1, Source: "manual", StartedBy: "alice"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })
	reporter := &recordingRunReporter{}
	params := ExecutionServiceParams{
		Namespace: "test-ns", Broker: br, BrokerCli: NewBrokerClient(br, "test-ns", nil, nil),
		Runs: runRepo, Results: store.NewEvalRunResultRepo(db), Scores: store.NewEvalRunScoreRepo(db),
		Experiments: expRepo, Examples: exampleRepo, Analytics: reporter,
	}
	for _, w := range with {
		w(&params)
	}
	return NewExecutionService(params), db, runRepo, run.ID, reporter
}

func expectOneErrorFor(t *testing.T, reporter *recordingRunReporter, targetType string) {
	t.Helper()
	if !slices.Equal(reporter.outcomes, []string{taxonomy.OutcomeError}) || !slices.Equal(reporter.targetTypes, []string{targetType}) {
		t.Errorf("reported outcomes %v with target types %v, want one error for %q", reporter.outcomes, reporter.targetTypes, targetType)
	}
}

func TestExecuteRunFailureMessagesAreSAMSentences(t *testing.T) {
	t.Run("experiment with no dataset", func(t *testing.T) {
		exec, _, runRepo, runID, reporter := failRunFixture(t, false)

		if err := exec.executeRun(t.Context(), runID, false); err != nil {
			t.Fatalf("executeRun: %v", err)
		}
		expectOneErrorFor(t, reporter, store.EvalTargetTypeAgent)

		run, err := runRepo.GetByID(t.Context(), runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.Status != StatusFailed {
			t.Errorf("run status = %q, want failed", run.Status)
		}
		if got := derefOrEmpty(run.ErrorMessage); got != msgRunNoDataset {
			t.Errorf("run error = %q, want %q", got, msgRunNoDataset)
		}
	})

	t.Run("experiment retargeted to a workflow after the trigger", func(t *testing.T) {
		exec, db, runRepo, runID, reporter := failRunFixture(t, true)
		if _, err := db.ExecContext(t.Context(), `UPDATE eval_experiments SET target_type = 'workflow', target_agent = 'order_flow'`); err != nil {
			t.Fatalf("retarget experiment: %v", err)
		}
		requests, subErr := exec.broker.CreateDirectSubscription()
		if subErr != nil {
			t.Fatalf("subscribe: %v", subErr)
		}
		if subErr = exec.broker.Subscribe(requests, a2a.AgentRequestTopic("test-ns", "order_flow")); subErr != nil {
			t.Fatalf("subscribe: %v", subErr)
		}

		if execErr := exec.executeRun(t.Context(), runID, false); execErr != nil {
			t.Fatalf("executeRun: %v", execErr)
		}
		expectOneErrorFor(t, reporter, store.EvalTargetTypeWorkflow)

		run, err := runRepo.GetByID(t.Context(), runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.Status != StatusFailed || derefOrEmpty(run.ErrorMessage) != msgRunTargetChanged {
			t.Errorf("run = %q %q, want failed with %q", run.Status, derefOrEmpty(run.ErrorMessage), msgRunTargetChanged)
		}
		recvCtx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		msg, recvErr := exec.broker.Receive(recvCtx, requests)
		if msg != nil {
			t.Errorf("a task was published to the workflow's name: %s", msg.Payload)
		}
		if recvErr != nil && !errors.Is(recvErr, context.DeadlineExceeded) {
			t.Fatalf("receive: %v", recvErr)
		}
	})

	for name, blank := range map[string]string{"spaces": "  ", "tab": "\t", "newline": "\n", "crlf": "\r\n", "nbsp": "\u00a0"} {
		t.Run("example's prompt blanked after the trigger: "+name, func(t *testing.T) {
			exec, db, runRepo, runID, reporter := failRunFixture(t, true)
			if _, err := db.ExecContext(t.Context(), `UPDATE eval_examples SET prompt = ?`, blank); err != nil {
				t.Fatalf("blank prompt: %v", err)
			}

			if err := exec.executeRun(t.Context(), runID, false); err != nil {
				t.Fatalf("executeRun: %v", err)
			}
			expectOneErrorFor(t, reporter, store.EvalTargetTypeAgent)

			run, err := runRepo.GetByID(t.Context(), runID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if run.Status != StatusFailed || derefOrEmpty(run.ErrorMessage) != msgRunPromptlessExamples {
				t.Errorf("run = %q %q, want failed with %q", run.Status, derefOrEmpty(run.ErrorMessage), msgRunPromptlessExamples)
			}
		})
	}

	for refusal, change := range map[string]string{
		"workflow retarget": `UPDATE eval_experiments SET target_type = 'workflow', target_agent = 'order_flow'`,
		"blanked prompt":    `UPDATE eval_examples SET prompt = ''`,
	} {
		for _, status := range []string{StatusRunning, StatusCompleted} {
			t.Run(refusal+" on a run already "+status, func(t *testing.T) {
				exec, db, runRepo, runID, _ := failRunFixture(t, true)
				if _, err := db.ExecContext(t.Context(), change); err != nil {
					t.Fatalf("change: %v", err)
				}
				if _, err := db.ExecContext(t.Context(), `UPDATE eval_runs SET status = ?`, status); err != nil {
					t.Fatalf("set status: %v", err)
				}

				if err := exec.executeRun(t.Context(), runID, false); err != nil {
					t.Fatalf("executeRun: %v", err)
				}

				run, err := runRepo.GetByID(t.Context(), runID)
				if err != nil {
					t.Fatalf("get run: %v", err)
				}
				if status == StatusRunning {
					if run.Takeovers != 1 {
						t.Errorf("takeovers = %d, want the running run taken over", run.Takeovers)
					}
					return
				}
				if run.Status != status || run.ErrorMessage != nil {
					t.Errorf("run = %q %q, want it left %q with no error", run.Status, derefOrEmpty(run.ErrorMessage), status)
				}
			})
		}
	}

	t.Run("example ids fail to load before the claim", func(t *testing.T) {
		exec, db, runRepo, runID, reporter := failRunFixture(t, true)
		if _, err := db.ExecContext(t.Context(), `ALTER TABLE eval_examples RENAME TO eval_examples_gone`); err != nil {
			t.Fatalf("rename eval_examples: %v", err)
		}

		if err := exec.executeRun(t.Context(), runID, false); err == nil {
			t.Fatal("executeRun = nil, want the load error")
		}
		expectOneErrorFor(t, reporter, store.EvalTargetTypeAgent)

		run, err := runRepo.GetByID(t.Context(), runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.Status != StatusFailed || derefOrEmpty(run.ErrorMessage) != msgRunExamplesUnavailable {
			t.Errorf("run = %q %q, want failed with %q rather than left pending", run.Status, derefOrEmpty(run.ErrorMessage), msgRunExamplesUnavailable)
		}
	})

	t.Run("examples prefetch fails", func(t *testing.T) {
		exec, db, runRepo, runID, reporter := failRunFixture(t, true)
		if _, err := db.ExecContext(t.Context(), `DROP TABLE eval_example_artifacts`); err != nil {
			t.Fatalf("drop eval_example_artifacts: %v", err)
		}

		if err := exec.executeRun(t.Context(), runID, false); err != nil {
			t.Fatalf("executeRun: %v", err)
		}
		expectOneErrorFor(t, reporter, store.EvalTargetTypeAgent)

		run, err := runRepo.GetByID(t.Context(), runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.Status != StatusFailed {
			t.Errorf("run status = %q, want failed", run.Status)
		}
		got := derefOrEmpty(run.ErrorMessage)
		if got != msgRunExamplesUnavailable {
			t.Errorf("run error = %q, want %q", got, msgRunExamplesUnavailable)
		}
		for _, leaked := range []string{"eval_example_artifacts", "EvalExampleRepo", "get example"} {
			if strings.Contains(got, leaked) {
				t.Errorf("run error %q leaks the underlying error text %q", got, leaked)
			}
		}
	})

	t.Run("the budget is spent", func(t *testing.T) {
		exec, _, runRepo, runID, reporter := failRunFixture(t, true, func(p *ExecutionServiceParams) {
			p.BudgetGate = budgettest.BlockedGate(t, time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC))
		})

		if err := exec.executeRun(t.Context(), runID, false); err != nil {
			t.Fatalf("executeRun: %v", err)
		}

		run, err := runRepo.GetByID(t.Context(), runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.Status != StatusFailed {
			t.Errorf("run status = %q, want failed", run.Status)
		}
		expectOneErrorFor(t, reporter, store.EvalTargetTypeAgent)
		if !slices.Equal(reporter.categories, []string{taxonomy.ErrorCategoryBudgetExhausted}) {
			t.Errorf("categories = %v, want budget_exhausted", reporter.categories)
		}
	})
}
