// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"testing"
)

func TestStructuredContentEmptyObjectStaysAddressable(t *testing.T) {
	parsed := mustParse(t, `{"content":[{"type":"text","text":"no matches"}],"structuredContent":{}}`)
	result := handle(t, parsed, "no matches")

	structured, ok := result.Data[structuredContentKey].(map[string]any)
	if !ok {
		t.Fatalf("Data[%q] missing or wrong type for an empty object: %#v",
			structuredContentKey, result.Data[structuredContentKey])
	}
	if len(structured) != 0 {
		t.Errorf("structured = %#v, want an empty map", structured)
	}
}

func TestStructuredContentEmptyObjectWithWhitespaceStaysAddressable(t *testing.T) {
	parsed := mustParse(t, `{"content":[{"type":"text","text":"x"}],"structuredContent": {  } }`)
	result := handle(t, parsed, "x")

	if _, ok := result.Data[structuredContentKey].(map[string]any); !ok {
		t.Errorf("Data[%q] missing for a whitespace-padded empty object: %#v",
			structuredContentKey, result.Data[structuredContentKey])
	}
}

func TestStructuredContentEmptyObjectWithNoContentItems(t *testing.T) {
	parsed := mustParse(t, `{"content":[],"structuredContent":{}}`)

	result, err := HandleMCPResponse(t.Context(), nil, "find_user",
		DefaultHandlerConfig(), newMCPToolCtx(t), parsed)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	structured, ok := result.Data[structuredContentKey].(map[string]any)
	if !ok {
		t.Fatalf("Data[%q] missing or wrong type: %#v", structuredContentKey, result.Data[structuredContentKey])
	}
	if len(structured) != 0 {
		t.Errorf("structured = %#v, want an empty map", structured)
	}
	if result.Message == "" {
		t.Error("Message was dropped; formatToolResult emits it alongside Data and it is " +
			"the only thing that says the tool returned nothing")
	}
}

func TestStructuredContentKeyIsTheWorkflowContract(t *testing.T) {
	if structuredContentKey != "mcp_structured_content" {
		t.Fatalf("structuredContentKey = %q; workflow YAML addresses this key by name, "+
			"so renaming it is a breaking change to authored conditions and output_mapping",
			structuredContentKey)
	}
}

func TestIsEmptyJSONObject(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want bool
	}{
		"empty object":            {`{}`, true},
		"padded empty object":     {"{\n\t}", true},
		"surrounding whitespace":  {"  {}  ", true},
		"explicit null":           {`null`, false},
		"populated object":        {`{"a":1}`, false},
		"empty array":             {`[]`, false},
		"scalar":                  {`"s"`, false},
		"number":                  {`7`, false},
		"empty bytes":             {``, false},
		"object with empty field": {`{"":1}`, false},
		"unbalanced open brace":   {`{x`, false},
		"two open braces":         {`{{`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isEmptyJSONObject(json.RawMessage(tc.raw)); got != tc.want {
				t.Errorf("isEmptyJSONObject(%s) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
