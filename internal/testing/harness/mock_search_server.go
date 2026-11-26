// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

type PrimedSearchResponse struct {
	Response        string                 `yaml:"response"`
	StatusCode      int                    `yaml:"status_code"`
	ExpectedRequest *ExpectedSearchRequest `yaml:"expected_request"`
}

type ExpectedSearchRequest struct {
	Headers       map[string]string `yaml:"headers"`
	HeadersAbsent []string          `yaml:"headers_absent"`
	Query         map[string]string `yaml:"query"`
	QueryAbsent   []string          `yaml:"query_absent"`
	BodyContains  map[string]any    `yaml:"body_contains"`
	BodyAbsent    []string          `yaml:"body_absent"`
}

type PrimedPageResponse struct {
	URL        string `yaml:"url"`
	Response   string `yaml:"response"`
	StatusCode int    `yaml:"status_code"`
}

const (
	mockGoogleSearchPath = "/customsearch/v1"
	mockTavilyBasePath   = "/tavily"
	mockExaBasePath      = "/exa"
	mockSearchRoute      = "/search"
)

var mockProviderBasePaths = map[string]string{
	"tavily": mockTavilyBasePath,
	"exa":    mockExaBasePath,
}

type servedSearch struct {
	provider string
	primed   PrimedSearchResponse
	header   http.Header
	query    url.Values
	body     []byte
}

type MockSearchServer struct {
	server *httptest.Server

	mu             sync.Mutex
	searchQueues   map[string][]PrimedSearchResponse
	pageResponses  []PrimedPageResponse
	served         []servedSearch
	searchRequests int
}

func NewMockSearchServer(searchQueues map[string][]PrimedSearchResponse, pageResponses []PrimedPageResponse) *MockSearchServer {
	m := &MockSearchServer{
		searchQueues:  searchQueues,
		pageResponses: pageResponses,
	}

	mux := http.NewServeMux()
	mux.HandleFunc(mockGoogleSearchPath, m.searchHandler("google"))
	mux.HandleFunc(mockTavilyBasePath+mockSearchRoute, m.searchHandler("tavily"))
	mux.HandleFunc(mockExaBasePath+mockSearchRoute, m.searchHandler("exa"))
	mux.HandleFunc("/fetch", m.handlePageFetch)
	mux.HandleFunc("/", m.handleUnknownPath)

	m.server = httptest.NewServer(mux)
	return m
}

func (m *MockSearchServer) URL() string {
	return m.server.URL
}

func (m *MockSearchServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

func (m *MockSearchServer) searchHandler(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		m.mu.Lock()
		defer m.mu.Unlock()

		m.searchRequests++
		queue := m.searchQueues[provider]
		if len(queue) == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprintf(w, `{"error": {"message": "No primed %s search response available"}}`, provider)
			return
		}
		resp := queue[0]
		m.searchQueues[provider] = queue[1:]
		m.served = append(m.served, servedSearch{
			provider: provider,
			primed:   resp,
			header:   r.Header.Clone(),
			query:    r.URL.Query(),
			body:     body,
		})

		statusCode := resp.StatusCode
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = fmt.Fprint(w, resp.Response)
	}
}

func (m *MockSearchServer) handleUnknownPath(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	m.searchRequests++
	m.mu.Unlock()
	w.WriteHeader(http.StatusNotFound)
}

func (m *MockSearchServer) handlePageFetch(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.pageResponses) == 0 {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, "No primed page response available")
		return
	}

	resp := m.pageResponses[0]
	m.pageResponses = m.pageResponses[1:]

	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprint(w, resp.Response)
}

func (m *MockSearchServer) assertRequests(t testing.TB) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.served {
		if s.primed.ExpectedRequest == nil {
			continue
		}
		for _, problem := range s.primed.ExpectedRequest.mismatches(s) {
			t.Errorf("search request %d (%s): %s", i, s.provider, problem)
		}
	}
	for provider, queue := range m.searchQueues {
		for i, r := range queue {
			if r.ExpectedRequest != nil {
				t.Errorf("primed %s search response %d expects a request but none reached it", provider, i)
			}
		}
	}
}

func (e *ExpectedSearchRequest) mismatches(s servedSearch) []string {
	var problems []string
	for name, want := range e.Headers {
		if got := s.header.Get(name); !matchesExpectedValue(got, want) {
			problems = append(problems, fmt.Sprintf("header %s = %q, want %q", name, got, want))
		}
	}
	for _, name := range e.HeadersAbsent {
		if s.header.Get(name) != "" {
			problems = append(problems, fmt.Sprintf("header %s is present, want absent", name))
		}
	}
	for name, want := range e.Query {
		if got := s.query.Get(name); !matchesExpectedValue(got, want) {
			problems = append(problems, fmt.Sprintf("query %s = %q, want %q", name, got, want))
		}
	}
	for _, name := range e.QueryAbsent {
		if s.query.Has(name) {
			problems = append(problems, fmt.Sprintf("query %s is present, want absent", name))
		}
	}
	if len(e.BodyContains) == 0 && len(e.BodyAbsent) == 0 {
		return problems
	}
	var body map[string]any
	if err := json.Unmarshal(s.body, &body); err != nil {
		return append(problems, fmt.Sprintf("body is not a JSON object: %v", err))
	}
	for field, want := range e.BodyContains {
		got, ok := body[field]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("body field %s is missing", field))
		case want == "*":
		default:
			wantJSON, _ := json.Marshal(want)
			gotJSON, _ := json.Marshal(got)
			if string(wantJSON) != string(gotJSON) {
				problems = append(problems, fmt.Sprintf("body field %s = %s, want %s", field, gotJSON, wantJSON))
			}
		}
	}
	for _, field := range e.BodyAbsent {
		if _, ok := body[field]; ok {
			problems = append(problems, fmt.Sprintf("body field %s is present, want absent", field))
		}
	}
	return problems
}

func matchesExpectedValue(got, want string) bool {
	if want == "*" {
		return got != ""
	}
	return got == want
}

func (h *TestHarness) AssertSearchRequests(t testing.TB) {
	t.Helper()
	if h.searchServer != nil {
		h.searchServer.assertRequests(t)
	}
	if err := checkSearchRequestCount(h.scenario.ExpectedSearchRequestCount, h.searchServer); err != nil {
		t.Error(err)
	}
}

func checkSearchRequestCount(want *int, server *MockSearchServer) error {
	if want == nil {
		return nil
	}
	got := 0
	if server != nil {
		server.mu.Lock()
		got = server.searchRequests
		server.mu.Unlock()
	}
	if got != *want {
		return fmt.Errorf("search providers received %d requests, want %d", got, *want)
	}
	return nil
}
