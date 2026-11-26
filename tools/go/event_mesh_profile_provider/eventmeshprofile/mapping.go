// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmeshprofile

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type FieldMapping struct {
	PassThroughUnmapped bool `json:"pass_through_unmapped,omitempty"`

	DefaultValues map[string]any `json:"default_values,omitempty"`

	FieldMapping map[string]string `json:"field_mapping,omitempty"`

	ComputedFields []ComputedField `json:"computed_fields,omitempty"`
}

type ComputedField struct {
	Target       string   `json:"target"`
	SourceFields []string `json:"source_fields"`
	Separator    string   `json:"separator,omitempty"`
}

func (fm FieldMapping) Validate() string {
	srcTargets := map[string][]string{}
	for canonical, source := range fm.FieldMapping {
		srcTargets[source] = append(srcTargets[source], canonical)
	}
	for source, targets := range srcTargets {
		if len(targets) > 1 {
			slices.Sort(targets)
			return fmt.Sprintf("field_mapping_config.field_mapping: source %q is mapped to multiple targets %v; one source can only feed one canonical target.", source, targets)
		}
	}
	return ""
}

func (fm FieldMapping) Apply(raw map[string]any) map[string]any {
	src := maps.Clone(raw)
	if src == nil {
		src = make(map[string]any)
	}

	for k, def := range fm.DefaultValues {
		if existing, ok := src[k]; !ok || isEmpty(existing) {
			src[k] = def
		}
	}

	out := make(map[string]any, len(src))

	for canonical, source := range fm.FieldMapping {
		if val, ok := src[source]; ok {
			out[canonical] = val
			delete(src, source)
		}
	}

	if fm.PassThroughUnmapped {
		for k, v := range src {
			if _, alreadySet := out[k]; !alreadySet {
				out[k] = v
			}
		}
	}

	for _, cf := range fm.ComputedFields {
		parts := make([]string, 0, len(cf.SourceFields))
		for _, sf := range cf.SourceFields {
			if s, ok := stringValue(out[sf]); ok && s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			out[cf.Target] = strings.Join(parts, cf.Separator)
		}
	}

	return out
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case int:
		return x == 0
	case int64:
		return x == 0
	case float64:
		return x == 0
	}
	return false
}

func stringValue(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return x, true
	case bool, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%v", x), true
	}
	return "", false
}
