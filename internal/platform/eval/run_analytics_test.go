// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type recordingRunReporter struct {
	outcomes    []string
	categories  []string
	targetTypes []string
	shapes      []*platpkg.EvalRunShape
	decisions   []*platpkg.EvalDecisionFacts
}

func (r *recordingRunReporter) EvalRun(_ context.Context, outcome, errorCategory, targetType string, shape *platpkg.EvalRunShape, decision *platpkg.EvalDecisionFacts) {
	r.outcomes = append(r.outcomes, outcome)
	r.categories = append(r.categories, errorCategory)
	r.targetTypes = append(r.targetTypes, targetType)
	r.shapes = append(r.shapes, shape)
	r.decisions = append(r.decisions, decision)
}

func (r *recordingRunReporter) assertUncounted(t *testing.T) {
	t.Helper()
	for i, d := range r.decisions {
		if d != nil {
			t.Errorf("report %d carried decision facts %+v, want nil: this path never held the run's scoring state", i, *d)
		}
	}
	for i, shape := range r.shapes {
		if shape != nil {
			t.Errorf("report %d carried run shape %+v, want nil: this path never held the run's setup", i, *shape)
		}
	}
}

func reporterFor(t *testing.T, status string) *recordingRunReporter {
	t.Helper()
	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Analytics: reporter})
	svc.reportRun(context.Background(), status, "", nil, nil)
	return reporter
}

func TestEvalRunOutcomeMapping(t *testing.T) {
	for _, tc := range []struct {
		status       string
		wantOutcome  string
		wantCategory string
		why          string
	}{
		{
			status:      StatusCompleted,
			wantOutcome: taxonomy.OutcomeSuccess,
			why:         "a completed run is the plain success case",
		},
		{
			status:      StatusCompletedWithWarnings,
			wantOutcome: taxonomy.OutcomeSuccess,
			why: "the trials ran and were graded; only an evaluator's judge was " +
				"unreachable. Folded into error, evals would read as failing on " +
				"installs where they work",
		},
		{
			status:       StatusFailed,
			wantOutcome:  taxonomy.OutcomeError,
			wantCategory: taxonomy.ErrorCategoryUnknown,
			why: "the run-level status is all this layer knows; a per-trial cause " +
				"would have to come off rows that name agents and datasets",
		},
	} {
		t.Run(tc.status, func(t *testing.T) {
			reporter := reporterFor(t, tc.status)

			if len(reporter.outcomes) != 1 {
				t.Fatalf("reported %d times, want once", len(reporter.outcomes))
			}
			if got := reporter.outcomes[0]; got != tc.wantOutcome {
				t.Errorf("status %q reported outcome %q, want %q: %s", tc.status, got, tc.wantOutcome, tc.why)
			}
			if got := reporter.categories[0]; got != tc.wantCategory {
				t.Errorf("status %q reported errorCategory %q, want %q", tc.status, got, tc.wantCategory)
			}
		})
	}
}

func TestEveryReportedOutcomeIsApproved(t *testing.T) {
	for _, status := range []string{StatusCompleted, StatusCompletedWithWarnings, StatusFailed} {
		reporter := reporterFor(t, status)
		outcome := reporter.outcomes[0]
		if !slices.Contains(taxonomy.EvalRunOutcomes, outcome) {
			t.Errorf("status %q reports outcome %q, which the catalog does not approve\n  approved: %v",
				status, outcome, taxonomy.EvalRunOutcomes)
		}
	}
	for _, cause := range []terminationCause{causeUnknown, causeWatchdog, causeAgentUnavailable} {
		outcome, _ := cancelledRunOutcome(cause)
		if !slices.Contains(taxonomy.EvalRunOutcomes, outcome) {
			t.Errorf("cause %v reports outcome %q, which the catalog does not approve\n  approved: %v",
				cause, outcome, taxonomy.EvalRunOutcomes)
		}
	}
}

func TestCancelledRunOutcomeIsDecidedByTerminationCause(t *testing.T) {
	for _, tc := range []struct {
		cause        terminationCause
		wantOutcome  string
		wantCategory string
		why          string
	}{
		{
			cause:       causeWatchdog,
			wantOutcome: taxonomy.OutcomeTimeout,
			why:         "a watchdog cancel is exactly what outcome=timeout means: the last dispatched call is not known to have returned",
		},
		{
			cause:        causeAgentUnavailable,
			wantOutcome:  taxonomy.OutcomeError,
			wantCategory: taxonomy.ErrorCategoryUnknown,
			why:          "the target agent leaving the registry is a failure this layer cannot classify further",
		},
		{
			cause:       causeUnknown,
			wantOutcome: taxonomy.OutcomeCancelled,
			why:         "every other cause is the REST endpoint's own user cancel",
		},
	} {
		t.Run(tc.wantOutcome, func(t *testing.T) {
			outcome, category := cancelledRunOutcome(tc.cause)
			if outcome != tc.wantOutcome {
				t.Errorf("outcome = %q, want %q: %s", outcome, tc.wantOutcome, tc.why)
			}
			if category != tc.wantCategory {
				t.Errorf("errorCategory = %q, want %q", category, tc.wantCategory)
			}
		})
	}
}

func TestAFinishedRunReportsNothingWithNoReporter(t *testing.T) {
	svc := NewExecutionService(ExecutionServiceParams{})
	svc.reportRun(context.Background(), StatusFailed, "", nil, &platpkg.EvalDecisionFacts{Evaluators: 1})
}

func TestPublishFailureReportsErrorExactlyOnce(t *testing.T) {
	db := store.TestDB(t)
	expRepo := store.NewEvalExperimentRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	exp := &store.EvalExperiment{
		Name: "publish-fail-fixture", TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow, Models: "[]",
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID}
	if err := runRepo.Create(t.Context(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Runs: runRepo, Analytics: reporter})

	publishErr := make(chan error, 1)
	publishErr <- errors.New("publish task: boom")
	state := &runState{allWork: []workItem{{exampleID: "ex-1", trialIndex: 0}}}

	outcome := svc.runReceiveLoop(t.Context(), receiveLoopArgs{
		RunID:      run.ID,
		Experiment: exp,
		AllWork:    state.allWork,
		PublishErr: publishErr,
		State:      state,
	})
	if outcome != receiveLoopFailed {
		t.Fatalf("outcome = %v, want receiveLoopFailed", outcome)
	}

	if len(reporter.outcomes) != 1 || reporter.outcomes[0] != taxonomy.OutcomeError {
		t.Errorf("reported outcomes = %v, want exactly one %q — a publish error must not also be reported as cancelled",
			reporter.outcomes, taxonomy.OutcomeError)
	}
	if !slices.Equal(reporter.targetTypes, []string{store.EvalTargetTypeWorkflow}) {
		t.Errorf("target types = %v, want the experiment's, workflow", reporter.targetTypes)
	}
	reporter.assertUncounted(t)
}

func TestCancelledPublishErrorIsReportedAsCancelledNotFailed(t *testing.T) {
	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Analytics: reporter})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	publishErr := make(chan error, 1)
	publishErr <- errors.New("publish task: context canceled")
	state := &runState{allWork: []workItem{{exampleID: "ex-1", trialIndex: 0}}}

	outcome := svc.runReceiveLoop(ctx, receiveLoopArgs{
		RunID:      "race-run",
		AllWork:    state.allWork,
		PublishErr: publishErr,
		State:      state,
	})
	if outcome != receiveLoopCancelled {
		t.Fatalf("outcome = %v, want receiveLoopCancelled — a ctx-driven publish error must defer to the cancel path", outcome)
	}
	if len(reporter.outcomes) != 0 {
		t.Errorf("reported outcomes = %v, want none — failRun must not run for a cancelled ctx", reporter.outcomes)
	}
}

func TestFailRunReportsCancelledWhenRunAlreadyCancelled(t *testing.T) {
	db := store.TestDB(t)
	expRepo := store.NewEvalExperimentRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	exp := &store.EvalExperiment{
		Name: "fail-cancel-race", TargetAgent: "Orchestrator", Models: "[]",
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID}
	if err := runRepo.Create(t.Context(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := runRepo.UpdateSummaryScores(t.Context(), run.ID, "{}", StatusCancelled, nil); err != nil {
		t.Fatalf("seed cancelled: %v", err)
	}

	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Runs: runRepo, Analytics: reporter})

	if err := svc.failRun(t.Context(), run.ID, "boom", exp); err != nil {
		t.Fatalf("failRun: %v", err)
	}
	if len(reporter.outcomes) != 1 || reporter.outcomes[0] != taxonomy.OutcomeCancelled || reporter.targetTypes[0] != store.EvalTargetTypeAgent {
		t.Errorf("reported outcomes = %v with target types %v, want exactly one %q for an agent", reporter.outcomes, reporter.targetTypes, taxonomy.OutcomeCancelled)
	}
	reporter.assertUncounted(t)

	got, err := runRepo.GetByID(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusCancelled {
		t.Errorf("status = %q, want %q — failRun must not clobber a cancel", got.Status, StatusCancelled)
	}
	if got.ErrorMessage != nil {
		t.Errorf("error message = %v, want nil — the cancel, not \"boom\", must win", *got.ErrorMessage)
	}
}

func seedStaleRunningRun(t *testing.T, db *store.DB, runs *store.EvalRunRepo, expRepo *store.EvalExperimentRepo) string {
	t.Helper()
	exp := &store.EvalExperiment{
		Name: "watchdog-fixture-" + idgen.New(), TargetAgent: "Orchestrator", Models: "[]",
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID}
	if err := runs.Create(t.Context(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if ok, err := runs.SetRunning(t.Context(), run.ID, 1); err != nil || !ok {
		t.Fatalf("set running: ok=%v err=%v", ok, err)
	}
	longAgo := time.Now().Add(-1 * time.Hour).UnixMilli()
	if _, err := db.ExecContext(t.Context(),
		`UPDATE eval_runs SET coordinator_started_at = ? WHERE id = ?`, longAgo, run.ID); err != nil {
		t.Fatalf("backdate coordinator_started_at: %v", err)
	}
	return run.ID
}

func TestWatchdogDirectFinalizeReportsTimeout(t *testing.T) {
	db := store.TestDB(t)
	runs := store.NewEvalRunRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	runID := seedStaleRunningRun(t, db, runs, expRepo)

	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Runs: runs, Analytics: reporter})

	svc.runWatchdogTick(t.Context())

	if len(reporter.outcomes) != 1 || reporter.outcomes[0] != taxonomy.OutcomeTimeout {
		t.Errorf("reported outcomes = %v, want exactly one %q", reporter.outcomes, taxonomy.OutcomeTimeout)
	}
	reporter.assertUncounted(t)

	run, err := runs.GetByID(t.Context(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != StatusFailed {
		t.Errorf("run status = %q, want %q", run.Status, StatusFailed)
	}
}

func TestStopRecordsShutdownCauseBeforeCancelling(t *testing.T) {
	svc := NewExecutionService(ExecutionServiceParams{})
	svc.started = true

	cancelled := false
	svc.activeRuns["run-1"] = func() { cancelled = true }

	svc.Stop()

	if !cancelled {
		t.Fatal("Stop() did not cancel the active run's context")
	}
	if cause := svc.consumeCause("run-1"); cause != causeShutdown {
		t.Errorf("cause = %v, want causeShutdown", cause)
	}
}

func TestStopDoesNotOverwriteAnExistingCause(t *testing.T) {
	svc := NewExecutionService(ExecutionServiceParams{})
	svc.started = true
	svc.activeRuns["run-1"] = func() {}
	svc.terminationCauses["run-1"] = causeUserCancel

	svc.Stop()

	if cause := svc.consumeCause("run-1"); cause != causeUserCancel {
		t.Errorf("cause = %v, want causeUserCancel to survive Stop()", cause)
	}
}

func TestCancelledRunReportsItsDecisionCounts(t *testing.T) {
	db := store.TestDB(t)
	runs := store.NewEvalRunRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	exp := &store.EvalExperiment{Name: "cancel-fixture", TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow, Models: "[]", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := expRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID}
	if err := runs.Create(t.Context(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	decisionEv := seedRunWithDecisionScores(t, db, run.ID, []ScoreDetails{
		{DecidedBy: decidedByDecision},
		{DecidedBy: decidedByLLMJudge, Escalated: true, EscalationReason: escalationLowConfidence},
	})

	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Runs: runs, Results: store.NewEvalRunResultRepo(db), Analytics: reporter})
	svc.finalizeCancelledRun(t.Context(), run.ID, exp, &runState{pending: map[string]*pendingTask{}, evaluators: []*store.EvalEvaluator{decisionEv}}, causeUserCancel)

	if len(reporter.decisions) != 1 {
		t.Fatalf("reported %d times, want once", len(reporter.decisions))
	}
	got := reporter.decisions[0]
	if got == nil || got.Evaluators != 1 || got.Decided != 1 || got.Escalated != 1 ||
		got.EscalatedBy[escalationLowConfidence] != 1 {
		t.Errorf("decision facts = %+v, want 1 evaluator, 1 decided, 1 escalated on low confidence", got)
	}
	if reporter.targetTypes[0] != store.EvalTargetTypeWorkflow {
		t.Errorf("targetType = %q, want the experiment's, workflow", reporter.targetTypes[0])
	}
}

func TestFailRunReportsTheTargetType(t *testing.T) {
	db := store.TestDB(t)
	runs := store.NewEvalRunRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	exp := &store.EvalExperiment{Name: "budget-fixture", TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow, Models: "[]", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := expRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Runs: runs, Analytics: reporter})
	for _, e := range []*store.EvalExperiment{exp, nil} {
		run := &store.EvalRun{ExperimentID: exp.ID}
		if err := runs.Create(t.Context(), run); err != nil {
			t.Fatalf("create run: %v", err)
		}
		if err := svc.failRunWithCategory(t.Context(), run.ID, "over budget", taxonomy.ErrorCategoryBudgetExhausted, e); err != nil {
			t.Fatalf("fail run: %v", err)
		}
	}

	want := []string{store.EvalTargetTypeWorkflow, ""}
	if !slices.Equal(reporter.targetTypes, want) || !slices.Equal(reporter.categories, []string{taxonomy.ErrorCategoryBudgetExhausted, taxonomy.ErrorCategoryBudgetExhausted}) {
		t.Errorf("reported target types %v and categories %v, want %v, both budget_exhausted", reporter.targetTypes, reporter.categories, want)
	}
}

func TestCompleteRunReportsTheTargetType(t *testing.T) {
	db := store.TestDB(t)
	runs := store.NewEvalRunRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	exp := &store.EvalExperiment{Name: "complete-fixture", TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow, Models: "[]", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := expRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID}
	if err := runs.Create(t.Context(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Runs: runs, Results: store.NewEvalRunResultRepo(db), Analytics: reporter})
	state := &runState{progress: 1}
	if err := svc.completeRun(t.Context(), run.ID, exp, state); err != nil {
		t.Fatalf("complete run: %v", err)
	}

	if len(reporter.outcomes) != 1 || reporter.outcomes[0] != taxonomy.OutcomeSuccess || reporter.targetTypes[0] != store.EvalTargetTypeWorkflow {
		t.Errorf("reported %v with target types %v, want one success for a workflow", reporter.outcomes, reporter.targetTypes)
	}
}

func TestCompleteRunReportsTheTargetTypeWhenACancelWonTheRace(t *testing.T) {
	db := store.TestDB(t)
	runs := store.NewEvalRunRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	exp := &store.EvalExperiment{Name: "complete-cancel-race", TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow, Models: "[]", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := expRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID}
	if err := runs.Create(t.Context(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := runs.UpdateSummaryScores(t.Context(), run.ID, "{}", StatusCancelled, nil); err != nil {
		t.Fatalf("seed cancelled: %v", err)
	}
	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Runs: runs, Results: store.NewEvalRunResultRepo(db), Analytics: reporter})
	state := &runState{progress: 1}
	if err := svc.completeRun(t.Context(), run.ID, exp, state); err != nil {
		t.Fatalf("complete run: %v", err)
	}

	if !slices.Equal(reporter.outcomes, []string{taxonomy.OutcomeCancelled}) || !slices.Equal(reporter.targetTypes, []string{store.EvalTargetTypeWorkflow}) {
		t.Errorf("reported %v with target types %v, want one cancelled for a workflow", reporter.outcomes, reporter.targetTypes)
	}
}

func TestTargetTypesMatchTheAnalyticsVocabulary(t *testing.T) {
	if !slices.Equal([]string{store.EvalTargetTypeAgent, store.EvalTargetTypeWorkflow}, taxonomy.EvalTargetTypes) {
		t.Errorf("store target types %q, %q differ from taxonomy.EvalTargetTypes %v",
			store.EvalTargetTypeAgent, store.EvalTargetTypeWorkflow, taxonomy.EvalTargetTypes)
	}
}

func TestCancelledRunReportsWhyItsInFlightTrialsEnded(t *testing.T) {
	for _, tc := range []struct {
		name              string
		targetType        string
		cause             terminationCause
		inFlight          bool
		want              []string
		wantUnknown       bool
		unreadableResults bool
	}{
		{name: "workflow, user cancel", targetType: store.EvalTargetTypeWorkflow, cause: causeUserCancel, inFlight: true, want: []string{FailureKindCancelled}},
		{name: "workflow, watchdog", targetType: store.EvalTargetTypeWorkflow, cause: causeWatchdog, inFlight: true, want: []string{FailureKindTimeout}},
		{name: "workflow, user cancel, never dispatched", targetType: store.EvalTargetTypeWorkflow, cause: causeUserCancel, want: []string{FailureKindCancelled}},
		{name: "workflow, watchdog, never dispatched", targetType: store.EvalTargetTypeWorkflow, cause: causeWatchdog, want: []string{FailureKindTimeout}},
		{name: "workflow, target gone", targetType: store.EvalTargetTypeWorkflow, cause: causeAgentUnavailable, inFlight: true, wantUnknown: true},
		{name: "workflow, target gone, never dispatched", targetType: store.EvalTargetTypeWorkflow, cause: causeAgentUnavailable, wantUnknown: true},
		{name: "workflow, user cancel, back-fill cannot read results", targetType: store.EvalTargetTypeWorkflow, cause: causeUserCancel, unreadableResults: true, wantUnknown: true},
		{name: "agent, user cancel", targetType: store.EvalTargetTypeAgent, cause: causeUserCancel, inFlight: true},
		{name: "agent, user cancel, never dispatched", targetType: store.EvalTargetTypeAgent, cause: causeUserCancel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := store.TestDB(t)
			runs := store.NewEvalRunRepo(db)
			ctx := t.Context()
			dataset := &store.EvalDataset{Name: "cancel-shape", CreatedBy: "alice", UpdatedBy: "alice"}
			if err := store.NewEvalDatasetRepo(db).Create(ctx, dataset); err != nil {
				t.Fatalf("create dataset: %v", err)
			}
			example := &store.EvalExample{DatasetID: dataset.ID, SequenceNumber: 1, Prompt: "ping"}
			if err := store.NewEvalExampleRepo(db).Create(ctx, example); err != nil {
				t.Fatalf("create example: %v", err)
			}
			exp := &store.EvalExperiment{
				Name: "cancel-shape-fixture", TargetAgent: "Orchestrator", TargetType: tc.targetType, Models: "[]",
				RunsPerExample: 3, DatasetID: &dataset.ID, CreatedBy: "alice", UpdatedBy: "alice",
			}
			if err := store.NewEvalExperimentRepo(db).Create(ctx, exp, nil); err != nil {
				t.Fatalf("create experiment: %v", err)
			}
			run := &store.EvalRun{ExperimentID: exp.ID}
			if err := runs.Create(ctx, run); err != nil {
				t.Fatalf("create run: %v", err)
			}
			if ok, err := runs.SetRunning(ctx, run.ID, 1); err != nil || !ok {
				t.Fatalf("set running: ok=%v err=%v", ok, err)
			}
			state := &runState{
				pending:      map[string]*pendingTask{},
				allWork:      []workItem{{exampleID: example.ID}},
				examplesByID: map[string]*store.EvalExample{example.ID: example},
				evaluators:   []*store.EvalEvaluator{{Type: evaluatorTypeHeuristic}},
			}
			if tc.inFlight {
				state.pending["task-1"] = &pendingTask{exampleID: example.ID, example: example, startedAt: time.Now()}
			}
			if tc.unreadableResults {
				if _, err := db.ExecContext(ctx, `ALTER TABLE eval_run_results RENAME TO eval_run_results_gone`); err != nil {
					t.Fatalf("rename eval_run_results: %v", err)
				}
			}

			reporter := &recordingRunReporter{}
			svc := NewExecutionService(ExecutionServiceParams{Runs: runs, Results: store.NewEvalRunResultRepo(db), Analytics: reporter})
			svc.finalizeCancelledRun(ctx, run.ID, exp, state, tc.cause)

			if len(reporter.shapes) != 1 || reporter.shapes[0] == nil {
				t.Fatalf("reported shapes = %v, want one", reporter.shapes)
			}
			shape := reporter.shapes[0]
			if !slices.Equal(shape.TrialFailureKinds, tc.want) || shape.TrialFailureKindsUnknown != tc.wantUnknown {
				t.Errorf("failure kinds = %v (unknown %v), want %v (unknown %v)", shape.TrialFailureKinds, shape.TrialFailureKindsUnknown, tc.want, tc.wantUnknown)
			}
			if !shape.MultiTrial || !slices.Equal(shape.EvaluatorTypes, []string{taxonomy.EvaluatorTypeHeuristic}) {
				t.Errorf("shape = %+v, want several trials and one heuristic evaluator", *shape)
			}
			if got := reporter.targetTypes[0]; got != tc.targetType {
				t.Errorf("target type = %q, want %q", got, tc.targetType)
			}
		})
	}
}

func TestFinalizeCancelledRunSkipsReportingOnShutdown(t *testing.T) {
	db := store.TestDB(t)
	runs := store.NewEvalRunRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	exp := &store.EvalExperiment{Name: "shutdown-fixture", TargetAgent: "Orchestrator", Models: "[]", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := expRepo.Create(t.Context(), exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID}
	if err := runs.Create(t.Context(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if ok, err := runs.SetRunning(t.Context(), run.ID, 1); err != nil || !ok {
		t.Fatalf("set running: ok=%v err=%v", ok, err)
	}

	reporter := &recordingRunReporter{}
	svc := NewExecutionService(ExecutionServiceParams{Runs: runs, Analytics: reporter})

	svc.finalizeCancelledRun(t.Context(), run.ID, exp, &runState{pending: map[string]*pendingTask{}}, causeShutdown)

	if len(reporter.outcomes) != 0 {
		t.Errorf("reported outcomes = %v, want none for a shutdown", reporter.outcomes)
	}
	got, err := runs.GetByID(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != StatusRunning {
		t.Errorf("run status = %q, want %q — shutdown must leave it for the watchdog", got.Status, StatusRunning)
	}
}

func seedRunWithDecisionScores(t *testing.T, db *store.DB, runID string, details []ScoreDetails) *store.EvalEvaluator {
	t.Helper()
	ctx := t.Context()
	dataset := &store.EvalDataset{Name: "decision-scores", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := store.NewEvalDatasetRepo(db).Create(ctx, dataset); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ev := &store.EvalEvaluator{Name: "decision", Type: evaluatorTypeDecision, CreatedBy: "alice", UpdatedBy: "alice"}
	if err := store.NewEvalEvaluatorRepo(db).Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	answer := "answer"
	for i, d := range details {
		example := &store.EvalExample{DatasetID: dataset.ID, SequenceNumber: i + 1, Prompt: "q"}
		if err := store.NewEvalExampleRepo(db).Create(ctx, example); err != nil {
			t.Fatalf("create example: %v", err)
		}
		result := &store.EvalRunResult{RunID: runID, ExampleID: example.ID, AgentResponse: &answer}
		if err := store.NewEvalRunResultRepo(db).Create(ctx, result); err != nil {
			t.Fatalf("create result: %v", err)
		}
		body, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		encoded := string(body)
		if err := store.NewEvalRunScoreRepo(db).Create(ctx, &store.EvalRunScore{RunResultID: result.ID, EvaluatorID: ev.ID, Details: &encoded}); err != nil {
			t.Fatalf("create score: %v", err)
		}
	}
	return ev
}
