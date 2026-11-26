// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const probeMaxResponseBytes = 256 << 10

const webSearchProbeQuery = "agent mesh"

type WebSearchProbeOutcome string

const (
	WebSearchProbeAccepted    WebSearchProbeOutcome = "accepted"
	WebSearchProbeKeyRejected WebSearchProbeOutcome = "key_rejected"
	WebSearchProbeRateLimited WebSearchProbeOutcome = "rate_limited"
	WebSearchProbeNotResults  WebSearchProbeOutcome = "not_results"
	WebSearchProbeFailed      WebSearchProbeOutcome = "failed"
)

type WebSearchProbeResult struct {
	Outcome WebSearchProbeOutcome
	Status  int
	Err     error
}

func ProbeWebSearch(ctx context.Context, transport http.RoundTripper, provider, apiKey, apiBase string) WebSearchProbeResult {
	if transport == nil {
		return WebSearchProbeResult{Outcome: WebSearchProbeFailed, Err: errors.New("web search probe needs the transport to send through")}
	}
	name := strings.ToLower(strings.TrimSpace(provider))
	if !slices.Contains(webSearchProviderNames, name) {
		return WebSearchProbeResult{Outcome: WebSearchProbeFailed, Err: fmt.Errorf(
			"web search provider %q cannot be checked; use one of: %s", name, strings.Join(webSearchProviderNames, ", "))}
	}
	resolved, err := resolveSearchProviderWith(map[string]any{
		cfgSearchProvider: name,
		cfgSearchAPIKey:   apiKey,
		cfgSearchAPIBase:  apiBase,
	}, webSearchProviderNames, searchDispatch{
		client:           &http.Client{Transport: transport, Timeout: searchRequestTimeout, CheckRedirect: checkSearchRedirect},
		noQuotaRetry:     true,
		maxResponseBytes: probeMaxResponseBytes,
	})
	if err != nil {
		return WebSearchProbeResult{Outcome: WebSearchProbeFailed, Err: err}
	}
	if _, err := resolved.Search(ctx, webSearchProbeQuery, 1, SearchOptions{}); err != nil {
		return classifyWebSearchProbe(err)
	}
	return WebSearchProbeResult{Outcome: WebSearchProbeAccepted, Status: http.StatusOK}
}

func classifyWebSearchProbe(err error) WebSearchProbeResult {
	if _, notResults := errors.AsType[*notSearchResultsError](err); notResults {
		return WebSearchProbeResult{Outcome: WebSearchProbeNotResults, Status: http.StatusOK, Err: err}
	}
	statusErr, answered := errors.AsType[*searchStatusError](err)
	if !answered {
		return WebSearchProbeResult{Outcome: WebSearchProbeFailed, Err: err}
	}
	if statusErr.status >= 200 && statusErr.status <= 299 {
		return WebSearchProbeResult{Outcome: WebSearchProbeNotResults, Status: statusErr.status, Err: err}
	}
	result := WebSearchProbeResult{Outcome: WebSearchProbeFailed, Status: statusErr.status, Err: err}
	switch statusErr.errorCode() {
	case tool.DispatchErrorCode(taxonomy.DispatchClassAuthRequired):
		result.Outcome = WebSearchProbeKeyRejected
	case taxonomy.ToolFailureRateLimited:
		result.Outcome = WebSearchProbeRateLimited
	}
	return result
}
