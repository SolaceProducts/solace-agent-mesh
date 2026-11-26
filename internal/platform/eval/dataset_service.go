// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var MaxExamplesPerDataset = 5000

const (
	maxDatasetNameLen        = 255
	maxDatasetDescriptionLen = 1000
)

const legacyExpectedResponseKey = "expected_response"

type DatasetService struct {
	datasets    *store.EvalDatasetRepo
	examples    *store.EvalExampleRepo
	runs        *store.EvalRunRepo
	logger      *slog.Logger
	aiGen       *AIGenConfig
	maxExamples int
	fileDeletes *FileDeletes
	generations *store.EvalGenerationOperationRepo
	dispatch    *generationDispatch
	registry    *features.Registry
}

func (s *DatasetService) SetFeatureRegistry(registry *features.Registry) {
	s.registry = registry
}

func (s *DatasetService) WithFileDeletes(f *FileDeletes) *DatasetService {
	s.fileDeletes = f
	return s
}

func (s *DatasetService) WithMaxExamples(n int) *DatasetService {
	s.maxExamples = n
	return s
}

func (s *DatasetService) maxExamplesCap() int {
	if s.maxExamples > 0 {
		return s.maxExamples
	}
	return MaxExamplesPerDataset
}

func NewDatasetService(
	datasets *store.EvalDatasetRepo,
	examples *store.EvalExampleRepo,
	runs *store.EvalRunRepo,
	logger *slog.Logger,
) *DatasetService {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &DatasetService{datasets: datasets, examples: examples, runs: runs, logger: logger}
}

func (s *DatasetService) List(ctx context.Context) ([]DatasetResponse, error) {
	rows, err := s.datasets.ListWithCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list datasets: %w", err)
	}
	out := make([]DatasetResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, datasetResponseFromCounts(r))
	}
	return out, nil
}

func (s *DatasetService) ListPaginated(
	ctx context.Context,
	pageNumber, pageSize int,
	nameFilter string,
	sortBy string,
	sortOrder string,
) (*PaginatedResponse[DatasetResponse], error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if sortBy == "" {
		sortBy = string(store.EvalDatasetSortCreatedTime)
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}

	rows, total, err := s.datasets.ListPaginated(ctx, pageNumber, pageSize, nameFilter, store.EvalDatasetSortField(sortBy), sortOrder)
	if err != nil {
		return nil, fmt.Errorf("list paginated datasets: %w", err)
	}

	data := make([]DatasetResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, datasetResponseFromCounts(r))
	}
	return &PaginatedResponse[DatasetResponse]{
		Data: data,
		Meta: newPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func (s *DatasetService) Get(ctx context.Context, datasetID string) (*DatasetResponse, error) {
	row, err := s.datasets.GetWithCounts(ctx, datasetID)
	if err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	resp := datasetResponseFromCounts(row)
	return &resp, nil
}

func (s *DatasetService) Create(ctx context.Context, req CreateDatasetRequest, userID string) (*DatasetResponse, error) {
	name := strings.TrimSpace(req.Name)
	if err := validateDatasetName(name); err != nil {
		return nil, err
	}
	if err := validateDatasetDescription(req.Description); err != nil {
		return nil, err
	}

	d := &store.EvalDataset{
		Name:      name,
		CreatedBy: userID,
		UpdatedBy: userID,
	}
	if req.Description != "" {
		desc := req.Description
		d.Description = &desc
	}

	if err := s.datasets.Create(ctx, d); err != nil {
		if errors.Is(err, store.ErrEvalNameTaken) {
			return nil, &ConflictError{Message: fmt.Sprintf("dataset with name %q already exists", name)}
		}
		return nil, fmt.Errorf("create dataset: %w", err)
	}

	return s.Get(ctx, d.ID)
}

func (s *DatasetService) Update(ctx context.Context, datasetID string, req UpdateDatasetRequest, userID string) (*DatasetResponse, error) {
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if err := validateDatasetName(trimmed); err != nil {
			return nil, err
		}
		req.Name = &trimmed
	}
	if req.Description != nil {
		if err := validateDatasetDescription(*req.Description); err != nil {
			return nil, err
		}
	}

	if _, err := s.datasets.Update(ctx, datasetID, req.Name, req.Description, userID); err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return nil, &NotFoundError{Resource: "EvalDataset", ID: datasetID}
		}
		if errors.Is(err, store.ErrEvalNameTaken) {
			return nil, &ConflictError{Message: "dataset name already taken"}
		}
		return nil, fmt.Errorf("update dataset: %w", err)
	}
	return s.Get(ctx, datasetID)
}

func (s *DatasetService) Delete(ctx context.Context, datasetID string, userID string) error {
	active, err := s.runs.HasActiveRunsForDataset(ctx, datasetID)
	if err != nil {
		return fmt.Errorf("check active runs: %w", err)
	}
	if active {
		return &BusinessRuleViolationError{
			Code: "DATASET_HAS_ACTIVE_RUNS",
			Message: "Cannot delete dataset while one or more of its experiments has active (pending/running) runs. " +
				"Cancel or wait for runs to complete first.",
		}
	}
	s.logger.Info("eval: deleting dataset", "datasetID", datasetID, "userID", userID)
	if err := s.datasets.Delete(ctx, datasetID, s.fileDeletes.event()); err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return &NotFoundError{Resource: "EvalDataset", ID: datasetID}
		}
		return fmt.Errorf("delete dataset: %w", err)
	}
	return nil
}

func (s *DatasetService) ImportExamples(ctx context.Context, datasetID string, body string, contentType string, userID string) (*ImportExamplesResult, error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	if strings.Contains(contentType, "application/json") {
		return s.importExamplesJSON(ctx, datasetID, body)
	}
	return s.importExamplesCSV(ctx, datasetID, body)
}

type optionalExportColumn struct {
	name  string
	value func(*store.EvalExample) *string
}

var optionalExportColumns = []optionalExportColumn{
	{inputDataColumn, func(ex *store.EvalExample) *string { return ex.InputData }},
	{expectedPathColumn, func(ex *store.EvalExample) *string { return ex.ExpectedPath }},
	{expectedFactsColumn, func(ex *store.EvalExample) *string { return ex.ExpectedFacts }},
}

func (s *DatasetService) ExportExamplesCSV(ctx context.Context, datasetID string) (string, error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return "", mapDatasetNotFound(err, datasetID)
	}
	rows, err := s.examples.ListByDataset(ctx, datasetID)
	if err != nil {
		return "", fmt.Errorf("list examples: %w", err)
	}
	header := []string{"sequence_number", "prompt", "expected_response"}
	var columns []optionalExportColumn
	for _, col := range optionalExportColumns {
		if slices.ContainsFunc(rows, func(ex *store.EvalExample) bool { return col.value(ex) != nil }) {
			columns = append(columns, col)
			header = append(header, col.name)
		}
	}
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("write csv header: %w", err)
	}
	for _, ex := range rows {
		record := []string{fmt.Sprintf("%d", ex.SequenceNumber), ex.Prompt, derefString(ex.ExpectedResponse)}
		for _, col := range columns {
			record = append(record, derefString(col.value(ex)))
		}
		if err := w.Write(record); err != nil {
			return "", fmt.Errorf("write csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("csv writer: %w", err)
	}
	return buf.String(), nil
}

type ExportExamplesJSONRecord struct {
	SequenceNumber   int             `json:"sequenceNumber"`
	Prompt           string          `json:"prompt"`
	ExpectedResponse *string         `json:"expectedResponse"`
	InputData        json.RawMessage `json:"inputData,omitempty"`
	ExpectedPath     *ExpectedPath   `json:"expectedPath,omitempty"`
	ExpectedFacts    *ExpectedFacts  `json:"expectedFacts,omitempty"`
}

func (s *DatasetService) ExportExamplesJSON(ctx context.Context, datasetID string) ([]ExportExamplesJSONRecord, error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	rows, err := s.examples.ListByDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("list examples: %w", err)
	}
	out := make([]ExportExamplesJSONRecord, 0, len(rows))
	for _, ex := range rows {
		out = append(out, ExportExamplesJSONRecord{
			SequenceNumber:   ex.SequenceNumber,
			Prompt:           ex.Prompt,
			ExpectedResponse: ex.ExpectedResponse,
			InputData:        inputDataJSON(ex.InputData),
			ExpectedPath:     s.exportedPath(datasetID, ex),
			ExpectedFacts:    s.exportedFacts(datasetID, ex),
		})
	}
	return out, nil
}

func (s *DatasetService) exportedPath(datasetID string, ex *store.EvalExample) *ExpectedPath {
	path, err := decodeExpectedPath(ex.ExpectedPath)
	if err != nil {
		s.logger.Warn("dataset service: export left out an unreadable expected path", "datasetID", datasetID, "exampleID", ex.ID)
	}
	return path
}

func (s *DatasetService) exportedFacts(datasetID string, ex *store.EvalExample) *ExpectedFacts {
	facts, err := decodeExpectedFacts(ex.ExpectedFacts)
	if err != nil {
		s.logger.Warn("dataset service: export left out unreadable expected facts", "datasetID", datasetID, "exampleID", ex.ID)
	}
	return facts
}

func (s *DatasetService) importExamplesCSV(ctx context.Context, datasetID, csvText string) (*ImportExamplesResult, error) {
	r := csv.NewReader(strings.NewReader(csvText))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, &ValidationError{
			Field:   "csv",
			Message: "CSV is empty or has no header row.",
		}
	}

	cols := newCSVColumns(header)
	if !cols.hasExampleColumn() {
		return nil, &ValidationError{
			Field:   "csv",
			Message: "CSV is missing required columns: [prompt]",
		}
	}

	batch, err := s.newImportBatch(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	for rowNumber := 2; ; rowNumber++ {
		record, err := r.Read()
		if err != nil {
			break
		}
		row, rowErr := cols.row(record)
		if rowErr != nil {
			return nil, &ValidationError{Field: "csv", Message: fmt.Sprintf("row %d: %v", rowNumber, rowErr)}
		}
		batch.add(row)
	}
	return s.createImportedExamples(ctx, datasetID, batch, logMsgCSVImport)
}

func lenientString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func (s *DatasetService) importExamplesJSON(ctx context.Context, datasetID, body string) (*ImportExamplesResult, error) {
	var parsed []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		var anyVal any
		if jerr := json.Unmarshal([]byte(body), &anyVal); jerr == nil {
			return nil, &ValidationError{
				Field:   "json",
				Message: "JSON body must be an array of example objects.",
			}
		}
		return nil, &ValidationError{
			Field:   "json",
			Message: fmt.Sprintf("Invalid JSON: %s", err.Error()),
		}
	}

	batch, err := s.newImportBatch(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	for i, row := range parsed {
		inputData, inputErr := inputDataOf(row)
		if inputErr != nil {
			return nil, &ValidationError{Field: "json", Message: fmt.Sprintf("example %d: %v", i, inputErr)}
		}
		var expected *string
		expectedRaw, ok := row["expectedResponse"]
		if !ok {
			expectedRaw = row[legacyExpectedResponseKey]
		}
		if s := lenientString(expectedRaw); s != "" {
			expected = &s
		}
		expectedPath, expectedFacts, objectErr := importJSONRow{
			ExpectedPath: row["expectedPath"], LegacyExpectedPath: row[expectedPathColumn],
			ExpectedFacts: row["expectedFacts"], LegacyExpectedFacts: row[expectedFactsColumn],
		}.expectations()
		if objectErr != nil {
			return nil, &ValidationError{Field: "json", Message: fmt.Sprintf("example %d %v", i, objectErr)}
		}
		batch.add(importRow{
			prompt: strings.TrimSpace(lenientString(row["prompt"])), expected: expected, inputData: inputData,
			expectedPath: expectedPath, expectedFacts: expectedFacts,
		})
	}
	return s.createImportedExamples(ctx, datasetID, batch, logMsgJSONImport)
}

func (req UpdateDatasetRequest) Problems() map[string][]string {
	fe := fieldErrors{}
	if req.Name != nil {
		fe.addValidation(validateDatasetName(strings.TrimSpace(*req.Name)))
	}
	if req.Description != nil {
		fe.addValidation(validateDatasetDescription(*req.Description))
	}
	return fe
}

func validateDatasetName(name string) error {
	if name == "" {
		return &ValidationError{Field: "name", Message: "name is required"}
	}
	if len(name) > maxDatasetNameLen {
		return &ValidationError{Field: "name", Message: fmt.Sprintf("name must be at most %d characters", maxDatasetNameLen)}
	}
	return nil
}

func validateDatasetDescription(desc string) error {
	if len(desc) > maxDatasetDescriptionLen {
		return &ValidationError{Field: "description", Message: fmt.Sprintf("description must be at most %d characters", maxDatasetDescriptionLen)}
	}
	return nil
}

func datasetResponseFromCounts(d *store.EvalDatasetWithCounts) DatasetResponse {
	return DatasetResponse{
		ID:                    d.ID,
		Name:                  d.Name,
		Description:           d.Description,
		IsDefault:             d.IsDefault,
		ExampleCount:          d.ExampleCount,
		ExperimentCount:       d.ExperimentCount,
		InputDataExampleCount: d.InputDataExampleCount,
		CreatedBy:             d.CreatedBy,
		UpdatedBy:             d.UpdatedBy,
		CreatedTime:           Timestamp(d.CreatedTime),
		UpdatedTime:           Timestamp(d.UpdatedTime),
	}
}

func mapDatasetNotFound(err error, id string) error {
	if errors.Is(err, store.ErrEvalNotFound) {
		return &NotFoundError{Resource: "EvalDataset", ID: id}
	}
	return fmt.Errorf("dataset lookup: %w", err)
}
