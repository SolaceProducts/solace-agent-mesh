// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestConnectorTypes_DeclareHiddenHIL(t *testing.T) {
	t.Parallel()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range defs {
		for _, st := range def.Subtypes {
			i := slices.IndexFunc(st.FieldSchema, func(f connectorFieldDef) bool { return f.Name == "hil" })
			if i < 0 {
				t.Errorf("%s/%s declares no hil field", def.Type, st.Subtype)
				continue
			}
			f := st.FieldSchema[i]
			if !f.Hidden || f.Required || f.Type != "hil" {
				t.Errorf("%s/%s hil = {type %q hidden %v required %v}, want hidden optional \"hil\"", def.Type, st.Subtype, f.Type, f.Hidden, f.Required)
			}
		}
	}
}

func TestValidateComponentConfig_AcceptsConnectorHILObjectEmptyAndNull(t *testing.T) {
	t.Parallel()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	hils := map[string]any{
		"object": map[string]any{"tools": map[string]any{"a_role": map[string]any{"require_approval": true}}},
		"empty":  map[string]any{},
		"null":   nil,
	}
	checked := 0
	for _, def := range defs {
		for _, st := range def.Subtypes {
			if !slices.ContainsFunc(st.FieldSchema, func(f connectorFieldDef) bool { return f.Name == "hil" }) {
				continue
			}
			for shape, hil := range hils {
				checked++
				t.Run(def.Type+"/"+st.Subtype+"/"+shape, func(t *testing.T) {
					t.Parallel()
					values := minimalConnectorValues(st)
					values["hil"] = hil
					doc, marshalErr := json.Marshal(map[string]any{
						"name": "Gated connector", "description": "A connector whose tools ask before they run",
						"type": def.Type, "subtype": st.Subtype, "values": values,
					})
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}

					res, execErr := (&tools.ValidateComponentConfigTool{}).Execute(t.Context(),
						map[string]any{"config_yaml": string(doc), "mime_type": tools.SAMConnectorConfigMIMEType}, nil)

					if execErr != nil {
						t.Fatalf("Execute: %v", execErr)
					}
					if valid, _ := res.Data["valid"].(bool); !valid {
						t.Errorf("hil %s rejected: %v", shape, res.Data["errors"])
					}
				})
			}
		}
	}
	if checked < 3*len(hils) {
		t.Fatalf("checked only %d combinations; the walk over the connector types found nothing", checked)
	}
}

func minimalConnectorValues(st connectorSubtypeDef) map[string]any {
	values := map[string]any{}
	if st.Subtype == "openapi" {
		values["specification_url"] = "https://api.example.com/openapi.json"
	}
	for _, f := range st.FieldSchema {
		if !f.Required || len(f.VisibleWhen) > 0 || f.Name == "hil" {
			continue
		}
		switch f.Type {
		case "select":
			values[f.Name] = f.Options[0].Value
		case "number":
			values[f.Name] = 1000
		case "boolean":
			values[f.Name] = true
		case "key_value":
			values[f.Name] = map[string]any{}
		case "operations":
			values[f.Name] = []any{map[string]any{
				"tool_name": "place_order", "tool_description": "Places an order on the broker.", "topic": "orders/place",
				"wait_for_response": false, "payload_format": "json", "input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
			}}
		default:
			values[f.Name] = "<<__SAM_REQUIRED__>>"
		}
	}
	return values
}

func TestValidateConnectorHIL_RolesPerType(t *testing.T) {
	t.Parallel()
	gate := map[string]any{"require_approval": true}
	tools := func(name string) map[string]any { return map[string]any{"tools": map[string]any{name: gate}} }
	op := func(name string) map[string]any { return emOperation(map[string]any{"tool_name": name}) }
	with := func(base, extra map[string]any) map[string]any {
		merged := maps.Clone(base)
		maps.Copy(merged, extra)
		return merged
	}
	email := map[string]any{"smtp_host": "smtp.example.com", "smtp_tls": "starttls", "smtp_auth_type": "none", "recipient_allowlist": "example.com"}
	slack := map[string]any{"bot_token": "xoxb-123-456-abc"}
	sql := map[string]any{"hostname": "db.example.com", "port": 5432, "database": "app", "username": "u", "password": "p"}
	oracle := with(sql, map[string]any{"service_name": "ORCL"})
	kb := map[string]any{"kb_id": "KB12345678", "region": "us-east-1", "auth_type": "iam"}
	mongo := map[string]any{"hostname": "mongo.example.com", "database": "app"}
	dynamo := map[string]any{"region": "us-east-1"}
	neo4j := map[string]any{"hostname": "neo.example.com"}
	neptune := map[string]any{"hostname": "neptune.example.com"}
	elastic := map[string]any{"hostname": "es.example.com"}
	opensearch := map[string]any{"hostname": "os.example.com"}
	broker := map[string]any{
		"broker_connection_type": "custom",
		"broker_url":             "tcps://broker.example.com:55443",
		"broker_vpn":             "default",
		"broker_username":        "u",
		"broker_password":        "p",
	}
	openapi := map[string]any{"specification_url": "https://api.example.com/openapi.json"}
	mcp := map[string]any{"server_url": "https://mcp.example.com", "connection_type": "sse", "auth_type": "none"}
	tests := []struct {
		name, connType, subtype string
		values                  map[string]any
		wantErr                 string
	}{
		{"email role", "email", "smtp", with(email, map[string]any{"hil": tools("send_email")}), ""},
		{"email suffixed name refused", "email", "smtp", with(email, map[string]any{"hil": tools("send_email_ab12cd34")}), "send_email"},
		{"slack update", "slack", "bot", with(slack, map[string]any{"hil": tools("slack_update")}), ""},
		{"sql every subtype", "sql", "oracle", with(oracle, map[string]any{"hil": tools("sql_query")}), ""},
		{"knowledge base", "knowledge_base", "bedrock", with(kb, map[string]any{"hil": tools("query_bedrock_kb")}), ""},
		{"mongo on mongodb", "document_db", "mongodb", with(mongo, map[string]any{"hil": tools("mongo_query")}), ""},
		{"mongo on dynamodb refused", "document_db", "dynamodb", with(dynamo, map[string]any{"hil": tools("mongo_query")}), "dynamodb_query"},
		{"dynamodb", "document_db", "dynamodb", with(dynamo, map[string]any{"hil": tools("dynamodb_query")}), ""},
		{"dynamodb on mongodb refused", "document_db", "mongodb", with(mongo, map[string]any{"hil": tools("dynamodb_query")}), "mongo_query"},
		{"neptune", "graph_db", "neptune", with(neptune, map[string]any{"hil": tools("neptune_query")}), ""},
		{"neo4j on neptune refused", "graph_db", "neptune", with(neptune, map[string]any{"hil": tools("neo4j_query")}), "neptune_query"},
		{"neo4j on neo4j", "graph_db", "neo4j", with(neo4j, map[string]any{"hil": tools("neo4j_query")}), ""},
		{"opensearch", "search", "opensearch", with(opensearch, map[string]any{"hil": tools("opensearch_query")}), ""},
		{"elasticsearch on opensearch refused", "search", "opensearch", with(opensearch, map[string]any{"hil": tools("elasticsearch_query")}), "opensearch_query"},
		{"elasticsearch", "search", "elasticsearch", with(elastic, map[string]any{"hil": tools("elasticsearch_query")}), ""},
		{"event_mesh operation", "event_mesh", "solace", with(broker, map[string]any{"operations": []any{op("create_order")}, "hil": tools("create_order")}), ""},
		{"event_mesh unknown operation", "event_mesh", "solace", with(broker, map[string]any{"operations": []any{op("create_order")}, "hil": tools("cancel_order")}), "create_order"},
		{"event_mesh flat tool_name", "event_mesh", "solace", with(broker, map[string]any{"tool_name": "legacy_req", "hil": tools("legacy_req")}), ""},
		{"event_mesh flat default", "event_mesh", "solace", with(broker, map[string]any{"hil": tools("event_mesh_request")}), ""},
		{"top-level defaults", "email", "smtp", with(email, map[string]any{"hil": gate}), ""},
		{"yaml string form", "email", "smtp", with(email, map[string]any{"hil": "tools:\n  send_email:\n    require_approval: true\n"}), ""},
		{"bad timeout", "email", "smtp", with(email, map[string]any{"hil": map[string]any{"tools": map[string]any{"send_email": map[string]any{"timeout": "soon"}}}}), "timeout"},
		{"unknown field", "email", "smtp", with(email, map[string]any{"hil": map[string]any{"require_consent": true}}), "unknown HIL field"},
		{"api operation tool name", "api", "openapi", with(openapi, map[string]any{"hil": tools("delete_user")}), ""},
		{"api raw operationId refused", "api", "openapi", with(openapi, map[string]any{"hil": tools("deleteUser")}), "delete_user"},
		{"api hyphenated name refused", "api", "openapi", with(openapi, map[string]any{"hil": tools("delete-user")}), "use delete_user"},
		{"api empty key refused", "api", "openapi", with(openapi, map[string]any{"hil": tools("")}), "empty key"},
		{"api key that is only separators refused", "api", "openapi", with(openapi, map[string]any{"hil": tools("___")}), "snake_case operation tool name"},
		{"mcp keeps free-form names", "mcp", "remote", with(mcp, map[string]any{"hil": tools("anyServerTool")}), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateConnectorValues(tc.connType, tc.subtype, tc.values)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateAuthoredConnectorHIL(t *testing.T) {
	t.Parallel()
	const spec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/users":{"get":{"operationId":"listUsers","responses":{"200":{"description":"ok"}}}}}}`
	gate := func(tool string) map[string]any {
		return map[string]any{"tools": map[string]any{tool: map[string]any{"require_approval": true}}}
	}
	tests := []struct {
		name              string
		connType, subtype string
		values            map[string]any
		spec              []byte
		wantErr           string
	}{
		{"api gate on a spec operation", "api", "openapi", map[string]any{"hil": gate("list_users")}, []byte(spec), ""},
		{"api gate typo names the spec operations", "api", "openapi", map[string]any{"hil": gate("list_user")}, []byte(spec), "list_users"},
		{"api gate without a spec is left to the platform", "api", "openapi", map[string]any{"hil": gate("anything")}, nil, ""},
		{"api gate with an unparseable spec is left to the platform", "api", "openapi", map[string]any{"hil": gate("anything")}, []byte("not a spec"), ""},
		{"event_mesh gate on a renamed-away operation", "event_mesh", "solace", map[string]any{
			"operations": []any{map[string]any{"tool_name": "place_order"}}, "hil": gate("create_order"),
		}, nil, "place_order"},
		{"role type gate on its role", "email", "smtp", map[string]any{"hil": gate("send_email")}, nil, ""},
		{"mcp is not checked", "mcp", "remote", map[string]any{"hil": gate("anyServerTool")}, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateAuthoredConnectorHIL(context.Background(), tc.connType, tc.subtype, tc.values, tc.spec)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestIsUncheckedGatesWarning(t *testing.T) {
	t.Parallel()
	keys := map[string]any{"hil": map[string]any{"tools": map[string]any{"list_users": map[string]any{}}}}
	for name, warnings := range map[string][]string{
		"no spec":     uncheckedHILWarnings(keys, uncheckedAPIHILReason),
		"unparseable": uncheckedHILWarnings(keys, unparseableSpecHILReason),
		"unreadable":  uncheckedHILWarnings(keys, unreadableSpecHILReason),
	} {
		if len(warnings) != 1 || !IsUncheckedGatesWarning(warnings[0]) {
			t.Errorf("%s: warnings %q are not recognised as the unchecked-gate warning", name, warnings)
		}
	}
	if IsUncheckedGatesWarning(removedGateWarnings([]string{"list_users"})[0]) {
		t.Error("a removed-gate warning was taken for the unchecked-gate warning")
	}
}

func TestUncheckedGatesWarningReasonsMatchTheWebUI(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "webui", "src", "features", "connectors", "utils", "saveConnector.ts"))
	if err != nil {
		t.Fatalf("read the web UI source that mirrors the warning endings: %v", err)
	}
	for _, reason := range uncheckedGatesWarningReasons {
		if !strings.Contains(string(source), strconv.Quote(reason)) {
			t.Errorf("saveConnector.ts has no string literal equal to %q; update UNCHECKED_GATES_WARNING_SUFFIXES to match connector_hil.go", reason)
		}
	}
}

var expectedConnectorHILRoles = map[string]map[string][]string{
	"email":          {"smtp": {"send_email"}},
	"slack":          {"bot": {"slack_send_message", "slack_update"}},
	"sql":            {"postgres": {"sql_query"}, "mysql": {"sql_query"}, "mariadb": {"sql_query"}, "mssql": {"sql_query"}, "oracle": {"sql_query"}},
	"knowledge_base": {"bedrock": {"query_bedrock_kb"}},
	"document_db":    {"mongodb": {"mongo_query"}, "dynamodb": {"dynamodb_query"}},
	"graph_db":       {"neo4j": {"neo4j_query"}, "neptune": {"neptune_query"}},
	"search":         {"elasticsearch": {"elasticsearch_query"}, "opensearch": {"opensearch_query"}},
	"web_search":     {"tavily": {"web_search"}, "exa": {"web_search"}},
}

func TestConnectorHILRoleNames_MatchTheIndependentTable(t *testing.T) {
	t.Parallel()
	for connType, bySubtype := range expectedConnectorHILRoles {
		for subtype, want := range bySubtype {
			if got := connectorHILRoleNames(connType, subtype, nil); !slices.Equal(got, want) {
				t.Errorf("%s/%s roles = %v, want %v", connType, subtype, got, want)
			}
		}
	}
	if got := connectorHILRoleNames("event_mesh", "solace", map[string]any{}); !slices.Equal(got, []string{"event_mesh_request"}) {
		t.Errorf("event_mesh without operations roles = %v, want [event_mesh_request]", got)
	}
	operations := map[string]any{"operations": []any{emOperation(map[string]any{"tool_name": "place_order"}), emOperation(map[string]any{"tool_name": "look_up_order"})}}
	if got := connectorHILRoleNames("event_mesh", "solace", operations); !slices.Equal(got, []string{"place_order", "look_up_order"}) {
		t.Errorf("event_mesh operations roles = %v, want [place_order look_up_order]", got)
	}
}

func TestEveryConnectorTypeWithHILHasRoles(t *testing.T) {
	t.Parallel()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]map[string]bool{}
	for _, def := range defs {
		for _, st := range def.Subtypes {
			if !slices.ContainsFunc(st.FieldSchema, func(f connectorFieldDef) bool { return f.Name == "hil" }) {
				continue
			}
			switch def.Type {
			case "api", "mcp", "event_mesh":
				continue
			}
			if seen[def.Type] == nil {
				seen[def.Type] = map[string]bool{}
			}
			seen[def.Type][st.Subtype] = true
			if _, ok := expectedConnectorHILRoles[def.Type][st.Subtype]; !ok {
				t.Errorf("%s/%s declares hil but has no entry in expectedConnectorHILRoles, so no role validates its gate keys", def.Type, st.Subtype)
			}
			if got := connectorHILRoleNames(def.Type, st.Subtype, nil); len(got) == 0 {
				t.Errorf("%s/%s declares hil but connectorHILRoles gives it no roles", def.Type, st.Subtype)
			}
		}
	}
	for connType, bySubtype := range expectedConnectorHILRoles {
		for subtype := range bySubtype {
			if !seen[connType][subtype] {
				t.Errorf("expectedConnectorHILRoles lists %s/%s, which no connector type definition declares with hil", connType, subtype)
			}
		}
	}
}

func TestValidateConnectorHIL_FailsClosedForAnUnlistedType(t *testing.T) {
	t.Parallel()
	gated := map[string]any{"hil": gateOn("anything")}

	err := validateConnectorHIL("widget", "gizmo", gated)
	if valErr, ok := errors.AsType[*ValidationError](err); !ok || !strings.Contains(valErr.Message, "not supported on a widget/gizmo connector") {
		t.Errorf("validateConnectorHIL for an unlisted type = %v, want the not-supported refusal", err)
	}
	for name, values := range map[string]map[string]any{"no hil": {}, "empty hil": {"hil": map[string]any{}}, "null hil": {"hil": nil}} {
		if err := validateConnectorHIL("widget", "gizmo", values); err != nil {
			t.Errorf("%s on an unlisted type = %v, want accepted", name, err)
		}
	}
	for _, connType := range []string{"api", "mcp"} {
		if err := validateConnectorHIL(connType, "", map[string]any{"hil": gateOn("delete_user")}); err != nil {
			t.Errorf("%s gates = %v, want accepted: its tools come from a spec or a server", connType, err)
		}
	}
}
