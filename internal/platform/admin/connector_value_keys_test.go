// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type schemaSubtype struct {
	connType, subtype string
	fields            []connectorFieldDef
}

func schemaSubtypes(t *testing.T) []schemaSubtype {
	t.Helper()
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		t.Fatalf("load connector types: %v", err)
	}
	var out []schemaSubtype
	for _, def := range defs {
		for _, st := range def.Subtypes {
			out = append(out, schemaSubtype{connType: def.Type, subtype: st.Subtype, fields: st.FieldSchema})
		}
	}
	if len(out) == 0 {
		t.Fatal("no connector subtypes loaded")
	}
	return out
}

func valuesWithEveryField(fields []connectorFieldDef) map[string]any {
	values := make(map[string]any, len(fields))
	for _, f := range fields {
		values[f.Name] = "x"
	}
	return values
}

func TestUndeclaredConnectorValueKeys_EverySubtype(t *testing.T) {
	for _, st := range schemaSubtypes(t) {
		t.Run(st.connType+"/"+st.subtype, func(t *testing.T) {
			everyDeclared := valuesWithEveryField(st.fields)
			if got := UndeclaredConnectorValueKeys(st.connType, st.subtype, everyDeclared); len(got) != 0 {
				t.Errorf("every declared field is reported undeclared: %v", got)
			}

			withRuntimeKeys := valuesWithEveryField(st.fields)
			for _, key := range connectorRuntimeValueKeys[st.connType] {
				withRuntimeKeys[key] = "x"
			}
			if got := UndeclaredConnectorValueKeys(st.connType, st.subtype, withRuntimeKeys); len(got) != 0 {
				t.Errorf("a runtime key is reported undeclared: %v", got)
			}

			for _, f := range st.fields {
				for _, tc := range []struct {
					name, key string
					wantHint  bool
				}{
					{"an extra letter", f.Name + "x", false},
					{"another case", strings.ToUpper(f.Name), true},
					{"abbreviated", f.Name[:len(f.Name)-1], len(f.Name)-1 >= minPrefixHintLen},
				} {
					if slices.ContainsFunc(st.fields, func(o connectorFieldDef) bool { return o.Name == tc.key }) {
						continue
					}
					if slices.Contains(connectorRuntimeValueKeys[st.connType], tc.key) {
						continue
					}
					values := valuesWithEveryField(st.fields)
					values[tc.key] = "x"
					err := ValidateConnectorValueKeys(st.connType, st.subtype, values)
					ve, ok := err.(*ValidationError)
					if !ok {
						t.Errorf("%s of %q (%q): err = %v, want a *ValidationError", tc.name, f.Name, tc.key, err)
						continue
					}
					if !strings.Contains(ve.Message, fmt.Sprintf("%q", tc.key)) || len(ve.Details["values."+tc.key]) != 1 {
						t.Errorf("%s of %q: message %q, details %v, want %q named", tc.name, f.Name, ve.Message, ve.Details, tc.key)
					}
					if hinted := strings.Contains(ve.Message, "did you mean"); tc.wantHint && !hinted {
						t.Errorf("%s of %q (%q): message %q, want a hint", tc.name, f.Name, tc.key, ve.Message)
					}
				}
			}
		})
	}
}

func TestValidateConnectorValueKeys_Shapes(t *testing.T) {
	for _, tc := range []struct {
		name              string
		connType, subtype string
		values            any
		wantKeys          []string
		wantHints         map[string]string
	}{
		{"the issue: host for hostname", "sql", "postgres", map[string]any{"host": "h", "database": "d"}, []string{"host"}, map[string]string{"host": "hostname"}},
		{"user for username", "sql", "postgres", map[string]any{"user": "u"}, []string{"user"}, map[string]string{"user": "username"}},
		{"no near match gets no hint", "sql", "postgres", map[string]any{"zzzzz": 1}, []string{"zzzzz"}, map[string]string{"zzzzz": ""}},
		{"collects every key, sorted", "sql", "postgres", map[string]any{"b": 1, "a": 2, "host": 3}, []string{"a", "b", "host"}, nil},
		{"a too-short abbreviation is not hinted", "sql", "postgres", map[string]any{"ho": 1}, []string{"ho"}, map[string]string{"ho": ""}},
		{"nil values", "sql", "postgres", nil, nil, nil},
		{"values that are not an object", "sql", "postgres", "text", nil, nil},
		{"empty values", "sql", "postgres", map[string]any{}, nil, nil},
		{"a type the platform does not declare", "acme_plugin", "x", map[string]any{"anything": 1}, nil, nil},
		{"a subtype the platform does not declare", "sql", "cockroach", map[string]any{"anything": 1}, nil, nil},
		{"no subtype stated", "sql", "", map[string]any{"anything": 1}, nil, nil},
		{"event_mesh in the legacy flat form keeps its top-level operation keys", "event_mesh", "solace", map[string]any{"broker_url": "u", "topic": "a/b", "tool_name": "x", "tool_description": "d", "wait_for_response": true}, nil, nil},
		{"event_mesh with operations is checked", "event_mesh", "solace", map[string]any{"broker_url": "u", "operations": []any{}, "topic": "a/b"}, []string{"topic"}, nil},
		{"event_mesh flat form: a mistyped operations key is not skipped", "event_mesh", "solace", map[string]any{"broker_url": "u", "operatons": []any{}}, []string{"operatons"}, map[string]string{"operatons": "operations"}},
		{"event_mesh flat form: a mistyped broker key is not skipped", "event_mesh", "solace", map[string]any{"broker_urll": "u", "topic": "a/b"}, []string{"broker_urll"}, map[string]string{"broker_urll": "broker_url"}},
		{"a one-character typo of a long key is hinted", "sql", "postgres", map[string]any{"passwrd": "x"}, []string{"passwrd"}, map[string]string{"passwrd": "password"}},
		{"a short key two edits away is not hinted", "sql", "postgres", map[string]any{"prot": 1}, []string{"prot"}, map[string]string{"prot": ""}},
		{"a declared key that is not visible is still declared", "mcp", "remote", map[string]any{"auth_type": "none", "auth_apikey_name": "X"}, nil, nil},
		{"free-form maps are not walked", "mcp", "remote", map[string]any{"custom_headers": map[string]any{"anything": "goes"}}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := UndeclaredConnectorValueKeys(tc.connType, tc.subtype, tc.values); !slices.Equal(got, tc.wantKeys) {
				t.Fatalf("undeclared = %v, want %v", got, tc.wantKeys)
			}
			err := ValidateConnectorValueKeys(tc.connType, tc.subtype, tc.values)
			if len(tc.wantKeys) == 0 {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("err = %T %v, want *ValidationError", err, err)
			}
			if ve.Field != "values" || len(ve.Details) != len(tc.wantKeys) {
				t.Errorf("field %q, details %v, want one entry per key %v", ve.Field, ve.Details, tc.wantKeys)
			}
			for key, hint := range tc.wantHints {
				got := strings.Contains(ve.Message, fmt.Sprintf("did you mean %q?", hint))
				if hint == "" {
					got = !strings.Contains(ve.Message, "did you mean")
				}
				if !got {
					t.Errorf("message %q, want hint %q for %q", ve.Message, hint, key)
				}
			}
		})
	}
}

func TestValidateConnectorValueKeys_DoesNotEchoAnOversizedKey(t *testing.T) {
	long := strings.Repeat("k", maxEchoedValueKeyLen*4)
	err := ValidateConnectorValueKeys("sql", "postgres", map[string]any{long: 1})
	if err == nil {
		t.Fatal("want a rejection")
	}
	if strings.Contains(err.Error(), long) {
		t.Errorf("message repeats the whole %d-character key", len(long))
	}
}

func TestValidateConnectorValueKeys_OversizedKeysAreCutOnRuneBoundaries(t *testing.T) {
	multibyte := "k" + strings.Repeat("\u00e9", maxEchoedValueKeyLen*2)
	err := ValidateConnectorValueKeys("sql", "postgres", map[string]any{multibyte: 1})
	cut, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	for field, messages := range cut.Details {
		if !utf8.ValidString(field) || !utf8.ValidString(strings.Join(messages, "")) {
			t.Errorf("details entry %q was cut inside a character", field)
		}
	}

	sharedPrefix := strings.Repeat("k", maxEchoedValueKeyLen)
	err = ValidateConnectorValueKeys("sql", "postgres", map[string]any{sharedPrefix + "1": 1, sharedPrefix + "2": 2})
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	var entries int
	for _, messages := range ve.Details {
		entries += len(messages)
	}
	if entries != 2 {
		t.Errorf("details = %v, want one message per rejected key even when their echoed names coincide", ve.Details)
	}
}

func TestMissingRequiredConnectorFields_EventMeshFlatFormNeedsNoOperations(t *testing.T) {
	got := MissingRequiredConnectorFields("event_mesh", "solace", map[string]any{"broker_url": "u", "topic": "a/b"}, false)
	want := []string{"broker_vpn", "broker_username", "broker_password"}
	if !slices.Equal(got, want) {
		t.Errorf("flat form: missing = %v, want %v (operations is not required of it)", got, want)
	}
	got = MissingRequiredConnectorFields("event_mesh", "solace", map[string]any{"broker_url": "u", "operatons": []any{}}, false)
	if !slices.Contains(got, "broker_vpn") {
		t.Errorf("a mistyped operations key: missing = %v, want the broker fields still reported", got)
	}
}

func TestValidateConnectorValueKeys_ReportsAtMostAFewKeys(t *testing.T) {
	values := map[string]any{}
	for i := range maxReportedKeys * 3 {
		values[fmt.Sprintf("bogus_%02d", i)] = i
	}
	err := ValidateConnectorValueKeys("sql", "postgres", values)
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	if len(ve.Details) != maxReportedKeys {
		t.Errorf("details has %d entries, want %d", len(ve.Details), maxReportedKeys)
	}
	if want := fmt.Sprintf("and %d more", maxReportedKeys*2); !strings.Contains(ve.Message, want) || !strings.Contains(ve.Message, fmt.Sprintf("%d key(s)", maxReportedKeys*3)) {
		t.Errorf("message %q, want the full count and %q", ve.Message, want)
	}
}

func runtimeDefaults(st schemaSubtype, f connectorFieldDef) bool {
	return slices.Contains(runtimeDefaultedFields[st.connType+"/"+st.subtype], f.Name)
}

func visibleUnderRuntimeDefaults(st schemaSubtype, f connectorFieldDef) bool {
	for parent, want := range f.VisibleWhen {
		visible := false
		for _, p := range st.fields {
			visible = visible || (p.Name == parent && runtimeDefaults(st, p) && fmt.Sprint(p.Default) == want)
		}
		if !visible {
			return false
		}
	}
	return true
}

func TestMissingRequiredConnectorFields_EverySubtype(t *testing.T) {
	for _, st := range schemaSubtypes(t) {
		t.Run(st.connType+"/"+st.subtype, func(t *testing.T) {
			var wantAlways, wantNonSecret []string
			for _, f := range st.fields {
				if f.Required && !runtimeDefaults(st, f) && visibleUnderRuntimeDefaults(st, f) {
					wantAlways = append(wantAlways, f.Name)
					if !f.Secret && f.Type != "password" {
						wantNonSecret = append(wantNonSecret, f.Name)
					}
				}
			}
			if got := MissingRequiredConnectorFields(st.connType, st.subtype, map[string]any{}, false); !slices.Equal(got, wantAlways) {
				t.Errorf("empty values: missing = %v, want %v", got, wantAlways)
			}
			if got := MissingRequiredConnectorFields(st.connType, st.subtype, map[string]any{}, true); !slices.Equal(got, wantNonSecret) {
				t.Errorf("empty values, secrets skipped: missing = %v, want %v", got, wantNonSecret)
			}
			placeholders := make(map[string]any)
			for _, name := range wantAlways {
				placeholders[name] = "<<__SAM_REQUIRED__>>"
			}
			if got := MissingRequiredConnectorFields(st.connType, st.subtype, placeholders, false); len(got) != 0 {
				t.Errorf("placeholders reported missing: %v", got)
			}
			if got := MissingRequiredConnectorFields(st.connType, st.subtype, nil, false); !slices.Equal(got, wantAlways) {
				t.Errorf("absent values: missing = %v, want %v", got, wantAlways)
			}
			blank := make(map[string]any)
			for _, name := range wantAlways {
				blank[name] = ""
			}
			if got := MissingRequiredConnectorFields(st.connType, st.subtype, blank, false); !slices.Equal(got, wantAlways) {
				t.Errorf("empty strings: missing = %v, want %v", got, wantAlways)
			}
		})
	}
}

func TestMissingRequiredConnectorFields_AFieldWithADefaultIsNotMissing(t *testing.T) {
	got := MissingRequiredConnectorFields("mcp", "remote", map[string]any{"server_url": "https://mcp.example.com/mcp"}, false)
	if len(got) != 0 {
		t.Errorf("missing = %v, want none: connection_type and auth_type default at runtime", got)
	}
	got = MissingRequiredConnectorFields("mcp", "remote", map[string]any{}, false)
	if !slices.Equal(got, []string{"server_url"}) {
		t.Errorf("missing = %v, want only the field with no default", got)
	}
}

func TestMissingRequiredConnectorFields_HonoursVisibleWhen(t *testing.T) {
	got := MissingRequiredConnectorFields("mcp", "remote",
		map[string]any{"server_url": "u", "connection_type": "sse", "auth_type": "apikey"}, false)
	want := []string{"auth_apikey_name", "auth_apikey_value"}
	if !slices.Equal(got, want) {
		t.Errorf("missing = %v, want the fields the chosen auth type shows: %v", got, want)
	}
	if got := MissingRequiredConnectorFields("mcp", "remote",
		map[string]any{"server_url": "u", "connection_type": "sse", "auth_type": "none"}, false); len(got) != 0 {
		t.Errorf("missing = %v, want none for auth_type none", got)
	}
	for _, tc := range []struct {
		connType, subtype string
		values            any
	}{
		{"sql", "cockroach", map[string]any{}}, {"acme_plugin", "x", map[string]any{}}, {"sql", "postgres", "text"},
		{"acme_plugin", "x", nil}, {"sql", "cockroach", nil},
	} {
		if got := MissingRequiredConnectorFields(tc.connType, tc.subtype, tc.values, false); got != nil {
			t.Errorf("%s/%s %v: missing = %v, want nil", tc.connType, tc.subtype, tc.values, got)
		}
	}
}

func TestConnectorRuntimeValueKeys_AreHonouredByTheRenderer(t *testing.T) {
	const id = "0123abcd-0000-0000-0000-000000000000"
	custom := map[string]any{"tool_name": "custom_tool", "tool_description": "Custom description"}

	toolFor := func(t *testing.T, connType, subtype string, base map[string]any) map[string]any {
		t.Helper()
		values := map[string]any{}
		for k, v := range base {
			values[k] = v
		}
		for k, v := range custom {
			values[k] = v
		}
		entries := buildConnectorToolEntries(ConnectorConfig{ID: id, Type: connType, Subtype: subtype, Values: values})
		if len(entries) == 0 {
			t.Fatalf("%s/%s rendered no tool", connType, subtype)
		}
		return entries[0]
	}

	overridable := []struct {
		connType, subtype string
		base              map[string]any
	}{
		{"sql", "postgres", map[string]any{"hostname": "h", "database": "d"}},
		{"document_db", "mongodb", map[string]any{"hostname": "h", "collection": "c"}},
		{"document_db", "dynamodb", map[string]any{"region": "us-east-1", "table_name": "t"}},
		{"graph_db", "neo4j", map[string]any{"hostname": "h"}},
		{"graph_db", "neptune", map[string]any{"hostname": "h", "region": "us-east-1"}},
		{"search", "elasticsearch", map[string]any{"hostname": "h"}},
		{"search", "opensearch", map[string]any{"hostname": "h", "region": "us-east-1"}},
		{"slack", "bot", map[string]any{"slack_bot_token": "xoxb-1"}},
		{"web_search", "tavily", map[string]any{"api_key": "k"}},
	}
	exercised := map[string]map[string]bool{}
	exercise := func(connType string, values map[string]any, subtype string) {
		if exercised[connType] == nil {
			exercised[connType] = map[string]bool{}
		}
		for key := range values {
			exercised[connType][key] = true
		}
		if got := UndeclaredConnectorValueKeys(connType, subtype, values); len(got) != 0 {
			t.Errorf("%s/%s: the renderer reads %v, which the key check would reject", connType, subtype, got)
		}
	}
	for _, tc := range overridable {
		exercise(tc.connType, custom, tc.subtype)
		t.Run("tool_name and tool_description/"+tc.connType+"/"+tc.subtype, func(t *testing.T) {
			entry := toolFor(t, tc.connType, tc.subtype, tc.base)
			if entry["tool_name"] != "custom_tool" {
				t.Errorf("tool_name = %v, want the override honoured", entry["tool_name"])
			}
			config, _ := entry["tool_config"].(map[string]any)
			if config["tool_description"] != "Custom description" {
				t.Errorf("tool_description = %v, want the override honoured", config["tool_description"])
			}
		})
	}

	for _, tc := range []struct {
		connType, subtype string
	}{{"document_db", "mongodb"}, {"graph_db", "neo4j"}, {"graph_db", "neptune"}, {"search", "elasticsearch"}, {"search", "opensearch"}} {
		exercise(tc.connType, map[string]any{"connection_string": ""}, tc.subtype)
		t.Run("connection_string/"+tc.connType+"/"+tc.subtype, func(t *testing.T) {
			entries := buildConnectorToolEntries(ConnectorConfig{ID: id, Type: tc.connType, Subtype: tc.subtype,
				Values: map[string]any{"connection_string": "legacy-host.example.com:1234"}})
			config, _ := entries[0]["tool_config"].(map[string]any)
			if got, _ := config["connection_string"].(string); !strings.Contains(got, "legacy-host.example.com:1234") {
				t.Errorf("connection_string = %q, want the stored single-string form honoured", got)
			}
		})
	}

	exercise("mcp", map[string]any{"selected_tools": ""}, "remote")
	exercise("mcp", map[string]any{"ssl_config": map[string]any{}}, "remote")
	t.Run("ssl_config/mcp", func(t *testing.T) {
		t.Setenv(mcpTLSDirEnvVar, "/etc/sam/mtls")
		ssl := map[string]any{"client_cert": "/etc/sam/mtls/client.crt", "client_key": "/etc/sam/mtls/client.key"}
		entries := buildConnectorToolEntries(ConnectorConfig{ID: id, Type: "mcp", Subtype: "remote",
			Values: map[string]any{"server_url": "https://mcp.example.com", "ssl_config": ssl}})
		params, _ := entries[0]["connection_params"].(map[string]any)
		if !reflect.DeepEqual(params["ssl_config"], ssl) {
			t.Errorf("connection_params.ssl_config = %v, want the stored block honoured", params["ssl_config"])
		}
	})
	t.Run("event_mesh in the legacy flat form", func(t *testing.T) {
		values := map[string]any{
			"broker_url": "u", "topic": "retail/orders", "tool_name": "custom_tool", "tool_description": "Custom description",
			"wait_for_response": false, "payload_format": "json", "qos": 1, "input_schema": map[string]any{"type": "object", "properties": map[string]any{}},
		}
		entries := buildConnectorToolEntries(ConnectorConfig{ID: id, Type: "event_mesh", Subtype: "solace", Values: values})
		if len(entries) != 1 {
			t.Fatalf("entries = %v, want the flat form rendered as one tool", entries)
		}
		if name, _ := entries[0]["tool_name"].(string); !strings.HasPrefix(name, "custom_tool_") {
			t.Fatalf("tool_name = %q, want the flat form's override honoured", name)
		}
		if got := UndeclaredConnectorValueKeys("event_mesh", "solace", values); len(got) != 0 {
			t.Errorf("the renderer reads %v in the flat form, which the key check would reject", got)
		}
	})

	t.Run("selected_tools/mcp", func(t *testing.T) {
		manifest := []any{
			map[string]any{"name": "keep_me", "description": "kept"},
			map[string]any{"name": "drop_me", "description": "dropped"},
		}
		result := map[string]any{}
		mergeMCPFilterAndManifest(result, map[string]any{"manifest": manifest, "selected_tools": "keep_me"})
		if kept := fmt.Sprint(result["manifest"]); !strings.Contains(kept, "keep_me") || strings.Contains(kept, "drop_me") {
			t.Errorf("manifest = %v, want only the selected tool", kept)
		}
	})

	for connType, keys := range connectorRuntimeValueKeys {
		for _, key := range keys {
			if !exercised[connType][key] {
				t.Errorf("%s lists %q as a runtime key but this test never renders it", connType, key)
			}
		}
	}
}

func TestConnectorRuntimeValueKeys_AreNotAlsoDeclared(t *testing.T) {
	for _, st := range schemaSubtypes(t) {
		for _, key := range connectorRuntimeValueKeys[st.connType] {
			if slices.ContainsFunc(st.fields, func(f connectorFieldDef) bool { return f.Name == key }) {
				t.Errorf("%s/%s declares %q, so its runtime-key entry is redundant", st.connType, st.subtype, key)
			}
		}
	}
	declaredTypes := map[string]bool{}
	for _, st := range schemaSubtypes(t) {
		declaredTypes[st.connType] = true
	}
	for connType := range connectorRuntimeValueKeys {
		if !declaredTypes[connType] {
			t.Errorf("connectorRuntimeValueKeys names %q, which is not a declared connector type", connType)
		}
	}
}

func TestConnectorService_UpdateStillAcceptsUndeclaredKeys(t *testing.T) {
	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", discardLogger())
	values := map[string]any{"hostname": "db.example.com", "database": "app", "username": "svc", "password": "real-password"}
	created, err := svc.Create(t.Context(), CreateConnectorRequest{Name: "svc_update_db", Description: "Seeded for the update test", Type: "sql", Subtype: "postgres", Values: values}, "tester")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	values["bogusKey"] = 1
	updated, err := svc.Update(t.Context(), created.ID, UpdateConnectorRequest{Values: values}, "tester")
	if err != nil {
		t.Fatalf("Update refused an undeclared key: %v", err)
	}
	if stored, _ := updated.Values.(map[string]any); stored["bogusKey"] == nil {
		t.Errorf("Update dropped the undeclared key: %v", updated.Values)
	}
}
