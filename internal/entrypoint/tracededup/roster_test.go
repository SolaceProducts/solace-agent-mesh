// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tracededup

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const credentialLadenFixture = `{"jsonrpc":"2.0","result":{"kind":"status-update","taskId":"t1","metadata":{"agent_name":"RosterAgent"},"status":{"state":"working","message":{"kind":"message","role":"agent","messageId":"m1","parts":[{"kind":"data","data":{"type":"llm_invocation","request":{"model":"m","contents":[],"config":{"system_instruction":"sys","tools":[` +
	`{"api_key":"sk-ENTRY-SECRET","auth":{"type":"oauth2","access_token":"ya29.ENTRY-TOKEN"},"function_declarations":[` +
	`{"name":"search_docs","description":"Search the docs","parameters":{"type":"object","properties":{"q":{"type":"string"}}},"auth_config":{"client_secret":"DECL-CLIENT-SECRET"},"headers":{"Authorization":"Bearer DECL-BEARER"},"credentials":"DECL-CREDENTIAL"},` +
	`{"name":"alpha_tool","description":"a","parameters":{"type":"object"},"x-api-key":"DECL-API-KEY"}]},` +
	`{"mcp_server":{"url":"https://mcp.example","token":"MCP-SERVER-TOKEN"}}` +
	`]}}}}]}}}}`

func TestB08_RosterBlobHoldsOnlyFunctionDeclarationFields(t *testing.T) {
	res := Process([]byte(credentialLadenFixture))
	if !res.Changed || res.ToolsHash == "" {
		t.Fatalf("tools block not extracted: changed=%v toolsHash=%q", res.Changed, res.ToolsHash)
	}
	var tools string
	for _, b := range res.Blobs {
		if b.Kind == KindTools {
			tools = b.Content
		}
	}
	if tools == "" {
		t.Fatal("no tools blob")
	}

	t.Run("B08_credentials_and_auth_are_not_declarations", func(t *testing.T) {
		for _, secret := range []string{
			"sk-ENTRY-SECRET", "ya29.ENTRY-TOKEN", "DECL-CLIENT-SECRET", "DECL-BEARER",
			"DECL-CREDENTIAL", "DECL-API-KEY", "MCP-SERVER-TOKEN", "https://mcp.example",
			`"auth"`, `"api_key"`, `"auth_config"`, `"headers"`, `"credentials"`, `"x-api-key"`,
		} {
			if strings.Contains(tools, secret) {
				t.Errorf("roster blob carries %s: %s", secret, tools)
			}
		}
		if strings.Contains(string(res.Payload), "SECRET") || strings.Contains(string(res.Payload), "TOKEN") {
			t.Errorf("deduped payload still carries auth material: %s", res.Payload)
		}
	})

	t.Run("B08_declarations_keep_name_description_parameters", func(t *testing.T) {
		var entries []struct {
			FunctionDeclarations []map[string]any `json:"function_declarations"`
			ToolKeys             []string         `json:"tool_keys"`
		}
		if err := json.Unmarshal([]byte(tools), &entries); err != nil {
			t.Fatalf("decode roster blob: %v", err)
		}
		if len(entries) != 2 || !slices.Equal(entries[1].ToolKeys, []string{"mcp_server"}) {
			t.Fatalf("roster = %s, want the declarations entry and the MCP server kept by name", tools)
		}
		var names []string
		for _, d := range entries[0].FunctionDeclarations {
			for key := range d {
				if key != "name" && key != "description" && key != "parameters" {
					t.Errorf("declaration %v carries non-declaration field %q", d["name"], key)
				}
			}
			if d["description"] == nil || d["parameters"] == nil {
				t.Errorf("declaration %v lost its description or parameters", d["name"])
			}
			names = append(names, d["name"].(string))
		}
		if !slices.Equal(names, []string{"alpha_tool", "search_docs"}) {
			t.Fatalf("declaration names = %v, want sorted [alpha_tool search_docs]", names)
		}
	})
}

func TestB08_RosterHashIgnoresStrippedFields(t *testing.T) {
	clean := strings.NewReplacer(
		`"api_key":"sk-ENTRY-SECRET","auth":{"type":"oauth2","access_token":"ya29.ENTRY-TOKEN"},`, "",
		`,"auth_config":{"client_secret":"DECL-CLIENT-SECRET"},"headers":{"Authorization":"Bearer DECL-BEARER"},"credentials":"DECL-CREDENTIAL"`, "",
		`,"x-api-key":"DECL-API-KEY"`, "",
		`{"mcp_server":{"url":"https://mcp.example","token":"MCP-SERVER-TOKEN"}}`, `{"mcp_server":{}}`,
	).Replace(credentialLadenFixture)
	if clean == credentialLadenFixture || strings.Contains(clean, "SECRET") {
		t.Fatal("fixture rewrite did not strip the auth material")
	}
	if a, b := Process([]byte(credentialLadenFixture)).ToolsHash, Process([]byte(clean)).ToolsHash; a != b {
		t.Fatalf("roster hash depends on auth material: %s vs %s", a, b)
	}
}

func TestB08_RosterKeepsToolsOfferedInOtherShapes(t *testing.T) {
	roster := func(tools string) string {
		t.Helper()
		var parsed any
		if err := json.Unmarshal([]byte(tools), &parsed); err != nil {
			t.Fatalf("fixture %s: %v", tools, err)
		}
		return canonicalizeTools(parsed)
	}
	for _, tc := range []struct {
		name  string
		tools string
		want  string
	}{
		{
			name:  "B08_openai_function_entries_keep_their_declaration",
			tools: `[{"type":"function","function":{"name":"lookup","description":"Look up","parameters":{"type":"object"},"api_key":"OPENAI-KEY"}}]`,
			want:  `[{"function":{"description":"Look up","name":"lookup","parameters":{"type":"object"}},"type":"function"}]`,
		},
		{
			name:  "B08_builtin_tool_entries_keep_their_names",
			tools: `[{"google_search":{}},{"code_execution":{"token":"SERVER-TOKEN"}}]`,
			want:  `[{"tool_keys":["google_search"]},{"tool_keys":["code_execution"]}]`,
		},
		{
			name:  "B08_flat_responses_function_keeps_its_declaration",
			tools: `[{"type":"function","name":"lookup","description":"Look up","parameters":{"type":"object"},"api_key":"RESP-KEY"}]`,
			want:  `[{"description":"Look up","name":"lookup","parameters":{"type":"object"},"type":"function"}]`,
		},
		{
			name:  "B08_typed_entry_keeps_its_type",
			tools: `[{"type":"mcp","server_label":"docs","server_url":"https://mcp.example.com/s/SECRET","headers":{"Authorization":"Bearer T"}}]`,
			want:  `[{"tool_keys":["headers","server_label","server_url","type"],"type":"mcp"}]`,
		},
		{
			name:  "B08_url_key_names_lose_their_credentials",
			tools: `[{"https://user:TOKEN@host.example.com:8443/sse?key=K":{}}]`,
			want:  `[{"tool_keys":["https://host.example.com:8443"]}]`,
		},
		{name: "B08_object_block_keeps_its_names", tools: `{"retrieval":{"datastore":"projects/x"}}`, want: `{"tool_keys":["retrieval"]}`},
		{name: "B08_null_block_stays_null", tools: `null`, want: `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := roster(tc.tools); got != tc.want {
				t.Fatalf("roster = %s, want %s", got, tc.want)
			}
		})
	}
}
