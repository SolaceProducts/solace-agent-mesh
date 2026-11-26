// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func mustMarshal(t *testing.T, v any) *string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	return &s
}

func createTestConnector(t *testing.T, db *store.DB, name, connType string, values map[string]any) string {
	t.Helper()
	repo := store.NewConnectorRepo(db)
	desc := "test connector for fixture purposes"
	subtype := "default"
	conn := &store.Connector{
		Name:        name,
		Description: &desc,
		Type:        connType,
		Subtype:     &subtype,
		Values:      mustMarshal(t, values),
		CreatedBy:   "test",
		UpdatedBy:   "test",
	}
	if err := repo.Create(t.Context(), conn); err != nil {
		t.Fatalf("create connector %s: %v", name, err)
	}
	return conn.ID
}

func TestValidateConnectorToolNames_DuplicateSQLToolNames(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "SQL-A", "sql", map[string]any{
		"tool_name": "my_query",
	})
	id2 := createTestConnector(t, db, "SQL-B", "sql", map[string]any{
		"tool_name": "my_query",
	})

	err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
	if err == nil {
		t.Fatal("expected duplicate tool name error, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if got := ve.Message; !strings.Contains(got, "Duplicate tool name detected: 'my_query'") {
		t.Errorf("unexpected message: %s", got)
	}
}

func TestValidateConnectorToolNames_SQLDefaultUniqueByDatabase(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "SQL-A", "sql", map[string]any{"database": "orders"})
	id2 := createTestConnector(t, db, "SQL-B", "sql", map[string]any{"database": "inventory"})

	err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
	if err != nil {
		t.Fatalf("expected no error — different database names produce unique tool names, got: %v", err)
	}
}

func TestValidateConnectorToolNames_SQLSameDbSameSuffix(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "SQL-A1", "sql", map[string]any{"database": "mydb"})
	id2 := createTestConnector(t, db, "SQL-B1", "sql", map[string]any{"database": "mydb"})

	conn1, _ := repo.GetByID(t.Context(), id1)
	conn2, _ := repo.GetByID(t.Context(), id2)
	suffix1 := connectorSuffix(conn1.ID)
	suffix2 := connectorSuffix(conn2.ID)

	if suffix1 == suffix2 {
		err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
		if err == nil {
			t.Fatal("expected duplicate error when same db + same prefix")
		}
	} else {
		err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
		if err != nil {
			t.Fatalf("different suffixes should produce unique names, got: %v", err)
		}
	}
}

func TestValidateConnectorToolNames_SQLSameExplicitName(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "SQL-A2", "sql", map[string]any{
		"tool_name": "execute_sql_query",
	})
	id2 := createTestConnector(t, db, "SQL-B2", "sql", map[string]any{
		"tool_name": "execute_sql_query",
	})

	err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
	if err == nil {
		t.Fatal("expected duplicate tool name error for explicit execute_sql_query, got nil")
	}
}

func TestValidateConnectorToolNames_MCPDuplicate(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "MCP-A", "mcp", map[string]any{
		"manifest": []map[string]any{
			{"name": "list_repos"},
			{"name": "get_issue"},
		},
	})
	id2 := createTestConnector(t, db, "MCP-B", "mcp", map[string]any{
		"manifest": []map[string]any{
			{"name": "list_repos"},
			{"name": "create_pr"},
		},
	})

	err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
	if err == nil {
		t.Fatal("expected duplicate MCP tool name error, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "list_repos") {
		t.Errorf("expected message to mention list_repos: %s", ve.Message)
	}
}

func TestValidateConnectorToolNames_MCPWithSelectedTools(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "MCP-A", "mcp", map[string]any{
		"manifest": []map[string]any{
			{"name": "list_repos"},
			{"name": "get_issue"},
		},
		"selected_tools": "get_issue",
	})
	id2 := createTestConnector(t, db, "MCP-B", "mcp", map[string]any{
		"manifest": []map[string]any{
			{"name": "list_repos"},
			{"name": "create_pr"},
		},
	})

	err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
	if err != nil {
		t.Fatalf("expected no error (selected_tools filters out list_repos from MCP-A), got: %v", err)
	}
}

func TestValidateConnectorToolNames_MCPPrefix(t *testing.T) {
	t.Parallel()
	manifest := []map[string]any{{"name": "search_datadog_logs"}, {"name": "get_datadog_metric"}}

	tests := []struct {
		name    string
		valuesA map[string]any
		valuesB map[string]any
		wantDup string
	}{
		{
			name:    "distinct prefixes",
			valuesA: map[string]any{"manifest": manifest, "tool_name_prefix": "us"},
			valuesB: map[string]any{"manifest": manifest, "tool_name_prefix": "eu"},
		},
		{
			name:    "one prefixed one bare",
			valuesA: map[string]any{"manifest": manifest},
			valuesB: map[string]any{"manifest": manifest, "tool_name_prefix": "eu"},
		},
		{
			name:    "same prefix",
			valuesA: map[string]any{"manifest": manifest, "tool_name_prefix": "eu"},
			valuesB: map[string]any{"manifest": manifest, "tool_name_prefix": "eu"},
			wantDup: "eu_search_datadog_logs",
		},
		{
			name:    "same prefix modulo trailing underscore",
			valuesA: map[string]any{"manifest": manifest, "tool_name_prefix": "eu"},
			valuesB: map[string]any{"manifest": manifest, "tool_name_prefix": "eu_"},
			wantDup: "eu_search_datadog_logs",
		},
		{
			name:    "names collide after sanitization",
			valuesA: map[string]any{"manifest": []map[string]any{{"name": "get-data"}}, "tool_name_prefix": "eu"},
			valuesB: map[string]any{"manifest": []map[string]any{{"name": "eu_get_data"}}},
			wantDup: "eu_get_data",
		},
		{
			name:    "prefixed name collides with bare name",
			valuesA: map[string]any{"manifest": manifest, "tool_name_prefix": "eu"},
			valuesB: map[string]any{"manifest": []map[string]any{{"name": "eu_get_datadog_metric"}}},
			wantDup: "eu_get_datadog_metric",
		},
		{
			name:    "selected_tools excludes unselected prefixed tool",
			valuesA: map[string]any{"manifest": manifest, "tool_name_prefix": "eu", "selected_tools": "search_datadog_logs"},
			valuesB: map[string]any{"manifest": []map[string]any{{"name": "eu_get_datadog_metric"}}},
		},
		{
			name:    "selected_tools matches bare name under prefix",
			valuesA: map[string]any{"manifest": manifest, "tool_name_prefix": "eu", "selected_tools": "search_datadog_logs"},
			valuesB: map[string]any{"manifest": []map[string]any{{"name": "eu_search_datadog_logs"}}},
			wantDup: "eu_search_datadog_logs",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := testOpenDB(t)
			repo := store.NewConnectorRepo(db)
			id1 := createTestConnector(t, db, "MCP-A", "mcp", tc.valuesA)
			id2 := createTestConnector(t, db, "MCP-B", "mcp", tc.valuesB)

			err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
			if tc.wantDup == "" {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
			}
			if !strings.Contains(ve.Message, "'"+tc.wantDup+"'") {
				t.Errorf("expected message to name %q: %s", tc.wantDup, ve.Message)
			}
		})
	}
}

func TestValidateConnectorToolNames_APICollidesWithSQLBareName(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "SQL-C", "sql", map[string]any{
		"tool_name": "get_users",
	})
	id2 := createTestConnector(t, db, "API-A", "api", map[string]any{
		"allow_list": "getUsers",
	})

	err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", nil)
	if err == nil {
		t.Fatal("expected collision — API connector's getUsers snake-cases to get_users which SQL also emits")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "get_users") {
		t.Errorf("expected message to mention get_users: %s", ve.Message)
	}
}

func TestValidateConnectorToolNames_BuiltinConflict(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "MCP-C", "mcp", map[string]any{
		"manifest": []map[string]any{
			{"name": "list_artifacts"},
		},
	})

	err := validateConnectorToolNames(t.Context(), []string{id1}, repo, "TestAgent", []string{"builtin_artifact_tools"})
	if err == nil {
		t.Fatal("expected conflict with builtin_artifact_tools, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "list_artifacts") {
		t.Errorf("expected message to mention list_artifacts: %s", ve.Message)
	}
	if !strings.Contains(ve.Message, "builtin toolset") {
		t.Errorf("expected message to mention builtin toolset: %s", ve.Message)
	}
}

func TestValidateConnectorToolNames_AllUnique(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "SQL-D", "sql", map[string]any{
		"tool_name": "query_orders",
	})
	id2 := createTestConnector(t, db, "MCP-D", "mcp", map[string]any{
		"manifest": []map[string]any{
			{"name": "list_repos"},
		},
	})

	err := validateConnectorToolNames(t.Context(), []string{id1, id2}, repo, "TestAgent", []string{"builtin_artifact_tools"})
	if err != nil {
		t.Fatalf("expected no error for unique tool names, got: %v", err)
	}
}

func TestValidateConnectorToolNames_EmptyConnectors(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	err := validateConnectorToolNames(t.Context(), nil, repo, "TestAgent", []string{"builtin_artifact_tools"})
	if err != nil {
		t.Fatalf("expected no error for empty connectors, got: %v", err)
	}
}

func TestValidateConnectorToolNames_ConnectorNotFound(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "SQL-E", "sql", map[string]any{
		"tool_name": "query_stuff",
	})

	err := validateConnectorToolNames(t.Context(), []string{"nonexistent-id", id1}, repo, "TestAgent", nil)
	if err != nil {
		t.Fatalf("expected no error when a connector is not found (graceful skip), got: %v", err)
	}
}

func TestValidateConnectorToolNames_KnowledgeBase(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "KB-A", "knowledge_base", map[string]any{})
	id2 := createTestConnector(t, db, "SQL-F", "sql", map[string]any{})

	conn1, _ := repo.GetByID(t.Context(), id1)
	expectedName := "query_bedrock_kb_" + connIDPrefix(conn1.ID)

	id3 := createTestConnector(t, db, "MCP-E", "mcp", map[string]any{
		"manifest": []map[string]any{
			{"name": expectedName},
		},
	})

	err := validateConnectorToolNames(t.Context(), []string{id1, id2, id3}, repo, "TestAgent", nil)
	if err == nil {
		t.Fatalf("expected duplicate between KB connector and MCP connector with matching tool name %s", expectedName)
	}
}

func TestExtractToolNamesFromConnector_NilValues(t *testing.T) {
	t.Parallel()
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "test-id",
		Name:        "test",
		Type:        "sql",
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 0 {
		t.Errorf("expected no entries for nil values, got %d", len(entries))
	}
}

func TestExtractToolNamesFromConnector_UnknownType(t *testing.T) {
	t.Parallel()
	vals := `{"key":"value"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "test-id",
		Name:        "test",
		Type:        "unknown_type",
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 0 {
		t.Errorf("expected no entries for unknown type, got %d", len(entries))
	}
}

func TestExtractSlackToolNames_ReturnsTwoEntries(t *testing.T) {
	t.Parallel()
	vals := `{"slack_bot_token":"xoxb-test"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "990e8400-e29b-41d4-a716-446655440000",
		Name:        "slack-test",
		Type:        "slack",
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "slack_send_message_990e8400" {
		t.Errorf("first entry = %q, want slack_send_message_990e8400", entries[0].Name)
	}
	if entries[1].Name != "slack_update_990e8400" {
		t.Errorf("second entry = %q, want slack_update_990e8400", entries[1].Name)
	}
}

func TestExtractSlackToolNames_CustomToolName(t *testing.T) {
	t.Parallel()
	vals := `{"slack_bot_token":"xoxb-test","tool_name":"my_slack"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "990e8400-e29b-41d4-a716-446655440000",
		Name:        "slack-test",
		Type:        "slack",
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "my_slack" {
		t.Errorf("first entry = %q, want my_slack (custom name)", entries[0].Name)
	}
	if entries[1].Name != "slack_update_990e8400" {
		t.Errorf("second entry = %q, want slack_update_990e8400", entries[1].Name)
	}
}

func TestExtractToolNamesFromConnector_EventMesh(t *testing.T) {
	t.Parallel()
	vals := `{"operations":[{"tool_name":"get_order"},{"tool_name":"create_order"}]}`
	conn := &store.Connector{
		Description: strPtr("event mesh connector"),
		ID:          "990e8400-e29b-41d4-a716-446655440000",
		Name:        "em-test",
		Type:        "event_mesh",
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "get_order_990e8400" {
		t.Errorf("first entry = %q, want get_order_990e8400", entries[0].Name)
	}
	if entries[1].Name != "create_order_990e8400" {
		t.Errorf("second entry = %q, want create_order_990e8400", entries[1].Name)
	}
}

func TestExtractToolNamesFromConnector_EventMeshMalformedOperations(t *testing.T) {
	t.Parallel()
	vals := `{"operations":"not-an-array"}`
	conn := &store.Connector{
		Description: strPtr("event mesh connector"),
		ID:          "990e8400-e29b-41d4-a716-446655440000",
		Name:        "em-bad",
		Type:        "event_mesh",
		Values:      &vals,
	}
	if entries := extractToolNamesFromConnector(conn); entries != nil {
		t.Fatalf("expected nil for malformed operations, got %v", entries)
	}
}

func TestGetBuiltinToolNames(t *testing.T) {
	t.Parallel()
	entries := getBuiltinToolNames([]string{"builtin_artifact_tools", "builtin_time_tools"})
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, expected := range []string{"list_artifacts", "load_artifact", "delete_artifact", "artifact_search_and_replace_regex", "artifact_grep", "get_current_time"} {
		if !names[expected] {
			t.Errorf("expected builtin tool name %q, not found", expected)
		}
	}
}

func TestGetBuiltinToolNames_ResearchTools(t *testing.T) {
	t.Parallel()
	entries := getBuiltinToolNames([]string{"builtin_research_tools"})
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, expected := range []string{"web_search_google", "deep_research"} {
		if !names[expected] {
			t.Errorf("expected builtin tool name %q, not found", expected)
		}
	}
}

func TestValidateConnectorToolNames_WebRequestAndResearchCoattach(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewConnectorRepo(db)

	id1 := createTestConnector(t, db, "SQL-WR", "sql", map[string]any{
		"tool_name": "query_research",
	})

	err := validateConnectorToolNames(t.Context(), []string{id1}, repo, "TestAgent",
		[]string{"builtin_web_request_tools", "builtin_research_tools"})
	if err != nil {
		t.Fatalf("expected no error when co-attaching web_request + research toolsets, got: %v", err)
	}
}

func TestGetBuiltinToolNames_UnknownToolset(t *testing.T) {
	t.Parallel()
	entries := getBuiltinToolNames([]string{"nonexistent_toolset"})
	if len(entries) != 0 {
		t.Errorf("expected no entries for unknown toolset, got %d", len(entries))
	}
}

func TestExtractToolNamesFromConnector_DocumentDB_MongoDB(t *testing.T) {
	t.Parallel()
	subtype := "mongodb"
	vals := `{"connection_string":"mongodb://localhost/db","collection":"orders"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "990e8400-e29b-41d4-a716-446655440000",
		Name:        "Mongo-A",
		Type:        "document_db",
		Subtype:     &subtype,
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "orders_mongo_query_990e8400" {
		t.Errorf("Name = %q, want orders_mongo_query_990e8400", entries[0].Name)
	}
}

func TestExtractToolNamesFromConnector_DocumentDB_DynamoDB(t *testing.T) {
	t.Parallel()
	subtype := "dynamodb"
	vals := `{"region":"us-east-1","table_name":"Events"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "990e8400-e29b-41d4-a716-446655440000",
		Name:        "DDB-A",
		Type:        "document_db",
		Subtype:     &subtype,
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "Events_dynamodb_query_990e8400" {
		t.Errorf("Name = %q, want Events_dynamodb_query_990e8400", entries[0].Name)
	}
}

func TestExtractToolNamesFromConnector_DocumentDB_ExplicitToolName(t *testing.T) {
	t.Parallel()
	subtype := "mongodb"
	vals := `{"tool_name":"my_mongo","collection":"orders"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "990e8400-e29b-41d4-a716-446655440000",
		Name:        "Mongo-B",
		Type:        "document_db",
		Subtype:     &subtype,
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 1 || entries[0].Name != "my_mongo" {
		t.Errorf("entries = %+v, want single entry named my_mongo", entries)
	}
}

func TestExtractToolNamesFromConnector_DocumentDB_UnknownSubtype(t *testing.T) {
	t.Parallel()
	subtype := "cassandra"
	vals := `{}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "id",
		Name:        "X",
		Type:        "document_db",
		Subtype:     &subtype,
		Values:      &vals,
	}
	if entries := extractToolNamesFromConnector(conn); len(entries) != 0 {
		t.Errorf("expected no entries for unknown subtype, got %d", len(entries))
	}
}

func TestExtractToolNamesFromConnector_GraphDB_Neo4j(t *testing.T) {
	t.Parallel()
	subtype := "neo4j"
	vals := `{"connection_string":"bolt://localhost","username":"n","password":"p","database":"graph"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "aa0e8400-e29b-41d4-a716-446655440000",
		Name:        "Neo4j-A",
		Type:        "graph_db",
		Subtype:     &subtype,
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 1 || entries[0].Name != "graph_neo4j_query_aa0e8400" {
		t.Errorf("entries = %+v, want graph_neo4j_query_aa0e8400", entries)
	}
}

func TestExtractToolNamesFromConnector_GraphDB_Neptune(t *testing.T) {
	t.Parallel()
	subtype := "neptune"
	vals := `{"connection_string":"bolt+s://x.neptune.amazonaws.com:8182","region":"us-east-1","auth_type":"access_key"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "aa0e8400-e29b-41d4-a716-446655440000",
		Name:        "Neptune-A",
		Type:        "graph_db",
		Subtype:     &subtype,
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 1 || entries[0].Name != "neptune_query_aa0e8400" {
		t.Errorf("entries = %+v, want neptune_query_aa0e8400", entries)
	}
}

func TestExtractToolNamesFromConnector_Search_Elasticsearch(t *testing.T) {
	t.Parallel()
	subtype := "elasticsearch"
	vals := `{"connection_string":"https://es.example.com:9200","api_key":"k"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "bb0e8400-e29b-41d4-a716-446655440000",
		Name:        "ES-A",
		Type:        "search",
		Subtype:     &subtype,
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 1 || entries[0].Name != "elasticsearch_query_bb0e8400" {
		t.Errorf("entries = %+v, want elasticsearch_query_bb0e8400", entries)
	}
}

func TestExtractToolNamesFromConnector_Search_OpenSearch(t *testing.T) {
	t.Parallel()
	subtype := "opensearch"
	vals := `{"connection_string":"https://os.example.com","region":"us-east-1","auth_type":"access_key"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "bb0e8400-e29b-41d4-a716-446655440000",
		Name:        "OS-A",
		Type:        "search",
		Subtype:     &subtype,
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 1 || entries[0].Name != "opensearch_query_bb0e8400" {
		t.Errorf("entries = %+v, want opensearch_query_bb0e8400", entries)
	}
}

func TestExtractToolNamesFromConnector_GraphDB_ExplicitToolName(t *testing.T) {
	t.Parallel()
	subtype := "neo4j"
	vals := `{"tool_name":"my_graph"}`
	conn := &store.Connector{
		Description: strPtr("test connector for fixture purposes"),
		ID:          "id",
		Name:        "X",
		Type:        "graph_db",
		Subtype:     &subtype,
		Values:      &vals,
	}
	entries := extractToolNamesFromConnector(conn)
	if len(entries) != 1 || entries[0].Name != "my_graph" {
		t.Errorf("entries = %+v, want my_graph", entries)
	}
}

func connIDPrefix(id string) string {
	prefix := id
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	result := make([]byte, 0, len(prefix))
	for i := range len(prefix) {
		if prefix[i] == '-' {
			result = append(result, '_')
		} else {
			result = append(result, prefix[i])
		}
	}
	return string(result)
}

func createReadyPackageWithSecretTool(t *testing.T, repo *store.ToolPackageRepo, pkgName, toolName, configKey string) {
	t.Helper()
	ctx := t.Context()
	pkg := &store.ToolPackage{
		Name:            pkgName,
		DiscoveryStatus: "ready",
		CreatedBy:       "tester",
		UpdatedBy:       "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package %s: %v", pkgName, err)
	}
	schemaJSON := `[{"key":"` + configKey + `","secret":true}]`
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID:    pkg.ID,
		ToolName:     toolName,
		ConfigSchema: &schemaJSON,
	}); err != nil {
		t.Fatalf("upsert tool file %s/%s: %v", pkgName, toolName, err)
	}
}

func TestSave_RejectsCrossToolsetCollision(t *testing.T) {
	t.Parallel()

	t.Run("hyphen-underscore normalisation triggers collision", func(t *testing.T) {
		t.Parallel()
		db := testOpenDB(t)
		repo := store.NewToolPackageRepo(db)

		createReadyPackageWithSecretTool(t, repo, "pkg-alpha", "my-tool", "api_key")
		createReadyPackageWithSecretTool(t, repo, "pkg_alpha", "my_tool", "api_key")

		err := validateCrossToolsetTCKeyCollisions(t.Context(), "", []string{"pkg-alpha", "pkg_alpha"}, repo)
		if err == nil {
			t.Fatal("expected ValidationError for colliding env-var keys")
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
	})

	t.Run("distinct packages with no collision pass", func(t *testing.T) {
		t.Parallel()
		db := testOpenDB(t)
		repo := store.NewToolPackageRepo(db)

		createReadyPackageWithSecretTool(t, repo, "pkg-one", "my-tool", "api_key")
		createReadyPackageWithSecretTool(t, repo, "pkg-two", "other-tool", "api_key")

		err := validateCrossToolsetTCKeyCollisions(t.Context(), "", []string{"pkg-one", "pkg-two"}, repo)
		if err != nil {
			t.Fatalf("expected no collision, got: %v", err)
		}
	})

	t.Run("same package same tool same key produces no false positive (single package)", func(t *testing.T) {
		t.Parallel()
		db := testOpenDB(t)
		repo := store.NewToolPackageRepo(db)

		createReadyPackageWithSecretTool(t, repo, "solo-pkg", "my-tool", "api_key")

		err := validateCrossToolsetTCKeyCollisions(t.Context(), "", []string{"solo-pkg"}, repo)
		if err != nil {
			t.Fatalf("single toolset should never collide with itself, got: %v", err)
		}
	})

	t.Run("create agent rejects colliding toolsets", func(t *testing.T) {
		t.Parallel()
		db := testOpenDB(t)
		agentRepo := store.NewAgentRepo(db)
		toolRepo := store.NewToolPackageRepo(db)

		createReadyPackageWithSecretTool(t, toolRepo, "col-alpha", "my-tool", "api_key")
		createReadyPackageWithSecretTool(t, toolRepo, "col_alpha", "my_tool", "api_key")

		svc := NewAgentService(agentRepo, nil, nil, toolRepo, nil, nil, nil)
		_, err := svc.Create(t.Context(), CreateAgentRequest{
			Description:  "test agent for fixture purposes",
			Name:         "collision-agent",
			Type:         AgentTypeStandard,
			SystemPrompt: "You are an agent.",
			Toolsets:     []string{"col-alpha", "col_alpha"},
			InputModes:   []string{"text"},
			OutputModes:  []string{"text"},
		}, "tester")
		if err == nil {
			t.Fatal("expected ValidationError from Create")
		}
		if _, ok := errors.AsType[*ValidationError](err); !ok {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
	})
}
