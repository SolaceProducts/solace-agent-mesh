// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"gopkg.in/yaml.v3"
)

const (
	DefaultSchemaMaxDepth = 4
	DefaultSchemaMaxKeys  = 20
)

func InferSchema(content []byte, mimeType string, maxDepth, maxKeys int) map[string]any {
	if maxDepth <= 0 {
		maxDepth = DefaultSchemaMaxDepth
	}
	if maxKeys <= 0 {
		maxKeys = DefaultSchemaMaxKeys
	}

	schema := map[string]any{
		"type":     mimeType,
		"inferred": false,
		"error":    nil,
	}

	switch {
	case IsCSVMIME(mimeType):
		inferCSVSchema(content, schema)
	case IsJSONMIME(mimeType):
		inferJSONSchema(content, schema, maxDepth, maxKeys)
	case IsYAMLMIME(mimeType):
		inferYAMLSchema(content, schema, maxDepth, maxKeys)
	}

	if schema["error"] == nil {
		delete(schema, "error")
	}

	return schema
}

func inferCSVSchema(content []byte, schema map[string]any) {
	reader := csv.NewReader(bytes.NewReader(content))
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil {
		schema["error"] = fmt.Sprintf("CSV header parsing failed: %s", err.Error())
		return
	}
	schema["columns"] = headers
	schema["inferred"] = true

	colIndices := make(map[string]int, len(headers))
	for i, h := range headers {
		colIndices[h] = i
	}
	schema["column_indices"] = colIndices
}

func inferJSONSchema(content []byte, schema map[string]any, maxDepth, maxKeys int) {
	var data any
	if err := json.Unmarshal(content, &data); err != nil {
		schema["error"] = fmt.Sprintf("JSON structure inference failed: %s", err.Error())
		return
	}
	schema["structure"] = inspectStructure(data, maxDepth, maxKeys)
	schema["inferred"] = true
}

func inferYAMLSchema(content []byte, schema map[string]any, maxDepth, maxKeys int) {
	var data any
	if err := yaml.Unmarshal(content, &data); err != nil {
		schema["error"] = fmt.Sprintf("YAML structure inference failed: %s", err.Error())
		return
	}
	data = normalizeYAML(data)
	schema["structure"] = inspectStructure(data, maxDepth, maxKeys)
	schema["inferred"] = true
}

func inspectStructure(v any, depth, maxKeys int) any {
	if v == nil {
		return "NoneType"
	}

	if depth <= 0 {
		return pythonTypeName(v)
	}

	switch val := v.(type) {
	case string:
		return "str"
	case bool:
		return "bool"
	case float64:
		if val == math.Trunc(val) && !math.IsInf(val, 0) && !math.IsNaN(val) {
			return "int"
		}
		return "float"
	case int:
		return "int"
	case int64:
		return "int"
	case json.Number:
		if _, err := val.Int64(); err == nil {
			return "int"
		}
		return "float"
	case []any:
		if len(val) == 0 {
			return []any{"list"}
		}
		return []any{inspectStructure(val[0], depth-1, maxKeys)}
	case map[string]any:
		return inspectMap(val, depth, maxKeys)
	default:
		return pythonTypeName(v)
	}
}

func inspectMap(m map[string]any, depth, maxKeys int) map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	result := make(map[string]any, min(len(keys), maxKeys)+1)
	for i, k := range keys {
		if i >= maxKeys {
			result["..."] = fmt.Sprintf("%d more keys", len(keys)-maxKeys)
			break
		}
		result[k] = inspectStructure(m[k], depth-1, maxKeys)
	}
	return result
}

func pythonTypeName(v any) string {
	if v == nil {
		return "NoneType"
	}
	switch v.(type) {
	case string:
		return "str"
	case bool:
		return "bool"
	case float64:
		return "float"
	case int, int64:
		return "int"
	case json.Number:
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	default:
		return "str"
	}
}

func normalizeYAML(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			val[k] = normalizeYAML(child)
		}
		return val
	case map[any]any:
		result := make(map[string]any, len(val))
		for k, child := range val {
			result[fmt.Sprintf("%v", k)] = normalizeYAML(child)
		}
		return result
	case []any:
		for i, child := range val {
			val[i] = normalizeYAML(child)
		}
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case uint64:
		return float64(val)
	default:
		return v
	}
}
