// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const googleMaxResults = 10

var googleDateRestrict = map[string]string{
	recencyDay:   "d1",
	recencyWeek:  "w1",
	recencyMonth: "m1",
	recencyYear:  "y1",
}

type GoogleSearchProvider struct {
	apiKey  string
	cx      string
	baseURL string
}

func NewGoogleSearchProvider(apiKey, cx, baseURL string) *GoogleSearchProvider {
	return &GoogleSearchProvider{apiKey: apiKey, cx: cx, baseURL: baseURL}
}

func (g *GoogleSearchProvider) Name() string { return searchProviderGoogle }

func (g *GoogleSearchProvider) Capabilities() SearchCapabilities {
	return SearchCapabilities{Image: true, Recency: true, MaxResults: googleMaxResults}
}

var htmlTagRegex = regexp.MustCompile(`<[^>]+>`)

const googleSafeSearchActive = "active"

type googleSearchParams struct {
	searchType   string
	dateRestrict string
	safe         string
}

func (g *GoogleSearchProvider) Search(ctx context.Context, query string, numResults int, opts SearchOptions) ([]SearchResult, error) {
	params := googleSearchParams{dateRestrict: googleDateRestrict[opts.Recency]}
	if opts.Image {
		params.searchType = searchTypeImage
	}
	if opts.SafeSearch {
		params.safe = googleSafeSearchActive
	}
	return g.search(ctx, query, numResults, params)
}

func (g *GoogleSearchProvider) search(ctx context.Context, query string, numResults int, params googleSearchParams) ([]SearchResult, error) {
	u, err := url.Parse(g.baseURL + "/customsearch/v1")
	if err != nil {
		return nil, errors.New("google search api_base is not a valid URL")
	}
	q := u.Query()
	q.Set("key", g.apiKey)
	q.Set("cx", g.cx)
	q.Set("q", query)
	q.Set("num", fmt.Sprintf("%d", clampResults(numResults, googleMaxResults)))
	if params.searchType != "" {
		q.Set("searchType", params.searchType)
	}
	if params.dateRestrict != "" {
		q.Set("dateRestrict", params.dateRestrict)
	}
	if params.safe != "" {
		q.Set("safe", params.safe)
	}
	u.RawQuery = q.Encode()

	data, err := searchCall{
		provider:     searchProviderGoogle,
		method:       http.MethodGet,
		endpoint:     u.String(),
		errorMessage: googleErrorMessage,
		errorReasons: googleErrorReasons,
	}.do(ctx)
	if err != nil {
		return nil, err
	}

	items, _ := data["items"].([]any)
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if params.searchType == searchTypeImage {
			results = append(results, googleImageResult(m))
		} else {
			results = append(results, googleWebResult(m))
		}
	}
	return results, nil
}

func googleImageResult(m map[string]any) SearchResult {
	imageURL, _ := m["link"].(string)
	title, _ := m["title"].(string)
	contextLink := imageURL
	if imgMeta, ok := m["image"].(map[string]any); ok {
		if cl, ok := imgMeta["contextLink"].(string); ok && cl != "" {
			contextLink = cl
		}
	}
	return SearchResult{Title: title, Link: contextLink, ImageURL: imageURL, Type: searchTypeImage}
}

func googleWebResult(m map[string]any) SearchResult {
	link, _ := m["link"].(string)
	title, _ := m["title"].(string)

	snippet, _ := m["snippet"].(string)
	if snippet == "" {
		if htmlSnippet, ok := m["htmlSnippet"].(string); ok {
			snippet = cleanHTMLSnippet(htmlSnippet)
		}
	}

	var imageURL string
	if pm, ok := m["pagemap"].(map[string]any); ok {
		if thumbnails, ok := pm["cse_thumbnail"].([]any); ok && len(thumbnails) > 0 {
			if thumb, ok := thumbnails[0].(map[string]any); ok {
				imageURL, _ = thumb["src"].(string)
			}
		}
	}
	return SearchResult{
		Title:       title,
		Link:        link,
		Snippet:     snippet,
		Attribution: extractDomain(link),
		ImageURL:    imageURL,
		Type:        "web",
	}
}

var googleQuotaReasons = []string{"dailyLimitExceeded", "quotaExceeded", "rateLimitExceeded", "userRateLimitExceeded"}

func isGoogleQuotaReason(reason string) bool {
	return slices.Contains(googleQuotaReasons, reason)
}

func googleErrorReasons(data map[string]any) []string {
	errObj, _ := data["error"].(map[string]any)
	var reasons []string
	for _, listKey := range []string{"errors", "details"} {
		entries, _ := errObj[listKey].([]any)
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			if reason, _ := entry["reason"].(string); reason != "" {
				reasons = append(reasons, reason)
			}
		}
	}
	return reasons
}

func googleErrorMessage(data map[string]any) string {
	if errObj, ok := data["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && msg != "" {
			return msg
		}
	}
	return unknownSearchError
}

const unknownSearchError = "unknown error"

func cleanHTMLSnippet(s string) string {
	s = html.UnescapeString(s)
	return htmlTagRegex.ReplaceAllString(s, "")
}

func extractDomain(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return strings.TrimPrefix(parsed.Hostname(), "www.")
}
