// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	listWatchlistErrFormat    = "list watchlist: %w"
	defaultAgentModelProvider = "general"
	topNExperiments           = 3
	lastNRunGroups            = 10
	maxWatchlistAgents        = 5
	maxAgentNameLength        = 255
)

type AgentModelProviderResolver interface {
	ModelProviderForAgent(ctx context.Context, agentName string) (provider string, found bool, err error)
}

type ModelIdentifierResolver interface {
	AcceptableModelIdentifiers(ctx context.Context, aliasOrID string) ([]string, error)
}

type WatchlistServiceParams struct {
	Watchlist   *store.EvalWatchlistRepo
	Workflows   WorkflowTargetResolver
	Runs        *store.EvalRunRepo
	Evaluators  *store.EvalEvaluatorRepo
	AgentModels AgentModelProviderResolver
	ModelConfig ModelIdentifierResolver
	Logger      *slog.Logger
}

type WatchlistService struct {
	watchlist   *store.EvalWatchlistRepo
	workflows   WorkflowTargetResolver
	runs        *store.EvalRunRepo
	evaluators  *store.EvalEvaluatorRepo
	agentModels AgentModelProviderResolver
	modelConfig ModelIdentifierResolver
	logger      *slog.Logger
}

func NewWatchlistService(p WatchlistServiceParams) *WatchlistService {
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "eval-watchlist")
	return &WatchlistService{
		watchlist:   p.Watchlist,
		workflows:   p.Workflows,
		runs:        p.Runs,
		evaluators:  p.Evaluators,
		agentModels: p.AgentModels,
		modelConfig: p.ModelConfig,
		logger:      logger,
	}
}

func (s *WatchlistService) List(ctx context.Context, userID string) ([]WatchlistEntryResponse, error) {
	rows, err := s.watchlist.ListForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf(listWatchlistErrFormat, err)
	}
	out := make([]WatchlistEntryResponse, 0, len(rows))
	for _, row := range rows {
		entry, err := s.toEntry(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *WatchlistService) ListPaginated(ctx context.Context, userID string, pageNumber, pageSize int) ([]WatchlistEntryResponse, int, error) {
	rows, total, err := s.watchlist.ListPaginatedForUser(ctx, userID, pageNumber, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf(listWatchlistErrFormat, err)
	}
	out := make([]WatchlistEntryResponse, 0, len(rows))
	for _, row := range rows {
		entry, err := s.toEntry(ctx, row)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, entry)
	}
	return out, total, nil
}

func (s *WatchlistService) Set(ctx context.Context, userID string, agentNames []string) ([]WatchlistEntryResponse, error) {
	var nameErrs []string
	trimmed := make([]string, 0, len(agentNames))
	for i, name := range agentNames {
		t := strings.TrimSpace(name)
		switch {
		case t == "":
			nameErrs = append(nameErrs, fmt.Sprintf("Agent name at position %d must not be blank.", i+1))
		case len(t) > maxAgentNameLength:
			nameErrs = append(nameErrs, fmt.Sprintf("Agent name at position %d exceeds %d characters.", i+1, maxAgentNameLength))
		default:
			trimmed = append(trimmed, t)
		}
	}
	deduped := dedupeStable(trimmed)
	if len(deduped) > maxWatchlistAgents {
		nameErrs = append(nameErrs, fmt.Sprintf("Submit at most %d agent names; got %d.", maxWatchlistAgents, len(deduped)))
	}
	if len(nameErrs) > 0 {
		return nil, &ValidationError{
			Field:   "agentNames",
			Message: "One or more watchlist agent names are invalid.",
			Details: map[string][]string{"agentNames": nameErrs},
		}
	}
	rows, err := s.watchlist.SetForUser(ctx, userID, deduped)
	if err != nil {
		return nil, fmt.Errorf("set watchlist: %w", err)
	}
	out := make([]WatchlistEntryResponse, 0, len(rows))
	for _, row := range rows {
		entry, err := s.toEntry(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *WatchlistService) ChartData(ctx context.Context, userID string) ([]WatchlistAgentChartResponse, error) {
	rows, err := s.watchlist.ListForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf(listWatchlistErrFormat, err)
	}
	out := make([]WatchlistAgentChartResponse, 0, len(rows))
	for _, row := range rows {
		chart, err := s.buildAgentChart(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, chart)
	}
	return out, nil
}

func (s *WatchlistService) ChartDataPaginated(ctx context.Context, userID string, pageNumber, pageSize int) ([]WatchlistAgentChartResponse, int, error) {
	rows, total, err := s.watchlist.ListPaginatedForUser(ctx, userID, pageNumber, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf(listWatchlistErrFormat, err)
	}
	out := make([]WatchlistAgentChartResponse, 0, len(rows))
	for _, row := range rows {
		chart, err := s.buildAgentChart(ctx, row)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, chart)
	}
	return out, total, nil
}

func (s *WatchlistService) toEntry(ctx context.Context, row *store.EvalAgentWatchlist) (WatchlistEntryResponse, error) {
	if s.workflows != nil {
		isWorkflow, err := s.namesWorkflow(ctx, row.AgentName)
		if err != nil {
			return WatchlistEntryResponse{}, err
		}
		if isWorkflow {
			return WatchlistEntryResponse{AgentName: row.AgentName, TargetType: store.EvalTargetTypeWorkflow, Position: row.Position}, nil
		}
	}
	provider, found, err := s.agentModels.ModelProviderForAgent(ctx, row.AgentName)
	if err != nil {
		return WatchlistEntryResponse{}, fmt.Errorf("resolve model provider for %q: %w", row.AgentName, err)
	}
	if provider == "" {
		provider = defaultAgentModelProvider
	}
	return WatchlistEntryResponse{
		AgentName:             row.AgentName,
		TargetType:            store.EvalTargetTypeAgent,
		ResolvedModelProvider: provider,
		IsBuiltIn:             !found,
		Position:              row.Position,
	}, nil
}

func (s *WatchlistService) namesWorkflow(ctx context.Context, name string) (bool, error) {
	targeted, err := s.runs.HasWorkflowExperiment(ctx, name)
	if err != nil {
		return false, fmt.Errorf("find workflow experiments for %q: %w", name, err)
	}
	if targeted {
		return true, nil
	}
	wf, err := s.workflows.ResolveWorkflow(ctx, name)
	if err != nil {
		return false, fmt.Errorf("resolve workflow %q: %w", name, err)
	}
	return wf != nil && wf.Name == name, nil
}

func (s *WatchlistService) buildAgentChart(ctx context.Context, row *store.EvalAgentWatchlist) (WatchlistAgentChartResponse, error) {
	entry, err := s.toEntry(ctx, row)
	if err != nil {
		return WatchlistAgentChartResponse{}, err
	}
	if entry.TargetType == store.EvalTargetTypeWorkflow {
		return s.buildWorkflowChart(ctx, entry)
	}
	acceptable, err := s.resolveAcceptableModels(ctx, entry.ResolvedModelProvider)
	if err != nil {
		return WatchlistAgentChartResponse{}, err
	}

	ranked, err := s.runs.TopExperimentsForAgent(ctx, row.AgentName, acceptable, topNExperiments)
	if err != nil {
		return WatchlistAgentChartResponse{}, fmt.Errorf("rank experiments for %q: %w", row.AgentName, err)
	}

	experiments, err := s.experimentSeries(ctx, ranked,
		func(rk *store.AgentExperimentRank) bool {
			return experimentTargetsModel(rk.Models, acceptable, rk.ExperimentID, s.logger)
		},
		func(experimentID string) ([]*store.EvalRun, error) {
			return s.runs.LastRunGroupsForExperiment(ctx, experimentID, acceptable, lastNRunGroups)
		})
	if err != nil {
		return WatchlistAgentChartResponse{}, err
	}

	if entry.ResolvedModelProvider != "" && len(experiments) == 0 {
		s.logger.Debug("watchlist agent resolved to a model provider but matched no experiments",
			"agentName", row.AgentName, "modelProvider", entry.ResolvedModelProvider)
	}

	return WatchlistAgentChartResponse{
		AgentName:             entry.AgentName,
		TargetType:            entry.TargetType,
		ResolvedModelProvider: entry.ResolvedModelProvider,
		IsBuiltIn:             entry.IsBuiltIn,
		Position:              entry.Position,
		Experiments:           experiments,
	}, nil
}

func (s *WatchlistService) buildWorkflowChart(ctx context.Context, entry WatchlistEntryResponse) (WatchlistAgentChartResponse, error) {
	ranked, err := s.runs.TopExperimentsForWorkflow(ctx, entry.AgentName, topNExperiments)
	if err != nil {
		return WatchlistAgentChartResponse{}, fmt.Errorf("rank experiments for workflow %q: %w", entry.AgentName, err)
	}
	experiments, err := s.experimentSeries(ctx, ranked,
		func(*store.AgentExperimentRank) bool { return true },
		func(experimentID string) ([]*store.EvalRun, error) {
			return s.runs.LastRunGroupsForWorkflowExperiment(ctx, experimentID, lastNRunGroups)
		})
	if err != nil {
		return WatchlistAgentChartResponse{}, err
	}
	if len(experiments) == 0 {
		s.logger.Debug("watchlist workflow matched no experiments", "workflowName", entry.AgentName)
	}
	return WatchlistAgentChartResponse{
		AgentName:   entry.AgentName,
		TargetType:  entry.TargetType,
		Position:    entry.Position,
		Experiments: experiments,
	}, nil
}

func (s *WatchlistService) experimentSeries(
	ctx context.Context,
	ranked []*store.AgentExperimentRank,
	include func(*store.AgentExperimentRank) bool,
	runsFor func(experimentID string) ([]*store.EvalRun, error),
) ([]WatchlistExperimentSeries, error) {
	evaluatorNames, err := s.evaluatorNamesFor(ctx, ranked)
	if err != nil {
		return nil, err
	}
	experiments := make([]WatchlistExperimentSeries, 0, len(ranked))
	for _, rk := range ranked {
		if rk.PrimaryEvaluatorID == nil || !include(rk) {
			continue
		}
		runs, err := runsFor(rk.ExperimentID)
		if err != nil {
			return nil, fmt.Errorf("load runs for experiment %q: %w", rk.ExperimentID, err)
		}
		evaluatorID := *rk.PrimaryEvaluatorID
		points := make([]WatchlistChartPoint, 0, len(runs))
		for _, run := range runs {
			points = append(points, WatchlistChartPoint{
				RunGroupID: run.RunGroupID,
				StartedAt:  Timestamp(run.StartedTime),
				Score:      extractScore(run.SummaryScores, evaluatorID, run.RunGroupID, s.logger),
			})
		}
		var evalName *string
		if name, ok := evaluatorNames[evaluatorID]; ok {
			evalName = &name
		}
		experiments = append(experiments, WatchlistExperimentSeries{
			ExperimentID:         rk.ExperimentID,
			ExperimentName:       rk.ExperimentName,
			PrimaryEvaluatorID:   evaluatorID,
			PrimaryEvaluatorName: evalName,
			Points:               points,
		})
	}
	return experiments, nil
}

func (s *WatchlistService) evaluatorNamesFor(ctx context.Context, ranked []*store.AgentExperimentRank) (map[string]string, error) {
	ids := make([]string, 0, len(ranked))
	for _, rk := range ranked {
		if rk.PrimaryEvaluatorID != nil {
			ids = append(ids, *rk.PrimaryEvaluatorID)
		}
	}
	evaluators, err := s.evaluators.GetByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load evaluators: %w", err)
	}
	names := make(map[string]string, len(evaluators))
	for _, e := range evaluators {
		names[e.ID] = e.Name
	}
	return names, nil
}

func (s *WatchlistService) resolveAcceptableModels(ctx context.Context, modelProvider string) ([]string, error) {
	seen := make(map[string]struct{}, 3)
	out := make([]string, 0, 3)
	add := func(v string) {
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	add(modelProvider)
	identifiers, err := s.modelConfig.AcceptableModelIdentifiers(ctx, modelProvider)
	if err != nil {
		return nil, fmt.Errorf("resolve acceptable models for %q: %w", modelProvider, err)
	}
	for _, id := range identifiers {
		add(id)
	}
	return out, nil
}

func experimentTargetsModel(modelsJSON string, acceptable []string, experimentID string, logger *slog.Logger) bool {
	if len(acceptable) == 0 || modelsJSON == "" {
		return false
	}
	var models []map[string]any
	if err := json.Unmarshal([]byte(modelsJSON), &models); err != nil {
		if logger != nil {
			logger.Error("eval: corrupt experiment models JSON; excluding experiment from watchlist chart",
				"error", err, "experimentID", experimentID)
		}
		return false
	}
	acc := make(map[string]struct{}, len(acceptable))
	for _, a := range acceptable {
		acc[a] = struct{}{}
	}
	for _, cfg := range models {
		if v, ok := cfg["modelConfigId"].(string); ok {
			if _, in := acc[v]; in {
				return true
			}
		}
		if v, ok := cfg["model"].(string); ok {
			if _, in := acc[v]; in {
				return true
			}
		}
	}
	return false
}

func extractScore(summaryScoresJSON *string, evaluatorID, runGroupID string, logger *slog.Logger) *float64 {
	if summaryScoresJSON == nil || *summaryScoresJSON == "" {
		return nil
	}
	var scores map[string]any
	if err := json.Unmarshal([]byte(*summaryScoresJSON), &scores); err != nil {
		if logger != nil {
			logger.Error("eval: corrupt summary_scores JSON; charting run as a gap",
				"error", err, "evaluatorID", evaluatorID, "runGroupID", runGroupID)
		}
		return nil
	}
	raw, ok := scores[evaluatorID]
	if !ok {
		return nil
	}
	if f, ok := raw.(float64); ok {
		return &f
	}
	return nil
}

func dedupeStable(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
