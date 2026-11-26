// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const validDecisionConfig = `{"question":{"type":"choice","instructions":"Answered?","criteria":[{"key":"answered"},{"key":"missed"}]},"scoring":"chosen","reads":["response"]}`

type decisionTriggerFixture struct {
	runSvc       *RunService
	runRepo      *store.EvalRunRepo
	experimentID string
}

func newDecisionTriggerFixture(t *testing.T, decisionConfig string, lookup modelSnapshotLookup) decisionTriggerFixture {
	t.Helper()
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	ctx := t.Context()

	ds := &store.EvalDataset{Name: "fixture", CreatedBy: "a", UpdatedBy: "a"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "ping"}); err != nil {
		t.Fatalf("create example: %v", err)
	}
	dec := &store.EvalEvaluator{
		Name: "answers", Type: evaluatorTypeDecision, Model: new("jev"),
		ChoiceScores:   new(`{"answered":1,"missed":0}`),
		DecisionConfig: &decisionConfig,
		CreatedBy:      "a", UpdatedBy: "a",
	}
	if err := evRepo.Create(ctx, dec); err != nil {
		t.Fatalf("create decision evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID, CreatedBy: "a", UpdatedBy: "a",
	}
	if err := expRepo.Create(ctx, exp, []string{dec.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: store.NewEvalRunResultRepo(db), Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		ModelResolver:       func(string) (map[string]any, map[string]any) { return nil, nil },
		DecisionModelLookup: lookup,
	})
	return decisionTriggerFixture{runSvc: runSvc, runRepo: runRepo, experimentID: exp.ID}
}

func (fx decisionTriggerFixture) assertRuns(t *testing.T, want int) {
	t.Helper()
	runs, err := fx.runRepo.ListByExperiment(t.Context(), fx.experimentID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != want {
		t.Fatalf("trigger created %d run rows, want %d", len(runs), want)
	}
}

func (fx decisionTriggerFixture) withDecisionFlag(on bool) decisionTriggerFixture {
	fx.runSvc.featureRegistry = decisionFlag(on)
	return fx
}

func (fx decisionTriggerFixture) assertRefusedForAuthor(t *testing.T, wantState string) {
	t.Helper()
	fx.assertRefusedOnEvaluatorIDs(t, `"answers"`, `"jev"`, wantState)
}

func (fx decisionTriggerFixture) assertRefusedOnEvaluatorIDs(t *testing.T, wants ...string) {
	t.Helper()
	_, err := fx.runSvc.TriggerRuns(t.Context(), fx.experimentID, "a", "decision", "manual")
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want *ValidationError", err, err)
	}
	if ve.Field != "evaluatorIds" || len(ve.Details["evaluatorIds"]) != 1 {
		t.Fatalf("validation error = %+v, want one detail on field evaluatorIds", ve)
	}
	for _, want := range wants {
		if !strings.Contains(ve.Message, want) {
			t.Fatalf("validation message = %q, want it to contain %q", ve.Message, want)
		}
	}
	fx.assertRuns(t, 0)
}

func (fx decisionTriggerFixture) assertRefusedInternally(t *testing.T, wantText string) {
	t.Helper()
	_, err := fx.runSvc.TriggerRuns(t.Context(), fx.experimentID, "a", "decision", "manual")
	if err == nil {
		t.Fatal("trigger succeeded, want it refused")
	}
	if _, ok := errors.AsType[*ValidationError](err); ok {
		t.Fatalf("err = %v, want an internal error, not a *ValidationError", err)
	}
	if !strings.Contains(err.Error(), wantText) {
		t.Fatalf("err = %v, want it to contain %q", err, wantText)
	}
	fx.assertRuns(t, 0)
}

func TestTriggerRunsDecisionSnapshotRefusesUnresolvedModel(t *testing.T) {
	fx := newDecisionTriggerFixture(t, validDecisionConfig, knowsNoModel)
	fx.assertRefusedForAuthor(t, "is not configured")
}

func TestTriggerRunsDecisionSnapshotRefusesChatModel(t *testing.T) {
	chat := func(context.Context, string) (map[string]any, map[string]any, error) {
		return map[string]any{"model": "gpt-4o"}, map[string]any{"alias": "jev"}, nil
	}
	fx := newDecisionTriggerFixture(t, validDecisionConfig, chat)
	fx.assertRefusedForAuthor(t, "chat model")
}

func TestTriggerRunsDecisionSnapshotStoreFailureIsInternal(t *testing.T) {
	failing := func(context.Context, string) (map[string]any, map[string]any, error) {
		return nil, nil, errors.New("connection refused")
	}
	fx := newDecisionTriggerFixture(t, validDecisionConfig, failing)
	fx.assertRefusedInternally(t, "connection refused")
}

func TestTriggerRunsDecisionSnapshotUsesDecisionLookup(t *testing.T) {
	decisionModel := func(context.Context, string) (map[string]any, map[string]any, error) {
		return resolvesDecisionModel()
	}
	fx := newDecisionTriggerFixture(t, validDecisionConfig, decisionModel)
	if _, err := fx.runSvc.TriggerRuns(t.Context(), fx.experimentID, "a", "decision", "manual"); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	fx.assertRuns(t, 1)
}

func TestTriggerRunsDecisionSnapshotRefusesWhileFlagOff(t *testing.T) {
	looked := false
	lookup := func(context.Context, string) (map[string]any, map[string]any, error) {
		looked = true
		return resolvesDecisionModel()
	}
	fx := newDecisionTriggerFixture(t, validDecisionConfig, lookup).withDecisionFlag(false)
	fx.assertRefusedOnEvaluatorIDs(t, `"answers"`, "SAM_FEATURE_EVAL_DECISION_EVALUATOR")
	if looked {
		t.Fatal("the decision model was looked up although decision evaluators are off")
	}
}

func TestTriggerRunsDecisionSnapshotRunsWhileFlagOn(t *testing.T) {
	lookup := func(context.Context, string) (map[string]any, map[string]any, error) {
		return resolvesDecisionModel()
	}
	fx := newDecisionTriggerFixture(t, validDecisionConfig, lookup).withDecisionFlag(true)
	if _, err := fx.runSvc.TriggerRuns(t.Context(), fx.experimentID, "a", "decision", "manual"); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	fx.assertRuns(t, 1)
}

func TestTriggerRunsDecisionSnapshotRefusesUnreadableSettings(t *testing.T) {
	fx := newDecisionTriggerFixture(t, "{bad", knowsNoModel)
	fx.assertRefusedInternally(t, "decision settings could not be read")
}

func TestTriggerRunsDecisionSnapshotPassesTheTriggerContext(t *testing.T) {
	var captured context.Context
	lookup := func(ctx context.Context, _ string) (map[string]any, map[string]any, error) {
		captured = ctx
		return nil, nil, nil
	}
	fx := newDecisionTriggerFixture(t, validDecisionConfig, lookup)
	ctx, cancel := context.WithCancel(t.Context())
	_, _ = fx.runSvc.TriggerRuns(ctx, fx.experimentID, "a", "decision", "manual")
	cancel()
	if captured == nil || captured.Err() == nil {
		t.Fatal("the decision model lookup did not run under the trigger's context")
	}
}

func TestTriggerRunsDecisionSnapshotNeedsTheLookup(t *testing.T) {
	fx := newDecisionTriggerFixture(t, validDecisionConfig, nil)
	fx.assertRefusedInternally(t, "no decision model lookup is configured")
}

func knowsNoModel(context.Context, string) (map[string]any, map[string]any, error) {
	return nil, nil, nil
}

func resolvesDecisionModel() (raw, snapshot map[string]any, err error) {
	return map[string]any{samapp.ModelTypeKey: samapp.DecisionModelType, "model": "jev-1.13.0"},
		map[string]any{"id": "m1", "alias": "jev"}, nil
}
