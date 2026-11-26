// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type describeQuerier struct {
	response json.RawMessage
	err      error

	calls                 int
	appName, method, path string
	body                  json.RawMessage
}

func (q *describeQuerier) Custom(_ context.Context, appName, method, path string, body json.RawMessage) (json.RawMessage, error) {
	q.calls++
	q.appName, q.method, q.path, q.body = appName, method, path, body
	return q.response, q.err
}

const describeResponseFixture = `{"tools":[
 {"ref":{"toolName":"get_current_time"},"status":"ok","kind":"builtin","name":"get_current_time","description":"Returns the current time.","parameters":{"type":"object","properties":{}},"result":{"type":"object","properties":{"time":{"type":"string"},"timezone":{"type":"string"}}},"toolNode":{"usable":true}},
 {"ref":{"connector":"Jira","tool":"search_issues"},"status":"ok","kind":"connector","connectorName":"Jira","connectorType":"mcp","name":"search_issues","parameters":{"type":"object","properties":{"jql":{"type":"string"},"max_results":{"type":"integer"}},"required":["jql"]},"result":null,"toolNode":{"usable":true},"notes":["result fields not declared"]},
 {"ref":{"toolName":"send_email"},"status":"not_found","reason":"no such tool on this platform","parameters":null,"result":null,"toolNode":{"usable":false},"suggestions":["send_mail"]}
]}`

func runDescribe(t *testing.T, q tool.ControlQuerier, args map[string]any) *tool.Result {
	t.Helper()
	res, err := (&DescribeToolsTool{}).Execute(t.Context(), args, &tool.Context{ControlQuerier: q})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func TestDescribeToolsTool_SendsTheWireRequest(t *testing.T) {
	q := &describeQuerier{response: json.RawMessage(describeResponseFixture)}

	res := runDescribe(t, q, map[string]any{"tools": []any{
		map[string]any{"tool_name": "get_current_time"},
		map[string]any{"connector": "Jira", "tool": "search_issues"},
		map[string]any{"connector": "CRM"},
	}})

	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %s: %s", res.Status, res.Message)
	}
	if q.appName != "platform" || q.method != "post" || q.path != "tools/describe" {
		t.Errorf("call = %s %s %s, want platform post tools/describe", q.appName, q.method, q.path)
	}
	const want = `{"tools":[{"toolName":"get_current_time"},{"connector":"Jira","tool":"search_issues"},{"connector":"CRM"}]}`
	if string(q.body) != want {
		t.Errorf("body = %s\nwant   %s", q.body, want)
	}
}

func TestDescribeToolsTool_RejectsBadRefsBeforeCallingThePlatform(t *testing.T) {
	tooMany := make([]any, describeMaxRefs+1)
	for i := range tooMany {
		tooMany[i] = map[string]any{"tool_name": "get_current_time"}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"no tools key", map[string]any{}, "tools is required"},
		{"empty list", map[string]any{"tools": []any{}}, "tools is required"},
		{"more than 25", map[string]any{"tools": tooMany}, "at most 25"},
		{"ref is a string", map[string]any{"tools": []any{"get_current_time"}}, "tools[0] must be an object"},
		{"tool_name and connector", map[string]any{"tools": []any{map[string]any{"tool_name": "a", "connector": "b"}}}, "not both"},
		{"tool_name and tool", map[string]any{"tools": []any{map[string]any{"tool_name": "a", "tool": "b"}}}, "not both"},
		{"tool without connector", map[string]any{"tools": []any{map[string]any{"tool": "a"}}}, "tool needs connector"},
		{"empty ref", map[string]any{"tools": []any{map[string]any{}}}, "needs tool_name or connector"},
		{"blank values", map[string]any{"tools": []any{map[string]any{"tool_name": "  "}}}, "needs tool_name or connector"},
		{"camelCase key", map[string]any{"tools": []any{map[string]any{"toolName": "a"}}}, `unknown field "toolName"`},
		{"tool is a number", map[string]any{"tools": []any{map[string]any{"connector": "jira", "tool": 5}}}, "tools[0].tool must be a string"},
		{"connector is a list", map[string]any{"tools": []any{map[string]any{"tool_name": "a"}, map[string]any{"connector": []any{"jira"}}}}, "tools[1].connector must be a string"},
		{"tool_name is a bool", map[string]any{"tools": []any{map[string]any{"tool_name": true}}}, "tools[0].tool_name must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &describeQuerier{response: json.RawMessage(describeResponseFixture)}
			res := runDescribe(t, q, tc.args)
			if res.Status != tool.StatusError || !strings.Contains(res.Message, tc.want) {
				t.Fatalf("got %s %q, want an error containing %q", res.Status, res.Message, tc.want)
			}
			if q.calls != 0 {
				t.Errorf("the platform was called %d time(s) for a request that cannot be valid", q.calls)
			}
		})
	}
}

func TestDescribeToolsTool_ANullRefFieldIsAbsent(t *testing.T) {
	q := &describeQuerier{response: json.RawMessage(describeResponseFixture)}

	res := runDescribe(t, q, map[string]any{"tools": []any{map[string]any{"connector": "jira", "tool": nil}}})

	if res.Status != tool.StatusSuccess {
		t.Fatalf("got %s %q: a null the model wrote for an omitted field is not a type error", res.Status, res.Message)
	}
	if string(q.body) != `{"tools":[{"connector":"jira"}]}` {
		t.Errorf("body = %s", q.body)
	}
}

func TestDescribeToolsTool_NoQuerier(t *testing.T) {
	res, err := (&DescribeToolsTool{}).Execute(t.Context(), map[string]any{"tools": []any{map[string]any{"tool_name": "a"}}}, &tool.Context{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != tool.StatusError {
		t.Fatalf("status = %s, want error", res.Status)
	}
}

func TestDescribeToolsTool_PlatformErrorIsReported(t *testing.T) {
	q := &describeQuerier{err: errors.New("insufficient permissions: requires tool:*:read")}
	res := runDescribe(t, q, map[string]any{"tools": []any{map[string]any{"tool_name": "a"}}})
	if res.Status != tool.StatusError || !strings.Contains(res.Message, "insufficient permissions") {
		t.Fatalf("got %s %q, want the platform's error", res.Status, res.Message)
	}
}

func TestDescribeToolsTool_UnreadableResponseIsAnError(t *testing.T) {
	q := &describeQuerier{response: json.RawMessage(`[1, 2]`)}
	res := runDescribe(t, q, map[string]any{"tools": []any{map[string]any{"tool_name": "a"}}})
	if res.Status != tool.StatusError {
		t.Fatalf("status = %s, want error: a response this tool cannot read must not read as an empty answer", res.Status)
	}
}

func TestDescribeToolsTool_ReturnsSummaryArtifactAndCompactData(t *testing.T) {
	q := &describeQuerier{response: json.RawMessage(describeResponseFixture)}
	res := runDescribe(t, q, map[string]any{"tools": []any{map[string]any{"tool_name": "get_current_time"}}})

	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %s: %s", res.Status, res.Message)
	}
	if want := "Described 3 tools: 2 ok, 1 not_found"; res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}
	if res.Data["summary"] != "3 tools: 2 ok, 1 not_found" || res.Data["artifact"] != "described_tools.json" {
		t.Errorf("summary/artifact = %v / %v", res.Data["summary"], res.Data["artifact"])
	}

	raw, err := json.Marshal(res.Data["tools"])
	if err != nil {
		t.Fatalf("marshal Data[tools]: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil || len(entries) != 3 {
		t.Fatalf("Data[tools] = %s (err %v), want 3 entries", raw, err)
	}
	for _, entry := range entries {
		for _, heavy := range []string{"parameters", "result", "description", "concreteName"} {
			if _, present := entry[heavy]; present {
				t.Errorf("Data[tools] carries %q; the schemas belong in the artifact, not in the model's context", heavy)
			}
		}
	}
	if entries[2]["status"] != "not_found" || entries[1]["connectorName"] != "Jira" {
		t.Errorf("Data[tools] lost status or connectorName: %v", entries)
	}

	if len(res.DataObjects) != 1 {
		t.Fatalf("len(DataObjects) = %d, want 1", len(res.DataObjects))
	}
	do := res.DataObjects[0]
	if do.Name != "described_tools.json" || do.MIMEType != "application/json" || do.Disposition != tool.DispositionArtifactWithPreview {
		t.Errorf("artifact = %s %s %s", do.Name, do.MIMEType, do.Disposition)
	}
	if !slices.Contains(do.Tags, a2a.ArtifactTagWorking) {
		t.Errorf("tags = %v, want %q so the interim result stays out of the user's files", do.Tags, a2a.ArtifactTagWorking)
	}
	if !json.Valid(do.Content) || !strings.Contains(string(do.Content), `"max_results"`) || !strings.Contains(string(do.Content), `"timezone"`) {
		t.Errorf("artifact content is not the full response: %s", do.Content)
	}
}

func TestDescribeToolsTool_ResultSchemaDeclaresExactlyTheDataKeys(t *testing.T) {
	q := &describeQuerier{response: json.RawMessage(describeResponseFixture)}
	res := runDescribe(t, q, map[string]any{"tools": []any{map[string]any{"tool_name": "get_current_time"}}})

	schema := tool.ResultSchemaOf(&DescribeToolsTool{})
	if schema == nil {
		t.Fatal("describe_tools declares no result schema")
	}
	declared := slices.Sorted(maps.Keys(schema.Properties))
	produced := slices.Sorted(maps.Keys(res.Data))
	if !slices.Equal(declared, produced) {
		t.Errorf("declared %v, produced %v", declared, produced)
	}
}

const truncatedListNote = "more tools than listed: describe one by name with tool"

func truncatedConnectorListResponse(t *testing.T, count int) json.RawMessage {
	t.Helper()
	listed := make([]describedTool, count)
	for i := range listed {
		listed[i] = describedTool{
			Ref:           describeToolRef{Connector: "bigapi"},
			Status:        describeStatusOK,
			Kind:          "connector",
			ConnectorName: "bigapi",
			Name:          fmt.Sprintf("operation_number_%03d", i),
			Parameters:    json.RawMessage(`{"type":"object","properties":{"first_parameter":{"type":"string"},"second_parameter":{"type":"string"}}}`),
			ToolNode:      describeToolNodeVerdict{Usable: true},
		}
	}
	listed[count-1].Notes = []string{truncatedListNote}
	raw, err := json.Marshal(describeToolsResponse{Tools: listed})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestDescribeToolsTool_ACutConnectorListIsSaidInEveryPreviewTierAndTheSummary(t *testing.T) {
	for _, count := range []int{3, 12, 50} {
		t.Run(fmt.Sprintf("%d tools", count), func(t *testing.T) {
			q := &describeQuerier{response: truncatedConnectorListResponse(t, count)}
			res := runDescribe(t, q, map[string]any{"tools": []any{map[string]any{"connector": "bigapi"}}})

			preview := res.DataObjects[0].Preview
			if !strings.Contains(preview, truncatedListNote) {
				t.Errorf("preview omits the cut-list note, so the model can believe it saw every tool:\n%s", preview)
			}
			if !strings.Contains(res.Message, "bigapi") || !strings.Contains(res.Message, "cut") {
				t.Errorf("message = %q, want it to say bigapi's list was cut", res.Message)
			}
			if summary, _ := res.Data["summary"].(string); !strings.Contains(summary, "cut") {
				t.Errorf("Data[summary] = %q, want it to say the list was cut", summary)
			}
		})
	}
}

func TestDescribeToolsTool_ACompleteListSaysNothingAboutCutting(t *testing.T) {
	q := &describeQuerier{response: json.RawMessage(describeResponseFixture)}
	res := runDescribe(t, q, map[string]any{"tools": []any{map[string]any{"tool_name": "get_current_time"}}})

	if strings.Contains(res.Message, "cut") || strings.Contains(res.DataObjects[0].Preview, truncatedListNote) {
		t.Errorf("message %q and preview %q mention a cut list for a complete one", res.Message, res.DataObjects[0].Preview)
	}
}

func TestDescribeToolsTool_DescriptionStatesTheToolsPerConnectorCap(t *testing.T) {
	if !strings.Contains((&DescribeToolsTool{}).Description(), "50") {
		t.Error("Description does not mention the 50-tool cap on a connector listing")
	}
}
