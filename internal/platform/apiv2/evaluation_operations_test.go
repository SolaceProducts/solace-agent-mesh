// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestOperations_StatusFoldsTheRunsOfOneTrigger(t *testing.T) {
	const (
		pending   = eval.StatusPending
		running   = eval.StatusRunning
		completed = eval.StatusCompleted
		warnings  = eval.StatusCompletedWithWarnings
		failed    = eval.StatusFailed
		cancelled = eval.StatusCancelled
	)
	for _, tc := range []struct {
		runs []string
		want string
	}{
		{[]string{pending, pending}, operationPending},
		{[]string{running, pending}, operationInProgress},
		{[]string{completed, running}, operationInProgress},
		{[]string{failed, running}, operationInProgress},
		{[]string{completed, pending}, operationInProgress},
		{[]string{completed, completed}, operationSucceeded},
		{[]string{completed, warnings}, operationSucceeded},
		{[]string{completed, failed}, operationFailed},
		{[]string{failed, failed}, operationFailed},
		{[]string{cancelled, cancelled}, operationCancelled},
		{[]string{cancelled, completed}, operationCancelled},
		{[]string{cancelled, failed}, operationFailed},
		{[]string{cancelled, pending}, operationInProgress},
		{[]string{completed}, operationSucceeded},
		{[]string{pending}, operationPending},
	} {
		if got := operationStatus(tc.runs); got != tc.want {
			t.Errorf("operationStatus(%v) = %q, want %q", tc.runs, got, tc.want)
		}
	}
}

func TestOperations_ReadThroughTheAPI(t *testing.T) {
	f := newExperimentFixture(t)
	for _, tc := range []struct {
		name       string
		runs       []string
		want       string
		completed  bool
		failedRuns int
	}{
		{"both pending", []string{eval.StatusPending, eval.StatusPending}, operationPending, false, 0},
		{"one running", []string{eval.StatusRunning, eval.StatusPending}, operationInProgress, false, 0},
		{"one done one running", []string{eval.StatusCompleted, eval.StatusRunning}, operationInProgress, false, 0},
		{"one done one pending", []string{eval.StatusCompleted, eval.StatusPending}, operationInProgress, false, 0},
		{"both done", []string{eval.StatusCompleted, eval.StatusCompleted}, operationSucceeded, true, 0},
		{"done with warnings", []string{eval.StatusCompleted, eval.StatusCompletedWithWarnings}, operationSucceeded, true, 0},
		{"one failed", []string{eval.StatusCompleted, eval.StatusFailed}, operationFailed, true, 1},
		{"both failed", []string{eval.StatusFailed, eval.StatusFailed}, operationFailed, true, 2},
		{"both cancelled", []string{eval.StatusCancelled, eval.StatusCancelled}, operationCancelled, true, 0},
		{"cancelled and done", []string{eval.StatusCancelled, eval.StatusCompleted}, operationCancelled, true, 0},
		{"cancelled and failed", []string{eval.StatusCancelled, eval.StatusFailed}, operationFailed, true, 1},
		{"cancelled and pending", []string{eval.StatusCancelled, eval.StatusPending}, operationInProgress, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			experiment := f.createWithModels("operation-"+strings.ReplaceAll(tc.name, " ", "-"), "support_agent", "model-a", "model-b")
			op := f.trigger(experiment.ID, map[string]any{})
			runs := f.groupRuns(op.ID)
			if len(runs) != len(tc.runs) {
				t.Fatalf("started %d runs, want %d", len(runs), len(tc.runs))
			}
			var failedIDs []string
			for i, status := range tc.runs {
				f.finish(runs[i].ID, status)
				if status == eval.StatusFailed {
					failedIDs = append(failedIDs, runs[i].ID)
				}
			}

			rec := f.api.call(http.MethodGet, experimentsPath+"/"+experiment.ID+"/operations/"+op.ID, nil)
			expectStatus(t, rec, http.StatusOK)
			raw := rawData(t, rec)
			got := decodeData[Operation](t, rec)
			if got.Status != tc.want || got.ID != op.ID || *got.ResourceID != experiment.ID || got.OperationType != "run" || got.Type != "operation" {
				t.Errorf("operation = %+v, want status %s", got, tc.want)
			}
			expectISOTimes(t, raw, "createdTime")
			if tc.completed {
				expectISOTimes(t, raw, "completedTime")
			} else {
				expectNull(t, raw, "completedTime")
			}
			if tc.failedRuns == 0 {
				expectNull(t, raw, "error")
				return
			}
			if got.Error == nil || got.Error.Message != operationFailureMessage || !slices.Contains(failedIDs, got.Error.ErrorID) {
				t.Fatalf("error = %+v, want the SAM sentence and the id of one of the failed runs %v", got.Error, failedIDs)
			}
			if strings.Contains(rec.Body.String(), rawRunFailure) {
				t.Errorf("operation = %s, must not carry the run's own error text", rec.Body.String())
			}
		})
	}
}

func TestOperations_CompletedTimeIsTheLatestCompletion(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.createWithModels("operation-completion-experiment", "support_agent", "model-a", "model-b")
	op := f.trigger(experiment.ID, map[string]any{})
	runs := f.groupRuns(op.ID)
	for _, run := range runs {
		f.finish(run.ID, eval.StatusCompleted)
	}
	f.exec(`UPDATE eval_runs SET started_time = 1700000000000, completed_time = 1700000100000 WHERE id = $1`, runs[0].ID)
	f.exec(`UPDATE eval_runs SET started_time = 1700000005000, completed_time = 1700000900000 WHERE id = $1`, runs[1].ID)

	got := decodeData[Operation](t, f.api.call(http.MethodGet, experimentsPath+"/"+experiment.ID+"/operations/"+op.ID, nil))
	if got.CreatedTime != Instant(1700000000000) || got.CompletedTime != Instant(1700000900000) {
		t.Errorf("times = %d and %d, want the earliest start and the latest completion", got.CreatedTime, got.CompletedTime)
	}
}

func TestOperations_FetchValidation(t *testing.T) {
	f := newExperimentFixture(t)
	one := f.create("operations-one", "support_agent")
	two := f.create("operations-two", "support_agent")
	op := f.trigger(one.ID, map[string]any{})
	path := experimentsPath + "/" + one.ID + "/operations/"

	expectStatus(t, f.api.call(http.MethodGet, path+op.ID, nil), http.StatusOK)
	rec := f.api.call(http.MethodGet, experimentsPath+"/"+two.ID+"/operations/"+op.ID, nil)
	expectStatus(t, rec, http.StatusNotFound)
	decodeError(t, rec)
	rec = f.api.call(http.MethodGet, path+missingID, nil)
	expectStatus(t, rec, http.StatusNotFound)
	decodeError(t, rec)
	expectDetail(t, f.api.call(http.MethodGet, path+"not-a-uuid", nil), http.StatusBadRequest, "id")
	expectDetail(t, f.api.call(http.MethodGet, experimentsPath+"/not-a-uuid/operations/"+op.ID, nil), http.StatusBadRequest, "experimentId")
}

func TestOperations_GoneWithTheirRunGroup(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("operations-deleted-group", "support_agent")
	kept := f.trigger(experiment.ID, map[string]any{})
	f.finishAll(kept.ID, eval.StatusCompleted)
	gone := f.trigger(experiment.ID, map[string]any{})
	f.finishAll(gone.ID, eval.StatusCompleted)
	path := experimentsPath + "/" + experiment.ID + "/operations"

	expectStatus(t, f.api.call(http.MethodDelete, runGroupsPath+"/"+gone.ID, nil), http.StatusNoContent)

	rec := f.api.call(http.MethodGet, path+"/"+gone.ID, nil)
	expectStatus(t, rec, http.StatusNotFound)
	decodeError(t, rec)
	expectStatus(t, f.api.call(http.MethodGet, path+"/"+kept.ID, nil), http.StatusOK)
	list := decodeList[Operation](t, f.api.call(http.MethodGet, path, nil))
	if len(list.Data) != 1 || list.Data[0].ID != kept.ID || list.Meta.Pagination.Count != 1 {
		t.Errorf("operations = %+v, want only the one whose runs remain", list)
	}
}

func TestOperations_PagingNewestFirst(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("operations-paging", "support_agent")
	other := f.create("operations-paging-other", "support_agent")
	f.trigger(other.ID, map[string]any{})

	var ids []string
	for i := range 3 {
		op := f.trigger(experiment.ID, map[string]any{})
		f.finishAll(op.ID, eval.StatusCompleted)
		f.exec(`UPDATE eval_runs SET started_time = $1 WHERE run_group_id = $2`, 1_700_000_000_000+int64(i)*1000, op.ID)
		ids = append([]string{op.ID}, ids...)
	}
	path := experimentsPath + "/" + experiment.ID + "/operations"

	all := decodeList[Operation](t, f.api.call(http.MethodGet, path, nil))
	var got []string
	for _, op := range all.Data {
		got = append(got, op.ID)
	}
	if !slices.Equal(got, ids) || all.Meta.Pagination.Count != 3 {
		t.Errorf("operations = %v with count %d, want %v newest first and the other experiment's left out", got, all.Meta.Pagination.Count, ids)
	}
	second := decodeList[Operation](t, f.api.call(http.MethodGet, path+"?pageSize=2&pageNumber=2", nil))
	if len(second.Data) != 1 || second.Data[0].ID != ids[2] || second.Meta.Pagination.TotalPages != 2 {
		t.Errorf("page 2 = %+v, want the oldest operation", second)
	}
	if beyond := decodeList[Operation](t, f.api.call(http.MethodGet, path+"?pageNumber=9", nil)); len(beyond.Data) != 0 {
		t.Errorf("a page past the end has %d items", len(beyond.Data))
	}
	expectDetail(t, f.api.call(http.MethodGet, path+"?pageSize=0", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, f.api.call(http.MethodGet, experimentsPath+"/not-a-uuid/operations", nil), http.StatusBadRequest, "experimentId")
	expectStatus(t, f.api.call(http.MethodGet, experimentsPath+"/"+missingID+"/operations", nil), http.StatusNotFound)
}

func TestOperations_Scopes(t *testing.T) {
	f := newExperimentFixture(t)
	experiment := f.create("operations-scopes", "support_agent")
	op := f.trigger(experiment.ID, map[string]any{})

	for _, path := range []string{
		experimentsPath + "/" + experiment.ID + "/operations/" + op.ID,
		experimentsPath + "/" + experiment.ID + "/operations",
	} {
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeEvaluationRead), http.StatusOK)
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeEvaluationInvoke), http.StatusForbidden)
		expectStatus(t, f.api.call(http.MethodGet, path, nil, rbac.ScopeAgentBuilderRead), http.StatusForbidden)
	}
}
