// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"testing"
)

func TestDetectBackend_Elasticsearch(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "https URL defaults to elasticsearch",
			config: map[string]any{"connection_string": "https://localhost:9200"},
			want:   "elasticsearch",
		},
		{
			name:   "http URL defaults to elasticsearch",
			config: map[string]any{"connection_string": "http://localhost:9200"},
			want:   "elasticsearch",
		},
		{
			name:   "cloud_id",
			config: map[string]any{"cloud_id": "my-deployment:dXMtY2VudH..."},
			want:   "elasticsearch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectBackend(tt.config)
			if got != tt.want {
				t.Errorf("detectBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectBackend_OpenSearch(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "AWS OpenSearch Serverless",
			config: map[string]any{"connection_string": "https://abc123.us-east-1.aoss.amazonaws.com"},
			want:   "opensearch",
		},
		{
			name:   "AWS OpenSearch domain",
			config: map[string]any{"connection_string": "https://search-domain.us-east-1.es.amazonaws.com"},
			want:   "opensearch",
		},
		{
			name:   "AWS OpenSearch Serverless in the China partition",
			config: map[string]any{"connection_string": "https://abc123.cn-north-1.aoss.amazonaws.com.cn"},
			want:   "opensearch",
		},
		{
			name:   "AWS suffix outside the host",
			config: map[string]any{"connection_string": "https://search.example.com/.es.amazonaws.com"},
			want:   "elasticsearch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectBackend(tt.config)
			if got != tt.want {
				t.Errorf("detectBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectBackend_Empty(t *testing.T) {
	got := detectBackend(map[string]any{})
	if got != "" {
		t.Errorf("detectBackend() = %q, want empty", got)
	}
}

func TestDetectBackend_ExplicitOverride(t *testing.T) {
	config := map[string]any{
		"backend":           "opensearch",
		"connection_string": "https://localhost:9200",
	}
	backend, err := newBackend(config)
	if err != nil {
		t.Fatalf("newBackend() error: %v", err)
	}
	if _, ok := backend.(*openSearchBackend); !ok {
		t.Errorf("expected *openSearchBackend, got %T", backend)
	}
}

func TestNewBackend_Unsupported(t *testing.T) {
	config := map[string]any{"backend": "solr"}
	_, err := newBackend(config)
	if err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}

func TestNewBackend_NoConfig(t *testing.T) {
	_, err := newBackend(map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestConfigString(t *testing.T) {
	config := map[string]any{"key": "value"}
	if got := configString(config, "key", "default"); got != "value" {
		t.Errorf("configString() = %q, want %q", got, "value")
	}
	if got := configString(config, "missing", "default"); got != "default" {
		t.Errorf("configString() = %q, want %q", got, "default")
	}
}

func TestConfigBool(t *testing.T) {
	config := map[string]any{"flag": true}
	if got := configBool(config, "flag", false); got != true {
		t.Errorf("configBool() = %v, want true", got)
	}
	if got := configBool(config, "missing", true); got != true {
		t.Errorf("configBool() = %v, want true", got)
	}
}

func TestConfigInt(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want int
	}{
		{"float64", float64(42), 42},
		{"int", int(42), 42},
		{"int64", int64(42), 42},
		{"string_returns_default", "not a number", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]any{"key": tt.val}
			if got := configInt(config, "key", 10); got != tt.want {
				t.Errorf("configInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDynamicSchema_NoBackend(t *testing.T) {
	result := dynamicSchema(map[string]any{})
	if result != nil {
		t.Errorf("expected nil for empty config, got %+v", result)
	}
}

func TestDynamicSchema_ToolNameOverride(t *testing.T) {
	config := map[string]any{
		"backend":            "elasticsearch",
		"connection_string":  "https://localhost:9200",
		"tool_name":          "search_products",
		"tool_description":   "Search the product catalog",
		"auto_detect_schema": false,
	}
	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil SchemaOverride")
	}
	if result.Name != "search_products" {
		t.Errorf("Name = %q, want %q", result.Name, "search_products")
	}
	if result.Description != "Search the product catalog" {
		t.Errorf("Description = %q, want %q", result.Description, "Search the product catalog")
	}
}

func TestDynamicSchema_SchemaSummaryOverride(t *testing.T) {
	config := map[string]any{
		"backend":                 "elasticsearch",
		"connection_string":       "https://localhost:9200",
		"auto_detect_schema":      false,
		"schema_summary_override": "products index with title and price fields",
	}
	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil SchemaOverride")
	}
	if result.Instructions == "" {
		t.Fatal("expected instructions to be set")
	}
	if !contains(result.Instructions, "products index with title and price fields") {
		t.Error("expected schema summary in instructions")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
