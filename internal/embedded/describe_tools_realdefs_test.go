// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !sam_lean

package embedded

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	platformadmin "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

type realDefsTool struct {
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	Reason     string          `json:"reason"`
	Parameters json.RawMessage `json:"parameters"`
}

func describeRealDefs(t *testing.T, name, typ, subtype, values string) []realDefsTool {
	t.Helper()
	wire := describeRealDefsWire(t, name, typ, subtype, values)
	var resp struct {
		Tools []realDefsTool `json:"tools"`
	}
	if err := json.Unmarshal(wire, &resp); err != nil {
		t.Fatalf("decode %s: %v", wire, err)
	}
	return resp.Tools
}

func describeRealDefsWire(t *testing.T, name, typ, subtype, values string) []byte {
	t.Helper()
	ctx := t.Context()
	db, err := store.Open("sqlite:///"+filepath.Join(t.TempDir(), "platform.db"), nil)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	connectors := store.NewConnectorRepo(db)
	desc := "real defs connector"
	if err = connectors.Create(ctx, &store.Connector{Name: name, Description: &desc, Type: typ, Subtype: &subtype, Values: &values, CreatedBy: "u", UpdatedBy: "u"}); err != nil {
		t.Fatalf("create connector: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	flags := features.NewRegistry()
	for _, key := range []string{"event_mesh_connector", "email_connector", "document_db_connector", "graph_db_connector", "search_connector"} {
		flags.Register(features.Definition{Key: key, ReleasePhase: features.PhaseExperimental, Default: true})
	}
	deps := &platformadmin.RouteDeps{
		FeatureRegistry: flags,
		ToolSvc:         platformadmin.NewToolService(store.NewToolRepo(db), logger),
		ConnectorSvc:    platformadmin.NewConnectorService(connectors, nil, "test-ns", logger),
	}
	mp := platformadmin.NewManageablePlatform(platpkg.NewPlatformCore(nil, platpkg.ExtensionPoints{}), deps, logger)

	body, _ := json.Marshal(map[string]any{"tools": []any{map[string]any{"connector": name}}})
	got, err := mp.HandleManagement(ctx, "post", []string{"tools", "describe"}, body, nil)
	if err != nil {
		t.Fatalf("tools/describe: %v", err)
	}
	wire, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return wire
}

func TestToolsDescribe_EverySTRBackedConnectorTypeDescribesAgainstTheRealDefs(t *testing.T) {
	cases := []struct {
		name, typ, subtype, values string
	}{
		{"sql", "sql", "postgres", `{"hostname":"db.example.com","port":"5432","database":"demo","username":"reader","password":"pw"}`},
		{"email", "email", "smtp", `{"smtp_host":"smtp.example.com","smtp_port":"2525","smtp_tls":"none","smtp_auth_type":"none","envelope_from_none":"noreply@example.com","recipient_allowlist":"example.com"}`},
		{"knowledge_base", "knowledge_base", "bedrock", `{"kb_id":"KB123","region":"us-east-1","aws_access_key_id":"AKIAEXAMPLE","aws_secret_access_key":"pw"}`},
		{"slack", "slack", "bot", `{"slack_bot_token":"xoxb-token"}`},
		{"search", "search", "elasticsearch", `{"hostname":"es.example.com","port":"9200"}`},
		{"graph_db", "graph_db", "neo4j", `{"hostname":"neo.example.com","username":"neo4j","password":"pw"}`},
		{"document_db mongodb", "document_db", "mongodb", `{"hostname":"mongo.example.com","database":"shop","collection":"orders"}`},
		{"document_db dynamodb", "document_db", "dynamodb", `{"region":"us-east-1","table_name":"orders"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tools := describeRealDefs(t, "the-"+tc.typ, tc.typ, tc.subtype, tc.values)
			if len(tools) == 0 {
				t.Fatal("no tools described")
			}
			for _, got := range tools {
				if got.Status != "ok" {
					t.Errorf("%s: status %q (%s), want ok: the registry this binary links has the tool's definition", got.Name, got.Status, got.Reason)
					continue
				}
				if len(propertyNames(t, got.Parameters)) == 0 {
					t.Errorf("%s: no parameters described", got.Name)
				}
			}
		})
	}
}

func TestToolsDescribe_EventMeshOperationParametersComeFromTheOperationConfig(t *testing.T) {
	const brokerPassword = "s3cret-broker-password"
	values := `{"broker_url":"tcp://broker.example.com:55555","broker_vpn":"default","broker_username":"u","broker_password":"` + brokerPassword + `","operations":[` +
		`{"tool_name":"get_order","topic":"orders/get","parameters":[{"name":"orderId","type":"string","required":true}]},` +
		`{"tool_name":"ping_orders","topic":"orders/ping"}]}`
	wire := describeRealDefsWire(t, "orders-mesh", "event_mesh", "solace", values)
	for _, secret := range []string{brokerPassword, "broker.example.com"} {
		if strings.Contains(string(wire), secret) {
			t.Errorf("the describe response carries %q from the connector's configuration: %s", secret, wire)
		}
	}
	tools := describeRealDefs(t, "orders-mesh", "event_mesh", "solace", values)

	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want the two operations", tools)
	}
	if got := propertyNames(t, tools[0].Parameters); !slices.Equal(got, []string{"orderId"}) {
		t.Errorf("get_order parameters = %v, want the operation's own orderId", got)
	}
	if got := propertyNames(t, tools[1].Parameters); slices.Contains(got, "orderId") {
		t.Errorf("ping_orders parameters = %v, want none from the other operation", got)
	}
}

func TestToolsDescribe_AnEventMeshOperationTheToolRefusesIsUnavailable(t *testing.T) {
	values := `{"broker_url":"tcp://broker.example.com:55555","broker_vpn":"default","broker_username":"u","broker_password":"pw","operations":[` +
		`{"tool_name":"get_order","topic":"orders/get","payload_format":"bogus"}]}`
	tools := describeRealDefs(t, "orders-mesh", "event_mesh", "solace", values)

	if len(tools) != 1 || tools[0].Status != "unavailable" || tools[0].Reason != "connector configuration invalid: parameters could not be derived" {
		t.Fatalf("tools = %+v, want unavailable: the operation's config makes the tool's own loader fail", tools)
	}
}

func propertyNames(t *testing.T, params json.RawMessage) []string {
	t.Helper()
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(params, &schema); err != nil || schema.Type != "object" {
		t.Fatalf("parameters %s are not an object schema: %v", params, err)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestInProcessToolSchema_EventMeshRequestAppliesItsOperationConfig(t *testing.T) {
	config := map[string]any{
		"tool_name": "get_order",
		"topic":     "orders/get/{request_id}",
		"parameters": []any{
			map[string]any{"name": "orderId", "type": "string", "required": true},
		},
		"event_mesh_config": map[string]any{
			"broker_config": map[string]any{
				"broker_url":      "tcp://broker.invalid:55555",
				"broker_username": "u",
				"broker_password": "p",
				"broker_vpn":      "default",
			},
		},
	}

	params, overrideApplied, ok := str.InProcessToolSchema("event_mesh_request", config)
	if !ok || !overrideApplied {
		t.Fatalf("ok = %t, overrideApplied = %t, want both true: the allowlist key must equal the registered tool name", ok, overrideApplied)
	}
	if got := propertyNames(t, params); !slices.Equal(got, []string{"orderId"}) {
		t.Errorf("parameters = %v, want the operation's orderId", got)
	}

	_, overrideApplied, ok = str.InProcessToolSchema("event_mesh_request", map[string]any{"topic": "orders/get"})
	if !ok || overrideApplied {
		t.Errorf("invalid config: ok = %t, overrideApplied = %t, want ok and no override", ok, overrideApplied)
	}
}

func TestInProcessToolSchema_DocumentDBQueryDescribesMongoWithoutConnecting(t *testing.T) {
	config := map[string]any{
		"backend":            "mongodb",
		"connection_string":  "mongodb://127.0.0.1:1/shop",
		"auto_detect_schema": true,
	}

	params, overrideApplied, ok := str.InProcessToolSchema("document_db_query", config)
	if !ok || !overrideApplied {
		t.Fatalf("ok = %t, overrideApplied = %t, want both true: the allowlist key must equal the registered tool name", ok, overrideApplied)
	}
	got := propertyNames(t, params)
	if !slices.Contains(got, "pipeline_str") || slices.Contains(got, "collection") {
		t.Errorf("parameters = %v, want the Mongo schema (pipeline_str, no collection)", got)
	}
}
