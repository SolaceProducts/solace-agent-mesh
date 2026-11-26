// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const defaultToolResultPreviewLengthChars = 500

func realisticDiscoveredTool(name string) map[string]any {
	return map[string]any{
		"name": name,
		"description": "Detects objects in an image and returns their bounding boxes, " +
			"confidence scores and a human-readable summary of what was found. " +
			"Accepts a URL or an artifact reference.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image_url":       map[string]any{"type": "string"},
				"artifact":        map[string]any{"type": "string"},
				"threshold":       map[string]any{"type": "number"},
				"max_detections":  map[string]any{"type": "integer"},
				"labels":          map[string]any{"type": "array"},
				"include_crops":   map[string]any{"type": "boolean"},
				"output_filename": map[string]any{"type": "string"},
			},
			"required": []any{"image_url"},
		},
		"outputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objects":      map[string]any{"type": "array"},
				"object_count": map[string]any{"type": "integer"},
				"summary":      map[string]any{"type": "string"},
			},
			"required": []any{"objects"},
		},
	}
}

func TestDiscoveredToolsPreview_NamesOutputFields(t *testing.T) {
	preview := discoveredToolsPreview([]map[string]any{realisticDiscoveredTool("detect_objects")})

	for _, want := range []string{"detect_objects", "image_url", "objects", "object_count", "summary"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview omits %q, which the agent needs to write the node: %s", want, preview)
		}
	}
	if !strings.Contains(preview, "mcp_structured_content") {
		t.Errorf("preview must say output fields are addressed under mcp_structured_content: %s", preview)
	}
}

func TestDiscoveredToolsPreview_FitsBudgetWhereMarshalIndentDidNot(t *testing.T) {
	tools := []map[string]any{
		realisticDiscoveredTool("detect_objects"),
		realisticDiscoveredTool("classify_image"),
		realisticDiscoveredTool("crop_region"),
	}

	full, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(full) <= defaultToolResultPreviewLengthChars {
		t.Fatalf("fixture is too small to exercise the truncation this guards (%d bytes) — make it realistic", len(full))
	}
	idx := strings.Index(string(full), "outputSchema")
	if idx < 0 {
		t.Fatalf("fixture has no outputSchema, so it cannot reproduce the bug this guards")
	}
	if idx < defaultToolResultPreviewLengthChars {
		t.Errorf("fixture no longer reproduces the bug: outputSchema at offset %d survives a %d-char cut",
			idx, defaultToolResultPreviewLengthChars)
	}

	preview := discoveredToolsPreview(tools)
	if len(preview) > defaultToolResultPreviewLengthChars {
		t.Errorf("preview is %d chars, over the %d-char budget the framework cuts at: %s",
			len(preview), defaultToolResultPreviewLengthChars, preview)
	}
	for _, name := range []string{"detect_objects", "classify_image", "crop_region"} {
		if !strings.Contains(preview, name) {
			t.Errorf("preview dropped tool %q: %s", name, preview)
		}
	}
}

func TestDiscoveredToolsPreview_DistinguishesTruncatedAwayFromUndeclaredSchema(t *testing.T) {
	withoutSchema := map[string]any{
		"name":        "legacy_tool",
		"description": "From before structured output existed",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
	}

	preview := discoveredToolsPreview([]map[string]any{withoutSchema})
	if !strings.Contains(preview, "none declared") {
		t.Errorf("preview must say so explicitly when a server declared no outputSchema: %s", preview)
	}
}

func TestDiscoveredToolsPreview_DegradesInTiers(t *testing.T) {
	mk := func(n int) []map[string]any {
		out := make([]map[string]any, 0, n)
		for i := range n {
			out = append(out, realisticDiscoveredTool("tool_"+string(rune('a'+i))))
		}
		return out
	}

	for _, n := range []int{1, 3, 4, 8, 20, 50} {
		preview := discoveredToolsPreview(mk(n))
		if n <= 20 && len(preview) > defaultToolResultPreviewLengthChars {
			t.Errorf("%d tools: preview is %d chars, over the %d-char budget: %s",
				n, len(preview), defaultToolResultPreviewLengthChars, preview)
		}
		for i := range n {
			if name := "tool_" + string(rune('a'+i)); !strings.Contains(preview, name) {
				t.Errorf("%d tools: preview dropped %q entirely: %s", n, name, preview)
			}
		}
		if !strings.Contains(preview, "mcp_structured_content") && !strings.Contains(preview, "load_artifact") {
			t.Errorf("%d tools: preview elided output fields without saying where to find them: %s", n, preview)
		}
	}

	if got := discoveredToolsPreview(mk(3)); !strings.Contains(got, "in(") {
		t.Errorf("3 tools should still carry input fields: %s", got)
	}
	if got := discoveredToolsPreview(mk(4)); strings.Contains(got, "in(") || !strings.Contains(got, "mcp_structured_content") {
		t.Errorf("4 tools should shed inputs and keep output fields: %s", got)
	}
	if got := discoveredToolsPreview(mk(8)); strings.Contains(got, "mcp_structured_content") {
		t.Errorf("8 tools should shed output fields too: %s", got)
	}
}

func TestDiscoveredToolsPreview_HardCapped(t *testing.T) {
	mk := func(n int, nameLen int) []map[string]any {
		out := make([]map[string]any, 0, n)
		for i := range n {
			out = append(out, realisticDiscoveredTool(strings.Repeat("x", nameLen)+strconv.Itoa(i)))
		}
		return out
	}

	const wantCapChars = 4000
	if previewHardCapChars != wantCapChars {
		t.Errorf("previewHardCapChars = %d, want %d — the cap is a prompt-size budget, so "+
			"changing it is a deliberate act that has to update this test",
			previewHardCapChars, wantCapChars)
	}

	for _, tc := range []struct{ tools, nameLen int }{{5000, 8}, {200, 200}, {50000, 4}} {
		preview := discoveredToolsPreview(mk(tc.tools, tc.nameLen))
		if len(preview) > wantCapChars+200 {
			t.Errorf("%d tools with %d-char names: preview is %d chars, over the %d-char hard cap",
				tc.tools, tc.nameLen, len(preview), wantCapChars)
		}
		if !strings.Contains(preview, "load_artifact") {
			t.Errorf("%d tools: capped preview does not point at the artifact: %q", tc.tools, preview)
		}
	}

	if got := len(discoveredToolsPreview(mk(34, 12))); got > wantCapChars {
		t.Errorf("a 34-tool server (the largest in this repo's own logs) hit the hard cap at %d chars", got)
	}
}

type stubControlQuerier struct{ body string }

func (s stubControlQuerier) Custom(_ context.Context, _, _, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(s.body), nil
}

func TestDiscoverMCPToolsTool_ExecuteSetsPreview(t *testing.T) {
	discovered, err := json.Marshal([]map[string]any{realisticDiscoveredTool("detect_objects")})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	tl := &DiscoverMCPToolsTool{}
	res, err := tl.Execute(context.Background(),
		map[string]any{"server_url": "http://127.0.0.1:9666/mcp"},
		&tool.Context{ControlQuerier: stubControlQuerier{body: string(discovered)}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("len(DataObjects) = %d, want 1", len(res.DataObjects))
	}

	do := res.DataObjects[0]
	if do.Preview == "" {
		t.Fatal("Preview is empty, so internal/agent falls back to tail-cutting the raw JSON — the bug this fixes")
	}
	for _, want := range []string{"detect_objects", "mcp_structured_content", "objects"} {
		if !strings.Contains(do.Preview, want) {
			t.Errorf("Preview omits %q: %q", want, do.Preview)
		}
	}
	if !strings.Contains(string(do.Content), "inputSchema") {
		t.Errorf("Content is not the full discovery document: %q", do.Content)
	}
	if do.Disposition != tool.DispositionArtifactWithPreview {
		t.Errorf("Disposition = %q, want %q", do.Disposition, tool.DispositionArtifactWithPreview)
	}
}

func TestSchemaFieldNames_SortedAndFieldless(t *testing.T) {
	got := schemaFieldNames(map[string]any{"type": "object", "properties": map[string]any{
		"zeta": true, "alpha": true, "mid": true,
	}})
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("schemaFieldNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("schemaFieldNames = %v, want %v (sorted so identical discoveries render identically)", got, want)
		}
	}

	for name, schema := range map[string]any{
		"nil":       nil,
		"not-a-map": "whatever",
		"array":     map[string]any{"type": "array"},
		"defs-only": map[string]any{"$defs": map[string]any{"Box": map[string]any{"type": "object"}}},
		"no-props":  map[string]any{"type": "object"},
	} {
		if got := schemaFieldNames(schema); len(got) != 0 {
			t.Errorf("schemaFieldNames(%s) = %v, want empty", name, got)
		}
	}
}
