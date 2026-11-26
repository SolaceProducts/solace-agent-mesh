// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func web(link string) SearchResult {
	return SearchResult{Title: "T", Link: link, Snippet: "s", Type: "web"}
}

func TestCitableResultsRefusesEachKindOfLink(t *testing.T) {
	t.Parallel()
	at2048 := "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/"))
	for name, tc := range map[string]struct {
		result SearchResult
		kept   bool
	}{
		"https":                {web("https://example.com/a"), true},
		"http":                 {web("http://example.com/a"), true},
		"no link":              {web(""), true},
		"2048 bytes":           {web(at2048), true},
		"2049 bytes":           {web(at2048 + "a"), false},
		"ftp":                  {web("ftp://example.com/x"), false},
		"file":                 {web("file://host/x"), false},
		"https without a host": {web("https:///x"), false},
		"relative":             {web("/x"), false},
		"user and password":    {web("https://u:p@example.com/"), false},
		"bad image url":        {SearchResult{Link: "https://example.com/", ImageURL: "data:image/png;base64,AA", Type: "image"}, false},
		"good image url":       {SearchResult{Link: "https://example.com/", ImageURL: "https://img.example/a.png", Type: "image"}, true},
		"web result with an unused bad image field": {SearchResult{Link: "https://example.com/", ImageURL: "data:x", Type: "web"}, true},
	} {
		if kept := len(citableResults([]SearchResult{tc.result})) == 1; kept != tc.kept {
			t.Errorf("%s: kept = %v, want %v", name, kept, tc.kept)
		}
	}
}

func TestCitableResultsKeepsAtMostFifty(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want int }{{50, 50}, {51, 50}} {
		results := make([]SearchResult, tc.in)
		for i := range results {
			results[i] = web("https://example.com/")
		}
		if got := len(citableResults(results)); got != tc.want {
			t.Errorf("%d results: kept %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestCitationTextBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		bound func(string) string
		limit int
	}{
		{"title", citationTitle, 500},
		{"preview", citationPreview, 2000},
	} {
		at := strings.Repeat("a", tc.limit)
		if got := tc.bound(at); got != at {
			t.Errorf("%s of %d bytes cut to %d, want it kept", tc.name, tc.limit, len(got))
		}
		if got := tc.bound(at + "a"); len(got) != tc.limit {
			t.Errorf("%s of %d bytes cut to %d, want %d", tc.name, tc.limit+1, len(got), tc.limit)
		}
		got := tc.bound(strings.Repeat("€", tc.limit))
		if want := tc.limit - tc.limit%3; len(got) != want || !utf8.ValidString(got) {
			t.Errorf("%s of 3-byte runes cut to %d bytes, valid UTF-8 %v; want %d and valid", tc.name, len(got), utf8.ValidString(got), want)
		}
	}
}

func TestEveryCitationTextFieldIsBounded(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 3000)
	page := buildRAGSources([]SearchResult{{Title: long, Attribution: long, Snippet: long, Link: "https://example.com/", Type: "web"}}, 0)[0]
	image := buildImageRAGSources([]SearchResult{{Title: long, Link: "https://example.com/", ImageURL: "https://img.example/a.png", Type: "image"}}, 0)[0]
	pageTitle, _ := page.Metadata["title"].(string)
	imageTitle, _ := image.Metadata["title"].(string)
	for field, got := range map[string]string{
		"page title": page.Title, "page metadata title": pageTitle, "page file name": page.Filename,
		"image title": image.Title, "image metadata title": imageTitle, "image file name": image.Filename,
		"image preview": image.ContentPreview,
	} {
		if len(got) != 500 {
			t.Errorf("%s is %d bytes, want 500", field, len(got))
		}
	}
	if len(page.ContentPreview) != 2000 {
		t.Errorf("page preview is %d bytes, want 2000", len(page.ContentPreview))
	}
}

func TestRenderKeepsCitationsWithinTheSizeLimit(t *testing.T) {
	t.Parallel()
	results := make([]SearchResult, 50)
	for i := range results {
		results[i] = SearchResult{
			Title: strings.Repeat("t", 600), Snippet: strings.Repeat("s", 3000), Type: "web",
			Link: "https://example.com/" + strings.Repeat("a", 2000),
		}
	}

	out := render("q", results, 0)

	kept := len(out.Citations.Sources)
	if size := encodedSize(out.Citations); size > 240<<10 {
		t.Fatalf("citations are %d bytes, want at most 240 KiB", size)
	}
	if _, _, more := citationsFor("q", results[:kept+1], 0); encodedSize(more) <= 240<<10 {
		t.Fatalf("kept %d results, but %d fit under 240 KiB", kept, kept+1)
	}
	if out.Result.Data["num_results"] != kept || out.Result.Data["num_omitted"] != 50-kept {
		t.Fatalf("data = num_results %v, num_omitted %v; want %d and %d", out.Result.Data["num_results"], out.Result.Data["num_omitted"], kept, 50-kept)
	}
}

func TestRenderTellsTheModelAboutOmittedResults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		results     []SearchResult
		wantOmitted any
		wantText    string
	}{
		{"one of two", []SearchResult{web("https://example.com/a"), web("ftp://example.com/b")}, 1, "\n\n1 more result was left out because the chat cannot show it."},
		{"two of three", []SearchResult{web("https://example.com/a"), web("ftp://example.com/b"), web("ftp://example.com/c")}, 2, "\n\n2 more results were left out because the chat cannot show them."},
		{"the only one", []SearchResult{web("ftp://example.com/b")}, 1, `The search for "q" returned 1 result, but the chat cannot show it. Try a different query.`},
		{"all two", []SearchResult{web("ftp://example.com/b"), web("ftp://example.com/c")}, 2, `The search for "q" returned 2 results, but the chat cannot show any of them. Try a different query.`},
		{"none", []SearchResult{web("https://example.com/a")}, nil, ""},
	} {
		data := render("q", tc.results, 0).Result.Data
		text, _ := data["formatted_results"].(string)
		if data["num_omitted"] != tc.wantOmitted {
			t.Errorf("%s: num_omitted = %v, want %v", tc.name, data["num_omitted"], tc.wantOmitted)
		}
		if !strings.HasSuffix(text, tc.wantText) || (tc.wantOmitted != nil && strings.Contains(text, "No results found")) {
			t.Errorf("%s: formatted_results = %q, want it to end %q", tc.name, text, tc.wantText)
		}
		if tc.wantOmitted == nil && strings.Contains(text, "left out") {
			t.Errorf("%s: formatted_results = %q, want no note when nothing was left out", tc.name, text)
		}
	}
}

func TestEncodedSizeOfAnUnencodableValueExceedsTheCap(t *testing.T) {
	t.Parallel()
	if got := encodedSize(make(chan int)); got <= maxCitationBytes {
		t.Fatalf("encodedSize = %d, want more than the %d byte cap", got, maxCitationBytes)
	}
}

func TestSearchWithoutATurnHookNumbersTurnZero(t *testing.T) {
	t.Parallel()
	fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
	outcome := Search(t.Context(), map[string]any{"provider": "tavily", "api_key": "K", "api_base": fake.server.URL},
		map[string]any{"query": "q"}, Hooks{})
	if outcome.Citations == nil || outcome.Citations.SearchTurn != 0 {
		t.Fatalf("outcome = %+v, want turn 0", outcome)
	}
}

func TestSearchReportsQuotaWaitsToItsHooks(t *testing.T) {
	withFastQuotaRetryBackoff(t)
	fake := newProviderFake(t, fakeResponse{http.StatusTooManyRequests, `{}`}, fakeResponse{http.StatusOK, tavilyCannedResponse})
	var logs bytes.Buffer
	var waits []time.Duration

	outcome := Search(t.Context(), map[string]any{"provider": "tavily", "api_key": "K", "api_base": fake.server.URL},
		map[string]any{"query": "q"}, Hooks{
			Turn:      func() int { return 0 },
			Logger:    slog.New(slog.NewTextHandler(&logs, nil)),
			QuotaWait: func(d time.Duration) { waits = append(waits, d) },
		})

	if outcome.Result.Status != "success" {
		t.Fatalf("result = %+v, want success after the retry", outcome.Result)
	}
	if len(waits) != 1 || waits[0] <= 0 {
		t.Errorf("waits = %v, want one positive wait", waits)
	}
	if !strings.Contains(logs.String(), "web search quota exceeded, waiting to retry") {
		t.Errorf("logs = %q, want the wait on the caller's logger", logs.String())
	}
}
