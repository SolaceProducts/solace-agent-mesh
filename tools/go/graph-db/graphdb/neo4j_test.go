// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"testing"
)

func TestNewNeo4jBackend_Config(t *testing.T) {
	config := map[string]any{
		"connection_string": "bolt://localhost:7687",
		"username":          "neo4j",
		"password":          "password",
		"database":          "mydb",
	}
	nb := newNeo4jBackend(config)
	if nb.connStr != "bolt://localhost:7687" {
		t.Errorf("connStr = %q", nb.connStr)
	}
	if nb.username != "neo4j" {
		t.Errorf("username = %q", nb.username)
	}
	if nb.database != "mydb" {
		t.Errorf("database = %q", nb.database)
	}
}

func TestNewNeo4jBackend_DefaultDatabase(t *testing.T) {
	nb := newNeo4jBackend(map[string]any{
		"connection_string": "bolt://localhost:7687",
	})
	if nb.database != "neo4j" {
		t.Errorf("default database = %q, want %q", nb.database, "neo4j")
	}
}

func TestNewNeo4jBackend_ConnectRequiresConnectionString(t *testing.T) {
	nb := newNeo4jBackend(map[string]any{})
	err := nb.Connect(t.Context())
	if err == nil {
		t.Fatal("expected error for missing connection_string")
	}
}

func TestEscapeCypher(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Person", "Person"},
		{"My`Label", "My``Label"},
		{"normal_label", "normal_label"},
	}
	for _, tt := range tests {
		got := escapeCypher(tt.input)
		if got != tt.want {
			t.Errorf("escapeCypher(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNeo4jTypeName(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"string", "hello", "STRING"},
		{"int64", int64(42), "INTEGER"},
		{"float64", float64(3.14), "FLOAT"},
		{"bool", true, "BOOLEAN"},
		{"nil", nil, "NULL"},
		{"list", []any{1, 2}, "LIST"},
		{"map", map[string]any{"a": 1}, "MAP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := neo4jTypeName(tt.val); got != tt.want {
				t.Errorf("neo4jTypeName(%v) = %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

func TestDominantType(t *testing.T) {
	tests := []struct {
		name   string
		counts map[string]int
		want   string
	}{
		{"single", map[string]int{"STRING": 10}, "STRING"},
		{"majority", map[string]int{"STRING": 8, "INTEGER": 2}, "STRING"},
		{"tie_alphabetical", map[string]int{"INTEGER": 5, "STRING": 5}, "INTEGER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dominantType(tt.counts); got != tt.want {
				t.Errorf("dominantType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectEnumValues(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		vals    map[string]struct{}
		total   int
		hasEnum bool
	}{
		{
			name:    "enum by name pattern",
			field:   "status",
			vals:    map[string]struct{}{"active": {}, "inactive": {}, "pending": {}},
			total:   100,
			hasEnum: true,
		},
		{
			name:    "enum by low cardinality",
			field:   "color",
			vals:    map[string]struct{}{"red": {}, "blue": {}},
			total:   100,
			hasEnum: true,
		},
		{
			name:    "not enum high cardinality",
			field:   "name",
			vals:    makeNStrings(50),
			total:   100,
			hasEnum: false,
		},
		{
			name:    "single value not enum",
			field:   "status",
			vals:    map[string]struct{}{"active": {}},
			total:   10,
			hasEnum: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectEnumValues(tt.field, tt.vals, tt.total)
			if tt.hasEnum && result == nil {
				t.Error("expected enum values, got nil")
			}
			if !tt.hasEnum && result != nil {
				t.Errorf("expected nil, got %v", result)
			}
		})
	}
}

func TestLooksLikeEnumField(t *testing.T) {
	if !looksLikeEnumField("status") {
		t.Error("expected status to look like enum")
	}
	if !looksLikeEnumField("user_role") {
		t.Error("expected user_role to look like enum")
	}
	if looksLikeEnumField("name") {
		t.Error("expected name to not look like enum")
	}
}

func makeNStrings(n int) map[string]struct{} {
	m := make(map[string]struct{}, n)
	for i := range n {
		m[string(rune('A'+i%26))+string(rune('a'+i/26))] = struct{}{}
	}
	return m
}
