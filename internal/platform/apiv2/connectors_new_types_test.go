// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"testing"
)

var databaseConnectorCases = []struct {
	name, connType, subtype string
	values                  map[string]any
	secret, clear           []string
	invalid                 map[string]any
}{
	{
		name: "MongoDB", connType: "document_db", subtype: "mongodb",
		values: map[string]any{"scheme": "mongodb+srv", "hostname": "cluster.example.net", "username": "u", "password": "p", "database": "db", "collection": "events"},
		secret: []string{"password"}, clear: []string{"hostname", "username", "collection"},
		invalid: map[string]any{"hostname": "invalid host name", "collection": "events"},
	},
	{
		name: "DynamoDB", connType: "document_db", subtype: "dynamodb",
		values: map[string]any{"region": "us-east-1", "table_name": "Orders", "auth_type": "access_key", "aws_access_key_id": "AKIAIOSFODNN7EXAMPLE", "aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE"},
		secret: []string{"aws_access_key_id", "aws_secret_access_key"}, clear: []string{"region", "table_name", "auth_type"},
		invalid: map[string]any{"region": "Not A Region", "table_name": "Orders", "auth_type": "access_key", "aws_access_key_id": "AKIAIOSFODNN7EXAMPLE", "aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE"},
	},
	{
		name: "Neo4j", connType: "graph_db", subtype: "neo4j",
		values: map[string]any{"scheme": "bolt+s", "hostname": "neo4j.example.com", "username": "neo4j", "password": "supersecret", "database": "graph"},
		secret: []string{"password"}, clear: []string{"hostname", "username", "database"},
		invalid: map[string]any{"hostname": "bad host name", "username": "neo4j", "password": "secret"},
	},
	{
		name: "Neptune", connType: "graph_db", subtype: "neptune",
		values: map[string]any{"scheme": "bolt+s", "hostname": "x.neptune.amazonaws.com", "region": "us-east-1", "auth_type": "access_key", "aws_access_key_id": "AKIAIOSFODNN7EXAMPLE", "aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE"},
		secret: []string{"aws_access_key_id", "aws_secret_access_key"}, clear: []string{"hostname", "region", "auth_type"},
		invalid: map[string]any{"hostname": "bad endpoint host", "region": "us-east-1", "auth_type": "iam", "role_arn": "arn:aws:iam::123456789012:role/SolaceNeptune"},
	},
	{
		name: "Elasticsearch", connType: "search", subtype: "elasticsearch",
		values: map[string]any{"scheme": "https", "hostname": "elastic.example.com", "port": 9200, "api_key": "ZGVtbzpkZW1v"},
		secret: []string{"api_key"}, clear: []string{"hostname"},
		invalid: map[string]any{"hostname": "bad es host", "api_key": "ZGVtbzpkZW1v"},
	},
	{
		name: "OpenSearch", connType: "search", subtype: "opensearch",
		values: map[string]any{"scheme": "https", "hostname": "my-domain.us-east-1.es.amazonaws.com", "region": "us-east-1", "auth_type": "access_key", "aws_access_key_id": "AKIAIOSFODNN7EXAMPLE", "aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE"},
		secret: []string{"aws_access_key_id", "aws_secret_access_key"}, clear: []string{"hostname", "region", "auth_type"},
		invalid: map[string]any{"scheme": "https", "hostname": "my-domain.us-east-1.es.amazonaws.com", "region": "US-East-1", "auth_type": "iam", "role_arn": "arn:aws:iam::123456789012:role/SolaceOS"},
	},
}

func TestConnectors_DatabaseTypesRedactSecretsOnCreateAndGet(t *testing.T) {
	app := newConnectorAPI(t)
	for _, tc := range databaseConnectorCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := app.call(http.MethodPost, "/connectors", map[string]any{
				"name": "redact-" + tc.name, "description": "Redaction probe for " + tc.name,
				"connectorType": tc.connType, "connectorSubtype": tc.subtype, "values": tc.values,
			})
			expectStatus(t, rec, http.StatusCreated)
			created := decodeData[Connector](t, rec)
			if created.ConnectorType != tc.connType || created.ConnectorSubtype != tc.subtype {
				t.Errorf("connectorType/Subtype = %s/%s, want %s/%s", created.ConnectorType, created.ConnectorSubtype, tc.connType, tc.subtype)
			}

			fetched := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+created.ID, nil))
			if fetched.ID != created.ID || fetched.ConnectorType != tc.connType || fetched.ConnectorSubtype != tc.subtype {
				t.Errorf("GET = %s %s/%s, want the created %s %s/%s", fetched.ID, fetched.ConnectorType, fetched.ConnectorSubtype, created.ID, tc.connType, tc.subtype)
			}

			for read, values := range map[string]map[string]any{"create": created.Values, "get": fetched.Values} {
				for _, key := range tc.secret {
					if got, present := values[key]; present && got != redacted {
						t.Errorf("%s: secret %s = %v, want %s", read, key, got, redacted)
					}
				}
				for _, key := range tc.clear {
					if got := values[key]; got != tc.values[key] {
						t.Errorf("%s: %s = %v, want %v as stored", read, key, got, tc.values[key])
					}
				}
			}
		})
	}
}

func TestConnectors_DatabaseTypesRefuseValuesTheirSchemaRejects(t *testing.T) {
	app := newConnectorAPI(t)
	for _, tc := range databaseConnectorCases {
		t.Run(tc.name, func(t *testing.T) {
			expectStatus(t, app.call(http.MethodPost, "/connectors", map[string]any{
				"name": "invalid-" + tc.name, "description": "Invalid values for " + tc.name,
				"connectorType": tc.connType, "connectorSubtype": tc.subtype, "values": tc.invalid,
			}), http.StatusUnprocessableEntity)
		})
	}
}

func TestConnectors_RefuseASubtypeTheTypeDoesNotDeclare(t *testing.T) {
	app := newConnectorAPI(t)
	for _, tc := range []struct{ connType, subtype string }{
		{"document_db", "cassandra"},
		{"graph_db", "janusgraph"},
		{"search", "solr"},
	} {
		t.Run(tc.connType+"/"+tc.subtype, func(t *testing.T) {
			expectStatus(t, app.call(http.MethodPost, "/connectors", map[string]any{
				"name": "bad-" + tc.subtype, "description": "A subtype the type does not declare.",
				"connectorType": tc.connType, "connectorSubtype": tc.subtype, "values": map[string]any{},
			}), http.StatusUnprocessableEntity)
		})
	}
}

func TestConnectors_AcceptValueKeysTheSchemaDoesNotDeclare(t *testing.T) {
	app := newConnectorAPI(t)
	values := map[string]any{"hostname": "db.example.com", "port": 5432, "database": "app", "username": "svc", "password": "real-password", "host": "legacy.example.com", "bogusKey": 1}

	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": "direct_route_db", "description": "Created on the platform's own route",
		"connectorType": "sql", "connectorSubtype": "postgres", "values": values,
	})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[Connector](t, rec)
	if created.Values["bogusKey"] == nil {
		t.Fatalf("create dropped the undeclared key it is meant to accept: %v", created.Values)
	}

	values["database"] = "app2"
	values["another_bogus"] = 2
	rec = app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"values": values})
	expectStatus(t, rec, http.StatusOK)
	if patched := decodeData[Connector](t, rec); patched.Values["another_bogus"] == nil || patched.Values["database"] != "app2" {
		t.Fatalf("PATCH did not store the edit with its undeclared keys: %v", patched.Values)
	}

	rec = app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"description": "Edited description of the connector"})
	expectStatus(t, rec, http.StatusOK)
	if kept := decodeData[Connector](t, rec); kept.Values["bogusKey"] == nil || kept.Values["database"] != "app2" {
		t.Errorf("a PATCH that is silent about values changed them: %v", kept.Values)
	}
}
