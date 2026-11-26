// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"context"
	"log/slog"
	"testing"
)

func TestParseModifierChain_SimpleArtifact(t *testing.T) {
	spec, mods, format := ParseModifierChain("data.csv")
	if spec != "data.csv" {
		t.Errorf("expected 'data.csv', got %q", spec)
	}
	if len(mods) != 0 {
		t.Errorf("expected 0 modifiers, got %d", len(mods))
	}
	if format != "" {
		t.Errorf("expected empty format, got %q", format)
	}
}

func TestParseModifierChain_WithVersion(t *testing.T) {
	spec, mods, format := ParseModifierChain("file.csv:1")
	if spec != "file.csv:1" {
		t.Errorf("expected 'file.csv:1', got %q", spec)
	}
	if len(mods) != 0 {
		t.Errorf("expected 0 modifiers, got %d", len(mods))
	}
	if format != "" {
		t.Errorf("expected empty format, got %q", format)
	}
}

func TestParseModifierChain_WithMultipleModifiers(t *testing.T) {
	spec, mods, format := ParseModifierChain("data.csv >>> select_cols:name,age >>> filter_rows_eq:name:Alice")
	if spec != "data.csv" {
		t.Errorf("expected 'data.csv', got %q", spec)
	}
	if len(mods) != 2 {
		t.Fatalf("expected 2 modifiers, got %d", len(mods))
	}
	if mods[0].Name != "select_cols" || mods[0].Value != "name,age" {
		t.Errorf("modifier 0: %+v", mods[0])
	}
	if mods[1].Name != "filter_rows_eq" || mods[1].Value != "name:Alice" {
		t.Errorf("modifier 1: %+v", mods[1])
	}
	if format != "" {
		t.Errorf("expected empty format, got %q", format)
	}
}

func TestParseModifierChain_WithFormat(t *testing.T) {
	spec, mods, format := ParseModifierChain("data.json >>> format:json_pretty")
	if spec != "data.json" {
		t.Errorf("expected 'data.json', got %q", spec)
	}
	if len(mods) != 0 {
		t.Errorf("expected 0 modifiers, got %d", len(mods))
	}
	if format != "json_pretty" {
		t.Errorf("expected 'json_pretty', got %q", format)
	}
}

func TestParseModifierChain_WithModifiersAndFormat(t *testing.T) {
	spec, mods, format := ParseModifierChain("data.csv >>> select_cols:name >>> format:text")
	if spec != "data.csv" {
		t.Errorf("expected 'data.csv', got %q", spec)
	}
	if len(mods) != 1 {
		t.Fatalf("expected 1 modifier, got %d", len(mods))
	}
	if mods[0].Name != "select_cols" {
		t.Errorf("expected 'select_cols', got %q", mods[0].Name)
	}
	if format != "text" {
		t.Errorf("expected 'text', got %q", format)
	}
}

func TestParseModifierChain_Empty(t *testing.T) {
	spec, mods, format := ParseModifierChain("")
	if spec != "" || len(mods) != 0 || format != "" {
		t.Errorf("expected all empty, got spec=%q mods=%v format=%q", spec, mods, format)
	}
}

func TestParseModifierChain_EmptySteps(t *testing.T) {
	spec, mods, format := ParseModifierChain("data.csv >>> >>> head:5")
	if spec != "data.csv" {
		t.Errorf("expected 'data.csv', got %q", spec)
	}
	if len(mods) != 1 {
		t.Fatalf("expected 1 modifier (empty steps skipped), got %d", len(mods))
	}
	if mods[0].Name != "head" {
		t.Errorf("expected 'head', got %q", mods[0].Name)
	}
	if format != "" {
		t.Errorf("expected empty format, got %q", format)
	}
}

func TestParseModifierChain_Complex(t *testing.T) {
	expr := "report.json:2 >>> jsonpath:$.items >>> select_fields:name,value >>> slice_rows:0:10 >>> format:json_pretty"
	spec, mods, format := ParseModifierChain(expr)
	if spec != "report.json:2" {
		t.Errorf("spec = %q", spec)
	}
	if len(mods) != 3 {
		t.Fatalf("expected 3 modifiers, got %d", len(mods))
	}
	if mods[0].Name != "jsonpath" {
		t.Errorf("mod 0 = %q", mods[0].Name)
	}
	if mods[1].Name != "select_fields" {
		t.Errorf("mod 1 = %q", mods[1].Name)
	}
	if mods[2].Name != "slice_rows" {
		t.Errorf("mod 2 = %q", mods[2].Name)
	}
	if format != "json_pretty" {
		t.Errorf("format = %q", format)
	}
}

func TestParseModifierChain_NoColonSkipped(t *testing.T) {
	spec, mods, _ := ParseModifierChain("data.csv >>> nocolon >>> head:5")
	if spec != "data.csv" {
		t.Errorf("spec = %q", spec)
	}
	if len(mods) != 1 {
		t.Fatalf("expected 1 modifier (nocolon skipped), got %d", len(mods))
	}
}

func TestExecuteChain_EmptyChain(t *testing.T) {
	data := "hello"
	result, format, err := ExecuteChain(context.Background(), data, FormatString, "", nil, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatString {
		t.Errorf("expected FormatString, got %s", format)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %v", result)
	}
}

func TestExecuteChain_SingleModifier(t *testing.T) {
	data := "line1\nline2\nline3\nline4\nline5\n"
	mods := []ModifierStep{{Name: "head", Value: "3"}}
	result, format, err := ExecuteChain(context.Background(), data, FormatString, "", mods, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatString {
		t.Errorf("expected FormatString, got %s", format)
	}
	s := result.(string)
	if count := countOccurrences(s, "line"); count != 3 {
		t.Errorf("expected 3 lines, got %d: %q", count, s)
	}
}

func TestExecuteChain_AutoConvert(t *testing.T) {
	data := `{"name": "Alice", "age": 30}`
	mods := []ModifierStep{{Name: "jsonpath", Value: "$.name"}}
	result, _, err := ExecuteChain(context.Background(), data, FormatString, "application/json", mods, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if result != "Alice" {
		t.Errorf("expected 'Alice', got %v", result)
	}
}

func TestExecuteChain_MultipleModifiers(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "role": "admin"},
		{"name": "Bob", "role": "user"},
		{"name": "Charlie", "role": "admin"},
	}
	mods := []ModifierStep{
		{Name: "filter_rows_eq", Value: "role:admin"},
		{Name: "select_fields", Value: "name"},
	}
	result, format, err := ExecuteChain(context.Background(), data, FormatListOfDicts, "", mods, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatListOfDicts {
		t.Errorf("expected FormatListOfDicts, got %s", format)
	}
	lod := result.([]map[string]any)
	if len(lod) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(lod))
	}
	if len(lod[0]) != 1 {
		t.Errorf("expected 1 field, got %d", len(lod[0]))
	}
}

func TestExecuteChain_UnknownModifier(t *testing.T) {
	_, _, err := ExecuteChain(context.Background(), "data", FormatString, "", []ModifierStep{{Name: "unknown", Value: ""}}, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for unknown modifier")
	}
}

func countOccurrences(s, sub string) int {
	count := 0
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			count++
		}
	}
	return count
}
