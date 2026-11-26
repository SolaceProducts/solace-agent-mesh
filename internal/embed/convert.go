// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type DataFormat int

const (
	FormatBytes DataFormat = iota
	FormatString
	FormatJSON
	FormatListOfDicts
)

func (f DataFormat) String() string {
	switch f {
	case FormatBytes:
		return "BYTES"
	case FormatString:
		return "STRING"
	case FormatJSON:
		return "JSON_OBJECT"
	case FormatListOfDicts:
		return "LIST_OF_DICTS"
	default:
		return "UNKNOWN"
	}
}

var textBasedMIMETypes = map[string]bool{
	"text/plain":             true,
	"text/markdown":          true,
	"text/html":              true,
	"text/csv":               true,
	"text/yaml":              true,
	"text/xml":               true,
	"text/x-yaml":            true,
	"application/json":       true,
	"application/yaml":       true,
	"application/x-yaml":     true,
	"application/xml":        true,
	"application/javascript": true,
	"application/csv":        true,
	"application/x-python":   true,
	"application/x-sh":       true,
	"application/sql":        true,
	"application/graphql":    true,
	"application/xhtml+xml":  true,
	"application/ld+json":    true,
}

func IsTextBasedMIME(mimeType string) bool {
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	if textBasedMIMETypes[lower] {
		return true
	}
	if strings.HasPrefix(lower, "text/") {
		return true
	}
	return strings.HasSuffix(lower, "+yaml") ||
		strings.HasSuffix(lower, "+json") ||
		strings.HasSuffix(lower, "+xml")
}

var inlineVisionExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
}

func IsImageArtifact(filename, mimeType string) bool {
	if mimeType != "" {
		normalized := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
		if normalized == "image/svg+xml" {
			return false
		}
		if strings.HasPrefix(normalized, "image/") {
			return true
		}
		if normalized != "application/octet-stream" {
			return false
		}
	}

	if filename != "" {
		ext := strings.ToLower(filepath.Ext(filename))
		if inlineVisionExtensions[ext] {
			return true
		}
	}

	return false
}

func ConvertData(data any, from, to DataFormat, mimeType string) (any, DataFormat, error) {
	if from == to {
		return data, to, nil
	}

	switch from {
	case FormatBytes:
		return convertFromBytes(data, to, mimeType)
	case FormatString:
		return convertFromString(data, to, mimeType)
	case FormatJSON:
		return convertFromJSON(data, to)
	case FormatListOfDicts:
		return convertFromListOfDicts(data, to)
	default:
		if to == FormatString {
			return fmt.Sprintf("%v", data), FormatString, nil
		}
		return data, from, fmt.Errorf("unsupported conversion from unknown format to %s", to)
	}
}

func convertFromBytes(data any, to DataFormat, mimeType string) (any, DataFormat, error) {
	b, ok := data.([]byte)
	if !ok {
		return data, FormatBytes, fmt.Errorf("expected []byte, got %T", data)
	}

	switch to {
	case FormatString:
		if !IsTextBasedMIME(mimeType) && mimeType != "" {
			return data, FormatBytes, fmt.Errorf("cannot decode binary MIME type %q to string", mimeType)
		}
		return string(b), FormatString, nil

	case FormatJSON:
		lower := strings.ToLower(mimeType)
		if !strings.Contains(lower, "json") && mimeType != "" {
			return data, FormatBytes, fmt.Errorf("cannot convert %q bytes to JSON", mimeType)
		}
		var parsed any
		if err := json.Unmarshal(b, &parsed); err != nil {
			return data, FormatBytes, fmt.Errorf("failed to parse JSON from bytes: %w", err)
		}
		return parsed, FormatJSON, nil

	case FormatListOfDicts:
		lower := strings.ToLower(mimeType)
		if !strings.Contains(lower, "csv") {
			return data, FormatBytes, fmt.Errorf("cannot convert %q bytes to list of dicts", mimeType)
		}
		s := string(b)
		lod, err := parseStringToListOfDicts(s, mimeType)
		if err != nil {
			return data, FormatBytes, err
		}
		return lod, FormatListOfDicts, nil

	default:
		return data, FormatBytes, fmt.Errorf("unsupported conversion from BYTES to %s", to)
	}
}

func convertFromString(data any, to DataFormat, mimeType string) (any, DataFormat, error) {
	s, ok := data.(string)
	if !ok {
		return data, FormatString, fmt.Errorf("expected string, got %T", data)
	}

	switch to {
	case FormatBytes:
		return []byte(s), FormatBytes, nil

	case FormatJSON:
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return data, FormatString, fmt.Errorf("failed to parse JSON from string: %w", err)
		}
		return parsed, FormatJSON, nil

	case FormatListOfDicts:
		lod, err := parseStringToListOfDicts(s, mimeType)
		if err != nil {
			return data, FormatString, err
		}
		return lod, FormatListOfDicts, nil

	default:
		return data, FormatString, fmt.Errorf("unsupported conversion from STRING to %s", to)
	}
}

func convertFromJSON(data any, to DataFormat) (any, DataFormat, error) {
	switch to {
	case FormatString:
		b, err := json.Marshal(data)
		if err != nil {
			return data, FormatJSON, fmt.Errorf("failed to serialize JSON: %w", err)
		}
		return string(b), FormatString, nil

	case FormatListOfDicts:
		switch v := data.(type) {
		case []any:
			result := make([]map[string]any, 0, len(v))
			for _, item := range v {
				m, ok := item.(map[string]any)
				if !ok {
					return data, FormatJSON, fmt.Errorf("JSON array contains non-object element: %T", item)
				}
				result = append(result, m)
			}
			return result, FormatListOfDicts, nil
		case []map[string]any:
			return v, FormatListOfDicts, nil
		default:
			return data, FormatJSON, fmt.Errorf("cannot convert %T to list of dicts", data)
		}

	case FormatBytes:
		return data, FormatJSON, fmt.Errorf("unsupported conversion from JSON to BYTES")

	default:
		return data, FormatJSON, fmt.Errorf("unsupported conversion from JSON to %s", to)
	}
}

func convertFromListOfDicts(data any, to DataFormat) (any, DataFormat, error) {
	lod, ok := data.([]map[string]any)
	if !ok {
		return data, FormatListOfDicts, fmt.Errorf("expected []map[string]any, got %T", data)
	}

	switch to {
	case FormatString:
		s, err := listOfDictsToCSV(lod)
		if err != nil {
			return data, FormatListOfDicts, err
		}
		return s, FormatString, nil

	case FormatJSON:
		result := make([]any, len(lod))
		for i, m := range lod {
			result[i] = m
		}
		return result, FormatJSON, nil

	case FormatBytes:
		return data, FormatListOfDicts, fmt.Errorf("unsupported conversion from LIST_OF_DICTS to BYTES")

	default:
		return data, FormatListOfDicts, fmt.Errorf("unsupported conversion from LIST_OF_DICTS to %s", to)
	}
}

func parseStringToListOfDicts(s, mimeType string) ([]map[string]any, error) {
	lower := strings.ToLower(mimeType)

	if strings.Contains(lower, "json") || mimeType == "" {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return jsonToListOfDicts(parsed)
		}
		if strings.Contains(lower, "json") {
			return nil, fmt.Errorf("failed to parse as JSON list of dicts")
		}
	}

	if strings.Contains(lower, "csv") || mimeType == "" {
		lod, err := csvToListOfDicts(s)
		if err == nil {
			return lod, nil
		}
		if strings.Contains(lower, "csv") {
			return nil, fmt.Errorf("failed to parse CSV: %w", err)
		}
	}

	if strings.Contains(lower, "yaml") || strings.Contains(lower, "yml") {
		var parsed any
		if err := yaml.Unmarshal([]byte(s), &parsed); err == nil {
			return yamlToListOfDicts(parsed)
		}
		return nil, fmt.Errorf("failed to parse YAML to list of dicts")
	}

	return nil, fmt.Errorf("unsupported MIME type %q for list-of-dicts conversion", mimeType)
}

func jsonToListOfDicts(parsed any) ([]map[string]any, error) {
	switch v := parsed.(type) {
	case []any:
		result := make([]map[string]any, 0, len(v))
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("JSON array contains non-object element: %T", item)
			}
			result = append(result, m)
		}
		return result, nil
	case map[string]any:
		return []map[string]any{v}, nil
	default:
		return nil, fmt.Errorf("JSON is not an array or object: %T", parsed)
	}
}

func yamlToListOfDicts(parsed any) ([]map[string]any, error) {
	switch v := parsed.(type) {
	case []any:
		result := make([]map[string]any, 0, len(v))
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("YAML array contains non-object element: %T", item)
			}
			result = append(result, m)
		}
		return result, nil
	case map[string]any:
		return []map[string]any{v}, nil
	default:
		return nil, fmt.Errorf("YAML is not an array or object: %T", parsed)
	}
}

func csvToListOfDicts(s string) ([]map[string]any, error) {
	reader := csv.NewReader(strings.NewReader(s))
	reader.FieldsPerRecord = -1

	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV headers: %w", err)
	}

	var result []map[string]any
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read CSV row: %w", err)
		}
		row := make(map[string]any, len(headers))
		for i, h := range headers {
			if i < len(record) {
				row[h] = record[i]
			} else {
				row[h] = ""
			}
		}
		result = append(result, row)
	}

	if result == nil {
		result = []map[string]any{}
	}
	return result, nil
}

func listOfDictsToCSV(lod []map[string]any) (string, error) {
	if len(lod) == 0 {
		return "", nil
	}

	headerSet := make(map[string]bool)
	for _, row := range lod {
		for k := range row {
			headerSet[k] = true
		}
	}
	headers := make([]string, 0, len(headerSet))
	for k := range headerSet {
		headers = append(headers, k)
	}
	slices.Sort(headers)

	var buf strings.Builder
	w := csv.NewWriter(&buf)

	if err := w.Write(headers); err != nil {
		return "", err
	}

	for _, row := range lod {
		record := make([]string, len(headers))
		for i, h := range headers {
			if v, ok := row[h]; ok {
				record[i] = fmt.Sprintf("%v", v)
			}
		}
		if err := w.Write(record); err != nil {
			return "", err
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}

	result := buf.String()
	result = strings.TrimRight(result, "\r\n")
	return result, nil
}
