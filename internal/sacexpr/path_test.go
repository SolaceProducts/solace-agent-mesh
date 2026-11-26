// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"testing"
)

func TestGetPath_Simple(t *testing.T) {
	data := map[string]any{
		"name": "alice",
		"age":  30,
	}

	tests := []struct {
		name string
		path string
		want any
	}{
		{"empty path returns root", "", data},
		{"simple key", "name", "alice"},
		{"integer value", "age", 30},
		{"missing key returns nil", "email", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getPath(data, tt.path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fmtAny(got) != fmtAny(tt.want) {
				t.Errorf("getPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestGetPath_Nested(t *testing.T) {
	data := map[string]any{
		"user": map[string]any{
			"name": "alice",
			"address": map[string]any{
				"city":    "Vancouver",
				"country": "Canada",
			},
		},
	}

	tests := []struct {
		path string
		want any
	}{
		{"user.name", "alice"},
		{"user.address.city", "Vancouver"},
		{"user.address.country", "Canada"},
		{"user.address.zip", nil},
		{"user.phone", nil},
		{"nonexistent.deep.path", nil},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := getPath(data, tt.path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fmtAny(got) != fmtAny(tt.want) {
				t.Errorf("getPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestGetPath_Arrays(t *testing.T) {
	data := map[string]any{
		"items": []any{"a", "b", "c"},
		"nested": []any{
			map[string]any{"id": 1, "name": "first"},
			map[string]any{"id": 2, "name": "second"},
		},
	}

	tests := []struct {
		path string
		want any
	}{
		{"items[0]", "a"},
		{"items[1]", "b"},
		{"items[2]", "c"},
		{"items[-1]", "c"},
		{"items[-2]", "b"},
		{"items[99]", nil},
		{"nested[0].name", "first"},
		{"nested[1].id", 2},
		{"nested[-1].name", "second"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := getPath(data, tt.path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fmtAny(got) != fmtAny(tt.want) {
				t.Errorf("getPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestGetPath_NilData(t *testing.T) {
	got, err := getPath(nil, "any.path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestSetPath_Simple(t *testing.T) {
	data := map[string]any{"existing": "value"}

	root, err := setPath(data, "new_key", "new_value")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := root.(map[string]any)
	if m["new_key"] != "new_value" {
		t.Errorf("expected new_value, got %v", m["new_key"])
	}
	if m["existing"] != "value" {
		t.Errorf("existing key modified")
	}
}

func TestSetPath_Nested(t *testing.T) {
	data := map[string]any{}

	root, err := setPath(data, "a.b.c", "deep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := root.(map[string]any)
	a := m["a"].(map[string]any)
	b := a["b"].(map[string]any)
	if b["c"] != "deep" {
		t.Errorf("expected deep, got %v", b["c"])
	}
}

func TestSetPath_Overwrite(t *testing.T) {
	data := map[string]any{"key": "old"}

	root, err := setPath(data, "key", "new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := root.(map[string]any)
	if m["key"] != "new" {
		t.Errorf("expected new, got %v", m["key"])
	}
}

func TestSetPath_EmptyPath(t *testing.T) {
	root, err := setPath(nil, "", "replacement")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root != "replacement" {
		t.Errorf("expected replacement, got %v", root)
	}
}

func TestSetPath_ArrayIndex(t *testing.T) {
	data := map[string]any{
		"items": []any{"a", "b", "c"},
	}

	root, err := setPath(data, "items[1]", "B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := root.(map[string]any)
	items := m["items"].([]any)
	if items[1] != "B" {
		t.Errorf("expected B, got %v", items[1])
	}
}

func TestSetPath_NilRoot(t *testing.T) {
	root, err := setPath(nil, "a.b", "val")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := root.(map[string]any)
	a := m["a"].(map[string]any)
	if a["b"] != "val" {
		t.Errorf("expected val, got %v", a["b"])
	}
}

func TestParsePath(t *testing.T) {
	tests := []struct {
		path     string
		expected int
	}{
		{"", 0},
		{"a", 1},
		{"a.b.c", 3},
		{"arr[0]", 2},
		{"items[0].name", 3},
		{"[0]", 1},
		{"a.b[1].c[2].d", 6},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			segs := parsePath(tt.path)
			if len(segs) != tt.expected {
				t.Errorf("parsePath(%q) = %d segments, want %d", tt.path, len(segs), tt.expected)
			}
		})
	}
}

func fmtAny(v any) string {
	if v == nil {
		return "<nil>"
	}
	return fmtVal(v)
}

func fmtVal(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return string(rune(val + '0'))
	default:
		return "<complex>"
	}
}
