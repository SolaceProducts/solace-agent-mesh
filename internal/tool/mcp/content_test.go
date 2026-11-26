// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestDetectTextFormat(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantFormat TextFormat
		wantMIME   string
	}{
		{
			name:       "json object",
			input:      `{"key": "value", "num": 42}`,
			wantFormat: TextFormatJSON,
			wantMIME:   "application/json",
		},
		{
			name:       "json array",
			input:      `[1, 2, 3]`,
			wantFormat: TextFormatJSON,
			wantMIME:   "application/json",
		},
		{
			name:       "csv data",
			input:      "name,age,city\nAlice,30,NYC\nBob,25,LA\n",
			wantFormat: TextFormatCSV,
			wantMIME:   "text/csv",
		},
		{
			name:       "yaml data",
			input:      "name: test\nversion: 1.0\ndescription: a test\n",
			wantFormat: TextFormatYAML,
			wantMIME:   "application/yaml",
		},
		{
			name:       "markdown",
			input:      "# Title\n\n## Section\n\n- item 1\n- item 2\n",
			wantFormat: TextFormatMarkdown,
			wantMIME:   "text/markdown",
		},
		{
			name:       "plain text",
			input:      "This is just plain text with no special formatting.",
			wantFormat: TextFormatPlain,
			wantMIME:   "text/plain",
		},
		{
			name:       "empty",
			input:      "",
			wantFormat: TextFormatPlain,
			wantMIME:   "text/plain",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			format, mime := DetectTextFormat(tc.input)
			if format != tc.wantFormat {
				t.Errorf("format = %q, want %q", format, tc.wantFormat)
			}
			if mime != tc.wantMIME {
				t.Errorf("mime = %q, want %q", mime, tc.wantMIME)
			}
		})
	}
}

func TestExtractContentItems_PlainText(t *testing.T) {
	items := extractContentItems("Hello, world!", "test_tool")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Type != ContentTypeText {
		t.Errorf("type = %q, want text", items[0].Type)
	}
	if items[0].Text != "Hello, world!" {
		t.Errorf("text = %q", items[0].Text)
	}
}

func TestExtractContentItems_MCPContentArray(t *testing.T) {
	content := []map[string]any{
		{
			"type": "text",
			"text": `{"result": "success"}`,
		},
		{
			"type":     "image",
			"data":     base64.StdEncoding.EncodeToString([]byte("fake-png")),
			"mimeType": "image/png",
		},
	}

	b, _ := json.Marshal(content)
	items := extractContentItems(string(b), "test_tool")

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	if items[0].Type != ContentTypeText {
		t.Errorf("item[0].type = %q, want text", items[0].Type)
	}
	if items[0].Metadata["detected_format"] != "json" {
		t.Errorf("item[0] format = %q, want json", items[0].Metadata["detected_format"])
	}

	if items[1].Type != ContentTypeImage {
		t.Errorf("item[1].type = %q, want image", items[1].Type)
	}
	if items[1].MIMEType != "image/png" {
		t.Errorf("item[1].mime = %q, want image/png", items[1].MIMEType)
	}
	if string(items[1].Data) != "fake-png" {
		t.Errorf("item[1].data = %q", items[1].Data)
	}
}

func TestExtractContentItems_ResourceContent(t *testing.T) {
	content := []map[string]any{
		{
			"type": "resource",
			"resource": map[string]any{
				"uri":      "file:///tmp/data.csv",
				"text":     "a,b\n1,2\n",
				"mimeType": "text/csv",
			},
		},
	}

	b, _ := json.Marshal(content)
	items := extractContentItems(string(b), "test_tool")

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Type != ContentTypeResource {
		t.Errorf("type = %q, want resource", items[0].Type)
	}
	if items[0].Filename != "data.csv" {
		t.Errorf("filename = %q, want data.csv", items[0].Filename)
	}
	if items[0].URI != "file:///tmp/data.csv" {
		t.Errorf("uri = %q", items[0].URI)
	}
}

func TestExtractFilenameFromURI(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"file:///tmp/data.csv", "data.csv"},
		{"https://example.com/path/to/file.json", "file.json"},
		{"", ""},
		{"file:///", ""},
	}

	for _, tc := range tests {
		got := extractFilenameFromURI(tc.uri)
		if got != tc.want {
			t.Errorf("extractFilenameFromURI(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

func TestGenerateFilename(t *testing.T) {
	got := generateFilename("my_tool", "json", ".json", 0)
	if !strings.HasPrefix(got, "my_tool_json_0_") || !strings.HasSuffix(got, ".json") {
		t.Errorf("got %q, want my_tool_json_0_*.json", got)
	}
	got2 := generateFilename("my_tool", string(ContentTypeImage), ".csv", 2)
	if !strings.HasPrefix(got2, "my_tool_image_2_") || !strings.HasSuffix(got2, ".csv") {
		t.Errorf("got %q, want my_tool_image_2_*.csv", got2)
	}
}

func TestFormatExtension(t *testing.T) {
	tests := []struct {
		format TextFormat
		want   string
	}{
		{TextFormatJSON, ".json"},
		{TextFormatCSV, ".csv"},
		{TextFormatYAML, ".yaml"},
		{TextFormatMarkdown, ".md"},
		{TextFormatPlain, ".txt"},
	}
	for _, tc := range tests {
		if got := FormatExtension(tc.format); got != tc.want {
			t.Errorf("FormatExtension(%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}

func TestMimeExtension(t *testing.T) {
	tests := []struct {
		mime string
		want string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"audio/wav", ".wav"},
		{"audio/mp3", ".mp3"},
		{"application/octet-stream", ".bin"},
	}
	for _, tc := range tests {
		if got := mimeExtension(tc.mime); got != tc.want {
			t.Errorf("mimeExtension(%q) = %q, want %q", tc.mime, got, tc.want)
		}
	}
}

func TestIsCSV(t *testing.T) {
	if !isCSV("a,b,c\n1,2,3\n4,5,6\n") {
		t.Error("expected CSV detection for valid CSV")
	}
	if isCSV("just a single line") {
		t.Error("single line should not be CSV")
	}
	if isCSV("line one\nline two\nline three\n") {
		t.Error("lines without commas should not be CSV")
	}
}

func TestIsJSON(t *testing.T) {
	if !isJSON(`{"key": "value"}`) {
		t.Error("expected JSON for object")
	}
	if !isJSON(`[1, 2, 3]`) {
		t.Error("expected JSON for array")
	}
	if isJSON("not json") {
		t.Error("plain text should not be JSON")
	}
	if isJSON("") {
		t.Error("empty string should not be JSON")
	}
}

func TestDetectTextFormat_JSONWithUnescapedControlChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "object with literal newline in value",
			input: "{\"description\": \"line one\nline two\", \"id\": 42}",
		},
		{
			name:  "array of objects with literal newlines",
			input: "[{\"k\": \"a\nb\"}, {\"k\": \"c\nd\"}]",
		},
		{
			name:  "literal tab in value",
			input: "{\"k\": \"col1\tcol2\"}",
		},
		{
			name:  "markdown-looking link inside string with newline",
			input: "{\"self\": \"https://x.example/api\", \"body\": \"see [docs](https://x.example/d)\nfor details\"}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			format, mime := DetectTextFormat(tc.input)
			if format != TextFormatJSON {
				t.Errorf("format = %q, want json", format)
			}
			if mime != "application/json" {
				t.Errorf("mime = %q, want application/json", mime)
			}
		})
	}
}

func TestDetectTextFormat_NonJSONUnaffected(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantFormat TextFormat
	}{
		{
			name:       "csv unaffected",
			input:      "name,age\nAlice,30\nBob,25\n",
			wantFormat: TextFormatCSV,
		},
		{
			name:       "yaml unaffected",
			input:      "name: test\nversion: 1.0\n",
			wantFormat: TextFormatYAML,
		},
		{
			name:       "markdown unaffected",
			input:      "# Title\n\n- item\n- item\n",
			wantFormat: TextFormatMarkdown,
		},
		{
			name:       "plain unaffected",
			input:      "Just a single sentence.",
			wantFormat: TextFormatPlain,
		},
		{
			name:       "non-json starting brace stays plain",
			input:      "{not json at all",
			wantFormat: TextFormatPlain,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			format, _ := DetectTextFormat(tc.input)
			if format != tc.wantFormat {
				t.Errorf("format = %q, want %q", format, tc.wantFormat)
			}
		})
	}
}

func TestLooseJSONUnmarshal_PreservesValues(t *testing.T) {
	input := []byte("{\"k\": \"line one\nline two\", \"n\": 7}")
	var v map[string]any
	if err := looseJSONUnmarshal(input, &v); err != nil {
		t.Fatalf("looseJSONUnmarshal: %v", err)
	}
	if got := v["k"]; got != "line one\nline two" {
		t.Errorf("k = %q, want line-broken string", got)
	}
	if got := v["n"]; got != float64(7) {
		t.Errorf("n = %v, want 7", got)
	}
}

func TestEscapeUnescapedJSONControlChars_OnlyInStrings(t *testing.T) {
	in := []byte("{\n  \"k\": \"a\nb\"\n}")
	out, changed := escapeUnescapedJSONControlChars(in)
	if !changed {
		t.Fatal("expected changed=true")
	}
	want := []byte("{\n  \"k\": \"a\\u000ab\"\n}")
	if string(out) != string(want) {
		t.Errorf("out = %q, want %q", out, want)
	}
}

func TestEscapeUnescapedJSONControlChars_RespectsEscapes(t *testing.T) {
	in := []byte(`{"k":"a\"b","m":"c\nd"}`)
	out, changed := escapeUnescapedJSONControlChars(in)
	if changed {
		t.Errorf("changed=true, expected false; out=%q", out)
	}
}

func TestEscapeUnescapedJSONControlChars_BackslashThenControl(t *testing.T) {
	in := []byte("{\"k\": \"a\\\nb\"}")
	out, changed := escapeUnescapedJSONControlChars(in)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if !json.Valid(out) {
		t.Errorf("sanitized output is not valid JSON: %q", out)
	}
}

func TestEscapeUnescapedJSONControlChars_PreservesEscapesAndUTF8(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{
			name: "surrogate pair preserved",
			in:   `{"emoji": "😀"}`,
		},
		{
			name: "multi-byte utf-8 in string",
			in:   "{\"k\": \"héllo 世界 🚀\"}",
		},
		{
			name: "valid escapes pass through",
			in:   `{"k": "tab\there\nnewline\\backslash\"quote"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := escapeUnescapedJSONControlChars([]byte(tc.in))
			if changed {
				t.Errorf("changed=true, expected false; out=%q", out)
			}
			var v any
			if err := json.Unmarshal([]byte(tc.in), &v); err != nil {
				t.Errorf("input does not parse strictly: %v", err)
			}
		})
	}
}

func TestEscapeUnescapedJSONControlChars_LeavesOutsideStringWhitespace(t *testing.T) {
	in := []byte("{\n\t\"k\": \"v\"\n}")
	out, changed := escapeUnescapedJSONControlChars(in)
	if changed {
		t.Errorf("changed=true for outside-string whitespace; out=%q", out)
	}
}

func TestLooseJSONUnmarshal_UnterminatedStringFallsBack(t *testing.T) {
	input := []byte(`{"k": "trailing\`)
	var v any
	if err := looseJSONUnmarshal(input, &v); err == nil {
		t.Errorf("expected parse error, got nil; v=%v", v)
	}
}

func TestExtractContentItems_JSONStoredAsValidBytes(t *testing.T) {
	input := "{\"issues\": [{\"description\": \"line one\nline two\\\nline three\"}]}"
	items := extractContentItems(input, "test_tool")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].MIMEType != "application/json" {
		t.Fatalf("mime=%q, want application/json", items[0].MIMEType)
	}
	if !json.Valid([]byte(items[0].Text)) {
		t.Errorf("stored text is not valid JSON:\n%s", items[0].Text)
	}
}
