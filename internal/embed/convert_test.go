// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"testing"
)

func TestConvertData_SameFormat(t *testing.T) {
	data := "hello"
	result, format, err := ConvertData(data, FormatString, FormatString, "")
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

func TestConvertData_BytesToString_TextMIME(t *testing.T) {
	data := []byte("hello world")
	result, format, err := ConvertData(data, FormatBytes, FormatString, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatString {
		t.Errorf("expected FormatString, got %s", format)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %v", result)
	}
}

func TestConvertData_BytesToString_BinaryMIME_Fails(t *testing.T) {
	data := []byte{0xff, 0xfe}
	_, _, err := ConvertData(data, FormatBytes, FormatString, "image/png")
	if err == nil {
		t.Fatal("expected error for binary MIME")
	}
}

func TestConvertData_BytesToString_EmptyMIME(t *testing.T) {
	data := []byte("text content")
	result, format, err := ConvertData(data, FormatBytes, FormatString, "")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatString || result != "text content" {
		t.Errorf("expected text content, got %v", result)
	}
}

func TestConvertData_BytesToJSON(t *testing.T) {
	data := []byte(`{"key": "value"}`)
	result, format, err := ConvertData(data, FormatBytes, FormatJSON, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatJSON {
		t.Errorf("expected FormatJSON, got %s", format)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["key"] != "value" {
		t.Errorf("expected key=value, got %v", m["key"])
	}
}

func TestConvertData_BytesToJSON_WrongMIME_Fails(t *testing.T) {
	data := []byte(`{"key": "value"}`)
	_, _, err := ConvertData(data, FormatBytes, FormatJSON, "text/plain")
	if err == nil {
		t.Fatal("expected error for non-JSON MIME")
	}
}

func TestConvertData_BytesToListOfDicts_CSV(t *testing.T) {
	data := []byte("name,age\nAlice,30\nBob,25")
	result, format, err := ConvertData(data, FormatBytes, FormatListOfDicts, "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatListOfDicts {
		t.Errorf("expected FormatListOfDicts, got %s", format)
	}
	lod, ok := result.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", result)
	}
	if len(lod) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(lod))
	}
	if lod[0]["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", lod[0]["name"])
	}
}

func TestConvertData_StringToBytes(t *testing.T) {
	result, format, err := ConvertData("hello", FormatString, FormatBytes, "")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatBytes {
		t.Errorf("expected FormatBytes, got %s", format)
	}
	b, ok := result.([]byte)
	if !ok || string(b) != "hello" {
		t.Errorf("expected 'hello' bytes, got %v", result)
	}
}

func TestConvertData_StringToJSON(t *testing.T) {
	result, format, err := ConvertData(`{"a": 1}`, FormatString, FormatJSON, "")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatJSON {
		t.Errorf("expected FormatJSON, got %s", format)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["a"] != float64(1) {
		t.Errorf("expected a=1, got %v", m["a"])
	}
}

func TestConvertData_StringToJSON_Invalid(t *testing.T) {
	_, _, err := ConvertData("not json", FormatString, FormatJSON, "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConvertData_StringToListOfDicts_JSON(t *testing.T) {
	result, format, err := ConvertData(`[{"name":"Alice"},{"name":"Bob"}]`, FormatString, FormatListOfDicts, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatListOfDicts {
		t.Errorf("expected FormatListOfDicts, got %s", format)
	}
	lod, ok := result.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", result)
	}
	if len(lod) != 2 {
		t.Fatalf("expected 2, got %d", len(lod))
	}
}

func TestConvertData_StringToListOfDicts_CSV(t *testing.T) {
	result, format, err := ConvertData("name,age\nAlice,30", FormatString, FormatListOfDicts, "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatListOfDicts {
		t.Errorf("expected FormatListOfDicts, got %s", format)
	}
	lod, ok := result.([]map[string]any)
	if !ok || len(lod) != 1 {
		t.Fatalf("expected 1 row, got %v", result)
	}
	if lod[0]["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", lod[0]["name"])
	}
}

func TestConvertData_JSONToString(t *testing.T) {
	data := map[string]any{"key": "value"}
	result, format, err := ConvertData(data, FormatJSON, FormatString, "")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatString {
		t.Errorf("expected FormatString, got %s", format)
	}
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string, got %T", result)
	}
	if s != `{"key":"value"}` {
		t.Errorf("expected compact JSON, got %s", s)
	}
}

func TestConvertData_JSONToListOfDicts(t *testing.T) {
	data := []any{
		map[string]any{"name": "Alice"},
		map[string]any{"name": "Bob"},
	}
	result, format, err := ConvertData(data, FormatJSON, FormatListOfDicts, "")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatListOfDicts {
		t.Errorf("expected FormatListOfDicts, got %s", format)
	}
	lod, ok := result.([]map[string]any)
	if !ok || len(lod) != 2 {
		t.Fatalf("expected 2 dicts, got %v", result)
	}
}

func TestConvertData_JSONToListOfDicts_NotArray(t *testing.T) {
	data := map[string]any{"key": "value"}
	_, _, err := ConvertData(data, FormatJSON, FormatListOfDicts, "")
	if err == nil {
		t.Fatal("expected error for non-array JSON")
	}
}

func TestConvertData_ListOfDictsToString_CSV(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "age": 30},
		{"name": "Bob", "age": 25},
	}
	result, format, err := ConvertData(data, FormatListOfDicts, FormatString, "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatString {
		t.Errorf("expected FormatString, got %s", format)
	}
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string, got %T", result)
	}
	if len(s) == 0 {
		t.Error("expected non-empty CSV")
	}
}

func TestConvertData_ListOfDictsToString_Empty(t *testing.T) {
	data := []map[string]any{}
	result, _, err := ConvertData(data, FormatListOfDicts, FormatString, "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Errorf("expected empty string, got %v", result)
	}
}

func TestConvertData_ListOfDictsToJSON(t *testing.T) {
	data := []map[string]any{{"a": 1}}
	result, format, err := ConvertData(data, FormatListOfDicts, FormatJSON, "")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatJSON {
		t.Errorf("expected FormatJSON, got %s", format)
	}
	arr, ok := result.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("expected []any with 1 element, got %T %v", result, result)
	}
}

func TestConvertData_UnknownToString(t *testing.T) {
	result, format, err := ConvertData(42, DataFormat(-1), FormatString, "")
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatString {
		t.Errorf("expected FormatString, got %s", format)
	}
	if result != "42" {
		t.Errorf("expected '42', got %v", result)
	}
}

func TestIsImageArtifact(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		mime     string
		want     bool
	}{
		{"png mime", "a.png", "image/png", true},
		{"jpeg mime", "a.jpg", "image/jpeg", true},
		{"gif mime", "a.gif", "image/gif", true},
		{"webp mime", "a.webp", "image/webp", true},
		{"bmp mime", "a.bmp", "image/bmp", true},
		{"tiff mime (unsupported ext but image/*)", "a.tiff", "image/tiff", true},

		{"svg mime", "a.svg", "image/svg+xml", false},
		{"svg mime mixed case", "a.SVG", "Image/SVG+XML", false},

		{"png filename but csv mime", "photo.png", "text/csv", false},
		{"png filename but pdf mime", "photo.png", "application/pdf", false},
		{"text/plain with png name", "a.png", "text/plain", false},

		{"empty mime + png ext", "photo.png", "", true},
		{"empty mime + jpg ext", "photo.jpg", "", true},
		{"empty mime + jpeg ext", "photo.jpeg", "", true},
		{"empty mime + gif ext", "photo.gif", "", true},
		{"empty mime + webp ext", "photo.webp", "", true},
		{"empty mime + bmp ext", "photo.bmp", "", true},
		{"empty mime + csv ext", "data.csv", "", false},
		{"empty mime + no ext", "README", "", false},
		{"empty mime + svg ext (excluded)", "logo.svg", "", false},
		{"empty mime + tiff ext (not in allow-list)", "a.tiff", "", false},

		{"octet-stream + png ext", "photo.png", "application/octet-stream", true},
		{"octet-stream + csv ext", "data.csv", "application/octet-stream", false},
		{"octet-stream + svg ext", "logo.svg", "application/octet-stream", false},

		{"mixed-case image mime", "a.png", "Image/PNG", true},
		{"mixed-case octet-stream + png ext", "a.png", "Application/Octet-Stream", true},

		{"mime with params", "a.png", "image/png; charset=binary", true},
		{"svg with params excluded", "a.svg", "image/svg+xml; charset=utf-8", false},

		{"both empty", "", "", false},

		{"uppercase PNG ext + empty mime", "PHOTO.PNG", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsImageArtifact(tc.filename, tc.mime)
			if got != tc.want {
				t.Errorf("IsImageArtifact(%q, %q) = %v, want %v", tc.filename, tc.mime, got, tc.want)
			}
		})
	}
}

func TestIsTextBasedMIME(t *testing.T) {
	textTypes := []string{
		"text/plain", "text/markdown", "text/html",
		"application/json", "application/yaml", "text/csv",
		"text/yaml", "text/xml",
	}
	for _, mt := range textTypes {
		if !IsTextBasedMIME(mt) {
			t.Errorf("expected %q to be text-based", mt)
		}
	}

	binaryTypes := []string{"image/png", "application/octet-stream", "audio/mp3"}
	for _, mt := range binaryTypes {
		if IsTextBasedMIME(mt) {
			t.Errorf("expected %q to NOT be text-based", mt)
		}
	}
}

func TestParseStringToListOfDicts_JSON(t *testing.T) {
	lod, err := parseStringToListOfDicts(`[{"name":"Alice"},{"name":"Bob"}]`, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if len(lod) != 2 {
		t.Fatalf("expected 2, got %d", len(lod))
	}
}

func TestParseStringToListOfDicts_SingleDict(t *testing.T) {
	lod, err := parseStringToListOfDicts(`{"name":"Alice"}`, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if len(lod) != 1 {
		t.Fatalf("expected 1, got %d", len(lod))
	}
}

func TestParseStringToListOfDicts_CSV(t *testing.T) {
	lod, err := parseStringToListOfDicts("name,age\nAlice,30\nBob,25", "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(lod) != 2 {
		t.Fatalf("expected 2, got %d", len(lod))
	}
	if lod[0]["name"] != "Alice" || lod[0]["age"] != "30" {
		t.Errorf("unexpected row: %v", lod[0])
	}
}

func TestParseStringToListOfDicts_InvalidJSON(t *testing.T) {
	_, err := parseStringToListOfDicts("not json", "application/json")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseStringToListOfDicts_UnsupportedMIME(t *testing.T) {
	_, err := parseStringToListOfDicts("data", "application/xml")
	if err == nil {
		t.Fatal("expected error for unsupported MIME")
	}
}

func TestCSVToListOfDicts_QuotedFields(t *testing.T) {
	lod, err := csvToListOfDicts(`name,desc` + "\n" + `Alice,"Hello, World"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(lod) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lod))
	}
	if lod[0]["desc"] != "Hello, World" {
		t.Errorf("expected 'Hello, World', got %v", lod[0]["desc"])
	}
}

func TestListOfDictsToCSV(t *testing.T) {
	lod := []map[string]any{
		{"name": "Alice", "age": 30},
		{"name": "Bob", "age": 25},
	}
	s, err := listOfDictsToCSV(lod)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(s, "name", "age", "Alice", "Bob", "30", "25") {
		t.Errorf("CSV missing expected content: %s", s)
	}
}

func TestListOfDictsToCSV_Empty(t *testing.T) {
	s, err := listOfDictsToCSV([]map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
