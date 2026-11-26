// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/xuri/excelize/v2"
)

func opFillFromData(f *excelize.File, op Operation, dataSources []sdk.Artifact) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	bounded := op.Range != nil && *op.Range != ""
	if !bounded && (op.Anchor == nil || *op.Anchor == "") {
		return fmt.Errorf("fill_from_data requires 'anchor' (or 'range' for a bounded target)")
	}
	if op.Source == nil || *op.Source == "" {
		return fmt.Errorf("fill_from_data requires 'source' (index or filename of a data_sources artifact)")
	}
	art, err := resolveDataSource(*op.Source, dataSources)
	if err != nil {
		return err
	}
	includeHeader := true
	if op.IncludeHeader != nil {
		includeHeader = *op.IncludeHeader
	}
	format := "auto"
	if op.Format != nil && *op.Format != "" {
		format = *op.Format
	}
	grid, err := parseDataSource(art, format, op.Columns, includeHeader)
	if err != nil {
		return err
	}
	if len(grid) == 0 {
		return fmt.Errorf("data source %q produced no rows", art.Filename)
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	if bounded {
		return writeGridBounded(f, sheet, *op.Range, grid, false)
	}
	return writeGrid(f, sheet, *op.Anchor, grid, false)
}

func resolveDataSource(ref string, dataSources []sdk.Artifact) (*sdk.Artifact, error) {
	if len(dataSources) == 0 {
		return nil, fmt.Errorf("no data_sources provided on the tool call")
	}
	if idx, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil {
		if idx < 0 || idx >= len(dataSources) {
			return nil, fmt.Errorf("data source index %d out of range (have %d)", idx, len(dataSources))
		}
		return &dataSources[idx], nil
	}
	for i := range dataSources {
		if dataSources[i].Filename == ref {
			return &dataSources[i], nil
		}
	}
	return nil, fmt.Errorf("data source %q not found in data_sources", ref)
}

func parseDataSource(art *sdk.Artifact, format string, columns []string, includeHeader bool) ([][]any, error) {
	if format == "auto" {
		format = detectFormat(art)
	}
	switch format {
	case "csv":
		return parseDelimited(art.Content, ',', includeHeader)
	case "tsv":
		return parseDelimited(art.Content, '\t', includeHeader)
	case "json":
		return parseJSON(art.Content, columns, includeHeader)
	default:
		return nil, fmt.Errorf("unsupported fill format %q (expected auto, csv, tsv, or json)", format)
	}
}

func detectFormat(art *sdk.Artifact) string {
	name := strings.ToLower(art.Filename)
	switch {
	case strings.HasSuffix(name, ".json"), strings.Contains(art.MIMEType, "json"):
		return "json"
	case strings.HasSuffix(name, ".tsv"), strings.Contains(art.MIMEType, "tab-separated"):
		return "tsv"
	default:
		return "csv"
	}
}

func parseDelimited(content []byte, comma rune, includeHeader bool) ([][]any, error) {
	r := csv.NewReader(bytes.NewReader(content))
	r.Comma = comma
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing delimited data: %v", err)
	}
	if !includeHeader && len(records) > 0 {
		records = records[1:]
	}
	grid := make([][]any, len(records))
	for i, rec := range records {
		row := make([]any, len(rec))
		for j, cell := range rec {
			row[j] = coerceScalar(cell)
		}
		grid[i] = row
	}
	return grid, nil
}

func parseJSON(content []byte, columns []string, includeHeader bool) ([][]any, error) {
	var arr []any
	if err := json.Unmarshal(content, &arr); err != nil {
		return nil, fmt.Errorf("parsing JSON array: %v", err)
	}
	if len(arr) == 0 {
		return nil, nil
	}
	if _, ok := arr[0].([]any); ok {
		grid := make([][]any, 0, len(arr))
		for _, row := range arr {
			if r, ok := row.([]any); ok {
				grid = append(grid, r)
			}
		}
		return grid, nil
	}
	if _, ok := arr[0].(map[string]any); ok {
		cols := columns
		if len(cols) == 0 {
			cols = unionKeys(arr)
		}
		var grid [][]any
		if includeHeader {
			header := make([]any, len(cols))
			for i, c := range cols {
				header[i] = c
			}
			grid = append(grid, header)
		}
		for _, item := range arr {
			obj, _ := item.(map[string]any)
			row := make([]any, len(cols))
			for i, c := range cols {
				row[i] = scalarCell(obj[c])
			}
			grid = append(grid, row)
		}
		return grid, nil
	}
	return nil, fmt.Errorf("JSON must be an array of arrays or an array of objects")
}

func scalarCell(v any) any {
	switch v.(type) {
	case map[string]any, []any:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	default:
		return v
	}
}

func unionKeys(arr []any) []string {
	seen := map[string]struct{}{}
	var keys []string
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for k := range obj {
			if _, dup := seen[k]; !dup {
				seen[k] = struct{}{}
				keys = append(keys, k)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

var plainNumber = regexp.MustCompile(`^-?(?:0|[1-9]\d*)(?:\.\d+)?$`)

func coerceScalar(s string) any {
	if s == "" {
		return ""
	}
	if plainNumber.MatchString(s) {
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n
		}
	}
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}
