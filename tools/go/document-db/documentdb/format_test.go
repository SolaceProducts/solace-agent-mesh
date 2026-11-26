// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatResults_JSONDefault(t *testing.T) {
	docs := []map[string]any{{"a": 1}, {"a": 2}}
	content, ext, mime, err := formatResults(docs, "")
	if err != nil {
		t.Fatalf("formatResults error: %v", err)
	}
	if ext != "json" {
		t.Errorf("ext = %q, want json", ext)
	}
	if mime != "text/json" {
		t.Errorf("mime = %q, want text/json", mime)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if len(parsed) != 2 {
		t.Errorf("parsed len = %d, want 2", len(parsed))
	}
}

func TestFormatResults_YAML(t *testing.T) {
	docs := []map[string]any{{"k": "v"}}
	content, ext, mime, err := formatResults(docs, "yaml")
	if err != nil {
		t.Fatalf("formatResults error: %v", err)
	}
	if ext != "yaml" {
		t.Errorf("ext = %q, want yaml", ext)
	}
	if mime != "text/yaml" {
		t.Errorf("mime = %q, want text/yaml", mime)
	}
	if !strings.Contains(string(content), "k:") {
		t.Errorf("yaml missing key: %q", string(content))
	}
}

func TestFormatResults_CSV(t *testing.T) {
	docs := []map[string]any{
		{"name": "alice", "age": 30},
		{"name": "bob", "age": 25},
	}
	content, ext, mime, err := formatResults(docs, "csv")
	if err != nil {
		t.Fatalf("formatResults error: %v", err)
	}
	if ext != "csv" {
		t.Errorf("ext = %q, want csv", ext)
	}
	if mime != "text/csv" {
		t.Errorf("mime = %q, want text/csv", mime)
	}
	s := string(content)
	if !strings.Contains(s, "name") || !strings.Contains(s, "age") {
		t.Errorf("csv missing headers: %q", s)
	}
	if !strings.Contains(s, "alice") || !strings.Contains(s, "bob") {
		t.Errorf("csv missing rows: %q", s)
	}
}

func TestFormatResults_CSVEmpty(t *testing.T) {
	content, _, _, err := formatResults(nil, "csv")
	if err != nil {
		t.Fatalf("formatResults error: %v", err)
	}
	if len(content) != 0 {
		t.Errorf("expected empty content, got %q", string(content))
	}
}

func TestFormatResults_Markdown(t *testing.T) {
	docs := []map[string]any{
		{"name": "alice", "age": 30},
	}
	content, ext, mime, err := formatResults(docs, "markdown")
	if err != nil {
		t.Fatalf("formatResults error: %v", err)
	}
	if ext != "md" {
		t.Errorf("ext = %q, want md", ext)
	}
	if mime != "text/md" {
		t.Errorf("mime = %q, want text/md", mime)
	}
	s := string(content)
	if !strings.Contains(s, "| name") || !strings.Contains(s, "| age") {
		t.Errorf("markdown missing headers: %q", s)
	}
	if !strings.Contains(s, "alice") {
		t.Errorf("markdown missing row: %q", s)
	}
	if !strings.Contains(s, "---") {
		t.Errorf("markdown missing separator: %q", s)
	}
}

func TestFormatResults_MarkdownEmpty(t *testing.T) {
	content, _, _, err := formatResults(nil, "markdown")
	if err != nil {
		t.Fatalf("formatResults error: %v", err)
	}
	if string(content) != "No results found." {
		t.Errorf("empty markdown content = %q, want %q", string(content), "No results found.")
	}
}

func TestFormatResults_UnknownDefaultsJSON(t *testing.T) {
	_, ext, mime, err := formatResults([]map[string]any{{"a": 1}}, "xml")
	if err != nil {
		t.Fatalf("formatResults error: %v", err)
	}
	if ext != "json" || mime != "text/json" {
		t.Errorf("unknown format should fall back to json; got ext=%q mime=%q", ext, mime)
	}
}

func TestUniqueSuffix(t *testing.T) {
	seen := map[string]struct{}{}
	for range 50 {
		s := uniqueSuffix()
		if len(s) != 8 {
			t.Errorf("suffix len = %d, want 8", len(s))
		}
		if _, dup := seen[s]; dup {
			t.Errorf("duplicate suffix %q", s)
		}
		seen[s] = struct{}{}
	}
}

func TestBuildArtifactDescription_Default(t *testing.T) {
	got := buildArtifactDescription("", "Results of MongoDB aggregation.", "Pipeline", "[{\"$match\":{}}]")
	if !strings.HasPrefix(got, "Results of MongoDB aggregation.") {
		t.Errorf("missing default prefix: %q", got)
	}
	if !strings.Contains(got, "Pipeline:") {
		t.Errorf("missing label: %q", got)
	}
}

func TestBuildArtifactDescription_CustomPrefix(t *testing.T) {
	got := buildArtifactDescription("user-supplied description", "Results of MongoDB aggregation.", "Pipeline", "[]")
	if !strings.HasPrefix(got, "user-supplied description") {
		t.Errorf("missing custom prefix: %q", got)
	}
	if strings.Contains(got, "Results of MongoDB aggregation.") {
		t.Errorf("default prefix should not appear when custom is supplied: %q", got)
	}
}

func TestBuildArtifactDescription_LongQueryTruncated(t *testing.T) {
	longQuery := strings.Repeat("q", maxQueryLenInDescription+50)
	got := buildArtifactDescription("", "Results.", "Label", longQuery)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected trailing '...' for truncated query, got %q", got[len(got)-10:])
	}
	if strings.Count(got, "q") != maxQueryLenInDescription {
		t.Errorf("truncated q count = %d, want %d", strings.Count(got, "q"), maxQueryLenInDescription)
	}
}

func TestClampInlineContent_WithinCap(t *testing.T) {
	inline, skipped := clampInlineContent("hello", 3, 10)
	if inline != "hello" {
		t.Errorf("inline = %q, want %q", inline, "hello")
	}
	if skipped {
		t.Errorf("expected skipped=false")
	}
}

func TestClampInlineContent_OverCap(t *testing.T) {
	inline, skipped := clampInlineContent("hello", 11, 10)
	if inline != "" {
		t.Errorf("inline = %q, want empty", inline)
	}
	if !skipped {
		t.Errorf("expected skipped=true")
	}
}

func TestCapFromConfig(t *testing.T) {
	if got := capFromConfig(map[string]any{}); got != defaultMaxInlineResults {
		t.Errorf("default cap = %d, want %d", got, defaultMaxInlineResults)
	}
	if got := capFromConfig(map[string]any{"max_inline_results": 42}); got != 42 {
		t.Errorf("override cap = %d, want 42", got)
	}
	if got := capFromConfig(map[string]any{"max_inline_results": -1}); got != defaultMaxInlineResults {
		t.Errorf("negative cap should fall back; got %d", got)
	}
}
