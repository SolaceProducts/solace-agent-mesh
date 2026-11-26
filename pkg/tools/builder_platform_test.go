// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type mockControlQuerier struct {
	responses map[string]json.RawMessage
	err       error
}

func (m *mockControlQuerier) Custom(_ context.Context, _, _, customPath string, _ json.RawMessage) (json.RawMessage, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.responses[customPath]; ok {
		return resp, nil
	}
	return nil, fmt.Errorf("no response configured for path: %s", customPath)
}

func TestListPlatformComponents_NilQuerier(t *testing.T) {
	lt := &ListPlatformComponentsTool{}
	toolCtx := &tool.Context{}
	result, err := lt.Execute(context.Background(), nil, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %s: %s", result.Status, result.Message)
	}
}

func TestListPlatformComponents_SingleType(t *testing.T) {
	lt := &ListPlatformComponentsTool{}
	querier := &mockControlQuerier{
		responses: map[string]json.RawMessage{
			"agents": json.RawMessage(`{"data":[{"id":"1","name":"TestAgent"}],"meta":{}}`),
		},
	}
	toolCtx := &tool.Context{ControlQuerier: querier}

	result, err := lt.Execute(context.Background(), map[string]any{"component_type": "agent"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["count"] != 1 {
		t.Errorf("expected count=1, got %v", result.Data["count"])
	}
	if len(result.DataObjects) != 1 {
		t.Fatalf("expected 1 DataObject, got %d", len(result.DataObjects))
	}
	if result.DataObjects[0].Name != "platform_agents.json" {
		t.Errorf("expected platform_agents.json, got %s", result.DataObjects[0].Name)
	}
	if !containsString(result.DataObjects[0].Tags, "__working") {
		t.Errorf("platform list artifact should be tagged __working, got %v", result.DataObjects[0].Tags)
	}
}

func TestListPlatformComponents_ResultMessageNamesTheTypeTheUserKnows(t *testing.T) {
	tests := []struct {
		componentType string
		resource      string
		wantMessage   string
	}{
		{"gateway", "gateways", "Found 2 entrypoint(s)"},
		{"agent", "agents", "Found 2 agent(s)"},
		{"workflow", "workflows", "Found 2 workflow(s)"},
	}
	for _, tt := range tests {
		t.Run(tt.componentType, func(t *testing.T) {
			querier := &mockControlQuerier{responses: map[string]json.RawMessage{
				tt.resource: json.RawMessage(`{"data":[{"id":"1","name":"A"},{"id":"2","name":"B"}],"meta":{}}`),
			}}
			result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": tt.componentType}, &tool.Context{ControlQuerier: querier})
			if err != nil {
				t.Fatal(err)
			}
			if result.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", result.Message, tt.wantMessage)
			}
			if result.Data["type"] != tt.componentType {
				t.Errorf("inline type = %v, want the wire value %q unchanged", result.Data["type"], tt.componentType)
			}
		})
	}
}

func TestListPlatformComponents_ModelsReportTypeAndDecisionNodes(t *testing.T) {
	lt := &ListPlatformComponentsTool{}
	querier := &mockControlQuerier{
		responses: map[string]json.RawMessage{
			"models": json.RawMessage(`{"data":[{"name":"general","modelType":"chat"},{"name":"jev","modelType":"decision"}],"decisionNodesEnabled":true}`),
		},
	}

	result, err := lt.Execute(context.Background(), map[string]any{"component_type": "model"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["count"] != 2 || result.Data["decision_nodes_enabled"] != true {
		t.Errorf("inline data = %v, want 2 models with decision nodes enabled", result.Data)
	}
	preview := result.DataObjects[0].Preview
	for _, want := range []string{"jev — decision model", "general — chat model", "Workflow decision nodes enabled: true"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview missing %q:\n%s", want, preview)
		}
	}
}

func TestListPlatformComponents_ModelsRejectAMalformedDecisionFlag(t *testing.T) {
	lt := &ListPlatformComponentsTool{}
	querier := &mockControlQuerier{
		responses: map[string]json.RawMessage{
			"models": json.RawMessage(`{"data":[],"decisionNodesEnabled":"yes"}`),
		},
	}

	result, err := lt.Execute(t.Context(), map[string]any{"component_type": "model"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" || !strings.Contains(result.Message, "decisionNodesEnabled") {
		t.Fatalf("result = %s %q, want an error naming decisionNodesEnabled", result.Status, result.Message)
	}
}

func TestListPlatformComponents_UnknownType(t *testing.T) {
	lt := &ListPlatformComponentsTool{}
	toolCtx := &tool.Context{ControlQuerier: &mockControlQuerier{}}

	result, err := lt.Execute(context.Background(), map[string]any{"component_type": "bogus"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for unknown type, got %s", result.Status)
	}
}

func TestListPlatformComponents_QuerierError(t *testing.T) {
	lt := &ListPlatformComponentsTool{}
	querier := &mockControlQuerier{err: fmt.Errorf("timeout")}
	toolCtx := &tool.Context{ControlQuerier: querier}

	result, err := lt.Execute(context.Background(), map[string]any{"component_type": "agent"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %s: %s", result.Status, result.Message)
	}
}

func TestGetComponentConfig_NilQuerier(t *testing.T) {
	gt := &GetComponentConfigTool{}
	toolCtx := &tool.Context{}
	result, err := gt.Execute(context.Background(), map[string]any{
		"component_type": "agent",
		"component_id":   "123",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %s", result.Status)
	}
}

func TestGetComponentConfig_Success(t *testing.T) {
	gt := &GetComponentConfigTool{}
	querier := &mockControlQuerier{
		responses: map[string]json.RawMessage{
			"agents/abc-123": json.RawMessage(`{"id":"abc-123","name":"TestAgent","type":"standard"}`),
		},
	}
	toolCtx := &tool.Context{ControlQuerier: querier}

	result, err := gt.Execute(context.Background(), map[string]any{
		"component_type": "agent",
		"component_id":   "abc-123",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if len(result.DataObjects) != 1 {
		t.Fatalf("expected 1 DataObject, got %d", len(result.DataObjects))
	}
	do := result.DataObjects[0]
	if do.MIMEType != "application/json" {
		t.Errorf("expected application/json, got %s", do.MIMEType)
	}
	if do.Disposition != tool.DispositionArtifactWithPreview {
		t.Errorf("expected artifact_with_preview disposition, got %s", do.Disposition)
	}
	var component map[string]any
	if err := json.Unmarshal(do.Content, &component); err != nil {
		t.Fatalf("unmarshal artifact content: %v", err)
	}
	if component["name"] != "TestAgent" {
		t.Errorf("expected name=TestAgent in artifact, got %v", component["name"])
	}
	if !containsString(do.Tags, "__working") {
		t.Errorf("component config artifact should be tagged __working (hidden from /artifacts), got %v", do.Tags)
	}
}

func TestGetComponentConfig_ArtifactFilenameFromName(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"TestAgent", "connector_TestAgent_config.json"},
		{"HR/Payroll API", "connector_HR_Payroll API_config.json"},
		{`Ops\Tools`, "connector_Ops_Tools_config.json"},
		{"v1..2", "connector_v1_2_config.json"},
		{"A/B ünï 🚀", "connector_A_B ünï 🚀_config.json"},
		{"", "connector_c-1_config.json"},
		{strings.Repeat("x", 255), "connector_" + strings.Repeat("x", 200) + "_config.json"},
		{"x" + strings.Repeat("é", 150), "connector_x" + strings.Repeat("é", 99) + "_config.json"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"id": "c-1", "name": tc.name})
			if err != nil {
				t.Fatal(err)
			}
			toolCtx := &tool.Context{ControlQuerier: &mockControlQuerier{responses: map[string]json.RawMessage{"connectors/c-1": body}}}

			result, err := (&GetComponentConfigTool{}).Execute(t.Context(), map[string]any{"component_type": "connector", "component_id": "c-1"}, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.DataObjects) != 1 {
				t.Fatalf("expected 1 DataObject, got %d (%s)", len(result.DataObjects), result.Message)
			}
			got := result.DataObjects[0].Name
			if got != tc.want {
				t.Errorf("artifact name = %q, want %q", got, tc.want)
			}
			if !artifact.IsFilenameSafe(got) {
				t.Errorf("artifact name %q is rejected by artifact.IsFilenameSafe, so the config would not be saved", got)
			}
		})
	}
}

func TestGetComponentConfig_UnparseableResponseReportsTheParseError(t *testing.T) {
	toolCtx := &tool.Context{ControlQuerier: &mockControlQuerier{responses: map[string]json.RawMessage{"connectors/c-1": json.RawMessage(`[1, 2]`)}}}

	result, err := (&GetComponentConfigTool{}).Execute(t.Context(), map[string]any{"component_type": "connector", "component_id": "c-1"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "cannot unmarshal") {
		t.Errorf("message should carry the JSON error, got %q", result.Message)
	}
}

func TestGetComponentConfig_MissingParams(t *testing.T) {
	gt := &GetComponentConfigTool{}
	toolCtx := &tool.Context{ControlQuerier: &mockControlQuerier{}}

	result, err := gt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for missing params, got %s", result.Status)
	}
}

func TestGetComponentConfig_UnknownType(t *testing.T) {
	gt := &GetComponentConfigTool{}
	toolCtx := &tool.Context{ControlQuerier: &mockControlQuerier{}}

	result, err := gt.Execute(context.Background(), map[string]any{
		"component_type": "bogus",
		"component_id":   "123",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for unknown type, got %s", result.Status)
	}
}

func TestSummaryLines_IncludeID(t *testing.T) {
	raw := json.RawMessage(`[
		{"id":"880e8400-e29b-41d4-a716-446655440000","name":"pet diet","type":"knowledge_base","subtype":"bedrock"},
		{"id":"990e8400-e29b-41d4-a716-446655440001","name":"Research","runtimeStatus":{"status":"running"}},
		{"id":"aa0e8400-e29b-41d4-a716-446655440002","name":"legacy-conn","subtype":"bedrock"},
		{"id":"bb0e8400-e29b-41d4-a716-446655440003","name":"Flaky","runtimeStatus":{"status":"degraded","problems":[{"id":"queue:x","severity":"degraded","detail":"temporary receive error; retrying automatically","since":"2026-01-01T00:00:00Z"}]}},
		{"name":"no-id-connector"}
	]`)
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	got := summaryLines(items, false)
	want := []string{
		"pet diet (880e8400-e29b-41d4-a716-446655440000) — knowledge_base/bedrock",
		"Research (990e8400-e29b-41d4-a716-446655440001) [running]",
		"legacy-conn (aa0e8400-e29b-41d4-a716-446655440002) — bedrock",
		"Flaky (bb0e8400-e29b-41d4-a716-446655440003) [degraded (1 problem(s))]",
		"no-id-connector",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d summaries %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("summary[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRuntimeStatusString(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"bare string (defensive fallback)", "running", "running"},
		{"object, no problems", map[string]any{"status": "running"}, "running"},
		{"object, empty problems", map[string]any{"status": "running", "problems": []any{}}, "running"},
		{
			"object, one problem",
			map[string]any{"status": "degraded", "problems": []any{map[string]any{"id": "x"}}},
			"degraded (1 problem(s))",
		},
		{
			"object, multiple problems",
			map[string]any{"status": "degraded", "problems": []any{map[string]any{"id": "x"}, map[string]any{"id": "y"}}},
			"degraded (2 problem(s))",
		},
		{"object, missing status", map[string]any{"problems": []any{}}, ""},
		{"unrelated type", 42, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeStatusString(tc.in); got != tc.want {
				t.Errorf("runtimeStatusString(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildConfigPreview_RuntimeStatusObjectShape(t *testing.T) {
	component := map[string]any{
		"name":          "Research",
		"runtimeStatus": map[string]any{"status": "degraded", "problems": []any{map[string]any{"id": "queue:x"}}},
	}
	got := buildConfigPreview("agent", component)
	if !strings.Contains(got, "Runtime Status: degraded (1 problem(s))") {
		t.Errorf("buildConfigPreview did not surface runtimeStatus, got:\n%s", got)
	}
}

func TestPlatformToolPreviews_SayEntrypointForTheGatewayType(t *testing.T) {
	listing := componentListing{items: []map[string]any{{"name": "order_events_entrypoint", "id": "g1"}}, total: 1}

	previews := map[string]string{
		"single-type list": buildSingleTypePreview("gateway", listing),
		"all-types list":   buildComponentListPreview(map[string]componentListing{"gateway": listing}, nil, []string{"gateway"}, 1),
		"config":           buildConfigPreview("gateway", map[string]any{"name": "order_events_entrypoint"}),
	}
	for name, preview := range previews {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(preview, "Entrypoint") {
				t.Errorf("preview should say Entrypoint:\n%s", preview)
			}
			if strings.Contains(strings.ToLower(preview), "gateway") {
				t.Errorf("preview leaks the internal type name:\n%s", preview)
			}
		})
	}
}

func TestGetComponentConfig_MessageSaysEntrypointForTheGatewayType(t *testing.T) {
	querier := &mockControlQuerier{responses: map[string]json.RawMessage{
		"gateways/g1": json.RawMessage(`{"id":"g1","name":"order_events_entrypoint"}`),
	}}
	result, err := (&GetComponentConfigTool{}).Execute(t.Context(), map[string]any{"component_type": "gateway", "component_id": "g1"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Retrieved entrypoint configuration: order_events_entrypoint"; result.Message != want {
		t.Errorf("message = %q, want %q", result.Message, want)
	}
}

const connectorTypesResponse = `{"data":[` +
	`{"type":"api","subtypes":[{"subtype":"openapi","description":"Connect to REST APIs via OpenAPI specification"}]},` +
	`{"type":"sql","subtypes":[{"subtype":"postgres","description":"Connect to PostgreSQL database"},{"subtype":"mysql"}]}` +
	`],"meta":{"pagination":{"pageNumber":1,"count":2,"pageSize":2,"nextPage":null,"totalPages":1}}}`

func TestListPlatformComponents_ConnectorTypes(t *testing.T) {
	querier := &mockControlQuerier{responses: map[string]json.RawMessage{"connectors/types": json.RawMessage(connectorTypesResponse)}}

	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "connector_type"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != "success" || result.Message != "Found 2 connector type(s)" {
		t.Fatalf("got %s %q", result.Status, result.Message)
	}
	if result.Data["type"] != "connector_type" || result.Data["count"] != 2 {
		t.Errorf("inline data = %v, want the wire type and 2 types", result.Data)
	}
	if len(result.DataObjects) != 1 || result.DataObjects[0].Name != "platform_connector_types.json" {
		t.Fatalf("artifact = %v, want platform_connector_types.json", result.DataObjects)
	}
	do := result.DataObjects[0]
	if !containsString(do.Tags, "__working") || !strings.Contains(string(do.Content), "Connect to PostgreSQL database") {
		t.Errorf("artifact must be __working and hold the subtype descriptions: %v %s", do.Tags, do.Content)
	}
	for _, want := range []string{
		"Connector Types (2)",
		"Enabled connector types on this deployment: api, sql",
		"  - api: openapi",
		"  - sql: postgres, mysql",
	} {
		if !strings.Contains(do.Preview, want) {
			t.Errorf("preview omits %q:\n%s", want, do.Preview)
		}
	}
}

func TestListPlatformComponents_ConnectorTypesEmptyAndUnreadable(t *testing.T) {
	empty := &mockControlQuerier{responses: map[string]json.RawMessage{"connectors/types": json.RawMessage(`{"data":[],"meta":{}}`)}}
	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "connector_type"}, &tool.Context{ControlQuerier: empty})
	if err != nil || result.Status != "success" {
		t.Fatalf("empty list: %v %v", result, err)
	}
	if !strings.Contains(result.DataObjects[0].Preview, "Enabled connector types on this deployment: none") {
		t.Errorf("an empty list must say none are enabled: %s", result.DataObjects[0].Preview)
	}

	bad := &mockControlQuerier{responses: map[string]json.RawMessage{"connectors/types": json.RawMessage(`{"data":[{"type":5}]}`)}}
	result, err = (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "connector_type"}, &tool.Context{ControlQuerier: bad})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.DataObjects[0].Preview, "unreadable") {
		t.Errorf("an unreadable list must not read as an empty one: %s", result.DataObjects[0].Preview)
	}
}

func TestListPlatformComponents_CataloguesAreNotInTheDefaultListing(t *testing.T) {
	for _, catalogue := range []string{"connector_type", "model", "tool"} {
		if slices.Contains(defaultListTypes, catalogue) {
			t.Errorf("defaultListTypes = %v, want %s left out", defaultListTypes, catalogue)
		}
	}
}

func TestListPlatformComponents_EnumMatchesResourceTypeMap(t *testing.T) {
	prop, _ := (&ListPlatformComponentsTool{}).Parameters().Properties["component_type"].(map[string]any)
	enum, _ := prop["enum"].([]string)

	want := append(slices.Sorted(maps.Keys(resourceTypeMap)), "all")
	slices.Sort(want)
	if got := slices.Sorted(slices.Values(enum)); !slices.Equal(got, want) {
		t.Errorf("component_type enum = %v, want every resourceTypeMap key plus all: %v", got, want)
	}
}

func TestGetComponentConfig_RejectsTheListOnlyConnectorType(t *testing.T) {
	querier := &mockControlQuerier{}
	result, err := (&GetComponentConfigTool{}).Execute(t.Context(),
		map[string]any{"component_type": "connector_type", "component_id": "sql"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" || !strings.Contains(result.Message, "platform_list_components") {
		t.Fatalf("got %s %q, want an error pointing at platform_list_components", result.Status, result.Message)
	}
}
