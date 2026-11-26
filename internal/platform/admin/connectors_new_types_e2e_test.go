// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func createStoredConnector(t *testing.T, db *store.DB, req CreateConnectorRequest) string {
	t.Helper()
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", discardLogger())
	created, err := svc.Create(t.Context(), req, "tester")
	if err != nil {
		t.Fatalf("create %s/%s connector: %v", req.Type, req.Subtype, err)
	}
	return created.ID
}

func toolsFromStoredConnector(t *testing.T, db *store.DB, id string) []map[string]any {
	t.Helper()
	row, err := store.NewConnectorRepo(db).GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("load stored connector %s: %v", id, err)
	}
	if row == nil {
		t.Fatalf("connector %s not found in DB", id)
	}
	cc := ConnectorConfig{ID: row.ID, Type: row.Type}
	if row.Subtype != nil {
		cc.Subtype = *row.Subtype
	}
	if row.Values != nil {
		var vals map[string]any
		if err := json.Unmarshal([]byte(*row.Values), &vals); err != nil {
			t.Fatalf("unmarshal values: %v", err)
		}
		cc.Values = vals
	}
	return buildConnectorTools(cc)
}

func requireSingleTool(t *testing.T, tools []map[string]any) (map[string]any, map[string]any) {
	t.Helper()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool := tools[0]
	cfg, ok := tool["tool_config"].(map[string]any)
	if !ok {
		t.Fatalf("tool_config missing or wrong type: %T", tool["tool_config"])
	}
	return tool, cfg
}

type e2eCase struct {
	name             string
	connectorName    string
	typ              string
	subtype          string
	values           map[string]any
	wantStrBinary    string
	wantBackend      string
	wantTimeoutMin   int
	wantConfigKeys   []string
	wantToolNameHas  string
	invalidValues    map[string]any
	invalidFieldHint string
}

func e2eCases() []e2eCase {
	return []e2eCase{
		{
			name:             "DocumentDB_MongoDB",
			connectorName:    "e2e-mongodb",
			typ:              "document_db",
			subtype:          "mongodb",
			values:           map[string]any{"scheme": "mongodb+srv", "hostname": "cluster.example.net", "username": "user", "password": "pass", "database": "orders", "collection": "events"},
			wantStrBinary:    "document_db_query",
			wantBackend:      "mongodb",
			wantTimeoutMin:   60,
			wantConfigKeys:   []string{"backend", "connection_string", "collection", "database", "_str_binary", "tool_name", "tool_description"},
			wantToolNameHas:  "events_mongo_query_",
			invalidValues:    map[string]any{"hostname": "invalid host name", "collection": "events"},
			invalidFieldHint: "host",
		},
		{
			name:             "DocumentDB_DynamoDB",
			connectorName:    "e2e-dynamodb",
			typ:              "document_db",
			subtype:          "dynamodb",
			values:           map[string]any{"region": "us-east-1", "table_name": "Orders", "auth_type": "access_key", "aws_access_key_id": "AKIAIOSFODNN7EXAMPLE", "aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE"},
			wantStrBinary:    "document_db_query",
			wantBackend:      "dynamodb",
			wantTimeoutMin:   60,
			wantConfigKeys:   []string{"backend", "region", "table_name", "aws_access_key_id", "aws_secret_access_key", "_str_binary"},
			wantToolNameHas:  "Orders_dynamodb_query_",
			invalidValues:    map[string]any{"region": "Not A Region", "table_name": "Orders", "auth_type": "access_key", "aws_access_key_id": "AKIAIOSFODNN7EXAMPLE", "aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE"},
			invalidFieldHint: "region",
		},
		{
			name:             "GraphDB_Neo4j",
			connectorName:    "e2e-neo4j",
			typ:              "graph_db",
			subtype:          "neo4j",
			values:           map[string]any{"scheme": "bolt+s", "hostname": "neo4j.example.com", "port": 7687, "username": "neo4j", "password": "secret", "database": "graph"},
			wantStrBinary:    "graph_db_query",
			wantBackend:      "neo4j",
			wantTimeoutMin:   60,
			wantConfigKeys:   []string{"backend", "connection_string", "username", "password", "database", "_str_binary"},
			wantToolNameHas:  "graph_neo4j_query_",
			invalidValues:    map[string]any{"hostname": "bad host name", "username": "neo4j", "password": "secret"},
			invalidFieldHint: "host",
		},
		{
			name:             "GraphDB_Neptune",
			connectorName:    "e2e-neptune",
			typ:              "graph_db",
			subtype:          "neptune",
			values:           map[string]any{"scheme": "bolt+s", "hostname": "my-cluster.cluster-abc.us-east-1.neptune.amazonaws.com", "port": 8182, "region": "us-east-1", "auth_type": "iam", "role_arn": "arn:aws:iam::123456789012:role/SolaceNeptune"},
			wantStrBinary:    "graph_db_query",
			wantBackend:      "neptune",
			wantTimeoutMin:   60,
			wantConfigKeys:   []string{"backend", "connection_string", "region", "role_arn", "_str_binary"},
			wantToolNameHas:  "neptune_query_",
			invalidValues:    map[string]any{"hostname": "bad endpoint host", "region": "us-east-1", "auth_type": "iam", "role_arn": "arn:aws:iam::123456789012:role/SolaceNeptune"},
			invalidFieldHint: "endpoint",
		},
		{
			name:             "Search_Elasticsearch",
			connectorName:    "e2e-es",
			typ:              "search",
			subtype:          "elasticsearch",
			values:           map[string]any{"scheme": "https", "hostname": "elastic.example.com", "port": 9200, "api_key": "ZGVtbzpkZW1v"},
			wantStrBinary:    "search_query",
			wantBackend:      "elasticsearch",
			wantTimeoutMin:   60,
			wantConfigKeys:   []string{"backend", "connection_string", "api_key", "_str_binary"},
			wantToolNameHas:  "elasticsearch_query_",
			invalidValues:    map[string]any{"hostname": "bad es host", "api_key": "ZGVtbzpkZW1v"},
			invalidFieldHint: "host",
		},
		{
			name:             "Search_OpenSearch",
			connectorName:    "e2e-os",
			typ:              "search",
			subtype:          "opensearch",
			values:           map[string]any{"scheme": "https", "hostname": "my-domain.us-east-1.es.amazonaws.com", "region": "us-east-1", "auth_type": "iam", "role_arn": "arn:aws:iam::123456789012:role/SolaceOS"},
			wantStrBinary:    "search_query",
			wantBackend:      "opensearch",
			wantTimeoutMin:   60,
			wantConfigKeys:   []string{"backend", "connection_string", "region", "role_arn", "_str_binary"},
			wantToolNameHas:  "opensearch_query_",
			invalidValues:    map[string]any{"scheme": "https", "hostname": "my-domain.us-east-1.es.amazonaws.com", "region": "US-East-1", "auth_type": "iam", "role_arn": "arn:aws:iam::123456789012:role/SolaceOS"},
			invalidFieldHint: "region",
		},
	}
}

func TestConnectorE2E_StoredConnectorBuildsTheToolTheSTRExpects(t *testing.T) {
	for _, tc := range e2eCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := testOpenDB(t)
			id := createStoredConnector(t, db, CreateConnectorRequest{
				Name:        tc.connectorName,
				Description: "e2e " + tc.name + " connector",
				Type:        tc.typ,
				Subtype:     tc.subtype,
				Values:      tc.values,
			})

			tools := toolsFromStoredConnector(t, db, id)
			tool, cfg := requireSingleTool(t, tools)
			if got := tool["tool_type"]; got != "builtin" {
				t.Errorf("tool_type=%v, want builtin", got)
			}
			toolName, _ := tool["tool_name"].(string)
			if !strings.Contains(toolName, tc.wantToolNameHas) {
				t.Errorf("tool_name=%q must contain %q", toolName, tc.wantToolNameHas)
			}
			timeout, ok := tool["timeout_seconds"].(int)
			if !ok || timeout < tc.wantTimeoutMin {
				t.Errorf("timeout_seconds=%v want int>=%d", tool["timeout_seconds"], tc.wantTimeoutMin)
			}
			if got := cfg["_str_binary"]; got != tc.wantStrBinary {
				t.Errorf("_str_binary=%v, want %q", got, tc.wantStrBinary)
			}
			if got := cfg["backend"]; got != tc.wantBackend {
				t.Errorf("backend=%v, want %q", got, tc.wantBackend)
			}
			for _, key := range tc.wantConfigKeys {
				if _, present := cfg[key]; !present {
					t.Errorf("tool_config missing key %q (have %v)", key, configKeys(cfg))
				}
			}
		})
	}
}

func TestConnectorE2E_AgentWithMultipleNewConnectors(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)

	var ids []string
	for _, tc := range e2eCases() {
		ids = append(ids, createStoredConnector(t, db, CreateConnectorRequest{
			Name:        tc.connectorName + "-multi",
			Description: "multi-connector e2e " + tc.name,
			Type:        tc.typ,
			Subtype:     tc.subtype,
			Values:      tc.values,
		}))
	}

	repo := store.NewConnectorRepo(db)
	var configs []ConnectorConfig
	for _, id := range ids {
		row, err := repo.GetByID(t.Context(), id)
		if err != nil || row == nil {
			t.Fatalf("load %s: %v", id, err)
		}
		cc := ConnectorConfig{ID: row.ID, Type: row.Type}
		if row.Subtype != nil {
			cc.Subtype = *row.Subtype
		}
		if row.Values != nil {
			var vals map[string]any
			if err := json.Unmarshal([]byte(*row.Values), &vals); err != nil {
				t.Fatalf("unmarshal values for %s: %v", id, err)
			}
			cc.Values = vals
		}
		configs = append(configs, cc)
	}

	tools := buildToolsListWithPackages(nil, configs, ToolEmission{})
	if len(tools) != len(configs) {
		t.Fatalf("expected %d tools (one per connector), got %d", len(configs), len(tools))
	}

	seen := make(map[string]int, len(tools))
	for i, tl := range tools {
		name, _ := tl["tool_name"].(string)
		if name == "" {
			t.Errorf("tool[%d]: empty tool_name", i)
			continue
		}
		seen[name]++
		cfg, _ := tl["tool_config"].(map[string]any)
		if _, ok := cfg["_str_binary"]; !ok {
			t.Errorf("tool %q missing _str_binary", name)
		}
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("tool_name %q emitted %d times; must be unique across the agent", name, count)
		}
	}
}

func configKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
