// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const exaMaxResults = 20

const exaStatusOutOfCredits = http.StatusPaymentRequired

const (
	exaSearchPath       = "/search"
	exaSearchTypeAuto   = "auto"
	exaSnippetMaxBytes  = 500
	exaHighlightJoinSep = " … "
)

var exaRecencyWindow = map[string]func(time.Time) time.Time{
	recencyDay:   func(t time.Time) time.Time { return t.AddDate(0, 0, -1) },
	recencyWeek:  func(t time.Time) time.Time { return t.AddDate(0, 0, -7) },
	recencyMonth: func(t time.Time) time.Time { return t.AddDate(0, -1, 0) },
	recencyYear:  func(t time.Time) time.Time { return t.AddDate(-1, 0, 0) },
}

type exaSearchProvider struct {
	apiKey   string
	baseURL  string
	now      func() time.Time
	dispatch searchDispatch
}

func newExaSearchProvider(apiKey, baseURL string) *exaSearchProvider {
	return &exaSearchProvider{apiKey: apiKey, baseURL: baseURL, now: time.Now}
}

func (p *exaSearchProvider) Name() string { return searchProviderExa }

func (p *exaSearchProvider) Capabilities() SearchCapabilities {
	return SearchCapabilities{Recency: true, Domains: true, MaxResults: exaMaxResults}
}

type exaContents struct {
	Highlights bool `json:"highlights"`
}

type exaSearchRequest struct {
	Query              string      `json:"query"`
	Type               string      `json:"type"`
	NumResults         int         `json:"numResults"`
	IncludeDomains     []string    `json:"includeDomains,omitempty"`
	ExcludeDomains     []string    `json:"excludeDomains,omitempty"`
	StartPublishedDate string      `json:"startPublishedDate,omitempty"`
	Moderation         bool        `json:"moderation,omitempty"`
	Contents           exaContents `json:"contents"`
}

func (p *exaSearchProvider) Search(ctx context.Context, query string, numResults int, opts SearchOptions) ([]SearchResult, error) {
	req := exaSearchRequest{
		Query:          query,
		Type:           exaSearchTypeAuto,
		NumResults:     clampResults(numResults, exaMaxResults),
		IncludeDomains: opts.IncludeDomains,
		ExcludeDomains: opts.ExcludeDomains,
		Moderation:     opts.SafeSearch,
		Contents:       exaContents{Highlights: true},
	}
	if since, ok := exaRecencyWindow[opts.Recency]; ok {
		req.StartPublishedDate = since(p.now().UTC()).Format(time.RFC3339)
	}
	header := http.Header{}
	header.Set("x-api-key", p.apiKey)
	call, err := postJSONSearchCall(searchProviderExa, p.baseURL+exaSearchPath, header, req)
	if err != nil {
		return nil, err
	}
	call.errorMessage = exaErrorMessage
	call.quotaStatuses = []int{exaStatusOutOfCredits}
	call.dispatch = p.dispatch
	data, err := call.do(ctx)
	if err != nil {
		return nil, err
	}

	items, err := searchResultItems(searchProviderExa, data)
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		link, _ := m["url"].(string)
		title, _ := m["title"].(string)
		image, _ := m["image"].(string)
		results = append(results, SearchResult{
			Title:       title,
			Link:        link,
			Snippet:     exaSnippet(m),
			Attribution: extractDomain(link),
			ImageURL:    image,
			Type:        "web",
		})
	}
	return results, nil
}

func exaSnippet(m map[string]any) string {
	if raw, ok := m["highlights"].([]any); ok {
		var parts []string
		for _, h := range raw {
			if s, _ := h.(string); strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, exaHighlightJoinSep)
		}
	}
	if summary, _ := m["summary"].(string); strings.TrimSpace(summary) != "" {
		return strings.TrimSpace(summary)
	}
	text, _ := m["text"].(string)
	return strutil.Truncate(strings.TrimSpace(text), exaSnippetMaxBytes, "…")
}

func exaErrorMessage(data map[string]any) string {
	for _, key := range []string{"error", "message"} {
		if msg, _ := data[key].(string); msg != "" {
			return msg
		}
	}
	return unknownSearchError
}
