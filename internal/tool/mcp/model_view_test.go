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

const ordersJSON = `{"count": 2, "orders": [{"id": "O1", "total": 12.5}, {"id": "O2", "total": 7}]}`

func modelText(t *testing.T, r *tool.Result) any {
	t.Helper()
	view, ok := r.LLMData[mcpToolOutputKey].(map[string]any)
	if !ok {
		t.Fatalf("LLMData[%q] missing: %#v", mcpToolOutputKey, r.LLMData)
	}
	return view["content"].([]map[string]any)[0]["text"]
}

func dataText(t *testing.T, r *tool.Result) any {
	t.Helper()
	return r.Data[mcpToolOutputKey].(map[string]any)["content"].([]map[string]any)[0]["text"]
}

func TestJSONTextReachesTheModelUnescaped(t *testing.T) {
	result := handle(t, mustParse(t, `{"content":[{"type":"text","text":"x"}]}`), ordersJSON)

	if got := dataText(t, result); got != ordersJSON {
		t.Errorf("Data text = %#v, want the server's string unchanged", got)
	}
	raw, ok := modelText(t, result).(json.RawMessage)
	if !ok {
		t.Fatalf("model text = %#v, want the JSON embedded as JSON", modelText(t, result))
	}
	rendered, err := json.Marshal(map[string]any{mcpToolOutputKey: result.LLMData[mcpToolOutputKey]})
	if err != nil {
		t.Fatalf("marshal model view: %v", err)
	}
	if strings.Contains(string(rendered), `\"`) {
		t.Errorf("model view still escapes quotes: %s", rendered)
	}
	escaped, _ := json.Marshal(map[string]any{mcpToolOutputKey: result.Data[mcpToolOutputKey]})
	if len(rendered) >= len(escaped) {
		t.Errorf("model view is %d bytes, not smaller than the escaped %d", len(rendered), len(escaped))
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded["count"] != float64(2) {
		t.Errorf("model text does not decode to the server's document: %s (%v)", raw, err)
	}
}

func TestPlainTextHasNoModelView(t *testing.T) {
	for _, text := range []string{"plain words", "[not json", "42", `"a json string"`, "{\"name\": \"a\xffb\"}"} {
		result := handle(t, mustParse(t, `{"content":[{"type":"text","text":"x"}]}`), text)
		if _, ok := result.LLMData[mcpToolOutputKey]; ok {
			t.Errorf("text %q got a model view; only JSON objects and arrays are re-embedded", text)
		}
	}
}

func TestAStructuredContentMirrorIsSentToTheModelOnce(t *testing.T) {
	parsed := mustParse(t, `{"content":[{"type":"text","text":"x"}],"structuredContent":`+ordersJSON+`}`)
	result := handle(t, parsed, ordersJSON)

	if got := modelText(t, result); got != structuredMirrorNote {
		t.Errorf("model text = %#v, want the mirror note", got)
	}
	if got := dataText(t, result); got != ordersJSON {
		t.Errorf("Data text = %#v, want the server's string unchanged", got)
	}
	if _, ok := result.Data[structuredContentKey].(map[string]any); !ok {
		t.Errorf("structured content missing from Data: %#v", result.Data)
	}
}

func TestTextThatDiffersFromStructuredContentIsKept(t *testing.T) {
	parsed := mustParse(t, `{"content":[{"type":"text","text":"x"}],"structuredContent":{"count":3}}`)
	result := handle(t, parsed, ordersJSON)

	if _, ok := modelText(t, result).(json.RawMessage); !ok {
		t.Errorf("model text = %#v, want the differing JSON kept", modelText(t, result))
	}
}

func TestASavedResultAlsoReachesTheModelUnescaped(t *testing.T) {
	cfg := DefaultHandlerConfig()
	cfg.SaveThresholdBytes = 10
	result, err := HandleMCPResponse(t.Context(), []ContentItem{{Type: ContentTypeText, Text: ordersJSON}},
		"list_orders", cfg, newMCPToolCtx(t), nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	if result.Status != "processed_and_saved" {
		t.Fatalf("Status = %q, want processed_and_saved", result.Status)
	}
	if _, ok := modelText(t, result).(json.RawMessage); !ok {
		t.Errorf("model text = %#v, want the JSON embedded as JSON", modelText(t, result))
	}
}

func TestAWithheldResultHasNoModelView(t *testing.T) {
	cfg := DefaultHandlerConfig()
	cfg.LLMReturnMaxBytes = 10
	result, err := HandleMCPResponse(t.Context(), []ContentItem{{Type: ContentTypeText, Text: ordersJSON}},
		"list_orders", cfg, newMCPToolCtx(t), nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	if result.Status != "processed_saved_artifact_only" {
		t.Fatalf("Status = %q, want processed_saved_artifact_only", result.Status)
	}
	if _, ok := result.LLMData[mcpToolOutputKey]; ok {
		t.Errorf("a withheld result gained a model view that would show the data: %#v", result.LLMData)
	}
}

func handleItems(t *testing.T, items []ContentItem, structured string) *tool.Result {
	t.Helper()
	var parsed *rawCallToolResult
	if structured != "" {
		parsed = mustParse(t, `{"content":[{"type":"text","text":"x"}],"structuredContent":`+structured+`}`)
	}
	result, err := HandleMCPResponse(t.Context(), items, "list_orders", DefaultHandlerConfig(), newMCPToolCtx(t), parsed)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	return result
}

func viewItems(t *testing.T, r *tool.Result, key string) []map[string]any {
	t.Helper()
	source := r.Data
	if key == "model" {
		source = r.LLMData
	}
	out, ok := source[mcpToolOutputKey].(map[string]any)
	if !ok {
		t.Fatalf("%s %q missing: %#v", key, mcpToolOutputKey, source)
	}
	return out["content"].([]map[string]any)
}

func TestEveryItemOfAMultiItemResultGetsItsOwnView(t *testing.T) {
	items := []ContentItem{
		{Type: ContentTypeText, Text: "two orders found"},
		{Type: ContentTypeText, Text: ordersJSON},
		{Type: ContentTypeText, Text: `{"count": 3}`},
	}
	result := handleItems(t, items, `{"count": 3}`)

	model := viewItems(t, result, "model")
	if model[0]["text"] != "two orders found" {
		t.Errorf("item 0 = %#v, want the plain text unchanged", model[0]["text"])
	}
	if _, ok := model[1]["text"].(json.RawMessage); !ok {
		t.Errorf("item 1 = %#v, want the orders embedded as JSON", model[1]["text"])
	}
	if model[2]["text"] != structuredMirrorNote {
		t.Errorf("item 2 = %#v, want the mirror note", model[2]["text"])
	}
	for i, item := range viewItems(t, result, "data") {
		if item["text"] != items[i].Text {
			t.Errorf("Data item %d = %#v, want the server's string unchanged", i, item["text"])
		}
	}
}

func TestAnImageBesideJSONKeepsItsEntry(t *testing.T) {
	items := []ContentItem{
		{Type: ContentTypeImage, Data: []byte("\x89PNG\r\n\x1a\n"), MIMEType: "image/png", Filename: "chart.png"},
		{Type: ContentTypeText, Text: ordersJSON},
	}
	result := handleItems(t, items, "")

	model := viewItems(t, result, "model")
	if model[0]["type"] != string(ContentTypeImage) || model[0]["mimeType"] != "image/png" {
		t.Errorf("image entry = %#v, want it as Data has it", model[0])
	}
	if _, ok := model[1]["text"].(json.RawMessage); !ok {
		t.Errorf("text entry = %#v, want the JSON embedded as JSON", model[1]["text"])
	}
}

func TestPrettyPrintedJSONIsEmbedded(t *testing.T) {
	pretty := "\n{\n  \"count\": 2,\n  \"orders\": [\"O1\", \"O2\"]\n}\n"
	result := handleItems(t, []ContentItem{{Type: ContentTypeText, Text: pretty}}, "")
	if _, ok := viewItems(t, result, "model")[0]["text"].(json.RawMessage); !ok {
		t.Errorf("model text = %#v, want pretty-printed JSON embedded as JSON", viewItems(t, result, "model")[0]["text"])
	}
}

func TestTheMirrorNoteNeedsAnExactCopy(t *testing.T) {
	cases := []struct {
		name, text, structured string
		wantNote               bool
	}{
		{"keys in another order", `{"a": 2, "b": 1}`, `{"b":1,"a":2}`, true},
		{"integer past 2^53 in both", `{"id": 9007199254740993}`, `{"id":9007199254740993}`, false},
		{"integers that differ past 2^53", `{"id": 9007199254740993}`, `{"id":9007199254740992}`, false},
		{"a 19-digit id", `{"id": 1234567890123456789}`, `{"id":1234567890123456789}`, false},
		{"whole numbers", `{"n": 12, "m": [1, 2]}`, `{"m":[1,2],"n":12}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := handleItems(t, []ContentItem{{Type: ContentTypeText, Text: tc.text}}, tc.structured)
			got := viewItems(t, result, "model")[0]["text"]
			if isNote := got == structuredMirrorNote; isNote != tc.wantNote {
				t.Errorf("model text = %#v, want note=%v", got, tc.wantNote)
			}
		})
	}
}

func TestAResultBetweenTheDefaultLimitsIsSavedAndShownUnescaped(t *testing.T) {
	page := `{"events": [` + strings.Repeat(`{"id": "EV000001", "type": "order_placed", "amount": 12.5},`, 40) + `{"id": "last"}]}`
	if len(page) <= DefaultSaveThresholdBytes || len(page) >= DefaultLLMReturnMaxBytes {
		t.Fatalf("page is %d bytes; it must sit between the save threshold and the inline limit", len(page))
	}
	result, err := HandleMCPResponse(t.Context(), []ContentItem{{Type: ContentTypeText, Text: page}},
		"list_events", DefaultHandlerConfig(), newMCPToolCtx(t), nil)
	if err != nil {
		t.Fatalf("HandleMCPResponse: %v", err)
	}
	if result.Status != "processed_and_saved" {
		t.Fatalf("Status = %q, want processed_and_saved (saved, and also shown inline)", result.Status)
	}
	if _, ok := modelText(t, result).(json.RawMessage); !ok {
		t.Errorf("model text = %#v, want the JSON embedded as JSON", modelText(t, result))
	}
}
