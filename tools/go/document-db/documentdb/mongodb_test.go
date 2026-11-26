// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"testing"
)

func TestDatabaseFromURI(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "standard URI",
			uri:  "mongodb://user:pass@localhost:27017/mydb",
			want: "mydb",
		},
		{
			name: "URI with options",
			uri:  "mongodb://user:pass@host:27017/testdb?retryWrites=true&w=majority",
			want: "testdb",
		},
		{
			name: "srv URI",
			uri:  "mongodb+srv://user:pass@cluster.mongodb.net/production",
			want: "production",
		},
		{
			name: "no database in path",
			uri:  "mongodb://localhost:27017/",
			want: "",
		},
		{
			name: "no path at all",
			uri:  "mongodb://localhost:27017",
			want: "",
		},
		{
			name: "multiple hosts",
			uri:  "mongodb://host1:27017,host2:27017,host3:27017/mydb?replicaSet=rs0",
			want: "mydb",
		},
		{
			name: "no user info",
			uri:  "mongodb://localhost:27017/testdb",
			want: "testdb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := databaseFromURI(tt.uri)
			if got != tt.want {
				t.Errorf("databaseFromURI(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestNewMongoBackend_Config(t *testing.T) {
	config := map[string]any{
		"connection_string": "mongodb://localhost:27017/testdb",
		"database":          "override_db",
		"collection":        "users",
	}
	mb := newMongoBackend(config)
	if mb.connStr != "mongodb://localhost:27017/testdb" {
		t.Errorf("connStr = %q, want %q", mb.connStr, "mongodb://localhost:27017/testdb")
	}
	if mb.database != "override_db" {
		t.Errorf("database = %q, want %q", mb.database, "override_db")
	}
	if mb.collection != "users" {
		t.Errorf("collection = %q, want %q", mb.collection, "users")
	}
}

func TestNewMongoBackend_ConnectRequiresConnectionString(t *testing.T) {
	mb := newMongoBackend(map[string]any{})
	err := mb.Connect(t.Context())
	if err == nil {
		t.Fatal("expected error for missing connection_string")
	}
}

func TestMongoExecuteQuery_MissingCollection(t *testing.T) {
	mb := newMongoBackend(map[string]any{
		"connection_string": "mongodb://localhost:27017/testdb",
	})
	_, err := mb.ExecuteQuery(t.Context(), map[string]any{
		"pipeline_str": "[]",
	})
	if err == nil {
		t.Fatal("expected error when 'collection' missing from tool_config")
	}
}

func TestMongoExecuteQuery_MissingPipeline(t *testing.T) {
	mb := newMongoBackend(map[string]any{
		"connection_string": "mongodb://localhost:27017/testdb",
		"collection":        "users",
	})
	_, err := mb.ExecuteQuery(t.Context(), map[string]any{
		"pipeline_str": "",
	})
	if err == nil {
		t.Fatal("expected error when pipeline_str is empty")
	}
}

func TestMongoExecuteQuery_InvalidPipelineJSON(t *testing.T) {
	mb := newMongoBackend(map[string]any{
		"connection_string": "mongodb://localhost:27017/testdb",
		"collection":        "users",
	})
	_, err := mb.ExecuteQuery(t.Context(), map[string]any{
		"pipeline_str": "{not valid json}",
	})
	if err == nil {
		t.Fatal("expected error on invalid pipeline JSON")
	}
}

func TestNormalizeBSON(t *testing.T) {
	input := map[string]any{
		"name":   "test",
		"age":    int32(25),
		"active": true,
	}
	result := normalizeBSON(input)
	if result["name"] != "test" {
		t.Errorf("name = %v, want %q", result["name"], "test")
	}
	if result["age"] != int32(25) {
		t.Errorf("age = %v, want 25", result["age"])
	}
	if result["active"] != true {
		t.Errorf("active = %v, want true", result["active"])
	}
}

func TestDerefString(t *testing.T) {
	if got := derefString(nil, "fallback"); got != "fallback" {
		t.Errorf("nil pointer = %q, want %q", got, "fallback")
	}
	s := "hello"
	if got := derefString(&s, "fallback"); got != "hello" {
		t.Errorf("non-nil pointer = %q, want %q", got, "hello")
	}
}
