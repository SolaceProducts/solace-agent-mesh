// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type testCSEServer struct {
	t              *testing.T
	mu             sync.Mutex
	capturedParams []url.Values
	capturedPaths  []string
	response       string
	responseStatus int
	server         *httptest.Server
}

func newTestCSEServer(t *testing.T, response string, status int) *testCSEServer {
	t.Helper()
	s := &testCSEServer{
		t:              t,
		response:       response,
		responseStatus: status,
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.capturedParams = append(s.capturedParams, r.URL.Query())
		s.capturedPaths = append(s.capturedPaths, r.URL.Path)
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.responseStatus)
		_, _ = w.Write([]byte(s.response))
	}))
	t.Cleanup(func() { s.server.Close() })
	return s
}

func (s *testCSEServer) baseURL() string { return s.server.URL }

func (s *testCSEServer) lastParams() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.capturedParams) == 0 {
		return nil
	}
	return s.capturedParams[len(s.capturedParams)-1]
}

func newWebSearchToolCtx(taskID string) *tool.Context {
	return &tool.Context{
		AgentName:    "test-agent",
		UserID:       "test-user",
		SessionID:    "test-session",
		ParentTaskID: taskID,
		ToolConfig: map[string]any{
			"google_search_api_key": "TEST_API_KEY",
			"google_cse_id":         "TEST_CX",
		},
	}
}

func captureRAGSignals(toolCtx *tool.Context) func() []a2a.RAGInfoUpdateData {
	var mu sync.Mutex
	var got []a2a.RAGInfoUpdateData
	toolCtx.SendSignal = func(signal any) error {
		if update, ok := signal.(a2a.RAGInfoUpdateData); ok {
			mu.Lock()
			got = append(got, update)
			mu.Unlock()
		}
		return nil
	}
	return func() []a2a.RAGInfoUpdateData {
		mu.Lock()
		defer mu.Unlock()
		return append([]a2a.RAGInfoUpdateData(nil), got...)
	}
}

func onlyRAGSignal(t *testing.T, signals func() []a2a.RAGInfoUpdateData) a2a.RAGInfoUpdateData {
	t.Helper()
	got := signals()
	if len(got) != 1 {
		t.Fatalf("rag_info_update count = %d, want exactly 1", len(got))
	}
	return got[0]
}

func citationIDsOf(sources []a2a.RAGSource) []string {
	ids := make([]string, 0, len(sources))
	for _, s := range sources {
		ids = append(ids, s.CitationID)
	}
	return ids
}

func sourcesByType(sources []a2a.RAGSource) (organic, images []a2a.RAGSource) {
	for _, s := range sources {
		if s.SourceType == "image" {
			images = append(images, s)
		} else {
			organic = append(organic, s)
		}
	}
	return organic, images
}

const cannedWebResponse = `{
	"items": [
		{
			"title": "Example Article",
			"link": "https://www.example.com/article",
			"snippet": "This is an example article snippet.",
			"pagemap": {"cse_thumbnail": [{"src": "https://example.com/thumb.jpg"}]}
		},
		{
			"title": "Second Result",
			"link": "https://blog.example.org/post",
			"snippet": "Second snippet body text."
		}
	]
}`

func TestWebSearch_MissingAPIBase(t *testing.T) {
	toolCtx := newWebSearchToolCtx("task-missing-api-base")

	tool := &GoogleWebSearchTool{}
	result, err := tool.Execute(context.Background(), map[string]any{"query": "anything"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if !strings.Contains(result.Message, "api_base") {
		t.Errorf("message = %q, want to mention api_base", result.Message)
	}
}

func TestWebSearch_QueryParamsMatchPython(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)

	cases := []struct {
		name     string
		args     map[string]any
		cfg      map[string]any
		wantKeys map[string]string
	}{
		{
			name: "basic query only",
			args: map[string]any{"query": "solace agent mesh"},
			wantKeys: map[string]string{
				"key": "TEST_API_KEY",
				"cx":  "TEST_CX",
				"q":   "solace agent mesh",
				"num": "5",
			},
		},
		{
			name: "all optional params set",
			args: map[string]any{
				"query":         "docker",
				"max_results":   float64(7),
				"search_type":   "image",
				"date_restrict": "d7",
				"safe_search":   "high",
			},
			wantKeys: map[string]string{
				"key":          "TEST_API_KEY",
				"cx":           "TEST_CX",
				"q":            "docker",
				"num":          "7",
				"searchType":   "image",
				"dateRestrict": "d7",
				"safe":         "high",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx := newWebSearchToolCtx("task-params-" + tc.name)
			toolCtx.ToolConfig["api_base"] = srv.baseURL()

			tool := &GoogleWebSearchTool{}
			_, err := tool.Execute(context.Background(), tc.args, toolCtx)
			if err != nil {
				t.Fatalf("Execute err = %v", err)
			}

			got := srv.lastParams()
			for k, want := range tc.wantKeys {
				if got.Get(k) != want {
					t.Errorf("param %q: got %q, want %q", k, got.Get(k), want)
				}
			}

			allowed := map[string]bool{
				"key": true, "cx": true, "q": true, "num": true,
				"searchType": true, "dateRestrict": true, "safe": true,
			}
			for k := range got {
				if !allowed[k] {
					t.Errorf("unexpected query param %q sent to Google CSE (not in Python's param set)", k)
				}
			}
		})
	}
}

func TestWebSearch_HTTPPathMatchesPython(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)

	toolCtx := newWebSearchToolCtx("task-path")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()

	tool := &GoogleWebSearchTool{}
	if _, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx); err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	srv.mu.Lock()
	paths := srv.capturedPaths
	srv.mu.Unlock()
	if len(paths) == 0 || paths[0] != "/customsearch/v1" {
		t.Errorf("path = %v, want [/customsearch/v1]", paths)
	}
}

func TestWebSearch_NumCapAt10(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)

	cases := []struct {
		name       string
		request    int
		expectSent string
	}{
		{"exactly 10 passes through", 10, "10"},
		{"11 is capped to 10", 11, "10"},
		{"50 is capped to 10", 50, "10"},
		{"5 passes through", 5, "5"},
		{"1 passes through", 1, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx := newWebSearchToolCtx("task-cap-" + tc.name)
			toolCtx.ToolConfig["api_base"] = srv.baseURL()
			tool := &GoogleWebSearchTool{}
			_, err := tool.Execute(context.Background(), map[string]any{
				"query":       "test",
				"max_results": float64(tc.request),
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute err = %v", err)
			}
			if got := srv.lastParams().Get("num"); got != tc.expectSent {
				t.Errorf("num sent = %q, want %q (request was %d)", got, tc.expectSent, tc.request)
			}
		})
	}
}

func TestWebSearch_MissingMaxResultsUsesDefault(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-default")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	tool := &GoogleWebSearchTool{}
	if _, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx); err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if got := srv.lastParams().Get("num"); got != "5" {
		t.Errorf("default num = %q, want %q", got, "5")
	}
}

func TestWebSearch_OrganicAndImageSplit(t *testing.T) {
	t.Run("web search returns only organic", func(t *testing.T) {
		srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
		toolCtx := newWebSearchToolCtx("task-web")
		toolCtx.ToolConfig["api_base"] = srv.baseURL()
		signals := captureRAGSignals(toolCtx)
		tool := &GoogleWebSearchTool{}
		r, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if r.Data["num_results"] != 2 {
			t.Errorf("num_results = %v, want 2", r.Data["num_results"])
		}
		organic, images := sourcesByType(onlyRAGSignal(t, signals).Sources)
		if len(organic) != 2 {
			t.Errorf("organic count = %d, want 2", len(organic))
		}
		if len(images) != 0 {
			t.Errorf("images count = %d, want 0", len(images))
		}
	})

	t.Run("image search returns only images", func(t *testing.T) {
		imageResponse := `{
			"items": [
				{
					"title": "Cat Photo",
					"link": "https://img.example.com/cat.jpg",
					"image": {"contextLink": "https://example.com/cat-post"}
				},
				{
					"title": "Dog Photo",
					"link": "https://img.example.com/dog.jpg",
					"image": {"contextLink": "https://example.com/dog-post"}
				}
			]
		}`
		srv := newTestCSEServer(t, imageResponse, http.StatusOK)
		toolCtx := newWebSearchToolCtx("task-img")
		toolCtx.ToolConfig["api_base"] = srv.baseURL()
		signals := captureRAGSignals(toolCtx)
		tool := &GoogleWebSearchTool{}
		r, err := tool.Execute(context.Background(), map[string]any{
			"query":       "cat",
			"search_type": "image",
		}, toolCtx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if r.Data["num_results"] != 2 {
			t.Errorf("num_results = %v, want 2 (both images counted)", r.Data["num_results"])
		}
		formatted, _ := r.Data["formatted_results"].(string)
		if strings.Contains(formatted, "No results found") {
			t.Errorf("image search reported no results to the model:\n%s", formatted)
		}
		for _, want := range []string{"IMAGE RESULTS", "2 image(s) found"} {
			if !strings.Contains(formatted, want) {
				t.Errorf("formatted_results missing %q:\n%s", want, formatted)
			}
		}
		if strings.Contains(formatted, "[[cite:") {
			t.Errorf("image section emits citation markers the UI cannot render:\n%s", formatted)
		}
		organic, images := sourcesByType(onlyRAGSignal(t, signals).Sources)
		if len(organic) != 0 {
			t.Errorf("organic count = %d, want 0 (image search)", len(organic))
		}
		if len(images) != 2 {
			t.Fatalf("images count = %d, want 2", len(images))
		}
		first := images[0]
		if got, _ := first.Metadata["imageUrl"].(string); got != "https://img.example.com/cat.jpg" {
			t.Errorf("image[0].metadata.imageUrl = %v, want cat.jpg URL", got)
		}
		if first.URL != "https://example.com/cat-post" {
			t.Errorf("image[0].url = %v, want cat-post URL", first.URL)
		}
		if first.Title != "Cat Photo" {
			t.Errorf("image[0].title = %v, want Cat Photo", first.Title)
		}
	})

	t.Run("image result falls back to link when contextLink missing", func(t *testing.T) {
		response := `{"items":[{"title":"X","link":"https://img.example.com/x.jpg"}]}`
		srv := newTestCSEServer(t, response, http.StatusOK)
		toolCtx := newWebSearchToolCtx("task-img-fb")
		toolCtx.ToolConfig["api_base"] = srv.baseURL()
		signals := captureRAGSignals(toolCtx)
		tool := &GoogleWebSearchTool{}
		if _, err := tool.Execute(context.Background(), map[string]any{"query": "x", "search_type": "image"}, toolCtx); err != nil {
			t.Fatalf("err: %v", err)
		}
		_, images := sourcesByType(onlyRAGSignal(t, signals).Sources)
		if len(images) != 1 {
			t.Fatalf("images count = %d, want 1", len(images))
		}
		if images[0].URL != "https://img.example.com/x.jpg" {
			t.Errorf("image[0].url = %v, want link fallback when contextLink absent", images[0].URL)
		}
	})
}

func TestWebSearch_ThumbnailExtraction(t *testing.T) {
	cases := []struct {
		name     string
		response string
		wantImg  string
	}{
		{
			name: "thumbnail present",
			response: `{"items":[{"title":"T","link":"https://a.com/p","snippet":"s",
				"pagemap":{"cse_thumbnail":[{"src":"https://a.com/t.jpg","width":"200"}]}}]}`,
			wantImg: "https://a.com/t.jpg",
		},
		{
			name:     "pagemap missing",
			response: `{"items":[{"title":"T","link":"https://a.com/p","snippet":"s"}]}`,
			wantImg:  "",
		},
		{
			name:     "pagemap present but cse_thumbnail absent",
			response: `{"items":[{"title":"T","link":"https://a.com/p","snippet":"s","pagemap":{}}]}`,
			wantImg:  "",
		},
		{
			name:     "cse_thumbnail empty array",
			response: `{"items":[{"title":"T","link":"https://a.com/p","snippet":"s","pagemap":{"cse_thumbnail":[]}}]}`,
			wantImg:  "",
		},
		{
			name: "first thumbnail of many is used",
			response: `{"items":[{"title":"T","link":"https://a.com/p","snippet":"s",
				"pagemap":{"cse_thumbnail":[{"src":"https://a.com/first.jpg"},{"src":"https://a.com/second.jpg"}]}}]}`,
			wantImg: "https://a.com/first.jpg",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestCSEServer(t, tc.response, http.StatusOK)
			provider := NewGoogleSearchProvider("TEST_API_KEY", "TEST_CX", srv.baseURL())
			results, err := provider.Search(context.Background(), "x", 5, SearchOptions{})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if len(results) == 0 {
				t.Fatal("no organic results")
			}
			if results[0].ImageURL != tc.wantImg {
				t.Errorf("imageUrl = %q, want %q", results[0].ImageURL, tc.wantImg)
			}
		})
	}
}

func TestCleanHTMLSnippet(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text unchanged", "hello world", "hello world"},
		{"strips simple tags", "<b>bold</b> text", "bold text"},
		{"unescapes then strips", "&lt;b&gt;quoted&lt;/b&gt; body", "quoted body"},
		{"strips multiple tags", `<a href="x">link</a> and <em>em</em>`, "link and em"},
		{"handles entities", "Tom &amp; Jerry", "Tom & Jerry"},
		{"nested tags", "<div><span>inner</span></div>", "inner"},
		{"empty string", "", ""},
		{"self closing tags", "line one<br/>line two", "line oneline two"},
		{"script wrapped entity", "&lt;script&gt;alert(1)&lt;/script&gt;done", "alert(1)done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanHTMLSnippet(tc.in)
			if got != tc.want {
				t.Errorf("cleanHTMLSnippet(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWebSearch_SnippetFallbackToHTMLSnippet(t *testing.T) {
	response := `{"items":[{
		"title":"T",
		"link":"https://a.com/p",
		"htmlSnippet":"&lt;b&gt;bold&lt;/b&gt; <span>content</span>"
	}]}`
	srv := newTestCSEServer(t, response, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-htmlsnip")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	tool := &GoogleWebSearchTool{}
	r, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	formatted, _ := r.Data["formatted_results"].(string)
	want := "CONTENT: bold content"
	if !strings.Contains(formatted, want) {
		t.Errorf("formatted_results missing %q\n  got:\n%s", want, formatted)
	}
}

func TestWebSearch_RAGInfoUpdateSignalShape(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-ragshape")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	signals := captureRAGSignals(toolCtx)

	tool := &GoogleWebSearchTool{}
	if _, err := tool.Execute(context.Background(), map[string]any{"query": "solace"}, toolCtx); err != nil {
		t.Fatalf("err: %v", err)
	}

	update := onlyRAGSignal(t, signals)

	if update.Type != string(a2a.SignalRAGInfoUpdate) {
		t.Errorf("type = %q, want %q", update.Type, a2a.SignalRAGInfoUpdate)
	}
	if update.SearchType != "web_search" {
		t.Errorf("searchType = %v, want %q", update.SearchType, "web_search")
	}
	if update.Query != "solace" {
		t.Errorf("query = %v, want %q", update.Query, "solace")
	}
	if !update.IsComplete {
		t.Error("isComplete = false, want true (a web search finishes in one shot)")
	}
	if update.Timestamp == "" {
		t.Error("timestamp should be a non-empty ISO 8601 string")
	}

	envelope, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal signal: %v", err)
	}
	for _, want := range []string{`"type":"rag_info_update"`, `"search_type":"web_search"`, `"is_complete":true`} {
		if !strings.Contains(string(envelope), want) {
			t.Errorf("signal JSON missing %q\n  full JSON: %s", want, envelope)
		}
	}

	b, _ := json.Marshal(update.Sources)
	js := string(b)
	for _, want := range []string{
		`"citationId":"s0r0"`,
		`"fileId":"web_search_0_0"`,
		`"sourceType":"web"`,
		`"sourceUrl":"https://www.example.com/article"`,
		`"contentPreview":"This is an example article snippet."`,
		`"relevanceScore":1`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("sources JSON missing %q\n  full JSON: %s", want, js)
		}
	}
	var decodedSources []map[string]any
	if err := json.Unmarshal(b, &decodedSources); err != nil {
		t.Fatalf("decode sources: %v", err)
	}
	if len(decodedSources) < 1 {
		t.Fatalf("decoded sources empty")
	}
	md0, _ := decodedSources[0]["metadata"].(map[string]any)
	gotFavicon, _ := md0["favicon"].(string)
	wantFavicon := "https://www.google.com/s2/favicons?domain=https://www.example.com/article&sz=32"
	if gotFavicon != wantFavicon {
		t.Errorf("favicon = %q, want %q", gotFavicon, wantFavicon)
	}
}

func TestWebSearch_CitationIDFormats(t *testing.T) {
	t.Run("web citation format", func(t *testing.T) {
		srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
		toolCtx := newWebSearchToolCtx("task-cite-web")
		toolCtx.ToolConfig["api_base"] = srv.baseURL()
		signals := captureRAGSignals(toolCtx)
		tool := &GoogleWebSearchTool{}
		if _, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx); err != nil {
			t.Fatalf("err: %v", err)
		}
		organic, _ := sourcesByType(onlyRAGSignal(t, signals).Sources)
		ids := citationIDsOf(organic)
		want := []string{"s0r0", "s0r1"}
		if len(ids) != len(want) {
			t.Fatalf("ids len %d, want %d", len(ids), len(want))
		}
		for i := range ids {
			if ids[i] != want[i] {
				t.Errorf("ids[%d] = %q, want %q", i, ids[i], want[i])
			}
		}
	})

	t.Run("image citation format", func(t *testing.T) {
		imageResponse := `{"items":[
			{"title":"a","link":"https://img.example.com/a.jpg","image":{"contextLink":"https://example.com/a"}},
			{"title":"b","link":"https://img.example.com/b.jpg","image":{"contextLink":"https://example.com/b"}}
		]}`
		srv := newTestCSEServer(t, imageResponse, http.StatusOK)
		toolCtx := newWebSearchToolCtx("task-cite-img")
		toolCtx.ToolConfig["api_base"] = srv.baseURL()
		signals := captureRAGSignals(toolCtx)
		tool := &GoogleWebSearchTool{}
		if _, err := tool.Execute(context.Background(), map[string]any{"query": "x", "search_type": "image"}, toolCtx); err != nil {
			t.Fatalf("err: %v", err)
		}
		update := onlyRAGSignal(t, signals)
		organic, images := sourcesByType(update.Sources)
		if len(organic) != 0 {
			t.Errorf("organic sources should be empty for image search, got %v", citationIDsOf(organic))
		}
		if got := citationIDsOf(images); len(got) != 2 || got[0] != "img0r0" || got[1] != "img0r1" {
			t.Errorf("image citation IDs = %v, want [img0r0 img0r1]", got)
		}
	})
}

func TestWebSearch_TurnCounterIncrementsPerTask(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)

	ws := &GoogleWebSearchTool{}

	t.Run("same task increments", func(t *testing.T) {
		toolCtx := newWebSearchToolCtx("task-turn")
		toolCtx.ToolConfig["api_base"] = srv.baseURL()
		signals := captureRAGSignals(toolCtx)
		for i := range 3 {
			r, err := ws.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
			if err != nil {
				t.Fatalf("call %d err: %v", i, err)
			}
			if r.Data["search_turn"] != i {
				t.Errorf("call %d: search_turn = %v, want %d", i, r.Data["search_turn"], i)
			}
			emitted := signals()
			if len(emitted) != i+1 {
				t.Fatalf("call %d: rag_info_update count = %d, want %d", i, len(emitted), i+1)
			}
			ids := citationIDsOf(emitted[i].Sources)
			if len(ids) == 0 {
				t.Fatalf("call %d: no citation IDs emitted", i)
			}
			wantPrefix := fmt.Sprintf("s%dr", i)
			for _, id := range ids {
				if !strings.HasPrefix(id, wantPrefix) {
					t.Errorf("call %d: citation %q lacks prefix %q", i, id, wantPrefix)
				}
			}
		}
	})

	t.Run("different tasks have independent counters", func(t *testing.T) {
		ws2 := &GoogleWebSearchTool{}
		ctxA := newWebSearchToolCtx("task-A")
		ctxA.ToolConfig["api_base"] = srv.baseURL()
		ctxB := newWebSearchToolCtx("task-B")
		ctxB.ToolConfig["api_base"] = srv.baseURL()

		rA1, _ := ws2.Execute(context.Background(), map[string]any{"query": "x"}, ctxA)
		rB1, _ := ws2.Execute(context.Background(), map[string]any{"query": "x"}, ctxB)
		rA2, _ := ws2.Execute(context.Background(), map[string]any{"query": "x"}, ctxA)

		if rA1.Data["search_turn"] != 0 {
			t.Errorf("task A first: %v, want 0", rA1.Data["search_turn"])
		}
		if rB1.Data["search_turn"] != 0 {
			t.Errorf("task B first: %v, want 0", rB1.Data["search_turn"])
		}
		if rA2.Data["search_turn"] != 1 {
			t.Errorf("task A second: %v, want 1", rA2.Data["search_turn"])
		}
	})
}

func TestWebSearch_APIErrorReturnsToolError(t *testing.T) {
	errResponse := `{"error":{"code":403,"message":"Daily Limit Exceeded"}}`
	srv := newTestCSEServer(t, errResponse, http.StatusForbidden)

	toolCtx := newWebSearchToolCtx("task-err")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	tool := &GoogleWebSearchTool{}
	r, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v (expected nil; error should be in tool.Result)", err)
	}
	if r.Status != "error" {
		t.Errorf("Status = %q, want %q", r.Status, "error")
	}
	if !strings.Contains(r.Message, "Daily Limit Exceeded") {
		t.Errorf("error message missing API message: %q", r.Message)
	}
	if r.ErrorCode != taxonomy.ToolFailurePermissionDenied {
		t.Errorf("ErrorCode = %q, want %q for a 403 without a quota reason", r.ErrorCode, taxonomy.ToolFailurePermissionDenied)
	}
}

func TestWebSearch_APIErrorCodeByStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantText string
		wantCode string
	}{
		{
			name:     "server error",
			status:   http.StatusServiceUnavailable,
			body:     `{"error":{"code":503,"message":"Backend Error"}}`,
			wantText: "Web search failed: google search API error (503): Backend Error",
			wantCode: "DISPATCH_BACKEND_5XX",
		},
		{
			name:     "403 quota reason",
			status:   http.StatusForbidden,
			body:     `{"error":{"code":403,"message":"Daily Limit Exceeded","errors":[{"domain":"usageLimits","reason":"dailyLimitExceeded","message":"Daily Limit Exceeded"}]}}`,
			wantText: "Web search failed: google search API error (403): Daily Limit Exceeded",
			wantCode: taxonomy.ToolFailureRateLimited,
		},
		{
			name:     "403 other reason",
			status:   http.StatusForbidden,
			body:     `{"error":{"code":403,"message":"Requests are blocked","errors":[{"domain":"global","reason":"forbidden","message":"Requests are blocked"}]}}`,
			wantText: "Web search failed: google search API error (403): Requests are blocked",
			wantCode: taxonomy.ToolFailurePermissionDenied,
		},
		{
			name:     "bad API key",
			status:   http.StatusBadRequest,
			body:     `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","errors":[{"message":"API key not valid. Please pass a valid API key.","domain":"global","reason":"badRequest"}],"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com","metadata":{"service":"customsearch.googleapis.com"}}]}}`,
			wantText: "Web search failed: google search API error (400): API key not valid. Please pass a valid API key.",
			wantCode: "DISPATCH_AUTH_REQUIRED",
		},
		{
			name:     "other 400",
			status:   http.StatusBadRequest,
			body:     `{"error":{"code":400,"message":"Invalid Value","errors":[{"message":"Invalid Value","domain":"global","reason":"invalid"}],"status":"INVALID_ARGUMENT"}}`,
			wantText: "Web search failed: google search API error (400): Invalid Value",
			wantCode: "DISPATCH_BACKEND_4XX",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestCSEServer(t, tt.body, tt.status)
			toolCtx := newWebSearchToolCtx("task-code-" + tt.name)
			toolCtx.ToolConfig["api_base"] = srv.baseURL()
			r, err := (&GoogleWebSearchTool{}).Execute(t.Context(), map[string]any{"query": "x"}, toolCtx)
			if err != nil {
				t.Fatalf("Execute err = %v", err)
			}
			if r.Status != "error" {
				t.Fatalf("Status = %q, want error", r.Status)
			}
			if r.Message != tt.wantText {
				t.Errorf("Message = %q, want %q", r.Message, tt.wantText)
			}
			if r.ErrorCode != tt.wantCode {
				t.Errorf("ErrorCode = %q, want %q", r.ErrorCode, tt.wantCode)
			}
		})
	}
}

func TestWebSearch_EmptyItemsReturnsZeroResults(t *testing.T) {
	srv := newTestCSEServer(t, `{"items":[]}`, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-empty")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	signals := captureRAGSignals(toolCtx)
	tool := &GoogleWebSearchTool{}
	r, err := tool.Execute(context.Background(), map[string]any{"query": "rare term"}, toolCtx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Status != "success" {
		t.Errorf("Status = %q, want success", r.Status)
	}
	if r.Data["num_results"] != 0 {
		t.Errorf("num_results = %v, want 0", r.Data["num_results"])
	}
	if got := onlyRAGSignal(t, signals).Sources; len(got) != 0 {
		t.Errorf("sources len = %d, want 0", len(got))
	}
}

func TestWebSearch_RequiresQuery(t *testing.T) {
	toolCtx := newWebSearchToolCtx("task-validate")
	tool := &GoogleWebSearchTool{}
	r, err := tool.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
}

func TestWebSearch_RequiresCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"missing api key", map[string]any{"google_cse_id": "cx"}},
		{"missing cse id", map[string]any{"google_search_api_key": "key"}},
		{"both missing", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx := &tool.Context{
				AgentName:    "a",
				UserID:       "u",
				SessionID:    "s",
				ParentTaskID: "t",
				ToolConfig:   tc.cfg,
			}
			tool := &GoogleWebSearchTool{}
			r, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if r.Status != "error" {
				t.Errorf("Status = %q, want error", r.Status)
			}
		})
	}
}

func TestExtractDomain(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://www.example.com/path", "example.com"},
		{"http://example.com", "example.com"},
		{"https://blog.example.org/a/b/c", "blog.example.org"},
		{"https://www.sub.domain.co.uk/x", "sub.domain.co.uk"},
		{"not a url", ""},
		{"https://example.com:8080/x", "example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := extractDomain(tc.in)
			if got != tc.want {
				t.Errorf("extractDomain(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWebSearch_ToolNameRenameDocumented(t *testing.T) {
	ws := &GoogleWebSearchTool{}
	if ws.Name() != "web_search_google" {
		t.Errorf("Go tool name = %q, want %q (must match Python's `web_search_google`)", ws.Name(), "web_search_google")
	}
}

func TestFormatSearchResultsText(t *testing.T) {
	results := []SearchResult{
		{Title: "First", Link: "https://a.com/1", Snippet: "snippet one", Type: "web"},
		{Title: "Second", Link: "https://b.com/2", Snippet: "snippet two", Type: "web"},
	}
	text := formatSearchResultsText(results, 3, "demo query")

	mustContain := []string{
		"=== SEARCH RESULTS (Turn 3) ===",
		"Query: demo query",
		"Valid citation IDs: s3r0, s3r1",
		"--- RESULT 1 ---",
		"CITATION ID: [[cite:s3r0]]",
		"TITLE: First",
		"URL: https://a.com/1",
		"CONTENT: snippet one",
		"USE [[cite:s3r0]] to cite facts from THIS result only",
		"--- RESULT 2 ---",
		"CITATION ID: [[cite:s3r1]]",
		"=== END SEARCH RESULTS ===",
		"IMPORTANT: Each citation ID is UNIQUE",
	}
	for _, s := range mustContain {
		if !strings.Contains(text, s) {
			t.Errorf("formatted text missing %q\n  got:\n%s", s, text)
		}
	}
}

func TestFormatSearchResultsText_Empty(t *testing.T) {
	text := formatSearchResultsText(nil, 0, "nothing")
	if !strings.Contains(text, "No results found for query: nothing") {
		t.Errorf("empty format text = %q", text)
	}
}

func TestFormatCitationIDHelpers(t *testing.T) {
	if got := formatCitationID(0, 0); got != "s0r0" {
		t.Errorf("formatCitationID(0, 0) = %q, want %q", got, "s0r0")
	}
	if got := formatCitationID(5, 7); got != "s5r7" {
		t.Errorf("formatCitationID(5, 7) = %q, want %q", got, "s5r7")
	}
	if got := formatImageCitationID(2, 3); got != "img2r3" {
		t.Errorf("formatImageCitationID(2, 3) = %q, want %q", got, "img2r3")
	}
}

func TestWebSearch_QueryURLEncoded(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-encode")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()

	query := "golang & python: naïve search"
	tool := &GoogleWebSearchTool{}
	_, err := tool.Execute(context.Background(), map[string]any{"query": query}, toolCtx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got := srv.lastParams().Get("q"); got != query {
		t.Errorf("decoded q = %q, want %q", got, query)
	}
}

const quotaErrorResponse = `{"error":{"code":429,"message":"Quota exceeded for quota metric 'Queries'"}}`

func newCountingCSEServer(t *testing.T, handle func(attempt int, w http.ResponseWriter)) (string, func() int) {
	t.Helper()
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		attempt := requests
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		handle(attempt, w)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}
}

func withQuotaRetryBackoff(t *testing.T, schedule ...time.Duration) {
	t.Helper()
	original := searchQuotaRetryBackoff
	searchQuotaRetryBackoff = schedule
	t.Cleanup(func() { searchQuotaRetryBackoff = original })
}

func withFastQuotaRetryBackoff(t *testing.T) {
	t.Helper()
	fast := make([]time.Duration, len(searchQuotaRetryBackoff))
	for i := range fast {
		fast[i] = time.Microsecond
	}
	withQuotaRetryBackoff(t, fast...)
}

func TestWebSearch_QuotaRetryScheduleIsThreeGrowingWaits(t *testing.T) {
	want := []time.Duration{1 * time.Second, 3 * time.Second, 8 * time.Second}
	if len(searchQuotaRetryBackoff) != len(want) {
		t.Fatalf("backoff schedule = %v, want %v", searchQuotaRetryBackoff, want)
	}
	for i, w := range want {
		if searchQuotaRetryBackoff[i] != w {
			t.Errorf("backoff[%d] = %v, want %v", i, searchQuotaRetryBackoff[i], w)
		}
	}
}

func TestWebSearch_RetriesOn429ThenSucceeds(t *testing.T) {
	withFastQuotaRetryBackoff(t)

	baseURL, requests := newCountingCSEServer(t, func(attempt int, w http.ResponseWriter) {
		if attempt <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(quotaErrorResponse))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(cannedWebResponse))
	})

	toolCtx := newWebSearchToolCtx("task-429-recover")
	toolCtx.ToolConfig["api_base"] = baseURL
	signals := captureRAGSignals(toolCtx)
	tl := &GoogleWebSearchTool{}
	r, err := tl.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if r.Status != "success" {
		t.Fatalf("Status = %q (%s), want success", r.Status, r.Message)
	}
	if r.Data["num_results"] != 2 {
		t.Errorf("num_results = %v, want 2", r.Data["num_results"])
	}
	if got := requests(); got != 3 {
		t.Errorf("request count = %d, want 3 (two 429s then success)", got)
	}

	organic, _ := sourcesByType(onlyRAGSignal(t, signals).Sources)
	if len(organic) != 2 {
		t.Fatalf("organic count = %d, want 2", len(organic))
	}
	if organic[0].Title != "Example Article" {
		t.Errorf("organic[0].title = %v, want Example Article", organic[0].Title)
	}
	if organic[0].URL != "https://www.example.com/article" {
		t.Errorf("organic[0].url = %v, want article URL", organic[0].URL)
	}
}

func TestWebSearch_429ExhaustsBackoffSchedule(t *testing.T) {
	withFastQuotaRetryBackoff(t)

	baseURL, requests := newCountingCSEServer(t, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(quotaErrorResponse))
	})

	toolCtx := newWebSearchToolCtx("task-429-exhausted")
	toolCtx.ToolConfig["api_base"] = baseURL
	tl := &GoogleWebSearchTool{}
	r, err := tl.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if !strings.Contains(r.Message, "429") {
		t.Errorf("message = %q, want it to mention 429", r.Message)
	}
	if r.ErrorCode != taxonomy.ToolFailureRateLimited {
		t.Errorf("ErrorCode = %q, want %q", r.ErrorCode, taxonomy.ToolFailureRateLimited)
	}
	if got := requests(); got != 4 {
		t.Errorf("request count = %d, want 4 (initial attempt + the 3 scheduled retries)", got)
	}
}

func TestWebSearch_Non429StatusNotRetried(t *testing.T) {
	withFastQuotaRetryBackoff(t)

	baseURL, requests := newCountingCSEServer(t, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Daily Limit Exceeded"}}`))
	})

	toolCtx := newWebSearchToolCtx("task-403-no-retry")
	toolCtx.ToolConfig["api_base"] = baseURL
	tl := &GoogleWebSearchTool{}
	r, err := tl.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if !strings.Contains(r.Message, "Daily Limit Exceeded") {
		t.Errorf("message = %q, want the API message", r.Message)
	}
	if got := requests(); got != 1 {
		t.Errorf("request count = %d, want exactly 1 (403 must not be retried)", got)
	}
}

func TestWebSearch_429BackoffHonoursContextCancellation(t *testing.T) {
	withQuotaRetryBackoff(t, 2*time.Second, 2*time.Second, 2*time.Second)

	baseURL, requests := newCountingCSEServer(t, func(_ int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(quotaErrorResponse))
	})

	toolCtx := newWebSearchToolCtx("task-429-cancelled")
	toolCtx.ToolConfig["api_base"] = baseURL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(25*time.Millisecond, cancel)

	tl := &GoogleWebSearchTool{}
	start := time.Now()
	r, err := tl.Execute(ctx, map[string]any{"query": "x"}, toolCtx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if !strings.Contains(r.Message, context.Canceled.Error()) {
		t.Errorf("message = %q, want it to mention %q", r.Message, context.Canceled.Error())
	}
	if elapsed > time.Second {
		t.Errorf("elapsed = %v, want the wait abandoned well before the 2s backoff", elapsed)
	}
	if got := requests(); got != 1 {
		t.Errorf("request count = %d, want 1 (no retry after cancellation)", got)
	}
}

func TestWebSearch_EmitsRAGInfoUpdateSignalOncePerSearch(t *testing.T) {
	mixedResponse := `{
		"items": [
			{"title": "Article One", "link": "https://a.example.com/1", "snippet": "first"},
			{"title": "Article Two", "link": "https://b.example.com/2", "snippet": "second"}
		]
	}`
	srv := newTestCSEServer(t, mixedResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-signal-once")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	signals := captureRAGSignals(toolCtx)

	tool := &GoogleWebSearchTool{}
	if _, err := tool.Execute(context.Background(), map[string]any{"query": "solace mesh"}, toolCtx); err != nil {
		t.Fatalf("Execute err = %v", err)
	}

	update := onlyRAGSignal(t, signals)
	if update.Query != "solace mesh" {
		t.Errorf("query = %q, want %q", update.Query, "solace mesh")
	}
	if update.SearchType != "web_search" {
		t.Errorf("searchType = %q, want %q", update.SearchType, "web_search")
	}
	if !update.IsComplete {
		t.Error("isComplete = false, want true")
	}
	if got := citationIDsOf(update.Sources); len(got) != 2 || got[0] != "s0r0" || got[1] != "s0r1" {
		t.Fatalf("citation IDs = %v, want [s0r0 s0r1]", got)
	}
	if update.Sources[0].Title != "Article One" || update.Sources[0].URL != "https://a.example.com/1" {
		t.Errorf("sources[0] = {%q, %q}, want {Article One, https://a.example.com/1}", update.Sources[0].Title, update.Sources[0].URL)
	}
	if update.Sources[0].ContentPreview != "first" {
		t.Errorf("sources[0].contentPreview = %q, want %q", update.Sources[0].ContentPreview, "first")
	}
}

func TestWebSearch_SignalCarriesImageSourcesToo(t *testing.T) {
	imageResponse := `{"items":[
		{"title":"Cat","link":"https://img.example.com/cat.jpg","image":{"contextLink":"https://example.com/cat"}}
	]}`
	srv := newTestCSEServer(t, imageResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-signal-img")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	signals := captureRAGSignals(toolCtx)

	tool := &GoogleWebSearchTool{}
	if _, err := tool.Execute(context.Background(), map[string]any{"query": "cat", "search_type": "image"}, toolCtx); err != nil {
		t.Fatalf("Execute err = %v", err)
	}

	update := onlyRAGSignal(t, signals)
	if update.SearchType != "web_search" {
		t.Errorf("searchType = %q, want %q (image is a mode of web_search)", update.SearchType, "web_search")
	}
	if got := citationIDsOf(update.Sources); len(got) != 1 || got[0] != "img0r0" {
		t.Errorf("citation IDs = %v, want [img0r0]", got)
	}
}

func TestWebSearch_ResultOmitsCitationPayloads(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-slim-result")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	captureRAGSignals(toolCtx)

	tool := &GoogleWebSearchTool{}
	r, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}

	for _, key := range []string{"rag_metadata", "result", "valid_citation_ids"} {
		if v, present := r.Data[key]; present {
			t.Errorf("tool result still carries %q = %v; it belongs on the rag_info_update signal", key, v)
		}
	}

	wantKeys := map[string]bool{"formatted_results": true, "num_results": true, "search_turn": true}
	for key := range r.Data {
		if !wantKeys[key] {
			t.Errorf("unexpected key %q in tool result", key)
		}
	}
}

func TestWebSearch_ResultKeepsFormattedResults(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-formatted")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	captureRAGSignals(toolCtx)

	tool := &GoogleWebSearchTool{}
	r, err := tool.Execute(context.Background(), map[string]any{"query": "solace"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}

	formatted, ok := r.Data["formatted_results"].(string)
	if !ok {
		t.Fatalf("formatted_results is not a string: %T", r.Data["formatted_results"])
	}
	for _, want := range []string{
		"=== SEARCH RESULTS (Turn 0) ===",
		"Query: solace",
		"Valid citation IDs: s0r0, s0r1",
		"CITATION ID: [[cite:s0r0]]",
		"TITLE: Example Article",
		"URL: https://www.example.com/article",
		"CONTENT: This is an example article snippet.",
		"=== END SEARCH RESULTS ===",
	} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatted_results missing %q\n  got:\n%s", want, formatted)
		}
	}
}

func TestWebSearch_NilSendSignalDoesNotPanic(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-nil-signal")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	if toolCtx.SendSignal != nil {
		t.Fatal("test precondition: SendSignal must be nil")
	}

	tool := &GoogleWebSearchTool{}
	r, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if r.Status != "success" {
		t.Errorf("Status = %q, want success", r.Status)
	}
	if r.Data["num_results"] != 2 {
		t.Errorf("num_results = %v, want 2", r.Data["num_results"])
	}
	if _, ok := r.Data["formatted_results"].(string); !ok {
		t.Error("formatted_results missing when SendSignal is nil")
	}
}

func TestWebSearch_SignalErrorDoesNotFailSearch(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
	toolCtx := newWebSearchToolCtx("task-signal-err")
	toolCtx.ToolConfig["api_base"] = srv.baseURL()
	toolCtx.SendSignal = func(any) error { return fmt.Errorf("publish failed") }

	tool := &GoogleWebSearchTool{}
	r, err := tool.Execute(context.Background(), map[string]any{"query": "x"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute err = %v", err)
	}
	if r.Status != "success" {
		t.Errorf("Status = %q (%s), want success", r.Status, r.Message)
	}
}

func always429(t *testing.T, body string, hdr map[string]string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestWebSearch_QuotaRetryActuallyWaitsTheSchedule(t *testing.T) {
	withQuotaRetryBackoff(t, 20*time.Millisecond, 80*time.Millisecond)
	srv, calls := always429(t, `{"error":{"message":"Quota exceeded"}}`, nil)
	p := NewGoogleSearchProvider("K", "CX", srv.URL)

	start := time.Now()
	_, err := p.Search(context.Background(), "q", 3, SearchOptions{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error after the schedule is exhausted")
	}
	if got := calls.Load(); got != int64(len(searchQuotaRetryBackoff))+1 {
		t.Errorf("requests = %d, want %d", got, len(searchQuotaRetryBackoff)+1)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("elapsed %v, want at least 100ms — the schedule was not actually waited", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("elapsed %v, far above the 100ms schedule plus jitter", elapsed)
	}
}

func TestWebSearch_QuotaRejectionWithNonJSONBodyStillReports429(t *testing.T) {
	for _, body := range []string{`<html><body>429 Too Many Requests</body></html>`, ``} {
		t.Run(fmt.Sprintf("body_len_%d", len(body)), func(t *testing.T) {
			withFastQuotaRetryBackoff(t)
			srv, _ := always429(t, body, nil)
			p := NewGoogleSearchProvider("K", "CX", srv.URL)

			_, err := p.Search(context.Background(), "q", 3, SearchOptions{})
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), "(429)") {
				t.Errorf("error hides the 429: %v", err)
			}
		})
	}
}

func TestWebSearch_RetryAfterHeaderIsHonoured(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		hdr  string
		want time.Duration
	}{
		{"seconds", "2", 2 * time.Second},
		{"zero", "0", 0},
		{"negative", "-5", 0},
		{"garbage", "soon", 0},
		{"absent", "", 0},
		{"http date in the past", now.Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.hdr != "" {
				h.Set("Retry-After", c.hdr)
			}
			if got := parseRetryAfter(h, now); got != c.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", c.hdr, got, c.want)
			}
		})
	}
}

func TestWebSearch_TransportFailureSurfacesTheError(t *testing.T) {
	withFastQuotaRetryBackoff(t)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	p := NewGoogleSearchProvider("K", "CX", url)

	_, err := p.Search(context.Background(), "q", 3, SearchOptions{})
	if err == nil {
		t.Fatal("want an error when the transport fails")
	}
	if !strings.Contains(err.Error(), "search request failed") {
		t.Errorf("error = %v, want the transport failure", err)
	}
	if strings.Contains(err.Error(), "API error (0)") {
		t.Errorf("statusCode 0 was treated as an API status: %v", err)
	}
}

func TestJitteredQuotaWait(t *testing.T) {
	if got := jitteredQuotaWait(0); got != 0 {
		t.Errorf("jitteredQuotaWait(0) = %v, want 0", got)
	}
	base := 1 * time.Second
	var sawSpread bool
	for range 200 {
		got := jitteredQuotaWait(base)
		if got < base {
			t.Fatalf("jitter shortened the wait: %v < %v", got, base)
		}
		if got >= base+base*searchQuotaJitterPercent/100 {
			t.Fatalf("jitter exceeded %d%%: %v", searchQuotaJitterPercent, got)
		}
		if got != base {
			sawSpread = true
		}
	}
	if !sawSpread {
		t.Error("no jitter applied across 200 draws — concurrent agents would retry in lockstep")
	}
}

func TestParseRetryAfterIsCapped(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		hdr  string
		want time.Duration
	}{
		{"well under the base is honoured", "30", 30 * time.Second},
		{"just under the base is honoured", "52", 52 * time.Second},
		{"a minute exceeds the base and is clamped", "60", maxQuotaRetryAfterBase},
		{"an hour is clamped", "3600", maxQuotaRetryAfterBase},
		{"overflowing seconds are clamped", "20000000000", maxQuotaRetryAfterBase},
		{"the value that wraps negative is clamped", "9223372037", maxQuotaRetryAfterBase},
		{"a far-future date is clamped", now.Add(72 * time.Hour).UTC().Format(http.TimeFormat), maxQuotaRetryAfterBase},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Retry-After", c.hdr)
			got := parseRetryAfter(h, now)
			if got != c.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", c.hdr, got, c.want)
			}
			if got > maxQuotaRetryAfterBase {
				t.Errorf("parseRetryAfter(%q) = %v exceeds the clamp base %v", c.hdr, got, maxQuotaRetryAfterBase)
			}
		})
	}
}

func TestWebSearch_CancellationIsNotReportedAsAnAPIError(t *testing.T) {
	withQuotaRetryBackoff(t, 5*time.Second, 5*time.Second)
	srv, _ := always429(t, `{"error":{"message":"Quota exceeded"}}`, nil)
	p := NewGoogleSearchProvider("K", "CX", srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	_, err := p.Search(ctx, "q", 3, SearchOptions{})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "API error") {
		t.Errorf("cancellation reported as a Google API error: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(err, DeadlineExceeded) = false; err = %v", err)
	}
}

func TestWebSearch_APIErrorWrapsItsCause(t *testing.T) {
	withFastQuotaRetryBackoff(t)
	srv, _ := always429(t, `<html>429</html>`, nil)
	p := NewGoogleSearchProvider("K", "CX", srv.URL)
	_, err := p.Search(context.Background(), "q", 3, SearchOptions{})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "(429)") {
		t.Errorf("status lost: %v", err)
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("cause not wrapped, so errors.Is/As cannot reach it: %v", err)
	}
}

func TestWebSearch_RetryAfterIsHonouredInTheLoop(t *testing.T) {
	withQuotaRetryBackoff(t, 10*time.Millisecond, 10*time.Millisecond)
	srv, calls := always429(t, `{"error":{"message":"Quota exceeded"}}`,
		map[string]string{"Retry-After": "1"})
	p := NewGoogleSearchProvider("K", "CX", srv.URL)

	start := time.Now()
	_, err := p.Search(context.Background(), "q", 3, SearchOptions{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error once the schedule is exhausted")
	}
	if got := calls.Load(); got != int64(len(searchQuotaRetryBackoff))+1 {
		t.Errorf("requests = %d, want %d", got, len(searchQuotaRetryBackoff)+1)
	}
	if elapsed < 2*time.Second {
		t.Errorf("elapsed %v: Retry-After was ignored in favour of the 10ms schedule", elapsed)
	}
	if elapsed > 15*time.Second {
		t.Errorf("elapsed %v: the wait is not bounded", elapsed)
	}
}

func TestQuotaWait(t *testing.T) {
	t.Run("Retry-After wins when longer, and is jittered", func(t *testing.T) {
		seen := map[time.Duration]bool{}
		for range 200 {
			got, used := quotaWait(10*time.Millisecond, 5*time.Second)
			if !used {
				t.Fatalf("Retry-After of 5s lost to a 10ms schedule")
			}
			if got < 5*time.Second {
				t.Fatalf("wait %v shorter than the Retry-After it came from", got)
			}
			seen[got] = true
		}
		if len(seen) < 2 {
			t.Error("Retry-After waits are identical across draws; concurrent agents would retry in lockstep")
		}
	})
	t.Run("a shorter Retry-After cannot shorten the schedule", func(t *testing.T) {
		base := 5 * time.Second
		got, used := quotaWait(base, 1*time.Second)
		if used {
			t.Error("a shorter Retry-After was taken")
		}
		if got < base {
			t.Errorf("wait %v is below the scheduled %v", got, base)
		}
	})
	t.Run("a clamped Retry-After stays under the hard bound and still varies", func(t *testing.T) {
		seen := map[time.Duration]bool{}
		for range 500 {
			got, used := quotaWait(10*time.Millisecond, maxQuotaRetryAfterBase)
			if !used {
				t.Fatal("the clamped Retry-After lost to a 10ms schedule")
			}
			if got >= maxQuotaRetryAfter {
				t.Fatalf("wait %v reached the hard bound %v", got, maxQuotaRetryAfter)
			}
			if got < maxQuotaRetryAfterBase {
				t.Fatalf("wait %v is below the clamped base %v", got, maxQuotaRetryAfterBase)
			}
			seen[got] = true
		}
		if len(seen) < 2 {
			t.Error("clamped waits are identical; every agent would retry at the same instant")
		}
	})
	t.Run("no header falls through to the schedule", func(t *testing.T) {
		base := 2 * time.Second
		got, used := quotaWait(base, 0)
		if used || got < base {
			t.Errorf("quotaWait(%v, 0) = (%v, %v)", base, got, used)
		}
	})
}

func withQuotaTotalWait(t *testing.T, d time.Duration) {
	t.Helper()
	orig := maxQuotaTotalWait
	maxQuotaTotalWait = d
	t.Cleanup(func() { maxQuotaTotalWait = orig })
}

func TestWebSearch_TotalQuotaWaitIsBudgeted(t *testing.T) {
	withQuotaRetryBackoff(t, 300*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond)
	withQuotaTotalWait(t, 700*time.Millisecond)
	srv, calls := always429(t, `{"error":{"message":"Quota exceeded"}}`, nil)
	p := NewGoogleSearchProvider("K", "CX", srv.URL)

	start := time.Now()
	_, err := p.Search(context.Background(), "q", 3, SearchOptions{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want 3 (budget stops the loop before the last retry)", got)
	}
	if elapsed > maxQuotaTotalWait+400*time.Millisecond {
		t.Errorf("elapsed %v exceeds the %v budget by more than request overhead", elapsed, maxQuotaTotalWait)
	}
}

func TestWebSearch_AWaitThatDoesNotFitEndsTheLoop(t *testing.T) {
	withQuotaRetryBackoff(t, 2*time.Second, 2*time.Second)
	withQuotaTotalWait(t, 3500*time.Millisecond)
	srv, calls := always429(t, `{"error":{"message":"Quota exceeded"}}`, nil)
	p := NewGoogleSearchProvider("K", "CX", srv.URL)

	start := time.Now()
	_, err := p.Search(context.Background(), "q", 3, SearchOptions{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("want an error")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2: a wait that does not fit must end the loop, not be shortened", got)
	}
	if elapsed > 3*time.Second {
		t.Errorf("elapsed %v: the remainder was slept rather than abandoned", elapsed)
	}
}

func TestQuotaWaitsNeverExceedTheBudget(t *testing.T) {
	cases := []struct {
		name      string
		schedule  []time.Duration
		budget    time.Duration
		wantCalls int64
	}{
		{"waits accumulate until the budget stops them", []time.Duration{200 * time.Millisecond,
			200 * time.Millisecond, 200 * time.Millisecond}, 500 * time.Millisecond, 3},
		{"one wait fits, the next does not", []time.Duration{400 * time.Millisecond,
			400 * time.Millisecond}, 600 * time.Millisecond, 2},
		{"budget never binds", []time.Duration{100 * time.Millisecond,
			100 * time.Millisecond}, 5 * time.Second, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withQuotaRetryBackoff(t, c.schedule...)
			withQuotaTotalWait(t, c.budget)
			srv, calls := always429(t, `{"error":{"message":"Quota exceeded"}}`, nil)
			p := NewGoogleSearchProvider("K", "CX", srv.URL)

			start := time.Now()
			if _, err := p.Search(context.Background(), "q", 3, SearchOptions{}); err == nil {
				t.Fatal("want an error")
			}
			elapsed := time.Since(start)

			if elapsed > c.budget+300*time.Millisecond {
				t.Errorf("elapsed %v exceeds budget %v plus request overhead", elapsed, c.budget)
			}
			if got := calls.Load(); got != c.wantCalls {
				t.Errorf("requests = %d, want %d", got, c.wantCalls)
			}
		})
	}
}

func TestMaxQuotaWaitConstantsAreCoherent(t *testing.T) {
	if maxQuotaRetryAfter > 2*time.Minute {
		t.Errorf("maxQuotaRetryAfter = %v; a server-controlled header must not park a task that long", maxQuotaRetryAfter)
	}
	if jittered := maxQuotaRetryAfterBase * (100 + searchQuotaJitterPercent) / 100; jittered > maxQuotaRetryAfter {
		t.Errorf("base %v jittered to %v exceeds the bound %v", maxQuotaRetryAfterBase, jittered, maxQuotaRetryAfter)
	}
	if maxQuotaTotalWait < maxQuotaRetryAfter {
		t.Errorf("total budget %v is below a single sleep bound %v", maxQuotaTotalWait, maxQuotaRetryAfter)
	}
	const workflowNodeDefaultTimeout = 5 * time.Minute
	worst := maxQuotaTotalWait + time.Duration(len(searchQuotaRetryBackoff)+1)*searchRequestTimeout
	if worst >= workflowNodeDefaultTimeout {
		t.Errorf("worst-case call %v does not fit a %v node budget (budget %v + %d requests)",
			worst, workflowNodeDefaultTimeout, maxQuotaTotalWait, len(searchQuotaRetryBackoff)+1)
	}
	if maxQuotaTotalWait > 2*time.Minute {
		t.Errorf("maxQuotaTotalWait = %v; a server-controlled stall must stay well under a node budget", maxQuotaTotalWait)
	}
	firstWait := searchQuotaRetryBackoff[0] * (100 + searchQuotaJitterPercent) / 100
	if firstWait > maxQuotaTotalWait {
		t.Errorf("first scheduled wait %v does not fit the budget %v; retries are disabled",
			firstWait, maxQuotaTotalWait)
	}
	if maxQuotaTotalWait < maxQuotaRetryAfterBase {
		t.Errorf("budget %v does not fit even one clamped wait (%v)", maxQuotaTotalWait, maxQuotaRetryAfterBase)
	}
	if maxQuotaTotalWait > 2*maxQuotaRetryAfterBase {
		t.Errorf("budget %v now fits two clamped waits; the two-request comment above is stale",
			maxQuotaTotalWait)
	}
}

func TestGoogleErrorMessageFallback(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
		want string
	}{
		{"nil body", nil, "unknown error"},
		{"no error key", map[string]any{"items": []any{}}, "unknown error"},
		{"error is not an object", map[string]any{"error": "boom"}, "unknown error"},
		{"error object without message", map[string]any{"error": map[string]any{"code": 429.0}}, "unknown error"},
		{"empty message", map[string]any{"error": map[string]any{"message": ""}}, "unknown error"},
		{"real message", map[string]any{"error": map[string]any{"message": "Quota exceeded"}}, "Quota exceeded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := googleErrorMessage(c.data); got != c.want {
				t.Errorf("googleErrorMessage(%v) = %q, want %q", c.data, got, c.want)
			}
		})
	}
}
