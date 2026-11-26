// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	DefaultNumResults = 5
	defaultNumResults = DefaultNumResults

	searchRequestTimeout = 30 * time.Second
)

func MaxCallDuration() time.Duration {
	return maxQuotaTotalWait + time.Duration(len(searchQuotaRetryBackoff)+1)*searchRequestTimeout
}

const ToolName = "web_search"

const GoogleWebSearchToolName = "web_search_google"

const (
	CfgToolName        = "tool_name"
	CfgToolDescription = "tool_description"
)

var ConnectorConfigKeys = []string{
	CfgToolName, CfgToolDescription, cfgSearchProvider, cfgSearchAPIKey, cfgSearchAPIBase, cfgSafeSearch, cfgMaxResults,
}

const CitationGuidance = "Always cite text sources using the citation format provided in your instructions. IMPORTANT: Image results will be displayed automatically in the UI - do NOT cite images, do NOT mention image URLs, and do NOT use citation markers like [[cite:imageX]] for images in your response text."

const defaultDescription = "Search the web. Use this when you need up-to-date information from the web."

const SearchTypeImage = searchTypeImage

const GoogleMaxResults = googleMaxResults

var unresolvedSearchCapabilities = SearchCapabilities{Image: true, Recency: true, Domains: true, MaxResults: tavilyMaxResults}

type Hooks struct {
	Searching func(query string)

	Turn func() int

	Logger *slog.Logger

	QuotaWait func(wait time.Duration)
}

func (h Hooks) dispatch() searchDispatch {
	return searchDispatch{logger: h.Logger, onQuotaWait: h.QuotaWait}
}

type Outcome struct {
	Result *tool.Result

	Citations *a2a.RAGInfoUpdateData
}

func Name(cfg map[string]any) string {
	if name := strings.TrimSpace(configString(cfg, CfgToolName)); name != "" {
		return name
	}
	return ToolName
}

func Description(cfg map[string]any) string {
	lead := strings.TrimSpace(configString(cfg, CfgToolDescription))
	if lead == "" {
		lead = defaultDescription
	}
	return lead + " " + CitationGuidance
}

func Parameters(cfg map[string]any) *tool.JSONSchema {
	caps := unresolvedSearchCapabilities
	if provider, err := resolveSearchProvider(cfg, webSearchProviderNames); err == nil {
		caps = provider.Capabilities()
	}
	return webSearchParameters(caps, defaultResults(cfg, caps))
}

func Search(ctx context.Context, cfg, args map[string]any, hooks Hooks) Outcome {
	query, _ := args["query"].(string)
	if query == "" {
		return Outcome{Result: tool.ValidationErr("query is required")}
	}

	provider, err := resolveSearchProviderWith(cfg, webSearchProviderNames, hooks.dispatch())
	if err != nil {
		return Outcome{Result: tool.Error(err.Error())}
	}

	opts, err := webSearchOptions(args, provider)
	if err != nil {
		return Outcome{Result: tool.ValidationErr(err.Error())}
	}
	if opts.SafeSearch, err = configSafeSearch(cfg); err != nil {
		return Outcome{Result: SafeSearchConfigError(Name(cfg), err)}
	}

	numResults := defaultResults(cfg, provider.Capabilities())
	if v, ok := args["max_results"].(float64); ok {
		numResults = int(v)
	}

	searching(hooks, query)
	results, err := provider.Search(ctx, query, numResults, opts)
	if err != nil {
		return failed(err)
	}
	return render(query, results, hooks.turn())
}

func SearchGoogle(ctx context.Context, cfg, args map[string]any, hooks Hooks) Outcome {
	query, _ := args["query"].(string)
	if query == "" {
		return Outcome{Result: tool.ValidationErr("query is required")}
	}

	numResults := defaultNumResults
	if v, ok := args["max_results"].(float64); ok {
		numResults = int(v)
	}

	var params googleSearchParams
	params.searchType, _ = args["search_type"].(string)
	params.dateRestrict, _ = args["date_restrict"].(string)
	params.safe, _ = args["safe_search"].(string)

	if err := providerBesideGoogleKey(cfg); err != nil {
		return Outcome{Result: tool.Error(err.Error())}
	}
	apiKey, _ := cfg[cfgGoogleSearchAPIKey].(string)
	cx, _ := cfg[cfgGoogleCSEID].(string)
	apiBase, _ := cfg[cfgSearchAPIBase].(string)
	if apiKey == "" || cx == "" {
		return Outcome{Result: tool.Error("Google Search API key and CSE ID must be configured via tool_config (google_search_api_key, google_cse_id)")}
	}
	if apiBase == "" {
		return Outcome{Result: tool.Error("Google Search 'api_base' must be configured in tool_config — no public-endpoint fallback")}
	}

	searching(hooks, query)
	results, err := NewGoogleSearchProvider(apiKey, cx, apiBase).search(ctx, query, numResults, params)
	if err != nil {
		return failed(err)
	}
	return render(query, results, hooks.turn())
}

func searching(hooks Hooks, query string) {
	if hooks.Searching != nil {
		hooks.Searching(query)
	}
}

func failed(err error) Outcome {
	return Outcome{Result: tool.Error(fmt.Sprintf("Web search failed: %v", err), ErrorCode(err))}
}

func defaultResults(cfg map[string]any, caps SearchCapabilities) int {
	if n := configMaxResults(cfg); n > 0 {
		return min(n, caps.MaxResults)
	}
	return defaultNumResults
}

func webSearchParameters(caps SearchCapabilities, defaultCount int) *tool.JSONSchema {
	properties := map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "The search query",
		},
		"max_results": map[string]any{
			"type":        "integer",
			"description": fmt.Sprintf("Maximum number of results (1-%d)", caps.MaxResults),
			"minimum":     1,
			"maximum":     caps.MaxResults,
			"default":     defaultCount,
		},
	}
	if caps.Recency {
		properties["recency"] = map[string]any{
			"type":        "string",
			"enum":        recencyValues,
			"description": "Only return results published within this window",
		}
	}
	if caps.Image {
		properties["search_type"] = map[string]any{
			"type":        "string",
			"enum":        []string{searchTypeImage},
			"description": "Set to 'image' for image search",
		}
	}
	if caps.Domains {
		properties["include_domains"] = map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Only return results from these domains (e.g. 'example.com')",
		}
		properties["exclude_domains"] = map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Never return results from these domains",
		}
	}
	return &tool.JSONSchema{Type: "object", Properties: properties, Required: []string{"query"}}
}

func render(query string, results []SearchResult, turn int) Outcome {
	returned := len(results)
	results = citableResults(results)
	organic, images, citations := citationsFor(query, results, turn)
	for len(results) > 0 && encodedSize(citations) > maxCitationBytes {
		results = results[:len(results)-1]
		organic, images, citations = citationsFor(query, results, turn)
	}

	text := formatCombinedResultsText(organic, images, turn, query)
	omitted := returned - len(results)
	switch {
	case omitted == 0:
	case len(results) == 0:
		text = allOmittedText(query, omitted)
	default:
		text += someOmittedNote(omitted)
	}
	data := map[string]any{
		"formatted_results": text,
		"num_results":       len(organic) + len(images),
		"search_turn":       turn,
	}
	if omitted > 0 {
		data["num_omitted"] = omitted
	}
	return Outcome{
		Result:    tool.OK("Web search completed", tool.WithData(data)),
		Citations: citations,
	}
}

func allOmittedText(query string, omitted int) string {
	if omitted == 1 {
		return fmt.Sprintf("The search for %q returned 1 result, but the chat cannot show it. Try a different query.", query)
	}
	return fmt.Sprintf("The search for %q returned %d results, but the chat cannot show any of them. Try a different query.", query, omitted)
}

func someOmittedNote(omitted int) string {
	if omitted == 1 {
		return "\n\n1 more result was left out because the chat cannot show it."
	}
	return fmt.Sprintf("\n\n%d more results were left out because the chat cannot show them.", omitted)
}

func citationsFor(query string, results []SearchResult, turn int) (organic, images []SearchResult, citations *a2a.RAGInfoUpdateData) {
	for _, r := range results {
		if r.Type == searchTypeImage {
			images = append(images, r)
		} else {
			organic = append(organic, r)
		}
	}
	sources := slices.Concat(buildRAGSources(organic, turn), buildImageRAGSources(images, turn))
	return organic, images, &a2a.RAGInfoUpdateData{
		Type:       string(a2a.SignalRAGInfoUpdate),
		Query:      query,
		SearchType: ToolName,
		SearchTurn: turn,
		Sources:    sources,
		IsComplete: true,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
}

func encodedSize(v any) int {
	encoded, err := json.Marshal(v)
	if err != nil {
		return math.MaxInt
	}
	return len(encoded)
}

func (h Hooks) turn() int {
	if h.Turn == nil {
		return 0
	}
	return h.Turn()
}

func webSearchOptions(args map[string]any, provider SearchProvider) (SearchOptions, error) {
	if _, ok := args["date_restrict"]; ok {
		return SearchOptions{}, errors.New("date_restrict is not a web search option; use recency (day, week, month or year)")
	}
	if _, ok := args["safe_search"]; ok {
		return SearchOptions{}, errors.New("safe_search is set by the operator in tool_config, not per search")
	}

	var opts SearchOptions
	switch searchType, _ := args["search_type"].(string); searchType {
	case "":
	case searchTypeImage:
		opts.Image = true
	default:
		return SearchOptions{}, fmt.Errorf("search_type must be %q or omitted, got %q", searchTypeImage, searchType)
	}

	recency, _ := args["recency"].(string)
	if recency != "" && !slices.Contains(recencyValues, recency) {
		return SearchOptions{}, fmt.Errorf("recency must be one of %s, got %q", strings.Join(recencyValues, ", "), recency)
	}
	opts.Recency = recency

	var err error
	if opts.IncludeDomains, err = domainList(args, "include_domains"); err != nil {
		return SearchOptions{}, err
	}
	if opts.ExcludeDomains, err = domainList(args, "exclude_domains"); err != nil {
		return SearchOptions{}, err
	}

	if option := unsupportedSearchOption(provider.Capabilities(), opts); option != "" {
		return SearchOptions{}, fmt.Errorf("%s is not supported by the configured web search provider (%s); search without it", option, provider.Name())
	}
	return opts, nil
}

func domainList(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of domain names", key)
	}
	domains := make([]string, 0, len(items))
	for _, item := range items {
		domain, ok := item.(string)
		if !ok || strings.TrimSpace(domain) == "" {
			return nil, fmt.Errorf("%s must be an array of domain names", key)
		}
		domains = append(domains, strings.TrimSpace(domain))
	}
	return domains, nil
}

func ErrorCode(err error) tool.ResultOption {
	if statusErr, ok := errors.AsType[*searchStatusError](err); ok {
		return tool.WithErrorCode(statusErr.errorCode())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassTimeout))
	}
	return tool.WithErrorCode("")
}

func ResolveProvider(cfg map[string]any) (SearchProvider, error) {
	return resolveSearchProvider(cfg, deepResearchProviderNames)
}

func NamesProvider(cfg map[string]any) bool {
	return configuredSearchProvider(cfg) != ""
}

func GoogleConfig(cfg map[string]any) (apiKey, cx, apiBase string, err error) {
	apiKey, _ = cfg[cfgGoogleSearchAPIKey].(string)
	cx, _ = cfg[cfgGoogleCSEID].(string)
	apiBase, err = googleAPIBase(cfg)
	return apiKey, cx, apiBase, err
}

var providerConfigKeys = map[string]bool{
	cfgSearchProvider: true, cfgSearchAPIKey: true, cfgSearchAPIBase: true,
	cfgGoogleSearchAPIKey: true, cfgGoogleCSEID: true, cfgGoogleAPIBase: true, cfgSafeSearch: true,
}

func IsProviderConfigKey(key string) bool { return providerConfigKeys[key] }

func SafeSearch(cfg map[string]any) (bool, error) { return configSafeSearch(cfg) }

func configString(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

func configInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}
