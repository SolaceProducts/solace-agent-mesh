// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"context"
	"net/http"
)

const tavilyMaxResults = 20

const (
	tavilyStatusPlanLimit    = 432
	tavilyStatusPayGoLimit   = 433
	tavilyStatusFlexCapacity = 434
)

const (
	tavilySearchPath          = "/search"
	tavilySearchDepthBasic    = "basic"
	tavilyTopicGeneral        = "general"
	tavilyAuthorizationScheme = "Bearer "
)

type tavilySearchProvider struct {
	apiKey   string
	baseURL  string
	dispatch searchDispatch
}

func newTavilySearchProvider(apiKey, baseURL string) *tavilySearchProvider {
	return &tavilySearchProvider{apiKey: apiKey, baseURL: baseURL}
}

func (p *tavilySearchProvider) Name() string { return searchProviderTavily }

func (p *tavilySearchProvider) Capabilities() SearchCapabilities {
	return SearchCapabilities{Image: true, Recency: true, Domains: true, MaxResults: tavilyMaxResults}
}

type tavilySearchRequest struct {
	Query                    string   `json:"query"`
	MaxResults               int      `json:"max_results"`
	SearchDepth              string   `json:"search_depth"`
	Topic                    string   `json:"topic"`
	TimeRange                string   `json:"time_range,omitempty"`
	IncludeDomains           []string `json:"include_domains,omitempty"`
	ExcludeDomains           []string `json:"exclude_domains,omitempty"`
	IncludeImages            bool     `json:"include_images,omitempty"`
	IncludeImageDescriptions bool     `json:"include_image_descriptions,omitempty"`
	SafeSearch               bool     `json:"safe_search,omitempty"`
}

func (p *tavilySearchProvider) Search(ctx context.Context, query string, numResults int, opts SearchOptions) ([]SearchResult, error) {
	header := http.Header{}
	header.Set("Authorization", tavilyAuthorizationScheme+p.apiKey)
	call, err := postJSONSearchCall(searchProviderTavily, p.baseURL+tavilySearchPath, header, tavilySearchRequest{
		Query:                    query,
		MaxResults:               clampResults(numResults, tavilyMaxResults),
		SearchDepth:              tavilySearchDepthBasic,
		Topic:                    tavilyTopicGeneral,
		TimeRange:                opts.Recency,
		IncludeDomains:           opts.IncludeDomains,
		ExcludeDomains:           opts.ExcludeDomains,
		IncludeImages:            opts.Image,
		IncludeImageDescriptions: opts.Image,
		SafeSearch:               opts.SafeSearch,
	})
	if err != nil {
		return nil, err
	}
	call.errorMessage = tavilyErrorMessage
	call.quotaStatuses = []int{tavilyStatusPlanLimit, tavilyStatusPayGoLimit, tavilyStatusFlexCapacity}
	call.dispatch = p.dispatch
	data, err := call.do(ctx)
	if err != nil {
		return nil, err
	}

	items, err := searchResultItems(searchProviderTavily, data)
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
		content, _ := m["content"].(string)
		results = append(results, SearchResult{
			Title:       title,
			Link:        link,
			Snippet:     content,
			Attribution: extractDomain(link),
			Type:        "web",
		})
	}
	if opts.Image {
		results = append(results, tavilyImageResults(data)...)
	}
	return results, nil
}

func tavilyImageResults(data map[string]any) []SearchResult {
	images, _ := data["images"].([]any)
	results := make([]SearchResult, 0, len(images))
	for _, raw := range images {
		var imageURL, description string
		switch img := raw.(type) {
		case string:
			imageURL = img
		case map[string]any:
			imageURL, _ = img["url"].(string)
			description, _ = img["description"].(string)
		}
		if imageURL == "" {
			continue
		}
		results = append(results, SearchResult{Title: description, Link: imageURL, ImageURL: imageURL, Type: searchTypeImage})
	}
	return results
}

func tavilyErrorMessage(data map[string]any) string {
	switch detail := data["detail"].(type) {
	case string:
		if detail != "" {
			return detail
		}
	case map[string]any:
		if msg, _ := detail["error"].(string); msg != "" {
			return msg
		}
	}
	return unknownSearchError
}
