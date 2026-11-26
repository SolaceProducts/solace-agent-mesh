// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Experiment struct {
	ID                 string            `json:"id" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	Type               string            `json:"type" example:"experiment"`
	Name               string            `json:"name" example:"Support answers, March"`
	Description        *string           `json:"description" example:"Checks the support agent against last month's tickets."`
	DatasetID          *string           `json:"datasetId" example:"0190f5e9-3c4d-7e5f-8a6b-7c8d9e0f1a2b"`
	DatasetName        *string           `json:"datasetName" example:"Support tickets"`
	DatasetUpdatedTime Instant           `json:"datasetUpdatedTime"`
	TargetType         string            `json:"targetType" example:"agent"`
	TargetAgent        string            `json:"targetAgent" example:"support_agent"`
	TargetWorkflow     *string           `json:"targetWorkflow" example:"order_exception_handler"`
	Models             []ExperimentModel `json:"models"`
	RunsPerExample     int               `json:"runsPerExample" example:"1"`
	MaxWorkers         int               `json:"maxWorkers" example:"4"`
	EvaluatorIDs       []string          `json:"evaluatorIds" example:"[\"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d\"]"`
	PrimaryEvaluatorID *string           `json:"primaryEvaluatorId" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	IsDefault          bool              `json:"isDefault" example:"false"`
	RunAs              *string           `json:"runAs" example:"system:nightly-evals"`
	LatestRunStatus    *string           `json:"latestRunStatus" example:"completed"`
	LatestRunTime      Instant           `json:"latestRunTime"`
	CreatedBy          string            `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy          string            `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime        Instant           `json:"createdTime"`
	UpdatedTime        Instant           `json:"updatedTime"`
}

type ExperimentModel struct {
	ModelConfigID *string `json:"modelConfigId" validate:"omitempty,uuid" example:"0190f5e7-7a8b-7c9d-8e0f-1a2b3c4d5e6f"`
	Model         string  `json:"model" example:"gpt-4o"`
	Alias         string  `json:"alias" example:"general"`
}

type CreateExperimentRequest struct {
	Name               string            `json:"name" validate:"required,max=255" example:"Support answers, March"`
	Description        string            `json:"description" example:"Checks the support agent against last month's tickets."`
	DatasetID          string            `json:"datasetId" validate:"required,uuid" example:"0190f5e9-3c4d-7e5f-8a6b-7c8d9e0f1a2b"`
	TargetAgent        string            `json:"targetAgent" validate:"omitempty,max=255" example:"support_agent"`
	TargetWorkflow     string            `json:"targetWorkflow" validate:"omitempty,max=255" example:"order_exception_handler"`
	Models             []ExperimentModel `json:"models" validate:"max=3,dive"`
	RunsPerExample     *int              `json:"runsPerExample" validate:"omitempty,min=1,max=10" example:"1"`
	MaxWorkers         *int              `json:"maxWorkers" validate:"omitempty,min=1,max=20" example:"4"`
	EvaluatorIDs       []string          `json:"evaluatorIds" validate:"required,min=1,max=5,dive,uuid" example:"[\"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d\"]"`
	PrimaryEvaluatorID *string           `json:"primaryEvaluatorId" validate:"omitempty,uuid" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	RunAs              *string           `json:"runAs" example:"nightly-evals"`
}

type UpdateExperimentRequest struct {
	ID                 string                   `path:"id" json:"-"`
	Name               Patch[string]            `json:"name" example:"Support answers, April"`
	Description        Patch[string]            `json:"description" clearable:"true" example:"Checks the support agent against this month's tickets."`
	DatasetID          Patch[string]            `json:"datasetId" example:"0190f5e9-3c4d-7e5f-8a6b-7c8d9e0f1a2b"`
	TargetAgent        Patch[string]            `json:"targetAgent" example:"support_agent"`
	TargetWorkflow     Patch[string]            `json:"targetWorkflow" example:"order_exception_handler"`
	Models             Patch[[]ExperimentModel] `json:"models" clearable:"true"`
	RunsPerExample     Patch[int]               `json:"runsPerExample" example:"2"`
	MaxWorkers         Patch[int]               `json:"maxWorkers" example:"8"`
	EvaluatorIDs       Patch[[]string]          `json:"evaluatorIds" example:"[\"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d\"]"`
	PrimaryEvaluatorID Patch[string]            `json:"primaryEvaluatorId" clearable:"true" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	RunAs              Patch[string]            `json:"runAs" clearable:"true" example:"nightly-evals"`
}

type listExperimentsRequest struct {
	Page        Page      `json:"-"`
	Sort        SortOrder `sort:"name,createdTime" json:"-"`
	DatasetID   string    `query:"datasetId" json:"-" example:"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d"`
	EvaluatorID string    `query:"evaluatorId" json:"-" example:"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d"`
	TargetAgent string    `query:"targetAgent" json:"-" example:"OrderAgent"`
}

const (
	experimentsPath = evaluationsPath + "/experiments"
	experimentPath  = experimentsPath + "/{id}"
)

func experimentRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: experimentsPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listExperiments", Summary: "Get a list of evaluation experiments",
				Text: "Returns one page of experiments, newest first unless sorted, optionally only those that run one dataset, are scored by one evaluator, or evaluate one agent.", Tag: experimentsTag},
			Handler: Handle(h.listExperiments, "Unable to list experiments")},
		{Method: http.MethodPost, Path: experimentsPath, Scope: rbac.ScopeEvaluationCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createExperiment", Summary: "Create an evaluation experiment",
				Text: "Creates an experiment without running it. Start a run with PUT /evaluations/experiments/{experimentId}/trigger. If the name is taken, the request fails with 409.", Tag: experimentsTag},
			Handler: Handle(h.createExperiment, "Unable to create experiment")},
		{Method: http.MethodGet, Path: evaluationsPath + "/sampleExperiment", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getSampleExperiment", Summary: "Get the sample evaluation experiment",
				Text: "Returns the sample experiment that Agent Mesh creates at installation. If the sample experiment has been deleted, the request fails with 404.", Tag: experimentsTag},
			Handler: Handle(h.sampleExperiment, "Unable to get sample experiment")},
		{Method: http.MethodGet, Path: experimentPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getExperiment", Summary: "Get an evaluation experiment",
				Text: "Returns one experiment with the status and start time of its latest run.", Tag: experimentsTag},
			Handler: Handle(h.getExperiment, "Unable to get experiment")},
		{Method: http.MethodPatch, Path: experimentPath, Scope: rbac.ScopeEvaluationUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateExperiment", Summary: "Update an evaluation experiment",
				Text: "Changes the fields the body includes and keeps the rest. Runs already started keep the settings they started with. If another experiment already has the name, the request fails with 409.", Tag: experimentsTag},
			Handler: Handle(h.updateExperiment, "Unable to update experiment")},
		{Method: http.MethodDelete, Path: experimentPath, Scope: rbac.ScopeEvaluationDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteExperiment", Summary: "Delete an evaluation experiment",
				Text: "Deletes the experiment with its runs and their results. If a run is pending or running, the request fails with 422. Cancel the run first.", Tag: experimentsTag},
			Handler: Handle(h.deleteExperiment, "Unable to delete experiment")},
	}
}

func (h *evaluationHandler) listExperiments(ctx context.Context, req listExperimentsRequest) (List[Experiment], error) {
	if err := CheckUUIDQuery("datasetId", req.DatasetID); err != nil {
		return List[Experiment]{}, err
	}
	if err := CheckUUIDQuery("evaluatorId", req.EvaluatorID); err != nil {
		return List[Experiment]{}, err
	}
	sortBy := map[string]store.EvalExperimentSortField{
		"name":        store.EvalExperimentSortName,
		"createdTime": store.EvalExperimentSortCreatedTime,
	}[req.Sort.Field]
	filter := store.EvalExperimentFilter{DatasetID: req.DatasetID, EvaluatorID: req.EvaluatorID, TargetAgent: req.TargetAgent}
	rows, total, err := h.experiments.ListPaginated(ctx, filter, req.Page.Number, req.Page.Size, sortBy, req.Sort.Desc)
	if err != nil {
		return List[Experiment]{}, err
	}
	items := make([]Experiment, len(rows))
	for i := range rows {
		items[i] = toExperiment(&rows[i])
	}
	return List[Experiment]{Items: items, Total: total}, nil
}

func (h *evaluationHandler) createExperiment(ctx context.Context, req CreateExperimentRequest) (Experiment, error) {
	resp, err := h.experiments.Create(ctx, eval.CreateExperimentRequest{
		Name:               req.Name,
		Description:        NullIfZero(req.Description),
		DatasetID:          req.DatasetID,
		TargetAgent:        req.TargetAgent,
		TargetWorkflow:     req.TargetWorkflow,
		Models:             fromExperimentModels(req.Models),
		RunsPerExample:     req.RunsPerExample,
		MaxWorkers:         req.MaxWorkers,
		EvaluatorIDs:       req.EvaluatorIDs,
		PrimaryEvaluatorID: req.PrimaryEvaluatorID,
		RunAs:              req.RunAs,
	}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Experiment{}, evaluationError(err)
	}
	h.analytics.EvalCreated(ctx, resp.TargetType)
	return toExperiment(resp), nil
}

func (h *evaluationHandler) sampleExperiment(ctx context.Context, _ struct{}) (Experiment, error) {
	resp, err := h.experiments.GetSample(ctx)
	if err != nil {
		return Experiment{}, err
	}
	return toExperiment(resp), nil
}

func (h *evaluationHandler) getExperiment(ctx context.Context, req IDRequest) (Experiment, error) {
	resp, err := h.experiments.Get(ctx, req.ID)
	if err != nil {
		return Experiment{}, err
	}
	return toExperiment(resp), nil
}

func (h *evaluationHandler) updateExperiment(ctx context.Context, req UpdateExperimentRequest) (Experiment, error) {
	update, err := req.toServiceRequest()
	if err != nil {
		return Experiment{}, err
	}
	resp, err := h.experiments.Update(ctx, req.ID, update, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Experiment{}, evaluationError(err)
	}
	return toExperiment(resp), nil
}

func (req UpdateExperimentRequest) toServiceRequest() (eval.UpdateExperimentRequest, error) {
	var update eval.UpdateExperimentRequest
	details := map[string][]string{}
	for _, f := range []struct {
		name  string
		patch Patch[string]
		dst   **string
	}{
		{"name", req.Name, &update.Name},
		{"datasetId", req.DatasetID, &update.DatasetID},
		{"targetAgent", req.TargetAgent, &update.TargetAgent},
		{"targetWorkflow", req.TargetWorkflow, &update.TargetWorkflow},
	} {
		switch {
		case f.patch.Set && (f.patch.Null || f.patch.Value == ""):
			details[f.name] = []string{"cannot be empty"}
		case f.patch.Set:
			*f.dst = &f.patch.Value
		}
	}
	if req.TargetAgent.Set && req.TargetWorkflow.Set {
		details["targetAgent"] = append(details["targetAgent"], msgOneExperimentTarget)
	}
	if req.DatasetID.Set && req.DatasetID.Value != "" && !IsCanonicalUUID(req.DatasetID.Value) {
		details["datasetId"] = []string{"must be a UUID"}
	}
	for _, f := range []struct {
		name  string
		patch Patch[int]
		dst   **int
	}{
		{"runsPerExample", req.RunsPerExample, &update.RunsPerExample},
		{"maxWorkers", req.MaxWorkers, &update.MaxWorkers},
	} {
		switch {
		case f.patch.Null:
			details[f.name] = []string{"cannot be null"}
		case f.patch.Set:
			*f.dst = &f.patch.Value
		}
	}
	switch {
	case req.EvaluatorIDs.Set && len(req.EvaluatorIDs.Value) == 0:
		details["evaluatorIds"] = []string{"cannot be empty"}
	case req.EvaluatorIDs.Set:
		update.EvaluatorIDs, update.EvaluatorIDsSet = req.EvaluatorIDs.Value, true
		for _, id := range req.EvaluatorIDs.Value {
			if !IsCanonicalUUID(id) {
				details["evaluatorIds"] = []string{"must be UUIDs"}
			}
		}
	}
	if req.Models.Set {
		if problems := modelProblems(req.Models.Value); len(problems) > 0 {
			details["models"] = problems
		}
	}
	if p := req.PrimaryEvaluatorID; p.Set && !p.Null && !IsCanonicalUUID(p.Value) {
		details["primaryEvaluatorId"] = []string{"must be a UUID"}
	}
	if len(details) > 0 {
		return update, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	if req.Description.Set {
		update.Description, update.DescriptionSet = &req.Description.Value, true
	}
	if req.Models.Set {
		update.Models, update.ModelsSet = fromExperimentModels(req.Models.Value), true
	}
	update.PrimaryEvaluatorID, update.PrimaryEvaluatorIDSet = patchPointer(req.PrimaryEvaluatorID)
	update.RunAs, update.RunAsSet = patchPointer(req.RunAs)
	return update, nil
}

func patchPointer[T any](p Patch[T]) (*T, bool) {
	if !p.Set || p.Null {
		return nil, p.Set
	}
	return &p.Value, true
}

func (h *evaluationHandler) deleteExperiment(ctx context.Context, req IDRequest) (struct{}, error) {
	if err := h.experiments.Delete(ctx, req.ID, entplatform.UserIDFromContext(ctx)); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, nil
}

func toExperiment(resp *eval.ExperimentResponse) Experiment {
	var description *string
	if resp.Description != nil {
		description = NullIfZero(*resp.Description)
	}
	return Experiment{
		ID:                 resp.ID,
		Type:               "experiment",
		Name:               resp.Name,
		Description:        description,
		DatasetID:          resp.DatasetID,
		DatasetName:        resp.DatasetName,
		DatasetUpdatedTime: InstantOf(resp.DatasetUpdatedTime),
		TargetType:         resp.TargetType,
		TargetAgent:        resp.TargetAgent,
		TargetWorkflow:     resp.TargetWorkflow,
		Models:             toExperimentModels(resp.Models),
		RunsPerExample:     resp.RunsPerExample,
		MaxWorkers:         resp.MaxWorkers,
		EvaluatorIDs:       nonNilList(resp.EvaluatorIDs),
		PrimaryEvaluatorID: resp.PrimaryEvaluatorID,
		IsDefault:          resp.IsDefault,
		RunAs:              resp.RunAs,
		LatestRunStatus:    resp.LatestRunStatus,
		LatestRunTime:      InstantOf(resp.LatestRunTime),
		CreatedBy:          resp.CreatedBy,
		UpdatedBy:          resp.UpdatedBy,
		CreatedTime:        Instant(resp.CreatedTime),
		UpdatedTime:        Instant(resp.UpdatedTime),
	}
}

const (
	msgExperimentTargetRequired = "targetAgent or targetWorkflow is required"
	msgOneExperimentTarget      = "set targetAgent or targetWorkflow, not both"
)

func (r CreateExperimentRequest) CrossFieldProblems() map[string][]string {
	problems := map[string][]string{}
	if models := modelProblems(r.Models); len(models) > 0 {
		problems["models"] = models
	}
	hasAgent, hasWorkflow := strings.TrimSpace(r.TargetAgent) != "", strings.TrimSpace(r.TargetWorkflow) != ""
	switch {
	case hasAgent && hasWorkflow:
		problems["targetAgent"] = []string{msgOneExperimentTarget}
	case !hasAgent && !hasWorkflow:
		problems["targetAgent"] = []string{msgExperimentTargetRequired}
	}
	return problems
}

func modelProblems(models []ExperimentModel) []string {
	var problems []string
	for i, m := range models {
		if m.Model == "" && m.Alias == "" && m.ModelConfigID == nil {
			problems = append(problems, fmt.Sprintf("models[%d] needs a modelConfigId, a model or an alias", i))
		}
		if m.ModelConfigID != nil && !IsCanonicalUUID(*m.ModelConfigID) {
			problems = append(problems, fmt.Sprintf("models[%d].modelConfigId must be a UUID", i))
		}
	}
	return problems
}

func toExperimentModels(models []map[string]any) []ExperimentModel {
	out := make([]ExperimentModel, len(models))
	for i, m := range models {
		configID, _ := m["modelConfigId"].(string)
		model, _ := m["model"].(string)
		alias, _ := m["alias"].(string)
		out[i] = ExperimentModel{ModelConfigID: NullIfZero(configID), Model: model, Alias: alias}
	}
	return out
}

func fromExperimentModels(models []ExperimentModel) []map[string]any {
	if models == nil {
		return nil
	}
	out := make([]map[string]any, len(models))
	for i, m := range models {
		entry := map[string]any{"model": m.Model, "alias": m.Alias}
		if m.ModelConfigID != nil {
			entry["modelConfigId"] = *m.ModelConfigID
		}
		out[i] = entry
	}
	return out
}
