// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type capturingT struct {
	testing.TB
	errors []string
}

func (r *capturingT) Helper() {}

func (r *capturingT) Error(args ...any) { r.errors = append(r.errors, fmt.Sprint(args...)) }

func (r *capturingT) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestAssertSearchRequests_Count(t *testing.T) {
	zero, one := 0, 1
	post := func(t *testing.T, server *MockSearchServer, path string) {
		t.Helper()
		resp, err := http.Post(server.URL()+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	assertSearchRequests := func(want *int, server *MockSearchServer) []string {
		recorder := &capturingT{TB: t}
		h := &TestHarness{scenario: &TestScenario{ExpectedSearchRequestCount: want}, searchServer: server}
		h.AssertSearchRequests(recorder)
		return recorder.errors
	}
	newServer := func(t *testing.T, queues map[string][]PrimedSearchResponse) *MockSearchServer {
		server := NewMockSearchServer(queues, nil)
		t.Cleanup(server.Close)
		return server
	}

	t.Run("no expectation never fails", func(t *testing.T) {
		server := newServer(t, nil)
		post(t, server, mockTavilyBasePath+mockSearchRoute)
		if got := assertSearchRequests(nil, server); len(got) != 0 {
			t.Errorf("errors = %v, want none", got)
		}
	})
	t.Run("zero holds when nothing was requested", func(t *testing.T) {
		if got := assertSearchRequests(&zero, newServer(t, nil)); len(got) != 0 {
			t.Errorf("errors = %v, want none", got)
		}
		if got := assertSearchRequests(&zero, nil); len(got) != 0 {
			t.Errorf("without a server: errors = %v, want none", got)
		}
	})
	t.Run("zero fails on a request no primed response answered", func(t *testing.T) {
		server := newServer(t, nil)
		post(t, server, mockExaBasePath+mockSearchRoute)
		got := assertSearchRequests(&zero, server)
		if len(got) != 1 || !strings.Contains(got[0], "received 1 requests, want 0") {
			t.Errorf("errors = %v, want a count mismatch", got)
		}
	})
	t.Run("zero fails on a request to a path no provider is served on", func(t *testing.T) {
		server := newServer(t, nil)
		post(t, server, "/brave/search")
		got := assertSearchRequests(&zero, server)
		if len(got) != 1 || !strings.Contains(got[0], "received 1 requests, want 0") {
			t.Errorf("errors = %v, want a count mismatch", got)
		}
	})
	t.Run("a page fetch is not a search request", func(t *testing.T) {
		server := newServer(t, nil)
		post(t, server, "/fetch")
		if got := assertSearchRequests(&zero, server); len(got) != 0 {
			t.Errorf("errors = %v, want none", got)
		}
	})
	t.Run("a count of one", func(t *testing.T) {
		server := newServer(t, map[string][]PrimedSearchResponse{"tavily": {{Response: "{}"}}})
		if got := assertSearchRequests(&one, server); len(got) != 1 {
			t.Errorf("errors = %v, want a mismatch before any request", got)
		}
		post(t, server, mockTavilyBasePath+mockSearchRoute)
		if got := assertSearchRequests(&one, server); len(got) != 0 {
			t.Errorf("errors = %v, want none", got)
		}
	})
}
