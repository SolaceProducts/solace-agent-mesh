// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func newResolveContext(store *memArtifactStore) *ResolveContext {
	return &ResolveContext{
		Artifacts: newTestArtifactSvc(store),
		AgentName: "test",
		UserID:    "user1",
		SessionID: "sess1",
		Config:    DefaultResolveConfig(),
		Logger:    slog.Default(),
	}
}

func saveArtifact(store *memArtifactStore, filename string, content []byte, mimeType string) {
	ctx := context.Background()
	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: filename}
	if _, err := store.Save(ctx, loc, content, mimeType); err != nil {
		panic(fmt.Sprintf("save artifact %q: %v", filename, err))
	}
}

func TestResolve_NoEmbeds(t *testing.T) {
	text := "Hello, world!"
	rctx := newResolveContext(newMemArtifactStore())

	result, signals, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != text {
		t.Errorf("got %q, want %q", result, text)
	}
	if len(signals) != 0 {
		t.Errorf("expected 0 signals, got %d", len(signals))
	}
}

func TestResolve_NilContext(t *testing.T) {
	result, signals, err := ResolveEmbedsInString(context.Background(), "hello", ModeA2AMessageToUser, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello" {
		t.Errorf("got %q", result)
	}
	if len(signals) != 0 {
		t.Errorf("expected 0 signals, got %d", len(signals))
	}
}

func TestResolve_StatelessEarlyEmbedsResolved(t *testing.T) {
	text := "result: «math:1+1»"
	rctx := newResolveContext(newMemArtifactStore())

	result, _, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != "result: 2" {
		t.Errorf("expected math embed resolved, got %q", result)
	}
}

func TestResolve_ArtifactMetaEarlyEmbedLeftUntouched(t *testing.T) {
	text := "info: «artifact_meta:file.csv»"
	rctx := newResolveContext(newMemArtifactStore())

	result, _, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != text {
		t.Errorf("artifact_meta should be unchanged, got %q", result)
	}
}

func TestResolve_UnknownEmbedLeftUntouched(t *testing.T) {
	text := "«unknown_type:something»"
	rctx := newResolveContext(newMemArtifactStore())

	result, _, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != text {
		t.Errorf("unknown embeds should be unchanged, got %q", result)
	}
}

func TestResolve_ArtifactContent_TextPlain(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "notes.txt", []byte("Hello from artifact"), "text/plain")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "Before «artifact_content:notes.txt» After", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Hello from artifact") {
		t.Errorf("artifact content missing: %q", result)
	}
	if !strings.Contains(result, "Before") || !strings.Contains(result, "After") {
		t.Errorf("surrounding text missing: %q", result)
	}
}

func TestResolve_ArtifactContent_JSON(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`{"name":"Alice","age":30}`), "application/json")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.json»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("JSON data missing: %q", result)
	}
}

func TestResolve_ArtifactContent_JSONAsJSON(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`{"name":"Alice","age":30}`), "application/json")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.json >>> format:json»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"name"`) || !strings.Contains(result, `"Alice"`) {
		t.Errorf("JSON format missing expected keys: %q", result)
	}
}

func TestResolve_ArtifactContent_JSONAsPretty(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`{"name":"Alice"}`), "application/json")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.json >>> format:json_pretty»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "\n") {
		t.Errorf("pretty JSON should be indented: %q", result)
	}
}

func TestResolve_ArtifactContent_CSVDefaultPreservesLiteralBytes(t *testing.T) {
	store := newMemArtifactStore()
	csv := "Name,Age\nAlice,30\nBob,25"
	saveArtifact(store, "data.csv", []byte(csv), "text/csv")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.csv»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != csv {
		t.Errorf("default CSV embed should pass through literal bytes\n  got:  %q\n  want: %q", result, csv)
	}
}

func TestResolve_ArtifactContent_JSONDefaultPreservesFormatting(t *testing.T) {
	store := newMemArtifactStore()
	pretty := "{\n  \"name\": \"Alice\",\n  \"age\": 30\n}"
	saveArtifact(store, "data.json", []byte(pretty), "application/json")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.json»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != pretty {
		t.Errorf("default JSON embed should pass through literal bytes (preserving indentation)\n  got:  %q\n  want: %q", result, pretty)
	}
}

func TestResolve_ArtifactContent_CSVAsCSV(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.csv", []byte("Name,Age\nAlice,30\nBob,25"), "text/csv")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.csv >>> format:csv»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Alice") || !strings.Contains(result, "Bob") {
		t.Errorf("CSV data missing: %q", result)
	}
}

func TestResolve_ArtifactContent_NotFound(t *testing.T) {
	store := newMemArtifactStore()
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:missing.txt»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[Embed Error:") || !strings.Contains(result, "not found") {
		t.Errorf("expected error message: %q", result)
	}
}

func TestResolve_ArtifactContent_NoArtifactService(t *testing.T) {
	rctx := &ResolveContext{
		Artifacts: nil,
		Config:    DefaultResolveConfig(),
		Logger:    slog.Default(),
	}

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:file.txt»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[Embed Error:") {
		t.Errorf("expected error for no artifact service: %q", result)
	}
}

func TestResolve_ArtifactContent_SizeLimit(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "big.txt", []byte(strings.Repeat("x", 1000)), "text/plain")
	rctx := newResolveContext(store)
	rctx.Config.MaxArtifactSizeBytes = 100

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:big.txt»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[Truncated: artifact size limit reached]") {
		t.Errorf("expected per-artifact truncation marker, got: %q", result)
	}
}

func TestResolve_ArtifactContent_EmptyExpression(t *testing.T) {
	store := newMemArtifactStore()
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "[Embed Error:") {
		t.Errorf("expected error for empty expression: %q", result)
	}
}

func TestResolve_Chain_JSONPath(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`{"items":[{"name":"Alice"},{"name":"Bob"}]}`), "application/json")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.json >>> jsonpath:$.items >>> format:json»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Alice") || !strings.Contains(result, "Bob") {
		t.Errorf("jsonpath result missing data: %q", result)
	}
}

func TestResolve_Chain_SelectCols(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.csv", []byte("Name,Age,City\nAlice,30,NYC\nBob,25,LA"), "text/csv")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.csv >>> select_cols:Name,Age >>> format:csv»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "NYC") || strings.Contains(result, "LA") {
		t.Errorf("select_cols should exclude City column: %q", result)
	}
	if !strings.Contains(result, "Alice") || !strings.Contains(result, "Bob") {
		t.Errorf("select_cols should keep Name column: %q", result)
	}
}

func TestResolve_Chain_FilterSelect(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.csv", []byte("Name,Role\nAlice,admin\nBob,user\nCharlie,admin"), "text/csv")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.csv >>> filter_rows_eq:Role:admin >>> select_cols:Name >>> format:csv»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Alice") || !strings.Contains(result, "Charlie") {
		t.Errorf("filter should keep admins: %q", result)
	}
	if strings.Contains(result, "Bob") {
		t.Errorf("filter should exclude Bob: %q", result)
	}
}

func TestResolve_Chain_GrepHead(t *testing.T) {
	store := newMemArtifactStore()
	content := "error: foo\ninfo: bar\nerror: baz\ninfo: qux\nerror: quux"
	saveArtifact(store, "log.txt", []byte(content), "text/plain")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:log.txt >>> grep:^error >>> head:2»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "error: foo") || !strings.Contains(result, "error: baz") {
		t.Errorf("grep+head should get first 2 error lines: %q", result)
	}
	if strings.Contains(result, "error: quux") {
		t.Errorf("head:2 should limit to 2 lines: %q", result)
	}
}

func TestResolve_RecursiveArtifact(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "inner.txt", []byte("INNER"), "text/plain")
	saveArtifact(store, "outer.txt", []byte("Start «artifact_content:inner.txt» End"), "text/plain")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:outer.txt»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Start INNER End") {
		t.Errorf("recursive resolution failed: %q", result)
	}
}

func TestResolve_DepthLimit(t *testing.T) {
	store := newMemArtifactStore()
	rctx := newResolveContext(store)
	rctx.Config.MaxDepth = 3

	saveArtifact(store, "d0.txt", []byte("«artifact_content:d1.txt»"), "text/plain")
	saveArtifact(store, "d1.txt", []byte("«artifact_content:d2.txt»"), "text/plain")
	saveArtifact(store, "d2.txt", []byte("«artifact_content:d3.txt»"), "text/plain")
	saveArtifact(store, "d3.txt", []byte("DEEP"), "text/plain")

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:d0.txt»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "DEEP") {
		t.Errorf("depth limit should prevent reaching DEEP: %q", result)
	}
}

func TestResolve_CircularReference(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "a.txt", []byte("A «artifact_content:b.txt» A"), "text/plain")
	saveArtifact(store, "b.txt", []byte("B «artifact_content:a.txt» B"), "text/plain")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:a.txt»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Circular") {
		t.Errorf("should detect circular reference: %q", result)
	}
}

func TestResolve_ArtifactReturn_A2AMode(t *testing.T) {
	store := newMemArtifactStore()
	rctx := newResolveContext(store)

	result, signals, err := ResolveEmbedsInString(context.Background(), "See «artifact_return:report.pdf» here", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "artifact_return") {
		t.Errorf("artifact_return should be stripped in A2A mode: %q", result)
	}
	if len(signals) != 1 || signals[0].Type != SignalArtifactReturn {
		t.Errorf("expected 1 artifact_return signal, got %v", signals)
	}
	data := signals[0].Data.(ArtifactReturnData)
	if data.Filename != "report.pdf" {
		t.Errorf("filename = %q, want 'report.pdf'", data.Filename)
	}
}

func TestResolve_ArtifactReturn_ToolMode(t *testing.T) {
	store := newMemArtifactStore()
	rctx := newResolveContext(store)

	result, signals, err := ResolveEmbedsInString(context.Background(), "«artifact_return:report.pdf»", ModeToolParameter, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "artifact_return") {
		t.Errorf("artifact_return should be preserved in tool mode: %q", result)
	}
	if len(signals) != 0 {
		t.Errorf("expected 0 signals in tool mode, got %d", len(signals))
	}
}

func TestResolve_StatusUpdate_A2AMode(t *testing.T) {
	store := newMemArtifactStore()
	rctx := newResolveContext(store)

	result, signals, err := ResolveEmbedsInString(context.Background(), "Start «status_update:Processing...» End", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result, "status_update") {
		t.Errorf("status_update should be stripped: %q", result)
	}
	if len(signals) != 1 || signals[0].Type != SignalStatusUpdate {
		t.Errorf("expected 1 status_update signal, got %v", signals)
	}
	if signals[0].Data != "Processing..." {
		t.Errorf("signal data = %v, want 'Processing...'", signals[0].Data)
	}
}

func TestResolve_StatusUpdate_ToolMode(t *testing.T) {
	store := newMemArtifactStore()
	rctx := newResolveContext(store)

	result, signals, err := ResolveEmbedsInString(context.Background(), "«status_update:msg»", ModeToolParameter, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "status_update") {
		t.Errorf("status_update should be preserved in tool mode: %q", result)
	}
	if len(signals) != 0 {
		t.Errorf("expected 0 signals in tool mode, got %d", len(signals))
	}
}

func TestResolve_InlineBinary_A2AMode(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "image.png", []byte{0x89, 0x50, 0x4E, 0x47}, "image/png")
	rctx := newResolveContext(store)

	result, signals, err := ResolveEmbedsInString(context.Background(), "«artifact_content:image.png»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Errorf("binary content should be replaced with empty string: %q", result)
	}
	if len(signals) != 1 || signals[0].Type != SignalInlineBinaryContent {
		t.Errorf("expected inline_binary signal, got %v", signals)
	}
	data := signals[0].Data.(InlineBinaryData)
	if data.MIMEType != "image/png" {
		t.Errorf("MIME type = %q", data.MIMEType)
	}
	if data.Name != "image.png" {
		t.Errorf("name = %q", data.Name)
	}
}

func TestResolve_DeepResearchReport(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "analysis_report.md", []byte("# Analysis\nDetailed findings."), "text/markdown")
	rctx := newResolveContext(store)

	result, signals, err := ResolveEmbedsInString(context.Background(), "«artifact_content:analysis_report.md»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Analysis") {
		t.Errorf("report content missing: %q", result)
	}
	found := false
	for _, s := range signals {
		if s.Type == SignalDeepResearchReport {
			found = true
			data := s.Data.(DeepResearchReportData)
			if data.Filename != "analysis_report.md" {
				t.Errorf("filename = %q", data.Filename)
			}
		}
	}
	if !found {
		t.Errorf("expected deep_research_report signal, got %v", signals)
	}
}

func TestResolve_MultipleEmbeds(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "a.txt", []byte("AAA"), "text/plain")
	saveArtifact(store, "b.txt", []byte("BBB"), "text/plain")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "1:«artifact_content:a.txt» 2:«artifact_content:b.txt»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "1:AAA") || !strings.Contains(result, "2:BBB") {
		t.Errorf("multiple embeds not resolved: %q", result)
	}
}

func TestResolve_MixedSignals(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.txt", []byte("content"), "text/plain")
	rctx := newResolveContext(store)

	text := "«status_update:Working» «artifact_content:data.txt» «artifact_return:result.pdf»"
	result, signals, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "content") {
		t.Errorf("artifact content missing: %q", result)
	}

	if len(signals) != 2 {
		t.Fatalf("expected 2 signals, got %d: %v", len(signals), signals)
	}
	types := map[string]bool{}
	for _, s := range signals {
		types[s.Type] = true
	}
	if !types[SignalStatusUpdate] || !types[SignalArtifactReturn] {
		t.Errorf("expected status_update and artifact_return signals, got types: %v", types)
	}
}

func TestResolve_TemplateBlockInText(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`[{"name":"Alice"},{"name":"Bob"}]`), "application/json")
	rctx := newResolveContext(store)

	text := `Before «««template_liquid: data="data.json"
{% for item in items %}{{ item.name }}
{% endfor %}»»» After`

	result, _, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Alice") || !strings.Contains(result, "Bob") {
		t.Errorf("template not resolved: %q", result)
	}
	if !strings.Contains(result, "Before") || !strings.Contains(result, "After") {
		t.Errorf("surrounding text missing: %q", result)
	}
}

func TestResolve_ApplyToTemplate(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.json", []byte(`[{"name":"Alice","score":95},{"name":"Bob","score":87}]`), "application/json")
	saveArtifact(store, "report.liquid", []byte("{% for item in items %}{{ item.name }}: {{ item.score }}\n{% endfor %}"), "text/plain")
	rctx := newResolveContext(store)

	result, _, err := ResolveEmbedsInString(context.Background(), "«artifact_content:data.json >>> apply_to_template:report.liquid»", ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Alice: 95") || !strings.Contains(result, "Bob: 87") {
		t.Errorf("template not applied: %q", result)
	}
}

func TestPreParseByMIME_JSON(t *testing.T) {
	data, format := preParseByMIME(`{"key":"val"}`, "application/json")
	if format != FormatJSON {
		t.Errorf("expected FormatJSON, got %v", format)
	}
	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", data)
	}
	if m["key"] != "val" {
		t.Errorf("key = %v", m["key"])
	}
}

func TestPreParseByMIME_YAML(t *testing.T) {
	data, format := preParseByMIME("name: test\nvalue: 42", "application/yaml")
	if format != FormatJSON {
		t.Errorf("expected FormatJSON, got %v", format)
	}
	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", data)
	}
	if m["name"] != "test" {
		t.Errorf("name = %v", m["name"])
	}
}

func TestPreParseByMIME_CSV(t *testing.T) {
	data, format := preParseByMIME("a,b\n1,2\n3,4", "text/csv")
	if format != FormatJSON {
		t.Errorf("expected FormatJSON, got %v", format)
	}
	arr, ok := data.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", data)
	}
	if len(arr) != 2 {
		t.Errorf("expected 2 rows, got %d", len(arr))
	}
}

func TestPreParseByMIME_PlainText(t *testing.T) {
	data, format := preParseByMIME("hello world", "text/plain")
	if format != FormatString {
		t.Errorf("expected FormatString, got %v", format)
	}
	s, ok := data.(string)
	if !ok || s != "hello world" {
		t.Errorf("expected 'hello world', got %v", data)
	}
}

func TestPreParseByMIME_InvalidJSON(t *testing.T) {
	data, format := preParseByMIME("{invalid}", "application/json")
	if format != FormatString {
		t.Errorf("expected FormatString for invalid JSON, got %v", format)
	}
	_, ok := data.(string)
	if !ok {
		t.Errorf("expected string fallback, got %T", data)
	}
}

func TestResolve_MathEmbedInTemplateBlock(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "items.json",
		[]byte(`[{"price":42.5,"qty":3},{"price":10,"qty":5}]`),
		"application/json")
	rctx := newResolveContext(store)

	text := `«««template_liquid: data="items.json"
{% for item in items %}Total: «math:{{ item.price }} * {{ item.qty }}»
{% endfor %}»»»`

	result, _, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "127.5") {
		t.Errorf("expected 42.5*3=127.5 in result, got %q", result)
	}
	if !strings.Contains(result, "50") {
		t.Errorf("expected 10*5=50 in result, got %q", result)
	}
}

func TestResolve_MathEmbedWithFormatInTemplateBlock(t *testing.T) {
	store := newMemArtifactStore()
	saveArtifact(store, "data.json",
		[]byte(`[{"price":42.567}]`),
		"application/json")
	rctx := newResolveContext(store)

	text := `«««template_liquid: data="data.json"
{% for item in items %}Price: «math:{{ item.price }} * 1.1 | .2f»
{% endfor %}»»»`

	result, _, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "46.82") {
		t.Errorf("expected formatted result 46.82, got %q", result)
	}
}

func TestResolve_MathEmbedSurvivorResolvedAtGateway(t *testing.T) {
	rctx := newResolveContext(newMemArtifactStore())

	text := "The answer is «math:6 * 7»."
	result, _, err := ResolveEmbedsInString(context.Background(), text, ModeA2AMessageToUser, rctx)
	if err != nil {
		t.Fatal(err)
	}
	if result != "The answer is 42." {
		t.Errorf("expected resolved math, got %q", result)
	}
}
