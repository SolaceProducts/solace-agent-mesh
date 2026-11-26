// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"encoding/json"
	"strings"
)

func CompileInputSchema(schema map[string]any) (*Schema, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	return Compile(schema)
}

func TextRunInput(text string, schema *Schema) map[string]any {
	wrapped := map[string]any{"text": text}
	if schema == nil {
		return wrapped
	}
	doc, ok := parseJSONObject(text)
	if !ok {
		return wrapped
	}
	if schema.declaresEveryKey(doc) && len(schema.Validate(doc)) == 0 {
		return doc
	}
	if len(schema.Validate(wrapped)) == 0 {
		return wrapped
	}
	return doc
}

func (s *Schema) declaresEveryKey(doc map[string]any) bool {
	if len(doc) == 0 || len(s.properties) == 0 {
		return false
	}
	for key := range doc {
		if _, declared := s.properties[key]; !declared {
			return false
		}
	}
	return true
}

func parseJSONObject(s string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return nil, false
	}
	return doc, true
}
