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
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type WatchlistEntry struct {
	AgentName             string  `json:"agentName" example:"support_agent"`
	TargetType            string  `json:"targetType" example:"agent"`
	ResolvedModelProvider *string `json:"resolvedModelProvider" example:"general"`
	IsBuiltIn             bool    `json:"isBuiltIn" example:"false"`
	Position              int     `json:"position" example:"0"`
}

type ReplaceWatchlistRequest struct {
	Page       Page     `json:"-"`
	AgentNames []string `json:"agentNames" nullable:"true" example:"[\"support_agent\",\"billing_agent\"]"`
}

type WatchlistChartSeries struct {
	AgentName             string                      `json:"agentName" example:"support_agent"`
	TargetType            string                      `json:"targetType" example:"agent"`
	ResolvedModelProvider *string                     `json:"resolvedModelProvider" example:"general"`
	IsBuiltIn             bool                        `json:"isBuiltIn" example:"false"`
	Position              int                         `json:"position" example:"0"`
	Experiments           []WatchlistExperimentSeries `json:"experiments"`
}

type WatchlistExperimentSeries struct {
	ExperimentID         string                `json:"experimentId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	ExperimentName       string                `json:"experimentName" example:"Support answers, March"`
	PrimaryEvaluatorID   string                `json:"primaryEvaluatorId" example:"0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`
	PrimaryEvaluatorName *string               `json:"primaryEvaluatorName" example:"Answer relevance"`
	Points               []WatchlistChartPoint `json:"points"`
}

type WatchlistChartPoint struct {
	RunGroupID  string   `json:"runGroupId" example:"0190f5ef-2e3f-7a4b-8c5d-6e7f8a9b0c1d"`
	StartedTime Instant  `json:"startedTime"`
	Score       *float64 `json:"score" example:"0.87"`
}

type listWatchlistRequest struct {
	Page Page `json:"-"`
}

type listWatchlistChartDataRequest struct {
	Page Page `json:"-"`
}

const watchlistPath = evaluationsPath + "/watchlist"

func watchlistRoutes(h *evaluationHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: watchlistPath, Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listWatchlist", Summary: "Get the evaluation watchlist",
				Text: "Returns one page of the agents and workflows the caller watches, in the order they chose.", Tag: watchlistTag},
			Handler: Handle(h.listWatchlist, "Unable to list watchlist")},
		{Method: http.MethodPut, Path: watchlistPath, Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "replaceWatchlist", Summary: "Replace the evaluation watchlist",
				Text: "Replaces the caller's whole watchlist with the agents or workflows named, in order, and returns a page of the new list, the first unless pageNumber says otherwise. Reports every invalid name at once.", Tag: watchlistTag},
			Handler: Handle(h.replaceWatchlist, "Unable to replace watchlist")},
		{Method: http.MethodGet, Path: watchlistPath + "/chartData", Scope: rbac.ScopeEvaluationRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listWatchlistChartData", Summary: "Get the evaluation watchlist's chart data",
				Text: "Returns one page of watched agents and workflows, each with its top experiments and the scores of their last 10 runs, under the agent's model for an agent and under any model for a workflow.", Tag: watchlistTag},
			Handler: Handle(h.listWatchlistChartData, "Unable to get watchlist chart data")},
	}
}

func (h *evaluationHandler) listWatchlist(ctx context.Context, req listWatchlistRequest) (List[WatchlistEntry], error) {
	return h.watchlistPage(ctx, req.Page)
}

func (h *evaluationHandler) replaceWatchlist(ctx context.Context, req ReplaceWatchlistRequest) (List[WatchlistEntry], error) {
	if _, err := h.watchlist.Set(ctx, entplatform.UserIDFromContext(ctx), req.AgentNames); err != nil {
		return List[WatchlistEntry]{}, evaluationError(err)
	}
	return h.watchlistPage(ctx, req.Page)
}

func (h *evaluationHandler) watchlistPage(ctx context.Context, p Page) (List[WatchlistEntry], error) {
	rows, total, err := h.watchlist.ListPaginated(ctx, entplatform.UserIDFromContext(ctx), p.Number, p.Size)
	if err != nil {
		return List[WatchlistEntry]{}, err
	}
	items := make([]WatchlistEntry, len(rows))
	for i, row := range rows {
		items[i] = WatchlistEntry{
			AgentName:             row.AgentName,
			TargetType:            row.TargetType,
			ResolvedModelProvider: NullIfZero(row.ResolvedModelProvider),
			IsBuiltIn:             row.IsBuiltIn,
			Position:              row.Position,
		}
	}
	return List[WatchlistEntry]{Items: items, Total: total}, nil
}

func (h *evaluationHandler) listWatchlistChartData(ctx context.Context, req listWatchlistChartDataRequest) (List[WatchlistChartSeries], error) {
	rows, total, err := h.watchlist.ChartDataPaginated(ctx, entplatform.UserIDFromContext(ctx), req.Page.Number, req.Page.Size)
	if err != nil {
		return List[WatchlistChartSeries]{}, err
	}
	items := make([]WatchlistChartSeries, len(rows))
	for i := range rows {
		items[i] = toWatchlistChartSeries(&rows[i])
	}
	return List[WatchlistChartSeries]{Items: items, Total: total}, nil
}

func toWatchlistChartSeries(resp *eval.WatchlistAgentChartResponse) WatchlistChartSeries {
	experiments := make([]WatchlistExperimentSeries, len(resp.Experiments))
	for i, e := range resp.Experiments {
		points := make([]WatchlistChartPoint, len(e.Points))
		for j, p := range e.Points {
			points[j] = WatchlistChartPoint{RunGroupID: p.RunGroupID, StartedTime: Instant(p.StartedAt), Score: p.Score}
		}
		experiments[i] = WatchlistExperimentSeries{
			ExperimentID:         e.ExperimentID,
			ExperimentName:       e.ExperimentName,
			PrimaryEvaluatorID:   e.PrimaryEvaluatorID,
			PrimaryEvaluatorName: e.PrimaryEvaluatorName,
			Points:               points,
		}
	}
	return WatchlistChartSeries{
		AgentName:             resp.AgentName,
		TargetType:            resp.TargetType,
		ResolvedModelProvider: NullIfZero(resp.ResolvedModelProvider),
		IsBuiltIn:             resp.IsBuiltIn,
		Position:              resp.Position,
		Experiments:           experiments,
	}
}
