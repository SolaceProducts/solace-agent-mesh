// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
)

type ModifierDef struct {
	Func     func(data any, value string, mimeType string) (any, error)
	Accepts  []DataFormat
	Produces DataFormat
}

var ModifierRegistry = map[string]ModifierDef{
	"jsonpath":          {applyJSONPath, []DataFormat{FormatJSON}, FormatJSON},
	"select_cols":       {applySelectCols, []DataFormat{FormatListOfDicts}, FormatListOfDicts},
	"filter_rows_eq":    {applyFilterRowsEq, []DataFormat{FormatListOfDicts}, FormatListOfDicts},
	"slice_rows":        {applySliceRows, []DataFormat{FormatListOfDicts}, FormatListOfDicts},
	"slice_lines":       {applySliceLines, []DataFormat{FormatString}, FormatString},
	"grep":              {applyGrep, []DataFormat{FormatString}, FormatString},
	"head":              {applyHead, []DataFormat{FormatString}, FormatString},
	"tail":              {applyTail, []DataFormat{FormatString}, FormatString},
	"select_fields":     {applySelectFields, []DataFormat{FormatListOfDicts}, FormatListOfDicts},
	"apply_to_template": {nil, []DataFormat{FormatJSON, FormatListOfDicts, FormatString}, FormatString},
}

func applyJSONPath(data any, expression string, _ string) (any, error) {
	return agent.ApplyJSONPath(data, expression)
}

func applySelectCols(data any, value string, _ string) (any, error) {
	lod, err := toListOfDicts(data)
	if err != nil {
		return nil, err
	}

	cols := splitAndTrim(value, ",")
	if len(cols) == 0 {
		return nil, fmt.Errorf("select_cols: no columns specified")
	}

	if len(lod) > 0 {
		for _, col := range cols {
			if _, ok := lod[0][col]; !ok {
				return nil, fmt.Errorf("select_cols: column %q not found in data", col)
			}
		}
	}

	result := make([]map[string]any, len(lod))
	for i, row := range lod {
		newRow := make(map[string]any, len(cols))
		for _, col := range cols {
			if v, ok := row[col]; ok {
				newRow[col] = v
			}
		}
		result[i] = newRow
	}
	return result, nil
}

func applyFilterRowsEq(data any, value string, _ string) (any, error) {
	lod, err := toListOfDicts(data)
	if err != nil {
		return nil, err
	}

	colonIdx := strings.Index(value, ":")
	if colonIdx == -1 {
		return nil, fmt.Errorf("filter_rows_eq: invalid format %q, expected 'column:value'", value)
	}
	col := value[:colonIdx]
	matchVal := value[colonIdx+1:]

	if len(lod) > 0 {
		if _, ok := lod[0][col]; !ok {
			return nil, fmt.Errorf("filter_rows_eq: column %q not found in data", col)
		}
	}

	var result []map[string]any
	for _, row := range lod {
		if fmt.Sprintf("%v", row[col]) == matchVal {
			result = append(result, row)
		}
	}
	if result == nil {
		result = []map[string]any{}
	}
	return result, nil
}

func applySliceRows(data any, value string, _ string) (any, error) {
	lod, err := toListOfDicts(data)
	if err != nil {
		return nil, err
	}

	start, end, err := parseSlice(value, len(lod))
	if err != nil {
		return nil, fmt.Errorf("slice_rows: %w", err)
	}

	if start >= len(lod) {
		return []map[string]any{}, nil
	}
	if end > len(lod) {
		end = len(lod)
	}
	if start < 0 {
		start = 0
	}

	return lod[start:end], nil
}

func applySliceLines(data any, value string, _ string) (any, error) {
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("slice_lines: expected string, got %T", data)
	}

	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	start, end, err := parseSlice(value, len(lines))
	if err != nil {
		return nil, fmt.Errorf("slice_lines: %w", err)
	}

	if start >= len(lines) {
		return "", nil
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start < 0 {
		start = 0
	}

	return strings.Join(lines[start:end], ""), nil
}

const maxGrepPatternLen = 1024

func applyGrep(data any, pattern string, _ string) (any, error) {
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("grep: expected string, got %T", data)
	}

	if len(pattern) > maxGrepPatternLen {
		return nil, fmt.Errorf("grep: pattern too long (%d chars, max %d)", len(pattern), maxGrepPatternLen)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("grep: invalid regex %q: %w", pattern, err)
	}

	lines := strings.Split(s, "\n")
	var matched []string
	for _, line := range lines {
		if re.MatchString(line) {
			matched = append(matched, line)
		}
	}

	return strings.Join(matched, "\n"), nil
}

func applyHead(data any, value string, _ string) (any, error) {
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("head: expected string, got %T", data)
	}

	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("head: invalid count %q: %w", value, err)
	}
	if n < 0 {
		return nil, fmt.Errorf("head: negative count %d", n)
	}
	if n == 0 {
		return "", nil
	}

	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if n >= len(lines) {
		return s, nil
	}

	return strings.Join(lines[:n], ""), nil
}

func applyTail(data any, value string, _ string) (any, error) {
	s, ok := data.(string)
	if !ok {
		return nil, fmt.Errorf("tail: expected string, got %T", data)
	}

	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("tail: invalid count %q: %w", value, err)
	}
	if n < 0 {
		return nil, fmt.Errorf("tail: negative count %d", n)
	}
	if n == 0 {
		return "", nil
	}

	lines := strings.SplitAfter(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if n >= len(lines) {
		return s, nil
	}

	return strings.Join(lines[len(lines)-n:], ""), nil
}

func applySelectFields(data any, value string, _ string) (any, error) {
	lod, err := toListOfDicts(data)
	if err != nil {
		return nil, err
	}

	fields := splitAndTrim(value, ",")
	if len(fields) == 0 {
		return nil, fmt.Errorf("select_fields: no fields specified")
	}

	result := make([]map[string]any, len(lod))
	for i, row := range lod {
		newRow := make(map[string]any)
		for _, field := range fields {
			if v, ok := row[field]; ok {
				newRow[field] = v
			}
		}
		result[i] = newRow
	}
	return result, nil
}

func toListOfDicts(data any) ([]map[string]any, error) {
	switch v := data.(type) {
	case []map[string]any:
		return v, nil
	case []any:
		result := make([]map[string]any, len(v))
		for i, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected list of dicts, element %d is %T", i, item)
			}
			result[i] = m
		}
		return result, nil
	default:
		return nil, fmt.Errorf("expected list of dicts, got %T", data)
	}
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseSlice(value string, length int) (int, int, error) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid slice format %q, expected 'start:end'", value)
	}

	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])

	start := 0
	end := length

	if startStr != "" {
		v, err := strconv.Atoi(startStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid start index %q: %w", startStr, err)
		}
		if v < 0 {
			v = length + v
			if v < 0 {
				v = 0
			}
		}
		start = v
	}

	if endStr != "" {
		v, err := strconv.Atoi(endStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid end index %q: %w", endStr, err)
		}
		if v < 0 {
			v = length + v
			if v < 0 {
				v = 0
			}
		}
		end = v
	}

	return start, end, nil
}
