// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const (
	errListRunResults  = "list run results"
	errListRunsInGroup = "list runs in group"
)

var ErrRunNotDispatched = errors.New("eval run not dispatched")

var errNoTriggerPublisher = errors.New("no trigger publisher configured")

func wrapEvalErr(prefix string, err error) error {
	return fmt.Errorf("%s: %w", prefix, err)
}

type RunResponse struct {
	ID                string                       `json:"id"`
	ExperimentID      string                       `json:"experimentId"`
	RunGroupID        string                       `json:"runGroupId"`
	Status            string                       `json:"status"`
	ProgressCurrent   int                          `json:"progressCurrent"`
	ProgressTotal     int                          `json:"progressTotal"`
	ErrorMessage      *string                      `json:"errorMessage"`
	SummaryScores     map[string]float64           `json:"summaryScores"`
	EvaluatorStats    map[string]EvaluatorRunStats `json:"evaluatorStats,omitempty"`
	Model             string                       `json:"model"`
	ConfigSnapshot    map[string]any               `json:"configSnapshot"`
	EvaluatorSnapshot []map[string]any             `json:"evaluatorSnapshot"`
	RunName           *string                      `json:"runName"`
	Source            string                       `json:"source"`
	RunAs             *string                      `json:"runAs"`
	StartedBy         string                       `json:"startedBy"`
	StartedAt         Timestamp                    `json:"startedAt"`
	CompletedAt       *Timestamp                   `json:"completedAt"`
}

type RunService struct {
	runs        *store.EvalRunRepo
	results     *store.EvalRunResultRepo
	experiments *store.EvalExperimentRepo
	examples    *store.EvalExampleRepo
	evaluators  *store.EvalEvaluatorRepo

	trigger     *TriggerPublisher
	storage     *StorageService
	fileDeletes *FileDeletes

	artifacts *artifact.ScopedService

	agentRepo     agentSnapshotSource
	connectorRepo connectorSnapshotSource
	agentRegistry agentRegistrySource
	modelResolver modelSnapshotResolver
	decisionModel modelSnapshotLookup

	featureRegistry *features.Registry
	workflowTargets *WorkflowTargetValidator
	spendLedger     traceCostSource

	logger *slog.Logger
}

type RunServiceParams struct {
	Runs        *store.EvalRunRepo
	Results     *store.EvalRunResultRepo
	Experiments *store.EvalExperimentRepo
	Examples    *store.EvalExampleRepo
	Evaluators  *store.EvalEvaluatorRepo
	Trigger     *TriggerPublisher
	Storage     *StorageService
	Artifacts   *artifact.ScopedService
	FileDeletes *FileDeletes

	AgentRepo           agentSnapshotSource
	ConnectorRepo       connectorSnapshotSource
	AgentRegistry       agentRegistrySource
	ModelResolver       modelSnapshotResolver
	DecisionModelLookup modelSnapshotLookup
	FeatureRegistry     *features.Registry
	SpendLedger         traceCostSource
	WorkflowTargets     *WorkflowTargetValidator

	Logger *slog.Logger
}

func NewRunService(p RunServiceParams) *RunService {
	logger := p.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &RunService{
		runs:          p.Runs,
		results:       p.Results,
		experiments:   p.Experiments,
		examples:      p.Examples,
		evaluators:    p.Evaluators,
		trigger:       p.Trigger,
		storage:       p.Storage,
		fileDeletes:   p.FileDeletes,
		artifacts:     p.Artifacts,
		agentRepo:     p.AgentRepo,
		connectorRepo: p.ConnectorRepo,
		agentRegistry: p.AgentRegistry,
		modelResolver: p.ModelResolver,
		decisionModel: p.DecisionModelLookup,

		featureRegistry: p.FeatureRegistry,
		workflowTargets: p.WorkflowTargets,
		spendLedger:     p.SpendLedger,

		logger: logger,
	}
}

func (s *RunService) ListRuns(ctx context.Context, experimentID string) ([]RunResponse, error) {
	if _, err := s.experiments.GetByID(ctx, experimentID); err != nil {
		return nil, mapEvalNotFound(err, "EvalExperiment", experimentID)
	}
	rows, err := s.runs.ListByExperiment(ctx, experimentID)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	out := make([]RunResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, runResponseFromRow(r, s.logger))
	}
	return out, nil
}

func (s *RunService) GetRun(ctx context.Context, runID string) (*RunResponse, error) {
	row, err := s.runs.GetByID(ctx, runID)
	if err != nil {
		return nil, mapEvalNotFound(err, "EvalRun", runID)
	}
	resp := runResponseFromRow(row, s.logger)
	return &resp, nil
}

func (s *RunService) GetRunResults(ctx context.Context, runID string) ([]*store.EvalRunResultWithScores, error) {
	if _, err := s.runs.GetByID(ctx, runID); err != nil {
		return nil, mapEvalNotFound(err, "EvalRun", runID)
	}
	results, err := s.results.ListByRun(ctx, runID)
	if err != nil {
		return nil, wrapEvalErr(errListRunResults, err)
	}
	return results, nil
}

func (s *RunService) GetExecutionData(ctx context.Context, resultID string) (map[string]any, error) {
	result, err := s.results.GetByID(ctx, resultID)
	if err != nil {
		return nil, mapEvalNotFound(err, "ExecutionData", resultID)
	}
	if result.ExecutionDataKey == nil || *result.ExecutionDataKey == "" || s.storage == nil || !s.storage.Available() {
		return nil, &NotFoundError{Resource: "ExecutionData", ID: resultID}
	}
	return s.storage.GetExecutionData(*result.ExecutionDataKey)
}

func (s *RunService) GetTaskEvents(ctx context.Context, resultID string) ([]map[string]any, error) {
	result, err := s.results.GetByID(ctx, resultID)
	if err != nil {
		return nil, mapEvalNotFound(err, "TaskEvents", resultID)
	}
	if result.TaskEventsKey == nil || *result.TaskEventsKey == "" || s.storage == nil || !s.storage.Available() {
		return nil, &NotFoundError{Resource: "TaskEvents", ID: resultID}
	}
	return s.storage.GetTaskEvents(*result.TaskEventsKey)
}

func (s *RunService) TriggerRuns(ctx context.Context, experimentID, userID, runName, source string) ([]*store.EvalRun, error) {
	created, err := s.triggerRuns(ctx, experimentID, userID, runName, source, s.createRuns)
	if err != nil {
		return nil, err
	}
	s.publishTriggers(ctx, experimentID, created)
	return created, nil
}

func (s *RunService) TriggerRunsIfIdle(ctx context.Context, experimentID, userID, runName, source string) ([]*store.EvalRun, error) {
	created, err := s.triggerRuns(ctx, experimentID, userID, runName, source, s.runs.CreateIfIdle)
	if err != nil {
		return nil, err
	}
	sent, err := s.dispatchOrFail(ctx, created)
	if err != nil && sent == 0 {
		return nil, err
	}
	if err != nil {
		s.logger.Warn("eval trigger partly dispatched", "experimentID", experimentID, "sent", sent, "runs", len(created), "error", err)
	}
	return s.refreshRuns(ctx, created), nil
}

func (s *RunService) dispatchOrFail(ctx context.Context, runs []*store.EvalRun) (int, error) {
	if s.trigger == nil {
		return 0, s.failUndispatched(ctx, runs, errNoTriggerPublisher)
	}
	for i, run := range runs {
		if err := s.trigger.PublishRunTrigger(ctx, run.ID); err != nil {
			return i, s.failUndispatched(ctx, runs[i:], err)
		}
	}
	return len(runs), nil
}

func (s *RunService) failUndispatched(ctx context.Context, runs []*store.EvalRun, cause error) error {
	ctx = context.WithoutCancel(ctx)
	errs := []error{ErrRunNotDispatched, cause}
	msg := msgRunNotDispatched
	for _, run := range runs {
		if _, err := s.runs.UpdateSummaryScores(ctx, run.ID, "{}", StatusFailed, &msg); err != nil {
			errs = append(errs, fmt.Errorf("fail run %s: %w", run.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (s *RunService) refreshRuns(ctx context.Context, runs []*store.EvalRun) []*store.EvalRun {
	out := make([]*store.EvalRun, len(runs))
	for i, run := range runs {
		out[i] = run
		if fresh, err := s.runs.GetByID(ctx, run.ID); err == nil {
			out[i] = fresh
		}
	}
	return out
}

func (s *RunService) createRuns(ctx context.Context, _ string, runs []*store.EvalRun) error {
	for _, run := range runs {
		if err := s.runs.Create(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (s *RunService) triggerRuns(ctx context.Context, experimentID, userID, runName, source string,
	insert func(ctx context.Context, experimentID string, runs []*store.EvalRun) error,
) ([]*store.EvalRun, error) {
	exp, err := s.experiments.GetByID(ctx, experimentID)
	if err != nil {
		return nil, mapEvalNotFound(err, "EvalExperiment", experimentID)
	}
	if exp.DatasetID == nil {
		return nil, &ValidationError{Field: "dataset_id", Message: "Experiment has no linked dataset. The dataset may have been deleted."}
	}
	if exp.IsWorkflowTarget() {
		if refusal := workflowTriggerRefusal(s.featureRegistry); refusal != nil {
			return nil, refusal
		}
	}
	enriched, err := s.experiments.GetEnriched(ctx, experimentID)
	if err != nil {
		return nil, fmt.Errorf("get experiment enriched: %w", err)
	}
	datasetName := ""
	if enriched.DatasetName != nil {
		datasetName = *enriched.DatasetName
	}
	examples, err := s.examples.ListByDataset(ctx, *exp.DatasetID)
	if err != nil {
		return nil, fmt.Errorf("list examples: %w", err)
	}
	if len(examples) == 0 {
		return nil, &ValidationError{Field: "dataset_id", Message: "Dataset has no examples. Add examples before triggering a run."}
	}
	if !exp.IsWorkflowTarget() {
		if refusal := refusePromptlessExamples(examples); refusal != nil {
			return nil, refusal
		}
	}
	evaluatorIDs, err := s.experiments.GetEvaluatorIDs(ctx, experimentID)
	if err != nil {
		return nil, fmt.Errorf("get evaluator ids: %w", err)
	}
	if len(evaluatorIDs) == 0 {
		return nil, &ValidationError{
			Message: "Experiment has no evaluators. Attach at least one evaluator before triggering a run.",
			Details: map[string][]string{"evaluatorIds": {"Experiment must have at least one evaluator"}},
		}
	}
	evaluators, err := s.evaluators.GetByIDs(ctx, evaluatorIDs)
	if err != nil {
		return nil, fmt.Errorf("get evaluators: %w", err)
	}

	progressTotal := len(examples) * exp.RunsPerExample

	models, err := decodeExperimentModels(exp.Models)
	if err != nil {
		return nil, fmt.Errorf("decode experiment models: %w", err)
	}

	enrichedModels := enrichExperimentModelSnapshots(models, s.modelResolver)
	configSnapshot, err := s.buildTargetConfigSnapshot(ctx, exp, datasetName, examples, enrichedModels)
	if err != nil {
		return nil, err
	}
	escalationTargets, err := s.escalationTargets(ctx, evaluators)
	if err != nil {
		return nil, err
	}
	evalSnapshot, err := buildEvaluatorSnapshot(ctx, evaluators, evaluatorSnapshotDeps{
		resolver:       s.modelResolver,
		decisionLookup: s.decisionModel,
		registry:       s.featureRegistry,
		targets:        escalationTargets,
		logger:         s.logger,
	})
	if err != nil {
		return nil, err
	}

	configBytes, err := json.Marshal(configSnapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal config snapshot: %w", err)
	}
	evalBytes, err := json.Marshal(evalSnapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal evaluator snapshot: %w", err)
	}

	runConfigs := models
	if len(runConfigs) == 0 {
		runConfigs = []map[string]any{{}}
	}

	stripped := strings.TrimSpace(runName)
	effective := stripped
	if effective == "" {
		effective = exp.Name
	}
	if source == "" {
		source = "manual"
	}

	runGroupID := idgen.New()
	created := make([]*store.EvalRun, 0, len(runConfigs))
	for _, mc := range runConfigs {
		modelName := ""
		if v, ok := mc["modelConfigId"].(string); ok && v != "" {
			modelName = v
		} else if v, ok := mc["model"].(string); ok && v != "" {
			modelName = v
		}
		configJSON := string(configBytes)
		evalJSON := string(evalBytes)
		runNamePtr := &effective
		created = append(created, &store.EvalRun{
			ExperimentID:      experimentID,
			RunGroupID:        runGroupID,
			Status:            StatusPending,
			ProgressTotal:     progressTotal,
			Model:             modelName,
			ConfigSnapshot:    &configJSON,
			EvaluatorSnapshot: &evalJSON,
			RunName:           runNamePtr,
			Source:            source,
			RunAs:             exp.RunAs,
			StartedBy:         userID,
		})
	}
	if err := insert(ctx, experimentID, created); err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return nil, &NotFoundError{Resource: "EvalExperiment", ID: experimentID}
		}
		return nil, fmt.Errorf("create run: %w", err)
	}
	return created, nil
}

func (s *RunService) publishTriggers(ctx context.Context, experimentID string, created []*store.EvalRun) {
	if s.trigger != nil {
		for _, run := range created {
			if err := s.trigger.PublishRunTrigger(ctx, run.ID); err != nil {
				s.logger.Warn("eval: failed to publish run trigger", "runID", run.ID, "error", err)
			}
		}
	} else {
		for _, run := range created {
			s.logger.Warn("eval: run created but trigger publisher not configured — run will not execute (no broker configured?)",
				"runID", run.ID,
				"experimentID", experimentID,
			)
		}
	}
}

func (s *RunService) CancelRun(ctx context.Context, runID string) (*RunResponse, error) {
	updated, err := s.CancelRunRecord(ctx, runID)
	if err != nil {
		return nil, err
	}
	resp := runResponseFromRow(updated, s.logger)
	return &resp, nil
}

func (s *RunService) CancelRunRecord(ctx context.Context, runID string) (*store.EvalRun, error) {
	updated, cancelled, err := s.runs.CancelActive(ctx, runID)
	if err != nil {
		return nil, mapEvalNotFound(err, "EvalRun", runID)
	}
	if !cancelled {
		return nil, &BusinessRuleViolationError{
			Code:    "RUN_IS_TERMINAL",
			Message: fmt.Sprintf("Run %q is already in terminal status %q and cannot be cancelled.", runID, updated.Status),
		}
	}
	if s.trigger != nil {
		if perr := s.trigger.PublishCancel(ctx, runID); perr != nil {
			s.logger.Warn("eval: cancel-event publish failed", "runID", runID, "error", perr)
		}
	}
	return updated, nil
}

func (s *RunService) DeleteRun(ctx context.Context, runID string) error {
	if err := s.runs.Delete(ctx, runID, s.fileDeletes.event()); err != nil {
		return runDeleteError(err, "EvalRun", runID)
	}
	return nil
}

func runDeleteError(err error, resource, id string) error {
	if errors.Is(err, store.ErrEvalRunActive) {
		return &BusinessRuleViolationError{
			Code:    "RUN_IS_ACTIVE",
			Message: "Cannot delete a run while it is pending or running. Cancel it or wait for it to finish first.",
		}
	}
	return mapEvalNotFound(err, resource, id)
}

func (s *RunService) ListRunsInGroup(ctx context.Context, groupID string) ([]RunResponse, error) {
	rows, err := s.runs.ListByRunGroup(ctx, groupID)
	if err != nil {
		return nil, wrapEvalErr(errListRunsInGroup, err)
	}
	if len(rows) == 0 {
		return nil, &NotFoundError{Resource: "EvalRunGroup", ID: groupID}
	}
	out := make([]RunResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, runResponseFromRow(r, s.logger))
	}
	return out, nil
}

func (s *RunService) ListResultsInGroup(ctx context.Context, groupID string) ([]*store.EvalRunResultWithScores, error) {
	rows, err := s.runs.ListByRunGroup(ctx, groupID)
	if err != nil {
		return nil, wrapEvalErr(errListRunsInGroup, err)
	}
	if len(rows) == 0 {
		return nil, &NotFoundError{Resource: "EvalRunGroup", ID: groupID}
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	results, err := s.results.ListByRunIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list results in group: %w", err)
	}
	return results, nil
}

func (s *RunService) DeleteRunGroup(ctx context.Context, groupID string) error {
	rows, err := s.runs.ListByRunGroup(ctx, groupID)
	if err != nil {
		return wrapEvalErr(errListRunsInGroup, err)
	}
	if len(rows) == 0 {
		return &NotFoundError{Resource: "EvalRunGroup", ID: groupID}
	}
	if _, err := s.runs.DeleteByRunGroup(ctx, groupID, s.fileDeletes.event()); err != nil {
		if errors.Is(err, store.ErrEvalRunActive) {
			return runDeleteError(err, "EvalRunGroup", groupID)
		}
		return fmt.Errorf("delete run group: %w", err)
	}
	return nil
}

func (s *RunService) ListRunGroupsPaginated(ctx context.Context, page, pageSize int, filter store.RunGroupFilter) (PaginatedResponse[RunGroupSummaryResponse], error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	rows, total, err := s.runs.ListRunGroupsPaginated(ctx, page, pageSize, filter)
	if err != nil {
		return PaginatedResponse[RunGroupSummaryResponse]{}, fmt.Errorf("list run groups: %w", err)
	}
	out := make([]RunGroupSummaryResponse, 0, len(rows))
	for _, r := range rows {
		summary := RunGroupSummaryResponse{
			RunGroupID:     r.RunGroupID,
			ExperimentID:   r.ExperimentID,
			ExperimentName: r.ExperimentName,
			TargetAgent:    r.TargetAgent,
			TargetType:     cmp.Or(r.TargetType, store.EvalTargetTypeAgent),
			RunName:        r.RunName,
			StartedAt:      Timestamp(r.StartedTime),
			RunCount:       r.RunCount,
			Status:         AggregateRunStatuses(r.Statuses),
		}
		if r.CompletedTime != nil {
			t := Timestamp(*r.CompletedTime)
			summary.CompletedAt = &t
			if r.StartedTime > 0 && *r.CompletedTime >= r.StartedTime {
				duration := float64(*r.CompletedTime-r.StartedTime) / 1000.0
				summary.DurationSeconds = &duration
			}
		}
		out = append(out, summary)
	}
	return PaginatedResponse[RunGroupSummaryResponse]{
		Data: out,
		Meta: newPaginationMeta(page, pageSize, total),
	}, nil
}

func (s *RunService) ListDistinctTargetAgents(ctx context.Context) ([]string, error) {
	out, err := s.runs.ListDistinctTargetAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list distinct target agents: %w", err)
	}
	return out, nil
}

func (s *RunService) GetRunResultByID(ctx context.Context, resultID string) (*RunResultResponse, error) {
	row, err := s.results.GetByID(ctx, resultID)
	if err != nil {
		return nil, mapEvalNotFound(err, "EvalRunResult", resultID)
	}
	resp := []RunResultResponse{runResultResponseFromRow(row, s.logger)}
	s.attachTrialCosts(ctx, resp)
	return &resp[0], nil
}

func (s *RunService) GetRunResultArtifact(ctx context.Context, resultID, filename string, version *int) (content []byte, mimeType string, err error) {
	if s.artifacts == nil {
		return nil, "", &NotFoundError{Resource: "EvalRunResultArtifact", ID: filename}
	}
	row, err := s.results.GetByID(ctx, resultID)
	if err != nil {
		return nil, "", mapEvalNotFound(err, "EvalRunResult", resultID)
	}

	metas := artifactsFromMetadata(row.ArtifactsMetadata, resultID, s.logger)
	matchIdx := -1
	for i, a := range metas {
		if a.Filename != filename {
			continue
		}
		if version != nil {
			if a.Version == *version {
				matchIdx = i
				break
			}
			continue
		}
		if matchIdx == -1 || a.Version > metas[matchIdx].Version {
			matchIdx = i
		}
	}
	if matchIdx == -1 {
		return nil, "", &NotFoundError{Resource: "EvalRunResultArtifact", ID: filename}
	}
	matched := metas[matchIdx]
	mimeType = matched.MimeType
	loadVersion := matched.Version

	ref := s.artifacts.Resolve("", EvalRunnerUserID, snapshotSessionFor(row.EvalRunResult, filename, loadVersion), filename)
	bytes, loadedMime, loadErr := s.artifacts.Inner.Load(ctx, ref, &loadVersion)
	if loadErr != nil {
		return nil, "", fmt.Errorf("load eval artifact %q for result %s: %w", filename, resultID, loadErr)
	}
	if bytes == nil {
		return nil, "", &NotFoundError{Resource: "EvalRunResultArtifact", ID: filename}
	}
	if loadedMime != "" {
		mimeType = loadedMime
	}
	return bytes, mimeType, nil
}

func snapshotSessionFor(row *store.EvalRunResult, filename string, version int) string {
	var metas []snapshotArtifactMeta
	if row.ArtifactsMetadata != nil && json.Unmarshal([]byte(*row.ArtifactsMetadata), &metas) == nil {
		for _, m := range metas {
			if m.Filename == filename && m.Version == version && m.SessionID != "" {
				return m.SessionID
			}
		}
	}
	return evalArtifactSnapshotSessionID(row.RunID, row.ExampleID, row.TrialIndex)
}

func (s *RunService) GetRunResultsForRun(ctx context.Context, runID string) ([]RunResultResponse, error) {
	rows, err := s.GetRunResults(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]RunResultResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, runResultResponseFromRow(r, s.logger))
	}
	s.attachTrialCosts(ctx, out)
	return out, nil
}

type RunGroupFold struct {
	Status          string
	ProgressCurrent int
	ProgressTotal   int
	StartedTime     int64
	CompletedTime   *int64
}

func FoldRunGroup(rows []*store.EvalRun) RunGroupFold {
	fold := RunGroupFold{StartedTime: rows[0].StartedTime}
	statuses := make([]string, len(rows))
	allTerminal := true
	var completed *int64
	for i, r := range rows {
		if r.StartedTime > 0 && r.StartedTime < fold.StartedTime {
			fold.StartedTime = r.StartedTime
		}
		fold.ProgressCurrent += r.ProgressCurrent
		fold.ProgressTotal += r.ProgressTotal
		statuses[i] = r.Status
		if !IsTerminalStatus(r.Status) {
			allTerminal = false
		}
		if r.CompletedTime != nil && (completed == nil || *r.CompletedTime > *completed) {
			completed = r.CompletedTime
		}
	}
	fold.Status = AggregateRunStatuses(statuses)
	if allTerminal {
		fold.CompletedTime = completed
	}
	return fold
}

func (s *RunService) GetRunGroup(ctx context.Context, groupID string, experiments *ExperimentService) (*RunGroupResponse, error) {
	rows, err := s.runs.ListByRunGroup(ctx, groupID)
	if err != nil {
		return nil, wrapEvalErr(errListRunsInGroup, err)
	}
	if len(rows) == 0 {
		return nil, &NotFoundError{Resource: "EvalRunGroup", ID: groupID}
	}
	first := rows[0]
	fold := FoldRunGroup(rows)

	configOutdated := false
	if experiments != nil {
		exp, err := experiments.Get(ctx, first.ExperimentID)
		if err == nil && int64(exp.UpdatedTime) > fold.StartedTime {
			configOutdated = true
		}
	}

	runResps := make([]RunResponse, 0, len(rows))
	for _, r := range rows {
		runResps = append(runResps, runResponseFromRow(r, s.logger))
	}

	resp := &RunGroupResponse{
		ID:              groupID,
		ExperimentID:    first.ExperimentID,
		RunName:         first.RunName,
		Status:          fold.Status,
		ProgressCurrent: fold.ProgressCurrent,
		ProgressTotal:   fold.ProgressTotal,
		StartedAt:       Timestamp(fold.StartedTime),
		Runs:            runResps,
		ConfigOutdated:  configOutdated,
	}
	if fold.CompletedTime != nil {
		t := Timestamp(*fold.CompletedTime)
		resp.CompletedAt = &t
	}
	return resp, nil
}

func (s *RunService) GetRunGroupResultsByModel(ctx context.Context, groupID string) (*RunGroupResultsResponse, error) {
	runs, err := s.runs.ListByRunGroup(ctx, groupID)
	if err != nil {
		return nil, wrapEvalErr(errListRunsInGroup, err)
	}
	if len(runs) == 0 {
		return nil, &NotFoundError{Resource: "EvalRunGroup", ID: groupID}
	}
	runIDToModel := make(map[string]string, len(runs))
	resultsByModel := make(map[string][]RunResultResponse, len(runs))
	for _, r := range runs {
		runIDToModel[r.ID] = r.Model
		resultsByModel[r.Model] = []RunResultResponse{}
	}
	results, err := s.ListResultsInGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	for _, res := range results {
		model, ok := runIDToModel[res.RunID]
		if !ok {
			continue
		}
		resultsByModel[model] = append(resultsByModel[model], runResultResponseFromRow(res, s.logger))
	}
	groups := make([][]RunResultResponse, 0, len(resultsByModel))
	for _, rs := range resultsByModel {
		groups = append(groups, rs)
	}
	s.attachTrialCosts(ctx, groups...)
	tokenSummaryByModel := make(map[string]ModelTokenSummary, len(resultsByModel))
	for model, rs := range resultsByModel {
		tokenSummaryByModel[model] = summarizeModelTokens(rs)
	}
	return &RunGroupResultsResponse{
		GroupID:             groupID,
		ResultsByModel:      resultsByModel,
		TokenSummaryByModel: tokenSummaryByModel,
	}, nil
}

func summarizeModelTokens(results []RunResultResponse) ModelTokenSummary {
	type acc struct {
		sum   int64
		count int
	}
	var usageCount int
	var in, cached, out, reason, total acc
	add := func(a *acc, p *int64) {
		if p != nil {
			a.sum += *p
			a.count++
		}
	}
	for _, r := range results {
		if r.TokenUsage == nil {
			continue
		}
		usageCount++
		add(&in, r.TokenUsage.InputTokens)
		add(&cached, r.TokenUsage.CachedInputTokens)
		add(&out, r.TokenUsage.OutputTokens)
		add(&reason, r.TokenUsage.ReasoningTokens)
		add(&total, r.TokenUsage.TotalTokens)
	}
	mean := func(a acc) *float64 {
		if a.count == 0 {
			return nil
		}
		v := float64(a.sum) / float64(a.count)
		return &v
	}
	return ModelTokenSummary{
		UsageResultCount:      usageCount,
		MeanInputTokens:       mean(in),
		MeanCachedInputTokens: mean(cached),
		MeanOutputTokens:      mean(out),
		MeanReasoningTokens:   mean(reason),
		MeanTotalTokens:       mean(total),
	}
}

func (s *RunService) ListRunsPaginated(ctx context.Context, experimentID string, pageNumber, pageSize int) ([]*store.EvalRun, int, error) {
	if _, err := s.experiments.GetByID(ctx, experimentID); err != nil {
		return nil, 0, mapEvalNotFound(err, "EvalExperiment", experimentID)
	}
	rows, total, err := s.runs.ListByExperimentPaginated(ctx, experimentID, pageNumber, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	return rows, total, nil
}

func (s *RunService) GetRunRecord(ctx context.Context, runID string) (*store.EvalRun, error) {
	row, err := s.runs.GetByID(ctx, runID)
	if err != nil {
		return nil, mapEvalNotFound(err, "EvalRun", runID)
	}
	return row, nil
}

func (s *RunService) ListRunGroupRuns(ctx context.Context, groupID string) ([]*store.EvalRun, error) {
	rows, err := s.runs.ListByRunGroup(ctx, groupID)
	if err != nil {
		return nil, wrapEvalErr(errListRunsInGroup, err)
	}
	if len(rows) == 0 {
		return nil, &NotFoundError{Resource: "EvalRunGroup", ID: groupID}
	}
	return rows, nil
}

func (s *RunService) GetExperimentRunGroupStates(ctx context.Context, experimentID, groupID string) ([]*store.EvalRun, error) {
	if _, err := s.experiments.GetByID(ctx, experimentID); err != nil {
		return nil, mapEvalNotFound(err, "EvalExperiment", experimentID)
	}
	rows, err := s.runs.ListRunStatesByRunGroups(ctx, experimentID, []string{groupID})
	if err != nil {
		return nil, wrapEvalErr(errListRunsInGroup, err)
	}
	if len(rows) == 0 {
		return nil, &NotFoundError{Resource: "EvalRunGroup", ID: groupID}
	}
	return rows, nil
}

func (s *RunService) ListExperimentRunGroupsPaginated(ctx context.Context, experimentID string, pageNumber, pageSize int) ([][]*store.EvalRun, int, error) {
	if _, err := s.experiments.GetByID(ctx, experimentID); err != nil {
		return nil, 0, mapEvalNotFound(err, "EvalExperiment", experimentID)
	}
	ids, total, err := s.runs.ListRunGroupIDsByExperiment(ctx, experimentID, pageNumber, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list run groups: %w", err)
	}
	rows, err := s.runs.ListRunStatesByRunGroups(ctx, experimentID, ids)
	if err != nil {
		return nil, 0, wrapEvalErr(errListRunsInGroup, err)
	}
	byGroup := make(map[string][]*store.EvalRun, len(ids))
	for _, r := range rows {
		byGroup[r.RunGroupID] = append(byGroup[r.RunGroupID], r)
	}
	groups := make([][]*store.EvalRun, 0, len(ids))
	for _, id := range ids {
		if runs := byGroup[id]; len(runs) > 0 {
			groups = append(groups, runs)
		}
	}
	return groups, total, nil
}

type RunGroupDetail struct {
	RunGroupFold
	Runs           []*store.EvalRun
	ConfigOutdated bool
}

func (s *RunService) GetRunGroupDetail(ctx context.Context, groupID string) (*RunGroupDetail, error) {
	rows, err := s.ListRunGroupRuns(ctx, groupID)
	if err != nil {
		return nil, err
	}
	detail := &RunGroupDetail{RunGroupFold: FoldRunGroup(rows), Runs: rows}
	exp, err := s.experiments.GetByID(ctx, rows[0].ExperimentID)
	detail.ConfigOutdated = err == nil && exp.UpdatedTime > detail.StartedTime
	return detail, nil
}

type RunGroupModelResult struct {
	Model        string
	RunID        string
	TokenSummary ModelTokenSummary
}

func (s *RunService) ListRunGroupModelResultsPaginated(ctx context.Context, groupID string, pageNumber, pageSize int) ([]RunGroupModelResult, int, error) {
	runs, total, err := s.runs.ListByRunGroupPaginated(ctx, groupID, pageNumber, pageSize)
	if err != nil {
		return nil, 0, wrapEvalErr(errListRunsInGroup, err)
	}
	if total == 0 {
		return nil, 0, &NotFoundError{Resource: "EvalRunGroup", ID: groupID}
	}
	ids := make([]string, len(runs))
	for i, r := range runs {
		ids[i] = r.ID
	}
	results, err := s.results.ListByRunIDs(ctx, ids)
	if err != nil {
		return nil, 0, fmt.Errorf("list results in group: %w", err)
	}
	byRun := make(map[string][]RunResultResponse, len(runs))
	for _, res := range results {
		byRun[res.RunID] = append(byRun[res.RunID], runResultResponseFromRow(res, s.logger))
	}
	out := make([]RunGroupModelResult, len(runs))
	for i, r := range runs {
		out[i] = RunGroupModelResult{Model: r.Model, RunID: r.ID, TokenSummary: summarizeModelTokens(byRun[r.ID])}
	}
	return out, total, nil
}

func (s *RunService) ListDistinctRunTargetsPaginated(ctx context.Context, targetType string, pageNumber, pageSize int) ([]store.RunTarget, int, error) {
	targets, total, err := s.runs.ListDistinctRunTargetsPaginated(ctx, targetType, pageNumber, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list distinct run targets: %w", err)
	}
	return targets, total, nil
}

func RunResponseFromRow(r *store.EvalRun, logger *slog.Logger) RunResponse {
	return runResponseFromRow(r, logger)
}

func DecodeStoredSnapshot[T any](stored *string) T {
	var snapshot T
	if stored != nil && *stored != "" {
		_ = json.Unmarshal([]byte(*stored), &snapshot)
	}
	return snapshot
}

type SnapshotModelConfig struct {
	ID             *string        `json:"id"`
	Alias          *string        `json:"alias"`
	Provider       *string        `json:"provider"`
	ModelName      *string        `json:"modelName"`
	APIBase        *string        `json:"apiBase"`
	ModelParams    map[string]any `json:"modelParams"`
	MaxInputTokens *int           `json:"maxInputTokens"`
	Description    *string        `json:"description"`
}

type SnapshotArtifact struct {
	Filename    string  `json:"filename"`
	Version     int     `json:"version"`
	MIMEType    *string `json:"mime_type"`
	SizeBytes   *int64  `json:"size_bytes"`
	ContentHash *string `json:"content_hash"`
}

type SnapshotConfig struct {
	Experiment *SnapshotExperiment `json:"experiment"`
	Dataset    *SnapshotDataset    `json:"dataset"`
	Agent      *SnapshotAgent      `json:"agent"`
	Workflow   *SnapshotWorkflow   `json:"workflow"`
}

type SnapshotWorkflow struct {
	Name         string          `json:"name"`
	InstanceName *string         `json:"instance_name"`
	InputSchema  json.RawMessage `json:"input_schema"`
}

type SnapshotExperiment struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Models      []struct {
		ModelConfigID string               `json:"modelConfigId"`
		Model         string               `json:"model"`
		Alias         string               `json:"alias"`
		ResolvedModel *string              `json:"resolvedModel"`
		ModelConfig   *SnapshotModelConfig `json:"modelConfig"`
	} `json:"models"`
}

type SnapshotDataset struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ExampleCount int    `json:"example_count"`
	Examples     []struct {
		ID               string            `json:"id"`
		SequenceNumber   int               `json:"sequence_number"`
		Prompt           string            `json:"prompt"`
		ExpectedResponse *string           `json:"expected_response"`
		InputData        json.RawMessage   `json:"input_data"`
		ExpectedPath     *ExpectedPath     `json:"expected_path"`
		ExpectedFacts    *ExpectedFacts    `json:"expected_facts"`
		Artifact         *SnapshotArtifact `json:"artifact"`
		ExpectedArtifact *SnapshotArtifact `json:"expected_artifact"`
	} `json:"examples"`
}

type SnapshotAgent struct {
	Name         string   `json:"name"`
	Description  *string  `json:"description"`
	Type         *string  `json:"type"`
	SystemPrompt *string  `json:"systemPrompt"`
	Toolsets     []string `json:"toolsets"`
	Tools        []struct {
		ID          *string `json:"id"`
		Name        string  `json:"name"`
		Description *string `json:"description"`
	} `json:"tools"`
	Connectors []struct {
		ID      string  `json:"id"`
		Name    string  `json:"name"`
		Type    *string `json:"type"`
		Subtype *string `json:"subtype"`
	} `json:"connectors"`
	Skills []struct {
		ID          *string `json:"id"`
		Name        string  `json:"name"`
		Description *string `json:"description"`
	} `json:"skills"`
	InputModes  []string             `json:"inputModes"`
	OutputModes []string             `json:"outputModes"`
	ModelConfig *SnapshotModelConfig `json:"modelConfig"`
	Origin      *string              `json:"origin"`
}

type SnapshotEvaluator struct {
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Description       *string              `json:"description"`
	Type              string               `json:"type"`
	HeuristicType     *string              `json:"heuristic_type"`
	IsDefault         bool                 `json:"is_default"`
	Model             *string              `json:"model"`
	PromptTemplate    *string              `json:"prompt_template"`
	ChoiceScores      map[string]float64   `json:"choice_scores"`
	ResolvedModelName *string              `json:"resolved_model_name"`
	ModelConfig       *SnapshotModelConfig `json:"modelConfig"`
}

func runResponseFromRow(r *store.EvalRun, logger *slog.Logger) RunResponse {
	resp := RunResponse{
		ID:              r.ID,
		ExperimentID:    r.ExperimentID,
		RunGroupID:      r.RunGroupID,
		Status:          r.Status,
		ProgressCurrent: r.ProgressCurrent,
		ProgressTotal:   r.ProgressTotal,
		ErrorMessage:    r.ErrorMessage,
		Model:           r.Model,
		RunName:         r.RunName,
		Source:          r.Source,
		RunAs:           r.RunAs,
		StartedBy:       r.StartedBy,
		StartedAt:       Timestamp(r.StartedTime),
		SummaryScores:   map[string]float64{},
	}
	if r.CompletedTime != nil {
		t := Timestamp(*r.CompletedTime)
		resp.CompletedAt = &t
	}
	if r.SummaryScores != nil && *r.SummaryScores != "" {
		_ = json.Unmarshal([]byte(*r.SummaryScores), &resp.SummaryScores)
	}
	resp.EvaluatorStats = evaluatorStatsFromRow(r, logger)
	if r.ConfigSnapshot != nil && *r.ConfigSnapshot != "" {
		var cs map[string]any
		if json.Unmarshal([]byte(*r.ConfigSnapshot), &cs) == nil {
			resp.ConfigSnapshot = cs
		}
	}
	if r.EvaluatorSnapshot != nil && *r.EvaluatorSnapshot != "" {
		var es []map[string]any
		if json.Unmarshal([]byte(*r.EvaluatorSnapshot), &es) == nil {
			resp.EvaluatorSnapshot = es
		}
	}
	return resp
}

func runResultResponseFromRow(r *store.EvalRunResultWithScores, logger *slog.Logger) RunResultResponse {
	resp := RunResultResponse{
		ID:                r.ID,
		RunID:             r.RunID,
		ExampleID:         r.ExampleID,
		TrialIndex:        r.TrialIndex,
		InputPrompt:       r.InputPrompt,
		ExpectedResponse:  r.ExpectedResponse,
		TargetAgent:       r.TargetAgent,
		AgentResponse:     r.AgentResponse,
		ResolvedModelName: r.ResolvedModelName,
		ExecutionDataKey:  r.ExecutionDataKey,
		TaskEventsKey:     r.TaskEventsKey,
		DurationSeconds:   r.DurationSeconds,
		ErrorMessage:      r.ErrorMessage,
		FailureKind:       r.FailureKind,
		TargetType:        cmp.Or(derefString(r.TargetType), store.EvalTargetTypeAgent),
		TokenUsage:        tokenUsageFromRow(r.EvalRunResult),
		TokenBreakdown:    decodeTokenBreakdown(r.TokenBreakdown),
		CreatedTime:       Timestamp(r.CreatedTime),
		Scores:            make([]ScoreResponse, 0, len(r.Scores)),
		Artifacts:         artifactsFromMetadata(r.ArtifactsMetadata, r.ID, logger),
		InputArtifact:     exampleArtifactRecordResponse(artifactFromMetadata(r.InputArtifactMetadata, r.ID, logger)),
		ExpectedArtifact:  exampleArtifactRecordResponse(artifactFromMetadata(r.ExpectedArtifactMetadata, r.ID, logger)),
	}
	for _, sc := range r.Scores {
		resp.Scores = append(resp.Scores, ScoreResponse{
			ID:          sc.ID,
			EvaluatorID: sc.EvaluatorID,
			Score:       sc.Score,
			Choice:      sc.Choice,
			Passed:      sc.Passed,
			Reasoning:   sc.Reasoning,
			Details:     scoreDetailsFromRow(sc.Details, sc.ID, logger),
			CreatedTime: Timestamp(sc.CreatedTime),
		})
	}
	return resp
}

func tokenUsageFromRow(r *store.EvalRunResult) *TokenUsage {
	if r.InputTokens == nil && r.OutputTokens == nil && r.CachedInputTokens == nil &&
		r.ReasoningTokens == nil && r.TotalTokens == nil {
		return nil
	}
	return &TokenUsage{
		InputTokens:       r.InputTokens,
		CachedInputTokens: r.CachedInputTokens,
		OutputTokens:      r.OutputTokens,
		ReasoningTokens:   r.ReasoningTokens,
		TotalTokens:       r.TotalTokens,
	}
}

func artifactsFromMetadata(metaJSON *string, resultID string, logger *slog.Logger) []ArtifactResponse {
	if metaJSON == nil || *metaJSON == "" {
		return nil
	}
	var metas []struct {
		Filename  string `json:"filename"`
		Version   int    `json:"version"`
		MIMEType  string `json:"mime_type"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal([]byte(*metaJSON), &metas); err != nil {
		if logger != nil {
			logger.Error("eval: corrupt artifacts_metadata JSON; treating result as having no artifacts",
				"error", err, "resultID", resultID)
		}
		return nil
	}
	out := make([]ArtifactResponse, 0, len(metas))
	for _, m := range metas {
		out = append(out, ArtifactResponse{
			Filename: m.Filename,
			MimeType: m.MIMEType,
			Version:  m.Version,
			Size:     m.SizeBytes,
		})
	}
	return out
}

func (s *RunService) buildTargetConfigSnapshot(ctx context.Context, exp *store.EvalExperiment, datasetName string, examples []*store.EvalExample, models []map[string]any) (map[string]any, error) {
	configSnapshot := buildConfigSnapshot(exp, *exp.DatasetID, datasetName, examples, models)
	if !exp.IsWorkflowTarget() {
		configSnapshot["agent"] = s.buildAgentSnapshot(ctx, exp.TargetAgent)
		return configSnapshot, nil
	}
	workflow, err := s.workflowSnapshot(ctx, exp)
	if err != nil {
		return nil, err
	}
	configSnapshot["workflow"] = workflow
	return configSnapshot, nil
}

func buildConfigSnapshot(exp *store.EvalExperiment, datasetID, datasetName string, examples []*store.EvalExample, models []map[string]any) map[string]any {
	exampleDicts := make([]map[string]any, len(examples))
	for i, ex := range examples {
		exampleDicts[i] = map[string]any{
			"id":                ex.ID,
			"sequence_number":   ex.SequenceNumber,
			"prompt":            ex.Prompt,
			"expected_response": ex.ExpectedResponse,
			"input_data":        inputDataSnapshot(ex.InputData),
			"expected_path":     shownExpectedPath(ex.ExpectedPath),
			"expected_facts":    shownExpectedFacts(ex.ExpectedFacts),
		}
		exampleDicts[i]["artifact"] = configSnapshotArtifact(ex, store.EvalArtifactRoleInput)
		exampleDicts[i]["expected_artifact"] = configSnapshotArtifact(ex, store.EvalArtifactRoleExpected)
	}
	expDict := map[string]any{
		"id":                   exp.ID,
		"name":                 exp.Name,
		"description":          exp.Description,
		"target_agent":         exp.TargetAgent,
		"target_type":          experimentTargetType(exp),
		"runs_per_example":     exp.RunsPerExample,
		"max_workers":          exp.MaxWorkers,
		"primary_evaluator_id": exp.PrimaryEvaluatorID,
		"run_as":               exp.RunAs,
		"models":               models,
	}
	return map[string]any{
		"experiment": expDict,
		"dataset": map[string]any{
			"id":            datasetID,
			"name":          datasetName,
			"example_count": len(examples),
			"examples":      exampleDicts,
		},
		"agent": nil,
	}
}

func (s *RunService) workflowSnapshot(ctx context.Context, exp *store.EvalExperiment) (map[string]any, error) {
	snapshot := map[string]any{"name": exp.TargetAgent}
	if s.workflowTargets == nil {
		return snapshot, nil
	}
	target, err := s.workflowTargets.Resolve(ctx, exp.TargetAgent, exp.RunAs)
	if err != nil {
		return nil, err
	}
	snapshot["instance_name"] = target.InstanceName
	snapshot["input_schema"] = target.InputSchema
	return snapshot, nil
}

func decodeExperimentModels(s string) ([]map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func mapEvalNotFound(err error, resource, id string) error {
	if errors.Is(err, store.ErrEvalNotFound) {
		return &NotFoundError{Resource: resource, ID: id}
	}
	return fmt.Errorf("%s lookup: %w", resource, err)
}

func (s *RunService) ExportRunResultsJSON(ctx context.Context, runID string) ([]map[string]any, error) {
	if _, err := s.runs.GetByID(ctx, runID); err != nil {
		return nil, mapEvalNotFound(err, "EvalRun", runID)
	}
	results, err := s.results.ListByRun(ctx, runID)
	if err != nil {
		return nil, wrapEvalErr(errListRunResults, err)
	}
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		dict := map[string]any{
			"id":                  r.ID,
			"run_id":              r.RunID,
			"example_id":          r.ExampleID,
			"trial_index":         r.TrialIndex,
			"input_prompt":        r.InputPrompt,
			"expected_response":   r.ExpectedResponse,
			"target_agent":        r.TargetAgent,
			"agent_response":      r.AgentResponse,
			"resolved_model_name": r.ResolvedModelName,
			"duration_seconds":    r.DurationSeconds,
			"error_message":       r.ErrorMessage,
		}
		dict["input_artifact"] = artifactFromMetadata(r.InputArtifactMetadata, r.ID, s.logger)
		dict["expected_artifact"] = artifactFromMetadata(r.ExpectedArtifactMetadata, r.ID, s.logger)
		scores := make([]map[string]any, 0, len(r.Scores))
		for _, sc := range r.Scores {
			scores = append(scores, map[string]any{
				"evaluator_id": sc.EvaluatorID,
				"score":        sc.Score,
				"choice":       sc.Choice,
				"passed":       sc.Passed,
				"reasoning":    sc.Reasoning,
			})
		}
		dict["scores"] = scores
		out = append(out, dict)
	}
	return out, nil
}

func (s *RunService) ExportRunResultsCSV(ctx context.Context, runID string) (string, error) {
	if _, err := s.runs.GetByID(ctx, runID); err != nil {
		return "", mapEvalNotFound(err, "EvalRun", runID)
	}
	results, err := s.results.ListByRun(ctx, runID)
	if err != nil {
		return "", wrapEvalErr(errListRunResults, err)
	}

	evaluatorIDs := []string{}
	seen := map[string]bool{}
	for _, r := range results {
		for _, sc := range r.Scores {
			if !seen[sc.EvaluatorID] {
				seen[sc.EvaluatorID] = true
				evaluatorIDs = append(evaluatorIDs, sc.EvaluatorID)
			}
		}
	}
	evaluatorRows, err := s.evaluators.GetByIDs(ctx, evaluatorIDs)
	if err != nil {
		return "", fmt.Errorf("get evaluators: %w", err)
	}
	nameMap := make(map[string]string, len(evaluatorRows))
	for _, e := range evaluatorRows {
		nameMap[e.ID] = e.Name
	}

	header := []string{"example_id", "trial_index", "input_prompt", "expected_response", "agent_response", "duration_seconds", "error_message", "input_artifact", "expected_artifact"}
	for _, eid := range evaluatorIDs {
		name := nameMap[eid]
		if name == "" {
			name = eid
		}
		header = append(header, "score_"+name)
	}
	for _, eid := range evaluatorIDs {
		name := nameMap[eid]
		if name == "" {
			name = eid
		}
		header = append(header, "passed_"+name)
	}

	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("write csv header: %w", err)
	}
	for _, r := range results {
		scoresByEval := make(map[string]*store.EvalRunScore, len(r.Scores))
		for _, sc := range r.Scores {
			scoresByEval[sc.EvaluatorID] = sc
		}
		row := []string{
			r.ExampleID,
			strconv.Itoa(r.TrialIndex),
			csvCellSafe(ptrOrEmpty(r.InputPrompt)),
			csvCellSafe(ptrOrEmpty(r.ExpectedResponse)),
			csvCellSafe(ptrOrEmpty(r.AgentResponse)),
			floatPtrToString(r.DurationSeconds),
			csvCellSafe(ptrOrEmpty(r.ErrorMessage)),
		}
		row = append(row,
			csvCellSafe(artifactCSVCell(r.InputArtifactMetadata, r.ID, s.logger)),
			csvCellSafe(artifactCSVCell(r.ExpectedArtifactMetadata, r.ID, s.logger)))
		for _, eid := range evaluatorIDs {
			sc := scoresByEval[eid]
			if sc == nil {
				row = append(row, "")
			} else {
				row = append(row, fmt.Sprintf("%g", sc.Score))
			}
		}
		for _, eid := range evaluatorIDs {
			sc := scoresByEval[eid]
			if sc == nil || sc.Passed == nil {
				row = append(row, "")
			} else if *sc.Passed {
				row = append(row, "true")
			} else {
				row = append(row, "false")
			}
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("write csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("flush csv: %w", err)
	}
	return buf.String(), nil
}

func ptrOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func csvCellSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}

func floatPtrToString(p *float64) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%g", *p)
}

type traceCostSource interface {
	CostByTrace(ctx context.Context, traceIDs []string, exclude []spend.Surface) (map[string]store.TraceCost, error)
}

func (s *RunService) attachTrialCosts(ctx context.Context, groups ...[]RunResultResponse) {
	ledger, read := s.trialLedgerCosts(ctx, groups)
	for _, results := range groups {
		for i := range results {
			r := &results[i]
			if cost, known := trialCost(r, ledger, read); known {
				formatted := pricing.FormatCost(cost)
				r.Cost = &formatted
			}
			markCountShortOfLedger(r.TokenBreakdown, ledger)
		}
	}
}

func (s *RunService) trialLedgerCosts(ctx context.Context, groups [][]RunResultResponse) (costs map[string]store.TraceCost, read bool) {
	if s.spendLedger == nil {
		return nil, false
	}
	var traceIDs []string
	for _, results := range groups {
		for _, r := range results {
			if r.TokenBreakdown != nil && r.TokenBreakdown.TraceID != "" {
				traceIDs = append(traceIDs, r.TokenBreakdown.TraceID)
			}
		}
	}
	if len(traceIDs) == 0 {
		return nil, true
	}
	costs, err := s.spendLedger.CostByTrace(ctx, traceIDs, []spend.Surface{spend.SurfaceEvalJudge})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.logger.Warn("eval: trial cost lookup failed", "traces", len(traceIDs), "error", err)
		}
		return nil, false
	}
	return costs, true
}
