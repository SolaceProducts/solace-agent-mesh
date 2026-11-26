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

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type askedResourcesQuerier struct {
	mockControlQuerier
	asked []string
}

func (r *askedResourcesQuerier) Custom(ctx context.Context, app, method, customPath string, body json.RawMessage) (json.RawMessage, error) {
	r.asked = append(r.asked, customPath)
	return r.mockControlQuerier.Custom(ctx, app, method, customPath, body)
}

var heavyPlatformRecords = map[string]string{
	"agents":     `{"id":"a1","name":"Research","instanceName":"agent_a1","description":"Finds papers","deploymentStatus":"deployed","runtimeStatus":{"status":"running"},"toolsets":["web_tools"],"skillRefs":["sam-docs"],"connectors":[],"modelProvider":["general"],"systemPrompt":"HEAVY-PROMPT","additionalConfigurations":{"x":1},"skills":[{"name":"s"}],"createdAt":"2026-01-01T00:00:00.000Z","syncStatus":null}`,
	"workflows":  `{"id":"w1","name":"DataPipeline","description":"Moves data","deploymentStatus":"deployed","runtimeStatus":{"status":"running"},"appConfig":{"nodes":"HEAVY-DAG"},"createdBy":"u"}`,
	"gateways":   `{"id":"g1","name":"webhook","description":"Receives events","type":"webhook","publicUrl":"https://x","deploymentStatus":"deployed","runtimeStatus":{"status":"running"},"values":{"secret":"HEAVY-VALUES"},"slug":"s"}`,
	"connectors": `{"id":"c1","name":"sales-db","description":"Sales Postgres","type":"sql","subtype":"postgres","agentCount":2,"hasResource":false,"values":{"password":"HEAVY-VALUES"},"createdBy":"u"}`,
	"a2a-agents": `{"id":"r1","name":"Remote","instanceName":"agent_r1","description":"Remote agent","agentUrl":"https://r","deploymentStatus":"deployed","runtimeStatus":{"status":"running"},"agentAuthConfig":{"token":"HEAVY-AUTH"},"skills":[]}`,
	"skills":     `{"id":"s1","name":"sam-docs","label":"SAM docs","description":"Reads docs","tags":["docs"],"origin":"filesystem","discoveryStatus":"ready","hasTools":true,"toolsets":["t"],"bundledTools":["HEAVY-TOOLS"],"configSummary":{"x":1},"agents":[]}`,
	"tools":      `{"id":"t1","name":"xlsx_read","description":"Reads a workbook","origin":"filesystem","status":"available","parameters":{"properties":"HEAVY-SCHEMA"},"instructions":"HEAVY-INSTRUCTIONS","artifactParams":{"a":1}}`,
}

func listingResponses(resources ...string) map[string]json.RawMessage {
	responses := make(map[string]json.RawMessage, len(resources))
	for _, r := range resources {
		responses[r] = json.RawMessage(`{"data":[` + heavyPlatformRecords[r] + `],"meta":{"pagination":{"count":1}}}`)
	}
	return responses
}

var defaultListResources = []string{"agents", "workflows", "gateways", "connectors", "a2a-agents", "skills"}

func TestListPlatformComponents_DefaultListingLeavesToolsOut(t *testing.T) {
	querier := &askedResourcesQuerier{mockControlQuerier: mockControlQuerier{responses: listingResponses(defaultListResources...)}}

	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), nil, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Message != "Found 6 component(s) across all types" {
		t.Fatalf("got %s %q", result.Status, result.Message)
	}
	if slices.Contains(querier.asked, "tools") {
		t.Errorf("the default listing asked the platform for tools: %v", querier.asked)
	}
	if got := slices.Sorted(slices.Values(querier.asked)); !slices.Equal(got, slices.Sorted(slices.Values(defaultListResources))) {
		t.Errorf("asked %v, want exactly the default resources", got)
	}
	if strings.Contains(result.DataObjects[0].Preview, "Tools") || strings.Contains(string(result.DataObjects[0].Content), `"tools"`) {
		t.Errorf("tools reached the listing:\n%s", result.DataObjects[0].Preview)
	}
}

func TestListPlatformComponents_PinsTheFieldsEachRecordKeeps(t *testing.T) {
	want := map[string][]string{
		"agent":     {"connectors", "deploymentStatus", "description", "id", "instanceName", "modelProvider", "name", "runtimeStatus", "skillRefs", "toolsets"},
		"workflow":  {"deploymentStatus", "description", "id", "name", "runtimeStatus"},
		"gateway":   {"deploymentStatus", "description", "id", "name", "publicUrl", "runtimeStatus", "type"},
		"connector": {"agentCount", "description", "hasResource", "id", "name", "subtype", "type"},
		"a2a_agent": {"agentUrl", "deploymentStatus", "description", "id", "instanceName", "name", "runtimeStatus"},
		"skill":     {"description", "discoveryStatus", "hasTools", "id", "label", "name", "origin", "tags", "toolsets"},
		"tool":      {"description", "id", "name", "origin", "status"},
	}
	if got := slices.Sorted(maps.Keys(ListingFields)); !slices.Equal(got, slices.Sorted(maps.Keys(want))) {
		t.Fatalf("ListingFields covers %v, want %v", got, slices.Sorted(maps.Keys(want)))
	}
	for typeName, wantKeys := range want {
		if got := slices.Sorted(slices.Values(ListingFields[typeName])); !slices.Equal(got, wantKeys) {
			t.Errorf("ListingFields[%q] = %v, want %v", typeName, got, wantKeys)
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(heavyPlatformRecords[resourceTypeMap[typeName]]), &item); err != nil {
			t.Fatal(err)
		}
		got := projectListing(typeName, []map[string]any{item})
		if len(got) != 1 {
			t.Fatalf("%s: projected %d records, want 1", typeName, len(got))
		}
		if gotKeys := slices.Sorted(maps.Keys(got[0])); !slices.Equal(gotKeys, wantKeys) {
			t.Errorf("%s keeps %v, want %v", typeName, gotKeys, wantKeys)
		}
	}
	for _, typeName := range []string{"model", connectorTypeComponent} {
		item := map[string]any{"name": "x", "anything": "kept"}
		if got := projectListing(typeName, []map[string]any{item}); len(got) != 1 || got[0]["anything"] != "kept" {
			t.Errorf("%s must pass through untouched, got %v", typeName, got)
		}
	}
}

func TestListPlatformComponents_ArtifactIsCompactAndSlim(t *testing.T) {
	querier := &mockControlQuerier{responses: listingResponses(defaultListResources...)}

	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "all"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	content := string(result.DataObjects[0].Content)
	if strings.Contains(content, "HEAVY") {
		t.Errorf("a heavy field reached the artifact:\n%s", content)
	}
	if !strings.Contains(content, "\n") {
		t.Errorf("the artifact is one line, which load_artifact cannot page:\n%s", content)
	}
	var combined map[string]any
	if err := json.Unmarshal(result.DataObjects[0].Content, &combined); err != nil {
		t.Fatal(err)
	}
	if combined["total_count"] != float64(6) {
		t.Errorf("total_count = %v, want 6", combined["total_count"])
	}
	for _, key := range []string{"agents", "workflows", "gateways", "connectors", "a2a_agents", "skills"} {
		if _, ok := combined[key].([]any); !ok {
			t.Errorf("artifact lacks %s: %v", key, combined[key])
		}
	}
}

func TestListPlatformComponents_PreviewCarriesTheDescription(t *testing.T) {
	long := strings.Repeat("word ", 60)
	querier := &mockControlQuerier{responses: map[string]json.RawMessage{
		"agents": json.RawMessage(`{"data":[{"id":"a1","name":"Research","description":"Finds  papers\nand summarizes them","runtimeStatus":{"status":"running"}},{"id":"a2","name":"Talker","description":"` + long + `"},{"id":"a3","name":"Accents","description":"` + strings.Repeat("é", 200) + `"}],"meta":{"pagination":{"count":3}}}`),
	}}

	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "agent"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	preview := result.DataObjects[0].Preview
	if !strings.Contains(preview, "  - Research (a1) [running]: Finds papers and summarizes them\n") {
		t.Errorf("preview lacks the one-line description:\n%s", preview)
	}
	cut := strings.TrimSpace(strings.Repeat("word ", 32))
	if !strings.Contains(preview, "Talker (a2): "+cut+"…\n") {
		t.Errorf("preview should cut the long description at %d runes:\n%s", previewDescriptionMaxRunes, preview)
	}
	if !strings.Contains(preview, "Accents (a3): "+strings.Repeat("é", previewDescriptionMaxRunes)+"…\n") {
		t.Errorf("preview should cut a multi-byte description at %d runes, not bytes:\n%s", previewDescriptionMaxRunes, preview)
	}
}

func TestListPlatformComponents_ModelPreviewIsUnchangedByDescriptions(t *testing.T) {
	querier := &mockControlQuerier{responses: map[string]json.RawMessage{
		"models": json.RawMessage(`{"data":[{"name":"general","modelType":"chat","description":"The default","provider":"openai"}],"decisionNodesEnabled":false}`),
	}}

	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "model"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.DataObjects[0].Preview, "  - general — chat model\n") {
		t.Errorf("model preview changed:\n%s", result.DataObjects[0].Preview)
	}
	if !strings.Contains(string(result.DataObjects[0].Content), `"provider": "openai"`) {
		t.Errorf("model records must pass through untouched:\n%s", result.DataObjects[0].Content)
	}
}

func TestListPlatformComponents_ReportsThePlatformTotalWhenTheListingWasCut(t *testing.T) {
	querier := &mockControlQuerier{responses: map[string]json.RawMessage{
		"tools": json.RawMessage(`{"data":[{"id":"t1","name":"xlsx_read"},{"id":"t2","name":"xlsx_write"}],"meta":{"pagination":{"count":300}}}`),
	}}

	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "tool"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "Found 300 tool(s); the platform returned the first 2" {
		t.Errorf("message = %q", result.Message)
	}
	if result.Data["count"] != 300 {
		t.Errorf("inline count = %v, want the platform total 300", result.Data["count"])
	}
	if !strings.HasPrefix(result.DataObjects[0].Preview, "Tools (2 of 300 shown)\n") {
		t.Errorf("preview must say what was cut:\n%s", result.DataObjects[0].Preview)
	}
}

func TestListPlatformComponents_DefaultListingCountsThePlatformTotals(t *testing.T) {
	responses := listingResponses(defaultListResources...)
	responses["agents"] = json.RawMessage(`{"data":[{"id":"a1","name":"A"}],"meta":{"pagination":{"count":150}}}`)
	querier := &mockControlQuerier{responses: responses}

	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), nil, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["total_count"] != 155 || result.Message != "Found 155 component(s) across all types; the platform returned the first 1 of 150 agents" {
		t.Errorf("total = %v %q, want the platform totals summed and the cut named", result.Data["total_count"], result.Message)
	}
	if !strings.Contains(result.DataObjects[0].Preview, "\nAgents (1 of 150 shown):\n") {
		t.Errorf("preview must say what was cut:\n%s", result.DataObjects[0].Preview)
	}
}

func TestListPlatformComponents_ATypeThatCannotBeListedIsSaidSo(t *testing.T) {
	responses := listingResponses(defaultListResources...)
	delete(responses, "skills")
	querier := &mockControlQuerier{responses: responses}

	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), nil, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("one refused type must not fail the listing: %s %q", result.Status, result.Message)
	}
	if result.Message != "Found 5 component(s) across all types; could not list skills" {
		t.Errorf("message = %q", result.Message)
	}
	preview := result.DataObjects[0].Preview
	if !strings.Contains(preview, "\nSkills: could not be listed (failed to query skill: no response configured for path: skills)\n") {
		t.Errorf("preview hides the failed type:\n%s", preview)
	}
	var combined map[string]any
	if err := json.Unmarshal(result.DataObjects[0].Content, &combined); err != nil {
		t.Fatal(err)
	}
	if errEntry, _ := combined["skills"].(map[string]any); errEntry["error"] == nil {
		t.Errorf("artifact lacks the skills error: %v", combined["skills"])
	}
}

func TestListPlatformComponents_NothingListableIsAnError(t *testing.T) {
	querier := &mockControlQuerier{err: fmt.Errorf("timeout")}
	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), nil, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" || result.Message != "Could not list any component type; failed to query agent: timeout" {
		t.Errorf("got %s %q, want an error when no type could be listed", result.Status, result.Message)
	}
}

func TestGetComponentConfig_ReadsSkillsAndTools(t *testing.T) {
	enum := (&GetComponentConfigTool{}).Parameters().Properties["component_type"].(map[string]any)["enum"].([]string)
	querier := &askedResourcesQuerier{mockControlQuerier: mockControlQuerier{responses: map[string]json.RawMessage{
		"skills/s1": json.RawMessage(`{"id":"s1","name":"SecurityAudit","agents":["a1"]}`),
		"tools/t1":  json.RawMessage(`{"id":"t1","name":"xlsx_read","parameters":{"type":"object"}}`),
	}}}
	for _, tc := range []struct{ typeName, id, artifact string }{{"skill", "s1", "skill_SecurityAudit_config.json"}, {"tool", "t1", "tool_xlsx_read_config.json"}} {
		if !slices.Contains(enum, tc.typeName) {
			t.Errorf("component_type enum lacks %q: %v", tc.typeName, enum)
		}
		result, err := (&GetComponentConfigTool{}).Execute(t.Context(), map[string]any{"component_type": tc.typeName, "component_id": tc.id}, &tool.Context{ControlQuerier: querier})
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "success" || result.DataObjects[0].Name != tc.artifact {
			t.Errorf("%s: %s %q %v", tc.typeName, result.Status, result.Message, result.DataObjects)
		}
	}
	if got := slices.Sorted(slices.Values(querier.asked)); !slices.Equal(got, []string{"skills/s1", "tools/t1"}) {
		t.Errorf("asked %v", got)
	}
}

func TestListPlatformComponents_SingleTypeErrorMessageReadsAsASentence(t *testing.T) {
	querier := &mockControlQuerier{err: fmt.Errorf("timeout")}
	result, err := (&ListPlatformComponentsTool{}).Execute(t.Context(), map[string]any{"component_type": "gateway"}, &tool.Context{ControlQuerier: querier})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" || result.Message != "Failed to query entrypoint: timeout" {
		t.Errorf("got %s %q", result.Status, result.Message)
	}
}
