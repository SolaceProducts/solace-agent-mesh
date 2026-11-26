// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
)

func TestLoadConnectorTypeDefinitions(t *testing.T) {
	t.Parallel()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("LoadConnectorTypeDefinitions() error: %v", err)
	}

	if len(defs) != 11 {
		t.Fatalf("expected 11 connector types, got %d", len(defs))
	}

	typeMap := make(map[string]connectorTypeDef)
	for _, d := range defs {
		typeMap[d.Type] = d
	}

	for _, expected := range []string{"sql", "mcp", "knowledge_base", "api", "document_db", "graph_db", "search", "slack", "email", "event_mesh", "web_search"} {
		if _, ok := typeMap[expected]; !ok {
			t.Errorf("missing connector type: %s", expected)
		}
	}

	if got := len(typeMap["sql"].Subtypes); got != 5 {
		t.Errorf("sql: expected 5 subtypes, got %d", got)
	}

	if got := len(typeMap["document_db"].Subtypes); got != 2 {
		t.Errorf("document_db: expected 2 subtypes, got %d", got)
	}

	if got := len(typeMap["graph_db"].Subtypes); got != 2 {
		t.Errorf("graph_db: expected 2 subtypes, got %d", got)
	}

	if got := len(typeMap["search"].Subtypes); got != 2 {
		t.Errorf("search: expected 2 subtypes, got %d", got)
	}

	if mcp, ok := typeMap["mcp"]; ok {
		if len(mcp.Subtypes) == 0 {
			t.Fatal("mcp: expected at least 1 subtype")
		}
		if mcp.Subtypes[0].UsageGuidance == "" {
			t.Error("mcp remote: expected non-empty usage_guidance")
		}
	}
}

func TestLoadConnectorSchemas(t *testing.T) {
	t.Parallel()
	schemas, err := LoadConnectorSchemas()
	if err != nil {
		t.Fatalf("LoadConnectorSchemas() error: %v", err)
	}

	if len(schemas) != 11 {
		t.Fatalf("expected 11 schemas, got %d", len(schemas))
	}

	typeMap := make(map[string]ConnectorSchemaResponse)
	for _, s := range schemas {
		typeMap[s.Type] = s
	}

	sql := typeMap["sql"]
	if len(sql.Subtypes) != 5 {
		t.Fatalf("sql: expected 5 subtypes, got %d", len(sql.Subtypes))
	}
	postgres := sql.Subtypes[0]
	if postgres.Subtype != "postgres" {
		t.Errorf("expected first sql subtype to be postgres, got %s", postgres.Subtype)
	}
	if len(postgres.FieldSchema) != 6 {
		t.Errorf("postgres: expected 6 fields, got %d", len(postgres.FieldSchema))
	}

	portField, ok := postgres.FieldSchema["port"]
	if !ok {
		t.Fatal("postgres: missing 'port' field")
	}
	if portField.Default == nil {
		t.Error("postgres port: expected default value")
	}
	if portField.Validation == nil || portField.Validation.Min == nil || *portField.Validation.Min != 1 {
		t.Error("postgres port: expected min validation of 1")
	}

	mcp := typeMap["mcp"]
	if len(mcp.Subtypes) != 1 {
		t.Fatalf("mcp: expected 1 subtype, got %d", len(mcp.Subtypes))
	}
	remote := mcp.Subtypes[0]
	hasConditionalDisplay := false
	for _, f := range remote.FieldSchema {
		if len(f.ConditionalDisplay) > 0 {
			hasConditionalDisplay = true
			break
		}
	}
	if !hasConditionalDisplay {
		t.Error("mcp remote: expected at least one field with conditionalDisplay")
	}

	api := typeMap["api"]
	if len(api.Subtypes) != 1 {
		t.Fatalf("api: expected 1 subtype, got %d", len(api.Subtypes))
	}
	openapi := api.Subtypes[0]
	hasConditionalDisplay = false
	for _, f := range openapi.FieldSchema {
		if len(f.ConditionalDisplay) > 0 {
			hasConditionalDisplay = true
			break
		}
	}
	if !hasConditionalDisplay {
		t.Error("api openapi: expected at least one field with conditionalDisplay")
	}
}

func TestLoadConnectorSchemas_BackwardCompatibility(t *testing.T) {
	t.Parallel()
	schemas, err := LoadConnectorSchemas()
	if err != nil {
		t.Fatalf("LoadConnectorSchemas() error: %v", err)
	}

	typeMap := make(map[string]ConnectorSchemaResponse)
	for _, s := range schemas {
		typeMap[s.Type] = s
	}

	postgres := typeMap["sql"].Subtypes[0]
	for _, name := range []string{"hostname", "port", "database", "username", "password"} {
		if _, ok := postgres.FieldSchema[name]; !ok {
			t.Errorf("postgres: missing field %s", name)
		}
	}

	remote := typeMap["mcp"].Subtypes[0]
	for _, name := range []string{"server_url", "connection_type", "auth_type"} {
		if _, ok := remote.FieldSchema[name]; !ok {
			t.Errorf("mcp remote: missing field %s", name)
		}
	}

	bedrock := typeMap["knowledge_base"].Subtypes[0]
	for _, name := range []string{"kb_id", "region", "auth_type", "aws_access_key_id", "aws_secret_access_key"} {
		if _, ok := bedrock.FieldSchema[name]; !ok {
			t.Errorf("bedrock: missing field %s", name)
		}
	}

	openapi := typeMap["api"].Subtypes[0]
	for _, name := range []string{"specification_url", "auth_type", "base_url"} {
		if _, ok := openapi.FieldSchema[name]; !ok {
			t.Errorf("api openapi: missing field %s", name)
		}
	}

	if pw, ok := postgres.FieldSchema["password"]; ok {
		if !pw.Secret {
			t.Error("postgres password: expected secret=true")
		}
	}
}

func TestLoadConnectorSchemas_ExperimentalIsPerSubtype(t *testing.T) {
	t.Parallel()
	schemas, err := LoadConnectorSchemas()
	if err != nil {
		t.Fatalf("load connector schemas: %v", err)
	}
	got := map[string]bool{}
	for _, s := range schemas {
		for _, st := range s.Subtypes {
			got[s.Type+"/"+st.Subtype] = st.Experimental
		}
	}
	want := map[string]bool{
		"document_db/mongodb":  false,
		"document_db/dynamodb": true,
		"graph_db/neo4j":       true,
		"graph_db/neptune":     true,
		"search/opensearch":    true,
		"search/elasticsearch": true,
	}
	for key, experimental := range want {
		reported, ok := got[key]
		if !ok {
			t.Errorf("%s is missing from the connector schemas", key)
			continue
		}
		if reported != experimental {
			t.Errorf("%s experimental = %v, want %v", key, reported, experimental)
		}
	}
}
