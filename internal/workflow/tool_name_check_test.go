// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"strings"
	"testing"
)

const shipmentToolNode = `
nodes:
  - id: ship
    type: tool
    tool_name: %s
`

func nodeCalling(tool string) string {
	return strings.Replace(shipmentToolNode, "%s", tool, 1)
}

func TestUnknownToolNodeProblems_FlagsANameNoPinnedSourceRegisters(t *testing.T) {
	cases := []struct {
		name      string
		appConfig string
		wantNames string
	}{
		{
			name: "mcp entry pinned by tool_name",
			appConfig: `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("track_package"),
			wantNames: "get_shipment",
		},
		{
			name: "mcp entry pinned by allow_list",
			appConfig: `
tools:
  - tool_type: mcp
    allow_list: [get_shipment, create_shipment]
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("cancel_shipment"),
			wantNames: "create_shipment, get_shipment",
		},
		{
			name: "mcp entry pinned by an inline manifest",
			appConfig: `
tools:
  - tool_type: mcp
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
    manifest:
      - name: get_shipment
      - id: create_shipment
` + nodeCalling("cancel_shipment"),
			wantNames: "create_shipment, get_shipment",
		},
		{
			name: "a manifest filtered by deny_list registers only what survives",
			appConfig: `
tools:
  - tool_type: mcp
    deny_list: [create_shipment]
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
    manifest:
      - name: get_shipment
      - name: create_shipment
` + nodeCalling("create_shipment"),
			wantNames: "get_shipment",
		},
		{
			name: "the registered name carries the prefix, so the bare name is unknown",
			appConfig: `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    tool_name_prefix: carrier
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("get_shipment"),
			wantNames: "carrier_get_shipment",
		},
		{
			name: "the registered name is sanitized, so the server's spelling is unknown",
			appConfig: `
tools:
  - tool_type: mcp
    tool_name: get-shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("get-shipment"),
			wantNames: "get_shipment",
		},
		{
			name: "pinned mcp beside a builtin entry",
			appConfig: `
tools:
  - tool_type: builtin
    tool_name: get_current_time
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("track_package"),
			wantNames: "get_current_time, get_shipment",
		},
		{
			name: "nested body with the entries beside it",
			appConfig: `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
workflow:
  nodes:
    - id: ship
      type: tool
      tool_name: track_package
`,
			wantNames: "get_shipment",
		},
		{
			name: "nested body with the entries inside it",
			appConfig: `
workflow:
  tools:
    - tool_type: mcp
      tool_name: get_shipment
      connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
  nodes:
    - id: ship
      type: tool
      toolName: track_package
`,
			wantNames: "get_shipment",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := UnknownToolNodeProblems(appConfigFromYAML(t, tc.appConfig))
			if len(problems) != 1 {
				t.Fatalf("problems = %v, want exactly one", problems)
			}
			p := problems[0]
			if !p.Fatal {
				t.Errorf("a name no source registers fails every run, so it must be Fatal")
			}
			for _, want := range []string{`"ship"`, "references unknown tool", "the tools it registers are: " + tc.wantNames} {
				if !strings.Contains(p.Message, want) {
					t.Errorf("message %q is missing %q", p.Message, want)
				}
			}
		})
	}
}

func TestUnknownToolNodeProblems_NamesEveryOffendingNode(t *testing.T) {
	problems := UnknownToolNodeProblems(appConfigFromYAML(t, `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
nodes:
  - id: a
    type: tool
    tool_name: nope_one
  - id: ok
    type: tool
    tool_name: get_shipment
  - id: b
    type: tool
    tool_name: nope_two
`))
	if len(problems) != 2 || !strings.Contains(problems[0].Message, `"a"`) || !strings.Contains(problems[1].Message, `"b"`) {
		t.Fatalf("problems = %v, want one each for a and b", problems)
	}
}

func TestUnknownToolNodeProblems_NeverRefusesWhereTheNamesAreNotKnowable(t *testing.T) {
	cases := map[string]string{
		"an mcp entry with no filter lists whatever the server offers": `
tools:
  - tool_type: mcp
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("anything"),
		"a deny_list alone pins nothing": `
tools:
  - tool_type: mcp
    deny_list: [delete_everything]
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("anything"),
		"a manifest_file is read from the engine host's disk": `
tools:
  - tool_type: mcp
    manifest_file: /etc/sam/tools.yaml
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("anything"),
		"an unpinned mcp entry beside a pinned one": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
  - tool_type: mcp
    connection_params: {type: streamable-http, url: http://localhost:9998/mcp}
` + nodeCalling("anything"),
		"an openapi entry registers one tool per operation": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
  - tool_type: openapi
    specification_url: http://localhost:9997/openapi.json
` + nodeCalling("anything"),
		"an entry of a type the checker does not know": `
tools:
  - tool_type: something_new
` + nodeCalling("anything"),
		"an entry with no tool_type": `
tools:
  - tool_name: get_shipment
` + nodeCalling("anything"),
		"a builtin-group, whose members the loading build decides and a CLI older than the platform would not know": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
  - tool_type: builtin-group
    group_name: builtin_file_tools
` + nodeCalling("pdf_extract_text_added_later"),
		"a prefix the engine expands from the environment": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    tool_name_prefix: "${CARRIER_PREFIX}"
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("acme_get_shipment"),
		"a tool_name the engine expands from the environment": `
tools:
  - tool_type: mcp
    tool_name: "${CARRIER_TOOL}"
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("get_shipment"),
		"an allow_list entry the engine expands from the environment": `
tools:
  - tool_type: mcp
    allow_list: ["${CARRIER_TOOL}"]
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("get_shipment"),
		"a builtin entry named from the environment": `
tools:
  - tool_type: builtin
    tool_name: "${TIME_TOOL}"
` + nodeCalling("get_current_time"),
		"a node whose tool_name is templated": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("\"{{workflow.input.tool}}\""),
		"a node whose tool_name is an environment reference": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("\"${CARRIER_TOOL}\""),
		"toolsets expand into tools at deploy": `
toolsets: [Shipping]
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("Shipping__track"),
		"toolsets beside a nested body": `
toolsets: [Shipping]
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
workflow:
  nodes:
    - id: ship
      type: tool
      tool_name: Shipping__track
`,
		"workflow-level connectors expand into tools": `
connectors:
  - name: ops
    type: email
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("send_email_ab12cd"),
		"a node-level connector adds every tool of its connector": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
nodes:
  - id: carrier
    type: tool
    connector: carrier-api
    tool: get_shipment
  - id: ship
    type: tool
    tool_name: get_shipment_ab12cd
`,
		"no tool source at all is the schema check's to report": nodeCalling("get_current_time"),
		"empty source lists declare nothing": `
tools: []
` + nodeCalling("get_current_time"),
		"a tools list of non-maps registers nothing and counts for nothing": `
tools: [get_shipment]
` + nodeCalling("get_current_time"),
		"a node of another type": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
nodes:
  - id: a
    type: agent
    agent_name: Worker
    tool_name: not_a_tool_node
`,
		"a tool node with no tool_name": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
nodes:
  - id: ship
    type: tool
`,
		"a connector-bound node whose tool_name hydration will replace": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
nodes:
  - id: ship
    type: tool
    connector: carrier-api
    tool_name: placeholder
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			for _, p := range UnknownToolNodeProblems(appConfigFromYAML(t, src)) {
				if p.Fatal || !strings.Contains(p.Message, "mcp entry names its tools only once its server is connected") {
					t.Fatalf("want nothing but the unlisted-mcp advisory, got %+v", p)
				}
			}
		})
	}
}

func TestUnknownToolNodeProblems_AcceptsEveryNameAPinnedSourceRegisters(t *testing.T) {
	cases := map[string]string{
		"tool_name pin": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("get_shipment"),
		"allow_list pin, either member": `
tools:
  - tool_type: mcp
    allow_list: [get_shipment, create_shipment]
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
nodes:
  - id: a
    type: tool
    tool_name: get_shipment
  - id: b
    type: tool
    tool_name: create_shipment
`,
		"manifest pin": `
tools:
  - tool_type: mcp
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
    manifest:
      - name: get_shipment
` + nodeCalling("get_shipment"),
		"prefixed name": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    tool_name_prefix: carrier
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("carrier_get_shipment"),
		"sanitized name": `
tools:
  - tool_type: mcp
    tool_name: get-shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("get_shipment"),
		"builtin entry beside a pinned mcp entry": `
tools:
  - tool_type: builtin
    tool_name: get_current_time
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
nodes:
  - id: a
    type: tool
    tool_name: get_current_time
  - id: b
    type: tool
    tool_name: get_shipment
`,
		"a member of a builtin-group": `
tools:
  - tool_type: builtin-group
    group_name: web_tools
` + nodeCalling("web_request"),
		"a toolset-qualified builtin entry": `
tools:
  - tool_type: builtin
    tool_name: Shipping__track
` + nodeCalling("Shipping__track"),
		"the camelCase spelling of the node's key": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
nodes:
  - id: ship
    type: tool
    toolName: get_shipment
`,
		"an inline manifest with a prefix": `
tools:
  - tool_type: mcp
    tool_name_prefix: carrier
    manifest: [{name: get_shipment}]
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("carrier_get_shipment"),
		"an inline manifest with a prefix that already ends in an underscore": `
tools:
  - tool_type: mcp
    tool_name_prefix: carrier_
    manifest: [{name: get_shipment}]
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
` + nodeCalling("carrier_get_shipment"),
		"nested body with app-level entries": `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
workflow:
  nodes:
    - id: ship
      type: tool
      tool_name: get_shipment
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if problems := UnknownToolNodeProblems(appConfigFromYAML(t, src)); len(problems) != 0 {
				t.Fatalf("want no problems, got %v", problems)
			}
		})
	}
}

const unpinnedMCPEntryYAML = `
tools:
  - tool_type: mcp
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
`

func TestUnknownToolNodeProblems_AdvisesWhenOnlyAnUnpinnedMCPEntryHidesTheNames(t *testing.T) {
	problems := UnknownToolNodeProblems(appConfigFromYAML(t, unpinnedMCPEntryYAML+`
nodes:
  - {id: forecast, type: tool, tool_name: get_forecast}
  - {id: alerts, type: tool, tool_name: get_alerts}
`))
	if len(problems) != 1 || problems[0].Fatal {
		t.Fatalf("problems = %v, want exactly one advisory", problems)
	}
	for _, want := range []string{`"forecast" (calls "get_forecast")`, `"alerts" (calls "get_alerts")`, "not checked", "allow_list or an inline manifest"} {
		if !strings.Contains(problems[0].Message, want) {
			t.Errorf("advisory %q is missing %q", problems[0].Message, want)
		}
	}
}

func TestUnknownToolNodeProblems_AdvisoryNamesOnlyTheNodesNothingElseRegisters(t *testing.T) {
	problems := UnknownToolNodeProblems(appConfigFromYAML(t, unpinnedMCPEntryYAML+`  - {tool_type: builtin, tool_name: get_current_time}
nodes:
  - {id: clock, type: tool, tool_name: get_current_time}
  - {id: forecast, type: tool, tool_name: get_forecast}
`))
	if len(problems) != 1 || strings.Contains(problems[0].Message, "get_current_time") || !strings.Contains(problems[0].Message, "get_forecast") {
		t.Fatalf("problems = %v, want an advisory for the forecast node alone", problems)
	}
}

func TestUnknownToolNodeProblems_NoAdvisoryWhenAnythingElseOpensTheCatalog(t *testing.T) {
	cases := map[string]string{
		"toolsets":                             "toolsets: [Shipping]\n" + unpinnedMCPEntryYAML + "nodes:\n  - {id: n, type: tool, tool_name: x}\n",
		"a node connector":                     unpinnedMCPEntryYAML + "nodes:\n  - {id: c, type: tool, connector: carrier}\n  - {id: n, type: tool, tool_name: x}\n",
		"an openapi entry":                     unpinnedMCPEntryYAML + "  - {tool_type: openapi, specification_url: http://spec.example/s.json}\nnodes:\n  - {id: n, type: tool, tool_name: x}\n",
		"a builtin-group":                      unpinnedMCPEntryYAML + "  - {tool_type: builtin-group, group_name: web_tools}\nnodes:\n  - {id: n, type: tool, tool_name: x}\n",
		"no tool nodes":                        unpinnedMCPEntryYAML + "nodes:\n  - {id: a, type: agent, agent_name: Worker}\n",
		"a templated name":                     unpinnedMCPEntryYAML + "nodes:\n  - {id: n, type: tool, tool_name: \"{{workflow.input.tool}}\"}\n",
		"a registered name":                    unpinnedMCPEntryYAML + "  - {tool_type: builtin, tool_name: web_request}\nnodes:\n  - {id: n, type: tool, tool_name: web_request}\n",
		"no tool source at all":                "nodes:\n  - {id: n, type: tool, tool_name: x}\n",
		"a manifest name from the environment": "tools:\n  - {tool_type: mcp, manifest: [{name: \"${CARRIER_TOOL}\"}, {name: other}], connection_params: {type: streamable-http, url: \"http://x/mcp\"}}\nnodes:\n  - {id: n, type: tool, tool_name: get_shipment}\n",
		"a manifest_file":                      "tools:\n  - {tool_type: mcp, manifest_file: /etc/sam/m.yaml, connection_params: {type: streamable-http, url: \"http://x/mcp\"}}\n" + "nodes:\n  - {id: n, type: tool, tool_name: x}\n",
		"a variable prefix":                    "tools:\n  - {tool_type: mcp, tool_name: a, tool_name_prefix: \"${P}\", connection_params: {type: streamable-http, url: \"http://x/mcp\"}}\nnodes:\n  - {id: n, type: tool, tool_name: x}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			problems := UnknownToolNodeProblems(appConfigFromYAML(t, src))
			if len(problems) != 0 {
				t.Fatalf("want nothing, got %v", problems)
			}
		})
	}
}

func TestUnknownToolNodeProblems_ReadsAConfigTheWayTheEngineWillRenderIt(t *testing.T) {
	config := map[string]any{
		"tools": []any{map[string]any{
			"tool_type":         "mcp",
			"connection_params": map[string]any{"type": "streamable-http", "url": "http://localhost:9999/mcp"},
			"allow_list":        []string{"get_shipment"},
			"manifest":          []map[string]any{{"name": "get_shipment"}},
		}},
		"nodes": []any{map[string]any{"id": "ship", "type": "tool", "tool_name": "track_package"}},
	}
	problems := UnknownToolNodeProblems(config)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "the tools it registers are: get_shipment") {
		t.Fatalf("problems = %v, want the manifest-pinned entry recognised from in-memory slices", problems)
	}
}

func TestPinnedMCPToolNames(t *testing.T) {
	cases := []struct {
		name    string
		entry   map[string]any
		want    []string
		pinning mcpPinning
	}{
		{"tool_name", map[string]any{"tool_name": "a-b"}, []string{"a_b"}, mcpPinned},
		{"prefixed allow_list", map[string]any{"allow_list": []any{"x", "y"}, "tool_name_prefix": "p"}, []string{"p_x", "p_y"}, mcpPinned},
		{"manifest with a prefix", map[string]any{"tool_name_prefix": "carrier", "manifest": []any{map[string]any{"name": "get_shipment"}}}, []string{"carrier_get_shipment"}, mcpPinned},
		{"manifest with a prefix that ends in an underscore", map[string]any{"tool_name_prefix": "carrier_", "manifest": []any{map[string]any{"name": "get-shipment"}}}, []string{"carrier_get_shipment"}, mcpPinned},
		{"manifest filtered by deny_list", map[string]any{"manifest": []any{map[string]any{"name": "x"}, map[string]any{"id": "y"}}, "deny_list": []any{"x"}}, []string{"y"}, mcpPinned},
		{"nothing pins it", map[string]any{"deny_list": []any{"x"}}, nil, mcpUnlisted},
		{"manifest_file", map[string]any{"manifest_file": "/tmp/m.yaml", "tool_name": "x"}, nil, mcpOpaque},
		{"a manifest entry with no name", map[string]any{"manifest": []any{map[string]any{"description": "d"}}}, nil, mcpOpaque},
		{"a manifest name from the environment", map[string]any{"manifest": []any{map[string]any{"name": "x"}, map[string]any{"name": "${CARRIER_TOOL}"}}}, nil, mcpOpaque},
		{"a manifest id that is a template", map[string]any{"manifest": []any{map[string]any{"id": "{{ x }}"}}}, nil, mcpOpaque},
		{"a tool_name from the environment", map[string]any{"tool_name": "${X}"}, nil, mcpOpaque},
		{"a prefix from the environment", map[string]any{"tool_name": "x", "tool_name_prefix": "${P}"}, nil, mcpOpaque},
		{"an allow_list from the environment", map[string]any{"allow_list": []any{"${X}"}}, nil, mcpOpaque},
		{"a deny_list from the environment", map[string]any{"allow_list": []any{"x"}, "deny_list": []any{"${X}"}}, nil, mcpOpaque},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, pinning := pinnedMCPToolNames(tc.entry)
			if pinning != tc.pinning || strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("pinnedMCPToolNames = %v, %v; want %v, %v", got, pinning, tc.want, tc.pinning)
			}
		})
	}
}

func TestUnknownToolNodeProblems_RefusalNamesWhatTheSourcesRegister(t *testing.T) {
	problems := UnknownToolNodeProblems(appConfigFromYAML(t, `
tools:
  - tool_type: mcp
    tool_name: get_shipment
    connection_params: {type: streamable-http, url: http://localhost:9999/mcp}
`+nodeCalling("track_package")))
	if len(problems) != 1 || !problems[0].Fatal {
		t.Fatalf("problems = %v, want one refusal", problems)
	}
	for _, want := range []string{`"references unknown tool"`, "get_shipment"} {
		if !strings.Contains(problems[0].Message, want) {
			t.Errorf("message %q is missing %q", problems[0].Message, want)
		}
	}
}
