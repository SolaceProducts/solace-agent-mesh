// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"strings"
	"testing"
)

func TestSerialize_Text_FromString(t *testing.T) {
	result, err := SerializeData("hello world", FormatString, "text", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %q", result)
	}
}

func TestSerialize_Text_FromJSON(t *testing.T) {
	data := map[string]any{"key": "value"}
	result, err := SerializeData(data, FormatJSON, "text", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "key") {
		t.Errorf("expected JSON text containing 'key', got %q", result)
	}
}

func TestSerialize_JSON_Compact(t *testing.T) {
	data := map[string]any{"key": "value"}
	result, err := SerializeData(data, FormatJSON, "json", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != `{"key":"value"}` {
		t.Errorf("expected compact JSON, got %q", result)
	}
}

func TestSerialize_JSON_Pretty(t *testing.T) {
	data := map[string]any{"key": "value"}
	result, err := SerializeData(data, FormatJSON, "json_pretty", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "\n") {
		t.Error("expected pretty JSON with newlines")
	}
	if !strings.Contains(result, "  ") {
		t.Error("expected pretty JSON with indentation")
	}
}

func TestSerialize_CSV(t *testing.T) {
	data := []map[string]any{
		{"name": "Alice", "age": "30"},
		{"name": "Bob", "age": "25"},
	}
	result, err := SerializeData(data, FormatListOfDicts, "csv", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Alice") || !strings.Contains(result, "Bob") {
		t.Errorf("CSV missing expected data: %q", result)
	}
}

func TestSerialize_DataURI(t *testing.T) {
	data := []byte("hello world")
	result, err := SerializeData(data, FormatBytes, "datauri", "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "data:text/plain;base64,") {
		t.Errorf("expected data URI prefix, got %q", result)
	}
}

func TestSerialize_DataURI_NoMIME_Fails(t *testing.T) {
	data := []byte("hello")
	_, err := SerializeData(data, FormatBytes, "datauri", "")
	if err == nil {
		t.Fatal("expected error without MIME type")
	}
}

func TestSerialize_NumericFormatSpec_Float(t *testing.T) {
	result, err := SerializeData(3.14159, FormatJSON, ".2f", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "3.14" {
		t.Errorf("expected '3.14', got %q", result)
	}
}

func TestSerialize_NumericFormatSpec_Percentage(t *testing.T) {
	result, err := SerializeData(0.856, FormatJSON, ".1%", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "85.6%" {
		t.Errorf("expected '85.6%%', got %q", result)
	}
}

func TestSerialize_UnknownFormat_DefaultsToText(t *testing.T) {
	result, err := SerializeData("hello", FormatString, "unknown_format", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestSerialize_EmptyFormat(t *testing.T) {
	result, err := SerializeData("hello", FormatString, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestSerialize_DefaultFormat(t *testing.T) {
	result, err := SerializeData("hello", FormatString, "text", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestSerialize_ConversionError(t *testing.T) {
	result, err := SerializeData([]byte{0xff, 0xfe}, FormatBytes, "json", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(result, "Serialization Error") {
		t.Errorf("expected error message in result, got %q", result)
	}
}

func TestSerialize_NumericFormatSpec_Integer(t *testing.T) {
	result, err := SerializeData(42, FormatJSON, ".2f", "")
	if err != nil {
		t.Fatal(err)
	}
	if result != "42.00" {
		t.Errorf("expected '42.00', got %q", result)
	}
}

func TestSerialize_DataURI_FromString(t *testing.T) {
	result, err := SerializeData("hello", FormatString, "datauri", "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, "data:text/plain;base64,") {
		t.Errorf("expected data URI, got %q", result)
	}
}
