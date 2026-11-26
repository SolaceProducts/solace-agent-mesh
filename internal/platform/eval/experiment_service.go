// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const MaxRunsPerExample = 10

const (
	maxExperimentNameLen    = 255
	maxExperimentModels     = 3
	maxExperimentEvaluators = 5
	minRunsPerExample       = 1
	minMaxWorkers           = 1
	maxMaxWorkers           = 20
)

func NormalizeRunAs(v string) (string, error) {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return "", nil
	}
	if len(strings.Fields(trimmed)) != 1 {
		return "", &ValidationError{Field: "run_as", Message: "runAs must not contain whitespace"}
	}
	principal := rbac.SystemUserPrincipal(trimmed)
	if principal == rbac.SystemSubPrefix {
		return "", &ValidationError{Field: "run_as", Message: "runAs must name a service account, not the bare \"system:\" prefix"}
	}
	if len(principal) > maxExperimentNameLen {
		return "", &ValidationError{
			Field:   "run_as",
			Message: fmt.Sprintf("runAs must be at most %d characters", maxExperimentNameLen),
		}
	}
	if principal != rbac.EvalSystemUserSub && rbac.IsReservedSystemUserSub(principal) {
		return "", &ValidationError{
			Field:   "run_as",
			Message: fmt.Sprintf("runAs must not name %q: it is a built-in service account that other Agent Mesh features run as, so an experiment would share its tool credentials with them. Leave runAs unset for the eval service account, or declare a serviceAccount of your own and name that", principal),
		}
	}
	return principal, nil
}

func normalizeRunAs(v *string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	principal, err := NormalizeRunAs(*v)
	if err != nil {
		return nil, err
	}
	if principal == "" {
		return nil, nil
	}
	return &principal, nil
}

type ExperimentService struct {
	experiments     *store.EvalExperimentRepo
	datasets        *store.EvalDatasetRepo
	evaluators      *store.EvalEvaluatorRepo
	runs            *store.EvalRunRepo
	registry        *features.Registry
	workflowTargets *WorkflowTargetValidator
	fileDeletes     *FileDeletes
	logger          *slog.Logger
}

func (s *ExperimentService) WithFileDeletes(f *FileDeletes) *ExperimentService {
	s.fileDeletes = f
	return s
}

func NewExperimentService(
	experiments *store.EvalExperimentRepo,
	datasets *store.EvalDatasetRepo,
	evaluators *store.EvalEvaluatorRepo,
	runs *store.EvalRunRepo,
	logger *slog.Logger,
) *ExperimentService {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &ExperimentService{
		experiments: experiments,
		datasets:    datasets,
		evaluators:  evaluators,
		runs:        runs,
		logger:      logger,
	}
}

func (s *ExperimentService) SetFeatureRegistry(registry *features.Registry) {
	s.registry = registry
}

func (s *ExperimentService) SetWorkflowTargetValidator(v *WorkflowTargetValidator) {
	s.workflowTargets = v
}

func (s *ExperimentService) List(ctx context.Context, datasetID, evaluatorID, targetAgent string) ([]ExperimentResponse, error) {
	rows, err := s.experiments.List(ctx, datasetID, evaluatorID, targetAgent)
	if err != nil {
		return nil, fmt.Errorf("list experiments: %w", err)
	}
	out := make([]ExperimentResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, experimentResponseFromEnriched(r))
	}
	return out, nil
}

func (s *ExperimentService) ListPaginated(
	ctx context.Context,
	filter store.EvalExperimentFilter,
	pageNumber, pageSize int,
	sortBy store.EvalExperimentSortField,
	desc bool,
) ([]ExperimentResponse, int, error) {
	if sortBy == "" {
		sortBy, desc = store.EvalExperimentSortCreatedTime, true
	}
	rows, total, err := s.experiments.ListPaginated(ctx, filter, pageNumber, pageSize, sortBy, desc)
	if err != nil {
		return nil, 0, fmt.Errorf("list experiments: %w", err)
	}
	out := make([]ExperimentResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, experimentResponseFromEnriched(r))
	}
	return out, total, nil
}

func (s *ExperimentService) Get(ctx context.Context, id string) (*ExperimentResponse, error) {
	row, err := s.experiments.GetEnriched(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return nil, &NotFoundError{Resource: "EvalExperiment", ID: id}
		}
		return nil, fmt.Errorf("get experiment: %w", err)
	}
	resp := experimentResponseFromEnriched(row)
	return &resp, nil
}

func (s *ExperimentService) GetBase(ctx context.Context, id string) (*store.EvalExperiment, error) {
	row, err := s.experiments.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return nil, &NotFoundError{Resource: "EvalExperiment", ID: id}
		}
		return nil, fmt.Errorf("get experiment: %w", err)
	}
	return row, nil
}

func (s *ExperimentService) GetSample(ctx context.Context) (*ExperimentResponse, error) {
	base, err := s.experiments.GetDefault(ctx)
	if err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return nil, &NotFoundError{Resource: "EvalExperiment", ID: "sample"}
		}
		return nil, fmt.Errorf("get sample experiment: %w", err)
	}
	return s.Get(ctx, base.ID)
}

func (s *ExperimentService) Create(ctx context.Context, req CreateExperimentRequest, userID string) (*ExperimentResponse, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}
	if len(name) > maxExperimentNameLen {
		return nil, &ValidationError{Field: "name", Message: fmt.Sprintf("name must be at most %d characters", maxExperimentNameLen)}
	}
	if strings.TrimSpace(req.DatasetID) == "" {
		return nil, &ValidationError{Field: "dataset_id", Message: "datasetId is required"}
	}
	target, err := parseExperimentTarget(req.TargetAgent, req.TargetWorkflow, s.registry)
	if err != nil {
		return nil, err
	}
	if len(req.Models) > maxExperimentModels {
		return nil, &ValidationError{Field: "models", Message: fmt.Sprintf("at most %d models allowed", maxExperimentModels)}
	}
	if req.RunsPerExample != nil && (*req.RunsPerExample < minRunsPerExample || *req.RunsPerExample > MaxRunsPerExample) {
		return nil, &ValidationError{Field: "runs_per_example", Message: fmt.Sprintf("runsPerExample must be between %d and %d", minRunsPerExample, MaxRunsPerExample)}
	}
	if req.MaxWorkers != nil && (*req.MaxWorkers < minMaxWorkers || *req.MaxWorkers > maxMaxWorkers) {
		return nil, &ValidationError{Field: "max_workers", Message: fmt.Sprintf("maxWorkers must be between %d and %d", minMaxWorkers, maxMaxWorkers)}
	}
	if len(req.EvaluatorIDs) > maxExperimentEvaluators {
		return nil, &ValidationError{Field: "evaluator_ids", Message: fmt.Sprintf("at most %d evaluators allowed", maxExperimentEvaluators)}
	}
	runAs, err := normalizeRunAs(req.RunAs)
	if err != nil {
		return nil, err
	}

	if _, err := s.datasets.GetByID(ctx, req.DatasetID); err != nil {
		return nil, mapDatasetNotFound(err, req.DatasetID)
	}
	if err := s.validateEvaluatorIDs(ctx, req.EvaluatorIDs, req.PrimaryEvaluatorID); err != nil {
		return nil, err
	}
	if target.kind == store.EvalTargetTypeWorkflow && s.workflowTargets != nil {
		if _, err := s.workflowTargets.Validate(ctx, target.name, runAs, req.DatasetID); err != nil {
			return nil, err
		}
	}

	modelsJSON := "[]"
	if len(req.Models) > 0 {
		b, err := json.Marshal(req.Models)
		if err != nil {
			return nil, fmt.Errorf("marshal models: %w", err)
		}
		modelsJSON = string(b)
	}

	exp := &store.EvalExperiment{
		Name:               name,
		Description:        req.Description,
		TargetAgent:        target.name,
		TargetType:         target.kind,
		Models:             modelsJSON,
		CreatedBy:          userID,
		UpdatedBy:          userID,
		PrimaryEvaluatorID: req.PrimaryEvaluatorID,
		RunAs:              runAs,
	}
	dsID := req.DatasetID
	exp.DatasetID = &dsID
	if req.RunsPerExample != nil {
		exp.RunsPerExample = *req.RunsPerExample
	}
	if req.MaxWorkers != nil {
		exp.MaxWorkers = *req.MaxWorkers
	}

	if err := s.experiments.Create(ctx, exp, req.EvaluatorIDs); err != nil {
		if errors.Is(err, store.ErrEvalNameTaken) {
			return nil, &ConflictError{Message: fmt.Sprintf("experiment with name %q already exists", name)}
		}
		return nil, fmt.Errorf("create experiment: %w", err)
	}

	return s.Get(ctx, exp.ID)
}

func (s *ExperimentService) Update(ctx context.Context, id string, req UpdateExperimentRequest, userID string) (*ExperimentResponse, error) {
	if err := normalizeUpdateExperimentRequest(&req); err != nil {
		return nil, err
	}
	target, retarget, targetErr := parseUpdateTarget(req, s.registry)
	if targetErr != nil {
		return nil, targetErr
	}

	if req.DatasetID != nil {
		if _, err := s.datasets.GetByID(ctx, *req.DatasetID); err != nil {
			return nil, mapDatasetNotFound(err, *req.DatasetID)
		}
	}

	if err := s.validateEvaluatorSetForUpdate(ctx, id, req); err != nil {
		return nil, err
	}
	if err := s.checkWorkflowTargetForUpdate(ctx, id, req, target, retarget); err != nil {
		return nil, err
	}

	in, err := buildExperimentRepoUpdate(req, userID)
	if err != nil {
		return nil, err
	}
	if retarget {
		in.TargetAgent, in.TargetType = &target.name, &target.kind
	}
	if _, err := s.experiments.Update(ctx, id, in); err != nil {
		return nil, mapExperimentUpdateErr(err, id)
	}

	if req.EvaluatorIDsSet {
		if err := s.experiments.DetachAllEvaluators(ctx, id); err != nil {
			return nil, fmt.Errorf("detach evaluators: %w", err)
		}
		if err := s.experiments.AttachEvaluators(ctx, id, req.EvaluatorIDs); err != nil {
			return nil, fmt.Errorf("attach evaluators: %w", err)
		}
	}

	return s.Get(ctx, id)
}

func (s *ExperimentService) checkWorkflowTargetForUpdate(ctx context.Context, id string, req UpdateExperimentRequest, target experimentTarget, retarget bool) error {
	if s.workflowTargets == nil || (!retarget && req.DatasetID == nil && !req.RunAsSet) {
		return nil
	}
	existing, err := s.experiments.GetByID(ctx, id)
	if err != nil {
		return mapExperimentUpdateErr(err, id)
	}
	name, kind := existing.TargetAgent, experimentTargetType(existing)
	if retarget {
		name, kind = target.name, target.kind
	}
	if kind != store.EvalTargetTypeWorkflow {
		return nil
	}
	runAs := existing.RunAs
	if req.RunAsSet {
		runAs = req.RunAs
	}
	datasetID := derefString(existing.DatasetID)
	if req.DatasetID != nil {
		datasetID = *req.DatasetID
	}
	if datasetID == "" {
		_, err = s.workflowTargets.Resolve(ctx, name, runAs)
		return err
	}
	_, err = s.workflowTargets.Validate(ctx, name, runAs, datasetID)
	return err
}

func normalizeUpdateExperimentRequest(req *UpdateExperimentRequest) error {
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			return &ValidationError{Field: "name", Message: "name cannot be empty"}
		}
		if len(trimmed) > maxExperimentNameLen {
			return &ValidationError{Field: "name", Message: fmt.Sprintf("name must be at most %d characters", maxExperimentNameLen)}
		}
		req.Name = &trimmed
	}
	if req.ModelsSet && len(req.Models) > maxExperimentModels {
		return &ValidationError{Field: "models", Message: fmt.Sprintf("at most %d models allowed", maxExperimentModels)}
	}
	if req.RunsPerExample != nil && (*req.RunsPerExample < minRunsPerExample || *req.RunsPerExample > MaxRunsPerExample) {
		return &ValidationError{Field: "runs_per_example", Message: fmt.Sprintf("runsPerExample must be between %d and %d", minRunsPerExample, MaxRunsPerExample)}
	}
	if req.MaxWorkers != nil && (*req.MaxWorkers < minMaxWorkers || *req.MaxWorkers > maxMaxWorkers) {
		return &ValidationError{Field: "max_workers", Message: fmt.Sprintf("maxWorkers must be between %d and %d", minMaxWorkers, maxMaxWorkers)}
	}
	if req.EvaluatorIDsSet && len(req.EvaluatorIDs) > maxExperimentEvaluators {
		return &ValidationError{Field: "evaluator_ids", Message: fmt.Sprintf("at most %d evaluators allowed", maxExperimentEvaluators)}
	}
	if req.DatasetID != nil && strings.TrimSpace(*req.DatasetID) == "" {
		return &ValidationError{Field: "dataset_id", Message: "datasetId cannot be empty"}
	}
	if req.RunAsSet {
		norm, err := normalizeRunAs(req.RunAs)
		if err != nil {
			return err
		}
		req.RunAs = norm
	}
	return nil
}

func (s *ExperimentService) validateEvaluatorSetForUpdate(ctx context.Context, experimentID string, req UpdateExperimentRequest) error {
	if req.EvaluatorIDsSet {
		var primary *string
		if req.PrimaryEvaluatorIDSet {
			primary = req.PrimaryEvaluatorID
		}
		return s.validateEvaluatorIDs(ctx, req.EvaluatorIDs, primary)
	}
	if req.PrimaryEvaluatorIDSet && req.PrimaryEvaluatorID != nil {
		existing, err := s.experiments.GetEvaluatorIDs(ctx, experimentID)
		if err != nil {
			return fmt.Errorf("get evaluator ids: %w", err)
		}
		return validatePrimaryInSet(*req.PrimaryEvaluatorID, existing)
	}
	return nil
}

func buildExperimentRepoUpdate(req UpdateExperimentRequest, userID string) (store.EvalExperimentUpdate, error) {
	in := store.EvalExperimentUpdate{
		UpdatedBy:      userID,
		Name:           req.Name,
		Description:    req.Description,
		DatasetID:      req.DatasetID,
		RunsPerExample: req.RunsPerExample,
		MaxWorkers:     req.MaxWorkers,
	}
	if req.ModelsSet {
		modelsJSON := "[]"
		if req.Models != nil {
			b, err := json.Marshal(req.Models)
			if err != nil {
				return in, fmt.Errorf("marshal models: %w", err)
			}
			modelsJSON = string(b)
		}
		in.Models = &modelsJSON
	}
	if req.PrimaryEvaluatorIDSet {
		if req.PrimaryEvaluatorID == nil {
			empty := ""
			in.PrimaryEvaluatorID = &empty
		} else {
			in.PrimaryEvaluatorID = req.PrimaryEvaluatorID
		}
	}
	if req.RunAsSet {
		if req.RunAs == nil {
			empty := ""
			in.RunAs = &empty
		} else {
			in.RunAs = req.RunAs
		}
	}
	return in, nil
}

func mapExperimentUpdateErr(err error, id string) error {
	if errors.Is(err, store.ErrEvalNotFound) {
		return &NotFoundError{Resource: "EvalExperiment", ID: id}
	}
	if errors.Is(err, store.ErrEvalNameTaken) {
		return &ConflictError{Message: "experiment name already taken"}
	}
	return fmt.Errorf("update experiment: %w", err)
}

func (s *ExperimentService) Delete(ctx context.Context, id string, userID string) error {
	active, err := s.runs.HasActiveRuns(ctx, id)
	if err != nil {
		return fmt.Errorf("check active runs: %w", err)
	}
	if active {
		return &BusinessRuleViolationError{
			Code: "EXPERIMENT_HAS_ACTIVE_RUNS",
			Message: "Cannot delete experiment while it has active (pending/running) runs. " +
				"Cancel or wait for runs to complete first.",
		}
	}
	s.logger.Info("eval: deleting experiment", "experimentID", id, "userID", userID)
	if err := s.experiments.Delete(ctx, id, s.fileDeletes.event()); err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return &NotFoundError{Resource: "EvalExperiment", ID: id}
		}
		return fmt.Errorf("delete experiment: %w", err)
	}
	return nil
}

func (s *ExperimentService) validateEvaluatorIDs(ctx context.Context, evaluatorIDs []string, primaryEvaluatorID *string) error {
	if len(evaluatorIDs) == 0 {
		return &ValidationError{
			Field:   "evaluator_ids",
			Message: "At least one evaluator is required.",
		}
	}
	found, err := s.evaluators.GetByIDs(ctx, evaluatorIDs)
	if err != nil {
		return fmt.Errorf("get evaluators by ids: %w", err)
	}
	foundIDs := make(map[string]bool, len(found))
	for _, e := range found {
		foundIDs[e.ID] = true
	}
	missing := make([]string, 0)
	for _, id := range evaluatorIDs {
		if !foundIDs[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return &ValidationError{
			Field:   "evaluator_ids",
			Message: fmt.Sprintf("Evaluator(s) not found: %v", missing),
		}
	}
	if primaryEvaluatorID != nil && *primaryEvaluatorID != "" {
		return validatePrimaryInSet(*primaryEvaluatorID, evaluatorIDs)
	}
	return nil
}

func validatePrimaryInSet(primary string, evaluatorIDs []string) error {
	if slices.Contains(evaluatorIDs, primary) {
		return nil
	}
	return &ValidationError{
		Field:   "primary_evaluator_id",
		Message: "primaryEvaluatorId must be one of the attached evaluator IDs.",
	}
}

func experimentResponseFromEnriched(r *store.EvalExperimentEnriched) ExperimentResponse {
	resp := ExperimentResponse{
		ID:                 r.ID,
		Name:               r.Name,
		Description:        r.Description,
		DatasetID:          r.DatasetID,
		DatasetName:        r.DatasetName,
		RunsPerExample:     r.RunsPerExample,
		MaxWorkers:         r.MaxWorkers,
		PrimaryEvaluatorID: r.PrimaryEvaluatorID,
		EvaluatorIDs:       r.EvaluatorIDs,
		IsDefault:          r.IsDefault,
		RunAs:              r.RunAs,
		LatestRunStatus:    r.LatestRunStatus,
		CreatedBy:          r.CreatedBy,
		UpdatedBy:          r.UpdatedBy,
		CreatedTime:        Timestamp(r.CreatedTime),
		UpdatedTime:        Timestamp(r.UpdatedTime),
	}
	if r.LatestRunTime != nil {
		t := Timestamp(*r.LatestRunTime)
		resp.LatestRunTime = &t
	}
	if r.DatasetUpdatedTime != nil {
		t := Timestamp(*r.DatasetUpdatedTime)
		resp.DatasetUpdatedTime = &t
	}
	if resp.EvaluatorIDs == nil {
		resp.EvaluatorIDs = []string{}
	}
	resp.Models = decodeModels(r.Models)
	setResponseTarget(&resp, r.EvalExperiment)
	return resp
}

func decodeModels(s string) []map[string]any {
	if s == "" {
		return []map[string]any{}
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []map[string]any{}
	}
	if out == nil {
		return []map[string]any{}
	}
	return out
}
