// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (f *runServiceFixture) twoModelExperiment(t *testing.T, name string) string {
	t.Helper()
	exp := &store.EvalExperiment{
		Name:           name,
		TargetAgent:    "Orchestrator",
		Models:         `[{"model":"m-b","alias":"b"},{"model":"m-a","alias":"a"}]`,
		RunsPerExample: 1,
		MaxWorkers:     1,
		DatasetID:      &f.datasetID,
		CreatedBy:      "alice",
		UpdatedBy:      "alice",
	}
	if err := f.expRepo.Create(f.ctx, exp, []string{f.evaluatorID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	return exp.ID
}

func TestRunServiceListRunsPaginated(t *testing.T) {
	f := newRunFixture(t, 1)
	for range 3 {
		if _, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual"); err != nil {
			t.Fatalf("trigger: %v", err)
		}
		f.finishAllRuns(t, StatusCompleted)
	}

	rows, total, err := f.svc.ListRunsPaginated(f.ctx, f.experimentID, 2, 2)
	if err != nil || total != 3 || len(rows) != 1 {
		t.Fatalf("page 2 of size 2 = %d rows, total %d, err %v; want one row and the total across pages", len(rows), total, err)
	}
	if _, _, err := f.svc.ListRunsPaginated(f.ctx, idgen.New(), 1, 10); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("unknown experiment = %v, want NotFoundError", err)
	}
}

func (f *runServiceFixture) finishAllRuns(t *testing.T, status string) {
	t.Helper()
	runs, err := f.svc.ListRuns(f.ctx, f.experimentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if _, err := f.runRepo.UpdateStatus(f.ctx, r.ID, status); err != nil {
			t.Fatalf("update status: %v", err)
		}
	}
}

func TestRunServiceGetRunRecord(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	row, err := f.svc.GetRunRecord(f.ctx, runs[0].ID)
	if err != nil || row.ID != runs[0].ID || row.ConfigSnapshot == nil {
		t.Fatalf("row = %+v, err %v; want the stored row with its snapshot", row, err)
	}
	if _, err := f.svc.GetRunRecord(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("unknown run = %v, want NotFoundError", err)
	}
}

func TestRunServiceCancelRunRecord(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	row, err := f.svc.CancelRunRecord(f.ctx, runs[0].ID)
	if err != nil || row.Status != StatusCancelled || row.CompletedTime == nil {
		t.Fatalf("cancelled = %+v, err %v; want the stored row, cancelled and completed", row, err)
	}
	if _, err = f.svc.CancelRunRecord(f.ctx, runs[0].ID); !errors.As(err, new(*BusinessRuleViolationError)) {
		t.Errorf("cancelling a finished run = %v, want BusinessRuleViolationError", err)
	}
	if _, err = f.svc.CancelRunRecord(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("unknown run = %v, want NotFoundError", err)
	}
	cancelled, err := f.svc.CancelRun(f.ctx, idgen.New())
	if cancelled != nil || !errors.As(err, new(*NotFoundError)) {
		t.Errorf("v1 cancel of an unknown run = %v, %v; want NotFoundError", cancelled, err)
	}
}

func TestRunServiceRunGroupReads(t *testing.T) {
	f := newRunFixture(t, 1)
	expID := f.twoModelExperiment(t, "two-models")
	first, err := f.svc.TriggerRuns(f.ctx, expID, "alice", "first", "manual")
	if err != nil || len(first) != 2 {
		t.Fatalf("trigger = %d runs, err %v; want two", len(first), err)
	}
	if _, err = f.svc.TriggerRuns(f.ctx, expID, "alice", "second", "manual"); err != nil {
		t.Fatalf("trigger: %v", err)
	}

	rows, err := f.svc.ListRunGroupRuns(f.ctx, first[0].RunGroupID)
	if err != nil || len(rows) != 2 || rows[0].Model != "m-a" || rows[1].Model != "m-b" {
		t.Fatalf("group runs = %+v, err %v; want both runs ordered by model", rows, err)
	}
	if _, err = f.svc.ListRunGroupRuns(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("unknown group = %v, want NotFoundError", err)
	}

	groups, total, err := f.svc.ListExperimentRunGroupsPaginated(f.ctx, expID, 1, 1)
	if err != nil || total != 2 || len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("groups = %d, total %d, err %v; want one group of two runs and the total across pages", len(groups), total, err)
	}
	if _, _, err = f.svc.ListExperimentRunGroupsPaginated(f.ctx, idgen.New(), 1, 10); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("unknown experiment = %v, want NotFoundError", err)
	}

	detail, err := f.svc.GetRunGroupDetail(f.ctx, first[0].RunGroupID)
	if err != nil || len(detail.Runs) != 2 || detail.ConfigOutdated {
		t.Errorf("detail = %+v, err %v; want two runs and no staleness check without an experiment service", detail, err)
	}
	if _, err := f.svc.GetRunGroupDetail(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("unknown group detail = %v, want NotFoundError", err)
	}
}

func TestRunServiceListRunGroupModelResultsPaginated(t *testing.T) {
	f := newRunFixture(t, 1)
	expID := f.twoModelExperiment(t, "token-models")
	runs, err := f.svc.TriggerRuns(f.ctx, expID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	groupID := runs[0].RunGroupID
	byModel := map[string]string{}
	for _, r := range runs {
		byModel[r.Model] = r.ID
	}

	in := func(n int64) *int64 { return &n }
	for i, tokens := range []struct{ in, out *int64 }{{in(100), in(10)}, {in(300), nil}, {nil, nil}} {
		res := &store.EvalRunResult{
			RunID: byModel["m-a"], ExampleID: f.exampleIDs[0], TrialIndex: i,
			InputTokens: tokens.in, OutputTokens: tokens.out,
		}
		if err = f.resultRepo.Create(f.ctx, res); err != nil {
			t.Fatalf("create result: %v", err)
		}
	}

	results, total, err := f.svc.ListRunGroupModelResultsPaginated(f.ctx, groupID, 1, 10)
	if err != nil || total != 2 || len(results) != 2 {
		t.Fatalf("results = %+v, total %d, err %v; want one per model", results, total, err)
	}
	if results[0].Model != "m-a" || results[0].RunID != byModel["m-a"] || results[1].Model != "m-b" {
		t.Errorf("results = %+v, want ordered by model", results)
	}
	a := results[0].TokenSummary
	if a.UsageResultCount != 2 || a.MeanInputTokens == nil || *a.MeanInputTokens != 200 || a.MeanOutputTokens == nil || *a.MeanOutputTokens != 10 || a.MeanTotalTokens != nil {
		t.Errorf("m-a summary = %+v, want two trials with usage, each mean over the trials that reported it", a)
	}
	if b := results[1].TokenSummary; b.UsageResultCount != 0 || b.MeanInputTokens != nil {
		t.Errorf("m-b summary = %+v, want no usage", b)
	}

	page, total, err := f.svc.ListRunGroupModelResultsPaginated(f.ctx, groupID, 2, 1)
	if err != nil || total != 2 || len(page) != 1 || page[0].Model != "m-b" {
		t.Errorf("page 2 of size 1 = %+v, total %d, err %v; want the second model", page, total, err)
	}
	if _, _, err := f.svc.ListRunGroupModelResultsPaginated(f.ctx, idgen.New(), 1, 10); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("unknown group = %v, want NotFoundError", err)
	}
}

func TestRunServiceListDistinctRunTargetsPaginated(t *testing.T) {
	f := newRunFixture(t, 1)
	if _, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual"); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	orchestrator := []store.RunTarget{{Name: "Orchestrator", TargetType: store.EvalTargetTypeAgent}}
	targets, total, err := f.svc.ListDistinctRunTargetsPaginated(f.ctx, "", 1, 10)
	if err != nil || total != 1 || !slices.Equal(targets, orchestrator) {
		t.Errorf("targets = %v, total %d, err %v; want the experiment's agent", targets, total, err)
	}
	if targets, total, err = f.svc.ListDistinctRunTargetsPaginated(f.ctx, "", 2, 10); err != nil || total != 1 || len(targets) != 0 {
		t.Errorf("past the end = %v, total %d, err %v; want none and the total", targets, total, err)
	}
	if targets, total, err = f.svc.ListDistinctRunTargetsPaginated(f.ctx, store.EvalTargetTypeWorkflow, 1, 10); err != nil || total != 0 || len(targets) != 0 {
		t.Errorf("workflows = %v, total %d, err %v; want none", targets, total, err)
	}
}

func TestRunServiceTriggerRacingADeleteIsNotFound(t *testing.T) {
	f := newRunFixture(t, 1)
	deletedMidTrigger := func(context.Context, string, []*store.EvalRun) error {
		return fmt.Errorf("EvalRunRepo.CreateIfIdle: %w", store.ErrEvalNotFound)
	}
	_, err := f.svc.triggerRuns(f.ctx, f.experimentID, "alice", "", "manual", deletedMidTrigger)
	if nf, ok := errors.AsType[*NotFoundError](err); !ok || nf.Resource != "EvalExperiment" {
		t.Errorf("trigger = %v, want NotFoundError for the experiment", err)
	}
}

func TestRunServiceGetExperimentRunGroupStatesNamesTheMissingResource(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}

	if _, err := f.svc.GetExperimentRunGroupStates(f.ctx, idgen.New(), runs[0].RunGroupID); !isNotFoundOf(err, "EvalExperiment") {
		t.Errorf("unknown experiment = %v, want NotFoundError for EvalExperiment", err)
	}
	if _, err := f.svc.GetExperimentRunGroupStates(f.ctx, f.experimentID, idgen.New()); !isNotFoundOf(err, "EvalRunGroup") {
		t.Errorf("unknown group = %v, want NotFoundError for EvalRunGroup", err)
	}
	if rows, err := f.svc.GetExperimentRunGroupStates(f.ctx, f.experimentID, runs[0].RunGroupID); err != nil || len(rows) != 1 {
		t.Errorf("known group = %d rows, %v; want its one run", len(rows), err)
	}
}

func isNotFoundOf(err error, resource string) bool {
	var nf *NotFoundError
	return errors.As(err, &nf) && nf.Resource == resource
}
