// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/maximhq/bifrost/core/schemas"
)

func testLogger() *slog.Logger {
	return slog.Default()
}

func TestMCPTool_Interface(t *testing.T) {
	var _ tool.BuiltinTool = (*MCPTool)(nil)
}

func TestMCPTool_Properties(t *testing.T) {
	desc := "A test tool"
	ct := schemas.ChatTool{
		Type: schemas.ChatToolTypeFunction,
		Function: &schemas.ChatToolFunction{
			Name:        "test_tool",
			Description: &desc,
		},
	}

	cfg := &ToolsetConfig{
		ToolNamePrefix: "mcp_",
		RequiredScopes: []string{"read"},
		ClientID:       "test_client",
	}

	mt := newMCPTool("mcp_test_tool", "test_client-test_tool", &ct, nil, cfg, nil, testLogger())
	if mt.Name() != "mcp_test_tool" {
		t.Errorf("Name() = %q", mt.Name())
	}
	if mt.Description() != "A test tool" {
		t.Errorf("Description() = %q", mt.Description())
	}
	if mt.IsLongRunning() {
		t.Error("IsLongRunning should be false")
	}
	if mt.Instructions() != "" {
		t.Error("Instructions should be empty")
	}
	if mt.ContextParam() != "" {
		t.Error("ContextParam should be empty")
	}
	if mt.ArtifactParams() != nil {
		t.Error("ArtifactParams should be nil")
	}
	if mt.RawStringArgs() != nil {
		t.Error("RawStringArgs should be nil")
	}
	if len(mt.RequiredScopes()) != 1 || mt.RequiredScopes()[0] != "read" {
		t.Errorf("RequiredScopes = %v", mt.RequiredScopes())
	}
}

func TestConvertParameters_Nil(t *testing.T) {
	schema := convertParameters(nil)
	if schema.Type != "object" {
		t.Errorf("type = %q, want object", schema.Type)
	}
}

func TestConvertParameters_WithProperties(t *testing.T) {
	props := schemas.NewOrderedMap()
	props.Set("filename", map[string]any{
		"type":        "string",
		"description": "The file to read",
	})

	params := &schemas.ToolFunctionParameters{
		Type:       "object",
		Properties: props,
		Required:   []string{"filename"},
	}

	schema := convertParameters(params)
	if schema.Type != "object" {
		t.Errorf("type = %q", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "filename" {
		t.Errorf("required = %v", schema.Required)
	}
	if _, ok := schema.Properties["filename"]; !ok {
		t.Error("missing filename property")
	}
	if schema.Raw == nil {
		t.Error("Raw schema is nil")
	}
	rawProps, ok := schema.Raw["properties"].(map[string]any)
	if !ok {
		t.Error("Raw properties is not a map")
	}
	if _, ok := rawProps["filename"]; !ok {
		t.Error("missing filename in Raw properties")
	}
}

func TestNewManifestMCPTool_RawSchemaPassthrough(t *testing.T) {
	raw := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cloudId": map[string]any{
				"type":        "string",
				"description": "Cloud ID",
			},
			"ids": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array", "items": map[string]any{"type": "number"}},
				},
			},
		},
		"required":             []any{"cloudId"},
		"additionalProperties": false,
		"$schema":              "http://json-schema.org/draft-07/schema#",
	}
	mt := ManifestTool{
		Name:        "getConfluenceSpaces",
		Description: "List Confluence spaces.",
		InputSchema: raw,
	}
	cfg := &ToolsetConfig{
		ClientID:       "atlassian",
		RequiredScopes: []string{"tool:space:read"},
	}

	tl := newManifestMCPTool("getConfluenceSpaces", "atlassian-getConfluenceSpaces", mt, nil, cfg, nil, testLogger())

	if tl.Name() != "getConfluenceSpaces" {
		t.Errorf("Name() = %q, want getConfluenceSpaces", tl.Name())
	}
	if tl.Description() != "List Confluence spaces." {
		t.Errorf("Description() = %q", tl.Description())
	}
	params := tl.Parameters()
	if params == nil || params.Raw == nil {
		t.Fatal("Parameters() or Raw is nil")
	}

	if got := params.Raw["$schema"]; got != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("Raw[$schema] = %v, want draft-07 URI", got)
	}
	if got := params.Raw["additionalProperties"]; got != false {
		t.Errorf("Raw[additionalProperties] = %v, want false", got)
	}
	rawProps, ok := params.Raw["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Raw[properties] = %T, want map[string]any", params.Raw["properties"])
	}
	ids, ok := rawProps["ids"].(map[string]any)
	if !ok {
		t.Fatalf("Raw[properties][ids] = %T, want map[string]any", rawProps["ids"])
	}
	if _, ok := ids["anyOf"].([]any); !ok {
		t.Errorf("Raw[properties][ids][anyOf] = %T, want []any", ids["anyOf"])
	}

	if params.Type != "object" {
		t.Errorf("Parameters().Type = %q, want object", params.Type)
	}
	if len(params.Required) != 1 || params.Required[0] != "cloudId" {
		t.Errorf("Parameters().Required = %v, want [cloudId]", params.Required)
	}
}

func TestNewManifestMCPTool_NoDescription(t *testing.T) {
	mt := ManifestTool{
		Name:        "noDesc",
		InputSchema: map[string]any{"type": "object"},
	}
	cfg := &ToolsetConfig{ClientID: "x"}
	tl := newManifestMCPTool("noDesc", "x-noDesc", mt, nil, cfg, nil, testLogger())
	if tl.Description() != "" {
		t.Errorf("Description() = %q, want empty", tl.Description())
	}
}

func TestDiscoverTools_FromManifest_RawPassthrough(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            "https://unused.example.com/sse",
		ClientID:       "manifest_raw",
		Manifest: []ManifestTool{
			{
				Name:        "searchThings",
				Description: "Search things.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"q": map[string]any{"type": "string"},
					},
					"additionalProperties": false,
					"$schema":              "http://json-schema.org/draft-07/schema#",
				},
			},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	params := tools[0].Parameters()
	if got := params.Raw["$schema"]; got != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("Raw[$schema] = %v, want preserved", got)
	}
	if got := params.Raw["additionalProperties"]; got != false {
		t.Errorf("Raw[additionalProperties] = %v, want preserved", got)
	}
}

func TestBuildSAMToolName(t *testing.T) {
	tests := []struct {
		original string
		prefix   string
		want     string
	}{
		{"read_file", "", "read_file"},
		{"read-file", "", "read_file"},
		{"read_file", "fs_", "fs_read_file"},
		{"get.data", "mcp_", "mcp_get_data"},
	}

	for _, tc := range tests {
		got := buildSAMToolName(tc.original, tc.prefix)
		if got != tc.want {
			t.Errorf("buildSAMToolName(%q, %q) = %q, want %q", tc.original, tc.prefix, got, tc.want)
		}
	}
}

func TestNewDirectMCPTool_SurfacesDeclaredOutputSchema(t *testing.T) {
	mcpTool := mcpgo.Tool{
		Name:        "detect_objects",
		Description: "Detect objects in an image",
		InputSchema: mcpgo.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{"image_url": map[string]any{"type": "string"}},
			Required:   []string{"image_url"},
		},
		OutputSchema: mcpgo.ToolOutputSchema{
			Type: "object",
			Properties: map[string]any{
				"objects":      map[string]any{"type": "array"},
				"object_count": map[string]any{"type": "integer"},
			},
			Required: []string{"objects"},
		},
	}
	cfg := &ToolsetConfig{ClientID: "vision"}

	tl := newDirectMCPTool("detect_objects", "vision-detect_objects", mcpTool, nil, cfg, nil, testLogger())

	schema := tool.ResultSchemaOf(tl)
	if schema == nil {
		t.Fatal("ResultSchemaOf is nil; the server-declared outputSchema was dropped")
	}
	if schema.Type != "object" {
		t.Errorf("result schema Type = %q, want object", schema.Type)
	}
	for _, field := range []string{"objects", "object_count"} {
		if _, ok := schema.Properties[field]; ok {
			t.Errorf("declared property %q sits at the top level of the result schema; it belongs under %q",
				field, structuredContentKey)
		}
	}
	declared, ok := schema.Properties[structuredContentKey].(map[string]any)
	if !ok {
		t.Fatalf("result schema has no %q property (got %v)", structuredContentKey, schema.Properties)
	}
	nested, _ := declared["properties"].(map[string]any)
	for _, field := range []string{"objects", "object_count"} {
		if _, ok := nested[field]; !ok {
			t.Errorf("nested schema is missing declared property %q (got %v)", field, nested)
		}
	}
	if req, _ := declared["required"].([]string); len(req) != 1 || req[0] != "objects" {
		t.Errorf("nested required = %v, want [objects]", declared["required"])
	}

	if schema.Raw != nil {
		t.Errorf("result schema Raw = %v, want nil so the fields read as lifted", schema.Raw)
	}

	if tool.HasAuthoredSchemas(tl) {
		t.Error("a declared outputSchema promoted the tool onto the authored tier")
	}
}

func TestNewDirectMCPTool_AbsentOutputSchemaReadsAsUnknown(t *testing.T) {
	mcpTool := mcpgo.Tool{
		Name:        "legacy_tool",
		InputSchema: mcpgo.ToolInputSchema{Type: "object"},
	}
	cfg := &ToolsetConfig{ClientID: "legacy"}

	tl := newDirectMCPTool("legacy_tool", "legacy-legacy_tool", mcpTool, nil, cfg, nil, testLogger())

	if got := tool.ResultSchemaOf(tl); got != nil {
		t.Errorf("ResultSchemaOf = %v, want nil for a server that declared no outputSchema", got)
	}
}

func TestNewManifestMCPTool_SurfacesDeclaredOutputSchema(t *testing.T) {
	mt := ManifestTool{
		Name:        "detect_objects",
		Description: "Detect objects in an image",
		InputSchema: map[string]any{"type": "object"},
		OutputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"objects": map[string]any{"type": "array"}},
			"required":   []any{"objects"},
		},
	}
	cfg := &ToolsetConfig{ClientID: "vision"}

	tl := newManifestMCPTool("detect_objects", "vision-detect_objects", mt, nil, cfg, nil, testLogger())

	schema := tool.ResultSchemaOf(tl)
	if schema == nil {
		t.Fatal("ResultSchemaOf is nil; the manifest outputSchema was dropped")
	}
	if _, ok := schema.Properties["objects"]; ok {
		t.Errorf("declared property objects sits at the top level of the result schema; it belongs under %q",
			structuredContentKey)
	}
	declared, ok := schema.Properties[structuredContentKey].(map[string]any)
	if !ok {
		t.Fatalf("result schema has no %q property (got %v)", structuredContentKey, schema.Properties)
	}
	nested, _ := declared["properties"].(map[string]any)
	if _, ok := nested["objects"]; !ok {
		t.Errorf("nested schema is missing declared property objects (got %v)", nested)
	}
	if req, _ := declared["required"].([]any); len(req) != 1 || req[0] != "objects" {
		t.Errorf("nested required = %v, want [objects]", declared["required"])
	}
	if tool.HasAuthoredSchemas(tl) {
		t.Error("a manifest outputSchema promoted the tool onto the authored tier")
	}
}

func TestNewManifestMCPTool_AbsentOutputSchemaReadsAsUnknown(t *testing.T) {
	mt := ManifestTool{Name: "noOut", InputSchema: map[string]any{"type": "object"}}
	cfg := &ToolsetConfig{ClientID: "x"}

	tl := newManifestMCPTool("noOut", "x-noOut", mt, nil, cfg, nil, testLogger())

	if got := tool.ResultSchemaOf(tl); got != nil {
		t.Errorf("ResultSchemaOf = %v, want nil for a manifest entry with no outputSchema", got)
	}
}

func TestNewDirectMCPTool_FieldlessOutputSchemaReadsAsUnknown(t *testing.T) {
	cfg := &ToolsetConfig{ClientID: "vision"}
	cases := map[string]mcpgo.ToolOutputSchema{
		"array-typed":  {Type: "array"},
		"object-empty": {Type: "object"},
		"defs-only":    {Defs: map[string]any{"Box": map[string]any{"type": "object"}}},
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			mcpTool := mcpgo.Tool{Name: "t", InputSchema: mcpgo.ToolInputSchema{Type: "object"}, OutputSchema: out}
			tl := newDirectMCPTool("t", "vision-t", mcpTool, nil, cfg, nil, testLogger())
			if got := tool.ResultSchemaOf(tl); got != nil {
				t.Errorf("ResultSchemaOf = %v, want nil for a schema with no named fields", got)
			}
		})
	}
}

func TestMCPResultEnvelope_CarriesDefsWithRefs(t *testing.T) {
	mcpTool := mcpgo.Tool{
		Name:        "detect_objects",
		InputSchema: mcpgo.ToolInputSchema{Type: "object"},
		OutputSchema: mcpgo.ToolOutputSchema{
			Type:       "object",
			Properties: map[string]any{"box": map[string]any{"$ref": "#/$defs/Box"}},
			Defs:       map[string]any{"Box": map[string]any{"type": "object"}},
		},
	}
	cfg := &ToolsetConfig{ClientID: "vision"}

	tl := newDirectMCPTool("detect_objects", "vision-detect_objects", mcpTool, nil, cfg, nil, testLogger())

	schema := tool.ResultSchemaOf(tl)
	if schema == nil {
		t.Fatal("ResultSchemaOf is nil for a schema whose properties carry a $ref")
	}
	declared, ok := schema.Properties[structuredContentKey].(map[string]any)
	if !ok {
		t.Fatalf("result schema has no %q property (got %v)", structuredContentKey, schema.Properties)
	}
	if _, ok := declared["$defs"]; !ok {
		t.Errorf("nested schema dropped $defs, leaving its $ref dangling (got %v)", declared)
	}
}

func TestMCPResultEnvelope_DeclaresOnlyUnconditionalKeys(t *testing.T) {
	envelope := mcpResultEnvelope(map[string]any{"type": "object", "properties": map[string]any{"x": true}})
	if envelope == nil {
		t.Fatal("mcpResultEnvelope returned nil for a declared schema")
	}

	want := []string{"mcp_structured_content"}
	got := slices.Sorted(maps.Keys(envelope.Properties))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("envelope keys = %v, want exactly %v — if this change is intended, update "+
			"envelopeResultTool in internal/workflow/reference_validation_test.go and the "+
			"out(...) prefix in pkg/tools/builder_connector_discovery.go to match", got, want)
	}
	if structuredContentKey != "mcp_structured_content" {
		t.Errorf("structuredContentKey = %q, but internal/workflow and pkg/tools hardcode "+
			"%q; rename them together or the reference check silently stops matching",
			structuredContentKey, "mcp_structured_content")
	}

	for _, key := range []string{
		"saved_mcp_response_artifact_details",
		"appended_artifact_metadata",
		"response_skeleton",
		"extracted_fields",
		"mcp_tool_output",
		"message_to_llm",
	} {
		if _, ok := envelope.Properties[key]; ok {
			t.Errorf("envelope declares %q, which only some responses carry; a reference to it "+
				"would validate clean and then fail the node", key)
		}
	}

	if len(envelope.Required) != 0 {
		t.Errorf("envelope Required = %v, want empty", envelope.Required)
	}
}

func TestMCPResultEnvelope_NilForNoDeclaredSchema(t *testing.T) {
	if got := mcpResultEnvelope(nil); got != nil {
		t.Errorf("mcpResultEnvelope(nil) = %v, want nil", got)
	}
}
