// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type resultSchemaTool struct {
	schemaTool
	result *tool.JSONSchema
}

func (t *resultSchemaTool) ResultSchema() *tool.JSONSchema { return t.result }

func webRequestResultFields() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object", Properties: map[string]any{
		"response_status_code":   map[string]any{"type": "integer"},
		"original_content_type":  map[string]any{"type": "string"},
		"processed_content_type": map[string]any{"type": "string"},
		"filename":               map[string]any{"type": "string"},
		"size_bytes":             map[string]any{"type": "integer"},
	}}
}

func webRequestResultTool() *resultSchemaTool {
	return &resultSchemaTool{
		schemaTool: schemaTool{name: "web_request", authored: true, props: map[string]any{
			"url":                      map[string]any{"type": "string"},
			"output_artifact_filename": map[string]any{"type": "string"},
		}},
		result: webRequestResultFields(),
	}
}

func validateRefs(t *testing.T, nodes []*NodeConfig, tools *tool.Set) []referenceProblem {
	t.Helper()
	return validateRefsWith(t, nodes, nil, tools)
}

func reported(problems []referenceProblem) string {
	details := make([]string, 0, len(problems))
	for _, p := range problems {
		details = append(details, p.detail)
	}
	return strings.Join(details, "; ")
}

func toolSetWith(t *testing.T, tools ...tool.Tool) *tool.Set {
	t.Helper()
	ts := tool.NewSet()
	for _, tl := range tools {
		if err := ts.Register(tl); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	return ts
}

func fetchImageNode() *NodeConfig {
	return &NodeConfig{
		ID:       "fetch_image",
		Type:     "tool",
		ToolName: "web_request",
		Input:    map[string]any{"url": "https://example.com/x.jpg"},
	}
}

func TestValidateNodeInputReferences_ReportsFieldTheToolCannotProduce(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
	}{
		{
			name:  "whole-string reference",
			input: map[string]any{"image": "{{fetch_image.output.output_filename}}"},
		},
		{
			name: "reference embedded in an embed directive",
			input: map[string]any{
				"image": "«artifact_content:{{fetch_image.output.output_filename}} >>> format:datauri»",
			},
		},
		{
			name:  "an input parameter name reused as a result field",
			input: map[string]any{"image": "{{fetch_image.output.output_artifact_filename}}"},
		},
		{
			name: "nested inside a coalesce operator",
			input: map[string]any{"image": map[string]any{
				"coalesce": []any{"{{fetch_image.output.output_filename}}", "fallback.png"},
			}},
		},
		{
			name: "nested in a list inside a map",
			input: map[string]any{"images": []any{
				map[string]any{"src": "{{fetch_image.output.output_filename}}"},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []*NodeConfig{
				fetchImageNode(),
				{ID: "draw_boxes", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch_image"}, Input: tc.input},
			}
			problems := validateRefs(t, nodes, toolSetWith(t, webRequestResultTool()))
			if len(problems) == 0 {
				t.Fatal("a reference to a field web_request cannot produce went unreported")
			}
			msg := reported(problems)
			if !strings.Contains(msg, `"draw_boxes"`) {
				t.Errorf("report %q does not name the node holding the reference", msg)
			}
			if !strings.Contains(msg, "filename") {
				t.Errorf("report %q does not name the field that does exist", msg)
			}
			if !strings.Contains(msg, "separate namespace") {
				t.Errorf("report %q omits the input-vs-result namespace warning", msg)
			}
		})
	}
}

func TestValidateNodeInputReferences_ChecksTheReroutingEscalationsAnswer(t *testing.T) {
	nodes := func(mode string) []*NodeConfig {
		escalate := fetchImageNode()
		escalate.DependsOn = []string{"triage"}
		return []*NodeConfig{
			{ID: "triage", Type: "decision", RouteOn: "department", Escalate: "fetch_image", EscalationMode: mode, MinConfidence: 0.8,
				Routes: map[string]string{"billing": "billing"}},
			escalate,
			{ID: "billing", Type: "tool", ToolName: "web_request", DependsOn: []string{"triage"}, Input: map[string]any{"url": "https://example.com"}},
		}
	}
	t.Run("reroute reads the escalate target's route_on field", func(t *testing.T) {
		problems := validateRefs(t, nodes(""), toolSetWith(t, webRequestResultTool()))
		if msg := reported(problems); !strings.Contains(msg, `node "triage" escalation answer`) {
			t.Fatalf("report %q does not flag the escalation answer web_request cannot produce", msg)
		}
	})
	t.Run("handoff reads nothing from the escalate target", func(t *testing.T) {
		if problems := validateRefs(t, nodes(EscalationModeHandoff), toolSetWith(t, webRequestResultTool())); len(problems) != 0 {
			t.Fatalf("handoff reported %q", reported(problems))
		}
	})
}

func TestValidateNodeInputReferences_AcceptsWhatTheToolProduces(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
	}{
		{
			name:  "the correct field",
			input: map[string]any{"image": "{{fetch_image.output.filename}}"},
		},
		{
			name:  "the framework-injected status and message",
			input: map[string]any{"s": "{{fetch_image.output.status}}", "m": "{{fetch_image.output.message}}"},
		},
		{
			name:  "the whole output map",
			input: map[string]any{"all": "{{fetch_image.output}}"},
		},
		{
			name:  "a path through a declared field",
			input: map[string]any{"deep": "{{fetch_image.output.filename.something}}"},
		},
		{
			name:  "text that merely contains braces",
			input: map[string]any{"tpl": "a {{ literal }} brace"},
		},
		{
			name:  "a reference to a declared node that may not run",
			input: map[string]any{"x": "{{fetch_image.output.filename}}"},
		},
		{
			name: "an unknown node inside a coalesce",
			input: map[string]any{"x": map[string]any{"coalesce": []any{
				"{{no_such_node.output.whatever}}", "fallback",
			}}},
		},
		{
			name:  "workflow input and map-item references",
			input: map[string]any{"a": "{{workflow.input.url}}", "b": "{{_map_item.id}}", "c": "{{item.name}}"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []*NodeConfig{
				fetchImageNode(),
				{ID: "draw_boxes", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch_image"}, Input: tc.input},
			}
			if problems := validateRefs(t, nodes, toolSetWith(t, webRequestResultTool())); len(problems) > 0 {
				t.Errorf("valid reference reported: %v", problems)
			}
		})
	}
}

func TestValidateNodeInputReferences_FailsOpenWhenFieldsAreNotKnowable(t *testing.T) {
	badRef := map[string]any{"image": "{{fetch_image.output.output_filename}}"}

	tests := []struct {
		name     string
		upstream *NodeConfig
		tools    []tool.Tool
		extra    []*NodeConfig
	}{
		{
			name:     "a built-in that declares no result schema",
			upstream: fetchImageNode(),
			tools:    []tool.Tool{webRequestLikeTool()},
		},
		{
			name:     "a tool that is not registered",
			upstream: fetchImageNode(),
			tools:    nil,
		},
		{
			name: "a node overriding its own output schema",
			upstream: &NodeConfig{
				ID: "fetch_image", Type: "tool", ToolName: "web_request",
				OutputSchemaOverride: map[string]any{"type": "object"},
			},
			tools: []tool.Tool{webRequestResultTool()},
		},
		{
			name:     "an agent node, whose outputs are not declared here",
			upstream: &NodeConfig{ID: "fetch_image", Type: "agent", AgentName: "fetcher"},
			tools:    []tool.Tool{webRequestResultTool()},
		},
		{
			name: "a map node, whose output is the collected iteration results",
			upstream: &NodeConfig{ID: "fetch_image", Type: "map", TemplateNode: "body",
				ItemsExpression: []any{"a", "b"}},
			tools: []tool.Tool{webRequestResultTool()},
			extra: []*NodeConfig{{ID: "body", Type: "tool", ToolName: "web_request",
				DependsOn: []string{"fetch_image"}, Input: map[string]any{"url": "https://x"}}},
		},
		{
			name:     "a loop node, whose output comes from its body",
			upstream: &NodeConfig{ID: "fetch_image", Type: "loop", BodyNode: "body", Condition: "false"},
			tools:    []tool.Tool{webRequestResultTool()},
			extra: []*NodeConfig{{ID: "body", Type: "tool", ToolName: "web_request",
				DependsOn: []string{"fetch_image"}, Input: map[string]any{"url": "https://x"}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nodes := append([]*NodeConfig{
				tc.upstream,
				{ID: "draw_boxes", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch_image"}, Input: badRef},
			}, tc.extra...)
			if problems := validateRefs(t, nodes, toolSetWith(t, tc.tools...)); len(problems) > 0 {
				t.Errorf("validation reported a reference it cannot actually judge: %v", problems)
			}
		})
	}
}

func TestValidateNodeInputReferences_DiscoveredSchemaReportsUnverified(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "detect", Type: "tool", ToolName: "detect_objects", Input: map[string]any{"image": "x.jpg"}},
		{ID: "draw", Type: "tool", ToolName: "detect_objects", DependsOn: []string{"detect"},
			Input: map[string]any{"boxes": "{{detect.output.extracted_fields}}"}},
	}
	problems := validateRefs(t, nodes, toolSetWith(t, discoveredResultTool()))

	if len(problems) != 1 {
		t.Fatalf("want exactly one report, got %v", problems)
	}
	if problems[0].kind != problemKindUnverifiedField {
		t.Errorf("kind = %q, want %q — a discovered schema must not accuse the tool of not producing the field",
			problems[0].kind, problemKindUnverifiedField)
	}
	if !strings.Contains(problems[0].detail, "may be incomplete") {
		t.Errorf("detail must tell the author the schema may be incomplete, got: %s", problems[0].detail)
	}
	if problems[0].upstreamNodeID != "detect" {
		t.Errorf("upstreamNodeID = %q, want %q", problems[0].upstreamNodeID, "detect")
	}
}

func discoveredResultTool() *resultSchemaTool {
	return &resultSchemaTool{
		schemaTool: schemaTool{name: "detect_objects", props: map[string]any{"image": map[string]any{"type": "string"}}},
		result:     &tool.JSONSchema{Type: "object", Properties: map[string]any{"detections": map[string]any{"type": "array"}}},
	}
}

func TestValidateNodeInputReferences_DiscoveredSchemaAcceptsDeclaredField(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "detect", Type: "tool", ToolName: "detect_objects", Input: map[string]any{"image": "x.jpg"}},
		{ID: "draw", Type: "tool", ToolName: "detect_objects", DependsOn: []string{"detect"},
			Input: map[string]any{"boxes": "{{detect.output.detections}}"}},
	}
	if problems := validateRefs(t, nodes, toolSetWith(t, discoveredResultTool())); len(problems) > 0 {
		t.Errorf("a reference the discovered schema declares was reported: %v", problems)
	}
}

func envelopeResultTool() *resultSchemaTool {
	return &resultSchemaTool{
		schemaTool: schemaTool{name: "detect_objects", props: map[string]any{"image": map[string]any{"type": "string"}}},
		result: &tool.JSONSchema{Type: "object", Properties: map[string]any{
			"mcp_structured_content": map[string]any{
				"type":       "object",
				"properties": map[string]any{"detections": map[string]any{"type": "array"}},
			},
		}},
	}
}

func envelopeNodes(ref string) []*NodeConfig {
	return []*NodeConfig{
		{ID: "detect", Type: "tool", ToolName: "detect_objects", Input: map[string]any{"image": "x.jpg"}},
		{ID: "draw", Type: "tool", ToolName: "detect_objects", DependsOn: []string{"detect"},
			Input: map[string]any{"boxes": ref}},
	}
}

func TestValidateNodeInputReferences_NestedDeclaredFieldAccepted(t *testing.T) {
	nodes := envelopeNodes("{{detect.output.mcp_structured_content.detections}}")
	if problems := validateRefs(t, nodes, toolSetWith(t, envelopeResultTool())); len(problems) > 0 {
		t.Errorf("the addressing an MCP tool's typed result requires was reported: %v", problems)
	}
}

func TestValidateNodeInputReferences_NestedUndeclaredFieldReportsUnverified(t *testing.T) {
	nodes := envelopeNodes("{{detect.output.mcp_structured_content.objects}}")
	problems := validateRefs(t, nodes, toolSetWith(t, envelopeResultTool()))

	if len(problems) != 1 {
		t.Fatalf("want exactly one report, got %v", problems)
	}
	if problems[0].kind != problemKindUnverifiedField {
		t.Errorf("kind = %q, want %q — a discovered schema must not accuse the tool of not producing the field",
			problems[0].kind, problemKindUnverifiedField)
	}
	if !strings.Contains(problems[0].detail, "detections") {
		t.Errorf("detail must name the fields the nested schema does declare, got: %s", problems[0].detail)
	}
	if problems[0].upstreamNodeID != "detect" {
		t.Errorf("upstreamNodeID = %q, want %q", problems[0].upstreamNodeID, "detect")
	}
}

func TestValidateNodeInputReferences_ConditionalEnvelopeKeyReportsUnverified(t *testing.T) {
	for _, field := range []string{
		"extracted_fields",
		"response_skeleton",
		"saved_mcp_response_artifact_details",
		"appended_artifact_metadata",
		"mcp_tool_output",
		"message_to_llm",
	} {
		t.Run(field, func(t *testing.T) {
			nodes := envelopeNodes("{{detect.output." + field + "}}")
			problems := validateRefs(t, nodes, toolSetWith(t, envelopeResultTool()))

			if len(problems) != 1 {
				t.Fatalf("want exactly one report for a key only some responses carry, got %v", problems)
			}
			if problems[0].kind != problemKindUnverifiedField {
				t.Errorf("kind = %q, want %q — it may still arrive, so this must not be the fatal-tier claim",
					problems[0].kind, problemKindUnverifiedField)
			}
		})
	}
}

func TestValidateNodeInputReferences_ConditionalFrameworkKeyReportsUnverified(t *testing.T) {
	for _, key := range []string{"message", "created_artifact", "created_artifact_name", "created_artifact_version"} {
		t.Run(key, func(t *testing.T) {
			nodes := envelopeNodes("{{detect.output." + key + "}}")
			problems := validateRefs(t, nodes, toolSetWith(t, envelopeResultTool()))

			if len(problems) != 1 {
				t.Fatalf("want exactly one report for a framework key an MCP tool sets only sometimes, got %v", problems)
			}
			if problems[0].kind != problemKindUnverifiedField {
				t.Errorf("kind = %q, want %q", problems[0].kind, problemKindUnverifiedField)
			}
			if strings.Contains(problems[0].detail, key+",") || strings.HasSuffix(problems[0].detail, key) {
				t.Errorf("the declared-fields list still advertises `%s`: %s", key, problems[0].detail)
			}
		})
	}
}

func TestValidateNodeInputReferences_StatusStaysProducibleForDiscoveredSchema(t *testing.T) {
	nodes := envelopeNodes("{{detect.output.status}}")
	if problems := validateRefs(t, nodes, toolSetWith(t, envelopeResultTool())); len(problems) > 0 {
		t.Errorf("status is set on every MCP response but was reported: %v", problems)
	}
}

func TestValidateNodeInputReferences_MessageStaysProducibleForAuthoredSchema(t *testing.T) {
	nodes := []*NodeConfig{
		fetchImageNode(),
		{ID: "next", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch_image"},
			Input: map[string]any{"url": "{{fetch_image.output.message}}"}},
	}
	if problems := validateRefs(t, nodes, toolSetWith(t, webRequestResultTool())); len(problems) > 0 {
		t.Errorf("an authored tool's `message` was reported: %v", problems)
	}
}

func TestValidateNodeInputReferences_NestedOpenValueNotWalked(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "detect", Type: "tool", ToolName: "detect_objects", Input: map[string]any{"image": "x.jpg"}},
		{ID: "draw", Type: "tool", ToolName: "detect_objects", DependsOn: []string{"detect"},
			Input: map[string]any{"boxes": "{{detect.output.detections.anything}}"}},
	}
	if problems := validateRefs(t, nodes, toolSetWith(t, discoveredResultTool())); len(problems) > 0 {
		t.Errorf("descended into a property that declares no properties of its own: %v", problems)
	}
}

func TestValidateNodeInputReferences_AuthoredSchemaNotWalked(t *testing.T) {
	authored := &resultSchemaTool{
		schemaTool: schemaTool{name: "nested_authored", authored: true, props: map[string]any{"in": map[string]any{"type": "string"}}},
		result: &tool.JSONSchema{Type: "object", Properties: map[string]any{
			"payload": map[string]any{
				"type":       "object",
				"properties": map[string]any{"known": map[string]any{"type": "string"}},
			},
		}},
	}
	nodes := []*NodeConfig{
		{ID: "first", Type: "tool", ToolName: "nested_authored", Input: map[string]any{"in": "x"}},
		{ID: "second", Type: "tool", ToolName: "nested_authored", DependsOn: []string{"first"},
			Input: map[string]any{"in": "{{first.output.payload.unknown}}"}},
	}
	if problems := validateRefs(t, nodes, toolSetWith(t, authored)); len(problems) > 0 {
		t.Errorf("walked into an authored schema's nested value: %v", problems)
	}
}

func TestSchemaDerivedNodeIDs_ExcludesDiscoveredSchemas(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"detect": {ID: "detect", Type: "tool", ToolName: "detect_objects"},
		"fetch":  fetchImageNode(),
	}
	tools := toolSetWith(t, discoveredResultTool(), webRequestResultTool())

	named := schemaDerivedNodeIDs(nodes, tools)
	if named["detect"] {
		t.Error("a discovered-schema node was marked nameable; its run-time keys can be user data")
	}
	if !named["fetch"] {
		t.Error("an authored-schema node stopped being nameable")
	}
}

func TestValidateNodeInputReferences_ChecksMapItemsExpression(t *testing.T) {
	nodes := []*NodeConfig{
		fetchImageNode(),
		{ID: "per_image", Type: "map", DependsOn: []string{"fetch_image"},
			ItemsExpression: "{{fetch_image.output.output_filename}}", TemplateNode: "body"},
		{ID: "body", Type: "tool", ToolName: "web_request", Input: map[string]any{"url": "{{_map_item}}"}},
	}
	problems := validateRefs(t, nodes, toolSetWith(t, webRequestResultTool()))
	if len(problems) == 0 {
		t.Fatal("a bad reference in a map items expression went unreported")
	}
	if msg := reported(problems); !strings.Contains(msg, `"per_image"`) {
		t.Errorf("report %q does not name the map node", msg)
	}
}

func TestValidateNodeInputReferences_ReportsReferenceMissingTheOutputSegment(t *testing.T) {
	nodes := []*NodeConfig{
		fetchImageNode(),
		{ID: "draw_boxes", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch_image"},
			Input: map[string]any{"image": "{{fetch_image.filename}}"}},
	}
	problems := validateRefs(t, nodes, toolSetWith(t, webRequestResultTool()))
	if len(problems) == 0 {
		t.Fatal("a reference skipping the .output segment went unreported")
	}
	if msg := reported(problems); !strings.Contains(msg, "fetch_image.output.<field>") {
		t.Errorf("report %q does not show the correct addressing form", msg)
	}
}

func TestWorkflowInit_WarnsOnBadToolFieldReferenceButStillStarts(t *testing.T) {
	imageObjectDetectionNodes := func(image string) []*NodeConfig {
		return []*NodeConfig{
			fetchImageNode(),
			{ID: "detect_and_annotate", Type: "tool", ToolName: "web_request",
				DependsOn: []string{"fetch_image"}, Input: map[string]any{"image": image}},
		}
	}

	initWith := func(t *testing.T, nodes []*NodeConfig) (string, error) {
		t.Helper()
		var logged bytes.Buffer
		w := NewWorkflowInstance(&WorkflowConfig{Name: "ImageObjectDetection", Namespace: "test-ns", Nodes: nodes})
		w.tools = toolSetWith(t, webRequestResultTool())
		svc := &stubServices{logger: slog.New(slog.NewTextHandler(&logged, nil))}
		err := w.Init(context.Background(), nil, svc)
		return logged.String(), err
	}

	logged, err := initWith(t, imageObjectDetectionNodes("{{fetch_image.output.output_filename}}"))
	if err != nil {
		t.Fatalf("Init refused a workflow over a bad field reference: %v", err)
	}
	for _, want := range []string{"detect_and_annotate", "output_filename", "filename", "web_request"} {
		if !strings.Contains(logged, want) {
			t.Errorf("Init log %q does not mention %q", logged, want)
		}
	}
	if !strings.Contains(logged, "level=WARN") {
		t.Errorf("the problem was not logged at WARN: %q", logged)
	}

	logged, err = initWith(t, imageObjectDetectionNodes("{{fetch_image.output.filename}}"))
	if err != nil {
		t.Fatalf("Init failed on the corrected reference: %v", err)
	}
	if strings.Contains(logged, "does not declare") {
		t.Errorf("a producible reference was warned about: %q", logged)
	}
}

func TestWorkflowInit_WarnsOnUnverifiedNestedReferenceButStillStarts(t *testing.T) {
	initWith := func(t *testing.T, ref string) (string, error) {
		t.Helper()
		var logged bytes.Buffer
		w := NewWorkflowInstance(&WorkflowConfig{Name: "Detect", Namespace: "test-ns", Nodes: envelopeNodes(ref)})
		w.tools = toolSetWith(t, envelopeResultTool())
		svc := &stubServices{logger: slog.New(slog.NewTextHandler(&logged, nil))}
		err := w.Init(context.Background(), nil, svc)
		return logged.String(), err
	}

	logged, err := initWith(t, "{{detect.output.mcp_structured_content.boxes}}")
	if err != nil {
		t.Fatalf("Init refused a workflow over an unverified field reference: %v", err)
	}
	if !strings.Contains(logged, "level=WARN") {
		t.Errorf("the problem was not logged at WARN: %q", logged)
	}
	if strings.Contains(logged, "level=ERROR") {
		t.Errorf("an unverified reference was raised to ERROR; a discovered schema cannot support that claim: %q", logged)
	}
	for _, want := range []string{problemKindUnverifiedField, "could not be verified", "detections", "boxes"} {
		if !strings.Contains(logged, want) {
			t.Errorf("Init log does not mention %q: %q", want, logged)
		}
	}

	logged, err = initWith(t, "{{detect.output.mcp_structured_content.detections}}")
	if err != nil {
		t.Fatalf("Init failed on the corrected reference: %v", err)
	}
	if strings.Contains(logged, "could not be verified") {
		t.Errorf("a reference the nested schema declares was warned about: %q", logged)
	}
}

func TestValidateNodeInputReferences_ReportsEveryProblem(t *testing.T) {
	nodes := []*NodeConfig{
		fetchImageNode(),
		{ID: "alpha", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch_image"},
			Input: map[string]any{"image": "{{fetch_image.output.output_filename}}"}},
		{ID: "beta", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch_image"},
			Input: map[string]any{"image": "{{fetch_image.output.mime_type}}"}},
	}
	problems := validateRefs(t, nodes, toolSetWith(t, webRequestResultTool()))
	if len(problems) == 0 {
		t.Fatal("bad references went unreported")
	}
	msg := reported(problems)
	for _, want := range []string{"alpha", "beta", "output_filename", "mime_type"} {
		if !strings.Contains(msg, want) {
			t.Errorf("report %q does not mention %q", msg, want)
		}
	}
	if strings.Index(msg, "alpha") > strings.Index(msg, "beta") {
		t.Errorf("problems are not reported in a deterministic order: %q", msg)
	}
}

func validateRefsWith(t *testing.T, nodes []*NodeConfig, outputMapping map[string]any, tools *tool.Set) []referenceProblem {
	t.Helper()
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	return unproducibleNodeReferences(dag.Nodes, outputMapping, tools)
}

func fetchNode() *NodeConfig {
	return &NodeConfig{
		ID:       "fetch",
		Type:     "tool",
		ToolName: "web_request",
		Input:    map[string]any{"url": "{{workflow.input.url}}"},
	}
}

func TestOutputMapping_UnproducibleReferenceIsReported(t *testing.T) {
	nodes := []*NodeConfig{fetchNode()}
	tools := toolSetWith(t, webRequestResultTool())

	problems := validateRefsWith(t, nodes, map[string]any{
		"image": "{{fetch.output.output_filename}}",
	}, tools)
	if len(problems) == 0 {
		t.Fatal("output_mapping referencing a field the tool cannot produce went unreported")
	}
	msg := reported(problems)
	for _, want := range []string{"output_mapping", "output_filename", "filename"} {
		if !strings.Contains(msg, want) {
			t.Errorf("report %q does not mention %q", msg, want)
		}
	}

	if clean := validateRefsWith(t, nodes, map[string]any{
		"image": "{{fetch.output.filename}}",
	}, tools); len(clean) > 0 {
		t.Errorf("a producible output_mapping reference was reported: %v", clean)
	}
}

func TestBareNodeReference_IsNotReported(t *testing.T) {
	tools := toolSetWith(t, webRequestResultTool())
	nodes := []*NodeConfig{
		fetchNode(),
		{
			ID:        "use",
			Type:      "tool",
			ToolName:  "web_request",
			DependsOn: []string{"fetch"},
			Input:     map[string]any{"url": "{{fetch}}"},
		},
	}
	if problems := validateRefsWith(t, nodes, nil, tools); len(problems) > 0 {
		t.Errorf("a bare {{node}} reference was reported: %v", problems)
	}
}

func TestProducibleOutputFields_DoesNotRepeatFrameworkKeys(t *testing.T) {
	declaresStatus := &resultSchemaTool{
		schemaTool: schemaTool{name: "load_artifact", authored: true, props: map[string]any{
			"filename": map[string]any{"type": "string"},
		}},
		result: &tool.JSONSchema{Type: "object", Properties: map[string]any{
			"filename": map[string]any{"type": "string"},
			"status":   map[string]any{"type": "string"},
			"message":  map[string]any{"type": "string"},
		}},
	}
	tools := toolSetWith(t, declaresStatus)
	node := &NodeConfig{ID: "load", Type: "tool", ToolName: "load_artifact"}

	fields, _ := producibleOutputFields(node, tools)
	seen := map[string]int{}
	for _, f := range fields {
		seen[f]++
	}
	for _, key := range frameworkOutputFields {
		if seen[key] != 1 {
			t.Errorf("field %q appears %d times in %v, want exactly 1", key, seen[key], fields)
		}
	}
}

func TestWorkflowInit_MarksOnlySchemaDerivedNodesAsNameable(t *testing.T) {
	nodes := []*NodeConfig{
		fetchImageNode(),
		{ID: "summarise", Type: "agent", AgentName: "writer", DependsOn: []string{"fetch_image"}},
	}
	w := NewWorkflowInstance(&WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: nodes})
	w.tools = toolSetWith(t, webRequestResultTool())
	if err := w.Init(context.Background(), nil, &stubServices{}); err != nil {
		t.Fatalf("init: %v", err)
	}

	if !w.namedOutputNodes["fetch_image"] {
		t.Error("a tool node with an authored result schema should be nameable")
	}
	if w.namedOutputNodes["summarise"] {
		t.Error("an agent node's output keys are returned data and must not be nameable")
	}
}

func TestFrameworkOutputFields_MatchWhatClassifyToolResultAdds(t *testing.T) {
	_, output, err := classifyToolResult("some_tool",
		tool.OK("a message"), nil, []savedArtifact{{name: "report.csv", version: 1}})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}

	added := slices.Sorted(maps.Keys(output))
	want := slices.Clone(frameworkOutputFields)
	slices.Sort(want)
	if !slices.Equal(added, want) {
		t.Errorf("classifyToolResult adds %v, but frameworkOutputFields declares %v — "+
			"a key in one and not the other either hides a valid reference or invents one",
			added, want)
	}
}

func TestFinalizeSuccess_UnresolvedOutputMappingDegradesInsteadOfVoidingTheRun(t *testing.T) {
	nodes := []*NodeConfig{fetchImageNode()}
	cfg := &WorkflowConfig{
		Name: "wf", Namespace: "ns", Nodes: nodes,
		OutputMapping: map[string]any{
			"report": "Found {{fetch_image.output.result_rows}} rows",
			"real":   "{{fetch_image.output.filename}}",
		},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-degrade"))
	defer e.execCtx.Cancel()
	e.instance.namedOutputNodes = schemaDerived{"fetch_image": true}
	e.state.NodeOutputs = map[string]map[string]any{
		"fetch_image": {"output": map[string]any{"filename": "x.jpg", "status": "success"}},
	}

	e.finalizeSuccess(time.Second)

	if e.state.ErrorState != nil {
		t.Fatalf("a completed run was voided by an output_mapping miss: %v", e.state.ErrorState.ErrorMessage)
	}
	if e.state.PendingOutcome != "success" {
		t.Fatalf("PendingOutcome = %q, want success", e.state.PendingOutcome)
	}

	outputMap, ok := e.state.Metadata[finalKeyOutputMap].(map[string]any)
	if !ok {
		t.Fatalf("no final output map recorded, got %T", e.state.Metadata[finalKeyOutputMap])
	}
	if got := outputMap["report"]; got != "Found {{fetch_image.output.result_rows}} rows" {
		t.Errorf("report = %#v, want the reference left verbatim", got)
	}
	if got := outputMap["real"]; got != "x.jpg" {
		t.Errorf("real = %#v, want the resolved filename", got)
	}
}

func TestUnproducibleNodeReferences_CoversConditionsAndInstructions(t *testing.T) {
	const stale = "{{fetch_image.output.output_filename}}"

	tests := []struct {
		name         string
		nodes        []*NodeConfig
		wantReferrer string
	}{
		{
			name: "when",
			nodes: []*NodeConfig{{ID: "gate", Type: "tool", ToolName: "web_request",
				DependsOn: []string{"fetch_image"}, When: stale + " != ''"}},
			wantReferrer: `node "gate" when`,
		},
		{
			name: "switch case condition",
			nodes: []*NodeConfig{
				{ID: "router", Type: "switch", DependsOn: []string{"fetch_image"},
					Cases: []SwitchCase{{Condition: stale + " == 'x'", Node: "body"}}},
				{ID: "body", Type: "tool", ToolName: "web_request",
					DependsOn: []string{"router"}, Input: map[string]any{"url": "https://x"}},
			},
			wantReferrer: `node "router" case 0 condition`,
		},
		{
			name: "loop condition",
			nodes: []*NodeConfig{
				{ID: "spin", Type: "loop", DependsOn: []string{"fetch_image"},
					BodyNode: "body", Condition: stale + " != ''"},
				{ID: "body", Type: "tool", ToolName: "web_request",
					DependsOn: []string{"spin"}, Input: map[string]any{"url": "https://x"}},
			},
			wantReferrer: `node "spin" condition`,
		},
		{
			name: "agent instruction",
			nodes: []*NodeConfig{{ID: "writer", Type: "agent", AgentName: "scribe",
				DependsOn: []string{"fetch_image"}, Instruction: "Summarise " + stale}},
			wantReferrer: `node "writer" instruction`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problems := validateRefs(t, append([]*NodeConfig{fetchImageNode()}, tc.nodes...),
				toolSetWith(t, webRequestResultTool()))
			if len(problems) == 0 {
				t.Fatalf("a stale reference in %s went unreported", tc.name)
			}
			msg := reported(problems)
			if !strings.Contains(msg, tc.wantReferrer) {
				t.Errorf("report %q does not name the site holding the reference (%q)", msg, tc.wantReferrer)
			}
			if !strings.Contains(msg, "output_filename") || !strings.Contains(msg, "filename") {
				t.Errorf("report %q does not name the bad field and the real one", msg)
			}
		})
	}
}

func TestUnproducibleNodeReferences_ReportsAMisspelledNodeID(t *testing.T) {
	consumer := func(input map[string]any) []*NodeConfig {
		return []*NodeConfig{
			fetchImageNode(),
			{ID: "use", Type: "tool", ToolName: "web_request",
				DependsOn: []string{"fetch_image"}, Input: input},
		}
	}
	tools := toolSetWith(t, webRequestResultTool())

	problems := validateRefs(t, consumer(map[string]any{
		"filename": "report_{{fetchh.output.filename}}.pdf",
	}), tools)
	if len(problems) == 0 {
		t.Fatal("a misspelled node id went unreported")
	}
	msg := reported(problems)
	for _, want := range []string{"fetchh", "no node", "fetch_image"} {
		if !strings.Contains(msg, want) {
			t.Errorf("report %q does not mention %q — it should name the typo and the nodes that exist", msg, want)
		}
	}
	if problems[0].kind != problemKindUnknownNode {
		t.Errorf("kind = %q, want %q", problems[0].kind, problemKindUnknownNode)
	}

	quiet := []struct {
		name  string
		input map[string]any
	}{
		{"braces meant for something else", map[string]any{"tpl": "Hello {{name}}, welcome"}},
		{"a Liquid template passed to a tool", map[string]any{"tpl": "{{ user.first_name | capitalize }}"}},
		{"a second segment that is not output", map[string]any{"x": "{{item.name}}"}},
		{"the synthetic workflow_input node", map[string]any{"x": "{{workflow_input.output.greeting}}"}},
		{"rescued by coalesce", map[string]any{"x": map[string]any{
			"coalesce": []any{"{{maybe_absent.output.f}}", "fallback"}}}},
		{"rescued by concat", map[string]any{"x": map[string]any{
			"concat": []any{"a", "{{maybe_absent.output.f}}"}}}},
	}
	for _, tc := range quiet {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateRefs(t, consumer(tc.input), tools); len(got) > 0 {
				t.Errorf("reported %v", reported(got))
			}
		})
	}
}
