// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const sqlRows = `[{"region":"north","total_orders":1204}]`

func inlineJSONObject(name string, content []byte) tool.DataObject {
	return tool.DataObject{
		Name:        name,
		Content:     content,
		MIMEType:    "application/json",
		Disposition: tool.DispositionInline,
	}
}

func resultDataMap(t *testing.T, r *tool.Result) map[string]any {
	t.Helper()
	got := vizPayload(r)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want a map carrying the DataObject content", got)
	}
	return m
}

func TestToolResultDataInlinesSingleDataObject(t *testing.T) {
	m := resultDataMap(t, tool.OK("Query returned 28 rows",
		tool.WithDataObjects(inlineJSONObject("sql_query_result.json", []byte(sqlRows)))))

	if m["content"] != sqlRows {
		t.Errorf("content = %v, want the rows JSON", m["content"])
	}
	if m["message"] != "Query returned 28 rows" {
		t.Errorf("message = %v, want the summary to survive", m["message"])
	}
}

func TestToolResultDataOmitsNonTerminalStatus(t *testing.T) {
	m := resultDataMap(t, tool.OK("Query returned 28 rows",
		tool.WithDataObjects(inlineJSONObject("r.json", []byte(sqlRows)))))

	if _, exists := m["status"]; exists {
		t.Errorf("status = %v, want it absent until the envelope is final", m["status"])
	}
}

func TestToolResultDataInlinesMultipleDataObjects(t *testing.T) {
	m := resultDataMap(t, tool.OK("two results",
		tool.WithDataObjects(
			inlineJSONObject("a.json", []byte(`{"a":1}`)),
			inlineJSONObject("b.json", []byte(`{"b":2}`)),
		)))

	outputs, ok := m["inline_outputs"].([]map[string]any)
	if !ok {
		t.Fatalf("inline_outputs = %T, want a list", m["inline_outputs"])
	}
	if len(outputs) != 2 {
		t.Fatalf("got %d inline outputs, want 2", len(outputs))
	}
	if outputs[0]["name"] != "a.json" || outputs[0]["content"] != `{"a":1}` {
		t.Errorf("first output = %v, want a.json with its content", outputs[0])
	}
	if _, exists := m["content"]; exists {
		t.Error("content key set alongside inline_outputs")
	}
}

func TestToolResultDataOmitsBinaryContent(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x00, 0xFF, 0xFE}
	r := tool.OK("chart rendered", tool.WithDataObjects(tool.DataObject{
		Name:        "chart.png",
		Content:     png,
		MIMEType:    "image/png",
		Disposition: tool.DispositionInline,
	}))

	got := vizPayload(r)
	if got != "chart rendered" {
		t.Fatalf("got %v, want the bare message when nothing is previewable", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "PNG") {
		t.Errorf("payload leaked binary content: %s", encoded)
	}
}

func TestToolResultDataOmitsInvalidUTF8(t *testing.T) {
	r := tool.OK("text-ish", tool.WithDataObjects(tool.DataObject{
		Name:        "broken.txt",
		Content:     []byte{0xFF, 0xFE, 0xFD},
		MIMEType:    "text/plain",
		Disposition: tool.DispositionInline,
	}))

	if got := vizPayload(r); got != "text-ish" {
		t.Errorf("got %v, want invalid UTF-8 elided", got)
	}
}

func TestToolResultDataOmitsArtifactBoundObjects(t *testing.T) {
	cases := map[string]tool.DataObject{
		"already saved upstream": {
			Name:        "spilled.json",
			MIMEType:    "application/json",
			ArtifactRef: &tool.ArtifactRef{Filename: "spilled.json", Version: 1, SizeBytes: 900},
		},
		"artifact disposition": {
			Name:        "report.csv",
			Content:     []byte("a,b,c"),
			MIMEType:    "text/csv",
			Disposition: tool.DispositionArtifact,
		},
		"artifact with preview": {
			Name:        "report.csv",
			Content:     []byte("a,b,c"),
			MIMEType:    "text/csv",
			Disposition: tool.DispositionArtifactWithPreview,
		},
	}

	for name, do := range cases {
		t.Run(name, func(t *testing.T) {
			if got := vizPayload(tool.OK("saved", tool.WithDataObjects(do))); got != "saved" {
				t.Errorf("got %v, want the pre-existing bare message", got)
			}
		})
	}
}

func TestToolResultDataPreviewsDegradedInlineWithoutArtifactService(t *testing.T) {
	csv := "a,b,c\n1,2,3"
	r := tool.OK("report ready", tool.WithDataObjects(tool.DataObject{
		Name:        "report.csv",
		Content:     []byte(csv),
		MIMEType:    "text/csv",
		Disposition: tool.DispositionArtifact,
	}))

	got := vizPayloadNoArtifactService(r)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T (%v), want the degraded inline content", got, got)
	}
	if m["content"] != csv {
		t.Errorf("content = %v, want the CSV the model was handed inline", m["content"])
	}
}

func TestPublishedToolResultHonoursProcessingKillSwitch(t *testing.T) {
	agent, _, _ := newTestAgent()
	disabled := false
	agent.cfg.ToolResultProcessingEnabled = &disabled

	r := tool.OK("Query returned 28 rows",
		tool.WithDataObjects(inlineJSONObject("rows.json", []byte(sqlRows))))

	got := agent.publishedToolResult(r, &tool.Context{})

	if got != "Query returned 28 rows" {
		t.Errorf("got %v, want the bare message; processing is disabled", got)
	}
	if strings.Contains(fmt.Sprint(got), "north") {
		t.Errorf("row content published with the processor disabled: %v", got)
	}
}

func TestToolResultDataKeepsEmptyInlineObject(t *testing.T) {
	empty := inlineJSONObject("empty.json", []byte{})
	rows := inlineJSONObject("rows.json", []byte(sqlRows))

	m := resultDataMap(t, tool.OK("two", tool.WithDataObjects(empty, rows)))

	outputs, ok := m["inline_outputs"].([]map[string]any)
	if !ok {
		t.Fatalf("inline_outputs = %T, want both entries", m["inline_outputs"])
	}
	if len(outputs) != 2 {
		t.Fatalf("got %d outputs, want 2 — the model sees both", len(outputs))
	}
	if outputs[0]["name"] != "empty.json" || outputs[1]["name"] != "rows.json" {
		t.Errorf("outputs = %v, want empty.json then rows.json", outputs)
	}
}

func TestToolResultDataOmitsContentOverInlineCap(t *testing.T) {
	huge := strings.Repeat("x", defaultToolOutputSaveThresholdBytes+1)

	got := vizPayload(tool.OK("big", tool.WithDataObjects(inlineJSONObject("big.json", []byte(huge)))))

	if got != "big" {
		t.Errorf("got %v, want the bare message; the artifact signals carry an over-cap result", got)
	}
}

func TestToolResultDataHonoursInlineMaxBytesOverride(t *testing.T) {
	body := strings.Repeat("x", defaultToolOutputSaveThresholdBytes+1)
	do := inlineJSONObject("big.json", []byte(body))
	do.InlineMaxBytes = defaultToolOutputSaveThresholdBytes * 4

	m := resultDataMap(t, tool.OK("big", tool.WithDataObjects(do)))

	if m["content"] != body {
		t.Errorf("content len = %d, want the full %d bytes the override admits",
			len(m["content"].(string)), len(body))
	}
}

func TestToolResultDataCapsContentAdmittedByOverride(t *testing.T) {
	body := strings.Repeat("x", 100_000)
	do := inlineJSONObject("big.json", []byte(body))
	do.InlineMaxBytes = 100_000

	m := resultDataMap(t, tool.OK("big", tool.WithDataObjects(do)))

	content, ok := m["content"].(string)
	if !ok {
		t.Fatalf("content = %T, want a string", m["content"])
	}
	if !strings.Contains(content, "…[truncated") {
		t.Error("content admitted by the override is missing the truncation marker")
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(encoded) > 2*maxStatusEventTextBytes {
		t.Errorf("payload is %d bytes, want it bounded near %d", len(encoded), maxStatusEventTextBytes)
	}
}

func TestToolResultDataSharesBudgetAcrossDataObjects(t *testing.T) {
	const perObject = defaultToolOutputSaveThresholdBytes - 192
	objects := make([]tool.DataObject, 0, 50)
	for i := range 50 {
		objects = append(objects, inlineJSONObject(
			fmt.Sprintf("part-%d.json", i),
			[]byte(strings.Repeat("y", perObject)),
		))
	}

	m := resultDataMap(t, tool.OK("many", tool.WithDataObjects(objects...)))

	outputs, ok := m["inline_outputs"].([]map[string]any)
	if !ok {
		t.Fatalf("inline_outputs = %T, want a list", m["inline_outputs"])
	}
	omitted, ok := m["omitted_outputs"].(int)
	if !ok || omitted == 0 {
		t.Fatalf("omitted_outputs = %v, want the objects the budget could not fit", m["omitted_outputs"])
	}
	if got := len(outputs) + omitted; got != len(objects) {
		t.Errorf("previewed %d + omitted %d = %d, want all %d accounted for",
			len(outputs), omitted, got, len(objects))
	}

	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(encoded) > 3*maxStatusEventTextBytes {
		t.Errorf("payload is %d bytes for %d x %d-byte objects; the budget is per-object, not shared",
			len(encoded), len(objects), perObject)
	}
}

func TestToolResultDataKeepsListShapeWhenOutputsAreOmitted(t *testing.T) {
	big := []byte(strings.Repeat("y", maxStatusEventTextBytes-64))
	objects := []tool.DataObject{
		inlineJSONObject("first.json", big),
		inlineJSONObject("second.json", big),
	}
	for i := range objects {
		objects[i].InlineMaxBytes = maxStatusEventTextBytes
	}

	m := resultDataMap(t, tool.OK("two", tool.WithDataObjects(objects...)))

	if _, exists := m["content"]; exists {
		t.Error("payload flipped to `content`, dropping the surviving output's name")
	}
	outputs, ok := m["inline_outputs"].([]map[string]any)
	if !ok || len(outputs) != 1 {
		t.Fatalf("inline_outputs = %v, want the one output that fit", m["inline_outputs"])
	}
	if outputs[0]["name"] != "first.json" {
		t.Errorf("name = %v, want first.json", outputs[0]["name"])
	}
	if m["omitted_outputs"] != 1 {
		t.Errorf("omitted_outputs = %v, want 1", m["omitted_outputs"])
	}
}

func TestToolResultDataPreviewFitsTheStatusBudget(t *testing.T) {
	do := inlineJSONObject("big.json", []byte(strings.Repeat("x", 100_000)))
	do.InlineMaxBytes = 100_000

	m := resultDataMap(t, tool.OK("big", tool.WithDataObjects(do)))

	content, ok := m["content"].(string)
	if !ok {
		t.Fatalf("content = %T, want a string", m["content"])
	}
	if len(content) > maxStatusEventTextBytes {
		t.Errorf("preview is %d bytes, over the %d budget — the gateway will re-truncate it",
			len(content), maxStatusEventTextBytes)
	}
	if m["content_truncated"] != true {
		t.Error("content_truncated not set; the marker is the only other signal and the gateway rewrites it")
	}
	if !strings.Contains(content, "…[truncated 8") {
		t.Errorf("marker should report the bytes actually dropped, got tail %q", content[max(0, len(content)-40):])
	}
}

func TestToolResultDataFlagsTruncatedInlineOutputs(t *testing.T) {
	big := []byte(strings.Repeat("y", maxStatusEventTextBytes+1000))
	a := inlineJSONObject("a.json", big)
	a.InlineMaxBytes = maxStatusEventTextBytes * 2
	b := inlineJSONObject("b.json", []byte(sqlRows))

	m := resultDataMap(t, tool.OK("two", tool.WithDataObjects(a, b)))

	outputs, ok := m["inline_outputs"].([]map[string]any)
	if !ok || len(outputs) == 0 {
		t.Fatalf("inline_outputs = %v, want at least the truncated first entry", m["inline_outputs"])
	}
	if outputs[0]["truncated"] != true {
		t.Errorf("first output not flagged truncated: %v", outputs[0])
	}
}

func TestToolResultDataKeepsDataAlongsideDataObjects(t *testing.T) {
	r := tool.OK("searched",
		tool.WithData(map[string]any{"rag_metadata": map[string]any{"query": "afl"}}),
		tool.WithDataObjects(inlineJSONObject("hits.json", []byte(sqlRows))))

	m := resultDataMap(t, r)
	if _, ok := m["rag_metadata"]; !ok {
		t.Error("rag_metadata dropped; scheduled-task RAG citations read it")
	}
	if m["content"] != sqlRows {
		t.Errorf("content = %v, want the DataObject content", m["content"])
	}
}

func TestToolResultDataPreservesErrorStatusWithDataObjects(t *testing.T) {
	r := &tool.Result{
		Status:      tool.StatusError,
		Message:     "boom",
		DataObjects: []tool.DataObject{inlineJSONObject("partial.json", []byte(`{"a":1}`))},
	}

	m := resultDataMap(t, r)
	if m["status"] != tool.StatusError {
		t.Errorf("status = %v, want the error status preserved for the Slack timeline", m["status"])
	}
	if m["message"] != "boom" {
		t.Errorf("message = %v, want boom", m["message"])
	}
}

func TestToolResultDataAvoidsReservedKeys(t *testing.T) {
	m := resultDataMap(t, tool.OK("done",
		tool.WithDataObjects(inlineJSONObject("r.json", []byte(sqlRows)))))

	for _, key := range []string{"error", "type"} {
		if _, exists := m[key]; exists {
			t.Errorf("payload sets the reserved %q key", key)
		}
	}
}

func TestToolResultDataDoesNotMutateResult(t *testing.T) {
	content := []byte(strings.Repeat("z", 100_000))
	do := inlineJSONObject("big.json", content)
	do.InlineMaxBytes = 100_000
	r := tool.OK("big",
		tool.WithData(map[string]any{"count": 1}),
		tool.WithDataObjects(do))

	vizPayload(r)

	if len(r.Data) != 1 || r.Data["count"] != 1 {
		t.Errorf("Data mutated: %v", r.Data)
	}
	if len(r.DataObjects[0].Content) != len(content) {
		t.Errorf("DataObject content truncated in place: %d bytes, want %d",
			len(r.DataObjects[0].Content), len(content))
	}
}
