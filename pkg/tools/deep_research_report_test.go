// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const wafTriggerPage = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd" [ <!ENTITY copy "c"> ]>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
<title>Quantum page</title>
<meta charset="utf-8">
<style>body { color: red }</style>
<script>(function(){ var a = 1; })();</script>
</head>
<body>
<!-- tracking comment -->
<svg><text>vector-label</text></svg>
<noscript>enable-javascript-notice</noscript>
<h1>Quantum computing basics</h1>
<p>Qubits can hold superpositions of states.</p>
</body>
</html>`

var rawMarkupMarkers = []string{"<!DOCTYPE", "<?xml", "<script", "<!ENTITY", "<style", "<svg", "tracking comment", "enable-javascript-notice", "vector-label"}

type deepResearchHarness struct {
	mu            sync.Mutex
	reportPrompts []string
	failReport    func(prompt string) error
	pageType      string
	parent        context.Context
	maxIterations int
	runtimeSecs   int
	reportCtxErrs []error
}

func (h *deepResearchHarness) llmCall(ctx context.Context, _, prompt string, _ float64) (string, error) {
	switch {
	case strings.Contains(prompt, "You are an expert research analyst"):
		h.mu.Lock()
		h.reportPrompts = append(h.reportPrompts, prompt)
		h.reportCtxErrs = append(h.reportCtxErrs, ctx.Err())
		h.mu.Unlock()
		if h.failReport != nil {
			if err := h.failReport(prompt); err != nil {
				return "", err
			}
		}
		return "## Executive Summary\nA report.", nil
	case strings.Contains(prompt, "Select the"):
		return `{"selected_sources": [1], "reasoning": "x"}`, nil
	case strings.Contains(prompt, "Analyze the current research findings"):
		if h.runtimeSecs > 0 {
			<-ctx.Done()
			return `{"quality_score": 0.1, "gaps": ["x"], "should_continue": true, "suggested_queries": ["more"], "reasoning": "x"}`, nil
		}
		return `{"quality_score": 0.9, "gaps": [], "should_continue": false, "suggested_queries": [], "reasoning": "x"}`, nil
	default:
		return "not json", nil
	}
}

func (h *deepResearchHarness) run(t *testing.T) *tool.Result {
	t.Helper()

	var pageURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/customsearch/v1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"items":[{"title":"Quantum page","link":%q,"snippet":"Short snippet about qubits."}]}`, pageURL)
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		contentType := "text/html; charset=utf-8"
		if h.pageType != "" {
			contentType = h.pageType
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(wafTriggerPage))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	pageURL = srv.URL + "/page"

	toolCtx := &tool.Context{
		AgentName:    "test-agent",
		UserID:       "u",
		SessionID:    "s",
		ParentTaskID: "t",
		ToolConfig: map[string]any{
			"google_search_api_key":         "k",
			"google_cse_id":                 "cx",
			"api_base":                      srv.URL,
			"allow_loopback":                true,
			"max_iterations":                max(h.maxIterations, 1),
			"interactive_plan_verification": false,
		},
		InternalLLMCall: h.llmCall,
	}
	if h.runtimeSecs > 0 {
		toolCtx.ToolConfig["max_runtime_seconds"] = h.runtimeSecs
	}
	parent := h.parent
	if parent == nil {
		parent = t.Context()
	}
	res, err := NewDeepResearchTool(nil).Execute(parent, map[string]any{"research_question": "How do qubits work?"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func TestDeepResearchReportPromptHasNoRawMarkup(t *testing.T) {
	h := &deepResearchHarness{}
	res := h.run(t)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want success", res.Status, res.Message)
	}
	if len(h.reportPrompts) != 1 {
		t.Fatalf("report calls = %d, want 1", len(h.reportPrompts))
	}
	prompt := h.reportPrompts[0]
	for _, marker := range rawMarkupMarkers {
		if strings.Contains(prompt, marker) {
			t.Errorf("report prompt contains %q from the fetched page", marker)
		}
	}
	for _, want := range []string{"Quantum computing basics", "Qubits can hold superpositions of states."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("report prompt lacks page body text %q", want)
		}
	}
}

func TestDeepResearchReportRetriesWithSnippetsOnHTML403(t *testing.T) {
	h := &deepResearchHarness{
		failReport: func(prompt string) error {
			if strings.Contains(prompt, "Detailed Sources") {
				return &llm.Error{StatusCode: http.StatusForbidden, Message: "<html><body>403 Forbidden</body></html>"}
			}
			return nil
		},
	}
	res := h.run(t)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want success from the snippet-only retry", res.Status, res.Message)
	}
	if len(h.reportPrompts) != 2 {
		t.Fatalf("report calls = %d, want 2", len(h.reportPrompts))
	}
	retry := h.reportPrompts[1]
	if strings.Contains(retry, "Detailed Sources") || strings.Contains(retry, "Quantum computing basics") {
		t.Errorf("retry prompt still carries fetched page content")
	}
	if !strings.Contains(retry, "Short snippet about qubits.") {
		t.Errorf("retry prompt lacks the search snippet")
	}
}

func TestDeepResearchReportDoesNotRetryWithoutFetchedPages(t *testing.T) {
	h := &deepResearchHarness{
		pageType: "application/pdf",
		failReport: func(string) error {
			return &llm.Error{StatusCode: http.StatusForbidden, Message: "<html><body>403 Forbidden</body></html>"}
		},
	}
	res := h.run(t)
	if res.Status == "success" {
		t.Fatalf("status = success, want error for an HTML 403")
	}
	if len(h.reportPrompts) != 1 {
		t.Fatalf("report calls = %d, want 1 (no fetched page, a retry would repeat the same prompt)", len(h.reportPrompts))
	}
	for _, marker := range rawMarkupMarkers {
		if strings.Contains(h.reportPrompts[0], marker) {
			t.Errorf("report prompt contains %q from a response that is not readable text", marker)
		}
	}
	if !strings.Contains(h.reportPrompts[0], "Short snippet about qubits.") {
		t.Errorf("report prompt lacks the search snippet")
	}
}

func TestDeepResearchReportDoesNotRetryOtherFailures(t *testing.T) {
	h := &deepResearchHarness{
		failReport: func(string) error {
			return &llm.Error{StatusCode: http.StatusInternalServerError, Message: "boom"}
		},
	}
	res := h.run(t)
	if res.Status != tool.StatusPartial {
		t.Fatalf("status = %q (%s), want partial so the findings survive a 500", res.Status, res.Message)
	}
	if len(h.reportPrompts) != 1 {
		t.Fatalf("report calls = %d, want 1 (no retry for non-HTML 4xx)", len(h.reportPrompts))
	}
	for _, want := range []string{"Quantum page", "Short snippet about qubits.", "provider returned an error"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message lacks %q: %s", want, res.Message)
		}
	}
	if got := res.Data["total_sources"]; got != 1 {
		t.Errorf("total_sources = %v, want 1", got)
	}
}

func TestDeepResearchReportAfterTimeoutRunsOnLiveContext(t *testing.T) {
	prev := minRuntimeSeconds
	minRuntimeSeconds = 1
	t.Cleanup(func() { minRuntimeSeconds = prev })

	h := &deepResearchHarness{maxIterations: 3, runtimeSecs: 1}
	res := h.run(t)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want a real report after the research time limit", res.Status, res.Message)
	}
	if len(h.reportPrompts) != 1 {
		t.Fatalf("report calls = %d, want 1", len(h.reportPrompts))
	}
	if h.reportCtxErrs[0] != nil {
		t.Errorf("report context error = %v, want a live context", h.reportCtxErrs[0])
	}
}

func TestGenerateReportWithFallbackKeepsEverySourceOnFirstAttempt(t *testing.T) {
	findings := fetchedFindings(15)
	for i := range findings {
		findings[i].Content = strings.Repeat("y", reportContentMaxChars)
	}
	var prompts []string
	llmCall := func(_ context.Context, _, prompt string, _ float64) (string, error) {
		prompts = append(prompts, prompt)
		return "report", nil
	}

	_, used, err := generateReportWithFallback(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), llmCall, "q", "t", findings)
	if err != nil {
		t.Fatalf("generateReportWithFallback: %v", err)
	}
	if len(prompts) != 1 || len(used) != len(findings) {
		t.Fatalf("calls = %d, used = %d, want 1 call using all %d sources", len(prompts), len(used), len(findings))
	}
	if len(prompts[0]) <= reportPayloadMaxBytes {
		t.Fatalf("prompt = %d bytes, want a healthy large run above the retry cap %d", len(prompts[0]), reportPayloadMaxBytes)
	}
	if got := strings.Count(prompts[0], "**Citation ID:**"); got != len(findings) {
		t.Errorf("prompt names %d sources, want %d", got, len(findings))
	}
}

func TestPlainTextOnlyConvertsRealMarkup(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"a < b and c > d", "a < b and c > d"},
		{"Use List<T> here", "Use List<T> here"},
		{"<p>Hello <b>world</b></p>", "Hello **world**"},
	}
	for _, tt := range tests {
		if got := strings.TrimSpace(plainText(tt.in)); got != tt.want {
			t.Errorf("plainText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDeepResearchReportDoesNotRetryHTMLRateLimit(t *testing.T) {
	h := &deepResearchHarness{
		failReport: func(string) error {
			return &llm.Error{StatusCode: http.StatusTooManyRequests, Message: "<html><body>Too many requests</body></html>"}
		},
	}
	res := h.run(t)
	if res.Status == "success" {
		t.Fatalf("status = success, want error for an HTML 429")
	}
	if len(h.reportPrompts) != 1 {
		t.Fatalf("report calls = %d, want 1 (an HTML 429 does not depend on the payload)", len(h.reportPrompts))
	}
}

func TestDeepResearchFetchPageContent(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		want        string
		wantErrPart string
	}{
		{
			name: "html without a declared type is sniffed and converted",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header()["Content-Type"] = nil
				_, _ = w.Write([]byte("<!DOCTYPE html><html><head><script>x()</script></head><body><p>Sniffed body text.</p></body></html>"))
			},
			want: "Sniffed body text.",
		},
		{
			name: "plain text is kept",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("Just plain words."))
			},
			want: "Just plain words.",
		},
		{
			name: "markdown is kept",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
				_, _ = w.Write([]byte("# Raw docs\nreadable markdown"))
			},
			want: "readable markdown",
		},
		{
			name: "csv is kept",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/csv")
				_, _ = w.Write([]byte("name,value\nqubit,2"))
			},
			want: "qubit,2",
		},
		{
			name: "json is kept",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"api":"reference"}`))
			},
			want: `"reference"`,
		},
		{
			name: "json variant is kept",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/ld+json")
				_, _ = w.Write([]byte(`{"a":"linked"}`))
			},
			want: `"linked"`,
		},
		{
			name: "xml is refused",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/xml")
				_, _ = w.Write([]byte(`<?xml version="1.0"?><a/>`))
			},
			wantErrPart: "unsupported content type",
		},
		{
			name: "feed is refused",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/rss+xml")
				_, _ = w.Write([]byte(`<?xml version="1.0"?><rss/>`))
			},
			wantErrPart: "unsupported content type",
		},
		{
			name: "javascript is refused",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/javascript")
				_, _ = w.Write([]byte(`var a = 1;`))
			},
			wantErrPart: "unsupported content type",
		},
		{
			name: "non-200 status is an error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantErrPart: "HTTP 404",
		},
		{
			name: "page without readable text is an error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<html><head><script>x()</script></head><body><script>y()</script></body></html>"))
			},
			wantErrPart: "no readable text",
		},
		{
			name: "truncated body is an error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.Header().Set("Content-Length", "1000")
				_, _ = w.Write([]byte("<html><body>cut"))
			},
			wantErrPart: "unexpected EOF",
		},
		{
			name: "pdf is refused",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/pdf")
				_, _ = w.Write([]byte("%PDF-1.4"))
			},
			wantErrPart: "unsupported content type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			got, err := fetchPageContent(t.Context(), srv.Client(), srv.URL)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchPageContent: %v", err)
			}
			if !strings.Contains(got, tt.want) || strings.Contains(got, "<") {
				t.Errorf("content = %q, want plain text containing %q", got, tt.want)
			}
		})
	}
}

func TestDeepResearchFetchPageContentRequestErrors(t *testing.T) {
	if _, err := fetchPageContent(t.Context(), http.DefaultClient, "http://bad host/"); err == nil {
		t.Error("invalid URL: want error")
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	target := srv.URL
	srv.Close()
	if _, err := fetchPageContent(t.Context(), http.DefaultClient, target); err == nil {
		t.Error("closed server: want error")
	}
}

func TestDeepResearchFetchSkipsNonTextContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 <?xml binary"))
	}))
	defer srv.Close()

	findings := []researchFinding{{URL: srv.URL, CitationID: "research0", Content: "snippet"}}
	(&DeepResearchTool{}).fetchSources(t.Context(), findings, []int{0}, findings, "", true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if findings[0].Fetched || findings[0].Content != "snippet" {
		t.Errorf("PDF response was stored: fetched=%v content=%q", findings[0].Fetched, findings[0].Content)
	}
}

func TestDeepResearchReportRetriesSmallerOnPlainProviderRejection(t *testing.T) {
	h := &deepResearchHarness{
		failReport: func(prompt string) error {
			if strings.Contains(prompt, "Detailed Sources") {
				return &llm.Error{StatusCode: http.StatusBadRequest, Message: "request blocked by content policy"}
			}
			return nil
		},
	}
	res := h.run(t)
	if res.Status != "success" {
		t.Fatalf("status = %q (%s), want success from the smaller retry", res.Status, res.Message)
	}
	if len(h.reportPrompts) != 2 {
		t.Fatalf("report calls = %d, want 2", len(h.reportPrompts))
	}
	if len(h.reportPrompts[1]) >= len(h.reportPrompts[0]) {
		t.Errorf("retry prompt is %d bytes, want smaller than the first at %d", len(h.reportPrompts[1]), len(h.reportPrompts[0]))
	}
}

func TestDeepResearchReportRejectedEverywhereReturnsGatheredSources(t *testing.T) {
	h := &deepResearchHarness{
		failReport: func(string) error {
			return &llm.Error{StatusCode: http.StatusBadRequest, Message: "request blocked by content policy"}
		},
	}
	res := h.run(t)
	if res.Status != tool.StatusPartial {
		t.Fatalf("status = %q (%s), want partial", res.Status, res.Message)
	}
	if res.ErrorCode != "" {
		t.Errorf("error code = %q, want none on a partial result", res.ErrorCode)
	}
	for _, want := range []string{"Quantum page", "Short snippet about qubits.", "could not be generated"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message lacks %q: %s", want, res.Message)
		}
	}
	if got := res.Data["total_sources"]; got != 1 {
		t.Errorf("total_sources = %v, want 1", got)
	}
}

func TestGenerateReportWithFallbackShrinksUntilAccepted(t *testing.T) {
	findings := fetchedFindings(12)
	var sourceCounts []int
	llmCall := func(_ context.Context, _, prompt string, _ float64) (string, error) {
		n := strings.Count(prompt, "**Citation ID:**")
		sourceCounts = append(sourceCounts, n)
		if n > 3 {
			return "", &llm.Error{StatusCode: http.StatusRequestEntityTooLarge, Message: "payload too large"}
		}
		return "report", nil
	}

	report, used, err := generateReportWithFallback(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), llmCall, "q", "t", findings)
	if err != nil {
		t.Fatalf("generateReportWithFallback: %v", err)
	}
	if report != "report" || len(used) != 3 {
		t.Errorf("report = %q with %d sources, want report with 3", report, len(used))
	}
	if want := []int{12, 8, 3}; !slices.Equal(sourceCounts, want) {
		t.Errorf("sources per attempt = %v, want %v", sourceCounts, want)
	}
	for _, f := range used {
		if f.Fetched || f.RelevanceScore < 0.45 {
			t.Errorf("kept source %s fetched=%v relevance=%v, want a top-ranked snippet", f.CitationID, f.Fetched, f.RelevanceScore)
		}
	}
}

func TestPrepareFindingsForReportCapsPayloadAndStripsSnippetMarkup(t *testing.T) {
	findings := make([]researchFinding, 15)
	for i := range findings {
		findings[i] = researchFinding{
			Title:      "Title <b>bold</b>",
			CitationID: fmt.Sprintf("research%d", i),
			Content:    strings.Repeat("x", 20000),
			Fetched:    true,
		}
	}
	findings = append(findings, researchFinding{
		Title:      "Snippet source",
		CitationID: "research99",
		Content:    `<div class="a"><script>evil()</script><p>Readable snippet.</p></div>`,
	})

	text, included := prepareFindingsWithinBudget(findings, reportPayloadMaxBytes)
	if len(text) > reportPayloadMaxBytes {
		t.Errorf("payload = %d bytes, want at most %d", len(text), reportPayloadMaxBytes)
	}
	if strings.Contains(text, "<b>") {
		t.Errorf("payload still carries title markup")
	}
	if got := strings.Count(text, "**Citation ID:**"); got != len(included) || got >= 15 {
		t.Errorf("payload names %d sources, included %d, want equal and fewer than 15 under the cap", got, len(included))
	}

	snippetText, _ := prepareFindingsForReport(findings[15:])
	if !strings.Contains(snippetText, "Readable snippet.") {
		t.Errorf("snippet text lost: %s", snippetText)
	}
	for _, marker := range []string{"<div", "<script", "evil()"} {
		if strings.Contains(snippetText, marker) {
			t.Errorf("snippet payload contains %q", marker)
		}
	}
}

func TestDeepResearchReportCallerCancelReturnsErrorNotPartial(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	h := &deepResearchHarness{
		parent: parent,
		failReport: func(string) error {
			cancel()
			return context.Canceled
		},
	}
	res := h.run(t)
	if res.Status != tool.StatusError {
		t.Fatalf("status = %q (%s), want error when the caller cancelled", res.Status, res.Message)
	}
}

func TestPartialResearchResultOnTimeoutSaysOutOfTime(t *testing.T) {
	findings := []researchFinding{{Title: "A", CitationID: "research0", URL: "https://example.test/a", Snippet: "gathered snippet", RelevanceScore: 0.5}}
	res := (&DeepResearchTool{}).partialResearchResult(t.Context(), &tool.Context{}, slog.New(slog.NewTextHandler(io.Discard, nil)), partialResearch{
		gaveUp:   &reportGaveUpError{attempts: 1, interrupted: true, cause: context.DeadlineExceeded},
		title:    "t",
		question: "q",
		findings: findings,
	})
	if res.Status != tool.StatusPartial {
		t.Fatalf("status = %q, want partial", res.Status)
	}
	if !strings.Contains(res.Message, "ran out of time") || strings.Contains(res.Message, "rejected") {
		t.Errorf("message = %q, want the out-of-time note and no rejection wording", res.Message)
	}
	if !strings.Contains(res.Message, "gathered snippet") {
		t.Errorf("message lacks the gathered snippet: %s", res.Message)
	}
}

func fetchedFindings(n int) []researchFinding {
	findings := make([]researchFinding, n)
	for i := range findings {
		findings[i] = researchFinding{
			Title:          fmt.Sprintf("Source %d", i),
			CitationID:     fmt.Sprintf("research%d", i),
			URL:            fmt.Sprintf("https://example.test/%d", i),
			Content:        strings.Repeat("page text ", 200),
			Snippet:        "snippet",
			RelevanceScore: float64(i) / 20,
			Fetched:        true,
		}
	}
	return findings
}

func TestGenerateReportWithFallbackLimitsContentFilterToOneShrink(t *testing.T) {
	tests := []struct {
		name      string
		rejection *llm.Error
		wantCalls int
	}{
		{"content filter", &llm.Error{StatusCode: http.StatusBadRequest, Message: "blocked by content filter"}, 2},
		{"size rejection", &llm.Error{StatusCode: http.StatusRequestEntityTooLarge, Message: "payload too large"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			llmCall := func(context.Context, string, string, float64) (string, error) {
				calls++
				return "", tt.rejection
			}
			_, _, err := generateReportWithFallback(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), llmCall, "q", "t", fetchedFindings(12))
			gaveUp, ok := errors.AsType[*reportGaveUpError](err)
			if !ok || gaveUp.interrupted {
				t.Fatalf("err = %v, want a give-up after rejection", err)
			}
			if calls != tt.wantCalls || gaveUp.attempts != tt.wantCalls {
				t.Errorf("calls = %d, attempts = %d, want %d", calls, gaveUp.attempts, tt.wantCalls)
			}
		})
	}
}

func TestPrepareFindingsWithinBudgetKeepsTopRankedAndSkipsOversized(t *testing.T) {
	findings := []researchFinding{
		{Title: "low", CitationID: "research0", Content: "small", RelevanceScore: 0.1},
		{Title: "huge", CitationID: "research1", Content: strings.Repeat("x", 5000), RelevanceScore: 0.9},
		{Title: "mid", CitationID: "research2", Content: "small", RelevanceScore: 0.5},
	}
	text, included := prepareFindingsWithinBudget(findings, 1000)
	got := make([]string, len(included))
	for i, f := range included {
		got[i] = f.CitationID
	}
	if want := []string{"research2", "research0"}; !slices.Equal(got, want) {
		t.Errorf("included = %v, want %v (oversized skipped, ranked order kept)", got, want)
	}
	if strings.Contains(text, "research1") {
		t.Errorf("payload carries the skipped source")
	}
}

func TestReportAttemptsSkipsReorderedIdenticalSet(t *testing.T) {
	findings := []researchFinding{
		{Title: "A", CitationID: "research0", URL: "https://example.test/a", Content: "a", RelevanceScore: 0.1},
		{Title: "B", CitationID: "research1", URL: "https://example.test/b", Content: "b", RelevanceScore: 0.9},
	}
	attempts := reportAttempts(findings)
	if len(attempts) != 1 {
		t.Errorf("attempts = %d, want 1: the top-ranked sets reorder the same two snippets", len(attempts))
	}
}
