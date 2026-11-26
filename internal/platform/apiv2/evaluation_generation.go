// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type GenerateDatasetRequest struct {
	TargetAgent string  `json:"targetAgent" validate:"required" example:"support_agent"`
	NumExamples int     `json:"numExamples" validate:"required,min=1" example:"10"`
	Focus       *string `json:"focus" example:"refund requests"`
	Caller      caller  `json:"-"`
}

type GenerateExamplesRequest struct {
	DatasetID   string  `path:"datasetId" json:"-"`
	TargetAgent string  `json:"targetAgent" validate:"required" example:"support_agent"`
	NumExamples int     `json:"numExamples" validate:"required,min=1" example:"10"`
	Focus       *string `json:"focus" example:"refund requests"`
	Caller      caller  `json:"-"`
}

type getDatasetOperationRequest struct {
	DatasetID string `path:"datasetId" json:"-"`
	ID        string `path:"id" json:"-"`
}

type listDatasetOperationsRequest struct {
	DatasetID string    `path:"datasetId" json:"-"`
	Page      Page      `json:"-"`
	Sort      SortOrder `sort:"createdTime" json:"-"`
}

const datasetOperationsPath = evaluationsPath + "/datasetOperations"

func generationRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodPut, Path: datasetsPath + "/generate", Scope: rbac.ScopeEvaluationCreate,
			Stability: StabilityDraft,
			Doc: Doc{ID: "generateDataset", Summary: "Initiate the generation of an evaluation dataset",
				Text: "Starts an AI writing a dataset of examples for an agent and returns 202 with the operation. Poll its Location until it finishes. The dataset is created only when generation succeeds, and the operation's resourceId then names it. A failed generation creates nothing. If AI generation is not configured, the request fails with 501.", Tag: datasetsTag,
				Errors: []int{http.StatusNotImplemented}},
			Handler: Handle(h.generateDataset, "Unable to generate dataset")},
		{Method: http.MethodGet, Path: datasetOperationsPath + "/{id}", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getDatasetGenerationOperation", Summary: "Get a dataset generation operation",
				Text: "Returns the operation PUT /evaluations/datasets/generate started: inProgress while the AI works, then succeeded with resourceId naming the new dataset, or failed. An operation in progress past the generation time limit reads as failed. If its dataset has been deleted, the request fails with 404.", Tag: datasetsTag},
			Handler: Handle(h.getDatasetGeneration, "Unable to get operation")},
		{Method: http.MethodPut, Path: datasetIDPath + "/examples/generate", Scope: rbac.ScopeEvaluationCreate,
			Stability: StabilityDraft,
			Doc: Doc{ID: "generateExamples", Summary: "Initiate the generation of examples for an evaluation dataset",
				Text: "Starts an AI adding examples to the dataset and returns 202 with the operation. Poll its Location until it finishes. The examples are added only when generation succeeds. If the dataset is already generating examples, the request fails with 409. If the examples would take it past 5,000, the request fails with 422. If AI generation is not configured, the request fails with 501.", Tag: datasetsTag,
				Errors: []int{http.StatusConflict, http.StatusNotImplemented}},
			Handler: Handle(h.generateExamples, "Unable to generate examples")},
		{Method: http.MethodGet, Path: datasetIDPath + "/operations/{id}", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getDatasetOperation", Summary: "Get an operation of an evaluation dataset",
				Text: "Returns an operation that acted on the dataset: inProgress while the AI works, then succeeded or failed. An operation in progress past the generation time limit reads as failed. If the operation acted on another dataset, the request fails with 404.", Tag: datasetsTag},
			Handler: Handle(h.getDatasetOperation, "Unable to get operation")},
		{Method: http.MethodGet, Path: datasetIDPath + "/operations", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listDatasetOperations", Summary: "Get a list of an evaluation dataset's operations",
				Text: "Returns one page of the operations that acted on the dataset, newest first: the generation that created it, if an AI did, and every generation of more examples.", Tag: datasetsTag},
			Handler: Handle(h.listDatasetOperations, "Unable to list operations")},
	}
}

func (h *evaluationHandler) generateDataset(ctx context.Context, req GenerateDatasetRequest) (Accepted, error) {
	op, err := h.datasets.StartDatasetGeneration(ctx, eval.GenerateDatasetRequest{
		TargetAgent: req.TargetAgent, NumExamples: req.NumExamples, Focus: req.Focus,
	}, entplatform.UserIDFromContext(ctx), requesterID(ctx), req.Caller.BaseURL, req.Caller.Authorization)
	if err != nil {
		return Accepted{}, generationError(err)
	}
	return Accepted{Operation: toGenerationOperation(op), Location: generationLocation(op)}, nil
}

func (h *evaluationHandler) generateExamples(ctx context.Context, req GenerateExamplesRequest) (Accepted, error) {
	op, err := h.datasets.StartExampleGeneration(ctx, req.DatasetID, eval.GenerateExamplesRequest{
		TargetAgent: req.TargetAgent, NumExamples: req.NumExamples, Focus: req.Focus,
	}, entplatform.UserIDFromContext(ctx), requesterID(ctx), req.Caller.BaseURL, req.Caller.Authorization)
	if err != nil {
		return Accepted{}, generationError(err)
	}
	return Accepted{Operation: toGenerationOperation(op), Location: generationLocation(op)}, nil
}

func requesterID(ctx context.Context) string {
	if identity, ok := auth.SubjectFromContext(ctx); ok && identity != nil {
		return identity.PlatformUserID()
	}
	return ""
}

func (h *evaluationHandler) getDatasetGeneration(ctx context.Context, req IDRequest) (Operation, error) {
	op, err := h.datasets.GetGeneration(ctx, req.ID)
	if err != nil {
		return Operation{}, err
	}
	if op.OperationType != store.EvalGenerationDataset {
		return Operation{}, &apierr.NotFoundError{Resource: "EvalGenerationOperation", ID: req.ID}
	}
	return toGenerationOperation(op), nil
}

func (h *evaluationHandler) getDatasetOperation(ctx context.Context, req getDatasetOperationRequest) (Operation, error) {
	op, err := h.datasets.GetGeneration(ctx, req.ID)
	if err != nil {
		return Operation{}, err
	}
	if op.DatasetID == nil || !strings.EqualFold(*op.DatasetID, req.DatasetID) {
		return Operation{}, &apierr.NotFoundError{Resource: "EvalGenerationOperation", ID: req.ID}
	}
	return toGenerationOperation(op), nil
}

func (h *evaluationHandler) listDatasetOperations(ctx context.Context, req listDatasetOperationsRequest) (List[Operation], error) {
	ops, total, err := h.datasets.ListGenerations(ctx, req.DatasetID, req.Page.Number, req.Page.Size, req.Sort.Field != "" && !req.Sort.Desc)
	if err != nil {
		return List[Operation]{}, err
	}
	items := make([]Operation, len(ops))
	for i, op := range ops {
		items[i] = toGenerationOperation(op)
	}
	return List[Operation]{Items: items, Total: total}, nil
}

func generationError(err error) error {
	if fd, ok := errors.AsType[*eval.FeatureDisabledError](err); ok {
		return &CallerError{Status: http.StatusNotImplemented, Message: fd.Message, Cause: err}
	}
	if br, ok := errors.AsType[*apierr.BusinessRuleViolationError](err); ok && br.Code == eval.CodeExampleCapReached {
		return &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed,
			Details: map[string][]string{"numExamples": {br.Message}}, Cause: err}
	}
	return camelFieldError(err)
}

func generationLocation(op *store.EvalGenerationOperation) string {
	if op.OperationType == store.EvalGenerationDataset {
		return platpkg.APIV2Prefix + datasetOperationsPath + "/" + op.ID
	}
	return platpkg.APIV2Prefix + datasetsPath + "/" + orEmpty(op.DatasetID) + "/operations/" + op.ID
}

func toGenerationOperation(op *store.EvalGenerationOperation) Operation {
	out := Operation{
		ID:            op.ID,
		Type:          operationResource,
		ResourceID:    op.DatasetID,
		OperationType: op.OperationType,
		CreatedBy:     op.CreatedBy,
		CreatedTime:   Instant(op.CreatedTime),
		CompletedTime: InstantOf(op.CompletedTime),
		Status:        op.Status,
	}
	if op.Status == store.EvalGenerationFailed && op.ErrorMessage != nil && op.ErrorID != nil {
		out.Error = &OperationError{Message: *op.ErrorMessage, ErrorID: *op.ErrorID}
	}
	return out
}
