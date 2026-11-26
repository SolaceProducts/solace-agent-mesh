// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"testing"
)

func TestDetectBackend_Neo4j(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "bolt URI",
			config: map[string]any{"connection_string": "bolt://localhost:7687"},
			want:   "neo4j",
		},
		{
			name:   "bolt+s URI",
			config: map[string]any{"connection_string": "bolt+s://localhost:7687"},
			want:   "neo4j",
		},
		{
			name:   "neo4j URI",
			config: map[string]any{"connection_string": "neo4j://localhost:7687"},
			want:   "neo4j",
		},
		{
			name:   "neo4j+s URI (Aura)",
			config: map[string]any{"connection_string": "neo4j+s://xxxx.databases.neo4j.io"},
			want:   "neo4j",
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

func TestDetectBackend_Neptune(t *testing.T) {
	config := map[string]any{
		"connection_string": "bolt+s://my-cluster.us-east-1.neptune.amazonaws.com:8182",
	}
	got := detectBackend(config)
	if got != "neptune" {
		t.Errorf("detectBackend() = %q, want %q", got, "neptune")
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
		"backend":           "neptune",
		"connection_string": "bolt://localhost:7687",
	}
	backend, err := newBackend(config)
	if err != nil {
		t.Fatalf("newBackend() error: %v", err)
	}
	if _, ok := backend.(*neptuneBackend); !ok {
		t.Errorf("expected *neptuneBackend, got %T", backend)
	}
}

func TestNewBackend_Unsupported(t *testing.T) {
	_, err := newBackend(map[string]any{"backend": "arangodb"})
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

func TestIsReadQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"MATCH (n) RETURN n", true},
		{"match (n) return n", true},
		{"  MATCH (n:Person) WHERE n.name = 'Alice' RETURN n", true},
		{"RETURN 1", true},
		{"CALL db.labels()", true},
		{"SHOW INDEXES", true},
		{"EXPLAIN MATCH (n) RETURN n", true},
		{"PROFILE MATCH (n) RETURN n", true},
		{"UNWIND [1,2,3] AS x RETURN x", true},
		{"CREATE (n:Person {name: 'Alice'})", false},
		{"MERGE (n:Person {name: 'Alice'})", false},
		{"DELETE n", false},
		{"SET n.name = 'Bob'", false},
		{"REMOVE n.name", false},
		{"DROP CONSTRAINT", false},
		{"MATCH (c:Company {name: 'Globex'}) CREATE (p:Person)-[:WORKS_AT]->(c)", false},
		{"MATCH (n:Person {name: 'Alice'}) SET n.role = 'Lead'", false},
		{"MATCH (n:Person {name: 'Bob'}) DETACH DELETE n", false},
		{"MATCH (n) WHERE n.name = 'created' RETURN n", true},
		{"MATCH (n:Asset) RETURN n", true},
		{"", true},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			if got := isReadQuery(tt.query); got != tt.want {
				t.Errorf("isReadQuery(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
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
	if got := configBool(config, "flag", false); !got {
		t.Error("expected true")
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
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestDynamicSchema_ToolNameOverride(t *testing.T) {
	config := map[string]any{
		"backend":            "neo4j",
		"connection_string":  "bolt://localhost:7687",
		"tool_name":          "query_knowledge_graph",
		"tool_description":   "Query the knowledge graph",
		"auto_detect_schema": false,
	}
	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Name != "query_knowledge_graph" {
		t.Errorf("Name = %q, want %q", result.Name, "query_knowledge_graph")
	}
	if result.Description != "Query the knowledge graph" {
		t.Errorf("Description = %q, want %q", result.Description, "Query the knowledge graph")
	}
}
