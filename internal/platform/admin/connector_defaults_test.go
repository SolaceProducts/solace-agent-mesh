// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"reflect"
	"slices"
	"testing"
)

func renderedWithAndWithout(t *testing.T, st schemaSubtype, f connectorFieldDef) bool {
	t.Helper()
	base := valuesWithEveryField(st.fields)
	for _, other := range st.fields {
		if other.Required && other.Default != nil {
			base[other.Name] = other.Default
		}
	}
	for parent, want := range f.VisibleWhen {
		base[parent] = want
	}
	without := maps.Clone(base)
	delete(without, f.Name)
	render := func(values map[string]any) []map[string]any {
		return buildConnectorToolEntries(ConnectorConfig{ID: "00000000-0000-0000-0000-000000000001", Type: st.connType, Subtype: st.subtype, Values: values})
	}
	if len(render(base)) == 0 {
		t.Fatalf("%s/%s renders no tool entries, so the comparison says nothing", st.connType, st.subtype)
	}
	return reflect.DeepEqual(render(base), render(without))
}

func TestRuntimeDefaultedFields_MatchTheRenderer(t *testing.T) {
	covered := map[string]bool{}
	for _, st := range schemaSubtypes(t) {
		for _, f := range st.fields {
			if !f.Required || f.Default == nil {
				continue
			}
			key := st.connType + "/" + st.subtype
			covered[key+" "+f.Name] = true
			t.Run(key+"/"+f.Name, func(t *testing.T) {
				applied := renderedWithAndWithout(t, st, f)
				listed := slices.Contains(runtimeDefaultedFields[key], f.Name)
				if applied != listed {
					t.Errorf("the renderer applies the default of %s = %v; runtimeDefaultedFields lists it = %v", f.Name, applied, listed)
				}
			})
		}
	}
	checkListedDefaultsAreRequiredFields(t, covered)
}

func checkListedDefaultsAreRequiredFields(t *testing.T, covered map[string]bool) {
	t.Helper()
	for key, names := range runtimeDefaultedFields {
		for _, name := range names {
			if !covered[key+" "+name] {
				t.Errorf("runtimeDefaultedFields lists %s %s, which is not a required field with a default", key, name)
			}
		}
	}
}

func TestMissingRequiredConnectorFields_JudgesTheChildrenOfADefaultedParent(t *testing.T) {
	for _, tc := range []struct {
		name              string
		connType, subtype string
		values            map[string]any
		want              []string
	}{
		{"an absent AWS auth_type is access_key, whose keys are then required", "document_db", "dynamodb",
			map[string]any{"region": "eu-west-1"}, []string{"table_name", "aws_access_key_id", "aws_secret_access_key"}},
		{"an absent smtp_auth_type is plain, whose login is then required", "email", "smtp",
			map[string]any{"smtp_host": "smtp.example.com", "smtp_port": 587}, []string{"smtp_username", "smtp_password", "envelope_from", "recipient_allowlist"}},
		{"region has no runtime default, so it is reported", "knowledge_base", "bedrock",
			map[string]any{"auth_type": "access_key", "aws_access_key_id": "a", "aws_secret_access_key": "b", "kb_id": "k"}, []string{"region"}},
		{"auth_http_scheme has no runtime default, so it is reported", "mcp", "remote",
			map[string]any{"server_url": "https://mcp.example.com", "auth_type": "http", "auth_http_basic_username": "u", "auth_http_basic_password": "p"}, []string{"auth_http_scheme"}},
		{"an empty AWS auth_type is access_key too", "document_db", "dynamodb",
			map[string]any{"region": "eu-west-1", "auth_type": ""}, []string{"table_name", "aws_access_key_id", "aws_secret_access_key"}},
		{"an empty smtp_auth_type is plain too", "email", "smtp",
			map[string]any{"smtp_host": "smtp.example.com", "smtp_port": 587, "smtp_auth_type": ""}, []string{"smtp_username", "smtp_password", "envelope_from", "recipient_allowlist"}},
		{"a nil map is no values", "mcp", "remote", map[string]any(nil), []string{"server_url"}},
		{"nothing is missing when only defaulted fields are left out", "mcp", "remote",
			map[string]any{"server_url": "https://mcp.example.com"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MissingRequiredConnectorFields(tc.connType, tc.subtype, tc.values, false)
			if !slices.Equal(got, tc.want) {
				t.Errorf("missing = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMissingRequiredConnectorFields_LeavesTheCallersValuesAlone(t *testing.T) {
	values := map[string]any{"server_url": "https://mcp.example.com/mcp", "auth_type": ""}
	want := maps.Clone(values)
	MissingRequiredConnectorFields("mcp", "remote", values, false)
	if !reflect.DeepEqual(values, want) {
		t.Errorf("values = %v after the call, want %v: the defaults are applied to a copy", values, want)
	}
}
