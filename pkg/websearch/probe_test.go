// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	tavilyProbeResults = `{"results":[{"title":"Agent Mesh","url":"https://example.com/","content":"An agent mesh."}]}`
	exaProbeResults    = `{"results":[{"title":"Agent Mesh","url":"https://example.com/","highlights":["An agent mesh."]}]}`
)

func TestProbeWebSearch_ClassifiesTheAnswer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		provider string
		response fakeResponse
		want     WebSearchProbeOutcome
	}{
		{"tavily accepts the key", searchProviderTavily, fakeResponse{http.StatusOK, tavilyProbeResults}, WebSearchProbeAccepted},
		{"exa accepts the key", searchProviderExa, fakeResponse{http.StatusOK, exaProbeResults}, WebSearchProbeAccepted},
		{"tavily rejects the key", searchProviderTavily, fakeResponse{http.StatusUnauthorized, `{"detail":{"error":"Unauthorized: missing or invalid API key."}}`}, WebSearchProbeKeyRejected},
		{"exa rejects the key", searchProviderExa, fakeResponse{http.StatusUnauthorized, `{"error":"Invalid API key"}`}, WebSearchProbeKeyRejected},
		{"a 429 is a rate limit", searchProviderTavily, fakeResponse{http.StatusTooManyRequests, `{"detail":{"error":"rate limited"}}`}, WebSearchProbeRateLimited},
		{"tavily's plan limit is a rate limit", searchProviderTavily, fakeResponse{tavilyStatusPlanLimit, `{"detail":{"error":"plan limit"}}`}, WebSearchProbeRateLimited},
		{"exa's spent credits are a rate limit", searchProviderExa, fakeResponse{exaStatusOutOfCredits, `{"error":"out of credits"}`}, WebSearchProbeRateLimited},
		{"a 403 says nothing about the key", searchProviderTavily, fakeResponse{http.StatusForbidden, `{"detail":{"error":"forbidden"}}`}, WebSearchProbeFailed},
		{"a server error says nothing about the key", searchProviderExa, fakeResponse{http.StatusBadGateway, `{"error":"bad gateway"}`}, WebSearchProbeFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := newProviderFake(t, tc.response)

			got := ProbeWebSearch(t.Context(), fake.server.Client().Transport, tc.provider, "probe-key", fake.server.URL)

			if got.Outcome != tc.want {
				t.Fatalf("Outcome = %q (status %d, err %v), want %q", got.Outcome, got.Status, got.Err, tc.want)
			}
			if tc.want == WebSearchProbeAccepted && got.Err != nil {
				t.Errorf("Err = %v on an accepted key", got.Err)
			}
			if tc.want != WebSearchProbeAccepted && got.Status != tc.response.status {
				t.Errorf("Status = %d, want %d", got.Status, tc.response.status)
			}
			if got.Err != nil && strings.Contains(got.Err.Error(), "probe-key") {
				t.Errorf("Err carries the key: %v", got.Err)
			}
		})
	}
}

func TestProbeWebSearch_SendsOneSmallSearchWithTheKey(t *testing.T) {
	t.Parallel()
	tavily := newProviderFake(t, fakeResponse{http.StatusOK, tavilyProbeResults})
	exa := newProviderFake(t, fakeResponse{http.StatusOK, exaProbeResults})

	ProbeWebSearch(t.Context(), tavily.server.Client().Transport, "Tavily", "tavily-key", tavily.server.URL+"/")
	ProbeWebSearch(t.Context(), exa.server.Client().Transport, searchProviderExa, "exa-key", exa.server.URL)

	tavilyRequest := tavily.only(t)
	if got := tavilyRequest.header.Get("Authorization"); got != "Bearer tavily-key" {
		t.Errorf("Tavily Authorization = %q", got)
	}
	if tavilyRequest.path != tavilySearchPath || tavilyRequest.body["max_results"] != float64(1) {
		t.Errorf("Tavily request = %s %v, want one result from %s", tavilyRequest.path, tavilyRequest.body, tavilySearchPath)
	}
	exaRequest := exa.only(t)
	if got := exaRequest.header.Get("x-api-key"); got != "exa-key" {
		t.Errorf("Exa x-api-key = %q", got)
	}
	if exaRequest.body["numResults"] != float64(1) {
		t.Errorf("Exa numResults = %v, want 1", exaRequest.body["numResults"])
	}
}

func TestProbeWebSearch_DoesNotWaitOutA429(t *testing.T) {
	t.Parallel()
	fake := newProviderFake(t, fakeResponse{http.StatusTooManyRequests, `{"detail":{"error":"slow down"}}`})

	start := time.Now()
	got := ProbeWebSearch(t.Context(), fake.server.Client().Transport, searchProviderTavily, "k", fake.server.URL)

	if got.Outcome != WebSearchProbeRateLimited {
		t.Fatalf("Outcome = %q, want %q", got.Outcome, WebSearchProbeRateLimited)
	}
	if n := len(fake.recorded()); n != 1 {
		t.Errorf("sent %d requests, want 1", n)
	}
	if elapsed := time.Since(start); elapsed >= searchQuotaRetryBackoff[0] {
		t.Errorf("took %s, so the probe waited for a quota retry", elapsed)
	}
}

func TestProbeWebSearch_UnreachableAPIIsAFailureWithNoStatus(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := listener.Addr().String()
	_ = listener.Close()

	got := ProbeWebSearch(t.Context(), &http.Transport{}, searchProviderExa, "k", "http://"+closedAddr)

	if got.Outcome != WebSearchProbeFailed || got.Status != 0 || got.Err == nil {
		t.Fatalf("got %+v, want a failure with no status and a cause", got)
	}
}

func TestProbeWebSearch_SendsThroughTheCallersTransport(t *testing.T) {
	t.Parallel()
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyProbeResults})
	var used bool
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return fake.server.Client().Transport.RoundTrip(r)
	})

	got := ProbeWebSearch(t.Context(), transport, searchProviderTavily, "k", fake.server.URL)

	if got.Outcome != WebSearchProbeAccepted || !used {
		t.Fatalf("Outcome = %q, transport used = %v", got.Outcome, used)
	}
}

func TestProbeWebSearch_RefusesWhatAConnectorCannotName(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][3]string{
		"google":          {searchProviderGoogle, "k", "http://search.internal"},
		"an unknown name": {"bing", "k", "http://search.internal"},
		"no key":          {searchProviderTavily, " ", "http://search.internal"},
		"no api base":     {searchProviderExa, "k", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ProbeWebSearch(t.Context(), &http.Transport{}, args[0], args[1], args[2])
			if got.Outcome != WebSearchProbeFailed || got.Err == nil || got.Status != 0 {
				t.Fatalf("got %+v, want a failure that sent nothing", got)
			}
		})
	}
}

func TestProbeWebSearch_RefusesANilTransport(t *testing.T) {
	t.Parallel()
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyProbeResults})

	got := ProbeWebSearch(t.Context(), nil, searchProviderTavily, "k", fake.server.URL)

	if got.Outcome != WebSearchProbeFailed || got.Err == nil || got.Status != 0 {
		t.Fatalf("got %+v, want a failure", got)
	}
	if n := len(fake.recorded()); n != 0 {
		t.Errorf("sent %d requests with no transport", n)
	}
}

func TestProbeWebSearch_AnAnswerThatIsNotSearchResultsIsItsOwnOutcome(t *testing.T) {
	t.Parallel()
	oversize := `{"results":[],"pad":"` + strings.Repeat("x", probeMaxResponseBytes) + `"}`
	cases := map[string]fakeResponse{
		"200 with no results field":   {http.StatusOK, `{"hello":"world"}`},
		"200 with results not a list": {http.StatusOK, `{"results":"none"}`},
		"200 that is not JSON":        {http.StatusOK, `<html>proxy landing page</html>`},
		"200 past the probe's cap":    {http.StatusOK, oversize},
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newProviderFake(t, response)

			got := ProbeWebSearch(t.Context(), fake.server.Client().Transport, searchProviderTavily, "probe-key", fake.server.URL)

			if got.Outcome != WebSearchProbeNotResults || got.Status != http.StatusOK || got.Err == nil {
				t.Fatalf("got %+v, want not_results with HTTP 200 and a cause", got)
			}
		})
	}
}

func TestProbeWebSearch_ReadsAtMostTheProbeCap(t *testing.T) {
	t.Parallel()
	oversize := `{"results":[],"pad":"` + strings.Repeat("x", probeMaxResponseBytes) + `"}`
	fake := newProviderFake(t, fakeResponse{http.StatusOK, oversize})

	got := ProbeWebSearch(t.Context(), fake.server.Client().Transport, searchProviderExa, "k", fake.server.URL)

	if got.Err == nil || !strings.Contains(got.Err.Error(), "256 KiB") {
		t.Fatalf("Err = %v, want it to name the 256 KiB cap", got.Err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
