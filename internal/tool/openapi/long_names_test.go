// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	longAttributePrefix = "replicationQueueRejectMsgToSenderOnDiscardEnabledXyzAbcDefGhiJ"
	longAttributeA      = longAttributePrefix + "k"
	longAttributeB      = longAttributePrefix + "z"
)

func longAttributesSpec(serverURL string) string {
	return `openapi: "3.0.3"
info: {title: t, version: "1"}
servers: [{url: "` + serverURL + `"}]
paths:
  /msgVpns:
    post:
      operationId: createMsgVpn
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                ` + longAttributeA + `: {type: string}
                ` + longAttributeB + `: {type: string}
                msgVpnName: {type: string}
      responses: {"200": {description: ok}}
`
}

func TestDescribeOperations_LongBodyAttributesSharingPrefixStayDistinct(t *testing.T) {
	t.Parallel()

	descriptions, err := DescribeOperations(t.Context(), []byte(longAttributesSpec("https://x.example.com")), nil, nil)
	if err != nil {
		t.Fatalf("DescribeOperations: %v", err)
	}
	schema := schemaOf(t, descriptionOf(t, descriptions, "create_msg_vpn"))
	if len(schema.Properties) != 3 {
		names := make([]string, 0, len(schema.Properties))
		for name := range schema.Properties {
			names = append(names, name)
		}
		t.Fatalf("parameters = %v, want msg_vpn_name and two distinct long attributes", names)
	}
	for name := range schema.Properties {
		if len(name) > maxParamNameLen {
			t.Errorf("parameter %q is %d characters, want at most %d", name, len(name), maxParamNameLen)
		}
	}
}

func TestExecuteTool_LongBodyAttributesSharingPrefixKeepTheirOwnValues(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(&ToolsetConfig{SpecInline: longAttributesSpec(srv.URL), SpecFormat: "yaml"})
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	args := map[string]any{}
	for _, p := range tools[0].tmpl.Params {
		switch p.OriginalName {
		case longAttributeA:
			args[p.PyName] = "value-a"
		case longAttributeB:
			args[p.PyName] = "value-b"
		}
	}
	if len(args) != 2 {
		t.Fatalf("tool declares %d parameters for the two long attributes, want 2", len(args))
	}

	if _, err := mgr.ExecuteTool(t.Context(), tools[0], args); err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if received[longAttributeA] != "value-a" || received[longAttributeB] != "value-b" {
		t.Errorf("body = %v, want %s=value-a and %s=value-b", received, longAttributeA, longAttributeB)
	}
}

func TestDedupPyNames_SuffixSurvivesTheLengthLimit(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", maxParamNameLen)
	params := []paramSpec{
		{OriginalName: "one", PyName: long},
		{OriginalName: "two", PyName: long},
		{OriginalName: "three", PyName: long},
		{OriginalName: "taken", PyName: long[:maxParamNameLen-2] + "_0"},
	}
	dedupPyNames(params)

	seen := map[string]string{}
	for _, p := range params {
		if len(p.PyName) > maxParamNameLen {
			t.Errorf("%s: PyName %q is %d characters, want at most %d", p.OriginalName, p.PyName, len(p.PyName), maxParamNameLen)
		}
		if other, dup := seen[p.PyName]; dup {
			t.Errorf("%s and %s share PyName %q", other, p.OriginalName, p.PyName)
		}
		seen[p.PyName] = p.OriginalName
	}
	if params[0].PyName != long {
		t.Errorf("first occurrence = %q, want it unchanged", params[0].PyName)
	}
}

func TestRenderSchema_NestedPropertiesSharingPrefixGetDistinctShortKeys(t *testing.T) {
	t.Parallel()

	nested := &openapi3.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi3.Schemas{
			longAttributeA: &openapi3.SchemaRef{Value: makeStringSchema()},
			longAttributeB: &openapi3.SchemaRef{Value: makeStringSchema()},
			"msgVpnName":   &openapi3.SchemaRef{Value: makeStringSchema()},
		},
		Required: []string{longAttributeB},
	}
	out := renderSchema(nested, "")

	props, ok := out["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties in %v", out)
	}
	if len(props) != 3 {
		t.Fatalf("properties = %v, want msgVpnName and two distinct long attributes", props)
	}
	if _, ok := props["msgVpnName"]; !ok {
		t.Errorf("short property msgVpnName renamed; got %v", props)
	}
	for name := range props {
		if len(name) > maxParamNameLen {
			t.Errorf("property %q is %d characters, want at most %d", name, len(name), maxParamNameLen)
		}
	}
	required, _ := out["required"].([]string)
	if len(required) != 1 {
		t.Fatalf("required = %v, want one entry", required)
	}
	if _, ok := props[required[0]]; !ok {
		t.Errorf("required %q names no rendered property in %v", required[0], props)
	}
}

func nestedLongAttributesSpec(serverURL string) string {
	attrs := func(indent string) string {
		return "\n" + indent + longAttributeA + ": {type: string, description: a}" +
			"\n" + indent + longAttributeB + ": {type: string, description: b}"
	}
	return `openapi: "3.0.3"
info: {title: t, version: "1"}
servers: [{url: "` + serverURL + `"}]
paths:
  /msgVpns:
    post:
      operationId: createMsgVpn
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                settings:
                  type: object
                  properties:` + attrs(strings.Repeat(" ", 20)) + `
                rules:
                  type: array
                  items:
                    type: object
                    properties:` + attrs(strings.Repeat(" ", 22)) + `
      responses: {"200": {description: ok}}
`
}

func shortKeysByDescription(t *testing.T, schema map[string]any) map[string]string {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties in %v", schema)
	}
	keys := map[string]string{}
	for key, prop := range props {
		if len(key) > maxParamNameLen {
			t.Errorf("nested key %q is %d characters, want at most %d", key, len(key), maxParamNameLen)
		}
		desc, _ := prop.(map[string]any)["description"].(string)
		keys[desc] = key
	}
	if len(keys) != 2 || keys["a"] == keys["b"] {
		t.Fatalf("nested keys = %v, want two distinct keys", props)
	}
	return keys
}

func TestExecuteTool_NestedLongAttributesAreSentUnderTheirSpecNames(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	mgr := NewManager(slog.Default(), nil)
	tools, err := mgr.LoadAndDiscoverTools(&ToolsetConfig{SpecInline: nestedLongAttributesSpec(srv.URL), SpecFormat: "yaml"})
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	args := map[string]any{}
	for _, p := range tools[0].tmpl.Params {
		switch p.OriginalName {
		case "settings":
			keys := shortKeysByDescription(t, p.SchemaJSON)
			args[p.PyName] = map[string]any{keys["a"]: "settings-a", keys["b"]: "settings-b"}
		case "rules":
			items, _ := p.SchemaJSON["items"].(map[string]any)
			keys := shortKeysByDescription(t, items)
			args[p.PyName] = []any{map[string]any{keys["a"]: "rule-a", keys["b"]: "rule-b"}}
		}
	}

	if _, err := mgr.ExecuteTool(t.Context(), tools[0], args); err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	want := map[string]any{
		"settings": map[string]any{longAttributeA: "settings-a", longAttributeB: "settings-b"},
		"rules":    []any{map[string]any{longAttributeA: "rule-a", longAttributeB: "rule-b"}},
	}
	got, _ := json.Marshal(received)
	wantJSON, _ := json.Marshal(want)
	if string(got) != string(wantJSON) {
		t.Errorf("body = %s, want %s", got, wantJSON)
	}
}
