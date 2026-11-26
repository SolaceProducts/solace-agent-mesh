// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestStructuredContentReachesToolResult(t *testing.T) {
	parsed := mustParse(t, `{"content":[{"type":"text","text":"{\"id\":\"cal-1\"}"}],`+
		`"structuredContent":{"id":"cal-1","summary":"Roadmap"}}`)
	if len(parsed.StructuredContent) == 0 {
		t.Fatal("structuredContent was dropped while parsing the JSON-RPC result")
	}

	result := handle(t, parsed, `{"id":"cal-1"}`)

	if result.Status != "processed" {
		t.Fatalf("Status = %q, want %q — the assertions below would not be on the inline path", result.Status, "processed")
	}
	structured, ok := result.Data[structuredContentKey].(map[string]any)
	if !ok {
		t.Fatalf("Data[%q] missing or wrong type: %#v", structuredContentKey, result.Data[structuredContentKey])
	}
	if structured["id"] != "cal-1" {
		t.Errorf(`id = %v, want "cal-1"`, structured["id"])
	}
}

func TestNoStructuredContentLeavesResultUnchanged(t *testing.T) {
	parsed := mustParse(t, `{"content":[{"type":"text","text":"plain"}]}`)
	result := handle(t, parsed, "plain")
	if _, exists := result.Data[structuredContentKey]; exists {
		t.Errorf("Data[%q] was invented for a server that sent no structuredContent", structuredContentKey)
	}
}

func TestStructuredContentNullDoesNotInventKey(t *testing.T) {
	parsed := mustParse(t, `{"content":[{"type":"text","text":"plain"}],"structuredContent":null}`)
	result := handle(t, parsed, "plain")
	if _, exists := result.Data[structuredContentKey]; exists {
		t.Errorf("Data[%q] was invented for an explicit null", structuredContentKey)
	}
}

func TestStructuredContentNonObjectIsRejected(t *testing.T) {
	for name, payload := range map[string]string{
		"array":  `[1,2]`,
		"scalar": `"scalar"`,
		"number": `7`,
	} {
		t.Run(name, func(t *testing.T) {
			parsed := mustParse(t, `{"content":[{"type":"text","text":"plain"}],"structuredContent":`+payload+`}`)
			result := handle(t, parsed, "plain")
			if _, exists := result.Data[structuredContentKey]; exists {
				t.Errorf("Data[%q] accepted a non-object payload %s", structuredContentKey, payload)
			}
		})
	}
}

func TestStructuredContentPartialDecodeKeepsGoodFields(t *testing.T) {
	parsed := mustParse(t, `{"content":[{"type":"text","text":"x"}],`+
		`"structuredContent":{"id":"cal-1","amount":1e999999}}`)
	result := handle(t, parsed, "x")

	structured, ok := result.Data[structuredContentKey].(map[string]any)
	if !ok {
		t.Fatalf("Data[%q] missing: a partial decode discarded every good field", structuredContentKey)
	}
	if structured["id"] != "cal-1" {
		t.Errorf(`id = %v, want "cal-1"`, structured["id"])
	}
}

func TestStructuredContentIsBoundedOnWithholdPath(t *testing.T) {
	big := strings.Repeat("z", 200_000)
	payload := map[string]any{"id": "cal-1", "blob": big}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed := mustParse(t, `{"content":[{"type":"text","text":`+string(mustJSON(string(encoded)))+`}],`+
		`"structuredContent":`+string(encoded)+`}`)

	result := handle(t, parsed, string(encoded))

	if !strings.Contains(result.Status, "artifact_only") {
		t.Fatalf("Status = %q, want an artifact-only (withheld) status — this test must run on the withhold path", result.Status)
	}
	attached, exists := result.Data[structuredContentKey]
	if !exists {
		t.Fatalf("Data[%q] missing entirely; the bounded form should survive", structuredContentKey)
	}
	attachedBytes := mustMarshalLen(t, attached)
	if attachedBytes >= len(encoded) {
		t.Errorf("attached %d bytes for a %d-byte payload: it was not bounded", attachedBytes, len(encoded))
	}
	if attachedBytes > skeletonOverallMaxBytes {
		t.Errorf("attached %d bytes, want <= %d", attachedBytes, skeletonOverallMaxBytes)
	}
	if m, ok := attached.(map[string]any); !ok || m["id"] != "cal-1" {
		t.Errorf("id did not survive bounding: %#v", attached)
	}
}

func TestStructuredContentSurvivesEmptyContent(t *testing.T) {
	parsed := mustParse(t, `{"content":[],"structuredContent":{"id":"cal-1"}}`)

	result, err := HandleMCPResponse(t.Context(), nil, "create_calendar",
		DefaultHandlerConfig(), newMCPToolCtx(t), parsed)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	structured, ok := result.Data[structuredContentKey].(map[string]any)
	if !ok {
		t.Fatalf("Data[%q] missing: the sole carrier of the data was dropped", structuredContentKey)
	}
	if structured["id"] != "cal-1" {
		t.Errorf(`id = %v, want "cal-1"`, structured["id"])
	}
}

func mustParse(t *testing.T, raw string) *rawCallToolResult {
	t.Helper()
	parsed, err := parseCallToolResultRaw(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return parsed
}

func handle(t *testing.T, parsed *rawCallToolResult, text string) *tool.Result {
	t.Helper()
	result, err := HandleMCPResponse(t.Context(),
		[]ContentItem{{Type: ContentTypeText, Text: text}},
		"create_calendar", DefaultHandlerConfig(), newMCPToolCtx(t), parsed)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	return result
}

func mustMarshalLen(t *testing.T, v any) int {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return len(b)
}
