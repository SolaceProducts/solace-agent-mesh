// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
)

const (
	searchProviderTavily = "tavily"
	searchProviderExa    = "exa"
	searchProviderGoogle = "google"
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

type testCSEServer struct {
	mu             sync.Mutex
	capturedParams []url.Values
	server         *httptest.Server
}

func newTestCSEServer(t *testing.T, response string, status int) *testCSEServer {
	t.Helper()
	s := &testCSEServer{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.capturedParams = append(s.capturedParams, r.URL.Query())
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(s.server.Close)
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
