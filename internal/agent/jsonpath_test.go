// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import "testing"

func TestApplyJSONPath_FilterAndBindsTighterThanOr(t *testing.T) {
	data := map[string]any{
		"items": []any{
			map[string]any{"id": int64(1), "a": int64(1), "b": int64(0), "c": int64(0)},
			map[string]any{"id": int64(2), "a": int64(0), "b": int64(2), "c": int64(3)},
			map[string]any{"id": int64(3), "a": int64(0), "b": int64(2), "c": int64(0)},
		},
	}

	got, err := ApplyJSONPath(data, `$.items[?(@.a == 1 || @.b == 2 && @.c == 3)]`)
	if err != nil {
		t.Fatalf("ApplyJSONPath: %v", err)
	}
	matches, ok := got.([]any)
	if !ok {
		t.Fatalf("filter result = %T, want []any", got)
	}
	var ids []int64
	for _, m := range matches {
		ids = append(ids, m.(map[string]any)["id"].(int64))
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Errorf("matched ids = %v, want [1 2]", ids)
	}
}

func TestApplyJSONPath_SingleFilterMatchStaysSlice(t *testing.T) {
	data := map[string]any{
		"items": []any{
			map[string]any{"name": "x"},
			map[string]any{"name": "y"},
		},
	}

	got, err := ApplyJSONPath(data, `$.items[?(@.name == 'y')]`)
	if err != nil {
		t.Fatalf("ApplyJSONPath: %v", err)
	}
	matches, ok := got.([]any)
	if !ok || len(matches) != 1 {
		t.Fatalf("filter result = %#v, want one-element []any", got)
	}
}

func TestApplyJSONPath_PlainPathReturnsNode(t *testing.T) {
	data := map[string]any{"a": map[string]any{"b": "v"}}

	got, err := ApplyJSONPath(data, `$.a.b`)
	if err != nil {
		t.Fatalf("ApplyJSONPath: %v", err)
	}
	if got != "v" {
		t.Errorf("got %#v, want \"v\"", got)
	}
	if _, err := ApplyJSONPath(data, `$.a.missing`); err == nil {
		t.Error("expected an error for a path with no match")
	}
}
