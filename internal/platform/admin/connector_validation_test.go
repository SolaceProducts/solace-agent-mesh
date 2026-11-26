// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newConnectorServiceForTest(t *testing.T) *ConnectorService {
	t.Helper()
	db := testOpenDB(t)
	return NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", slog.Default())
}

func TestValidateConnectorTypeSubtype(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		connType string
		subtype  string
		wantErr  bool
	}{
		{"api openapi", "api", "openapi", false},
		{"mcp remote", "mcp", "remote", false},
		{"sql postgres", "sql", "postgres", false},
		{"sql mysql", "sql", "mysql", false},
		{"knowledge_base bedrock", "knowledge_base", "bedrock", false},
		{"document_db mongodb", "document_db", "mongodb", false},
		{"document_db dynamodb", "document_db", "dynamodb", false},
		{"document_db cassandra rejected", "document_db", "cassandra", true},
		{"graph_db neo4j", "graph_db", "neo4j", false},
		{"graph_db neptune", "graph_db", "neptune", false},
		{"graph_db janusgraph rejected", "graph_db", "janusgraph", true},
		{"search elasticsearch", "search", "elasticsearch", false},
		{"search opensearch", "search", "opensearch", false},
		{"search solr rejected", "search", "solr", true},
		{"slack bot", "slack", "bot", false},

		{"api http rejected (bug #387)", "api", "http", true},
		{"mcp http rejected", "mcp", "http", true},

		{"sql nosql rejected", "sql", "nosql", true},
		{"api graphql rejected", "api", "graphql", true},
		{"knowledge_base pinecone rejected", "knowledge_base", "pinecone", true},

		{"empty type rejected", "", "openapi", true},
		{"empty subtype for known type rejected", "api", "", true},

		{"unknown type passes through", "webhook", "slack", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorTypeSubtype(tt.connType, tt.subtype)
			if tt.wantErr && err == nil {
				t.Errorf("expected ValidationError for (%q, %q), got nil", tt.connType, tt.subtype)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for (%q, %q): %v", tt.connType, tt.subtype, err)
			}
			if tt.wantErr {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Errorf("expected ValidationError type, got %T: %v", err, err)
				}
			}
		})
	}
}

func TestConnectorService_Create_RejectsUnknownSubtype(t *testing.T) {
	t.Parallel()
	svc := newConnectorServiceForTest(t)

	_, err := svc.Create(t.Context(), CreateConnectorRequest{
		Description: "E2E test connector for fixture purposes",
		Name:        "bad-connector",
		Type:        "api",
		Subtype:     "http",
		Values:      map[string]any{},
	}, "test-user")
	if err == nil {
		t.Fatal("expected error for unknown subtype, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(ve.Message, "subtype") {
		t.Errorf("error message should mention subtype; got %q", ve.Message)
	}
}

func TestConnectorService_Create_AcceptsDeclaredSubtype(t *testing.T) {
	t.Parallel()
	svc := newConnectorServiceForTest(t)

	_, err := svc.Create(t.Context(), CreateConnectorRequest{
		Description: "E2E test connector for fixture purposes",
		Name:        "ok-connector",
		Type:        "api",
		Subtype:     "openapi",
		Values:      map[string]any{},
	}, "test-user")
	if err != nil {
		t.Fatalf("declared (type, subtype) should be accepted: %v", err)
	}
}

func TestValidateConnectorValues_DispatchesByType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		connType string
		subtype  string
		values   any
		wantErr  bool
	}{
		{
			name:     "sql valid",
			connType: "sql",
			subtype:  "postgres",
			values:   map[string]any{"hostname": "db.example.com", "database": "mydb"},
		},
		{
			name:     "sql invalid hostname",
			connType: "sql",
			subtype:  "postgres",
			values:   map[string]any{"hostname": "invalid hostname with spaces"},
			wantErr:  true,
		},
		{
			name:     "mcp valid empty",
			connType: "mcp",
			values:   map[string]any{},
		},
		{
			name:     "api valid empty",
			connType: "api",
			values:   map[string]any{},
		},
		{
			name:     "unknown type passes",
			connType: "webhook",
			values:   map[string]any{"anything": "goes"},
		},
		{
			name:     "nil values passes",
			connType: "sql",
			values:   nil,
		},
		{
			name:     "non-map values rejected",
			connType: "sql",
			values:   "not a map",
			wantErr:  true,
		},
		{
			name:     "list values rejected",
			connType: "api",
			values:   []any{1, 2, 3},
			wantErr:  true,
		},
		{
			name:     "document_db mongodb valid",
			connType: "document_db",
			subtype:  "mongodb",
			values: map[string]any{
				"scheme":     "mongodb+srv",
				"hostname":   "cluster.example.net",
				"username":   "user",
				"password":   "pass",
				"database":   "db",
				"collection": "orders",
			},
		},
		{
			name:     "document_db mongodb invalid hostname",
			connType: "document_db",
			subtype:  "mongodb",
			values: map[string]any{
				"hostname":   "bad host!",
				"collection": "orders",
			},
			wantErr: true,
		},
		{
			name:     "document_db mongodb invalid collection chars",
			connType: "document_db",
			subtype:  "mongodb",
			values: map[string]any{
				"hostname":   "localhost",
				"collection": "bad name!",
			},
			wantErr: true,
		},
		{
			name:     "document_db mongodb invalid scheme enum",
			connType: "document_db",
			subtype:  "mongodb",
			values: map[string]any{
				"scheme":     "mysql",
				"hostname":   "localhost",
				"collection": "orders",
			},
			wantErr: true,
		},
		{
			name:     "document_db dynamodb valid access_key",
			connType: "document_db",
			subtype:  "dynamodb",
			values: map[string]any{
				"region":                "us-east-1",
				"table_name":            "Events",
				"auth_type":             "access_key",
				"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
				"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE",
			},
		},
		{
			name:     "document_db dynamodb invalid region",
			connType: "document_db",
			subtype:  "dynamodb",
			values: map[string]any{
				"region":     "US East 1",
				"table_name": "Events",
			},
			wantErr: true,
		},
		{
			name:     "graph_db neo4j valid",
			connType: "graph_db",
			subtype:  "neo4j",
			values: map[string]any{
				"scheme":   "bolt+s",
				"hostname": "my-neo4j.example.com",
				"port":     7687,
				"username": "neo4j",
				"password": "secret",
				"database": "neo4j",
			},
		},
		{
			name:     "graph_db neo4j invalid hostname",
			connType: "graph_db",
			subtype:  "neo4j",
			values: map[string]any{
				"hostname": "http://wrong-scheme",
				"username": "neo4j",
				"password": "secret",
			},
			wantErr: true,
		},
		{
			name:     "graph_db neo4j invalid scheme enum",
			connType: "graph_db",
			subtype:  "neo4j",
			values: map[string]any{
				"scheme":   "https",
				"hostname": "my-neo4j.example.com",
				"username": "neo4j",
				"password": "secret",
			},
			wantErr: true,
		},
		{
			name:     "graph_db neptune valid access_key",
			connType: "graph_db",
			subtype:  "neptune",
			values: map[string]any{
				"scheme":                "bolt+s",
				"hostname":              "my-cluster.cluster-abc.us-east-1.neptune.amazonaws.com",
				"port":                  8182,
				"region":                "us-east-1",
				"auth_type":             "access_key",
				"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
				"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE",
			},
		},
		{
			name:     "graph_db neptune invalid role_arn",
			connType: "graph_db",
			subtype:  "neptune",
			values: map[string]any{
				"scheme":    "bolt+s",
				"hostname":  "my-cluster.neptune.amazonaws.com",
				"region":    "us-east-1",
				"auth_type": "iam",
				"role_arn":  "not-an-arn",
			},
			wantErr: true,
		},
		{
			name:     "search elasticsearch valid url",
			connType: "search",
			subtype:  "elasticsearch",
			values: map[string]any{
				"scheme":   "https",
				"hostname": "elastic.example.com",
				"port":     9200,
				"api_key":  "ZGVtbzpkZW1v",
			},
		},
		{
			name:     "search elasticsearch valid cloud_id",
			connType: "search",
			subtype:  "elasticsearch",
			values: map[string]any{
				"cloud_id": "my-deployment:dXMtZWFzdC0xLmF3cy5jbG91ZC5lcy5pbyQ",
				"api_key":  "ZGVtbzpkZW1v",
			},
		},
		{
			name:     "search elasticsearch invalid hostname",
			connType: "search",
			subtype:  "elasticsearch",
			values: map[string]any{
				"hostname": "ftp://wrong",
				"api_key":  "ZGVtbzpkZW1v",
			},
			wantErr: true,
		},
		{
			name:     "search opensearch valid",
			connType: "search",
			subtype:  "opensearch",
			values: map[string]any{
				"scheme":                "https",
				"hostname":              "my-domain.us-east-1.es.amazonaws.com",
				"region":                "us-east-1",
				"auth_type":             "access_key",
				"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
				"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE",
			},
		},
		{
			name:     "search opensearch invalid hostname",
			connType: "search",
			subtype:  "opensearch",
			values: map[string]any{
				"hostname": "ftp://wrong",
				"region":   "us-east-1",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorValues(tt.connType, tt.subtype, tt.values)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSQLValues_Hostname(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hostname string
		wantErr  bool
	}{
		{"valid fqdn", "db.example.com", false},
		{"valid ipv4", "192.168.1.1", false},
		{"valid short host", "myhost", false},
		{"valid hyphenated", "my-db-host", false},
		{"valid single char", "a", false},
		{"empty skips validation", "", false},
		{"spaces invalid", "host with spaces", true},
		{"scheme invalid", "http://foo", true},
		{"semicolon invalid", "host;DROP", true},
		{"placeholder skips", "<<__SAM_HOSTNAME__>>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateSQLValues("postgres", map[string]any{"hostname": tt.hostname})
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Errorf("expected ValidationError, got %T", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSQLValues_Database(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		db      string
		wantErr bool
	}{
		{"valid simple", "mydb", false},
		{"valid underscore", "my_db", false},
		{"valid digits", "DB123", false},
		{"valid dollar", "my$db", false},
		{"empty skips validation", "", false},
		{"hyphen invalid", "my-db", true},
		{"dot invalid", "my.db", true},
		{"semicolon invalid", "db; DROP", true},
		{"space invalid", "my db", true},
		{"placeholder skips", "<<__SAM_DATABASE__>>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateSQLValues("postgres", map[string]any{"database": tt.db})
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Errorf("expected ValidationError, got %T", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSQLValues_OracleServiceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		svcName string
		wantErr bool
	}{
		{"valid simple", "ORCL", false},
		{"valid with dot", "my_service.123", false},
		{"valid with dollar", "svc$name", false},
		{"empty skips", "", false},
		{"hyphen invalid", "service-name", true},
		{"space invalid", "my service", true},
		{"placeholder skips", "<<__SAM_SERVICE_NAME__>>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateSQLValues("oracle", map[string]any{"service_name": tt.svcName})
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSQLValues_SubtypeRouting(t *testing.T) {
	t.Parallel()

	err := validateSQLValues("oracle", map[string]any{"database": "my-db-with-hyphens"})
	if err != nil {
		t.Error("oracle should not validate database field, but got error:", err)
	}

	for _, subtype := range []string{"mysql", "mariadb", "mssql", "postgres", ""} {
		err := validateSQLValues(subtype, map[string]any{"service_name": "svc-with-hyphens"})
		if err != nil {
			t.Errorf("subtype %q should not validate service_name, but got error: %v", subtype, err)
		}
	}
}

func TestValidateOperationUniqueness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ops     []string
		wantErr bool
	}{
		{"no duplicates", []string{"getUser", "createUser"}, false},
		{"single op", []string{"getUser"}, false},
		{"empty", nil, false},
		{"exact duplicate snake", []string{"getUser", "get_user"}, true},
		{
			"truncation collision",
			[]string{
				"thisIsAVeryLongOperationNameThatWillBeTruncatedAtSixtyCharsXYZ_A",
				"thisIsAVeryLongOperationNameThatWillBeTruncatedAtSixtyCharsXYZ_B",
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateOperationUniqueness(tt.ops)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateCrossSpecNameCollisions(t *testing.T) {
	t.Parallel()

	spec := map[string]any{
		"paths": map[string]any{
			"/users": map[string]any{
				"get": map[string]any{
					"operationId": "getUsers",
				},
				"post": map[string]any{
					"operationId": "get_users",
				},
			},
		},
	}

	err := validateCrossSpecNameCollisions([]string{"getUsers"}, spec)
	if err == nil {
		t.Error("expected cross-spec collision error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected ValidationError, got %T", err)
	}

	err = validateCrossSpecNameCollisions([]string{"getUsers", "get_users"}, spec)
	if err != nil {
		t.Errorf("both allowed should not collide: %v", err)
	}
}

func TestValidateSchemaPropertyUniqueness(t *testing.T) {
	t.Parallel()

	spec := map[string]any{
		"paths": map[string]any{
			"/items": map[string]any{
				"post": map[string]any{
					"operationId": "createItem",
					"requestBody": map[string]any{
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"properties": map[string]any{
										"firstName":  map[string]any{"type": "string"},
										"first_name": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	err := validateSchemaPropertyUniqueness([]string{"createItem"}, spec)
	if err == nil {
		t.Error("expected property name collision error")
	}

	err = validateSchemaPropertyUniqueness([]string{"otherOp"}, spec)
	if err != nil {
		t.Errorf("unrelated operation should not cause error: %v", err)
	}
}

func TestValidateAllowListAgainstStoredSpec(t *testing.T) {
	t.Parallel()

	const key = "test-ns/connectors/c1/spec.json"
	collisionSpec := `{"paths":{"/users":{"get":{"operationId":"getUsers"},"post":{"operationId":"get_users"}}}}`

	newSvc := func(t *testing.T, putKey, spec string) *ConnectorService {
		t.Helper()
		rs := objectstore.NewStore(objectstore.NewFilesystemClient(t.TempDir()))
		if putKey != "" {
			if err := rs.Put(t.Context(), putKey, strings.NewReader(spec)); err != nil {
				t.Fatalf("put spec: %v", err)
			}
		}
		return NewConnectorService(store.NewConnectorRepo(testOpenDB(t)), rs, "test-ns", slog.Default())
	}
	conn := func(rk string) *store.Connector {
		k := rk
		return &store.Connector{ID: "c1", Type: "api", Subtype: strPtr("openapi"), ResourceKey: &k}
	}

	t.Run("collision detected from stored spec", func(t *testing.T) {
		t.Parallel()
		svc := newSvc(t, key, collisionSpec)
		err := svc.validateAllowListAgainstStoredSpec(t.Context(), conn(key), map[string]any{"allow_list": "getUsers"})
		if err == nil {
			t.Fatal("expected collision error from stored spec")
		}
	})

	t.Run("no resource key skips check", func(t *testing.T) {
		t.Parallel()
		svc := newSvc(t, "", "")
		c := &store.Connector{ID: "c1", Type: "api", Subtype: strPtr("openapi")}
		if err := svc.validateAllowListAgainstStoredSpec(t.Context(), c, map[string]any{"allow_list": "getUsers"}); err != nil {
			t.Errorf("no resource key should skip, got %v", err)
		}
	})

	t.Run("missing object skips check", func(t *testing.T) {
		t.Parallel()
		svc := newSvc(t, "", "")
		if err := svc.validateAllowListAgainstStoredSpec(t.Context(), conn(key), map[string]any{"allow_list": "getUsers"}); err != nil {
			t.Errorf("missing object should skip, got %v", err)
		}
	})

	t.Run("empty allow_list skips check", func(t *testing.T) {
		t.Parallel()
		svc := newSvc(t, key, collisionSpec)
		if err := svc.validateAllowListAgainstStoredSpec(t.Context(), conn(key), map[string]any{}); err != nil {
			t.Errorf("empty allow_list should skip, got %v", err)
		}
	})

	t.Run("allow_list of only separators skips check", func(t *testing.T) {
		t.Parallel()
		svc := newSvc(t, key, collisionSpec)
		if err := svc.validateAllowListAgainstStoredSpec(t.Context(), conn(key), map[string]any{"allow_list": " , , "}); err != nil {
			t.Errorf("zero parsed operations should skip, got %v", err)
		}
	})

	t.Run("unparseable stored spec skips check", func(t *testing.T) {
		t.Parallel()
		svc := newSvc(t, key, "this: is: not: valid: openapi: at: all: [")
		if err := svc.validateAllowListAgainstStoredSpec(t.Context(), conn(key), map[string]any{"allow_list": "getUsers"}); err != nil {
			t.Errorf("unparseable spec should skip, got %v", err)
		}
	})

	t.Run("non-colliding spec passes full check", func(t *testing.T) {
		t.Parallel()
		cleanSpec := `{"paths":{"/users":{"get":{"operationId":"getUsers"}},"/items":{"get":{"operationId":"getItems"}}}}`
		svc := newSvc(t, key, cleanSpec)
		if err := svc.validateAllowListAgainstStoredSpec(t.Context(), conn(key), map[string]any{"allow_list": "getUsers, getItems"}); err != nil {
			t.Errorf("non-colliding operations should pass, got %v", err)
		}
	})
}

func TestPersistSpecURL_MalformedExistingValues(t *testing.T) {
	t.Parallel()
	svc := NewConnectorService(store.NewConnectorRepo(testOpenDB(t)), nil, "test-ns", slog.Default())
	bad := "{not valid json"
	c := &store.Connector{ID: "c1", Type: "api", Subtype: strPtr("openapi"), Values: &bad}
	if _, err := svc.persistSpecURL(t.Context(), nil, c, nil); err == nil {
		t.Fatal("expected error unmarshaling malformed connector values")
	}
}

func TestValidateMCPValues_ToolSelection(t *testing.T) {
	t.Parallel()

	manifest := []map[string]any{
		{"name": "tool_a"},
		{"name": "tool_b"},
		{"name": "tool_c"},
	}

	tests := []struct {
		name     string
		selected []string
		wantErr  bool
	}{
		{"all exist", []string{"tool_a", "tool_b"}, false},
		{"empty selection", nil, false},
		{"missing tool", []string{"tool_a", "tool_missing"}, true},
		{"all missing", []string{"x", "y"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMCPToolSelection(tt.selected, manifest)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}

	err := validateMCPToolSelection([]string{"tool_a"}, nil)
	if err == nil {
		t.Error("expected error when selected_tools used without manifest")
	}
}

func TestValidateMCPToolNameLength(t *testing.T) {
	t.Parallel()

	shortName := "short_tool"
	longName := strings.Repeat("a", maxMCPToolNameLen+1)
	entrypointToolName := "agent_019e9445_4061_7104_87a5_3e29ad2fe230_unified_jira_confluence_knowledge_search"

	pinnedOK := strings.Repeat("c", 64)
	pinnedTooLong := strings.Repeat("d", 65)

	const remedyNoSelection = "Remove the offending entries from the manifest."
	const remedyWithSelection = "Unselect the offending tool(s)."

	tests := []struct {
		name     string
		manifest []map[string]any
		selected []string
		wantErr  bool
		wantMsg  string
	}{
		{
			"all short",
			[]map[string]any{{"name": shortName}},
			nil,
			false, "",
		},
		{
			"long tool not selected",
			[]map[string]any{{"name": shortName}, {"name": longName}},
			[]string{shortName},
			false, "",
		},
		{
			"long tool selected names the unselect remedy",
			[]map[string]any{{"name": shortName}, {"name": longName}},
			[]string{longName},
			true, remedyWithSelection,
		},
		{
			"empty manifest",
			nil,
			nil,
			false, "",
		},
		{
			"64 characters is accepted",
			[]map[string]any{{"name": pinnedOK}},
			nil,
			false, "",
		},
		{
			"65 characters is rejected",
			[]map[string]any{{"name": pinnedTooLong}},
			nil,
			true, remedyNoSelection,
		},
		{
			"an 83-character entrypoint tool name is rejected",
			[]map[string]any{{"name": entrypointToolName}},
			nil,
			true, remedyNoSelection,
		},
		{
			"the message states the bound it enforced",
			[]map[string]any{{"name": pinnedTooLong}},
			nil,
			true, "exceeding the 64 character limit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMCPToolNameLength(tt.manifest, tt.selected)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("message %q does not contain %q", err.Error(), tt.wantMsg)
			}
			if strings.Contains(err.Error(), "Google ADK") {
				t.Errorf("message still blames Google ADK: %q", err.Error())
			}
			if strings.Contains(strings.ToLower(err.Error()), "renam") {
				t.Errorf("message advises renaming, which breaks tools/call: %q", err.Error())
			}
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"getUser", "get_user"},
		{"GetUser", "get_user"},
		{"getUserByID", "get_user_by_id"},
		{"already_snake", "already_snake"},
		{"HTTPSProxy", "https_proxy"},
		{"with spaces here", "with_spaces_here"},
		{"with---dashes", "with_dashes"},
		{"mixedCamelCase123", "mixed_camel_case123"},
		{"", ""},
		{strings.Repeat("a", 100), strings.Repeat("a", 60)},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := toSnakeCase(tt.input)
			if got != tt.want {
				t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsPlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"<<__SAM_HOSTNAME__>>", true},
		{"<<__SAM_DATABASE__>>", true},
		{"<<__SAM_ANYTHING_HERE__>>", true},
		{"regular_value", false},
		{"", false},
		{"<<__SAM_>>", false},
		{"<<__SAM_PARTIAL", false},
		{"not<<__SAM_X__>>", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := isPlaceholder(tt.input)
			if got != tt.want {
				t.Errorf("isPlaceholder(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateConnectorValues_MCPManifestShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  map[string]any
		wantErr bool
	}{
		{
			name: "inline list of entries",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"manifest": []any{
					map[string]any{"name": "tool_a", "description": "the a tool"},
				},
			},
			wantErr: false,
		},
		{
			name: "JSON string blob",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"manifest":        `[{"name":"tool_a"}]`,
			},
			wantErr: false,
		},
		{
			name: "missing manifest is fine",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
			},
			wantErr: false,
		},
		{
			name: "boolean annotation hints",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"manifest": []any{
					map[string]any{"name": "tool_a", "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": nil}},
				},
			},
			wantErr: false,
		},
		{
			name: "annotations that are not a map",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"manifest":        `[{"name":"tool_a","annotations":"readOnly"}]`,
			},
			wantErr: true,
		},
		{
			name: "a hint that is not a bool",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"manifest": []any{
					map[string]any{"name": "tool_a", "annotations": map[string]any{"readOnlyHint": "yes"}},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorValues("mcp", "remote", tt.values)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateMCPToolFilterExclusivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  map[string]any
		wantErr string
	}{
		{
			name:    "no filter fields",
			values:  map[string]any{},
			wantErr: "",
		},
		{
			name:    "tool_name only",
			values:  map[string]any{"tool_name": "search"},
			wantErr: "",
		},
		{
			name:    "allow_list only",
			values:  map[string]any{"allow_list": "a,b"},
			wantErr: "",
		},
		{
			name:    "deny_list only",
			values:  map[string]any{"deny_list": "x,y"},
			wantErr: "",
		},
		{
			name:    "tool_name_prefix coexists with allow_list",
			values:  map[string]any{"tool_name_prefix": "atlassian_", "allow_list": "a,b"},
			wantErr: "",
		},
		{
			name:    "tool_name + allow_list",
			values:  map[string]any{"tool_name": "search", "allow_list": "a,b"},
			wantErr: "mutually exclusive",
		},
		{
			name:    "tool_name + deny_list",
			values:  map[string]any{"tool_name": "search", "deny_list": "x"},
			wantErr: "mutually exclusive",
		},
		{
			name:    "allow_list + deny_list",
			values:  map[string]any{"allow_list": "a", "deny_list": "x"},
			wantErr: "mutually exclusive",
		},
		{
			name: "manifest + tool_name",
			values: map[string]any{
				"tool_name": "search",
				"manifest":  []any{map[string]any{"name": "search"}},
			},
			wantErr: "manifest is mutually exclusive",
		},
		{
			name: "manifest + allow_list",
			values: map[string]any{
				"allow_list": "a,b",
				"manifest":   []any{map[string]any{"name": "a"}},
			},
			wantErr: "manifest is mutually exclusive",
		},
		{
			name: "manifest alone is allowed",
			values: map[string]any{
				"manifest": []any{map[string]any{"name": "x"}},
			},
			wantErr: "",
		},
		{
			name: "empty allow_list (whitespace only) is treated as unset",
			values: map[string]any{
				"tool_name":  "search",
				"allow_list": "   ,   ",
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMCPToolFilterExclusivity(tt.values)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("error is not a *ValidationError: %T", err)
			}
		})
	}
}

func TestValidateConnectorHILShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     any
		wantErr string
	}{
		{name: "nil ok", raw: nil, wantErr: ""},
		{name: "empty map ok", raw: map[string]any{}, wantErr: ""},
		{name: "empty string ok", raw: "", wantErr: ""},
		{name: "whitespace only ok", raw: "   \n  ", wantErr: ""},
		{
			name: "structured one tool",
			raw: map[string]any{
				"tools": map[string]any{
					"deleteIssue": map[string]any{
						"require_approval": true,
						"approval_message": "Confirm?",
						"show_args":        true,
						"timeout":          "30m",
					},
				},
			},
			wantErr: "",
		},
		{
			name: "structured top-level defaults",
			raw: map[string]any{
				"require_approval": true,
				"approval_message": "all calls need approval",
			},
			wantErr: "",
		},
		{
			name:    "string yaml form",
			raw:     "tools:\n  deleteIssue:\n    require_approval: true\n    timeout: \"30m\"\n",
			wantErr: "",
		},
		{
			name:    "string json form",
			raw:     `{"tools":{"deleteIssue":{"require_approval":true}}}`,
			wantErr: "",
		},
		{
			name:    "malformed yaml",
			raw:     "tools: [\n  - broken",
			wantErr: "hil:",
		},
		{
			name: "tools must be a map",
			raw: map[string]any{
				"tools": []any{"deleteIssue"},
			},
			wantErr: "hil.tools must be a map",
		},
		{
			name: "per-tool entry must be a map",
			raw: map[string]any{
				"tools": map[string]any{
					"deleteIssue": "approval needed",
				},
			},
			wantErr: "hil.tools.deleteIssue must be a map",
		},
		{
			name: "require_approval wrong type",
			raw: map[string]any{
				"tools": map[string]any{
					"deleteIssue": map[string]any{"require_approval": "yes"},
				},
			},
			wantErr: "must be a bool",
		},
		{
			name: "approval_message wrong type",
			raw: map[string]any{
				"tools": map[string]any{
					"deleteIssue": map[string]any{"approval_message": 42},
				},
			},
			wantErr: "must be a string",
		},
		{
			name: "timeout unparseable",
			raw: map[string]any{
				"tools": map[string]any{
					"deleteIssue": map[string]any{"timeout": "30 minutes"},
				},
			},
			wantErr: "hil.tools.deleteIssue.timeout",
		},
		{
			name: "unknown field rejected",
			raw: map[string]any{
				"tools": map[string]any{
					"deleteIssue": map[string]any{"requireApproval": true},
				},
			},
			wantErr: "unknown HIL field",
		},
		{
			name: "nested tools rejected",
			raw: map[string]any{
				"tools": map[string]any{
					"deleteIssue": map[string]any{
						"tools": map[string]any{"x": map[string]any{}},
					},
				},
			},
			wantErr: "tools is only valid at the top level",
		},
		{
			name: "require_approval_when accepted per-tool",
			raw: map[string]any{
				"tools": map[string]any{
					"atlassian_rest_request": map[string]any{
						"require_approval_when": []any{
							map[string]any{"arg": "method", "in": []any{"POST", "PUT", "DELETE"}},
						},
						"approval_message": "{{.method}} {{.path}}",
					},
				},
			},
			wantErr: "",
		},
		{
			name: "require_approval_when bad operand rejected",
			raw: map[string]any{
				"tools": map[string]any{
					"atlassian_rest_request": map[string]any{
						"require_approval_when": []any{
							map[string]any{"arg": "method", "in": "POST"},
						},
					},
				},
			},
			wantErr: "value must be a list",
		},
		{
			name: "null require_approval_when rejected at top level",
			raw: map[string]any{
				"require_approval_when": nil,
			},
			wantErr: "require_approval_when must be a list",
		},
		{
			name: "null require_approval_when rejected per-tool",
			raw: map[string]any{
				"tools": map[string]any{
					"atlassian_rest_request": map[string]any{"require_approval_when": nil},
				},
			},
			wantErr: "require_approval_when must be a list",
		},
		{
			name:    "wrong root type",
			raw:     []any{"deleteIssue"},
			wantErr: "must be a map",
		},
		{
			name:    "null require_approval",
			raw:     map[string]any{"tools": map[string]any{"deleteIssue": map[string]any{"require_approval": nil}}},
			wantErr: "require_approval must be a bool",
		},
		{
			name:    "null show_args",
			raw:     map[string]any{"show_args": nil},
			wantErr: "show_args must be a bool",
		},
		{
			name:    "null approval_message",
			raw:     map[string]any{"tools": map[string]any{"deleteIssue": map[string]any{"approval_message": nil}}},
			wantErr: "approval_message must be a string",
		},
		{
			name:    "null tools",
			raw:     map[string]any{"tools": nil},
			wantErr: "hil.tools must be a map",
		},
		{
			name:    "null tools in yaml string",
			raw:     "tools:\n",
			wantErr: "hil.tools must be a map",
		},
		{
			name:    "null top-level require_approval_when",
			raw:     map[string]any{"require_approval_when": nil},
			wantErr: "hil.require_approval_when must be a list",
		},
		{
			name:    "null per-tool require_approval_when",
			raw:     map[string]any{"tools": map[string]any{"deleteIssue": map[string]any{"require_approval_when": nil}}},
			wantErr: "hil.tools.deleteIssue.require_approval_when must be a list",
		},
		{
			name:    "null per-tool require_approval_when in yaml string",
			raw:     "tools:\n  deleteIssue:\n    require_approval_when:\n",
			wantErr: "hil.tools.deleteIssue.require_approval_when must be a list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorHILShape(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("error is not a *ValidationError: %T", err)
			}
		})
	}
}

func TestValidateConnectorValues_MCPHILShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  map[string]any
		wantErr bool
	}{
		{
			name: "structured map",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"hil": map[string]any{
					"tools": map[string]any{
						"deleteIssue": map[string]any{
							"require_approval": true,
							"timeout":          "30m",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "yaml string blob",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"hil":             "tools:\n  deleteIssue:\n    require_approval: true\n",
			},
			wantErr: false,
		},
		{
			name: "malformed string blob",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"hil":             "tools: [broken",
			},
			wantErr: true,
		},
		{
			name: "bad timeout",
			values: map[string]any{
				"server_url":      "https://mcp.example.com",
				"connection_type": "streamable-http",
				"auth_type":       "none",
				"hil": map[string]any{
					"tools": map[string]any{
						"deleteIssue": map[string]any{"timeout": "30 minutes"},
					},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorValues("mcp", "remote", tt.values)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseMCPManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input map[string]any
		want  int
	}{
		{
			"array of maps",
			map[string]any{"manifest": []any{map[string]any{"name": "tool1"}, map[string]any{"name": "tool2"}}},
			2,
		},
		{
			"typed slice of maps, as discovery builds it",
			map[string]any{"manifest": []map[string]any{{"name": "tool1"}, {"name": "tool2"}}},
			2,
		},
		{
			"json string",
			map[string]any{"manifest": `[{"name":"tool1"}]`},
			1,
		},
		{
			"empty string",
			map[string]any{"manifest": ""},
			0,
		},
		{
			"nil",
			map[string]any{},
			0,
		},
		{
			"invalid json string",
			map[string]any{"manifest": "not json"},
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseMCPManifest(tt.input)
			if len(result) != tt.want {
				t.Errorf("got %d entries, want %d", len(result), tt.want)
			}
		})
	}
}

func TestParseMCPSelectedTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input map[string]any
		want  int
	}{
		{
			"comma-separated string",
			map[string]any{"selected_tools": "tool_a, tool_b, tool_c"},
			3,
		},
		{
			"array of strings",
			map[string]any{"selected_tools": []any{"tool_a", "tool_b"}},
			2,
		},
		{
			"empty string",
			map[string]any{"selected_tools": ""},
			0,
		},
		{
			"nil",
			map[string]any{},
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseMCPSelectedTools(tt.input)
			if len(result) != tt.want {
				t.Errorf("got %d entries, want %d", len(result), tt.want)
			}
		})
	}
}

func TestValidateSlackConnectorValues_Valid(t *testing.T) {
	t.Parallel()
	err := validateSlackConnectorValues(map[string]any{"slack_bot_token": "xoxb-valid-token"})
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

func TestValidateSlackConnectorValues_InvalidPrefix(t *testing.T) {
	t.Parallel()
	err := validateSlackConnectorValues(map[string]any{"slack_bot_token": "invalid-token"})
	if err == nil {
		t.Error("expected validation error for invalid token prefix")
	}
}

func TestValidateSlackConnectorValues_Placeholder(t *testing.T) {
	t.Parallel()
	err := validateSlackConnectorValues(map[string]any{"slack_bot_token": "<<__SAM_REQUIRED__>>"})
	if err != nil {
		t.Errorf("expected nil error for placeholder, got: %v", err)
	}
}

func TestValidateSlackConnectorValues_EmptyToken(t *testing.T) {
	t.Parallel()
	err := validateSlackConnectorValues(map[string]any{"slack_bot_token": ""})
	if err != nil {
		t.Errorf("expected nil error for empty token, got: %v", err)
	}
}

func TestValidateSlackConnectorValues_MissingKey(t *testing.T) {
	t.Parallel()
	err := validateSlackConnectorValues(map[string]any{})
	if err != nil {
		t.Errorf("expected nil error for missing key, got: %v", err)
	}
}

func TestValidateFieldsAgainstSchema_BUG019(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		connType  string
		subtype   string
		values    map[string]any
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "mcp valid server_url passes",
			connType: "mcp",
			subtype:  "remote",
			values:   map[string]any{"server_url": "https://mcp.example.com/mcp"},
		},
		{
			name:      "mcp server_url too short",
			connType:  "mcp",
			subtype:   "remote",
			values:    map[string]any{"server_url": "http://a"},
			wantErr:   true,
			errSubstr: "server_url",
		},
		{
			name:      "mcp server_url bad scheme",
			connType:  "mcp",
			subtype:   "remote",
			values:    map[string]any{"server_url": "ftp://example.com/mcp-server"},
			wantErr:   true,
			errSubstr: "server_url",
		},
		{
			name:     "mcp connection_type valid",
			connType: "mcp",
			subtype:  "remote",
			values:   map[string]any{"connection_type": "sse"},
		},
		{
			name:      "mcp connection_type invalid enum",
			connType:  "mcp",
			subtype:   "remote",
			values:    map[string]any{"connection_type": "websocket"},
			wantErr:   true,
			errSubstr: "connection_type",
		},
		{
			name:      "mcp auth_type invalid enum",
			connType:  "mcp",
			subtype:   "remote",
			values:    map[string]any{"auth_type": "kerberos"},
			wantErr:   true,
			errSubstr: "auth_type",
		},
		{
			name:     "mcp auth_apikey_value valid when apikey selected",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"auth_type":         "apikey",
				"auth_apikey_value": "long-enough-secret-value",
			},
		},
		{
			name:     "mcp auth_apikey_value too short rejected",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"auth_type":         "apikey",
				"auth_apikey_value": "abc",
			},
			wantErr:   true,
			errSubstr: "auth_apikey_value",
		},
		{
			name:     "mcp auth_apikey_value too short hidden when oauth selected",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"auth_type":         "oauth",
				"auth_apikey_value": "abc",
			},
		},
		{
			name:     "mcp auth_apikey_name pattern violation",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"auth_type":        "apikey",
				"auth_apikey_name": "X-Has Space",
			},
			wantErr:   true,
			errSubstr: "auth_apikey_name",
		},
		{
			name:     "mcp custom_headers CRLF in value blocked",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"custom_headers": map[string]any{
					"X-Bad": "value\r\nX-Injected: pwn",
				},
			},
			wantErr:   true,
			errSubstr: "header injection",
		},
		{
			name:     "mcp custom_headers CRLF in key blocked",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"custom_headers": map[string]any{
					"X-Bad\r\nX-Injected": "ok",
				},
			},
			wantErr:   true,
			errSubstr: "header injection",
		},
		{
			name:     "mcp custom_headers as list of pairs CRLF blocked",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"custom_headers": []any{
					map[string]any{"key": "X-Bad", "value": "v\r\nX-Injected: pwn"},
				},
			},
			wantErr:   true,
			errSubstr: "header injection",
		},
		{
			name:     "mcp custom_headers benign passes",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"custom_headers": map[string]any{
					"X-Tenant-ID": "abc-123",
				},
			},
		},
		{
			name:     "mcp placeholder bypasses pattern",
			connType: "mcp",
			subtype:  "remote",
			values: map[string]any{
				"server_url": "<<__SAM_SERVER_URL__>>",
			},
		},
		{
			name:      "mcp non-string string field rejected",
			connType:  "mcp",
			subtype:   "remote",
			values:    map[string]any{"server_url": 42},
			wantErr:   true,
			errSubstr: "server_url",
		},
		{
			name:     "unknown type is allowed through",
			connType: "webhook",
			subtype:  "anything",
			values:   map[string]any{"server_url": "ftp://x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateFieldsAgainstSchema(tt.connType, tt.subtype, tt.values)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected validation error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected *ValidationError, got %T: %v", err, err)
				}
				if tt.errSubstr != "" && !strings.Contains(ve.Message, tt.errSubstr) {
					t.Errorf("error message %q missing substring %q", ve.Message, tt.errSubstr)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func emValidBroker() map[string]any {
	return map[string]any{
		"broker_url":      "tcps://broker.example.com:55443",
		"broker_vpn":      "production",
		"broker_username": "user",
		"broker_password": "pass",
	}
}

func emValues(extra map[string]any) map[string]any {
	v := emValidBroker()
	for k, val := range extra {
		v[k] = val
	}
	return v
}

func TestValidateEventMeshConnectorValues_BrokerConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		values    map[string]any
		wantErr   bool
		errSubstr string
	}{
		{
			name: "all fields present (no inheritance)",
			values: map[string]any{
				"broker_url":      "tcps://broker.example.com:55443",
				"broker_vpn":      "production",
				"broker_username": "user",
				"broker_password": "pass",
				"tool_name":       "my_tool",
				"topic":           "test/topic",
			},
			wantErr: false,
		},
		{
			name: "missing password rejected",
			values: map[string]any{
				"broker_url":      "tcps://broker.example.com:55443",
				"broker_vpn":      "production",
				"broker_username": "user",
				"tool_name":       "my_tool",
				"topic":           "test/topic",
			},
			wantErr:   true,
			errSubstr: "broker_password",
		},
		{
			name: "no broker fields rejected",
			values: map[string]any{
				"tool_name": "my_tool",
				"topic":     "test/topic",
			},
			wantErr:   true,
			errSubstr: "broker connection requires fields",
		},
		{
			name: "missing broker_url",
			values: map[string]any{
				"broker_vpn":      "production",
				"broker_username": "user",
				"broker_password": "pass",
				"tool_name":       "my_tool",
				"topic":           "test/topic",
			},
			wantErr:   true,
			errSubstr: "broker_url",
		},
		{
			name: "missing multiple fields",
			values: map[string]any{
				"tool_name": "my_tool",
				"topic":     "test/topic",
			},
			wantErr:   true,
			errSubstr: "broker connection requires fields",
		},
		{
			name: "placeholder fields accepted (declarative secret substitution)",
			values: map[string]any{
				"broker_url":      "<<__SAM_BROKER_URL__>>",
				"broker_vpn":      "<<__SAM_BROKER_VPN__>>",
				"broker_username": "<<__SAM_BROKER_USERNAME__>>",
				"broker_password": "<<__SAM_BROKER_PASSWORD__>>",
				"tool_name":       "my_tool",
				"topic":           "test/topic",
			},
			wantErr: false,
		},
		{
			name: "lingering broker_connection_type is ignored",
			values: map[string]any{
				"broker_connection_type": "sam",
				"broker_url":             "tcps://broker.example.com:55443",
				"broker_vpn":             "production",
				"broker_username":        "user",
				"broker_password":        "pass",
				"tool_name":              "my_tool",
				"topic":                  "test/topic",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(tt.values)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else {
					var ve *ValidationError
					if !errors.As(err, &ve) {
						t.Errorf("expected ValidationError, got %T", err)
					}
					if tt.errSubstr != "" && !strings.Contains(ve.Message, tt.errSubstr) {
						t.Errorf("error message %q missing substring %q", ve.Message, tt.errSubstr)
					}
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_BrokerURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid tcp", "tcp://broker.example.com:55555", false},
		{"valid tcps", "tcps://broker.example.com:55443", false},
		{"valid tcp with path", "tcp://broker.example.com:55555/vpn", false},
		{"empty url required", "", true},
		{"http invalid", "http://broker.example.com", true},
		{"https invalid", "https://broker.example.com", true},
		{"ws invalid", "ws://broker.example.com", true},
		{"no scheme invalid", "broker.example.com:55555", true},
		{"placeholder skips", "<<__SAM_BROKER_URL__>>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{"broker_url": tt.url}))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Errorf("expected ValidationError, got %T", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_ToolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tool    string
		wantErr bool
	}{
		{"valid simple", "get_order", false},
		{"valid with numbers", "get_order_v2", false},
		{"valid single char", "a", false},
		{"empty skips", "", false},
		{"uppercase invalid", "Get_Order", true},
		{"starts with number invalid", "2get_order", true},
		{"hyphen invalid", "get-order", true},
		{"space invalid", "get order", true},
		{"placeholder skips", "<<__SAM_TOOL_NAME__>>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{"tool_name": tt.tool}))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Errorf("expected ValidationError, got %T", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_Parameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  string
		wantErr bool
	}{
		{"valid json array", `[{"name": "order_id", "type": "string"}]`, false},
		{"valid empty array", `[]`, false},
		{"valid multiple params", `[{"name": "a"}, {"name": "b"}]`, false},
		{"empty skips", "", false},
		{"invalid json", `[{"name": }]`, true},
		{"not array", `{"name": "order_id"}`, true},
		{"missing name field", `[{"type": "string"}]`, true},
		{"placeholder skips", "<<__SAM_PARAMS__>>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{"parameters": tt.params}))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Errorf("expected ValidationError, got %T", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateConnectorTypeSubtype_EventMesh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subtype string
		wantErr bool
	}{
		{"solace accepted", "solace", false},
		{"unknown subtype rejected", "kafka", true},
		{"empty subtype rejected", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorTypeSubtype("event_mesh", tt.subtype)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_BrokerURLStructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		{"valid with default port", "tcps://broker.example.com", false, ""},
		{"valid IPv4", "tcp://192.168.1.1:55555", false, ""},
		{"valid IPv4 no port", "tcps://10.0.0.1", false, ""},
		{"missing host", "tcp://:55555", true, "must include a host"},
		{"empty after scheme", "tcp://", true, "must include a host"},
		{"invalid port zero", "tcp://broker.example.com:0", true, "valid port"},
		{"invalid port negative", "tcp://broker.example.com:-1", true, "valid port"},
		{"invalid port too high", "tcp://broker.example.com:65536", true, "valid port"},
		{"invalid port non-numeric", "tcp://broker.example.com:abc", true, "valid port"},
		{"invalid hostname", "tcp://broker..example.com:55555", true, "valid hostname"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{"broker_url": tt.url}))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
				if tt.errMsg != "" && !strings.Contains(ve.Message, tt.errMsg) {
					t.Errorf("error message %q does not contain %q", ve.Message, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_Topic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		topic   string
		params  string
		wantErr bool
		errMsg  string
	}{
		{"valid simple", "orders/status", "", false, ""},
		{"valid with placeholder", "orders/{{ order_id }}/status", `[{"name": "order_id"}]`, false, ""},
		{"valid multiple placeholders", "{{ namespace }}/orders/{{ action }}", `[{"name": "namespace"}, {"name": "action"}]`, false, ""},
		{"valid with underscores", "my_app/v1/orders", "", false, ""},
		{"empty skips", "", "", false, ""},
		{"empty level invalid", "orders//status", "", true, "empty levels"},
		{"leading slash invalid", "/orders/status", "", true, "should not start with a slash"},
		{"unbalanced open", "orders/{{ order_id/status", "", true, "unbalanced"},
		{"unbalanced close", "orders/order_id }}/status", "", true, "unbalanced"},
		{"invalid placeholder name digit start", "orders/{{ 123abc }}/status", "", true, "not a valid identifier"},
		{"placeholder skips", "<<__SAM_TOPIC__>>", "", false, ""},
		{"placeholder without params", "orders/{{ order_id }}", "", true, "undefined parameters"},
		{"empty placeholder", "orders/{{ }}/status", "", true, "empty placeholder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values := emValues(map[string]any{"topic": tt.topic})
			if tt.params != "" {
				values["parameters"] = tt.params
			}
			err := validateEventMeshConnectorValues(values)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
				if tt.errMsg != "" && !strings.Contains(ve.Message, tt.errMsg) {
					t.Errorf("error message %q does not contain %q", ve.Message, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_ParametersEnhanced(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  string
		wantErr bool
		errMsg  string
	}{
		{"valid with all fields", `[{"name": "order_id", "type": "string", "required": true, "description": "Order ID", "payload_path": "request.order_id"}]`, false, ""},
		{"valid type integer", `[{"name": "count", "type": "integer"}]`, false, ""},
		{"valid type number", `[{"name": "price", "type": "number"}]`, false, ""},
		{"valid type boolean", `[{"name": "active", "type": "boolean"}]`, false, ""},
		{"invalid type array", `[{"name": "items", "type": "array"}]`, true, "invalid type"},
		{"invalid type object", `[{"name": "metadata", "type": "object"}]`, true, "invalid type"},
		{"invalid type", `[{"name": "foo", "type": "invalid_type"}]`, true, "invalid type"},
		{"invalid name digit start", `[{"name": "123abc"}]`, true, "valid identifier"},
		{"invalid name hyphen", `[{"name": "order-id"}]`, true, "valid identifier"},
		{"duplicate names", `[{"name": "foo"}, {"name": "foo"}]`, true, "Duplicate parameter"},
		{"invalid payload_path leading dot", `[{"name": "foo", "payload_path": ".request.foo"}]`, true, "should not start or end with a dot"},
		{"invalid payload_path trailing dot", `[{"name": "foo", "payload_path": "request.foo."}]`, true, "should not start or end with a dot"},
		{"invalid payload_path consecutive dots", `[{"name": "foo", "payload_path": "request..foo"}]`, true, "consecutive dots"},
		{"empty enum array", `[{"name": "status", "enum": []}]`, true, "enum array must not be empty"},
		{"valid enum", `[{"name": "status", "type": "string", "enum": ["pending", "shipped"]}]`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{"parameters": tt.params}))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
				if tt.errMsg != "" && !strings.Contains(ve.Message, tt.errMsg) {
					t.Errorf("error message %q does not contain %q", ve.Message, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_TopicParamConsistency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		topic   string
		params  string
		wantErr bool
		errMsg  string
	}{
		{
			"matching params",
			"orders/{{ order_id }}/status",
			`[{"name": "order_id", "type": "string"}]`,
			false,
			"",
		},
		{
			"multiple matching params",
			"{{ namespace }}/orders/{{ action }}",
			`[{"name": "namespace", "type": "string"}, {"name": "action", "type": "string"}]`,
			false,
			"",
		},
		{
			"undefined param in topic",
			"orders/{{ order_id }}/status",
			`[{"name": "customer_id", "type": "string"}]`,
			true,
			"undefined parameters: order_id",
		},
		{
			"extra params ok",
			"orders/status",
			`[{"name": "order_id", "type": "string"}]`,
			false,
			"",
		},
		{
			"multiple undefined params",
			"{{ ns }}/orders/{{ id }}/{{ action }}",
			`[{"name": "other", "type": "string"}]`,
			true,
			"undefined parameters",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{
				"topic":      tt.topic,
				"parameters": tt.params,
			}))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
				if tt.errMsg != "" && !strings.Contains(ve.Message, tt.errMsg) {
					t.Errorf("error message %q does not contain %q", ve.Message, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_PayloadFormatText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  string
		wantErr bool
		errMsg  string
	}{
		{"text with no payload_path errors", `[{"name": "a"}, {"name": "b"}]`, true, "exactly one parameter with a payload_path"},
		{"text with one payload_path ok", `[{"name": "a", "payload_path": "request.a"}, {"name": "b"}]`, false, ""},
		{"text with multiple payload_path error", `[{"name": "a", "payload_path": "request.a"}, {"name": "b", "payload_path": "request.b"}]`, true, "exactly one parameter with a payload_path"},
		{"json format allows multiple payload_path", `[{"name": "a", "payload_path": "request.a"}, {"name": "b", "payload_path": "request.b"}]`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values := emValues(map[string]any{"parameters": tt.params, "payload_format": "text"})
			if tt.name == "json format allows multiple payload_path" {
				values["payload_format"] = "json"
			}
			err := validateEventMeshConnectorValues(values)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
				if tt.errMsg != "" && !strings.Contains(ve.Message, tt.errMsg) {
					t.Errorf("error message %q does not contain %q", ve.Message, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_RequestExpiry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expiry  any
		wantErr bool
		errMsg  string
	}{
		{"valid 30s", float64(30000), false, ""},
		{"valid 5min", float64(300000), false, ""},
		{"valid 1s minimum", float64(1000), false, ""},
		{"too low", float64(500), true, "at least 1000ms"},
		{"too high", float64(400000), true, "must not exceed 300000ms"},
		{"valid string number", "60000", false, ""},
		{"nil ok", nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values := emValues(nil)
			if tt.expiry != nil {
				values["request_expiry_ms"] = tt.expiry
			}
			err := validateEventMeshConnectorValues(values)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected ValidationError, got %T", err)
				}
				if tt.errMsg != "" && !strings.Contains(ve.Message, tt.errMsg) {
					t.Errorf("error message %q does not contain %q", ve.Message, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateEventMeshConnectorValues_ToolNameLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tool    string
		wantErr bool
	}{
		{"valid 64 chars", strings.Repeat("a", 64), false},
		{"invalid 65 chars", strings.Repeat("a", 65), true},
		{"valid short", "a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{"tool_name": tt.tool}))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func validElasticsearchBase() map[string]any {
	return map[string]any{}
}

func validOpensearchBase() map[string]any {
	return map[string]any{
		"scheme":                "https",
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
}

func TestValidateSearchValues_ElasticsearchEndpointRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  map[string]any
		wantErr bool
	}{
		{
			name:    "both absent — rejected",
			values:  validElasticsearchBase(),
			wantErr: true,
		},
		{
			name: "both empty strings — rejected",
			values: func() map[string]any {
				v := validElasticsearchBase()
				v["hostname"] = ""
				v["cloud_id"] = ""
				return v
			}(),
			wantErr: true,
		},
		{
			name: "hostname only — accepted",
			values: func() map[string]any {
				v := validElasticsearchBase()
				v["hostname"] = "elastic.example.com"
				return v
			}(),
			wantErr: false,
		},
		{
			name: "cloud_id only — accepted",
			values: func() map[string]any {
				v := validElasticsearchBase()
				v["cloud_id"] = "my-deployment:dXMtZWFzdC0xLmF3cy5jbG91ZC5lcy5pbyQ"
				return v
			}(),
			wantErr: false,
		},
		{
			name: "both provided — accepted",
			values: func() map[string]any {
				v := validElasticsearchBase()
				v["hostname"] = "elastic.example.com"
				v["cloud_id"] = "my-deployment:dXMtZWFzdC0xLmF3cy5jbG91ZC5lcy5pbyQ"
				return v
			}(),
			wantErr: false,
		},
		{
			name: "placeholder hostname counts as provided — accepted",
			values: func() map[string]any {
				v := validElasticsearchBase()
				v["hostname"] = "<<__SAM_HOSTNAME__>>"
				return v
			}(),
			wantErr: false,
		},
		{
			name: "placeholder cloud_id counts as provided — accepted",
			values: func() map[string]any {
				v := validElasticsearchBase()
				v["cloud_id"] = "<<__SAM_CLOUD_ID__>>"
				return v
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorValues("search", "elasticsearch", tt.values)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected ValidationError, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected *ValidationError, got %T: %v", err, err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSearchValues_OpensearchEndpointRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  map[string]any
		wantErr bool
	}{
		{
			name:    "hostname absent — rejected",
			values:  validOpensearchBase(),
			wantErr: true,
		},
		{
			name: "hostname empty string — rejected",
			values: func() map[string]any {
				v := validOpensearchBase()
				v["hostname"] = ""
				return v
			}(),
			wantErr: true,
		},
		{
			name: "hostname present — accepted",
			values: func() map[string]any {
				v := validOpensearchBase()
				v["hostname"] = "my-domain.us-east-1.es.amazonaws.com"
				return v
			}(),
			wantErr: false,
		},
		{
			name: "placeholder hostname counts as provided — accepted",
			values: func() map[string]any {
				v := validOpensearchBase()
				v["hostname"] = "<<__SAM_HOSTNAME__>>"
				return v
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorValues("search", "opensearch", tt.values)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected ValidationError, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected *ValidationError, got %T: %v", err, err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func validMongoBase() map[string]any {
	return map[string]any{
		"hostname":   "cluster.example.net",
		"collection": "orders",
	}
}

func TestValidateMongoOptions_Pattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options string
		wantErr bool
	}{
		{
			name:    "clean value — accepted",
			options: "retryWrites=true&w=majority",
			wantErr: false,
		},
		{
			name:    "empty options — accepted",
			options: "",
			wantErr: false,
		},
		{
			name:    "hash character — rejected",
			options: "authSource=admin#x&tls=true",
			wantErr: true,
		},
		{
			name:    "question mark — rejected",
			options: "?retryWrites=true",
			wantErr: true,
		},
		{
			name:    "space in value — rejected",
			options: "authSource=admin db",
			wantErr: true,
		},
		{
			name:    "tab in value — rejected",
			options: "authSource=admin\ttls=true",
			wantErr: true,
		},
		{
			name:    "placeholder — accepted",
			options: "<<__SAM_OPTIONS__>>",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := validMongoBase()
			if tt.options != "" {
				v["options"] = tt.options
			}
			err := validateConnectorValues("document_db", "mongodb", v)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected ValidationError, got nil")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected *ValidationError, got %T: %v", err, err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
func emOperation(extra map[string]any) map[string]any {
	op := map[string]any{
		"tool_name":         "get_order_status",
		"tool_description":  "Returns the status of an order for the given identifier",
		"topic":             "acme/orders/v1/status",
		"wait_for_response": true,
		"payload_format":    "json",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
	for k, v := range extra {
		op[k] = v
	}
	return op
}

func TestValidateEventMeshConnectorValues_Operations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		operations any
		wantErr    bool
	}{
		{"single valid operation", []any{emOperation(nil)}, false},
		{"multiple valid operations", []any{emOperation(nil), emOperation(map[string]any{"tool_name": "create_order", "topic": "acme/orders/v1/create"})}, false},
		{"empty operations array", []any{}, true},
		{"present operations as string is rejected, not routed to legacy", "not-an-array", true},
		{"present operations as object is rejected, not routed to legacy", map[string]any{"tool_name": "x"}, true},
		{"present operations array of non-objects is rejected, not routed to legacy", []any{123}, true},
		{"duplicate tool names", []any{emOperation(nil), emOperation(nil)}, true},
		{"missing tool_name", []any{emOperation(map[string]any{"tool_name": ""})}, true},
		{"missing tool_description", []any{emOperation(map[string]any{"tool_description": ""})}, true},
		{"missing topic", []any{emOperation(map[string]any{"topic": ""})}, true},
		{"missing input_schema", []any{map[string]any{"tool_name": "get_order_status", "tool_description": "Returns the order status", "topic": "acme/orders/v1/status", "payload_format": "json"}}, true},
		{"invalid input_schema type", []any{emOperation(map[string]any{"input_schema": map[string]any{"type": "array", "properties": map[string]any{}}})}, true},
		{"invalid topic in operation", []any{emOperation(map[string]any{"topic": "acme//status"})}, true},
		{
			"topic references undeclared parameter",
			[]any{emOperation(map[string]any{"topic": "acme/orders/{missing}/status"})},
			true,
		},
		{
			"plain text payload without a single payload mapping",
			[]any{emOperation(map[string]any{
				"payload_format": "text",
				"input_schema":   map[string]any{"type": "object", "properties": map[string]any{}},
			})},
			true,
		},
		{
			"invalid request expiry",
			[]any{emOperation(map[string]any{"request_expiry_ms": -5})},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{"operations": tt.operations}))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestEventMeshParametersFromInputSchema(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"zebra": map[string]any{"type": "string", "x-solace-payload-path": "z.path"},
			"alpha": map[string]any{"type": "integer", "description": "first"},
			"mango": map[string]any{"type": "string", "x-solace-context-expression": "user.id"},
		},
		"required": []any{"alpha"},
	}

	params, err := eventMeshParametersFromInputSchema(schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotOrder := make([]string, len(params))
	for i, p := range params {
		gotOrder[i] = p["name"].(string)
	}
	wantOrder := []string{"alpha", "mango", "zebra"}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Errorf("parameter order = %v, want %v", gotOrder, wantOrder)
	}

	byName := map[string]map[string]any{}
	for _, p := range params {
		byName[p["name"].(string)] = p
	}
	if byName["alpha"]["required"] != true {
		t.Errorf("alpha should be required")
	}
	if byName["zebra"]["payload_path"] != "z.path" {
		t.Errorf("zebra payload_path = %v, want z.path", byName["zebra"]["payload_path"])
	}
	if byName["mango"]["context_expression"] != "user.id" {
		t.Errorf("mango context_expression = %v, want user.id", byName["mango"]["context_expression"])
	}
	if _, hasRequired := byName["mango"]["required"]; hasRequired {
		t.Errorf("mango (context-sourced) must not be marked required")
	}
}

func TestValidateEventMeshConnectorValues_EnumValidation(t *testing.T) {
	t.Parallel()

	withEnum := func(propType string, enum any) any {
		return []any{emOperation(map[string]any{
			"topic": "acme/orders/v1/status",
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"choice": map[string]any{"type": propType, "enum": enum}},
			},
		})}
	}

	tests := []struct {
		name    string
		ops     any
		wantErr bool
	}{
		{"valid string enum", withEnum("string", []any{"a", "b"}), false},
		{"valid integer enum", withEnum("integer", []any{float64(1), float64(2)}), false},
		{"valid boolean enum", withEnum("boolean", []any{true, false}), false},
		{"valid number enum", withEnum("number", []any{float64(1.5)}), false},
		{"enum not an array", withEnum("string", "nope"), true},
		{"empty enum array", withEnum("string", []any{}), true},
		{"enum value type mismatch", withEnum("string", []any{float64(1)}), true},
		{"integer enum with fractional value", withEnum("integer", []any{float64(1.5)}), true},
		{"unknown parameter type", withEnum("object", []any{"x"}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshConnectorValues(emValues(map[string]any{"operations": tt.ops}))
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestEventMeshParametersFromInputSchema_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema any
	}{
		{"not an object", "just a string"},
		{"type not object", map[string]any{"type": "array", "properties": map[string]any{}}},
		{"properties not an object", map[string]any{"type": "object", "properties": "nope"}},
		{"required not an array", map[string]any{"type": "object", "properties": map[string]any{}, "required": "order_id"}},
		{"required item not a string", map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{42}}},
		{"required references undefined property", map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []any{"missing"}}},
		{"property not an object", map[string]any{"type": "object", "properties": map[string]any{"a": "not-a-schema"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := eventMeshParametersFromInputSchema(tt.schema); err == nil {
				t.Errorf("expected error for %q, got nil", tt.name)
			}
		})
	}
}

func TestEventMeshParametersFromInputSchema_RequiredStringSlice(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
		"required":   []string{"order_id"},
	}
	params, err := eventMeshParametersFromInputSchema(schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 1 || params[0]["required"] != true {
		t.Errorf("expected order_id marked required, got %v", params)
	}
}

func TestEventMeshOperations_Errors(t *testing.T) {
	t.Parallel()

	t.Run("missing operations key", func(t *testing.T) {
		t.Parallel()
		if _, err := eventMeshOperations(map[string]any{}); err == nil {
			t.Error("expected error when operations is absent")
		}
	})
	t.Run("operations not an array of objects", func(t *testing.T) {
		t.Parallel()
		if _, err := eventMeshOperations(map[string]any{"operations": "nope"}); err == nil {
			t.Error("expected error when operations is not an array")
		}
	})
	t.Run("valid operations round-trip", func(t *testing.T) {
		t.Parallel()
		ops, err := eventMeshOperations(map[string]any{"operations": []any{map[string]any{"tool_name": "x"}}})
		if err != nil || len(ops) != 1 || ops[0]["tool_name"] != "x" {
			t.Errorf("ops = %v, err = %v", ops, err)
		}
	})
}

func TestEventMeshOperationValues(t *testing.T) {
	t.Parallel()

	t.Run("string parameters pass through untouched", func(t *testing.T) {
		t.Parallel()
		got := eventMeshOperationValues(map[string]any{"tool_name": "x", "parameters": `[{"name":"a"}]`})
		if got["parameters"] != `[{"name":"a"}]` {
			t.Errorf("parameters = %v, want the original string", got["parameters"])
		}
	})
	t.Run("object parameters are JSON-encoded", func(t *testing.T) {
		t.Parallel()
		got := eventMeshOperationValues(map[string]any{"parameters": []any{map[string]any{"name": "a"}}})
		s, ok := got["parameters"].(string)
		if !ok || s != `[{"name":"a"}]` {
			t.Errorf("parameters = %v, want JSON string", got["parameters"])
		}
	})
	t.Run("input_schema is derived into parameters when no parameters key", func(t *testing.T) {
		t.Parallel()
		got := eventMeshOperationValues(map[string]any{
			"input_schema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
				"required":   []any{"order_id"},
			},
		})
		s, ok := got["parameters"].(string)
		if !ok || !strings.Contains(s, `"name":"order_id"`) {
			t.Errorf("parameters = %v, want derived from input_schema", got["parameters"])
		}
	})
}

func TestValidateEventMeshTopic_AcceptsBothSyntaxes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		topic   string
		wantErr bool
	}{
		{"single brace", "acme/orders/{order_id}/status", false},
		{"legacy double brace", "acme/orders/{{order_id}}/status", false},
		{"legacy double brace spaced", "acme/orders/{{ order_id }}/status", false},
		{"legacy scheme embed", "acme/user/{{ text://order_id }}/get", false},
		{"consecutive slashes", "acme//status", true},
		{"invalid identifier in double brace", "acme/{{123}}/x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshTopic(tt.topic)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestEventMeshTopicPlaceholders(t *testing.T) {
	t.Parallel()
	got := eventMeshTopicPlaceholders("a/{x}/{{y}}/{{ text://z }}/static")
	want := []string{"x", "y", "z"}
	if !slices.Equal(got, want) {
		t.Errorf("placeholders = %v, want %v", got, want)
	}
}

func TestValidateAPIHILAgainstStoredSpec(t *testing.T) {
	t.Parallel()

	const key = "test-ns/connectors/c1/spec.json"
	const spec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{
	  "/users":{"get":{"operationId":"listUsers","responses":{"200":{"description":"ok"}}},
	            "delete":{"operationId":"deleteUser","responses":{"200":{"description":"ok"}}}}}}`
	rs := objectstore.NewStore(objectstore.NewFilesystemClient(t.TempDir()))
	if err := rs.Put(t.Context(), key, strings.NewReader(spec)); err != nil {
		t.Fatal(err)
	}
	svc := NewConnectorService(store.NewConnectorRepo(testOpenDB(t)), rs, "test-ns", slog.Default())
	resourceKey := key
	conn := &store.Connector{ID: "c1", Type: "api", Subtype: strPtr("openapi"), ResourceKey: &resourceKey}
	gate := func(name string) map[string]any {
		return map[string]any{"tools": map[string]any{name: map[string]any{"require_approval": true}}}
	}

	if err := svc.validateAPIHILAgainstStoredSpec(t.Context(), conn, map[string]any{"hil": gate("delete_user")}); err != nil {
		t.Errorf("registered name refused: %v", err)
	}
	if err := svc.validateAPIHILAgainstStoredSpec(t.Context(), conn, map[string]any{"hil": gate("deleteUser")}); err == nil {
		t.Error("raw operationId accepted; the registered name is delete_user")
	}
	if err := svc.validateAPIHILAgainstStoredSpec(t.Context(), conn, map[string]any{"allow_list": "listUsers", "hil": gate("delete_user")}); err != nil {
		t.Errorf("an operation the allow_list narrows away is still a spec operation: %v", err)
	}
	if err := svc.validateAPIHILAgainstStoredSpec(t.Context(), conn, map[string]any{"allow_list": "listUsers", "hil": gate("purge_users")}); err == nil {
		t.Error("an operation the spec lacks was accepted")
	}
	if err := svc.validateAPIHILAgainstStoredSpec(t.Context(), conn, map[string]any{"allow_list": "listUsers"}); err != nil {
		t.Errorf("connector without hil refused: %v", err)
	}
}

func TestConnectorService_Update_RefusesAPIHILKeyOutsideStoredSpec(t *testing.T) {
	t.Parallel()
	svc, _, created := newOpenAPIConnectorFixture(t)
	ctx := t.Context()
	spec := `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/users":{"delete":{"operationId":"deleteUser","responses":{"200":{"description":"ok"}}}}}}`
	if _, err := svc.UploadResource(ctx, created.ID, "spec.json", strings.NewReader(spec)); err != nil {
		t.Fatalf("UploadResource: %v", err)
	}
	valuesWithGate := func(tool string) map[string]any {
		return map[string]any{
			"base_url":  "https://api.example.com",
			"auth_type": "none",
			"hil":       map[string]any{"tools": map[string]any{tool: map[string]any{"require_approval": true}}},
		}
	}

	_, err := svc.Update(ctx, created.ID, UpdateConnectorRequest{Values: valuesWithGate("deleteUser")}, "user-1")
	valErr, ok := errors.AsType[*ValidationError](err)
	if !ok || !strings.Contains(valErr.Message, "hil.tools.deleteUser must be the snake_case operation tool name; use delete_user") {
		t.Fatalf("Update with raw operationId = %v, want the snake_case operation tool name refusal", err)
	}
	_, err = svc.Update(ctx, created.ID, UpdateConnectorRequest{Values: valuesWithGate("purge_users")}, "user-1")
	valErr, ok = errors.AsType[*ValidationError](err)
	if !ok || !strings.Contains(valErr.Message, "hil.tools.purge_users is not a tool this connector provides; use one of: delete_user") {
		t.Fatalf("Update with a snake_case name the spec lacks = %v, want the not-in-spec refusal listing delete_user", err)
	}
	if _, err := svc.Update(ctx, created.ID, UpdateConnectorRequest{Values: valuesWithGate("delete_user")}, "user-1"); err != nil {
		t.Fatalf("Update with registered name: %v", err)
	}
}

func TestValidateConnectorValues_RefusesNullHILPieces(t *testing.T) {
	t.Parallel()
	sqlValues := func(hil any) map[string]any {
		return map[string]any{"hostname": "db.example.com", "port": 5432, "database": "app", "username": "u", "password": "p", "hil": hil}
	}
	apiValues := func(hil any) map[string]any {
		return map[string]any{"specification_url": "https://api.example.com/openapi.json", "hil": hil}
	}
	hils := map[string]struct {
		hil     any
		wantErr string
	}{
		"map null tools":             {map[string]any{"tools": nil}, "hil.tools must be a map"},
		"string null tools":          {"tools:\n", "hil.tools must be a map"},
		"map null top-level when":    {map[string]any{"require_approval_when": nil}, "require_approval_when must be a list"},
		"map null per-tool when":     {map[string]any{"tools": map[string]any{"sql_query": map[string]any{"require_approval_when": nil}}}, "require_approval_when must be a list"},
		"string null per-tool when":  {"tools:\n  sql_query:\n    require_approval_when:\n", "require_approval_when must be a list"},
		"string null top-level when": {"require_approval_when:\n", "require_approval_when must be a list"},
	}
	for name, tt := range hils {
		for _, conn := range []struct {
			typ, subtype string
			values       func(any) map[string]any
		}{{"sql", "postgres", sqlValues}, {"api", "openapi", apiValues}} {
			t.Run(conn.typ+"/"+name, func(t *testing.T) {
				t.Parallel()
				err := validateConnectorValues(conn.typ, conn.subtype, conn.values(tt.hil))
				var ve *ValidationError
				if !errors.As(err, &ve) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want a ValidationError containing %q", err, tt.wantErr)
				}
			})
		}
	}
}
