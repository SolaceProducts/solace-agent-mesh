// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

const maxExamplesFileBytes = 32 * 1024 * 1024

type ExampleSpec struct {
	SequenceNumber   int
	Prompt           string
	ExpectedResponse *string
	InputData        json.RawMessage
	ExpectedPath     *evalplatform.ExpectedPath
	ExpectedFacts    *evalplatform.ExpectedFacts
}

const inputDataColumn = "input_data"

const expectedPathColumn = "expected_path"

const expectedFactsColumn = "expected_facts"

const rowErrorFormat = "row %d: %w"

func ResolveExamplesFile(spec map[string]any, sourceFile string) ([]ExampleSpec, bool, error) {
	if spec == nil {
		return nil, false, nil
	}
	raw, ok := spec["examples_file"]
	if !ok {
		return nil, false, nil
	}
	delete(spec, "examples_file")

	relPath, ok := raw.(string)
	if !ok {
		return nil, false, fmt.Errorf("examples_file must be a string, got %T", raw)
	}
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return nil, false, fmt.Errorf("examples_file must not be empty")
	}

	full := relPath
	if !filepath.IsAbs(full) {
		full = filepath.Join(filepath.Dir(sourceFile), relPath)
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return nil, false, fmt.Errorf("resolve examples_file %q: %w", relPath, err)
	}
	if info, statErr := os.Stat(abs); statErr == nil && info.Size() > maxExamplesFileBytes {
		return nil, false, fmt.Errorf("examples_file %q is %d bytes, exceeds %d-byte cap",
			relPath, info.Size(), maxExamplesFileBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, false, fmt.Errorf("read examples_file %q: %w", relPath, err)
	}
	rows, err := ParseExamplesCSV(data)
	if err != nil {
		return nil, false, fmt.Errorf("parse examples_file %q: %w", relPath, err)
	}
	return rows, true, nil
}

func ParseExamplesCSV(data []byte) ([]ExampleSpec, error) {
	r := csv.NewReader(strings.NewReader(string(data)))
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("examples CSV is empty (must include a header row)")
		}
		return nil, fmt.Errorf("read header: %w", err)
	}
	cols, err := readExampleColumns(header)
	if err != nil {
		return nil, err
	}

	out := make([]ExampleSpec, 0, 16)
	seen := make(map[int]int, 16)
	rowNum := 1
	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf(rowErrorFormat, rowNum+1, err)
		}
		rowNum++
		if isBlankRow(row) {
			continue
		}
		spec, err := parseExampleRow(row, rowNum, cols)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[spec.SequenceNumber]; dup {
			return nil, fmt.Errorf("row %d: duplicate sequence_number %d (also on row %d)",
				rowNum, spec.SequenceNumber, prev)
		}
		seen[spec.SequenceNumber] = rowNum
		out = append(out, spec)
	}
	return out, nil
}

type exampleColumns struct {
	seq, prompt, expected, input, path, facts int
}

func readExampleColumns(header []string) (exampleColumns, error) {
	colIndex := make(map[string]int, len(header))
	for i, h := range header {
		colIndex[strings.TrimSpace(h)] = i
	}
	_, hasPrompt := colIndex["prompt"]
	_, hasInput := colIndex[inputDataColumn]
	if !hasPrompt && !hasInput {
		return exampleColumns{}, fmt.Errorf("examples CSV missing required column %q", "prompt")
	}
	if _, ok := colIndex["sequence_number"]; !ok {
		return exampleColumns{}, fmt.Errorf("examples CSV missing required column %q", "sequence_number")
	}
	return exampleColumns{
		seq:      colIndex["sequence_number"],
		prompt:   columnOrAbsent(colIndex, "prompt"),
		expected: columnOrAbsent(colIndex, "expected_response"),
		input:    columnOrAbsent(colIndex, inputDataColumn),
		path:     columnOrAbsent(colIndex, expectedPathColumn),
		facts:    columnOrAbsent(colIndex, expectedFactsColumn),
	}, nil
}

func columnOrAbsent(colIndex map[string]int, name string) int {
	if idx, ok := colIndex[name]; ok {
		return idx
	}
	return -1
}

func cellAt(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return row[idx]
}

func parseExampleRow(row []string, rowNum int, cols exampleColumns) (ExampleSpec, error) {
	seqRaw := strings.TrimSpace(cellAt(row, cols.seq))
	if seqRaw == "" {
		return ExampleSpec{}, fmt.Errorf("row %d: sequence_number is required", rowNum)
	}
	seq, err := strconv.Atoi(seqRaw)
	if err != nil || seq <= 0 {
		return ExampleSpec{}, fmt.Errorf("row %d: sequence_number must be a positive integer (got %q)", rowNum, seqRaw)
	}
	prompt := cellAt(row, cols.prompt)
	input, err := parseInputDataCell(cellAt(row, cols.input))
	if err != nil {
		return ExampleSpec{}, fmt.Errorf(rowErrorFormat, rowNum, err)
	}
	if strings.TrimSpace(prompt) == "" && input == nil {
		return ExampleSpec{}, fmt.Errorf("row %d: prompt is required unless the row has %s", rowNum, inputDataColumn)
	}
	var expected *string
	if v := cellAt(row, cols.expected); strings.TrimSpace(v) != "" {
		expected = &v
	}
	spec := ExampleSpec{SequenceNumber: seq, Prompt: prompt, ExpectedResponse: expected, InputData: input}
	if err := readExpectations(&spec, row, cols); err != nil {
		return ExampleSpec{}, fmt.Errorf(rowErrorFormat, rowNum, err)
	}
	return spec, nil
}

func readExpectations(spec *ExampleSpec, row []string, cols exampleColumns) error {
	var err error
	if spec.ExpectedPath, err = evalplatform.ParseExpectedPathCell(cellAt(row, cols.path)); err != nil {
		return err
	}
	spec.ExpectedFacts, err = evalplatform.ParseExpectedFactsCell(cellAt(row, cols.facts))
	return err
}

func parseInputDataCell(cell string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(cell)
	if trimmed == "" {
		return nil, nil
	}
	canonical, err := evalplatform.CanonicalInputData([]byte(trimmed))
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s %w", inputDataColumn, err)
	case canonical == nil:
		return nil, fmt.Errorf("%s must be a JSON object", inputDataColumn)
	}
	return canonical, nil
}

type optionalColumn struct {
	name string
	has  func(ExampleSpec) bool
	cell func(ExampleSpec) (string, error)
}

var optionalColumns = []optionalColumn{
	{inputDataColumn,
		func(r ExampleSpec) bool { return r.InputData != nil },
		func(r ExampleSpec) (string, error) { return string(r.InputData), nil }},
	{expectedPathColumn,
		func(r ExampleSpec) bool { return r.ExpectedPath != nil },
		func(r ExampleSpec) (string, error) { return jsonCell(r.ExpectedPath) }},
	{expectedFactsColumn,
		func(r ExampleSpec) bool { return r.ExpectedFacts != nil },
		func(r ExampleSpec) (string, error) { return jsonCell(r.ExpectedFacts) }},
}

func exampleRecord(r ExampleSpec, columns []optionalColumn) ([]string, error) {
	expected := ""
	if r.ExpectedResponse != nil {
		expected = *r.ExpectedResponse
	}
	record := []string{strconv.Itoa(r.SequenceNumber), r.Prompt, expected}
	for _, col := range columns {
		cell, err := col.cell(r)
		if err != nil {
			return nil, err
		}
		record = append(record, cell)
	}
	return record, nil
}

func jsonCell[T any](v *T) (string, error) {
	if v == nil {
		return "", nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func isBlankRow(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func EmitExamplesCSV(rows []ExampleSpec) ([]byte, error) {
	header := []string{"sequence_number", "prompt", "expected_response"}
	var columns []optionalColumn
	for _, col := range optionalColumns {
		if slices.ContainsFunc(rows, col.has) {
			columns = append(columns, col)
			header = append(header, col.name)
		}
	}
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return nil, fmt.Errorf("write csv header: %w", err)
	}
	for _, r := range rows {
		record, err := exampleRecord(r, columns)
		if err != nil {
			return nil, fmt.Errorf("write csv row %d: %w", r.SequenceNumber, err)
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("write csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("csv writer: %w", err)
	}
	return []byte(buf.String()), nil
}
