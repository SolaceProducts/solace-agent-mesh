// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
)

type SchemaSkewSink struct {
	mu    sync.Mutex
	byKey map[string]map[string]struct{}
}

func NewSchemaSkewSink() *SchemaSkewSink {
	return &SchemaSkewSink{byKey: map[string]map[string]struct{}{}}
}

func (s *SchemaSkewSink) record(path string, keys []string) {
	if s == nil || len(keys) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		paths := s.byKey[k]
		if paths == nil {
			paths = map[string]struct{}{}
			s.byKey[k] = paths
		}
		paths[path] = struct{}{}
	}
}

func (s *SchemaSkewSink) Empty() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byKey) == 0
}

type SchemaSkewFinding struct {
	Key   string
	Paths []string
}

func (s *SchemaSkewSink) Findings() []SchemaSkewFinding {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SchemaSkewFinding, 0, len(s.byKey))
	for k, paths := range s.byKey {
		ps := make([]string, 0, len(paths))
		for p := range paths {
			ps = append(ps, p)
		}
		slices.Sort(ps)
		out = append(out, SchemaSkewFinding{Key: k, Paths: ps})
	}
	slices.SortFunc(out, func(a, b SchemaSkewFinding) int { return strings.Compare(a.Key, b.Key) })
	return out
}

func FormatSchemaSkew(s *SchemaSkewSink) string {
	if s.Empty() {
		return ""
	}
	parts := make([]string, 0)
	for _, f := range s.Findings() {
		parts = append(parts, f.Key+" on "+strings.Join(f.Paths, ", "))
	}
	return "the platform returned fields this sam CLI does not recognize (" +
		strings.Join(parts, "; ") +
		") — your CLI may be older than the platform; " + rebuildHint
}

func detectUnknownKeys(raw []byte, dst any) []string {
	t := derefType(reflect.TypeOf(dst))
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	if elem, ok := paginatedElem(t); ok {
		return unknownKeysInList(raw, elem)
	}
	return unknownKeysInObject(raw, t)
}

func paginatedElem(t reflect.Type) (reflect.Type, bool) {
	for f := range t.Fields() {
		if jsonName(f) != "data" {
			continue
		}
		ft := f.Type
		if ft.Kind() != reflect.Slice {
			return nil, false
		}
		elem := derefType(ft.Elem())
		if elem != nil && elem.Kind() == reflect.Struct {
			return elem, true
		}
		return nil, false
	}
	return nil, false
}

func unknownKeysInList(raw []byte, elem reflect.Type) []string {
	var wrapper struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, el := range wrapper.Data {
		for _, k := range unknownKeysInObject(el, elem) {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}

func unknownKeysInObject(raw []byte, t reflect.Type) []string {
	if hasCatchAll(t) {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	known := knownJSONKeys(t)
	var out []string
	for k := range obj {
		if _, ok := known[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func hasCatchAll(t reflect.Type) bool {
	for f := range t.Fields() {
		ft := f.Type
		switch ft.Kind() {
		case reflect.Interface:
			return true
		case reflect.Map:
			if ft.Elem().Kind() == reflect.Interface {
				return true
			}
		}
	}
	return false
}

func knownJSONKeys(t reflect.Type) map[string]struct{} {
	out := map[string]struct{}{}
	for f := range t.Fields() {
		if f.Anonymous {
			if et := derefType(f.Type); et != nil && et.Kind() == reflect.Struct {
				for k := range knownJSONKeys(et) {
					out[k] = struct{}{}
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		name := jsonName(f)
		if name == "" || name == "-" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

func jsonName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "-"
	}
	if name == "" {
		return f.Name
	}
	return name
}

func derefType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
