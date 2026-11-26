// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	searchProviderTavily = "tavily"
	searchProviderExa    = "exa"
	searchProviderGoogle = "google"
)

var webSearchProviderNames = []string{searchProviderTavily, searchProviderExa}

var deepResearchProviderNames = []string{searchProviderTavily, searchProviderExa, searchProviderGoogle}

const (
	cfgSearchProvider     = "provider"
	cfgSearchAPIKey       = "api_key"
	cfgSearchAPIBase      = "api_base"
	cfgGoogleSearchAPIKey = "google_search_api_key"
	cfgGoogleCSEID        = "google_cse_id"
	cfgGoogleAPIBase      = "google_api_base"
	cfgSafeSearch         = "safe_search"
	cfgMaxResults         = "max_results"
)

const (
	recencyDay   = "day"
	recencyWeek  = "week"
	recencyMonth = "month"
	recencyYear  = "year"
)

var recencyValues = []string{recencyDay, recencyWeek, recencyMonth, recencyYear}

const searchTypeImage = "image"

type SearchResult struct {
	Title       string
	Link        string
	Snippet     string
	Attribution string
	ImageURL    string
	Type        string
}

type SearchOptions struct {
	Recency        string
	Image          bool
	IncludeDomains []string
	ExcludeDomains []string
	SafeSearch     bool
}

type SearchCapabilities struct {
	Image      bool
	Recency    bool
	Domains    bool
	MaxResults int
}

type SearchProvider interface {
	Name() string
	Capabilities() SearchCapabilities
	Search(ctx context.Context, query string, numResults int, opts SearchOptions) ([]SearchResult, error)
}

func unsupportedSearchOption(caps SearchCapabilities, opts SearchOptions) string {
	switch {
	case opts.Image && !caps.Image:
		return "search_type"
	case opts.Recency != "" && !caps.Recency:
		return "recency"
	case len(opts.IncludeDomains) > 0 && !caps.Domains:
		return "include_domains"
	case len(opts.ExcludeDomains) > 0 && !caps.Domains:
		return "exclude_domains"
	}
	return ""
}

func clampResults(n, maxResults int) int {
	if n <= 0 {
		n = defaultNumResults
	}
	return min(n, maxResults)
}

func resolveSearchProvider(cfg map[string]any, accepted []string) (SearchProvider, error) {
	return resolveSearchProviderWith(cfg, accepted, searchDispatch{})
}

func resolveSearchProviderWith(cfg map[string]any, accepted []string, dispatch searchDispatch) (SearchProvider, error) {
	name := configuredSearchProvider(cfg)
	switch {
	case name == "":
		return nil, fmt.Errorf("web search has no provider configured: add a Web Search connector to this agent, "+
			"or set tool_config %s (%s) with %s and %s",
			cfgSearchProvider, searchProviderTavily+" or "+searchProviderExa, cfgSearchAPIKey, cfgSearchAPIBase)
	case !slices.Contains(accepted, name):
		return nil, unsupportedSearchProviderError(name, accepted)
	case name == searchProviderGoogle:
		return resolveGoogleSearchProvider(cfg)
	}
	return resolveConnectorSearchProvider(cfg, name, dispatch)
}

func unsupportedSearchProviderError(name string, accepted []string) error {
	if name == searchProviderGoogle {
		return fmt.Errorf("web search provider google is not supported by this tool: use the %s tool for Google Custom Search, "+
			"or set the Web Search connector's provider (tool_config %s) to one of: %s",
			GoogleWebSearchToolName, cfgSearchProvider, strings.Join(accepted, ", "))
	}
	return fmt.Errorf("web search provider %q is not supported; set the Web Search connector's provider (tool_config %s) to one of: %s",
		name, cfgSearchProvider, strings.Join(accepted, ", "))
}

func resolveConnectorSearchProvider(cfg map[string]any, name string, dispatch searchDispatch) (SearchProvider, error) {
	if err := providerBesideGoogleKey(cfg); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(configString(cfg, cfgSearchAPIKey))
	if key == "" {
		return nil, fmt.Errorf("web search provider %s has no API key: set it on the Web Search connector (tool_config %s)", name, cfgSearchAPIKey)
	}
	base, err := searchAPIBase(cfg, name)
	if err != nil {
		return nil, err
	}
	if name == searchProviderTavily {
		provider := newTavilySearchProvider(key, base)
		provider.dispatch = dispatch
		return provider, nil
	}
	provider := newExaSearchProvider(key, base)
	provider.dispatch = dispatch
	return provider, nil
}

func resolveGoogleSearchProvider(cfg map[string]any) (SearchProvider, error) {
	key := strings.TrimSpace(configString(cfg, cfgGoogleSearchAPIKey))
	cx := strings.TrimSpace(configString(cfg, cfgGoogleCSEID))
	if key == "" || cx == "" {
		return nil, fmt.Errorf("web search provider google needs tool_config %s and %s", cfgGoogleSearchAPIKey, cfgGoogleCSEID)
	}
	base, err := searchAPIBase(cfg, searchProviderGoogle)
	if err != nil {
		return nil, err
	}
	return NewGoogleSearchProvider(key, cx, base), nil
}

func configuredSearchProvider(cfg map[string]any) string {
	return strings.ToLower(strings.TrimSpace(configString(cfg, cfgSearchProvider)))
}

func providerBesideGoogleKey(cfg map[string]any) error {
	name := configuredSearchProvider(cfg)
	if name == "" || name == searchProviderGoogle || strings.TrimSpace(configString(cfg, cfgGoogleSearchAPIKey)) == "" {
		return nil
	}
	return fmt.Errorf("web search tool_config sets provider %s and also %s, and both use %s, "+
		"so one provider's key would be sent to the other's endpoint: remove %s to search with %s, "+
		"or remove %s to search with Google Custom Search; to use both, give each tool its own entry and tool_config "+
		"instead of one shared group config",
		name, cfgGoogleSearchAPIKey, cfgSearchAPIBase, cfgGoogleSearchAPIKey, name, cfgSearchProvider)
}

func googleAPIBase(cfg map[string]any) (string, error) {
	for _, key := range []string{cfgSearchAPIBase, cfgGoogleAPIBase} {
		base, err := configBaseURL(cfg, key)
		if err != nil || base != "" {
			return base, err
		}
	}
	return "", nil
}

func configBaseURL(cfg map[string]any, key string) (string, error) {
	switch v := cfg[key].(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(v), nil
	default:
		return "", fmt.Errorf("web search %s must be a URL string, got %T", key, v)
	}
}

func searchAPIBase(cfg map[string]any, provider string) (string, error) {
	var base string
	var err error
	if provider == searchProviderGoogle {
		base, err = googleAPIBase(cfg)
	} else {
		base, err = configBaseURL(cfg, cfgSearchAPIBase)
	}
	if err != nil {
		return "", err
	}
	if base == "" {
		if provider == searchProviderGoogle {
			return "", fmt.Errorf("web search provider google has no API base: set tool_config %s (or %s); there is no public-endpoint fallback",
				cfgSearchAPIBase, cfgGoogleAPIBase)
		}
		return "", fmt.Errorf("web search provider %s has no API base: set it on the Web Search connector (tool_config %s); there is no public-endpoint fallback",
			provider, cfgSearchAPIBase)
	}
	return strings.TrimRight(base, "/"), nil
}

var googleSafeSearchWords = map[string]bool{"off": false, "active": true, "medium": true, "high": true}

func configSafeSearch(cfg map[string]any) (bool, error) {
	switch v := cfg[cfgSafeSearch].(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case int:
		return safeSearchFromNumber(float64(v))
	case int64:
		return safeSearchFromNumber(float64(v))
	case float64:
		return safeSearchFromNumber(v)
	case string:
		return safeSearchFromString(v)
	default:
		return false, fmt.Errorf("%s must be true or false, got %T", cfgSafeSearch, v)
	}
}

func SafeSearchConfigError(toolName string, err error) *tool.Result {
	return tool.Error(fmt.Sprintf("%s tool_config is invalid: %v", toolName, err))
}

func safeSearchFromNumber(n float64) (bool, error) {
	if n != 0 && n != 1 {
		return false, fmt.Errorf("%s must be true or false, got %v", cfgSafeSearch, n)
	}
	return n == 1, nil
}

func safeSearchFromString(s string) (bool, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false, fmt.Errorf("%s is set but empty; remove it or set true or false", cfgSafeSearch)
	}
	if on, err := strconv.ParseBool(trimmed); err == nil {
		return on, nil
	}
	if on, known := googleSafeSearchWords[strings.ToLower(trimmed)]; known {
		return on, nil
	}
	return false, fmt.Errorf("%s must be true or false, got %q", cfgSafeSearch, trimmed)
}

func configMaxResults(cfg map[string]any) int {
	if s, ok := cfg[cfgMaxResults].(string); ok {
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		return max(n, 0)
	}
	return max(configInt(cfg, cfgMaxResults), 0)
}
