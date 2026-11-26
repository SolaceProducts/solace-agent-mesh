// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type recordedSearchRequest struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

type providerFake struct {
	mu        sync.Mutex
	requests  []recordedSearchRequest
	responses []fakeResponse
	server    *httptest.Server
}

type fakeResponse struct {
	status int
	body   string
}

func newProviderFake(t *testing.T, responses ...fakeResponse) *providerFake {
	t.Helper()
	f := &providerFake{responses: responses}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, recordedSearchRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: body})
		resp := f.responses[min(len(f.requests), len(f.responses))-1]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *providerFake) recorded() []recordedSearchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *providerFake) only(t *testing.T) recordedSearchRequest {
	t.Helper()
	got := f.recorded()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	return got[0]
}

const tavilyCannedResponse = `{
	"query": "q",
	"results": [
		{"title": "Tavily One", "url": "https://www.one.example/a", "content": "first content", "score": 0.9},
		{"title": "Tavily Two", "url": "https://two.example/b", "content": "second content", "score": 0.8, "published_date": "2026-01-01"}
	],
	"images": [
		{"url": "https://img.example/1.png", "description": "a diagram"},
		"https://img.example/2.png"
	],
	"response_time": 0.4,
	"request_id": "r1"
}`

const exaCannedResponse = `{
	"requestId": "e1",
	"results": [
		{"title": "Exa One", "url": "https://one.example/x", "highlights": ["first point", "second point"], "image": "https://one.example/og.png"},
		{"title": "Exa Two", "url": "https://two.example/y", "summary": "a summary"},
		{"title": "Exa Three", "url": "https://three.example/z", "text": "` + "plain text body" + `"}
	],
	"costDollars": {"total": 0.005}
}`

func tavilyToolCtx(baseURL string) *tool.Context {
	return &tool.Context{ParentTaskID: "task", ToolConfig: map[string]any{
		"provider": "tavily", "api_key": "TAVILY_KEY", "api_base": baseURL,
	}}
}

func exaToolCtx(baseURL string) *tool.Context {
	return &tool.Context{ParentTaskID: "task", ToolConfig: map[string]any{
		"provider": "exa", "api_key": "EXA_KEY", "api_base": baseURL,
	}}
}

func TestTavily_RequestMapping(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
	toolCtx := tavilyToolCtx(fake.server.URL)
	toolCtx.ToolConfig["safe_search"] = "true"

	r, err := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{
		"query":           "solace",
		"max_results":     float64(50),
		"recency":         "month",
		"search_type":     "image",
		"include_domains": []any{"solace.com"},
		"exclude_domains": []any{"spam.example"},
	}, toolCtx)
	if err != nil || r.Status != tool.StatusSuccess {
		t.Fatalf("Execute = %+v, %v", r, err)
	}

	req := fake.only(t)
	if req.method != http.MethodPost || req.path != "/search" {
		t.Errorf("request = %s %s, want POST /search", req.method, req.path)
	}
	if got := req.header.Get("Authorization"); got != "Bearer TAVILY_KEY" {
		t.Errorf("Authorization = %q, want the bearer key", got)
	}
	want := map[string]any{
		"query":                      "solace",
		"max_results":                float64(20),
		"search_depth":               "basic",
		"topic":                      "general",
		"time_range":                 "month",
		"include_domains":            []any{"solace.com"},
		"exclude_domains":            []any{"spam.example"},
		"include_images":             true,
		"include_image_descriptions": true,
		"safe_search":                true,
	}
	assertJSONFields(t, req.body, want)
	if len(req.body) != len(want) {
		t.Errorf("body = %v, want exactly the fields %v", req.body, want)
	}
}

func TestTavily_OptionalFieldsOmittedWhenUnset(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
	if _, err := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "q"}, tavilyToolCtx(fake.server.URL)); err != nil {
		t.Fatal(err)
	}
	body := fake.only(t).body
	for _, field := range []string{"time_range", "include_domains", "exclude_domains", "include_images", "safe_search"} {
		if _, ok := body[field]; ok {
			t.Errorf("body carries %s on a plain search: %v", field, body)
		}
	}
	if body["max_results"] != float64(defaultNumResults) {
		t.Errorf("max_results = %v, want the default %d", body["max_results"], defaultNumResults)
	}
}

func TestTavily_ResponseParsing(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
	p := newTavilySearchProvider("K", fake.server.URL)

	results, err := p.Search(t.Context(), "q", 5, SearchOptions{Image: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchResult{
		{Title: "Tavily One", Link: "https://www.one.example/a", Snippet: "first content", Attribution: "one.example", Type: "web"},
		{Title: "Tavily Two", Link: "https://two.example/b", Snippet: "second content", Attribution: "two.example", Type: "web"},
		{Title: "a diagram", Link: "https://img.example/1.png", ImageURL: "https://img.example/1.png", Type: "image"},
		{Link: "https://img.example/2.png", ImageURL: "https://img.example/2.png", Type: "image"},
	}
	if !slices.Equal(results, want) {
		t.Errorf("results =\n%+v\nwant\n%+v", results, want)
	}

	webOnly, err := p.Search(t.Context(), "q", 5, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(webOnly) != 2 {
		t.Errorf("a web search returned %d results, want only the 2 organic ones", len(webOnly))
	}
}

func TestExa_RequestMapping(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})
	p := newExaSearchProvider("EXA_KEY", fake.server.URL)
	p.now = func() time.Time { return time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC) }

	_, err := p.Search(t.Context(), "solace", 50, SearchOptions{
		Recency:        recencyWeek,
		IncludeDomains: []string{"solace.com"},
		ExcludeDomains: []string{"spam.example"},
		SafeSearch:     true,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := fake.only(t)
	if req.method != http.MethodPost || req.path != "/search" {
		t.Errorf("request = %s %s, want POST /search", req.method, req.path)
	}
	if got := req.header.Get("x-api-key"); got != "EXA_KEY" {
		t.Errorf("x-api-key = %q, want the key", got)
	}
	if got := req.header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none: Exa authenticates with x-api-key", got)
	}
	assertJSONFields(t, req.body, map[string]any{
		"query":              "solace",
		"type":               "auto",
		"numResults":         float64(exaMaxResults),
		"includeDomains":     []any{"solace.com"},
		"excludeDomains":     []any{"spam.example"},
		"startPublishedDate": "2026-03-08T12:00:00Z",
		"moderation":         true,
		"contents":           map[string]any{"highlights": true},
	})
}

func TestExa_RecencyWindows(t *testing.T) {
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	want := map[string]string{
		recencyDay:   "2026-03-14T12:00:00Z",
		recencyWeek:  "2026-03-08T12:00:00Z",
		recencyMonth: "2026-02-15T12:00:00Z",
		recencyYear:  "2025-03-15T12:00:00Z",
	}
	for recency, wantDate := range want {
		if got := exaRecencyWindow[recency](now).Format(time.RFC3339); got != wantDate {
			t.Errorf("%s window starts %s, want %s", recency, got, wantDate)
		}
	}
	if len(exaRecencyWindow) != len(recencyValues) {
		t.Errorf("exaRecencyWindow covers %d windows, want every one of %v", len(exaRecencyWindow), recencyValues)
	}
}

func TestExa_ResponseParsing(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})
	results, err := newExaSearchProvider("K", fake.server.URL).Search(t.Context(), "q", 5, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchResult{
		{Title: "Exa One", Link: "https://one.example/x", Snippet: "first point … second point", Attribution: "one.example", ImageURL: "https://one.example/og.png", Type: "web"},
		{Title: "Exa Two", Link: "https://two.example/y", Snippet: "a summary", Attribution: "two.example", Type: "web"},
		{Title: "Exa Three", Link: "https://three.example/z", Snippet: "plain text body", Attribution: "three.example", Type: "web"},
	}
	if !slices.Equal(results, want) {
		t.Errorf("results =\n%+v\nwant\n%+v", results, want)
	}
}

func TestExa_LongTextSnippetIsTruncated(t *testing.T) {
	snippet := exaSnippet(map[string]any{"text": strings.Repeat("é", exaSnippetMaxBytes)})
	if len(snippet) > exaSnippetMaxBytes+len("…") {
		t.Errorf("snippet is %d bytes, want at most %d", len(snippet), exaSnippetMaxBytes+len("…"))
	}
	if !strings.HasSuffix(snippet, "…") {
		t.Errorf("truncated snippet does not say so: %q", snippet)
	}
}

func TestSearchProviders_ErrorClassification(t *testing.T) {
	type provider struct {
		name  string
		build func(base string) SearchProvider
	}
	google := provider{"google", func(base string) SearchProvider { return NewGoogleSearchProvider("SECRET_KEY", "CX", base) }}
	tavily := provider{"tavily", func(base string) SearchProvider { return newTavilySearchProvider("SECRET_KEY", base) }}
	exa := provider{"exa", func(base string) SearchProvider { return newExaSearchProvider("SECRET_KEY", base) }}

	cases := []struct {
		provider provider
		status   int
		body     string
		wantText string
		wantCode string
	}{
		{tavily, http.StatusUnauthorized, `{"detail":{"error":"Unauthorized: missing or invalid API key."}}`, "tavily search API error (401): Unauthorized: missing or invalid API key.", "DISPATCH_AUTH_REQUIRED"},
		{tavily, 432, `{"detail":{"error":"This request exceeds your plan's set usage limit."}}`, "tavily search API error (432): This request exceeds your plan's set usage limit.", taxonomy.ToolFailureRateLimited},
		{tavily, 433, `{"detail":{"error":"This request exceeds the pay-as-you-go limit."}}`, "tavily search API error (433)", taxonomy.ToolFailureRateLimited},
		{tavily, 434, `{"detail":"Flex capacity reached"}`, "tavily search API error (434): Flex capacity reached", taxonomy.ToolFailureRateLimited},
		{tavily, http.StatusUnprocessableEntity, `{"detail":{"error":"Invalid time_range"}}`, "tavily search API error (422): Invalid time_range", "DISPATCH_BACKEND_4XX"},
		{tavily, http.StatusInternalServerError, `{}`, "tavily search API error (500): unknown error", "DISPATCH_BACKEND_5XX"},
		{exa, http.StatusPaymentRequired, `{"error":"Out of credits"}`, "exa search API error (402): Out of credits", taxonomy.ToolFailureRateLimited},
		{exa, http.StatusUnauthorized, `{"error":"Invalid API key"}`, "exa search API error (401): Invalid API key", "DISPATCH_AUTH_REQUIRED"},
		{exa, http.StatusForbidden, `{"message":"Forbidden"}`, "exa search API error (403): Forbidden", taxonomy.ToolFailurePermissionDenied},
		{exa, http.StatusServiceUnavailable, `{"error":"Service unavailable"}`, "exa search API error (503): Service unavailable", "DISPATCH_BACKEND_5XX"},
		{google, http.StatusForbidden, `{"error":{"code":403,"message":"Quota","errors":[{"reason":"quotaExceeded"}]}}`, "google search API error (403): Quota", taxonomy.ToolFailureRateLimited},
		{google, http.StatusUnauthorized, `{"error":{"code":401,"message":"Login Required"}}`, "google search API error (401): Login Required", "DISPATCH_AUTH_REQUIRED"},
	}
	withFastQuotaRetryBackoff(t)
	for _, tc := range cases {
		t.Run(tc.provider.name+"_"+http.StatusText(tc.status)+"_"+tc.wantCode, func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{tc.status, tc.body})
			_, err := tc.provider.build(fake.server.URL).Search(t.Context(), "secret query", 3, SearchOptions{})
			if err == nil {
				t.Fatal("want an error")
			}
			if got := len(fake.recorded()); got != 1 {
				t.Errorf("requests = %d, want 1: only a 429 is retried, a spent plan or credit does not refill within the schedule", got)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantText)
			}
			for _, leak := range []string{"SECRET_KEY", "secret query"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q leaks %q", err, leak)
				}
			}
			r := tool.Error("x", ErrorCode(err))
			if r.ErrorCode != tc.wantCode {
				t.Errorf("code = %q, want %q", r.ErrorCode, tc.wantCode)
			}
		})
	}
}

func TestSearchProviders_QuotaRetryResendsThePOSTBody(t *testing.T) {
	withFastQuotaRetryBackoff(t)
	fake := newProviderFake(t,
		fakeResponse{http.StatusTooManyRequests, `{"detail":{"error":"rate limited"}}`},
		fakeResponse{http.StatusOK, tavilyCannedResponse})

	results, err := newTavilySearchProvider("K", fake.server.URL).Search(t.Context(), "retry me", 3, SearchOptions{})
	if err != nil {
		t.Fatalf("Search after one 429 = %v, want success", err)
	}
	if len(results) != 2 {
		t.Errorf("results = %d, want 2", len(results))
	}
	reqs := fake.recorded()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for i, r := range reqs {
		if r.body["query"] != "retry me" {
			t.Errorf("attempt %d body = %v, want the query", i+1, r.body)
		}
	}
}

func TestResolveSearchProvider(t *testing.T) {
	const base = "http://search.internal"
	cases := []struct {
		name         string
		cfg          map[string]any
		wantProvider string
		wantErr      []string
	}{
		{"nothing configured", map[string]any{}, "", []string{"no provider configured", "Web Search connector", "provider", "api_key", "api_base"}},
		{"key without a provider", map[string]any{"api_key": "t", "api_base": base}, "", []string{"no provider configured"}},
		{"tavily", map[string]any{"provider": "tavily", "api_key": "t", "api_base": base}, "tavily", nil},
		{"exa", map[string]any{"provider": "exa", "api_key": "e", "api_base": base}, "exa", nil},
		{"google", map[string]any{"provider": "google", "google_search_api_key": "g", "google_cse_id": "cx", "api_base": base}, "google", nil},
		{"provider is case-insensitive", map[string]any{"provider": " EXA ", "api_key": "e", "api_base": base}, "exa", nil},
		{"unknown provider", map[string]any{"provider": "brave", "api_key": "t", "api_base": base}, "", []string{`"brave" is not supported`, "Web Search connector", "tavily, exa, google"}},
		{"tavily without key", map[string]any{"provider": "tavily", "api_base": base}, "", []string{"tavily has no API key", "Web Search connector", "api_key"}},
		{"blank key is not a key", map[string]any{"provider": "exa", "api_key": "  ", "api_base": base}, "", []string{"exa has no API key"}},
		{"tavily without base", map[string]any{"provider": "tavily", "api_key": "t"}, "", []string{"tavily has no API base", "api_base", "no public-endpoint fallback"}},
		{"exa without base", map[string]any{"provider": "exa", "api_key": "e"}, "", []string{"exa has no API base", "no public-endpoint fallback"}},
		{"google without base", map[string]any{"provider": "google", "google_search_api_key": "g", "google_cse_id": "cx"}, "", []string{"api_base", "no public-endpoint fallback"}},
		{"google without cse id", map[string]any{"provider": "google", "google_search_api_key": "g", "api_base": base}, "", []string{"google_cse_id"}},
		{"google reads its own key, not api_key", map[string]any{"provider": "google", "api_key": "g", "google_cse_id": "cx", "api_base": base}, "", []string{"google_search_api_key"}},
		{"exa beside a google key", map[string]any{"provider": "exa", "api_key": "e", "api_base": base, "google_search_api_key": "g", "google_cse_id": "cx"}, "",
			[]string{"provider exa", "google_search_api_key", "api_base", "remove google_search_api_key to search with exa", "remove provider"}},
		{"tavily beside a google key", map[string]any{"provider": "tavily", "api_key": "t", "api_base": base, "google_search_api_key": "g"}, "",
			[]string{"provider tavily", "google_search_api_key"}},
		{"google beside its own key and an api_key", map[string]any{"provider": "google", "api_key": "e", "google_search_api_key": "g", "google_cse_id": "cx", "api_base": base}, "google", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := resolveSearchProvider(tc.cfg, deepResearchProviderNames)
			if tc.wantProvider != "" {
				if err != nil {
					t.Fatalf("err = %v, want provider %s", err, tc.wantProvider)
				}
				if p.Name() != tc.wantProvider {
					t.Errorf("provider = %s, want %s", p.Name(), tc.wantProvider)
				}
				return
			}
			if err == nil {
				t.Fatalf("resolved %s, want an error", p.Name())
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			for _, secret := range []string{`"t"`, `"e"`, `"g"`} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error %q quotes a key", err)
				}
			}
		})
	}
}

func TestSearchProviders_Capabilities(t *testing.T) {
	cases := map[string]struct {
		provider SearchProvider
		want     SearchCapabilities
	}{
		"google": {NewGoogleSearchProvider("k", "cx", "b"), SearchCapabilities{Image: true, Recency: true, MaxResults: 10}},
		"tavily": {newTavilySearchProvider("k", "b"), SearchCapabilities{Image: true, Recency: true, Domains: true, MaxResults: 20}},
		"exa":    {newExaSearchProvider("k", "b"), SearchCapabilities{Recency: true, Domains: true, MaxResults: 20}},
	}
	for name, tc := range cases {
		if got := tc.provider.Capabilities(); got != tc.want {
			t.Errorf("%s capabilities = %+v, want %+v", name, got, tc.want)
		}
		if tc.provider.Name() != name {
			t.Errorf("Name() = %s, want %s", tc.provider.Name(), name)
		}
	}
}

func TestWebSearch_ParametersFollowTheProvider(t *testing.T) {
	const base = "http://search.internal"
	cases := []struct {
		name      string
		cfg       map[string]any
		wantProps []string
		wantMax   int
	}{
		{"google is web_search_google's, so it is unresolved", map[string]any{"provider": "google", "google_search_api_key": "g", "google_cse_id": "cx", "api_base": base},
			[]string{"exclude_domains", "include_domains", "max_results", "query", "recency", "search_type"}, 20},
		{"tavily", map[string]any{"provider": "tavily", "api_key": "t", "api_base": base},
			[]string{"exclude_domains", "include_domains", "max_results", "query", "recency", "search_type"}, 20},
		{"exa", map[string]any{"provider": "exa", "api_key": "e", "api_base": base},
			[]string{"exclude_domains", "include_domains", "max_results", "query", "recency"}, 20},
		{"unconfigured offers every option", nil,
			[]string{"exclude_domains", "include_domains", "max_results", "query", "recency", "search_type"}, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := NewWebSearchTool(tc.cfg).Parameters()
			got := slices.Sorted(maps.Keys(schema.Properties))
			if !slices.Equal(got, tc.wantProps) {
				t.Errorf("properties = %v, want %v", got, tc.wantProps)
			}
			maxResults := schema.Properties["max_results"].(map[string]any)["maximum"]
			if maxResults != tc.wantMax {
				t.Errorf("max_results maximum = %v, want %d", maxResults, tc.wantMax)
			}
			if !slices.Equal(schema.Required, []string{"query"}) {
				t.Errorf("required = %v, want [query]", schema.Required)
			}
		})
	}
}

func TestWebSearch_RejectsOptionsTheProviderCannotHonour(t *testing.T) {
	cases := []struct {
		name    string
		ctx     func(base string) *tool.Context
		args    map[string]any
		wantErr string
	}{
		{"exa has no image search", exaToolCtx, map[string]any{"search_type": "image"}, "search_type is not supported by the configured web search provider (exa)"},
		{"unknown recency", tavilyToolCtx, map[string]any{"recency": "fortnight"}, `recency must be one of day, week, month, year, got "fortnight"`},
		{"unknown search_type", tavilyToolCtx, map[string]any{"search_type": "news"}, `search_type must be "image" or omitted`},
		{"domains not an array", tavilyToolCtx, map[string]any{"include_domains": "a.example"}, "include_domains must be an array of domain names"},
		{"date_restrict is gone", tavilyToolCtx, map[string]any{"date_restrict": "d7"}, "use recency"},
		{"safe_search is the operator's", exaToolCtx, map[string]any{"safe_search": "off"}, "set by the operator"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{http.StatusOK, `{}`})
			args := map[string]any{"query": "q"}
			for k, v := range tc.args {
				args[k] = v
			}
			r, err := NewWebSearchTool(nil).Execute(t.Context(), args, tc.ctx(fake.server.URL))
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tool.StatusError || !strings.Contains(r.ValidationError, tc.wantErr) {
				t.Errorf("result = status %q validation %q, want a validation error containing %q", r.Status, r.ValidationError, tc.wantErr)
			}
			if n := len(fake.recorded()); n != 0 {
				t.Errorf("%d requests reached the provider, want none", n)
			}
		})
	}
}

func googleToolCtx(baseURL string) *tool.Context {
	return &tool.Context{ParentTaskID: "task", ToolConfig: map[string]any{
		"provider": "google", "google_search_api_key": "G", "google_cse_id": "CX", "api_base": baseURL,
	}}
}

func TestWebSearch_RefusesGoogle(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)

	r, err := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "q"}, googleToolCtx(srv.baseURL()))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != tool.StatusError || r.ValidationError != "" {
		t.Errorf("result = %+v, want an operator-config error, not one the model could retry around", r)
	}
	for _, want := range []string{"provider google is not supported", "web_search_google", "one of: tavily, exa"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message = %q, want it to contain %q", r.Message, want)
		}
	}
	if srv.lastParams() != nil {
		t.Error("web_search sent a request to the Google endpoint")
	}
}

func TestGoogleWebSearch_RefusesAProviderBesideItsKey(t *testing.T) {
	for _, provider := range []string{searchProviderExa, searchProviderTavily, "brave"} {
		t.Run(provider, func(t *testing.T) {
			srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
			toolCtx := googleToolCtx(srv.baseURL())
			toolCtx.ToolConfig["provider"] = provider
			toolCtx.ToolConfig["api_key"] = "OTHER_KEY"

			r, err := NewGoogleWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "q"}, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tool.StatusError || !strings.Contains(r.Message, "provider "+provider) || !strings.Contains(r.Message, "google_search_api_key") {
				t.Errorf("result = %+v, want a refusal naming provider %s and google_search_api_key", r, provider)
			}
			if strings.Contains(r.Message, "OTHER_KEY") || strings.Contains(r.Message, `"G"`) {
				t.Errorf("message quotes a key: %q", r.Message)
			}
			if srv.lastParams() != nil {
				t.Error("web_search_google sent its key to an api_base configured beside another provider")
			}
		})
	}
	t.Run("provider google is its own", func(t *testing.T) {
		srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
		r, err := NewGoogleWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "q"}, googleToolCtx(srv.baseURL()))
		if err != nil || r.Status != tool.StatusSuccess {
			t.Fatalf("Execute = %+v, %v", r, err)
		}
	})
}

func TestGoogleSearchProvider_MapsTheNeutralOptions(t *testing.T) {
	srv := newTestCSEServer(t, cannedWebResponse, http.StatusOK)

	opts := SearchOptions{Image: true, Recency: recencyWeek, SafeSearch: true}
	if _, err := NewGoogleSearchProvider("G", "CX", srv.baseURL()).Search(t.Context(), "docker", 7, opts); err != nil {
		t.Fatalf("Search = %v", err)
	}
	got := srv.lastParams()
	want := map[string]string{"key": "G", "cx": "CX", "q": "docker", "num": "7", "searchType": "image", "dateRestrict": "w1", "safe": "active"}
	if len(got) != len(want) {
		t.Errorf("query params = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
}

func TestWebSearch_NameAndDescriptionFromConfig(t *testing.T) {
	plain := NewWebSearchTool(nil)
	if plain.Name() != ToolName {
		t.Errorf("Name() = %q, want %q", plain.Name(), ToolName)
	}
	if !strings.HasPrefix(plain.Description(), defaultDescription) {
		t.Errorf("Description() = %q, want the default lead", plain.Description())
	}

	named := NewWebSearchTool(map[string]any{"tool_name": "web_search_ab12cd34", "tool_description": "Search the company's approved sources."})
	if named.Name() != "web_search_ab12cd34" {
		t.Errorf("Name() = %q, want the configured name", named.Name())
	}
	if desc := named.Description(); !strings.HasPrefix(desc, "Search the company's approved sources. ") || !strings.HasSuffix(desc, CitationGuidance) {
		t.Errorf("Description() = %q, want the configured lead followed by the citation rules", desc)
	}
}

func TestWebSearch_MaxResultsDefaultFromConfig(t *testing.T) {
	cases := []struct {
		name    string
		value   any
		args    map[string]any
		wantNum float64
	}{
		{"unset", nil, nil, defaultNumResults},
		{"yaml number", 8, nil, 8},
		{"connector string", "12", nil, 12},
		{"above the provider maximum", 50, nil, tavilyMaxResults},
		{"the model's count wins", 8, map[string]any{"max_results": float64(3)}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
			toolCtx := tavilyToolCtx(fake.server.URL)
			cfg := map[string]any{}
			if tc.value != nil {
				cfg["max_results"] = tc.value
			}
			ws := NewWebSearchTool(cfg)
			args := map[string]any{"query": "q"}
			maps.Copy(args, tc.args)
			if _, err := ws.Execute(t.Context(), args, toolCtx); err != nil {
				t.Fatal(err)
			}
			if got := fake.only(t).body["max_results"]; got != tc.wantNum {
				t.Errorf("request max_results = %v, want %v", got, tc.wantNum)
			}
			if tc.args == nil {
				schemaCfg := maps.Clone(toolCtx.ToolConfig)
				maps.Copy(schemaCfg, cfg)
				schemaDefault := NewWebSearchTool(schemaCfg).Parameters().Properties["max_results"].(map[string]any)["default"]
				if schemaDefault != int(tc.wantNum) {
					t.Errorf("schema default = %v, want %v", schemaDefault, tc.wantNum)
				}
			}
		})
	}
}

func TestWebSearch_ResultShapeIsProviderNeutral(t *testing.T) {
	cases := map[string]struct {
		response string
		ctx      func(string) *tool.Context
	}{
		"tavily": {tavilyCannedResponse, tavilyToolCtx},
		"exa":    {exaCannedResponse, exaToolCtx},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{http.StatusOK, tc.response})
			toolCtx := tc.ctx(fake.server.URL)
			signals := captureRAGSignals(toolCtx)

			r, err := NewWebSearchTool(nil).Execute(context.Background(), map[string]any{"query": "q"}, toolCtx)
			if err != nil || r.Status != tool.StatusSuccess {
				t.Fatalf("Execute = %+v, %v", r, err)
			}
			for _, key := range []string{"formatted_results", "num_results", "search_turn"} {
				if _, ok := r.Data[key]; !ok {
					t.Errorf("result data lacks %s", key)
				}
			}
			if len(r.Data) != 3 {
				t.Errorf("result data = %v, want only formatted_results, num_results, search_turn", r.Data)
			}
			sig := onlyRAGSignal(t, signals)
			if sig.SearchType != "web_search" {
				t.Errorf("search_type = %q, want web_search", sig.SearchType)
			}
			ids := citationIDsOf(sig.Sources)
			if len(ids) == 0 || ids[0] != "s0r0" {
				t.Errorf("citation ids = %v, want s0r0 first", ids)
			}
			if !strings.Contains(r.Data["formatted_results"].(string), "[[cite:s0r0]]") {
				t.Errorf("formatted_results lacks the citation marker")
			}
		})
	}
}

func TestConfigSafeSearch(t *testing.T) {
	cases := []struct {
		value   any
		want    bool
		wantErr string
	}{
		{value: true, want: true}, {value: false}, {value: "true", want: true}, {value: "TRUE", want: true},
		{value: " 1 ", want: true}, {value: "false"}, {value: nil},
		{value: 1, want: true}, {value: 0}, {value: int64(1), want: true}, {value: 1.0, want: true}, {value: 0.0},
		{value: "off"}, {value: " OFF "}, {value: "active", want: true}, {value: "Medium", want: true}, {value: "high", want: true},
		{value: "on", wantErr: `got "on"`}, {value: "yes please", wantErr: `got "yes please"`},
		{value: "strict", wantErr: `got "strict"`}, {value: 2, wantErr: "got 2"}, {value: 0.5, wantErr: "got 0.5"},
		{value: []any{true}, wantErr: "got []interface {}"},
	}
	for _, tc := range cases {
		got, err := configSafeSearch(map[string]any{"safe_search": tc.value})
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), "safe_search must be true or false, "+tc.wantErr) {
				t.Errorf("configSafeSearch(%#v) error = %v, want one containing %q", tc.value, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("configSafeSearch(%#v) = %v, %v, want %v", tc.value, got, err, tc.want)
		}
	}
	if got, err := configSafeSearch(map[string]any{}); got || err != nil {
		t.Errorf("configSafeSearch with the key absent = %v, %v, want false, nil", got, err)
	}
}

func TestConfigSafeSearch_PresentButEmptyIsAnError(t *testing.T) {
	for _, value := range []string{"", "  ", "\t"} {
		if got, err := configSafeSearch(map[string]any{"safe_search": value}); err == nil || got {
			t.Errorf("configSafeSearch(%q) = %v, %v, want an error", value, got, err)
		}
	}
}

func TestWebSearch_RejectsAnUnreadableSafeSearchBeforeSearching(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})
	toolCtx := exaToolCtx(fake.server.URL)
	toolCtx.ToolConfig["safe_search"] = "on"

	r, err := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "q"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == tool.StatusSuccess || !strings.Contains(r.Message, "tool_config") || !strings.Contains(r.Message, "safe_search must be true or false") {
		t.Errorf("result = %+v, want an operator-config error naming tool_config and safe_search", r)
	}
	if !strings.HasPrefix(r.Message, "web_search tool_config is invalid") {
		t.Errorf("message = %q, want it to name the calling tool", r.Message)
	}
	if r.ValidationError != "" {
		t.Errorf("ValidationError = %q, want none: the model cannot fix operator config", r.ValidationError)
	}
	if got := fake.recorded(); len(got) != 0 {
		t.Errorf("searched %d times despite an invalid safe_search", len(got))
	}
}

type redirectProvider struct {
	name       string
	method     string
	build      func(base string) SearchProvider
	answer     string
	carriesKey func(r seenRequest) bool
}

const redirectTestKey = "SECRET_KEY"

func redirectProviders() []redirectProvider {
	return []redirectProvider{
		{"google", http.MethodGet, func(base string) SearchProvider { return NewGoogleSearchProvider(redirectTestKey, "CX", base) }, cannedWebResponse,
			func(r seenRequest) bool { return r.query.Get("key") == redirectTestKey }},
		{"tavily", http.MethodPost, func(base string) SearchProvider { return newTavilySearchProvider(redirectTestKey, base) }, tavilyCannedResponse,
			func(r seenRequest) bool { return r.header.Get("Authorization") == "Bearer "+redirectTestKey }},
		{"exa", http.MethodPost, func(base string) SearchProvider { return newExaSearchProvider(redirectTestKey, base) }, exaCannedResponse,
			func(r seenRequest) bool { return r.header.Get("X-Api-Key") == redirectTestKey }},
	}
}

type seenRequest struct {
	method string
	host   string
	header http.Header
	query  url.Values
	body   string
}

type requestLog struct {
	mu       sync.Mutex
	requests []seenRequest
}

func (l *requestLog) add(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, seenRequest{
		method: r.Method, host: r.Host, header: r.Header.Clone(), query: r.URL.Query(), body: string(body),
	})
}

func (l *requestLog) all() []seenRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.requests)
}

const redirectedBasePath = "/moved"

func followsRedirect(provider redirectProvider, status int) bool {
	return provider.method == http.MethodGet || status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}

func redirectingServer(t *testing.T, status int, answer string) (*httptest.Server, *requestLog) {
	t.Helper()
	var landed requestLog
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, redirectedBasePath) {
			target := strings.TrimPrefix(r.URL.Path, redirectedBasePath)
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, status)
			return
		}
		landed.add(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	return server, &landed
}

const redirectRefusalFormat = "%s search API answered with a redirect (HTTP %d) that is not followed; point api_base at the final endpoint"

var redirectStatuses = []int{
	http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
	http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
}

func TestSearchProviders_FollowSameHostRedirects(t *testing.T) {
	for _, tc := range redirectProviders() {
		for _, status := range redirectStatuses {
			t.Run(tc.name+"_"+strconv.Itoa(status), func(t *testing.T) {
				server, landed := redirectingServer(t, status, tc.answer)

				results, err := tc.build(server.URL+redirectedBasePath).Search(t.Context(), "q", 3, SearchOptions{})

				got := landed.all()
				if !followsRedirect(tc, status) {
					if want := fmt.Sprintf(redirectRefusalFormat, tc.name, status); err == nil || err.Error() != want {
						t.Errorf("error = %v, want %q", err, want)
					}
					if len(got) != 0 {
						t.Errorf("redirect target received %d requests, want none", len(got))
					}
					return
				}
				if err != nil {
					t.Fatalf("Search: %v", err)
				}
				if len(results) == 0 {
					t.Error("the redirected search returned no results")
				}
				if len(got) != 1 {
					t.Fatalf("redirect target received %d requests, want 1", len(got))
				}
				seen := got[0]
				if seen.method != tc.method {
					t.Errorf("method = %s, want %s", seen.method, tc.method)
				}
				if !tc.carriesKey(seen) {
					t.Errorf("redirected request lost the provider key: header %v query %v", seen.header, seen.query)
				}
				if tc.method == http.MethodPost {
					if !strings.Contains(seen.body, `"query":"q"`) {
						t.Errorf("redirected body = %q, want the search request", seen.body)
					}
					if ct := seen.header.Get("Content-Type"); ct != "application/json" {
						t.Errorf("Content-Type = %q, want application/json", ct)
					}
				}
				if referer := seen.header.Get("Referer"); referer != "" {
					t.Errorf("redirected request carries Referer %q", referer)
				}
			})
		}
	}
}

func TestSearchProviders_RefuseRedirectsOffTheHost(t *testing.T) {
	assertRefused := func(t *testing.T, tc redirectProvider, status int, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("Search followed a redirect off the host")
		}
		if want := fmt.Sprintf(redirectRefusalFormat, tc.name, status); err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
		for _, leak := range []string{redirectTestKey, "secret query", "SECRET_BODY_MARKER", "localhost", "127.0.0.1"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error %q leaks %q", err, leak)
			}
		}
		var statusErr *searchStatusError
		if !errors.As(err, &statusErr) || statusErr.status != status {
			t.Fatalf("error = %v, want a searchStatusError for %d", err, status)
		}
		if got, want := statusErr.errorCode(), tool.HTTPFailureCode(status); got != want {
			t.Errorf("code = %q, want %q", got, want)
		}
	}
	for _, tc := range redirectProviders() {
		for _, status := range redirectStatuses {
			t.Run(tc.name+"_"+strconv.Itoa(status)+"_another_host", func(t *testing.T) {
				var hitsOnLocalhost, hitsOnOrigin atomic.Int32
				var redirector *httptest.Server
				redirector = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.Host, "localhost") {
						hitsOnLocalhost.Add(1)
						_, _ = w.Write([]byte(tc.answer))
						return
					}
					hitsOnOrigin.Add(1)
					w.Header().Set("Location", strings.Replace(redirector.URL, "127.0.0.1", "localhost", 1)+r.URL.RequestURI())
					w.WriteHeader(status)
					_, _ = w.Write([]byte("moved to SECRET_BODY_MARKER"))
				}))
				t.Cleanup(redirector.Close)

				_, err := tc.build(redirector.URL).Search(t.Context(), "secret query", 3, SearchOptions{})

				assertRefused(t, tc, status, err)
				if got := hitsOnLocalhost.Load(); got != 0 {
					t.Errorf("the other hostname, on the same port, received %d requests", got)
				}
				if got := hitsOnOrigin.Load(); got != 1 {
					t.Errorf("origin received %d requests, want 1", got)
				}
			})
			t.Run(tc.name+"_"+strconv.Itoa(status)+"_another_port", func(t *testing.T) {
				var elsewhere requestLog
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					elsewhere.add(r)
					_, _ = w.Write([]byte(tc.answer))
				}))
				t.Cleanup(target.Close)
				redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Location", target.URL+r.URL.RequestURI())
					w.WriteHeader(status)
					_, _ = w.Write([]byte("moved to SECRET_BODY_MARKER"))
				}))
				t.Cleanup(redirector.Close)

				_, err := tc.build(redirector.URL).Search(t.Context(), "secret query", 3, SearchOptions{})

				assertRefused(t, tc, status, err)
				if got := elsewhere.all(); len(got) != 0 {
					t.Errorf("redirect target received %d requests, headers %v", len(got), got[0].header)
				}
			})
		}
	}
}

func TestSearchProviders_UnfollowedRedirectStatuses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		location string
	}{
		{"multiple choices", http.StatusMultipleChoices, "/elsewhere"},
		{"not modified", http.StatusNotModified, ""},
		{"found without a location", http.StatusFound, ""},
	}
	for _, provider := range redirectProviders() {
		for _, tc := range cases {
			t.Run(provider.name+" "+tc.name, func(t *testing.T) {
				var hits requestLog
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.add(r)
					if tc.location != "" {
						w.Header().Set("Location", tc.location)
					}
					w.WriteHeader(tc.status)
				}))
				t.Cleanup(server.Close)

				_, err := provider.build(server.URL).Search(t.Context(), "q", 3, SearchOptions{})

				if want := fmt.Sprintf(redirectRefusalFormat, provider.name, tc.status); err == nil || err.Error() != want {
					t.Errorf("error = %v, want %q", err, want)
				}
				if got := len(hits.all()); got != 1 {
					t.Errorf("server received %d requests, want 1", got)
				}
			})
		}
	}
}

func TestSearchProviders_InvalidRedirectLocationIsNotEchoed(t *testing.T) {
	const location = "%zz?key=SECRET_IN_LOCATION&q=QUERY_IN_LOCATION"
	for _, tc := range redirectProviders() {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusFound)
			}))
			t.Cleanup(server.Close)

			_, err := tc.build(server.URL).Search(t.Context(), "q", 3, SearchOptions{})

			if want := tc.name + " search API answered with an invalid redirect location"; err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
	t.Run("the tool result", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusFound)
		}))
		t.Cleanup(server.Close)

		r, err := NewWebSearchTool(nil).Execute(t.Context(), map[string]any{"query": "QUERY_IN_LOCATION"}, tavilyToolCtx(server.URL))
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == tool.StatusSuccess {
			t.Fatalf("result = %+v, want a failure", r)
		}
		for _, leak := range []string{"SECRET_IN_LOCATION", "QUERY_IN_LOCATION", "%zz"} {
			if strings.Contains(r.Message, leak) {
				t.Errorf("message %q leaks %q", r.Message, leak)
			}
		}
	})
}

func TestSearchProviders_RedirectChainIsBounded(t *testing.T) {
	var hops requestLog
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.add(r)
		http.Redirect(w, r, r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)

	_, err := newTavilySearchProvider("SECRET_KEY", server.URL).Search(t.Context(), "q", 3, SearchOptions{})

	if err == nil || !strings.Contains(err.Error(), "answered with a redirect (HTTP 307) that is not followed") {
		t.Fatalf("error = %v, want the redirect answer", err)
	}
	if got, want := len(hops.all()), maxSearchRedirects+1; got != want {
		t.Errorf("server saw %d requests, want the original and %d hops", got, maxSearchRedirects)
	}
}

func TestCheckSearchRedirect(t *testing.T) {
	mustURL := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	cases := []struct {
		name     string
		original string
		previous string
		target   string
		follow   bool
	}{
		{"same host, new path", "http://search.internal/a", "http://search.internal/a", "http://search.internal/b", true},
		{"host compared without case", "http://Search.Internal/a", "http://Search.Internal/a", "http://search.internal/b", true},
		{"upgrade to https on the same host", "http://search.internal/a", "http://search.internal/a", "https://search.internal/a", true},
		{"upgrade to https on another port", "http://search.internal:8080/a", "http://search.internal:8080/a", "https://search.internal:8443/a", true},
		{"upgrade from an explicit default port", "http://search.internal:80/a", "http://search.internal:80/a", "https://search.internal/a", true},
		{"upgrade from a default port to an arbitrary one", "http://search.internal:80/a", "http://search.internal:80/a", "https://search.internal:9443/a", false},
		{"upgrade from an implicit default port to an arbitrary one", "http://search.internal/a", "http://search.internal/a", "https://search.internal:9443/a", false},
		{"upgrade from a numbered port to the explicit https default", "http://search.internal:8080/a", "http://search.internal:8080/a", "https://search.internal:443/a", true},
		{"upgrade from a numbered port to the implicit https default", "http://search.internal:8080/a", "http://search.internal:8080/a", "https://search.internal/a", true},
		{"downgrade to http", "https://search.internal/a", "https://search.internal/a", "http://search.internal/a", false},
		{"downgrade at a later hop", "http://search.internal/a", "https://search.internal/a", "http://search.internal/a", false},
		{"another host", "https://search.internal/a", "https://search.internal/a", "https://other.example/a", false},
		{"a subdomain", "https://search.internal/a", "https://search.internal/a", "https://api.search.internal/a", false},
		{"another host after a same-host hop", "https://search.internal/a", "https://search.internal/b", "https://other.example/a", false},
		{"same scheme, another port", "https://search.internal/a", "https://search.internal/a", "https://search.internal:8443/a", false},
		{"https to its explicit default port", "https://search.internal/a", "https://search.internal/a", "https://search.internal:443/a", true},
		{"https from its explicit default port", "https://search.internal:443/a", "https://search.internal:443/a", "https://search.internal/b", true},
		{"http to its explicit default port", "http://search.internal/a", "http://search.internal/a", "http://search.internal:80/a", true},
		{"https default port to http default port", "https://search.internal:443/a", "https://search.internal:443/a", "http://search.internal:80/a", false},
		{"a scheme that is not http", "http://search.internal/a", "http://search.internal/a", "ftp://search.internal/a", false},
		{"a scheme that is not http, from https", "https://search.internal/a", "https://search.internal/a", "gopher://search.internal/a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := &http.Request{URL: mustURL(tc.original)}
			previous := &http.Request{URL: mustURL(tc.previous)}
			req := &http.Request{URL: mustURL(tc.target), Header: http.Header{"Referer": {tc.previous + "?key=SECRET_KEY"}}}

			via := []*http.Request{original}
			if tc.original != tc.previous {
				via = append(via, previous)
			}
			err := checkSearchRedirect(req, via)

			if followed := err == nil; followed != tc.follow {
				t.Fatalf("followed = %v (err %v), want %v", followed, err, tc.follow)
			}
			if tc.follow && req.Header.Get("Referer") != "" {
				t.Errorf("a followed redirect kept Referer %q", req.Header.Get("Referer"))
			}
			if !tc.follow && !errors.Is(err, http.ErrUseLastResponse) {
				t.Errorf("err = %v, want http.ErrUseLastResponse", err)
			}
		})
	}
	t.Run("a hop that changes the method", func(t *testing.T) {
		original := &http.Request{Method: http.MethodPost, URL: mustURL("https://search.internal/a")}
		for method, follow := range map[string]bool{http.MethodPost: true, http.MethodGet: false} {
			req := &http.Request{Method: method, URL: mustURL("https://search.internal/b"), Header: http.Header{}}
			err := checkSearchRedirect(req, []*http.Request{original})
			if followed := err == nil; followed != follow {
				t.Errorf("%s after a POST: followed = %v (err %v), want %v", method, followed, err, follow)
			}
		}
	})
	t.Run("the hop limit", func(t *testing.T) {
		via := make([]*http.Request, maxSearchRedirects+1)
		for i := range via {
			via[i] = &http.Request{URL: mustURL("http://search.internal/a")}
		}
		req := &http.Request{URL: mustURL("http://search.internal/a"), Header: http.Header{}}
		if err := checkSearchRedirect(req, via[:maxSearchRedirects]); err != nil {
			t.Errorf("hop %d refused: %v", maxSearchRedirects, err)
		}
		if err := checkSearchRedirect(req, via); !errors.Is(err, http.ErrUseLastResponse) {
			t.Errorf("hop %d err = %v, want http.ErrUseLastResponse", maxSearchRedirects+1, err)
		}
	})
}

func TestSearchProviders_MissingResultsFieldIsAnError(t *testing.T) {
	for _, tc := range redirectProviders()[1:] {
		t.Run(tc.name+" without the field", func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{http.StatusOK, `{"requestId":"r"}`})

			_, err := tc.build(fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

			if want := tc.name + " search API returned no results field"; err == nil || err.Error() != want {
				t.Errorf("error = %v, want %q", err, want)
			}
		})
		for name, body := range map[string]string{"a string": `{"results":"oops"}`, "an object": `{"results":{}}`, "a number": `{"results":3}`} {
			t.Run(tc.name+" with "+name, func(t *testing.T) {
				fake := newProviderFake(t, fakeResponse{http.StatusOK, body})

				_, err := tc.build(fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

				if want := tc.name + " search API returned a results field that is not a list"; err == nil || err.Error() != want {
					t.Errorf("error = %v, want %q", err, want)
				}
			})
		}
		for name, body := range map[string]string{"empty list": `{"results":[]}`, "null list": `{"results":null}`} {
			t.Run(tc.name+" with an "+name, func(t *testing.T) {
				fake := newProviderFake(t, fakeResponse{http.StatusOK, body})

				results, err := tc.build(fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

				if err != nil || len(results) != 0 {
					t.Errorf("Search = %v, %v, want no results and no error", results, err)
				}
			})
		}
	}
	t.Run("google keeps reading a missing items list as no results", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusOK, `{"searchInformation":{"totalResults":"0"}}`})

		results, err := NewGoogleSearchProvider("K", "CX", fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

		if err != nil || len(results) != 0 {
			t.Errorf("Search = %v, %v, want no results and no error", results, err)
		}
	})
}

func TestSearchProviders_UnreadableSuccessNamesProviderAndStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		cause  func(error) bool
	}{
		{"html with a 200", http.StatusOK, "<html>SECRET_BODY_MARKER</html>", func(err error) bool {
			_, ok := errors.AsType[*json.SyntaxError](err)
			return ok
		}},
		{"truncated json with a 200", http.StatusOK, `{"results": [SECRET_BODY_MARKER`, func(err error) bool {
			_, ok := errors.AsType[*json.SyntaxError](err)
			return ok
		}},
		{"204 with no body", http.StatusNoContent, "", func(err error) bool { return errors.Is(err, io.EOF) }},
	}
	for _, provider := range redirectProviders() {
		for _, tc := range cases {
			t.Run(provider.name+" "+tc.name, func(t *testing.T) {
				fake := newProviderFake(t, fakeResponse{tc.status, tc.body})

				_, err := provider.build(fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

				wantPrefix := fmt.Sprintf("%s search API returned an unreadable response (HTTP %d): ", provider.name, tc.status)
				if err == nil || !strings.HasPrefix(err.Error(), wantPrefix) {
					t.Fatalf("error = %v, want it to start with %q", err, wantPrefix)
				}
				if !tc.cause(err) {
					t.Errorf("error %v does not unwrap to the decode cause", err)
				}
				if strings.Contains(err.Error(), "SECRET_BODY_MARKER") {
					t.Errorf("error %q leaks the body", err)
				}
				if r := tool.Error("x", ErrorCode(err)); r.ErrorCode != "" {
					t.Errorf("code = %q, want none for a 2xx", r.ErrorCode)
				}
			})
		}
		t.Run(provider.name+" 202 with a json body", func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{http.StatusAccepted, `{"results":[{"title":"SECRET_BODY_MARKER"}]}`})

			_, err := provider.build(fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

			want := provider.name + " search API answered with an unexpected success status (HTTP 202)"
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

func TestSearchProviders_ResponseSizeIsBounded(t *testing.T) {
	padded := func(size int) string {
		const head, tail = `{"results":[],"pad":"`, `"}`
		return head + strings.Repeat("x", size-len(head)-len(tail)) + tail
	}
	for _, provider := range redirectProviders() {
		t.Run(provider.name+" over the bound", func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{http.StatusOK, padded(maxSearchResponseBytes + 1)})

			_, err := provider.build(fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

			want := fmt.Sprintf("%s search API returned an unreadable response (HTTP 200): the response is larger than 8 MiB", provider.name)
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("error = %v, want it to start with %q", err, want)
			}
			if !errors.Is(err, errSearchResponseTooLarge) {
				t.Errorf("error %v does not unwrap to errSearchResponseTooLarge", err)
			}
			if got := len(fake.recorded()); got != 1 {
				t.Errorf("requests = %d, want 1: an oversized answer is not retried", got)
			}
		})
	}
	t.Run("at the bound", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusOK, padded(maxSearchResponseBytes)})

		results, err := newTavilySearchProvider("K", fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

		if err != nil || len(results) != 0 {
			t.Errorf("Search = %v, %v, want no results and no error", results, err)
		}
	})
}

func TestSearchProviders_DeadlineWhileReadingTheBodyIsTheContexts(t *testing.T) {
	for _, provider := range redirectProviders() {
		t.Run(provider.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results": [`))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()

			_, err := provider.build(server.URL).Search(ctx, "q", 3, SearchOptions{})

			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want one that unwraps to the context deadline", err)
			}
			if strings.Contains(err.Error(), "unreadable") {
				t.Errorf("error %q blames the provider for the deadline", err)
			}
			if got, want := tool.Error("x", ErrorCode(err)).ErrorCode, tool.DispatchErrorCode(taxonomy.DispatchClassTimeout); got != want {
				t.Errorf("code = %q, want %q", got, want)
			}
		})
	}
}

func TestSearchProviders_ProviderErrorTextIsCapped(t *testing.T) {
	for name, filler := range map[string]string{"ascii": "x", "multibyte": "é"} {
		t.Run(name, func(t *testing.T) {
			long := strings.Repeat(filler, 500)
			fake := newProviderFake(t, fakeResponse{http.StatusBadRequest, `{"detail":{"error":"` + long + `"}}`})

			_, err := newTavilySearchProvider("K", fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})

			if err == nil {
				t.Fatal("want an error")
			}
			prefix := "tavily search API error (400): "
			if got := len(err.Error()) - len(prefix); got > maxSearchErrorTextBytes+len("...") {
				t.Errorf("error text is %d bytes, want at most %d", got, maxSearchErrorTextBytes+len("..."))
			}
			if !utf8.ValidString(err.Error()) {
				t.Errorf("error %q is not valid UTF-8", err)
			}
		})
	}
	t.Run("a short text is kept whole", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusBadRequest, `{"detail":{"error":"bad query"}}`})
		_, err := newTavilySearchProvider("K", fake.server.URL).Search(t.Context(), "q", 3, SearchOptions{})
		if err == nil || err.Error() != "tavily search API error (400): bad query" {
			t.Errorf("error = %v", err)
		}
	})
}

func TestResolveSearchProvider_APIBaseDiagnostics(t *testing.T) {
	google := func(extra map[string]any) map[string]any {
		return withConfig(map[string]any{"provider": "google", "google_search_api_key": "g", "google_cse_id": "cx"}, extra)
	}
	t.Run("google without a base points at api_base, not a connector", func(t *testing.T) {
		_, err := resolveSearchProvider(google(nil), deepResearchProviderNames)
		if err == nil {
			t.Fatal("want an error")
		}
		for _, want := range []string{"api_base", "google_api_base", "no public-endpoint fallback"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "connector") {
			t.Errorf("error %q sends the operator to a connector that does not offer google", err)
		}
	})
	t.Run("tavily still points at its connector", func(t *testing.T) {
		_, err := resolveSearchProvider(map[string]any{"provider": "tavily", "api_key": "k"}, webSearchProviderNames)
		if err == nil || !strings.Contains(err.Error(), "Web Search connector") {
			t.Errorf("error = %v, want the connector hint", err)
		}
	})
	cases := []struct {
		name string
		cfg  map[string]any
		key  string
	}{
		{"tavily number", map[string]any{"provider": "tavily", "api_key": "k", "api_base": 8080}, "api_base"},
		{"exa map", map[string]any{"provider": "exa", "api_key": "k", "api_base": map[string]any{"url": "http://x"}}, "api_base"},
		{"google api_base number", google(map[string]any{"api_base": 8080}), "api_base"},
		{"google google_api_base list", google(map[string]any{"google_api_base": []any{"http://x"}}), "google_api_base"},
	}
	for _, tc := range cases {
		t.Run("non-string "+tc.name, func(t *testing.T) {
			_, err := resolveSearchProvider(tc.cfg, deepResearchProviderNames)
			if err == nil {
				t.Fatal("want an error")
			}
			if want := "web search " + tc.key + " must be a URL string, got "; !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
			if strings.Contains(err.Error(), "no API base") {
				t.Errorf("error %q reads the bad value as unset", err)
			}
		})
	}
	t.Run("a null api_base is unset", func(t *testing.T) {
		_, err := resolveSearchProvider(map[string]any{"provider": "tavily", "api_key": "k", "api_base": nil}, webSearchProviderNames)
		if err == nil || !strings.Contains(err.Error(), "no API base") {
			t.Errorf("error = %v, want the missing-base error", err)
		}
	})
	t.Run("google falls back to google_api_base only when api_base is empty", func(t *testing.T) {
		p, err := resolveSearchProvider(google(map[string]any{"api_base": "  ", "google_api_base": "http://cluster.internal/"}), deepResearchProviderNames)
		if err != nil || p.(*GoogleSearchProvider).baseURL != "http://cluster.internal" {
			t.Errorf("provider = %v, err = %v, want the cluster default without its trailing slash", p, err)
		}
	})
}

func assertJSONFields(t *testing.T, body, want map[string]any) {
	t.Helper()
	for field, wantVal := range want {
		gotJSON, _ := json.Marshal(body[field])
		wantJSON, _ := json.Marshal(wantVal)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("body field %s = %s, want %s", field, gotJSON, wantJSON)
		}
	}
}

func TestSearchProviders_MalformedHeaderIsNotEchoed(t *testing.T) {
	for _, tc := range redirectProviders() {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ln.Close() })
			go func() {
				conn, acceptErr := ln.Accept()
				if acceptErr != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				_, _ = bufio.NewReader(conn).ReadString('\n')
				_, _ = io.WriteString(conn, "HTTP/1.1 302 Found\r\nLocation: http://h/?key=SECRET_IN_HEADER&q=QUERY_IN_HEADER\x01\r\nContent-Length: 0\r\n\r\n")
			}()

			_, err = tc.build("http://"+ln.Addr().String()).Search(t.Context(), "q", 3, SearchOptions{})

			if err == nil {
				t.Fatal("want an error")
			}
			for _, leak := range []string{"SECRET_IN_HEADER", "QUERY_IN_HEADER"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q leaks %q", err, leak)
				}
			}
		})
	}
}

func TestSearchProviders_ClientTimeoutReadingTheBodyIsATimeout(t *testing.T) {
	previous := searchHTTPClient.Timeout
	searchHTTPClient.Timeout = 200 * time.Millisecond
	t.Cleanup(func() { searchHTTPClient.Timeout = previous })
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"results": [`)
		w.(http.Flusher).Flush()
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	_, err := redirectProviders()[0].build(server.URL).Search(t.Context(), "q", 3, SearchOptions{})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want it to wrap a deadline", err)
	}
	if got, want := tool.Error("x", ErrorCode(err)).ErrorCode, tool.DispatchErrorCode(taxonomy.DispatchClassTimeout); got != want {
		t.Errorf("code = %q, want %q", got, want)
	}
}

func TestSearchProviders_TransportErrorsNameTheCause(t *testing.T) {
	const query = "QUERY_IN_TRANSPORT"
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(untrusted.Close)
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(plain.Close)
	closer, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	go func() {
		for {
			conn, acceptErr := closer.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	cases := []struct {
		name string
		base string
		want []string
	}{
		{"an untrusted certificate", untrusted.URL, []string{"certificate"}},
		{"an https api_base on a plain HTTP port", "https://" + strings.TrimPrefix(plain.URL, "http://"), []string{"HTTP response to HTTPS client"}},
		{"a connection closed before a response", "http://" + closer.Addr().String(), []string{"EOF", "connection reset", "closed"}},
	}
	for _, provider := range redirectProviders() {
		for _, tc := range cases {
			t.Run(provider.name+"/"+tc.name, func(t *testing.T) {
				_, err := provider.build(tc.base).Search(t.Context(), query, 3, SearchOptions{})
				if err == nil || !slices.ContainsFunc(tc.want, func(cause string) bool { return strings.Contains(err.Error(), cause) }) {
					t.Fatalf("error = %v, want it to name one of %q", err, tc.want)
				}
				for _, leak := range []string{redirectTestKey, query} {
					if strings.Contains(err.Error(), leak) {
						t.Errorf("error %q leaks %q", err, leak)
					}
				}
			})
		}
	}
}

func TestSearchProviders_ClientTimeoutBeforeTheHeadersIsATimeout(t *testing.T) {
	previous := searchHTTPClient.Timeout
	searchHTTPClient.Timeout = 200 * time.Millisecond
	t.Cleanup(func() { searchHTTPClient.Timeout = previous })
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	_, err := redirectProviders()[0].build(server.URL).Search(t.Context(), "q", 3, SearchOptions{})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want it to wrap a deadline", err)
	}
	if got, want := tool.Error("x", ErrorCode(err)).ErrorCode, tool.DispatchErrorCode(taxonomy.DispatchClassTimeout); got != want {
		t.Errorf("code = %q, want %q", got, want)
	}
}

func TestSearchProviders_CancelInFlightIsInterrupted(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)

	_, err := redirectProviders()[1].build(server.URL).Search(ctx, "q", 3, SearchOptions{})

	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("error = %v, want an interrupted search wrapping context.Canceled", err)
	}
}

func TestSearchProviders_ResponseParseErrorsNeverEchoTheKeyOrQuery(t *testing.T) {
	const query = "QUERY_CANARY"
	echo := redirectTestKey + " " + query
	rows := map[string]string{
		"a malformed status code":     "HTTP/1.1 2x0 " + echo + "\r\n\r\n",
		"a malformed HTTP version":    echo + " 200 OK\r\n\r\n",
		"a header with a control":     "HTTP/1.1 200 OK\r\nX-Echo: " + echo + "\x01\r\n\r\n",
		"a header line with no colon": "HTTP/1.1 200 OK\r\n" + echo + "\r\n\r\n",
		"a bad Transfer-Encoding":     "HTTP/1.1 200 OK\r\nTransfer-Encoding: " + echo + "\r\n\r\n",
		"a bad Content-Length":        "HTTP/1.1 200 OK\r\nContent-Length: " + echo + "\r\n\r\n",
		"a duplicate Content-Length":  "HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: " + echo + "\r\n\r\n",
	}
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for _, provider := range redirectProviders() {
		for name, response := range rows {
			t.Run(provider.name+"/"+name, func(t *testing.T) {
				logged.Reset()
				base := rawResponseServer(t, response)

				_, err := provider.build(base).Search(t.Context(), query, 3, SearchOptions{})

				if want := provider.name + " search API sent a malformed response"; err == nil || err.Error() != want {
					t.Fatalf("error = %v, want %q", err, want)
				}
				if got := strings.Count(logged.String(), "web search transport error withheld"); got != 1 {
					t.Errorf("withheld warnings = %d, want 1; logs:\n%s", got, logged.String())
				}
				for _, leak := range []string{redirectTestKey, query} {
					if strings.Contains(logged.String(), leak) {
						t.Errorf("logs leak %q:\n%s", leak, logged.String())
					}
				}
			})
		}
	}
}

func rawResponseServer(t *testing.T, response string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = bufio.NewReader(conn).ReadString('\n')
				_, _ = io.WriteString(conn, response)
			}()
		}
	}()
	return "http://" + ln.Addr().String()
}

func TestSearchProviders_FailureAfterAFollowedRedirectNamesItsCause(t *testing.T) {
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(untrusted.Close)
	target := strings.Replace(untrusted.URL, "127.0.0.1", "localhost", 1)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.RequestURI(), http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	base := strings.Replace(redirector.URL, "127.0.0.1", "localhost", 1)

	_, err := redirectProviders()[0].build(base).Search(t.Context(), "q", 3, SearchOptions{})

	if err == nil || !strings.Contains(err.Error(), "certificate") || strings.Contains(err.Error(), "malformed") {
		t.Fatalf("error = %v, want the hop-2 certificate failure named", err)
	}
}

func TestSearchProviders_StallAfterTheStatusLineIsATimeout(t *testing.T) {
	previous := searchHTTPClient.Timeout
	searchHTTPClient.Timeout = 200 * time.Millisecond
	t.Cleanup(func() { searchHTTPClient.Timeout = previous })
	var logged bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = bufio.NewReader(conn).ReadString('\n')
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n")
		time.Sleep(time.Second)
	}()

	_, err = redirectProviders()[0].build("http://"+ln.Addr().String()).Search(t.Context(), "q", 3, SearchOptions{})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want it to wrap a deadline", err)
	}
	if got, want := tool.Error("x", ErrorCode(err)).ErrorCode, tool.DispatchErrorCode(taxonomy.DispatchClassTimeout); got != want {
		t.Errorf("code = %q, want %q", got, want)
	}
	if strings.Contains(logged.String(), "withheld") {
		t.Errorf("a timeout was withheld as malformed:\n%s", logged.String())
	}
}

func TestSearchProviders_MisconfiguredAPIBaseNamesItsCause(t *testing.T) {
	t.Parallel()
	for base, want := range map[string]string{
		"search.example.com/x": "unsupported protocol scheme",
		"http:///x":            "no Host in request URL",
	} {
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			_, err := redirectProviders()[0].build(base).Search(t.Context(), "q", 3, SearchOptions{})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want it to name %q", err, want)
			}
		})
	}
}

func TestSearchProviders_ProxyRefusalIsReportedByStatusOnly(t *testing.T) {
	proxy := rawResponseServer(t, "HTTP/1.1 407 "+redirectTestKey+" QUERY_CANARY\r\nContent-Length: 0\r\n\r\n")
	proxyURL, err := url.Parse(proxy)
	if err != nil {
		t.Fatal(err)
	}
	transport := newSearchTransport()
	transport.Proxy = http.ProxyURL(proxyURL)
	previous := searchHTTPClient.Transport
	searchHTTPClient.Transport = transport
	t.Cleanup(func() { searchHTTPClient.Transport = previous })

	_, err = redirectProviders()[1].build("https://search.example.invalid").Search(t.Context(), "QUERY_CANARY", 3, SearchOptions{})

	if err == nil || !strings.Contains(err.Error(), "the proxy refused the connection (HTTP 407)") {
		t.Fatalf("error = %v, want the refusal by status", err)
	}
	for _, leak := range []string{redirectTestKey, "QUERY_CANARY"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q leaks %q", err, leak)
		}
	}
}

func TestSearchCall_BodyReadErrorWithholdsUnknownCauses(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	call := searchCall{provider: "tavily"}

	err := call.bodyReadError(t.Context(), errors.New("trailer echo "+redirectTestKey))

	if err == nil || err.Error() != "parse search response: the body could not be read" {
		t.Fatalf("error = %v, want the fixed message", err)
	}
	logs := logged.String()
	if strings.Count(logs, "level=WARN") != 1 || !strings.Contains(logs, "web search transport error withheld") ||
		!strings.Contains(logs, `errorType=*errors.errorString`) {
		t.Errorf("want one WARN naming the error type; logs:\n%s", logs)
	}
	if strings.Contains(logs, redirectTestKey) {
		t.Errorf("logs leak the withheld cause:\n%s", logs)
	}

	var syntax *json.SyntaxError
	kept := call.bodyReadError(t.Context(), json.Unmarshal([]byte("{x"), &map[string]any{}))
	if !errors.As(kept, &syntax) {
		t.Errorf("error = %v, want the JSON syntax error kept", kept)
	}
}

func TestSearchHTTPClient_UsesTheProxyAwareSearchTransport(t *testing.T) {
	t.Parallel()
	transport, ok := searchHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", searchHTTPClient.Transport)
	}
	if transport.OnProxyConnectResponse == nil {
		t.Error("OnProxyConnectResponse is nil: a CONNECT refusal would report the proxy's reason phrase")
	}
	if transport.Proxy == nil {
		t.Error("Proxy is nil: HTTPS_PROXY / HTTP_PROXY / NO_PROXY would be ignored")
	}
}

func TestSearchProviders_SearchThroughAnAcceptingProxy(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, tavilyCannedResponse)
	}))
	t.Cleanup(target.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n")
		go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := newSearchTransport()
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.TLSClientConfig = target.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	previous := searchHTTPClient.Transport
	searchHTTPClient.Transport = transport
	t.Cleanup(func() { searchHTTPClient.Transport = previous })

	results, err := newTavilySearchProvider(redirectTestKey, target.URL).Search(t.Context(), "q", 3, SearchOptions{})

	if err != nil {
		t.Fatalf("search through the proxy: %v", err)
	}
	if len(results) == 0 {
		t.Error("search through the proxy returned no results")
	}
}

func TestSearchProviders_ProxyRefusalOtherStatuses(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			proxy := rawResponseServer(t, fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Length: 0\r\n\r\n", status, redirectTestKey))
			proxyURL, err := url.Parse(proxy)
			if err != nil {
				t.Fatal(err)
			}
			transport := newSearchTransport()
			transport.Proxy = http.ProxyURL(proxyURL)
			previous := searchHTTPClient.Transport
			searchHTTPClient.Transport = transport
			t.Cleanup(func() { searchHTTPClient.Transport = previous })

			_, err = redirectProviders()[2].build("https://search.example.invalid").Search(t.Context(), "q", 3, SearchOptions{})

			want := fmt.Sprintf("the proxy refused the connection (HTTP %d)", status)
			if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), redirectTestKey) {
				t.Fatalf("error = %v, want %q with no echo", err, want)
			}
		})
	}
}
