// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	logMsgCSVImport  = "eval: csv import"
	logMsgJSONImport = "eval: json import"
)

type ImportFormat string

const (
	ImportFormatCSV  ImportFormat = "csv"
	ImportFormatJSON ImportFormat = "json"
)

const importFileField = "file"

type importRow struct {
	prompt        string
	expected      *string
	inputData     *string
	expectedPath  *string
	expectedFacts *string
}

type csvColumns struct {
	prompt, expected, input, path, facts int
}

func newCSVColumns(header []string) csvColumns {
	cols := csvColumns{prompt: -1, expected: -1, input: -1, path: -1, facts: -1}
	for i, name := range header {
		switch strings.TrimSpace(name) {
		case "prompt":
			cols.prompt = i
		case "expected_response":
			cols.expected = i
		case inputDataColumn:
			cols.input = i
		case expectedPathColumn:
			cols.path = i
		case expectedFactsColumn:
			cols.facts = i
		}
	}
	return cols
}

func (c csvColumns) hasExampleColumn() bool {
	return c.prompt >= 0 || c.input >= 0
}

func (c csvColumns) row(record []string) (importRow, error) {
	row := importRow{prompt: strings.TrimSpace(cell(record, c.prompt))}
	if val := strings.TrimSpace(cell(record, c.expected)); val != "" {
		row.expected = &val
	}
	inputData, err := parseInputDataCell(cell(record, c.input))
	if err != nil {
		return importRow{}, err
	}
	row.inputData = inputData
	expectedPath, err := encodeExpectedPathCell(cell(record, c.path))
	if err != nil {
		return importRow{}, err
	}
	row.expectedPath = expectedPath
	expectedFacts, err := encodeExpectedFactsCell(cell(record, c.facts))
	if err != nil {
		return importRow{}, err
	}
	row.expectedFacts = expectedFacts
	return row, nil
}

func cell(record []string, i int) string {
	if i < 0 || i >= len(record) {
		return ""
	}
	return record[i]
}

type importBatch struct {
	limit                          int
	rows                           []importRow
	skipped, dropped               int
	withInput, withPath, withFacts bool
}

func (b *importBatch) add(row importRow) {
	b.withInput = b.withInput || row.inputData != nil
	b.withPath = b.withPath || row.expectedPath != nil
	b.withFacts = b.withFacts || row.expectedFacts != nil
	switch {
	case row.prompt == "" && row.inputData == nil:
		b.skipped++
	case len(b.rows) >= b.limit:
		b.dropped++
	default:
		b.rows = append(b.rows, row)
	}
}

func (s *DatasetService) newImportBatch(ctx context.Context, datasetID string) (*importBatch, error) {
	currentCount, err := s.examples.CountByDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("count examples: %w", err)
	}
	return &importBatch{limit: max(s.maxExamplesCap()-currentCount, 0)}, nil
}

func (s *DatasetService) createImportedExamples(ctx context.Context, datasetID string, batch *importBatch, logMsg string) (*ImportExamplesResult, error) {
	if err := gatedFieldsRefusal(s.registry, batch.withInput, batch.withPath, batch.withFacts); err != nil {
		return nil, err
	}
	nextSeq, err := s.examples.NextSequenceNumber(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("next sequence: %w", err)
	}
	toCreate := make([]*store.EvalExample, len(batch.rows))
	for i, row := range batch.rows {
		toCreate[i] = &store.EvalExample{
			DatasetID:        datasetID,
			SequenceNumber:   nextSeq + i,
			Prompt:           row.prompt,
			ExpectedResponse: row.expected,
			InputData:        row.inputData,
			ExpectedPath:     row.expectedPath,
			ExpectedFacts:    row.expectedFacts,
		}
	}

	if len(toCreate) > 0 {
		if err := s.examples.BulkCreate(ctx, toCreate); err != nil {
			return nil, fmt.Errorf("bulk create examples: %w", err)
		}
	}
	created := len(toCreate)
	s.logger.Info(logMsg,
		"datasetID", datasetID, "created", created, "skipped", batch.skipped, "droppedOverLimit", batch.dropped)
	return &ImportExamplesResult{Created: created, Skipped: batch.skipped, Dropped: batch.dropped, Limit: s.maxExamplesCap()}, nil
}

func (s *DatasetService) ImportExamplesStrict(ctx context.Context, datasetID string, format ImportFormat, body []byte) (*ImportExamplesResult, error) {
	if _, err := s.datasets.GetByID(ctx, datasetID); err != nil {
		return nil, mapDatasetNotFound(err, datasetID)
	}
	batch, err := s.newImportBatch(ctx, datasetID)
	if err != nil {
		return nil, err
	}
	body = bytes.TrimPrefix(body, utf8BOM)
	var logMsg string
	switch format {
	case ImportFormatJSON:
		err = parseStrictJSONRows(body, batch)
		logMsg = logMsgJSONImport
	case ImportFormatCSV:
		err = parseStrictCSVRows(body, batch)
		logMsg = logMsgCSVImport
	default:
		return nil, fmt.Errorf("import format %q is not csv or json", format)
	}
	if err != nil {
		return nil, err
	}
	return s.createImportedExamples(ctx, datasetID, batch, logMsg)
}

var utf8BOM = []byte("\xef\xbb\xbf")

func importFileError(detail string) error {
	return &ValidationError{Message: detail, Details: map[string][]string{importFileField: {detail}}}
}

func parseStrictCSVRows(body []byte, batch *importBatch) error {
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	switch {
	case errors.Is(err, io.EOF):
		return importFileError("CSV is empty or has no header row")
	case err != nil:
		return importFileError("the header row is not valid CSV: " + csvProblem(err))
	}
	cols := newCSVColumns(header)
	if !cols.hasExampleColumn() {
		return importFileError("CSV is missing the required column prompt")
	}
	for rowNumber := 1; ; rowNumber++ {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return importFileError(fmt.Sprintf("row %d is not valid CSV: %s", rowNumber, csvProblem(err)))
		}
		row, err := cols.row(record)
		if err != nil {
			return importFileError(fmt.Sprintf("row %d: %v", rowNumber, err))
		}
		batch.add(row)
	}
}

func csvProblem(err error) string {
	switch {
	case errors.Is(err, csv.ErrBareQuote):
		return "a quote appears inside a field that is not quoted"
	case errors.Is(err, csv.ErrQuote):
		return "a quoted field is not closed or is followed by other text"
	default:
		return "it cannot be read"
	}
}

type importJSONRow struct {
	SequenceNumber         *int            `json:"sequenceNumber"`
	Prompt                 *string         `json:"prompt"`
	ExpectedResponse       *string         `json:"expectedResponse"`
	LegacySequenceNumber   *int            `json:"sequence_number"`
	LegacyExpectedResponse *string         `json:"expected_response"`
	InputData              json.RawMessage `json:"inputData"`
	LegacyInputData        json.RawMessage `json:"input_data"`
	ExpectedPath           json.RawMessage `json:"expectedPath"`
	LegacyExpectedPath     json.RawMessage `json:"expected_path"`
	ExpectedFacts          json.RawMessage `json:"expectedFacts"`
	LegacyExpectedFacts    json.RawMessage `json:"expected_facts"`
}

const (
	msgImportNotJSON  = "the file is not valid JSON"
	msgImportNotArray = "the file must be a JSON array of example objects"
)

func parseStrictJSONRows(body []byte, batch *importBatch) error {
	if !json.Valid(body) {
		return importFileError(msgImportNotJSON)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if open, _ := dec.Token(); open != json.Delim('[') {
		return importFileError(msgImportNotArray)
	}
	for rowNumber := 1; dec.More(); rowNumber++ {
		var item json.RawMessage
		if err := dec.Decode(&item); err != nil {
			return importFileError(msgImportNotJSON)
		}
		row, err := parseStrictJSONRow(item)
		if err != nil {
			return importFileError(fmt.Sprintf("row %d %s", rowNumber, err))
		}
		batch.add(row)
	}
	return nil
}

func parseStrictJSONRow(item json.RawMessage) (importRow, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(item), []byte("{")) {
		return importRow{}, errors.New("must be an object")
	}
	var parsed importJSONRow
	dec := json.NewDecoder(bytes.NewReader(item))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return importRow{}, describeJSONRowError(err)
	}
	var row importRow
	if parsed.Prompt != nil {
		row.prompt = strings.TrimSpace(*parsed.Prompt)
	}
	expected := cmp.Or(parsed.ExpectedResponse, parsed.LegacyExpectedResponse)
	if expected != nil && *expected != "" {
		row.expected = expected
	}
	rawInput := parsed.InputData
	if len(rawInput) == 0 {
		rawInput = parsed.LegacyInputData
	}
	inputData, err := parseJSONInputData(rawInput)
	if err != nil {
		return importRow{}, err
	}
	row.inputData = inputData
	if row.expectedPath, row.expectedFacts, err = parsed.expectations(); err != nil {
		return importRow{}, err
	}
	return row, nil
}

func (r importJSONRow) expectations() (path, facts *string, err error) {
	if path, err = importedExpectedPath(rawOr(r.ExpectedPath, r.LegacyExpectedPath)); err != nil {
		return nil, nil, fmt.Errorf("has an invalid expectedPath: %w", err)
	}
	if facts, err = importedExpectedFacts(rawOr(r.ExpectedFacts, r.LegacyExpectedFacts)); err != nil {
		return nil, nil, fmt.Errorf("has invalid expectedFacts: %w", err)
	}
	return path, facts, nil
}

func rawOr(primary, fallback json.RawMessage) json.RawMessage {
	if len(primary) == 0 {
		return fallback
	}
	return primary
}

func parseJSONInputData(raw json.RawMessage) (*string, error) {
	stored, err := storedInputData(raw)
	switch {
	case errors.Is(err, errInputDataTooLarge):
		return nil, fmt.Errorf("has an inputData over %d bytes as JSON", maxExampleInputDataBytes)
	case err != nil:
		return nil, errors.New("has an inputData that must be a JSON object or null")
	}
	return stored, nil
}

func describeJSONRowError(err error) error {
	if key, ok := strings.CutPrefix(err.Error(), `json: unknown field "`); ok {
		return fmt.Errorf("has the unknown field %q", strings.TrimSuffix(key, `"`))
	}
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		want := "a string"
		if typeErr.Field == "sequenceNumber" || typeErr.Field == "sequence_number" {
			want = "an integer"
		}
		return fmt.Errorf("has a %s that must be %s or null", typeErr.Field, want)
	}
	return errors.New("is not a valid example")
}
