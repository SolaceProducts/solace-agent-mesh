// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func collectEvents(ch <-chan OutputEvent) []OutputEvent {
	var events []OutputEvent
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

func sendChunks(chunks ...llm.StreamChunk) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch
}

func combineText(events []OutputEvent) string {
	var sb strings.Builder
	for _, ev := range events {
		if ev.Type == EventText {
			sb.WriteString(ev.Text)
		}
	}
	return sb.String()
}

func TestStreamPipelineTextOnly(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "Hello "},
		llm.StreamChunk{ContentDelta: "World"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))
	text := strings.TrimRight(combineText(events), "\n")

	if text != "Hello World" {
		t.Errorf("combined text = %q", text)
	}
	if events[len(events)-1].Type != EventDone {
		t.Error("missing done event")
	}
}

func TestStreamPipelineToolCalls(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "Let me search."},
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ID: "call-1", Name: "search"},
			},
		},
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ArgumentsDelta: `{"query": "test"}`},
			},
			FinishReason: "tool_calls",
		},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var hasText, hasToolStart, hasDone bool
	for _, ev := range events {
		switch ev.Type {
		case EventText:
			hasText = true
		case EventToolStart:
			hasToolStart = true
			if ev.ToolName != "search" {
				t.Errorf("tool name = %q", ev.ToolName)
			}
			if ev.ToolCallID != "call-1" {
				t.Errorf("tool call ID = %q", ev.ToolCallID)
			}
		case EventDone:
			hasDone = true
		}
	}

	if !hasText {
		t.Error("missing text event")
	}
	if !hasToolStart {
		t.Error("missing tool start event")
	}
	if !hasDone {
		t.Error("missing done event")
	}
}

func TestStreamPipelineMultipleToolCalls(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ID: "c1", Name: "tool_a"},
				{Index: 1, ID: "c2", Name: "tool_b"},
			},
		},
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ArgumentsDelta: "{}"},
				{Index: 1, ArgumentsDelta: "{}"},
			},
			FinishReason: "tool_calls",
		},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	toolStarts := 0
	for _, ev := range events {
		if ev.Type == EventToolStart {
			toolStarts++
		}
	}
	if toolStarts != 2 {
		t.Errorf("expected 2 tool start events, got %d", toolStarts)
	}
}

func TestStreamPipelineError(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "partial"},
		llm.StreamChunk{Err: fmt.Errorf("connection lost")},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var hasError bool
	for _, ev := range events {
		if ev.Type == EventError {
			hasError = true
			if ev.Err == nil {
				t.Error("error event has nil error")
			}
		}
	}
	if !hasError {
		t.Error("missing error event")
	}
}

func TestStreamPipelineContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	stream := make(chan llm.StreamChunk)
	out := runStreamingPipeline(ctx, stream, 1)
	cancel()
	_ = collectEvents(out)
}

func TestStreamPipelineEmptyStream(t *testing.T) {
	stream := sendChunks()
	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	if len(events) != 1 || events[0].Type != EventDone {
		t.Errorf("expected just done event, got %d events", len(events))
	}
}

func TestChunkAccumulatorToolCallDeltas(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ID: "call-1", Name: "add"},
			},
		},
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ArgumentsDelta: `{"a": 1, `},
			},
		},
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ArgumentsDelta: `"b": 2}`},
			},
			FinishReason: "tool_calls",
		},
	)

	var toolCall *llm.ToolCall
	for c := range chunkAccumulator(context.Background(), stream) {
		if c.toolCall != nil {
			toolCall = c.toolCall
		}
	}

	if toolCall == nil {
		t.Fatal("no tool call accumulated")
	}
	if toolCall.Name != "add" {
		t.Errorf("name = %q", toolCall.Name)
	}
	if toolCall.Arguments != `{"a": 1, "b": 2}` {
		t.Errorf("arguments = %q", toolCall.Arguments)
	}
}

func TestEmbedTokenizerPlainText(t *testing.T) {
	in := make(chan accumulatedChunk, 3)
	in <- accumulatedChunk{text: "Hello "}
	in <- accumulatedChunk{text: "World"}
	close(in)

	var text string
	for tok := range embedTokenizer(context.Background(), in) {
		if tok.token != nil && tok.token.Type == TextToken {
			text += tok.token.Text
		}
	}

	if text != "Hello World" {
		t.Errorf("text = %q", text)
	}
}

func TestEmbedTokenizerEmbed(t *testing.T) {
	in := make(chan accumulatedChunk, 3)
	in <- accumulatedChunk{text: "Hello «artifact_content:file.csv» world"}
	close(in)

	var tokens []Token
	for tok := range embedTokenizer(context.Background(), in) {
		if tok.token != nil {
			tokens = append(tokens, *tok.token)
		}
	}

	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %+v", len(tokens), tokens)
	}
	if tokens[0].Type != TextToken || tokens[0].Text != "Hello " {
		t.Errorf("tokens[0] = %+v", tokens[0])
	}
	if tokens[1].Type != EmbedToken || tokens[1].Embed.Type != "artifact_content" {
		t.Errorf("tokens[1] = %+v", tokens[1])
	}
	if tokens[2].Type != TextToken || tokens[2].Text != " world" {
		t.Errorf("tokens[2] = %+v", tokens[2])
	}
}

func TestEmbedTokenizerSplitAcrossChunks(t *testing.T) {
	in := make(chan accumulatedChunk, 5)
	in <- accumulatedChunk{text: "Hello «art"}
	in <- accumulatedChunk{text: "ifact_content:file.csv» world"}
	close(in)

	var tokens []Token
	for tok := range embedTokenizer(context.Background(), in) {
		if tok.token != nil {
			tokens = append(tokens, *tok.token)
		}
	}

	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %+v", len(tokens), tokens)
	}
	if tokens[1].Type != EmbedToken {
		t.Errorf("expected embed token, got %+v", tokens[1])
	}
}

func TestEmbedTokenizerNoEmbed(t *testing.T) {
	in := make(chan accumulatedChunk, 3)
	in <- accumulatedChunk{text: "No embeds { here } at all"}
	close(in)

	var text string
	for tok := range embedTokenizer(context.Background(), in) {
		if tok.token != nil && tok.token.Type == TextToken {
			text += tok.token.Text
		}
	}

	if text != "No embeds { here } at all" {
		t.Errorf("text = %q", text)
	}
}

func TestEmbedTokenizerPassesThroughToolCalls(t *testing.T) {
	in := make(chan accumulatedChunk, 3)
	in <- accumulatedChunk{toolCall: &llm.ToolCall{ID: "c1", Name: "search"}}
	close(in)

	var gotToolCall bool
	for tok := range embedTokenizer(context.Background(), in) {
		if tok.toolCall != nil {
			gotToolCall = true
		}
	}

	if !gotToolCall {
		t.Error("tool call not passed through")
	}
}

func tokenizeText(chunks ...string) ([]Token, string) {
	in := make(chan accumulatedChunk, len(chunks)+1)
	for _, c := range chunks {
		in <- accumulatedChunk{text: c}
	}
	close(in)

	var tokens []Token
	var text string
	for tok := range embedTokenizer(context.Background(), in) {
		if tok.token != nil {
			tokens = append(tokens, *tok.token)
			if tok.token.Type == TextToken {
				text += tok.token.Text
			}
		}
	}
	return tokens, text
}

func TestEmbedTokenizerTripleGuillemetInlineEmbed(t *testing.T) {
	tokens, text := tokenizeText("Hello «««status_update:Updating the plan...»»» world")

	if strings.ContainsAny(text, "«»") {
		t.Errorf("rendered text leaked guillemets: %q", text)
	}
	if text != "Hello  world" {
		t.Errorf("rendered text = %q, want %q", text, "Hello  world")
	}

	var embeds []Token
	for _, tk := range tokens {
		if tk.Type == EmbedToken {
			embeds = append(embeds, tk)
		}
	}
	if len(embeds) != 1 {
		t.Fatalf("expected 1 embed token, got %d: %+v", len(embeds), tokens)
	}
	if embeds[0].Embed.Type != "status_update" {
		t.Errorf("embed type = %q, want status_update", embeds[0].Embed.Type)
	}
	if got := embeds[0].Embed.Params["value"]; got != "Updating the plan..." {
		t.Errorf("embed value = %q", got)
	}
}

func TestEmbedTokenizerTripleGuillemetSplitAcrossChunks(t *testing.T) {
	tokens, text := tokenizeText("Hi «««status_up", "date:msg»", "»» bye")

	if strings.ContainsAny(text, "«»") {
		t.Errorf("rendered text leaked guillemets: %q", text)
	}
	if text != "Hi  bye" {
		t.Errorf("rendered text = %q, want %q", text, "Hi  bye")
	}

	var sawStatus bool
	for _, tk := range tokens {
		if tk.Type == EmbedToken && tk.Embed.Type == "status_update" {
			sawStatus = true
		}
	}
	if !sawStatus {
		t.Fatalf("expected a status_update embed token, got %+v", tokens)
	}
}

func TestEmbedTokenizerTripleGuillemetBlockUnaffected(t *testing.T) {
	const input = "«««save_artifact:filename=\"x.txt\" mime_type=\"text/plain\"»»»"
	tokens, text := tokenizeText(input)

	if text != input {
		t.Errorf("block text changed:\n got %q\nwant %q", text, input)
	}
	for _, tk := range tokens {
		if tk.Type == EmbedToken {
			t.Errorf("fenced block must not produce an embed token: %+v", tk)
		}
	}
}

func TestEmbedTokenizerSingleGuillemetStillResolves(t *testing.T) {
	tokens, text := tokenizeText("a «status_update:hi» b")

	if text != "a  b" {
		t.Errorf("rendered text = %q, want %q", text, "a  b")
	}
	var sawStatus bool
	for _, tk := range tokens {
		if tk.Type == EmbedToken && tk.Embed.Type == "status_update" {
			sawStatus = true
		}
	}
	if !sawStatus {
		t.Fatalf("expected status_update embed token, got %+v", tokens)
	}
}

func TestEmbedTokenizerHelpers(t *testing.T) {
	if got := leadingRun("«««x", EmbedOpen); got != 3 {
		t.Errorf("leadingRun(«««) = %d, want 3", got)
	}
	if got := leadingRun("x«", EmbedOpen); got != 0 {
		t.Errorf("leadingRun(no leading) = %d, want 0", got)
	}
	if name, ok := embedTypeToken("status_update:msg"); !ok || name != "status_update" {
		t.Errorf("embedTypeToken = (%q,%v)", name, ok)
	}
	if _, ok := embedTypeToken("status_update"); ok {
		t.Error("embedTypeToken without colon should be incomplete")
	}
	if _, ok := embedTypeToken("not an ident:x"); ok {
		t.Error("embedTypeToken with non-identifier should be false")
	}
	if !isBlockType("save_artifact") || isBlockType("status_update") {
		t.Error("isBlockType classification wrong")
	}
}

func TestParseEmbed(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		wantTyp string
		early   bool
	}{
		{"simple", "«math:2+3»", false, "math", true},
		{"datetime", "«datetime:now»", false, "datetime", true},
		{"artifact", "«artifact_content:file.csv»", false, "artifact_content", false},
		{"empty", "«»", true, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := ParseEmbed(tc.input)
			if tc.wantNil {
				if ref != nil {
					t.Errorf("expected nil, got %+v", ref)
				}
				return
			}
			if ref == nil {
				t.Fatal("expected non-nil")
			}
			if ref.Type != tc.wantTyp {
				t.Errorf("type = %q, want %q", ref.Type, tc.wantTyp)
			}
			if ref.IsEarly != tc.early {
				t.Errorf("isEarly = %v, want %v", ref.IsEarly, tc.early)
			}
		})
	}
}

func TestParseStreamedEmbedRecoversOneStrayRuneBeforeABuiltinType(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantType  string
		wantValue string
		wantRaw   string
		wantEarly bool
	}{
		{"latin z caron", "«žstatus_update:Rendering the full table...»", "status_update", "Rendering the full table...", "«status_update:Rendering the full table...»", true},
		{"latin z dot", "«żstatus_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"latin i ogonek", "«įstatus_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"section sign", "«§status_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"latin s caron", "«šstatus_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"hangul syllable", "«렇status_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"ascii letter", "«fstatus_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"late type", "«öartifact_return:report.md:2»", "artifact_return", "report.md:2", "«artifact_return:report.md:2»", false},
		{"late type with a modifier chain", "«partifact_content:data.csv >>> format:json»", "artifact_content", "data.csv >>> format:json", "«artifact_content:data.csv >>> format:json»", false},
		{"result", "«xresult:artifact=a.json status=success»", "result", "artifact=a.json status=success", "«result:artifact=a.json status=success»", false},
		{"early type without a value", "«žuuid»", "uuid", "", "«uuid»", true},
		{"early type with an empty value", "«žmath:»", "math", "", "«math:»", true},
		{"digit", "«1uuid»", "uuid", "", "«uuid»", true},
		{"capitalised type", "«Status_update:Preparing table...»", "status_update", "Preparing table...", "«status_update:Preparing table...»", true},
		{"combining mark", "«\u0741status_update:Building the PDF report...»", "status_update", "Building the PDF report...", "«status_update:Building the PDF report...»", true},
		{"broken character", "«\ufffd\ufffd\ufffdstatus_update:Saving the report...»", "status_update", "Saving the report...", "«status_update:Saving the report...»", true},
		{"mark and letter", "«\u0741žstatus_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"stray word", "«überstatus_update:Building the deck...»", "status_update", "Building the deck...", "«status_update:Building the deck...»", true},
		{"stray word and opener", "«ions«status_update:Creating the HTML page for the PDF...»", "status_update", "Creating the HTML page for the PDF...", "«status_update:Creating the HTML page for the PDF...»", true},
		{"separator after a stray word", "«ided_status_update:Building the PDF report...»", "status_update", "Building the PDF report...", "«status_update:Building the PDF report...»", true},
		{"space after a stray word", "«et status_update:Writing the summary...»", "status_update", "Writing the summary...", "«status_update:Writing the summary...»", true},
		{"colon after a stray word", "«id:status_update:Saving the report HTML...»", "status_update", "Saving the report HTML...", "«status_update:Saving the report HTML...»", true},
		{"thai sign", "«๛status_update:Building the report...»", "status_update", "Building the report...", "«status_update:Building the report...»", true},
		{"stray word and capitalised type", "«VuStatus_update:Saving the report page...»", "status_update", "Saving the report page...", "«status_update:Saving the report page...»", true},
		{"short stray word and opener", "«et«status_update:Saving the slide spec...»", "status_update", "Saving the slide spec...", "«status_update:Saving the slide spec...»", true},
		{"real builtin is left alone", "«status_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"unknown type is left alone", "«žmy_custom:x»", "žmy_custom", "x", "«žmy_custom:x»", false},
		{"two stray runes", "«žžstatus_update:Working»", "status_update", "Working", "«status_update:Working»", true},
		{"two stray runes before another builtin are left alone", "«žžartifact_return:report.md»", "žžartifact_return", "report.md", "«žžartifact_return:report.md»", false},
		{"stray rune after the type is left alone", "«status_updatež:Working»", "status_updatež", "Working", "«status_updatež:Working»", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := parseStreamedEmbed(tc.input)
			if ref == nil {
				t.Fatal("parseStreamedEmbed returned nil")
			}
			if ref.Type != tc.wantType || ref.Params["value"] != tc.wantValue || ref.Raw != tc.wantRaw || ref.IsEarly != tc.wantEarly {
				t.Errorf("got type=%q value=%q raw=%q early=%v, want type=%q value=%q raw=%q early=%v",
					ref.Type, ref.Params["value"], ref.Raw, ref.IsEarly, tc.wantType, tc.wantValue, tc.wantRaw, tc.wantEarly)
			}
		})
	}
}

func TestParseStreamedEmbedLeavesALongPrefixBeforeStatusUpdateAlone(t *testing.T) {
	for _, input := range []string{
		"«a sentence that runs on before status_update:Working»",
		"«line one\nstatus_update:Working»",
	} {
		if ref := parseStreamedEmbed(input); ref != nil && ref.Type == "status_update" {
			t.Errorf("parseStreamedEmbed(%q) recovered a status_update, want it left as text", input)
		}
	}
}

func TestEmbedTokenizerRecoversAStrayWordAndOpenerSplitAcrossChunks(t *testing.T) {
	tokens, text := tokenizeText("«io", "ns«status_", "update:Creating the page...»\n\nDone.")
	if text != "\n\nDone." {
		t.Errorf("text = %q, want only the answer", text)
	}
	assertOneEmbed(t, tokens, "status_update", "Creating the page...")
}

func TestEmbedTokenizerDropsAStrayWordBeforeAStatusUpdate(t *testing.T) {
	cases := map[string][]string{
		"one chunk":           {"«llm»«status_update:Writing the report HTML...»\n\nDone."},
		"split after word":    {"«americ»", "«status_update:Saving the summary files...»\n\nDone."},
		"split inside opener": {"«americ»«stat", "us_update:Saving the summary files...»\n\nDone."},
		"split inside word":   {"«ame", "ric»«status_update:Saving the summary files...»\n\nDone."},
		"blank lines between": {"«készen»\n\n«status_update:Building the report...»\n\nDone."},
		"space between":       {"«once» «status_update:Saving the report HTML...»\n\nDone."},
		"split in the gap":    {"«ators»\n", "\n«stat", "us_update:Saving the report HTML...»\n\nDone."},
	}
	for name, chunks := range cases {
		t.Run(name, func(t *testing.T) {
			tokens, text := tokenizeText(chunks...)
			if text != "\n\nDone." {
				t.Errorf("text = %q, want only the answer", text)
			}
			if len(tokens) == 0 || tokens[0].Type != EmbedToken || tokens[0].Embed.Type != "status_update" {
				t.Errorf("first token = %+v, want the status_update embed", tokens)
			}
		})
	}
}

func TestEmbedTokenizerRecoversAMangledStatusUpdateType(t *testing.T) {
	cases := map[string][]string{
		"suffix status":     {"«table_status:Building the comparison table...»\n\nDone."},
		"accented type":     {"«ståtus_update:Creating the summary and PDF...»\n\nDone."},
		"split across type": {"«llm_sta", "tus:Saving the report...»\n\nDone."},
	}
	for name, chunks := range cases {
		t.Run(name, func(t *testing.T) {
			tokens, text := tokenizeText(chunks...)
			if text != "\n\nDone." {
				t.Errorf("text = %q, want only the answer", text)
			}
			if len(tokens) == 0 || tokens[0].Type != EmbedToken || tokens[0].Embed.Type != "status_update" {
				t.Errorf("first token = %+v, want the status_update embed", tokens)
			}
		})
	}
}

func TestEmbedTokenizerDropsAMangledStatusUpdateWithNoText(t *testing.T) {
	for _, input := range []string{"Done.\n\n«ator_status»", "Done.«llm_status»"} {
		tokens, text := tokenizeText(input)
		if strings.Contains(text, "«") || len(tokens) != 1 || tokens[0].Type != TextToken {
			t.Errorf("tokenizeText(%q) = %+v, want only the text before it", input, tokens)
		}
	}
}

func TestEmbedTokenizerKeepsQuotedTextThatOnlyMentionsStatus(t *testing.T) {
	cases := map[string]string{
		"Le «status quo» reste":       "«status quo»",
		"Set «order status» to open":  "«order status»",
		"The field «status» is empty": "«status»",
	}
	for input, quote := range cases {
		tokens, text := tokenizeText(input)
		shown := text
		for _, tk := range tokens {
			if tk.Type == EmbedToken && !isBuiltinEmbedType(tk.Embed.Type) {
				shown += tk.Embed.Raw
			}
		}
		if !strings.Contains(shown, quote) {
			t.Errorf("tokenizeText(%q) = %+v, want %q kept", input, tokens, quote)
		}
	}
}

func tokenizeBeforeToolCall(chunks ...string) ([]Token, string, bool) {
	in := make(chan accumulatedChunk, len(chunks)+1)
	for _, c := range chunks {
		in <- accumulatedChunk{text: c}
	}
	in <- accumulatedChunk{toolCall: &llm.ToolCall{Name: "query_data_with_sql"}}
	close(in)
	var tokens []Token
	var text string
	sawToolCall := false
	for tok := range embedTokenizer(context.Background(), in) {
		if tok.toolCall != nil {
			sawToolCall = true
		}
		if tok.token != nil {
			tokens = append(tokens, *tok.token)
			if tok.token.Type == TextToken {
				text += tok.token.Text
			}
		}
	}
	return tokens, text, sawToolCall
}

func TestEmbedTokenizerReadsATypelessQuoteBeforeAToolCallAsProgress(t *testing.T) {
	cases := map[string][]string{
		"sentence":            {"«I've queried the data. Next I'll write the report.»\n\n"},
		"split sentence":      {"«I've queried the da", "ta. Next I'll write the report.»"},
		"after a sentence":    {"Done with the query.«I've queried the data. Next I'll write the report.»"},
		"before a save block": {"«I've queried the data. Next I'll write the report.»\n\n«««save_artifact: filename=\"a.md\"\nhi\n»»»"},
	}
	for name, chunks := range cases {
		t.Run(name, func(t *testing.T) {
			tokens, text, sawToolCall := tokenizeBeforeToolCall(chunks...)
			if strings.Contains(text, "I've queried") || !sawToolCall {
				t.Errorf("text = %q, want the quote out of the chat", text)
			}
			var status string
			for _, tk := range tokens {
				if tk.Type == EmbedToken && tk.Embed.Type == "status_update" {
					status = tk.Embed.Params["value"]
				}
			}
			if status != "I've queried the data. Next I'll write the report." {
				t.Errorf("status = %q, want the quote as the progress line", status)
			}
		})
	}
}

func TestEmbedTokenizerReadsAnUnclosedLineBeforeASaveBlockAsProgress(t *testing.T) {
	block := "«««save_artifact: filename=\"spec.yaml\" mime_type=\"application/yaml\"\ntitle: Top suppliers\n»»»"
	cases := map[string][]string{
		"one chunk":             {"«I've queried the data. Next I'll write the files.\n\n" + block},
		"split before block":    {"«I've queried the data. Next I'll write the files.\n", "\n«", "««save_artifact: filename=\"spec.yaml\" mime_type=\"application/yaml\"\ntitle: Top suppliers\n»»»"},
		"split inside the line": {"«I've queried the da", "ta. Next I'll write the files.\n\n" + block},
		"status update between": {"«I've queried the data. Next I'll write the files.\n\n«status_update:Writing the files...»\n\n" + block},
	}
	for name, chunks := range cases {
		t.Run(name, func(t *testing.T) {
			tokens, text, _ := tokenizeBeforeToolCall(chunks...)
			if strings.Contains(text, "I've queried") {
				t.Errorf("text = %q, want the line out of the chat", text)
			}
			if trimmed := strings.TrimSpace(text); !strings.HasPrefix(trimmed, "«««save_artifact: filename=\"spec.yaml\"") || !strings.HasSuffix(trimmed, "»»»") {
				t.Errorf("text = %q, want the save block intact for the block parser", text)
			}
			if len(tokens) == 0 || tokens[0].Type != EmbedToken || tokens[0].Embed.Params["value"] != "I've queried the data. Next I'll write the files." {
				t.Errorf("first token = %+v, want the line as a progress update", tokens)
			}
		})
	}
}

func TestEmbedTokenizerKeepsAnUnclosedLineFollowedByProse(t *testing.T) {
	_, text := tokenizeText("«a note\nmore text that is not a block")
	if !strings.Contains(text, "«a note") {
		t.Errorf("text = %q, want the line kept", text)
	}
}

func TestEmbedTokenizerDropsAStrayWordBeforeAToolCall(t *testing.T) {
	for _, chunks := range [][]string{{"«ors»"}, {"«状态»\n\n"}, {"Next I'll build the deck.«ided»\n\n«««save_artifact: filename=\"a.yaml\"\nx\n»»»"}} {
		tokens, text, _ := tokenizeBeforeToolCall(chunks...)
		shown := text
		for _, tk := range tokens {
			if tk.Type == EmbedToken && !isBuiltinEmbedType(tk.Embed.Type) {
				shown += tk.Embed.Raw
			}
		}
		if strings.Contains(strings.ReplaceAll(strings.ReplaceAll(shown, tripleOpen, ""), "»»»", ""), "«") {
			t.Errorf("tokens for %q show %q, want the stray word dropped", chunks, shown)
		}
	}
}

func TestEmbedTokenizerKeepsAQuoteThatIsPartOfTheText(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		quote  string
		tool   bool
	}{
		{"prose follows", []string{"«does not exist yet» was the cause."}, "«does not exist yet»", true},
		{"mid sentence before a tool call", []string{"Searching for «Bonjour»"}, "«Bonjour»", true},
		{"end of the answer", []string{"Le mot est «Bonjour tout le monde»\n\n"}, "«Bonjour tout le monde»", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tokens []Token
			var text string
			if tc.tool {
				tokens, text, _ = tokenizeBeforeToolCall(tc.chunks...)
			} else {
				tokens, text = tokenizeText(tc.chunks...)
			}
			shown := text
			for _, tk := range tokens {
				if tk.Type == EmbedToken && !isBuiltinEmbedType(tk.Embed.Type) {
					shown += tk.Embed.Raw
				}
			}
			if !strings.Contains(shown, tc.quote) {
				t.Errorf("tokens %+v drop %q, want it kept", tokens, tc.quote)
			}
		})
	}
}

func TestEmbedTokenizerKeepsAQuotedWordThatNoStatusUpdateFollows(t *testing.T) {
	cases := []struct{ name, input, word string }{
		{"before prose", "«Bonjour» tout le monde", "«Bonjour»"},
		{"at the end", "Le mot «Bonjour»", "«Bonjour»"},
		{"before another builtin", "«americ»«datetime:%Y»", "«americ»"},
		{"before a blank line and prose", "«Bonjour»\n\nSuite du texte", "«Bonjour»"},
		{"before a blank line at the end", "Le mot «Bonjour»\n\n", "«Bonjour»"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, text := tokenizeText(tc.input)
			shown := text
			for _, tk := range tokens {
				if tk.Type == EmbedToken && !isBuiltinEmbedType(tk.Embed.Type) {
					shown += tk.Embed.Raw
				}
			}
			if !strings.Contains(shown, tc.word) {
				t.Errorf("tokens %+v drop %q, want it kept", tokens, tc.word)
			}
		})
	}
}

func TestParseEmbedKeepsAStrayRuneTypeAsWritten(t *testing.T) {
	for _, input := range []string{"«žstatus_update:Working»", "«xartifact_content:data.json»", "«_uuid»"} {
		ref := ParseEmbed(input)
		if ref == nil || ref.Raw != input || isBuiltinEmbedType(ref.Type) {
			t.Errorf("ParseEmbed(%q) = %+v, want the type and raw text as written", input, ref)
		}
	}
}

func TestEmbedTokenizerRecoversGarbledStatusUpdateSplitAcrossChunks(t *testing.T) {
	tokens, text := tokenizeText("«ž", "status_upd", "ate:Rendering the table...»\n\nHere is the answer.")
	if text != "\n\nHere is the answer." {
		t.Errorf("text = %q, want only the answer", text)
	}
	assertOneEmbed(t, tokens, "status_update", "Rendering the table...")
}

func TestEmbedTokenizerRecoversABrokenCharacterSplitAcrossChunks(t *testing.T) {
	tokens, text := tokenizeText("«\ufffd", "\ufffd\ufffdstatus_upd", "ate:Saving the report...»\n\nDone.")
	if text != "\n\nDone." {
		t.Errorf("text = %q, want only the answer", text)
	}
	assertOneEmbed(t, tokens, "status_update", "Saving the report...")
}

func TestEmbedTokenizerRecoversAStrayRuneInsideBlockDelimiters(t *testing.T) {
	for _, chunks := range [][]string{
		{"A «««žstatus_update:x»»» B"},
		{"A «««fstatus_update:x»»» B"},
		{"A «««\xc5", "\xbestatus_update:x»»» B"},
	} {
		tokens, text := tokenizeText(chunks...)
		if text != "A  B" {
			t.Errorf("%q: text = %q, want the embed removed", chunks, text)
		}
		assertOneEmbed(t, tokens, "status_update", "x")
	}
}

func TestEmbedTokenizerLeavesNonStrayShapesAlone(t *testing.T) {
	for _, chunks := range [][]string{
		{"A «««žmy_custom:x»»» B"},
		{"A «««ž", "my_custom:x»»» B"},
		{"«««žsave_artifact: filename=\"a\"\nhi\n»»»"},
		{"echo « «math:1+1»» done"},
		{"A «« math:1+1»» B"},
		{"A «««\nresult: x»»» B"},
		{"A «««:math:1+1»»» B"},
		{"so «(math: 2+2)» is"},
		{"so «`math:2+2`» is"},
		{"In PHP «$result» holds it"},
		{"see «#result: 5» here"},
		{"also «_uuid» and «*datetime:date*»"},
		{"so «“math: 2+2”» is"},
		{"say «„result: x“» there"},
		{"list «•result: ok» and «•uuid»"},
		{"¿«¿math» y «（math:2+2）»"},
		{"price «€datetime: date» and «—datetime: x»"},
		{"A «««\u00a0math:1+1»»» B"},
	} {
		tokens, _ := tokenizeText(chunks...)
		var spelled strings.Builder
		for _, tk := range tokens {
			if tk.Type != EmbedToken {
				spelled.WriteString(tk.Text)
				continue
			}
			spelled.WriteString(tk.Embed.Raw)
			if isBuiltinEmbedType(tk.Embed.Type) {
				t.Errorf("%q: unexpected builtin embed %+v", chunks, tk.Embed)
			}
		}
		if want := strings.Join(chunks, ""); spelled.String() != want {
			t.Errorf("%q: tokens spell %q, want the input unchanged", chunks, spelled.String())
		}
	}
}

func TestFencedBlockAfterARecoveredEmbedIsIntact(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "«žstatus_update:Saving»\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"a.txt\" mime_type=\"text/plain\"\n"},
		llm.StreamChunk{ContentDelta: "hi\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)
	var blocks []string
	for _, ev := range collectEvents(runStreamingPipeline(context.Background(), stream, 1)) {
		if ev.Type == EventBlockEnd {
			blocks = append(blocks, ev.Block.Filename+"="+ev.Block.Content)
		}
	}
	if !slices.Equal(blocks, []string{"a.txt=hi\n"}) {
		t.Errorf("blocks = %q, want the one saved block", blocks)
	}
}

func TestFencedBlockBodyCarriesTheRecoveredEmbed(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"notes.md\" mime_type=\"text/markdown\"\n"},
		llm.StreamChunk{ContentDelta: "id: «žuuid»\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)
	var content []string
	for _, ev := range collectEvents(runStreamingPipeline(context.Background(), stream, 1)) {
		if ev.Type == EventBlockEnd {
			content = append(content, ev.Block.Content)
		}
	}
	if !slices.Equal(content, []string{"id: «uuid»\n"}) {
		t.Errorf("block contents = %q, want one block carrying the canonical embed", content)
	}
}

func TestFencedBlockBodyKeepsPunctuatedEmbedLikeText(t *testing.T) {
	const body = "Syntax: «“math: 2+2”» and «•result: 5»\n"
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"notes.md\" mime_type=\"text/markdown\"\n"},
		llm.StreamChunk{ContentDelta: body},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)
	var content []string
	for _, ev := range collectEvents(runStreamingPipeline(context.Background(), stream, 1)) {
		if ev.Type == EventBlockEnd {
			content = append(content, ev.Block.Content)
		}
	}
	if !slices.Equal(content, []string{body}) {
		t.Errorf("block contents = %q, want the body unchanged", content)
	}
}

func TestFencedBlockHeaderCarriesTheRecoveredEmbed(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"log-«žuuid».txt\" mime_type=\"text/plain\"\n"},
		llm.StreamChunk{ContentDelta: "hi\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)
	var filenames []string
	for _, ev := range collectEvents(runStreamingPipeline(context.Background(), stream, 1)) {
		if ev.Type == EventBlockStart {
			filenames = append(filenames, ev.Block.Filename)
		}
	}
	if !slices.Equal(filenames, []string{"log-«uuid».txt"}) {
		t.Errorf("filenames = %q, want the canonical embed for save-time resolution", filenames)
	}
}

func assertOneEmbed(t *testing.T, tokens []Token, wantType, wantValue string) {
	t.Helper()
	var embeds []*EmbedRef
	for _, tk := range tokens {
		if tk.Type == EmbedToken {
			embeds = append(embeds, tk.Embed)
		}
	}
	if len(embeds) != 1 || embeds[0].Type != wantType || embeds[0].Params["value"] != wantValue {
		t.Fatalf("embeds = %+v, want one %s carrying %q", embeds, wantType, wantValue)
	}
}

func TestFencedBlockParserArtifact(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "Here is a file:\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"test.csv\" mime_type=\"text/csv\"\n"},
		llm.StreamChunk{ContentDelta: "a,b,c\n"},
		llm.StreamChunk{ContentDelta: "1,2,3\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: "Done!"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var hasBlockStart, hasBlockEnd, hasBlockContent bool
	for _, ev := range events {
		switch ev.Type {
		case EventBlockStart:
			hasBlockStart = true
			if ev.Block.Filename != "test.csv" {
				t.Errorf("filename = %q", ev.Block.Filename)
			}
			if ev.Block.MIMEType != "text/csv" {
				t.Errorf("mime_type = %q", ev.Block.MIMEType)
			}
		case EventBlockContent:
			hasBlockContent = true
		case EventBlockEnd:
			hasBlockEnd = true
			if !strings.Contains(ev.Block.Content, "a,b,c") {
				t.Errorf("block content = %q", ev.Block.Content)
			}
		}
	}

	if !hasBlockStart {
		t.Error("missing block start event")
	}
	if !hasBlockContent {
		t.Error("missing block content event")
	}
	if !hasBlockEnd {
		t.Error("missing block end event")
	}
}

func TestFencedBlockParserTemplate(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixTemplateLiquid + "\n"},
		llm.StreamChunk{ContentDelta: "{{ user.name }}\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var foundTemplate bool
	for _, ev := range events {
		if ev.Type == EventBlockStart && ev.Block.Type == FencedBlockTemplate {
			foundTemplate = true
		}
	}
	if !foundTemplate {
		t.Error("missing template block start")
	}
}

func TestFencedBlockParserRegularCodeBlock(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "```python\n"},
		llm.StreamChunk{ContentDelta: "print('hello')\n"},
		llm.StreamChunk{ContentDelta: "```\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	for _, ev := range events {
		if ev.Type == EventBlockStart {
			t.Error("regular code block should not trigger block start")
		}
	}

	text := combineText(events)
	if !strings.Contains(text, "python") {
		t.Error("code block text should pass through")
	}
}

func TestFencedBlockParserLegacyBackticksDontTrigger(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "```artifact filename=\"test.csv\"\n"},
		llm.StreamChunk{ContentDelta: "data\n"},
		llm.StreamChunk{ContentDelta: "```\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	for _, ev := range events {
		if ev.Type == EventBlockStart {
			t.Error("backtick artifact syntax should not trigger block start")
		}
	}
}

func TestFencedBlockParserToleratesOpenerVariants(t *testing.T) {
	for name, opener := range map[string]string{
		"space after delimiter": BlockDelimiterOpen + " save_artifact: filename=\"r.json\"",
		"indented":              "  " + BlockPrefixSaveArtifact + " filename=\"r.json\"",
	} {
		t.Run(name, func(t *testing.T) {
			stream := sendChunks(
				llm.StreamChunk{ContentDelta: opener + "\n"},
				llm.StreamChunk{ContentDelta: "{\"ok\": true}\n"},
				llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
				llm.StreamChunk{FinishReason: "stop"},
			)

			events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

			end := blockEnd(events)
			if end == nil || end.Filename != "r.json" || end.Content != "{\"ok\": true}\n" {
				t.Fatalf("block = %+v, want r.json saved with its content", end)
			}
			if text := combineText(events); strings.Contains(text, "save_artifact") {
				t.Errorf("block syntax leaked into the text: %q", text)
			}
		})
	}
}

func TestFencedBlockParserDropsCodeFenceAroundBlock(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "Saved it.\n```json\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"r.json\"\n"},
		llm.StreamChunk{ContentDelta: "{}\n" + BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: "```"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	if end := blockEnd(events); end == nil || end.Content != "{}\n" {
		t.Fatalf("block = %+v, want the fenced block saved", end)
	}
	if text := combineText(events); text != "Saved it.\n" {
		t.Errorf("text = %q, want the code fence around the block dropped", text)
	}
}

func TestFencedBlockParserKeepsRegularCodeFences(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "```python\nprint(1)\n```\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"a.txt\"\nx\n" + BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: "```\nplain\n```\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	if blockEnd(events) == nil {
		t.Fatal("missing block")
	}
	if text, want := combineText(events), "```python\nprint(1)\n```\n```\nplain\n```\n"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func blockEnd(events []OutputEvent) *FencedBlockEvent {
	for _, ev := range events {
		if ev.Type == EventBlockEnd {
			return ev.Block
		}
	}
	return nil
}

func TestFencedBlockParserArtifactWithoutParams(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + "\n"},
		llm.StreamChunk{ContentDelta: "some content\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var hasBlockStart, hasBlockEnd bool
	for _, ev := range events {
		if ev.Type == EventBlockStart {
			hasBlockStart = true
		}
		if ev.Type == EventBlockEnd {
			hasBlockEnd = true
		}
	}
	if !hasBlockStart {
		t.Error("artifact without filename should still trigger block start (validation deferred to save-time)")
	}
	if !hasBlockEnd {
		t.Error("expected block end event")
	}
}

func TestFencedBlockParserNestedTemplate(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"report.md\" mime_type=\"text/markdown\"\n"},
		llm.StreamChunk{ContentDelta: "# Report\n"},
		llm.StreamChunk{ContentDelta: "## Data Table\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixTemplateLiquid + " data=\"data.csv\"\n"},
		llm.StreamChunk{ContentDelta: "| {% for h in headers %}{{ h }} | {% endfor %}\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: "## End\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var blockStarts, blockEnds int
	var artifactContent string
	for _, ev := range events {
		switch ev.Type {
		case EventBlockStart:
			blockStarts++
			if ev.Block.Filename != "report.md" {
				t.Errorf("expected filename report.md, got %q", ev.Block.Filename)
			}
		case EventBlockEnd:
			blockEnds++
			artifactContent = ev.Block.Content
		}
	}

	if blockStarts != 1 {
		t.Errorf("expected 1 block start, got %d", blockStarts)
	}
	if blockEnds != 1 {
		t.Errorf("expected 1 block end, got %d", blockEnds)
	}

	if !strings.Contains(artifactContent, BlockPrefixTemplateLiquid) {
		t.Error("artifact content should contain the template opener literally")
	}
	if !strings.Contains(artifactContent, "{% for h in headers %}") {
		t.Error("artifact content should contain the template body literally")
	}
	if !strings.Contains(artifactContent, BlockDelimiterClose) {
		t.Error("artifact content should contain the inner closing delimiter literally")
	}
	if !strings.Contains(artifactContent, "## End") {
		t.Error("artifact content should contain text after the nested template")
	}
}

func TestFencedBlockParserDoubleNestedTemplate(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"tables.md\" mime_type=\"text/markdown\"\n"},
		llm.StreamChunk{ContentDelta: "# Table 1\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixTemplateLiquid + " data=\"data1.csv\"\n"},
		llm.StreamChunk{ContentDelta: "row1\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: "# Table 2\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixTemplateLiquid + " data=\"data2.csv\"\n"},
		llm.StreamChunk{ContentDelta: "row2\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var blockStarts, blockEnds int
	var artifactContent string
	for _, ev := range events {
		switch ev.Type {
		case EventBlockStart:
			blockStarts++
		case EventBlockEnd:
			blockEnds++
			artifactContent = ev.Block.Content
		}
	}

	if blockStarts != 1 {
		t.Errorf("expected 1 block start, got %d", blockStarts)
	}
	if blockEnds != 1 {
		t.Errorf("expected 1 block end, got %d", blockEnds)
	}

	if strings.Count(artifactContent, "data1.csv") != 1 {
		t.Error("should contain data1.csv reference")
	}
	if strings.Count(artifactContent, "data2.csv") != 1 {
		t.Error("should contain data2.csv reference")
	}
	if strings.Count(artifactContent, BlockDelimiterClose) != 2 {
		t.Errorf("should have 2 inner closing delimiters, got %d",
			strings.Count(artifactContent, BlockDelimiterClose))
	}
}

func TestFencedBlockParserIndentedNestedOpener(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"agent.yaml\" mime_type=\"application/yaml\"\n"},
		llm.StreamChunk{ContentDelta: "apps:\n"},
		llm.StreamChunk{ContentDelta: "  instruction: |\n"},
		llm.StreamChunk{ContentDelta: "    Example:\n"},
		llm.StreamChunk{ContentDelta: "    " + BlockPrefixSaveArtifact + " filename=\"output.html\"\n"},
		llm.StreamChunk{ContentDelta: "    <html>content</html>\n"},
		llm.StreamChunk{ContentDelta: "    " + BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: "  tools:\n"},
		llm.StreamChunk{ContentDelta: "    - web_request\n"},
		llm.StreamChunk{ContentDelta: "  agent_card:\n"},
		llm.StreamChunk{ContentDelta: "    description: test\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var blockEnds int
	var artifactContent string
	for _, ev := range events {
		if ev.Type == EventBlockEnd {
			blockEnds++
			artifactContent = ev.Block.Content
		}
	}

	if blockEnds != 1 {
		t.Errorf("expected 1 block end, got %d", blockEnds)
	}

	if !strings.Contains(artifactContent, "agent_card:") {
		t.Errorf("artifact content should contain agent_card after nested block close, got:\n%s", artifactContent)
	}
	if !strings.Contains(artifactContent, "tools:") {
		t.Errorf("artifact content should contain tools section, got:\n%s", artifactContent)
	}
	if !strings.Contains(artifactContent, BlockPrefixSaveArtifact) {
		t.Error("artifact content should contain nested save_artifact opener")
	}
	if !strings.Contains(artifactContent, BlockDelimiterClose) {
		t.Error("artifact content should contain inner closing delimiter")
	}
}

func TestFencedBlockParserEmbedInsideArtifact(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"page.html\" mime_type=\"text/html\"\n"},
		llm.StreamChunk{ContentDelta: "<h1>Report</h1>\n"},
		llm.StreamChunk{ContentDelta: "<img src=\"" + EmbedOpen + "artifact_content:chart.png >>> format:datauri" + EmbedClose + "\">\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var hasEmbed bool
	var artifactContent string
	for _, ev := range events {
		if ev.Type == EventEmbed {
			hasEmbed = true
		}
		if ev.Type == EventBlockEnd {
			artifactContent = ev.Block.Content
		}
	}

	if hasEmbed {
		t.Error("embed inside artifact should NOT produce an EventEmbed")
	}

	if !strings.Contains(artifactContent, "artifact_content:chart.png") {
		t.Errorf("artifact content should contain embed raw text, got: %q", artifactContent)
	}
}

func TestFencedBlockParserInlineClosingDelimiter(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"table.md\" mime_type=\"text/markdown\"\n"},
		llm.StreamChunk{ContentDelta: "# Table\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixTemplateLiquid + " data=\"data.csv\"\n"},
		llm.StreamChunk{ContentDelta: "| row |\n"},
		llm.StreamChunk{ContentDelta: "{% endfor %}" + BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: "Footer\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var blockStarts, blockEnds int
	var artifactContent string
	for _, ev := range events {
		switch ev.Type {
		case EventBlockStart:
			blockStarts++
		case EventBlockEnd:
			blockEnds++
			artifactContent = ev.Block.Content
		}
	}

	if blockStarts != 1 {
		t.Errorf("expected 1 block start, got %d", blockStarts)
	}
	if blockEnds != 1 {
		t.Errorf("expected 1 block end, got %d", blockEnds)
	}

	if !strings.Contains(artifactContent, BlockPrefixTemplateLiquid) {
		t.Error("artifact content should contain the template opener")
	}
	if !strings.Contains(artifactContent, "{% endfor %}"+BlockDelimiterClose) {
		t.Error("artifact content should contain the inline closing delimiter")
	}
	if !strings.Contains(artifactContent, "Footer") {
		t.Error("artifact content should contain text after the nested template")
	}
}

func TestFencedBlockParserInlineCloserBeforeTrailingWhitespace(t *testing.T) {
	for name, ending := range map[string]string{"space": " \n", "crlf": "\r\n", "space at end of stream": " "} {
		t.Run(name, func(t *testing.T) {
			stream := sendChunks(
				llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"note.txt\"\n"},
				llm.StreamChunk{ContentDelta: "ok" + BlockDelimiterClose + ending},
				llm.StreamChunk{FinishReason: "stop"},
			)

			var content string
			for _, ev := range collectEvents(runStreamingPipeline(context.Background(), stream, 1)) {
				if ev.Type == EventBlockEnd {
					content = ev.Block.Content
				}
			}
			if strings.TrimSpace(content) != "ok" {
				t.Errorf("content = %q, want just the text before the closer", content)
			}
		})
	}
}

func TestFencedBlockParserInlineCloserOuterBlock(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixTemplateLiquid + " data=\"data.csv\"\n"},
		llm.StreamChunk{ContentDelta: "| row |\n"},
		llm.StreamChunk{ContentDelta: "{% endfor %}" + BlockDelimiterClose + "\n"},
		llm.StreamChunk{ContentDelta: "after text\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var blockEnd *OutputEvent
	var afterText string
	for i := range events {
		if events[i].Type == EventBlockEnd {
			blockEnd = &events[i]
		}
		if events[i].Type == EventText {
			afterText += events[i].Text
		}
	}

	if blockEnd == nil {
		t.Fatal("missing block end event")
	}
	if !strings.Contains(blockEnd.Block.Content, "| row |") {
		t.Error("block content should include table row")
	}
	if !strings.Contains(blockEnd.Block.Content, "{% endfor %}") {
		t.Error("block content should include preceding text before inline closer")
	}
	if strings.Contains(blockEnd.Block.Content, BlockDelimiterClose) {
		t.Error("block content should NOT include the closing delimiter itself")
	}
	if !strings.Contains(afterText, "after text") {
		t.Error("text after inline-closed block should pass through as text")
	}
}

func TestOutputBatcherBatching(t *testing.T) {
	in := make(chan fencedOutput, 10)
	in <- fencedOutput{outputType: fencedOutputText, text: "ab"}
	in <- fencedOutput{outputType: fencedOutputText, text: "cd"}
	in <- fencedOutput{outputType: fencedOutputText, text: "ef"}
	in <- fencedOutput{outputType: fencedOutputText, text: "ghijklmnop"}
	close(in)

	batcher := newOutputBatcher(10)
	events := collectEvents(batcher.run(context.Background(), in))

	totalText := combineText(events)
	if totalText != "abcdefghijklmnop" {
		t.Errorf("total text = %q", totalText)
	}
}

func TestOutputBatcherFlushesOnToolCall(t *testing.T) {
	in := make(chan fencedOutput, 10)
	in <- fencedOutput{outputType: fencedOutputText, text: "partial"}
	in <- fencedOutput{outputType: fencedOutputToolCall, toolCall: &llm.ToolCall{ID: "c1", Name: "search"}}
	close(in)

	batcher := newOutputBatcher(100)
	events := collectEvents(batcher.run(context.Background(), in))

	if len(events) < 3 {
		t.Fatalf("expected at least 3 events (text, tool, done), got %d", len(events))
	}
	if events[0].Type != EventText || events[0].Text != "partial" {
		t.Errorf("events[0] = %+v", events[0])
	}
	if events[1].Type != EventToolStart {
		t.Errorf("events[1] = %+v", events[1])
	}
}

func TestFencedBlockIncompleteArtifact(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "Hello\n"},
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"data.csv\"\n"},
		llm.StreamChunk{ContentDelta: "col1,col2\n"},
		llm.StreamChunk{ContentDelta: "a,b\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var blockEnd *OutputEvent
	for i := range events {
		if events[i].Type == EventBlockEnd {
			blockEnd = &events[i]
			break
		}
	}
	if blockEnd == nil {
		t.Fatal("expected EventBlockEnd for unclosed block")
	}
	if !blockEnd.Block.Incomplete {
		t.Error("expected Incomplete=true for block without closing delimiter")
	}
	if blockEnd.Block.Filename != "data.csv" {
		t.Errorf("expected filename 'data.csv', got %q", blockEnd.Block.Filename)
	}
}

func TestFencedBlockIncompleteTemplate(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixTemplateLiquid + " data=\"items.json\"\n"},
		llm.StreamChunk{ContentDelta: "{% for item in items %}{{ item }}{% endfor %}\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var blockEnd *OutputEvent
	for i := range events {
		if events[i].Type == EventBlockEnd {
			blockEnd = &events[i]
			break
		}
	}
	if blockEnd == nil {
		t.Fatal("expected EventBlockEnd for unclosed template block")
	}
	if !blockEnd.Block.Incomplete {
		t.Error("expected Incomplete=true for template block without closing delimiter")
	}
}

func TestFencedBlockCompleteNotIncomplete(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: BlockPrefixSaveArtifact + " filename=\"data.csv\"\n"},
		llm.StreamChunk{ContentDelta: "col1,col2\n"},
		llm.StreamChunk{ContentDelta: BlockDelimiterClose + "\n"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var blockEnd *OutputEvent
	for i := range events {
		if events[i].Type == EventBlockEnd {
			blockEnd = &events[i]
			break
		}
	}
	if blockEnd == nil {
		t.Fatal("expected EventBlockEnd for closed block")
	}
	if blockEnd.Block.Incomplete {
		t.Error("expected Incomplete=false for properly closed block")
	}
}

func TestFencedBlockTagParsing(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantTags     []string
		wantFilename string
	}{
		{
			name:         "single tag",
			input:        BlockPrefixSaveArtifact + ` filename="data.csv" tags="__working"` + "\nsome content\n" + BlockDelimiterClose + "\n",
			wantTags:     []string{"__working"},
			wantFilename: "data.csv",
		},
		{
			name:         "multiple tags",
			input:        BlockPrefixSaveArtifact + ` filename="photo.png" tags="__user_uploaded,custom,__working"` + "\ncontent\n" + BlockDelimiterClose + "\n",
			wantTags:     []string{"__user_uploaded", "custom", "__working"},
			wantFilename: "photo.png",
		},
		{
			name:         "tags with spaces",
			input:        BlockPrefixSaveArtifact + ` filename="f.txt" tags=" tag1 , tag2 "` + "\ncontent\n" + BlockDelimiterClose + "\n",
			wantTags:     []string{"tag1", "tag2"},
			wantFilename: "f.txt",
		},
		{
			name:         "empty tags",
			input:        BlockPrefixSaveArtifact + ` filename="f.txt" tags=""` + "\ncontent\n" + BlockDelimiterClose + "\n",
			wantTags:     nil,
			wantFilename: "f.txt",
		},
		{
			name:         "no tags parameter",
			input:        BlockPrefixSaveArtifact + ` filename="f.txt"` + "\ncontent\n" + BlockDelimiterClose + "\n",
			wantTags:     nil,
			wantFilename: "f.txt",
		},
		{
			name:         "tags with mime_type and description",
			input:        BlockPrefixSaveArtifact + ` filename="report.pdf" mime_type="application/pdf" description="Monthly report" tags="__working"` + "\ndata\n" + BlockDelimiterClose + "\n",
			wantTags:     []string{"__working"},
			wantFilename: "report.pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := sendChunks(
				llm.StreamChunk{ContentDelta: tt.input},
				llm.StreamChunk{FinishReason: "stop"},
			)

			events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

			var blockEnd *OutputEvent
			for i := range events {
				if events[i].Type == EventBlockEnd {
					blockEnd = &events[i]
					break
				}
			}
			if blockEnd == nil {
				t.Fatal("expected EventBlockEnd")
			}
			if blockEnd.Block == nil {
				t.Fatal("expected non-nil Block")
			}
			if blockEnd.Block.Filename != tt.wantFilename {
				t.Errorf("filename = %q, want %q", blockEnd.Block.Filename, tt.wantFilename)
			}

			if len(tt.wantTags) == 0 {
				if len(blockEnd.Block.Tags) != 0 {
					t.Errorf("expected no tags, got %v", blockEnd.Block.Tags)
				}
			} else {
				if len(blockEnd.Block.Tags) != len(tt.wantTags) {
					t.Fatalf("tags length = %d, want %d; got %v", len(blockEnd.Block.Tags), len(tt.wantTags), blockEnd.Block.Tags)
				}
				for i, want := range tt.wantTags {
					if blockEnd.Block.Tags[i] != want {
						t.Errorf("tag[%d] = %q, want %q", i, blockEnd.Block.Tags[i], want)
					}
				}
			}
		})
	}
}

func TestParseBlockParams_Tags(t *testing.T) {
	tests := []struct {
		name     string
		paramStr string
		wantTags []string
	}{
		{"single", `tags="__working"`, []string{"__working"}},
		{"multiple", `tags="a,b,c"`, []string{"a", "b", "c"}},
		{"with spaces", `tags=" a , b "`, []string{"a", "b"}},
		{"empty", `tags=""`, nil},
		{"with other params", `filename="f.txt" tags="__working" mime_type="text/plain"`, []string{"__working"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := &FencedBlockEvent{}
			parseBlockParams(tt.paramStr, block)
			if len(tt.wantTags) == 0 {
				if len(block.Tags) != 0 {
					t.Errorf("expected no tags, got %v", block.Tags)
				}
			} else {
				if len(block.Tags) != len(tt.wantTags) {
					t.Fatalf("tags = %v, want %v", block.Tags, tt.wantTags)
				}
				for i, want := range tt.wantTags {
					if block.Tags[i] != want {
						t.Errorf("tag[%d] = %q, want %q", i, block.Tags[i], want)
					}
				}
			}
		})
	}
}

func TestStreamPipelineReasoningThenText(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ReasoningDelta: "Let me think"},
		llm.StreamChunk{ReasoningDelta: " about this..."},
		llm.StreamChunk{ContentDelta: "The answer is 42."},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var thinkingTexts []string
	var contentText string
	var hasDone bool
	for _, ev := range events {
		switch ev.Type {
		case EventThinkingContent:
			thinkingTexts = append(thinkingTexts, ev.Text)
		case EventText:
			contentText += ev.Text
		case EventDone:
			hasDone = true
		}
	}

	if len(thinkingTexts) != 2 {
		t.Errorf("expected 2 thinking events, got %d", len(thinkingTexts))
	}
	if len(thinkingTexts) > 0 && thinkingTexts[0] != "Let me think" {
		t.Errorf("thinking[0] = %q", thinkingTexts[0])
	}
	if contentText != "The answer is 42." {
		t.Errorf("content = %q, want %q", contentText, "The answer is 42.")
	}
	if !hasDone {
		t.Error("missing done event")
	}
}

func TestStreamPipelineReasoningOnly(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ReasoningDelta: "Thinking..."},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var hasThinking, hasDone bool
	var hasText bool
	for _, ev := range events {
		switch ev.Type {
		case EventThinkingContent:
			hasThinking = true
		case EventText:
			hasText = true
		case EventDone:
			hasDone = true
		}
	}

	if !hasThinking {
		t.Error("expected thinking event")
	}
	if hasText {
		t.Error("expected no text event for reasoning-only response")
	}
	if !hasDone {
		t.Error("missing done event")
	}
}

func TestStreamPipelineReasoningWithToolCalls(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ReasoningDelta: "I should use a tool."},
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ID: "call-1", Name: "search"},
			},
		},
		llm.StreamChunk{
			ToolCallDeltas: []llm.ToolCallDelta{
				{Index: 0, ArgumentsDelta: `{"q":"test"}`},
			},
			FinishReason: "tool_calls",
		},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	var hasThinking, hasToolStart, hasDone bool
	for _, ev := range events {
		switch ev.Type {
		case EventThinkingContent:
			hasThinking = true
		case EventToolStart:
			hasToolStart = true
			if ev.ToolName != "search" {
				t.Errorf("tool name = %q, want search", ev.ToolName)
			}
		case EventDone:
			hasDone = true
		}
	}

	if !hasThinking {
		t.Error("expected thinking event")
	}
	if !hasToolStart {
		t.Error("expected tool start event")
	}
	if !hasDone {
		t.Error("missing done event")
	}
}

func TestStreamPipelineNoReasoning(t *testing.T) {
	stream := sendChunks(
		llm.StreamChunk{ContentDelta: "Hello world"},
		llm.StreamChunk{FinishReason: "stop"},
	)

	events := collectEvents(runStreamingPipeline(context.Background(), stream, 1))

	for _, ev := range events {
		if ev.Type == EventThinkingContent {
			t.Error("unexpected thinking event in non-reasoning response")
		}
	}
}

func recvEvent(t *testing.T, out <-chan OutputEvent) OutputEvent {
	t.Helper()
	select {
	case ev := <-out:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for batcher event")
		return OutputEvent{}
	}
}

func TestOutputBatcherFirstChunkImmediate(t *testing.T) {
	in := make(chan fencedOutput)
	batcher := newOutputBatcher(1024, 60_000)
	out := batcher.run(t.Context(), in)

	in <- fencedOutput{outputType: fencedOutputText, text: "hi"}

	if ev := recvEvent(t, out); ev.Type != EventText || ev.Text != "hi" {
		t.Fatalf("first chunk not flushed immediately: type=%d text=%q", ev.Type, ev.Text)
	}

	close(in)
	for range out {
	}
}

func TestOutputBatcherIntervalFlush(t *testing.T) {
	in := make(chan fencedOutput)
	batcher := newOutputBatcher(1024, 20)
	out := batcher.run(t.Context(), in)

	in <- fencedOutput{outputType: fencedOutputText, text: "first"}
	if ev := recvEvent(t, out); ev.Type != EventText || ev.Text != "first" {
		t.Fatalf("first flush: type=%d text=%q", ev.Type, ev.Text)
	}

	in <- fencedOutput{outputType: fencedOutputText, text: "second"}
	if ev := recvEvent(t, out); ev.Type != EventText || ev.Text != "second" {
		t.Fatalf("interval flush: type=%d text=%q", ev.Type, ev.Text)
	}

	close(in)
	for range out {
	}
}

func TestOutputBatcherSizeFlush(t *testing.T) {
	in := make(chan fencedOutput, 4)
	batcher := newOutputBatcher(10, 60_000)
	out := batcher.run(t.Context(), in)

	in <- fencedOutput{outputType: fencedOutputText, text: "x"}
	recvEvent(t, out)

	in <- fencedOutput{outputType: fencedOutputText, text: "0123456789ABC"}
	if ev := recvEvent(t, out); ev.Type != EventText || ev.Text != "0123456789ABC" {
		t.Fatalf("size flush: type=%d text=%q", ev.Type, ev.Text)
	}

	close(in)
	for range out {
	}
}

func TestOutputBatcherByteIdenticalReassembly(t *testing.T) {
	chunks := []string{"Lorem ", "ipsum ", "dolor ", "sit ", "amet, ", "consectetur ", "adipiscing ", "elit"}
	var want strings.Builder
	for _, c := range chunks {
		want.WriteString(c)
	}

	in := make(chan fencedOutput, len(chunks))
	for _, c := range chunks {
		in <- fencedOutput{outputType: fencedOutputText, text: c}
	}
	close(in)

	batcher := newOutputBatcher(8, 5)
	events := collectEvents(batcher.run(t.Context(), in))

	if got := combineText(events); got != want.String() {
		t.Errorf("reassembled text = %q, want %q", got, want.String())
	}
}

func TestOutputBatcherGoroutineExitsOnInputClose(t *testing.T) {
	in := make(chan fencedOutput)
	batcher := newOutputBatcher(1024, 10)
	out := batcher.run(t.Context(), in)

	close(in)

	done := make(chan struct{})
	go func() {
		for range out {
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("batcher goroutine did not exit after input close")
	}
}
