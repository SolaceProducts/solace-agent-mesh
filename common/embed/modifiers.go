// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ModifierFunc func(data any, format DataFormat, param string) (any, DataFormat, error)

var modifierRegistry = map[string]struct {
	Accepts  []DataFormat
	Produces DataFormat
	Fn       ModifierFunc
}{
	"jsonpath":       {Accepts: []DataFormat{FormatJSONObject}, Produces: FormatJSONObject, Fn: modJSONPath},
	"head":           {Accepts: []DataFormat{FormatString}, Produces: FormatString, Fn: modHead},
	"tail":           {Accepts: []DataFormat{FormatString}, Produces: FormatString, Fn: modTail},
	"grep":           {Accepts: []DataFormat{FormatString}, Produces: FormatString, Fn: modGrep},
	"slice_lines":    {Accepts: []DataFormat{FormatString}, Produces: FormatString, Fn: modSliceLines},
	"select_cols":    {Accepts: []DataFormat{FormatListOfDicts}, Produces: FormatListOfDicts, Fn: modSelectCols},
	"select_fields":  {Accepts: []DataFormat{FormatListOfDicts}, Produces: FormatListOfDicts, Fn: modSelectFields},
	"filter_rows_eq": {Accepts: []DataFormat{FormatListOfDicts}, Produces: FormatListOfDicts, Fn: modFilterRowsEq},
	"slice_rows":     {Accepts: []DataFormat{FormatListOfDicts}, Produces: FormatListOfDicts, Fn: modSliceRows},
}

func applyModifier(data any, format DataFormat, step ModifierStep) (any, DataFormat, error) {
	reg, ok := modifierRegistry[step.Name]
	if !ok {
		return nil, format, fmt.Errorf("unknown modifier: %s", step.Name)
	}

	var err error
	accepted := false
	for _, af := range reg.Accepts {
		if format == af {
			accepted = true
			break
		}
	}
	if !accepted {
		data, format, err = convertData(data, format, reg.Accepts[0])
		if err != nil {
			return nil, format, fmt.Errorf("Failed to convert data for modifier '%s': %w", step.Name, err) //nolint:staticcheck // ST1005: user-facing message must match Python wire format
		}
	}

	result, outFmt, err := reg.Fn(data, format, step.Param)
	if err != nil {
		return nil, format, fmt.Errorf("modifier %s: %w", step.Name, err)
	}
	return result, outFmt, nil
}

func modHead(data any, _ DataFormat, param string) (any, DataFormat, error) {
	s, ok := data.(string)
	if !ok {
		return nil, FormatString, fmt.Errorf("expected string, got %T", data)
	}
	n, err := strconv.Atoi(param)
	if err != nil || n <= 0 {
		return nil, FormatString, fmt.Errorf("invalid head count: %s", param)
	}
	lines := strings.Split(s, "\n")
	if n > len(lines) {
		n = len(lines)
	}
	return strings.Join(lines[:n], "\n"), FormatString, nil
}

func modTail(data any, _ DataFormat, param string) (any, DataFormat, error) {
	s, ok := data.(string)
	if !ok {
		return nil, FormatString, fmt.Errorf("expected string, got %T", data)
	}
	n, err := strconv.Atoi(param)
	if err != nil || n <= 0 {
		return nil, FormatString, fmt.Errorf("invalid tail count: %s", param)
	}
	lines := strings.Split(s, "\n")
	if n > len(lines) {
		n = len(lines)
	}
	return strings.Join(lines[len(lines)-n:], "\n"), FormatString, nil
}

func modGrep(data any, _ DataFormat, param string) (any, DataFormat, error) {
	s, ok := data.(string)
	if !ok {
		return nil, FormatString, fmt.Errorf("expected string, got %T", data)
	}
	re, err := regexp.Compile(param)
	if err != nil {
		return nil, FormatString, fmt.Errorf("invalid regex: %w", err)
	}
	lines := strings.Split(s, "\n")
	var matched []string
	for _, line := range lines {
		if re.MatchString(line) {
			matched = append(matched, line)
		}
	}
	return strings.Join(matched, "\n"), FormatString, nil
}

func modSliceLines(data any, _ DataFormat, param string) (any, DataFormat, error) {
	s, ok := data.(string)
	if !ok {
		return nil, FormatString, fmt.Errorf("expected string, got %T", data)
	}
	parts := strings.SplitN(param, ":", 2)
	if len(parts) != 2 {
		return nil, FormatString, fmt.Errorf("expected start:end, got %s", param)
	}
	start, err1 := strconv.Atoi(parts[0])
	end, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return nil, FormatString, fmt.Errorf("invalid slice range: %s", param)
	}
	lines := strings.Split(s, "\n")
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return "", FormatString, nil
	}
	return strings.Join(lines[start:end], "\n"), FormatString, nil
}

func modJSONPath(data any, _ DataFormat, param string) (any, DataFormat, error) {
	path := param
	if !strings.HasPrefix(path, "$") {
		return nil, FormatJSONObject, fmt.Errorf("jsonpath must start with $")
	}
	path = path[1:]

	results := []any{data}

	for path != "" {
		if strings.HasPrefix(path, ".") {
			path = path[1:]
			end := strings.IndexAny(path, ".[")
			if end < 0 {
				end = len(path)
			}
			key := path[:end]
			path = path[end:]

			var next []any
			for _, r := range results {
				m, ok := r.(map[string]any)
				if !ok {
					continue
				}
				val, exists := m[key]
				if exists {
					next = append(next, val)
				}
			}
			results = next
		} else if strings.HasPrefix(path, "[") {
			closeBracket := strings.Index(path, "]")
			if closeBracket < 0 {
				return nil, FormatJSONObject, fmt.Errorf("unclosed bracket in jsonpath")
			}
			idxStr := path[1:closeBracket]
			path = path[closeBracket+1:]

			if idxStr == "*" {
				var next []any
				for _, r := range results {
					arr, ok := r.([]any)
					if !ok {
						continue
					}
					next = append(next, arr...)
				}
				results = next
			} else {
				idx, err := strconv.Atoi(idxStr)
				if err != nil {
					return nil, FormatJSONObject, fmt.Errorf("invalid array index: %s", idxStr)
				}
				var next []any
				for _, r := range results {
					arr, ok := r.([]any)
					if !ok {
						continue
					}
					if idx >= 0 && idx < len(arr) {
						next = append(next, arr[idx])
					}
				}
				results = next
			}
		} else {
			return nil, FormatJSONObject, fmt.Errorf("unexpected jsonpath segment: %s", path)
		}
	}

	return jsonpathResultsAsPythonList(results), FormatJSONObject, nil
}

func jsonpathResultsAsPythonList(results []any) []any {
	if results == nil {
		results = []any{}
	}
	return results
}

func modSelectCols(data any, _ DataFormat, param string) (any, DataFormat, error) {
	rows, ok := data.([]map[string]any)
	if !ok {
		return nil, FormatListOfDicts, fmt.Errorf("expected []map[string]any, got %T", data)
	}
	cols := strings.Split(param, ",")
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		newRow := make(map[string]any, len(cols))
		for _, col := range cols {
			if v, ok := row[col]; ok {
				newRow[col] = v
			}
		}
		newRow["_column_order"] = cols
		result = append(result, newRow)
	}
	return result, FormatListOfDicts, nil
}

func modSelectFields(data any, _ DataFormat, param string) (any, DataFormat, error) {
	return modSelectCols(data, FormatListOfDicts, param)
}

func modFilterRowsEq(data any, _ DataFormat, param string) (any, DataFormat, error) {
	rows, ok := data.([]map[string]any)
	if !ok {
		return nil, FormatListOfDicts, fmt.Errorf("expected []map[string]any, got %T", data)
	}
	parts := strings.SplitN(param, ":", 2)
	if len(parts) != 2 {
		return nil, FormatListOfDicts, fmt.Errorf("expected col:value, got %s", param)
	}
	col := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])

	var result []map[string]any
	for _, row := range rows {
		if fmt.Sprint(row[col]) == val {
			result = append(result, row)
		}
	}
	return result, FormatListOfDicts, nil
}

func modSliceRows(data any, _ DataFormat, param string) (any, DataFormat, error) {
	rows, ok := data.([]map[string]any)
	if !ok {
		return nil, FormatListOfDicts, fmt.Errorf("expected []map[string]any, got %T", data)
	}
	parts := strings.SplitN(param, ":", 2)
	if len(parts) != 2 {
		return nil, FormatListOfDicts, fmt.Errorf("expected start:end, got %s", param)
	}
	start, err1 := strconv.Atoi(parts[0])
	end, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return nil, FormatListOfDicts, fmt.Errorf("invalid slice range: %s", param)
	}
	if start < 0 {
		start = 0
	}
	if end > len(rows) {
		end = len(rows)
	}
	if start >= end {
		return []map[string]any{}, FormatListOfDicts, nil
	}
	return rows[start:end], FormatListOfDicts, nil
}

func convertData(data any, from, to DataFormat) (any, DataFormat, error) {
	if from == to {
		return data, to, nil
	}

	switch {
	case from == FormatBytes && to == FormatString:
		b, ok := data.([]byte)
		if !ok {
			return nil, from, fmt.Errorf("expected []byte, got %T", data)
		}
		return string(b), FormatString, nil

	case from == FormatBytes && to == FormatJSONObject:
		b, ok := data.([]byte)
		if !ok {
			return nil, from, fmt.Errorf("expected []byte, got %T", data)
		}
		var obj any
		if err := json.Unmarshal(b, &obj); err != nil {
			return nil, from, fmt.Errorf("parse JSON: %w", err)
		}
		return obj, FormatJSONObject, nil

	case from == FormatString && to == FormatJSONObject:
		s, ok := data.(string)
		if !ok {
			return nil, from, fmt.Errorf("expected string, got %T", data)
		}
		var obj any
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			return nil, from, fmt.Errorf("Failed to parse string as JSON") //nolint:staticcheck // ST1005: user-facing message must match Python wire format
		}
		return obj, FormatJSONObject, nil

	case from == FormatString && to == FormatListOfDicts:
		s, ok := data.(string)
		if !ok {
			return nil, from, fmt.Errorf("expected string, got %T", data)
		}
		reader := csv.NewReader(strings.NewReader(s))
		records, err := reader.ReadAll()
		if err != nil || len(records) < 2 {
			var list []map[string]any
			if jsonErr := json.Unmarshal([]byte(s), &list); jsonErr == nil {
				return list, FormatListOfDicts, nil
			}
			return nil, from, fmt.Errorf("cannot convert string to list of dicts")
		}
		headers := records[0]
		result := make([]map[string]any, 0, len(records)-1)
		for _, row := range records[1:] {
			m := make(map[string]any, len(headers)+1)
			for j, h := range headers {
				if j < len(row) {
					m[h] = row[j]
				}
			}
			m["_column_order"] = headers
			result = append(result, m)
		}
		return result, FormatListOfDicts, nil

	case from == FormatJSONObject && to == FormatListOfDicts:
		switch v := data.(type) {
		case []any:
			result := make([]map[string]any, 0, len(v))
			for _, item := range v {
				m, ok := item.(map[string]any)
				if !ok {
					return nil, from, fmt.Errorf("array element is not an object: %T", item)
				}
				result = append(result, m)
			}
			return result, FormatListOfDicts, nil
		case []map[string]any:
			return v, FormatListOfDicts, nil
		default:
			return nil, from, fmt.Errorf("cannot convert %T to list of dicts", data)
		}

	case from == FormatJSONObject && to == FormatString:
		b, err := json.Marshal(data)
		if err != nil {
			return nil, from, fmt.Errorf("marshal JSON: %w", err)
		}
		return string(b), FormatString, nil

	case from == FormatListOfDicts && to == FormatString:
		rows, ok := data.([]map[string]any)
		if !ok {
			return nil, from, fmt.Errorf("expected []map[string]any, got %T", data)
		}
		return listOfDictsToCSV(rows), FormatString, nil

	case from == FormatBytes && to == FormatListOfDicts:
		b, ok := data.([]byte)
		if !ok {
			return nil, from, fmt.Errorf("expected []byte, got %T", data)
		}
		return convertData(string(b), FormatString, FormatListOfDicts)

	default:
		return nil, from, fmt.Errorf("unsupported conversion: %d → %d", from, to)
	}
}

func listOfDictsToCSV(rows []map[string]any) string {
	if len(rows) == 0 {
		return ""
	}

	var headers []string
	if len(rows) > 0 {
		if order, ok := rows[0]["_column_order"].([]string); ok && len(order) > 0 {
			headers = order
		}
	}

	if len(headers) == 0 {
		headerSet := make(map[string]bool)
		for _, row := range rows {
			for k := range row {
				if k != "_column_order" && !headerSet[k] {
					headerSet[k] = true
					headers = append(headers, k)
				}
			}
		}
	}

	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.Write(headers)
	for _, row := range rows {
		record := make([]string, len(headers))
		for i, h := range headers {
			if v, ok := row[h]; ok {
				record[i] = fmt.Sprint(v)
			}
		}
		_ = w.Write(record)
	}
	w.Flush()
	return strings.TrimRight(sb.String(), "\n")
}
