// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const runGroupType = "runGroup"

type RunGroup struct {
	ID              string  `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-1c2d3e4f5a6b"`
	Type            string  `json:"type" example:"runGroup"`
	ExperimentID    string  `json:"experimentId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	RunName         *string `json:"runName" example:"Support answers, March"`
	Status          string  `json:"status" example:"completed"`
	ProgressCurrent int     `json:"progressCurrent" example:"6"`
	ProgressTotal   int     `json:"progressTotal" example:"20"`
	CreatedTime     Instant `json:"createdTime"`
	CompletedTime   Instant `json:"completedTime"`
	Runs            []Run   `json:"runs"`
	ConfigOutdated  bool    `json:"configOutdated" example:"false"`
}

type RunGroupSummary struct {
	ID              string   `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-1c2d3e4f5a6b"`
	Type            string   `json:"type" example:"runGroup"`
	ExperimentID    string   `json:"experimentId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	ExperimentName  string   `json:"experimentName" example:"Support answers"`
	TargetType      string   `json:"targetType" example:"agent"`
	TargetAgent     string   `json:"targetAgent" example:"support_agent"`
	RunName         *string  `json:"runName" example:"Support answers, March"`
	Status          string   `json:"status" example:"completed"`
	CreatedTime     Instant  `json:"createdTime"`
	CompletedTime   Instant  `json:"completedTime"`
	DurationSeconds *float64 `json:"durationSeconds" example:"42.5"`
	RunCount        int      `json:"runCount" example:"2"`
}

type RunGroupAgent struct {
	TargetAgent string `json:"targetAgent" example:"support_agent"`
	TargetType  string `json:"targetType" example:"agent"`
}

type RunGroupModelResult struct {
	ModelConfigID *string         `json:"modelConfigId" example:"0190f5e7-7a8b-7c9d-8e0f-1a2b3c4d5e6f"`
	RunID         string          `json:"runId" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	TokenSummary  RunTokenSummary `json:"tokenSummary"`
}

type RunTokenSummary struct {
	UsageResultCount      int      `json:"usageResultCount" example:"10"`
	MeanInputTokens       *float64 `json:"meanInputTokens" example:"1200.5"`
	MeanCachedInputTokens *float64 `json:"meanCachedInputTokens" example:"800"`
	MeanOutputTokens      *float64 `json:"meanOutputTokens" example:"250"`
	MeanReasoningTokens   *float64 `json:"meanReasoningTokens" example:"0"`
	MeanTotalTokens       *float64 `json:"meanTotalTokens" example:"1450.5"`
}

type listRunGroupsRequest struct {
	Page        Page   `json:"-"`
	Name        string `query:"name" json:"-" example:"nightly"`
	TargetAgent string `query:"targetAgent" json:"-" example:"OrderAgent"`
	TargetType  string `query:"targetType" json:"-" example:"workflow"`
}

var runGroupTargetTypes = []string{store.EvalTargetTypeAgent, store.EvalTargetTypeWorkflow}

type listRunGroupAgentsRequest struct {
	Page       Page   `json:"-"`
	TargetType string `query:"targetType" json:"-" example:"workflow"`
}

type listRunGroupResultsRequest struct {
	RunGroupID string `path:"runGroupId" json:"-"`
	Page       Page   `json:"-"`
}

const (
	runGroupsPath      = evaluationsPath + "/runGroups"
	runGroupPath       = runGroupsPath + "/{id}"
	runGroupAgentsPath = evaluationsPath + "/runGroupAgents"
)

func runGroupRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: runGroupsPath, Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listRunGroups", Summary: "Get a list of evaluation run groups",
				Text: "Returns one page of run groups across every experiment, newest first, optionally only those whose run name contains some text, whose experiment evaluates one agent or workflow name, or whose experiment targets one kind (agent or workflow). A targetType other than agent or workflow answers 422.", Tag: runsTag},
			Handler: Handle(h.listRunGroups, "Unable to list run groups")},
		{Method: http.MethodGet, Path: runGroupAgentsPath, Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listRunGroupAgents", Summary: "Get a list of the agents and workflows that evaluation run groups evaluated",
				Text: "Returns one page of the agent and workflow names the run groups' experiments target, alphabetically, each with its target type, for filtering the run group list.", Tag: runsTag},
			Handler: Handle(h.listRunGroupAgents, "Unable to list run group agents")},
		{Method: http.MethodGet, Path: runGroupPath, Scope: rbac.ScopeEvaluationRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getRunGroup", Summary: "Get an evaluation run group",
				Text: "Returns one run group with its runs and whether the experiment changed since it started.", Tag: runsTag},
			Handler: Handle(h.getRunGroup, "Unable to get run group")},
		{Method: http.MethodDelete, Path: runGroupPath, Scope: rbac.ScopeEvaluationDelete,
			Stability: StabilityDraft,
			Doc: Doc{ID: "deleteRunGroup", Summary: "Delete an evaluation run group",
				Text: "Deletes every run of the group with their results and the execution data stored for them. Answers 422 while a run of the group is pending or running; cancel it first.", Tag: runsTag},
			Handler: Handle(h.deleteRunGroup, "Unable to delete run group")},
		{Method: http.MethodGet, Path: runGroupsPath + "/{runGroupId}/results", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listRunGroupResults", Summary: "Get a list of an evaluation run group's results by model",
				Text: "Returns one page of the group's runs, ordered by model, each with the mean token usage of its trials.", Tag: runsTag},
			Handler: Handle(h.listRunGroupResults, "Unable to list run group results")},
	}
}

func (h *evaluationHandler) listRunGroups(ctx context.Context, req listRunGroupsRequest) (List[RunGroupSummary], error) {
	if err := OneOfQueryError("targetType", req.TargetType, runGroupTargetTypes); err != nil {
		return List[RunGroupSummary]{}, err
	}
	filter := store.RunGroupFilter{Name: req.Name, Agent: req.TargetAgent, TargetType: req.TargetType}
	resp, err := h.runs.ListRunGroupsPaginated(ctx, req.Page.Number, req.Page.Size, filter)
	if err != nil {
		return List[RunGroupSummary]{}, err
	}
	items := make([]RunGroupSummary, len(resp.Data))
	for i := range resp.Data {
		items[i] = toRunGroupSummary(&resp.Data[i])
	}
	return List[RunGroupSummary]{Items: items, Total: resp.Meta.Pagination.Count}, nil
}

func (h *evaluationHandler) listRunGroupAgents(ctx context.Context, req listRunGroupAgentsRequest) (List[RunGroupAgent], error) {
	if err := OneOfQueryError("targetType", req.TargetType, runGroupTargetTypes); err != nil {
		return List[RunGroupAgent]{}, err
	}
	targets, total, err := h.runs.ListDistinctRunTargetsPaginated(ctx, req.TargetType, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[RunGroupAgent]{}, err
	}
	items := make([]RunGroupAgent, len(targets))
	for i, target := range targets {
		items[i] = RunGroupAgent{TargetAgent: target.Name, TargetType: target.TargetType}
	}
	return List[RunGroupAgent]{Items: items, Total: total}, nil
}

func (h *evaluationHandler) getRunGroup(ctx context.Context, req IDRequest) (RunGroup, error) {
	detail, err := h.runs.GetRunGroupDetail(ctx, req.ID)
	if err != nil {
		return RunGroup{}, err
	}
	return toRunGroup(detail), nil
}

func (h *evaluationHandler) deleteRunGroup(ctx context.Context, req IDRequest) (struct{}, error) {
	return struct{}{}, h.runs.DeleteRunGroup(ctx, req.ID)
}

func (h *evaluationHandler) listRunGroupResults(ctx context.Context, req listRunGroupResultsRequest) (List[RunGroupModelResult], error) {
	results, total, err := h.runs.ListRunGroupModelResultsPaginated(ctx, req.RunGroupID, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[RunGroupModelResult]{}, err
	}
	items := make([]RunGroupModelResult, len(results))
	for i, r := range results {
		items[i] = RunGroupModelResult{
			ModelConfigID: modelConfigIDOf(r.Model),
			RunID:         r.RunID,
			TokenSummary:  toRunTokenSummary(r.TokenSummary),
		}
	}
	return List[RunGroupModelResult]{Items: items, Total: total}, nil
}

func modelConfigIDOf(model string) *string {
	if !IsCanonicalUUID(model) {
		return nil
	}
	return &model
}

func toRunTokenSummary(s eval.ModelTokenSummary) RunTokenSummary {
	return RunTokenSummary{
		UsageResultCount:      s.UsageResultCount,
		MeanInputTokens:       s.MeanInputTokens,
		MeanCachedInputTokens: s.MeanCachedInputTokens,
		MeanOutputTokens:      s.MeanOutputTokens,
		MeanReasoningTokens:   s.MeanReasoningTokens,
		MeanTotalTokens:       s.MeanTotalTokens,
	}
}

func toRunGroupSummary(s *eval.RunGroupSummaryResponse) RunGroupSummary {
	return RunGroupSummary{
		ID:              s.RunGroupID,
		Type:            runGroupType,
		ExperimentID:    s.ExperimentID,
		ExperimentName:  s.ExperimentName,
		TargetType:      s.TargetType,
		TargetAgent:     s.TargetAgent,
		RunName:         s.RunName,
		Status:          s.Status,
		CreatedTime:     Instant(s.StartedAt),
		CompletedTime:   InstantOf(s.CompletedAt),
		DurationSeconds: s.DurationSeconds,
		RunCount:        s.RunCount,
	}
}

func toRunGroup(detail *eval.RunGroupDetail) RunGroup {
	first := detail.Runs[0]
	return RunGroup{
		ID:              first.RunGroupID,
		Type:            runGroupType,
		ExperimentID:    first.ExperimentID,
		RunName:         first.RunName,
		Status:          detail.Status,
		ProgressCurrent: detail.ProgressCurrent,
		ProgressTotal:   detail.ProgressTotal,
		CreatedTime:     Instant(detail.StartedTime),
		CompletedTime:   InstantOf(detail.CompletedTime),
		Runs:            toRuns(detail.Runs),
		ConfigOutdated:  detail.ConfigOutdated,
	}
}
