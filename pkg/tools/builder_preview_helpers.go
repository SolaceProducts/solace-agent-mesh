// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

const unknownSignature = "?"

func schemaProperties(raw json.RawMessage) (names []string, required map[string]bool, known bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil, false
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, false
	}
	required = make(map[string]bool, len(doc.Required))
	for _, name := range doc.Required {
		required[name] = true
	}
	return slices.Sorted(maps.Keys(doc.Properties)), required, true
}

func schemaSignature(raw json.RawMessage, marksRequired bool) string {
	names, required, known := schemaProperties(raw)
	if !known {
		return unknownSignature
	}
	fields := make([]string, len(names))
	for i, name := range names {
		fields[i] = name
		if marksRequired && required[name] {
			fields[i] += "*"
		}
	}
	return strings.Join(fields, ", ")
}

func previewChars(s string) int { return utf8.RuneCountInString(s) }

func capPreview(rendered, artifact string) string {
	if previewChars(rendered) <= previewHardCapChars {
		return rendered
	}
	kept := string([]rune(rendered)[:previewHardCapChars])
	if i := strings.LastIndex(kept, "\n"); i >= 0 {
		kept = kept[:i+1]
	} else {
		kept += "\n"
	}
	return kept + fmt.Sprintf("(preview cut at %d characters) load_artifact %s for the rest.\n", previewHardCapChars, artifact)
}
