// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"encoding/json"
	"fmt"
	"reflect"
)

func coerceString(field string, v any) (string, error) {
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q: expected string, got %T", field, v)
	}
	return s, nil
}

func coerceInt(field string, v any) (int, error) {
	if v == nil {
		return 0, nil
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return int(n), nil
	case uint:
		return int(n), nil
	case uint32:
		return int(n), nil
	case uint64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("field %q: expected integer, got non-integral float %v", field, n)
		}
		return int(n), nil
	case float32:
		if n != float32(int(n)) {
			return 0, fmt.Errorf("field %q: expected integer, got non-integral float %v", field, n)
		}
		return int(n), nil
	}
	return 0, fmt.Errorf("field %q: expected int, got %T", field, v)
}

func coerceInt64(field string, v any) (int64, error) {
	n, err := coerceInt(field, v)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}

func coerceFloat64(field string, v any) (float64, error) {
	if v == nil {
		return 0, nil
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case uint:
		return float64(n), nil
	case uint32:
		return float64(n), nil
	case uint64:
		return float64(n), nil
	}
	return 0, fmt.Errorf("field %q: expected number, got %T", field, v)
}

func coerceBool(field string, v any) (bool, error) {
	if v == nil {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("field %q: expected bool, got %T", field, v)
	}
	return b, nil
}

func coerceStringSlice(field string, v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	switch xs := v.(type) {
	case []string:
		return xs, nil
	case []any:
		out := make([]string, len(xs))
		for i, x := range xs {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("field %q: element %d: expected string, got %T", field, i, x)
			}
			out[i] = s
		}
		return out, nil
	}
	return nil, fmt.Errorf("field %q: expected []string, got %T", field, v)
}

func coerceMap(field string, v any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("field %q: expected map[string]any, got %T", field, v)
	}
	return m, nil
}

func coerceJSON(field string, v any, out any) error {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("field %q: marshal: %w", field, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("field %q: %w", field, err)
	}
	return nil
}

func coerceJSONObject(field string, v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("field %q: marshal: %w", field, err)
	}
	return json.RawMessage(raw), nil
}

func reverseJSONObject(in json.RawMessage, out *any) error {
	if len(in) == 0 {
		return nil
	}
	if err := json.Unmarshal(in, out); err != nil {
		return err
	}
	if m, ok := (*out).(map[string]any); ok && len(m) == 0 {
		*out = nil
	}
	return nil
}

func reverseJSON(in any, out *any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func withoutEmptyMembers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		dropEmptyMembers(t)
	case []any:
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				dropEmptyMembers(m)
			}
		}
	}
	return v
}

func dropEmptyMembers(m map[string]any) {
	for k, member := range m {
		switch t := member.(type) {
		case nil:
			delete(m, k)
		case []any:
			if len(t) == 0 {
				delete(m, k)
			}
		case map[string]any:
			if len(t) == 0 {
				delete(m, k)
			}
		}
	}
}

func isZeroAny(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array, reflect.String:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}
	return false
}
