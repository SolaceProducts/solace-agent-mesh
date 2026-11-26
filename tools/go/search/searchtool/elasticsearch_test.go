// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFlattenMappings(t *testing.T) {
	raw := `{
		"title": {"type": "text", "analyzer": "standard"},
		"price": {"type": "float"},
		"status": {"type": "keyword"},
		"metadata": {
			"properties": {
				"created_at": {"type": "date"},
				"author": {"type": "keyword"}
			}
		}
	}`

	var properties map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &properties); err != nil {
		t.Fatal(err)
	}

	out := make(map[string]*mapping)
	flattenMappings("", properties, out)

	tests := []struct {
		field    string
		wantType string
	}{
		{"title", "text"},
		{"price", "float"},
		{"status", "keyword"},
		{"metadata.created_at", "date"},
		{"metadata.author", "keyword"},
	}

	for _, tt := range tests {
		m, ok := out[tt.field]
		if !ok {
			t.Errorf("field %q not found in output", tt.field)
			continue
		}
		if m.Type != tt.wantType {
			t.Errorf("field %q type = %q, want %q", tt.field, m.Type, tt.wantType)
		}
	}

	if m := out["title"]; m.Analyzer == nil || *m.Analyzer != "standard" {
		t.Error("expected title analyzer = standard")
	}

	if _, ok := out["metadata"]; ok {
		t.Error("nested object 'metadata' should not appear as a leaf mapping")
	}
}

func TestFlattenMappings_Empty(t *testing.T) {
	out := make(map[string]*mapping)
	flattenMappings("", map[string]json.RawMessage{}, out)
	if len(out) != 0 {
		t.Errorf("expected empty output, got %d entries", len(out))
	}
}

func TestCollectKeywordFields(t *testing.T) {
	mappings := map[string]*mapping{
		"title":  {Type: "text"},
		"status": {Type: "keyword"},
		"region": {Type: "keyword"},
		"price":  {Type: "float"},
	}

	fields := collectKeywordFields(mappings)
	if len(fields) != 2 {
		t.Fatalf("expected 2 keyword fields, got %d", len(fields))
	}
	if fields[0] != "region" || fields[1] != "status" {
		t.Errorf("expected [region, status], got %v", fields)
	}
}

func TestReadBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"short", "hello", "hello"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.body)
			got := readBody(r)
			if got != tt.want {
				t.Errorf("readBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchResponseParsing(t *testing.T) {
	raw := `{
		"hits": {
			"total": {"value": 42},
			"hits": [
				{"_id": "1", "_score": 1.5, "_source": {"title": "Test"}},
				{"_id": "2", "_score": 1.2, "_source": {"title": "Other"}}
			]
		},
		"aggregations": {
			"by_status": {"buckets": [{"key": "active", "doc_count": 30}]}
		}
	}`

	var result searchResponse
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}

	if result.Hits.Total.Value != 42 {
		t.Errorf("total hits = %d, want 42", result.Hits.Total.Value)
	}
	if len(result.Hits.Hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(result.Hits.Hits))
	}
	if result.Hits.Hits[0].ID != "1" {
		t.Errorf("hit[0].ID = %q, want %q", result.Hits.Hits[0].ID, "1")
	}
	if result.Hits.Hits[0].Source["title"] != "Test" {
		t.Errorf("hit[0].source.title = %v, want %q", result.Hits.Hits[0].Source["title"], "Test")
	}
	if result.Aggregations == nil {
		t.Error("expected aggregations to be non-nil")
	}
}

func TestNewElasticsearchBackend_Config(t *testing.T) {
	config := map[string]any{
		"connection_string": "https://localhost:9200",
		"api_key":           "test-key",
	}
	eb := newElasticsearchBackend(config)
	if eb.config["connection_string"] != "https://localhost:9200" {
		t.Error("config not stored correctly")
	}
}
