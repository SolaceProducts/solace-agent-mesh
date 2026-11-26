// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/liquidsafe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const (
	inlineTestUser    = "user-1"
	inlineTestSession = "sess-1"
	inlineTestPrefix  = "Here are the products:\n"
	inlineTestSSN     = "123-45-6789"
	publishedSentinel = "<<end-of-test-publishes>>"
	inlineFailedLog   = "inline template render failed"
	productsJSON      = `[{"name":"Widget"},{"name":"Gadget"}]`
	productsTemplate  = "{% for item in items %}- {{ item.name }}\n{% endfor %}"
	productsRows      = "- Widget\n- Gadget\n"
)

type templateBlockHarness struct {
	agent  *Agent
	arts   *artifact.ScopedService
	sp     *StatusPublisher
	mb     broker.Broker
	q      broker.Queue
	logBuf *bytes.Buffer
}

func newTemplateBlockHarness(t *testing.T, llmResponses ...*llm.ChatResponse) *templateBlockHarness {
	t.Helper()
	agent, _, _ := newTestAgent(llmResponses...)
	arts := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = arts
	logBuf := &bytes.Buffer{}
	agent.logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mb.Disconnect(context.WithoutCancel(t.Context())) })

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-template"
	q, err := mb.CreateQueue("template-inline-status", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := mb.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}
	sp := NewStatusPublisher(mb, StatusPublisherConfig{
		StatusTopic: statusTopic,
		TaskID:      "task-template",
		ContextID:   "ctx-1",
		AgentName:   agent.cfg.Name,
		ClientID:    "client-1",
		UserID:      inlineTestUser,
	}, slog.Default())

	return &templateBlockHarness{agent: agent, arts: arts, sp: sp, mb: mb, q: q, logBuf: logBuf}
}

func (h *templateBlockHarness) seed(t *testing.T, filename, content, mimeType string) {
	t.Helper()
	loc := h.arts.Resolve(h.agent.cfg.Name, inlineTestUser, inlineTestSession, filename)
	if _, err := h.arts.Inner.Save(t.Context(), loc, []byte(content), mimeType); err != nil {
		t.Fatalf("seed %s: %v", filename, err)
	}
}

func (h *templateBlockHarness) publishedParts(t *testing.T) []a2a.Part {
	t.Helper()
	h.sp.PublishText(t.Context(), publishedSentinel)
	var parts []a2a.Part
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		msg, err := h.mb.Receive(ctx, h.q)
		cancel()
		if err != nil {
			t.Fatalf("receive status event: %v", err)
		}
		var resp a2a.Response
		if err = json.Unmarshal(msg.Payload, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		resultBytes, err := json.Marshal(resp.Result)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		var event a2a.TaskStatusUpdateEvent
		if err = json.Unmarshal(resultBytes, &event); err != nil {
			t.Fatalf("unmarshal status event: %v", err)
		}
		if event.Status.Message == nil {
			continue
		}
		for _, p := range event.Status.Message.Parts {
			if p.Kind == "text" && p.Text == publishedSentinel {
				return parts
			}
			parts = append(parts, p)
		}
	}
}

func isTemplateBlockSignal(p a2a.Part) bool {
	dm, ok := p.Data.(map[string]any)
	return ok && p.Kind == "data" && dm["type"] == "template_block"
}

func countTemplateBlockSignals(parts []a2a.Part) int {
	n := 0
	for _, p := range parts {
		if isTemplateBlockSignal(p) {
			n++
		}
	}
	return n
}

func publishedText(parts []a2a.Part) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Kind == "text" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func TestHandleBlockEndTemplateAnswerText(t *testing.T) {
	const rawBlockHeader = BlockPrefixTemplateLiquid + ` data="products.json"`

	tests := []struct {
		name                string
		inline              bool
		callerDisplays      bool
		dataSource          string
		jsonPath            string
		template            string
		seed                func(t *testing.T, h *templateBlockHarness)
		maxReturnBytes      int
		wantBVE             bool
		wantBVEContains     string
		wantErrorKind       string
		wantVisible         string
		wantVisibleContains string
		wantTruncatedFor    string
		wantEmitted         bool
		wantTemplateSignals int
		wantTextContains    string
		wantTextExcludes    string
		wantRawSuffix       string
	}{
		{
			name:                "run-based caller receives the rendered rows as streamed text",
			inline:              true,
			dataSource:          "products.json",
			template:            productsTemplate,
			wantVisible:         inlineTestPrefix + productsRows,
			wantEmitted:         true,
			wantTemplateSignals: 0,
			wantTextContains:    productsRows,
		},
		{
			name:                "delegated caller's UI shows the rows from the forwarded signal and the answer says so",
			inline:              true,
			callerDisplays:      true,
			dataSource:          "products.json",
			template:            productsTemplate,
			wantVisible:         inlineTestPrefix + productsRows + templateShownToUserNote,
			wantEmitted:         true,
			wantTemplateSignals: 1,
			wantTextExcludes:    "Widget",
		},
		{
			name:       "unparseable data answers with the parse error instead of retrying",
			inline:     true,
			dataSource: "broken.json",
			template:   productsTemplate,
			seed: func(t *testing.T, h *templateBlockHarness) {
				h.seed(t, "broken.json", `{not json`, "application/json")
			},
			wantVisibleContains: "[Template Error: Data artifact 'broken.json' could not be parsed]",
			wantErrorKind:       "data_parse",
			wantEmitted:         true,
			wantTextContains:    "could not be parsed",
			wantTextExcludes:    "invalid character",
			wantRawSuffix:       "»»»\n[Template Error: Data artifact 'broken.json' could not be parsed]",
		},
		{
			name:            "jsonpath that matches nothing is fed back to the LLM for a retry",
			inline:          true,
			dataSource:      "products.json",
			jsonPath:        "$.missing",
			template:        productsTemplate,
			wantBVE:         true,
			wantBVEContains: "jsonpath",
			wantErrorKind:   "jsonpath",
			wantVisible:     inlineTestPrefix,
		},
		{
			name:                "interactive turn leaves rendering to the gateway signal",
			inline:              false,
			dataSource:          "products.json",
			template:            productsTemplate,
			wantVisible:         inlineTestPrefix,
			wantEmitted:         true,
			wantTemplateSignals: 1,
			wantTextExcludes:    "Widget",
		},
		{
			name:            "missing data artifact is fed back to the LLM for a retry",
			inline:          true,
			dataSource:      "absent.json",
			template:        productsTemplate,
			wantBVE:         true,
			wantBVEContains: "'absent.json'",
			wantErrorKind:   "not_found",
			wantVisible:     inlineTestPrefix,
		},
		{
			name:       "liquid conversion error never echoes the data value",
			inline:     true,
			dataSource: "people.json",
			template:   "{% for item in items %}{{ item.ssn | divided_by: 2 }}{% endfor %}",
			seed: func(t *testing.T, h *templateBlockHarness) {
				h.seed(t, "people.json", `[{"ssn":"`+inlineTestSSN+`"}]`, "application/json")
			},
			wantBVE:         true,
			wantBVEContains: "'people.json'",
			wantErrorKind:   "render",
			wantVisible:     inlineTestPrefix,
		},
		{
			name:       "oversized data renders the size-limit error instead of retrying",
			inline:     true,
			dataSource: "huge.txt",
			template:   "{{ text | size }}",
			seed: func(t *testing.T, h *templateBlockHarness) {
				h.seed(t, "huge.txt", strings.Repeat("x", MaxTemplateDataSize+1), "text/plain")
			},
			wantVisibleContains: "[Template Error: Data artifact 'huge.txt' exceeds size limit",
			wantErrorKind:       "too_large",
			wantEmitted:         true,
			wantTextContains:    "exceeds size limit",
			wantRawSuffix:       "»»»\n[Template Error: Data artifact 'huge.txt' exceeds size limit (2000000 bytes)]",
		},
		{
			name:             "rendered output over the return ceiling is capped with a marker",
			inline:           true,
			dataSource:       "products.json",
			template:         "{% for i in (1..50) %}{% for item in items %}{{ item.name }} {% endfor %}{% endfor %}",
			maxReturnBytes:   64,
			wantTruncatedFor: "products.json",
			wantEmitted:      true,
			wantTextContains: "output truncated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTemplateBlockHarness(t)
			h.seed(t, "products.json", productsJSON, "application/json")
			if tt.seed != nil {
				tt.seed(t, h)
			}
			h.agent.cfg.ToolOutputLLMReturnMaxBytes = tt.maxReturnBytes

			var visible, raw strings.Builder
			visible.WriteString(inlineTestPrefix)
			raw.WriteString(inlineTestPrefix)
			st := newInlineTestPipelineState(&visible, &raw, &streamCallInfo{
				UserID:                     inlineTestUser,
				SessionID:                  inlineTestSession,
				RenderTemplatesInline:      tt.inline,
				CallerDisplaysTemplates:    tt.callerDisplays,
				BlockValidationRetriesLeft: maxBlockValidationRetries,
			})
			block := &FencedBlockEvent{Type: FencedBlockTemplate, DataSource: tt.dataSource, JSONPath: tt.jsonPath, Limit: -1, Content: tt.template}

			bve := h.agent.handleBlockEnd(t.Context(), h.sp, block, st)

			switch {
			case tt.wantBVE && bve == nil:
				t.Fatal("expected a block validation error, got nil")
			case !tt.wantBVE && bve != nil:
				t.Fatalf("unexpected block validation error: %s", bve.ErrorMsg)
			case bve != nil:
				if !strings.Contains(bve.ErrorMsg, tt.wantBVEContains) || !strings.HasSuffix(bve.ErrorMsg, templateRetryInstruction) {
					t.Errorf("ErrorMsg = %q, want it to contain %q and end with %q", bve.ErrorMsg, tt.wantBVEContains, templateRetryInstruction)
				}
				if !strings.HasSuffix(bve.RawText, BlockDelimiterClose+"\n") {
					t.Errorf("RawText = %q, want it to end with the failing raw block", bve.RawText)
				}
				if strings.Contains(bve.ErrorMsg, inlineTestSSN) {
					t.Errorf("ErrorMsg leaks the data value: %q", bve.ErrorMsg)
				}
				if bve.BlockType != FencedBlockTemplate {
					t.Errorf("BlockType = %v, want FencedBlockTemplate", bve.BlockType)
				}
				if bve.ErrorKind != tt.wantErrorKind {
					t.Errorf("ErrorKind = %q, want %q", bve.ErrorKind, tt.wantErrorKind)
				}
				if bve.Filename != "" {
					t.Errorf("Filename = %q, want empty so the retry feedback does not report a failed artifact save", bve.Filename)
				}
				if strings.Contains(h.logBuf.String(), inlineFailedLog) {
					t.Errorf("agent log = %q, want no %q line: the retry path logs the failure once", h.logBuf.String(), inlineFailedLog)
				}
			}

			gotVisible := visible.String()
			if tt.wantVisible != "" && gotVisible != tt.wantVisible {
				t.Errorf("visibleText = %q, want %q", gotVisible, tt.wantVisible)
			}
			if tt.wantVisibleContains != "" && !strings.Contains(gotVisible, tt.wantVisibleContains) {
				t.Errorf("visibleText = %q, want it to contain %q", gotVisible, tt.wantVisibleContains)
			}
			if tt.wantTruncatedFor != "" {
				marker := fmt.Sprintf(templateInlineTruncatedFormat, tt.wantTruncatedFor)
				if !strings.HasSuffix(gotVisible, marker) {
					t.Errorf("visibleText = %q, want truncation marker %q", gotVisible, marker)
				}
				if maxLen := len(inlineTestPrefix) + tt.maxReturnBytes + len(marker); len(gotVisible) > maxLen {
					t.Errorf("visibleText length = %d, want <= %d", len(gotVisible), maxLen)
				}
			}

			if tt.dataSource == "products.json" && !strings.Contains(raw.String(), rawBlockHeader) {
				t.Errorf("rawText = %q, want it to keep the raw block %q", raw.String(), rawBlockHeader)
			}
			if tt.wantRawSuffix != "" && !strings.HasSuffix(raw.String(), tt.wantRawSuffix) {
				t.Errorf("rawText = %q, want the raw block followed by the shown error %q", raw.String(), tt.wantRawSuffix)
			}
			if *st.templateEmitted != tt.wantEmitted {
				t.Errorf("templateEmitted = %v, want %v", *st.templateEmitted, tt.wantEmitted)
			}
			if !tt.wantBVE && tt.wantErrorKind != "" && !strings.Contains(h.logBuf.String(), "errorKind="+tt.wantErrorKind) {
				t.Errorf("agent log = %q, want errorKind=%s", h.logBuf.String(), tt.wantErrorKind)
			}
			if strings.Contains(h.logBuf.String(), inlineTestSSN) {
				t.Errorf("agent log leaks the data value: %s", h.logBuf.String())
			}

			parts := h.publishedParts(t)
			if got := countTemplateBlockSignals(parts); got != tt.wantTemplateSignals {
				t.Errorf("template_block signals = %d, want %d", got, tt.wantTemplateSignals)
			}
			text := publishedText(parts)
			if tt.wantBVE && text != "" {
				t.Errorf("published text = %q, want nothing from the failing block on the status stream", text)
			}
			if strings.Contains(text, inlineTestSSN) {
				t.Errorf("published text leaks the data value: %q", text)
			}
			if tt.wantTextContains != "" && !strings.Contains(text, tt.wantTextContains) {
				t.Errorf("published text = %q, want it to contain %q", text, tt.wantTextContains)
			}
			if tt.wantTextExcludes != "" && strings.Contains(text, tt.wantTextExcludes) {
				t.Errorf("published text = %q, must not contain %q", text, tt.wantTextExcludes)
			}
		})
	}
}

func parsedTemplateBlocks(t *testing.T, text string) []*FencedBlockEvent {
	t.Helper()
	stream := sendChunks(llm.StreamChunk{ContentDelta: text}, llm.StreamChunk{FinishReason: "stop"})
	var blocks []*FencedBlockEvent
	for ev := range runStreamingPipeline(t.Context(), stream, 1) {
		if ev.Type == EventBlockEnd && ev.Block != nil && ev.Block.Type == FencedBlockTemplate {
			blocks = append(blocks, ev.Block)
		}
	}
	return blocks
}

func TestRawTemplateBlock(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "data only",
			source: "«««template_liquid: data=\"rows.csv\"\n{{ headers }}\n»»»\n",
			want:   "«««template_liquid: data=\"rows.csv\"\n{{ headers }}\n»»»\n",
		},
		{
			name:   "data, jsonpath and limit over several lines",
			source: "«««template_liquid: data=\"rows.json:2\" jsonpath=\"$.items\" limit=\"5\"\n{% for i in items %}\n{{ i }}\n{% endfor %}\n»»»\n",
			want:   "«««template_liquid: data=\"rows.json:2\" jsonpath=\"$.items\" limit=\"5\"\n{% for i in items %}\n{{ i }}\n{% endfor %}\n»»»\n",
		},
		{
			name:   "closer on the last content line",
			source: "«««template_liquid: data=\"rows.csv\"\n{{ headers }}»»»\n",
			want:   "«««template_liquid: data=\"rows.csv\"\n{{ headers }}\n»»»\n",
		},
		{
			name:   "empty template",
			source: "«««template_liquid: data=\"rows.csv\"\n»»»\n",
			want:   "«««template_liquid: data=\"rows.csv\"\n»»»\n",
		},
		{
			name:   "jsonpath regex with backslashes",
			source: "«««template_liquid: data=\"rows.json\" jsonpath=\"$.items[?(@.sku =~ /^A\\d+/)]\"\n{{ items }}\n»»»\n",
			want:   "«««template_liquid: data=\"rows.json\" jsonpath=\"$.items[?(@.sku =~ /^A\\d+/)]\"\n{{ items }}\n»»»\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parsedTemplateBlocks(t, tt.source)
			if len(parsed) != 1 {
				t.Fatalf("parsed %d template blocks from %q, want 1", len(parsed), tt.source)
			}
			got := rawTemplateBlock(parsed[0])
			if got != tt.want {
				t.Errorf("rawTemplateBlock() = %q, want %q", got, tt.want)
			}

			reparsed := parsedTemplateBlocks(t, got)
			if len(reparsed) != 1 {
				t.Fatalf("reparsed %d template blocks from %q, want 1", len(reparsed), got)
			}
			orig, again := parsed[0], reparsed[0]
			if again.DataSource != orig.DataSource || again.JSONPath != orig.JSONPath || again.Limit != orig.Limit || again.Content != orig.Content {
				t.Errorf("round trip = %+v, want the original %+v", *again, *orig)
			}
		})
	}
}

func TestHandleBlockEndTemplateRetryKeepsTheRawBlock(t *testing.T) {
	const header = BlockPrefixTemplateLiquid + ` data="products.json"` + "\n"
	tests := []struct {
		name    string
		block   FencedBlockEvent
		wantRaw string
	}{
		{
			name:    "invalid Liquid syntax",
			block:   FencedBlockEvent{Type: FencedBlockTemplate, DataSource: "products.json", Limit: -1, Content: "{% for item in items %}\n"},
			wantRaw: inlineTestPrefix + header + "{% for item in items %}\n" + BlockDelimiterClose + "\n",
		},
		{
			name:    "block cut off before its closer",
			block:   FencedBlockEvent{Type: FencedBlockTemplate, DataSource: "products.json", Limit: -1, Content: "- {{ item.na", Incomplete: true},
			wantRaw: inlineTestPrefix + header + "- {{ item.na",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTemplateBlockHarness(t)
			h.seed(t, "products.json", productsJSON, "application/json")
			var visible, raw strings.Builder
			visible.WriteString(inlineTestPrefix)
			raw.WriteString(inlineTestPrefix)
			st := newInlineTestPipelineState(&visible, &raw, &streamCallInfo{UserID: inlineTestUser, SessionID: inlineTestSession, RenderTemplatesInline: true, BlockValidationRetriesLeft: maxBlockValidationRetries})

			bve := h.agent.handleBlockEnd(t.Context(), h.sp, &tt.block, st)
			if bve == nil {
				t.Fatal("expected a block validation error")
			}
			if bve.RawText != tt.wantRaw {
				t.Errorf("RawText = %q, want %q", bve.RawText, tt.wantRaw)
			}
		})
	}
}

func TestPrepareTemplateContextMIMETypeIsCaseInsensitive(t *testing.T) {
	ctx, err := PrepareTemplateContext(`[{"name":"Widget"},{"name":"Gadget"}]`, "Application/JSON", "", -1)
	if err != nil {
		t.Fatalf("PrepareTemplateContext: %v", err)
	}
	items, ok := ctx["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %#v, want the two parsed rows", ctx["items"])
	}
}

func seedTemplateData(t *testing.T, filename, content, mimeType string) *artifact.ScopedService {
	t.Helper()
	arts := &artifact.ScopedService{Inner: artmem.New()}
	loc := arts.Resolve("agent", inlineTestUser, inlineTestSession, filename)
	if _, err := arts.Inner.Save(t.Context(), loc, []byte(content), mimeType); err != nil {
		t.Fatal(err)
	}
	return arts
}

func renderSeededTemplate(t *testing.T, arts *artifact.ScopedService, filename, template string) (string, error) {
	t.Helper()
	return renderSeededTemplateWith(t, arts, filename, template, "", -1)
}

func renderSeededTemplateWith(t *testing.T, arts *artifact.ScopedService, filename, template, jsonPath string, limit int) (string, error) {
	t.Helper()
	renderer := NewTemplateRenderer(arts, "agent", inlineTestUser, inlineTestSession, slog.Default())
	return renderer.RenderBlock(t.Context(), &FencedBlockEvent{DataSource: filename, JSONPath: jsonPath, Limit: limit, Content: template})
}

const (
	rowsCSV       = "name,qty\nWidget,3\nGadget,5\n"
	raggedRowsCSV = "name,qty\nWidget,3,extra\nGadget,5\n"
)

func TestRenderBlockReadsCSVStoredAsPlainText(t *testing.T) {
	arts := seedTemplateData(t, "rows.csv", rowsCSV, "text/plain")

	shown, err := renderSeededTemplate(t, arts, "rows.csv", "{% for row in data_rows %}{{ row[0] }}={{ row[1] }};{% endfor %}")
	if err != nil {
		t.Fatalf("RenderBlock: %v", err)
	}
	if want := "Widget=3;Gadget=5;"; strings.TrimSpace(shown) != want {
		t.Errorf("rendered %q, want %q", shown, want)
	}
}

func TestRenderBlockPlainTextCSVKeepsTheTextContext(t *testing.T) {
	arts := seedTemplateData(t, "rows.csv", rowsCSV, "text/plain")

	shown, err := renderSeededTemplate(t, arts, "rows.csv", "{{ headers[0] }}|{{ text }}")
	if err != nil {
		t.Fatalf("RenderBlock: %v", err)
	}
	if want := "name|" + rowsCSV; strings.TrimSpace(shown) != strings.TrimSpace(want) {
		t.Errorf("rendered %q, want headers and the raw text together: %q", shown, want)
	}
}

func TestRenderBlockRaggedPlainTextCSVFallsBackToText(t *testing.T) {
	arts := seedTemplateData(t, "ragged.csv", raggedRowsCSV, "text/plain")

	shown, err := renderSeededTemplate(t, arts, "ragged.csv", "{{ text }}")
	if err != nil {
		t.Fatalf("RenderBlock: %v", err)
	}
	if strings.TrimSpace(shown) != strings.TrimSpace(raggedRowsCSV) {
		t.Errorf("rendered %q, want the raw text %q", shown, raggedRowsCSV)
	}
}

func TestRenderBlockPlainTextParseErrorSurfacesWhenTheTemplateReadsTheRows(t *testing.T) {
	const rowsLoop = "{% for row in data_rows %}{{ row[0] }};{% endfor %}"
	tests := []struct {
		name     string
		filename string
		content  string
		template string
	}{
		{"ragged csv", "ragged.csv", raggedRowsCSV, rowsLoop},
		{"ragged csv read through headers", "ragged.csv", raggedRowsCSV, "{{ headers[0] }}"},
		{"invalid json", "d.json", `{"rows":`, rowsLoop},
		{"invalid json read through items", "d.json", `[{"name":`, "{% for item in items %}{{ item.name }};{% endfor %}"},
		{"invalid json object read through a key", "d.json", `{"name":`, "{{ name }}"},
		{"invalid json object read through a looped key", "d.json", `{"rows":[`, "{% for r in rows %}[{{ r }}]{% endfor %}"},
		{"invalid json scalar read through value", "d.json", `"unterminated`, "{{ value }}"},
		{"malformed json array of records read through a text property", "d.json", `[{"text":"hi","who":"a"},`, "{% for m in items %}{{ m.text }};{% endfor %}"},
		{"malformed csv rows read through a text property", "ragged.csv", raggedRowsCSV, "{% for row in data_rows %}{{ row.text }}{% endfor %}"},
		{"ragged csv read through text and rows", "ragged.csv", raggedRowsCSV, "T={{ text | size }}{% for r in data_rows %}[{{ r[0] }}]{% endfor %}"},
		{"invalid yaml read through a key", "d.yaml", "name: [1,\n", "{{ name }}"},
		{"invalid yaml read through value", "d.yaml", "a: [1,\n", "{{ value }}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arts := seedTemplateData(t, tt.filename, tt.content, "text/plain")

			shown, err := renderSeededTemplate(t, arts, tt.filename, tt.template)
			if err == nil {
				t.Fatalf("expected a parse error, rendered %q", shown)
			}
			if got := templateRenderErrorKind(err); got != templateErrorDataParse {
				t.Errorf("kind = %q, want %q", got, templateErrorDataParse)
			}
			if want := templateErrorText(templateErrorDataParse, tt.filename); shown != want {
				t.Errorf("shown text = %q, want %q", shown, want)
			}
		})
	}
}

func TestRenderBlockPlainTextFallsBackToTextWhenTheTemplateRendersFromItAlone(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{"text only", "{{ text }}", strings.TrimSpace(raggedRowsCSV)},
		{"trimmed conditional", "{%- if text -%}has text{%- endif -%}", "has text"},
		{"split filter", `{{ text | split: "\n" | size }}`, "3"},
		{"capture", "{% capture c %}{{ text | size }}{% endcapture %}n={{ c }}", "n=" + strconv.Itoa(len(raggedRowsCSV))},
		{"truncate", "{{ text | truncate: 4, '' }}", "name"},
		{"raw block", "{% raw %}{{ text }}{% endraw %}", "{{ text }}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arts := seedTemplateData(t, "ragged.csv", raggedRowsCSV, "text/plain")

			shown, err := renderSeededTemplate(t, arts, "ragged.csv", tt.template)
			if err != nil {
				t.Fatalf("RenderBlock: %v", err)
			}
			if strings.TrimSpace(shown) != tt.want {
				t.Errorf("rendered %q, want %q", shown, tt.want)
			}
		})
	}
}

func TestRenderBlockLogsTheTextFallbackWithoutTheData(t *testing.T) {
	const secret = "secret-value"
	arts := seedTemplateData(t, "d.yaml", "name: ["+secret+",\n", "text/plain")
	logBuf := &bytes.Buffer{}
	renderer := NewTemplateRenderer(arts, "agent", inlineTestUser, inlineTestSession, slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	if _, err := renderer.RenderBlock(t.Context(), &FencedBlockEvent{DataSource: "d.yaml", Limit: -1, Content: "{{ text }}"}); err != nil {
		t.Fatalf("RenderBlock: %v", err)
	}

	const fallbackMessage = "template data parse failed; rendered from the text context"
	logged := logBuf.String()
	if got := strings.Count(logged, fallbackMessage); got != 1 {
		t.Errorf("fallback logged %d times, want once: %s", got, logged)
	}
	for _, want := range []string{"level=DEBUG", "dataArtifact=d.yaml", "storedMime=text/plain", "resolvedMime=application/x-yaml", "errorKind=data_parse"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log does not contain %q: %s", want, logged)
		}
	}
	for _, leaked := range []string{secret, "{{ text }}", "error=", "err=", "yaml:"} {
		if strings.Contains(logged, leaked) {
			t.Errorf("log carries %q: %s", leaked, logged)
		}
	}
}

func TestRenderBlockAppliesJSONPathAndLimitToDataResolvedFromTheExtension(t *testing.T) {
	const itemsLoop = "{% for i in items %}{{ i }};{% endfor %}"
	tests := []struct {
		name     string
		filename string
		content  string
		template string
		jsonPath string
		limit    int
		want     string
		wantKind templateErrorKind
	}{
		{name: "jsonpath selects rows", filename: "d.json", content: `{"rows":[1,2,3],"x":1}`, template: itemsLoop, jsonPath: "$.rows[*]", limit: -1, want: "1;2;3;"},
		{name: "limit truncates items", filename: "d.json", content: `[1,2,3,4]`, template: itemsLoop, limit: 2, want: "1;2;"},
		{name: "limit truncates data_rows", filename: "rows.csv", content: "n\n1\n2\n3\n", template: "{% for row in data_rows %}{{ row[0] }};{% endfor %}", limit: 2, want: "1;2;"},
		{name: "bad jsonpath with a text-only template", filename: "d.json", content: `{"a":1}`, template: "{{ text }}", jsonPath: "$.[[[", limit: -1, wantKind: templateErrorJSONPath},
		{name: "missing jsonpath with a structured template", filename: "d.json", content: `{"a":[1]}`, template: itemsLoop, jsonPath: "$.zzz", limit: -1, wantKind: templateErrorJSONPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arts := seedTemplateData(t, tt.filename, tt.content, "text/plain")

			shown, err := renderSeededTemplateWith(t, arts, tt.filename, tt.template, tt.jsonPath, tt.limit)
			if tt.wantKind != "" {
				if err == nil {
					t.Fatalf("expected a %s error, rendered %q", tt.wantKind, shown)
				}
				if got := templateRenderErrorKind(err); got != tt.wantKind {
					t.Errorf("kind = %q, want %q", got, tt.wantKind)
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderBlock: %v", err)
			}
			if strings.TrimSpace(shown) != tt.want {
				t.Errorf("rendered %q, want %q", shown, tt.want)
			}
		})
	}
}

func TestRenderBlockTextFallbackIgnoresTheJSONPath(t *testing.T) {
	const malformedRows = `{"rows":[1,2`
	const jsonPath = "$.rows[*]"
	tests := []struct {
		name         string
		template     string
		wantParseErr bool
	}{
		{"items loop", "{% for i in items %}{{ i }}{% endfor %}", true},
		{"items condition", "{% if items %}Y{% else %}none{% endif %}", true},
		{"text", "{{ text }}", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arts := seedTemplateData(t, "d.json", malformedRows, "text/plain")

			shown, err := renderSeededTemplateWith(t, arts, "d.json", tt.template, jsonPath, -1)
			if !tt.wantParseErr {
				if err != nil {
					t.Fatalf("RenderBlock: %v", err)
				}
				if shown != malformedRows {
					t.Errorf("rendered %q, want the raw text %q", shown, malformedRows)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a parse error, rendered %q", shown)
			}
			if got := templateRenderErrorKind(err); got != templateErrorDataParse {
				t.Errorf("kind = %q, want %q", got, templateErrorDataParse)
			}
			if want := templateErrorText(templateErrorDataParse, "d.json"); shown != want {
				t.Errorf("shown text = %q, want %q", shown, want)
			}
		})
	}
}

func TestRenderBlockStaticTemplateOverMalformedPlainTextIgnoresTheJSONPath(t *testing.T) {
	arts := seedTemplateData(t, "d.json", `{"rows":[1,2`, "text/plain")

	shown, err := renderSeededTemplateWith(t, arts, "d.json", "static table", "$.[[[", -1)
	if err != nil {
		t.Fatalf("RenderBlock: %v", err)
	}
	if shown != "static table" {
		t.Errorf("rendered %q, want the template as written", shown)
	}
}

func TestRenderBlockTextFallbackDeadlineReturnsTheTruncationMarker(t *testing.T) {
	prev := liquidsafe.DefaultRenderDeadline
	liquidsafe.DefaultRenderDeadline = 50 * time.Millisecond
	t.Cleanup(func() { liquidsafe.DefaultRenderDeadline = prev })
	arts := seedTemplateData(t, "d.json", `{"rows":[1,2`, "text/plain")

	shown, err := renderSeededTemplate(t, arts, "d.json", "{% for n in (1..10000000) %}{{ text | size }},{% endfor %}")
	if err != nil {
		t.Fatalf("RenderBlock should return the marker on a deadline, got: %v", err)
	}
	if !strings.Contains(shown, TemplateRenderDeadlineExceededMarker) {
		t.Errorf("expected the render-deadline truncation marker, got %d bytes", len(shown))
	}
}

func TestRenderBlockPlainTextJSONKeepsItsOwnTextField(t *testing.T) {
	arts := seedTemplateData(t, "d.json", `{"text":"mine","n":1}`, "text/plain")

	shown, err := renderSeededTemplate(t, arts, "d.json", "{{ text }}|{{ n }}")
	if err != nil {
		t.Fatalf("RenderBlock: %v", err)
	}
	if want := "mine|1"; strings.TrimSpace(shown) != want {
		t.Errorf("rendered %q, want %q", shown, want)
	}
}

func TestRenderBlockCSVStoredAsCSVHasNoTextContext(t *testing.T) {
	arts := seedTemplateData(t, "rows.csv", rowsCSV, "text/csv")

	shown, err := renderSeededTemplate(t, arts, "rows.csv", "[{{ text }}]{{ headers[0] }}")
	if err != nil {
		t.Fatalf("RenderBlock: %v", err)
	}
	if want := "[]name"; strings.TrimSpace(shown) != want {
		t.Errorf("rendered %q, want %q", shown, want)
	}
}

func TestRenderBlockRaggedCSVStoredAsCSVStillErrors(t *testing.T) {
	arts := seedTemplateData(t, "ragged.csv", raggedRowsCSV, "text/csv")

	_, err := renderSeededTemplate(t, arts, "ragged.csv", "{{ text }}")
	if err == nil {
		t.Fatal("expected a parse error for a ragged text/csv artifact")
	}
	if got := templateRenderErrorKind(err); got != templateErrorDataParse {
		t.Errorf("kind = %q, want %q", got, templateErrorDataParse)
	}
}

func TestValidateTemplateIndexBoundsSeesCSVStoredAsPlainText(t *testing.T) {
	const threeColumns = "a,b,c\n1,2,3\n"
	const wantMessage = "only has 3 columns"
	for _, mimeType := range []string{"text/csv", "text/plain"} {
		t.Run(mimeType, func(t *testing.T) {
			arts := seedTemplateData(t, "rows.csv", threeColumns, mimeType)
			block := &FencedBlockEvent{DataSource: "rows.csv", Content: "{% for row in data_rows %}{{ row[3] }}{% endfor %}"}

			err := validateTemplateIndexBounds(t.Context(), block, arts, "agent", inlineTestUser, inlineTestSession)
			if err == nil || !strings.Contains(err.Error(), wantMessage) {
				t.Errorf("validateTemplateIndexBounds = %v, want an error containing %q", err, wantMessage)
			}
		})
	}
}

func newInlineTestPipelineState(visible, raw *strings.Builder, info *streamCallInfo) *streamPipelineState {
	return &streamPipelineState{
		visibleText:        visible,
		rawText:            raw,
		info:               info,
		createdArtifacts:   &[]artifactCreationRecord{},
		templateEmitted:    new(false),
		thinkingActive:     new(false),
		turnSpacingPending: new(false),
	}
}

func TestRenderTemplateInlineAfterSameStreamSaveRendersErrorInsteadOfRetrying(t *testing.T) {
	tests := []struct {
		name         string
		dataSource   string
		jsonPath     string
		template     string
		seed         func(t *testing.T, h *templateBlockHarness)
		wantText     string
		wantKind     string
		wantExcludes []string
	}{
		{
			name:       "missing data renders the not-found error",
			dataSource: "absent.json",
			template:   productsTemplate,
			wantText:   "[Template Error: Data artifact 'absent.json' not found]",
			wantKind:   "not_found",
		},
		{
			name:       "liquid conversion error renders without the data value",
			dataSource: "people.json",
			template:   "{% for item in items %}{{ item.ssn | divided_by: 2 }}{% endfor %}",
			seed: func(t *testing.T, h *templateBlockHarness) {
				h.seed(t, "people.json", `[{"ssn":"`+inlineTestSSN+`"}]`, "application/json")
			},
			wantText:     "[Template Error: Rendering against 'people.json' failed]",
			wantKind:     "render",
			wantExcludes: []string{inlineTestSSN},
		},
		{
			name:       "failing jsonpath renders without the evaluator error",
			dataSource: "people.json",
			jsonPath:   "$.missing",
			template:   productsTemplate,
			seed: func(t *testing.T, h *templateBlockHarness) {
				h.seed(t, "people.json", `{"ssn":"`+inlineTestSSN+`"}`, "application/json")
			},
			wantText:     "[Template Error: The jsonpath filter on data artifact 'people.json' failed]",
			wantKind:     "jsonpath",
			wantExcludes: []string{inlineTestSSN, "no match"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTemplateBlockHarness(t)
			if tt.seed != nil {
				tt.seed(t, h)
			}
			var visible, raw strings.Builder
			st := newInlineTestPipelineState(&visible, &raw, &streamCallInfo{UserID: inlineTestUser, SessionID: inlineTestSession, RenderTemplatesInline: true, BlockValidationRetriesLeft: maxBlockValidationRetries})
			*st.createdArtifacts = append(*st.createdArtifacts, artifactCreationRecord{Filename: "report.md", Version: 1})
			block := &FencedBlockEvent{Type: FencedBlockTemplate, DataSource: tt.dataSource, JSONPath: tt.jsonPath, Limit: -1, Content: tt.template}

			if bve := h.agent.handleBlockEnd(t.Context(), h.sp, block, st); bve != nil {
				t.Fatalf("got block validation error %q, want the error rendered so the saved artifact record survives", bve.ErrorMsg)
			}
			text := publishedText(h.publishedParts(t))
			if !strings.Contains(visible.String(), tt.wantText) {
				t.Errorf("visibleText = %q, want it to contain %q", visible.String(), tt.wantText)
			}
			if !strings.Contains(text, tt.wantText) {
				t.Errorf("published text = %q, want it to contain %q", text, tt.wantText)
			}
			for _, excluded := range tt.wantExcludes {
				if strings.Contains(visible.String(), excluded) {
					t.Errorf("visibleText = %q, must not contain %q", visible.String(), excluded)
				}
				if strings.Contains(text, excluded) {
					t.Errorf("published text = %q, must not contain %q", text, excluded)
				}
				if strings.Contains(h.logBuf.String(), excluded) {
					t.Errorf("agent log = %q, must not contain %q", h.logBuf.String(), excluded)
				}
			}
			if !strings.Contains(h.logBuf.String(), "errorKind="+tt.wantKind) {
				t.Errorf("agent log = %q, want the render failure logged with errorKind=%s", h.logBuf.String(), tt.wantKind)
			}
		})
	}
}

func TestRenderTemplateInlineLogsOnTaskLogger(t *testing.T) {
	h := newTemplateBlockHarness(t)
	h.seed(t, "huge.txt", strings.Repeat("x", MaxTemplateDataSize+1), "text/plain")
	taskLogBuf := &bytes.Buffer{}
	taskLogger := slog.New(slog.NewTextHandler(taskLogBuf, nil)).With("traceID", "t-1")
	var visible, raw strings.Builder
	st := newInlineTestPipelineState(&visible, &raw, &streamCallInfo{
		UserID:                inlineTestUser,
		SessionID:             inlineTestSession,
		RenderTemplatesInline: true,
		Logger:                taskLogger,
	})
	block := &FencedBlockEvent{Type: FencedBlockTemplate, DataSource: "huge.txt", Limit: -1, Content: "{{ text | size }}"}

	if bve := h.agent.handleBlockEnd(t.Context(), h.sp, block, st); bve != nil {
		t.Fatalf("unexpected block validation error: %s", bve.ErrorMsg)
	}
	var failureLine string
	for line := range strings.SplitSeq(taskLogBuf.String(), "\n") {
		if strings.Contains(line, inlineFailedLog) {
			failureLine = line
		}
	}
	if !strings.Contains(failureLine, "errorKind=too_large") || !strings.Contains(failureLine, "traceID=t-1") {
		t.Errorf("task log line = %q, want errorKind=too_large and traceID=t-1", failureLine)
	}
	if strings.Contains(h.logBuf.String(), inlineFailedLog) {
		t.Errorf("agent log = %q, want the failure only on the task logger", h.logBuf.String())
	}
}

func TestDataArrayBoundsMIMETypeIsCaseInsensitive(t *testing.T) {
	if got := dataArrayBounds(`[["a","b","c"]]`, "Application/JSON")["row"]; got != 3 {
		t.Errorf("JSON row bound = %d, want 3", got)
	}
	if got := dataArrayBounds("h1,h2\n1,2\n", "Text/CSV")["row"]; got != 2 {
		t.Errorf("CSV row bound = %d, want 2", got)
	}
}

func TestStreamLLMCallInlineTemplateAppliesTurnSpacing(t *testing.T) {
	const llmOutput = BlockPrefixTemplateLiquid + ` data="products.json"` + "\n" + productsTemplate + "\n" + BlockDelimiterClose

	h := newTemplateBlockHarness(t, &llm.ChatResponse{Content: llmOutput})
	h.seed(t, "products.json", productsJSON, "application/json")

	resp, _, err := h.agent.streamLLMCall(t.Context(), h.sp, &llm.ChatRequest{}, &streamCallInfo{
		UserID:                inlineTestUser,
		SessionID:             inlineTestSession,
		MaxContinuations:      -1,
		PrependTurnSpacing:    true,
		RenderTemplatesInline: true,
	})
	if err != nil {
		t.Fatalf("streamLLMCall: %v", err)
	}
	if text := publishedText(h.publishedParts(t)); !strings.HasPrefix(text, "\n\n"+productsRows) {
		t.Errorf("published text = %q, want the turn spacing before the rows", text)
	}
	if !strings.HasPrefix(resp.Content, productsRows) {
		t.Errorf("Content = %q, want it to start with %q (turn spacing is a stream-only separator)", resp.Content, productsRows)
	}
}

func TestStreamLLMCallAppliesTurnSpacingOnceBeforeTheFirstAnswerContent(t *testing.T) {
	const template = BlockPrefixTemplateLiquid + ` data="products.json"` + "\n" + productsTemplate + "\n" + BlockDelimiterClose
	tests := []struct {
		name       string
		llmOutput  string
		spacing    bool
		wantBefore string
		wantText   string
	}{
		{
			name:       "gateway template block",
			llmOutput:  template + "\nThat is the full list.",
			spacing:    true,
			wantBefore: turnSeparator,
			wantText:   turnSeparator + "That is the full list.",
		},
		{
			name:       "text before the template in the same turn",
			llmOutput:  "Here they are:\n" + template,
			spacing:    true,
			wantBefore: turnSeparator + "Here they are:\n",
			wantText:   turnSeparator + "Here they are:\n",
		},
		{
			name:       "template block without pending spacing",
			llmOutput:  template + "\nDone.",
			wantBefore: "",
			wantText:   "Done.",
		},
		{
			name:      "early embed resolved by the agent",
			llmOutput: "«math:2+3» items are ready.",
			spacing:   true,
			wantText:  turnSeparator + "5 items are ready.",
		},
		{
			name:      "late embed passed through for the gateway",
			llmOutput: "«artifact_content:report.md» is attached.",
			spacing:   true,
			wantText:  turnSeparator + "«artifact_content:report.md» is attached.",
		},
		{
			name:      "status update leaves the spacing for the next text",
			llmOutput: "«status_update:Working»Done.",
			spacing:   true,
			wantText:  turnSeparator + "Done.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTemplateBlockHarness(t, &llm.ChatResponse{Content: tt.llmOutput})
			h.seed(t, "products.json", productsJSON, "application/json")

			if _, _, err := h.agent.streamLLMCall(t.Context(), h.sp, &llm.ChatRequest{}, &streamCallInfo{
				UserID:             inlineTestUser,
				SessionID:          inlineTestSession,
				MaxContinuations:   -1,
				PrependTurnSpacing: tt.spacing,
			}); err != nil {
				t.Fatalf("streamLLMCall: %v", err)
			}

			parts := h.publishedParts(t)
			if block := slices.IndexFunc(parts, isTemplateBlockSignal); block >= 0 {
				if got := publishedText(parts[:block]); got != tt.wantBefore {
					t.Errorf("text before the template block = %q, want %q", got, tt.wantBefore)
				}
			}
			if got := publishedText(parts); got != tt.wantText {
				t.Errorf("published text = %q, want %q", got, tt.wantText)
			}
		})
	}
}

func TestStreamLLMCallResolvesStatusUpdateWithStrayLeadingRune(t *testing.T) {
	const llmOutput = "«žstatus_update:Rendering the table...»\n\nHere is the answer."
	h := newTemplateBlockHarness(t, &llm.ChatResponse{Content: llmOutput})

	resp, _, err := h.agent.streamLLMCall(t.Context(), h.sp, &llm.ChatRequest{}, &streamCallInfo{
		UserID:           inlineTestUser,
		SessionID:        inlineTestSession,
		MaxContinuations: -1,
	})
	if err != nil {
		t.Fatalf("streamLLMCall: %v", err)
	}

	parts := h.publishedParts(t)
	if text := publishedText(parts); text != "\n\nHere is the answer." {
		t.Errorf("published text = %q, want only the answer", text)
	}
	if strings.Contains(resp.Content, "status_update") {
		t.Errorf("Content = %q, want no raw embed", resp.Content)
	}
	var progress []string
	for _, p := range parts {
		if dm, ok := p.Data.(map[string]any); ok && dm["type"] == "agent_progress_update" {
			progress = append(progress, fmt.Sprint(dm["status_text"]))
		}
	}
	if !slices.Equal(progress, []string{"Rendering the table..."}) {
		t.Errorf("progress updates = %q, want the one status update", progress)
	}
}

func TestHandleEmbedEventFailedResolutionAppliesTurnSpacing(t *testing.T) {
	h := newTemplateBlockHarness(t)
	resolver := NewEmbedResolver(nil, "agent", inlineTestUser, inlineTestSession, "", h.agent.logger)
	var visible strings.Builder
	pending := true

	h.agent.handleEmbedEvent(t.Context(), h.sp, resolver,
		&EmbedRef{Type: "unknown_early", Raw: "«unknown_early:x»", IsEarly: true}, &visible, &pending)

	if got := publishedText(h.publishedParts(t)); got != turnSeparator+"«unknown_early:x»" {
		t.Errorf("published text = %q, want the separator then the raw embed", got)
	}
	if pending || visible.String() != "«unknown_early:x»" {
		t.Errorf("pending = %v, visible = %q; the separator is used up and stays out of visible text", pending, visible.String())
	}
}

func TestStreamLLMCallInlineTemplateKeepsRawBlockForHistory(t *testing.T) {
	const (
		rawBlockHeader = BlockPrefixTemplateLiquid + ` data="products.json"`
		templateBlock  = rawBlockHeader + "\n" + productsTemplate + "\n" + BlockDelimiterClose
		saveBlock      = BlockPrefixSaveArtifact + ` filename="report.md" description="A report"` + "\n# Report\n" + BlockDelimiterClose
		saveHeader     = BlockPrefixSaveArtifact + ` filename="report.md"`
		absentBlock    = BlockPrefixTemplateLiquid + ` data="absent.json"` + "\n" + productsTemplate + "\n" + BlockDelimiterClose
		absentError    = "[Template Error: Data artifact 'absent.json' not found]"
	)

	tests := []struct {
		name            string
		inline          bool
		llmOutput       string
		wantContent     string
		wantRaw         bool
		wantRawContains []string
		contentExcludes []string
	}{
		{
			name:            "run-based turn answers with rows and remembers the block",
			inline:          true,
			llmOutput:       inlineTestPrefix + templateBlock,
			wantContent:     inlineTestPrefix + productsRows,
			wantRaw:         true,
			wantRawContains: []string{rawBlockHeader},
		},
		{
			name:            "interactive turn shows the prose and remembers the template block",
			inline:          false,
			llmOutput:       inlineTestPrefix + templateBlock,
			wantContent:     inlineTestPrefix,
			wantRaw:         true,
			wantRawContains: []string{rawBlockHeader},
			contentExcludes: []string{BlockPrefixTemplateLiquid},
		},
		{
			name:            "render failure with no retries left answers with the error and remembers it after the block",
			inline:          true,
			llmOutput:       inlineTestPrefix + absentBlock,
			wantContent:     inlineTestPrefix + absentError,
			wantRaw:         true,
			wantRawContains: []string{absentBlock + "\n" + absentError},
		},
		{
			name:            "interactive save and template turn remembers the model's own template block",
			inline:          false,
			llmOutput:       inlineTestPrefix + saveBlock + "\n" + templateBlock,
			wantRaw:         true,
			wantRawContains: []string{saveHeader, rawBlockHeader},
			contentExcludes: []string{BlockPrefixTemplateLiquid, "Widget"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTemplateBlockHarness(t, &llm.ChatResponse{Content: tt.llmOutput})
			h.seed(t, "products.json", productsJSON, "application/json")

			resp, _, err := h.agent.streamLLMCall(t.Context(), h.sp, &llm.ChatRequest{}, &streamCallInfo{
				UserID:                inlineTestUser,
				SessionID:             inlineTestSession,
				MaxContinuations:      -1,
				RenderTemplatesInline: tt.inline,
			})
			if err != nil {
				t.Fatalf("streamLLMCall: %v", err)
			}
			if tt.wantContent != "" && strings.TrimSpace(resp.Content) != strings.TrimSpace(tt.wantContent) {
				t.Errorf("Content = %q, want %q", resp.Content, tt.wantContent)
			}
			for _, excluded := range tt.contentExcludes {
				if strings.Contains(resp.Content, excluded) {
					t.Errorf("Content = %q, must not contain %q", resp.Content, excluded)
				}
			}
			if gotRaw := resp.RawContent != ""; gotRaw != tt.wantRaw {
				t.Fatalf("RawContent = %q, want set=%v", resp.RawContent, tt.wantRaw)
			}
			for _, want := range tt.wantRawContains {
				if !strings.Contains(resp.RawContent, want) {
					t.Errorf("RawContent = %q, want it to contain %q", resp.RawContent, want)
				}
			}
		})
	}
}

func TestRenderBlockErrorKinds(t *testing.T) {
	tests := []struct {
		name        string
		noArtifacts bool
		dataSource  string
		content     string
		mimeType    string
		jsonPath    string
		template    string
		wantKind    templateErrorKind
		wantFixable bool
	}{
		{name: "missing data parameter", template: "x", wantKind: templateErrorMissingData, wantFixable: true},
		{name: "no artifact service", noArtifacts: true, dataSource: "d.json", template: "x", wantKind: templateErrorNoArtifactService},
		{name: "data not saved yet", dataSource: "absent.json", template: "x", wantKind: templateErrorNotFound, wantFixable: true},
		{name: "unparseable JSON", dataSource: "d.json", content: `{"ssn":"` + inlineTestSSN, template: "x", wantKind: templateErrorDataParse},
		{name: "unparseable CSV", dataSource: "d.csv", content: "a,b\n\"" + inlineTestSSN + ",2\n", mimeType: "text/csv", template: "x", wantKind: templateErrorDataParse},
		{name: "unparseable YAML", dataSource: "d.yaml", content: "ssn: [" + inlineTestSSN, mimeType: "application/yaml", template: "x", wantKind: templateErrorDataParse},
		{name: "jsonpath with no match", dataSource: "d.json", content: `{"ssn":"` + inlineTestSSN + `"}`, jsonPath: "$.missing", template: "x", wantKind: templateErrorJSONPath, wantFixable: true},
		{name: "liquid conversion failure", dataSource: "d.json", content: `[{"ssn":"` + inlineTestSSN + `"}]`, template: "{% for item in items %}{{ item.ssn | divided_by: 2 }}{% endfor %}", wantKind: templateErrorRender, wantFixable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var arts *artifact.ScopedService
			if !tt.noArtifacts {
				arts = &artifact.ScopedService{Inner: artmem.New()}
				if tt.content != "" {
					loc := arts.Resolve("agent", inlineTestUser, inlineTestSession, tt.dataSource)
					if _, err := arts.Inner.Save(t.Context(), loc, []byte(tt.content), cmp.Or(tt.mimeType, "application/json")); err != nil {
						t.Fatal(err)
					}
				}
			}
			renderer := NewTemplateRenderer(arts, "agent", inlineTestUser, inlineTestSession, slog.Default())

			shown, err := renderer.RenderBlock(t.Context(), &FencedBlockEvent{DataSource: tt.dataSource, JSONPath: tt.jsonPath, Limit: -1, Content: tt.template})
			if err == nil {
				t.Fatal("expected a render error")
			}
			if got := templateRenderErrorKind(err); got != tt.wantKind {
				t.Errorf("kind = %q, want %q", got, tt.wantKind)
			}
			if got := TemplateErrorKind(err); got != string(tt.wantKind) {
				t.Errorf("TemplateErrorKind = %q, want %q", got, tt.wantKind)
			}
			if want := templateErrorText(tt.wantKind, tt.dataSource); shown != want {
				t.Errorf("shown text = %q, want %q", shown, want)
			}
			if strings.Contains(shown, inlineTestSSN) {
				t.Errorf("shown text leaks the data value: %q", shown)
			}
			msg := fixableTemplateRenderMessage(tt.wantKind, tt.dataSource)
			if (msg != "") != tt.wantFixable {
				t.Errorf("fixable message = %q, want fixable=%v", msg, tt.wantFixable)
			}
			if strings.Contains(msg, inlineTestSSN) {
				t.Errorf("fixable message leaks the data value: %q", msg)
			}
		})
	}
	if got := templateRenderErrorKind(errors.New("other")); got != templateErrorUnknown {
		t.Errorf("kind of an unrelated error = %q, want unknown", got)
	}
	wrapped := fmt.Errorf("outer: %w", wrapTemplateErr(templateErrorJSONPath, errors.New("inner")))
	if got := templateRenderErrorKind(wrapped); got != templateErrorJSONPath {
		t.Errorf("kind through a wrapping error = %q, want jsonpath", got)
	}
}

func TestCallerDisplaysTemplates(t *testing.T) {
	namespaceArts := &artifact.ScopedService{Inner: artmem.New(), Scope: artifact.ScopeNamespace, Namespace: "test-ns"}
	agentArts := &artifact.ScopedService{Inner: artmem.New(), Scope: "app"}
	delegated := map[string]any{"sessionBehavior": "RUN_BASED", "parentTaskId": "task-parent"}
	tests := []struct {
		name         string
		metadata     map[string]any
		isRunBased   bool
		rootDisplays bool
		arts         *artifact.ScopedService
		want         bool
	}{
		{name: "delegated run-based task under a displaying root", metadata: delegated, isRunBased: true, rootDisplays: true, arts: namespaceArts, want: true},
		{name: "root does not display template signals", metadata: delegated, isRunBased: true, arts: namespaceArts},
		{name: "run-based task with no parent (scheduled, sentinel)", metadata: map[string]any{"sessionBehavior": "RUN_BASED"}, isRunBased: true, rootDisplays: true, arts: namespaceArts},
		{name: "empty parentTaskId", metadata: map[string]any{"parentTaskId": ""}, isRunBased: true, rootDisplays: true, arts: namespaceArts},
		{name: "interactive task", metadata: delegated, rootDisplays: true, arts: namespaceArts},
		{name: "agent-scoped artifacts the gateway cannot resolve", metadata: delegated, isRunBased: true, rootDisplays: true, arts: agentArts},
		{name: "no artifact service", metadata: delegated, isRunBased: true, rootDisplays: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := callerDisplaysTemplates(a2a.Message{Metadata: tt.metadata}, tt.isRunBased, tt.rootDisplays, tt.arts); got != tt.want {
				t.Errorf("callerDisplaysTemplates() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetupTaskRecordsCallerDisplaysTemplates(t *testing.T) {
	delegated := func(extra map[string]any) map[string]any {
		metadata := map[string]any{"sessionBehavior": "RUN_BASED", "parentTaskId": "task-parent"}
		maps.Copy(metadata, extra)
		return metadata
	}
	tests := []struct {
		name            string
		metadata        map[string]any
		wantDisplays    bool
		wantCallerShows bool
	}{
		{name: "peer delegation from a displaying root", metadata: delegated(map[string]any{a2a.MetadataDisplaysTemplateSignals: true}), wantDisplays: true, wantCallerShows: true},
		{name: "peer delegation without the root flag", metadata: delegated(nil)},
		{name: "a non-bool flag is not the flag", metadata: delegated(map[string]any{a2a.MetadataDisplaysTemplateSignals: "true"})},
		{name: "run-based without a parent", metadata: map[string]any{"sessionBehavior": "RUN_BASED", a2a.MetadataDisplaysTemplateSignals: true}, wantDisplays: true},
		{name: "interactive with a parent", metadata: map[string]any{"parentTaskId": "task-parent", a2a.MetadataDisplaysTemplateSignals: true}, wantDisplays: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: artmem.New(), Scope: artifact.ScopeNamespace, Namespace: "test-ns"}
			msg := newUserMessage("hi")
			msg.Metadata = tt.metadata

			state, rc, earlyResp, err := agent.setupTask(t.Context(), "task-1", msg, a2a.UserProperties{UserID: inlineTestUser}, taskDelivery{})
			if err != nil || earlyResp != nil {
				t.Fatalf("setupTask: err=%v earlyResp=%+v", err, earlyResp)
			}
			if state.CallerDisplaysTemplates != tt.wantCallerShows {
				t.Errorf("CallerDisplaysTemplates = %v, want %v", state.CallerDisplaysTemplates, tt.wantCallerShows)
			}
			if state.DisplaysTemplateSignals != tt.wantDisplays {
				t.Errorf("state.DisplaysTemplateSignals = %v, want %v", state.DisplaysTemplateSignals, tt.wantDisplays)
			}
			if rc.toolCtx.DisplaysTemplateSignals != tt.wantDisplays {
				t.Errorf("toolCtx.DisplaysTemplateSignals = %v, want %v", rc.toolCtx.DisplaysTemplateSignals, tt.wantDisplays)
			}
			if resumed := agent.buildToolCtxFromState(t.Context(), state, rc.sp); resumed.DisplaysTemplateSignals != tt.wantDisplays {
				t.Errorf("resumed toolCtx.DisplaysTemplateSignals = %v, want %v", resumed.DisplaysTemplateSignals, tt.wantDisplays)
			}
		})
	}
}

func newBlockValidationTestRunContext(logger *slog.Logger) *taskRunContext {
	return &taskRunContext{
		sess:   &session.Session{},
		sp:     NewStatusPublisher(nil, StatusPublisherConfig{}, logger),
		logger: logger,
	}
}

func TestInlineTemplateRetryBudgetBoundary(t *testing.T) {
	const absentError = "[Template Error: Data artifact 'absent.json' not found]"
	for used := 0; used <= maxBlockValidationRetries; used++ {
		t.Run(fmt.Sprintf("%d retries used", used), func(t *testing.T) {
			h := newTemplateBlockHarness(t)
			var visible, raw strings.Builder
			st := newInlineTestPipelineState(&visible, &raw, &streamCallInfo{
				UserID:                     inlineTestUser,
				SessionID:                  inlineTestSession,
				RenderTemplatesInline:      true,
				BlockValidationRetriesLeft: maxBlockValidationRetries - used,
			})
			block := &FencedBlockEvent{Type: FencedBlockTemplate, DataSource: "absent.json", Limit: -1, Content: productsTemplate}

			bve := h.agent.handleBlockEnd(t.Context(), h.sp, block, st)

			state := newTestTaskState(h.agent)
			state.BlockValidationRetries = used
			var allText replyText
			rc := newBlockValidationTestRunContext(slog.New(slog.NewTextHandler(io.Discard, nil)))
			if used == maxBlockValidationRetries {
				if bve != nil {
					t.Fatalf("got a retry with the budget spent: %q", bve.ErrorMsg)
				}
				if !strings.Contains(visible.String(), absentError) {
					t.Errorf("visibleText = %q, want the fallback error %q", visible.String(), absentError)
				}
				spentRetry := &blockValidationError{ErrorMsg: "x", BlockType: FencedBlockTemplate}
				if _, outcome, _ := h.agent.handleStreamCallError(t.Context(), state, rc, nil, spentRetry, &allText); outcome != loopReturn {
					t.Errorf("outcome = %v, want loopReturn: a retry at this count must fail the task, which is why the render falls back", outcome)
				}
				return
			}
			if bve == nil {
				t.Fatalf("got no retry with %d retries left", maxBlockValidationRetries-used)
			}
			if resp, outcome, err := h.agent.handleStreamCallError(t.Context(), state, rc, nil, bve, &allText); outcome != loopContinue || resp != nil || err != nil {
				t.Errorf("handleStreamCallError = (%+v, %v, %v), want the retry to continue the loop", resp, outcome, err)
			}
		})
	}
}

func TestHandleStreamCallErrorLogsTemplateErrorKind(t *testing.T) {
	tests := []struct {
		name     string
		bve      *blockValidationError
		wantKind string
	}{
		{name: "template render failure", bve: &blockValidationError{ErrorMsg: "fix it", BlockType: FencedBlockTemplate, ErrorKind: "jsonpath"}, wantKind: "jsonpath"},
		{name: "malformed artifact block", bve: &blockValidationError{ErrorMsg: "fix it", BlockType: FencedBlockArtifact, Filename: "a.md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			var buf syncBuffer
			rc := newBlockValidationTestRunContext(slog.New(slog.NewJSONHandler(&buf, nil)))
			var allText replyText

			if _, outcome, _ := agent.handleStreamCallError(t.Context(), newTestTaskState(agent), rc, nil, tt.bve, &allText); outcome != loopContinue {
				t.Fatalf("outcome = %v, want loopContinue", outcome)
			}
			var record map[string]any
			for line := range strings.SplitSeq(buf.String(), "\n") {
				var rec map[string]any
				if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == "block validation error, retrying" {
					record = rec
				}
			}
			if record == nil {
				t.Fatalf("no retry line logged; captured:\n%s", buf.String())
			}
			got, present := record["errorKind"]
			if tt.wantKind == "" && present {
				t.Errorf("errorKind = %v, want the key absent for a non-template block", got)
			}
			if tt.wantKind != "" && got != tt.wantKind {
				t.Errorf("errorKind = %v, want %q", got, tt.wantKind)
			}
		})
	}
}

func TestContinueTaskLoopInlineTemplateSpendsEveryRetryThenAnswers(t *testing.T) {
	const llmOutput = BlockPrefixTemplateLiquid + ` data="absent.json"` + "\n" + productsTemplate + "\n" + BlockDelimiterClose
	responses := make([]*llm.ChatResponse, maxBlockValidationRetries+1)
	for i := range responses {
		responses[i] = &llm.ChatResponse{Content: llmOutput, FinishReason: "stop"}
	}
	agent, mock, _ := newTestAgent(responses...)
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: artmem.New()}
	msg := newUserMessage("show the products")
	msg.Metadata = map[string]any{"sessionBehavior": "RUN_BASED"}

	resp, err := agent.HandleTask(t.Context(), "task-retries", msg, a2a.UserProperties{UserID: inlineTestUser, ClientID: "client-1"})
	if err != nil || resp.Error != nil {
		t.Fatalf("HandleTask: err=%v resp.Error=%+v, want the task to complete with the error text", err, resp.Error)
	}
	calls := mock.getCalls()
	if len(calls) != maxBlockValidationRetries+1 {
		t.Fatalf("LLM calls = %d, want %d: the first attempt plus every allowed retry", len(calls), maxBlockValidationRetries+1)
	}
	feedback := calls[1].Messages[len(calls[1].Messages)-1].Content
	if !strings.Contains(feedback, "'absent.json' was not found") || !strings.Contains(feedback, templateRetryInstruction) {
		t.Errorf("retry feedback = %q, want the not-found message and %q", feedback, templateRetryInstruction)
	}
}

func TestContinueTaskLoopDelegatedTemplateAnswerNotesTheDisplay(t *testing.T) {
	const llmOutput = BlockPrefixTemplateLiquid + ` data="products.json"` + "\n" + productsTemplate + "\n" + BlockDelimiterClose
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: llmOutput, FinishReason: "stop"})
	arts := &artifact.ScopedService{Inner: artmem.New(), Scope: artifact.ScopeNamespace, Namespace: "test-ns"}
	agent.svc.(*mockServices).arts = arts
	msg := newUserMessage("show the products")
	msg.Metadata = map[string]any{"sessionBehavior": "RUN_BASED", "parentTaskId": "task-parent", a2a.MetadataDisplaysTemplateSignals: true}
	loc := arts.Resolve(agent.cfg.Name, inlineTestUser, msg.ContextID, "products.json")
	if _, err := arts.Inner.Save(t.Context(), loc, []byte(productsJSON), "application/json"); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(t.Context(), "task-delegated", msg, a2a.UserProperties{UserID: inlineTestUser, ClientID: "client-1"})
	if err != nil || resp.Error != nil {
		t.Fatalf("HandleTask: err=%v resp.Error=%+v", err, resp.Error)
	}
	task, ok := resp.Result.(a2a.Task)
	if !ok || task.Status.Message == nil {
		t.Fatalf("result = %#v, want a task with a status message", resp.Result)
	}
	if got := publishedText(task.Status.Message.Parts); !strings.Contains(got, productsRows) || !strings.HasSuffix(got, templateShownToUserNote) {
		t.Errorf("answer = %q, want the rows followed by %q", got, templateShownToUserNote)
	}
}

func TestContinueTaskLoopInlineTemplateFailureLogsOnTaskLogger(t *testing.T) {
	const (
		traceID   = "trace-inline-template"
		llmOutput = BlockPrefixTemplateLiquid + ` data="huge.txt"` + "\n{{ text | size }}\n" + BlockDelimiterClose
	)
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: llmOutput, FinishReason: "stop"})
	arts := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = arts
	var buf syncBuffer
	agent.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	msg := newUserMessage("summarize the data")
	msg.Metadata = map[string]any{"sessionBehavior": "RUN_BASED"}
	loc := arts.Resolve(agent.cfg.Name, inlineTestUser, msg.ContextID, "huge.txt")
	if _, err := arts.Inner.Save(t.Context(), loc, []byte(strings.Repeat("x", MaxTemplateDataSize+1)), "text/plain"); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(t.Context(), "task-inline", msg, a2a.UserProperties{UserID: inlineTestUser, ClientID: "client-1", TraceID: traceID})
	if err != nil || resp.Error != nil {
		t.Fatalf("HandleTask: err=%v resp.Error=%+v", err, resp.Error)
	}
	var record map[string]any
	for line := range strings.SplitSeq(buf.String(), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == inlineFailedLog {
			record = rec
		}
	}
	if record == nil {
		t.Fatalf("no %q line logged; captured:\n%s", inlineFailedLog, buf.String())
	}
	if record["traceID"] != traceID || record["errorKind"] != "too_large" {
		t.Errorf("log record = %v, want traceID=%s and errorKind=too_large", record, traceID)
	}
}
