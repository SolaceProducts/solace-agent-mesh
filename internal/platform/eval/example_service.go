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
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ExampleService struct {
	examples    *store.EvalExampleRepo
	datasets    *store.EvalDatasetRepo
	logger      *slog.Logger
	maxExamples int
	fileDeletes *FileDeletes
	registry    *features.Registry
}

func (s *ExampleService) SetFeatureRegistry(registry *features.Registry) {
	s.registry = registry
}

func (s *ExampleService) WithFileDeletes(f *FileDeletes) *ExampleService {
	s.fileDeletes = f
	return s
}

func (s *ExampleService) WithMaxExamples(n int) *ExampleService {
	s.maxExamples = n
	return s
}

func (s *ExampleService) maxExamplesCap() int {
	if s.maxExamples > 0 {
		return s.maxExamples
	}
	return MaxExamplesPerDataset
}

func NewExampleService(examples *store.EvalExampleRepo, datasets *store.EvalDatasetRepo, logger *slog.Logger) *ExampleService {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &ExampleService{examples: examples, datasets: datasets, logger: logger}
}

func (s *ExampleService) List(ctx context.Context, datasetID string) ([]ExampleResponse, error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	rows, err := s.examples.ListByDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("list examples: %w", err)
	}
	out := make([]ExampleResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, exampleResponseFromRow(r))
	}
	return out, nil
}

func (s *ExampleService) ListPaginated(
	ctx context.Context,
	datasetID string,
	pageNumber, pageSize int,
	contentFilter string,
	sortBy store.EvalExampleSortField,
	desc bool,
) (*PaginatedResponse[ExampleResponse], error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	rows, total, err := s.examples.ListByDatasetPaginated(ctx, datasetID, pageNumber, pageSize, contentFilter, sortBy, desc)
	if err != nil {
		return nil, fmt.Errorf("list paginated examples: %w", err)
	}
	data := make([]ExampleResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, exampleResponseFromRow(r))
	}
	return &PaginatedResponse[ExampleResponse]{
		Data: data,
		Meta: newPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *ExampleService) Get(ctx context.Context, id string) (*ExampleResponse, error) {
	row, err := s.examples.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return nil, &NotFoundError{Resource: "EvalExample", ID: id}
		}
		return nil, fmt.Errorf("get example: %w", err)
	}
	resp := exampleResponseFromRow(row)
	return &resp, nil
}

func (s *ExampleService) Create(ctx context.Context, datasetID string, req CreateExampleRequest) (*ExampleResponse, error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	inputData, err := s.acceptInputData(req.InputData)
	if err != nil {
		return nil, err
	}
	if !hasExampleInput(req.Prompt, inputData != nil) {
		return nil, &ValidationError{Field: "prompt", Message: "prompt is required unless the example has inputData"}
	}
	expectedPath, err := s.acceptExpectedPath(req.ExpectedPath)
	if err != nil {
		return nil, err
	}
	expectedFacts, err := s.acceptExpectedFacts(req.ExpectedFacts)
	if err != nil {
		return nil, err
	}

	count, err := s.examples.CountByDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("count examples: %w", err)
	}
	maxCap := s.maxExamplesCap()
	if count >= maxCap {
		return nil, &BusinessRuleViolationError{
			Code:    "DATASET_EXAMPLE_CAP_REACHED",
			Message: fmt.Sprintf("Dataset already has the maximum of %d examples.", maxCap),
		}
	}

	seq, err := s.resolveSequenceNumber(ctx, datasetID, req.SequenceNumber)
	if err != nil {
		return nil, err
	}

	e := &store.EvalExample{
		DatasetID:        datasetID,
		SequenceNumber:   seq,
		Prompt:           req.Prompt,
		ExpectedResponse: req.ExpectedResponse,
		InputData:        inputData,
		ExpectedPath:     expectedPath,
		ExpectedFacts:    expectedFacts,
	}
	if err := s.examples.Create(ctx, e); err != nil {
		return nil, fmt.Errorf("create example: %w", err)
	}
	resp := exampleResponseFromRow(e)
	return &resp, nil
}

func (s *ExampleService) Update(ctx context.Context, id string, req UpdateExampleRequest) (*ExampleResponse, error) {
	var inputData *string
	if req.InputDataSet {
		accepted, err := s.acceptInputData(req.InputData)
		if err != nil {
			return nil, err
		}
		inputData = accepted
	}
	clearsInput := req.InputDataSet && inputData == nil
	if err := s.checkUpdateLeavesInput(ctx, id, req.Prompt, req.InputDataSet, inputData); err != nil {
		return nil, err
	}
	expectedPath, err := acceptedWhenSet(req.ExpectedPathSet, req.ExpectedPath, s.acceptExpectedPath)
	if err != nil {
		return nil, err
	}
	expectedFacts, err := acceptedWhenSet(req.ExpectedFactsSet, req.ExpectedFacts, s.acceptExpectedFacts)
	if err != nil {
		return nil, err
	}

	clearExpected := req.ExpectedResponseSet && req.ExpectedResponse == nil
	expectedPtr := req.ExpectedResponse
	if !req.ExpectedResponseSet {
		expectedPtr = nil
	}

	updated, err := s.examples.UpdateWith(ctx, id, store.EvalExampleUpdate{
		Prompt:           req.Prompt,
		ExpectedResponse: expectedPtr,
		ClearExpected:    clearExpected,
		InputData:        inputData,
		ClearInputData:   clearsInput,
		ExpectedPath:     expectedPath,
		ClearPath:        req.ExpectedPathSet && req.ExpectedPath == nil,
		ExpectedFacts:    expectedFacts,
		ClearFacts:       req.ExpectedFactsSet && req.ExpectedFacts == nil,
	})
	if err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return nil, &NotFoundError{Resource: "EvalExample", ID: id}
		}
		return nil, fmt.Errorf("update example: %w", err)
	}
	resp := exampleResponseFromRow(updated)
	return &resp, nil
}

func (s *ExampleService) checkUpdateLeavesInput(ctx context.Context, id string, newPrompt *string, inputDataSet bool, newInputData *string) error {
	emptiesPrompt := newPrompt != nil && strings.TrimSpace(*newPrompt) == ""
	clearsInput := inputDataSet && newInputData == nil
	if !emptiesPrompt && !clearsInput {
		return nil
	}
	existing, err := s.examples.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return &NotFoundError{Resource: "EvalExample", ID: id}
		}
		return fmt.Errorf("get example: %w", err)
	}
	prompt := existing.Prompt
	if newPrompt != nil {
		prompt = *newPrompt
	}
	hasInputData := existing.InputData != nil
	if inputDataSet {
		hasInputData = newInputData != nil
	}
	if hasExampleInput(prompt, hasInputData) {
		return nil
	}
	if emptiesPrompt {
		return &ValidationError{Field: "prompt", Message: "prompt cannot be empty unless the example has inputData"}
	}
	return &ValidationError{Field: "input_data", Message: "inputData cannot be cleared from an example with no prompt"}
}

func (s *ExampleService) acceptInputData(raw json.RawMessage) (*string, error) {
	inputData, err := encodeInputData(raw)
	if err != nil || inputData == nil {
		return nil, err
	}
	if err := inputDataRefusal(s.registry); err != nil {
		return nil, err
	}
	return inputData, nil
}

func (s *ExampleService) acceptExpectedPath(path *ExpectedPath) (*string, error) {
	expectedPath, err := encodeExpectedPath(path)
	if err != nil || expectedPath == nil {
		return nil, err
	}
	if err := expectedPathRefusal(s.registry); err != nil {
		return nil, err
	}
	return expectedPath, nil
}

func (s *ExampleService) acceptExpectedFacts(facts *ExpectedFacts) (*string, error) {
	expectedFacts, err := encodeExpectedFacts(facts)
	if err != nil || expectedFacts == nil {
		return nil, err
	}
	if err := expectedFactsRefusal(s.registry); err != nil {
		return nil, err
	}
	return expectedFacts, nil
}

func acceptedWhenSet[T any](set bool, v *T, accept func(*T) (*string, error)) (*string, error) {
	if !set {
		return nil, nil
	}
	return accept(v)
}

func (s *ExampleService) Delete(ctx context.Context, id string) error {
	if err := s.examples.Delete(ctx, id, s.fileDeletes.event()); err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return &NotFoundError{Resource: "EvalExample", ID: id}
		}
		return fmt.Errorf("delete example: %w", err)
	}
	return nil
}

func (s *ExampleService) resolveSequenceNumber(ctx context.Context, datasetID string, requested *int) (int, error) {
	if requested == nil {
		seq, err := s.examples.NextSequenceNumber(ctx, datasetID)
		if err != nil {
			return 0, fmt.Errorf("next sequence: %w", err)
		}
		return seq, nil
	}
	if *requested <= 0 {
		return 0, &ValidationError{Field: "sequenceNumber", Message: "sequenceNumber must be a positive integer"}
	}
	rows, err := s.examples.ListByDataset(ctx, datasetID)
	if err != nil {
		return 0, fmt.Errorf("list examples: %w", err)
	}
	for _, r := range rows {
		if r.SequenceNumber == *requested {
			return 0, &BusinessRuleViolationError{
				Code:    "EXAMPLE_SEQUENCE_NUMBER_TAKEN",
				Message: fmt.Sprintf("sequenceNumber %d already exists in dataset", *requested),
			}
		}
	}
	return *requested, nil
}

func exampleResponseFromRow(e *store.EvalExample) ExampleResponse {
	return ExampleResponse{
		ID:               e.ID,
		DatasetID:        e.DatasetID,
		SequenceNumber:   e.SequenceNumber,
		Prompt:           e.Prompt,
		ExpectedResponse: e.ExpectedResponse,
		InputData:        inputDataJSON(e.InputData),
		ExpectedPath:     shownExpectedPath(e.ExpectedPath),
		ExpectedFacts:    shownExpectedFacts(e.ExpectedFacts),
		CreatedTime:      Timestamp(e.CreatedTime),
		UpdatedTime:      Timestamp(e.UpdatedTime),
		InputArtifact:    exampleArtifactResponseFromRow(e.ArtifactByRole(store.EvalArtifactRoleInput)),
		ExpectedArtifact: exampleArtifactResponseFromRow(e.ArtifactByRole(store.EvalArtifactRoleExpected)),
	}
}

func exampleArtifactResponseFromRow(row *store.EvalExampleArtifact) *ExampleArtifactResponse {
	if row == nil || row.Filename == "" {
		return nil
	}
	return &ExampleArtifactResponse{
		Filename:    row.Filename,
		MimeType:    row.MIMEType,
		Size:        row.SizeBytes,
		Version:     row.Version,
		ContentHash: row.ContentHash,
	}
}
