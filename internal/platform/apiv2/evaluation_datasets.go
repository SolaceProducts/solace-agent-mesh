// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Dataset struct {
	ID                    string  `json:"id" example:"0190f5e9-3c4d-7e5f-8a6b-7c8d9e0f1a2b"`
	Type                  string  `json:"type" example:"dataset"`
	Name                  string  `json:"name" example:"Support tickets"`
	Description           *string `json:"description" example:"Questions customers asked support last month."`
	IsDefault             bool    `json:"isDefault" example:"false"`
	ExampleCount          int     `json:"exampleCount" example:"25"`
	InputDataExampleCount int     `json:"inputDataExampleCount" example:"25"`
	ExperimentCount       int     `json:"experimentCount" example:"2"`
	CreatedBy             string  `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy             string  `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime           Instant `json:"createdTime"`
	UpdatedTime           Instant `json:"updatedTime"`
}

type CreateDatasetRequest struct {
	Name        string `json:"name" validate:"required,max=255" example:"Support tickets"`
	Description string `json:"description" validate:"max=1000" example:"Questions customers asked support last month."`
}

type UpdateDatasetRequest struct {
	ID          string        `path:"id" json:"-"`
	Name        Patch[string] `json:"name" example:"Support tickets, April"`
	Description Patch[string] `json:"description" clearable:"true" example:"Questions customers asked support in April."`
}

type listDatasetsRequest struct {
	Page Page      `json:"-"`
	Sort SortOrder `sort:"name,createdTime,updatedTime,exampleCount,experimentCount" json:"-"`
	Name string    `query:"name" json:"-" example:"support"`
}

const (
	datasetsPath    = evaluationsPath + "/datasets"
	datasetPath     = datasetsPath + "/{id}"
	datasetIDPath   = datasetsPath + "/{datasetId}"
	datasetResource = "dataset"
)

var datasetSortFields = map[string]store.EvalDatasetSortField{
	"name":            store.EvalDatasetSortName,
	"createdTime":     store.EvalDatasetSortCreatedTime,
	"updatedTime":     store.EvalDatasetSortUpdatedTime,
	"exampleCount":    store.EvalDatasetSortExampleCount,
	"experimentCount": store.EvalDatasetSortExperimentCount,
}

func datasetRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: datasetsPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listDatasets", Summary: "Get a list of evaluation datasets",
				Text: "Returns one page of datasets with their example and experiment counts, newest first unless sorted, optionally only those whose name contains some text.", Tag: datasetsTag},
			Handler: Handle(h.listDatasets, "Unable to list datasets")},
		{Method: http.MethodPost, Path: datasetsPath, Scope: rbac.ScopeEvaluationCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createDataset", Summary: "Create an evaluation dataset",
				Text: "Creates an empty dataset. Add examples with POST /evaluations/datasets/{datasetId}/examples. If the name is taken, the request fails with 409.", Tag: datasetsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.createDataset, "Unable to create dataset")},
		{Method: http.MethodGet, Path: datasetPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getDataset", Summary: "Get an evaluation dataset",
				Text: "Returns one dataset with its example and experiment counts.", Tag: datasetsTag},
			Handler: Handle(h.getDataset, "Unable to get dataset")},
		{Method: http.MethodPatch, Path: datasetPath, Scope: rbac.ScopeEvaluationUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateDataset", Summary: "Update an evaluation dataset",
				Text: "Changes the fields the body includes and keeps the rest. If another dataset already has the name, the request fails with 409.", Tag: datasetsTag,
				Errors: []int{http.StatusConflict}},
			Handler: Handle(h.updateDataset, "Unable to update dataset")},
		{Method: http.MethodDelete, Path: datasetPath, Scope: rbac.ScopeEvaluationDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteDataset", Summary: "Delete an evaluation dataset",
				Text: "Deletes the dataset with its examples and their files, its generation operations, and the run results scored against its examples. Experiments that ran the dataset are kept, with datasetId null. If one of those experiments has a run pending or running, the request fails with 422.", Tag: datasetsTag},
			Handler: Handle(h.deleteDataset, "Unable to delete dataset")},
	}
}

func (h *evaluationHandler) listDatasets(ctx context.Context, req listDatasetsRequest) (List[Dataset], error) {
	var sortBy, order string
	if req.Sort.Field != "" {
		sortBy, order = string(datasetSortFields[req.Sort.Field]), "asc"
		if req.Sort.Desc {
			order = "desc"
		}
	}
	resp, err := h.datasets.ListPaginated(ctx, req.Page.Number, req.Page.Size, req.Name, sortBy, order)
	if err != nil {
		return List[Dataset]{}, err
	}
	items := make([]Dataset, len(resp.Data))
	for i := range resp.Data {
		items[i] = toDataset(&resp.Data[i])
	}
	return List[Dataset]{Items: items, Total: resp.Meta.Pagination.Count}, nil
}

func (h *evaluationHandler) createDataset(ctx context.Context, req CreateDatasetRequest) (Dataset, error) {
	resp, err := h.datasets.Create(ctx, eval.CreateDatasetRequest{Name: req.Name, Description: req.Description},
		entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Dataset{}, camelFieldError(err)
	}
	return toDataset(resp), nil
}

func (h *evaluationHandler) getDataset(ctx context.Context, req IDRequest) (Dataset, error) {
	resp, err := h.datasets.Get(ctx, req.ID)
	if err != nil {
		return Dataset{}, err
	}
	return toDataset(resp), nil
}

func (h *evaluationHandler) updateDataset(ctx context.Context, req UpdateDatasetRequest) (Dataset, error) {
	var update eval.UpdateDatasetRequest
	details := map[string][]string{}
	update.Name, _ = RequiredPatch(details, "name", req.Name)
	if req.Description.Set {
		update.Description, update.DescriptionSet = &req.Description.Value, true
	}
	for field, problems := range update.Problems() {
		details[field] = append(details[field], problems...)
	}
	if len(details) > 0 {
		return Dataset{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	resp, err := h.datasets.Update(ctx, req.ID, update, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Dataset{}, camelFieldError(err)
	}
	return toDataset(resp), nil
}

func (h *evaluationHandler) deleteDataset(ctx context.Context, req IDRequest) (struct{}, error) {
	return struct{}{}, h.datasets.Delete(ctx, req.ID, entplatform.UserIDFromContext(ctx))
}

func toDataset(resp *eval.DatasetResponse) Dataset {
	return Dataset{
		ID:                    resp.ID,
		Type:                  datasetResource,
		Name:                  resp.Name,
		Description:           NullIfEmpty(resp.Description),
		IsDefault:             resp.IsDefault,
		ExampleCount:          resp.ExampleCount,
		InputDataExampleCount: resp.InputDataExampleCount,
		ExperimentCount:       resp.ExperimentCount,
		CreatedBy:             resp.CreatedBy,
		UpdatedBy:             resp.UpdatedBy,
		CreatedTime:           Instant(resp.CreatedTime),
		UpdatedTime:           Instant(resp.UpdatedTime),
	}
}
