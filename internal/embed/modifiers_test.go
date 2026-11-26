// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"strings"
	"testing"
)

func TestJSONPath_SimpleQuery(t *testing.T) {
	data := map[string]any{"name": "Alice", "age": float64(30)}
	result, err := applyJSONPath(data, "$.name", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "Alice" {
		t.Errorf("expected 'Alice', got %v", result)
	}
}

func TestJSONPath_ArrayQuery(t *testing.T) {
	data := []any{
		map[string]any{"name": "Alice"},
		map[string]any{"name": "Bob"},
	}
	result, err := applyJSONPath(data, "$[0].name", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "Alice" {
		t.Errorf("expected 'Alice', got %v", result)
	}
}

func TestJSONPath_InvalidInputType(t *testing.T) {
	_, err := applyJSONPath("not json", "$.name", "")
	if err == nil {
		t.Fatal("expected error for string input")
	}
}

func TestJSONPath_InvalidExpression(t *testing.T) {
	data := map[string]any{"name": "Alice"}
	_, err := applyJSONPath(data, "$[invalid", "")
	if err == nil {
		t.Fatal("expected error for invalid expression")
	}
}

func TestJSONPath_MissingKey(t *testing.T) {
	data := map[string]any{"name": "Alice"}
	_, err := applyJSONPath(data, "$.missing", "")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestJSONPath_SelectsContainer(t *testing.T) {
	data := map[string]any{"items": []any{
		map[string]any{"name": "Alice"},
		map[string]any{"name": "Bob"},
	}}
	result, err := applyJSONPath(data, "$.items", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 items, got %d", len(list))
	}
}

func TestJSONPath_Wildcard(t *testing.T) {
	data := map[string]any{"items": []any{10.0, 20.0, 30.0}}
	result, err := applyJSONPath(data, "$.items[*]", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 items, got %d", len(list))
	}
}

func TestJSONPath_WildcardSingleMatch(t *testing.T) {
	data := map[string]any{"items": []any{42.0}}
	result, err := applyJSONPath(data, "$.items[*]", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any wrapper, got %T (%v)", result, result)
	}
	if len(list) != 1 || list[0] != 42.0 {
		t.Errorf("expected [42], got %v", list)
	}
}

func TestJSONPath_RecursiveDescent(t *testing.T) {
	data := []any{
		map[string]any{"name": "Alice"},
		map[string]any{"nested": map[string]any{"name": "Bob"}},
	}
	result, err := applyJSONPath(data, "$..name", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 name matches, got %v", list)
	}
}

func TestJSONPath_FilterExpression(t *testing.T) {
	data := map[string]any{"items": []any{
		map[string]any{"name": "Alice", "age": 30.0},
		map[string]any{"name": "Bob", "age": 50.0},
	}}
	result, err := applyJSONPath(data, "$.items[?(@.age > 40)].name", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 1 || list[0] != "Bob" {
		t.Errorf("expected [Bob], got %v", list)
	}
}

func TestJSONPath_FilterNoMatches(t *testing.T) {
	data := map[string]any{"items": []any{
		map[string]any{"name": "Alice", "age": 30.0},
	}}
	result, err := applyJSONPath(data, "$.items[?(@.age > 1000)]", "")
	if err != nil {
		t.Fatalf("filter with zero matches should not error: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %v", list)
	}
}

func TestJSONPath_DeepPath(t *testing.T) {
	data := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": map[string]any{"d": "found"},
			},
		},
	}
	result, err := applyJSONPath(data, "$.a.b.c.d", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "found" {
		t.Errorf("expected 'found', got %v", result)
	}
}

func TestJSONPath_RootExpression(t *testing.T) {
	data := map[string]any{"x": 1.0, "y": 2.0}
	result, err := applyJSONPath(data, "$", "")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if got["x"] != 1.0 || got["y"] != 2.0 {
		t.Errorf("expected full map, got %v", got)
	}
}

func TestJSONPath_NegativeIndex(t *testing.T) {
	data := []any{10.0, 20.0, 30.0}
	result, err := applyJSONPath(data, "$[-1]", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != 30.0 {
		t.Errorf("expected 30, got %v", result)
	}
}

func TestJSONPath_Slice(t *testing.T) {
	data := map[string]any{"items": []any{"a", "b", "c", "d"}}
	result, err := applyJSONPath(data, "$.items[1:3]", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 2 || list[0] != "b" || list[1] != "c" {
		t.Errorf("expected [b c], got %v", list)
	}
}

func TestJSONPath_QuotedKey(t *testing.T) {
	data := map[string]any{"with space": "ok"}
	result, err := applyJSONPath(data, "$['with space']", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %v", result)
	}
}

func TestJSONPath_Union(t *testing.T) {
	data := map[string]any{
		"a": map[string]any{"x": 1.0},
		"b": map[string]any{"x": 2.0},
		"c": map[string]any{"x": 3.0},
	}
	result, err := applyJSONPath(data, "$['a','c']", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 matches, got %v", list)
	}
}

func TestJSONPath_KeyWildcard(t *testing.T) {
	data := map[string]any{
		"a": map[string]any{"x": 1.0},
		"b": map[string]any{"x": 2.0},
	}
	result, err := applyJSONPath(data, "$.*.x", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 matches, got %v", list)
	}
}

func TestJSONPath_EmptyExpression(t *testing.T) {
	data := map[string]any{"name": "Alice"}
	_, err := applyJSONPath(data, "", "")
	if err == nil {
		t.Fatal("expected error for empty expression")
	}
}

func TestJSONPath_NilData(t *testing.T) {
	_, err := applyJSONPath(nil, "$.name", "")
	if err == nil {
		t.Fatal("expected error for nil data")
	}
}

func TestJSONPath_ListOfDictsShape(t *testing.T) {
	data := map[string]any{"rows": []any{
		map[string]any{"id": 1.0, "v": "a"},
		map[string]any{"id": 2.0, "v": "b"},
	}}
	result, err := applyJSONPath(data, "$.rows", "")
	if err != nil {
		t.Fatal(err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any (directly consumable by select_fields), got %T", result)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 rows, got %d", len(list))
	}
	if _, ok := list[0].(map[string]any); !ok {
		t.Errorf("expected first row to be map[string]any, got %T", list[0])
	}
}

func TestJSONPath_IntegerLiteralType(t *testing.T) {
	data := map[string]any{"count": 42.0}
	result, err := applyJSONPath(data, "$.count", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != 42.0 {
		t.Errorf("expected 42.0 (float64), got %v (%T)", result, result)
	}
}

func TestSelectCols_SingleColumn(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "age": 30, "city": "NYC"},
	}
	result, err := applySelectCols(data, "name", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 1 || lod[0]["name"] != "Alice" {
		t.Errorf("unexpected result: %v", lod)
	}
	if _, ok := lod[0]["age"]; ok {
		t.Error("age should be excluded")
	}
}

func TestSelectCols_MultipleColumns(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "age": 30, "city": "NYC"},
	}
	result, err := applySelectCols(data, "name,age", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod[0]) != 2 {
		t.Errorf("expected 2 columns, got %d", len(lod[0]))
	}
}

func TestSelectCols_InvalidColumn(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice"},
	}
	_, err := applySelectCols(data, "nonexistent", "")
	if err == nil {
		t.Fatal("expected error for invalid column")
	}
}

func TestSelectCols_EmptyData(t *testing.T) {
	result, err := applySelectCols([]map[string]any{}, "name", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 0 {
		t.Errorf("expected empty, got %d rows", len(lod))
	}
}

func TestSelectCols_InvalidInputType(t *testing.T) {
	_, err := applySelectCols("not a list", "name", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSelectCols_WithWhitespace(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "age": 30},
	}
	result, err := applySelectCols(data, " name , age ", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod[0]) != 2 {
		t.Errorf("expected 2 columns, got %d", len(lod[0]))
	}
}

func TestFilterRowsEq_Basic(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "role": "admin"},
		{"name": "Bob", "role": "user"},
		{"name": "Charlie", "role": "admin"},
	}
	result, err := applyFilterRowsEq(data, "role:admin", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(lod))
	}
}

func TestFilterRowsEq_NumericValue(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "age": 30},
		{"name": "Bob", "age": 25},
	}
	result, err := applyFilterRowsEq(data, "age:30", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 1 {
		t.Fatalf("expected 1 match, got %d", len(lod))
	}
}

func TestFilterRowsEq_NoMatches(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice"},
	}
	result, err := applyFilterRowsEq(data, "name:Nobody", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(lod))
	}
}

func TestFilterRowsEq_InvalidFormat(t *testing.T) {
	data := []map[string]any{{"name": "Alice"}}
	_, err := applyFilterRowsEq(data, "nocolon", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFilterRowsEq_InvalidColumn(t *testing.T) {
	data := []map[string]any{{"name": "Alice"}}
	_, err := applyFilterRowsEq(data, "nonexistent:value", "")
	if err == nil {
		t.Fatal("expected error for invalid column")
	}
}

func TestFilterRowsEq_EmptyData(t *testing.T) {
	result, err := applyFilterRowsEq([]map[string]any{}, "name:Alice", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 0 {
		t.Errorf("expected empty, got %d", len(lod))
	}
}

func TestFilterRowsEq_ColonInValue(t *testing.T) {
	data := []map[string]any{
		{"url": "http://example.com"},
		{"url": "http://other.com"},
	}
	result, err := applyFilterRowsEq(data, "url:http://example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 1 {
		t.Fatalf("expected 1, got %d", len(lod))
	}
}

func TestSliceRows_Basic(t *testing.T) {
	data := makeRows(10)
	result, err := applySliceRows(data, "2:5", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(lod))
	}
}

func TestSliceRows_FromStart(t *testing.T) {
	data := makeRows(5)
	result, err := applySliceRows(data, ":3", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 3 {
		t.Fatalf("expected 3, got %d", len(lod))
	}
}

func TestSliceRows_ToEnd(t *testing.T) {
	data := makeRows(10)
	result, err := applySliceRows(data, "7:", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 3 {
		t.Fatalf("expected 3, got %d", len(lod))
	}
}

func TestSliceRows_InvalidFormat(t *testing.T) {
	_, err := applySliceRows(makeRows(5), "invalid", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSliceRows_InvalidIndices(t *testing.T) {
	_, err := applySliceRows(makeRows(5), "a:b", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSliceRows_InvalidInputType(t *testing.T) {
	_, err := applySliceRows("not a list", "0:2", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSliceRows_NegativeIndices(t *testing.T) {
	data := makeRows(5)
	result, err := applySliceRows(data, "-2:", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod) != 2 {
		t.Fatalf("expected 2, got %d", len(lod))
	}
}

func TestSliceLines_Basic(t *testing.T) {
	data := "line0\nline1\nline2\nline3\nline4\n"
	result, err := applySliceLines(data, "1:3", "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	if !strings.Contains(s, "line1") || !strings.Contains(s, "line2") {
		t.Errorf("expected line1 and line2, got %q", s)
	}
	if strings.Contains(s, "line0") || strings.Contains(s, "line3") {
		t.Errorf("should not contain line0 or line3, got %q", s)
	}
}

func TestSliceLines_FromStart(t *testing.T) {
	data := "line0\nline1\nline2\n"
	result, err := applySliceLines(data, ":2", "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	if !strings.Contains(s, "line0") || !strings.Contains(s, "line1") {
		t.Errorf("expected line0 and line1, got %q", s)
	}
}

func TestSliceLines_ToEnd(t *testing.T) {
	data := "line0\nline1\nline2\n"
	result, err := applySliceLines(data, "1:", "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	if !strings.Contains(s, "line1") || !strings.Contains(s, "line2") {
		t.Errorf("expected line1 and line2, got %q", s)
	}
}

func TestSliceLines_InvalidInputType(t *testing.T) {
	_, err := applySliceLines(42, "0:2", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGrep_Basic(t *testing.T) {
	data := "match this\nskip this\nmatch that\n"
	result, err := applyGrep(data, "match", "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	lines := strings.Split(s, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if l != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	if len(nonEmpty) != 2 {
		t.Fatalf("expected 2 matches, got %d: %v", len(nonEmpty), nonEmpty)
	}
}

func TestGrep_RegexPattern(t *testing.T) {
	data := "test1\nfoo\ntest23\nbar\n"
	result, err := applyGrep(data, `test\d+`, "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	if !strings.Contains(s, "test1") || !strings.Contains(s, "test23") {
		t.Errorf("expected test1 and test23, got %q", s)
	}
}

func TestGrep_NoMatches(t *testing.T) {
	data := "line1\nline2\n"
	result, err := applyGrep(data, "nomatch", "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	if s != "" {
		t.Errorf("expected empty, got %q", s)
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	_, err := applyGrep("data", "[invalid", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGrep_InvalidInputType(t *testing.T) {
	_, err := applyGrep(42, "pattern", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGrep_SpecialCharacters(t *testing.T) {
	data := "file.txt\nfile_txt\n"
	result, err := applyGrep(data, `file\.txt`, "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	if !strings.Contains(s, "file.txt") {
		t.Errorf("expected 'file.txt', got %q", s)
	}
	if strings.Contains(s, "file_txt") {
		t.Error("should not match 'file_txt'")
	}
}

func TestGrep_PatternTooLong(t *testing.T) {
	data := "line1\nline2\n"
	longPattern := strings.Repeat("a", maxGrepPatternLen+1)
	_, err := applyGrep(data, longPattern, "")
	if err == nil {
		t.Fatal("expected error for pattern exceeding max length")
	}
	if !strings.Contains(err.Error(), "pattern too long") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHead_Basic(t *testing.T) {
	data := "line1\nline2\nline3\nline4\nline5\n"
	result, err := applyHead(data, "3", "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	if strings.Count(s, "line") != 3 {
		t.Errorf("expected 3 lines, got %q", s)
	}
}

func TestHead_Zero(t *testing.T) {
	result, err := applyHead("line1\nline2\n", "0", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestHead_MoreThanAvailable(t *testing.T) {
	data := "line1\nline2\n"
	result, err := applyHead(data, "100", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != data {
		t.Errorf("expected all lines")
	}
}

func TestHead_Negative(t *testing.T) {
	_, err := applyHead("data", "-1", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHead_InvalidCount(t *testing.T) {
	_, err := applyHead("data", "abc", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHead_LargeCount(t *testing.T) {
	data := "line1\nline2\n"
	result, err := applyHead(data, "999999", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != data {
		t.Errorf("expected all lines")
	}
}

func TestTail_Basic(t *testing.T) {
	data := "line1\nline2\nline3\nline4\nline5\n"
	result, err := applyTail(data, "3", "")
	if err != nil {
		t.Fatal(err)
	}
	s := result.(string)
	if !strings.Contains(s, "line3") || !strings.Contains(s, "line5") {
		t.Errorf("expected last 3 lines, got %q", s)
	}
	if strings.Contains(s, "line1") || strings.Contains(s, "line2") {
		t.Error("should not contain line1 or line2")
	}
}

func TestTail_Zero(t *testing.T) {
	result, err := applyTail("line1\nline2\n", "0", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Errorf("expected empty, got %v", result)
	}
}

func TestTail_MoreThanAvailable(t *testing.T) {
	data := "line1\nline2\n"
	result, err := applyTail(data, "100", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != data {
		t.Errorf("expected all lines")
	}
}

func TestTail_Negative(t *testing.T) {
	_, err := applyTail("data", "-1", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSelectFields_Basic(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "age": 30, "city": "NYC"},
	}
	result, err := applySelectFields(data, "name,age", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod[0]) != 2 {
		t.Errorf("expected 2 fields, got %d", len(lod[0]))
	}
}

func TestSelectFields_SingleField(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "age": 30},
	}
	result, err := applySelectFields(data, "name", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if lod[0]["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", lod[0]["name"])
	}
	if len(lod[0]) != 1 {
		t.Errorf("expected 1 field, got %d", len(lod[0]))
	}
}

func TestSelectFields_MissingField(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice"},
		{"name": "Bob", "age": 25},
	}
	result, err := applySelectFields(data, "name,age", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if len(lod[0]) != 1 {
		t.Errorf("expected 1 field for Alice, got %d", len(lod[0]))
	}
	if len(lod[1]) != 2 {
		t.Errorf("expected 2 fields for Bob, got %d", len(lod[1]))
	}
}

func TestSelectFields_InvalidInputType(t *testing.T) {
	_, err := applySelectFields("not a list", "name", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSelectFields_NestedDicts(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "meta": map[string]any{"role": "admin"}},
	}
	result, err := applySelectFields(data, "name,meta", "")
	if err != nil {
		t.Fatal(err)
	}
	lod := result.([]map[string]any)
	if _, ok := lod[0]["meta"].(map[string]any); !ok {
		t.Error("expected nested dict to be preserved")
	}
}

func TestParseSlice_Basic(t *testing.T) {
	start, end, err := parseSlice("2:5", 10)
	if err != nil {
		t.Fatal(err)
	}
	if start != 2 || end != 5 {
		t.Errorf("expected 2,5 got %d,%d", start, end)
	}
}

func TestParseSlice_EmptyStart(t *testing.T) {
	start, end, err := parseSlice(":3", 10)
	if err != nil {
		t.Fatal(err)
	}
	if start != 0 || end != 3 {
		t.Errorf("expected 0,3 got %d,%d", start, end)
	}
}

func TestParseSlice_EmptyEnd(t *testing.T) {
	start, end, err := parseSlice("5:", 10)
	if err != nil {
		t.Fatal(err)
	}
	if start != 5 || end != 10 {
		t.Errorf("expected 5,10 got %d,%d", start, end)
	}
}

func TestParseSlice_Negative(t *testing.T) {
	start, _, err := parseSlice("-2:", 10)
	if err != nil {
		t.Fatal(err)
	}
	if start != 8 {
		t.Errorf("expected 8, got %d", start)
	}
}

func TestModifierRegistry_AllDefined(t *testing.T) {
	expected := []string{
		"jsonpath", "select_cols", "filter_rows_eq", "slice_rows",
		"slice_lines", "grep", "head", "tail", "select_fields", "apply_to_template",
	}
	for _, name := range expected {
		if _, ok := ModifierRegistry[name]; !ok {
			t.Errorf("missing modifier: %s", name)
		}
	}
}

func TestModifierRegistry_AllHaveAccepts(t *testing.T) {
	for name, def := range ModifierRegistry {
		if len(def.Accepts) == 0 {
			t.Errorf("modifier %s has no Accepts formats", name)
		}
	}
}

func makeRows(n int) []map[string]any {
	rows := make([]map[string]any, n)
	for i := range n {
		rows[i] = map[string]any{"idx": i}
	}
	return rows
}
