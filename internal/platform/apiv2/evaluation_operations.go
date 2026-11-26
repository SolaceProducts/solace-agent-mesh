// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"
	"slices"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	operationTypeRun = "run"

	operationFailureMessage = "A run in this operation failed. Get the run by the error's errorId to see its status."
)

type getExperimentOperationRequest struct {
	ExperimentID string `path:"experimentId" json:"-"`
	ID           string `path:"id" json:"-"`
}

type listExperimentOperationsRequest struct {
	ExperimentID string `path:"experimentId" json:"-"`
	Page         Page   `json:"-"`
}

func operationRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: experimentIDPath + "/operations/{id}", Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getExperimentOperation", Summary: "Get an operation of an evaluation experiment",
				Text: "Returns the operation a trigger started. Its status is pending until the first run starts, and inProgress until every run has finished. After that, the status is failed if any run failed, else cancelled if any run was canceled, else succeeded. A run stopped at its time limit finishes with the results it has, so the operation can still end succeeded. That run's errorMessage says it was stopped. The status comes from the operation's remaining runs, so deleting one of them can change it. It can also differ from the run group's status, which stays pending while any run waits to start. If the operation belongs to another experiment, the request fails with 404.", Tag: runsTag},
			Handler: Handle(h.getOperation, "Unable to get operation")},
		{Method: http.MethodGet, Path: experimentIDPath + "/operations", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listExperimentOperations", Summary: "Get a list of an evaluation experiment's operations",
				Text: "Returns one page of the operations the experiment's triggers started, newest first.", Tag: runsTag},
			Handler: Handle(h.listOperations, "Unable to list operations")},
	}
}

func (h *evaluationHandler) getOperation(ctx context.Context, req getExperimentOperationRequest) (Operation, error) {
	rows, err := h.runs.GetExperimentRunGroupStates(ctx, req.ExperimentID, req.ID)
	if err != nil {
		return Operation{}, err
	}
	return toOperation(rows), nil
}

func (h *evaluationHandler) listOperations(ctx context.Context, req listExperimentOperationsRequest) (List[Operation], error) {
	groups, total, err := h.runs.ListExperimentRunGroupsPaginated(ctx, req.ExperimentID, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[Operation]{}, err
	}
	operations := make([]Operation, len(groups))
	for i, rows := range groups {
		operations[i] = toOperation(rows)
	}
	return List[Operation]{Items: operations, Total: total}, nil
}

func operationLocation(experimentID, operationID string) string {
	return platpkg.APIV2Prefix + experimentsPath + "/" + experimentID + "/operations/" + operationID
}

func toOperation(rows []*store.EvalRun) Operation {
	fold := eval.FoldRunGroup(rows)
	statuses := make([]string, len(rows))
	for i, r := range rows {
		statuses[i] = r.Status
	}
	op := Operation{
		ID:            rows[0].RunGroupID,
		Type:          operationResource,
		ResourceID:    &rows[0].ExperimentID,
		OperationType: operationTypeRun,
		CreatedBy:     rows[0].StartedBy,
		CreatedTime:   Instant(fold.StartedTime),
		CompletedTime: InstantOf(fold.CompletedTime),
		Status:        operationStatus(statuses),
	}
	if op.Status == operationFailed {
		op.Error = &OperationError{Message: operationFailureMessage, ErrorID: failedRunID(rows)}
	}
	return op
}

func failedRunID(rows []*store.EvalRun) string {
	i := slices.IndexFunc(rows, func(r *store.EvalRun) bool { return r.Status == eval.StatusFailed })
	return rows[i].ID
}

func operationStatus(statuses []string) string {
	all := func(match func(string) bool) bool {
		return !slices.ContainsFunc(statuses, func(s string) bool { return !match(s) })
	}
	switch {
	case all(func(s string) bool { return s == eval.StatusPending }):
		return operationPending
	case !all(eval.IsTerminalStatus):
		return operationInProgress
	case slices.Contains(statuses, eval.StatusFailed):
		return operationFailed
	case slices.Contains(statuses, eval.StatusCancelled):
		return operationCancelled
	default:
		return operationSucceeded
	}
}
